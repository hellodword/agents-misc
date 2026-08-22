use super::*;

#[derive(Default, Deserialize)]
pub(super) struct RawListQuery {
    limit: Option<usize>,
    cursor: Option<String>,
}

pub(super) async fn raw_list(
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

pub(super) async fn raw_record(
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn raw_query_deserializes_only_its_owned_fields() {
        let query: RawListQuery = serde_json::from_value(serde_json::json!({
            "limit": 7,
            "cursor": "next"
        }))
        .unwrap();
        assert_eq!(query.limit, Some(7));
        assert_eq!(query.cursor.as_deref(), Some("next"));
    }
}
