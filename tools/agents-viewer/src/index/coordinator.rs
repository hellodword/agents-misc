use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

use anyhow::{Context as _, Result};
use futures::{StreamExt as _, stream};
use sqlx::Row as _;
use tokio::sync::mpsc;
use tokio_util::sync::CancellationToken;

use crate::model::IndexProgress;
use crate::paths::SourceRoots;
use crate::rollout::RootKind;
use crate::watch::WatchEvent;

use super::scanner::{discover_sources_cancellable, scan_source};
use super::writer::WriterHandle;
use super::{Database, InitialIndexPolicy};

pub const MAX_PARSER_TASKS: usize = 2;
pub const RECONCILE_INTERVAL: std::time::Duration = std::time::Duration::from_secs(30);

#[derive(Clone)]
pub struct IndexCoordinator {
    database: Database,
    writer: WriterHandle,
    roots: SourceRoots,
    max_event_bytes: usize,
    policy: InitialIndexPolicy,
    generation: Arc<AtomicU64>,
}

#[derive(Clone, Debug, Default)]
pub struct ReconcileReport {
    pub generation: u64,
    pub discovered_files: u64,
    pub discovered_bytes: u64,
    pub indexed_files: u64,
    pub appended_files: u64,
    pub failed_files: u64,
    pub removed_files: u64,
    pub discovery_issues: u64,
    pub excluded_files: u64,
    pub excluded_bytes: u64,
    pub reconcile_again: bool,
    pub failures: Vec<String>,
    pub updated_sessions: Vec<String>,
    pub appended_sessions: Vec<String>,
}

#[derive(Clone, Debug)]
pub enum IndexUpdate {
    Discovering {
        generation: u64,
    },
    Progress {
        generation: u64,
        progress: IndexProgress,
    },
    Completed {
        report: ReconcileReport,
        foreground: bool,
    },
}

impl IndexCoordinator {
    #[must_use]
    pub fn new(
        database: Database,
        writer: WriterHandle,
        roots: SourceRoots,
        max_event_bytes: usize,
        policy: InitialIndexPolicy,
    ) -> Self {
        Self {
            database,
            writer,
            roots,
            max_event_bytes,
            policy,
            generation: Arc::new(AtomicU64::new(0)),
        }
    }

    pub async fn reconcile(&self) -> Result<ReconcileReport> {
        self.reconcile_mode(&CancellationToken::new(), None, false)
            .await
    }

    pub async fn reconcile_with_updates(
        &self,
        shutdown: &CancellationToken,
        updates: Option<&mpsc::Sender<IndexUpdate>>,
    ) -> Result<ReconcileReport> {
        self.reconcile_mode(shutdown, updates, true).await
    }

    async fn reconcile_mode(
        &self,
        shutdown: &CancellationToken,
        updates: Option<&mpsc::Sender<IndexUpdate>>,
        foreground: bool,
    ) -> Result<ReconcileReport> {
        let generation = self.generation.fetch_add(1, Ordering::SeqCst) + 1;
        if foreground {
            send_update(updates, IndexUpdate::Discovering { generation }).await;
        }
        let roots = self.roots.clone();
        let max_event_bytes = self.max_event_bytes;
        let policy = self.policy;
        let now_micros = chrono::Utc::now().timestamp_micros();
        let discovery_shutdown = shutdown.clone();
        let discovery = tokio::task::spawn_blocking(move || {
            discover_sources_cancellable(
                &roots,
                max_event_bytes,
                generation,
                now_micros,
                policy,
                &discovery_shutdown,
            )
        })
        .await
        .context("metadata discovery task panicked")??;
        if shutdown.is_cancelled() {
            anyhow::bail!("index reconcile cancelled");
        }
        let mut progress = IndexProgress {
            total_files: discovery.sources.len() as u64,
            processed_files: 0,
            total_bytes: discovery.total_bytes,
            processed_bytes: 0,
            failed_files: 0,
            excluded_files: discovery.excluded_files,
            excluded_bytes: discovery.excluded_bytes,
        };
        if foreground {
            send_update(
                updates,
                IndexUpdate::Progress {
                    generation,
                    progress: progress.clone(),
                },
            )
            .await;
        }
        let mut report = ReconcileReport {
            generation,
            discovered_files: discovery.sources.len() as u64,
            discovered_bytes: discovery.total_bytes,
            discovery_issues: discovery.issues.len() as u64,
            excluded_files: discovery.excluded_files,
            excluded_bytes: discovery.excluded_bytes,
            ..ReconcileReport::default()
        };
        let mut changed = Vec::new();
        for source in &discovery.sources {
            if shutdown.is_cancelled() {
                anyhow::bail!("index reconcile cancelled");
            }
            if source_unchanged(&self.database, source).await? {
                self.writer
                    .mark_seen(
                        source.source.root_kind,
                        source.source.relative_path.clone(),
                        generation,
                    )
                    .await?;
                progress.processed_files = progress.processed_files.saturating_add(1);
                progress.processed_bytes = progress
                    .processed_bytes
                    .saturating_add(source.source.size_bytes);
                if foreground {
                    send_update(
                        updates,
                        IndexUpdate::Progress {
                            generation,
                            progress: progress.clone(),
                        },
                    )
                    .await;
                }
            } else {
                changed.push(source.clone());
            }
        }

        let writer = self.writer.clone();
        let mut results = stream::iter(changed.into_iter().map(|source| {
            let writer = writer.clone();
            let database = self.database.clone();
            let scan_shutdown = shutdown.clone();
            let bytes = source.source.size_bytes;
            async move {
                (
                    bytes,
                    scan_source(
                        database,
                        writer,
                        source,
                        max_event_bytes,
                        now_micros,
                        scan_shutdown,
                    )
                    .await,
                )
            }
        }))
        .buffer_unordered(MAX_PARSER_TASKS);
        while let Some((bytes, result)) = results.next().await {
            progress.processed_files = progress.processed_files.saturating_add(1);
            progress.processed_bytes = progress.processed_bytes.saturating_add(bytes);
            match result {
                Ok(outcome) => {
                    report.indexed_files = report.indexed_files.saturating_add(1);
                    report.appended_files = report
                        .appended_files
                        .saturating_add(u64::from(outcome.appended));
                    report.reconcile_again |= outcome.changed_during_scan;
                    if outcome.appended {
                        report.appended_sessions.push(outcome.session_id.clone());
                    }
                    report.updated_sessions.push(outcome.session_id);
                }
                Err(error) => {
                    if !shutdown.is_cancelled() {
                        report.failed_files = report.failed_files.saturating_add(1);
                        progress.failed_files = progress.failed_files.saturating_add(1);
                        report.reconcile_again = true;
                        report.failures.push(format!("{error:#}"));
                    }
                }
            }
            if foreground {
                send_update(
                    updates,
                    IndexUpdate::Progress {
                        generation,
                        progress: progress.clone(),
                    },
                )
                .await;
            }
        }
        if shutdown.is_cancelled() {
            anyhow::bail!("index reconcile cancelled");
        }
        if discovery.issues.is_empty() {
            report.removed_files = self.writer.remove_unseen(generation).await?;
        } else {
            report.reconcile_again = true;
        }
        send_update(
            updates,
            IndexUpdate::Completed {
                report: report.clone(),
                foreground,
            },
        )
        .await;
        Ok(report)
    }

    pub async fn run(
        &self,
        watch_events: mpsc::Receiver<WatchEvent>,
        shutdown: CancellationToken,
    ) -> Result<()> {
        self.run_with_updates(watch_events, shutdown, None, false)
            .await
    }

    pub async fn run_with_updates(
        &self,
        mut watch_events: mpsc::Receiver<WatchEvent>,
        shutdown: CancellationToken,
        updates: Option<mpsc::Sender<IndexUpdate>>,
        foreground_first: bool,
    ) -> Result<()> {
        let mut interval = tokio::time::interval(RECONCILE_INTERVAL);
        interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut foreground_next = foreground_first;
        let mut bootstrap_pending = foreground_first;
        loop {
            tokio::select! {
                () = shutdown.cancelled() => return Ok(()),
                _ = interval.tick() => {
                    let foreground = std::mem::take(&mut foreground_next);
                    if let Err(error) = self.reconcile_trigger(&shutdown, updates.as_ref(), foreground, &mut bootstrap_pending).await {
                        if shutdown.is_cancelled() { return Ok(()); }
                        return Err(error);
                    }
                }
                event = watch_events.recv() => {
                    match event {
                        Some(WatchEvent::Paths(_) | WatchEvent::Reconcile) => {
                            let foreground = std::mem::take(&mut foreground_next);
                            if let Err(error) = self.reconcile_trigger(&shutdown, updates.as_ref(), foreground, &mut bootstrap_pending).await {
                                if shutdown.is_cancelled() { return Ok(()); }
                                return Err(error);
                            }
                        }
                        Some(WatchEvent::Degraded(_)) => {
                            // Periodic reconcile remains active as watcher fallback.
                        }
                        None => {
                            let foreground = std::mem::take(&mut foreground_next);
                            if let Err(error) = self.reconcile_trigger(&shutdown, updates.as_ref(), foreground, &mut bootstrap_pending).await {
                                if shutdown.is_cancelled() { return Ok(()); }
                                return Err(error);
                            }
                        }
                    }
                }
            }
        }
    }

    async fn reconcile_trigger(
        &self,
        shutdown: &CancellationToken,
        updates: Option<&mpsc::Sender<IndexUpdate>>,
        foreground: bool,
        bootstrap_pending: &mut bool,
    ) -> Result<()> {
        let report = self.reconcile_mode(shutdown, updates, foreground).await?;
        if *bootstrap_pending && report_is_healthy(&report) {
            self.database.mark_bootstrap_complete().await?;
            *bootstrap_pending = false;
        }
        Ok(())
    }

    #[must_use]
    pub fn generation(&self) -> u64 {
        self.generation.load(Ordering::SeqCst)
    }
}

fn report_is_healthy(report: &ReconcileReport) -> bool {
    report.failed_files == 0 && report.discovery_issues == 0 && !report.reconcile_again
}

async fn send_update(sender: Option<&mpsc::Sender<IndexUpdate>>, update: IndexUpdate) {
    if let Some(sender) = sender {
        let _ = sender.send(update).await;
    }
}

async fn source_unchanged(
    database: &Database,
    source: &super::scanner::DiscoveredSource,
) -> Result<bool> {
    let root_kind = match source.source.root_kind {
        RootKind::Active => "active",
        RootKind::Archived => "archived",
    };
    let row = sqlx::query(
        "SELECT file_key, size_bytes, mtime_ns, scan_state FROM source_files \
         WHERE root_kind = ? AND relative_path = ?",
    )
    .bind(root_kind)
    .bind(&source.source.relative_path)
    .fetch_optional(database.pool())
    .await?;
    Ok(row.is_some_and(|row| {
        row.get::<String, _>("file_key") == source.source.file_key
            && row.get::<i64, _>("size_bytes")
                == i64::try_from(source.source.size_bytes).unwrap_or(i64::MAX)
            && row.get::<i64, _>("mtime_ns") == source.source.mtime_ns
            && row.get::<String, _>("scan_state") == "ready"
    }))
}
