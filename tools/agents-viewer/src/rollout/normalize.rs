use std::collections::{BTreeMap, HashMap, HashSet};
use std::io::{self, BufRead};

use chrono::{DateTime, NaiveDateTime, Utc};
use serde_json::Value;
use sha2::{Digest, Sha256};
use tokio_util::sync::CancellationToken;
use unicode_segmentation::UnicodeSegmentation;
use uuid::Uuid;

use crate::model::{
    Completeness, DiagnosticSeverity, EntryKind, EntryPresentation, IndexState, MessageRole, Phase,
    SessionParentRelation, SourceKind, ToolKind, ToolStatus,
};

use super::dedup::Deduper;
use super::envelope::Envelope;
use super::reader::{BoundedJsonlReader, LineReadStatus};
use super::types::{
    EntryOrigin, NormalizedEntry, ParseContext, ParseSeed, ParseSummary, ParsedRollout,
    ParserDiagnostic, ParserOutput, RawRecord, RootKind, SessionRecord,
};

pub trait ParseSink {
    fn emit(&mut self, output: ParserOutput);
}

#[derive(Default)]
pub struct CollectingSink {
    raw_records: Vec<RawRecord>,
    entries: Vec<NormalizedEntry>,
    entry_indices: HashMap<String, usize>,
    diagnostics: Vec<ParserDiagnostic>,
}

impl CollectingSink {
    #[must_use]
    pub fn finish(self, summary: ParseSummary) -> ParsedRollout {
        ParsedRollout {
            summary,
            raw_records: self.raw_records,
            entries: self.entries,
            diagnostics: self.diagnostics,
        }
    }
}

impl ParseSink for CollectingSink {
    fn emit(&mut self, output: ParserOutput) {
        match output {
            ParserOutput::Raw(raw) => self.raw_records.push(raw),
            ParserOutput::Diagnostic(diagnostic) => self.diagnostics.push(diagnostic),
            ParserOutput::EntryUpsert(entry) => {
                if let Some(index) = self.entry_indices.get(&entry.id).copied() {
                    self.entries[index] = entry;
                } else {
                    self.entry_indices
                        .insert(entry.id.clone(), self.entries.len());
                    self.entries.push(entry);
                }
            }
        }
    }
}

pub fn parse_rollout<R: BufRead, S: ParseSink>(
    reader: R,
    context: &ParseContext,
    sink: &mut S,
) -> io::Result<ParseSummary> {
    parse_rollout_inner(reader, context, sink, None, None)
}

pub(crate) fn parse_rollout_cancellable<R: BufRead, S: ParseSink>(
    reader: R,
    context: &ParseContext,
    sink: &mut S,
    shutdown: &CancellationToken,
) -> io::Result<ParseSummary> {
    parse_rollout_inner(reader, context, sink, None, Some(shutdown))
}

pub(crate) fn parse_rollout_from_seed_cancellable<R: BufRead, S: ParseSink>(
    reader: R,
    context: &ParseContext,
    sink: &mut S,
    seed: ParseSeed,
    shutdown: &CancellationToken,
) -> io::Result<ParseSummary> {
    parse_rollout_inner(reader, context, sink, Some(seed), Some(shutdown))
}

fn parse_rollout_inner<R: BufRead, S: ParseSink>(
    reader: R,
    context: &ParseContext,
    sink: &mut S,
    seed: Option<ParseSeed>,
    shutdown: Option<&CancellationToken>,
) -> io::Result<ParseSummary> {
    let initial_session_id = session_id_from_file(context);
    let seed_next_sequence = seed.as_ref().map_or(0, |value| value.next_sequence);
    let mut session = seed.as_ref().map_or_else(
        || SessionBuilder::new(context, initial_session_id.clone()),
        |value| SessionBuilder::from_record(value.session.clone()),
    );
    if let Some(source_id) = seed.as_ref().and_then(|value| {
        value
            .recent
            .iter()
            .rev()
            .map(|(_, entry)| entry)
            .find(|entry| entry.title == "Inter-agent message")
            .and_then(source_item_id)
    }) {
        session.last_inter_agent_source_id = Some(source_id.to_owned());
    }
    let mut deduper = seed
        .as_ref()
        .map_or_else(|| Deduper::new(initial_session_id), Deduper::from_seed);
    let mut jsonl = match seed.as_ref() {
        Some(value) => BoundedJsonlReader::from_position(
            reader,
            context.max_event_bytes,
            value.checkpoint_line.saturating_add(1),
            value.checkpoint_offset,
        ),
        None => BoundedJsonlReader::new(reader, context.max_event_bytes),
    };
    let mut raw_record_count = seed.as_ref().map_or(0, |value| value.raw_record_count);
    let mut recognized_record_count = seed
        .as_ref()
        .map_or(0, |value| value.recognized_record_count);
    let mut incomplete_tail = false;
    let mut partial = seed.as_ref().is_some_and(|value| value.partial);
    let mut new_entry_ids = HashSet::new();

    loop {
        if shutdown.is_some_and(CancellationToken::is_cancelled) {
            return Err(io::Error::new(
                io::ErrorKind::Interrupted,
                "index scan cancelled",
            ));
        }
        let Some(line) = jsonl.read_next()? else {
            break;
        };
        raw_record_count = raw_record_count.saturating_add(1);
        if line.status == LineReadStatus::IncompleteTail {
            incomplete_tail = true;
            let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
            sink.emit(ParserOutput::Raw(RawRecord {
                id: raw_id.clone(),
                line_no: line.line_no,
                byte_offset: line.byte_offset,
                byte_length: line.byte_length,
                envelope_type: String::new(),
                parse_status: "incomplete_tail".into(),
                content_hash: line.content_hash,
                utf8: true,
                oversize: false,
                hex_preview: None,
            }));
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Info,
                "incomplete_tail",
                "trailing JSONL record is incomplete and will be retried",
                Some(line.line_no),
                Some(raw_id),
            );
            break;
        }

        if line.status == LineReadStatus::Oversize {
            partial = true;
            let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
            sink.emit(ParserOutput::Raw(RawRecord {
                id: raw_id.clone(),
                line_no: line.line_no,
                byte_offset: line.byte_offset,
                byte_length: line.byte_length,
                envelope_type: String::new(),
                parse_status: "oversize".into(),
                content_hash: line.content_hash,
                utf8: true,
                oversize: true,
                hex_preview: None,
            }));
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Warning,
                "content_too_large",
                "JSONL record exceeds configured event size limit",
                Some(line.line_no),
                Some(raw_id),
            );
            continue;
        }

        let bytes = line.bytes.expect("complete bounded line has bytes");
        if std::str::from_utf8(&bytes).is_err() {
            partial = true;
            let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
            sink.emit(ParserOutput::Raw(RawRecord {
                id: raw_id.clone(),
                line_no: line.line_no,
                byte_offset: line.byte_offset,
                byte_length: line.byte_length,
                envelope_type: String::new(),
                parse_status: "invalid_utf8".into(),
                content_hash: line.content_hash,
                utf8: false,
                oversize: false,
                hex_preview: Some(hex_preview(&bytes)),
            }));
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Warning,
                "invalid_utf8",
                "JSONL record is not valid UTF-8",
                Some(line.line_no),
                Some(raw_id),
            );
            continue;
        }

        let envelope = match Envelope::parse(&bytes) {
            Ok(envelope) => envelope,
            Err(_) => {
                partial = true;
                let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
                sink.emit(ParserOutput::Raw(RawRecord {
                    id: raw_id.clone(),
                    line_no: line.line_no,
                    byte_offset: line.byte_offset,
                    byte_length: line.byte_length,
                    envelope_type: String::new(),
                    parse_status: "invalid_json".into(),
                    content_hash: line.content_hash,
                    utf8: true,
                    oversize: false,
                    hex_preview: None,
                }));
                emit_diagnostic(
                    sink,
                    &mut session,
                    DiagnosticSeverity::Warning,
                    "invalid_json",
                    "JSONL record is not valid JSON",
                    Some(line.line_no),
                    Some(raw_id),
                );
                continue;
            }
        };

        if envelope.kind == "session_meta"
            && let Some(id) = payload_session_id(&envelope.payload)
        {
            session.id.clone_from(&id);
            deduper.set_session_id(id);
        }
        let raw_id = raw_ref_id(&session.id, line.byte_offset, line.byte_length);
        let known_envelope = is_known_envelope(&envelope.kind);
        let inherited =
            envelope.kind != "session_meta" && session.is_inherited_ordinal(envelope.ordinal);
        sink.emit(ParserOutput::Raw(RawRecord {
            id: raw_id.clone(),
            line_no: line.line_no,
            byte_offset: line.byte_offset,
            byte_length: line.byte_length,
            envelope_type: envelope.kind.clone(),
            parse_status: if inherited {
                "inherited"
            } else if known_envelope {
                "valid"
            } else {
                "unknown"
            }
            .into(),
            content_hash: line.content_hash,
            utf8: true,
            oversize: false,
            hex_preview: None,
        }));

        let timestamp_micros = envelope.timestamp.as_deref().and_then(parse_timestamp);
        if let Some(timestamp) = timestamp_micros {
            session.updated_at_micros = session.updated_at_micros.max(timestamp);
        } else if envelope.timestamp.is_some() {
            partial = true;
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Warning,
                "invalid_timestamp",
                "record timestamp is not valid RFC3339",
                Some(line.line_no),
                Some(raw_id.clone()),
            );
        }

        if envelope.kind == "session_meta"
            && envelope
                .payload
                .get("history_base")
                .is_some_and(|value| !value.is_null())
        {
            partial = true;
            emit_diagnostic(
                sink,
                &mut session,
                DiagnosticSeverity::Warning,
                "unsupported_history_base",
                "history_base references another rollout; only this rollout's local records are indexed",
                Some(line.line_no),
                Some(raw_id.clone()),
            );
        }

        if known_envelope {
            recognized_record_count = recognized_record_count.saturating_add(1);
        }
        if inherited {
            continue;
        }

        let normalized = normalize_envelope(
            &envelope,
            timestamp_micros,
            &raw_id,
            line.line_no,
            &mut session,
        );
        match normalized {
            NormalizeResult::None => {}
            NormalizeResult::Entry(candidate) => {
                let entry = deduper.accept(candidate, line.line_no);
                session.observe_entry(&entry);
                if entry.sequence > seed_next_sequence {
                    new_entry_ids.insert(entry.id.clone());
                }
                sink.emit(ParserOutput::EntryUpsert(entry));
            }
            NormalizeResult::Unknown(candidate, code) => {
                partial = true;
                let entry = deduper.accept(candidate, line.line_no);
                if entry.sequence > seed_next_sequence {
                    new_entry_ids.insert(entry.id.clone());
                }
                sink.emit(ParserOutput::EntryUpsert(entry));
                emit_diagnostic(
                    sink,
                    &mut session,
                    DiagnosticSeverity::Warning,
                    code,
                    "record type is not supported; raw metadata remains available",
                    Some(line.line_no),
                    Some(raw_id),
                );
            }
        }
    }

    let new_entry_count = new_entry_ids.len() as u64;
    session.entry_count = session.entry_count.saturating_add(new_entry_count);
    let checkpoint = jsonl.stable_prefix();
    Ok(ParseSummary {
        session: session.finish(context, recognized_record_count, partial, incomplete_tail),
        raw_record_count,
        recognized_record_count,
        incomplete_tail,
        stable_prefix_bytes: checkpoint.offset,
        stable_prefix_hash: checkpoint.prefix_hash,
    })
}

enum NormalizeResult {
    None,
    Entry(NormalizedEntry),
    Unknown(NormalizedEntry, &'static str),
}

fn normalize_envelope(
    envelope: &Envelope,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    line_no: u64,
    session: &mut SessionBuilder,
) -> NormalizeResult {
    match envelope.kind.as_str() {
        "session_meta" => {
            session.apply_metadata(&envelope.payload, timestamp_micros, line_no);
            NormalizeResult::None
        }
        "turn_context" => NormalizeResult::Entry(context_entry(
            "Turn context",
            &envelope.payload,
            timestamp_micros,
            raw_id,
        )),
        "world_state" => NormalizeResult::Entry(context_entry(
            "World state",
            &envelope.payload,
            timestamp_micros,
            raw_id,
        )),
        "inter_agent_communication" => {
            let entry =
                inter_agent_communication_entry(&envelope.payload, timestamp_micros, raw_id);
            session.last_inter_agent_source_id = source_item_id(&entry).map(str::to_owned);
            NormalizeResult::Entry(entry)
        }
        "inter_agent_communication_metadata" => {
            let mut entry = simple_entry(
                EntryKind::Context,
                "Inter-agent delivery metadata",
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            );
            if let Some(source_id) = &session.last_inter_agent_source_id {
                entry
                    .metadata
                    .insert("sourceItemId".into(), Value::String(source_id.clone()));
            }
            if let Some(trigger_turn) = envelope
                .payload
                .get("trigger_turn")
                .and_then(Value::as_bool)
            {
                entry
                    .metadata
                    .insert("triggerTurn".into(), Value::Bool(trigger_turn));
            }
            NormalizeResult::Entry(entry)
        }
        "event_msg" => normalize_event(&envelope.payload, timestamp_micros, raw_id),
        "response_item" => {
            normalize_response_item(&envelope.payload, timestamp_micros, raw_id, session)
        }
        "compacted" => NormalizeResult::Entry(simple_entry(
            EntryKind::Marker,
            "Conversation compacted",
            String::new(),
            timestamp_micros,
            raw_id,
            EntryOrigin::Derived,
            false,
            true,
        )),
        _ => NormalizeResult::Unknown(
            simple_entry(
                EntryKind::Unknown,
                if envelope.kind.is_empty() {
                    "Unknown record"
                } else {
                    "Unknown envelope"
                },
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            ),
            "unknown_envelope",
        ),
    }
}

fn normalize_event(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizeResult {
    let kind = payload
        .get("type")
        .and_then(Value::as_str)
        .unwrap_or_default();
    match kind {
        "user_message" => {
            let text = string_field(payload, &["message", "text"]);
            let mut entry = message_entry(
                MessageRole::User,
                None,
                text,
                timestamp_micros,
                raw_id,
                EntryOrigin::EventPresentation,
            );
            add_attachment_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "agent_message" => NormalizeResult::Entry(message_entry(
            MessageRole::Assistant,
            phase_field(payload),
            string_field(payload, &["message"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::EventPresentation,
        )),
        "agent_reasoning" => NormalizeResult::Entry(reasoning_entry(
            string_field(payload, &["text", "message"]),
            timestamp_micros,
            raw_id,
            true,
        )),
        "agent_reasoning_raw_content" | "reasoning_raw_content_delta" => NormalizeResult::None,
        "item_completed" => normalize_item_completed(payload, timestamp_micros, raw_id),
        "thread_settings_applied" => {
            let mut entry = simple_entry(
                EntryKind::Context,
                "Thread settings applied",
                pretty_value(payload.get("thread_settings")),
                timestamp_micros,
                raw_id,
                EntryOrigin::EventPresentation,
                false,
                true,
            );
            entry.metadata.insert(
                "eventType".into(),
                Value::String("thread_settings_applied".into()),
            );
            NormalizeResult::Entry(entry)
        }
        "plan_update" | "plan_delta" => NormalizeResult::Entry(simple_entry(
            EntryKind::Plan,
            "Plan",
            plan_text(payload),
            timestamp_micros,
            raw_id,
            EntryOrigin::EventPresentation,
            true,
            false,
        )),
        "warning" | "guardian_warning" | "stream_error" | "deprecation_notice" => {
            NormalizeResult::Entry(simple_entry(
                EntryKind::Warning,
                "Warning",
                string_field(payload, &["message", "text"]),
                timestamp_micros,
                raw_id,
                EntryOrigin::EventPresentation,
                true,
                false,
            ))
        }
        "error" => NormalizeResult::Entry(simple_entry(
            EntryKind::Error,
            "Error",
            string_field(payload, &["message", "text"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::EventPresentation,
            true,
            false,
        )),
        "request_user_input" => NormalizeResult::Entry(request_user_input_event_entry(
            payload,
            timestamp_micros,
            raw_id,
        )),
        event if tool_event_kind(event).is_some() => {
            NormalizeResult::Entry(tool_event_entry(event, payload, timestamp_micros, raw_id))
        }
        "context_compacted"
        | "thread_rolled_back"
        | "task_started"
        | "turn_started"
        | "task_complete"
        | "turn_complete"
        | "turn_aborted"
        | "entered_review_mode"
        | "exited_review_mode"
        | "collab_agent_spawn_begin"
        | "collab_agent_spawn_end"
        | "collab_agent_interaction_begin"
        | "collab_agent_interaction_end"
        | "sub_agent_activity"
        | "thread_goal_updated" => NormalizeResult::Entry(simple_entry(
            EntryKind::Marker,
            event_title(kind),
            string_field(payload, &["message", "goal"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::EventPresentation,
            false,
            true,
        )),
        "token_count" | "session_configured" | "mcp_startup_update" | "mcp_startup_complete" => {
            let mut entry = simple_entry(
                EntryKind::Context,
                event_title(kind),
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            );
            entry
                .metadata
                .insert("eventType".into(), Value::String(kind.into()));
            NormalizeResult::Entry(entry)
        }
        _ => NormalizeResult::Unknown(
            simple_entry(
                EntryKind::Unknown,
                if kind.is_empty() {
                    "Unknown event"
                } else {
                    kind
                },
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            ),
            "unknown_event",
        ),
    }
}

fn normalize_item_completed(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizeResult {
    let item = payload.get("item").unwrap_or(&Value::Null);
    let kind = item.get("type").and_then(Value::as_str).unwrap_or_default();
    let mut entry = match kind {
        "UserMessage" | "user_message" => {
            let mut entry = message_entry(
                MessageRole::User,
                None,
                user_input_text(item.get("content")),
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            );
            add_attachment_metadata(&mut entry, item);
            entry
        }
        "HookPrompt" | "hook_prompt" => simple_entry(
            EntryKind::Context,
            "Hook prompt",
            content_text(item.get("fragments")),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            false,
            true,
        ),
        "AgentMessage" | "agent_message" => message_entry(
            MessageRole::Assistant,
            phase_field(item),
            content_text(item.get("content")),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "Plan" | "plan" => simple_entry(
            EntryKind::Plan,
            "Plan",
            string_field(item, &["text"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            true,
            false,
        ),
        "Reasoning" | "reasoning" => {
            let text = content_text(item.get("summary_text"));
            let mut entry =
                reasoning_entry(text.clone(), timestamp_micros, raw_id, !text.is_empty());
            entry.origin = EntryOrigin::ItemCompleted;
            entry
        }
        "CommandExecution" | "command_execution" => tool_entry(
            ToolKind::Command,
            "Command",
            string_array(item.get("command"), " "),
            joined_fields(
                item,
                &["formatted_output", "aggregated_output", "stdout", "stderr"],
            ),
            item_id(item),
            status_field(item),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "DynamicToolCall" | "dynamic_tool_call" => {
            let status = if item.get("success").and_then(Value::as_bool) == Some(false) {
                Some(ToolStatus::Failed)
            } else {
                status_field(item)
            };
            let mut entry = tool_entry(
                ToolKind::Dynamic,
                &string_field(item, &["tool"]),
                pretty_value(item.get("arguments")),
                joined_fields(item, &["content_items", "error"]),
                item_id(item),
                status,
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            );
            add_attachment_metadata(&mut entry, item);
            entry
        }
        "CollabAgentToolCall" | "collab_agent_tool_call" => tool_entry(
            ToolKind::Other,
            "Collaboration",
            string_field(item, &["prompt", "tool"]),
            joined_fields(
                item,
                &["receiver_agents", "receiver_thread_ids", "agents_states"],
            ),
            item_id(item),
            status_field(item),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "SubAgentActivity" | "sub_agent_activity" => simple_entry(
            EntryKind::Marker,
            "Sub-agent activity",
            string_field(item, &["kind", "agent_path"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            true,
            true,
        ),
        "WebSearch" | "web_search" => tool_entry(
            ToolKind::WebSearch,
            "Web search",
            string_field(item, &["query"]),
            joined_fields(item, &["action", "results"]),
            item_id(item),
            Some(ToolStatus::Succeeded),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "ImageView" | "image_view" => tool_entry(
            ToolKind::ViewImage,
            "Image attachment",
            string_field(item, &["path"]),
            String::new(),
            item_id(item),
            Some(ToolStatus::Succeeded),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "Extension" | "extension" => {
            match normalize_extension_item(item, timestamp_micros, raw_id) {
                NormalizeResult::Entry(entry) => entry,
                other => return other,
            }
        }
        "ImageGeneration" | "image_generation" => {
            let mut entry = tool_entry(
                ToolKind::Other,
                "Image generation",
                string_field(item, &["revised_prompt", "revisedPrompt"]),
                string_field(item, &["saved_path", "savedPath"]),
                item_id(item),
                status_field(item),
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            );
            add_attachment_counts(&mut entry, 1, 0);
            entry
        }
        "EnteredReviewMode" | "entered_review_mode" => simple_entry(
            EntryKind::Marker,
            "Review started",
            string_field(item, &["user_facing_hint"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            true,
            true,
        ),
        "ExitedReviewMode" | "exited_review_mode" => simple_entry(
            EntryKind::Marker,
            "Review completed",
            pretty_value(item.get("review_output")),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            true,
            true,
        ),
        "FileChange" | "file_change" => tool_entry(
            ToolKind::Patch,
            "Patch",
            pretty_value(item.get("changes")),
            joined_fields(item, &["stdout", "stderr"]),
            item_id(item),
            status_field(item).or(Some(ToolStatus::Succeeded)),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "McpToolCall" | "mcp_tool_call" => {
            let server = string_field(item, &["server"]);
            let tool = string_field(item, &["tool"]);
            let title = if server.is_empty() && tool.is_empty() {
                "MCP tool".into()
            } else {
                format!("{server}/{tool}")
            };
            tool_entry(
                ToolKind::Mcp,
                &title,
                pretty_value(item.get("arguments")),
                joined_fields(item, &["result", "error"]),
                item_id(item),
                status_field(item),
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            )
        }
        "ContextCompaction" | "context_compaction" => simple_entry(
            EntryKind::Marker,
            "Conversation compacted",
            String::new(),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            false,
            true,
        ),
        _ => {
            return NormalizeResult::Unknown(
                simple_entry(
                    EntryKind::Unknown,
                    if kind.is_empty() {
                        "Unknown item"
                    } else {
                        kind
                    },
                    String::new(),
                    timestamp_micros,
                    raw_id,
                    EntryOrigin::Derived,
                    false,
                    true,
                ),
                "unknown_turn_item",
            );
        }
    };
    add_source_item_id(&mut entry, item);
    if let Some(turn_id) = string_option(payload, "turn_id") {
        entry
            .metadata
            .insert("turnId".into(), Value::String(turn_id));
    }
    NormalizeResult::Entry(entry)
}

fn normalize_extension_item(
    item: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizeResult {
    let kind = item.get("kind").and_then(Value::as_str).unwrap_or_default();
    let mut entry = match kind {
        "clock.sleep" => tool_entry(
            ToolKind::Other,
            "Sleep",
            item.get("durationMs")
                .or_else(|| item.get("duration_ms"))
                .and_then(Value::as_u64)
                .map(|duration| format!("{duration} ms"))
                .unwrap_or_default(),
            String::new(),
            item_id(item),
            Some(ToolStatus::Succeeded),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "web.search" => tool_entry(
            ToolKind::WebSearch,
            "Web search",
            string_field(item, &["query"]),
            joined_fields(item, &["action", "results"]),
            item_id(item),
            Some(ToolStatus::Succeeded),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "image_gen.generation" => {
            let mut entry = tool_entry(
                ToolKind::Other,
                "Image generation",
                string_field(item, &["revisedPrompt", "revised_prompt"]),
                string_field(item, &["savedPath", "saved_path"]),
                item_id(item),
                status_field(item),
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            );
            add_attachment_counts(&mut entry, 1, 0);
            entry
        }
        _ => {
            return NormalizeResult::Unknown(
                simple_entry(
                    EntryKind::Unknown,
                    if kind.is_empty() {
                        "Unknown extension"
                    } else {
                        kind
                    },
                    String::new(),
                    timestamp_micros,
                    raw_id,
                    EntryOrigin::Derived,
                    false,
                    true,
                ),
                "unknown_extension_item",
            );
        }
    };
    add_source_item_id(&mut entry, item);
    NormalizeResult::Entry(entry)
}

fn inter_agent_communication_entry(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizedEntry {
    let plaintext = string_field(payload, &["content"]);
    let encrypted = has_encrypted_content(payload);
    let mut entry = message_entry(
        MessageRole::Assistant,
        None,
        if encrypted {
            "Encrypted inter-agent message".into()
        } else {
            plaintext
        },
        timestamp_micros,
        raw_id,
        EntryOrigin::ResponseItem,
    );
    entry.presentation = EntryPresentation::Technical;
    entry.title = "Inter-agent message".into();
    entry.default_collapsed = true;
    entry.searchable = !encrypted && !entry.primary_text.is_empty();
    add_source_item_id(&mut entry, payload);
    for (source, target) in [
        ("author", "author"),
        ("recipient", "recipient"),
        ("other_recipients", "otherRecipients"),
    ] {
        if let Some(value) = payload.get(source).filter(|value| !value.is_null()) {
            entry.metadata.insert(target.into(), value.clone());
        }
    }
    if let Some(trigger_turn) = payload.get("trigger_turn").and_then(Value::as_bool) {
        entry
            .metadata
            .insert("triggerTurn".into(), Value::Bool(trigger_turn));
    }
    entry
}

fn normalize_response_item(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    session: &SessionBuilder,
) -> NormalizeResult {
    let kind = payload
        .get("type")
        .and_then(Value::as_str)
        .unwrap_or_default();
    match kind {
        "message" => {
            let role = role_field(payload).unwrap_or(MessageRole::Assistant);
            let startup_context =
                !session.saw_user && matches!(role, MessageRole::System | MessageRole::Developer);
            let mut entry = message_entry(
                role,
                phase_field(payload),
                content_text(payload.get("content")),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            if startup_context {
                entry.default_collapsed = true;
                entry.searchable = false;
            }
            add_attachment_metadata(&mut entry, payload);
            add_source_item_id(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "agent_message" => {
            let mut entry = inter_agent_communication_entry(payload, timestamp_micros, raw_id);
            entry.origin = EntryOrigin::ResponseItem;
            NormalizeResult::Entry(entry)
        }
        "reasoning" => {
            let summary = content_text(payload.get("summary"));
            let searchable = !summary.is_empty();
            let text = if searchable {
                summary
            } else if payload
                .get("encrypted_content")
                .is_some_and(|value| !value.is_null())
            {
                "Encrypted reasoning".into()
            } else {
                String::new()
            };
            let mut entry = reasoning_entry(text, timestamp_micros, raw_id, searchable);
            entry.origin = EntryOrigin::ResponseItem;
            add_source_item_id(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "function_call" | "custom_tool_call" | "tool_search_call" => {
            let name = string_field(payload, &["name", "execution"]);
            let primary = string_field(payload, &["arguments", "input"]);
            let mut entry = tool_entry(
                tool_kind_from_name(&name),
                if name.is_empty() { "Tool call" } else { &name },
                primary.clone(),
                String::new(),
                call_id(payload),
                Some(ToolStatus::Running),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            if entry.tool_kind == Some(ToolKind::RequestUserInput) {
                add_request_user_input_questions_from_text(&mut entry, &primary);
            }
            add_source_item_id(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "function_call_output" | "custom_tool_call_output" | "tool_search_output" => {
            let secondary = [
                output_text(payload.get("output")),
                value_text(payload.get("execution")).unwrap_or_default(),
                pretty_value(payload.get("tools")),
            ]
            .into_iter()
            .filter(|text| !text.is_empty())
            .collect::<Vec<_>>()
            .join("\n");
            let mut entry = tool_entry(
                ToolKind::Function,
                "Tool output",
                String::new(),
                secondary.clone(),
                call_id(payload),
                Some(ToolStatus::Succeeded),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            add_request_user_input_response_from_text(&mut entry, &secondary);
            add_attachment_metadata(&mut entry, payload);
            add_source_item_id(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "local_shell_call" => {
            let mut entry = tool_entry(
                ToolKind::Command,
                "Command",
                pretty_value(payload.get("action")),
                String::new(),
                call_id(payload),
                status_field(payload).or(Some(ToolStatus::Running)),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            add_source_item_id(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "web_search_call" => {
            let mut entry = tool_entry(
                ToolKind::WebSearch,
                "Web search",
                pretty_value(payload.get("action")),
                String::new(),
                call_id(payload).or_else(|| string_option(payload, "id")),
                status_field(payload),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            add_source_item_id(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "image_generation_call" => {
            let mut entry = tool_entry(
                ToolKind::Other,
                "Image generation",
                string_field(payload, &["revised_prompt"]),
                String::new(),
                call_id(payload).or_else(|| string_option(payload, "id")),
                status_field(payload),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            add_attachment_counts(&mut entry, 1, 0);
            add_source_item_id(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "compaction" | "compaction_summary" | "context_compaction" | "compaction_trigger" => {
            let mut entry = simple_entry(
                EntryKind::Marker,
                "Conversation compacted",
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            );
            add_source_item_id(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        _ => NormalizeResult::Unknown(
            simple_entry(
                EntryKind::Unknown,
                if kind.is_empty() {
                    "Unknown response"
                } else {
                    kind
                },
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            ),
            "unknown_response_item",
        ),
    }
}

fn context_entry(
    title: &str,
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizedEntry {
    simple_entry(
        EntryKind::Context,
        title,
        pretty_value(Some(payload)),
        timestamp_micros,
        raw_id,
        EntryOrigin::Derived,
        false,
        true,
    )
}

fn message_entry(
    role: MessageRole,
    phase: Option<Phase>,
    text: String,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    origin: EntryOrigin,
) -> NormalizedEntry {
    let presentation = message_presentation(role, &text);
    let internal = presentation == EntryPresentation::Internal;
    let technical = presentation == EntryPresentation::Technical;
    NormalizedEntry {
        id: String::new(),
        session_id: String::new(),
        sequence: 0,
        timestamp_micros,
        kind: EntryKind::Message,
        presentation,
        role: Some(role),
        phase,
        tool_kind: None,
        tool_status: None,
        title: match presentation {
            EntryPresentation::User => "User",
            EntryPresentation::Response if role == MessageRole::User => "Received",
            EntryPresentation::Response => "Assistant",
            EntryPresentation::Technical => "Technical message",
            EntryPresentation::Internal => "Internal context",
        }
        .into(),
        primary_text: text,
        secondary_text: String::new(),
        metadata: BTreeMap::new(),
        call_id: None,
        parent_entry_id: None,
        default_collapsed: internal || technical,
        searchable: !internal && !technical,
        raw_refs: vec![raw_id.into()],
        origin,
        id_basis: String::new(),
    }
}

fn message_presentation(role: MessageRole, text: &str) -> EntryPresentation {
    if is_internal_message(text) || matches!(role, MessageRole::Developer | MessageRole::System) {
        return EntryPresentation::Internal;
    }
    if is_received_wrapper(text) {
        return EntryPresentation::Response;
    }
    if is_technical_wrapper(text) {
        return EntryPresentation::Technical;
    }
    match role {
        MessageRole::User => EntryPresentation::User,
        MessageRole::Assistant => EntryPresentation::Response,
        MessageRole::Developer | MessageRole::System => EntryPresentation::Internal,
    }
}

fn is_internal_message(text: &str) -> bool {
    let trimmed = text.trim_start();
    trimmed.starts_with("# AGENTS.md instructions for ")
        || [
            "<environment_context",
            "<codex_internal_context",
            "<permissions instructions>",
            "<collaboration_mode>",
            "<skills_instructions>",
            "<plugins_instructions>",
            "<system-reminder",
            "<skill>",
        ]
        .iter()
        .any(|prefix| trimmed.starts_with(prefix))
}

fn is_received_wrapper(text: &str) -> bool {
    text.trim_start().starts_with("<user_action>")
}

fn is_technical_wrapper(text: &str) -> bool {
    text.trim_start().starts_with("<turn_aborted>")
}

fn reasoning_entry(
    text: String,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    searchable: bool,
) -> NormalizedEntry {
    let mut entry = simple_entry(
        EntryKind::Reasoning,
        "Reasoning",
        text,
        timestamp_micros,
        raw_id,
        EntryOrigin::EventPresentation,
        searchable,
        true,
    );
    entry.phase = Some(Phase::Analysis);
    entry
}

#[allow(clippy::too_many_arguments)]
fn tool_entry(
    kind: ToolKind,
    title: &str,
    primary: String,
    secondary: String,
    call_id: Option<String>,
    status: Option<ToolStatus>,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    origin: EntryOrigin,
) -> NormalizedEntry {
    NormalizedEntry {
        id: String::new(),
        session_id: String::new(),
        sequence: 0,
        timestamp_micros,
        kind: EntryKind::Tool,
        presentation: EntryPresentation::Technical,
        role: None,
        phase: None,
        tool_kind: Some(kind),
        tool_status: status,
        title: title.into(),
        primary_text: primary,
        secondary_text: secondary,
        metadata: BTreeMap::new(),
        call_id,
        parent_entry_id: None,
        default_collapsed: true,
        searchable: true,
        raw_refs: vec![raw_id.into()],
        origin,
        id_basis: String::new(),
    }
}

#[allow(clippy::too_many_arguments)]
fn simple_entry(
    kind: EntryKind,
    title: &str,
    primary: String,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    origin: EntryOrigin,
    searchable: bool,
    default_collapsed: bool,
) -> NormalizedEntry {
    NormalizedEntry {
        id: String::new(),
        session_id: String::new(),
        sequence: 0,
        timestamp_micros,
        kind,
        presentation: EntryPresentation::Technical,
        role: None,
        phase: None,
        tool_kind: None,
        tool_status: None,
        title: title.into(),
        primary_text: primary,
        secondary_text: String::new(),
        metadata: BTreeMap::new(),
        call_id: None,
        parent_entry_id: None,
        default_collapsed,
        searchable,
        raw_refs: vec![raw_id.into()],
        origin,
        id_basis: String::new(),
    }
}

fn tool_event_entry(
    event: &str,
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizedEntry {
    let kind = tool_event_kind(event).unwrap_or(ToolKind::Other);
    let title = string_field(payload, &["name", "server", "query"]);
    let primary = string_field(
        payload,
        &["command", "input", "arguments", "query", "revised_prompt"],
    );
    let secondary_fields: &[&str] = match event {
        "mcp_tool_call_end" => &["result"],
        "web_search_end" => &["action", "results"],
        "image_generation_end" => &["saved_path"],
        _ => &["delta", "output", "stdout", "stderr"],
    };
    let secondary = joined_fields(payload, secondary_fields);
    let status = if event.ends_with("_end") || event.ends_with("_response") {
        status_field(payload).or(Some(
            if payload.get("success").and_then(Value::as_bool) == Some(false) {
                ToolStatus::Failed
            } else {
                ToolStatus::Succeeded
            },
        ))
    } else {
        Some(ToolStatus::Running)
    };
    let mut entry = tool_entry(
        kind,
        if title.is_empty() {
            event_title(event)
        } else {
            &title
        },
        primary,
        secondary,
        call_id(payload),
        status,
        timestamp_micros,
        raw_id,
        EntryOrigin::EventPresentation,
    );
    add_attachment_metadata(&mut entry, payload);
    if event == "image_generation_end" {
        add_attachment_counts(&mut entry, 1, 0);
    }
    entry
}

fn request_user_input_event_entry(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizedEntry {
    let mut entry = tool_entry(
        ToolKind::RequestUserInput,
        "request_user_input",
        pretty_value(Some(payload)),
        String::new(),
        call_id(payload),
        Some(ToolStatus::Running),
        timestamp_micros,
        raw_id,
        EntryOrigin::EventPresentation,
    );
    add_request_user_input_questions(&mut entry, payload.get("questions"));
    entry
}

fn add_request_user_input_questions_from_text(entry: &mut NormalizedEntry, text: &str) {
    let Ok(value) = serde_json::from_str::<Value>(text) else {
        return;
    };
    add_request_user_input_questions(entry, value.get("questions"));
}

fn add_request_user_input_questions(entry: &mut NormalizedEntry, questions: Option<&Value>) {
    if let Some(questions) = questions.filter(|value| value.is_array()) {
        entry
            .metadata
            .insert("requestUserInputQuestions".into(), questions.clone());
    }
}

fn add_request_user_input_response_from_text(entry: &mut NormalizedEntry, text: &str) {
    let Ok(value) = serde_json::from_str::<Value>(text) else {
        return;
    };
    if let Some(answers) = value.get("answers").filter(|value| value.is_object()) {
        entry
            .metadata
            .insert("requestUserInputAnswers".into(), answers.clone());
    }
    if let Some(notes) = value.get("notes").filter(|value| value.is_string()) {
        entry
            .metadata
            .insert("requestUserInputNotes".into(), notes.clone());
    }
}

fn tool_event_kind(event: &str) -> Option<ToolKind> {
    if event.starts_with("exec_command") || event == "terminal_interaction" {
        Some(ToolKind::Command)
    } else if event.starts_with("patch_apply") {
        Some(ToolKind::Patch)
    } else if event.starts_with("mcp_tool_call") {
        Some(ToolKind::Mcp)
    } else if event.starts_with("web_search") {
        Some(ToolKind::WebSearch)
    } else if event.starts_with("dynamic_tool_call") {
        Some(ToolKind::Dynamic)
    } else if event.starts_with("image_generation") || event == "view_image_tool_call" {
        Some(ToolKind::ViewImage)
    } else {
        None
    }
}

fn tool_kind_from_name(name: &str) -> ToolKind {
    if name == "request_user_input" {
        ToolKind::RequestUserInput
    } else if name.contains("apply_patch") || name == "patch" {
        ToolKind::Patch
    } else if name.starts_with("mcp__") {
        ToolKind::Mcp
    } else if name.contains("web") || name.contains("search") {
        ToolKind::WebSearch
    } else if name.contains("command") || name.contains("shell") || name == "exec" {
        ToolKind::Command
    } else {
        ToolKind::Function
    }
}

struct SessionBuilder {
    id: String,
    source: SourceKind,
    parent_thread_id: Option<String>,
    parent_relation: Option<SessionParentRelation>,
    proposed_plan_hash: Option<String>,
    proposed_plan_at_micros: Option<i64>,
    handoff_plan_hash: Option<String>,
    handoff_at_micros: Option<i64>,
    cwd: Option<String>,
    title: Option<String>,
    preview: Option<String>,
    created_at_micros: i64,
    updated_at_micros: i64,
    cli_version: Option<String>,
    provider: Option<String>,
    history_line: Option<u64>,
    git_branch: Option<String>,
    git_commit: Option<String>,
    entry_count: u64,
    diagnostic_count: u64,
    saw_user: bool,
    subagent_history_start_ordinal: Option<u64>,
    last_inter_agent_source_id: Option<String>,
}

impl SessionBuilder {
    fn new(context: &ParseContext, id: String) -> Self {
        let created =
            timestamp_from_filename(&context.file_name).unwrap_or(context.modified_at_micros);
        Self {
            id,
            source: SourceKind::Unknown,
            parent_thread_id: None,
            parent_relation: None,
            proposed_plan_hash: None,
            proposed_plan_at_micros: None,
            handoff_plan_hash: None,
            handoff_at_micros: None,
            cwd: None,
            title: None,
            preview: None,
            created_at_micros: created,
            updated_at_micros: context.modified_at_micros,
            cli_version: None,
            provider: None,
            history_line: None,
            git_branch: None,
            git_commit: None,
            entry_count: 0,
            diagnostic_count: 0,
            saw_user: false,
            subagent_history_start_ordinal: None,
            last_inter_agent_source_id: None,
        }
    }

    fn from_record(record: SessionRecord) -> Self {
        let saw_user = !record.title.starts_with("Untitled · ");
        Self {
            id: record.id,
            source: record.source,
            parent_thread_id: record.parent_thread_id,
            parent_relation: record.parent_relation,
            proposed_plan_hash: record.proposed_plan_hash,
            proposed_plan_at_micros: record.proposed_plan_at_micros,
            handoff_plan_hash: record.handoff_plan_hash,
            handoff_at_micros: record.handoff_at_micros,
            cwd: record.cwd,
            title: saw_user.then_some(record.title),
            preview: saw_user.then_some(record.preview),
            created_at_micros: record.created_at_micros,
            updated_at_micros: record.updated_at_micros,
            cli_version: record.cli_version,
            provider: record.provider,
            history_line: record.history_line,
            git_branch: record.git_branch,
            git_commit: record.git_commit,
            entry_count: record.entry_count,
            diagnostic_count: record.diagnostic_count,
            saw_user,
            subagent_history_start_ordinal: None,
            last_inter_agent_source_id: None,
        }
    }

    fn apply_metadata(&mut self, payload: &Value, timestamp: Option<i64>, line_no: u64) {
        if let Some(id) = payload_session_id(payload) {
            self.id = id;
        }
        self.cwd = string_option(payload, "cwd").or_else(|| self.cwd.take());
        if let Some(parent) = string_option(payload, "parent_thread_id")
            .or_else(|| source_parent(payload.get("source")))
        {
            self.parent_thread_id = Some(parent);
            self.parent_relation = Some(SessionParentRelation::Parent);
        } else if self.parent_relation != Some(SessionParentRelation::Parent)
            && let Some(parent) = string_option(payload, "forked_from_id")
        {
            self.parent_thread_id = Some(parent);
            self.parent_relation = Some(SessionParentRelation::Fork);
        }
        self.cli_version =
            string_option(payload, "cli_version").or_else(|| self.cli_version.take());
        self.provider = string_option(payload, "model_provider").or_else(|| self.provider.take());
        self.subagent_history_start_ordinal = payload
            .get("subagent_history_start_ordinal")
            .and_then(Value::as_u64)
            .or(self.subagent_history_start_ordinal);
        self.source = source_kind(payload);
        self.history_line = payload
            .get("history_line")
            .and_then(Value::as_u64)
            .or(self.history_line)
            .or(Some(line_no));
        if let Some(metadata_timestamp) = payload
            .get("timestamp")
            .and_then(Value::as_str)
            .and_then(parse_timestamp)
            .or(timestamp)
        {
            self.created_at_micros = metadata_timestamp;
            self.updated_at_micros = self.updated_at_micros.max(metadata_timestamp);
        }
        if let Some(git) = payload.get("git") {
            self.git_branch = string_option(git, "branch");
            self.git_commit =
                string_option(git, "commit_hash").or_else(|| string_option(git, "commit"));
        }
    }

    fn is_inherited_ordinal(&self, ordinal: Option<u64>) -> bool {
        ordinal.is_some_and(|ordinal| {
            self.subagent_history_start_ordinal
                .is_some_and(|start| ordinal < start)
        })
    }

    fn observe_entry(&mut self, entry: &NormalizedEntry) {
        if entry.presentation == EntryPresentation::Response
            && entry.role == Some(MessageRole::Assistant)
            && let Some(hash) = proposed_plan_hash(&entry.primary_text)
        {
            self.proposed_plan_hash = Some(hash);
            self.proposed_plan_at_micros =
                Some(entry.timestamp_micros.unwrap_or(self.updated_at_micros));
        }
        if !self.saw_user
            && entry.presentation == EntryPresentation::User
            && !entry.primary_text.trim().is_empty()
        {
            if let Some(hash) = handoff_plan_hash(&entry.primary_text) {
                self.handoff_plan_hash = Some(hash);
                self.handoff_at_micros =
                    Some(entry.timestamp_micros.unwrap_or(self.updated_at_micros));
            }
            self.saw_user = true;
            self.title = Some(title_from_user_message(&entry.primary_text));
            self.preview = Some(truncate_graphemes(&entry.primary_text, 160));
        }
    }

    fn finish(
        self,
        context: &ParseContext,
        recognized: u64,
        partial: bool,
        incomplete_tail: bool,
    ) -> SessionRecord {
        let completeness = if recognized == 0 {
            Completeness::Unsupported
        } else if partial {
            Completeness::Partial
        } else if context.root_kind == RootKind::Active
            && (incomplete_tail
                || context.now_micros.saturating_sub(self.updated_at_micros) <= 60_000_000)
        {
            Completeness::Live
        } else {
            Completeness::Complete
        };
        let fallback_title = format!(
            "Untitled · {}",
            DateTime::<Utc>::from_timestamp_micros(self.created_at_micros)
                .map_or_else(|| "unknown time".into(), |value| value.to_rfc3339())
        );
        SessionRecord {
            id: self.id,
            source: self.source,
            parent_thread_id: self.parent_thread_id,
            parent_relation: self.parent_relation,
            proposed_plan_hash: self.proposed_plan_hash,
            proposed_plan_at_micros: self.proposed_plan_at_micros,
            handoff_plan_hash: self.handoff_plan_hash,
            handoff_at_micros: self.handoff_at_micros,
            cwd: self.cwd,
            title: self.title.unwrap_or(fallback_title),
            preview: self.preview.unwrap_or_default(),
            created_at_micros: self.created_at_micros,
            updated_at_micros: self.updated_at_micros,
            archived: context.root_kind == RootKind::Archived,
            cli_version: self.cli_version,
            provider: self.provider,
            history_line: self.history_line,
            git_branch: self.git_branch,
            git_commit: self.git_commit,
            entry_count: self.entry_count,
            index_state: if partial {
                IndexState::Partial
            } else {
                IndexState::Ready
            },
            completeness,
            diagnostic_count: self.diagnostic_count,
        }
    }
}

const PLAN_HANDOFF_PREFIX: &str = "A previous agent produced the plan below to accomplish the user's task. Implement the plan in a fresh context. Treat the plan as the source of user intent, re-read files as needed, and carry the work through implementation and verification.";

fn proposed_plan_hash(text: &str) -> Option<String> {
    let normalized = normalize_plan_text(text);
    let close = normalized.rfind("</proposed_plan>")?;
    let before_close = &normalized[..close];
    let open = before_close.rfind("<proposed_plan>")? + "<proposed_plan>".len();
    normalized_plan_hash(&before_close[open..])
}

fn handoff_plan_hash(text: &str) -> Option<String> {
    let normalized = normalize_plan_text(text);
    let plan = normalized
        .trim()
        .strip_prefix(PLAN_HANDOFF_PREFIX)?
        .strip_prefix("\n\n")?;
    normalized_plan_hash(plan)
}

fn normalized_plan_hash(text: &str) -> Option<String> {
    let normalized = normalize_plan_text(text);
    let plan = normalized.trim();
    (!plan.is_empty()).then(|| sha256(plan.as_bytes()))
}

fn normalize_plan_text(text: &str) -> String {
    text.replace("\r\n", "\n").replace('\r', "\n")
}

fn title_from_user_message(text: &str) -> String {
    let first_line = text
        .lines()
        .map(str::trim)
        .find(|line| !line.is_empty())
        .unwrap_or_default();
    let without_markdown = first_line
        .trim_start_matches(['#', '-', '*', '+'])
        .trim_start();
    let normalized = without_markdown
        .split_whitespace()
        .collect::<Vec<_>>()
        .join(" ");
    truncate_graphemes(&normalized, 80)
}

#[allow(clippy::too_many_arguments)]
fn emit_diagnostic<S: ParseSink>(
    sink: &mut S,
    session: &mut SessionBuilder,
    severity: DiagnosticSeverity,
    code: &str,
    message: &str,
    line_no: Option<u64>,
    raw_ref_id: Option<String>,
) {
    session.diagnostic_count = session.diagnostic_count.saturating_add(1);
    sink.emit(ParserOutput::Diagnostic(ParserDiagnostic {
        severity,
        code: code.into(),
        message: message.into(),
        line_no,
        raw_ref_id,
    }));
}

fn source_kind(payload: &Value) -> SourceKind {
    let originator = payload
        .get("originator")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_ascii_lowercase();
    let source = payload.get("source");
    if let Some(value) = source.and_then(Value::as_str) {
        return match value.to_ascii_lowercase().as_str() {
            "cli" => SourceKind::Cli,
            "vscode" | "vs_code" => SourceKind::Vscode,
            "exec" => SourceKind::Exec,
            "mcp" | "appserver" | "app-server" | "app_server" => SourceKind::AppServer,
            _ => source_from_originator(&originator),
        };
    }
    if let Some(object) = source.and_then(Value::as_object)
        && let Some(subagent) = object.get("subagent").or_else(|| object.get("sub_agent"))
    {
        if subagent.as_str() == Some("review")
            || subagent.get("review").is_some()
            || object.get("review").is_some()
        {
            return SourceKind::Review;
        }
        return SourceKind::Subagent;
    }
    source_from_originator(&originator)
}

fn source_from_originator(originator: &str) -> SourceKind {
    if originator.contains("vscode") {
        SourceKind::Vscode
    } else if originator.contains("exec") {
        SourceKind::Exec
    } else if originator.contains("app-server") || originator.contains("appserver") {
        SourceKind::AppServer
    } else if originator.contains("cli") {
        SourceKind::Cli
    } else {
        SourceKind::Unknown
    }
}

fn source_parent(source: Option<&Value>) -> Option<String> {
    let object = source?.as_object()?;
    let subagent = object.get("subagent").or_else(|| object.get("sub_agent"))?;
    subagent
        .get("thread_spawn")
        .and_then(|spawn| string_option(spawn, "parent_thread_id"))
        .or_else(|| string_option(subagent, "parent_thread_id"))
}

fn payload_session_id(payload: &Value) -> Option<String> {
    ["session_id", "id"]
        .iter()
        .filter_map(|key| payload.get(*key).and_then(Value::as_str))
        .find_map(|value| Uuid::parse_str(value).ok().map(|id| id.to_string()))
}

fn session_id_from_file(context: &ParseContext) -> String {
    let stem = context
        .file_name
        .strip_suffix(".jsonl")
        .unwrap_or(&context.file_name);
    if stem.len() >= 36 {
        let candidate = &stem[stem.len() - 36..];
        if let Ok(id) = Uuid::parse_str(candidate) {
            return id.to_string();
        }
    }
    format!("s_{}", sha256(context.relative_path.as_bytes()))
}

fn timestamp_from_filename(file_name: &str) -> Option<i64> {
    let stem = file_name.strip_suffix(".jsonl")?.strip_prefix("rollout-")?;
    let timestamp = stem.get(..stem.len().checked_sub(37)?)?;
    ["%Y-%m-%dT%H-%M-%S%.f", "%Y-%m-%dT%H-%M-%S"]
        .iter()
        .find_map(|format| NaiveDateTime::parse_from_str(timestamp, format).ok())
        .map(|value| value.and_utc().timestamp_micros())
}

fn parse_timestamp(value: &str) -> Option<i64> {
    DateTime::parse_from_rfc3339(value)
        .ok()
        .map(|timestamp| timestamp.with_timezone(&Utc).timestamp_micros())
}

fn role_field(payload: &Value) -> Option<MessageRole> {
    match payload.get("role").and_then(Value::as_str)? {
        "user" => Some(MessageRole::User),
        "assistant" => Some(MessageRole::Assistant),
        "developer" => Some(MessageRole::Developer),
        "system" => Some(MessageRole::System),
        _ => None,
    }
}

fn phase_field(payload: &Value) -> Option<Phase> {
    match payload.get("phase").and_then(Value::as_str)? {
        "commentary" => Some(Phase::Commentary),
        "final" | "final_answer" => Some(Phase::Final),
        "analysis" => Some(Phase::Analysis),
        _ => Some(Phase::Unknown),
    }
}

fn status_field(payload: &Value) -> Option<ToolStatus> {
    match payload.get("status").and_then(Value::as_str)? {
        "pending" => Some(ToolStatus::Pending),
        "in_progress" | "running" => Some(ToolStatus::Running),
        "completed" | "succeeded" | "success" => Some(ToolStatus::Succeeded),
        "failed" | "error" => Some(ToolStatus::Failed),
        "interrupted" | "cancelled" | "canceled" | "declined" => Some(ToolStatus::Interrupted),
        _ => Some(ToolStatus::Unknown),
    }
}

fn call_id(payload: &Value) -> Option<String> {
    string_option(payload, "call_id")
        .or_else(|| string_option(payload, "id").filter(|id| id.starts_with("call")))
}

fn item_id(payload: &Value) -> Option<String> {
    string_option(payload, "id").filter(|id| !id.is_empty())
}

fn add_source_item_id(entry: &mut NormalizedEntry, payload: &Value) {
    if let Some(id) = item_id(payload) {
        entry
            .metadata
            .insert("sourceItemId".into(), Value::String(id));
    }
}

fn source_item_id(entry: &NormalizedEntry) -> Option<&str> {
    entry.metadata.get("sourceItemId").and_then(Value::as_str)
}

fn string_option(payload: &Value, key: &str) -> Option<String> {
    payload.get(key).and_then(Value::as_str).map(str::to_owned)
}

fn string_field(payload: &Value, keys: &[&str]) -> String {
    keys.iter()
        .find_map(|key| value_text(payload.get(*key)))
        .unwrap_or_default()
}

fn content_text(value: Option<&Value>) -> String {
    value_text(value).unwrap_or_default()
}

fn user_input_text(value: Option<&Value>) -> String {
    let Some(items) = value.and_then(Value::as_array) else {
        return content_text(value);
    };
    items
        .iter()
        .filter_map(|item| {
            let kind = item.get("type").and_then(Value::as_str).unwrap_or_default();
            match kind {
                "text" | "input_text" => {
                    item.get("text").and_then(Value::as_str).map(str::to_owned)
                }
                "skill" => item
                    .get("name")
                    .and_then(Value::as_str)
                    .map(|name| format!("[skill: {name}]")),
                "mention" => item
                    .get("name")
                    .and_then(Value::as_str)
                    .map(|name| format!("[mention: {name}]")),
                _ => None,
            }
        })
        .collect::<Vec<_>>()
        .join("\n")
}

fn string_array(value: Option<&Value>, separator: &str) -> String {
    match value {
        Some(Value::Array(values)) => values
            .iter()
            .filter_map(Value::as_str)
            .collect::<Vec<_>>()
            .join(separator),
        Some(Value::String(value)) => value.clone(),
        _ => String::new(),
    }
}

fn joined_fields(payload: &Value, keys: &[&str]) -> String {
    keys.iter()
        .filter_map(|key| payload.get(*key))
        .filter_map(display_value)
        .filter(|text| !text.is_empty())
        .collect::<Vec<_>>()
        .join("\n")
}

fn output_text(value: Option<&Value>) -> String {
    value.and_then(display_value).unwrap_or_default()
}

fn display_value(value: &Value) -> Option<String> {
    match value {
        Value::Null => None,
        Value::String(text) => (!is_media_data_uri(text)).then(|| text.clone()),
        Value::Number(number) => Some(number.to_string()),
        Value::Bool(value) => Some(value.to_string()),
        Value::Array(items) => {
            let typed_content = items.iter().any(|item| {
                item.get("type")
                    .and_then(Value::as_str)
                    .is_some_and(is_content_item_kind)
            });
            if typed_content {
                value_text(Some(value))
            } else if items.iter().all(Value::is_string) {
                Some(
                    items
                        .iter()
                        .filter_map(Value::as_str)
                        .collect::<Vec<_>>()
                        .join("\n"),
                )
            } else {
                pretty_sanitized(value)
            }
        }
        Value::Object(object) => {
            if let Some(content) = object
                .get("content")
                .or_else(|| object.get("content_items"))
                .or_else(|| object.get("contentItems"))
            {
                let text = output_text(Some(content));
                return (!text.is_empty()).then_some(text);
            }
            object
                .get("message")
                .and_then(Value::as_str)
                .map(str::to_owned)
                .or_else(|| pretty_sanitized(value))
        }
    }
}

fn pretty_sanitized(value: &Value) -> Option<String> {
    let sanitized = sanitize_for_display(value);
    (!sanitized.is_null())
        .then(|| serde_json::to_string_pretty(&sanitized).ok())
        .flatten()
}

fn sanitize_for_display(value: &Value) -> Value {
    match value {
        Value::Array(items) => Value::Array(items.iter().map(sanitize_for_display).collect()),
        Value::Object(object) => {
            let attachment_payload =
                object
                    .get("type")
                    .and_then(Value::as_str)
                    .is_some_and(|kind| {
                        let kind = kind.to_ascii_lowercase();
                        kind.contains("image")
                            || kind.contains("audio")
                            || kind.contains("encrypted")
                    });
            let sanitized = object
                .iter()
                .filter(|(key, _)| {
                    !(matches!(
                        key.as_str(),
                        "image_url"
                            | "imageUrl"
                            | "audio_url"
                            | "audioUrl"
                            | "encrypted_content"
                            | "encryptedContent"
                    ) || attachment_payload
                        && matches!(key.as_str(), "data" | "blob" | "base64" | "b64_json"))
                })
                .map(|(key, value)| (key.clone(), sanitize_for_display(value)))
                .collect();
            Value::Object(sanitized)
        }
        other => other.clone(),
    }
}

fn is_content_item_kind(kind: &str) -> bool {
    let kind = kind.to_ascii_lowercase();
    kind.contains("text")
        || kind.contains("image")
        || kind.contains("audio")
        || kind.contains("encrypted")
}

fn is_media_data_uri(value: &str) -> bool {
    let value = value.trim_start();
    ["data:image/", "data:audio/", "data:video/"]
        .iter()
        .any(|prefix| {
            value
                .get(..prefix.len())
                .is_some_and(|candidate| candidate.eq_ignore_ascii_case(prefix))
        })
}

fn value_text(value: Option<&Value>) -> Option<String> {
    match value? {
        Value::String(text) => Some(text.clone()),
        Value::Array(items) => {
            let parts = items
                .iter()
                .filter_map(|item| {
                    if item
                        .get("type")
                        .and_then(Value::as_str)
                        .is_some_and(|kind| {
                            let kind = kind.to_ascii_lowercase();
                            kind.contains("image")
                                || kind.contains("audio")
                                || kind.contains("encrypted")
                        })
                    {
                        None
                    } else {
                        item.get("text")
                            .and_then(Value::as_str)
                            .map(str::to_owned)
                            .or_else(|| item.as_str().map(str::to_owned))
                    }
                })
                .collect::<Vec<_>>();
            (!parts.is_empty()).then(|| parts.join("\n"))
        }
        Value::Number(number) => Some(number.to_string()),
        Value::Bool(value) => Some(value.to_string()),
        _ => None,
    }
}

fn pretty_value(value: Option<&Value>) -> String {
    value
        .filter(|value| !value.is_null())
        .and_then(|value| serde_json::to_string_pretty(value).ok())
        .unwrap_or_default()
}

fn plan_text(payload: &Value) -> String {
    payload
        .get("plan")
        .and_then(Value::as_array)
        .map(|items| {
            items
                .iter()
                .filter_map(|item| item.get("step").and_then(Value::as_str))
                .collect::<Vec<_>>()
                .join("\n")
        })
        .filter(|text| !text.is_empty())
        .unwrap_or_else(|| string_field(payload, &["message", "delta"]))
}

fn add_attachment_metadata(entry: &mut NormalizedEntry, payload: &Value) {
    let (image_count, audio_count) = attachment_counts(payload);
    add_attachment_counts(entry, image_count, audio_count);
}

fn attachment_counts(payload: &Value) -> (usize, usize) {
    let mut image_count = ["images", "local_images", "localImages"]
        .iter()
        .filter_map(|key| payload.get(*key).and_then(Value::as_array))
        .map(Vec::len)
        .sum();
    let mut audio_count = [
        "audio",
        "local_audio",
        "localAudio",
        "audios",
        "local_audios",
        "localAudios",
    ]
    .iter()
    .filter_map(|key| payload.get(*key).and_then(Value::as_array))
    .map(Vec::len)
    .sum();

    for items in ["content", "content_items", "contentItems"]
        .iter()
        .filter_map(|key| payload.get(*key).and_then(Value::as_array))
    {
        let (content_images, content_audio) = content_attachment_counts(items);
        image_count += content_images;
        audio_count += content_audio;
    }

    for nested in ["output", "result", "Ok", "ok"]
        .iter()
        .filter_map(|key| payload.get(*key))
    {
        let (nested_images, nested_audio) = if let Some(items) = nested.as_array() {
            content_attachment_counts(items)
        } else if nested.is_object() {
            attachment_counts(nested)
        } else {
            (0, 0)
        };
        image_count += nested_images;
        audio_count += nested_audio;
    }
    (image_count, audio_count)
}

fn content_attachment_counts(items: &[Value]) -> (usize, usize) {
    let mut image_count = 0;
    let mut audio_count = 0;
    for item in items {
        let kind = item
            .get("type")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_ascii_lowercase();
        if kind.contains("image") {
            image_count += 1;
        } else if kind.contains("audio") {
            audio_count += 1;
        }
    }
    (image_count, audio_count)
}

fn add_attachment_counts(entry: &mut NormalizedEntry, image_count: usize, audio_count: usize) {
    let attachment_count = image_count.saturating_add(audio_count);
    if attachment_count > 0 {
        entry
            .metadata
            .insert("attachmentCount".into(), Value::from(attachment_count));
    }
    if image_count > 0 {
        entry
            .metadata
            .insert("imageAttachmentCount".into(), Value::from(image_count));
    }
    if audio_count > 0 {
        entry
            .metadata
            .insert("audioAttachmentCount".into(), Value::from(audio_count));
    }
}

fn has_encrypted_content(payload: &Value) -> bool {
    payload
        .get("encrypted_content")
        .or_else(|| payload.get("encryptedContent"))
        .is_some_and(|value| !value.is_null())
        || ["content", "content_items", "contentItems"]
            .iter()
            .filter_map(|key| payload.get(*key).and_then(Value::as_array))
            .flatten()
            .any(|item| {
                item.get("type")
                    .and_then(Value::as_str)
                    .is_some_and(|kind| kind.to_ascii_lowercase().contains("encrypted"))
                    || item
                        .get("encrypted_content")
                        .or_else(|| item.get("encryptedContent"))
                        .is_some_and(|value| !value.is_null())
            })
}

fn truncate_graphemes(value: &str, max: usize) -> String {
    value.graphemes(true).take(max).collect()
}

fn event_title(event: &str) -> &str {
    match event {
        "task_started" | "turn_started" => "Turn started",
        "task_complete" | "turn_complete" => "Turn completed",
        "turn_aborted" => "Turn interrupted",
        "context_compacted" => "Conversation compacted",
        "thread_rolled_back" => "Conversation rolled back",
        "entered_review_mode" => "Review started",
        "exited_review_mode" => "Review completed",
        "thread_goal_updated" => "Goal updated",
        "token_count" => "Token usage",
        "session_configured" => "Session configured",
        "mcp_startup_update" => "MCP startup",
        "mcp_startup_complete" => "MCP ready",
        "exec_command_begin" | "exec_command_output_delta" | "exec_command_end" => "Command",
        "terminal_interaction" => "Terminal",
        "patch_apply_begin" | "patch_apply_updated" | "patch_apply_end" => "Patch",
        "mcp_tool_call_begin" | "mcp_tool_call_end" => "MCP tool",
        "web_search_begin" | "web_search_end" => "Web search",
        "dynamic_tool_call_request" | "dynamic_tool_call_response" => "Dynamic tool",
        "view_image_tool_call" => "Image attachment",
        "image_generation_begin" | "image_generation_end" => "Image generation",
        _ => "Activity",
    }
}

fn is_known_envelope(kind: &str) -> bool {
    matches!(
        kind,
        "session_meta"
            | "turn_context"
            | "world_state"
            | "inter_agent_communication"
            | "inter_agent_communication_metadata"
            | "event_msg"
            | "response_item"
            | "compacted"
    )
}

fn raw_ref_id(session_id: &str, offset: u64, length: u64) -> String {
    format!(
        "r_{}",
        sha256(format!("{session_id}\0{offset}\0{length}").as_bytes())
    )
}

fn sha256(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    let mut output = String::with_capacity(digest.len() * 2);
    for byte in digest {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}

fn hex_preview(bytes: &[u8]) -> String {
    let mut output = String::with_capacity(bytes.len().min(4096) * 2);
    for byte in bytes.iter().take(4096) {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}
