use std::cmp::Ordering as CmpOrdering;
use std::collections::{BinaryHeap, HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, RwLock};
use std::time::{Duration, Instant};

use anyhow::{Context as _, Result, anyhow};
use futures::{StreamExt as _, stream};
use sqlx::Row as _;
use tokio::sync::{Mutex, mpsc};
use tokio_util::sync::CancellationToken;

use crate::model::{IndexProgress, SessionFreshness, SessionSyncState, SessionSyncStatus};
use crate::paths::SourceRoots;
use crate::rollout::RootKind;
use crate::watch::WatchEvent;

use super::control::{IoGate, ScanLease, WorkPriority};
use super::relationships::reconcile_plan_handoffs;
use super::scanner::{
    DiscoveredSource, Discovery, discover_source_path, discover_sources_cancellable,
    scan_source_with_lease, source_precedes,
};
use super::writer::WriterHandle;
use super::{Database, InitialIndexPolicy};

pub const MAX_PARSER_TASKS: usize = 2;
pub const DIRECT_SYNC_QUEUE_CAPACITY: usize = 128;
pub const FULL_SWEEP_INTERVAL: Duration = Duration::from_secs(6 * 60 * 60);
pub const BACKGROUND_IDLE_DELAY: Duration = Duration::from_secs(30);

const SCHEDULER_TICK: Duration = Duration::from_millis(100);
const FULL_SWEEP_BACKOFF: [Duration; 4] = [
    Duration::from_secs(60),
    Duration::from_secs(5 * 60),
    Duration::from_secs(30 * 60),
    FULL_SWEEP_INTERVAL,
];

#[derive(Clone)]
pub struct IndexCoordinator {
    database: Database,
    writer: WriterHandle,
    roots: SourceRoots,
    max_event_bytes: usize,
    policy: InitialIndexPolicy,
    generation: Arc<AtomicU64>,
    io_gate: IoGate,
    shared: Arc<RwLock<SharedState>>,
    commands: mpsc::Sender<CoordinatorCommand>,
    command_receiver: Arc<Mutex<Option<mpsc::Receiver<CoordinatorCommand>>>>,
}

#[derive(Clone)]
pub struct CoordinatorHandle {
    shared: Arc<RwLock<SharedState>>,
    commands: mpsc::Sender<CoordinatorCommand>,
}

#[derive(Default)]
struct SharedState {
    catalog_ready: bool,
    catalog: HashMap<String, Arc<DiscoveredSource>>,
    freshness: HashMap<String, SessionFreshness>,
    snapshots: HashSet<String>,
    sync_states: HashMap<String, SessionSyncState>,
}

enum CoordinatorCommand {
    EnsureSession(String),
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
    SessionCommitted {
        generation: u64,
        session_id: String,
    },
    SessionState {
        generation: u64,
        session_id: String,
        state: SessionSyncState,
    },
    Completed {
        report: ReconcileReport,
        foreground: bool,
    },
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct SourceFingerprint {
    root_kind: &'static str,
    relative_path: String,
    file_key: String,
    size_bytes: u64,
    mtime_ns: i64,
}

#[derive(Clone, Debug)]
struct WorkItem {
    source: Arc<DiscoveredSource>,
    fingerprint: SourceFingerprint,
    priority: WorkPriority,
    cycle_generation: Option<u64>,
}

impl WorkItem {
    fn new(
        source: Arc<DiscoveredSource>,
        priority: WorkPriority,
        cycle_generation: Option<u64>,
    ) -> Self {
        let fingerprint = source_fingerprint(&source);
        Self {
            source,
            fingerprint,
            priority,
            cycle_generation,
        }
    }
}

impl PartialEq for WorkItem {
    fn eq(&self, other: &Self) -> bool {
        self.priority == other.priority
            && self.fingerprint == other.fingerprint
            && self.source.session_id == other.source.session_id
    }
}

impl Eq for WorkItem {}

impl PartialOrd for WorkItem {
    fn partial_cmp(&self, other: &Self) -> Option<CmpOrdering> {
        Some(self.cmp(other))
    }
}

impl Ord for WorkItem {
    fn cmp(&self, other: &Self) -> CmpOrdering {
        self.priority
            .cmp(&other.priority)
            .then_with(|| {
                self.source
                    .source
                    .mtime_ns
                    .cmp(&other.source.source.mtime_ns)
            })
            .then_with(|| {
                self.source
                    .created_at_micros
                    .cmp(&other.source.created_at_micros)
            })
            // BinaryHeap is a max heap. Reverse stable text keys so the smaller ID/path wins.
            .then_with(|| other.source.session_id.cmp(&self.source.session_id))
            .then_with(|| {
                other
                    .source
                    .source
                    .relative_path
                    .cmp(&self.source.source.relative_path)
            })
    }
}

struct InflightWork {
    work: WorkItem,
    lease: ScanLease,
}

struct WorkCompletion {
    work: WorkItem,
    result: Result<super::scanner::ScanOutcome>,
}

struct ActiveCycle {
    report: ReconcileReport,
    progress: IndexProgress,
    pending_sessions: HashSet<String>,
    foreground: bool,
}

#[derive(Clone, Debug)]
struct StoredSource {
    root_kind: RootKind,
    relative_path: String,
    file_key: String,
    size_bytes: u64,
    mtime_ns: i64,
    scan_state: String,
    session_id: Option<String>,
}

struct RuntimeScheduler {
    database: Database,
    writer: WriterHandle,
    roots: SourceRoots,
    max_event_bytes: usize,
    policy: InitialIndexPolicy,
    gate: IoGate,
    shared: Arc<RwLock<SharedState>>,
    updates: Option<mpsc::Sender<IndexUpdate>>,
    shutdown: CancellationToken,
    queue: BinaryHeap<WorkItem>,
    queued: HashMap<String, WorkItem>,
    inflight: HashMap<String, InflightWork>,
    deferred: HashMap<String, WorkItem>,
    completion_sender: mpsc::Sender<WorkCompletion>,
    last_high_activity: Instant,
    background_hold: Option<ScanLease>,
    cycle: Option<ActiveCycle>,
    relationships_dirty: bool,
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
        let (commands, command_receiver) = mpsc::channel(DIRECT_SYNC_QUEUE_CAPACITY);
        Self {
            database,
            writer,
            roots,
            max_event_bytes,
            policy,
            generation: Arc::new(AtomicU64::new(0)),
            io_gate: IoGate::new(),
            shared: Arc::new(RwLock::new(SharedState::default())),
            commands,
            command_receiver: Arc::new(Mutex::new(Some(command_receiver))),
        }
    }

    #[must_use]
    pub fn handle(&self) -> CoordinatorHandle {
        CoordinatorHandle {
            shared: Arc::clone(&self.shared),
            commands: self.commands.clone(),
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
        let generation = self.next_generation();
        if foreground {
            send_update(updates, IndexUpdate::Discovering { generation }).await;
        }
        let now_micros = chrono::Utc::now().timestamp_micros();
        let discovery = self
            .discover(generation, now_micros, shutdown.clone())
            .await?;
        let stored = load_stored_sources(&self.database).await?;
        let stored_by_key = stored
            .iter()
            .cloned()
            .map(|source| (stored_key(source.root_kind, &source.relative_path), source))
            .collect::<HashMap<_, _>>();
        let discovered_keys = discovery
            .sources
            .iter()
            .map(source_key)
            .collect::<HashSet<_>>();
        let mut missing = Vec::new();
        if discovery.issues.is_empty() {
            for source in &stored {
                if !discovered_keys.contains(&stored_key(source.root_kind, &source.relative_path))
                    && source.scan_state != "source_missing"
                {
                    missing.push((source.root_kind, source.relative_path.clone()));
                }
            }
        }
        let mut present = Vec::new();
        let mut changed = Vec::new();
        let mut processed_files = 0_u64;
        let mut processed_bytes = 0_u64;
        let mut excluded_files = 0_u64;
        let mut excluded_bytes = 0_u64;
        for source in &discovery.sources {
            let automatic = automatic_priority(self.policy, source, now_micros);
            let Some(_priority) = automatic else {
                excluded_files = excluded_files.saturating_add(1);
                excluded_bytes = excluded_bytes.saturating_add(source.source.size_bytes);
                continue;
            };
            // Explicit reconciliation and atomic rebuild complete the foreground window. Older
            // history remains the long-lived runtime scheduler's responsibility.
            if automatic == Some(WorkPriority::Background) {
                excluded_files = excluded_files.saturating_add(1);
                excluded_bytes = excluded_bytes.saturating_add(source.source.size_bytes);
                continue;
            }
            match stored_by_key.get(&source_key(source)) {
                Some(stored) if metadata_matches(stored, source) => {
                    if stored.scan_state == "source_missing" {
                        present.push((stored.root_kind, stored.relative_path.clone()));
                    }
                    processed_files = processed_files.saturating_add(1);
                    processed_bytes = processed_bytes.saturating_add(source.source.size_bytes);
                }
                _ => changed.push(source.clone()),
            }
        }
        self.writer.mark_sources_missing(missing.clone()).await?;
        self.writer.mark_sources_present(present).await?;
        let mut progress = IndexProgress {
            total_files: processed_files.saturating_add(changed.len() as u64),
            processed_files,
            total_bytes: processed_bytes
                .saturating_add(changed.iter().map(|source| source.source.size_bytes).sum()),
            processed_bytes,
            failed_files: 0,
            excluded_files,
            excluded_bytes,
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
            removed_files: missing.len() as u64,
            discovery_issues: discovery.issues.len() as u64,
            excluded_files,
            excluded_bytes,
            reconcile_again: !discovery.issues.is_empty(),
            ..ReconcileReport::default()
        };
        let gate = self.io_gate.clone();
        let writer = self.writer.clone();
        let database = self.database.clone();
        let max_event_bytes = self.max_event_bytes;
        let mut results = stream::iter(changed.into_iter().map(|source| {
            let writer = writer.clone();
            let database = database.clone();
            let scan_shutdown = shutdown.clone();
            let lease = gate.register(WorkPriority::Recent);
            let bytes = source.source.size_bytes;
            async move {
                (
                    bytes,
                    scan_source_with_lease(
                        database,
                        writer,
                        source,
                        max_event_bytes,
                        now_micros,
                        scan_shutdown,
                        lease,
                    )
                    .await,
                )
            }
        }))
        .buffer_unordered(MAX_PARSER_TASKS);
        let mut notified = HashSet::new();
        while let Some((bytes, result)) = results.next().await {
            progress.processed_files = progress.processed_files.saturating_add(1);
            progress.processed_bytes = progress.processed_bytes.saturating_add(bytes);
            match result {
                Ok(outcome) => {
                    record_outcome(&mut report, &outcome);
                    send_session_committed(updates, &mut notified, generation, &outcome.session_id)
                        .await;
                }
                Err(error) if !shutdown.is_cancelled() => {
                    report.failed_files = report.failed_files.saturating_add(1);
                    report.reconcile_again = true;
                    report.failures.push(format!("{error:#}"));
                    progress.failed_files = progress.failed_files.saturating_add(1);
                }
                Err(_) => anyhow::bail!("index reconcile cancelled"),
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
        if report.indexed_files > 0 {
            let relationship_updates = reconcile_plan_handoffs(&self.database).await?;
            for session_id in &relationship_updates {
                send_update(
                    updates,
                    IndexUpdate::SessionCommitted {
                        generation,
                        session_id: session_id.clone(),
                    },
                )
                .await;
            }
            report.updated_sessions.extend(relationship_updates);
        }
        report.updated_sessions.sort();
        report.updated_sessions.dedup();
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
        let mut command_receiver = self
            .command_receiver
            .lock()
            .await
            .take()
            .ok_or_else(|| anyhow!("index coordinator is already running"))?;
        let (completion_sender, mut completion_receiver) = mpsc::channel(8);
        let mut runtime = RuntimeScheduler::new(self, updates, shutdown.clone(), completion_sender);
        let generation = self.next_generation();
        if foreground_first {
            send_update(
                runtime.updates.as_ref(),
                IndexUpdate::Discovering { generation },
            )
            .await;
        }
        let now_micros = chrono::Utc::now().timestamp_micros();
        let discovery = self
            .discover(generation, now_micros, shutdown.clone())
            .await?;
        runtime
            .apply_full_discovery(discovery, generation, now_micros, foreground_first)
            .await?;
        let mut bootstrap_pending = foreground_first;
        runtime.start_available();
        runtime.maybe_finalize_cycle(&mut bootstrap_pending).await?;

        let mut scheduler_tick = tokio::time::interval(SCHEDULER_TICK);
        scheduler_tick.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut safety_sweep = tokio::time::interval_at(
            tokio::time::Instant::now() + FULL_SWEEP_INTERVAL,
            FULL_SWEEP_INTERVAL,
        );
        safety_sweep.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
        let mut watch_open = true;
        let mut commands_open = true;
        let mut draining = false;
        let mut next_recovery_sweep = Instant::now();
        let mut recovery_backoff = 0_usize;

        loop {
            tokio::select! {
                biased;
                () = shutdown.cancelled(), if !draining => {
                    draining = true;
                }
                completion = completion_receiver.recv() => {
                    if let Some(completion) = completion {
                        runtime.handle_completion(completion).await?;
                    }
                }
                command = command_receiver.recv(), if commands_open && !draining => {
                    match command {
                        Some(CoordinatorCommand::EnsureSession(session_id)) => {
                            runtime.ensure_session(&session_id).await;
                        }
                        None => commands_open = false,
                    }
                }
                event = watch_events.recv(), if watch_open && !draining => {
                    match event {
                        Some(WatchEvent::Paths(paths)) => {
                            runtime.handle_paths(paths, self.next_generation()).await?;
                        }
                        Some(WatchEvent::Reconcile | WatchEvent::Degraded(_)) => {
                            if runtime.cycle.is_none() && Instant::now() >= next_recovery_sweep {
                                let generation = self.next_generation();
                                let now_micros = chrono::Utc::now().timestamp_micros();
                                let discovery = self.discover(generation, now_micros, shutdown.clone()).await?;
                                runtime.apply_full_discovery(discovery, generation, now_micros, false).await?;
                                let delay = FULL_SWEEP_BACKOFF[recovery_backoff.min(FULL_SWEEP_BACKOFF.len() - 1)];
                                recovery_backoff = recovery_backoff.saturating_add(1);
                                next_recovery_sweep = Instant::now() + delay;
                            }
                        }
                        None => watch_open = false,
                    }
                }
                _ = safety_sweep.tick(), if !draining => {
                    if runtime.cycle.is_none() {
                        let generation = self.next_generation();
                        let now_micros = chrono::Utc::now().timestamp_micros();
                        let discovery = self.discover(generation, now_micros, shutdown.clone()).await?;
                        runtime.apply_full_discovery(discovery, generation, now_micros, false).await?;
                        recovery_backoff = 0;
                        next_recovery_sweep = Instant::now();
                    }
                }
                _ = scheduler_tick.tick() => {}
            }
            if !draining {
                runtime.start_available();
                runtime.maybe_finalize_cycle(&mut bootstrap_pending).await?;
                runtime.flush_relationships_if_idle().await?;
            }
            if draining && runtime.inflight.is_empty() {
                return Ok(());
            }
        }
    }

    async fn discover(
        &self,
        generation: u64,
        now_micros: i64,
        shutdown: CancellationToken,
    ) -> Result<Discovery> {
        let roots = self.roots.clone();
        let max_event_bytes = self.max_event_bytes;
        let policy = self.policy;
        tokio::task::spawn_blocking(move || {
            discover_sources_cancellable(
                &roots,
                max_event_bytes,
                generation,
                now_micros,
                policy,
                &shutdown,
            )
        })
        .await
        .context("metadata discovery task panicked")?
    }

    #[must_use]
    pub fn generation(&self) -> u64 {
        self.generation.load(Ordering::SeqCst)
    }

    fn next_generation(&self) -> u64 {
        self.generation.fetch_add(1, Ordering::SeqCst) + 1
    }
}

impl CoordinatorHandle {
    pub fn ensure_session(&self, session_id: &str) -> Result<SessionSyncStatus> {
        let mut shared = self
            .shared
            .write()
            .map_err(|_| anyhow!("index catalog lock poisoned"))?;
        let has_snapshot = shared.snapshots.contains(session_id);
        if let Some(state) = shared.sync_states.get(session_id).copied() {
            return Ok(SessionSyncStatus {
                session_id: session_id.to_owned(),
                state,
                has_snapshot,
            });
        }
        let freshness = shared.freshness.get(session_id).copied();
        let state = match freshness {
            Some(SessionFreshness::Current) => SessionSyncState::Current,
            Some(SessionFreshness::SourceMissing) => SessionSyncState::SourceMissing,
            Some(SessionFreshness::Checking | SessionFreshness::Stale) => SessionSyncState::Queued,
            None if shared.catalog_ready => SessionSyncState::NotFound,
            None => SessionSyncState::Checking,
        };
        if matches!(state, SessionSyncState::Queued | SessionSyncState::Checking) {
            shared.sync_states.insert(session_id.to_owned(), state);
            if let Err(error) = self
                .commands
                .try_send(CoordinatorCommand::EnsureSession(session_id.to_owned()))
            {
                shared.sync_states.remove(session_id);
                return Err(anyhow!(
                    "direct synchronization queue is unavailable: {error}"
                ));
            }
        }
        Ok(SessionSyncStatus {
            session_id: session_id.to_owned(),
            state,
            has_snapshot,
        })
    }

    #[must_use]
    pub fn freshness(&self, session_id: &str) -> SessionFreshness {
        self.shared
            .read()
            .ok()
            .and_then(|shared| shared.freshness.get(session_id).copied())
            .unwrap_or(SessionFreshness::Checking)
    }
}

impl RuntimeScheduler {
    fn new(
        coordinator: &IndexCoordinator,
        updates: Option<mpsc::Sender<IndexUpdate>>,
        shutdown: CancellationToken,
        completion_sender: mpsc::Sender<WorkCompletion>,
    ) -> Self {
        Self {
            database: coordinator.database.clone(),
            writer: coordinator.writer.clone(),
            roots: coordinator.roots.clone(),
            max_event_bytes: coordinator.max_event_bytes,
            policy: coordinator.policy,
            gate: coordinator.io_gate.clone(),
            shared: Arc::clone(&coordinator.shared),
            updates,
            shutdown,
            queue: BinaryHeap::new(),
            queued: HashMap::new(),
            inflight: HashMap::new(),
            deferred: HashMap::new(),
            completion_sender,
            last_high_activity: Instant::now(),
            background_hold: None,
            cycle: None,
            relationships_dirty: false,
        }
    }

    async fn apply_full_discovery(
        &mut self,
        discovery: Discovery,
        generation: u64,
        now_micros: i64,
        foreground: bool,
    ) -> Result<()> {
        let stored = load_stored_sources(&self.database).await?;
        let stored_by_key = stored
            .iter()
            .cloned()
            .map(|source| (stored_key(source.root_kind, &source.relative_path), source))
            .collect::<HashMap<_, _>>();
        let snapshots = stored
            .iter()
            .filter_map(|source| source.session_id.clone())
            .collect::<HashSet<_>>();
        let discovered_keys = discovery
            .sources
            .iter()
            .map(source_key)
            .collect::<HashSet<_>>();
        let discovered_ids = discovery
            .sources
            .iter()
            .map(|source| source.session_id.clone())
            .collect::<HashSet<_>>();
        let mut missing = Vec::new();
        if discovery.issues.is_empty() {
            for source in &stored {
                if !discovered_keys.contains(&stored_key(source.root_kind, &source.relative_path))
                    && source.scan_state != "source_missing"
                {
                    missing.push((source.root_kind, source.relative_path.clone()));
                }
            }
        }
        let mut present = Vec::new();
        let mut freshness = HashMap::new();
        let mut catalog = HashMap::new();
        let mut pending_sessions = HashSet::new();
        let mut progress = IndexProgress {
            total_files: 0,
            processed_files: 0,
            total_bytes: 0,
            processed_bytes: 0,
            failed_files: 0,
            excluded_files: 0,
            excluded_bytes: 0,
        };
        for source in &discovery.sources {
            let source = Arc::new(source.clone());
            let stored_source = stored_by_key.get(&source_key(&source));
            let current = stored_source.is_some_and(|stored| metadata_matches(stored, &source));
            let has_snapshot = snapshots.contains(&source.session_id);
            let session_freshness = if current {
                SessionFreshness::Current
            } else if has_snapshot {
                SessionFreshness::Stale
            } else {
                SessionFreshness::Checking
            };
            freshness.insert(source.session_id.clone(), session_freshness);
            catalog.insert(source.session_id.clone(), Arc::clone(&source));
            if current && stored_source.is_some_and(|stored| stored.scan_state == "source_missing")
            {
                let stored = stored_source.expect("checked above");
                present.push((stored.root_kind, stored.relative_path.clone()));
            }
            match automatic_priority(self.policy, &source, now_micros) {
                Some(WorkPriority::Recent) => {
                    progress.total_files = progress.total_files.saturating_add(1);
                    progress.total_bytes = progress
                        .total_bytes
                        .saturating_add(source.source.size_bytes);
                    if current {
                        progress.processed_files = progress.processed_files.saturating_add(1);
                        progress.processed_bytes = progress
                            .processed_bytes
                            .saturating_add(source.source.size_bytes);
                    } else {
                        pending_sessions.insert(source.session_id.clone());
                        self.enqueue(WorkItem::new(
                            Arc::clone(&source),
                            WorkPriority::Recent,
                            Some(generation),
                        ));
                    }
                }
                Some(WorkPriority::Background) => {
                    progress.excluded_files = progress.excluded_files.saturating_add(1);
                    progress.excluded_bytes = progress
                        .excluded_bytes
                        .saturating_add(source.source.size_bytes);
                    if !current {
                        self.enqueue(WorkItem::new(
                            Arc::clone(&source),
                            WorkPriority::Background,
                            None,
                        ));
                    }
                }
                Some(WorkPriority::Interactive) => unreachable!(),
                None => {
                    progress.excluded_files = progress.excluded_files.saturating_add(1);
                    progress.excluded_bytes = progress
                        .excluded_bytes
                        .saturating_add(source.source.size_bytes);
                }
            }
        }
        for source in &stored {
            if let Some(session_id) = &source.session_id
                && !discovered_ids.contains(session_id)
                && !discovered_keys.contains(&stored_key(source.root_kind, &source.relative_path))
            {
                freshness.insert(session_id.clone(), SessionFreshness::SourceMissing);
            }
        }
        self.writer.mark_sources_missing(missing.clone()).await?;
        self.writer.mark_sources_present(present).await?;
        {
            let mut shared = self.shared.write().expect("index catalog lock poisoned");
            shared.catalog_ready = true;
            shared.catalog = catalog;
            shared.freshness = freshness;
            shared.snapshots = snapshots;
            shared.sync_states.retain(|session_id, _| {
                self.queued.contains_key(session_id) || self.inflight.contains_key(session_id)
            });
        }
        let report = ReconcileReport {
            generation,
            discovered_files: discovery.sources.len() as u64,
            discovered_bytes: discovery.total_bytes,
            removed_files: missing.len() as u64,
            discovery_issues: discovery.issues.len() as u64,
            excluded_files: progress.excluded_files,
            excluded_bytes: progress.excluded_bytes,
            reconcile_again: !discovery.issues.is_empty(),
            ..ReconcileReport::default()
        };
        if foreground {
            send_update(
                self.updates.as_ref(),
                IndexUpdate::Progress {
                    generation,
                    progress: progress.clone(),
                },
            )
            .await;
        }
        self.cycle = Some(ActiveCycle {
            report,
            progress,
            pending_sessions,
            foreground,
        });
        Ok(())
    }

    fn enqueue(&mut self, mut work: WorkItem) {
        let session_id = work.source.session_id.clone();
        if work.priority >= WorkPriority::Recent {
            self.last_high_activity = Instant::now();
            if self.background_hold.is_none() {
                self.background_hold = Some(self.gate.register(WorkPriority::Recent));
            }
        }
        if let Some(inflight) = self.inflight.get_mut(&session_id) {
            inflight.lease.promote(work.priority);
            if inflight.work.fingerprint == work.fingerprint {
                inflight.work.priority = inflight.work.priority.max(work.priority);
                inflight.work.cycle_generation =
                    inflight.work.cycle_generation.or(work.cycle_generation);
                return;
            }
            if let Some(existing) = self.deferred.get(&session_id) {
                work.priority = work.priority.max(existing.priority);
                work.cycle_generation = work.cycle_generation.or(existing.cycle_generation);
            }
            self.deferred.insert(session_id.clone(), work);
            self.set_sync_state(&session_id, SessionSyncState::Queued);
            return;
        }
        if let Some(existing) = self.queued.get(&session_id) {
            work.priority = work.priority.max(existing.priority);
            work.cycle_generation = work.cycle_generation.or(existing.cycle_generation);
            if work.fingerprint == existing.fingerprint && work.priority == existing.priority {
                return;
            }
        }
        self.queued.insert(session_id.clone(), work.clone());
        self.queue.push(work);
        self.set_sync_state(&session_id, SessionSyncState::Queued);
    }

    fn start_available(&mut self) {
        if self.last_high_activity.elapsed() >= BACKGROUND_IDLE_DELAY {
            self.background_hold = None;
        }
        loop {
            let high_count = self
                .inflight
                .values()
                .filter(|inflight| inflight.work.priority >= WorkPriority::Recent)
                .count();
            let interactive_active = self
                .inflight
                .values()
                .any(|inflight| inflight.work.priority == WorkPriority::Interactive);
            let Some(next) = self.peek_valid() else { break };
            if next.priority >= WorkPriority::Recent {
                let can_start = high_count < MAX_PARSER_TASKS
                    || (next.priority == WorkPriority::Interactive && !interactive_active);
                if !can_start {
                    break;
                }
                let work = self.pop_valid().expect("peeked valid work");
                self.start(work);
                continue;
            }
            let background_active = self
                .inflight
                .values()
                .any(|inflight| inflight.work.priority == WorkPriority::Background);
            if high_count == 0
                && !background_active
                && self.last_high_activity.elapsed() >= BACKGROUND_IDLE_DELAY
            {
                let work = self.pop_valid().expect("peeked valid work");
                self.start(work);
            }
            break;
        }
    }

    fn peek_valid(&mut self) -> Option<&WorkItem> {
        loop {
            let candidate = self.queue.peek()?;
            let valid = self
                .queued
                .get(&candidate.source.session_id)
                .is_some_and(|current| {
                    current.fingerprint == candidate.fingerprint
                        && current.priority == candidate.priority
                        && current.cycle_generation == candidate.cycle_generation
                });
            if valid {
                return self.queue.peek();
            }
            self.queue.pop();
        }
    }

    fn pop_valid(&mut self) -> Option<WorkItem> {
        self.peek_valid()?;
        let work = self.queue.pop()?;
        self.queued.remove(&work.source.session_id);
        Some(work)
    }

    fn start(&mut self, work: WorkItem) {
        let session_id = work.source.session_id.clone();
        let lease = self.gate.register(work.priority);
        self.inflight.insert(
            session_id.clone(),
            InflightWork {
                work: work.clone(),
                lease: lease.clone(),
            },
        );
        self.set_sync_state(&session_id, SessionSyncState::Indexing);
        let database = self.database.clone();
        let writer = self.writer.clone();
        let shutdown = self.shutdown.clone();
        let completion_sender = self.completion_sender.clone();
        let max_event_bytes = self.max_event_bytes;
        tokio::spawn(async move {
            let result = scan_source_with_lease(
                database,
                writer,
                work.source.as_ref().clone(),
                max_event_bytes,
                chrono::Utc::now().timestamp_micros(),
                shutdown,
                lease,
            )
            .await;
            let _ = completion_sender
                .send(WorkCompletion { work, result })
                .await;
        });
    }

    async fn handle_completion(&mut self, completion: WorkCompletion) -> Result<()> {
        let session_id = completion.work.source.session_id.clone();
        let actual = self
            .inflight
            .remove(&session_id)
            .map_or(completion.work, |inflight| inflight.work);
        if actual.priority >= WorkPriority::Recent {
            self.last_high_activity = Instant::now();
            if self.background_hold.is_none() {
                self.background_hold = Some(self.gate.register(WorkPriority::Recent));
            }
        }
        match completion.result {
            Ok(outcome) => {
                {
                    let mut shared = self.shared.write().expect("index catalog lock poisoned");
                    shared
                        .freshness
                        .insert(outcome.session_id.clone(), SessionFreshness::Current);
                    shared.snapshots.insert(outcome.session_id.clone());
                    shared.sync_states.remove(&outcome.session_id);
                }
                self.relationships_dirty = true;
                send_update(
                    self.updates.as_ref(),
                    IndexUpdate::SessionCommitted {
                        generation: self
                            .cycle
                            .as_ref()
                            .map_or(0, |cycle| cycle.report.generation),
                        session_id: outcome.session_id.clone(),
                    },
                )
                .await;
                if let Some(cycle) = self.cycle.as_mut()
                    && cycle.pending_sessions.remove(&session_id)
                {
                    cycle.progress.processed_files =
                        cycle.progress.processed_files.saturating_add(1);
                    cycle.progress.processed_bytes = cycle
                        .progress
                        .processed_bytes
                        .saturating_add(actual.source.source.size_bytes);
                    record_outcome(&mut cycle.report, &outcome);
                    if cycle.foreground {
                        send_update(
                            self.updates.as_ref(),
                            IndexUpdate::Progress {
                                generation: cycle.report.generation,
                                progress: cycle.progress.clone(),
                            },
                        )
                        .await;
                    }
                }
                if outcome.changed_during_scan {
                    self.rediscover_and_enqueue(&actual.source.path, WorkPriority::Recent)
                        .await?;
                }
            }
            Err(error) if !self.shutdown.is_cancelled() => {
                let has_snapshot = self
                    .shared
                    .read()
                    .expect("index catalog lock poisoned")
                    .snapshots
                    .contains(&session_id);
                {
                    let mut shared = self.shared.write().expect("index catalog lock poisoned");
                    shared.freshness.insert(
                        session_id.clone(),
                        if has_snapshot {
                            SessionFreshness::Stale
                        } else {
                            SessionFreshness::Checking
                        },
                    );
                    shared.sync_states.remove(&session_id);
                }
                if let Some(cycle) = self.cycle.as_mut()
                    && cycle.pending_sessions.remove(&session_id)
                {
                    cycle.progress.processed_files =
                        cycle.progress.processed_files.saturating_add(1);
                    cycle.progress.processed_bytes = cycle
                        .progress
                        .processed_bytes
                        .saturating_add(actual.source.source.size_bytes);
                    cycle.progress.failed_files = cycle.progress.failed_files.saturating_add(1);
                    cycle.report.failed_files = cycle.report.failed_files.saturating_add(1);
                    cycle.report.reconcile_again = true;
                    cycle.report.failures.push(format!("{error:#}"));
                }
            }
            Err(_) => {}
        }
        if let Some(deferred) = self.deferred.remove(&session_id) {
            self.enqueue(deferred);
        }
        Ok(())
    }

    async fn ensure_session(&mut self, session_id: &str) {
        let source = self
            .shared
            .read()
            .expect("index catalog lock poisoned")
            .catalog
            .get(session_id)
            .cloned();
        if let Some(source) = source {
            let freshness = self
                .shared
                .read()
                .expect("index catalog lock poisoned")
                .freshness
                .get(session_id)
                .copied();
            if freshness == Some(SessionFreshness::Current) {
                self.clear_sync_state(session_id);
                self.publish_session_state(session_id, SessionSyncState::Current)
                    .await;
            } else {
                self.enqueue(WorkItem::new(source, WorkPriority::Interactive, None));
            }
            return;
        }
        let state = if self
            .shared
            .read()
            .expect("index catalog lock poisoned")
            .freshness
            .get(session_id)
            == Some(&SessionFreshness::SourceMissing)
        {
            SessionSyncState::SourceMissing
        } else {
            SessionSyncState::NotFound
        };
        self.clear_sync_state(session_id);
        self.publish_session_state(session_id, state).await;
    }

    async fn handle_paths(&mut self, paths: Vec<PathBuf>, generation: u64) -> Result<()> {
        let mut unique = paths
            .into_iter()
            .collect::<HashSet<_>>()
            .into_iter()
            .collect::<Vec<_>>();
        unique.sort();
        let mut rediscovered = Vec::new();
        let mut deleted = Vec::new();
        for path in unique {
            if path.is_dir() {
                let roots = self.roots.clone();
                let directory = path.clone();
                let sources = tokio::task::spawn_blocking(move || {
                    walkdir::WalkDir::new(directory)
                        .follow_links(false)
                        .into_iter()
                        .filter_map(Result::ok)
                        .filter(|entry| entry.file_type().is_file())
                        .filter_map(|entry| {
                            discover_source_path(&roots, entry.path(), generation)
                                .ok()
                                .flatten()
                        })
                        .collect::<Vec<_>>()
                })
                .await
                .context("targeted directory discovery task panicked")?;
                rediscovered.extend(sources);
                continue;
            }
            if path.extension().and_then(|value| value.to_str()) != Some("jsonl") {
                if !path.exists() {
                    deleted.push(path);
                }
                continue;
            }
            if path.is_file() {
                let roots = self.roots.clone();
                let source_path = path.clone();
                match tokio::task::spawn_blocking(move || {
                    discover_source_path(&roots, &source_path, generation)
                })
                .await
                .context("targeted source discovery task panicked")?
                {
                    Ok(Some(source)) => rediscovered.push(source),
                    Ok(None) => {}
                    Err(_) => {
                        // A racing append or rename will produce another event. The six-hour
                        // safety sweep remains the bounded fallback.
                    }
                }
            } else {
                deleted.push(path);
            }
        }
        let rediscovered_ids = rediscovered
            .iter()
            .map(|source| source.session_id.clone())
            .collect::<HashSet<_>>();
        for source in rediscovered {
            let source = Arc::new(source);
            let existing = self
                .shared
                .read()
                .expect("index catalog lock poisoned")
                .catalog
                .get(&source.session_id)
                .cloned();
            if existing
                .as_ref()
                .is_some_and(|current| !source_precedes(&source, current))
                && existing
                    .as_ref()
                    .is_some_and(|current| current.path != source.path)
            {
                continue;
            }
            let stored = load_stored_source(&self.database, &source).await?;
            let current = stored
                .as_ref()
                .is_some_and(|stored| metadata_matches(stored, &source));
            let has_snapshot = self
                .shared
                .read()
                .expect("index catalog lock poisoned")
                .snapshots
                .contains(&source.session_id);
            {
                let mut shared = self.shared.write().expect("index catalog lock poisoned");
                shared
                    .catalog
                    .insert(source.session_id.clone(), Arc::clone(&source));
                shared.freshness.insert(
                    source.session_id.clone(),
                    if current {
                        SessionFreshness::Current
                    } else if has_snapshot {
                        SessionFreshness::Stale
                    } else {
                        SessionFreshness::Checking
                    },
                );
            }
            if current {
                if let Some(stored) = stored
                    && stored.scan_state == "source_missing"
                {
                    self.writer
                        .mark_source_present(stored.root_kind, stored.relative_path)
                        .await?;
                }
            } else {
                self.enqueue(WorkItem::new(source, WorkPriority::Recent, None));
            }
        }
        let mut mark_missing = Vec::new();
        for path in deleted {
            if path.extension().and_then(|value| value.to_str()) == Some("jsonl")
                && let Some(source) = source_coordinates_for_path(&self.roots, &path)
            {
                mark_missing.push(source);
            }
            let matches = self
                .shared
                .read()
                .expect("index catalog lock poisoned")
                .catalog
                .iter()
                .filter(|(_, source)| source.path == path || source.path.starts_with(&path))
                .map(|(session_id, source)| (session_id.clone(), source.clone()))
                .collect::<Vec<_>>();
            for (session_id, source) in matches {
                if rediscovered_ids.contains(&session_id) {
                    continue;
                }
                {
                    let mut shared = self.shared.write().expect("index catalog lock poisoned");
                    shared.catalog.remove(&session_id);
                    if shared.snapshots.contains(&session_id) {
                        shared
                            .freshness
                            .insert(session_id.clone(), SessionFreshness::SourceMissing);
                    }
                }
                mark_missing.push((source.source.root_kind, source.source.relative_path.clone()));
            }
        }
        mark_missing.sort_by(|left, right| {
            root_kind_value(left.0)
                .cmp(root_kind_value(right.0))
                .then_with(|| left.1.cmp(&right.1))
        });
        mark_missing.dedup();
        self.writer.mark_sources_missing(mark_missing).await?;
        self.start_available();
        Ok(())
    }

    async fn rediscover_and_enqueue(&mut self, path: &Path, priority: WorkPriority) -> Result<()> {
        let roots = self.roots.clone();
        let path = path.to_path_buf();
        let generation = self
            .cycle
            .as_ref()
            .map_or(0, |cycle| cycle.report.generation);
        if let Some(source) =
            tokio::task::spawn_blocking(move || discover_source_path(&roots, &path, generation))
                .await
                .context("targeted source rediscovery task panicked")??
        {
            self.enqueue(WorkItem::new(Arc::new(source), priority, None));
        }
        Ok(())
    }

    async fn maybe_finalize_cycle(&mut self, bootstrap_pending: &mut bool) -> Result<()> {
        if self
            .cycle
            .as_ref()
            .is_none_or(|cycle| !cycle.pending_sessions.is_empty())
        {
            return Ok(());
        }
        if self.relationships_dirty {
            self.flush_relationships().await?;
        }
        let mut cycle = self.cycle.take().expect("checked above");
        cycle.report.updated_sessions.sort();
        cycle.report.updated_sessions.dedup();
        if *bootstrap_pending && report_is_healthy(&cycle.report) {
            self.database.mark_bootstrap_complete().await?;
            *bootstrap_pending = false;
        }
        send_update(
            self.updates.as_ref(),
            IndexUpdate::Completed {
                report: cycle.report,
                foreground: cycle.foreground,
            },
        )
        .await;
        Ok(())
    }

    async fn flush_relationships_if_idle(&mut self) -> Result<()> {
        if self.relationships_dirty
            && self.queue.is_empty()
            && self.inflight.is_empty()
            && self.deferred.is_empty()
        {
            self.flush_relationships().await?;
        }
        Ok(())
    }

    async fn flush_relationships(&mut self) -> Result<()> {
        let updates = reconcile_plan_handoffs(&self.database).await?;
        let generation = self
            .cycle
            .as_ref()
            .map_or(0, |cycle| cycle.report.generation);
        for session_id in updates {
            if let Some(cycle) = self.cycle.as_mut() {
                cycle.report.updated_sessions.push(session_id.clone());
            }
            send_update(
                self.updates.as_ref(),
                IndexUpdate::SessionCommitted {
                    generation,
                    session_id,
                },
            )
            .await;
        }
        self.relationships_dirty = false;
        Ok(())
    }

    fn set_sync_state(&self, session_id: &str, state: SessionSyncState) {
        self.shared
            .write()
            .expect("index catalog lock poisoned")
            .sync_states
            .insert(session_id.to_owned(), state);
    }

    fn clear_sync_state(&self, session_id: &str) {
        self.shared
            .write()
            .expect("index catalog lock poisoned")
            .sync_states
            .remove(session_id);
    }

    async fn publish_session_state(&self, session_id: &str, state: SessionSyncState) {
        send_update(
            self.updates.as_ref(),
            IndexUpdate::SessionState {
                generation: self
                    .cycle
                    .as_ref()
                    .map_or(0, |cycle| cycle.report.generation),
                session_id: session_id.to_owned(),
                state,
            },
        )
        .await;
    }
}

fn automatic_priority(
    policy: InitialIndexPolicy,
    source: &DiscoveredSource,
    now_micros: i64,
) -> Option<WorkPriority> {
    if policy.is_recent(source.source.mtime_ns / 1_000, now_micros) {
        Some(WorkPriority::Recent)
    } else if policy.background_enabled() {
        Some(WorkPriority::Background)
    } else {
        None
    }
}

fn record_outcome(report: &mut ReconcileReport, outcome: &super::scanner::ScanOutcome) {
    report.indexed_files = report.indexed_files.saturating_add(1);
    report.appended_files = report
        .appended_files
        .saturating_add(u64::from(outcome.appended));
    report.reconcile_again |= outcome.changed_during_scan;
    if outcome.appended {
        report.appended_sessions.push(outcome.session_id.clone());
    }
    report.updated_sessions.push(outcome.session_id.clone());
}

fn source_fingerprint(source: &DiscoveredSource) -> SourceFingerprint {
    SourceFingerprint {
        root_kind: root_kind_value(source.source.root_kind),
        relative_path: source.source.relative_path.clone(),
        file_key: source.source.file_key.clone(),
        size_bytes: source.source.size_bytes,
        mtime_ns: source.source.mtime_ns,
    }
}

fn source_key(source: &DiscoveredSource) -> String {
    stored_key(source.source.root_kind, &source.source.relative_path)
}

fn stored_key(root_kind: RootKind, relative_path: &str) -> String {
    format!("{}\0{relative_path}", root_kind_value(root_kind))
}

fn root_kind_value(root_kind: RootKind) -> &'static str {
    match root_kind {
        RootKind::Active => "active",
        RootKind::Archived => "archived",
    }
}

fn source_coordinates_for_path(roots: &SourceRoots, path: &Path) -> Option<(RootKind, String)> {
    for (root_kind, root) in [
        (RootKind::Active, roots.active.as_ref()),
        (RootKind::Archived, roots.archived.as_ref()),
    ] {
        let Some(root) = root else { continue };
        let Ok(relative) = path.strip_prefix(root) else {
            continue;
        };
        let relative = relative
            .components()
            .map(|component| component.as_os_str().to_string_lossy())
            .collect::<Vec<_>>()
            .join("/");
        return Some((root_kind, relative));
    }
    None
}

fn metadata_matches(stored: &StoredSource, source: &DiscoveredSource) -> bool {
    stored.file_key == source.source.file_key
        && stored.size_bytes == source.source.size_bytes
        && stored.mtime_ns == source.source.mtime_ns
        && matches!(stored.scan_state.as_str(), "ready" | "source_missing")
        && stored.session_id.as_deref() == Some(source.session_id.as_str())
}

async fn load_stored_sources(database: &Database) -> Result<Vec<StoredSource>> {
    let rows = sqlx::query(
        "SELECT root_kind, relative_path, file_key, size_bytes, mtime_ns, scan_state, session_id \
         FROM source_files",
    )
    .fetch_all(database.pool())
    .await?;
    rows.iter().map(stored_source_from_row).collect()
}

async fn load_stored_source(
    database: &Database,
    source: &DiscoveredSource,
) -> Result<Option<StoredSource>> {
    let row = sqlx::query(
        "SELECT root_kind, relative_path, file_key, size_bytes, mtime_ns, scan_state, session_id \
         FROM source_files WHERE root_kind = ? AND relative_path = ?",
    )
    .bind(root_kind_value(source.source.root_kind))
    .bind(&source.source.relative_path)
    .fetch_optional(database.pool())
    .await?;
    row.as_ref().map(stored_source_from_row).transpose()
}

fn stored_source_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<StoredSource> {
    let root_kind = match row.get::<String, _>("root_kind").as_str() {
        "active" => RootKind::Active,
        "archived" => RootKind::Archived,
        value => return Err(anyhow!("invalid stored root kind: {value}")),
    };
    Ok(StoredSource {
        root_kind,
        relative_path: row.get("relative_path"),
        file_key: row.get("file_key"),
        size_bytes: u64::try_from(row.get::<i64, _>("size_bytes"))?,
        mtime_ns: row.get("mtime_ns"),
        scan_state: row.get("scan_state"),
        session_id: row.get("session_id"),
    })
}

fn report_is_healthy(report: &ReconcileReport) -> bool {
    report.failed_files == 0 && report.discovery_issues == 0 && !report.reconcile_again
}

async fn send_update(sender: Option<&mpsc::Sender<IndexUpdate>>, update: IndexUpdate) {
    if let Some(sender) = sender {
        let _ = sender.send(update).await;
    }
}

async fn send_session_committed(
    sender: Option<&mpsc::Sender<IndexUpdate>>,
    notified: &mut HashSet<String>,
    generation: u64,
    session_id: &str,
) {
    if notified.insert(session_id.to_owned()) {
        send_update(
            sender,
            IndexUpdate::SessionCommitted {
                generation,
                session_id: session_id.to_owned(),
            },
        )
        .await;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::index::writer::SourceFileRecord;

    fn source(id: &str, mtime_ns: i64, created_at_micros: i64) -> DiscoveredSource {
        DiscoveredSource {
            root: PathBuf::from("/source"),
            path: PathBuf::from(format!("/source/{id}.jsonl")),
            session_id: id.into(),
            created_at_micros,
            source: SourceFileRecord {
                root_kind: RootKind::Active,
                relative_path: format!("{id}.jsonl"),
                file_key: id.into(),
                size_bytes: 1,
                mtime_ns,
                head_hash: None,
                tail_hash: None,
                generation: 1,
                placeholder: None,
            },
            duplicate_paths: Vec::new(),
        }
    }

    #[test]
    fn work_order_is_priority_then_mtime_then_creation() {
        let mut queue = BinaryHeap::new();
        queue.push(WorkItem::new(
            Arc::new(source("recent-old", 20, 1)),
            WorkPriority::Recent,
            None,
        ));
        queue.push(WorkItem::new(
            Arc::new(source("recent-new", 20, 2)),
            WorkPriority::Recent,
            None,
        ));
        queue.push(WorkItem::new(
            Arc::new(source("direct", 1, 1)),
            WorkPriority::Interactive,
            None,
        ));
        queue.push(WorkItem::new(
            Arc::new(source("background", 100, 100)),
            WorkPriority::Background,
            None,
        ));
        assert_eq!(queue.pop().unwrap().source.session_id, "direct");
        assert_eq!(queue.pop().unwrap().source.session_id, "recent-new");
        assert_eq!(queue.pop().unwrap().source.session_id, "recent-old");
        assert_eq!(queue.pop().unwrap().source.session_id, "background");
    }

    #[test]
    fn rolling_window_defers_old_history_but_zero_disables_backfill() {
        const DAY_MICROS: i64 = 86_400_000_000;
        let now = 20 * DAY_MICROS;
        let mut recent = source("recent", (19 * DAY_MICROS) * 1_000, 1);
        let old = source("old", (10 * DAY_MICROS) * 1_000, 2);
        assert_eq!(
            automatic_priority(InitialIndexPolicy::new(7, now).unwrap(), &recent, now),
            Some(WorkPriority::Recent)
        );
        assert_eq!(
            automatic_priority(InitialIndexPolicy::new(7, now).unwrap(), &old, now),
            Some(WorkPriority::Background)
        );
        assert_eq!(
            automatic_priority(InitialIndexPolicy::new(0, now).unwrap(), &old, now),
            None
        );
        recent.source.mtime_ns = i64::MIN;
        assert_eq!(
            automatic_priority(InitialIndexPolicy::all(), &recent, now),
            Some(WorkPriority::Recent)
        );
    }

    #[test]
    fn repeated_direct_sync_is_singleflight_before_the_scheduler_receives_it() {
        let (commands, mut receiver) = mpsc::channel(1);
        let handle = CoordinatorHandle {
            shared: Arc::new(RwLock::new(SharedState::default())),
            commands,
        };

        let first = handle.ensure_session("session").unwrap();
        let repeated = handle.ensure_session("session").unwrap();

        assert_eq!(first.state, SessionSyncState::Checking);
        assert_eq!(repeated.state, SessionSyncState::Checking);
        assert!(matches!(
            receiver.try_recv(),
            Ok(CoordinatorCommand::EnsureSession(session_id)) if session_id == "session"
        ));
        assert!(receiver.try_recv().is_err());
    }
}
