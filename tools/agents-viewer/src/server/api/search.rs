use super::*;

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
        let mut session = session_from_row(&row)?;
        apply_session_freshness(&state, &mut session)?;
        hits.push(SearchHit {
            session,
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
