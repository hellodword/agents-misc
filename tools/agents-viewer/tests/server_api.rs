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
    assert_eq!(status["initialIndexDays"], -1);
    assert!(status.get("initialIndexCutoff").is_none());
    assert_eq!(status["progress"]["excludedFiles"], 0);
    assert_eq!(status["progress"]["excludedBytes"], 0);

    let response = router
        .clone()
        .oneshot(support::request("/api/v1/sessions?limit=1"))
        .await
        .unwrap();
    let page = support::json(response).await;
    let session_id = page["data"][0]["id"].as_str().unwrap();
    assert!(
        page["data"][0]["updatedAt"]
            .as_str()
            .unwrap()
            .ends_with('Z')
    );
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
async fn sse_ring_replays_recent_events_and_marks_expired_ids_for_resync() {
    let hub = SseHub::new();
    for generation in 0..=SSE_RING_CAPACITY as u64 {
        hub.publish(
            SseEventType::IndexProgress,
            SseEventPayload {
                generation,
                phase: None,
                session_id: None,
                entry_id: None,
                progress: None,
                diagnostic: None,
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
async fn removing_a_plan_parent_clears_the_derived_handoff_relation() {
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
    let report = coordinator.reconcile().await.unwrap();
    writer.shutdown().await.unwrap();
    task.wait().await.unwrap();
    let row = sqlx::query(
        "SELECT parent_thread_id, parent_relation FROM sessions WHERE id = 'bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb'",
    )
    .fetch_one(app.state.database.pool())
    .await
    .unwrap();
    use sqlx::Row as _;
    assert_eq!(row.get::<Option<String>, _>("parent_thread_id"), None);
    assert_eq!(row.get::<Option<String>, _>("parent_relation"), None);
    assert!(
        report
            .updated_sessions
            .contains(&"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb".to_owned())
    );
}
