use agents_viewer::model::{
    Completeness, ContentField, ContentFreshness, ContentStatus, DiagnosticSeverity, EntryKind,
    IndexState, LiveSyncState, MessageRole, Phase, RawEncoding, RawParseStatus, ServicePhase,
    SessionFreshness, SessionParentRelation, SessionSummary, SourceKind, SourceLocation,
    SourceRootKind, SseEventType, ToolKind, ToolStatus, typescript_contract,
};
use pretty_assertions::assert_eq;
use std::process::Command;

#[test]
fn session_summary_round_trips_and_omits_absent_optional_fields() {
    let summary = SessionSummary {
        id: "s_fixture".into(),
        source: SourceKind::AppServer,
        source_location: SourceLocation {
            root_kind: SourceRootKind::Active,
            relative_path: "2026/01/02/s_fixture.jsonl".into(),
        },
        first_user_message: None,
        content_status: ContentStatus {
            freshness: ContentFreshness::Current,
            live_state: LiveSyncState::Inactive,
            has_snapshot: true,
            snapshot_revision: 1,
            synced_through_bytes: 100,
            observed_bytes: 100,
            last_synced_at: None,
        },
        parent_thread_id: None,
        parent_relation: None,
        cwd: Some("/synthetic/workspace".into()),
        title: "Synthetic session".into(),
        preview: "Fixture preview".into(),
        created_at: "2026-01-02T03:04:05.123456Z".into(),
        updated_at: "2026-01-02T03:05:06Z".into(),
        archived: false,
        cli_version: Some("0.144.1".into()),
        provider: None,
        git: None,
        entry_count: 3,
        diagnostic_count: 0,
        index_state: IndexState::Ready,
        completeness: Completeness::Complete,
        freshness: SessionFreshness::Current,
    };

    let json = serde_json::to_value(&summary).expect("summary serializes");
    assert_eq!(json["source"], "appServer");
    assert_eq!(json["indexState"], "ready");
    assert_eq!(json["createdAt"], "2026-01-02T03:04:05.123456Z");
    assert!(json.get("parentThreadId").is_none());
    assert!(json.get("provider").is_none());
    assert_eq!(
        serde_json::from_value::<SessionSummary>(json).expect("summary deserializes"),
        summary
    );
}

#[test]
fn fixed_enums_use_contract_json_values() {
    macro_rules! assert_values {
        ($($value:expr => $expected:literal),+ $(,)?) => {
            $(assert_eq!(serde_json::to_value($value).unwrap(), $expected);)+
        };
    }

    assert_values!(
        SourceKind::Cli => "cli",
        SourceKind::Vscode => "vscode",
        SourceKind::Exec => "exec",
        SourceKind::Review => "review",
        SourceKind::Subagent => "subagent",
        SourceKind::AppServer => "appServer",
        SourceKind::GuardianReview => "guardianReview",
        SourceKind::Unknown => "unknown",
        SessionParentRelation::Parent => "parent",
        SessionParentRelation::Fork => "fork",
        SessionParentRelation::PlanHandoff => "planHandoff",
        IndexState::Pending => "pending",
        IndexState::Indexing => "indexing",
        IndexState::Ready => "ready",
        IndexState::Partial => "partial",
        IndexState::Error => "error",
        Completeness::Live => "live",
        Completeness::Complete => "complete",
        Completeness::Partial => "partial",
        Completeness::Unsupported => "unsupported",
        EntryKind::Message => "message",
        EntryKind::Reasoning => "reasoning",
        EntryKind::Tool => "tool",
        EntryKind::Plan => "plan",
        EntryKind::Context => "context",
        EntryKind::Marker => "marker",
        EntryKind::Warning => "warning",
        EntryKind::Error => "error",
        EntryKind::Unknown => "unknown",
        MessageRole::User => "user",
        MessageRole::Assistant => "assistant",
        MessageRole::Developer => "developer",
        MessageRole::System => "system",
        Phase::Commentary => "commentary",
        Phase::Final => "final",
        Phase::Analysis => "analysis",
        Phase::Unknown => "unknown",
        ToolKind::Command => "command",
        ToolKind::Patch => "patch",
        ToolKind::Mcp => "mcp",
        ToolKind::WebSearch => "webSearch",
        ToolKind::RequestUserInput => "requestUserInput",
        ToolKind::Function => "function",
        ToolKind::Dynamic => "dynamic",
        ToolKind::Terminal => "terminal",
        ToolKind::ViewImage => "viewImage",
        ToolKind::Other => "other",
        ToolStatus::Pending => "pending",
        ToolStatus::Running => "running",
        ToolStatus::Succeeded => "succeeded",
        ToolStatus::Failed => "failed",
        ToolStatus::Interrupted => "interrupted",
        ToolStatus::Unknown => "unknown",
        ServicePhase::Starting => "starting",
        ServicePhase::Discovering => "discovering",
        ServicePhase::Indexing => "indexing",
        ServicePhase::Ready => "ready",
        ServicePhase::Degraded => "degraded",
        ServicePhase::ShuttingDown => "shuttingDown",
        DiagnosticSeverity::Info => "info",
        DiagnosticSeverity::Warning => "warning",
        DiagnosticSeverity::Error => "error",
        ContentField::Primary => "primary",
        ContentField::Secondary => "secondary",
        RawEncoding::Utf8 => "utf8",
        RawEncoding::Binary => "binary",
        RawParseStatus::Valid => "valid",
        RawParseStatus::Inherited => "inherited",
        RawParseStatus::InvalidJson => "invalidJson",
        RawParseStatus::InvalidUtf8 => "invalidUtf8",
        RawParseStatus::Oversize => "oversize",
        RawParseStatus::IncompleteTail => "incompleteTail",
        RawParseStatus::Unknown => "unknown",
        SseEventType::CatalogProgress => "catalogProgress",
        SseEventType::CatalogUpdated => "catalogUpdated",
        SseEventType::SnapshotUpdated => "snapshotUpdated",
        SseEventType::LiveSyncStateChanged => "liveSyncStateChanged",
        SseEventType::Diagnostic => "diagnostic",
        SseEventType::Resync => "resync",
        SseEventType::Heartbeat => "heartbeat",
    );
}

#[test]
fn generated_contract_is_deterministic_and_machine_independent() {
    let first = typescript_contract();
    let second = typescript_contract();
    assert_eq!(first, second);
    assert!(!first.contains(env!("CARGO_MANIFEST_DIR")));
    assert!(!first.contains("/home/"));
    assert!(!first.contains("generated at"));
    assert!(first.contains("export type SourceKind"));
    assert!(first.contains("parentThreadId?: string"));
    assert!(!first.contains("bigint"));
}

#[test]
fn exporter_requires_an_explicit_output_path() {
    let result = Command::new(env!("CARGO_BIN_EXE_export_types"))
        .arg("--check")
        .output()
        .expect("run export_types");

    assert_eq!(result.status.code(), Some(2));
    let stderr = String::from_utf8(result.stderr).expect("UTF-8 stderr");
    assert!(
        stderr.contains("--output <PATH>"),
        "unexpected stderr: {stderr}"
    );
}

#[test]
fn exporter_rejects_a_mismatched_explicit_target() {
    let temporary = tempfile::tempdir().expect("temporary directory");
    let output = temporary.path().join("wrong-api.ts");
    std::fs::write(&output, "stale contract\n").expect("write stale contract");

    let result = Command::new(env!("CARGO_BIN_EXE_export_types"))
        .args(["--check", "--output"])
        .arg(&output)
        .output()
        .expect("run export_types");

    assert_eq!(result.status.code(), Some(1));
    let stderr = String::from_utf8(result.stderr).expect("UTF-8 stderr");
    assert!(
        stderr.contains(output.to_string_lossy().as_ref()),
        "unexpected stderr: {stderr}"
    );
}
