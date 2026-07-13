use anyhow::{Context as _, Result, anyhow};
use serde::Serialize;
use sha2::{Digest, Sha256};
use sqlx::{Sqlite, Transaction};
use tokio::sync::{mpsc, oneshot};

use crate::rollout::{ParseSummary, ParserDiagnostic, ParserOutput, RootKind};

use super::Database;

pub const WRITER_QUEUE_CAPACITY: usize = 32;
pub const MAX_BATCH_ENTRIES: usize = 500;
pub const MAX_BATCH_BYTES: usize = 4 * 1024 * 1024;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ScanMode {
    Full,
    Append { checkpoint_offset: u64 },
}

#[derive(Clone, Debug)]
pub struct SourceFileRecord {
    pub root_kind: RootKind,
    pub relative_path: String,
    pub file_key: String,
    pub size_bytes: u64,
    pub mtime_ns: i64,
    pub head_hash: Option<String>,
    pub tail_hash: Option<String>,
    pub generation: u64,
    pub placeholder: Option<crate::rollout::SessionRecord>,
}

#[derive(Clone)]
pub struct WriterHandle {
    sender: mpsc::Sender<WriteCommand>,
}

pub struct WriterTask {
    join: tokio::task::JoinHandle<Result<()>>,
}

enum WriteCommand {
    Begin {
        source: SourceFileRecord,
        scan_token: String,
        mode: ScanMode,
        reply: oneshot::Sender<Result<i64>>,
    },
    Batch {
        source_file_id: i64,
        scan_token: String,
        outputs: Vec<ParserOutput>,
        reply: oneshot::Sender<Result<()>>,
    },
    Finish {
        source_file_id: i64,
        scan_token: String,
        summary: ParseSummary,
        mode: ScanMode,
        reply: oneshot::Sender<Result<()>>,
    },
    Abort {
        scan_token: String,
        reply: oneshot::Sender<Result<()>>,
    },
    MarkSeen {
        root_kind: RootKind,
        relative_path: String,
        generation: u64,
        reply: oneshot::Sender<Result<()>>,
    },
    RemoveUnseen {
        generation: u64,
        reply: oneshot::Sender<Result<u64>>,
    },
    Shutdown,
}

pub fn spawn_writer(database: Database) -> (WriterHandle, WriterTask) {
    let (sender, mut receiver) = mpsc::channel(WRITER_QUEUE_CAPACITY);
    let join = tokio::spawn(async move {
        while let Some(command) = receiver.recv().await {
            match command {
                WriteCommand::Begin {
                    source,
                    scan_token,
                    mode,
                    reply,
                } => {
                    let _ = reply.send(begin_scan(&database, &source, &scan_token, mode).await);
                }
                WriteCommand::Batch {
                    source_file_id,
                    scan_token,
                    outputs,
                    reply,
                } => {
                    let _ = reply
                        .send(write_batch(&database, source_file_id, &scan_token, outputs).await);
                }
                WriteCommand::Finish {
                    source_file_id,
                    scan_token,
                    summary,
                    mode,
                    reply,
                } => {
                    let _ = reply.send(
                        finish_scan(&database, source_file_id, &scan_token, &summary, mode).await,
                    );
                }
                WriteCommand::Abort { scan_token, reply } => {
                    let _ = reply.send(abort_scan(&database, &scan_token).await);
                }
                WriteCommand::MarkSeen {
                    root_kind,
                    relative_path,
                    generation,
                    reply,
                } => {
                    let _ = reply
                        .send(mark_seen(&database, root_kind, &relative_path, generation).await);
                }
                WriteCommand::RemoveUnseen { generation, reply } => {
                    let _ = reply.send(remove_unseen(&database, generation).await);
                }
                WriteCommand::Shutdown => break,
            }
        }
        Ok(())
    });
    (WriterHandle { sender }, WriterTask { join })
}

impl WriterHandle {
    pub async fn begin(
        &self,
        source: SourceFileRecord,
        scan_token: String,
        mode: ScanMode,
    ) -> Result<i64> {
        let (reply, response) = oneshot::channel();
        self.sender
            .send(WriteCommand::Begin {
                source,
                scan_token,
                mode,
                reply,
            })
            .await
            .map_err(|_| anyhow!("database writer stopped"))?;
        response
            .await
            .map_err(|_| anyhow!("database writer stopped"))?
    }

    pub fn write_batch_blocking(
        &self,
        source_file_id: i64,
        scan_token: String,
        outputs: Vec<ParserOutput>,
    ) -> Result<()> {
        let (reply, response) = oneshot::channel();
        self.sender
            .blocking_send(WriteCommand::Batch {
                source_file_id,
                scan_token,
                outputs,
                reply,
            })
            .map_err(|_| anyhow!("database writer stopped"))?;
        response
            .blocking_recv()
            .map_err(|_| anyhow!("database writer stopped"))?
    }

    pub async fn finish(
        &self,
        source_file_id: i64,
        scan_token: String,
        summary: ParseSummary,
        mode: ScanMode,
    ) -> Result<()> {
        let (reply, response) = oneshot::channel();
        self.sender
            .send(WriteCommand::Finish {
                source_file_id,
                scan_token,
                summary,
                mode,
                reply,
            })
            .await
            .map_err(|_| anyhow!("database writer stopped"))?;
        response
            .await
            .map_err(|_| anyhow!("database writer stopped"))?
    }

    pub async fn abort(&self, scan_token: String) -> Result<()> {
        let (reply, response) = oneshot::channel();
        self.sender
            .send(WriteCommand::Abort { scan_token, reply })
            .await
            .map_err(|_| anyhow!("database writer stopped"))?;
        response
            .await
            .map_err(|_| anyhow!("database writer stopped"))?
    }

    pub async fn mark_seen(
        &self,
        root_kind: RootKind,
        relative_path: String,
        generation: u64,
    ) -> Result<()> {
        let (reply, response) = oneshot::channel();
        self.sender
            .send(WriteCommand::MarkSeen {
                root_kind,
                relative_path,
                generation,
                reply,
            })
            .await
            .map_err(|_| anyhow!("database writer stopped"))?;
        response
            .await
            .map_err(|_| anyhow!("database writer stopped"))?
    }

    pub async fn remove_unseen(&self, generation: u64) -> Result<u64> {
        let (reply, response) = oneshot::channel();
        self.sender
            .send(WriteCommand::RemoveUnseen { generation, reply })
            .await
            .map_err(|_| anyhow!("database writer stopped"))?;
        response
            .await
            .map_err(|_| anyhow!("database writer stopped"))?
    }

    pub async fn shutdown(&self) -> Result<()> {
        self.sender
            .send(WriteCommand::Shutdown)
            .await
            .map_err(|_| anyhow!("database writer stopped"))
    }
}

impl WriterTask {
    pub async fn wait(self) -> Result<()> {
        self.join.await.context("database writer task panicked")?
    }
}

pub struct BatchingSink {
    writer: WriterHandle,
    source_file_id: i64,
    scan_token: String,
    outputs: Vec<ParserOutput>,
    entry_count: usize,
    byte_count: usize,
    error: Option<anyhow::Error>,
}

impl BatchingSink {
    #[must_use]
    pub fn new(writer: WriterHandle, source_file_id: i64, scan_token: String) -> Self {
        Self {
            writer,
            source_file_id,
            scan_token,
            outputs: Vec::new(),
            entry_count: 0,
            byte_count: 0,
            error: None,
        }
    }

    pub fn finish(mut self) -> Result<()> {
        self.flush();
        self.error.map_or(Ok(()), Err)
    }

    fn flush(&mut self) {
        if self.outputs.is_empty() || self.error.is_some() {
            return;
        }
        let outputs = std::mem::take(&mut self.outputs);
        self.entry_count = 0;
        self.byte_count = 0;
        if let Err(error) =
            self.writer
                .write_batch_blocking(self.source_file_id, self.scan_token.clone(), outputs)
        {
            self.error = Some(error);
        }
    }
}

impl crate::rollout::ParseSink for BatchingSink {
    fn emit(&mut self, output: ParserOutput) {
        if self.error.is_some() {
            return;
        }
        if matches!(output, ParserOutput::EntryUpsert(_)) {
            self.entry_count = self.entry_count.saturating_add(1);
        }
        self.byte_count = self.byte_count.saturating_add(output_size(&output));
        self.outputs.push(output);
        if self.entry_count >= MAX_BATCH_ENTRIES || self.byte_count >= MAX_BATCH_BYTES {
            self.flush();
        }
    }
}

async fn begin_scan(
    database: &Database,
    source: &SourceFileRecord,
    scan_token: &str,
    mode: ScanMode,
) -> Result<i64> {
    let root_kind = match source.root_kind {
        RootKind::Active => "active",
        RootKind::Archived => "archived",
    };
    let generation = i64::try_from(source.generation).context("generation exceeds SQLite range")?;
    let size = i64::try_from(source.size_bytes).context("source size exceeds SQLite range")?;
    let result = sqlx::query(
        "INSERT INTO source_files( \
            root_kind, relative_path, file_key, size_bytes, mtime_ns, head_hash, tail_hash, \
            scan_state, scan_token, seen_generation \
         ) VALUES (?, ?, ?, ?, ?, ?, ?, 'indexing', ?, ?) \
         ON CONFLICT(root_kind, relative_path) DO UPDATE SET \
            file_key = excluded.file_key, size_bytes = excluded.size_bytes, \
            mtime_ns = excluded.mtime_ns, head_hash = excluded.head_hash, \
            tail_hash = excluded.tail_hash, scan_state = 'indexing', \
            scan_token = excluded.scan_token, last_error = NULL, \
            seen_generation = excluded.seen_generation \
         RETURNING id",
    )
    .bind(root_kind)
    .bind(&source.relative_path)
    .bind(&source.file_key)
    .bind(size)
    .bind(source.mtime_ns)
    .bind(&source.head_hash)
    .bind(&source.tail_hash)
    .bind(scan_token)
    .bind(generation)
    .fetch_one(database.pool())
    .await?;
    let source_file_id: i64 = sqlx::Row::get(&result, "id");
    abort_scan(database, scan_token).await?;
    if mode == ScanMode::Full
        && let Some(placeholder) = &source.placeholder
    {
        insert_placeholder(database, source_file_id, placeholder).await?;
    }
    sqlx::query("UPDATE source_files SET scan_state = 'indexing', scan_token = ? WHERE id = ?")
        .bind(scan_token)
        .bind(source_file_id)
        .execute(database.pool())
        .await?;
    Ok(source_file_id)
}

async fn insert_placeholder(
    database: &Database,
    source_file_id: i64,
    session: &crate::rollout::SessionRecord,
) -> Result<()> {
    sqlx::query(
        "INSERT INTO sessions( \
            id, source_file_id, source_kind, parent_thread_id, cwd, title, preview, \
            created_at_micros, updated_at_micros, archived, cli_version, provider, history_line, \
            git_branch, git_commit, entry_count, index_state, completeness, diagnostic_count \
         ) SELECT ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 0, 'pending', ?, ? \
         WHERE NOT EXISTS (SELECT 1 FROM sessions WHERE source_file_id = ?) \
           AND NOT EXISTS (SELECT 1 FROM sessions WHERE id = ?)",
    )
    .bind(&session.id)
    .bind(source_file_id)
    .bind(enum_value(&session.source)?)
    .bind(&session.parent_thread_id)
    .bind(&session.cwd)
    .bind(&session.title)
    .bind(&session.preview)
    .bind(session.created_at_micros)
    .bind(session.updated_at_micros)
    .bind(session.archived)
    .bind(&session.cli_version)
    .bind(&session.provider)
    .bind(
        session
            .history_line
            .and_then(|value| i64::try_from(value).ok()),
    )
    .bind(&session.git_branch)
    .bind(&session.git_commit)
    .bind(enum_value(&session.completeness)?)
    .bind(i64::try_from(session.diagnostic_count)?)
    .bind(source_file_id)
    .bind(&session.id)
    .execute(database.pool())
    .await?;
    Ok(())
}

async fn write_batch(
    database: &Database,
    source_file_id: i64,
    scan_token: &str,
    outputs: Vec<ParserOutput>,
) -> Result<()> {
    let mut transaction = database.pool().begin().await?;
    for output in outputs {
        match output {
            ParserOutput::Raw(raw) => {
                sqlx::query(
                    "INSERT INTO staged_raw_records( \
                        scan_token, id, source_file_id, session_id, line_no, byte_offset, \
                        byte_length, envelope_type, parse_status, content_hash, utf8, oversize, \
                        hex_preview \
                     ) VALUES (?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?, ?, ?) \
                     ON CONFLICT(scan_token, id) DO UPDATE SET \
                        parse_status = excluded.parse_status, content_hash = excluded.content_hash, \
                        utf8 = excluded.utf8, oversize = excluded.oversize, \
                        hex_preview = excluded.hex_preview",
                )
                .bind(scan_token)
                .bind(raw.id)
                .bind(source_file_id)
                .bind(i64::try_from(raw.line_no)?)
                .bind(i64::try_from(raw.byte_offset)?)
                .bind(i64::try_from(raw.byte_length)?)
                .bind(raw.envelope_type)
                .bind(raw.parse_status)
                .bind(raw.content_hash)
                .bind(raw.utf8)
                .bind(raw.oversize)
                .bind(raw.hex_preview)
                .execute(&mut *transaction)
                .await?;
            }
            ParserOutput::EntryUpsert(entry) => {
                let metadata = serde_json::to_string(&entry.metadata)?;
                sqlx::query(
                    "INSERT INTO staged_entries( \
                        scan_token, id, session_id, sequence, timestamp_micros, kind, presentation, role, phase, \
                        tool_kind, tool_status, title, primary_text, secondary_text, metadata_json, id_basis, \
                        call_id, parent_entry_id, default_collapsed, searchable, primary_bytes, \
                        secondary_bytes \
                     ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) \
                     ON CONFLICT(scan_token, id) DO UPDATE SET \
                        timestamp_micros = excluded.timestamp_micros, kind = excluded.kind, presentation = excluded.presentation, \
                        role = excluded.role, phase = excluded.phase, tool_kind = excluded.tool_kind, \
                        tool_status = excluded.tool_status, title = excluded.title, \
                        primary_text = excluded.primary_text, secondary_text = excluded.secondary_text, \
                        metadata_json = excluded.metadata_json, id_basis = excluded.id_basis, call_id = excluded.call_id, \
                        parent_entry_id = excluded.parent_entry_id, \
                        default_collapsed = excluded.default_collapsed, searchable = excluded.searchable, \
                        primary_bytes = excluded.primary_bytes, secondary_bytes = excluded.secondary_bytes",
                )
                .bind(scan_token)
                .bind(&entry.id)
                .bind(&entry.session_id)
                .bind(entry.sequence)
                .bind(entry.timestamp_micros)
                .bind(enum_value(&entry.kind)?)
                .bind(enum_value(&entry.presentation)?)
                .bind(optional_enum(entry.role.as_ref())?)
                .bind(optional_enum(entry.phase.as_ref())?)
                .bind(optional_enum(entry.tool_kind.as_ref())?)
                .bind(optional_enum(entry.tool_status.as_ref())?)
                .bind(entry.title)
                .bind(&entry.primary_text)
                .bind(&entry.secondary_text)
                .bind(metadata)
                .bind(&entry.id_basis)
                .bind(entry.call_id)
                .bind(entry.parent_entry_id)
                .bind(entry.default_collapsed)
                .bind(entry.searchable)
                .bind(i64::try_from(entry.primary_text.len())?)
                .bind(i64::try_from(entry.secondary_text.len())?)
                .execute(&mut *transaction)
                .await?;
                for (ordinal, raw_id) in entry.raw_refs.into_iter().enumerate() {
                    sqlx::query(
                        "INSERT INTO staged_entry_raw_refs( \
                            scan_token, entry_id, raw_id, ordinal \
                         ) VALUES (?, ?, ?, ?) \
                         ON CONFLICT(scan_token, entry_id, raw_id) DO UPDATE SET \
                            ordinal = excluded.ordinal",
                    )
                    .bind(scan_token)
                    .bind(&entry.id)
                    .bind(raw_id)
                    .bind(i64::try_from(ordinal)?)
                    .execute(&mut *transaction)
                    .await?;
                }
            }
            ParserOutput::Diagnostic(diagnostic) => {
                stage_diagnostic(&mut transaction, source_file_id, scan_token, &diagnostic).await?;
            }
        }
    }
    transaction.commit().await?;
    Ok(())
}

async fn finish_scan(
    database: &Database,
    source_file_id: i64,
    scan_token: &str,
    summary: &ParseSummary,
    mode: ScanMode,
) -> Result<()> {
    let session = &summary.session;
    let mut transaction = database.pool().begin().await?;
    sqlx::query(
        "INSERT INTO staged_sessions( \
            scan_token, id, source_file_id, source_kind, parent_thread_id, cwd, title, preview, \
            created_at_micros, updated_at_micros, archived, cli_version, provider, history_line, \
            git_branch, git_commit, entry_count, index_state, completeness, diagnostic_count \
         ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
    )
    .bind(scan_token)
    .bind(&session.id)
    .bind(source_file_id)
    .bind(enum_value(&session.source)?)
    .bind(&session.parent_thread_id)
    .bind(&session.cwd)
    .bind(&session.title)
    .bind(&session.preview)
    .bind(session.created_at_micros)
    .bind(session.updated_at_micros)
    .bind(session.archived)
    .bind(&session.cli_version)
    .bind(&session.provider)
    .bind(
        session
            .history_line
            .and_then(|value| i64::try_from(value).ok()),
    )
    .bind(&session.git_branch)
    .bind(&session.git_commit)
    .bind(i64::try_from(session.entry_count)?)
    .bind(enum_value(&session.index_state)?)
    .bind(enum_value(&session.completeness)?)
    .bind(i64::try_from(session.diagnostic_count)?)
    .execute(&mut *transaction)
    .await?;

    if mode == ScanMode::Full {
        sqlx::query("DELETE FROM sessions WHERE source_file_id = ? OR id = ?")
            .bind(source_file_id)
            .bind(&session.id)
            .execute(&mut *transaction)
            .await?;
    }
    sqlx::query(
        "INSERT INTO sessions( \
            id, source_file_id, source_kind, parent_thread_id, cwd, title, preview, \
            created_at_micros, updated_at_micros, archived, cli_version, provider, history_line, \
            git_branch, git_commit, entry_count, index_state, completeness, diagnostic_count \
         ) SELECT id, source_file_id, source_kind, parent_thread_id, cwd, title, preview, \
            created_at_micros, updated_at_micros, archived, cli_version, provider, history_line, \
            git_branch, git_commit, entry_count, index_state, completeness, diagnostic_count \
         FROM staged_sessions WHERE scan_token = ? \
         ON CONFLICT(id) DO UPDATE SET \
            source_kind = excluded.source_kind, parent_thread_id = excluded.parent_thread_id, \
            cwd = excluded.cwd, title = excluded.title, preview = excluded.preview, \
            created_at_micros = excluded.created_at_micros, updated_at_micros = excluded.updated_at_micros, \
            archived = excluded.archived, cli_version = excluded.cli_version, provider = excluded.provider, \
            history_line = excluded.history_line, git_branch = excluded.git_branch, \
            git_commit = excluded.git_commit, entry_count = excluded.entry_count, \
            index_state = excluded.index_state, completeness = excluded.completeness, \
            diagnostic_count = excluded.diagnostic_count",
    )
    .bind(scan_token)
    .execute(&mut *transaction)
    .await?;
    if let ScanMode::Append { checkpoint_offset } = mode {
        sqlx::query("DELETE FROM raw_records WHERE source_file_id = ? AND byte_offset >= ?")
            .bind(source_file_id)
            .bind(i64::try_from(checkpoint_offset)?)
            .execute(&mut *transaction)
            .await?;
    }
    sqlx::query(
        "INSERT INTO raw_records( \
            id, source_file_id, session_id, line_no, byte_offset, byte_length, envelope_type, \
            parse_status, content_hash, utf8, oversize, hex_preview \
         ) SELECT id, source_file_id, ?, line_no, byte_offset, byte_length, envelope_type, \
            parse_status, content_hash, utf8, oversize, hex_preview \
         FROM staged_raw_records WHERE scan_token = ? \
         ON CONFLICT(id) DO UPDATE SET \
            parse_status = excluded.parse_status, content_hash = excluded.content_hash, \
            utf8 = excluded.utf8, oversize = excluded.oversize, hex_preview = excluded.hex_preview",
    )
    .bind(&session.id)
    .bind(scan_token)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO entries( \
            id, session_id, sequence, timestamp_micros, kind, presentation, role, phase, tool_kind, tool_status, \
            title, primary_text, secondary_text, metadata_json, id_basis, call_id, parent_entry_id, \
            default_collapsed, searchable, primary_bytes, secondary_bytes \
         ) SELECT id, session_id, sequence, timestamp_micros, kind, presentation, role, phase, tool_kind, \
            tool_status, title, primary_text, secondary_text, metadata_json, id_basis, call_id, \
            parent_entry_id, default_collapsed, searchable, primary_bytes, secondary_bytes \
         FROM staged_entries WHERE scan_token = ? ORDER BY sequence \
         ON CONFLICT(id) DO UPDATE SET \
            timestamp_micros = excluded.timestamp_micros, kind = excluded.kind, presentation = excluded.presentation, role = excluded.role, \
            phase = excluded.phase, tool_kind = excluded.tool_kind, tool_status = excluded.tool_status, \
            title = excluded.title, primary_text = excluded.primary_text, \
            secondary_text = excluded.secondary_text, metadata_json = excluded.metadata_json, \
            id_basis = excluded.id_basis, call_id = excluded.call_id, \
            parent_entry_id = excluded.parent_entry_id, default_collapsed = excluded.default_collapsed, \
            searchable = excluded.searchable, primary_bytes = excluded.primary_bytes, \
            secondary_bytes = excluded.secondary_bytes",
    )
    .bind(scan_token)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO entry_raw_refs(entry_id, raw_id, ordinal) \
         SELECT entry_id, raw_id, ordinal FROM staged_entry_raw_refs WHERE scan_token = ? \
         ON CONFLICT(entry_id, raw_id) DO UPDATE SET ordinal = excluded.ordinal",
    )
    .bind(scan_token)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "INSERT INTO diagnostics( \
            session_id, source_file_id, severity, code, message, dedup_key, \
            first_seen_at_micros, last_seen_at_micros, count \
         ) SELECT ?, source_file_id, severity, code, message, dedup_key, \
            first_seen_at_micros, last_seen_at_micros, count \
         FROM staged_diagnostics WHERE scan_token = ? \
         ON CONFLICT(source_file_id, dedup_key) DO UPDATE SET \
            last_seen_at_micros = excluded.last_seen_at_micros, count = diagnostics.count + excluded.count",
    )
    .bind(&session.id)
    .bind(scan_token)
    .execute(&mut *transaction)
    .await?;
    sqlx::query(
        "UPDATE source_files SET session_id = ?, checkpoint_offset = ?, checkpoint_line = ?, \
            checkpoint_hash = ?, scan_state = 'ready', scan_token = NULL, last_error = NULL \
         WHERE id = ?",
    )
    .bind(&session.id)
    .bind(i64::try_from(summary.stable_prefix_bytes)?)
    .bind(i64::try_from(
        summary
            .raw_record_count
            .saturating_sub(u64::from(summary.incomplete_tail)),
    )?)
    .bind(&summary.stable_prefix_hash)
    .bind(source_file_id)
    .execute(&mut *transaction)
    .await?;
    cleanup_token(&mut transaction, scan_token).await?;
    transaction.commit().await?;
    Ok(())
}

async fn abort_scan(database: &Database, scan_token: &str) -> Result<()> {
    let mut transaction = database.pool().begin().await?;
    cleanup_token(&mut transaction, scan_token).await?;
    transaction.commit().await?;
    Ok(())
}

async fn mark_seen(
    database: &Database,
    root_kind: RootKind,
    relative_path: &str,
    generation: u64,
) -> Result<()> {
    let root_kind = match root_kind {
        RootKind::Active => "active",
        RootKind::Archived => "archived",
    };
    sqlx::query(
        "UPDATE source_files SET seen_generation = ? WHERE root_kind = ? AND relative_path = ?",
    )
    .bind(i64::try_from(generation)?)
    .bind(root_kind)
    .bind(relative_path)
    .execute(database.pool())
    .await?;
    Ok(())
}

async fn remove_unseen(database: &Database, generation: u64) -> Result<u64> {
    let result = sqlx::query("DELETE FROM source_files WHERE seen_generation != ?")
        .bind(i64::try_from(generation)?)
        .execute(database.pool())
        .await?;
    if result.rows_affected() > 0 {
        sqlx::query("PRAGMA incremental_vacuum")
            .execute(database.pool())
            .await?;
    }
    Ok(result.rows_affected())
}

async fn cleanup_token(transaction: &mut Transaction<'_, Sqlite>, scan_token: &str) -> Result<()> {
    for statement in [
        "DELETE FROM staged_entry_raw_refs WHERE scan_token = ?",
        "DELETE FROM staged_diagnostics WHERE scan_token = ?",
        "DELETE FROM staged_entries WHERE scan_token = ?",
        "DELETE FROM staged_raw_records WHERE scan_token = ?",
        "DELETE FROM staged_sessions WHERE scan_token = ?",
    ] {
        sqlx::query(statement)
            .bind(scan_token)
            .execute(&mut **transaction)
            .await?;
    }
    Ok(())
}

async fn stage_diagnostic(
    transaction: &mut Transaction<'_, Sqlite>,
    source_file_id: i64,
    scan_token: &str,
    diagnostic: &ParserDiagnostic,
) -> Result<()> {
    let dedup_key = sha256_hex(
        format!(
            "{}\0{}\0{}",
            diagnostic.code,
            diagnostic.line_no.unwrap_or_default(),
            diagnostic.raw_ref_id.as_deref().unwrap_or_default()
        )
        .as_bytes(),
    );
    let now = chrono::Utc::now().timestamp_micros();
    sqlx::query(
        "INSERT INTO staged_diagnostics( \
            scan_token, session_id, source_file_id, severity, code, message, dedup_key, \
            first_seen_at_micros, last_seen_at_micros, count \
         ) VALUES (?, NULL, ?, ?, ?, ?, ?, ?, ?, 1) \
         ON CONFLICT(scan_token, dedup_key) DO UPDATE SET \
            last_seen_at_micros = excluded.last_seen_at_micros, count = count + 1",
    )
    .bind(scan_token)
    .bind(source_file_id)
    .bind(enum_value(&diagnostic.severity)?)
    .bind(&diagnostic.code)
    .bind(&diagnostic.message)
    .bind(dedup_key)
    .bind(now)
    .bind(now)
    .execute(&mut **transaction)
    .await?;
    Ok(())
}

fn enum_value<T: Serialize>(value: &T) -> Result<String> {
    serde_json::to_value(value)?
        .as_str()
        .map(str::to_owned)
        .ok_or_else(|| anyhow!("enum did not serialize to a string"))
}

fn optional_enum<T: Serialize>(value: Option<&T>) -> Result<Option<String>> {
    value.map(enum_value).transpose()
}

fn output_size(output: &ParserOutput) -> usize {
    match output {
        ParserOutput::Raw(raw) => raw.hex_preview.as_ref().map_or(256, String::len),
        ParserOutput::EntryUpsert(entry) => entry
            .primary_text
            .len()
            .saturating_add(entry.secondary_text.len())
            .saturating_add(entry.title.len())
            .saturating_add(512),
        ParserOutput::Diagnostic(diagnostic) => diagnostic.message.len().saturating_add(256),
    }
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
