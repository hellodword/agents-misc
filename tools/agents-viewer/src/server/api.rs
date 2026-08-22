use std::collections::{BTreeMap, BTreeSet, HashMap};
use std::io::{Read as _, Seek as _, SeekFrom};

use axum::Json;
use axum::Router;
use axum::extract::{Path, Query, RawQuery, State};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, put};
use chrono::{SecondsFormat, TimeZone as _, Utc};
use http::StatusCode;
use serde::Deserialize;
use sha2::{Digest, Sha256};
use sqlx::{QueryBuilder, Row as _, Sqlite};

use crate::index::coordinator::CoordinatorError;
use crate::index::search::{ArchiveFilter, SearchFilters, SearchRequest, search as search_index};
use crate::model::{
    ApiPage, ContentChunk, ContentField, Diagnostic, EntryKind, EntryListItem, GitMetadata,
    RawEncoding, RawRecord, RawRecordSummary, RawRefSummary, SearchHit, SessionDetail,
    SessionFreshness, SessionGroup, SessionSummary, SessionSyncState, SessionSyncStatus,
    SessionTreeNode, SourceKind, TranscriptEntry,
};
use crate::permissions::open_source_read_only;

use super::{ApiFailure, AppState, cursor};

mod entries;
mod events;
mod query;
mod raw;
mod search;
mod sessions;
#[cfg(test)]
mod tests;

use entries::*;
use events::*;
use query::*;
use raw::*;
use search::*;
use sessions::*;
const MAX_JSON_PAGE_BYTES: usize = 4 * 1024 * 1024;
const DEFAULT_CONTENT_BYTES: usize = 256 * 1024;
const MAX_CONTENT_BYTES: usize = 1024 * 1024;

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/status", get(status))
        .route("/sessions", get(sessions))
        .route("/session-groups", get(session_groups))
        .route("/sessions/{session_id}", get(session_detail))
        .route("/sessions/{session_id}/sync", put(sync_session))
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
    status.initial_index_cutoff = match status.initial_index_days {
        days if days > 0 => chrono::Utc::now()
            .checked_sub_signed(chrono::Duration::days(days))
            .map(|time| time.to_rfc3339_opts(SecondsFormat::Micros, true)),
        -1 => None,
        _ => Some(chrono::Utc::now().to_rfc3339_opts(SecondsFormat::Micros, true)),
    };
    status.database_bytes = database_family_bytes(&state.cache.database);
    Json(status)
}
