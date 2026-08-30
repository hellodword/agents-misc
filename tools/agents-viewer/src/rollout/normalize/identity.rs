use super::*;

pub(super) fn event_title(event: &str) -> &str {
    match event {
        "task_started" | "turn_started" => "Turn started",
        "task_complete" | "turn_complete" => "Turn completed",
        "turn_aborted" => "Turn interrupted",
        "context_compacted" => "Conversation compacted",
        "thread_rolled_back" => "Conversation rolled back",
        "entered_review_mode" => "Review started",
        "exited_review_mode" => "Review completed",
        "thread_goal_updated" => "Goal updated",
        "token_count" => "Token usage",
        "session_configured" => "Session configured",
        "mcp_startup_update" => "MCP startup",
        "mcp_startup_complete" => "MCP ready",
        "exec_command_begin" | "exec_command_output_delta" | "exec_command_end" => "Command",
        "terminal_interaction" => "Terminal",
        "patch_apply_begin" | "patch_apply_updated" | "patch_apply_end" => "Patch",
        "mcp_tool_call_begin" | "mcp_tool_call_end" => "MCP tool",
        "web_search_begin" | "web_search_end" => "Web search",
        "dynamic_tool_call_request" | "dynamic_tool_call_response" => "Dynamic tool",
        "view_image_tool_call" => "Image attachment",
        "image_generation_begin" | "image_generation_end" => "Image generation",
        _ => "Activity",
    }
}

pub(super) fn is_known_envelope(kind: &str) -> bool {
    matches!(
        kind,
        "session_meta"
            | "turn_context"
            | "world_state"
            | "security_risk_score"
            | "inter_agent_communication"
            | "inter_agent_communication_metadata"
            | "event_msg"
            | "response_item"
            | "realtime_item"
            | "compacted"
    )
}

pub(super) fn raw_ref_id(session_id: &str, offset: u64, length: u64) -> String {
    format!(
        "r_{}",
        sha256(format!("{session_id}\0{offset}\0{length}").as_bytes())
    )
}

pub(super) fn sha256(bytes: &[u8]) -> String {
    let digest = Sha256::digest(bytes);
    let mut output = String::with_capacity(digest.len() * 2);
    for byte in digest {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}

pub(super) fn hex_preview(bytes: &[u8]) -> String {
    let mut output = String::with_capacity(bytes.len().min(4096) * 2);
    for byte in bytes.iter().take(4096) {
        use std::fmt::Write as _;
        write!(&mut output, "{byte:02x}").expect("writing to String cannot fail");
    }
    output
}
