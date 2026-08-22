use super::*;

pub(super) fn normalize_item_completed(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizeResult {
    let item = payload.get("item").unwrap_or(&Value::Null);
    let kind = item.get("type").and_then(Value::as_str).unwrap_or_default();
    if matches!(kind, "AgentMessage" | "agent_message") {
        let mut entries = assistant_message_entries(
            phase_field(item),
            content_text(item.get("content")),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        );
        for entry in &mut entries {
            if entry.kind == EntryKind::Message {
                add_source_item_id(entry, item);
            }
            add_execution_attribution_metadata(entry, item);
            add_tool_capability_metadata(entry, item);
            add_event_timing_metadata(entry, payload);
            if let Some(turn_id) = string_option(payload, "turn_id") {
                entry
                    .metadata
                    .insert("turnId".into(), Value::String(turn_id));
            }
        }
        return NormalizeResult::Entries(entries);
    }
    let mut entry = match kind {
        "UserMessage" | "user_message" => {
            let mut entry = message_entry(
                MessageRole::User,
                None,
                user_input_text(item.get("content")),
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            );
            add_attachment_metadata(&mut entry, item);
            entry
        }
        "HookPrompt" | "hook_prompt" => simple_entry(
            EntryKind::Context,
            "Hook prompt",
            content_text(item.get("fragments")),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            false,
            true,
        ),
        "Plan" | "plan" => simple_entry(
            EntryKind::Plan,
            "Plan",
            string_field(item, &["text"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            true,
            false,
        ),
        "Reasoning" | "reasoning" => {
            let text = content_text(item.get("summary_text"));
            let mut entry =
                reasoning_entry(text.clone(), timestamp_micros, raw_id, !text.is_empty());
            entry.origin = EntryOrigin::ItemCompleted;
            entry
        }
        "CommandExecution" | "command_execution" => tool_entry(
            ToolKind::Command,
            "Command",
            string_array(item.get("command"), " "),
            joined_fields(
                item,
                &["formatted_output", "aggregated_output", "stdout", "stderr"],
            ),
            item_id(item),
            status_field(item),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "DynamicToolCall" | "dynamic_tool_call" => {
            let status = if item.get("success").and_then(Value::as_bool) == Some(false) {
                Some(ToolStatus::Failed)
            } else {
                status_field(item)
            };
            let mut entry = tool_entry(
                ToolKind::Dynamic,
                &string_field(item, &["tool"]),
                pretty_value(item.get("arguments")),
                joined_fields(item, &["content_items", "error"]),
                item_id(item),
                status,
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            );
            add_attachment_metadata(&mut entry, item);
            entry
        }
        "CollabAgentToolCall" | "collab_agent_tool_call" => tool_entry(
            ToolKind::Other,
            "Collaboration",
            string_field(item, &["prompt", "tool"]),
            joined_fields(
                item,
                &["receiver_agents", "receiver_thread_ids", "agents_states"],
            ),
            item_id(item),
            status_field(item),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "SubAgentActivity" | "sub_agent_activity" => simple_entry(
            EntryKind::Marker,
            "Sub-agent activity",
            string_field(item, &["kind", "agent_path"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            true,
            true,
        ),
        "WebSearch" | "web_search" => tool_entry(
            ToolKind::WebSearch,
            "Web search",
            string_field(item, &["query"]),
            joined_fields(item, &["action", "results"]),
            item_id(item),
            Some(ToolStatus::Succeeded),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "ImageView" | "image_view" => tool_entry(
            ToolKind::ViewImage,
            "Image attachment",
            string_field(item, &["path"]),
            String::new(),
            item_id(item),
            Some(ToolStatus::Succeeded),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "Extension" | "extension" => {
            match normalize_extension_item(item, timestamp_micros, raw_id) {
                NormalizeResult::Entry(entry) => entry,
                other => return other,
            }
        }
        "ImageGeneration" | "image_generation" => {
            let mut entry = tool_entry(
                ToolKind::Other,
                "Image generation",
                string_field(item, &["revised_prompt", "revisedPrompt"]),
                string_field(item, &["saved_path", "savedPath"]),
                item_id(item),
                status_field(item),
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            );
            add_attachment_counts(&mut entry, 1, 0);
            add_image_generation_failure_metadata(&mut entry, item);
            entry
        }
        "EnteredReviewMode" | "entered_review_mode" => simple_entry(
            EntryKind::Marker,
            "Review started",
            string_field(item, &["user_facing_hint"]),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            true,
            true,
        ),
        "ExitedReviewMode" | "exited_review_mode" => simple_entry(
            EntryKind::Marker,
            "Review completed",
            pretty_value(item.get("review_output")),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            true,
            true,
        ),
        "FileChange" | "file_change" => tool_entry(
            ToolKind::Patch,
            "Patch",
            pretty_value(item.get("changes")),
            joined_fields(item, &["stdout", "stderr"]),
            item_id(item),
            status_field(item).or(Some(ToolStatus::Succeeded)),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "McpToolCall" | "mcp_tool_call" => {
            let server = string_field(item, &["server"]);
            let tool = string_field(item, &["tool"]);
            let title = if server.is_empty() && tool.is_empty() {
                "MCP tool".into()
            } else {
                format!("{server}/{tool}")
            };
            tool_entry(
                ToolKind::Mcp,
                &title,
                pretty_value(item.get("arguments")),
                joined_fields(item, &["result", "error"]),
                item_id(item),
                status_field(item),
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            )
        }
        "ContextCompaction" | "context_compaction" => simple_entry(
            EntryKind::Marker,
            "Conversation compacted",
            String::new(),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
            false,
            true,
        ),
        _ => {
            return NormalizeResult::Unknown(
                simple_entry(
                    EntryKind::Unknown,
                    if kind.is_empty() {
                        "Unknown item"
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
                "unknown_turn_item",
            );
        }
    };
    add_source_item_id(&mut entry, item);
    add_execution_attribution_metadata(&mut entry, item);
    add_tool_capability_metadata(&mut entry, item);
    add_event_timing_metadata(&mut entry, payload);
    if let Some(turn_id) = string_option(payload, "turn_id") {
        entry
            .metadata
            .insert("turnId".into(), Value::String(turn_id));
    }
    NormalizeResult::Entry(entry)
}

pub(super) fn normalize_extension_item(
    item: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizeResult {
    let kind = item.get("kind").and_then(Value::as_str).unwrap_or_default();
    let mut entry = match kind {
        "clock.sleep" => tool_entry(
            ToolKind::Other,
            "Sleep",
            item.get("durationMs")
                .or_else(|| item.get("duration_ms"))
                .and_then(Value::as_u64)
                .map(|duration| format!("{duration} ms"))
                .unwrap_or_default(),
            String::new(),
            item_id(item),
            Some(ToolStatus::Succeeded),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "web.search" => tool_entry(
            ToolKind::WebSearch,
            "Web search",
            string_field(item, &["query"]),
            joined_fields(item, &["action", "results"]),
            item_id(item),
            Some(ToolStatus::Succeeded),
            timestamp_micros,
            raw_id,
            EntryOrigin::ItemCompleted,
        ),
        "image_gen.generation" => {
            let mut entry = tool_entry(
                ToolKind::Other,
                "Image generation",
                string_field(item, &["revisedPrompt", "revised_prompt"]),
                string_field(item, &["savedPath", "saved_path"]),
                item_id(item),
                status_field(item),
                timestamp_micros,
                raw_id,
                EntryOrigin::ItemCompleted,
            );
            add_attachment_counts(&mut entry, 1, 0);
            add_tool_capability_metadata(&mut entry, item);
            add_image_generation_failure_metadata(&mut entry, item);
            entry
        }
        _ => {
            return NormalizeResult::Unknown(
                simple_entry(
                    EntryKind::Unknown,
                    if kind.is_empty() {
                        "Unknown extension"
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
                "unknown_extension_item",
            );
        }
    };
    add_source_item_id(&mut entry, item);
    NormalizeResult::Entry(entry)
}

pub(super) fn inter_agent_communication_entry(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizedEntry {
    let plaintext = string_field(payload, &["content"]);
    let encrypted = has_encrypted_content(payload);
    let mut entry = message_entry(
        MessageRole::Assistant,
        None,
        if encrypted {
            "Encrypted inter-agent message".into()
        } else {
            plaintext
        },
        timestamp_micros,
        raw_id,
        EntryOrigin::ResponseItem,
    );
    entry.presentation = EntryPresentation::Technical;
    entry.title = "Inter-agent message".into();
    entry.default_collapsed = true;
    entry.searchable = !encrypted && !entry.primary_text.is_empty();
    add_source_item_id(&mut entry, payload);
    for (source, target) in [
        ("author", "author"),
        ("recipient", "recipient"),
        ("other_recipients", "otherRecipients"),
    ] {
        if let Some(value) = payload.get(source).filter(|value| !value.is_null()) {
            entry.metadata.insert(target.into(), value.clone());
        }
    }
    if let Some(trigger_turn) = payload.get("trigger_turn").and_then(Value::as_bool) {
        entry
            .metadata
            .insert("triggerTurn".into(), Value::Bool(trigger_turn));
    }
    entry
}

pub(super) fn normalize_response_item(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    session: &SessionBuilder,
) -> NormalizeResult {
    let kind = payload
        .get("type")
        .and_then(Value::as_str)
        .unwrap_or_default();
    match kind {
        "message" => {
            let role = role_field(payload).unwrap_or(MessageRole::Assistant);
            let startup_context =
                !session.saw_user && matches!(role, MessageRole::System | MessageRole::Developer);
            if role == MessageRole::Assistant {
                let mut entries = assistant_message_entries(
                    phase_field(payload),
                    content_text(payload.get("content")),
                    timestamp_micros,
                    raw_id,
                    EntryOrigin::ResponseItem,
                );
                for entry in &mut entries {
                    if entry.kind == EntryKind::Message {
                        add_attachment_metadata(entry, payload);
                        add_source_item_id(entry, payload);
                    }
                    add_response_item_metadata(entry, payload);
                }
                return NormalizeResult::Entries(entries);
            }
            let mut entry = message_entry(
                role,
                phase_field(payload),
                content_text(payload.get("content")),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            if startup_context {
                entry.default_collapsed = true;
                entry.searchable = false;
            }
            add_attachment_metadata(&mut entry, payload);
            add_source_item_id(&mut entry, payload);
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "agent_message" => {
            let mut entry = inter_agent_communication_entry(payload, timestamp_micros, raw_id);
            entry.origin = EntryOrigin::ResponseItem;
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "reasoning" => {
            let summary = content_text(payload.get("summary"));
            let searchable = !summary.is_empty();
            let text = if searchable {
                summary
            } else if payload
                .get("encrypted_content")
                .is_some_and(|value| !value.is_null())
            {
                "Encrypted reasoning".into()
            } else {
                String::new()
            };
            let mut entry = reasoning_entry(text, timestamp_micros, raw_id, searchable);
            entry.origin = EntryOrigin::ResponseItem;
            add_source_item_id(&mut entry, payload);
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "function_call" | "custom_tool_call" | "tool_search_call" => {
            let name = string_field(payload, &["name", "execution"]);
            let primary = string_field(payload, &["arguments", "input"]);
            let mut entry = tool_entry(
                tool_kind_from_name(&name),
                if name.is_empty() { "Tool call" } else { &name },
                primary.clone(),
                String::new(),
                call_id(payload),
                Some(ToolStatus::Running),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            if entry.tool_kind == Some(ToolKind::RequestUserInput) {
                add_request_user_input_questions_from_text(&mut entry, &primary);
            }
            add_source_item_id(&mut entry, payload);
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "function_call_output" | "custom_tool_call_output" | "tool_search_output" => {
            let secondary = [
                output_text(payload.get("output")),
                value_text(payload.get("execution")).unwrap_or_default(),
                pretty_value(payload.get("tools")),
            ]
            .into_iter()
            .filter(|text| !text.is_empty())
            .collect::<Vec<_>>()
            .join("\n");
            let mut entry = tool_entry(
                ToolKind::Function,
                "Tool output",
                String::new(),
                secondary.clone(),
                call_id(payload),
                Some(ToolStatus::Succeeded),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            add_request_user_input_response_from_text(&mut entry, &secondary);
            add_attachment_metadata(&mut entry, payload);
            add_source_item_id(&mut entry, payload);
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "local_shell_call" => {
            let mut entry = tool_entry(
                ToolKind::Command,
                "Command",
                pretty_value(payload.get("action")),
                String::new(),
                call_id(payload),
                status_field(payload).or(Some(ToolStatus::Running)),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            add_source_item_id(&mut entry, payload);
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "web_search_call" => {
            let mut entry = tool_entry(
                ToolKind::WebSearch,
                "Web search",
                pretty_value(payload.get("action")),
                String::new(),
                call_id(payload).or_else(|| string_option(payload, "id")),
                status_field(payload),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            add_source_item_id(&mut entry, payload);
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "image_generation_call" => {
            let mut entry = tool_entry(
                ToolKind::Other,
                "Image generation",
                string_field(payload, &["revised_prompt"]),
                String::new(),
                call_id(payload).or_else(|| string_option(payload, "id")),
                status_field(payload),
                timestamp_micros,
                raw_id,
                EntryOrigin::ResponseItem,
            );
            add_attachment_counts(&mut entry, 1, 0);
            add_source_item_id(&mut entry, payload);
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        "compaction" | "compaction_summary" | "context_compaction" | "compaction_trigger" => {
            let mut entry = simple_entry(
                EntryKind::Marker,
                "Conversation compacted",
                String::new(),
                timestamp_micros,
                raw_id,
                EntryOrigin::Derived,
                false,
                true,
            );
            add_source_item_id(&mut entry, payload);
            add_response_item_metadata(&mut entry, payload);
            NormalizeResult::Entry(entry)
        }
        _ => NormalizeResult::Unknown(
            simple_entry(
                EntryKind::Unknown,
                if kind.is_empty() {
                    "Unknown response"
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
            "unknown_response_item",
        ),
    }
}
