use std::io::Write as _;
use std::time::{Duration, Instant};

use agents_viewer::index::Database;
use agents_viewer::index::coordinator::IndexCoordinator;
use agents_viewer::index::writer::spawn_writer;
use agents_viewer::watch::{WatchEvent, start_watcher};
use tempfile::TempDir;
use tokio::sync::mpsc;

#[tokio::test]
async fn watcher_debounces_source_changes_without_losing_reconcile_signal() {
    let temp = TempDir::new().unwrap();
    let source_home = temp.path().join("codex-home");
    std::fs::create_dir_all(source_home.join("sessions")).unwrap();
    let existing = source_home.join("sessions/existing.jsonl");
    std::fs::write(&existing, b"{}\n").unwrap();
    let roots = agents_viewer::paths::resolve_source_roots(&source_home).unwrap();
    let (sender, mut receiver) = mpsc::channel(8);
    let watcher = start_watcher(&roots, sender).unwrap();

    std::fs::read(&existing).unwrap();
    assert!(
        tokio::time::timeout(Duration::from_millis(750), receiver.recv())
            .await
            .is_err(),
        "read-only access must not trigger a source update"
    );

    std::fs::write(source_home.join("sessions/new.jsonl"), b"{}\n").unwrap();
    let event = tokio::time::timeout(Duration::from_secs(2), receiver.recv())
        .await
        .expect("watcher event within two seconds")
        .expect("watcher channel remains open");
    assert!(matches!(
        event,
        WatchEvent::Paths(_) | WatchEvent::Reconcile
    ));
    watcher.shutdown().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn appended_complete_line_reaches_sqlite_within_two_seconds() {
    let temp = TempDir::new().unwrap();
    let source_home = temp.path().join("codex-home");
    let sessions = source_home.join("sessions/2025/01/02");
    std::fs::create_dir_all(&sessions).unwrap();
    let source =
        sessions.join("rollout-2025-01-02T03-04-05-11111111-1111-4111-8111-111111111111.jsonl");
    std::fs::write(&source, include_bytes!("fixtures/rollouts/v0_120.jsonl")).unwrap();
    let roots = agents_viewer::paths::resolve_source_roots(&source_home).unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let database = Database::open_or_recover(&cache.join("index.sqlite3"), "live-source")
        .await
        .unwrap();
    let (writer, writer_task) = spawn_writer(database.clone());
    let coordinator = IndexCoordinator::new(
        database.clone(),
        writer.clone(),
        roots.clone(),
        1024 * 1024,
        agents_viewer::index::InitialIndexPolicy::all(),
    );
    let (sender, mut receiver) = mpsc::channel(8);
    let watcher = start_watcher(&roots, sender).unwrap();
    coordinator.reconcile().await.unwrap();
    assert!(
        tokio::time::timeout(Duration::from_millis(750), receiver.recv())
            .await
            .is_err(),
        "index reads must not schedule another reconcile"
    );
    let before = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM entries")
        .fetch_one(database.pool())
        .await
        .unwrap();

    let started = Instant::now();
    let mut file = std::fs::OpenOptions::new()
        .append(true)
        .open(&source)
        .unwrap();
    file.write_all(
        b"{\"timestamp\":\"2025-01-02T03:04:09.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_message\",\"message\":\"Live synthetic line\",\"phase\":\"final\"}}\n",
    )
    .unwrap();
    file.flush().unwrap();
    let event = tokio::time::timeout(Duration::from_secs(2), receiver.recv())
        .await
        .expect("watch event within two seconds")
        .expect("watch channel open");
    assert!(matches!(
        event,
        WatchEvent::Paths(_) | WatchEvent::Reconcile
    ));
    coordinator.reconcile().await.unwrap();
    let after = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM entries")
        .fetch_one(database.pool())
        .await
        .unwrap();
    assert_eq!(after, before + 1);
    assert!(started.elapsed() < Duration::from_secs(2));

    watcher.shutdown().await;
    writer.shutdown().await.unwrap();
    writer_task.wait().await.unwrap();
    database.close().await;
}
