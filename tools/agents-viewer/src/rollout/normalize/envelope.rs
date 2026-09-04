use super::*;

pub(super) enum NormalizeResult {
    None,
    Entry(NormalizedEntry),
    Entries(Vec<NormalizedEntry>),
    Unknown(NormalizedEntry, &'static str),
}

pub(super) fn normalize_envelope(
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
        "token_usage_record" => NormalizeResult::Entry(context_entry(
            "Token usage record",
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
        "security_risk_score" => NormalizeResult::Entry(context_entry(
            "Security risk score",
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
            let mut result =
                normalize_response_item(&envelope.payload, timestamp_micros, raw_id, session);
            add_response_item_envelope_metadata(&mut result, &envelope.harness_metadata);
            result
        }
        "realtime_item" => normalize_realtime_item(&envelope.payload, timestamp_micros, raw_id),
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

fn normalize_realtime_item(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizeResult {
    let kind = payload
        .get("type")
        .and_then(Value::as_str)
        .unwrap_or_default();
    let mut entry = match kind {
        "transcript_segment" => {
            let role = match payload.get("role").and_then(Value::as_str) {
                Some("user") => MessageRole::User,
                Some("assistant") => MessageRole::Assistant,
                _ => {
                    return NormalizeResult::Unknown(
                        simple_entry(
                            EntryKind::Unknown,
                            "Realtime transcript",
                            String::new(),
                            timestamp_micros,
                            raw_id,
                            EntryOrigin::Derived,
                            false,
                            true,
                        ),
                        "unknown_realtime_role",
                    );
                }
            };
            message_entry(
                role,
                None,
                string_field(payload, &["text"]),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            )
        }
        "realtime_session_started" => simple_entry(
            EntryKind::Marker,
            "Realtime session started",
            String::new(),
            timestamp_micros,
            raw_id,
            EntryOrigin::Derived,
            false,
            true,
        ),
        "bem_item_promoted" => {
            let mut entry = simple_entry(
                EntryKind::Marker,
                "Realtime item promoted",
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            );
            for (source, target) in [("turn_id", "turnId"), ("item_id", "promotedItemId")] {
                if let Some(value) = string_option(payload, source) {
                    entry.metadata.insert(target.into(), Value::String(value));
                }
            }
            if let Some(presentation) = payload.get("presentation") {
                entry
                    .metadata
                    .insert("realtimePresentation".into(), presentation.clone());
            }
            entry
        }
        "realtime_session_closed" => {
            let mut entry = simple_entry(
                EntryKind::Marker,
                "Realtime session closed",
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            );
            if let Some(outcome) = string_option(payload, "outcome") {
                entry
                    .metadata
                    .insert("realtimeOutcome".into(), Value::String(outcome));
            }
            entry
        }
        _ => {
            return NormalizeResult::Unknown(
                simple_entry(
                    EntryKind::Unknown,
                    if kind.is_empty() {
                        "Unknown realtime item"
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
                "unknown_realtime_item",
            );
        }
    };
    add_source_item_id(&mut entry, payload);
    if let Some(realtime_session_id) = string_option(payload, "realtime_session_id") {
        entry.metadata.insert(
            "realtimeSessionId".into(),
            Value::String(realtime_session_id),
        );
    }
    NormalizeResult::Entry(entry)
}
