use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::convert::Infallible;
use std::io::{Read as _, Seek as _, SeekFrom};

use axum::Json;
use axum::Router;
use axum::extract::{Path, Query, RawQuery, State};
use axum::response::sse::Event;
use axum::response::{IntoResponse, Response, Sse};
use axum::routing::{get, post};
use chrono::{SecondsFormat, TimeZone as _, Utc};
use http::StatusCode;
use serde::Deserialize;
use sqlx::{QueryBuilder, Row as _, Sqlite};

use crate::index::coordinator::CoordinatorError;
use crate::index::search::{ArchiveFilter, SearchFilters, SearchRequest, search as search_index};
use crate::model::{
    ApiPage, ContentChunk, ContentField, ContentFreshness, ContentStatus, Diagnostic, EntryKind,
    EntryListItem, FirstUserMessage, GitMetadata, LiveSyncState, RawEncoding, RawRecord,
    RawRecordSummary, RawRefSummary, SearchHit, SessionDetail, SessionFreshness, SessionGroup,
    SessionSummary, SessionSyncState, SessionTreeNode, SourceKind, SourceLocation, SourceRootKind,
    TranscriptEntry,
};
use crate::permissions::open_source_read_only;

use super::{ApiFailure, AppState, cursor};

mod entries;
mod events;
mod query;
mod raw;
mod search;
pub(crate) mod sessions;
#[cfg(test)]
mod tests;

use entries::*;
use events::*;
use query::*;
use raw::*;
use search::*;
use sessions::*;
const MAX_JSON_PAGE_BYTES: usize = 4 * 1024 * 1024;
const MAX_ENTRY_TITLE_BYTES: usize = 4 * 1024;
const MAX_ENTRY_METADATA_BYTES: usize = 64 * 1024;
const DEFAULT_CONTENT_BYTES: usize = 256 * 1024;
const MAX_CONTENT_BYTES: usize = 1024 * 1024;

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/status", get(status))
        .route("/sessions", get(sessions))
        .route("/session-groups", get(session_groups))
        .route("/sessions/{session_id}", get(session_detail))
        .route("/sessions/{session_id}/live-sync", post(live_sync_session))
        .route("/sessions/{session_id}/entries", get(entries))
        .route(
            "/sessions/{session_id}/entries/{entry_id}",
            get(entry_detail),
        )
        .route(
            "/sessions/{session_id}/entries/{entry_id}/content",
            get(entry_content),
        )
        .route("/sessions/{session_id}/raw", get(raw_list))
        .route("/sessions/{session_id}/raw/{raw_id}", get(raw_record))
        .route("/search", get(search))
        .route("/events", get(events))
}

pub async fn unknown_api() -> ApiFailure {
    ApiFailure::not_found("API endpoint does not exist")
}

async fn status(State(state): State<AppState>) -> Json<crate::model::Status> {
    let mut status = state.status.read().await.clone();
    status.database_bytes = database_family_bytes(&state.cache.database);
    Json(status)
}
