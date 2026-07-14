use std::collections::BTreeMap;

use serde::{Deserialize, Serialize};
use ts_rs::TS;

macro_rules! contract_enum {
    ($name:ident { $($variant:ident),+ $(,)? }) => {
        #[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize, TS)]
        #[serde(rename_all = "camelCase")]
        #[ts(rename_all = "camelCase")]
        pub enum $name {
            $($variant),+
        }
    };
}

contract_enum!(SourceKind {
    Cli,
    Vscode,
    Exec,
    Review,
    Subagent,
    AppServer,
    Unknown,
});
contract_enum!(SessionParentRelation {
    Parent,
    Fork,
    PlanHandoff,
});
contract_enum!(IndexState {
    Pending,
    Indexing,
    Ready,
    Partial,
    Error,
});
contract_enum!(Completeness {
    Live,
    Complete,
    Partial,
    Unsupported,
});
contract_enum!(EntryKind {
    Message,
    Reasoning,
    Tool,
    Plan,
    Context,
    Marker,
    Warning,
    Error,
    Unknown,
});
contract_enum!(EntryPresentation {
    User,
    Response,
    Technical,
    Internal,
});
contract_enum!(MessageRole {
    User,
    Assistant,
    Developer,
    System,
});
contract_enum!(Phase {
    Commentary,
    Final,
    Analysis,
    Unknown,
});
contract_enum!(ToolKind {
    Command,
    Patch,
    Mcp,
    WebSearch,
    Function,
    Dynamic,
    Terminal,
    ViewImage,
    Other,
});
contract_enum!(ToolStatus {
    Pending,
    Running,
    Succeeded,
    Failed,
    Interrupted,
    Unknown,
});
contract_enum!(ServicePhase {
    Starting,
    Discovering,
    Indexing,
    Ready,
    Degraded,
    ShuttingDown,
});
contract_enum!(DiagnosticSeverity {
    Info,
    Warning,
    Error
});
contract_enum!(ContentField { Primary, Secondary });
contract_enum!(SearchField {
    SessionTitle,
    EntryTitle,
    Primary,
    Secondary,
});
contract_enum!(RawEncoding { Utf8, Binary });
contract_enum!(RawParseStatus {
    Valid,
    InvalidJson,
    InvalidUtf8,
    Oversize,
    IncompleteTail,
    Unknown,
});
contract_enum!(SseEventType {
    IndexProgress,
    SessionUpdated,
    EntryUpdated,
    Diagnostic,
    Resync,
    Heartbeat,
});

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct IndexProgress {
    #[ts(type = "number")]
    pub total_files: u64,
    #[ts(type = "number")]
    pub processed_files: u64,
    #[ts(type = "number")]
    pub total_bytes: u64,
    #[ts(type = "number")]
    pub processed_bytes: u64,
    #[ts(type = "number")]
    pub failed_files: u64,
    #[ts(type = "number")]
    pub excluded_files: u64,
    #[ts(type = "number")]
    pub excluded_bytes: u64,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct Status {
    pub app_version: String,
    pub source_home: String,
    pub cache_dir: String,
    #[ts(type = "number")]
    pub initial_index_days: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub initial_index_cutoff: Option<String>,
    #[ts(type = "number")]
    pub generation: u64,
    pub phase: ServicePhase,
    pub progress: IndexProgress,
    pub fts_ready: bool,
    #[ts(type = "number")]
    pub database_bytes: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub last_reconcile_at: Option<String>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct GitMetadata {
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub branch: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub commit: Option<String>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct SessionSummary {
    pub id: String,
    pub source: SourceKind,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub parent_thread_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub parent_relation: Option<SessionParentRelation>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub cwd: Option<String>,
    pub title: String,
    pub preview: String,
    pub created_at: String,
    pub updated_at: String,
    pub archived: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub cli_version: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub provider: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub git: Option<GitMetadata>,
    #[ts(type = "number")]
    pub entry_count: u64,
    #[ts(type = "number")]
    pub diagnostic_count: u64,
    pub index_state: IndexState,
    pub completeness: Completeness,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct SessionTreeNode {
    pub session: SessionSummary,
    pub children: Vec<SessionTreeNode>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct SessionGroup {
    pub root: SessionTreeNode,
    pub latest_session_id: String,
    pub updated_at: String,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct Diagnostic {
    #[ts(type = "number")]
    pub id: i64,
    pub severity: DiagnosticSeverity,
    pub code: String,
    pub message: String,
    pub first_seen_at: String,
    pub last_seen_at: String,
    #[ts(type = "number")]
    pub count: u64,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct SessionDetail {
    pub summary: SessionSummary,
    pub diagnostics: Vec<Diagnostic>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct EntryListItem {
    pub id: String,
    pub session_id: String,
    #[ts(type = "number")]
    pub sequence: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub timestamp: Option<String>,
    pub kind: EntryKind,
    pub presentation: EntryPresentation,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub role: Option<MessageRole>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub phase: Option<Phase>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub tool_kind: Option<ToolKind>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub tool_status: Option<ToolStatus>,
    pub title: String,
    pub primary_preview: String,
    pub secondary_preview: String,
    #[ts(type = "number")]
    pub primary_bytes: u64,
    #[ts(type = "number")]
    pub secondary_bytes: u64,
    pub primary_complete: bool,
    pub secondary_complete: bool,
    pub default_collapsed: bool,
    #[ts(type = "Record<string, unknown>")]
    pub metadata: BTreeMap<String, serde_json::Value>,
    #[ts(type = "number")]
    pub raw_ref_count: u64,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct RawRefSummary {
    pub id: String,
    #[ts(type = "number")]
    pub line: u64,
    #[ts(type = "number")]
    pub byte_offset: u64,
    #[ts(type = "number")]
    pub byte_length: u64,
    pub envelope_type: String,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct TranscriptEntry {
    pub item: EntryListItem,
    #[ts(type = "Record<string, unknown>")]
    pub derived_metadata: BTreeMap<String, serde_json::Value>,
    pub raw_refs: Vec<RawRefSummary>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct ContentChunk {
    pub field: ContentField,
    pub text: String,
    #[ts(type = "number")]
    pub byte_offset: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional, type = "number")]
    pub next_offset: Option<u64>,
    #[ts(type = "number")]
    pub total_bytes: u64,
    pub complete: bool,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct RawRecordSummary {
    pub id: String,
    pub session_id: String,
    #[ts(type = "number")]
    pub line: u64,
    #[ts(type = "number")]
    pub byte_offset: u64,
    #[ts(type = "number")]
    pub byte_length: u64,
    pub envelope_type: String,
    pub parse_status: RawParseStatus,
    pub encoding: RawEncoding,
    pub oversize: bool,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct RawRecord {
    pub summary: RawRecordSummary,
    pub chunk: ContentChunk,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct MatchRange {
    pub start: u32,
    pub end: u32,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct SearchHit {
    pub session: SessionSummary,
    pub entry_id: String,
    pub kind: EntryKind,
    pub snippet: String,
    pub match_ranges: Vec<MatchRange>,
    pub field: SearchField,
    pub rank: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub timestamp: Option<String>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct ApiPage<T> {
    pub data: Vec<T>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub next_cursor: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub previous_cursor: Option<String>,
    pub partial: bool,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct ApiError {
    pub code: String,
    pub message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    #[ts(type = "Record<string, string>")]
    pub details: Option<BTreeMap<String, String>>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
pub struct ApiErrorEnvelope {
    pub error: ApiError,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct SseEventPayload {
    #[ts(type = "number")]
    pub generation: u64,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub phase: Option<ServicePhase>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub session_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub entry_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub progress: Option<IndexProgress>,
    #[serde(skip_serializing_if = "Option::is_none")]
    #[ts(optional)]
    pub diagnostic: Option<Diagnostic>,
}

#[derive(Clone, Debug, Deserialize, PartialEq, Serialize, TS)]
#[serde(rename_all = "camelCase")]
#[ts(rename_all = "camelCase")]
pub struct SseEvent {
    #[ts(type = "number")]
    pub id: u64,
    pub event: SseEventType,
    pub data: SseEventPayload,
}

#[must_use]
pub fn typescript_contract() -> String {
    let declarations = [
        SourceKind::decl(),
        SessionParentRelation::decl(),
        IndexState::decl(),
        Completeness::decl(),
        EntryKind::decl(),
        EntryPresentation::decl(),
        MessageRole::decl(),
        Phase::decl(),
        ToolKind::decl(),
        ToolStatus::decl(),
        ServicePhase::decl(),
        DiagnosticSeverity::decl(),
        ContentField::decl(),
        SearchField::decl(),
        RawEncoding::decl(),
        RawParseStatus::decl(),
        SseEventType::decl(),
        IndexProgress::decl(),
        Status::decl(),
        GitMetadata::decl(),
        SessionSummary::decl(),
        SessionTreeNode::decl(),
        SessionGroup::decl(),
        Diagnostic::decl(),
        SessionDetail::decl(),
        EntryListItem::decl(),
        RawRefSummary::decl(),
        TranscriptEntry::decl(),
        ContentChunk::decl(),
        RawRecordSummary::decl(),
        RawRecord::decl(),
        MatchRange::decl(),
        SearchHit::decl(),
        ApiPage::<SessionSummary>::decl(),
        ApiError::decl(),
        ApiErrorEnvelope::decl(),
        SseEventPayload::decl(),
        SseEvent::decl(),
    ];

    let mut output = String::from(
        "// @generated by `cargo run --bin export_types -- --write`; do not edit.\n\n",
    );
    for declaration in declarations {
        output.push_str("export ");
        output.push_str(&declaration);
        output.push_str("\n\n");
    }
    let _ = output.pop();
    output
}
