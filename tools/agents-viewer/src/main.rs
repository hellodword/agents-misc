use std::io::{IsTerminal as _, Write as _};
use std::process::ExitCode;
use std::time::{Duration, Instant};

use agents_viewer::cli::Cli;
use agents_viewer::config::{Config, LogLevel};
use agents_viewer::index::coordinator::{IndexCoordinator, IndexUpdate, ReconcileReport};
use agents_viewer::index::recovery::replace_database_atomically;
use agents_viewer::index::writer::spawn_writer;
use agents_viewer::index::{Database, InitialIndexPolicy};
use agents_viewer::model::{IndexProgress, ServicePhase, SseEventPayload, SseEventType};
use agents_viewer::permissions::{acquire_cache_lock, prepare_cache_directory};
use agents_viewer::server::{self, AppState};
use agents_viewer::watch::start_watcher;
use anyhow::{Context as _, Result};
use clap::Parser as _;
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

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
    let (database, policy, bootstrap_required) = if config.rebuild_index {
        match rebuild_database(&config, &fingerprint, now_micros, shutdown.clone()).await {
            Ok((database, policy)) => (database, policy, false),
            Err(_) if shutdown.is_cancelled() => return Ok(()),
            Err(error) => return Err(error),
        }
    } else {
        let opened =
            Database::open_or_recover_with_disposition(&config.cache.database, &fingerprint)
                .await?;
        let database = opened.database;
        let policy = database
            .resolve_index_policy(
                config.initial_index_days,
                config.max_event_bytes,
                now_micros,
            )
            .await?;
        (database, policy, opened.bootstrap_required)
    };

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
    );
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
    let server = axum::serve(listener, server::router(state.clone(), bound))
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
        watcher.shutdown().await;
        coordinator_task
            .await
            .context("coordinator task panicked")??;
        writer.shutdown().await?;
        writer_task.wait().await?;
        let _ = update_task.await;
        let _ = heartbeat_task.await;
        server_task.await.context("server task panicked")??;
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

async fn rebuild_database(
    config: &Config,
    fingerprint: &str,
    now_micros: i64,
    shutdown: CancellationToken,
) -> Result<(Database, InitialIndexPolicy)> {
    let new_path = config.cache.namespace.join("index.sqlite3.new");
    remove_database_family(&new_path)?;
    let rebuilt = Database::open_or_recover(&new_path, fingerprint).await?;
    let policy = rebuilt
        .resolve_index_policy(
            config.initial_index_days,
            config.max_event_bytes,
            now_micros,
        )
        .await?;
    let (writer, task) = spawn_writer(rebuilt.clone());
    let coordinator = IndexCoordinator::new(
        rebuilt.clone(),
        writer.clone(),
        config.roots.clone(),
        config.max_event_bytes,
        policy,
    );
    let (update_sender, update_receiver) = mpsc::channel(64);
    let terminal_task = tokio::spawn(update_terminal_only(update_receiver, shutdown.clone()));
    let reconcile = coordinator
        .reconcile_with_updates(&shutdown, Some(&update_sender))
        .await
        .context("rebuild index");
    drop(update_sender);
    let _ = terminal_task.await;
    writer.shutdown().await?;
    task.wait().await?;
    let report = match reconcile {
        Ok(report) => report,
        Err(error) => {
            rebuilt.close().await;
            return Err(error);
        }
    };
    if !report_is_healthy(&report) {
        rebuilt.close().await;
        anyhow::bail!(
            "rebuild index completed with {} failed files and {} discovery issues",
            report.failed_files,
            report.discovery_issues
        );
    }
    rebuilt.mark_bootstrap_complete().await?;
    rebuilt.optimize().await?;
    rebuilt.close().await;
    let database = replace_database_atomically(&config.cache.database, &new_path, fingerprint)
        .await
        .context("activate rebuilt index")?;
    Ok((database, policy))
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
                        for session_id in &report.updated_sessions {
                            state.sse.publish(SseEventType::SessionUpdated, SseEventPayload { generation: report.generation, phase: None, session_id: Some(session_id.clone()), entry_id: None, progress: None, diagnostic: None }).await;
                        }
                        for session_id in &report.updated_sessions {
                            let entry_id = sqlx::query_scalar::<_, String>("SELECT id FROM entries WHERE session_id = ? ORDER BY sequence DESC LIMIT 1")
                                .bind(session_id)
                                .fetch_optional(state.database.pool())
                                .await
                                .ok()
                                .flatten();
                            state.sse.publish(SseEventType::EntryUpdated, SseEventPayload { generation: report.generation, phase: None, session_id: Some(session_id.clone()), entry_id, progress: None, diagnostic: None }).await;
                        }
                    }
                }
            }
        }
    }
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
            SseEventType::IndexProgress,
            SseEventPayload {
                generation,
                phase: Some(phase),
                session_id: None,
                entry_id: None,
                progress: Some(progress),
                diagnostic: None,
            },
        )
        .await;
}

async fn update_terminal_only(
    mut updates: mpsc::Receiver<IndexUpdate>,
    shutdown: CancellationToken,
) {
    let mut terminal = TerminalProgress::new();
    loop {
        tokio::select! {
            () = shutdown.cancelled() => { terminal.finish(); return; }
            update = updates.recv() => match update {
                Some(IndexUpdate::Discovering { .. }) => terminal.render(ServicePhase::Discovering, &IndexProgress { total_files: 0, processed_files: 0, total_bytes: 0, processed_bytes: 0, failed_files: 0, excluded_files: 0, excluded_bytes: 0 }, false),
                Some(IndexUpdate::Progress { progress, .. }) => terminal.render(ServicePhase::Indexing, &progress, false),
                Some(IndexUpdate::Completed { report, .. }) => {
                    let phase = if report.failed_files == 0 && report.discovery_issues == 0 && !report.reconcile_again { ServicePhase::Ready } else { ServicePhase::Degraded };
                    let progress = report_progress(&report);
                    terminal.render(phase, &progress, true);
                }
                None => { terminal.finish(); return; }
            }
        }
    }
}

fn report_progress(report: &ReconcileReport) -> IndexProgress {
    IndexProgress {
        total_files: report.discovered_files,
        processed_files: report.discovered_files,
        total_bytes: report.discovered_bytes,
        processed_bytes: report.discovered_bytes,
        failed_files: report.failed_files,
        excluded_files: report.excluded_files,
        excluded_bytes: report.excluded_bytes,
    }
}

fn report_is_healthy(report: &ReconcileReport) -> bool {
    report.failed_files == 0 && report.discovery_issues == 0 && !report.reconcile_again
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
                state.sse.publish(SseEventType::Heartbeat, SseEventPayload { generation, phase: None, session_id: None, entry_id: None, progress: None, diagnostic: None }).await;
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

fn remove_database_family(path: &std::path::Path) -> Result<()> {
    for suffix in ["", "-wal", "-shm"] {
        let member = std::path::PathBuf::from(format!("{}{}", path.display(), suffix));
        if member.exists() {
            std::fs::remove_file(&member)
                .with_context(|| format!("remove stale rebuild file {}", member.display()))?;
        }
    }
    Ok(())
}

fn init_tracing(level: LogLevel) {
    let filter = tracing_subscriber::EnvFilter::new(level.as_filter());
    let _ = tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_writer(std::io::stderr)
        .try_init();
}
