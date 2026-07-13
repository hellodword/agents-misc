use std::collections::BTreeSet;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicBool, Ordering};
use std::time::Duration;

use anyhow::{Context as _, Result};
use notify::{RecursiveMode, Watcher as _};
use tokio::sync::mpsc;

use crate::paths::SourceRoots;

pub const FILE_EVENT_QUEUE_CAPACITY: usize = 1_024;
pub const WATCH_DEBOUNCE: Duration = Duration::from_millis(250);

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
            Ok(event) => RawWatchEvent::Event(event),
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

async fn debounce_loop(
    mut receiver: mpsc::Receiver<RawWatchEvent>,
    output: mpsc::Sender<WatchEvent>,
    overflowed: Arc<AtomicBool>,
) {
    while let Some(first) = receiver.recv().await {
        let mut paths = BTreeSet::new();
        let mut degraded = None;
        collect_event(first, &mut paths, &mut degraded);
        while let Ok(Some(event)) = tokio::time::timeout(WATCH_DEBOUNCE, receiver.recv()).await {
            collect_event(event, &mut paths, &mut degraded);
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
}
