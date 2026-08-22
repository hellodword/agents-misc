use super::*;

pub(super) fn context_entry(
    title: &str,
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizedEntry {
    simple_entry(
        EntryKind::Context,
        title,
        pretty_value(Some(payload)),
        timestamp_micros,
        raw_id,
        EntryOrigin::Derived,
        false,
        true,
    )
}

pub(super) fn message_entry(
    role: MessageRole,
    phase: Option<Phase>,
    text: String,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    origin: EntryOrigin,
) -> NormalizedEntry {
    let presentation = message_presentation(role, &text);
    let internal = presentation == EntryPresentation::Internal;
    let technical = presentation == EntryPresentation::Technical;
    NormalizedEntry {
        id: String::new(),
        session_id: String::new(),
        sequence: 0,
        timestamp_micros,
        kind: EntryKind::Message,
        presentation,
        role: Some(role),
        phase,
        tool_kind: None,
        tool_status: None,
        title: match presentation {
            EntryPresentation::User => "User",
            EntryPresentation::Response if role == MessageRole::User => "Received",
            EntryPresentation::Response => "Assistant",
            EntryPresentation::Technical => "Technical message",
            EntryPresentation::Internal => "Internal context",
        }
        .into(),
        primary_text: text,
        secondary_text: String::new(),
        metadata: BTreeMap::new(),
        call_id: None,
        parent_entry_id: None,
        default_collapsed: internal || technical,
        searchable: !internal && !technical,
        raw_refs: vec![raw_id.into()],
        origin,
        id_basis: String::new(),
    }
}

struct ProposedPlanParts {
    visible_text: String,
    plan_text: String,
}

pub(super) fn assistant_message_entries(
    phase: Option<Phase>,
    text: String,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    origin: EntryOrigin,
) -> Vec<NormalizedEntry> {
    let Some(parts) = split_proposed_plan_blocks(&text) else {
        return vec![message_entry(
            MessageRole::Assistant,
            phase,
            text,
            timestamp_micros,
            raw_id,
            origin,
        )];
    };

    let mut entries = Vec::with_capacity(2);
    if !parts.visible_text.trim().is_empty() {
        entries.push(message_entry(
            MessageRole::Assistant,
            phase,
            parts.visible_text,
            timestamp_micros,
            raw_id,
            origin,
        ));
    }
    entries.push(simple_entry(
        EntryKind::Plan,
        "Plan",
        parts.plan_text,
        timestamp_micros,
        raw_id,
        origin,
        true,
        false,
    ));
    entries
}

fn split_proposed_plan_blocks(text: &str) -> Option<ProposedPlanParts> {
    const OPEN_TAG: &str = "<proposed_plan>";
    const CLOSE_TAG: &str = "</proposed_plan>";

    let mut visible_text = String::with_capacity(text.len());
    let mut active_plan: Option<String> = None;
    let mut last_plan = None;
    for line in text.split_inclusive('\n') {
        let slug = line.strip_suffix('\n').unwrap_or(line).trim();
        if active_plan.is_none() && slug == OPEN_TAG {
            active_plan = Some(String::new());
        } else if active_plan.is_some() && slug == CLOSE_TAG {
            last_plan = active_plan.take();
        } else if let Some(plan) = active_plan.as_mut() {
            plan.push_str(line);
        } else {
            visible_text.push_str(line);
        }
    }
    if active_plan.is_some() {
        last_plan = active_plan;
    }
    last_plan.map(|plan_text| ProposedPlanParts {
        visible_text,
        plan_text,
    })
}

pub(super) fn message_presentation(role: MessageRole, text: &str) -> EntryPresentation {
    if is_internal_message(text) || matches!(role, MessageRole::Developer | MessageRole::System) {
        return EntryPresentation::Internal;
    }
    if is_received_wrapper(text) {
        return EntryPresentation::Response;
    }
    if is_technical_wrapper(text) {
        return EntryPresentation::Technical;
    }
    match role {
        MessageRole::User => EntryPresentation::User,
        MessageRole::Assistant => EntryPresentation::Response,
        MessageRole::Developer | MessageRole::System => EntryPresentation::Internal,
    }
}

pub(super) fn is_internal_message(text: &str) -> bool {
    let trimmed = text.trim_start();
    trimmed.starts_with("# AGENTS.md instructions for ")
        || [
            "<environment_context",
            "<codex_internal_context",
            "<permissions instructions>",
            "<collaboration_mode>",
            "<skills_instructions>",
            "<plugins_instructions>",
            "<system-reminder",
            "<skill>",
        ]
        .iter()
        .any(|prefix| trimmed.starts_with(prefix))
}

pub(super) fn is_received_wrapper(text: &str) -> bool {
    text.trim_start().starts_with("<user_action>")
}

pub(super) fn is_technical_wrapper(text: &str) -> bool {
    text.trim_start().starts_with("<turn_aborted>")
}

pub(super) fn reasoning_entry(
    text: String,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    searchable: bool,
) -> NormalizedEntry {
    let mut entry = simple_entry(
        EntryKind::Reasoning,
        "Reasoning",
        text,
        timestamp_micros,
        raw_id,
        EntryOrigin::EventPresentation,
        searchable,
        true,
    );
    entry.phase = Some(Phase::Analysis);
    entry
}

#[allow(clippy::too_many_arguments)]
pub(super) fn tool_entry(
    kind: ToolKind,
    title: &str,
    primary: String,
    secondary: String,
    call_id: Option<String>,
    status: Option<ToolStatus>,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    origin: EntryOrigin,
) -> NormalizedEntry {
    NormalizedEntry {
        id: String::new(),
        session_id: String::new(),
        sequence: 0,
        timestamp_micros,
        kind: EntryKind::Tool,
        presentation: EntryPresentation::Technical,
        role: None,
        phase: None,
        tool_kind: Some(kind),
        tool_status: status,
        title: title.into(),
        primary_text: primary,
        secondary_text: secondary,
        metadata: BTreeMap::new(),
        call_id,
        parent_entry_id: None,
        default_collapsed: true,
        searchable: true,
        raw_refs: vec![raw_id.into()],
        origin,
        id_basis: String::new(),
    }
}

#[allow(clippy::too_many_arguments)]
pub(super) fn simple_entry(
    kind: EntryKind,
    title: &str,
    primary: String,
    timestamp_micros: Option<i64>,
    raw_id: &str,
    origin: EntryOrigin,
    searchable: bool,
    default_collapsed: bool,
) -> NormalizedEntry {
    NormalizedEntry {
        id: String::new(),
        session_id: String::new(),
        sequence: 0,
        timestamp_micros,
        kind,
        presentation: EntryPresentation::Technical,
        role: None,
        phase: None,
        tool_kind: None,
        tool_status: None,
        title: title.into(),
        primary_text: primary,
        secondary_text: String::new(),
        metadata: BTreeMap::new(),
        call_id: None,
        parent_entry_id: None,
        default_collapsed,
        searchable,
        raw_refs: vec![raw_id.into()],
        origin,
        id_basis: String::new(),
    }
}

pub(super) fn tool_event_entry(
    event: &str,
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizedEntry {
    let kind = tool_event_kind(event).unwrap_or(ToolKind::Other);
    let title = string_field(payload, &["name", "server", "query"]);
    let primary = string_field(
        payload,
        &["command", "input", "arguments", "query", "revised_prompt"],
    );
    let secondary_fields: &[&str] = match event {
        "mcp_tool_call_end" => &["result"],
        "web_search_end" => &["action", "results"],
        "image_generation_end" => &["saved_path"],
        _ => &["delta", "output", "stdout", "stderr"],
    };
    let secondary = joined_fields(payload, secondary_fields);
    let status = if event.ends_with("_end") || event.ends_with("_response") {
        status_field(payload).or(Some(
            if payload.get("success").and_then(Value::as_bool) == Some(false) {
                ToolStatus::Failed
            } else {
                ToolStatus::Succeeded
            },
        ))
    } else {
        Some(ToolStatus::Running)
    };
    let mut entry = tool_entry(
        kind,
        if title.is_empty() {
            event_title(event)
        } else {
            &title
        },
        primary,
        secondary,
        call_id(payload),
        status,
        timestamp_micros,
        raw_id,
        EntryOrigin::EventPresentation,
    );
    add_attachment_metadata(&mut entry, payload);
    add_execution_attribution_metadata(&mut entry, payload);
    add_tool_capability_metadata(&mut entry, payload);
    add_event_timing_metadata(&mut entry, payload);
    if event == "image_generation_end" {
        add_attachment_counts(&mut entry, 1, 0);
        add_image_generation_failure_metadata(&mut entry, payload);
    }
    entry
}

pub(super) fn request_user_input_event_entry(
    payload: &Value,
    timestamp_micros: Option<i64>,
    raw_id: &str,
) -> NormalizedEntry {
    let mut entry = tool_entry(
        ToolKind::RequestUserInput,
        "request_user_input",
        pretty_value(Some(payload)),
        String::new(),
        call_id(payload),
        Some(ToolStatus::Running),
        timestamp_micros,
        raw_id,
        EntryOrigin::EventPresentation,
    );
    add_request_user_input_questions(&mut entry, payload.get("questions"));
    entry
}

pub(super) fn add_request_user_input_questions_from_text(entry: &mut NormalizedEntry, text: &str) {
    let Ok(value) = serde_json::from_str::<Value>(text) else {
        return;
    };
    add_request_user_input_questions(entry, value.get("questions"));
}

pub(super) fn add_request_user_input_questions(
    entry: &mut NormalizedEntry,
    questions: Option<&Value>,
) {
    if let Some(questions) = questions.filter(|value| value.is_array()) {
        entry
            .metadata
            .insert("requestUserInputQuestions".into(), questions.clone());
    }
}

pub(super) fn add_request_user_input_response_from_text(entry: &mut NormalizedEntry, text: &str) {
    let Ok(value) = serde_json::from_str::<Value>(text) else {
        return;
    };
    if let Some(answers) = value.get("answers").filter(|value| value.is_object()) {
        entry
            .metadata
            .insert("requestUserInputAnswers".into(), answers.clone());
    }
    if let Some(notes) = value.get("notes").filter(|value| value.is_string()) {
        entry
            .metadata
            .insert("requestUserInputNotes".into(), notes.clone());
    }
}

pub(super) fn tool_event_kind(event: &str) -> Option<ToolKind> {
    if event.starts_with("exec_command") || event == "terminal_interaction" {
        Some(ToolKind::Command)
    } else if event.starts_with("patch_apply") {
        Some(ToolKind::Patch)
    } else if event.starts_with("mcp_tool_call") {
        Some(ToolKind::Mcp)
    } else if event.starts_with("web_search") {
        Some(ToolKind::WebSearch)
    } else if event.starts_with("dynamic_tool_call") {
        Some(ToolKind::Dynamic)
    } else if event.starts_with("image_generation") || event == "view_image_tool_call" {
        Some(ToolKind::ViewImage)
    } else {
        None
    }
}

pub(super) fn tool_kind_from_name(name: &str) -> ToolKind {
    if name == "request_user_input" {
        ToolKind::RequestUserInput
    } else if name.contains("apply_patch") || name == "patch" {
        ToolKind::Patch
    } else if name.starts_with("mcp__") {
        ToolKind::Mcp
    } else if name.contains("web") || name.contains("search") {
        ToolKind::WebSearch
    } else if name.contains("command") || name.contains("shell") || name == "exec" {
        ToolKind::Command
    } else {
        ToolKind::Function
    }
}
