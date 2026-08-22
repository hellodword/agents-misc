mod support;

use std::io::{BufReader, Cursor};

use agents_viewer::model::{SseEventPayload, SseEventType};
use agents_viewer::rollout::{
    CollectingSink, EntryOrigin, ParseContext, ParsedRollout, RootKind, parse_rollout,
};
use agents_viewer::server::sse::SseHub;
use axum::body::to_bytes;
use base64::Engine as _;
use http::StatusCode;
use serde_json::{Value, json};
use sha2::{Digest as _, Sha256};
use sqlx::Row as _;
use tower::ServiceExt as _;

const ROLLOUTS: &[(&str, &[u8])] = &[
    ("dedup", include_bytes!("fixtures/rollouts/dedup.jsonl")),
    (
        "malformed",
        include_bytes!("fixtures/rollouts/malformed.jsonl"),
    ),
    (
        "plan-mode-final-answer",
        include_bytes!("fixtures/rollouts/plan_mode_final_answer.jsonl"),
    ),
    ("plans", include_bytes!("fixtures/rollouts/plans.jsonl")),
    (
        "request-user-input",
        include_bytes!("fixtures/rollouts/request_user_input.jsonl"),
    ),
    (
        "subagent-review",
        include_bytes!("fixtures/rollouts/subagent_review.jsonl"),
    ),
    ("v0.120", include_bytes!("fixtures/rollouts/v0_120.jsonl")),
    ("v0.144", include_bytes!("fixtures/rollouts/v0_144.jsonl")),
    ("v0.145", include_bytes!("fixtures/rollouts/v0_145.jsonl")),
    (
        "v0.145-subagent",
        include_bytes!("fixtures/rollouts/v0_145_subagent.jsonl"),
    ),
    ("v0.146", include_bytes!("fixtures/rollouts/v0_146.jsonl")),
    ("v0.147", include_bytes!("fixtures/rollouts/v0_147.jsonl")),
    ("v0.148", include_bytes!("fixtures/rollouts/v0_148.jsonl")),
];

#[test]
fn normalized_rollout_json_is_byte_stable() {
    let actual = ROLLOUTS
        .iter()
        .map(|(name, bytes)| {
            let context = ParseContext {
                root_kind: RootKind::Active,
                relative_path: format!("2026/08/22/{name}.jsonl"),
                file_name: format!("{name}.jsonl"),
                modified_at_micros: 1_776_777_600_000_000,
                now_micros: 1_776_777_700_000_000,
                max_event_bytes: 1024 * 1024,
            };
            let mut sink = CollectingSink::default();
            let summary = parse_rollout(BufReader::new(Cursor::new(bytes)), &context, &mut sink)
                .expect("characterization fixture parses");
            let normalized = normalized_json(&sink.finish(summary));
            format!(
                "{name} {}",
                sha256(&serde_json::to_vec(&normalized).unwrap())
            )
        })
        .collect::<Vec<_>>()
        .join("\n");

    assert_eq!(
        actual,
        include_str!("fixtures/characterization/normalize.sha256").trim()
    );
}

#[tokio::test]
async fn sqlite_and_http_contracts_are_byte_stable() {
    let app = support::TestApp::new().await;
    let database_json = sqlite_characterization(&app).await;
    let router = app.router();
    let session = "11111111-1111-4111-8111-111111111111";
    let mut fingerprints = vec![format!("sqlite {}", sha256(database_json.as_bytes()))];

    for (name, path) in [
        ("sessions", "/api/v1/sessions?limit=10"),
        (
            "entries",
            "/api/v1/sessions/11111111-1111-4111-8111-111111111111/entries?limit=500&includeTechnical=true",
        ),
        ("search", "/api/v1/search?q=hello&limit=10&allTypes=true"),
        ("invalid-search", "/api/v1/search?q="),
    ] {
        let bytes = response_bytes(router.clone(), path).await;
        fingerprints.push(format!("{name} {}", sha256(&bytes)));
    }

    let first_path = format!("/api/v1/sessions/{session}/entries?limit=1");
    let first = response_bytes(router.clone(), &first_path).await;
    let first_json: Value =
        serde_json::from_slice(first.splitn(4, |byte| *byte == b'\n').nth(3).unwrap()).unwrap();
    let cursor = first_json["nextCursor"].as_str().unwrap();
    let second = response_bytes(
        router.clone(),
        &format!("/api/v1/sessions/{session}/entries?limit=1&cursor={cursor}"),
    )
    .await;
    fingerprints.push(format!("cursor-first {}", sha256(&first)));
    fingerprints.push(format!("cursor-second {}", sha256(&second)));

    let protected = app.router_with_password("synthetic-password");
    let denied = response_bytes(protected.clone(), "/api/v1/status").await;
    fingerprints.push(format!("permission-denied {}", sha256(&denied)));
    let mut authorized = support::request("/api/v1/sessions?limit=1");
    let credential =
        base64::engine::general_purpose::STANDARD.encode("agents-viewer:synthetic-password");
    authorized.headers_mut().insert(
        "authorization",
        format!("Basic {credential}").parse().unwrap(),
    );
    let response = protected.oneshot(authorized).await.unwrap();
    let allowed = stable_response(response).await;
    fingerprints.push(format!("permission-allowed {}", sha256(&allowed)));

    assert_eq!(
        fingerprints.join("\n"),
        include_str!("fixtures/characterization/service.sha256").trim()
    );
}

#[tokio::test]
async fn sse_payload_json_and_replay_order_are_byte_stable() {
    let hub = SseHub::new();
    for (event, generation, session_id, entry_id) in [
        (SseEventType::IndexProgress, 7, None, None),
        (SseEventType::SessionUpdated, 8, Some("session-a"), None),
        (
            SseEventType::EntryUpdated,
            9,
            Some("session-a"),
            Some("entry-b"),
        ),
    ] {
        hub.publish(
            event,
            SseEventPayload {
                generation,
                phase: None,
                session_id: session_id.map(str::to_owned),
                entry_id: entry_id.map(str::to_owned),
                progress: None,
                diagnostic: None,
                sync_state: None,
            },
        )
        .await;
    }
    let (events, expired) = hub.replay_after(Some(0)).await;
    assert!(!expired);
    let bytes = serde_json::to_vec(&events).unwrap();
    assert_eq!(
        sha256(&bytes),
        include_str!("fixtures/characterization/sse.sha256").trim()
    );
}

async fn response_bytes(router: axum::Router, path: &str) -> Vec<u8> {
    stable_response(router.oneshot(support::request(path)).await.unwrap()).await
}

async fn stable_response(response: http::Response<axum::body::Body>) -> Vec<u8> {
    let status = response.status();
    assert!(
        status == StatusCode::OK
            || status == StatusCode::BAD_REQUEST
            || status == StatusCode::UNAUTHORIZED
    );
    let content_type = response
        .headers()
        .get("content-type")
        .and_then(|value| value.to_str().ok())
        .unwrap_or("")
        .to_owned();
    let authentication = response
        .headers()
        .get("www-authenticate")
        .and_then(|value| value.to_str().ok())
        .unwrap_or("")
        .to_owned();
    let body = to_bytes(response.into_body(), 8 * 1024 * 1024)
        .await
        .unwrap();
    [
        status.as_str().as_bytes(),
        b"\n",
        content_type.as_bytes(),
        b"\n",
        authentication.as_bytes(),
        b"\n",
        body.as_ref(),
    ]
    .concat()
}

async fn sqlite_characterization(app: &support::TestApp) -> String {
    let mut output = String::new();
    for (label, query) in [
        (
            "sessions",
            "SELECT id || '|' || source_kind || '|' || COALESCE(parent_thread_id, '') || '|' || COALESCE(parent_relation, '') || '|' || title || '|' || preview || '|' || created_at_micros || '|' || updated_at_micros || '|' || archived || '|' || entry_count || '|' || index_state || '|' || completeness || '|' || diagnostic_count FROM sessions ORDER BY id",
        ),
        (
            "entries",
            "SELECT id || '|' || session_id || '|' || sequence || '|' || COALESCE(timestamp_micros, '') || '|' || kind || '|' || presentation || '|' || COALESCE(role, '') || '|' || COALESCE(phase, '') || '|' || COALESCE(tool_kind, '') || '|' || COALESCE(tool_status, '') || '|' || title || '|' || primary_text || '|' || secondary_text || '|' || metadata_json || '|' || COALESCE(call_id, '') || '|' || COALESCE(parent_entry_id, '') || '|' || default_collapsed || '|' || searchable FROM entries ORDER BY session_id, sequence",
        ),
        (
            "raw",
            "SELECT id || '|' || session_id || '|' || line_no || '|' || byte_offset || '|' || byte_length || '|' || envelope_type || '|' || parse_status || '|' || content_hash || '|' || utf8 || '|' || oversize FROM raw_records ORDER BY session_id, line_no",
        ),
        (
            "refs",
            "SELECT entry_id || '|' || raw_id || '|' || ordinal FROM entry_raw_refs ORDER BY entry_id, ordinal",
        ),
        (
            "diagnostics",
            "SELECT COALESCE(session_id, '') || '|' || severity || '|' || code || '|' || message || '|' || count FROM diagnostics ORDER BY session_id, code",
        ),
    ] {
        output.push_str(label);
        output.push('\n');
        let rows = sqlx::query(query)
            .fetch_all(app.state.database.pool())
            .await
            .unwrap();
        for row in rows {
            output.push_str(row.get::<String, _>(0).as_str());
            output.push('\n');
        }
    }
    output
}

fn normalized_json(parsed: &ParsedRollout) -> Value {
    let session = &parsed.summary.session;
    json!({
        "summary": {
            "session": {
                "id": session.id,
                "source": session.source,
                "parentThreadId": session.parent_thread_id,
                "parentRelation": session.parent_relation,
                "proposedPlanHash": session.proposed_plan_hash,
                "proposedPlanAtMicros": session.proposed_plan_at_micros,
                "handoffPlanHash": session.handoff_plan_hash,
                "handoffAtMicros": session.handoff_at_micros,
                "cwd": session.cwd,
                "title": session.title,
                "preview": session.preview,
                "createdAtMicros": session.created_at_micros,
                "updatedAtMicros": session.updated_at_micros,
                "archived": session.archived,
                "cliVersion": session.cli_version,
                "provider": session.provider,
                "historyLine": session.history_line,
                "gitBranch": session.git_branch,
                "gitCommit": session.git_commit,
                "entryCount": session.entry_count,
                "indexState": session.index_state,
                "completeness": session.completeness,
                "diagnosticCount": session.diagnostic_count,
            },
            "rawRecordCount": parsed.summary.raw_record_count,
            "recognizedRecordCount": parsed.summary.recognized_record_count,
            "incompleteTail": parsed.summary.incomplete_tail,
            "stablePrefixBytes": parsed.summary.stable_prefix_bytes,
            "stablePrefixHash": parsed.summary.stable_prefix_hash,
        },
        "rawRecords": parsed.raw_records.iter().map(|record| json!({
            "id": record.id,
            "lineNo": record.line_no,
            "byteOffset": record.byte_offset,
            "byteLength": record.byte_length,
            "envelopeType": record.envelope_type,
            "parseStatus": record.parse_status,
            "contentHash": record.content_hash,
            "utf8": record.utf8,
            "oversize": record.oversize,
            "hexPreview": record.hex_preview,
        })).collect::<Vec<_>>(),
        "entries": parsed.entries.iter().map(|entry| json!({
            "id": entry.id,
            "sessionId": entry.session_id,
            "sequence": entry.sequence,
            "timestampMicros": entry.timestamp_micros,
            "kind": entry.kind,
            "presentation": entry.presentation,
            "role": entry.role,
            "phase": entry.phase,
            "toolKind": entry.tool_kind,
            "toolStatus": entry.tool_status,
            "title": entry.title,
            "primaryText": entry.primary_text,
            "secondaryText": entry.secondary_text,
            "metadata": entry.metadata,
            "callId": entry.call_id,
            "parentEntryId": entry.parent_entry_id,
            "defaultCollapsed": entry.default_collapsed,
            "searchable": entry.searchable,
            "rawRefs": entry.raw_refs,
            "origin": match entry.origin {
                EntryOrigin::EventPresentation => "eventPresentation",
                EntryOrigin::ItemCompleted => "itemCompleted",
                EntryOrigin::ResponseItem => "responseItem",
                EntryOrigin::Derived => "derived",
            },
        })).collect::<Vec<_>>(),
        "diagnostics": parsed.diagnostics.iter().map(|diagnostic| json!({
            "severity": diagnostic.severity,
            "code": diagnostic.code,
            "message": diagnostic.message,
            "lineNo": diagnostic.line_no,
            "rawRefId": diagnostic.raw_ref_id,
        })).collect::<Vec<_>>(),
    })
}

fn sha256(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}
