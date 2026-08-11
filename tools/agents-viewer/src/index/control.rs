use std::collections::HashMap;
use std::io;
use std::sync::atomic::{AtomicU8, AtomicU64, Ordering};
use std::sync::{Arc, Condvar, Mutex};
use std::time::{Duration, Instant};

use tokio_util::sync::CancellationToken;

pub const MAX_CONCURRENT_SOURCE_READS: usize = 2;
pub const MAX_READ_CHUNK_BYTES: usize = 64 * 1024;
pub const BACKGROUND_BYTES_PER_SECOND: usize = 8 * 1024 * 1024;

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

    #[must_use]
    pub fn register(&self, priority: WorkPriority) -> ScanLease {
        let id = self.inner.next_id.fetch_add(1, Ordering::Relaxed) + 1;
        self.inner
            .state
            .lock()
            .expect("I/O gate mutex poisoned")
            .tasks
            .insert(id, priority);
        self.inner.changed.notify_all();
        ScanLease {
            inner: Arc::new(LeaseInner {
                id,
                priority: AtomicU8::new(priority as u8),
                gate: Arc::clone(&self.inner),
            }),
        }
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

    pub fn promote(&self, priority: WorkPriority) {
        let previous = self
            .inner
            .priority
            .fetch_max(priority as u8, Ordering::AcqRel);
        if previous >= priority as u8 {
            return;
        }
        let mut state = self
            .inner
            .gate
            .state
            .lock()
            .expect("I/O gate mutex poisoned");
        state.tasks.insert(self.inner.id, priority);
        drop(state);
        self.inner.gate.changed.notify_all();
    }

    pub fn before_io(
        &self,
        requested_bytes: usize,
        shutdown: &CancellationToken,
    ) -> io::Result<IoPermit> {
        let requested_bytes = requested_bytes.clamp(1, MAX_READ_CHUNK_BYTES);
        let mut state = self
            .inner
            .gate
            .state
            .lock()
            .expect("I/O gate mutex poisoned");
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
                .expect("I/O gate mutex poisoned");
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
        let mut state = self.gate.state.lock().expect("I/O gate mutex poisoned");
        state.active_io = state.active_io.saturating_sub(1);
        drop(state);
        self.gate.changed.notify_all();
    }
}

impl Drop for LeaseInner {
    fn drop(&mut self) {
        let mut state = self.gate.state.lock().expect("I/O gate mutex poisoned");
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
        let lease = IoGate::new().register(WorkPriority::Background);
        lease.promote(WorkPriority::Recent);
        lease.promote(WorkPriority::Background);
        assert_eq!(lease.priority(), WorkPriority::Recent);
        lease.promote(WorkPriority::Interactive);
        assert_eq!(lease.priority(), WorkPriority::Interactive);
    }

    #[test]
    fn higher_priority_work_preempts_at_a_permit_boundary() {
        let gate = IoGate::new();
        let background = gate.register(WorkPriority::Background);
        let first = background
            .before_io(MAX_READ_CHUNK_BYTES, &CancellationToken::new())
            .unwrap();
        let interactive = gate.register(WorkPriority::Interactive);
        drop(first);
        let permit = interactive
            .before_io(MAX_READ_CHUNK_BYTES, &CancellationToken::new())
            .unwrap();
        assert_eq!(permit.max_bytes(), MAX_READ_CHUNK_BYTES);
    }
}
