use std::collections::BTreeSet;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use anyhow::{Context as _, Result};
use notify::event::{AccessKind, AccessMode};
use notify::{RecursiveMode, Watcher as _};
use tokio::sync::mpsc;

use crate::paths::SourceRoots;

pub const FILE_EVENT_QUEUE_CAPACITY: usize = 1_024;
pub const WATCH_DEBOUNCE: Duration = Duration::from_millis(250);
pub const WATCH_MAX_LATENCY: Duration = Duration::from_secs(1);

#[derive(Clone, Debug, Eq, PartialEq)]
pub enum WatchEvent {
    Paths(Vec<PathBuf>),
    Reconcile,
    Degraded(String),
}

enum RawWatchEvent {
    Event(notify::Event),
    Error(String),
}

pub struct SourceWatcher {
    _watcher: notify::RecommendedWatcher,
    task: tokio::task::JoinHandle<()>,
}

impl SourceWatcher {
    pub async fn shutdown(self) {
        drop(self._watcher);
        self.task.abort();
        let _ = self.task.await;
    }
}

pub fn start_watcher(
    roots: &SourceRoots,
    output: mpsc::Sender<WatchEvent>,
) -> Result<SourceWatcher> {
    let (sender, receiver) = mpsc::channel(FILE_EVENT_QUEUE_CAPACITY);
    let overflowed = Arc::new(AtomicBool::new(false));
    let callback_overflowed = Arc::clone(&overflowed);
    let mut watcher = notify::recommended_watcher(move |result: notify::Result<notify::Event>| {
        let event = match result {
            Ok(event) if should_forward_event(&event) => RawWatchEvent::Event(event),
            Ok(_) => return,
            Err(error) => RawWatchEvent::Error(error.to_string()),
        };
        if let Err(mpsc::error::TrySendError::Full(_)) = sender.try_send(event) {
            callback_overflowed.store(true, Ordering::Release);
        }
    })
    .context("create source watcher")?;
    for root in [roots.active.as_ref(), roots.archived.as_ref()]
        .into_iter()
        .flatten()
    {
        watcher
            .watch(root, RecursiveMode::Recursive)
            .with_context(|| format!("watch source root {}", root.display()))?;
    }
    let task = tokio::spawn(debounce_loop(receiver, output, overflowed));
    Ok(SourceWatcher {
        _watcher: watcher,
        task,
    })
}

fn should_forward_event(event: &notify::Event) -> bool {
    // Index discovery opens every rollout; forwarding read access would schedule another scan.
    event.need_rescan()
        || matches!(
            event.kind,
            notify::EventKind::Access(AccessKind::Close(AccessMode::Write))
        )
        || !event.kind.is_access()
}

async fn debounce_loop(
    mut receiver: mpsc::Receiver<RawWatchEvent>,
    output: mpsc::Sender<WatchEvent>,
    overflowed: Arc<AtomicBool>,
) {
    while let Some(first) = receiver.recv().await {
        let mut paths = BTreeSet::new();
        let mut degraded = None;
        collect_event(first, &mut paths, &mut degraded);
        let max_deadline = tokio::time::Instant::now() + WATCH_MAX_LATENCY;
        loop {
            let idle_deadline = tokio::time::Instant::now() + WATCH_DEBOUNCE;
            let deadline = idle_deadline.min(max_deadline);
            match tokio::time::timeout_at(deadline, receiver.recv()).await {
                Ok(Some(event)) => collect_event(event, &mut paths, &mut degraded),
                Ok(None) | Err(_) => break,
            }
        }
        if overflowed.swap(false, Ordering::AcqRel) {
            if output.send(WatchEvent::Reconcile).await.is_err() {
                break;
            }
        } else if let Some(message) = degraded {
            if output.send(WatchEvent::Degraded(message)).await.is_err() {
                break;
            }
        } else if !paths.is_empty()
            && output
                .send(WatchEvent::Paths(paths.into_iter().collect()))
                .await
                .is_err()
        {
            break;
        }
    }
}

fn collect_event(
    event: RawWatchEvent,
    paths: &mut BTreeSet<PathBuf>,
    degraded: &mut Option<String>,
) {
    match event {
        RawWatchEvent::Event(event) => paths.extend(event.paths),
        RawWatchEvent::Error(error) => *degraded = Some(error),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use notify::event::{Flag, ModifyKind};

    #[test]
    fn access_events_are_ignored_unless_they_close_a_writer_or_request_a_rescan() {
        for kind in [
            AccessKind::Read,
            AccessKind::Open(AccessMode::Read),
            AccessKind::Close(AccessMode::Read),
        ] {
            let access = notify::Event::new(notify::EventKind::Access(kind));
            assert!(!should_forward_event(&access));
        }

        let close_write = notify::Event::new(notify::EventKind::Access(AccessKind::Close(
            AccessMode::Write,
        )));
        assert!(should_forward_event(&close_write));

        let rescan =
            notify::Event::new(notify::EventKind::Access(AccessKind::Read)).set_flag(Flag::Rescan);
        assert!(should_forward_event(&rescan));
    }

    #[test]
    fn mutation_events_are_forwarded() {
        let event = notify::Event::new(notify::EventKind::Modify(ModifyKind::Any));
        assert!(should_forward_event(&event));
    }

    #[tokio::test]
    async fn queue_overflow_requests_full_reconcile() {
        let (raw_sender, raw_receiver) = mpsc::channel(1);
        let (output_sender, mut output_receiver) = mpsc::channel(1);
        let overflowed = Arc::new(AtomicBool::new(true));
        let task = tokio::spawn(debounce_loop(raw_receiver, output_sender, overflowed));
        raw_sender
            .send(RawWatchEvent::Error("synthetic overflow".into()))
            .await
            .unwrap();
        drop(raw_sender);
        assert_eq!(output_receiver.recv().await, Some(WatchEvent::Reconcile));
        task.await.unwrap();
    }

    #[tokio::test]
    async fn continuously_arriving_changes_flush_at_the_maximum_latency() {
        let (raw_sender, raw_receiver) = mpsc::channel(64);
        let (output_sender, mut output_receiver) = mpsc::channel(4);
        let overflowed = Arc::new(AtomicBool::new(false));
        let debounce_task = tokio::spawn(debounce_loop(raw_receiver, output_sender, overflowed));
        let producer = tokio::spawn(async move {
            for _ in 0..40 {
                let event = notify::Event::new(notify::EventKind::Modify(ModifyKind::Any))
                    .add_path(PathBuf::from("/source/live.jsonl"));
                if raw_sender.send(RawWatchEvent::Event(event)).await.is_err() {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(50)).await;
            }
        });

        let event = tokio::time::timeout(
            WATCH_MAX_LATENCY + Duration::from_millis(500),
            output_receiver.recv(),
        )
        .await
        .expect("continuous changes flush before the maximum-latency deadline")
        .expect("watch output remains open");
        assert_eq!(
            event,
            WatchEvent::Paths(vec![PathBuf::from("/source/live.jsonl")])
        );

        producer.abort();
        debounce_task.abort();
        let _ = producer.await;
        let _ = debounce_task.await;
    }
}
