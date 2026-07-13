use std::io::{BufReader, Cursor, Write};

use agents_viewer::model::{
    Completeness, EntryKind, EntryPresentation, MessageRole, SourceKind, ToolStatus,
};
use agents_viewer::rollout::{
    CollectingSink, ParseContext, RootKind, checkpoint_for_file, parse_rollout, verify_checkpoint,
};
use pretty_assertions::assert_eq;
use tempfile::NamedTempFile;

const V120: &[u8] = include_bytes!("fixtures/rollouts/v0_120.jsonl");
const V144: &[u8] = include_bytes!("fixtures/rollouts/v0_144.jsonl");
const DEDUP: &[u8] = include_bytes!("fixtures/rollouts/dedup.jsonl");
const MALFORMED: &[u8] = include_bytes!("fixtures/rollouts/malformed.jsonl");
const REVIEW: &[u8] = include_bytes!("fixtures/rollouts/subagent_review.jsonl");

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
