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
