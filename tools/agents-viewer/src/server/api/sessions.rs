use super::*;

#[derive(Default)]
pub(super) struct SessionsQuery {
    pub(super) source: Vec<SourceKind>,
    pub(super) archived: Option<String>,
    pub(super) cwd: Option<String>,
    pub(super) parent: Option<String>,
    pub(super) limit: Option<usize>,
    pub(super) cursor: Option<String>,
}

pub(super) async fn sessions(
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
    let mut data = rows
        .iter()
        .map(session_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    apply_freshness(&state, &mut data)?;
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

pub(super) async fn session_groups(
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
    let mut sessions = sqlx::query("SELECT * FROM sessions")
        .fetch_all(state.database.pool())
        .await?
        .iter()
        .map(session_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    apply_freshness(&state, &mut sessions)?;
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

pub(super) struct BuiltTree {
    node: SessionTreeNode,
    updated_at_micros: i64,
    latest_created_at_micros: i64,
    latest_session_id: String,
}

pub(super) fn build_session_groups(sessions: Vec<SessionSummary>) -> Vec<SessionGroup> {
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

pub(super) fn break_parent_cycles(parents: &mut HashMap<String, Option<String>>) {
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

pub(super) fn build_session_tree(
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

pub(super) fn group_matches(
    group: &SessionGroup,
    query: &SessionsQuery,
    archived: ArchiveFilter,
) -> bool {
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

pub(super) async fn session_detail(
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
    let mut summary = session_from_row(&row)?;
    apply_session_freshness(&state, &mut summary)?;
    Ok(Json(SessionDetail {
        summary,
        diagnostics,
    }))
}

pub(super) async fn sync_session(
    State(state): State<AppState>,
    Path(session_id): Path<String>,
) -> Result<Response, ApiFailure> {
    validate_id(&session_id)?;
    let has_snapshot =
        sqlx::query_scalar::<_, i64>("SELECT EXISTS(SELECT 1 FROM sessions WHERE id = ?)")
            .bind(&session_id)
            .fetch_one(state.database.pool())
            .await?
            != 0;
    if !has_snapshot && uuid::Uuid::parse_str(&session_id).is_err() {
        return Err(ApiFailure::invalid(
            "an uncached session identifier must be a UUID",
        ));
    }
    let coordinator = state
        .coordinator
        .as_ref()
        .ok_or_else(|| ApiFailure::service_unavailable("session synchronization is unavailable"))?;
    let status = coordinator
        .ensure_session(&session_id)
        .map_err(|error| coordinator_failure(error, &session_id))?;
    if status.state == SessionSyncState::NotFound {
        return Err(ApiFailure::not_found("session source does not exist"));
    }
    let response_status = if matches!(
        status.state,
        SessionSyncState::Checking | SessionSyncState::Queued | SessionSyncState::Indexing
    ) {
        StatusCode::ACCEPTED
    } else {
        StatusCode::OK
    };
    Ok((response_status, Json::<SessionSyncStatus>(status)).into_response())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parent_cycles_are_broken_at_a_stable_node() {
        let mut parents = HashMap::from([
            ("a".to_owned(), Some("b".to_owned())),
            ("b".to_owned(), Some("a".to_owned())),
            ("c".to_owned(), Some("b".to_owned())),
        ]);

        break_parent_cycles(&mut parents);

        assert_eq!(parents["a"], None);
        assert_eq!(parents["b"].as_deref(), Some("a"));
        assert_eq!(parents["c"].as_deref(), Some("b"));
    }
}
