use std::time::{Duration, Instant};

use anyhow::{Result, bail};
use futures::TryStreamExt as _;
use serde::Serialize;
use sqlx::{QueryBuilder, Row as _, Sqlite};
use std::collections::HashSet;
use unicode_segmentation::UnicodeSegmentation as _;

use crate::model::{EntryKind, MatchRange, SearchField, SourceKind};

use super::Database;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ArchiveFilter {
    Exclude,
    Only,
    Include,
}

#[derive(Clone, Debug)]
pub struct SearchFilters {
    pub session_id: Option<String>,
    pub sources: Vec<SourceKind>,
    pub kinds: Vec<EntryKind>,
    pub from_micros: Option<i64>,
    pub to_micros: Option<i64>,
    pub archived: ArchiveFilter,
    pub all_types: bool,
}

impl Default for SearchFilters {
    fn default() -> Self {
        Self {
            session_id: None,
            sources: Vec::new(),
            kinds: Vec::new(),
            from_micros: None,
            to_micros: None,
            archived: ArchiveFilter::Exclude,
            all_types: false,
        }
    }
}

#[derive(Clone, Debug)]
pub struct SearchRequest {
    pub query: String,
    pub limit: usize,
    pub filters: SearchFilters,
}

#[derive(Clone, Debug, PartialEq)]
pub struct SearchResult {
    pub hits: Vec<SearchRow>,
    pub partial: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct SearchRow {
    pub session_id: String,
    pub entry_id: String,
    pub kind: EntryKind,
    pub snippet: String,
    pub match_ranges: Vec<MatchRange>,
    pub field: SearchField,
    pub rank: f64,
    pub timestamp_micros: Option<i64>,
}

pub async fn search(database: &Database, request: &SearchRequest) -> Result<SearchResult> {
    let scalar_count = request.query.chars().count();
    if scalar_count == 0 {
        bail!("search query must not be empty");
    }
    if scalar_count > 512 {
        bail!("search query exceeds 512 Unicode scalars");
    }
    if request.limit == 0 || request.limit > 200 {
        bail!("search limit must be between 1 and 200");
    }
    if scalar_count >= 3 {
        search_fts(database, request).await
    } else {
        search_short(database, request, scalar_count).await
    }
}

async fn search_fts(database: &Database, request: &SearchRequest) -> Result<SearchResult> {
    let mut builder = QueryBuilder::<Sqlite>::new(
        "SELECT e.id, e.session_id, e.kind, e.title, e.primary_text, e.secondary_text, \
                e.timestamp_micros, s.title AS session_title, -bm25(entries_fts) AS rank \
         FROM entries_fts \
         JOIN entries e ON e.rowid = entries_fts.rowid \
         JOIN sessions s ON s.id = e.session_id \
         WHERE entries_fts MATCH ",
    );
    builder.push_bind(fts_literal(&request.query));
    push_filters(&mut builder, &request.filters)?;
    builder.push(" ORDER BY rank DESC, e.timestamp_micros DESC, e.id LIMIT ");
    builder.push_bind(i64::try_from(request.limit)?);
    let rows = builder.build().fetch_all(database.pool()).await?;
    let mut hits = rows
        .into_iter()
        .filter_map(|row| row_to_hit(&row, &request.query).ok())
        .collect::<Vec<_>>();
    let mut title_builder = QueryBuilder::<Sqlite>::new(
        "SELECT e.id, e.session_id, e.kind, e.title, e.primary_text, e.secondary_text, \
                e.timestamp_micros, s.title AS session_title, 0.0 AS rank \
         FROM sessions s JOIN entries e ON e.session_id = s.id \
         WHERE instr(lower(s.title), lower(",
    );
    title_builder.push_bind(&request.query);
    title_builder.push(")) > 0");
    push_filters(&mut title_builder, &request.filters)?;
    title_builder.push(" ORDER BY e.timestamp_micros DESC, e.id LIMIT ");
    title_builder.push_bind(i64::try_from(
        request.limit.saturating_mul(4).max(request.limit),
    )?);
    let mut seen = hits
        .iter()
        .map(|hit| hit.entry_id.clone())
        .collect::<HashSet<_>>();
    for row in title_builder.build().fetch_all(database.pool()).await? {
        if let Ok(hit) = row_to_hit(&row, &request.query)
            && seen.insert(hit.entry_id.clone())
        {
            hits.push(hit);
        }
    }
    let partial = if request.filters.all_types {
        let result = search_unindexed(database, request).await?;
        for hit in result.hits {
            if seen.insert(hit.entry_id.clone()) {
                hits.push(hit);
            }
        }
        result.partial
    } else {
        false
    };
    hits.sort_by(|left, right| {
        right
            .rank
            .total_cmp(&left.rank)
            .then_with(|| right.timestamp_micros.cmp(&left.timestamp_micros))
            .then_with(|| left.entry_id.cmp(&right.entry_id))
    });
    hits.truncate(request.limit);
    Ok(SearchResult { hits, partial })
}

async fn search_unindexed(database: &Database, request: &SearchRequest) -> Result<SearchResult> {
    let cap = 10_000_usize;
    let mut builder = QueryBuilder::<Sqlite>::new(
        "SELECT e.id, e.session_id, e.kind, e.title, e.primary_text, e.secondary_text, \
                e.timestamp_micros, s.title AS session_title, 0.0 AS rank \
         FROM entries e JOIN sessions s ON s.id = e.session_id \
         WHERE e.searchable = 0",
    );
    push_filters(&mut builder, &request.filters)?;
    builder.push(" ORDER BY e.timestamp_micros DESC, e.id LIMIT ");
    builder.push_bind(i64::try_from(cap + 1)?);
    scan_rows(database, builder, request, cap, true).await
}

async fn search_short(
    database: &Database,
    request: &SearchRequest,
    scalar_count: usize,
) -> Result<SearchResult> {
    let cap = if scalar_count == 1 { 400 } else { 10_000 };
    let mut builder = QueryBuilder::<Sqlite>::new(
        "SELECT e.id, e.session_id, e.kind, e.title, e.primary_text, e.secondary_text, \
                e.timestamp_micros, s.title AS session_title, 0.0 AS rank \
         FROM entries e JOIN sessions s ON s.id = e.session_id \
         WHERE 1 = 1",
    );
    if !request.filters.all_types {
        builder.push(" AND e.searchable = 1");
    }
    push_filters(&mut builder, &request.filters)?;
    builder.push(" ORDER BY e.timestamp_micros DESC, e.id LIMIT ");
    builder.push_bind(i64::try_from(cap + 1)?);
    scan_rows(database, builder, request, cap, scalar_count == 2).await
}

async fn scan_rows(
    database: &Database,
    mut builder: QueryBuilder<Sqlite>,
    request: &SearchRequest,
    cap: usize,
    enforce_deadline: bool,
) -> Result<SearchResult> {
    let mut rows = builder.build().fetch(database.pool());
    let started = Instant::now();
    let mut scanned = 0_usize;
    let mut partial = false;
    let mut hits = Vec::new();
    while let Some(row) = rows.try_next().await? {
        if scanned == cap {
            partial = true;
            break;
        }
        if enforce_deadline && started.elapsed() >= Duration::from_secs(2) {
            partial = true;
            break;
        }
        scanned += 1;
        if hits.len() < request.limit
            && let Ok(hit) = row_to_hit(&row, &request.query)
        {
            hits.push(hit);
        }
    }
    if scanned == cap {
        partial = true;
    }
    Ok(SearchResult { hits, partial })
}

fn push_filters(builder: &mut QueryBuilder<Sqlite>, filters: &SearchFilters) -> Result<()> {
    if !filters.all_types {
        builder.push(" AND e.kind = 'message' AND e.presentation IN ('user', 'response')");
    }
    if let Some(session_id) = &filters.session_id {
        builder.push(" AND e.session_id = ").push_bind(session_id);
    }
    push_enum_filter(builder, "s.source_kind", &filters.sources)?;
    push_enum_filter(builder, "e.kind", &filters.kinds)?;
    if let Some(from) = filters.from_micros {
        builder.push(" AND e.timestamp_micros >= ").push_bind(from);
    }
    if let Some(to) = filters.to_micros {
        builder.push(" AND e.timestamp_micros <= ").push_bind(to);
    }
    match filters.archived {
        ArchiveFilter::Exclude => {
            builder.push(" AND s.archived = 0");
        }
        ArchiveFilter::Only => {
            builder.push(" AND s.archived = 1");
        }
        ArchiveFilter::Include => {}
    }
    Ok(())
}

fn push_enum_filter<T: Serialize>(
    builder: &mut QueryBuilder<Sqlite>,
    column: &str,
    values: &[T],
) -> Result<()> {
    if values.is_empty() {
        return Ok(());
    }
    builder.push(" AND ").push(column).push(" IN (");
    let mut separated = builder.separated(", ");
    for value in values {
        let serialized = serde_json::to_value(value)?;
        separated.push_bind(
            serialized
                .as_str()
                .ok_or_else(|| anyhow::anyhow!("enum did not serialize to a string"))?
                .to_owned(),
        );
    }
    separated.push_unseparated(")");
    Ok(())
}

fn row_to_hit(row: &sqlx::sqlite::SqliteRow, query: &str) -> Result<SearchRow> {
    let fields = [
        (
            SearchField::SessionTitle,
            row.get::<String, _>("session_title"),
        ),
        (SearchField::EntryTitle, row.get::<String, _>("title")),
        (SearchField::Primary, row.get::<String, _>("primary_text")),
        (
            SearchField::Secondary,
            row.get::<String, _>("secondary_text"),
        ),
    ];
    let (field, text) = fields
        .into_iter()
        .find(|(_, text)| contains_unicode_lowercase(text, query))
        .ok_or_else(|| anyhow::anyhow!("FTS candidate has no literal match"))?;
    let (snippet, match_ranges) = snippet(&text, query, 240);
    Ok(SearchRow {
        session_id: row.get("session_id"),
        entry_id: row.get("id"),
        kind: serde_json::from_value(serde_json::Value::String(row.get("kind")))?,
        snippet,
        match_ranges,
        field,
        rank: row.get("rank"),
        timestamp_micros: row.get("timestamp_micros"),
    })
}

fn fts_literal(query: &str) -> String {
    format!("\"{}\"", query.replace('"', "\"\""))
}

fn contains_unicode_lowercase(text: &str, query: &str) -> bool {
    let (text, _) = lowercase_with_map(text);
    let query = query
        .chars()
        .flat_map(char::to_lowercase)
        .collect::<Vec<_>>();
    !query.is_empty() && text.windows(query.len()).any(|window| window == query)
}

fn snippet(text: &str, query: &str, max_graphemes: usize) -> (String, Vec<MatchRange>) {
    let graphemes = text.graphemes(true).collect::<Vec<_>>();
    if graphemes.len() <= max_graphemes {
        return (text.to_owned(), match_ranges(text, query));
    }
    let first_scalar = first_match_scalar(text, query).unwrap_or(0);
    let mut scalar_cursor = 0_usize;
    let match_grapheme = graphemes
        .iter()
        .position(|grapheme| {
            let next = scalar_cursor + grapheme.chars().count();
            let contains = first_scalar >= scalar_cursor && first_scalar < next;
            scalar_cursor = next;
            contains
        })
        .unwrap_or(0);
    let start = match_grapheme
        .saturating_sub(max_graphemes / 3)
        .min(graphemes.len() - max_graphemes);
    let snippet = graphemes[start..start + max_graphemes].concat();
    let ranges = match_ranges(&snippet, query);
    (snippet, ranges)
}

fn first_match_scalar(text: &str, query: &str) -> Option<usize> {
    match_scalar_ranges(text, query)
        .into_iter()
        .next()
        .map(|range| range.0)
}

fn match_ranges(text: &str, query: &str) -> Vec<MatchRange> {
    match_scalar_ranges(text, query)
        .into_iter()
        .map(|(start, end)| MatchRange {
            start: u32::try_from(start).unwrap_or(u32::MAX),
            end: u32::try_from(end).unwrap_or(u32::MAX),
        })
        .collect()
}

fn match_scalar_ranges(text: &str, query: &str) -> Vec<(usize, usize)> {
    let (lowered, scalar_map) = lowercase_with_map(text);
    let query = query
        .chars()
        .flat_map(char::to_lowercase)
        .collect::<Vec<_>>();
    if query.is_empty() || query.len() > lowered.len() {
        return Vec::new();
    }
    lowered
        .windows(query.len())
        .enumerate()
        .filter(|(_, window)| *window == query)
        .map(|(index, _)| {
            let start = scalar_map[index];
            let end = scalar_map[index + query.len() - 1] + 1;
            (start, end)
        })
        .collect()
}

fn lowercase_with_map(text: &str) -> (Vec<char>, Vec<usize>) {
    let mut lowered = Vec::new();
    let mut scalar_map = Vec::new();
    for (scalar_index, character) in text.chars().enumerate() {
        for lowered_character in character.to_lowercase() {
            lowered.push(lowered_character);
            scalar_map.push(scalar_index);
        }
    }
    (lowered, scalar_map)
}
