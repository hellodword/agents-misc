use std::collections::VecDeque;
use std::convert::Infallible;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};

use axum::response::Sse;
use axum::response::sse::Event;
use futures::stream;
use tokio::sync::{Mutex, Semaphore, broadcast};
use tokio_util::sync::CancellationToken;

use crate::model::{SseEvent, SseEventPayload, SseEventType};

use super::ApiFailure;

pub const SSE_RING_CAPACITY: usize = 512;
pub const SSE_CONNECTION_LIMIT: usize = 16;

#[derive(Clone)]
pub struct SseHub {
    inner: Arc<Inner>,
}

struct Inner {
    next_id: AtomicU64,
    ring: Mutex<VecDeque<SseEvent>>,
    sender: broadcast::Sender<SseEvent>,
    connections: Arc<Semaphore>,
    shutdown: CancellationToken,
}

impl Default for SseHub {
    fn default() -> Self {
        Self::new()
    }
}

impl SseHub {
    #[must_use]
    pub fn new() -> Self {
        Self::new_with_shutdown(CancellationToken::new())
    }

    #[must_use]
    pub fn new_with_shutdown(shutdown: CancellationToken) -> Self {
        let (sender, _) = broadcast::channel(SSE_RING_CAPACITY);
        Self {
            inner: Arc::new(Inner {
                next_id: AtomicU64::new(0),
                ring: Mutex::new(VecDeque::with_capacity(SSE_RING_CAPACITY)),
                sender,
                connections: Arc::new(Semaphore::new(SSE_CONNECTION_LIMIT)),
                shutdown,
            }),
        }
    }

    pub async fn publish(&self, event: SseEventType, data: SseEventPayload) -> u64 {
        let id = self.inner.next_id.fetch_add(1, Ordering::SeqCst) + 1;
        let item = SseEvent { id, event, data };
        let mut ring = self.inner.ring.lock().await;
        if ring.len() == SSE_RING_CAPACITY {
            ring.pop_front();
        }
        ring.push_back(item.clone());
        drop(ring);
        let _ = self.inner.sender.send(item);
        id
    }

    pub async fn subscribe(
        &self,
        last_event_id: Option<u64>,
    ) -> Result<Sse<impl futures::Stream<Item = Result<Event, Infallible>> + use<>>, ApiFailure>
    {
        let permit = Arc::clone(&self.inner.connections)
            .try_acquire_owned()
            .map_err(|_| ApiFailure::service_unavailable("SSE connection limit reached"))?;
        let receiver = self.inner.sender.subscribe();
        let shutdown = self.inner.shutdown.clone();
        let (items, expired) = self.replay_after(last_event_id).await;
        let mut replay = VecDeque::from(items);
        if expired {
            replay.clear();
            let id = self.inner.next_id.fetch_add(1, Ordering::SeqCst) + 1;
            replay.push_back(SseEvent {
                id,
                event: SseEventType::Resync,
                data: SseEventPayload {
                    generation: 0,
                    phase: None,
                    session_id: None,
                    entry_id: None,
                    progress: None,
                    diagnostic: None,
                },
            });
        }
        let initial_last = last_event_id.unwrap_or_default();
        let state = (replay, receiver, permit, initial_last, shutdown);
        let events = stream::unfold(
            state,
            |(mut replay, mut receiver, permit, mut last, shutdown)| async move {
                loop {
                    let item = match replay.pop_front() {
                        Some(item) => item,
                        None => match tokio::select! {
                            () = shutdown.cancelled() => return None,
                            received = receiver.recv() => received,
                        } {
                            Ok(item) => item,
                            Err(broadcast::error::RecvError::Lagged(_)) => SseEvent {
                                id: last.saturating_add(1),
                                event: SseEventType::Resync,
                                data: SseEventPayload {
                                    generation: 0,
                                    phase: None,
                                    session_id: None,
                                    entry_id: None,
                                    progress: None,
                                    diagnostic: None,
                                },
                            },
                            Err(broadcast::error::RecvError::Closed) => return None,
                        },
                    };
                    if item.id <= last {
                        continue;
                    }
                    last = item.id;
                    let event = Event::default()
                        .id(item.id.to_string())
                        .event(event_name(item.event))
                        .json_data(&item.data)
                        .expect("SSE payload is serializable");
                    return Some((Ok(event), (replay, receiver, permit, last, shutdown)));
                }
            },
        );
        Ok(Sse::new(events))
    }

    pub async fn replay_after(&self, last_event_id: Option<u64>) -> (Vec<SseEvent>, bool) {
        let ring = self.inner.ring.lock().await;
        let earliest = ring.front().map(|event| event.id);
        let replay = last_event_id.map_or_else(Vec::new, |last| {
            ring.iter()
                .filter(|event| event.id > last)
                .cloned()
                .collect()
        });
        let expired = last_event_id
            .zip(earliest)
            .is_some_and(|(last, first)| last.saturating_add(1) < first);
        (replay, expired)
    }
}

fn event_name(event: SseEventType) -> &'static str {
    match event {
        SseEventType::IndexProgress => "indexProgress",
        SseEventType::SessionUpdated => "sessionUpdated",
        SseEventType::EntryUpdated => "entryUpdated",
        SseEventType::Diagnostic => "diagnostic",
        SseEventType::Resync => "resync",
        SseEventType::Heartbeat => "heartbeat",
    }
}
