use std::io::Write as _;

use super::*;
use crate::index::writer::{SourceFileRecord, spawn_writer};
use tempfile::TempDir;

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
fn every_live_sync_lease_is_reliably_acquired_and_released() {
    let (commands, mut receiver) = mpsc::unbounded_channel();
    let mut shared = SharedState::default();
    shared
        .catalog
        .insert("session".into(), Arc::new(source("session", 1, 1)));
    shared
        .freshness
        .insert("session".into(), SessionFreshness::Current);
    shared.snapshots.insert("session".into());
    let handle = CoordinatorHandle {
        shared: Arc::new(RwLock::new(shared)),
        commands,
    };

    let (first_status, first) = handle.acquire_live_sync("session").unwrap();
    let (second_status, second) = handle.acquire_live_sync("session").unwrap();

    assert_eq!(first_status.state, SessionSyncState::Queued);
    assert!(first_status.has_snapshot);
    assert_eq!(second_status, first_status);
    assert!(matches!(
        receiver.try_recv(),
        Ok(CoordinatorCommand::AcquireSession(session_id)) if session_id == "session"
    ));
    assert!(matches!(
        receiver.try_recv(),
        Ok(CoordinatorCommand::AcquireSession(session_id)) if session_id == "session"
    ));
    drop(first);
    drop(second);
    assert!(matches!(
        receiver.try_recv(),
        Ok(CoordinatorCommand::ReleaseSession(session_id)) if session_id == "session"
    ));
    assert!(matches!(
        receiver.try_recv(),
        Ok(CoordinatorCommand::ReleaseSession(session_id)) if session_id == "session"
    ));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn hot_audit_requeues_a_completed_session_without_a_watcher_event() {
    let temp = TempDir::new().unwrap();
    let source_home = temp.path().join("codex-home");
    let sessions = source_home.join("sessions/2025/01/02");
    std::fs::create_dir_all(&sessions).unwrap();
    let session_id = "11111111-1111-4111-8111-111111111111";
    let rollout = sessions.join(format!("rollout-2025-01-02T03-04-05-{session_id}.jsonl"));
    std::fs::write(
        &rollout,
        include_bytes!("../../../tests/fixtures/rollouts/v0_120.jsonl"),
    )
    .unwrap();
    let roots = crate::paths::resolve_source_roots(&source_home).unwrap();
    let cache = temp.path().join("cache");
    crate::permissions::prepare_cache_directory(&cache).unwrap();
    let database = Database::open_or_recover(&cache.join("index.sqlite3"), "hot-audit")
        .await
        .unwrap();
    let (writer, writer_task) = spawn_writer(database.clone());
    let coordinator = IndexCoordinator::new(
        database.clone(),
        writer.clone(),
        roots,
        1024 * 1024,
        InitialIndexPolicy::all(),
    );
    let (completion_sender, mut completion_receiver) = mpsc::channel(8);
    let (update_sender, mut update_receiver) = mpsc::channel(32);
    let shutdown = CancellationToken::new();
    let mut runtime = RuntimeScheduler::new(
        &coordinator,
        Some(update_sender),
        shutdown,
        completion_sender,
    );
    let now_micros = chrono::Utc::now().timestamp_micros();
    let discovery = coordinator
        .discover(1, now_micros, CancellationToken::new())
        .await
        .unwrap();
    runtime
        .apply_full_discovery(discovery, 1, now_micros, false)
        .await
        .unwrap();
    runtime.acquire_session(session_id).await.unwrap();
    runtime.start_available().unwrap();
    let completion = tokio::time::timeout(Duration::from_secs(2), completion_receiver.recv())
        .await
        .unwrap()
        .unwrap();
    runtime.handle_completion(completion).await.unwrap();
    let mut bootstrap_pending = false;
    runtime
        .maybe_finalize_cycle(&mut bootstrap_pending)
        .await
        .unwrap();
    assert!(runtime.hot_sessions.contains(session_id));
    while update_receiver.try_recv().is_ok() {}

    let before = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM entries")
        .fetch_one(database.pool())
        .await
        .unwrap();
    let read_bytes = runtime.gate.bytes_read();
    runtime.audit_hot_sessions(2).await.unwrap();
    assert_eq!(runtime.gate.bytes_read(), read_bytes);
    assert!(runtime.queue.is_empty());
    assert!(runtime.inflight.is_empty());
    assert!(completion_receiver.try_recv().is_err());
    assert!(
        update_receiver.try_recv().is_err(),
        "an unchanged hot audit must not publish a catalog update"
    );

    let mut append = std::fs::OpenOptions::new()
        .append(true)
        .open(&rollout)
        .unwrap();
    append
        .write_all(
            b"{\"timestamp\":\"2025-01-02T03:04:09.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_message\",\"message\":\"Hot audit synthetic line\",\"phase\":\"final\"}}\n",
        )
        .unwrap();
    append.flush().unwrap();
    drop(append);

    let background = WorkItem::new(
        Arc::new(source("background-inflight", 1, 1)),
        WorkPriority::Background,
        None,
    );
    runtime.inflight.insert(
        background.source.session_id.clone(),
        InflightWork {
            work: background,
            lease: runtime.gate.register(WorkPriority::Background).unwrap(),
            cancel: CancellationToken::new(),
        },
    );
    runtime.audit_hot_sessions(3).await.unwrap();
    let mut catalog_updated = false;
    while let Ok(update) = update_receiver.try_recv() {
        catalog_updated |= matches!(update, IndexUpdate::CatalogCommitted { .. });
    }
    assert!(
        catalog_updated,
        "a changed hot source must update the catalog"
    );
    assert!(runtime.inflight.contains_key("background-inflight"));
    assert!(runtime.inflight.contains_key(session_id));
    let completion = tokio::time::timeout(Duration::from_secs(2), completion_receiver.recv())
        .await
        .unwrap()
        .unwrap();
    runtime.handle_completion(completion).await.unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM entries")
            .fetch_one(database.pool())
            .await
            .unwrap(),
        before + 1
    );
    runtime.inflight.remove("background-inflight");

    drop(runtime);
    writer.shutdown().await.unwrap();
    writer_task.wait().await.unwrap();
    database.close().await;
}
