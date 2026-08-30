mod support;

use http::StatusCode;
use tower::ServiceExt as _;

use agents_viewer::model::{SseEventPayload, SseEventType};
use agents_viewer::server::sse::{SSE_RING_CAPACITY, SseHub};

#[tokio::test]
async fn status_sessions_entries_content_raw_and_search_follow_contract() {
    let app = support::TestApp::new().await;
    let router = app.router();
    let response = router
        .clone()
        .oneshot(support::request("/api/v1/status"))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["cache-control"], "no-store");
    let status = support::json(response).await;
    assert!(status.get("initialIndexDays").is_none());
    assert!(status.get("initialIndexCutoff").is_none());
    assert_eq!(status["progress"]["excludedFiles"], 0);
    assert_eq!(status["progress"]["excludedBytes"], 0);

    let response = router
        .clone()
        .oneshot(support::request("/api/v1/sessions?limit=1"))
        .await
        .unwrap();
    let page = support::json(response).await;
    assert_eq!(page["data"].as_array().unwrap().len(), 1);
    assert!(
        page["data"][0]["updatedAt"]
            .as_str()
            .unwrap()
            .ends_with('Z')
    );
    let session_id = "11111111-1111-4111-8111-111111111111";
    let response = router
        .clone()
        .oneshot(support::request(
            "/api/v1/sessions?source=cli&source=vscode&archived=include",
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);

    let groups = support::json(
        router
            .clone()
            .oneshot(support::request("/api/v1/session-groups?limit=10"))
            .await
            .unwrap(),
    )
    .await;
    let plan_group = groups["data"]
        .as_array()
        .unwrap()
        .iter()
        .find(|group| group["root"]["session"]["id"] == "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
        .expect("plan group");
    assert_eq!(
        plan_group["latestSessionId"],
        "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
    );
    assert_eq!(
        plan_group["root"]["children"][0]["session"]["parentRelation"],
        "planHandoff"
    );

    let plan_session_id = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa";
    let plan_entries = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{plan_session_id}/entries?limit=1&displayTypes=plan"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(plan_entries["data"].as_array().unwrap().len(), 1);
    assert!(plan_entries["nextCursor"].is_null());
    assert!(plan_entries["previousCursor"].is_null());
    assert_eq!(plan_entries["data"][0]["kind"], "plan");
    assert_eq!(
        plan_entries["data"][0]["primaryPreview"],
        "# Group sessions\nImplement the tree"
    );
    assert_eq!(plan_entries["data"][0]["rawRefCount"], 2);
    assert!(
        !plan_entries["data"][0]["primaryPreview"]
            .as_str()
            .unwrap()
            .contains("proposed_plan")
    );
    let plan_entry_id = plan_entries["data"][0]["id"].as_str().unwrap();
    let around_plan = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{plan_session_id}/entries?limit=1&displayTypes=plan&aroundEntryId={plan_entry_id}"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(around_plan["data"][0]["id"], plan_entry_id);
    let received = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{plan_session_id}/entries?limit=10&displayTypes=received"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert!(received["data"].as_array().unwrap().is_empty());
    let plan_search = support::json(
        router
            .clone()
            .oneshot(support::request(
                "/api/v1/search?q=Implement&limit=10&allTypes=true",
            ))
            .await
            .unwrap(),
    )
    .await;
    assert!(
        plan_search["data"]
            .as_array()
            .unwrap()
            .iter()
            .any(|hit| hit["entryId"] == plan_entry_id && hit["kind"] == "plan")
    );
    let exec_groups = support::json(
        router
            .clone()
            .oneshot(support::request(
                "/api/v1/session-groups?source=exec&limit=10",
            ))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(exec_groups["data"].as_array().unwrap().len(), 1);
    assert_eq!(
        exec_groups["data"][0]["root"]["session"]["id"], "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
        "a child match keeps its complete group visible"
    );

    let response = router
        .clone()
        .oneshot(support::request(&format!("/api/v1/sessions/{session_id}")))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let response = router
        .clone()
        .oneshot(support::request(&format!(
            "/api/v1/sessions/{session_id}/entries?limit=2"
        )))
        .await
        .unwrap();
    let entries = support::json(response).await;
    let entry_id = entries["data"][0]["id"].as_str().unwrap();
    assert!(entries["nextCursor"].is_string());
    let response = router
        .clone()
        .oneshot(support::request(&format!(
            "/api/v1/sessions/{session_id}/entries/{entry_id}"
        )))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let response = router
        .clone()
        .oneshot(support::request(&format!(
            "/api/v1/sessions/{session_id}/entries/{entry_id}/content?limit=5&offset=1"
        )))
        .await
        .unwrap();
    let content = support::json(response).await;
    assert!(content["text"].as_str().unwrap().len() <= 5);
    sqlx::query("UPDATE entries SET presentation = 'internal' WHERE id = ?")
        .bind(entry_id)
        .execute(app.state.database.pool())
        .await
        .unwrap();
    let hidden = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session_id}/entries?limit=500"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert!(
        hidden["data"]
            .as_array()
            .unwrap()
            .iter()
            .all(|entry| entry["id"] != entry_id)
    );
    let included = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session_id}/entries?limit=500&includeTechnical=true"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert!(
        included["data"]
            .as_array()
            .unwrap()
            .iter()
            .any(|entry| entry["id"] == entry_id)
    );
    assert!(
        included["data"]
            .as_array()
            .unwrap()
            .iter()
            .any(|entry| entry["kind"] == "context")
    );

    let response = router
        .clone()
        .oneshot(support::request(&format!(
            "/api/v1/sessions/{session_id}/raw?limit=1"
        )))
        .await
        .unwrap();
    let raw = support::json(response).await;
    let raw_id = raw["data"][0]["id"].as_str().unwrap();
    let response = router
        .clone()
        .oneshot(support::request(&format!(
            "/api/v1/sessions/{session_id}/raw/{raw_id}?limit=64"
        )))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);

    let response = router
        .clone()
        .oneshot(support::request(
            "/api/v1/search?q=hello&limit=5&archived=include&allTypes=true",
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
}

#[tokio::test]
async fn plan_mode_final_answer_is_visible_in_the_tail_and_default_search() {
    use agents_viewer::index::Database;
    use agents_viewer::index::coordinator::IndexCoordinator;
    use agents_viewer::index::writer::spawn_writer;
    use agents_viewer::server::AppState;
    use tempfile::TempDir;

    let temp = TempDir::new().unwrap();
    let source_home = temp.path().join("codex-home");
    let sessions = source_home.join("sessions/2026/07/31");
    std::fs::create_dir_all(&sessions).unwrap();
    std::fs::write(
        sessions.join("rollout-2026-07-31T00-00-00-81818181-8181-4181-8181-818181818181.jsonl"),
        include_bytes!("fixtures/rollouts/plan_mode_final_answer.jsonl"),
    )
    .unwrap();
    let roots = agents_viewer::paths::resolve_source_roots(&source_home).unwrap();
    let cache =
        agents_viewer::paths::resolve_cache_paths(&roots.home, &temp.path().join("cache")).unwrap();
    agents_viewer::permissions::prepare_cache_directory(&cache.namespace).unwrap();
    let database = Database::open_or_recover(&cache.database, "plan-mode-api")
        .await
        .unwrap();
    let (writer, writer_task) = spawn_writer(database.clone());
    IndexCoordinator::new(
        database.clone(),
        writer.clone(),
        roots.clone(),
        1024 * 1024,
        agents_viewer::index::InitialIndexPolicy::all(),
    )
    .reconcile()
    .await
    .unwrap();
    writer.shutdown().await.unwrap();
    writer_task.wait().await.unwrap();
    let state = AppState::new(
        database,
        roots,
        cache,
        agents_viewer::index::InitialIndexPolicy::all(),
    );
    let router = agents_viewer::server::router(state, "127.0.0.1:4747".parse().unwrap(), "");
    let session_id = "81818181-8181-4181-8181-818181818181";

    let received = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session_id}/entries?limit=10&displayTypes=received"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(received["data"].as_array().unwrap().len(), 1);

    let tail = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session_id}/entries?limit=1&direction=backward"
            )))
            .await
            .unwrap(),
    )
    .await;
    let answer = &tail["data"][0];
    assert_eq!(answer["sequence"], 2);
    assert_eq!(answer["kind"], "message");
    assert_eq!(answer["presentation"], "response");
    assert_eq!(answer["phase"], "final");
    assert_eq!(
        answer["primaryPreview"],
        "Ordinary plan-mode answer keeps zetaunique visible."
    );
    assert_eq!(answer["rawRefCount"], 2);
    let entry_id = answer["id"].as_str().unwrap();

    let detail = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session_id}/entries/{entry_id}"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(detail["rawRefs"].as_array().unwrap().len(), 2);
    assert_eq!(detail["rawRefs"][0]["line"], 3);
    assert_eq!(detail["rawRefs"][1]["line"], 4);

    let search = support::json(
        router
            .oneshot(support::request("/api/v1/search?q=zetaunique&limit=10"))
            .await
            .unwrap(),
    )
    .await;
    assert!(
        search["data"]
            .as_array()
            .unwrap()
            .iter()
            .any(|hit| hit["entryId"] == entry_id && hit["kind"] == "message")
    );
}

#[tokio::test]
async fn request_user_input_is_visible_without_other_technical_activity() {
    let app = support::TestApp::new().await;
    let session = "11111111-1111-4111-8111-111111111111";
    for (id, sequence, tool_kind) in [
        ("request-user-input-entry", 999_i64, "requestUserInput"),
        ("generic-function-entry", 1_000_i64, "function"),
    ] {
        sqlx::query(
            "INSERT INTO entries( \
                id, session_id, sequence, timestamp_micros, kind, presentation, role, phase, \
                tool_kind, tool_status, title, primary_text, secondary_text, metadata_json, \
                id_basis, call_id, parent_entry_id, default_collapsed, searchable, primary_bytes, \
                secondary_bytes \
             ) VALUES (?, ?, ?, NULL, 'tool', 'technical', NULL, NULL, ?, 'succeeded', \
                'synthetic tool', '{}', '', '{}', ?, NULL, NULL, 1, 1, 2, 0)",
        )
        .bind(id)
        .bind(session)
        .bind(sequence)
        .bind(tool_kind)
        .bind(format!("basis-{id}"))
        .execute(app.state.database.pool())
        .await
        .unwrap();
    }

    let entries = support::json(
        app.router()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session}/entries?limit=500"
            )))
            .await
            .unwrap(),
    )
    .await;
    let ids = entries["data"]
        .as_array()
        .unwrap()
        .iter()
        .filter_map(|entry| entry["id"].as_str())
        .collect::<Vec<_>>();
    assert!(ids.contains(&"request-user-input-entry"));
    assert!(!ids.contains(&"generic-function-entry"));
}

#[tokio::test]
async fn conversation_display_types_filter_every_normalized_entry_category() {
    let app = support::TestApp::new().await;
    let router = app.router();
    let session = "11111111-1111-4111-8111-111111111111";
    let categories = [
        ("received", "message", "response", None),
        ("sent", "message", "user", None),
        (
            "requestUserInput",
            "tool",
            "technical",
            Some("requestUserInput"),
        ),
        ("reasoning", "reasoning", "technical", None),
        ("exec", "tool", "technical", Some("command")),
        ("plan", "plan", "technical", None),
        ("patch", "tool", "technical", Some("patch")),
        ("mcp", "tool", "technical", Some("mcp")),
        ("webSearch", "tool", "technical", Some("webSearch")),
        ("function", "tool", "technical", Some("function")),
        ("dynamic", "tool", "technical", Some("dynamic")),
        ("terminal", "tool", "technical", Some("terminal")),
        ("viewImage", "tool", "technical", Some("viewImage")),
        ("otherTool", "tool", "technical", Some("other")),
        ("warning", "warning", "technical", None),
        ("error", "error", "technical", None),
        ("context", "context", "technical", None),
        ("marker", "marker", "technical", None),
        ("technicalMessage", "message", "technical", None),
        ("internalMessage", "message", "internal", None),
        ("unknown", "unknown", "technical", None),
    ];
    for (index, (display_type, kind, presentation, tool_kind)) in categories.iter().enumerate() {
        let id = format!("display-filter-{display_type}");
        sqlx::query(
            "INSERT INTO entries( \
                id, session_id, sequence, timestamp_micros, kind, presentation, role, phase, \
                tool_kind, tool_status, title, primary_text, secondary_text, metadata_json, \
                id_basis, call_id, parent_entry_id, default_collapsed, searchable, primary_bytes, \
                secondary_bytes \
             ) VALUES (?, ?, ?, NULL, ?, ?, NULL, NULL, ?, NULL, ?, '', '', '{}', ?, NULL, \
                NULL, 1, 1, 0, 0)",
        )
        .bind(&id)
        .bind(session)
        .bind(10_000_i64 + i64::try_from(index).unwrap())
        .bind(kind)
        .bind(presentation)
        .bind(tool_kind)
        .bind(display_type)
        .bind(format!("basis-{id}"))
        .execute(app.state.database.pool())
        .await
        .unwrap();
    }

    for (display_type, _, _, _) in categories {
        let response = router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session}/entries?limit=500&displayTypes={display_type}"
            )))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK, "{display_type}");
        let page = support::json(response).await;
        let filtered_ids = page["data"]
            .as_array()
            .unwrap()
            .iter()
            .filter_map(|entry| entry["id"].as_str())
            .filter(|id| id.starts_with("display-filter-"))
            .collect::<Vec<_>>();
        assert_eq!(
            filtered_ids,
            vec![format!("display-filter-{display_type}")],
            "{display_type}"
        );
    }

    let first = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session}/entries?limit=1&displayTypes=received,sent"
            )))
            .await
            .unwrap(),
    )
    .await;
    let cursor = first["nextCursor"].as_str().unwrap();
    let reordered = router
        .clone()
        .oneshot(support::request(&format!(
            "/api/v1/sessions/{session}/entries?limit=1&displayTypes=sent,received&cursor={cursor}"
        )))
        .await
        .unwrap();
    assert_eq!(reordered.status(), StatusCode::OK);

    for query in [
        "displayTypes=",
        "displayTypes=sent,notAType",
        "displayTypes=sent&includeTechnical=true",
    ] {
        let response = router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session}/entries?{query}"
            )))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_REQUEST, "{query}");
    }
}

#[tokio::test]
async fn sse_ring_replays_recent_events_and_marks_expired_ids_for_resync() {
    let hub = SseHub::new();
    for generation in 0..=SSE_RING_CAPACITY as u64 {
        hub.publish(
            SseEventType::CatalogProgress,
            SseEventPayload {
                generation,
                phase: None,
                session_id: None,
                entry_id: None,
                progress: None,
                diagnostic: None,
                sync_state: None,
                snapshot_revision: None,
            },
        )
        .await;
    }
    let (expired_replay, expired) = hub.replay_after(Some(0)).await;
    assert!(expired);
    assert_eq!(expired_replay.len(), SSE_RING_CAPACITY);
    let (recent, expired) = hub.replay_after(Some(SSE_RING_CAPACITY as u64)).await;
    assert!(!expired);
    assert_eq!(recent.len(), 1);
    assert_eq!(recent[0].id, SSE_RING_CAPACITY as u64 + 1);
}

#[tokio::test]
async fn pagination_cursor_validation_and_api_errors_are_stable() {
    let app = support::TestApp::new().await;
    let router = app.router();
    let session = "11111111-1111-4111-8111-111111111111";
    let first = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session}/entries?limit=1"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert!(first.get("previousCursor").is_none());
    let first_id = first["data"][0]["id"].as_str().unwrap();
    let cursor = first["nextCursor"].as_str().unwrap();
    let second = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session}/entries?limit=1&cursor={cursor}"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert_ne!(first_id, second["data"][0]["id"].as_str().unwrap());
    assert!(second["previousCursor"].is_string());
    assert!(second["nextCursor"].is_string());

    let latest = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session}/entries?limit=1&direction=backward"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert!(latest.get("nextCursor").is_none());
    let older_cursor = latest["previousCursor"].as_str().unwrap();
    let older = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/sessions/{session}/entries?limit=1&cursor={older_cursor}"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert!(older["nextCursor"].is_string());
    assert_ne!(latest["data"][0]["id"], older["data"][0]["id"]);

    let response = router
        .clone()
        .oneshot(support::request(&format!(
            "/api/v1/sessions/{session}/entries?limit=1&cursor={cursor}&includeTechnical=true"
        )))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    let response = router
        .clone()
        .oneshot(support::request(&format!(
            "/api/v1/sessions?cursor={cursor}"
        )))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    let body = support::json(response).await;
    assert_eq!(body["error"]["code"], "invalid_argument");
    let response = router
        .clone()
        .oneshot(support::request("/api/v1/search?q="))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    let response = router
        .clone()
        .oneshot(support::request(
            "/api/v1/search?q=hello&allTypes=sometimes",
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    let response = router
        .oneshot(support::request("/api/v1/no-such-endpoint"))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert_eq!(support::json(response).await["error"]["code"], "not_found");
}

#[tokio::test]
async fn session_group_pagination_and_cycle_guard_keep_every_session_browsable() {
    let app = support::TestApp::new().await;
    let router = app.router();
    let first = support::json(
        router
            .clone()
            .oneshot(support::request("/api/v1/session-groups?limit=1"))
            .await
            .unwrap(),
    )
    .await;
    let cursor = first["nextCursor"].as_str().expect("second group cursor");
    let second = support::json(
        router
            .clone()
            .oneshot(support::request(&format!(
                "/api/v1/session-groups?limit=1&cursor={cursor}"
            )))
            .await
            .unwrap(),
    )
    .await;
    assert_ne!(
        first["data"][0]["root"]["session"]["id"],
        second["data"][0]["root"]["session"]["id"]
    );
    assert!(second["previousCursor"].is_string());

    sqlx::query(
        "UPDATE sessions SET parent_thread_id = CASE id \
            WHEN 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa' THEN 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb' \
            WHEN 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb' THEN 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa' \
            ELSE parent_thread_id END \
         WHERE id IN ('aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa', 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb')",
    )
    .execute(app.state.database.pool())
    .await
    .unwrap();
    let groups = support::json(
        router
            .oneshot(support::request("/api/v1/session-groups?limit=10"))
            .await
            .unwrap(),
    )
    .await;
    fn collect_ids(node: &serde_json::Value, ids: &mut Vec<String>) {
        ids.push(node["session"]["id"].as_str().unwrap().to_owned());
        for child in node["children"].as_array().unwrap() {
            collect_ids(child, ids);
        }
    }
    let mut ids = Vec::new();
    for group in groups["data"].as_array().unwrap() {
        collect_ids(&group["root"], &mut ids);
    }
    ids.sort();
    ids.dedup();
    assert_eq!(ids.len(), 3);
}

#[tokio::test]
async fn missing_source_retains_cached_session_and_handoff_snapshot() {
    use agents_viewer::index::coordinator::IndexCoordinator;
    use agents_viewer::index::writer::spawn_writer;

    let app = support::TestApp::new().await;
    let parent =
        app.state.roots.active.as_ref().unwrap().join(
            "2025/01/02/rollout-2024-01-01T00-00-00-aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa.jsonl",
        );
    std::fs::remove_file(parent).unwrap();
    let (writer, task) = spawn_writer(app.state.database.clone());
    let coordinator = IndexCoordinator::new(
        app.state.database.clone(),
        writer.clone(),
        app.state.roots.clone(),
        1024 * 1024,
        agents_viewer::index::InitialIndexPolicy::all(),
    );
    coordinator.reconcile().await.unwrap();
    let (updates, mut received) = tokio::sync::mpsc::channel(16);
    let report = coordinator
        .reconcile_with_updates(&tokio_util::sync::CancellationToken::new(), Some(&updates))
        .await
        .unwrap();
    drop(updates);
    let mut events = Vec::new();
    while let Some(update) = received.recv().await {
        events.push(update);
    }
    writer.shutdown().await.unwrap();
    task.wait().await.unwrap();
    let row = sqlx::query(
        "SELECT parent_thread_id, parent_relation FROM sessions WHERE id = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'",
    )
    .fetch_one(app.state.database.pool())
    .await
    .unwrap();
    use sqlx::Row as _;
    assert_eq!(
        row.get::<Option<String>, _>("parent_thread_id").as_deref(),
        Some("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
    );
    assert_eq!(
        row.get::<Option<String>, _>("parent_relation").as_deref(),
        Some("planHandoff")
    );
    assert_eq!(
        sqlx::query_scalar::<_, String>(
            "SELECT scan_state FROM source_files WHERE session_id = 'aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa'",
        )
        .fetch_one(app.state.database.pool())
        .await
        .unwrap(),
        "source_missing"
    );
    assert!(report.updated_sessions.is_empty());
    let relationship_updates = events
        .iter()
        .enumerate()
        .filter_map(|(index, update)| match update {
            agents_viewer::index::coordinator::IndexUpdate::SessionCommitted {
                session_id, ..
            } => Some((index, session_id.as_str())),
            _ => None,
        })
        .collect::<Vec<_>>();
    assert!(relationship_updates.is_empty());
    assert!(matches!(
        events.last(),
        Some(agents_viewer::index::coordinator::IndexUpdate::Completed { .. })
    ));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn page_scoped_live_sync_follows_a_cataloged_session_until_disconnect() {
    use std::io::Write as _;

    use agents_viewer::index::Database;
    use agents_viewer::index::coordinator::{IndexCoordinator, IndexUpdate};
    use agents_viewer::index::writer::spawn_writer;
    use agents_viewer::server::{AppState, router};
    use tokio::sync::mpsc;
    use tokio_util::sync::CancellationToken;

    let temp = tempfile::TempDir::new().unwrap();
    let source_home = temp.path().join("codex-home");
    let sessions = source_home.join("sessions/2024/01/01");
    std::fs::create_dir_all(&sessions).unwrap();
    let session_id = "019f5a6f-512b-7ae2-bbe9-884d39f6f500";
    let source = sessions.join(format!("rollout-2024-01-01T00-00-00-{session_id}.jsonl"));
    std::fs::write(
        &source,
        format!(
            "{{\"timestamp\":\"2024-01-01T00:00:00Z\",\"type\":\"session_meta\",\"payload\":{{\"id\":\"{session_id}\",\"cwd\":\"/deferred\"}}}}\n"
        ),
    )
    .unwrap();
    let roots = agents_viewer::paths::resolve_source_roots(&source_home).unwrap();
    let cache =
        agents_viewer::paths::resolve_cache_paths(&roots.home, &temp.path().join("cache")).unwrap();
    agents_viewer::permissions::prepare_cache_directory(&cache.namespace).unwrap();
    let database = Database::open_or_recover(&cache.database, "direct-sync-source")
        .await
        .unwrap();
    let (writer, writer_task) = spawn_writer(database.clone());
    let policy =
        agents_viewer::index::InitialIndexPolicy::new(0, chrono::Utc::now().timestamp_micros())
            .unwrap();
    let coordinator = IndexCoordinator::new(
        database.clone(),
        writer.clone(),
        roots.clone(),
        1024 * 1024,
        policy,
    );
    let state = AppState::new(database.clone(), roots, cache, policy)
        .with_coordinator(coordinator.handle());
    let app = router(state, "127.0.0.1:4747".parse().unwrap(), "");
    let (_watch_sender, watch_receiver) = mpsc::channel(4);
    let (update_sender, mut updates) = mpsc::channel(16);
    let shutdown = CancellationToken::new();
    let coordinator_task = tokio::spawn({
        let shutdown = shutdown.clone();
        async move {
            coordinator
                .run_with_updates(watch_receiver, shutdown, Some(update_sender), false)
                .await
        }
    });
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            if matches!(updates.recv().await, Some(IndexUpdate::Completed { .. })) {
                break;
            }
        }
    })
    .await
    .unwrap();

    let removed_sync = app
        .clone()
        .oneshot(
            http::Request::builder()
                .method("PUT")
                .uri(format!("/api/v1/sessions/{session_id}/sync"))
                .header("host", "127.0.0.1:4747")
                .body(axum::body::Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(removed_sync.status(), StatusCode::METHOD_NOT_ALLOWED);

    let live_sync = app
        .clone()
        .oneshot(
            http::Request::builder()
                .method("POST")
                .uri(format!("/api/v1/sessions/{session_id}/live-sync"))
                .header("host", "127.0.0.1:4747")
                .body(axum::body::Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(live_sync.status(), StatusCode::OK);
    assert!(
        live_sync.headers()[http::header::CONTENT_TYPE]
            .to_str()
            .unwrap()
            .starts_with("text/event-stream")
    );

    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            if matches!(
                updates.recv().await,
                Some(IndexUpdate::SessionCommitted { session_id: committed, .. }) if committed == session_id
            ) {
                break;
            }
        }
    })
    .await
    .unwrap();
    let detail = app
        .clone()
        .oneshot(support::request(&format!("/api/v1/sessions/{session_id}")))
        .await
        .unwrap();
    assert_eq!(detail.status(), StatusCode::OK);
    let detail = support::json(detail).await;
    assert_eq!(detail["summary"]["freshness"], "current");
    assert_eq!(detail["summary"]["entryCount"], 0);
    assert_eq!(detail["summary"]["contentStatus"]["liveState"], "following");

    let mut append = std::fs::OpenOptions::new()
        .append(true)
        .open(&source)
        .unwrap();
    append
        .write_all(
            b"{\"timestamp\":\"2024-01-01T00:00:01Z\",\"type\":\"event_msg\",\"payload\":{\"type\":\"agent_message\",\"message\":\"Direct refresh line\",\"phase\":\"final\"}}\n",
        )
        .unwrap();
    append.flush().unwrap();
    drop(append);

    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            if matches!(
                updates.recv().await,
                Some(IndexUpdate::SessionCommitted { session_id: committed, .. }) if committed == session_id
            ) {
                break;
            }
        }
    })
    .await
    .unwrap();
    let detail = app
        .clone()
        .oneshot(support::request(&format!("/api/v1/sessions/{session_id}")))
        .await
        .unwrap();
    let detail = support::json(detail).await;
    assert_eq!(detail["summary"]["entryCount"], 1);
    assert_eq!(detail["summary"]["contentStatus"]["liveState"], "following");

    let unknown = app
        .clone()
        .oneshot(
            http::Request::builder()
                .method("POST")
                .uri("/api/v1/sessions/019f5a6f-512b-7ae2-bbe9-884d39f6f501/live-sync")
                .header("host", "127.0.0.1:4747")
                .body(axum::body::Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(unknown.status(), StatusCode::NOT_FOUND);
    let uncached_label = app
        .clone()
        .oneshot(
            http::Request::builder()
                .method("POST")
                .uri("/api/v1/sessions/not-a-cached-uuid/live-sync")
                .header("host", "127.0.0.1:4747")
                .body(axum::body::Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(uncached_label.status(), StatusCode::NOT_FOUND);

    drop(live_sync);
    tokio::time::timeout(std::time::Duration::from_secs(2), async {
        loop {
            if matches!(
                updates.recv().await,
                Some(IndexUpdate::SessionStateCleared { session_id: released, .. }) if released == session_id
            ) {
                break;
            }
        }
    })
    .await
    .unwrap();
    let detail = app
        .clone()
        .oneshot(support::request(&format!("/api/v1/sessions/{session_id}")))
        .await
        .unwrap();
    assert_eq!(
        support::json(detail).await["summary"]["contentStatus"]["liveState"],
        "inactive"
    );
    shutdown.cancel();
    coordinator_task.await.unwrap().unwrap();
    writer.shutdown().await.unwrap();
    writer_task.wait().await.unwrap();
}
