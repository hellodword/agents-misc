use axum::body::{Body, to_bytes};
use http::{Request, StatusCode};
use tempfile::TempDir;
use tower::ServiceExt as _;

use super::*;
use crate::index::coordinator::IndexCoordinator;
use crate::index::writer::spawn_writer;
use crate::index::{Database, InitialIndexPolicy};

#[tokio::test]
async fn poisoned_catalog_returns_internal_error_and_router_stays_alive() {
    let temp = TempDir::new().unwrap();
    let source_home = temp.path().join("codex-home");
    std::fs::create_dir_all(source_home.join("sessions")).unwrap();
    let roots = crate::paths::resolve_source_roots(&source_home).unwrap();
    let cache = crate::paths::resolve_cache_paths(&roots.home, &temp.path().join("cache")).unwrap();
    crate::permissions::prepare_cache_directory(&cache.namespace).unwrap();
    let database = Database::open_or_recover(&cache.database, "poison-test")
        .await
        .unwrap();
    let (writer, writer_task) = spawn_writer(database.clone());
    let coordinator = IndexCoordinator::new(
        database.clone(),
        writer.clone(),
        roots.clone(),
        1024 * 1024,
        InitialIndexPolicy::all(),
    );
    let handle = coordinator.handle();
    handle.poison_catalog_for_test();
    let app = crate::server::router(
        AppState::new(database, roots, cache, InitialIndexPolicy::all()).with_coordinator(handle),
        "127.0.0.1:4747".parse().unwrap(),
        "",
    );

    let failed = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/v1/sessions/019f5a6f-512b-7ae2-bbe9-884d39f6f599/live-sync")
                .header("host", "127.0.0.1:4747")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(failed.status(), StatusCode::INTERNAL_SERVER_ERROR);
    let body = to_bytes(failed.into_body(), 1024).await.unwrap();
    assert_eq!(
        serde_json::from_slice::<serde_json::Value>(&body).unwrap(),
        serde_json::json!({
            "error": {
                "code": "internal",
                "message": "an internal error occurred"
            }
        })
    );

    let healthy = app
        .oneshot(
            Request::builder()
                .uri("/api/v1/status")
                .header("host", "127.0.0.1:4747")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(healthy.status(), StatusCode::OK);

    writer.shutdown().await.unwrap();
    writer_task.wait().await.unwrap();
}
