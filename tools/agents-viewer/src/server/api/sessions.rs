use super::*;
use std::sync::Arc;
use std::sync::atomic::{AtomicU64, Ordering};
use tokio::sync::Mutex;

pub(super) const MAX_SESSION_TREE_DEPTH: usize = 64;

#[derive(Clone)]
pub(crate) struct SessionGroupCatalog {
    inner: Arc<SessionGroupCatalogInner>,
}

struct SessionGroupCatalogInner {
    generation: AtomicU64,
    revision: AtomicU64,
    cached: Mutex<Option<CatalogSnapshot>>,
}

struct CatalogSnapshot {
    generation: u64,
    revision: u64,
    groups: Arc<Vec<SessionGroup>>,
}

impl SessionGroupCatalog {
    pub(crate) fn new() -> Self {
        Self {
            inner: Arc::new(SessionGroupCatalogInner {
                generation: AtomicU64::new(0),
                revision: AtomicU64::new(0),
                cached: Mutex::new(None),
            }),
        }
    }

    pub(crate) fn invalidate(&self, generation: u64) {
        self.inner
            .generation
            .fetch_max(generation, Ordering::AcqRel);
        self.inner.revision.fetch_add(1, Ordering::AcqRel);
    }

    async fn groups(
        &self,
        database: &crate::index::Database,
    ) -> Result<Arc<Vec<SessionGroup>>, ApiFailure> {
        let mut cached = self.inner.cached.lock().await;
        loop {
            let generation = self.inner.generation.load(Ordering::Acquire);
            let revision = self.inner.revision.load(Ordering::Acquire);
            if let Some(snapshot) = cached.as_ref()
                && snapshot.generation == generation
                && snapshot.revision == revision
            {
                return Ok(Arc::clone(&snapshot.groups));
            }

            let sessions = sqlx::query("SELECT * FROM sessions")
                .fetch_all(database.pool())
                .await?
                .iter()
                .map(session_from_row)
                .collect::<Result<Vec<_>, _>>()?;
            let groups = tokio::task::spawn_blocking(move || build_session_groups(sessions))
                .await
                .map_err(|error| {
                    tracing::error!(%error, "session group catalog builder panicked");
                    ApiFailure::internal()
                })?;
            if generation != self.inner.generation.load(Ordering::Acquire)
                || revision != self.inner.revision.load(Ordering::Acquire)
            {
                continue;
            }
            let groups = Arc::new(groups);
            *cached = Some(CatalogSnapshot {
                generation,
                revision,
                groups: Arc::clone(&groups),
            });
            return Ok(groups);
        }
    }
}

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
    rows.truncate(limit);
    if previous {
        rows.reverse();
    }
    let mut data = rows
        .iter()
        .map(session_from_row)
        .collect::<Result<Vec<_>, _>>()?;
    apply_freshness(&state, &mut data)?;
    let next_cursor = if let Some(item) = data.last()
        && session_exists_relative(
            state.database.pool(),
            &query,
            archived,
            micros(&item.updated_at),
            &item.id,
            false,
        )
        .await?
    {
        Some(cursor::encode(
            "sessions",
            &filters,
            micros(&item.updated_at),
            &item.id,
            "next",
        ))
    } else {
        None
    };
    let previous_cursor = if let Some(item) = data.first()
        && session_exists_relative(
            state.database.pool(),
            &query,
            archived,
            micros(&item.updated_at),
            &item.id,
            true,
        )
        .await?
    {
        Some(cursor::encode(
            "sessions",
            &filters,
            micros(&item.updated_at),
            &item.id,
            "previous",
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
    let catalog = state.session_groups.groups(&state.database).await?;
    let groups = catalog
        .iter()
        .filter(|group| group_matches(group, &query, archived))
        .collect::<Vec<_>>();
    let (start, end) = group_page_bounds(&groups, decoded.as_ref(), previous, limit);
    let mut data = groups[start..end]
        .iter()
        .map(|group| (*group).clone())
        .collect::<Vec<_>>();
    refresh_group_freshness(&state, &mut data)?;
    let next_cursor = if end < groups.len() {
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
    let previous_cursor = (start > 0)
        .then(|| {
            data.first().map(|group| {
                cursor::encode(
                    "session-groups",
                    &filters,
                    micros(&group.updated_at),
                    &group.root.session.id,
                    "previous",
                )
            })
        })
        .flatten();
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
    let _resolved_roots = resolve_parent_roots(&mut parents);
    let mut original_children = HashMap::<String, Vec<String>>::new();
    for (id, parent) in &parents {
        if let Some(parent) = parent {
            original_children
                .entry(parent.clone())
                .or_default()
                .push(id.clone());
        }
    }
    for children in original_children.values_mut() {
        children.sort();
    }
    let mut roots = parents
        .iter()
        .filter_map(|(id, parent)| parent.is_none().then_some(id.clone()))
        .collect::<Vec<_>>();
    roots.sort();
    let mut groups = roots
        .into_iter()
        .map(|root| {
            let (children, hierarchy_complete) = display_children(&root, &original_children);
            let built = build_session_tree(&root, &sessions, &children);
            SessionGroup {
                root: built.node,
                latest_session_id: built.latest_session_id,
                updated_at: format_time(built.updated_at_micros),
                hierarchy_complete,
            }
        })
        .collect::<Vec<_>>();
    groups.sort_by(|left, right| {
        micros(&right.updated_at)
            .cmp(&micros(&left.updated_at))
            .then_with(|| left.root.session.id.cmp(&right.root.session.id))
    });
    groups
}

#[cfg(test)]
pub(super) fn break_parent_cycles(parents: &mut HashMap<String, Option<String>>) {
    let _ = resolve_parent_roots(parents);
}

fn resolve_parent_roots(parents: &mut HashMap<String, Option<String>>) -> HashMap<String, String> {
    let mut resolved = HashMap::<String, String>::with_capacity(parents.len());
    let mut starts = parents.keys().cloned().collect::<Vec<_>>();
    starts.sort();
    for start in starts {
        if resolved.contains_key(&start) {
            continue;
        }
        let mut path = Vec::<String>::new();
        let mut positions = HashMap::<String, usize>::new();
        let mut current = start;
        let root = loop {
            if let Some(root) = resolved.get(&current) {
                break root.clone();
            }
            if let Some(position) = positions.get(&current).copied() {
                let root = path[position..]
                    .iter()
                    .min()
                    .expect("a repeated node creates a non-empty cycle")
                    .clone();
                parents.insert(root.clone(), None);
                break root;
            }
            positions.insert(current.clone(), path.len());
            path.push(current.clone());
            let Some(parent) = parents.get(&current).and_then(Clone::clone) else {
                break current;
            };
            current = parent;
        };
        for id in path {
            resolved.insert(id, root.clone());
        }
    }
    resolved
}

fn display_children(
    root: &str,
    original_children: &HashMap<String, Vec<String>>,
) -> (HashMap<String, Vec<String>>, bool) {
    let mut display = HashMap::<String, Vec<String>>::new();
    let mut hierarchy_complete = true;
    let mut stack = vec![(root.to_owned(), 1_usize)];
    while let Some((id, depth)) = stack.pop() {
        let Some(children) = original_children.get(&id) else {
            continue;
        };
        for child in children.iter().rev() {
            let candidate_depth = depth.saturating_add(1);
            let (display_parent, child_depth) = if candidate_depth > MAX_SESSION_TREE_DEPTH {
                hierarchy_complete = false;
                (root, 2)
            } else {
                (id.as_str(), candidate_depth)
            };
            display
                .entry(display_parent.to_owned())
                .or_default()
                .push(child.clone());
            stack.push((child.clone(), child_depth));
        }
    }
    for children in display.values_mut() {
        children.sort();
        children.dedup();
    }
    (display, hierarchy_complete)
}

pub(super) fn build_session_tree(
    id: &str,
    sessions: &HashMap<String, SessionSummary>,
    children: &HashMap<String, Vec<String>>,
) -> BuiltTree {
    let mut built = HashMap::<String, BuiltTree>::with_capacity(sessions.len());
    let mut stack = vec![(id.to_owned(), false)];
    while let Some((current, expanded)) = stack.pop() {
        if !expanded {
            stack.push((current.clone(), true));
            if let Some(descendants) = children.get(&current) {
                for child in descendants.iter().rev() {
                    stack.push((child.clone(), false));
                }
            }
            continue;
        }
        let session = sessions
            .get(&current)
            .expect("tree IDs originate from the session map")
            .clone();
        let mut built_children = children
            .get(&current)
            .into_iter()
            .flatten()
            .map(|child| {
                built
                    .remove(child)
                    .expect("postorder traversal builds children first")
            })
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
        built.insert(
            current,
            BuiltTree {
                node: SessionTreeNode {
                    session,
                    children: built_children.into_iter().map(|child| child.node).collect(),
                },
                updated_at_micros,
                latest_created_at_micros,
                latest_session_id,
            },
        );
    }
    built
        .remove(id)
        .expect("postorder traversal builds the requested root")
}

pub(super) fn group_matches(
    group: &SessionGroup,
    query: &SessionsQuery,
    archived: ArchiveFilter,
) -> bool {
    let mut stack = vec![&group.root];
    while let Some(node) = stack.pop() {
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
        if source_matches && archive_matches && cwd_matches && parent_matches {
            return true;
        }
        stack.extend(node.children.iter());
    }
    false
}

fn group_page_bounds(
    groups: &[&SessionGroup],
    decoded: Option<&(i64, String, String)>,
    previous: bool,
    limit: usize,
) -> (usize, usize) {
    let Some((sort, id, _)) = decoded else {
        return (0, limit.min(groups.len()));
    };
    if previous {
        let end = groups
            .iter()
            .position(|group| {
                let updated = micros(&group.updated_at);
                !(updated > *sort || (updated == *sort && group.root.session.id < *id))
            })
            .unwrap_or(groups.len());
        (end.saturating_sub(limit), end)
    } else {
        let start = groups
            .iter()
            .position(|group| {
                let updated = micros(&group.updated_at);
                updated < *sort || (updated == *sort && group.root.session.id > *id)
            })
            .unwrap_or(groups.len());
        (start, start.saturating_add(limit).min(groups.len()))
    }
}

fn refresh_group_freshness(
    state: &AppState,
    groups: &mut [SessionGroup],
) -> Result<(), ApiFailure> {
    for group in groups {
        let mut stack = vec![&mut group.root];
        while let Some(node) = stack.pop() {
            apply_session_freshness(state, &mut node.session)?;
            stack.extend(node.children.iter_mut());
        }
    }
    Ok(())
}

async fn session_exists_relative(
    pool: &sqlx::SqlitePool,
    query: &SessionsQuery,
    archived: ArchiveFilter,
    sort: i64,
    id: &str,
    before: bool,
) -> Result<bool, ApiFailure> {
    let mut builder =
        QueryBuilder::<Sqlite>::new("SELECT EXISTS(SELECT 1 FROM sessions s WHERE 1=1");
    push_session_filters(&mut builder, query, archived)?;
    builder.push(if before {
        " AND (s.updated_at_micros > "
    } else {
        " AND (s.updated_at_micros < "
    });
    builder.push_bind(sort);
    builder.push(" OR (s.updated_at_micros = ").push_bind(sort);
    builder.push(if before {
        " AND s.id < "
    } else {
        " AND s.id > "
    });
    builder.push_bind(id).push(")))");
    Ok(builder.build_query_scalar::<i64>().fetch_one(pool).await? != 0)
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
    use std::collections::HashSet;

    use crate::model::{Completeness, IndexState, SessionParentRelation};

    fn session(id: String, parent_thread_id: Option<String>) -> SessionSummary {
        SessionSummary {
            id: id.clone(),
            source: SourceKind::Cli,
            parent_thread_id,
            parent_relation: Some(SessionParentRelation::Parent),
            cwd: None,
            title: id,
            preview: String::new(),
            created_at: "2026-01-01T00:00:00.000000Z".to_owned(),
            updated_at: "2026-01-01T00:00:00.000000Z".to_owned(),
            archived: false,
            cli_version: None,
            provider: None,
            git: None,
            entry_count: 0,
            diagnostic_count: 0,
            index_state: IndexState::Ready,
            completeness: Completeness::Complete,
            freshness: SessionFreshness::Current,
        }
    }

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

    #[test]
    fn deep_session_groups_are_complete_bounded_and_iterative() {
        const SESSION_COUNT: usize = 10_000;
        let sessions = (0..SESSION_COUNT)
            .map(|index| {
                let id = format!("session-{index:05}");
                let parent = (index > 0).then(|| format!("session-{:05}", index - 1));
                session(id, parent)
            })
            .collect();

        let groups = build_session_groups(sessions);

        assert_eq!(groups.len(), 1);
        assert!(!groups[0].hierarchy_complete);
        let mut seen = HashSet::new();
        let mut max_depth = 0;
        let mut stack = vec![(&groups[0].root, 1_usize)];
        while let Some((node, depth)) = stack.pop() {
            assert!(seen.insert(node.session.id.clone()));
            max_depth = max_depth.max(depth);
            stack.extend(node.children.iter().map(|child| (child, depth + 1)));
        }
        assert_eq!(seen.len(), SESSION_COUNT);
        assert!(max_depth <= MAX_SESSION_TREE_DEPTH);
    }
}
