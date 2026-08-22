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
