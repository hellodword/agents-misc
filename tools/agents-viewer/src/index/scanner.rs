use std::collections::{BTreeMap, HashMap};
use std::fs::File;
use std::io::{self, BufReader, Read as _, Seek as _, SeekFrom};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::{Context as _, Result, anyhow, bail};
use sha2::{Digest, Sha256};
use sqlx::Row as _;
use tokio_util::sync::CancellationToken;

use crate::model::DiagnosticSeverity;
use crate::paths::SourceRoots;
use crate::permissions::{open_source_read_only, opened_file_identity};
use crate::rollout::{
    EntryOrigin, NormalizedEntry, ParseContext, ParseSeed, ParseSink as _, ParserDiagnostic,
    ParserOutput, RootKind, SessionRecord,
};

use super::control::{IoGate, ScanLease, WorkPriority};
use super::writer::{BatchingSink, ScanMode, SourceFileRecord, WriterHandle};
use super::{Database, InitialIndexPolicy};

const FINGERPRINT_BYTES: usize = 64 * 1024;

#[derive(Clone, Debug)]
pub struct DiscoveredSource {
    pub root: PathBuf,
    pub path: PathBuf,
    pub session_id: String,
    pub created_at_micros: i64,
    pub source: SourceFileRecord,
    pub duplicate_paths: Vec<String>,
}

#[derive(Clone, Debug)]
pub struct DiscoveryIssue {
    pub code: String,
    pub message: String,
}

#[derive(Clone, Debug, Default)]
pub struct Discovery {
    pub sources: Vec<DiscoveredSource>,
    pub issues: Vec<DiscoveryIssue>,
    pub total_bytes: u64,
    pub excluded_files: u64,
    pub excluded_bytes: u64,
}

#[derive(Clone, Debug)]
pub struct ScanOutcome {
    pub source_file_id: i64,
    pub session_id: String,
    pub changed_during_scan: bool,
    pub appended: bool,
}

struct ScanPlan {
    mode: ScanMode,
    seed: Option<ParseSeed>,
    tail_seed: Vec<u8>,
}

pub fn discover_sources(
    roots: &SourceRoots,
    max_event_bytes: usize,
    generation: u64,
    now_micros: i64,
    policy: InitialIndexPolicy,
) -> Discovery {
    discover_sources_inner(roots, max_event_bytes, generation, now_micros, policy, None)
        .expect("discovery without cancellation cannot be interrupted")
}

pub fn discover_sources_cancellable(
    roots: &SourceRoots,
    max_event_bytes: usize,
    generation: u64,
    now_micros: i64,
    policy: InitialIndexPolicy,
    shutdown: &CancellationToken,
) -> Result<Discovery> {
    discover_sources_inner(
        roots,
        max_event_bytes,
        generation,
        now_micros,
        policy,
        Some(shutdown),
    )
}

pub fn discover_source_path(
    roots: &SourceRoots,
    path: &Path,
    generation: u64,
) -> Result<Option<DiscoveredSource>> {
    if path.extension().and_then(|value| value.to_str()) != Some("jsonl") || !path.is_file() {
        return Ok(None);
    }
    for (root_kind, root) in [
        (RootKind::Active, roots.active.as_ref()),
        (RootKind::Archived, roots.archived.as_ref()),
    ] {
        let Some(root) = root else { continue };
        if path.strip_prefix(root).is_ok() {
            return discover_file(root, path, root_kind, generation).map(Some);
        }
    }
    Ok(None)
}

fn discover_sources_inner(
    roots: &SourceRoots,
    _max_event_bytes: usize,
    generation: u64,
    now_micros: i64,
    policy: InitialIndexPolicy,
    shutdown: Option<&CancellationToken>,
) -> Result<Discovery> {
    let mut discovered = Vec::new();
    let mut issues = Vec::new();
    let mut excluded_files = 0_u64;
    let mut excluded_bytes = 0_u64;
    for (root_kind, root) in [
        (RootKind::Active, roots.active.as_ref()),
        (RootKind::Archived, roots.archived.as_ref()),
    ] {
        let Some(root) = root else { continue };
        for entry in walkdir::WalkDir::new(root).follow_links(false).into_iter() {
            if shutdown.is_some_and(CancellationToken::is_cancelled) {
                bail!("index discovery cancelled");
            }
            let entry = match entry {
                Ok(entry) => entry,
                Err(_) => {
                    issues.push(DiscoveryIssue {
                        code: "source_unavailable".into(),
                        message: "source directory entry could not be inspected".into(),
                    });
                    continue;
                }
            };
            if !entry.file_type().is_file()
                || entry.path().extension().and_then(|value| value.to_str()) != Some("jsonl")
            {
                continue;
            }
            match discover_file(root, entry.path(), root_kind, generation) {
                Ok(source) => discovered.push(source),
                Err(error) => issues.push(DiscoveryIssue {
                    code: "source_changed".into(),
                    message: format!("source file skipped during discovery: {error:#}"),
                }),
            }
        }
    }

    let mut winners = HashMap::<String, DiscoveredSource>::new();
    for source in discovered {
        let session_id = source.session_id.clone();
        if let Some(current) = winners.get_mut(&session_id) {
            if source_precedes(&source, current) {
                let loser = current.source.relative_path.clone();
                let mut replacement = source;
                replacement
                    .duplicate_paths
                    .extend(current.duplicate_paths.clone());
                replacement.duplicate_paths.push(loser);
                *current = replacement;
            } else {
                current
                    .duplicate_paths
                    .push(source.source.relative_path.clone());
            }
        } else {
            winners.insert(session_id, source);
        }
    }
    let mut sources = winners.into_values().collect::<Vec<_>>();
    sources.sort_by(|left, right| {
        right
            .source
            .mtime_ns
            .cmp(&left.source.mtime_ns)
            .then_with(|| right.created_at_micros.cmp(&left.created_at_micros))
            .then_with(|| left.session_id.cmp(&right.session_id))
            .then_with(|| left.source.relative_path.cmp(&right.source.relative_path))
    });
    if !policy.background_enabled() {
        for source in &sources {
            let updated_at_micros = source.source.mtime_ns / 1_000;
            if !policy.is_recent(updated_at_micros, now_micros) {
                excluded_files = excluded_files.saturating_add(1);
                excluded_bytes = excluded_bytes.saturating_add(source.source.size_bytes);
            }
        }
    }
    let total_bytes = sources.iter().map(|source| source.source.size_bytes).sum();
    Ok(Discovery {
        sources,
        issues,
        total_bytes,
        excluded_files,
        excluded_bytes,
    })
}

pub async fn scan_source(
    database: Database,
    writer: WriterHandle,
    discovered: DiscoveredSource,
    max_event_bytes: usize,
    now_micros: i64,
    shutdown: CancellationToken,
) -> Result<ScanOutcome> {
    let gate = IoGate::new();
    let lease = gate.register(WorkPriority::Recent)?;
    scan_source_with_lease(
        database,
        writer,
        discovered,
        max_event_bytes,
        now_micros,
        shutdown,
        lease,
    )
    .await
}

pub async fn scan_source_with_lease(
    database: Database,
    writer: WriterHandle,
    discovered: DiscoveredSource,
    max_event_bytes: usize,
    now_micros: i64,
    shutdown: CancellationToken,
    lease: ScanLease,
) -> Result<ScanOutcome> {
    if shutdown.is_cancelled() {
        bail!("index scan cancelled");
    }
    let hydrate_source = discovered;
    let hydrate_lease = lease.clone();
    let hydrate_shutdown = shutdown.clone();
    let discovered = tokio::task::spawn_blocking(move || {
        hydrate_source_fingerprints(hydrate_source, &hydrate_lease, &hydrate_shutdown)
    })
    .await
    .context("source fingerprint task panicked")??;
    let scan_token = uuid::Uuid::new_v4().to_string();
    let plan = append_plan(&database, &discovered, &lease, &shutdown).await?;
    let mode = plan.mode;
    let source_file_id = writer
        .begin_with_priority(
            discovered.source.clone(),
            scan_token.clone(),
            mode,
            lease.priority(),
        )
        .await?;
    let blocking_writer = writer.clone();
    let blocking_token = scan_token.clone();
    let blocking_source = discovered.clone();
    let blocking_shutdown = shutdown.clone();
    let blocking_lease = lease.clone();
    let result = tokio::task::spawn_blocking(move || {
        parse_source_blocking(
            blocking_writer,
            source_file_id,
            blocking_token,
            blocking_source,
            max_event_bytes,
            now_micros,
            mode,
            plan.seed,
            plan.tail_seed,
            blocking_shutdown,
            blocking_lease,
        )
    })
    .await
    .context("source parser task panicked")?;
    match result {
        Ok((mut summary, changed_during_scan, final_tail_hash)) if !shutdown.is_cancelled() => {
            summary.session.diagnostic_count = summary
                .session
                .diagnostic_count
                .saturating_add(discovered.duplicate_paths.len() as u64);
            writer
                .finish_with_priority_and_tail(
                    source_file_id,
                    scan_token,
                    summary.clone(),
                    mode,
                    lease.priority(),
                    Some(final_tail_hash),
                )
                .await?;
            Ok(ScanOutcome {
                source_file_id,
                session_id: summary.session.id,
                changed_during_scan,
                appended: matches!(mode, ScanMode::Append { .. }),
            })
        }
        Ok(_) => {
            // A cancelled process is about to close this database. Deleting a large staged
            // scan here can exceed the shutdown deadline; startup already clears all staging.
            bail!("index scan cancelled")
        }
        Err(error) => {
            if !shutdown.is_cancelled() {
                writer
                    .abort_with_priority(scan_token, lease.priority())
                    .await?;
            }
            Err(error)
        }
    }
}

fn discover_file(
    root: &Path,
    path: &Path,
    root_kind: RootKind,
    generation: u64,
) -> Result<DiscoveredSource> {
    let opened = open_source_read_only(root, path)?;
    let relative_path = normalized_relative(root, &opened.canonical_path)?;
    let file_name = opened
        .canonical_path
        .file_name()
        .and_then(|value| value.to_str())
        .ok_or_else(|| anyhow!("source filename is not valid Unicode"))?
        .to_owned();
    let session_id =
        crate::rollout::normalize::session_id_from_filename(&file_name, &relative_path);
    let modified_at_micros = system_time_micros(opened.identity.modified.unwrap_or(UNIX_EPOCH));
    let created_at_micros = crate::rollout::normalize::timestamp_from_filename(&file_name)
        .unwrap_or(modified_at_micros);
    let after = opened_file_identity(
        &opened.file,
        &opened
            .file
            .metadata()
            .context("re-stat discovered source")?,
        &opened.canonical_path,
    );
    if after != opened.identity {
        bail!("source changed during metadata discovery");
    }
    Ok(DiscoveredSource {
        root: root.to_path_buf(),
        path: opened.canonical_path,
        session_id,
        created_at_micros,
        source: SourceFileRecord {
            root_kind,
            relative_path,
            file_key: after.file_key,
            size_bytes: after.size,
            mtime_ns: system_time_nanos(after.modified.unwrap_or(UNIX_EPOCH)),
            head_hash: None,
            tail_hash: None,
            generation,
            placeholder: None,
        },
        duplicate_paths: Vec::new(),
    })
}

#[allow(clippy::too_many_arguments)]
fn parse_source_blocking(
    writer: WriterHandle,
    source_file_id: i64,
    scan_token: String,
    discovered: DiscoveredSource,
    max_event_bytes: usize,
    now_micros: i64,
    mode: ScanMode,
    seed: Option<ParseSeed>,
    tail_seed: Vec<u8>,
    shutdown: CancellationToken,
    lease: ScanLease,
) -> Result<(crate::rollout::ParseSummary, bool, String)> {
    let mut opened = open_source_read_only(&discovered.root, &discovered.path)?;
    let modified_at_micros = system_time_micros(opened.identity.modified.unwrap_or(UNIX_EPOCH));
    let context = ParseContext {
        root_kind: discovered.source.root_kind,
        relative_path: discovered.source.relative_path.clone(),
        file_name: discovered
            .path
            .file_name()
            .and_then(|value| value.to_str())
            .ok_or_else(|| anyhow!("source filename is not valid Unicode"))?
            .to_owned(),
        modified_at_micros,
        now_micros,
        max_event_bytes,
    };
    let mut sink = BatchingSink::new_with_lease(writer, source_file_id, scan_token, lease.clone());
    for duplicate in &discovered.duplicate_paths {
        sink.emit(ParserOutput::Diagnostic(ParserDiagnostic {
            severity: DiagnosticSeverity::Warning,
            code: "duplicate_session".into(),
            message: format!("lower-priority duplicate rollout ignored: {duplicate}"),
            line_no: None,
            raw_ref_id: None,
        }));
    }
    if let ScanMode::Append { checkpoint_offset } = mode {
        opened.file.seek(SeekFrom::Start(checkpoint_offset))?;
    }
    let snapshot_size = discovered.source.size_bytes;
    if opened.identity.file_key != discovered.source.file_key
        || opened.identity.size < snapshot_size
    {
        bail!("source changed before parsing began");
    }
    let start_offset = match mode {
        ScanMode::Full => 0,
        ScanMode::Append { checkpoint_offset } => checkpoint_offset,
    };
    let snapshot_bytes = snapshot_size.saturating_sub(start_offset);
    let controlled = ControlledReader::with_tail_seed(
        (&opened.file).take(snapshot_bytes),
        lease,
        shutdown.clone(),
        tail_seed,
    );
    let mut reader = BufReader::new(controlled);
    let summary = match seed {
        Some(seed) => crate::rollout::normalize::parse_rollout_from_seed_cancellable(
            &mut reader,
            &context,
            &mut sink,
            seed,
            &shutdown,
        )?,
        None => crate::rollout::normalize::parse_rollout_cancellable(
            &mut reader,
            &context,
            &mut sink,
            &shutdown,
        )?,
    };
    sink.finish()?;
    let final_tail_hash = reader.into_inner().tail_hash();
    let after = opened_file_identity(
        &opened.file,
        &opened.file.metadata().context("re-stat parsed source")?,
        &opened.canonical_path,
    );
    let changed = after != opened.identity;
    Ok((summary, changed, final_tail_hash))
}

async fn append_plan(
    database: &Database,
    discovered: &DiscoveredSource,
    lease: &ScanLease,
    shutdown: &CancellationToken,
) -> Result<ScanPlan> {
    let root_kind = match discovered.source.root_kind {
        RootKind::Active => "active",
        RootKind::Archived => "archived",
    };
    let Some(source) = sqlx::query(
        "SELECT id, file_key, size_bytes, head_hash, tail_hash, checkpoint_offset, checkpoint_line, \
            checkpoint_hash, session_id, scan_state \
         FROM source_files WHERE root_kind = ? AND relative_path = ?",
    )
    .bind(root_kind)
    .bind(&discovered.source.relative_path)
    .fetch_optional(database.pool())
    .await?
    else {
        return Ok(full_scan_plan());
    };
    let old_size = source.get::<i64, _>("size_bytes");
    let checkpoint_offset = source.get::<i64, _>("checkpoint_offset");
    let head_matches = old_size < FINGERPRINT_BYTES as i64
        || source.get::<Option<String>, _>("head_hash") == discovered.source.head_hash;
    let append_candidate = source.get::<String, _>("file_key") == discovered.source.file_key
        && source.get::<String, _>("scan_state") == "ready"
        && old_size >= 0
        && discovered.source.size_bytes > u64::try_from(old_size).unwrap_or(u64::MAX)
        && head_matches
        && checkpoint_offset >= 0;
    if !append_candidate {
        return Ok(full_scan_plan());
    }
    let checkpoint_offset = u64::try_from(checkpoint_offset)?;
    let Some(stored_checkpoint_hash) = source.get::<Option<String>, _>("checkpoint_hash") else {
        return Ok(full_scan_plan());
    };
    if stored_checkpoint_hash.is_empty() {
        // Releases that did not preserve the full-prefix hash left an empty compatibility value.
        // Rebuild this source once on its next growth so future appends can be verified strictly.
        return Ok(full_scan_plan());
    }
    let Some(stored_tail) = source.get::<Option<String>, _>("tail_hash") else {
        return Ok(full_scan_plan());
    };
    let Some(old_tail) = verify_old_tail(
        discovered,
        u64::try_from(old_size)?,
        &stored_tail,
        lease,
        shutdown,
    )?
    else {
        return Ok(full_scan_plan());
    };
    let Some(stable_hasher) = verify_checkpoint_prefix(
        discovered,
        checkpoint_offset,
        &stored_checkpoint_hash,
        lease,
        shutdown,
    )?
    else {
        return Ok(full_scan_plan());
    };
    let source_file_id = source.get::<i64, _>("id");
    let session_id = source
        .get::<Option<String>, _>("session_id")
        .ok_or_else(|| anyhow!("ready source has no session"))?;
    let session = load_session(database, &session_id).await?;
    let next_sequence = sqlx::query_scalar::<_, Option<i64>>(
        "SELECT MAX(sequence) FROM entries WHERE session_id = ?",
    )
    .bind(&session_id)
    .fetch_one(database.pool())
    .await?
    .unwrap_or_default();
    let mut occurrences = HashMap::new();
    for row in sqlx::query(
        "SELECT id_basis, COUNT(*) AS occurrence_count FROM entries \
         WHERE session_id = ? GROUP BY id_basis",
    )
    .bind(&session_id)
    .fetch_all(database.pool())
    .await?
    {
        occurrences.insert(
            row.get::<String, _>("id_basis"),
            u64::try_from(row.get::<i64, _>("occurrence_count"))?,
        );
    }
    let recent = load_recent_entries(database, &session_id).await?;
    let recognized_record_count = sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*) FROM raw_records WHERE source_file_id = ? AND parse_status = 'valid'",
    )
    .bind(source_file_id)
    .fetch_one(database.pool())
    .await?;
    let checkpoint_line = u64::try_from(source.get::<i64, _>("checkpoint_line"))?;
    let old_size = u64::try_from(old_size)?;
    let old_tail_start = old_size.saturating_sub(old_tail.len() as u64);
    let prefix_end = checkpoint_offset
        .saturating_sub(old_tail_start)
        .min(old_tail.len() as u64) as usize;
    let prefix_start = prefix_end.saturating_sub(FINGERPRINT_BYTES);
    Ok(ScanPlan {
        mode: ScanMode::Append { checkpoint_offset },
        seed: Some(ParseSeed {
            partial: matches!(
                session.completeness,
                crate::model::Completeness::Partial | crate::model::Completeness::Unsupported
            ),
            session,
            next_sequence,
            occurrences,
            recent,
            raw_record_count: checkpoint_line,
            recognized_record_count: u64::try_from(recognized_record_count)?,
            checkpoint_offset,
            checkpoint_line,
            stable_hasher,
        }),
        tail_seed: old_tail[prefix_start..prefix_end].to_vec(),
    })
}

fn full_scan_plan() -> ScanPlan {
    ScanPlan {
        mode: ScanMode::Full,
        seed: None,
        tail_seed: Vec::new(),
    }
}

async fn load_session(database: &Database, id: &str) -> Result<SessionRecord> {
    let row = sqlx::query("SELECT * FROM sessions WHERE id = ?")
        .bind(id)
        .fetch_one(database.pool())
        .await?;
    Ok(SessionRecord {
        id: row.get("id"),
        source: decode_enum(&row.get::<String, _>("source_kind"))?,
        parent_thread_id: row.get("parent_thread_id"),
        parent_relation: decode_optional_enum(row.get("parent_relation"))?,
        proposed_plan_hash: row.get("proposed_plan_hash"),
        proposed_plan_at_micros: row.get("proposed_plan_at_micros"),
        handoff_plan_hash: row.get("handoff_plan_hash"),
        handoff_at_micros: row.get("handoff_at_micros"),
        cwd: row.get("cwd"),
        title: row.get("title"),
        preview: row.get("preview"),
        created_at_micros: row.get("created_at_micros"),
        updated_at_micros: row.get("updated_at_micros"),
        archived: row.get("archived"),
        cli_version: row.get("cli_version"),
        provider: row.get("provider"),
        history_line: row
            .get::<Option<i64>, _>("history_line")
            .map(u64::try_from)
            .transpose()?,
        git_branch: row.get("git_branch"),
        git_commit: row.get("git_commit"),
        entry_count: u64::try_from(row.get::<i64, _>("entry_count"))?,
        index_state: decode_enum(&row.get::<String, _>("index_state"))?,
        completeness: decode_enum(&row.get::<String, _>("completeness"))?,
        diagnostic_count: u64::try_from(row.get::<i64, _>("diagnostic_count"))?,
    })
}

async fn load_recent_entries(
    database: &Database,
    session_id: &str,
) -> Result<Vec<(u64, NormalizedEntry)>> {
    let rows = sqlx::query(
        "SELECT e.*, COALESCE(MAX(r.line_no), 0) AS last_line \
         FROM entries e \
         LEFT JOIN entry_raw_refs x ON x.entry_id = e.id \
         LEFT JOIN raw_records r ON r.id = x.raw_id \
         WHERE e.session_id = ? AND (e.sequence > (SELECT COALESCE(MAX(sequence), 0) - 32 \
             FROM entries WHERE session_id = ?) OR e.tool_status IN ('pending', 'running')) \
         GROUP BY e.rowid ORDER BY e.sequence",
    )
    .bind(session_id)
    .bind(session_id)
    .fetch_all(database.pool())
    .await?;
    let mut entries = Vec::with_capacity(rows.len());
    for row in rows {
        let id = row.get::<String, _>("id");
        let raw_refs = sqlx::query_scalar::<_, String>(
            "SELECT raw_id FROM entry_raw_refs WHERE entry_id = ? ORDER BY ordinal",
        )
        .bind(&id)
        .fetch_all(database.pool())
        .await?;
        entries.push((
            u64::try_from(row.get::<i64, _>("last_line"))?,
            NormalizedEntry {
                id,
                session_id: row.get("session_id"),
                sequence: row.get("sequence"),
                timestamp_micros: row.get("timestamp_micros"),
                kind: decode_enum(&row.get::<String, _>("kind"))?,
                presentation: decode_enum(&row.get::<String, _>("presentation"))?,
                role: decode_optional_enum(row.get("role"))?,
                phase: decode_optional_enum(row.get("phase"))?,
                tool_kind: decode_optional_enum(row.get("tool_kind"))?,
                tool_status: decode_optional_enum(row.get("tool_status"))?,
                title: row.get("title"),
                primary_text: row.get("primary_text"),
                secondary_text: row.get("secondary_text"),
                metadata: serde_json::from_str::<BTreeMap<String, serde_json::Value>>(
                    &row.get::<String, _>("metadata_json"),
                )?,
                call_id: row.get("call_id"),
                parent_entry_id: row.get("parent_entry_id"),
                default_collapsed: row.get("default_collapsed"),
                searchable: row.get("searchable"),
                raw_refs,
                origin: EntryOrigin::EventPresentation,
                id_basis: row.get("id_basis"),
            },
        ));
    }
    Ok(entries)
}

fn decode_enum<T: serde::de::DeserializeOwned>(value: &str) -> Result<T> {
    Ok(serde_json::from_value(serde_json::Value::String(
        value.to_owned(),
    ))?)
}

fn decode_optional_enum<T: serde::de::DeserializeOwned>(
    value: Option<String>,
) -> Result<Option<T>> {
    value.as_deref().map(decode_enum).transpose()
}

pub(crate) fn source_precedes(left: &DiscoveredSource, right: &DiscoveredSource) -> bool {
    let left_active = left.source.root_kind == RootKind::Active;
    let right_active = right.source.root_kind == RootKind::Active;
    left_active
        .cmp(&right_active)
        .then_with(|| left.source.mtime_ns.cmp(&right.source.mtime_ns))
        .then_with(|| right.source.relative_path.cmp(&left.source.relative_path))
        .is_gt()
}

fn normalized_relative(root: &Path, path: &Path) -> Result<String> {
    let relative = path.strip_prefix(root).context("source escaped root")?;
    Ok(relative
        .components()
        .map(|component| component.as_os_str().to_string_lossy())
        .collect::<Vec<_>>()
        .join("/"))
}

fn hydrate_source_fingerprints(
    mut discovered: DiscoveredSource,
    lease: &ScanLease,
    shutdown: &CancellationToken,
) -> Result<DiscoveredSource> {
    let mut opened = open_source_read_only(&discovered.root, &discovered.path)?;
    let head_len =
        FINGERPRINT_BYTES.min(usize::try_from(opened.identity.size).unwrap_or(usize::MAX));
    let head = read_controlled_range(&mut opened.file, 0, head_len, lease, shutdown)?;
    let after = opened_file_identity(
        &opened.file,
        &opened
            .file
            .metadata()
            .context("re-stat fingerprinted source")?,
        &opened.canonical_path,
    );
    if after != opened.identity {
        bail!("source changed while its bounded fingerprint was read");
    }
    discovered.path = opened.canonical_path;
    discovered.source.file_key = after.file_key;
    discovered.source.size_bytes = after.size;
    discovered.source.mtime_ns = system_time_nanos(after.modified.unwrap_or(UNIX_EPOCH));
    discovered.source.head_hash = Some(sha256_hex(&head));
    // The final tail is captured while the parser reads the snapshot, avoiding a third window
    // read on append.
    discovered.source.tail_hash = None;
    Ok(discovered)
}

fn verify_old_tail(
    discovered: &DiscoveredSource,
    old_size: u64,
    expected_hash: &str,
    lease: &ScanLease,
    shutdown: &CancellationToken,
) -> Result<Option<Vec<u8>>> {
    let mut opened = open_source_read_only(&discovered.root, &discovered.path)?;
    if opened.identity.file_key != discovered.source.file_key || opened.identity.size < old_size {
        return Ok(None);
    }
    let tail_len = FINGERPRINT_BYTES.min(usize::try_from(old_size).unwrap_or(usize::MAX));
    let tail = read_controlled_range(
        &mut opened.file,
        old_size.saturating_sub(tail_len as u64),
        tail_len,
        lease,
        shutdown,
    )?;
    let after = opened_file_identity(
        &opened.file,
        &opened.file.metadata().context("re-stat append candidate")?,
        &opened.canonical_path,
    );
    if after != opened.identity || sha256_hex(&tail) != expected_hash {
        return Ok(None);
    }
    Ok(Some(tail))
}

fn verify_checkpoint_prefix(
    discovered: &DiscoveredSource,
    checkpoint_offset: u64,
    expected_hash: &str,
    lease: &ScanLease,
    shutdown: &CancellationToken,
) -> Result<Option<Sha256>> {
    let mut opened = open_source_read_only(&discovered.root, &discovered.path)?;
    if opened.identity.file_key != discovered.source.file_key
        || opened.identity.size < checkpoint_offset
    {
        return Ok(None);
    }
    let mut reader = ControlledReader::new(
        (&mut opened.file).take(checkpoint_offset),
        lease.clone(),
        shutdown.clone(),
    );
    let mut hasher = Sha256::new();
    let mut buffer = [0_u8; FINGERPRINT_BYTES];
    let mut read_total = 0_u64;
    loop {
        let read = reader.read(&mut buffer)?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
        read_total = read_total.saturating_add(read as u64);
    }
    drop(reader);
    let after = opened_file_identity(
        &opened.file,
        &opened
            .file
            .metadata()
            .context("re-stat checkpoint prefix")?,
        &opened.canonical_path,
    );
    if read_total != checkpoint_offset
        || after != opened.identity
        || sha256_hasher_hex(&hasher) != expected_hash
    {
        return Ok(None);
    }
    Ok(Some(hasher))
}

fn read_controlled_range(
    file: &mut File,
    offset: u64,
    length: usize,
    lease: &ScanLease,
    shutdown: &CancellationToken,
) -> Result<Vec<u8>> {
    file.seek(SeekFrom::Start(offset))?;
    let mut reader = ControlledReader::new(
        (&mut *file).take(length as u64),
        lease.clone(),
        shutdown.clone(),
    );
    let mut bytes = Vec::with_capacity(length);
    reader.read_to_end(&mut bytes)?;
    if bytes.len() != length {
        bail!("source changed while a bounded fingerprint window was read");
    }
    Ok(bytes)
}

struct ControlledReader<R> {
    inner: R,
    lease: ScanLease,
    shutdown: CancellationToken,
    tail: Vec<u8>,
}

impl<R> ControlledReader<R> {
    fn new(inner: R, lease: ScanLease, shutdown: CancellationToken) -> Self {
        Self::with_tail_seed(inner, lease, shutdown, Vec::new())
    }

    fn with_tail_seed(
        inner: R,
        lease: ScanLease,
        shutdown: CancellationToken,
        mut tail: Vec<u8>,
    ) -> Self {
        if tail.len() > FINGERPRINT_BYTES {
            tail.drain(..tail.len() - FINGERPRINT_BYTES);
        }
        Self {
            inner,
            lease,
            shutdown,
            tail,
        }
    }

    fn tail_hash(&self) -> String {
        sha256_hex(&self.tail)
    }

    fn capture_tail(&mut self, bytes: &[u8]) {
        if bytes.len() >= FINGERPRINT_BYTES {
            self.tail.clear();
            self.tail
                .extend_from_slice(&bytes[bytes.len() - FINGERPRINT_BYTES..]);
            return;
        }
        let overflow = self
            .tail
            .len()
            .saturating_add(bytes.len())
            .saturating_sub(FINGERPRINT_BYTES);
        if overflow > 0 {
            self.tail.drain(..overflow);
        }
        self.tail.extend_from_slice(bytes);
    }
}

impl<R: io::Read> io::Read for ControlledReader<R> {
    fn read(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
        if buffer.is_empty() {
            return Ok(0);
        }
        let permit = self.lease.before_io(buffer.len(), &self.shutdown)?;
        let read = self.inner.read(&mut buffer[..permit.max_bytes()])?;
        drop(permit);
        self.lease.record_read(read);
        self.capture_tail(&buffer[..read]);
        Ok(read)
    }
}

fn system_time_micros(time: SystemTime) -> i64 {
    time.duration_since(UNIX_EPOCH)
        .ok()
        .and_then(|duration| i64::try_from(duration.as_micros()).ok())
        .unwrap_or_default()
}

fn system_time_nanos(time: SystemTime) -> i64 {
    time.duration_since(UNIX_EPOCH)
        .ok()
        .and_then(|duration| i64::try_from(duration.as_nanos()).ok())
        .unwrap_or_default()
}

fn sha256_hex(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    let mut output = String::with_capacity(digest.len() * 2);
    for byte in digest {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}

fn sha256_hasher_hex(hasher: &Sha256) -> String {
    let digest = hasher.clone().finalize();
    let mut output = String::with_capacity(digest.len() * 2);
    for byte in digest {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}
