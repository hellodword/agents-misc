use std::collections::{BTreeMap, HashMap};
use std::fs::File;
use std::io::{BufReader, Read as _, Seek as _, SeekFrom};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::{Context as _, Result, anyhow, bail};
use sha2::{Digest, Sha256};
use sqlx::Row as _;
use tokio_util::sync::CancellationToken;

use crate::model::{Completeness, DiagnosticSeverity, IndexState, SourceKind};
use crate::paths::SourceRoots;
use crate::permissions::{file_identity, open_source_read_only};
use crate::rollout::{
    BoundedJsonlReader, CollectingSink, EntryOrigin, LineReadStatus, NormalizedEntry, ParseContext,
    ParseSeed, ParseSink as _, ParserDiagnostic, ParserOutput, RootKind, SessionRecord,
    parse_rollout, verify_checkpoint,
};

use super::writer::{BatchingSink, ScanMode, SourceFileRecord, WriterHandle};
use super::{Database, InitialIndexPolicy};

const FINGERPRINT_BYTES: usize = 64 * 1024;

#[derive(Clone, Debug)]
pub struct DiscoveredSource {
    pub root: PathBuf,
    pub path: PathBuf,
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

enum FileDiscovery {
    Included(Box<DiscoveredSource>),
    Excluded(u64),
}

#[derive(Clone, Debug)]
pub struct ScanOutcome {
    pub source_file_id: i64,
    pub session_id: String,
    pub changed_during_scan: bool,
    pub appended: bool,
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

fn discover_sources_inner(
    roots: &SourceRoots,
    max_event_bytes: usize,
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
            match discover_file(
                root,
                entry.path(),
                root_kind,
                max_event_bytes,
                generation,
                now_micros,
                policy,
            ) {
                Ok(FileDiscovery::Included(source)) => discovered.push(*source),
                Ok(FileDiscovery::Excluded(bytes)) => {
                    excluded_files = excluded_files.saturating_add(1);
                    excluded_bytes = excluded_bytes.saturating_add(bytes);
                }
                Err(error) => issues.push(DiscoveryIssue {
                    code: "source_changed".into(),
                    message: format!("source file skipped during discovery: {error:#}"),
                }),
            }
        }
    }

    let mut winners = HashMap::<String, DiscoveredSource>::new();
    for source in discovered {
        let session_id = source
            .source
            .placeholder
            .as_ref()
            .map(|session| session.id.clone())
            .unwrap_or_else(|| source.source.relative_path.clone());
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
            .then_with(|| left.source.relative_path.cmp(&right.source.relative_path))
    });
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
    if shutdown.is_cancelled() {
        bail!("index scan cancelled");
    }
    let scan_token = uuid::Uuid::new_v4().to_string();
    let (mode, seed) = append_plan(&database, &discovered).await?;
    let source_file_id = writer
        .begin(discovered.source.clone(), scan_token.clone(), mode)
        .await?;
    let blocking_writer = writer.clone();
    let blocking_token = scan_token.clone();
    let blocking_source = discovered.clone();
    let blocking_shutdown = shutdown.clone();
    let result = tokio::task::spawn_blocking(move || {
        parse_source_blocking(
            blocking_writer,
            source_file_id,
            blocking_token,
            blocking_source,
            max_event_bytes,
            now_micros,
            mode,
            seed,
            blocking_shutdown,
        )
    })
    .await
    .context("source parser task panicked")?;
    match result {
        Ok((mut summary, changed_during_scan)) if !shutdown.is_cancelled() => {
            summary.session.diagnostic_count = summary
                .session
                .diagnostic_count
                .saturating_add(discovered.duplicate_paths.len() as u64);
            writer
                .finish(source_file_id, scan_token, summary.clone(), mode)
                .await?;
            Ok(ScanOutcome {
                source_file_id,
                session_id: summary.session.id,
                changed_during_scan,
                appended: matches!(mode, ScanMode::Append { .. }),
            })
        }
        Ok(_) => {
            writer.abort(scan_token).await?;
            bail!("index scan cancelled")
        }
        Err(error) => {
            writer.abort(scan_token).await?;
            Err(error)
        }
    }
}

fn discover_file(
    root: &Path,
    path: &Path,
    root_kind: RootKind,
    max_event_bytes: usize,
    generation: u64,
    now_micros: i64,
    policy: InitialIndexPolicy,
) -> Result<FileDiscovery> {
    let mut opened = open_source_read_only(root, path)?;
    let relative_path = normalized_relative(root, &opened.canonical_path)?;
    let file_name = opened
        .canonical_path
        .file_name()
        .and_then(|value| value.to_str())
        .ok_or_else(|| anyhow!("source filename is not valid Unicode"))?
        .to_owned();
    let modified_at_micros = system_time_micros(opened.identity.modified.unwrap_or(UNIX_EPOCH));
    let context = ParseContext {
        root_kind,
        relative_path: relative_path.clone(),
        file_name,
        modified_at_micros,
        now_micros,
        max_event_bytes,
    };
    let placeholder = metadata_placeholder(&mut opened.file, &context)?;
    if !policy.includes(placeholder.created_at_micros) {
        let after = file_identity(
            &opened.file.metadata().context("re-stat excluded source")?,
            &opened.canonical_path,
        );
        if after != opened.identity {
            bail!("source changed during metadata discovery");
        }
        return Ok(FileDiscovery::Excluded(after.size));
    }
    let (head_hash, tail_hash) = head_tail_hash(&mut opened.file, opened.identity.size)?;
    let after = file_identity(
        &opened
            .file
            .metadata()
            .context("re-stat discovered source")?,
        &opened.canonical_path,
    );
    if after != opened.identity {
        bail!("source changed during metadata discovery");
    }
    Ok(FileDiscovery::Included(Box::new(DiscoveredSource {
        root: root.to_path_buf(),
        path: opened.canonical_path,
        source: SourceFileRecord {
            root_kind,
            relative_path,
            file_key: after.file_key,
            size_bytes: after.size,
            mtime_ns: system_time_nanos(after.modified.unwrap_or(UNIX_EPOCH)),
            head_hash: Some(head_hash),
            tail_hash: Some(tail_hash),
            generation,
            placeholder: Some(placeholder),
        },
        duplicate_paths: Vec::new(),
    })))
}

fn metadata_placeholder(file: &mut File, context: &ParseContext) -> Result<SessionRecord> {
    file.seek(SeekFrom::Start(0))?;
    let mut reader = BoundedJsonlReader::new(BufReader::new(&mut *file), context.max_event_bytes);
    let bytes = match reader.read_next()? {
        Some(line) if line.status == LineReadStatus::Complete => line.bytes.unwrap_or_default(),
        _ => Vec::new(),
    };
    file.seek(SeekFrom::Start(0))?;
    let mut first_record = bytes;
    if !first_record.is_empty() {
        first_record.push(b'\n');
    }
    let mut sink = CollectingSink::default();
    let summary = parse_rollout(BufReader::new(first_record.as_slice()), context, &mut sink)?;
    let mut placeholder = summary.session;
    placeholder.index_state = IndexState::Pending;
    placeholder.entry_count = 0;
    placeholder.preview.clear();
    if placeholder.completeness == Completeness::Unsupported {
        placeholder.source = SourceKind::Unknown;
    }
    Ok(placeholder)
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
    shutdown: CancellationToken,
) -> Result<(crate::rollout::ParseSummary, bool)> {
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
    let mut sink = BatchingSink::new(writer, source_file_id, scan_token);
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
    let mut reader = BufReader::new(&opened.file);
    let mut summary = match seed {
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
    let after = file_identity(
        &opened.file.metadata().context("re-stat parsed source")?,
        &opened.canonical_path,
    );
    let changed = after != opened.identity;
    let stable_checkpoint = crate::rollout::checkpoint_for_file(
        &discovered.root,
        &discovered.path,
        summary.stable_prefix_bytes,
    )?;
    summary.stable_prefix_hash = stable_checkpoint.prefix_hash;
    if changed {
        let checkpoint = crate::rollout::FileCheckpoint {
            offset: summary.stable_prefix_bytes,
            prefix_hash: summary.stable_prefix_hash.clone(),
        };
        if !verify_checkpoint(&discovered.root, &discovered.path, &checkpoint)? {
            bail!("source changed before stable prefix could be revalidated");
        }
    }
    Ok((summary, changed))
}

async fn append_plan(
    database: &Database,
    discovered: &DiscoveredSource,
) -> Result<(ScanMode, Option<ParseSeed>)> {
    let root_kind = match discovered.source.root_kind {
        RootKind::Active => "active",
        RootKind::Archived => "archived",
    };
    let Some(source) = sqlx::query(
        "SELECT id, file_key, size_bytes, head_hash, checkpoint_offset, checkpoint_line, \
            checkpoint_hash, session_id, scan_state \
         FROM source_files WHERE root_kind = ? AND relative_path = ?",
    )
    .bind(root_kind)
    .bind(&discovered.source.relative_path)
    .fetch_optional(database.pool())
    .await?
    else {
        return Ok((ScanMode::Full, None));
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
        return Ok((ScanMode::Full, None));
    }
    let checkpoint = crate::rollout::FileCheckpoint {
        offset: u64::try_from(checkpoint_offset)?,
        prefix_hash: source
            .get::<Option<String>, _>("checkpoint_hash")
            .unwrap_or_default(),
    };
    if checkpoint.prefix_hash.is_empty()
        || !verify_checkpoint(&discovered.root, &discovered.path, &checkpoint)?
    {
        return Ok((ScanMode::Full, None));
    }
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
    Ok((
        ScanMode::Append {
            checkpoint_offset: checkpoint.offset,
        },
        Some(ParseSeed {
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
            checkpoint_offset: checkpoint.offset,
            checkpoint_line,
        }),
    ))
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

fn source_precedes(left: &DiscoveredSource, right: &DiscoveredSource) -> bool {
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

fn head_tail_hash(file: &mut File, size: u64) -> Result<(String, String)> {
    let mut head = vec![0_u8; FINGERPRINT_BYTES.min(usize::try_from(size).unwrap_or(usize::MAX))];
    file.seek(SeekFrom::Start(0))?;
    file.read_exact(&mut head)?;
    let tail_len = FINGERPRINT_BYTES.min(usize::try_from(size).unwrap_or(usize::MAX));
    let mut tail = vec![0_u8; tail_len];
    file.seek(SeekFrom::Start(size.saturating_sub(tail_len as u64)))?;
    file.read_exact(&mut tail)?;
    file.seek(SeekFrom::Start(0))?;
    Ok((sha256_hex(&head), sha256_hex(&tail)))
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
