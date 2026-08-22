use std::collections::HashMap;
use std::io;
use std::sync::atomic::{AtomicU8, AtomicU64, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

use tokio_util::sync::CancellationToken;

pub const MAX_CONCURRENT_SOURCE_READS: usize = 2;
pub const MAX_READ_CHUNK_BYTES: usize = 64 * 1024;
pub const BACKGROUND_BYTES_PER_SECOND: usize = 8 * 1024 * 1024;

#[derive(Debug, thiserror::Error)]
pub enum IoGateError {
    #[error("{context}: I/O gate mutex poisoned")]
    LockPoisoned { context: &'static str },
}

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
#[repr(u8)]
pub enum WorkPriority {
    Background = 1,
    Recent = 2,
    Interactive = 3,
}

impl WorkPriority {
    fn from_u8(value: u8) -> Self {
        match value {
            3 => Self::Interactive,
            2 => Self::Recent,
            _ => Self::Background,
        }
    }
}

#[derive(Clone)]
pub struct IoGate {
    inner: Arc<GateInner>,
}

struct GateInner {
    next_id: AtomicU64,
    bytes_read: AtomicU64,
    state: Mutex<GateState>,
    changed: Condvar,
}

struct GateState {
    tasks: HashMap<u64, WorkPriority>,
    active_io: usize,
    background_tokens: f64,
    last_refill: Instant,
}

#[derive(Clone)]
pub struct ScanLease {
    inner: Arc<LeaseInner>,
}

struct LeaseInner {
    id: u64,
    priority: AtomicU8,
    gate: Arc<GateInner>,
}

pub struct IoPermit {
    gate: Arc<GateInner>,
    max_bytes: usize,
}

impl Default for IoGate {
    fn default() -> Self {
        Self::new()
    }
}

impl IoGate {
    #[must_use]
    pub fn new() -> Self {
        Self {
            inner: Arc::new(GateInner {
                next_id: AtomicU64::new(0),
                bytes_read: AtomicU64::new(0),
                state: Mutex::new(GateState {
                    tasks: HashMap::new(),
                    active_io: 0,
                    background_tokens: MAX_READ_CHUNK_BYTES as f64,
                    last_refill: Instant::now(),
                }),
                changed: Condvar::new(),
            }),
        }
    }

    pub fn register(&self, priority: WorkPriority) -> Result<ScanLease, IoGateError> {
        let mut state = self
            .inner
            .state
            .lock()
            .map_err(|_| IoGateError::LockPoisoned {
                context: "registering an index scan",
            })?;
        let id = self.inner.next_id.fetch_add(1, Ordering::Relaxed) + 1;
        state.tasks.insert(id, priority);
        drop(state);
        self.inner.changed.notify_all();
        Ok(ScanLease {
            inner: Arc::new(LeaseInner {
                id,
                priority: AtomicU8::new(priority as u8),
                gate: Arc::clone(&self.inner),
            }),
        })
    }

    #[must_use]
    pub fn bytes_read(&self) -> u64 {
        self.inner.bytes_read.load(Ordering::Acquire)
    }
}

impl ScanLease {
    #[must_use]
    pub fn priority(&self) -> WorkPriority {
        WorkPriority::from_u8(self.inner.priority.load(Ordering::Acquire))
    }

    pub fn promote(&self, priority: WorkPriority) -> Result<(), IoGateError> {
        let mut state = self
            .inner
            .gate
            .state
            .lock()
            .map_err(|_| IoGateError::LockPoisoned {
                context: "promoting an index scan",
            })?;
        let previous = self
            .inner
            .priority
            .fetch_max(priority as u8, Ordering::AcqRel);
        if previous >= priority as u8 {
            return Ok(());
        }
        state.tasks.insert(self.inner.id, priority);
        drop(state);
        self.inner.gate.changed.notify_all();
        Ok(())
    }

    pub fn before_io(
        &self,
        requested_bytes: usize,
        shutdown: &CancellationToken,
    ) -> io::Result<IoPermit> {
        let requested_bytes = requested_bytes.clamp(1, MAX_READ_CHUNK_BYTES);
        let mut state = self.inner.gate.state.lock().map_err(|_| {
            io::Error::other(IoGateError::LockPoisoned {
                context: "acquiring an index I/O permit",
            })
        })?;
        loop {
            if shutdown.is_cancelled() {
                return Err(io::Error::new(
                    io::ErrorKind::Interrupted,
                    "index scan cancelled",
                ));
            }
            refill_background_tokens(&mut state);
            let priority = self.priority();
            let higher_priority_waiting =
                state.tasks.values().any(|candidate| *candidate > priority);
            let concurrency_available = state.active_io < MAX_CONCURRENT_SOURCE_READS;
            let available = if priority == WorkPriority::Background {
                state.background_tokens.floor() as usize
            } else {
                requested_bytes
            };
            if !higher_priority_waiting && concurrency_available && available > 0 {
                let max_bytes = requested_bytes.min(available);
                if priority == WorkPriority::Background {
                    state.background_tokens -= max_bytes as f64;
                }
                state.active_io += 1;
                return Ok(IoPermit {
                    gate: Arc::clone(&self.inner.gate),
                    max_bytes,
                });
            }
            let (next, _) = self
                .inner
                .gate
                .changed
                .wait_timeout(state, Duration::from_millis(10))
                .map_err(|_| {
                    io::Error::other(IoGateError::LockPoisoned {
                        context: "waiting for an index I/O permit",
                    })
                })?;
            state = next;
        }
    }

    pub fn record_read(&self, bytes: usize) {
        self.inner
            .gate
            .bytes_read
            .fetch_add(bytes as u64, Ordering::Relaxed);
    }
}

impl IoPermit {
    #[must_use]
    pub fn max_bytes(&self) -> usize {
        self.max_bytes
    }
}

impl Drop for IoPermit {
    fn drop(&mut self) {
        let Ok(mut state) = self.gate.state.lock() else {
            let error = IoGateError::LockPoisoned {
                context: "releasing an index I/O permit",
            };
            tracing::error!(%error, "index I/O permit cleanup failed");
            return;
        };
        state.active_io = state.active_io.saturating_sub(1);
        drop(state);
        self.gate.changed.notify_all();
    }
}

impl Drop for LeaseInner {
    fn drop(&mut self) {
        let Ok(mut state) = self.gate.state.lock() else {
            let error = IoGateError::LockPoisoned {
                context: "unregistering an index scan",
            };
            tracing::error!(%error, "index scan cleanup failed");
            return;
        };
        state.tasks.remove(&self.id);
        drop(state);
        self.gate.changed.notify_all();
    }
}

fn refill_background_tokens(state: &mut GateState) {
    let now = Instant::now();
    let elapsed = now.duration_since(state.last_refill).as_secs_f64();
    state.last_refill = now;
    state.background_tokens = (state.background_tokens
        + elapsed * BACKGROUND_BYTES_PER_SECOND as f64)
        .min(MAX_READ_CHUNK_BYTES as f64);
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn promotion_is_monotonic() {
        let lease = IoGate::new().register(WorkPriority::Background).unwrap();
        lease.promote(WorkPriority::Recent).unwrap();
        lease.promote(WorkPriority::Background).unwrap();
        assert_eq!(lease.priority(), WorkPriority::Recent);
        lease.promote(WorkPriority::Interactive).unwrap();
        assert_eq!(lease.priority(), WorkPriority::Interactive);
    }

    #[test]
    fn higher_priority_work_preempts_at_a_permit_boundary() {
        let gate = IoGate::new();
        let background = gate.register(WorkPriority::Background).unwrap();
        let first = background
            .before_io(MAX_READ_CHUNK_BYTES, &CancellationToken::new())
            .unwrap();
        let interactive = gate.register(WorkPriority::Interactive).unwrap();
        drop(first);
        let permit = interactive
            .before_io(MAX_READ_CHUNK_BYTES, &CancellationToken::new())
            .unwrap();
        assert_eq!(permit.max_bytes(), MAX_READ_CHUNK_BYTES);
    }

    #[test]
    fn poisoned_gate_returns_a_typed_error_and_cleanup_does_not_panic() {
        let gate = IoGate::new();
        let lease = gate.register(WorkPriority::Recent).unwrap();
        let inner = Arc::clone(&gate.inner);
        let injected = std::thread::spawn(move || {
            let Ok(_guard) = inner.state.lock() else {
                panic!("I/O gate was already poisoned before fault injection");
            };
            panic!("injected I/O gate lock poison");
        })
        .join();
        assert!(injected.is_err(), "fault injection must poison the lock");

        let next_id = gate.inner.next_id.load(Ordering::Relaxed);
        assert!(matches!(
            gate.register(WorkPriority::Recent),
            Err(IoGateError::LockPoisoned {
                context: "registering an index scan"
            })
        ));
        assert_eq!(gate.inner.next_id.load(Ordering::Relaxed), next_id);
        assert!(matches!(
            lease.promote(WorkPriority::Interactive),
            Err(IoGateError::LockPoisoned {
                context: "promoting an index scan"
            })
        ));
        assert_eq!(lease.priority(), WorkPriority::Recent);
        assert!(std::panic::catch_unwind(|| drop(lease)).is_ok());
    }
}
