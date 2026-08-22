use super::*;

#[allow(clippy::too_many_arguments)]
pub(super) fn emit_diagnostic<S: ParseSink>(
    sink: &mut S,
    session: &mut SessionBuilder,
    severity: DiagnosticSeverity,
    code: &str,
    message: &str,
    line_no: Option<u64>,
    raw_ref_id: Option<String>,
) {
    session.diagnostic_count = session.diagnostic_count.saturating_add(1);
    sink.emit(ParserOutput::Diagnostic(ParserDiagnostic {
        severity,
        code: code.into(),
        message: message.into(),
        line_no,
        raw_ref_id,
    }));
}

pub(super) fn source_kind(payload: &Value) -> SourceKind {
    let originator = payload
        .get("originator")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .to_ascii_lowercase();
    let source = payload.get("source");
    if let Some(value) = source.and_then(Value::as_str) {
        return match value.to_ascii_lowercase().as_str() {
            "cli" => SourceKind::Cli,
            "vscode" | "vs_code" => SourceKind::Vscode,
            "exec" => SourceKind::Exec,
            "mcp" | "appserver" | "app-server" | "app_server" => SourceKind::AppServer,
            _ => source_from_originator(&originator),
        };
    }
    if let Some(object) = source.and_then(Value::as_object)
        && let Some(subagent) = object.get("subagent").or_else(|| object.get("sub_agent"))
    {
        if subagent.as_str() == Some("review")
            || subagent.get("review").is_some()
            || object.get("review").is_some()
        {
            return SourceKind::Review;
        }
        return SourceKind::Subagent;
    }
    source_from_originator(&originator)
}

pub(super) fn source_from_originator(originator: &str) -> SourceKind {
    if originator.contains("vscode") {
        SourceKind::Vscode
    } else if originator.contains("exec") {
        SourceKind::Exec
    } else if originator.contains("app-server") || originator.contains("appserver") {
        SourceKind::AppServer
    } else if originator.contains("cli") {
        SourceKind::Cli
    } else {
        SourceKind::Unknown
    }
}

pub(super) fn source_parent(source: Option<&Value>) -> Option<String> {
    let object = source?.as_object()?;
    let subagent = object.get("subagent").or_else(|| object.get("sub_agent"))?;
    subagent
        .get("thread_spawn")
        .and_then(|spawn| string_option(spawn, "parent_thread_id"))
        .or_else(|| string_option(subagent, "parent_thread_id"))
}

pub(super) fn payload_session_id(payload: &Value) -> Option<String> {
    ["session_id", "id"]
        .iter()
        .filter_map(|key| payload.get(*key).and_then(Value::as_str))
        .find_map(|value| Uuid::parse_str(value).ok().map(|id| id.to_string()))
}

pub(super) fn session_id_from_file(context: &ParseContext) -> String {
    session_id_from_filename(&context.file_name, &context.relative_path)
}

pub(crate) fn session_id_from_filename(file_name: &str, relative_path: &str) -> String {
    let stem = file_name.strip_suffix(".jsonl").unwrap_or(file_name);
    if stem.len() >= 36 {
        let candidate = &stem[stem.len() - 36..];
        if let Ok(id) = Uuid::parse_str(candidate) {
            return id.to_string();
        }
    }
    format!("s_{}", sha256(relative_path.as_bytes()))
}

pub(crate) fn timestamp_from_filename(file_name: &str) -> Option<i64> {
    let stem = file_name.strip_suffix(".jsonl")?.strip_prefix("rollout-")?;
    let timestamp = stem.get(..stem.len().checked_sub(37)?)?;
    ["%Y-%m-%dT%H-%M-%S%.f", "%Y-%m-%dT%H-%M-%S"]
        .iter()
        .find_map(|format| NaiveDateTime::parse_from_str(timestamp, format).ok())
        .map(|value| value.and_utc().timestamp_micros())
}

pub(super) fn parse_timestamp(value: &str) -> Option<i64> {
    DateTime::parse_from_rfc3339(value)
        .ok()
        .map(|timestamp| timestamp.with_timezone(&Utc).timestamp_micros())
}

pub(super) fn role_field(payload: &Value) -> Option<MessageRole> {
    match payload.get("role").and_then(Value::as_str)? {
        "user" => Some(MessageRole::User),
        "assistant" => Some(MessageRole::Assistant),
        "developer" => Some(MessageRole::Developer),
        "system" => Some(MessageRole::System),
        _ => None,
    }
}

pub(super) fn phase_field(payload: &Value) -> Option<Phase> {
    match payload.get("phase").and_then(Value::as_str)? {
        "commentary" => Some(Phase::Commentary),
        "final" | "final_answer" => Some(Phase::Final),
        "analysis" => Some(Phase::Analysis),
        _ => Some(Phase::Unknown),
    }
}

pub(super) fn status_field(payload: &Value) -> Option<ToolStatus> {
    match payload.get("status").and_then(Value::as_str)? {
        "pending" => Some(ToolStatus::Pending),
        "in_progress" | "running" => Some(ToolStatus::Running),
        "completed" | "succeeded" | "success" => Some(ToolStatus::Succeeded),
        "failed" | "error" => Some(ToolStatus::Failed),
        "interrupted" | "cancelled" | "canceled" | "declined" => Some(ToolStatus::Interrupted),
        _ => Some(ToolStatus::Unknown),
    }
}

pub(super) fn call_id(payload: &Value) -> Option<String> {
    string_option(payload, "call_id")
        .or_else(|| string_option(payload, "id").filter(|id| id.starts_with("call")))
}

pub(super) fn item_id(payload: &Value) -> Option<String> {
    string_option(payload, "id").filter(|id| !id.is_empty())
}

pub(super) fn add_source_item_id(entry: &mut NormalizedEntry, payload: &Value) {
    if let Some(id) = item_id(payload) {
        entry
            .metadata
            .insert("sourceItemId".into(), Value::String(id));
    }
}

pub(super) fn add_execution_attribution_metadata(entry: &mut NormalizedEntry, payload: &Value) {
    for (source, target) in [("plugin_id", "pluginId"), ("script_path", "scriptPath")] {
        if let Some(value) = string_option(payload, source).filter(|value| !value.is_empty()) {
            entry.metadata.insert(target.into(), Value::String(value));
        }
    }
}

pub(super) fn add_tool_capability_metadata(entry: &mut NormalizedEntry, payload: &Value) {
    for (snake_case, camel_case) in [
        ("read_only_hint", "readOnlyHint"),
        ("transparent_background", "transparentBackground"),
    ] {
        if let Some(value) = payload
            .get(snake_case)
            .or_else(|| payload.get(camel_case))
            .and_then(Value::as_bool)
        {
            entry.metadata.insert(camel_case.into(), Value::Bool(value));
        }
    }
}

pub(super) fn add_response_item_metadata(entry: &mut NormalizedEntry, payload: &Value) {
    add_tool_capability_metadata(entry, payload);

    if let Some(encrypted_args) = payload
        .get("encrypted_function_args")
        .or_else(|| payload.get("encryptedFunctionArgs"))
        .and_then(Value::as_array)
    {
        entry.metadata.insert(
            "encryptedFunctionArgsCount".into(),
            Value::from(encrypted_args.len()),
        );
    }

    let Some(metadata) = payload
        .get("internal_chat_message_metadata_passthrough")
        .or_else(|| payload.get("internalChatMessageMetadataPassthrough"))
        .and_then(Value::as_object)
    else {
        return;
    };
    if let Some(turn_id) = metadata
        .get("turn_id")
        .or_else(|| metadata.get("turnId"))
        .and_then(Value::as_str)
        .filter(|turn_id| !turn_id.is_empty())
    {
        entry
            .metadata
            .insert("turnId".into(), Value::String(turn_id.into()));
    }
    if let Some(create_time) = metadata
        .get("create_time")
        .or_else(|| metadata.get("createTime"))
        .filter(|value| value.is_number())
    {
        entry
            .metadata
            .insert("createTime".into(), create_time.clone());
    }

    let Some(executed_calls) = metadata
        .get("executed_tool_calls")
        .or_else(|| metadata.get("executedToolCalls"))
        .and_then(Value::as_array)
    else {
        return;
    };
    entry.metadata.insert(
        "executedToolCallCount".into(),
        Value::from(executed_calls.len()),
    );
    let names = executed_calls
        .iter()
        .filter_map(|call| call.get("name").and_then(Value::as_str))
        .filter(|name| !name.is_empty())
        .map(|name| Value::String(name.into()))
        .collect::<Vec<_>>();
    if !names.is_empty() {
        entry
            .metadata
            .insert("executedToolCallNames".into(), Value::Array(names));
    }
    let omitted_count = executed_calls
        .iter()
        .filter_map(|call| call.get("arguments"))
        .filter_map(|arguments| arguments.get("_codex_executed_tool_call_truncated"))
        .filter_map(|truncation| truncation.get("omitted_calls"))
        .filter_map(Value::as_u64)
        .fold(0_u64, u64::saturating_add);
    if omitted_count > 0 {
        entry.metadata.insert(
            "executedToolCallOmittedCount".into(),
            Value::from(omitted_count),
        );
    }
}

pub(super) fn add_response_item_envelope_metadata(result: &mut NormalizeResult, metadata: &Value) {
    let Some(client_authored) = metadata
        .get("client_authored")
        .or_else(|| metadata.get("clientAuthored"))
        .and_then(Value::as_bool)
    else {
        return;
    };
    let add = |entry: &mut NormalizedEntry| {
        entry
            .metadata
            .insert("clientAuthored".into(), Value::Bool(client_authored));
    };
    match result {
        NormalizeResult::Entry(entry) | NormalizeResult::Unknown(entry, _) => add(entry),
        NormalizeResult::Entries(entries) => entries.iter_mut().for_each(add),
        NormalizeResult::None => {}
    }
}

pub(super) fn add_image_generation_failure_metadata(entry: &mut NormalizedEntry, payload: &Value) {
    if let Some(failure) = payload.get("failure").filter(|value| !value.is_null()) {
        entry.metadata.insert(
            "imageGenerationFailure".into(),
            sanitize_for_display(failure),
        );
    }
}

pub(super) fn add_event_timing_metadata(entry: &mut NormalizedEntry, payload: &Value) {
    for (snake_case, camel_case) in [
        ("started_at_ms", "startedAtMs"),
        ("completed_at_ms", "completedAtMs"),
    ] {
        if let Some(value) = payload
            .get(snake_case)
            .or_else(|| payload.get(camel_case))
            .and_then(Value::as_i64)
        {
            entry.metadata.insert(camel_case.into(), Value::from(value));
        }
    }
}

pub(super) fn source_item_id(entry: &NormalizedEntry) -> Option<&str> {
    entry.metadata.get("sourceItemId").and_then(Value::as_str)
}

pub(super) fn string_option(payload: &Value, key: &str) -> Option<String> {
    payload.get(key).and_then(Value::as_str).map(str::to_owned)
}
