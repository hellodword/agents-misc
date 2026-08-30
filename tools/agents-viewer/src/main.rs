mod shutdown_io;

use std::io::{IsTerminal as _, Write as _};
use std::process::ExitCode;
use std::time::{Duration, Instant};

use agents_viewer::cli::Cli;
use agents_viewer::config::{Config, LogLevel};
use agents_viewer::index::Database;
use agents_viewer::index::coordinator::{IndexCoordinator, IndexUpdate, ReconcileReport};
use agents_viewer::index::writer::spawn_writer;
use agents_viewer::model::{
    IndexProgress, ServicePhase, SessionSyncState, SseEventPayload, SseEventType,
};
use agents_viewer::permissions::{acquire_cache_lock, prepare_cache_directory};
use agents_viewer::server::{self, AppState};
use agents_viewer::watch::start_watcher;
use anyhow::{Context as _, Result};
use clap::Parser as _;
use shutdown_io::ShutdownListener;
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

// Axum's graceful shutdown has no deadline and can wait forever on an incomplete request.
const HTTP_SHUTDOWN_GRACE: Duration = Duration::from_secs(1);

#[tokio::main]
async fn main() -> ExitCode {
    let cli = Cli::parse();
    let config = match Config::load(cli) {
        Ok(config) => config,
        Err(error) => {
            eprintln!("agents-viewer: {error:#}");
            return ExitCode::from(1);
        }
    };
    init_tracing(config.log_level);
    match run(config).await {
        Ok(()) => ExitCode::SUCCESS,
        Err(error) => {
            eprintln!("agents-viewer: {error:#}");
            ExitCode::from(1)
        }
    }
}

async fn run(config: Config) -> Result<()> {
    prepare_cache_directory(&config.cache.top)?;
    prepare_cache_directory(&config.cache.namespace)?;
    let _lock = acquire_cache_lock(&config.cache.lock)?;
    let shutdown = CancellationToken::new();
    let signal_shutdown = shutdown.clone();
    tokio::spawn(async move {
        if let Err(error) = wait_for_signal().await {
            eprintln!("agents-viewer: failed to wait for shutdown signal: {error:#}");
        }
        signal_shutdown.cancel();
    });
    let fingerprint = config.roots.home.to_string_lossy().into_owned();
    let now_micros = chrono::Utc::now().timestamp_micros();
    let opened =
        Database::open_or_recover_with_disposition(&config.cache.database, &fingerprint).await?;
    let database = opened.database;
    let policy = database
        .resolve_index_policy(-1, config.max_event_bytes, now_micros)
        .await?;
    let bootstrap_required = opened.bootstrap_required;

    let (watch_sender, watch_receiver) = mpsc::channel(1_024);
    let watcher = start_watcher(&config.roots, watch_sender)?;
    let listener = tokio::net::TcpListener::bind(config.listen)
        .await
        .with_context(|| format!("bind {}", config.listen))?;
    let bound = listener.local_addr().context("read bound address")?;
    let (writer, writer_task) = spawn_writer(database.clone());
    let coordinator = IndexCoordinator::new(
        database.clone(),
        writer.clone(),
        config.roots.clone(),
        config.max_event_bytes,
        policy,
    );
    let state = AppState::new_with_shutdown(
        database.clone(),
        config.roots.clone(),
        config.cache.clone(),
        policy,
        shutdown.clone(),
        bootstrap_required,
    )
    .with_coordinator(coordinator.handle());
    let (update_sender, update_receiver) = mpsc::channel(64);
    let coordinator_shutdown = shutdown.clone();
    let coordinator_task = tokio::spawn(async move {
        coordinator
            .run_with_updates(
                watch_receiver,
                coordinator_shutdown,
                Some(update_sender),
                bootstrap_required,
            )
            .await
    });
    let update_task = tokio::spawn(update_status(
        state.clone(),
        update_receiver,
        shutdown.clone(),
    ));
    let heartbeat_task = tokio::spawn(heartbeat(state.clone(), shutdown.clone()));
    let server_shutdown = shutdown.clone();
    let forced_server_shutdown = CancellationToken::new();
    let server = axum::serve(
        ShutdownListener::new(listener, forced_server_shutdown.clone()),
        server::router(state.clone(), bound, &config.password),
    )
    .with_graceful_shutdown(async move { server_shutdown.cancelled().await });
    let server_task = tokio::spawn(async move { server.await.context("HTTP server failed") });

    let url = format!("http://{bound}");
    println!("{url}");

    shutdown.cancelled().await;
    if std::io::stderr().is_terminal() {
        eprintln!();
    }
    eprintln!("agents-viewer: shutting down...");
    let cleanup = async {
        let background_cleanup = async {
            watcher.shutdown().await;
            coordinator_task
                .await
                .context("coordinator task panicked")??;
            writer.shutdown().await?;
            writer_task.wait().await?;
            let _ = update_task.await;
            let _ = heartbeat_task.await;
            Result::<()>::Ok(())
        };
        let (background_result, server_result) = tokio::join!(
            background_cleanup,
            drain_http_server(server_task, forced_server_shutdown)
        );
        background_result?;
        server_result?;
        database.close().await;
        Result::<()>::Ok(())
    };
    tokio::select! {
        result = tokio::time::timeout(Duration::from_secs(10), cleanup) => {
            result.context("graceful shutdown exceeded 10 seconds")??;
            Ok(())
        }
        second = wait_for_signal() => {
            second?;
            anyhow::bail!("second shutdown signal forced termination")
        }
    }
}

async fn drain_http_server(
    mut server_task: tokio::task::JoinHandle<Result<()>>,
    forced_shutdown: CancellationToken,
) -> Result<()> {
    match tokio::time::timeout(HTTP_SHUTDOWN_GRACE, &mut server_task).await {
        Ok(result) => result.context("server task panicked")??,
        Err(_) => {
            forced_shutdown.cancel();
            server_task.await.context("server task panicked")??;
        }
    }
    Ok(())
}

async fn update_status(
    state: AppState,
    mut updates: mpsc::Receiver<IndexUpdate>,
    shutdown: CancellationToken,
) {
    let mut terminal = TerminalProgress::new();
    let mut last_sse = Instant::now() - Duration::from_secs(1);
    loop {
        tokio::select! {
            () = shutdown.cancelled() => {
                state.status.write().await.phase = ServicePhase::ShuttingDown;
                terminal.finish();
                return;
            }
            update = updates.recv() => {
                let Some(update) = update else { terminal.finish(); return };
                match update {
                    IndexUpdate::Discovering { generation } => {
                        let progress = IndexProgress { total_files: 0, processed_files: 0, total_bytes: 0, processed_bytes: 0, failed_files: 0, excluded_files: 0, excluded_bytes: 0 };
                        {
                            let mut status = state.status.write().await;
                            status.generation = generation;
                            status.phase = ServicePhase::Discovering;
                            status.progress = progress.clone();
                        }
                        terminal.render(ServicePhase::Discovering, &progress, false);
                        publish_progress(
                            &state,
                            generation,
                            ServicePhase::Discovering,
                            progress,
                        )
                        .await;
                        last_sse = Instant::now();
                    }
                    IndexUpdate::Progress { generation, progress } => {
                        {
                            let mut status = state.status.write().await;
                            status.generation = generation;
                            status.phase = ServicePhase::Indexing;
                            status.progress = progress.clone();
                        }
                        terminal.render(ServicePhase::Indexing, &progress, false);
                        if last_sse.elapsed() >= Duration::from_millis(250)
                            || progress.processed_files == progress.total_files
                        {
                            publish_progress(
                                &state,
                                generation,
                                ServicePhase::Indexing,
                                progress,
                            )
                            .await;
                            last_sse = Instant::now();
                        }
                    }
                    IndexUpdate::SessionCommitted { generation, session_id } => {
                        publish_snapshot_committed(&state, generation, &session_id).await;
                    }
                    IndexUpdate::CatalogCommitted { generation, session_id } => {
                        publish_catalog_committed(&state, generation, &session_id).await;
                    }
                    IndexUpdate::SessionState { generation, session_id, state: sync_state } => {
                        publish_session_state(&state, generation, &session_id, sync_state).await;
                    }
                    IndexUpdate::SessionStateCleared { generation, session_id } => {
                        publish_session_state_cleared(&state, generation, &session_id).await;
                    }
                    IndexUpdate::Completed { report, foreground } => {
                        let phase = if report.failed_files == 0
                            && report.discovery_issues == 0
                            && !report.reconcile_again
                        {
                            ServicePhase::Ready
                        } else {
                            ServicePhase::Degraded
                        };
                        let progress = report_progress(&report);
                        let previous_phase = {
                            let mut status = state.status.write().await;
                            let previous_phase = status.phase;
                            status.generation = report.generation;
                            status.phase = phase;
                            status.last_reconcile_at = Some(chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Micros, true));
                            status.progress = progress.clone();
                            previous_phase
                        };
                        if foreground {
                            terminal.render(phase, &progress, true);
                        }
                        if foreground || phase != previous_phase {
                            publish_progress(&state, report.generation, phase, progress).await;
                        }
                    }
                }
            }
        }
    }
}

async fn publish_catalog_committed(state: &AppState, generation: u64, session_id: &str) {
    state.invalidate_session_groups(generation);
    state
        .sse
        .publish(
            SseEventType::CatalogUpdated,
            SseEventPayload {
                generation,
                phase: None,
                session_id: Some(session_id.to_owned()),
                entry_id: None,
                progress: None,
                diagnostic: None,
                sync_state: None,
                snapshot_revision: None,
            },
        )
        .await;
}

async fn publish_snapshot_committed(state: &AppState, generation: u64, session_id: &str) {
    state.invalidate_session_groups(generation);
    let snapshot_revision = sqlx::query_scalar::<_, i64>(
        "SELECT snapshot_revision FROM source_files WHERE session_id = ?",
    )
    .bind(session_id)
    .fetch_optional(state.database.pool())
    .await
    .ok()
    .flatten()
    .and_then(|value| u64::try_from(value).ok());
    let entry_id = sqlx::query_scalar::<_, String>(
        "SELECT id FROM entries WHERE session_id = ? ORDER BY sequence DESC LIMIT 1",
    )
    .bind(session_id)
    .fetch_optional(state.database.pool())
    .await
    .ok()
    .flatten();
    state
        .sse
        .publish(
            SseEventType::SnapshotUpdated,
            SseEventPayload {
                generation,
                phase: None,
                session_id: Some(session_id.to_owned()),
                entry_id,
                progress: None,
                diagnostic: None,
                sync_state: None,
                snapshot_revision,
            },
        )
        .await;
}

async fn publish_session_state(
    state: &AppState,
    generation: u64,
    session_id: &str,
    sync_state: SessionSyncState,
) {
    state
        .sse
        .publish(
            SseEventType::LiveSyncStateChanged,
            SseEventPayload {
                generation,
                phase: None,
                session_id: Some(session_id.to_owned()),
                entry_id: None,
                progress: None,
                diagnostic: None,
                sync_state: Some(sync_state),
                snapshot_revision: None,
            },
        )
        .await;
}

async fn publish_session_state_cleared(state: &AppState, generation: u64, session_id: &str) {
    state
        .sse
        .publish(
            SseEventType::LiveSyncStateChanged,
            SseEventPayload {
                generation,
                phase: None,
                session_id: Some(session_id.to_owned()),
                entry_id: None,
                progress: None,
                diagnostic: None,
                sync_state: None,
                snapshot_revision: None,
            },
        )
        .await;
}

async fn publish_progress(
    state: &AppState,
    generation: u64,
    phase: ServicePhase,
    progress: IndexProgress,
) {
    state
        .sse
        .publish(
            SseEventType::CatalogProgress,
            SseEventPayload {
                generation,
                phase: Some(phase),
                session_id: None,
                entry_id: None,
                progress: Some(progress),
                diagnostic: None,
                sync_state: None,
                snapshot_revision: None,
            },
        )
        .await;
}

fn report_progress(report: &ReconcileReport) -> IndexProgress {
    let foreground_files = report
        .discovered_files
        .saturating_sub(report.excluded_files);
    let foreground_bytes = report
        .discovered_bytes
        .saturating_sub(report.excluded_bytes);
    IndexProgress {
        total_files: foreground_files,
        processed_files: foreground_files,
        total_bytes: foreground_bytes,
        processed_bytes: foreground_bytes,
        failed_files: report.failed_files,
        excluded_files: report.excluded_files,
        excluded_bytes: report.excluded_bytes,
    }
}

struct TerminalProgress {
    tty: bool,
    last_phase: Option<ServicePhase>,
    line_open: bool,
}

impl TerminalProgress {
    fn new() -> Self {
        Self {
            tty: std::io::stderr().is_terminal(),
            last_phase: None,
            line_open: false,
        }
    }

    fn render(&mut self, phase: ServicePhase, progress: &IndexProgress, final_line: bool) {
        let line = match phase {
            ServicePhase::Discovering => "agents-viewer: discovering sessions...".to_owned(),
            ServicePhase::Indexing => {
                let percent = progress
                    .processed_bytes
                    .saturating_mul(100)
                    .checked_div(progress.total_bytes)
                    .unwrap_or(100);
                format!(
                    "agents-viewer: indexing {}/{} files ({percent}%)",
                    progress.processed_files, progress.total_files
                )
            }
            ServicePhase::Ready => format!(
                "agents-viewer: index ready ({} files, {} excluded)",
                progress.processed_files, progress.excluded_files
            ),
            ServicePhase::Degraded => format!(
                "agents-viewer: index completed with {} failures",
                progress.failed_files
            ),
            ServicePhase::Starting | ServicePhase::ShuttingDown => return,
        };
        if self.tty {
            if final_line {
                eprintln!("\r{line}\x1b[K");
                self.line_open = false;
            } else {
                eprint!("\r{line}\x1b[K");
                let _ = std::io::stderr().flush();
                self.line_open = true;
            }
        } else if self.last_phase != Some(phase) || final_line {
            eprintln!("{line}");
        }
        self.last_phase = Some(phase);
    }

    fn finish(&mut self) {
        if self.tty && self.line_open {
            eprintln!();
            self.line_open = false;
        }
    }
}

async fn heartbeat(state: AppState, shutdown: CancellationToken) {
    let mut interval = tokio::time::interval(Duration::from_secs(15));
    interval.tick().await;
    loop {
        tokio::select! {
            () = shutdown.cancelled() => return,
            _ = interval.tick() => {
                let generation = state.status.read().await.generation;
                state.sse.publish(SseEventType::Heartbeat, SseEventPayload { generation, phase: None, session_id: None, entry_id: None, progress: None, diagnostic: None, sync_state: None, snapshot_revision: None }).await;
            }
        }
    }
}

async fn wait_for_signal() -> Result<()> {
    #[cfg(unix)]
    {
        let mut terminate =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                .context("install SIGTERM handler")?;
        tokio::select! {
            result = tokio::signal::ctrl_c() => result.context("install SIGINT handler"),
            _ = terminate.recv() => Ok(()),
        }
    }
    #[cfg(not(unix))]
    tokio::signal::ctrl_c()
        .await
        .context("install Ctrl-C handler")
}

fn init_tracing(level: LogLevel) {
    let filter = tracing_subscriber::EnvFilter::new(level.as_filter());
    let _ = tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_writer(std::io::stderr)
        .try_init();
}
