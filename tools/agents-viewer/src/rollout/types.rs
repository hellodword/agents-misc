use std::collections::{BTreeMap, HashMap};

use serde_json::Value;

use crate::model::{
    Completeness, DiagnosticSeverity, EntryKind, EntryPresentation, IndexState, MessageRole, Phase,
    SessionParentRelation, SourceKind, ToolKind, ToolStatus,
};

pub const PARSER_VERSION: u32 = 2;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RootKind {
    Active,
    Archived,
}

#[derive(Clone, Debug)]
pub struct ParseContext {
    pub root_kind: RootKind,
    pub relative_path: String,
    pub file_name: String,
    pub modified_at_micros: i64,
    pub now_micros: i64,
    pub max_event_bytes: usize,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EntryOrigin {
    EventPresentation,
    ResponseItem,
    Derived,
}

#[derive(Clone, Debug, PartialEq)]
pub struct RawRecord {
    pub id: String,
    pub line_no: u64,
    pub byte_offset: u64,
    pub byte_length: u64,
    pub envelope_type: String,
    pub parse_status: String,
    pub content_hash: String,
    pub utf8: bool,
    pub oversize: bool,
    pub hex_preview: Option<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct ParserDiagnostic {
    pub severity: DiagnosticSeverity,
    pub code: String,
    pub message: String,
    pub line_no: Option<u64>,
    pub raw_ref_id: Option<String>,
}

#[derive(Clone, Debug, PartialEq)]
pub struct NormalizedEntry {
    pub id: String,
    pub session_id: String,
    pub sequence: i64,
    pub timestamp_micros: Option<i64>,
    pub kind: EntryKind,
    pub presentation: EntryPresentation,
    pub role: Option<MessageRole>,
    pub phase: Option<Phase>,
    pub tool_kind: Option<ToolKind>,
    pub tool_status: Option<ToolStatus>,
    pub title: String,
    pub primary_text: String,
    pub secondary_text: String,
    pub metadata: BTreeMap<String, Value>,
    pub call_id: Option<String>,
    pub parent_entry_id: Option<String>,
    pub default_collapsed: bool,
    pub searchable: bool,
    pub raw_refs: Vec<String>,
    pub origin: EntryOrigin,
    pub(crate) id_basis: String,
}

#[derive(Clone, Debug)]
pub(crate) struct ParseSeed {
    pub session: SessionRecord,
    pub next_sequence: i64,
    pub occurrences: HashMap<String, u64>,
    pub recent: Vec<(u64, NormalizedEntry)>,
    pub raw_record_count: u64,
    pub recognized_record_count: u64,
    pub checkpoint_offset: u64,
    pub checkpoint_line: u64,
    pub partial: bool,
}

#[derive(Clone, Debug, PartialEq)]
pub struct SessionRecord {
    pub id: String,
    pub source: SourceKind,
    pub parent_thread_id: Option<String>,
    pub parent_relation: Option<SessionParentRelation>,
    pub proposed_plan_hash: Option<String>,
    pub proposed_plan_at_micros: Option<i64>,
    pub handoff_plan_hash: Option<String>,
    pub handoff_at_micros: Option<i64>,
    pub cwd: Option<String>,
    pub title: String,
    pub preview: String,
    pub created_at_micros: i64,
    pub updated_at_micros: i64,
    pub archived: bool,
    pub cli_version: Option<String>,
    pub provider: Option<String>,
    pub history_line: Option<u64>,
    pub git_branch: Option<String>,
    pub git_commit: Option<String>,
    pub entry_count: u64,
    pub index_state: IndexState,
    pub completeness: Completeness,
    pub diagnostic_count: u64,
}

#[derive(Clone, Debug, PartialEq)]
pub enum ParserOutput {
    Raw(RawRecord),
    EntryUpsert(NormalizedEntry),
    Diagnostic(ParserDiagnostic),
}

#[derive(Clone, Debug, PartialEq)]
pub struct ParseSummary {
    pub session: SessionRecord,
    pub raw_record_count: u64,
    pub recognized_record_count: u64,
    pub incomplete_tail: bool,
    pub stable_prefix_bytes: u64,
    pub stable_prefix_hash: String,
}

#[derive(Clone, Debug, PartialEq)]
pub struct ParsedRollout {
    pub summary: ParseSummary,
    pub raw_records: Vec<RawRecord>,
    pub entries: Vec<NormalizedEntry>,
    pub diagnostics: Vec<ParserDiagnostic>,
}
