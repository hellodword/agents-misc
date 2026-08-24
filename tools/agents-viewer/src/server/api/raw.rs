use super::*;
use sha2::{Digest as _, Sha256};
use std::sync::Arc;

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
    let byte_offset =
        u64::try_from(row.get::<i64, _>("byte_offset")).map_err(|_| ApiFailure::internal())?;
    let byte_length =
        u64::try_from(row.get::<i64, _>("byte_length")).map_err(|_| ApiFailure::internal())?;
    let requested_offset = query.offset.unwrap_or(0);
    let limit = bounded_content(query.limit)?;
    let utf8 = row.get::<bool, _>("utf8");
    let chunk = if utf8 {
        let _permit = Arc::clone(&state.raw_reads)
            .acquire_owned()
            .await
            .map_err(|_| ApiFailure::service_unavailable("raw source reads are unavailable"))?;
        let root = root.to_path_buf();
        let file_key = row.get::<String, _>("file_key");
        let expected_hash = row.get::<String, _>("content_hash");
        tokio::task::spawn_blocking(move || {
            read_utf8_raw_chunk(RawChunkRequest {
                root,
                path,
                file_key,
                expected_hash,
                byte_offset,
                byte_length,
                requested_offset,
                limit,
            })
        })
        .await
        .map_err(|error| {
            tracing::error!(%error, "raw source reader panicked");
            ApiFailure::internal()
        })??
    } else {
        let _permit = Arc::clone(&state.raw_reads)
            .acquire_owned()
            .await
            .map_err(|_| ApiFailure::service_unavailable("raw source reads are unavailable"))?;
        let root = root.to_path_buf();
        let path_for_hash = path.clone();
        let file_key = row.get::<String, _>("file_key");
        let expected_hash = row.get::<String, _>("content_hash");
        tokio::task::spawn_blocking(move || {
            verify_raw_record(
                &root,
                &path_for_hash,
                &file_key,
                &expected_hash,
                byte_offset,
                byte_length,
                None,
            )
        })
        .await
        .map_err(|error| {
            tracing::error!(%error, "raw source reader panicked");
            ApiFailure::internal()
        })??;
        let text = row
            .get::<Option<String>, _>("hex_preview")
            .unwrap_or_default();
        text_chunk(ContentField::Primary, &text, requested_offset, limit)?
    };
    Ok(Json(RawRecord {
        summary: raw_summary_from_row(&row)?,
        chunk,
    }))
}

struct RawChunkRequest {
    root: std::path::PathBuf,
    path: std::path::PathBuf,
    file_key: String,
    expected_hash: String,
    byte_offset: u64,
    byte_length: u64,
    requested_offset: u64,
    limit: usize,
}

fn read_utf8_raw_chunk(request: RawChunkRequest) -> Result<ContentChunk, ApiFailure> {
    let requested = request.requested_offset.min(request.byte_length);
    let retain_end = requested
        .saturating_add(request.limit as u64)
        .saturating_add(4)
        .min(request.byte_length);
    let retained = verify_raw_record(
        &request.root,
        &request.path,
        &request.file_key,
        &request.expected_hash,
        request.byte_offset,
        request.byte_length,
        Some((requested, retain_end)),
    )?;
    let relative_start = retained
        .iter()
        .position(|byte| byte & 0b1100_0000 != 0b1000_0000)
        .unwrap_or(retained.len());
    let start = requested.saturating_add(relative_start as u64);
    let mut end = start
        .saturating_add(request.limit as u64)
        .min(request.byte_length);
    let mut relative_end =
        usize::try_from(end.saturating_sub(requested)).map_err(|_| ApiFailure::internal())?;
    while end > start
        && end < request.byte_length
        && retained
            .get(relative_end)
            .is_some_and(|byte| byte & 0b1100_0000 == 0b1000_0000)
    {
        end -= 1;
        relative_end -= 1;
    }
    let text = std::str::from_utf8(&retained[relative_start..relative_end])
        .map_err(|_| ApiFailure::source_changed("source encoding changed"))?
        .to_owned();
    Ok(ContentChunk {
        field: ContentField::Primary,
        text,
        byte_offset: start,
        next_offset: (end < request.byte_length).then_some(end),
        total_bytes: request.byte_length,
        complete: start == 0 && end == request.byte_length,
    })
}

fn verify_raw_record(
    root: &std::path::Path,
    path: &std::path::Path,
    file_key: &str,
    expected_hash: &str,
    byte_offset: u64,
    byte_length: u64,
    retain: Option<(u64, u64)>,
) -> Result<Vec<u8>, ApiFailure> {
    let mut opened = open_source_read_only(root, path)
        .map_err(|_| ApiFailure::source_changed("source file changed or became unavailable"))?;
    if opened.identity.file_key != file_key {
        return Err(ApiFailure::source_changed("source file identity changed"));
    }
    opened
        .file
        .seek(SeekFrom::Start(byte_offset))
        .map_err(|_| ApiFailure::source_changed("source record cannot be read"))?;
    let retained_capacity = retain
        .and_then(|(start, end)| usize::try_from(end.saturating_sub(start)).ok())
        .unwrap_or(0);
    let mut retained = Vec::with_capacity(retained_capacity);
    let mut hasher = Sha256::new();
    let mut buffer = [0_u8; 64 * 1024];
    let mut consumed = 0_u64;
    while consumed < byte_length {
        let remaining = usize::try_from(byte_length.saturating_sub(consumed))
            .unwrap_or(usize::MAX)
            .min(buffer.len());
        let read = opened
            .file
            .read(&mut buffer[..remaining])
            .map_err(|_| ApiFailure::source_changed("source record changed"))?;
        if read == 0 {
            return Err(ApiFailure::source_changed("source record changed"));
        }
        hasher.update(&buffer[..read]);
        if let Some((retain_start, retain_end)) = retain {
            let chunk_start = consumed;
            let chunk_end = consumed.saturating_add(read as u64);
            let copy_start = chunk_start.max(retain_start);
            let copy_end = chunk_end.min(retain_end);
            if copy_start < copy_end {
                let local_start = usize::try_from(copy_start - chunk_start)
                    .map_err(|_| ApiFailure::internal())?;
                let local_end =
                    usize::try_from(copy_end - chunk_start).map_err(|_| ApiFailure::internal())?;
                retained.extend_from_slice(&buffer[local_start..local_end]);
            }
        }
        consumed = consumed.saturating_add(read as u64);
    }
    let digest = hasher.finalize();
    let actual_hash = digest
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect::<String>();
    if actual_hash != expected_hash {
        return Err(ApiFailure::source_changed("source record content changed"));
    }
    Ok(retained)
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

    #[test]
    fn streaming_raw_reader_preserves_utf8_boundaries_and_checks_full_hash() {
        let temporary = tempfile::tempdir().unwrap();
        let root = temporary.path().to_path_buf();
        let path = root.join("record.jsonl");
        let mut bytes = vec![b'a'; 65_535];
        bytes.extend_from_slice("😀".as_bytes());
        bytes.extend(std::iter::repeat_n(b'b', 2 * 1024 * 1024));
        std::fs::write(&path, &bytes).unwrap();
        let file_key = open_source_read_only(&root, &path)
            .unwrap()
            .identity
            .file_key;
        let expected_hash = Sha256::digest(&bytes)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        let byte_length = bytes.len() as u64;
        let request = |requested_offset, limit| RawChunkRequest {
            root: root.clone(),
            path: path.clone(),
            file_key: file_key.clone(),
            expected_hash: expected_hash.clone(),
            byte_offset: 0,
            byte_length,
            requested_offset,
            limit,
        };

        let before_scalar = read_utf8_raw_chunk(request(65_534, 3)).unwrap();
        assert_eq!(before_scalar.text, "a");
        assert_eq!(before_scalar.next_offset, Some(65_535));
        let scalar = read_utf8_raw_chunk(request(65_535, 4)).unwrap();
        assert_eq!(scalar.text, "😀");
        assert_eq!(scalar.next_offset, Some(65_539));

        bytes[100] = b'c';
        std::fs::write(&path, &bytes).unwrap();
        assert!(read_utf8_raw_chunk(request(0, 64)).is_err());
    }
}
