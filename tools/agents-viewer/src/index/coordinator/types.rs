use super::*;

pub const MAX_PARSER_TASKS: usize = 2;
pub const DIRECT_SYNC_QUEUE_CAPACITY: usize = 128;
pub const FULL_SWEEP_INTERVAL: Duration = Duration::from_secs(6 * 60 * 60);
pub const BACKGROUND_IDLE_DELAY: Duration = Duration::from_secs(30);
pub const HOT_REFRESH_INTERVAL: Duration = Duration::from_secs(30);

pub(super) const SCHEDULER_TICK: Duration = Duration::from_millis(100);
pub(super) const FULL_SWEEP_BACKOFF: [Duration; 4] = [
    Duration::from_secs(60),
    Duration::from_secs(5 * 60),
    Duration::from_secs(30 * 60),
    FULL_SWEEP_INTERVAL,
];

#[derive(Clone)]
pub struct IndexCoordinator {
    pub(super) database: Database,
    pub(super) writer: WriterHandle,
    pub(super) roots: SourceRoots,
    pub(super) max_event_bytes: usize,
    pub(super) policy: InitialIndexPolicy,
    pub(super) generation: Arc<AtomicU64>,
    pub(super) io_gate: IoGate,
    pub(super) shared: Arc<RwLock<SharedState>>,
    pub(super) commands: mpsc::Sender<CoordinatorCommand>,
    pub(super) command_receiver: Arc<Mutex<Option<mpsc::Receiver<CoordinatorCommand>>>>,
}

#[derive(Clone)]
pub struct CoordinatorHandle {
    pub(super) shared: Arc<RwLock<SharedState>>,
    pub(super) commands: mpsc::Sender<CoordinatorCommand>,
}

#[derive(Debug, thiserror::Error)]
pub enum CoordinatorError {
    #[error("{context}: index catalog lock poisoned")]
    LockPoisoned { context: &'static str },

    #[error("direct synchronization queue is unavailable: {reason}")]
    QueueUnavailable { reason: String },
}

impl CoordinatorError {
    #[must_use]
    pub fn is_internal(&self) -> bool {
        matches!(self, Self::LockPoisoned { .. })
    }
}

#[derive(Default)]
pub(super) struct SharedState {
    pub(super) catalog_ready: bool,
    pub(super) catalog: HashMap<String, Arc<DiscoveredSource>>,
    pub(super) freshness: HashMap<String, SessionFreshness>,
    pub(super) snapshots: HashSet<String>,
    pub(super) sync_states: HashMap<String, SessionSyncState>,
}

pub(super) enum CoordinatorCommand {
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
pub(super) struct SourceFingerprint {
    pub(super) root_kind: &'static str,
    pub(super) relative_path: String,
    pub(super) file_key: String,
    pub(super) size_bytes: u64,
    pub(super) mtime_ns: i64,
}

#[derive(Clone, Debug)]
pub(super) struct WorkItem {
    pub(super) source: Arc<DiscoveredSource>,
    pub(super) fingerprint: SourceFingerprint,
    pub(super) priority: WorkPriority,
    pub(super) cycle_generation: Option<u64>,
}

impl WorkItem {
    pub(super) fn new(
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

pub(super) struct InflightWork {
    pub(super) work: WorkItem,
    pub(super) lease: ScanLease,
}

pub(super) struct WorkCompletion {
    pub(super) work: WorkItem,
    pub(super) result: Result<crate::index::scanner::ScanOutcome>,
}

pub(super) struct ActiveCycle {
    pub(super) report: ReconcileReport,
    pub(super) progress: IndexProgress,
    pub(super) pending_sessions: HashSet<String>,
    pub(super) foreground: bool,
}

#[derive(Clone, Debug)]
pub(super) struct StoredSource {
    pub(super) root_kind: RootKind,
    pub(super) relative_path: String,
    pub(super) file_key: String,
    pub(super) size_bytes: u64,
    pub(super) mtime_ns: i64,
    pub(super) scan_state: String,
    pub(super) session_id: Option<String>,
}

pub(super) struct RuntimeScheduler {
    pub(super) database: Database,
    pub(super) writer: WriterHandle,
    pub(super) roots: SourceRoots,
    pub(super) max_event_bytes: usize,
    pub(super) policy: InitialIndexPolicy,
    pub(super) gate: IoGate,
    pub(super) shared: Arc<RwLock<SharedState>>,
    pub(super) updates: Option<mpsc::Sender<IndexUpdate>>,
    pub(super) shutdown: CancellationToken,
    pub(super) queue: BinaryHeap<WorkItem>,
    pub(super) queued: HashMap<String, WorkItem>,
    pub(super) inflight: HashMap<String, InflightWork>,
    pub(super) deferred: HashMap<String, WorkItem>,
    pub(super) hot_sessions: HashSet<String>,
    pub(super) completion_sender: mpsc::Sender<WorkCompletion>,
    pub(super) last_high_activity: Instant,
    pub(super) background_hold: Option<ScanLease>,
    pub(super) cycle: Option<ActiveCycle>,
    pub(super) relationships_dirty: bool,
}

impl RuntimeScheduler {
    pub(super) fn read_shared(
        &self,
        context: &'static str,
    ) -> std::result::Result<RwLockReadGuard<'_, SharedState>, CoordinatorError> {
        self.shared
            .read()
            .map_err(|_| CoordinatorError::LockPoisoned { context })
    }

    pub(super) fn write_shared(
        &self,
        context: &'static str,
    ) -> std::result::Result<RwLockWriteGuard<'_, SharedState>, CoordinatorError> {
        self.shared
            .write()
            .map_err(|_| CoordinatorError::LockPoisoned { context })
    }
}
