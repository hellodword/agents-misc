use super::*;

pub(super) fn normalize_event(
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
        "agent_message" => {
            let mut entries = assistant_message_entries(
                phase_field(payload),
                string_field(payload, &["message"]),
                timestamp_micros,
                raw_id,
                EntryOrigin::EventPresentation,
            );
            entries
                .iter_mut()
                .for_each(|entry| add_agent_message_delivery_metadata(entry, payload));
            NormalizeResult::Entries(entries)
        }
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
