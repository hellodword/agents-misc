use super::*;

pub(super) async fn entry_row(
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

pub(super) fn session_from_row(
    row: &sqlx::sqlite::SqliteRow,
) -> Result<SessionSummary, ApiFailure> {
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
        freshness: SessionFreshness::Current,
    })
}

pub(super) fn apply_freshness(
    state: &AppState,
    sessions: &mut [SessionSummary],
) -> Result<(), ApiFailure> {
    for session in sessions {
        apply_session_freshness(state, session)?;
    }
    Ok(())
}

pub(super) fn apply_session_freshness(
    state: &AppState,
    session: &mut SessionSummary,
) -> Result<(), ApiFailure> {
    if let Some(coordinator) = &state.coordinator {
        session.freshness = coordinator
            .freshness(&session.id)
            .map_err(|error| coordinator_failure(error, &session.id))?;
    }
    Ok(())
}

pub(super) fn coordinator_failure(error: CoordinatorError, session_id: &str) -> ApiFailure {
    if error.is_internal() {
        tracing::error!(%error, %session_id, "session coordination failed");
        ApiFailure::internal()
    } else {
        tracing::warn!(%error, %session_id, "session synchronization could not be queued");
        ApiFailure::service_unavailable("session synchronization queue is full")
    }
}

pub(super) fn entry_item_from_row(
    row: &sqlx::sqlite::SqliteRow,
) -> Result<EntryListItem, ApiFailure> {
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

pub(super) fn diagnostic_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<Diagnostic, ApiFailure> {
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
pub(super) fn raw_ref_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<RawRefSummary, ApiFailure> {
    Ok(RawRefSummary {
        id: row.get("id"),
        line: to_u64(row, "line_no")?,
        byte_offset: to_u64(row, "byte_offset")?,
        byte_length: to_u64(row, "byte_length")?,
        envelope_type: row.get("envelope_type"),
    })
}
pub(super) fn raw_summary_from_row(
    row: &sqlx::sqlite::SqliteRow,
) -> Result<RawRecordSummary, ApiFailure> {
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

pub(super) fn text_chunk(
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

pub(super) fn push_session_filters(
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

pub(super) fn canonical_session_filters(query: &SessionsQuery, archived: ArchiveFilter) -> String {
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
pub(super) fn parse_archive(value: Option<&str>) -> Result<ArchiveFilter, ApiFailure> {
    match value.unwrap_or("exclude") {
        "exclude" => Ok(ArchiveFilter::Exclude),
        "only" => Ok(ArchiveFilter::Only),
        "include" => Ok(ArchiveFilter::Include),
        _ => Err(ApiFailure::invalid(
            "archived must be exclude, only, or include",
        )),
    }
}
pub(super) fn bounded_limit(
    value: Option<usize>,
    default: usize,
    max: usize,
) -> Result<usize, ApiFailure> {
    let value = value.unwrap_or(default);
    if value == 0 || value > max {
        Err(ApiFailure::invalid(format!(
            "limit must be between 1 and {max}"
        )))
    } else {
        Ok(value)
    }
}
pub(super) fn bounded_content(value: Option<usize>) -> Result<usize, ApiFailure> {
    let value = value.unwrap_or(DEFAULT_CONTENT_BYTES);
    if value == 0 || value > MAX_CONTENT_BYTES {
        Err(ApiFailure::invalid(
            "content limit must be between 1 and 1048576",
        ))
    } else {
        Ok(value)
    }
}
pub(super) fn validate_id(value: &str) -> Result<(), ApiFailure> {
    if value.is_empty() || value.len() > 512 || value.chars().any(char::is_control) {
        Err(ApiFailure::invalid("ID is invalid"))
    } else {
        Ok(())
    }
}
pub(super) fn parse_time(value: &str) -> Result<i64, ApiFailure> {
    chrono::DateTime::parse_from_rfc3339(value)
        .map(|time| time.timestamp_micros())
        .map_err(|_| ApiFailure::invalid("time filter must be RFC3339"))
}
pub(super) fn format_time(value: i64) -> String {
    Utc.timestamp_micros(value).single().map_or_else(
        || "1970-01-01T00:00:00Z".into(),
        |time| time.to_rfc3339_opts(SecondsFormat::Micros, true),
    )
}
pub(super) fn micros(value: &str) -> i64 {
    chrono::DateTime::parse_from_rfc3339(value).map_or(0, |time| time.timestamp_micros())
}
pub(super) fn enum_string<T: serde::Serialize>(value: &T) -> Result<String, ApiFailure> {
    serde_json::to_value(value)?
        .as_str()
        .map(str::to_owned)
        .ok_or_else(ApiFailure::internal)
}
pub(super) fn decode<T: serde::de::DeserializeOwned>(
    row: &sqlx::sqlite::SqliteRow,
    field: &str,
) -> Result<T, ApiFailure> {
    Ok(serde_json::from_value(serde_json::Value::String(
        row.get(field),
    ))?)
}
pub(super) fn decode_optional<T: serde::de::DeserializeOwned>(
    row: &sqlx::sqlite::SqliteRow,
    field: &str,
) -> Result<Option<T>, ApiFailure> {
    row.get::<Option<String>, _>(field)
        .map(|value| {
            serde_json::from_value(serde_json::Value::String(value)).map_err(ApiFailure::from)
        })
        .transpose()
}
pub(super) fn to_u64(row: &sqlx::sqlite::SqliteRow, field: &str) -> Result<u64, ApiFailure> {
    u64::try_from(row.get::<i64, _>(field)).map_err(|_| ApiFailure::internal())
}
pub(super) fn utf8_prefix(value: &str, max: usize) -> String {
    if value.len() <= max {
        return value.to_owned();
    }
    let mut end = max;
    while !value.is_char_boundary(end) {
        end -= 1;
    }
    value[..end].to_owned()
}
pub(super) fn sha256_hex(bytes: &[u8]) -> String {
    Sha256::digest(bytes)
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect()
}
pub(super) fn database_family_bytes(path: &std::path::Path) -> u64 {
    ["", "-wal", "-shm"]
        .iter()
        .filter_map(|suffix| std::fs::metadata(format!("{}{}", path.display(), suffix)).ok())
        .map(|metadata| metadata.len())
        .sum()
}

pub(super) fn parse_sessions_query(raw: Option<&str>) -> Result<SessionsQuery, ApiFailure> {
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

pub(super) fn parse_search_query(raw: Option<&str>) -> Result<SearchQuery, ApiFailure> {
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

pub(super) fn query_pairs(raw: Option<&str>) -> Result<Vec<(String, String)>, ApiFailure> {
    raw.unwrap_or_default()
        .split('&')
        .filter(|pair| !pair.is_empty())
        .map(|pair| {
            let (key, value) = pair.split_once('=').unwrap_or((pair, ""));
            Ok((percent_decode(key)?, percent_decode(value)?))
        })
        .collect()
}

pub(super) fn percent_decode(value: &str) -> Result<String, ApiFailure> {
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

pub(super) fn hex_digit(value: u8) -> Result<u8, ApiFailure> {
    match value {
        b'0'..=b'9' => Ok(value - b'0'),
        b'a'..=b'f' => Ok(value - b'a' + 10),
        b'A'..=b'F' => Ok(value - b'A' + 10),
        _ => Err(ApiFailure::invalid(
            "query contains invalid percent encoding",
        )),
    }
}

pub(super) fn set_once<T>(slot: &mut Option<T>, value: T, name: &str) -> Result<(), ApiFailure> {
    if slot.replace(value).is_some() {
        Err(ApiFailure::invalid(format!("{name} may only appear once")))
    } else {
        Ok(())
    }
}

pub(super) fn parse_usize(value: &str, name: &str) -> Result<usize, ApiFailure> {
    value
        .parse()
        .map_err(|_| ApiFailure::invalid(format!("{name} must be a positive integer")))
}

pub(super) fn parse_bool(value: &str, name: &str) -> Result<bool, ApiFailure> {
    match value {
        "true" => Ok(true),
        "false" => Ok(false),
        _ => Err(ApiFailure::invalid(format!("{name} must be true or false"))),
    }
}

pub(super) fn decode_string_enum<T: serde::de::DeserializeOwned>(
    value: &str,
) -> Result<T, ApiFailure> {
    Ok(serde_json::from_value(serde_json::Value::String(
        value.to_owned(),
    ))?)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn query_decoding_rejects_duplicates_and_bad_percent_encoding() {
        assert_eq!(
            query_pairs(Some("a=hello+world&b=%2Ftmp")).unwrap(),
            vec![
                ("a".to_owned(), "hello world".to_owned()),
                ("b".to_owned(), "/tmp".to_owned())
            ]
        );
        assert!(query_pairs(Some("bad=%")).is_err());
        assert!(parse_sessions_query(Some("limit=1&limit=2")).is_err());
    }
}
