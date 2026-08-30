use std::collections::{BTreeMap, HashMap};
use std::fs::File;
use std::io::{self, BufReader, Read as _, Seek as _, SeekFrom};
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};

use anyhow::{Context as _, Result, anyhow, bail};
use sha2::{Digest, Sha256};
use sqlx::Row as _;
use tokio_util::sync::CancellationToken;

use crate::model::{DiagnosticSeverity, EntryPresentation};
use crate::paths::SourceRoots;
use crate::permissions::{open_source_read_only, opened_file_identity};
use crate::rollout::{
    CollectingSink, EntryOrigin, NormalizedEntry, ParseContext, ParseSeed, ParseSink as _,
    ParserDiagnostic, ParserOutput, RootKind, SessionRecord,
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

#[derive(Clone, Debug)]
pub struct CatalogOutcome {
    pub source: DiscoveredSource,
    pub has_snapshot: bool,
    pub changed: bool,
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

pub async fn refresh_catalog_source(
    database: Database,
    mut discovered: DiscoveredSource,
    max_event_bytes: usize,
    now_micros: i64,
    shutdown: CancellationToken,
    lease: ScanLease,
) -> Result<CatalogOutcome> {
    if shutdown.is_cancelled() {
        bail!("catalog scan cancelled");
    }
    let root_kind = root_kind_value(discovered.source.root_kind);
    let stored = sqlx::query(
        "SELECT sf.id, sf.file_key, sf.size_bytes, sf.mtime_ns, sf.catalog_checkpoint_offset, \
            sf.catalog_complete, sf.catalog_head_hash, sf.session_id, sf.snapshot_revision \
         FROM source_files sf WHERE sf.root_kind = ? AND sf.relative_path = ?",
    )
    .bind(root_kind)
    .bind(&discovered.source.relative_path)
    .fetch_optional(database.pool())
    .await?;
    if let Some(stored) = stored.as_ref() {
        let catalog_complete = stored.get::<i64, _>("catalog_complete") != 0;
        let old_size = u64::try_from(stored.get::<i64, _>("size_bytes"))?;
        let checkpoint = u64::try_from(stored.get::<i64, _>("catalog_checkpoint_offset"))?;
        let append_candidate = catalog_complete
            && stored.get::<String, _>("file_key") == discovered.source.file_key
            && discovered.source.size_bytes >= old_size
            && discovered.source.size_bytes != old_size;
        let unchanged = stored.get::<String, _>("file_key") == discovered.source.file_key
            && discovered.source.size_bytes == old_size
            && stored.get::<i64, _>("mtime_ns") == discovered.source.mtime_ns;
        let guarded_append = if append_candidate {
            let guard_len =
                FINGERPRINT_BYTES.min(usize::try_from(checkpoint).unwrap_or(usize::MAX));
            match stored.get::<Option<String>, _>("catalog_head_hash") {
                Some(expected) => {
                    let mut opened = open_source_read_only(&discovered.root, &discovered.path)?;
                    let bytes =
                        read_controlled_range(&mut opened.file, 0, guard_len, &lease, &shutdown)?;
                    sha256_hex(&bytes) == expected
                }
                None => false,
            }
        } else {
            false
        };
        if unchanged {
            let session_id = stored
                .get::<Option<String>, _>("session_id")
                .unwrap_or_else(|| discovered.session_id.clone());
            discovered.session_id.clone_from(&session_id);
            let revision = u64::try_from(stored.get::<i64, _>("snapshot_revision"))?;
            return Ok(CatalogOutcome {
                source: discovered,
                has_snapshot: revision > 0,
                changed: false,
            });
        }
        if guarded_append {
            let session_id = stored
                .get::<Option<String>, _>("session_id")
                .unwrap_or_else(|| discovered.session_id.clone());
            discovered.session_id.clone_from(&session_id);
            let revision = u64::try_from(stored.get::<i64, _>("snapshot_revision"))?;
            update_catalog_observation(&database, &discovered, &session_id).await?;
            return Ok(CatalogOutcome {
                source: discovered,
                has_snapshot: revision > 0,
                changed: true,
            });
        }
    }

    let parse_source = discovered.clone();
    let parse_shutdown = shutdown.clone();
    let parse_lease = lease.clone();
    let parsed = tokio::task::spawn_blocking(move || {
        parse_catalog_blocking(
            parse_source,
            max_event_bytes,
            now_micros,
            parse_shutdown,
            parse_lease,
        )
    })
    .await
    .context("catalog parser task panicked")??;
    let first = parsed
        .entries
        .iter()
        .find(|entry| {
            entry.presentation == EntryPresentation::User && !entry.primary_text.trim().is_empty()
        })
        .cloned();
    discovered.session_id.clone_from(&parsed.summary.session.id);
    let guard_len = FINGERPRINT_BYTES
        .min(usize::try_from(parsed.summary.stable_prefix_bytes).unwrap_or(usize::MAX));
    let guard_hash = {
        let mut opened = open_source_read_only(&discovered.root, &discovered.path)?;
        let bytes = read_controlled_range(&mut opened.file, 0, guard_len, &lease, &shutdown)?;
        sha256_hex(&bytes)
    };
    let revision = upsert_catalog(
        &database,
        &discovered,
        &parsed.summary,
        first.as_ref(),
        &guard_hash,
    )
    .await?;
    Ok(CatalogOutcome {
        source: discovered,
        has_snapshot: revision > 0,
        changed: true,
    })
}

fn parse_catalog_blocking(
    discovered: DiscoveredSource,
    max_event_bytes: usize,
    now_micros: i64,
    shutdown: CancellationToken,
    lease: ScanLease,
) -> Result<crate::rollout::ParsedRollout> {
    let opened = open_source_read_only(&discovered.root, &discovered.path)?;
    if opened.identity.file_key != discovered.source.file_key {
        bail!("source changed before catalog parsing began");
    }
    let context = ParseContext {
        root_kind: discovered.source.root_kind,
        relative_path: discovered.source.relative_path,
        file_name: discovered
            .path
            .file_name()
            .and_then(|value| value.to_str())
            .ok_or_else(|| anyhow!("source filename is not valid Unicode"))?
            .to_owned(),
        modified_at_micros: system_time_micros(opened.identity.modified.unwrap_or(UNIX_EPOCH)),
        now_micros,
        max_event_bytes,
    };
    let controlled = ControlledReader::new(opened.file, lease, shutdown.clone());
    let mut sink = CollectingSink::default();
    let summary = crate::rollout::normalize::parse_catalog_rollout_cancellable(
        BufReader::new(controlled),
        &context,
        &mut sink,
        &shutdown,
    )?;
    Ok(sink.finish(summary))
}

async fn update_catalog_observation(
    database: &Database,
    discovered: &DiscoveredSource,
    session_id: &str,
) -> Result<()> {
    let size = i64::try_from(discovered.source.size_bytes)?;
    let mut transaction = database.pool().begin().await?;
    sqlx::query(
        "UPDATE source_files SET file_key = ?, size_bytes = ?, mtime_ns = ?, \
            seen_generation = ?, catalog_error = NULL, \
            scan_state = CASE WHEN scan_state = 'source_missing' \
                THEN CASE WHEN snapshot_revision > 0 THEN 'ready' ELSE 'catalog' END \
                ELSE scan_state END \
         WHERE root_kind = ? AND relative_path = ?",
    )
    .bind(&discovered.source.file_key)
    .bind(size)
    .bind(discovered.source.mtime_ns)
    .bind(i64::try_from(discovered.source.generation)?)
    .bind(root_kind_value(discovered.source.root_kind))
    .bind(&discovered.source.relative_path)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "UPDATE sessions SET updated_at_micros = MAX(updated_at_micros, ?), archived = ? \
         WHERE id = ?",
    )
    .bind(discovered.source.mtime_ns / 1_000)
    .bind(discovered.source.root_kind == RootKind::Archived)
    .bind(session_id)
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;
    Ok(())
}

async fn upsert_catalog(
    database: &Database,
    discovered: &DiscoveredSource,
    summary: &crate::rollout::ParseSummary,
    first: Option<&NormalizedEntry>,
    catalog_head_hash: &str,
) -> Result<u64> {
    let session = &summary.session;
    let mut transaction = database.pool().begin().await?;
    let row = sqlx::query(
        "INSERT INTO source_files( \
            root_kind, relative_path, file_key, size_bytes, mtime_ns, catalog_checkpoint_offset, \
            catalog_checkpoint_line, catalog_head_hash, catalog_complete, session_id, scan_state, \
            seen_generation \
         ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'catalog', ?) \
         ON CONFLICT(root_kind, relative_path) DO UPDATE SET \
            file_key = excluded.file_key, size_bytes = excluded.size_bytes, \
            mtime_ns = excluded.mtime_ns, catalog_checkpoint_offset = excluded.catalog_checkpoint_offset, \
            catalog_checkpoint_line = excluded.catalog_checkpoint_line, \
            catalog_head_hash = excluded.catalog_head_hash, catalog_complete = excluded.catalog_complete, \
            catalog_error = NULL, \
            checkpoint_offset = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN 0 ELSE source_files.checkpoint_offset END, \
            checkpoint_line = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN 0 ELSE source_files.checkpoint_line END, \
            checkpoint_hash = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN NULL ELSE source_files.checkpoint_hash END, \
            snapshot_file_key = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN NULL ELSE source_files.snapshot_file_key END, \
            snapshot_size_bytes = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN NULL ELSE source_files.snapshot_size_bytes END, \
            snapshot_mtime_ns = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN NULL ELSE source_files.snapshot_mtime_ns END, \
            snapshot_head_hash = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN NULL ELSE source_files.snapshot_head_hash END, \
            snapshot_tail_hash = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN NULL ELSE source_files.snapshot_tail_hash END, \
            snapshot_revision = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN 0 ELSE source_files.snapshot_revision END, \
            last_synced_at_micros = CASE WHEN source_files.session_id IS NOT excluded.session_id \
                THEN NULL ELSE source_files.last_synced_at_micros END, \
            scan_state = CASE WHEN source_files.scan_state = 'indexing' THEN source_files.scan_state \
                WHEN source_files.session_id IS NOT excluded.session_id THEN 'catalog' \
                WHEN source_files.snapshot_revision > 0 THEN 'ready' ELSE 'catalog' END, \
            session_id = excluded.session_id, \
            seen_generation = excluded.seen_generation \
         RETURNING id, snapshot_revision",
    )
    .bind(root_kind_value(discovered.source.root_kind))
    .bind(&discovered.source.relative_path)
    .bind(&discovered.source.file_key)
    .bind(i64::try_from(discovered.source.size_bytes)?)
    .bind(discovered.source.mtime_ns)
    .bind(i64::try_from(summary.stable_prefix_bytes)?)
    .bind(i64::try_from(summary.raw_record_count)?)
    .bind(catalog_head_hash)
    .bind(first.is_some())
    .bind(&session.id)
    .bind(i64::try_from(discovered.source.generation)?)
    .fetch_one(&mut *transaction)
    .await?;
    let source_file_id = row.get::<i64, _>("id");
    let revision = u64::try_from(row.get::<i64, _>("snapshot_revision"))?;
    sqlx::query("DELETE FROM sessions WHERE source_file_id = ? AND id <> ?")
        .bind(source_file_id)
        .bind(&session.id)
        .execute(&mut *transaction)
        .await?;
    let source_kind = enum_string(&session.source)?;
    let parent_relation = session
        .parent_relation
        .as_ref()
        .map(enum_string)
        .transpose()?;
    let updated_at = session
        .updated_at_micros
        .max(discovered.source.mtime_ns / 1_000);
    sqlx::query(
        "INSERT INTO sessions( \
            id, source_file_id, source_kind, parent_thread_id, parent_relation, cwd, title, preview, \
            first_user_message, first_user_message_at_micros, created_at_micros, updated_at_micros, \
            archived, cli_version, provider, history_line, git_branch, git_commit, entry_count, \
            index_state, completeness, diagnostic_count \
         ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 'pending', 'partial', 0) \
         ON CONFLICT(id) DO UPDATE SET \
            source_file_id = excluded.source_file_id, source_kind = excluded.source_kind, \
            parent_thread_id = excluded.parent_thread_id, parent_relation = excluded.parent_relation, \
            cwd = excluded.cwd, title = excluded.title, preview = excluded.preview, \
            first_user_message = COALESCE(excluded.first_user_message, sessions.first_user_message), \
            first_user_message_at_micros = COALESCE(excluded.first_user_message_at_micros, sessions.first_user_message_at_micros), \
            created_at_micros = excluded.created_at_micros, updated_at_micros = excluded.updated_at_micros, \
            archived = excluded.archived, cli_version = excluded.cli_version, provider = excluded.provider, \
            history_line = excluded.history_line, git_branch = excluded.git_branch, git_commit = excluded.git_commit",
    )
    .bind(&session.id)
    .bind(source_file_id)
    .bind(source_kind)
    .bind(&session.parent_thread_id)
    .bind(parent_relation)
    .bind(&session.cwd)
    .bind(&session.title)
    .bind(&session.preview)
    .bind(first.map(|entry| entry.primary_text.as_str()))
    .bind(first.and_then(|entry| entry.timestamp_micros))
    .bind(session.created_at_micros)
    .bind(updated_at)
    .bind(discovered.source.root_kind == RootKind::Archived)
    .bind(&session.cli_version)
    .bind(&session.provider)
    .bind(session.history_line.and_then(|value| i64::try_from(value).ok()))
    .bind(&session.git_branch)
    .bind(&session.git_commit)
    .execute(&mut *transaction)
    .await?;
    transaction.commit().await?;
    Ok(revision)
}

fn enum_string<T: serde::Serialize>(value: &T) -> Result<String> {
    serde_json::to_value(value)?
        .as_str()
        .map(str::to_owned)
        .ok_or_else(|| anyhow!("enum did not serialize to a string"))
}

const fn root_kind_value(root_kind: RootKind) -> &'static str {
    match root_kind {
        RootKind::Active => "active",
        RootKind::Archived => "archived",
    }
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
            // Staged rows stay hidden. The coordinator releases the source token after this
            // worker stops, then the bounded background collector reclaims those rows.
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
        "SELECT id, snapshot_file_key, snapshot_size_bytes, snapshot_head_hash, \
            snapshot_tail_hash, checkpoint_offset, checkpoint_line, session_id, snapshot_revision \
         FROM source_files WHERE root_kind = ? AND relative_path = ?",
    )
    .bind(root_kind)
    .bind(&discovered.source.relative_path)
    .fetch_optional(database.pool())
    .await?
    else {
        return Ok(full_scan_plan());
    };
    let old_size = source.get::<Option<i64>, _>("snapshot_size_bytes");
    let checkpoint_offset = source.get::<i64, _>("checkpoint_offset");
    let head_matches = old_size.is_some_and(|size| size < FINGERPRINT_BYTES as i64)
        || source.get::<Option<String>, _>("snapshot_head_hash") == discovered.source.head_hash;
    let append_candidate = source
        .get::<Option<String>, _>("snapshot_file_key")
        .as_deref()
        == Some(discovered.source.file_key.as_str())
        && source.get::<i64, _>("snapshot_revision") > 0
        && old_size.is_some_and(|size| size >= 0)
        && discovered.source.size_bytes
            > old_size
                .and_then(|size| u64::try_from(size).ok())
                .unwrap_or(u64::MAX)
        && head_matches
        && checkpoint_offset >= 0;
    if !append_candidate {
        return Ok(full_scan_plan());
    }
    let checkpoint_offset = u64::try_from(checkpoint_offset)?;
    let Some(stored_tail) = source.get::<Option<String>, _>("snapshot_tail_hash") else {
        return Ok(full_scan_plan());
    };
    let Some(old_tail) = verify_old_tail(
        discovered,
        u64::try_from(old_size.expect("append candidate has snapshot size"))?,
        &stored_tail,
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
    let recent = load_recent_entries(database, &session_id).await?;
    let recognized_record_count = sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*) FROM raw_records WHERE source_file_id = ? AND parse_status = 'valid'",
    )
    .bind(source_file_id)
    .fetch_one(database.pool())
    .await?;
    let checkpoint_line = u64::try_from(source.get::<i64, _>("checkpoint_line"))?;
    let old_size = u64::try_from(old_size.expect("append candidate has snapshot size"))?;
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
            recent,
            raw_record_count: checkpoint_line,
            recognized_record_count: u64::try_from(recognized_record_count)?,
            checkpoint_offset,
            checkpoint_line,
            stable_hasher: Sha256::new(),
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
