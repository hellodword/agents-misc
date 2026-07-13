use agents_viewer::index::Database;
use agents_viewer::index::coordinator::IndexCoordinator;
use agents_viewer::index::writer::spawn_writer;
use agents_viewer::server::{AppState, router};
use tempfile::TempDir;

pub struct TestApp {
    pub _temp: TempDir,
    pub state: AppState,
}

impl TestApp {
    pub async fn new() -> Self {
        let temp = TempDir::new().unwrap();
        let source_home = temp.path().join("codex-home");
        let sessions = source_home.join("sessions/2025/01/02");
        std::fs::create_dir_all(&sessions).unwrap();
        std::fs::write(
            sessions.join("rollout-2025-01-02T03-04-05-11111111-1111-4111-8111-111111111111.jsonl"),
            include_bytes!("../fixtures/rollouts/v0_120.jsonl"),
        )
        .unwrap();
        let roots = agents_viewer::paths::resolve_source_roots(&source_home).unwrap();
        let cache =
            agents_viewer::paths::resolve_cache_paths(&roots.home, &temp.path().join("cache"))
                .unwrap();
        agents_viewer::permissions::prepare_cache_directory(&cache.namespace).unwrap();
        let database = Database::open_or_recover(&cache.database, "test-source")
            .await
            .unwrap();
        let (writer, task) = spawn_writer(database.clone());
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
        task.wait().await.unwrap();
        let state = AppState::new(
            database,
            roots,
            cache,
            agents_viewer::index::InitialIndexPolicy::all(),
        );
        Self { _temp: temp, state }
    }

    pub fn router(&self) -> axum::Router {
        router(self.state.clone(), "127.0.0.1:4747".parse().unwrap())
    }
}

pub fn request(path: &str) -> http::Request<axum::body::Body> {
    http::Request::builder()
        .uri(path)
        .header("host", "127.0.0.1:4747")
        .body(axum::body::Body::empty())
        .unwrap()
}

#[allow(dead_code)]
pub async fn json(response: http::Response<axum::body::Body>) -> serde_json::Value {
    let bytes = axum::body::to_bytes(response.into_body(), 8 * 1024 * 1024)
        .await
        .unwrap();
    serde_json::from_slice(&bytes).unwrap()
}
