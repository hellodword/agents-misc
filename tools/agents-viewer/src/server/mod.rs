pub mod api;
pub mod assets;
pub mod cursor;
pub mod middleware;
pub mod sse;

use std::collections::BTreeMap;
use std::net::SocketAddr;
use std::sync::Arc;

use axum::Router;
use axum::extract::State;
use axum::middleware as axum_middleware;
use axum::response::{IntoResponse, Response};
use http::StatusCode;
use tokio::sync::{RwLock, Semaphore};
use tokio_util::sync::CancellationToken;

use crate::index::{Database, InitialIndexPolicy};
use crate::model::{ApiError, ApiErrorEnvelope, IndexProgress, ServicePhase, Status};
use crate::paths::{CachePaths, SourceRoots};

#[derive(Clone)]
pub struct AppState {
    pub database: Database,
    pub roots: SourceRoots,
    pub cache: CachePaths,
    pub status: Arc<RwLock<Status>>,
    pub sse: sse::SseHub,
    requests: Arc<Semaphore>,
}

impl AppState {
    #[must_use]
    pub fn new(
        database: Database,
        roots: SourceRoots,
        cache: CachePaths,
        policy: InitialIndexPolicy,
    ) -> Self {
        Self::new_with_shutdown(
            database,
            roots,
            cache,
            policy,
            CancellationToken::new(),
            false,
        )
    }

    #[must_use]
    pub fn new_with_shutdown(
        database: Database,
        roots: SourceRoots,
        cache: CachePaths,
        policy: InitialIndexPolicy,
        shutdown: CancellationToken,
        bootstrap_required: bool,
    ) -> Self {
        let initial_index_cutoff = policy.cutoff_micros.and_then(|cutoff| {
            chrono::DateTime::<chrono::Utc>::from_timestamp_micros(cutoff)
                .map(|time| time.to_rfc3339_opts(chrono::SecondsFormat::Micros, true))
        });
        Self {
            database,
            status: Arc::new(RwLock::new(Status {
                app_version: env!("CARGO_PKG_VERSION").into(),
                source_home: roots.home.to_string_lossy().into_owned(),
                cache_dir: cache.namespace.to_string_lossy().into_owned(),
                initial_index_days: policy.days,
                initial_index_cutoff,
                generation: 0,
                phase: if bootstrap_required {
                    ServicePhase::Starting
                } else {
                    ServicePhase::Ready
                },
                progress: IndexProgress {
                    total_files: 0,
                    processed_files: 0,
                    total_bytes: 0,
                    processed_bytes: 0,
                    failed_files: 0,
                    excluded_files: 0,
                    excluded_bytes: 0,
                },
                fts_ready: true,
                database_bytes: 0,
                last_reconcile_at: None,
            })),
            roots,
            cache,
            sse: sse::SseHub::new_with_shutdown(shutdown),
            requests: Arc::new(Semaphore::new(64)),
        }
    }
}

pub fn router(state: AppState, bound: SocketAddr) -> Router {
    let api =
        api::router()
            .fallback(api::unknown_api)
            .route_layer(axum_middleware::from_fn_with_state(
                Arc::clone(&state.requests),
                limit_requests,
            ));
    Router::new()
        .nest("/api/v1", api)
        .route("/", axum::routing::get(assets::root))
        .route("/{*path}", axum::routing::get(assets::fallback))
        .with_state(state)
        .layer(axum_middleware::from_fn_with_state(
            middleware::SecurityConfig::new(bound),
            middleware::secure_request,
        ))
}

async fn limit_requests(
    State(semaphore): State<Arc<Semaphore>>,
    request: axum::extract::Request,
    next: axum::middleware::Next,
) -> Result<Response, ApiFailure> {
    let _permit = Arc::clone(&semaphore)
        .try_acquire_owned()
        .map_err(|_| ApiFailure::service_unavailable("HTTP concurrency limit reached"))?;
    if request.uri().path().ends_with("/events") {
        Ok(next.run(request).await)
    } else {
        tokio::time::timeout(std::time::Duration::from_secs(30), next.run(request))
            .await
            .map_err(|_| {
                ApiFailure::new(StatusCode::GATEWAY_TIMEOUT, "internal", "request timed out")
            })
    }
}

#[derive(Debug)]
pub struct ApiFailure {
    status: StatusCode,
    code: &'static str,
    message: String,
    details: Option<BTreeMap<String, String>>,
}

impl ApiFailure {
    #[must_use]
    pub fn new(status: StatusCode, code: &'static str, message: impl Into<String>) -> Self {
        Self {
            status,
            code,
            message: message.into(),
            details: None,
        }
    }

    pub fn invalid(message: impl Into<String>) -> Self {
        Self::new(StatusCode::BAD_REQUEST, "invalid_argument", message)
    }

    pub fn not_found(message: impl Into<String>) -> Self {
        Self::new(StatusCode::NOT_FOUND, "not_found", message)
    }

    pub fn forbidden(message: impl Into<String>) -> Self {
        Self::new(StatusCode::FORBIDDEN, "invalid_argument", message)
    }

    pub fn source_changed(message: impl Into<String>) -> Self {
        Self::new(StatusCode::CONFLICT, "source_changed", message)
    }

    pub fn too_large(message: impl Into<String>) -> Self {
        Self::new(StatusCode::PAYLOAD_TOO_LARGE, "content_too_large", message)
    }

    pub fn service_unavailable(message: impl Into<String>) -> Self {
        Self::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "source_unavailable",
            message,
        )
    }

    fn internal() -> Self {
        Self::new(
            StatusCode::INTERNAL_SERVER_ERROR,
            "internal",
            "an internal error occurred",
        )
    }
}

impl IntoResponse for ApiFailure {
    fn into_response(self) -> Response {
        (
            self.status,
            axum::Json(ApiErrorEnvelope {
                error: ApiError {
                    code: self.code.into(),
                    message: self.message,
                    details: self.details,
                },
            }),
        )
            .into_response()
    }
}

impl From<sqlx::Error> for ApiFailure {
    fn from(_error: sqlx::Error) -> Self {
        Self::internal()
    }
}

impl From<serde_json::Error> for ApiFailure {
    fn from(_error: serde_json::Error) -> Self {
        Self::internal()
    }
}
