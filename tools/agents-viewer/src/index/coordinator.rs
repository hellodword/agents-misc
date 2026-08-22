use std::cmp::Ordering as CmpOrdering;
use std::collections::{BinaryHeap, HashMap, HashSet};
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicU64, Ordering};
use std::sync::{Arc, RwLock, RwLockReadGuard, RwLockWriteGuard};
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

mod completion;
mod coordinator_impl;
mod directory;
mod scheduler;
#[cfg(test)]
mod tests;
mod types;

use directory::*;
use types::{
    ActiveCycle, CoordinatorCommand, FULL_SWEEP_BACKOFF, InflightWork, RuntimeScheduler,
    SCHEDULER_TICK, SharedState, SourceFingerprint, StoredSource, WorkCompletion, WorkItem,
};
pub use types::{
    BACKGROUND_IDLE_DELAY, CoordinatorError, CoordinatorHandle, DIRECT_SYNC_QUEUE_CAPACITY,
    FULL_SWEEP_INTERVAL, HOT_REFRESH_INTERVAL, IndexCoordinator, IndexUpdate, MAX_PARSER_TASKS,
    ReconcileReport,
};
