use std::collections::{BTreeMap, HashMap};
use std::io::{Read as _, Seek as _, SeekFrom};

use axum::Json;
use axum::Router;
use axum::extract::{Path, Query, RawQuery, State};
use axum::response::{IntoResponse, Response};
use axum::routing::get;
use chrono::{SecondsFormat, TimeZone as _, Utc};
use serde::Deserialize;
use sha2::{Digest, Sha256};
use sqlx::{QueryBuilder, Row as _, Sqlite};

use crate::index::search::{ArchiveFilter, SearchFilters, SearchRequest, search as search_index};
use crate::model::{
    ApiPage, ContentChunk, ContentField, Diagnostic, EntryKind, EntryListItem, GitMetadata,
    RawEncoding, RawRecord, RawRecordSummary, RawRefSummary, SearchHit, SessionDetail,
    SessionGroup, SessionSummary, SessionTreeNode, SourceKind, TranscriptEntry,
};
use crate::permissions::open_source_read_only;

use super::{ApiFailure, AppState, cursor};

const MAX_JSON_PAGE_BYTES: usize = 4 * 1024 * 1024;
const DEFAULT_CONTENT_BYTES: usize = 256 * 1024;
const MAX_CONTENT_BYTES: usize = 1024 * 1024;

pub fn router() -> Router<AppState> {
    Router::new()
        .route("/status", get(status))
        .route("/sessions", get(sessions))
        .route("/session-groups", get(session_groups))
        .route("/sessions/{session_id}", get(session_detail))
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

#[derive(Default)]
struct SessionsQuery {
    source: Vec<SourceKind>,
    archived: Option<String>,
    cwd: Option<String>,
    parent: Option<String>,
    limit: Option<usize>,
    cursor: Option<String>,
}

async fn sessions(
    State(state): State<AppState>,
    RawQuery(raw_query): RawQuery,
) -> Result<Json<ApiPage<SessionSummary>>, ApiFailure> {
    let query = parse_sessions_query(raw_query.as_deref())?;
    let limit = bounded_limit(query.limit, 50, 200)?;
    let archived = parse_archive(query.archived.as_deref())?;
    let filters = canonical_session_filters(&query, archived);
    let decoded = query
        .cursor
        .as_deref()
        .map(|value| cursor::decode(value, "sessions", &filters))
        .transpose()?;
    let previous = decoded
        .as_ref()
        .is_some_and(|(_, _, direction)| direction == "previous");
    let mut builder = QueryBuilder::<Sqlite>::new("SELECT * FROM sessions s WHERE 1=1");
    push_session_filters(&mut builder, &query, archived)?;
    if let Some((sort, id, _)) = &decoded {
        if previous {
            builder
                .push(" AND (s.updated_at_micros > ")
                .push_bind(*sort)
                .push(" OR (s.updated_at_micros = ")
                .push_bind(*sort)
                .push(" AND s.id < ")
                .push_bind(id)
                .push("))");
        } else {
            builder
                .push(" AND (s.updated_at_micros < ")
                .push_bind(*sort)
                .push(" OR (s.updated_at_micros = ")
                .push_bind(*sort)
                .push(" AND s.id > ")
                .push_bind(id)
                .push("))");
        }
    }
    if previous {
        builder.push(" ORDER BY s.updated_at_micros ASC, s.id DESC");
    } else {
        builder.push(" ORDER BY s.updated_at_micros DESC, s.id ASC");
    }
    builder.push(" LIMIT ").push_bind(
        i64::try_from(limit + 1).map_err(|_| ApiFailure::invalid("limit is too large"))?,
    );
    let mut rows = builder.build().fetch_all(state.database.pool()).await?;
    let has_more = rows.len() > limit;
    rows.truncate(limit);
    if previous {
        rows.reverse();
    }
    let data = rows
        .iter()
        .map(session_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    let next_cursor = if previous || has_more {
        data.last().map(|item| {
            cursor::encode(
                "sessions",
                &filters,
                micros(&item.updated_at),
                &item.id,
                "next",
            )
        })
    } else {
        None
    };
    let previous_cursor = if decoded.is_some() {
        data.first().map(|item| {
            cursor::encode(
                "sessions",
                &filters,
                micros(&item.updated_at),
                &item.id,
                "previous",
            )
        })
    } else {
        None
    };
    Ok(Json(ApiPage {
        data,
        next_cursor,
        previous_cursor,
        partial: false,
    }))
}

async fn session_groups(
    State(state): State<AppState>,
    RawQuery(raw_query): RawQuery,
) -> Result<Json<ApiPage<SessionGroup>>, ApiFailure> {
    let query = parse_sessions_query(raw_query.as_deref())?;
    let limit = bounded_limit(query.limit, 50, 200)?;
    let archived = parse_archive(query.archived.as_deref())?;
    if let Some(parent) = query.parent.as_deref()
        && parent != "root"
    {
        validate_id(parent)?;
    }
    let filters = canonical_session_filters(&query, archived);
    let decoded = query
        .cursor
        .as_deref()
        .map(|value| cursor::decode(value, "session-groups", &filters))
        .transpose()?;
    let previous = decoded
        .as_ref()
        .is_some_and(|(_, _, direction)| direction == "previous");
    let sessions = sqlx::query("SELECT * FROM sessions")
        .fetch_all(state.database.pool())
        .await?
        .iter()
        .map(session_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    let mut groups = build_session_groups(sessions);
    groups.retain(|group| group_matches(group, &query, archived));
    groups.sort_by(|left, right| {
        micros(&right.updated_at)
            .cmp(&micros(&left.updated_at))
            .then_with(|| left.root.session.id.cmp(&right.root.session.id))
    });

    let mut candidates = match decoded.as_ref() {
        Some((sort, id, _)) if previous => groups
            .into_iter()
            .filter(|group| {
                let updated = micros(&group.updated_at);
                updated > *sort || (updated == *sort && group.root.session.id < *id)
            })
            .collect::<Vec<_>>(),
        Some((sort, id, _)) => groups
            .into_iter()
            .filter(|group| {
                let updated = micros(&group.updated_at);
                updated < *sort || (updated == *sort && group.root.session.id > *id)
            })
            .collect::<Vec<_>>(),
        None => groups,
    };
    let has_more = candidates.len() > limit;
    let data = if previous {
        candidates.split_off(candidates.len().saturating_sub(limit))
    } else {
        candidates.truncate(limit);
        candidates
    };
    let next_cursor = if previous || has_more {
        data.last().map(|group| {
            cursor::encode(
                "session-groups",
                &filters,
                micros(&group.updated_at),
                &group.root.session.id,
                "next",
            )
        })
    } else {
        None
    };
    let previous_cursor = decoded.as_ref().and_then(|_| {
        data.first().map(|group| {
            cursor::encode(
                "session-groups",
                &filters,
                micros(&group.updated_at),
                &group.root.session.id,
                "previous",
            )
        })
    });
    Ok(Json(ApiPage {
        data,
        next_cursor,
        previous_cursor,
        partial: false,
    }))
}

struct BuiltTree {
    node: SessionTreeNode,
    updated_at_micros: i64,
    latest_created_at_micros: i64,
    latest_session_id: String,
}

fn build_session_groups(sessions: Vec<SessionSummary>) -> Vec<SessionGroup> {
    let sessions = sessions
        .into_iter()
        .map(|session| (session.id.clone(), session))
        .collect::<HashMap<_, _>>();
    let mut parents = sessions
        .iter()
        .map(|(id, session)| {
            let parent = session
                .parent_thread_id
                .as_ref()
                .filter(|parent| *parent != id && sessions.contains_key(*parent))
                .cloned();
            (id.clone(), parent)
        })
        .collect::<HashMap<_, _>>();
    break_parent_cycles(&mut parents);
    let mut children = HashMap::<String, Vec<String>>::new();
    for (id, parent) in &parents {
        if let Some(parent) = parent {
            children.entry(parent.clone()).or_default().push(id.clone());
        }
    }
    let mut roots = parents
        .iter()
        .filter_map(|(id, parent)| parent.is_none().then_some(id.clone()))
        .collect::<Vec<_>>();
    roots.sort();
    roots
        .into_iter()
        .map(|root| {
            let built = build_session_tree(&root, &sessions, &children);
            SessionGroup {
                root: built.node,
                latest_session_id: built.latest_session_id,
                updated_at: format_time(built.updated_at_micros),
            }
        })
        .collect()
}

fn break_parent_cycles(parents: &mut HashMap<String, Option<String>>) {
    let mut starts = parents.keys().cloned().collect::<Vec<_>>();
    starts.sort();
    for start in starts {
        let mut path = Vec::<String>::new();
        let mut positions = HashMap::<String, usize>::new();
        let mut current = start;
        loop {
            if let Some(position) = positions.get(&current).copied() {
                if let Some(root) = path[position..].iter().min().cloned() {
                    parents.insert(root, None);
                }
                break;
            }
            positions.insert(current.clone(), path.len());
            path.push(current.clone());
            let Some(parent) = parents.get(&current).and_then(Clone::clone) else {
                break;
            };
            current = parent;
        }
    }
}

fn build_session_tree(
    id: &str,
    sessions: &HashMap<String, SessionSummary>,
    children: &HashMap<String, Vec<String>>,
) -> BuiltTree {
    let session = sessions
        .get(id)
        .expect("tree IDs originate from the session map")
        .clone();
    let mut built_children = children
        .get(id)
        .into_iter()
        .flatten()
        .map(|child| build_session_tree(child, sessions, children))
        .collect::<Vec<_>>();
    built_children.sort_by(|left, right| {
        right
            .updated_at_micros
            .cmp(&left.updated_at_micros)
            .then_with(|| {
                right
                    .latest_created_at_micros
                    .cmp(&left.latest_created_at_micros)
            })
            .then_with(|| left.node.session.id.cmp(&right.node.session.id))
    });
    let mut updated_at_micros = micros(&session.updated_at);
    let mut latest_created_at_micros = micros(&session.created_at);
    let mut latest_session_id = session.id.clone();
    for child in &built_children {
        if child.updated_at_micros > updated_at_micros
            || (child.updated_at_micros == updated_at_micros
                && (child.latest_created_at_micros > latest_created_at_micros
                    || (child.latest_created_at_micros == latest_created_at_micros
                        && child.latest_session_id < latest_session_id)))
        {
            updated_at_micros = child.updated_at_micros;
            latest_created_at_micros = child.latest_created_at_micros;
            latest_session_id.clone_from(&child.latest_session_id);
        }
    }
    BuiltTree {
        node: SessionTreeNode {
            session,
            children: built_children.into_iter().map(|child| child.node).collect(),
        },
        updated_at_micros,
        latest_created_at_micros,
        latest_session_id,
    }
}

fn group_matches(group: &SessionGroup, query: &SessionsQuery, archived: ArchiveFilter) -> bool {
    fn node_matches(
        node: &SessionTreeNode,
        query: &SessionsQuery,
        archived: ArchiveFilter,
    ) -> bool {
        let session = &node.session;
        let source_matches = query.source.is_empty() || query.source.contains(&session.source);
        let archive_matches = match archived {
            ArchiveFilter::Exclude => !session.archived,
            ArchiveFilter::Only => session.archived,
            ArchiveFilter::Include => true,
        };
        let cwd_matches = query
            .cwd
            .as_ref()
            .is_none_or(|cwd| session.cwd.as_ref() == Some(cwd));
        let parent_matches = query.parent.as_ref().is_none_or(|parent| {
            if parent == "root" {
                session.parent_thread_id.is_none()
            } else {
                session.parent_thread_id.as_ref() == Some(parent)
            }
        });
        (source_matches && archive_matches && cwd_matches && parent_matches)
            || node
                .children
                .iter()
                .any(|child| node_matches(child, query, archived))
    }
    node_matches(&group.root, query, archived)
}

async fn session_detail(
    State(state): State<AppState>,
    Path(session_id): Path<String>,
) -> Result<Json<SessionDetail>, ApiFailure> {
    validate_id(&session_id)?;
    let row = sqlx::query("SELECT * FROM sessions WHERE id = ?")
        .bind(&session_id)
        .fetch_optional(state.database.pool())
        .await?
        .ok_or_else(|| ApiFailure::not_found("session does not exist"))?;
    let diagnostics =
        sqlx::query("SELECT * FROM diagnostics WHERE session_id = ? ORDER BY severity DESC, id")
            .bind(&session_id)
            .fetch_all(state.database.pool())
            .await?
            .iter()
            .map(diagnostic_from_row)
            .collect::<Result<Vec<_>, _>>()?;
    Ok(Json(SessionDetail {
        summary: session_from_row(&row)?,
        diagnostics,
    }))
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase")]
struct EntriesQuery {
    limit: Option<usize>,
    cursor: Option<String>,
    direction: Option<String>,
    around_entry_id: Option<String>,
    #[serde(default)]
    include_technical: bool,
}

async fn entries(
    State(state): State<AppState>,
    Path(session_id): Path<String>,
    Query(query): Query<EntriesQuery>,
) -> Result<Json<ApiPage<EntryListItem>>, ApiFailure> {
    validate_id(&session_id)?;
    if query.cursor.is_some() && query.around_entry_id.is_some() {
        return Err(ApiFailure::invalid(
            "aroundEntryId and cursor are mutually exclusive",
        ));
    }
    let limit = bounded_limit(query.limit, 100, 500)?;
    let direction = query.direction.as_deref().unwrap_or("forward");
    if !matches!(direction, "forward" | "backward") {
        return Err(ApiFailure::invalid("direction must be forward or backward"));
    }
    let filters = format!(
        "session={session_id};include_technical={}",
        query.include_technical
    );
    let decoded = query
        .cursor
        .as_deref()
        .map(|value| cursor::decode(value, "entries", &filters))
        .transpose()?;
    let anchor = if let Some(around) = &query.around_entry_id {
        Some(
            sqlx::query_scalar::<_, i64>(
                "SELECT sequence FROM entries WHERE session_id = ? AND id = ?",
            )
            .bind(&session_id)
            .bind(around)
            .fetch_optional(state.database.pool())
            .await?
            .ok_or_else(|| ApiFailure::not_found("aroundEntryId does not exist in this session"))?,
        )
    } else {
        None
    };
    let mut builder = QueryBuilder::<Sqlite>::new(
        "SELECT e.*, (SELECT COUNT(*) FROM entry_raw_refs x WHERE x.entry_id=e.id) AS raw_ref_count FROM entries e WHERE e.session_id = ",
    );
    builder.push_bind(&session_id);
    push_entry_visibility(&mut builder, query.include_technical);
    let backward = direction == "backward"
        || decoded
            .as_ref()
            .is_some_and(|(_, _, cursor_direction)| cursor_direction == "previous");
    if let Some(anchor) = anchor {
        builder
            .push(" AND e.sequence >= ")
            .push_bind(anchor.saturating_sub(i64::try_from(limit / 2).unwrap_or_default()));
    } else if let Some((sequence, id, _)) = &decoded {
        if backward {
            builder
                .push(" AND (e.sequence < ")
                .push_bind(*sequence)
                .push(" OR (e.sequence = ")
                .push_bind(*sequence)
                .push(" AND e.id < ")
                .push_bind(id)
                .push("))");
        } else {
            builder
                .push(" AND (e.sequence > ")
                .push_bind(*sequence)
                .push(" OR (e.sequence = ")
                .push_bind(*sequence)
                .push(" AND e.id > ")
                .push_bind(id)
                .push("))");
        }
    }
    builder.push(if backward {
        " ORDER BY e.sequence DESC, e.id DESC"
    } else {
        " ORDER BY e.sequence ASC, e.id ASC"
    });
    builder
        .push(" LIMIT ")
        .push_bind(i64::try_from(limit + 1).map_err(|_| ApiFailure::invalid("limit too large"))?);
    let mut rows = builder.build().fetch_all(state.database.pool()).await?;
    rows.truncate(limit);
    if backward {
        rows.reverse();
    }
    let mut data = Vec::with_capacity(rows.len());
    for row in &rows {
        let item = entry_item_from_row(row)?;
        data.push(item);
        if serde_json::to_vec(&data)?.len() > MAX_JSON_PAGE_BYTES {
            data.pop();
            break;
        }
    }
    let previous_cursor = if let Some(first) = data.first()
        && entry_exists(
            state.database.pool(),
            &session_id,
            query.include_technical,
            first.sequence,
            &first.id,
            true,
        )
        .await?
    {
        Some(cursor::encode(
            "entries",
            &filters,
            first.sequence,
            &first.id,
            "previous",
        ))
    } else {
        None
    };
    let next_cursor = if let Some(last) = data.last()
        && entry_exists(
            state.database.pool(),
            &session_id,
            query.include_technical,
            last.sequence,
            &last.id,
            false,
        )
        .await?
    {
        Some(cursor::encode(
            "entries",
            &filters,
            last.sequence,
            &last.id,
            "next",
        ))
    } else {
        None
    };
    Ok(Json(ApiPage {
        data,
        next_cursor,
        previous_cursor,
        partial: false,
    }))
}

fn push_entry_visibility(builder: &mut QueryBuilder<Sqlite>, include_technical: bool) {
    if !include_technical {
        builder.push(
            " AND ((e.kind = 'message' AND e.presentation IN ('user', 'response')) \
             OR e.kind = 'reasoning' \
             OR (e.kind = 'tool' AND e.tool_kind IN ('command', 'requestUserInput')) \
             OR e.kind IN ('warning', 'error'))",
        );
    }
}

async fn entry_exists(
    pool: &sqlx::SqlitePool,
    session_id: &str,
    include_technical: bool,
    sequence: i64,
    id: &str,
    before: bool,
) -> Result<bool, ApiFailure> {
    let mut builder =
        QueryBuilder::<Sqlite>::new("SELECT EXISTS(SELECT 1 FROM entries e WHERE e.session_id = ");
    builder.push_bind(session_id);
    push_entry_visibility(&mut builder, include_technical);
    builder.push(if before {
        " AND (e.sequence < "
    } else {
        " AND (e.sequence > "
    });
    builder.push_bind(sequence);
    builder.push(" OR (e.sequence = ");
    builder.push_bind(sequence);
    builder.push(if before {
        " AND e.id < "
    } else {
        " AND e.id > "
    });
    builder.push_bind(id);
    builder.push(")))");
    Ok(builder.build_query_scalar::<i64>().fetch_one(pool).await? != 0)
}

async fn entry_detail(
    State(state): State<AppState>,
    Path((session_id, entry_id)): Path<(String, String)>,
) -> Result<Json<TranscriptEntry>, ApiFailure> {
    let row = entry_row(&state, &session_id, &entry_id).await?;
    let raw_refs = sqlx::query("SELECT r.* FROM raw_records r JOIN entry_raw_refs x ON x.raw_id=r.id WHERE x.entry_id=? ORDER BY x.ordinal")
        .bind(&entry_id).fetch_all(state.database.pool()).await?
        .iter().map(raw_ref_from_row).collect::<Result<Vec<_>, _>>()?;
    let metadata = serde_json::from_str::<BTreeMap<String, serde_json::Value>>(
        &row.get::<String, _>("metadata_json"),
    )?;
    Ok(Json(TranscriptEntry {
        item: entry_item_from_row(&row)?,
        derived_metadata: metadata,
        raw_refs,
    }))
}

#[derive(Deserialize)]
struct ContentQuery {
    field: Option<ContentField>,
    offset: Option<u64>,
    limit: Option<usize>,
}

async fn entry_content(
    State(state): State<AppState>,
    Path((session_id, entry_id)): Path<(String, String)>,
    Query(query): Query<ContentQuery>,
) -> Result<Json<ContentChunk>, ApiFailure> {
    let row = entry_row(&state, &session_id, &entry_id).await?;
    let field = query.field.unwrap_or(ContentField::Primary);
    let text: String = match field {
        ContentField::Primary => row.get("primary_text"),
        ContentField::Secondary => row.get("secondary_text"),
    };
    Ok(Json(text_chunk(
        field,
        &text,
        query.offset.unwrap_or(0),
        bounded_content(query.limit)?,
    )?))
}

#[derive(Default, Deserialize)]
struct RawListQuery {
    limit: Option<usize>,
    cursor: Option<String>,
}

async fn raw_list(
    State(state): State<AppState>,
    Path(session_id): Path<String>,
    Query(query): Query<RawListQuery>,
) -> Result<Json<ApiPage<RawRecordSummary>>, ApiFailure> {
    validate_id(&session_id)?;
    let limit = bounded_limit(query.limit, 100, 500)?;
    let filters = format!("session={session_id}");
    let decoded = query
        .cursor
        .as_deref()
        .map(|value| cursor::decode(value, "raw", &filters))
        .transpose()?;
    let mut builder = QueryBuilder::<Sqlite>::new("SELECT * FROM raw_records WHERE session_id = ");
    builder.push_bind(&session_id);
    if let Some((line, id, _)) = &decoded {
        builder
            .push(" AND (line_no > ")
            .push_bind(*line)
            .push(" OR (line_no = ")
            .push_bind(*line)
            .push(" AND id > ")
            .push_bind(id)
            .push("))");
    }
    builder
        .push(" ORDER BY line_no, id LIMIT ")
        .push_bind(i64::try_from(limit + 1).map_err(|_| ApiFailure::invalid("limit too large"))?);
    let mut rows = builder.build().fetch_all(state.database.pool()).await?;
    let has_more = rows.len() > limit;
    rows.truncate(limit);
    let data = rows
        .iter()
        .map(raw_summary_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    let next_cursor = has_more
        .then(|| {
            data.last().map(|item| {
                cursor::encode(
                    "raw",
                    &filters,
                    i64::try_from(item.line).unwrap_or(i64::MAX),
                    &item.id,
                    "next",
                )
            })
        })
        .flatten();
    Ok(Json(ApiPage {
        data,
        next_cursor,
        previous_cursor: None,
        partial: false,
    }))
}

async fn raw_record(
    State(state): State<AppState>,
    Path((session_id, raw_id)): Path<(String, String)>,
    Query(query): Query<ContentQuery>,
) -> Result<Json<RawRecord>, ApiFailure> {
    validate_id(&session_id)?;
    validate_id(&raw_id)?;
    let row = sqlx::query("SELECT r.*, f.root_kind, f.relative_path, f.file_key FROM raw_records r JOIN source_files f ON f.id=r.source_file_id WHERE r.session_id=? AND r.id=?")
        .bind(&session_id).bind(&raw_id).fetch_optional(state.database.pool()).await?
        .ok_or_else(|| ApiFailure::not_found("raw record does not exist"))?;
    if row.get::<bool, _>("oversize") {
        return Err(ApiFailure::too_large(
            "raw record exceeds the configured event size",
        ));
    }
    let root_kind = row.get::<String, _>("root_kind");
    let root = match root_kind.as_str() {
        "active" => state.roots.active.as_ref(),
        "archived" => state.roots.archived.as_ref(),
        _ => None,
    }
    .ok_or_else(|| ApiFailure::service_unavailable("source root is unavailable"))?;
    let path = root.join(row.get::<String, _>("relative_path"));
    let mut opened = open_source_read_only(root, &path)
        .map_err(|_| ApiFailure::source_changed("source file changed or became unavailable"))?;
    if opened.identity.file_key != row.get::<String, _>("file_key") {
        return Err(ApiFailure::source_changed("source file identity changed"));
    }
    let byte_offset =
        u64::try_from(row.get::<i64, _>("byte_offset")).map_err(|_| ApiFailure::internal())?;
    let byte_length =
        u64::try_from(row.get::<i64, _>("byte_length")).map_err(|_| ApiFailure::internal())?;
    opened
        .file
        .seek(SeekFrom::Start(byte_offset))
        .map_err(|_| ApiFailure::source_changed("source record cannot be read"))?;
    let mut bytes = vec![
        0;
        usize::try_from(byte_length)
            .map_err(|_| ApiFailure::too_large("raw record is too large"))?
    ];
    opened
        .file
        .read_exact(&mut bytes)
        .map_err(|_| ApiFailure::source_changed("source record changed"))?;
    if sha256_hex(&bytes) != row.get::<String, _>("content_hash") {
        return Err(ApiFailure::source_changed("source record content changed"));
    }
    let text = if row.get::<bool, _>("utf8") {
        String::from_utf8(bytes)
            .map_err(|_| ApiFailure::source_changed("source encoding changed"))?
    } else {
        row.get::<Option<String>, _>("hex_preview")
            .unwrap_or_default()
    };
    let chunk = text_chunk(
        ContentField::Primary,
        &text,
        query.offset.unwrap_or(0),
        bounded_content(query.limit)?,
    )?;
    Ok(Json(RawRecord {
        summary: raw_summary_from_row(&row)?,
        chunk,
    }))
}

#[derive(Default)]
struct SearchQuery {
    q: Option<String>,
    limit: Option<usize>,
    session: Option<String>,
    source: Vec<SourceKind>,
    kind: Vec<EntryKind>,
    from: Option<String>,
    to: Option<String>,
    archived: Option<String>,
    all_types: Option<bool>,
}

async fn search(
    State(state): State<AppState>,
    RawQuery(raw_query): RawQuery,
) -> Result<Json<ApiPage<SearchHit>>, ApiFailure> {
    let query = parse_search_query(raw_query.as_deref())?;
    let q = query
        .q
        .ok_or_else(|| ApiFailure::invalid("q is required"))?;
    let limit = bounded_limit(query.limit, 50, 200)?;
    let filters = SearchFilters {
        session_id: query.session,
        sources: query.source,
        kinds: query.kind,
        from_micros: query.from.as_deref().map(parse_time).transpose()?,
        to_micros: query.to.as_deref().map(parse_time).transpose()?,
        archived: parse_archive(query.archived.as_deref())?,
        all_types: query.all_types.unwrap_or(false),
    };
    let result = search_index(
        &state.database,
        &SearchRequest {
            query: q,
            limit,
            filters,
        },
    )
    .await
    .map_err(|error| ApiFailure::invalid(error.to_string()))?;
    let mut hits = Vec::with_capacity(result.hits.len());
    for hit in result.hits {
        let row = sqlx::query("SELECT * FROM sessions WHERE id=?")
            .bind(&hit.session_id)
            .fetch_one(state.database.pool())
            .await?;
        hits.push(SearchHit {
            session: session_from_row(&row)?,
            entry_id: hit.entry_id,
            kind: hit.kind,
            snippet: hit.snippet,
            match_ranges: hit.match_ranges,
            field: hit.field,
            rank: hit.rank,
            timestamp: hit.timestamp_micros.map(format_time),
        });
    }
    let indexing = !matches!(
        state.status.read().await.phase,
        crate::model::ServicePhase::Ready
    );
    Ok(Json(ApiPage {
        data: hits,
        next_cursor: None,
        previous_cursor: None,
        partial: result.partial || indexing,
    }))
}

async fn events(
    State(state): State<AppState>,
    headers: http::HeaderMap,
) -> Result<Response, ApiFailure> {
    let last = headers
        .get("last-event-id")
        .map(|value| {
            value
                .to_str()
                .map_err(|_| ApiFailure::invalid("Last-Event-ID is invalid"))
        })
        .transpose()?
        .map(|value| {
            value
                .parse::<u64>()
                .map_err(|_| ApiFailure::invalid("Last-Event-ID must be an integer"))
        })
        .transpose()?;
    Ok(state.sse.subscribe(last).await?.into_response())
}

async fn entry_row(
    state: &AppState,
    session_id: &str,
    entry_id: &str,
) -> Result<sqlx::sqlite::SqliteRow, ApiFailure> {
    validate_id(session_id)?;
    validate_id(entry_id)?;
    sqlx::query("SELECT e.*, (SELECT COUNT(*) FROM entry_raw_refs x WHERE x.entry_id=e.id) AS raw_ref_count FROM entries e WHERE e.session_id=? AND e.id=?")
        .bind(session_id).bind(entry_id).fetch_optional(state.database.pool()).await?
        .ok_or_else(|| ApiFailure::not_found("entry does not exist"))
}

fn session_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<SessionSummary, ApiFailure> {
    let branch: Option<String> = row.get("git_branch");
    let commit: Option<String> = row.get("git_commit");
    Ok(SessionSummary {
        id: row.get("id"),
        source: decode(row, "source_kind")?,
        parent_thread_id: row.get("parent_thread_id"),
        parent_relation: decode_optional(row, "parent_relation")?,
        cwd: row.get("cwd"),
        title: row.get("title"),
        preview: row.get("preview"),
        created_at: format_time(row.get("created_at_micros")),
        updated_at: format_time(row.get("updated_at_micros")),
        archived: row.get("archived"),
        cli_version: row.get("cli_version"),
        provider: row.get("provider"),
        git: (branch.is_some() || commit.is_some()).then_some(GitMetadata { branch, commit }),
        entry_count: u64::try_from(row.get::<i64, _>("entry_count"))
            .map_err(|_| ApiFailure::internal())?,
        diagnostic_count: u64::try_from(row.get::<i64, _>("diagnostic_count"))
            .map_err(|_| ApiFailure::internal())?,
        index_state: decode(row, "index_state")?,
        completeness: decode(row, "completeness")?,
    })
}

fn entry_item_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<EntryListItem, ApiFailure> {
    let kind: EntryKind = decode(row, "kind")?;
    let primary: String = row.get("primary_text");
    let secondary: String = row.get("secondary_text");
    let (primary_limit, secondary_limit) = if kind == EntryKind::Tool {
        (8 * 1024, 8 * 1024)
    } else {
        (16 * 1024, 0)
    };
    let primary_preview = utf8_prefix(&primary, primary_limit);
    let secondary_preview = utf8_prefix(&secondary, secondary_limit);
    Ok(EntryListItem {
        id: row.get("id"),
        session_id: row.get("session_id"),
        sequence: row.get("sequence"),
        timestamp: row
            .get::<Option<i64>, _>("timestamp_micros")
            .map(format_time),
        kind,
        presentation: decode(row, "presentation")?,
        role: decode_optional(row, "role")?,
        phase: decode_optional(row, "phase")?,
        tool_kind: decode_optional(row, "tool_kind")?,
        tool_status: decode_optional(row, "tool_status")?,
        title: row.get("title"),
        primary_complete: primary_preview.len() == primary.len(),
        secondary_complete: secondary_preview.len() == secondary.len(),
        primary_preview,
        secondary_preview,
        primary_bytes: u64::try_from(row.get::<i64, _>("primary_bytes"))
            .map_err(|_| ApiFailure::internal())?,
        secondary_bytes: u64::try_from(row.get::<i64, _>("secondary_bytes"))
            .map_err(|_| ApiFailure::internal())?,
        default_collapsed: row.get("default_collapsed"),
        metadata: serde_json::from_str(&row.get::<String, _>("metadata_json"))?,
        raw_ref_count: u64::try_from(row.get::<i64, _>("raw_ref_count"))
            .map_err(|_| ApiFailure::internal())?,
    })
}

fn diagnostic_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<Diagnostic, ApiFailure> {
    Ok(Diagnostic {
        id: row.get("id"),
        severity: decode(row, "severity")?,
        code: row.get("code"),
        message: row.get("message"),
        first_seen_at: format_time(row.get("first_seen_at_micros")),
        last_seen_at: format_time(row.get("last_seen_at_micros")),
        count: u64::try_from(row.get::<i64, _>("count")).map_err(|_| ApiFailure::internal())?,
    })
}
fn raw_ref_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<RawRefSummary, ApiFailure> {
    Ok(RawRefSummary {
        id: row.get("id"),
        line: to_u64(row, "line_no")?,
        byte_offset: to_u64(row, "byte_offset")?,
        byte_length: to_u64(row, "byte_length")?,
        envelope_type: row.get("envelope_type"),
    })
}
fn raw_summary_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<RawRecordSummary, ApiFailure> {
    Ok(RawRecordSummary {
        id: row.get("id"),
        session_id: row.get("session_id"),
        line: to_u64(row, "line_no")?,
        byte_offset: to_u64(row, "byte_offset")?,
        byte_length: to_u64(row, "byte_length")?,
        envelope_type: row.get("envelope_type"),
        parse_status: decode(row, "parse_status")?,
        encoding: if row.get::<bool, _>("utf8") {
            RawEncoding::Utf8
        } else {
            RawEncoding::Binary
        },
        oversize: row.get("oversize"),
    })
}

fn text_chunk(
    field: ContentField,
    text: &str,
    requested_offset: u64,
    limit: usize,
) -> Result<ContentChunk, ApiFailure> {
    let total = text.len();
    let mut start = usize::try_from(requested_offset)
        .map_err(|_| ApiFailure::invalid("offset is too large"))?
        .min(total);
    while start < total && !text.is_char_boundary(start) {
        start += 1;
    }
    let mut end = start.saturating_add(limit).min(total);
    while end > start && !text.is_char_boundary(end) {
        end -= 1;
    }
    Ok(ContentChunk {
        field,
        text: text[start..end].to_owned(),
        byte_offset: u64::try_from(start).map_err(|_| ApiFailure::internal())?,
        next_offset: (end < total).then(|| u64::try_from(end).unwrap_or(u64::MAX)),
        total_bytes: u64::try_from(total).map_err(|_| ApiFailure::internal())?,
        complete: start == 0 && end == total,
    })
}

fn push_session_filters(
    builder: &mut QueryBuilder<Sqlite>,
    query: &SessionsQuery,
    archived: ArchiveFilter,
) -> Result<(), ApiFailure> {
    if !query.source.is_empty() {
        builder.push(" AND s.source_kind IN (");
        let mut separated = builder.separated(",");
        for source in &query.source {
            separated.push_bind(enum_string(source)?);
        }
        separated.push_unseparated(")");
    }
    match archived {
        ArchiveFilter::Exclude => {
            builder.push(" AND s.archived=0");
        }
        ArchiveFilter::Only => {
            builder.push(" AND s.archived=1");
        }
        ArchiveFilter::Include => {}
    }
    if let Some(cwd) = &query.cwd {
        builder.push(" AND s.cwd=").push_bind(cwd);
    }
    if let Some(parent) = &query.parent {
        if parent == "root" {
            builder.push(" AND s.parent_thread_id IS NULL");
        } else {
            validate_id(parent)?;
            builder.push(" AND s.parent_thread_id=").push_bind(parent);
        }
    }
    Ok(())
}

fn canonical_session_filters(query: &SessionsQuery, archived: ArchiveFilter) -> String {
    let mut sources = query
        .source
        .iter()
        .map(|value| enum_string(value).unwrap_or_default())
        .collect::<Vec<_>>();
    sources.sort();
    format!(
        "source={};archived={archived:?};cwd={};parent={}",
        sources.join(","),
        query.cwd.as_deref().unwrap_or(""),
        query.parent.as_deref().unwrap_or("")
    )
}
fn parse_archive(value: Option<&str>) -> Result<ArchiveFilter, ApiFailure> {
    match value.unwrap_or("exclude") {
        "exclude" => Ok(ArchiveFilter::Exclude),
        "only" => Ok(ArchiveFilter::Only),
        "include" => Ok(ArchiveFilter::Include),
        _ => Err(ApiFailure::invalid(
            "archived must be exclude, only, or include",
        )),
    }
}
fn bounded_limit(value: Option<usize>, default: usize, max: usize) -> Result<usize, ApiFailure> {
    let value = value.unwrap_or(default);
    if value == 0 || value > max {
        Err(ApiFailure::invalid(format!(
            "limit must be between 1 and {max}"
        )))
    } else {
        Ok(value)
    }
}
fn bounded_content(value: Option<usize>) -> Result<usize, ApiFailure> {
    let value = value.unwrap_or(DEFAULT_CONTENT_BYTES);
    if value == 0 || value > MAX_CONTENT_BYTES {
        Err(ApiFailure::invalid(
            "content limit must be between 1 and 1048576",
        ))
    } else {
        Ok(value)
    }
}
fn validate_id(value: &str) -> Result<(), ApiFailure> {
    if value.is_empty() || value.len() > 512 || value.chars().any(char::is_control) {
        Err(ApiFailure::invalid("ID is invalid"))
    } else {
        Ok(())
    }
}
fn parse_time(value: &str) -> Result<i64, ApiFailure> {
    chrono::DateTime::parse_from_rfc3339(value)
        .map(|time| time.timestamp_micros())
        .map_err(|_| ApiFailure::invalid("time filter must be RFC3339"))
}
fn format_time(value: i64) -> String {
    Utc.timestamp_micros(value).single().map_or_else(
        || "1970-01-01T00:00:00Z".into(),
        |time| time.to_rfc3339_opts(SecondsFormat::Micros, true),
    )
}
fn micros(value: &str) -> i64 {
    chrono::DateTime::parse_from_rfc3339(value).map_or(0, |time| time.timestamp_micros())
}
fn enum_string<T: serde::Serialize>(value: &T) -> Result<String, ApiFailure> {
    serde_json::to_value(value)?
        .as_str()
        .map(str::to_owned)
        .ok_or_else(ApiFailure::internal)
}
fn decode<T: serde::de::DeserializeOwned>(
    row: &sqlx::sqlite::SqliteRow,
    field: &str,
) -> Result<T, ApiFailure> {
    Ok(serde_json::from_value(serde_json::Value::String(
        row.get(field),
    ))?)
}
fn decode_optional<T: serde::de::DeserializeOwned>(
    row: &sqlx::sqlite::SqliteRow,
    field: &str,
) -> Result<Option<T>, ApiFailure> {
    row.get::<Option<String>, _>(field)
        .map(|value| {
            serde_json::from_value(serde_json::Value::String(value)).map_err(ApiFailure::from)
        })
        .transpose()
}
fn to_u64(row: &sqlx::sqlite::SqliteRow, field: &str) -> Result<u64, ApiFailure> {
    u64::try_from(row.get::<i64, _>(field)).map_err(|_| ApiFailure::internal())
}
fn utf8_prefix(value: &str, max: usize) -> String {
    if value.len() <= max {
        return value.to_owned();
    }
    let mut end = max;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_owned()
}
fn sha256_hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
fn database_family_bytes(path: &std::path::Path) -> u64 {
    ["", "-wal", "-shm"]
        .iter()
        .filter_map(|suffix| std::fs::metadata(format!("{}{}", path.display(), suffix)).ok())
        .map(|metadata| metadata.len())
        .sum()
}

fn parse_sessions_query(raw: Option<&str>) -> Result<SessionsQuery, ApiFailure> {
    let mut query = SessionsQuery::default();
    for (key, value) in query_pairs(raw)? {
        match key.as_str() {
            "source" => query.source.push(decode_string_enum(&value)?),
            "archived" => set_once(&mut query.archived, value, "archived")?,
            "cwd" => set_once(&mut query.cwd, value, "cwd")?,
            "parent" => set_once(&mut query.parent, value, "parent")?,
            "limit" => set_once(&mut query.limit, parse_usize(&value, "limit")?, "limit")?,
            "cursor" => set_once(&mut query.cursor, value, "cursor")?,
            _ => {
                return Err(ApiFailure::invalid(format!(
                    "unknown query parameter: {key}"
                )));
            }
        }
    }
    Ok(query)
}

fn parse_search_query(raw: Option<&str>) -> Result<SearchQuery, ApiFailure> {
    let mut query = SearchQuery::default();
    for (key, value) in query_pairs(raw)? {
        match key.as_str() {
            "q" => set_once(&mut query.q, value, "q")?,
            "limit" => set_once(&mut query.limit, parse_usize(&value, "limit")?, "limit")?,
            "session" => set_once(&mut query.session, value, "session")?,
            "source" => query.source.push(decode_string_enum(&value)?),
            "kind" => query.kind.push(decode_string_enum(&value)?),
            "from" => set_once(&mut query.from, value, "from")?,
            "to" => set_once(&mut query.to, value, "to")?,
            "archived" => set_once(&mut query.archived, value, "archived")?,
            "allTypes" => set_once(
                &mut query.all_types,
                parse_bool(&value, "allTypes")?,
                "allTypes",
            )?,
            _ => {
                return Err(ApiFailure::invalid(format!(
                    "unknown query parameter: {key}"
                )));
            }
        }
    }
    Ok(query)
}

fn query_pairs(raw: Option<&str>) -> Result<Vec<(String, String)>, ApiFailure> {
    raw.unwrap_or_default()
        .split('&')
        .filter(|pair| !pair.is_empty())
        .map(|pair| {
            let (key, value) = pair.split_once('=').unwrap_or((pair, ""));
            Ok((percent_decode(key)?, percent_decode(value)?))
        })
        .collect()
}

fn percent_decode(value: &str) -> Result<String, ApiFailure> {
    let bytes = value.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'+' => decoded.push(b' '),
            b'%' if index + 2 < bytes.len() => {
                let high = hex_digit(bytes[index + 1])?;
                let low = hex_digit(bytes[index + 2])?;
                decoded.push((high << 4) | low);
                index += 2;
            }
            b'%' => {
                return Err(ApiFailure::invalid(
                    "query contains incomplete percent encoding",
                ));
            }
            byte => decoded.push(byte),
        }
        index += 1;
    }
    String::from_utf8(decoded).map_err(|_| ApiFailure::invalid("query is not valid UTF-8"))
}

fn hex_digit(value: u8) -> Result<u8, ApiFailure> {
    match value {
        b'0'..=b'9' => Ok(value - b'0'),
        b'a'..=b'f' => Ok(value - b'a' + 10),
        b'A'..=b'F' => Ok(value - b'A' + 10),
        _ => Err(ApiFailure::invalid(
            "query contains invalid percent encoding",
        )),
    }
}

fn set_once<T>(slot: &mut Option<T>, value: T, name: &str) -> Result<(), ApiFailure> {
    if slot.replace(value).is_some() {
        Err(ApiFailure::invalid(format!("{name} may only appear once")))
    } else {
        Ok(())
    }
}

fn parse_usize(value: &str, name: &str) -> Result<usize, ApiFailure> {
    value
        .parse()
        .map_err(|_| ApiFailure::invalid(format!("{name} must be a positive integer")))
}

fn parse_bool(value: &str, name: &str) -> Result<bool, ApiFailure> {
    match value {
        "true" => Ok(true),
        "false" => Ok(false),
        _ => Err(ApiFailure::invalid(format!("{name} must be true or false"))),
    }
}

fn decode_string_enum<T: serde::de::DeserializeOwned>(value: &str) -> Result<T, ApiFailure> {
    Ok(serde_json::from_value(serde_json::Value::String(
        value.to_owned(),
    ))?)
}
