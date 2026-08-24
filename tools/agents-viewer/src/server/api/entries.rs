use super::*;

#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase")]
pub(super) struct EntriesQuery {
    limit: Option<usize>,
    cursor: Option<String>,
    direction: Option<String>,
    around_entry_id: Option<String>,
    include_technical: Option<bool>,
    display_types: Option<String>,
}

#[derive(Clone, Copy, Debug, Eq, Ord, PartialEq, PartialOrd)]
pub(super) enum ConversationDisplayType {
    Received,
    Sent,
    RequestUserInput,
    Reasoning,
    Exec,
    Plan,
    Patch,
    Mcp,
    WebSearch,
    Function,
    Dynamic,
    Terminal,
    ViewImage,
    OtherTool,
    Warning,
    Error,
    Context,
    Marker,
    TechnicalMessage,
    InternalMessage,
    Unknown,
}

impl ConversationDisplayType {
    fn parse(value: &str) -> Option<Self> {
        Some(match value {
            "received" => Self::Received,
            "sent" => Self::Sent,
            "requestUserInput" => Self::RequestUserInput,
            "reasoning" => Self::Reasoning,
            "exec" => Self::Exec,
            "plan" => Self::Plan,
            "patch" => Self::Patch,
            "mcp" => Self::Mcp,
            "webSearch" => Self::WebSearch,
            "function" => Self::Function,
            "dynamic" => Self::Dynamic,
            "terminal" => Self::Terminal,
            "viewImage" => Self::ViewImage,
            "otherTool" => Self::OtherTool,
            "warning" => Self::Warning,
            "error" => Self::Error,
            "context" => Self::Context,
            "marker" => Self::Marker,
            "technicalMessage" => Self::TechnicalMessage,
            "internalMessage" => Self::InternalMessage,
            "unknown" => Self::Unknown,
            _ => return None,
        })
    }

    fn as_str(self) -> &'static str {
        match self {
            Self::Received => "received",
            Self::Sent => "sent",
            Self::RequestUserInput => "requestUserInput",
            Self::Reasoning => "reasoning",
            Self::Exec => "exec",
            Self::Plan => "plan",
            Self::Patch => "patch",
            Self::Mcp => "mcp",
            Self::WebSearch => "webSearch",
            Self::Function => "function",
            Self::Dynamic => "dynamic",
            Self::Terminal => "terminal",
            Self::ViewImage => "viewImage",
            Self::OtherTool => "otherTool",
            Self::Warning => "warning",
            Self::Error => "error",
            Self::Context => "context",
            Self::Marker => "marker",
            Self::TechnicalMessage => "technicalMessage",
            Self::InternalMessage => "internalMessage",
            Self::Unknown => "unknown",
        }
    }
}

pub(super) enum EntryVisibility {
    Default,
    IncludeTechnical,
    DisplayTypes(BTreeSet<ConversationDisplayType>),
}

impl EntryVisibility {
    fn cursor_filters(&self, session_id: &str) -> String {
        match self {
            Self::Default => format!("session={session_id};include_technical=false"),
            Self::IncludeTechnical => format!("session={session_id};include_technical=true"),
            Self::DisplayTypes(types) => format!(
                "session={session_id};display_types={}",
                types
                    .iter()
                    .map(|value| value.as_str())
                    .collect::<Vec<_>>()
                    .join(",")
            ),
        }
    }
}

pub(super) fn entry_visibility(query: &EntriesQuery) -> Result<EntryVisibility, ApiFailure> {
    if query.display_types.is_some() && query.include_technical.is_some() {
        return Err(ApiFailure::invalid(
            "displayTypes and includeTechnical are mutually exclusive",
        ));
    }
    if let Some(value) = query.display_types.as_deref() {
        if value.is_empty() {
            return Err(ApiFailure::invalid("displayTypes must not be empty"));
        }
        let mut types = BTreeSet::new();
        for display_type in value.split(',') {
            let parsed = ConversationDisplayType::parse(display_type).ok_or_else(|| {
                ApiFailure::invalid(format!("unknown display type: {display_type}"))
            })?;
            types.insert(parsed);
        }
        return Ok(EntryVisibility::DisplayTypes(types));
    }
    Ok(if query.include_technical.unwrap_or(false) {
        EntryVisibility::IncludeTechnical
    } else {
        EntryVisibility::Default
    })
}

pub(super) async fn entries(
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
    let visibility = entry_visibility(&query)?;
    let filters = visibility.cursor_filters(&session_id);
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
    push_entry_visibility(&mut builder, &visibility);
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
    let mut encoded_page_bytes = 2_usize;
    for row in &rows {
        let item = entry_item_from_row(row)?;
        let encoded_item_bytes = serde_json::to_vec(&item)?.len();
        let candidate_bytes = encoded_page_bytes
            .saturating_add(usize::from(!data.is_empty()))
            .saturating_add(encoded_item_bytes);
        if !data.is_empty() && candidate_bytes > MAX_JSON_PAGE_BYTES {
            break;
        }
        encoded_page_bytes = candidate_bytes;
        data.push(item);
    }
    let previous_cursor = if let Some(first) = data.first()
        && entry_exists(
            state.database.pool(),
            &session_id,
            &visibility,
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
            &visibility,
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

pub(super) fn push_entry_visibility(
    builder: &mut QueryBuilder<Sqlite>,
    visibility: &EntryVisibility,
) {
    match visibility {
        EntryVisibility::IncludeTechnical => {}
        EntryVisibility::Default => {
            builder.push(
                " AND ((e.kind = 'message' AND e.presentation IN ('user', 'response')) \
                 OR e.kind = 'reasoning' \
                 OR (e.kind = 'tool' AND e.tool_kind IN ('command', 'requestUserInput')) \
                 OR e.kind IN ('warning', 'error'))",
            );
        }
        EntryVisibility::DisplayTypes(types) => {
            builder.push(" AND (");
            for (index, display_type) in types.iter().enumerate() {
                if index > 0 {
                    builder.push(" OR ");
                }
                builder.push(match display_type {
                    ConversationDisplayType::Received => {
                        "(e.kind = 'message' AND e.presentation = 'response')"
                    }
                    ConversationDisplayType::Sent => {
                        "(e.kind = 'message' AND e.presentation = 'user')"
                    }
                    ConversationDisplayType::RequestUserInput => {
                        "(e.kind = 'tool' AND e.tool_kind = 'requestUserInput')"
                    }
                    ConversationDisplayType::Reasoning => "e.kind = 'reasoning'",
                    ConversationDisplayType::Exec => {
                        "(e.kind = 'tool' AND e.tool_kind = 'command')"
                    }
                    ConversationDisplayType::Plan => "e.kind = 'plan'",
                    ConversationDisplayType::Patch => "(e.kind = 'tool' AND e.tool_kind = 'patch')",
                    ConversationDisplayType::Mcp => "(e.kind = 'tool' AND e.tool_kind = 'mcp')",
                    ConversationDisplayType::WebSearch => {
                        "(e.kind = 'tool' AND e.tool_kind = 'webSearch')"
                    }
                    ConversationDisplayType::Function => {
                        "(e.kind = 'tool' AND e.tool_kind = 'function')"
                    }
                    ConversationDisplayType::Dynamic => {
                        "(e.kind = 'tool' AND e.tool_kind = 'dynamic')"
                    }
                    ConversationDisplayType::Terminal => {
                        "(e.kind = 'tool' AND e.tool_kind = 'terminal')"
                    }
                    ConversationDisplayType::ViewImage => {
                        "(e.kind = 'tool' AND e.tool_kind = 'viewImage')"
                    }
                    ConversationDisplayType::OtherTool => {
                        "(e.kind = 'tool' AND e.tool_kind = 'other')"
                    }
                    ConversationDisplayType::Warning => "e.kind = 'warning'",
                    ConversationDisplayType::Error => "e.kind = 'error'",
                    ConversationDisplayType::Context => "e.kind = 'context'",
                    ConversationDisplayType::Marker => "e.kind = 'marker'",
                    ConversationDisplayType::TechnicalMessage => {
                        "(e.kind = 'message' AND e.presentation = 'technical')"
                    }
                    ConversationDisplayType::InternalMessage => {
                        "(e.kind = 'message' AND e.presentation = 'internal')"
                    }
                    ConversationDisplayType::Unknown => "e.kind = 'unknown'",
                });
            }
            builder.push(")");
        }
    }
}

pub(super) async fn entry_exists(
    pool: &sqlx::SqlitePool,
    session_id: &str,
    visibility: &EntryVisibility,
    sequence: i64,
    id: &str,
    before: bool,
) -> Result<bool, ApiFailure> {
    let mut builder =
        QueryBuilder::<Sqlite>::new("SELECT EXISTS(SELECT 1 FROM entries e WHERE e.session_id = ");
    builder.push_bind(session_id);
    push_entry_visibility(&mut builder, visibility);
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

pub(super) async fn entry_detail(
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
        item: complete_entry_item_from_row(&row)?,
        derived_metadata: metadata,
        raw_refs,
    }))
}

#[derive(Deserialize)]
pub(super) struct ContentQuery {
    pub(super) field: Option<ContentField>,
    pub(super) offset: Option<u64>,
    pub(super) limit: Option<usize>,
}

pub(super) async fn entry_content(
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn display_types_are_validated_deduplicated_and_canonicalized() {
        let visibility = entry_visibility(&EntriesQuery {
            display_types: Some("sent,received,sent".to_owned()),
            ..EntriesQuery::default()
        })
        .unwrap();
        assert_eq!(
            visibility.cursor_filters("session"),
            "session=session;display_types=received,sent"
        );

        let Err(error) = entry_visibility(&EntriesQuery {
            include_technical: Some(true),
            display_types: Some("sent".to_owned()),
            ..EntriesQuery::default()
        }) else {
            panic!("conflicting visibility controls must be rejected");
        };
        assert_eq!(error.status, StatusCode::BAD_REQUEST);
    }
}
