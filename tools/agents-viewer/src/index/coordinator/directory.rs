use super::*;

pub(super) fn automatic_priority(
    policy: InitialIndexPolicy,
    source: &DiscoveredSource,
    now_micros: i64,
) -> Option<WorkPriority> {
    if policy.is_recent(source.source.mtime_ns / 1_000, now_micros) {
        Some(WorkPriority::Recent)
    } else if policy.background_enabled() {
        Some(WorkPriority::Background)
    } else {
        None
    }
}

pub(super) fn record_outcome(
    report: &mut ReconcileReport,
    outcome: &crate::index::scanner::ScanOutcome,
) {
    report.indexed_files = report.indexed_files.saturating_add(1);
    report.appended_files = report
        .appended_files
        .saturating_add(u64::from(outcome.appended));
    report.reconcile_again |= outcome.changed_during_scan;
    if outcome.appended {
        report.appended_sessions.push(outcome.session_id.clone());
    }
    report.updated_sessions.push(outcome.session_id.clone());
}

pub(super) fn source_fingerprint(source: &DiscoveredSource) -> SourceFingerprint {
    SourceFingerprint {
        root_kind: root_kind_value(source.source.root_kind),
        relative_path: source.source.relative_path.clone(),
        file_key: source.source.file_key.clone(),
        size_bytes: source.source.size_bytes,
        mtime_ns: source.source.mtime_ns,
    }
}

pub(super) fn source_key(source: &DiscoveredSource) -> String {
    stored_key(source.source.root_kind, &source.source.relative_path)
}

pub(super) fn stored_key(root_kind: RootKind, relative_path: &str) -> String {
    format!("{}\0{relative_path}", root_kind_value(root_kind))
}

pub(super) fn root_kind_value(root_kind: RootKind) -> &'static str {
    match root_kind {
        RootKind::Active => "active",
        RootKind::Archived => "archived",
    }
}

pub(super) fn source_coordinates_for_path(
    roots: &SourceRoots,
    path: &Path,
) -> Option<(RootKind, String)> {
    for (root_kind, root) in [
        (RootKind::Active, roots.active.as_ref()),
        (RootKind::Archived, roots.archived.as_ref()),
    ] {
        let Some(root) = root else { continue };
        let Ok(relative) = path.strip_prefix(root) else {
            continue;
        };
        let relative = relative
            .components()
            .map(|component| component.as_os_str().to_string_lossy())
            .collect::<Vec<_>>()
            .join("/");
        return Some((root_kind, relative));
    }
    None
}

pub(super) fn metadata_matches(stored: &StoredSource, source: &DiscoveredSource) -> bool {
    stored.file_key == source.source.file_key
        && stored.size_bytes == source.source.size_bytes
        && stored.mtime_ns == source.source.mtime_ns
        && stored.session_id.as_deref() == Some(source.session_id.as_str())
        && (stored.catalog_complete || snapshot_matches(stored, source))
}

pub(super) fn snapshot_matches(stored: &StoredSource, source: &DiscoveredSource) -> bool {
    stored.snapshot_revision > 0
        && stored.snapshot_file_key.as_deref() == Some(source.source.file_key.as_str())
        && stored.snapshot_size_bytes == Some(source.source.size_bytes)
        && stored.snapshot_mtime_ns == Some(source.source.mtime_ns)
}

pub(super) async fn load_stored_sources(database: &Database) -> Result<Vec<StoredSource>> {
    let rows = sqlx::query(
        "SELECT root_kind, relative_path, file_key, size_bytes, mtime_ns, snapshot_file_key, \
            snapshot_size_bytes, snapshot_mtime_ns, snapshot_revision, catalog_complete, \
            scan_state, session_id \
         FROM source_files",
    )
    .fetch_all(database.pool())
    .await?;
    rows.iter().map(stored_source_from_row).collect()
}

pub(super) async fn load_stored_source(
    database: &Database,
    source: &DiscoveredSource,
) -> Result<Option<StoredSource>> {
    let row = sqlx::query(
        "SELECT root_kind, relative_path, file_key, size_bytes, mtime_ns, snapshot_file_key, \
            snapshot_size_bytes, snapshot_mtime_ns, snapshot_revision, catalog_complete, \
            scan_state, session_id \
         FROM source_files WHERE root_kind = ? AND relative_path = ?",
    )
    .bind(root_kind_value(source.source.root_kind))
    .bind(&source.source.relative_path)
    .fetch_optional(database.pool())
    .await?;
    row.as_ref().map(stored_source_from_row).transpose()
}

pub(super) fn stored_source_from_row(row: &sqlx::sqlite::SqliteRow) -> Result<StoredSource> {
    let root_kind = match row.get::<String, _>("root_kind").as_str() {
        "active" => RootKind::Active,
        "archived" => RootKind::Archived,
        value => return Err(anyhow!("invalid stored root kind: {value}")),
    };
    Ok(StoredSource {
        root_kind,
        relative_path: row.get("relative_path"),
        file_key: row.get("file_key"),
        size_bytes: u64::try_from(row.get::<i64, _>("size_bytes"))?,
        mtime_ns: row.get("mtime_ns"),
        snapshot_file_key: row.get("snapshot_file_key"),
        snapshot_size_bytes: row
            .get::<Option<i64>, _>("snapshot_size_bytes")
            .map(u64::try_from)
            .transpose()?,
        snapshot_mtime_ns: row.get("snapshot_mtime_ns"),
        snapshot_revision: u64::try_from(row.get::<i64, _>("snapshot_revision"))?,
        catalog_complete: row.get::<i64, _>("catalog_complete") != 0,
        scan_state: row.get("scan_state"),
        session_id: row.get("session_id"),
    })
}

pub(super) fn report_is_healthy(report: &ReconcileReport) -> bool {
    report.failed_files == 0 && report.discovery_issues == 0 && !report.reconcile_again
}

pub(super) async fn send_update(sender: Option<&mpsc::Sender<IndexUpdate>>, update: IndexUpdate) {
    if let Some(sender) = sender {
        let _ = sender.send(update).await;
    }
}

pub(super) async fn send_session_committed(
    sender: Option<&mpsc::Sender<IndexUpdate>>,
    notified: &mut HashSet<String>,
    generation: u64,
    session_id: &str,
) {
    if notified.insert(session_id.to_owned()) {
        send_update(
            sender,
            IndexUpdate::SessionCommitted {
                generation,
                session_id: session_id.to_owned(),
            },
        )
        .await;
    }
}
