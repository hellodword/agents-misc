use std::io::{BufReader, Cursor, Write};

use agents_viewer::model::{
    Completeness, EntryKind, EntryPresentation, MessageRole, SessionParentRelation, SourceKind,
    ToolKind, ToolStatus,
};
use agents_viewer::rollout::{
    CollectingSink, ParseContext, RootKind, checkpoint_for_file, parse_rollout, verify_checkpoint,
};
use pretty_assertions::assert_eq;
use tempfile::NamedTempFile;

const V120: &[u8] = include_bytes!("fixtures/rollouts/v0_120.jsonl");
const V144: &[u8] = include_bytes!("fixtures/rollouts/v0_144.jsonl");
const V145: &[u8] = include_bytes!("fixtures/rollouts/v0_145.jsonl");
const V145_SUBAGENT: &[u8] = include_bytes!("fixtures/rollouts/v0_145_subagent.jsonl");
const DEDUP: &[u8] = include_bytes!("fixtures/rollouts/dedup.jsonl");
const MALFORMED: &[u8] = include_bytes!("fixtures/rollouts/malformed.jsonl");
const REVIEW: &[u8] = include_bytes!("fixtures/rollouts/subagent_review.jsonl");
const REQUEST_USER_INPUT: &[u8] = include_bytes!("fixtures/rollouts/request_user_input.jsonl");

fn context(file_name: &str, max_event_bytes: usize) -> ParseContext {
    ParseContext {
        root_kind: RootKind::Active,
        relative_path: format!("2026/01/01/{file_name}"),
        file_name: file_name.into(),
        modified_at_micros: 1_767_225_600_000_000,
        now_micros: 1_767_225_700_000_000,
        max_event_bytes,
    }
}

fn parse(
    bytes: &[u8],
    file_name: &str,
    max_event_bytes: usize,
) -> agents_viewer::rollout::ParsedRollout {
    let mut sink = CollectingSink::default();
    let summary = parse_rollout(
        BufReader::new(Cursor::new(bytes)),
        &context(file_name, max_event_bytes),
        &mut sink,
    )
    .expect("fixture parses");
    sink.finish(summary)
}

#[test]
fn parses_v120_with_stable_ids_and_merged_tool_lifecycle() {
    let first = parse(
        V120,
        "rollout-2025-01-02T03-04-05-11111111-1111-4111-8111-111111111111.jsonl",
        1024 * 1024,
    );
    let second = parse(
        V120,
        "rollout-2025-01-02T03-04-05-11111111-1111-4111-8111-111111111111.jsonl",
        1024 * 1024,
    );

    assert_eq!(first, second);
    assert_eq!(first.summary.session.source, SourceKind::Cli);
    assert_eq!(
        first.summary.session.cli_version.as_deref(),
        Some("0.120.0")
    );
    assert_eq!(first.summary.session.title, "Inspect synthetic fixture");
    let tools = first
        .entries
        .iter()
        .filter(|entry| entry.kind == EntryKind::Tool)
        .collect::<Vec<_>>();
    assert_eq!(tools.len(), 1);
    assert_eq!(tools[0].tool_status, Some(ToolStatus::Succeeded));
    assert_eq!(tools[0].raw_refs.len(), 3);
    assert!(tools[0].secondary_text.contains("synthetic output"));
}

#[test]
fn parses_v144_without_exposing_raw_or_encrypted_reasoning() {
    let parsed = parse(
        V144,
        "rollout-2026-07-01T10-00-00-22222222-2222-4222-8222-222222222222.jsonl",
        1024 * 1024,
    );

    assert_eq!(parsed.summary.session.source, SourceKind::Vscode);
    assert_eq!(
        parsed.summary.session.git_branch.as_deref(),
        Some("fixture")
    );
    let reasoning = parsed
        .entries
        .iter()
        .find(|entry| entry.kind == EntryKind::Reasoning)
        .expect("reasoning summary");
    assert_eq!(reasoning.primary_text, "Use bounded parsing");
    assert!(!reasoning.primary_text.contains("raw reasoning"));
    assert!(!reasoning.primary_text.contains("opaque"));
    let context = parsed
        .entries
        .iter()
        .find(|entry| entry.kind == EntryKind::Context)
        .expect("world state context");
    assert!(context.default_collapsed);
    assert!(!context.searchable);
}

#[test]
fn parses_v145_durable_items_without_losing_types_or_attachment_safety() {
    let parsed = parse(
        V145,
        "rollout-2026-07-20T12-00-00-14514514-5145-4145-8145-145145145145.jsonl",
        1024 * 1024,
    );

    assert_eq!(parsed.summary.session.source, SourceKind::Cli);
    assert_eq!(
        parsed.summary.session.cli_version.as_deref(),
        Some("0.145.0")
    );
    assert_eq!(parsed.summary.recognized_record_count, 29);
    assert!(parsed.diagnostics.is_empty());
    assert_ne!(parsed.summary.session.completeness, Completeness::Partial);

    let user = parsed
        .entries
        .iter()
        .find(|entry| {
            entry
                .metadata
                .get("sourceItemId")
                .and_then(serde_json::Value::as_str)
                == Some("item-user-145")
        })
        .expect("0.145 user item");
    assert_eq!(user.raw_refs.len(), 2);
    assert!(user.primary_text.contains("[skill: fixture-skill]"));
    assert!(user.primary_text.contains("[mention: fixture-app]"));
    assert_eq!(user.metadata["attachmentCount"], 2);
    assert_eq!(user.metadata["imageAttachmentCount"], 1);
    assert_eq!(user.metadata["audioAttachmentCount"], 1);
    assert_eq!(user.metadata["turnId"], "turn-145");

    let expected_item_ids = [
        "item-hook-145",
        "item-agent-145",
        "item-plan-145",
        "item-reasoning-145",
        "item-command-145",
        "item-dynamic-145",
        "item-collab-145",
        "item-subagent-145",
        "item-web-145",
        "item-image-view-145",
        "item-sleep-145",
        "item-extension-web-145",
        "item-extension-image-145",
        "item-hosted-image-145",
        "item-review-enter-145",
        "item-review-exit-145",
        "item-file-145",
        "item-mcp-145",
        "item-compaction-145",
    ];
    for expected in expected_item_ids {
        assert!(
            parsed.entries.iter().any(|entry| {
                entry
                    .metadata
                    .get("sourceItemId")
                    .and_then(serde_json::Value::as_str)
                    == Some(expected)
            }),
            "missing normalized 0.145 item {expected}"
        );
    }

    let reasoning = parsed
        .entries
        .iter()
        .find(|entry| {
            entry
                .metadata
                .get("sourceItemId")
                .and_then(serde_json::Value::as_str)
                == Some("item-reasoning-145")
        })
        .expect("0.145 reasoning item");
    assert_eq!(reasoning.primary_text, "Use the 0.145 schema");

    let dynamic = parsed
        .entries
        .iter()
        .find(|entry| {
            entry
                .metadata
                .get("sourceItemId")
                .and_then(serde_json::Value::as_str)
                == Some("item-dynamic-145")
        })
        .expect("0.145 dynamic tool item");
    assert_eq!(dynamic.secondary_text, "dynamic tool text");
    assert_eq!(dynamic.metadata["attachmentCount"], 2);
    assert_eq!(dynamic.metadata["imageAttachmentCount"], 1);
    assert_eq!(dynamic.metadata["audioAttachmentCount"], 1);

    let web_search = parsed
        .entries
        .iter()
        .find(|entry| {
            entry
                .metadata
                .get("sourceItemId")
                .and_then(serde_json::Value::as_str)
                == Some("item-web-145")
        })
        .expect("0.145 web search item");
    assert!(
        web_search
            .secondary_text
            .contains("Structured result survives")
    );
    assert!(
        web_search
            .secondary_text
            .contains("https://invalid.example/result")
    );

    let structured_output = parsed
        .entries
        .iter()
        .find(|entry| entry.call_id.as_deref() == Some("call-output-145"))
        .expect("structured tool output");
    assert_eq!(structured_output.secondary_text, "structured output text");
    assert_eq!(structured_output.metadata["attachmentCount"], 2);
    assert_eq!(structured_output.metadata["imageAttachmentCount"], 1);
    assert_eq!(structured_output.metadata["audioAttachmentCount"], 1);

    let delivered = parsed
        .entries
        .iter()
        .find(|entry| {
            entry
                .metadata
                .get("sourceItemId")
                .and_then(serde_json::Value::as_str)
                == Some("amsg-delivery-145")
        })
        .expect("inter-agent delivery");
    assert_eq!(delivered.raw_refs.len(), 2);
    assert_eq!(delivered.metadata["triggerTurn"], true);
    assert_eq!(delivered.primary_text, "Follow-up synthetic message");

    let encrypted = parsed
        .entries
        .iter()
        .find(|entry| {
            entry
                .metadata
                .get("sourceItemId")
                .and_then(serde_json::Value::as_str)
                == Some("amsg-encrypted-145")
        })
        .expect("encrypted inter-agent message");
    assert_eq!(encrypted.primary_text, "Encrypted inter-agent message");
    assert!(!encrypted.searchable);

    assert!(parsed.entries.iter().any(|entry| {
        entry.kind == EntryKind::Context && entry.title == "Thread settings applied"
    }));
    let rendered = parsed
        .entries
        .iter()
        .flat_map(|entry| [&entry.primary_text, &entry.secondary_text])
        .cloned()
        .collect::<Vec<_>>()
        .join("\n");
    for forbidden in [
        "must-not-render",
        "raw-reasoning",
        "ciphertext",
        "extension-base64",
        "hosted-base64",
    ] {
        assert!(!rendered.contains(forbidden), "rendered {forbidden}");
    }
}

#[test]
fn preserves_v145_legacy_audio_and_search_results_without_exposing_payloads() {
    let bytes = br##"{"timestamp":"2026-07-20T12:30:00Z","type":"session_meta","payload":{"id":"24524524-5245-4245-8245-245245245245","history_mode":"legacy"}}
{"timestamp":"2026-07-20T12:30:01Z","type":"event_msg","payload":{"type":"user_message","message":"Audio prompt","audio":["data:audio/wav;base64,remote-must-not-render"],"local_audio":["/synthetic/local-must-not-render.wav"]}}
{"timestamp":"2026-07-20T12:30:02Z","type":"event_msg","payload":{"type":"web_search_end","call_id":"legacy-search-145","query":"legacy synthetic query","action":{"type":"search","query":"legacy synthetic query"},"results":[{"title":"Legacy structured result","url":"https://invalid.example/legacy-result"}]}}
{"timestamp":"2026-07-20T12:30:03Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"legacy-mcp-145","invocation":{"server":"legacy-server","tool":"legacy-tool","arguments":{}},"duration":{"secs":0,"nanos":1},"result":{"Ok":{"content":[{"type":"text","text":"legacy MCP text"},{"type":"audio","data":"legacy-audio-must-not-render","mimeType":"audio/wav"}]}}}}
"##;
    let parsed = parse(
        bytes,
        "rollout-2026-07-20T12-30-00-24524524-5245-4245-8245-245245245245.jsonl",
        1024 * 1024,
    );

    let user = parsed
        .entries
        .iter()
        .find(|entry| entry.role == Some(MessageRole::User))
        .expect("legacy 0.145 user message");
    assert_eq!(user.metadata["attachmentCount"], 2);
    assert_eq!(user.metadata["audioAttachmentCount"], 2);
    assert_eq!(user.primary_text, "Audio prompt");
    assert!(!user.primary_text.contains("must-not-render"));

    let search = parsed
        .entries
        .iter()
        .find(|entry| entry.call_id.as_deref() == Some("legacy-search-145"))
        .expect("legacy 0.145 web search");
    assert!(search.secondary_text.contains("Legacy structured result"));

    let mcp = parsed
        .entries
        .iter()
        .find(|entry| entry.call_id.as_deref() == Some("legacy-mcp-145"))
        .expect("legacy 0.145 MCP result");
    assert!(mcp.secondary_text.contains("legacy MCP text"));
    assert_eq!(mcp.metadata["attachmentCount"], 1);
    assert_eq!(mcp.metadata["audioAttachmentCount"], 1);
    assert!(!mcp.secondary_text.contains("must-not-render"));
}

#[test]
fn excludes_v145_inherited_subagent_prefix_by_ordinal_even_with_gaps() {
    let parsed = parse(
        V145_SUBAGENT,
        "rollout-2026-07-20T13-00-00-34534534-5345-4345-8345-345345345345.jsonl",
        1024 * 1024,
    );

    assert_eq!(parsed.summary.session.source, SourceKind::Subagent);
    assert_eq!(
        parsed.summary.session.parent_thread_id.as_deref(),
        Some("14514514-5145-4145-8145-145145145145")
    );
    assert_eq!(parsed.summary.recognized_record_count, 6);
    assert!(parsed.diagnostics.is_empty());
    assert_eq!(
        parsed
            .raw_records
            .iter()
            .map(|raw| raw.parse_status.as_str())
            .collect::<Vec<_>>(),
        vec![
            "valid",
            "inherited",
            "inherited",
            "inherited",
            "valid",
            "valid"
        ]
    );
    assert!(
        parsed
            .entries
            .iter()
            .all(|entry| !entry.primary_text.contains("Inherited"))
    );
    assert!(
        parsed
            .entries
            .iter()
            .any(|entry| entry.primary_text == "Child-owned prompt")
    );
    assert!(
        parsed
            .entries
            .iter()
            .any(|entry| entry.primary_text == "Child-owned response")
    );
    assert_eq!(parsed.summary.session.title, "Child-owned prompt");
}

#[test]
fn non_null_history_base_is_explicitly_partial() {
    let bytes = br##"{"timestamp":"2026-07-20T14:00:00Z","ordinal":0,"type":"session_meta","payload":{"id":"44544544-5445-4445-8445-445445445445","history_mode":"paginated","history_base":{"thread_id":"14514514-5145-4145-8145-145145145145","end_ordinal_exclusive":5,"end_byte_offset":1024}}}
"##;
    let parsed = parse(
        bytes,
        "rollout-2026-07-20T14-00-00-44544544-5445-4445-8445-445445445445.jsonl",
        1024 * 1024,
    );

    assert_eq!(parsed.summary.session.completeness, Completeness::Partial);
    assert!(
        parsed
            .diagnostics
            .iter()
            .any(|diagnostic| { diagnostic.code == "unsupported_history_base" })
    );
}

#[test]
fn unknown_v145_extension_degrades_without_hiding_raw_record() {
    let bytes = br##"{"timestamp":"2026-07-20T15:00:00Z","ordinal":0,"type":"session_meta","payload":{"id":"54554554-5545-4545-8545-545545545545","history_mode":"paginated","history_base":null}}
{"timestamp":"2026-07-20T15:00:01Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"54554554-5545-4545-8545-545545545545","turn_id":"future-turn","completed_at_ms":0,"item":{"type":"Extension","kind":"future.widget","id":"future-item","future":"value"}}}
"##;
    let parsed = parse(
        bytes,
        "rollout-2026-07-20T15-00-00-54554554-5545-4545-8545-545545545545.jsonl",
        1024 * 1024,
    );

    assert_eq!(parsed.raw_records.len(), 2);
    assert_eq!(parsed.summary.session.completeness, Completeness::Partial);
    assert!(
        parsed
            .entries
            .iter()
            .any(|entry| entry.kind == EntryKind::Unknown)
    );
    assert!(
        parsed
            .diagnostics
            .iter()
            .any(|diagnostic| { diagnostic.code == "unknown_extension_item" })
    );
}

#[test]
fn empty_response_item_ids_do_not_merge_unrelated_entries() {
    let bytes = br##"{"timestamp":"2026-07-20T16:00:00Z","type":"session_meta","payload":{"id":"64564564-5645-4645-8645-645645645645"}}
{"timestamp":"2026-07-20T16:00:01Z","type":"response_item","payload":{"type":"reasoning","id":"","summary":[{"type":"summary_text","text":"First independent summary"}],"encrypted_content":null}}
{"timestamp":"2026-07-20T16:00:02Z","type":"response_item","payload":{"type":"reasoning","id":"","summary":[{"type":"summary_text","text":"Second independent summary"}],"encrypted_content":null}}
"##;
    let parsed = parse(
        bytes,
        "rollout-2026-07-20T16-00-00-64564564-5645-4645-8645-645645645645.jsonl",
        1024 * 1024,
    );

    let reasoning = parsed
        .entries
        .iter()
        .filter(|entry| entry.kind == EntryKind::Reasoning)
        .collect::<Vec<_>>();
    assert_eq!(reasoning.len(), 2);
    assert!(
        reasoning
            .iter()
            .all(|entry| !entry.metadata.contains_key("sourceItemId"))
    );
}

#[test]
fn parses_and_merges_request_user_input_with_answers_and_notes() {
    let parsed = parse(
        REQUEST_USER_INPUT,
        "rollout-2026-07-02T10-00-00-66666666-6666-4666-8666-666666666666.jsonl",
        1024 * 1024,
    );

    let request = parsed
        .entries
        .iter()
        .find(|entry| entry.tool_kind == Some(ToolKind::RequestUserInput))
        .expect("request_user_input entry");
    assert_eq!(request.tool_status, Some(ToolStatus::Succeeded));
    assert_eq!(request.raw_refs.len(), 3);
    assert_eq!(
        request.metadata["requestUserInputQuestions"][0]["options"][1]["description"],
        "Use the synthetic production environment."
    );
    assert_eq!(
        request.metadata["requestUserInputAnswers"]["target"]["answers"][0],
        "Production"
    );
    assert_eq!(
        request.metadata["requestUserInputAnswers"]["target"]["answers"][1],
        "user_note: Use the synthetic canary."
    );
    assert!(!request.metadata.contains_key("requestUserInputNotes"));
    assert!(
        parsed
            .diagnostics
            .iter()
            .all(|diagnostic| diagnostic.code != "unknown_event")
    );
}

#[test]
fn deduplicates_presentation_and_streaming_entries() {
    let parsed = parse(
        DEDUP,
        "rollout-2026-06-01T00-00-00-33333333-3333-4333-8333-333333333333.jsonl",
        1024 * 1024,
    );
    let users = parsed
        .entries
        .iter()
        .filter(|entry| entry.role == Some(MessageRole::User))
        .collect::<Vec<_>>();
    assert_eq!(users.len(), 1);
    assert_eq!(users[0].primary_text, "Duplicate message");
    assert_eq!(users[0].raw_refs.len(), 2);
    let assistant = parsed
        .entries
        .iter()
        .find(|entry| entry.role == Some(MessageRole::Assistant))
        .expect("assistant entry");
    assert_eq!(assistant.primary_text, "Final answer");
    assert_eq!(assistant.raw_refs.len(), 2);
    let tool = parsed
        .entries
        .iter()
        .find(|entry| entry.call_id.as_deref() == Some("call-missing-begin"))
        .expect("synthetic tool begin");
    assert_eq!(tool.raw_refs.len(), 2);
    assert_eq!(tool.tool_status, Some(ToolStatus::Succeeded));
}

#[test]
fn hides_injected_context_and_titles_from_the_first_active_user_message() {
    let bytes = br##"{"timestamp":"2026-07-01T00:00:00Z","type":"session_meta","payload":{"id":"88888888-8888-4888-8888-888888888888","cwd":"/work/example"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"# AGENTS.md instructions for /work/example\n<INSTRUCTIONS>hidden</INSTRUCTIONS>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<environment_context>hidden</environment_context>"}]}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"- Fix indexing status\nwith details"}]}}
{"type":"event_msg","payload":{"type":"user_message","message":"- Fix indexing status\nwith details"}}
{"type":"event_msg","payload":{"type":"agent_message","message":"Implemented"}}
{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"<user_action>Review passed</user_action>"}]}}
"##;
    let parsed = parse(
        bytes,
        "rollout-2026-07-01T00-00-00-88888888-8888-4888-8888-888888888888.jsonl",
        1024 * 1024,
    );

    assert_eq!(parsed.summary.session.title, "Fix indexing status");
    assert_eq!(parsed.summary.session.cwd.as_deref(), Some("/work/example"));
    let internal = parsed
        .entries
        .iter()
        .filter(|entry| entry.presentation == EntryPresentation::Internal)
        .collect::<Vec<_>>();
    assert_eq!(internal.len(), 2);
    assert!(
        internal
            .iter()
            .all(|entry| entry.default_collapsed && !entry.searchable)
    );
    assert!(parsed.entries.iter().any(|entry| {
        entry.presentation == EntryPresentation::User
            && entry.primary_text.starts_with("- Fix indexing status")
    }));
    assert!(parsed.entries.iter().any(|entry| {
        entry.presentation == EntryPresentation::Response
            && entry.primary_text.contains("Review passed")
    }));
}

#[test]
fn unknown_and_malformed_records_degrade_without_file_failure() {
    let parsed = parse(
        MALFORMED,
        "rollout-2026-05-01T00-00-00-44444444-4444-4444-8444-444444444444.jsonl",
        1024 * 1024,
    );
    let codes = parsed
        .diagnostics
        .iter()
        .map(|diagnostic| diagnostic.code.as_str())
        .collect::<Vec<_>>();
    assert!(codes.contains(&"unknown_envelope"));
    assert!(codes.contains(&"unknown_event"));
    assert!(codes.contains(&"unknown_response_item"));
    assert!(codes.contains(&"invalid_json"));
    assert_eq!(parsed.summary.session.completeness, Completeness::Partial);
    assert_eq!(parsed.raw_records.len(), 5);
}

#[test]
fn maps_review_source_and_parent_relation() {
    let parsed = parse(
        REVIEW,
        "rollout-2026-04-01T00-00-00-55555555-5555-4555-8555-555555555555.jsonl",
        1024 * 1024,
    );
    assert_eq!(parsed.summary.session.source, SourceKind::Review);
    assert_eq!(
        parsed.summary.session.parent_thread_id.as_deref(),
        Some("66666666-6666-4666-8666-666666666666")
    );
    assert_eq!(
        parsed.summary.session.parent_relation,
        Some(SessionParentRelation::Parent)
    );
}

#[test]
fn maps_forks_and_hashes_exact_plan_handoffs() {
    let fork = br##"{"timestamp":"2026-07-01T00:00:00Z","type":"session_meta","payload":{"id":"99999999-9999-4999-8999-999999999999","cwd":"/work/example","forked_from_id":"aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"}}
"##;
    let parsed_fork = parse(
        fork,
        "rollout-2026-07-01T00-00-00-99999999-9999-4999-8999-999999999999.jsonl",
        1024 * 1024,
    );
    assert_eq!(
        parsed_fork.summary.session.parent_thread_id.as_deref(),
        Some("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
    );
    assert_eq!(
        parsed_fork.summary.session.parent_relation,
        Some(SessionParentRelation::Fork)
    );

    let parent = br##"{"timestamp":"2026-07-01T00:00:00Z","type":"session_meta","payload":{"id":"bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb","cwd":"/work/example"}}
{"timestamp":"2026-07-01T00:01:00Z","type":"response_item","payload":{"type":"message","role":"assistant","content":[{"type":"output_text","text":"before\n<proposed_plan>\r\n# Exact plan\r\nImplement it\r\n</proposed_plan>\nafter"}]}}
"##;
    let child = br##"{"timestamp":"2026-07-01T00:02:00Z","type":"session_meta","payload":{"id":"cccccccc-cccc-4ccc-8ccc-cccccccccccc","cwd":"/work/example"}}
{"timestamp":"2026-07-01T00:03:00Z","type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"A previous agent produced the plan below to accomplish the user's task. Implement the plan in a fresh context. Treat the plan as the source of user intent, re-read files as needed, and carry the work through implementation and verification.\n\n# Exact plan\nImplement it"}]}}
"##;
    let parsed_parent = parse(
        parent,
        "rollout-2026-07-01T00-00-00-bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb.jsonl",
        1024 * 1024,
    );
    let parsed_child = parse(
        child,
        "rollout-2026-07-01T00-02-00-cccccccc-cccc-4ccc-8ccc-cccccccccccc.jsonl",
        1024 * 1024,
    );
    assert_eq!(
        parsed_parent.summary.session.proposed_plan_hash,
        parsed_child.summary.session.handoff_plan_hash
    );
    assert!(parsed_parent.summary.session.proposed_plan_hash.is_some());
    assert!(parsed_child.summary.session.parent_thread_id.is_none());
}

#[test]
fn bounds_oversize_invalid_utf8_and_incomplete_tail() {
    let mut bytes = Vec::new();
    bytes.extend_from_slice(
        b"{\"timestamp\":\"2026-01-01T00:00:00Z\",\"type\":\"session_meta\",\"payload\":{\"id\":\"77777777-7777-4777-8777-777777777777\"}}\n",
    );
    bytes.extend_from_slice(
        b"{\"type\":\"event_msg\",\"payload\":{\"type\":\"user_message\",\"message\":\"",
    );
    bytes.extend(std::iter::repeat_n(b'x', 512));
    bytes.extend_from_slice(b"\"}}\n");
    bytes.extend_from_slice(&[0xff, 0xfe, b'\n']);
    bytes.extend_from_slice(b"{\"type\":\"event_msg\"");

    let parsed = parse(
        &bytes,
        "rollout-2026-01-01T00-00-00-77777777-7777-4777-8777-777777777777.jsonl",
        128,
    );
    let codes = parsed
        .diagnostics
        .iter()
        .map(|diagnostic| diagnostic.code.as_str())
        .collect::<Vec<_>>();
    assert!(codes.contains(&"content_too_large"));
    assert!(codes.contains(&"invalid_utf8"));
    assert!(codes.contains(&"incomplete_tail"));
    assert!(parsed.summary.incomplete_tail);
    assert!(parsed.raw_records.iter().any(|raw| raw.oversize));
    let invalid = parsed
        .raw_records
        .iter()
        .find(|raw| !raw.utf8)
        .expect("invalid UTF-8 raw record");
    assert_eq!(invalid.hex_preview.as_deref(), Some("fffe"));
    assert!(parsed.summary.stable_prefix_bytes < bytes.len() as u64);
}

#[test]
fn checkpoint_accepts_append_and_rejects_truncate_or_replacement() {
    let mut source = NamedTempFile::new().expect("temp source");
    source.write_all(b"one\ntwo\n").unwrap();
    source.flush().unwrap();
    let root = source.path().parent().unwrap().to_path_buf();
    let checkpoint = checkpoint_for_file(&root, source.path(), 8).unwrap();
    assert!(verify_checkpoint(&root, source.path(), &checkpoint).unwrap());

    source.write_all(b"three\n").unwrap();
    source.flush().unwrap();
    assert!(verify_checkpoint(&root, source.path(), &checkpoint).unwrap());

    std::fs::write(source.path(), b"one\n").unwrap();
    assert!(!verify_checkpoint(&root, source.path(), &checkpoint).unwrap());

    std::fs::write(source.path(), b"ONE\ntwo\nthree\n").unwrap();
    assert!(!verify_checkpoint(&root, source.path(), &checkpoint).unwrap());
}
