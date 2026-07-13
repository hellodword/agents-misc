use std::io::{BufReader, Cursor};

use agents_viewer::index::Database;
use agents_viewer::index::coordinator::{IndexCoordinator, IndexUpdate};
use agents_viewer::index::search::{ArchiveFilter, SearchFilters};
use agents_viewer::index::search::{SearchRequest, search};
use agents_viewer::index::writer::{ScanMode, SourceFileRecord, spawn_writer};
use agents_viewer::model::{EntryKind, SearchField, SourceKind};
use agents_viewer::rollout::{CollectingSink, ParseContext, ParserOutput, RootKind, parse_rollout};
use sha2::Digest as _;
use tempfile::TempDir;
use tokio_util::sync::CancellationToken;

const FIXTURE: &[u8] = include_bytes!("fixtures/rollouts/v0_144.jsonl");

fn parse_fixture() -> agents_viewer::rollout::ParsedRollout {
    let context = ParseContext {
        root_kind: RootKind::Active,
        relative_path: "2026/07/01/fixture.jsonl".into(),
        file_name: "rollout-2026-07-01T10-00-00-22222222-2222-4222-8222-222222222222.jsonl".into(),
        modified_at_micros: 1_782_902_405_000_000,
        now_micros: 1_782_902_410_000_000,
        max_event_bytes: 1024 * 1024,
    };
    let mut sink = CollectingSink::default();
    let summary = parse_rollout(BufReader::new(Cursor::new(FIXTURE)), &context, &mut sink)
        .expect("fixture parses");
    sink.finish(summary)
}

fn source_record() -> SourceFileRecord {
    SourceFileRecord {
        root_kind: RootKind::Active,
        relative_path: "2026/07/01/fixture.jsonl".into(),
        file_key: "fixture-key".into(),
        size_bytes: FIXTURE.len() as u64,
        mtime_ns: 1_782_902_405_000_000_000,
        head_hash: Some("head".into()),
        tail_hash: Some("tail".into()),
        generation: 1,
        placeholder: None,
    }
}

fn outputs(parsed: &agents_viewer::rollout::ParsedRollout) -> Vec<ParserOutput> {
    parsed
        .raw_records
        .iter()
        .cloned()
        .map(ParserOutput::Raw)
        .chain(
            parsed
                .entries
                .iter()
                .cloned()
                .map(ParserOutput::EntryUpsert),
        )
        .chain(
            parsed
                .diagnostics
                .iter()
                .cloned()
                .map(ParserOutput::Diagnostic),
        )
        .collect()
}

#[tokio::test]
async fn fresh_database_uses_baseline_schema_and_only_the_first_open_requires_bootstrap() {
    let temp = TempDir::new().unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let path = cache.join("index.sqlite3");
    let first = Database::open_or_recover_with_disposition(&path, "fixture-source")
        .await
        .unwrap();
    assert!(first.bootstrap_required);
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT value FROM app_meta WHERE key = 'schema_signature'",
        )
        .fetch_one(first.database.pool())
        .await
        .unwrap(),
        format!(
            "{:x}",
            sha2::Sha256::digest(include_bytes!("../schema.sql"))
        )
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM pragma_table_info('entries') WHERE name = 'presentation'",
        )
        .fetch_one(first.database.pool())
        .await
        .unwrap(),
        1
    );
    first.database.mark_bootstrap_complete().await.unwrap();
    first.database.close().await;

    let second = Database::open_or_recover_with_disposition(&path, "fixture-source")
        .await
        .unwrap();
    assert!(!second.bootstrap_required);
    second.database.close().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn initializes_baseline_schema_with_fts5_and_atomically_swaps_staged_session() {
    let temp = TempDir::new().unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let database_path = cache.join("index.sqlite3");
    let database = Database::open_or_recover(&database_path, "fixture-source")
        .await
        .unwrap();

    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT sqlite_compileoption_used('ENABLE_FTS5')")
            .fetch_one(database.pool())
            .await
            .unwrap(),
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>("PRAGMA journal_mode")
            .fetch_one(database.pool())
            .await
            .unwrap()
            .to_ascii_lowercase(),
        "wal"
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("PRAGMA foreign_keys")
            .fetch_one(database.pool())
            .await
            .unwrap(),
        1
    );

    let parsed = parse_fixture();
    let (writer, writer_task) = spawn_writer(database.clone());
    let token = "scan-token-1".to_owned();
    let source_id = writer
        .begin(source_record(), token.clone(), ScanMode::Full)
        .await
        .unwrap();
    let batch_writer = writer.clone();
    let batch_token = token.clone();
    let batch = outputs(&parsed);
    tokio::task::spawn_blocking(move || {
        batch_writer.write_batch_blocking(source_id, batch_token, batch)
    })
    .await
    .unwrap()
    .unwrap();

    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM sessions")
            .fetch_one(database.pool())
            .await
            .unwrap(),
        0,
        "old formal data remains untouched until swap"
    );
    writer
        .finish(source_id, token, parsed.summary.clone(), ScanMode::Full)
        .await
        .unwrap();

    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM sessions")
            .fetch_one(database.pool())
            .await
            .unwrap(),
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM entries")
            .fetch_one(database.pool())
            .await
            .unwrap(),
        parsed.entries.len() as i64
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM entries_fts WHERE entries_fts MATCH 'bounded'",
        )
        .fetch_one(database.pool())
        .await
        .unwrap(),
        1
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM staged_entries")
            .fetch_one(database.pool())
            .await
            .unwrap(),
        0
    );

    let fts = search(
        &database,
        &SearchRequest {
            query: "bounded".into(),
            limit: 50,
            filters: SearchFilters {
                all_types: true,
                ..SearchFilters::default()
            },
        },
    )
    .await
    .unwrap();
    assert_eq!(fts.hits.len(), 1);
    assert!(!fts.partial);
    assert_eq!(fts.hits[0].snippet, "Use bounded parsing");
    assert_eq!(fts.hits[0].match_ranges[0].start, 4);
    assert_eq!(fts.hits[0].match_ranges[0].end, 11);

    let short = search(
        &database,
        &SearchRequest {
            query: "sy".into(),
            limit: 50,
            filters: SearchFilters {
                all_types: true,
                ..SearchFilters::default()
            },
        },
    )
    .await
    .unwrap();
    assert!(!short.hits.is_empty());
    assert!(!short.partial);

    writer.shutdown().await.unwrap();
    writer_task.wait().await.unwrap();
    database.close().await;
}

#[tokio::test]
async fn corrupt_database_is_preserved_and_rebuilt() {
    let temp = TempDir::new().unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let database_path = cache.join("index.sqlite3");
    let database = Database::open_or_recover(&database_path, "fixture-source")
        .await
        .unwrap();
    database.close().await;

    std::fs::write(&database_path, b"not a sqlite database").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt as _;
        std::fs::set_permissions(&database_path, std::fs::Permissions::from_mode(0o600)).unwrap();
    }
    let recovered = Database::open_or_recover(&database_path, "fixture-source")
        .await
        .unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, String>("PRAGMA integrity_check")
            .fetch_one(recovered.pool())
            .await
            .unwrap(),
        "ok"
    );
    let preserved = std::fs::read_dir(&cache)
        .unwrap()
        .filter_map(Result::ok)
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .any(|name| name.starts_with("index.sqlite3.corrupt-"));
    assert!(preserved);
    recovered.close().await;
}

#[tokio::test]
async fn incompatible_schema_and_stale_staging_recover_safely() {
    let temp = TempDir::new().unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let path = cache.join("index.sqlite3");
    let database = Database::open_or_recover(&path, "future-source")
        .await
        .unwrap();
    sqlx::query("INSERT INTO staged_sessions(scan_token, id, source_file_id, source_kind, title, preview, created_at_micros, updated_at_micros, archived, entry_count, index_state, completeness, diagnostic_count) VALUES ('stale', 'stale', 1, 'unknown', '', '', 0, 0, 0, 0, 'pending', 'complete', 0)")
        .execute(database.pool())
        .await
        .unwrap();
    database.close().await;
    let reopened = Database::open_or_recover(&path, "future-source")
        .await
        .unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM staged_sessions")
            .fetch_one(reopened.pool())
            .await
            .unwrap(),
        0
    );
    sqlx::query("UPDATE app_meta SET value = 'mismatch' WHERE key = 'schema_signature'")
        .execute(reopened.pool())
        .await
        .unwrap();
    reopened.close().await;
    let recovered = Database::open_or_recover(&path, "future-source")
        .await
        .unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT value FROM app_meta WHERE key = 'schema_signature'",
        )
        .fetch_one(recovered.pool())
        .await
        .unwrap(),
        format!(
            "{:x}",
            sha2::Sha256::digest(include_bytes!("../schema.sql"))
        )
    );
    assert!(
        std::fs::read_dir(&cache)
            .unwrap()
            .filter_map(Result::ok)
            .any(|entry| {
                entry
                    .file_name()
                    .to_string_lossy()
                    .starts_with("index.sqlite3.incompatible-")
            })
    );
    recovered.close().await;
}

#[tokio::test]
async fn rebuilt_database_uses_new_previous_swap_and_restores_on_failure() {
    let temp = TempDir::new().unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let path = cache.join("index.sqlite3");
    let old = Database::open_or_recover(&path, "swap-source")
        .await
        .unwrap();
    sqlx::query("INSERT INTO app_meta(key, value) VALUES ('marker', 'old')")
        .execute(old.pool())
        .await
        .unwrap();
    old.close().await;

    let new_path = cache.join("index.sqlite3.new");
    let new = Database::open_or_recover(&new_path, "swap-source")
        .await
        .unwrap();
    sqlx::query("INSERT INTO app_meta(key, value) VALUES ('marker', 'new')")
        .execute(new.pool())
        .await
        .unwrap();
    new.close().await;
    let active = agents_viewer::index::recovery::replace_database_atomically(
        &path,
        &new_path,
        "swap-source",
    )
    .await
    .unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT value FROM app_meta WHERE key = 'marker'")
            .fetch_one(active.pool())
            .await
            .unwrap(),
        "new"
    );
    assert!(!cache.join("index.sqlite3.previous").exists());
    active.close().await;

    std::fs::write(&new_path, b"bad rebuilt database").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt as _;
        std::fs::set_permissions(&new_path, std::fs::Permissions::from_mode(0o600)).unwrap();
    }
    assert!(
        agents_viewer::index::recovery::replace_database_atomically(
            &path,
            &new_path,
            "swap-source"
        )
        .await
        .is_err()
    );
    let restored = Database::open_or_recover(&path, "swap-source")
        .await
        .unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT value FROM app_meta WHERE key = 'marker'")
            .fetch_one(restored.pool())
            .await
            .unwrap(),
        "new"
    );
    restored.close().await;
}

#[tokio::test]
async fn search_covers_titles_literals_filters_and_short_query_caps() {
    let temp = TempDir::new().unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let database = Database::open_or_recover(&cache.join("index.sqlite3"), "search-source")
        .await
        .unwrap();
    sqlx::query("INSERT INTO source_files(id, root_kind, relative_path, file_key, size_bytes, mtime_ns) VALUES (1, 'active', 'search.jsonl', 'key', 1, 1)")
        .execute(database.pool())
        .await
        .unwrap();
    sqlx::query("INSERT INTO sessions(id, source_file_id, source_kind, title, preview, created_at_micros, updated_at_micros, archived, entry_count, index_state, completeness) VALUES ('search-session', 1, 'cli', 'OnlyTitleNeedle', '', 1, 1, 0, 10002, 'ready', 'complete')")
        .execute(database.pool())
        .await
        .unwrap();
    let mut transaction = database.pool().begin().await.unwrap();
    for sequence in 0_i64..=10_001 {
        let primary = if sequence == 0 { "unrelated" } else { "zz" };
        sqlx::query("INSERT INTO entries(id, session_id, sequence, timestamp_micros, kind, presentation, title, primary_text, secondary_text, metadata_json, id_basis, default_collapsed, searchable, primary_bytes, secondary_bytes) VALUES (?, 'search-session', ?, ?, 'message', 'user', '', ?, '', '{}', ?, 0, 1, ?, 0)")
            .bind(format!("entry-{sequence:05}"))
            .bind(sequence)
            .bind(sequence)
            .bind(primary)
            .bind(format!("basis-{sequence}"))
            .bind(i64::try_from(primary.len()).unwrap())
            .execute(&mut *transaction)
            .await
            .unwrap();
    }
    transaction.commit().await.unwrap();

    let title = search(
        &database,
        &SearchRequest {
            query: "Needle".into(),
            limit: 5,
            filters: SearchFilters::default(),
        },
    )
    .await
    .unwrap();
    assert_eq!(title.hits[0].field, SearchField::SessionTitle);
    let one = search(
        &database,
        &SearchRequest {
            query: "z".into(),
            limit: 5,
            filters: SearchFilters::default(),
        },
    )
    .await
    .unwrap();
    assert!(one.partial);
    let two = search(
        &database,
        &SearchRequest {
            query: "zz".into(),
            limit: 5,
            filters: SearchFilters::default(),
        },
    )
    .await
    .unwrap();
    assert!(two.partial);
    assert_eq!(two.hits.len(), 5);
    let literal = search(
        &database,
        &SearchRequest {
            query: "zz\" OR".into(),
            limit: 5,
            filters: SearchFilters::default(),
        },
    )
    .await
    .unwrap();
    assert!(literal.hits.is_empty());
    let filtered = search(
        &database,
        &SearchRequest {
            query: "zz".into(),
            limit: 5,
            filters: SearchFilters {
                archived: ArchiveFilter::Only,
                sources: vec![SourceKind::Vscode],
                kinds: vec![EntryKind::Tool],
                ..SearchFilters::default()
            },
        },
    )
    .await
    .unwrap();
    assert!(filtered.hits.is_empty());
    database.close().await;
}

#[tokio::test]
async fn search_defaults_to_conversation_and_all_types_includes_unindexed_activity() {
    let temp = TempDir::new().unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let database = Database::open_or_recover(&cache.join("index.sqlite3"), "scope-source")
        .await
        .unwrap();
    sqlx::query("INSERT INTO source_files(id, root_kind, relative_path, file_key, size_bytes, mtime_ns) VALUES (1, 'active', 'scope.jsonl', 'scope-key', 1, 1)")
        .execute(database.pool())
        .await
        .unwrap();
    sqlx::query("INSERT INTO sessions(id, source_file_id, source_kind, title, preview, created_at_micros, updated_at_micros, archived, entry_count, index_state, completeness) VALUES ('scope-session', 1, 'cli', 'Scope session', '', 1, 4, 0, 4, 'ready', 'complete')")
        .execute(database.pool())
        .await
        .unwrap();
    for (id, sequence, kind, presentation, searchable) in [
        ("scope-user", 1_i64, "message", "user", 1_i64),
        ("scope-assistant", 2, "message", "response", 1),
        ("scope-reasoning", 3, "reasoning", "technical", 1),
        ("scope-context", 4, "context", "internal", 0),
    ] {
        sqlx::query("INSERT INTO entries(id, session_id, sequence, timestamp_micros, kind, presentation, title, primary_text, secondary_text, metadata_json, id_basis, default_collapsed, searchable, primary_bytes, secondary_bytes) VALUES (?, 'scope-session', ?, ?, ?, ?, '', 'scope-needle', '', '{}', ?, 0, ?, 12, 0)")
            .bind(id)
            .bind(sequence)
            .bind(sequence)
            .bind(kind)
            .bind(presentation)
            .bind(format!("basis-{id}"))
            .bind(searchable)
            .execute(database.pool())
            .await
            .unwrap();
    }

    let conversation = search(
        &database,
        &SearchRequest {
            query: "scope-needle".into(),
            limit: 10,
            filters: SearchFilters::default(),
        },
    )
    .await
    .unwrap();
    let conversation_ids = conversation
        .hits
        .iter()
        .map(|hit| hit.entry_id.as_str())
        .collect::<Vec<_>>();
    assert_eq!(conversation_ids.len(), 2);
    assert!(conversation_ids.contains(&"scope-user"));
    assert!(conversation_ids.contains(&"scope-assistant"));

    let all_types = search(
        &database,
        &SearchRequest {
            query: "scope-needle".into(),
            limit: 10,
            filters: SearchFilters {
                all_types: true,
                ..SearchFilters::default()
            },
        },
    )
    .await
    .unwrap();
    let all_ids = all_types
        .hits
        .iter()
        .map(|hit| hit.entry_id.as_str())
        .collect::<Vec<_>>();
    assert_eq!(all_ids.len(), 4);
    assert!(all_ids.contains(&"scope-reasoning"));
    assert!(all_ids.contains(&"scope-context"));
    database.close().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn coordinator_prefers_active_duplicate_skips_unchanged_and_reconciles_append() {
    let temp = TempDir::new().unwrap();
    let source_home = temp.path().join("codex-home");
    let active_dir = source_home.join("sessions/2025/01/02");
    let archived_dir = source_home.join("archived_sessions");
    std::fs::create_dir_all(&active_dir).unwrap();
    std::fs::create_dir_all(&archived_dir).unwrap();
    let file_name = "rollout-2025-01-02T03-04-05-11111111-1111-4111-8111-111111111111.jsonl";
    let active = active_dir.join(file_name);
    let archived = archived_dir.join(file_name);
    std::fs::write(&active, include_bytes!("fixtures/rollouts/v0_120.jsonl")).unwrap();
    std::fs::write(&archived, include_bytes!("fixtures/rollouts/v0_120.jsonl")).unwrap();
    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(&active, active_dir.join("ignored.jsonl")).unwrap();
    }

    let roots = agents_viewer::paths::resolve_source_roots(&source_home).unwrap();
    let cache = temp.path().join("cache");
    agents_viewer::permissions::prepare_cache_directory(&cache).unwrap();
    let database = Database::open_or_recover(&cache.join("index.sqlite3"), "coordinator-source")
        .await
        .unwrap();
    let (writer, writer_task) = spawn_writer(database.clone());
    let coordinator = IndexCoordinator::new(
        database.clone(),
        writer.clone(),
        roots,
        1024 * 1024,
        agents_viewer::index::InitialIndexPolicy::all(),
    );

    let (updates, mut received) = tokio::sync::mpsc::channel(16);
    let first = coordinator
        .reconcile_with_updates(&CancellationToken::new(), Some(&updates))
        .await
        .unwrap();
    drop(updates);
    let mut events = Vec::new();
    while let Some(update) = received.recv().await {
        events.push(update);
    }
    assert!(matches!(
        events.first(),
        Some(IndexUpdate::Discovering { .. })
    ));
    assert!(events.iter().any(|update| matches!(update, IndexUpdate::Progress { progress, .. } if progress.total_files == 1 && progress.processed_files == 1)));
    assert!(matches!(
        events.last(),
        Some(IndexUpdate::Completed {
            foreground: true,
            ..
        })
    ));
    assert_eq!(first.discovered_files, 1);
    assert_eq!(first.indexed_files, 1);
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT archived FROM sessions WHERE id = '11111111-1111-4111-8111-111111111111'",
        )
        .fetch_one(database.pool())
        .await
        .unwrap(),
        0
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM diagnostics WHERE code = 'duplicate_session'",
        )
        .fetch_one(database.pool())
        .await
        .unwrap(),
        1
    );

    let second = coordinator.reconcile().await.unwrap();
    assert_eq!(second.indexed_files, 0);
    let (watch_sender, watch_receiver) = tokio::sync::mpsc::channel(1);
    let (background_sender, mut background_updates) = tokio::sync::mpsc::channel(4);
    let background_shutdown = CancellationToken::new();
    let background_task = tokio::spawn({
        let coordinator = coordinator.clone();
        let shutdown = background_shutdown.clone();
        async move {
            coordinator
                .run_with_updates(watch_receiver, shutdown, Some(background_sender), false)
                .await
        }
    });
    let update = tokio::time::timeout(std::time::Duration::from_secs(2), background_updates.recv())
        .await
        .unwrap()
        .unwrap();
    assert!(matches!(
        update,
        IndexUpdate::Completed {
            foreground: false,
            ..
        }
    ));
    assert!(background_updates.try_recv().is_err());
    background_shutdown.cancel();
    drop(watch_sender);
    background_task.await.unwrap().unwrap();
    let before = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM entries")
        .fetch_one(database.pool())
        .await
        .unwrap();
    use std::io::Write as _;
    let mut append = std::fs::OpenOptions::new()
        .append(true)
        .open(&active)
        .unwrap();
    append
        .write_all(
            b"{\"timestamp\":\"2025-01-02T03:04:09.000Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_message\",\"message\":\"Appended synthetic line\",\"phase\":\"final\"}}\n",
        )
        .unwrap();
    append.flush().unwrap();
    let third = coordinator.reconcile().await.unwrap();
    assert_eq!(third.indexed_files, 1);
    assert_eq!(
        third.appended_files, 1,
        "growth should parse only the suffix"
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM entries")
            .fetch_one(database.pool())
            .await
            .unwrap(),
        before + 1
    );

    std::fs::write(&active, include_bytes!("fixtures/rollouts/v0_120.jsonl")).unwrap();
    let truncated = coordinator.reconcile().await.unwrap();
    assert_eq!(truncated.indexed_files, 1);
    assert_eq!(truncated.appended_files, 0);

    std::fs::remove_file(&archived).unwrap();
    std::fs::rename(&active, &archived).unwrap();
    let moved = coordinator.reconcile().await.unwrap();
    assert_eq!(moved.indexed_files, 1, "{moved:?}");
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT archived FROM sessions WHERE id = '11111111-1111-4111-8111-111111111111'",
        )
        .fetch_one(database.pool())
        .await
        .unwrap(),
        1
    );

    writer.shutdown().await.unwrap();
    writer_task.wait().await.unwrap();
    database.close().await;
}
