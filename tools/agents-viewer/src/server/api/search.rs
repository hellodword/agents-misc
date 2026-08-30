use super::*;
use std::collections::HashSet;

#[derive(Default)]
pub(super) struct SearchQuery {
    pub(super) q: Option<String>,
    pub(super) limit: Option<usize>,
    pub(super) session: Option<String>,
    pub(super) source: Vec<SourceKind>,
    pub(super) kind: Vec<EntryKind>,
    pub(super) from: Option<String>,
    pub(super) to: Option<String>,
    pub(super) archived: Option<String>,
    pub(super) all_types: Option<bool>,
}

pub(super) async fn search(
    State(state): State<AppState>,
    RawQuery(raw_query): RawQuery,
) -> Result<Json<ApiPage<SearchHit>>, ApiFailure> {
    let query = parse_search_query(raw_query.as_deref())?;
    let q = query
        .q
        .ok_or_else(|| ApiFailure::invalid("q is required"))?;
    let query_scalars = q.chars().count();
    if query_scalars == 0 {
        return Err(ApiFailure::invalid("search query must not be empty"));
    }
    if query_scalars > 512 {
        return Err(ApiFailure::invalid(
            "search query exceeds 512 Unicode scalars",
        ));
    }
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
    .map_err(|error| {
        tracing::error!(%error, "search index request failed");
        ApiFailure::internal()
    })?;
    let mut unique_session_ids = Vec::new();
    let mut seen_session_ids = HashSet::new();
    for hit in &result.hits {
        if seen_session_ids.insert(hit.session_id.clone()) {
            unique_session_ids.push(hit.session_id.clone());
        }
    }
    let mut sessions = HashMap::<String, SessionSummary>::new();
    if !unique_session_ids.is_empty() {
        let mut builder = QueryBuilder::<Sqlite>::new(
            "SELECT s.*, sf.root_kind, sf.relative_path, sf.size_bytes AS observed_bytes, sf.snapshot_size_bytes, sf.snapshot_revision, sf.last_synced_at_micros FROM sessions s JOIN source_files sf ON sf.id = s.source_file_id WHERE s.id IN (",
        );
        let mut separated = builder.separated(",");
        for session_id in &unique_session_ids {
            separated.push_bind(session_id);
        }
        separated.push_unseparated(")");
        for row in builder.build().fetch_all(state.database.pool()).await? {
            let session = session_from_row(&row)?;
            sessions.insert(session.id.clone(), session);
        }
        for session in sessions.values_mut() {
            apply_session_freshness(&state, session)?;
        }
    }
    let mut hits = Vec::with_capacity(result.hits.len());
    for hit in result.hits {
        let session = sessions.get(&hit.session_id).cloned().ok_or_else(|| {
            tracing::error!(session_id = %hit.session_id, "search hit references a missing session");
            ApiFailure::internal()
        })?;
        hits.push(SearchHit {
            session,
            origin: hit.origin,
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn search_query_parsing_is_strict_and_keeps_repeated_filters() {
        let query =
            parse_search_query(Some("q=hello+world&source=cli&source=exec&allTypes=true")).unwrap();
        assert_eq!(query.q.as_deref(), Some("hello world"));
        assert_eq!(query.source, vec![SourceKind::Cli, SourceKind::Exec]);
        assert_eq!(query.all_types, Some(true));

        assert!(parse_search_query(Some("q=one&q=two")).is_err());
    }
}
