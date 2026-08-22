use std::io::{BufReader, Cursor};

use serde_json::json;

use super::*;

fn context() -> ParseContext {
    ParseContext {
        root_kind: RootKind::Active,
        relative_path: "2026/08/22/direct.jsonl".into(),
        file_name: "direct.jsonl".into(),
        modified_at_micros: 1_776_777_600_000_000,
        now_micros: 1_776_777_700_000_000,
        max_event_bytes: 1024 * 1024,
    }
}

#[test]
fn driver_parses_a_minimal_rollout_directly() {
    let bytes = br#"{"timestamp":"2026-08-22T00:00:00Z","type":"session_meta","payload":{"id":"11111111-1111-4111-8111-111111111111","source":"cli"}}
"#;
    let mut sink = driver::CollectingSink::default();
    let summary = driver::parse_rollout(BufReader::new(Cursor::new(bytes)), &context(), &mut sink)
        .expect("minimal rollout");
    assert_eq!(summary.recognized_record_count, 1);
    assert_eq!(summary.session.id, "11111111-1111-4111-8111-111111111111");
}

#[test]
fn envelope_dispatches_compaction_directly() {
    let mut session = session::SessionBuilder::new(&context(), "session".into());
    let result = envelope::normalize_envelope(
        &Envelope {
            timestamp: None,
            ordinal: None,
            kind: "compacted".into(),
            payload: Value::Null,
            harness_metadata: Value::Null,
        },
        None,
        "raw",
        1,
        &mut session,
    );
    let NormalizeResult::Entry(entry) = result else {
        panic!("expected normalized compaction entry");
    };
    assert_eq!(entry.title, "Conversation compacted");
}

#[test]
fn event_normalizes_user_messages_directly() {
    let result = event::normalize_event(
        &json!({"type": "user_message", "message": "Direct event"}),
        Some(1),
        "raw",
    );
    let NormalizeResult::Entry(entry) = result else {
        panic!("expected user entry");
    };
    assert_eq!(entry.primary_text, "Direct event");
    assert_eq!(entry.presentation, EntryPresentation::User);
}

#[test]
fn item_normalizes_plans_directly() {
    let result = item::normalize_item_completed(
        &json!({"item": {"type": "Plan", "id": "plan", "text": "# Direct plan"}}),
        Some(2),
        "raw",
    );
    let NormalizeResult::Entry(entry) = result else {
        panic!("expected plan entry");
    };
    assert_eq!(entry.kind, EntryKind::Plan);
    assert_eq!(entry.primary_text, "# Direct plan");
}

#[test]
fn message_builds_presentation_directly() {
    let entry = message::message_entry(
        MessageRole::Assistant,
        Some(Phase::Final),
        "Direct answer".into(),
        Some(3),
        "raw",
        EntryOrigin::ResponseItem,
    );
    assert_eq!(entry.presentation, EntryPresentation::Response);
    assert_eq!(entry.phase, Some(Phase::Final));
}

#[test]
fn metadata_parses_source_and_timestamp_directly() {
    assert_eq!(
        metadata::parse_timestamp("2026-08-22T12:34:56Z"),
        Some(1_787_402_096_000_000)
    );
    assert_eq!(
        metadata::source_kind(&json!({"source": "vscode"})),
        SourceKind::Vscode
    );
}

#[test]
fn content_extracts_nested_text_directly() {
    assert_eq!(
        content::string_field(&json!({"message": "nested"}), &["message"]),
        "nested"
    );
    assert_eq!(content::truncate_graphemes("a🙂b", 2), "a🙂");
}

#[test]
fn session_derives_title_directly() {
    let context = context();
    let mut builder = session::SessionBuilder::new(&context, "session".into());
    let entry = message::message_entry(
        MessageRole::User,
        None,
        "# Direct session".into(),
        Some(4),
        "raw",
        EntryOrigin::ResponseItem,
    );
    builder.observe_entry(&entry);
    assert_eq!(
        builder.finish(&context, 1, false, false).title,
        "Direct session"
    );
}

#[test]
fn identity_is_stable_directly() {
    assert_eq!(identity::event_title("web_search_begin"), "Web search");
    assert_eq!(
        identity::raw_ref_id("session", 1, 2),
        identity::raw_ref_id("session", 1, 2)
    );
}
