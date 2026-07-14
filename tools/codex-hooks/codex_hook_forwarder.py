#!/usr/bin/env python3
"""Forward Codex hook stdin to the local notification server.

Codex hook command example:

    python3 /home/<user>/.codex/codex_hook_forwarder.py

The script intentionally writes nothing to stdout so it stays neutral for all
hook output contracts. Delivery failures are non-fatal unless strict mode is
enabled with CODEX_HOOK_FORWARDER_STRICT=1. Runtime options are configured
only with environment variables.
"""

from __future__ import annotations

from dataclasses import dataclass
import json
import os
import sys
import time
import urllib.error
import urllib.request
from typing import Any


DEFAULT_SERVER_URL = "http://172.17.0.1:8765/hook"
DEFAULT_TIMEOUT_SEC = 1.5
DEFAULT_PREVIEW_LIMIT = 500
DEFAULT_MAX_STDIN_BYTES = 2 * 1024 * 1024


@dataclass(frozen=True)
class Options:
    url: str
    timeout: float
    events: str
    preview_limit: int
    max_stdin_bytes: int
    include_raw: bool
    strict: bool
    verbose: bool


def main() -> int:
    try:
        options = load_options()
    except ValueError as exc:
        log(True, f"codex hook forwarder: invalid configuration: {exc}")
        return 2

    strict = options.strict
    verbose = options.verbose

    try:
        raw_text = read_stdin(options.max_stdin_bytes)
        payload = json.loads(raw_text)
        if not isinstance(payload, dict):
            raise ValueError("hook stdin JSON must be an object")
    except Exception as exc:
        log(verbose or strict, f"codex hook forwarder: invalid stdin: {exc}")
        return 1 if strict else 0

    normalized = normalize_hook_payload(payload, options.preview_limit)
    event_name = str(normalized.get("hookEventName") or "")
    if not event_matches(event_name, options.events):
        log(verbose, f"codex hook forwarder: skipped event {event_name!r}")
        return 0

    message = {
        "protocolVersion": 1,
        "source": "codex_hook_forwarder",
        "sentAt": int(time.time()),
        "hookEventName": event_name,
        "severity": severity_for(normalized),
        "title": title_for(normalized),
        "message": message_for(normalized),
        "summary": normalized,
    }
    if options.include_raw:
        message["rawPayload"] = payload

    try:
        post_json(options.url, message, options.timeout)
    except Exception as exc:
        log(verbose or strict, f"codex hook forwarder: delivery failed: {exc}")
        return 1 if strict else 0

    log(verbose, f"codex hook forwarder: delivered {event_name}")
    return 0


def load_options() -> Options:
    if len(sys.argv) > 1:
        raise ValueError(
            "command-line arguments are not supported; use CODEX_HOOK_* environment variables"
        )

    return Options(
        url=os.environ.get("CODEX_HOOK_SERVER_URL", DEFAULT_SERVER_URL),
        timeout=float_env("CODEX_HOOK_FORWARDER_TIMEOUT", DEFAULT_TIMEOUT_SEC),
        events=os.environ.get("CODEX_HOOK_FORWARDER_EVENTS", "*"),
        preview_limit=int_env(
            "CODEX_HOOK_FORWARDER_PREVIEW_LIMIT", DEFAULT_PREVIEW_LIMIT),
        max_stdin_bytes=int_env(
            "CODEX_HOOK_FORWARDER_MAX_STDIN_BYTES", DEFAULT_MAX_STDIN_BYTES),
        include_raw=truthy(os.environ.get(
            "CODEX_HOOK_FORWARDER_INCLUDE_RAW", "1")),
        strict=truthy(os.environ.get("CODEX_HOOK_FORWARDER_STRICT")),
        verbose=truthy(os.environ.get("CODEX_HOOK_FORWARDER_VERBOSE")),
    )


def float_env(name: str, default: float) -> float:
    value = os.environ.get(name)
    if value is None:
        return default
    try:
        return float(value)
    except ValueError as exc:
        raise ValueError(f"{name} must be a number") from exc


def int_env(name: str, default: int) -> int:
    value = os.environ.get(name)
    if value is None:
        return default
    try:
        return int(value)
    except ValueError as exc:
        raise ValueError(f"{name} must be an integer") from exc


def read_stdin(max_bytes: int) -> str:
    data = sys.stdin.buffer.read(max_bytes + 1)
    if len(data) > max_bytes:
        raise ValueError(f"stdin exceeded {max_bytes} bytes")
    if not data:
        raise ValueError("empty stdin")
    return data.decode("utf-8")


def post_json(url: str, payload: dict[str, Any], timeout: float) -> None:
    body = json.dumps(payload, ensure_ascii=False,
                      separators=(",", ":")).encode("utf-8")
    request = urllib.request.Request(
        url,
        data=body,
        headers={
            "Content-Type": "application/json",
            "User-Agent": "codex-hook-forwarder/1",
        },
        method="POST",
    )
    with urllib.request.urlopen(request, timeout=timeout) as response:
        if response.status >= 400:
            raise RuntimeError(f"server returned HTTP {response.status}")


def normalize_hook_payload(payload: dict[str, Any], preview_limit: int) -> dict[str, Any]:
    event_name = first(payload, "hookEventName", "hook_event_name")
    summary: dict[str, Any] = {
        "hookEventName": event_name,
        "sessionId": first(payload, "sessionId", "session_id"),
        "turnId": first(payload, "turnId", "turn_id"),
        "agentId": first(payload, "agentId", "agent_id"),
        "agentType": first(payload, "agentType", "agent_type"),
        "transcriptPath": first(payload, "transcriptPath", "transcript_path"),
        "agentTranscriptPath": first(payload, "agentTranscriptPath", "agent_transcript_path"),
        "cwd": payload.get("cwd"),
        "model": payload.get("model"),
        "permissionMode": first(payload, "permissionMode", "permission_mode"),
        "source": payload.get("source"),
        "trigger": payload.get("trigger"),
        "toolName": first(payload, "toolName", "tool_name"),
        "toolUseId": first(payload, "toolUseId", "tool_use_id"),
        "provider": payload.get("provider"),
        "requestType": first(payload, "requestType", "request_type"),
        "requestSubtype": first(payload, "requestSubtype", "request_subtype"),
        "endpointPath": first(payload, "endpointPath", "endpoint_path"),
        "retryAttempt": first(payload, "retryAttempt", "retry_attempt"),
        "maxRetries": first(payload, "maxRetries", "max_retries"),
        "willRetry": first(payload, "willRetry", "will_retry"),
        "nextRetryAttempt": first(payload, "nextRetryAttempt", "next_retry_attempt"),
        "errorRetryable": first(payload, "errorRetryable", "error_retryable"),
        "errorKind": first(payload, "errorKind", "error_kind"),
        "errorMessage": first(payload, "errorMessage", "error_message"),
        "reason": payload.get("reason"),
        "stopHookActive": first(payload, "stopHookActive", "stop_hook_active"),
    }

    tool_input = first(payload, "toolInput", "tool_input")
    tool_response = first(payload, "toolResponse", "tool_response")
    prompt = payload.get("prompt")
    last_assistant_message = first(
        payload, "lastAssistantMessage", "last_assistant_message")
    codex_error_info = first(payload, "codexErrorInfo", "codex_error_info")

    if tool_input is not None:
        summary["toolInputPreview"] = preview_value(tool_input, preview_limit)
        command = command_from_tool_input(tool_input)
        if command:
            summary["toolCommand"] = command
    if tool_response is not None:
        summary["toolResponsePreview"] = preview_value(
            tool_response, preview_limit)
    if prompt is not None:
        summary["promptPreview"] = preview_value(prompt, preview_limit)
    if last_assistant_message is not None:
        summary["lastAssistantMessagePreview"] = preview_value(
            last_assistant_message, preview_limit)
    if codex_error_info is not None:
        summary["codexErrorInfoPreview"] = preview_value(
            codex_error_info, preview_limit)

    return {key: value for key, value in summary.items() if value is not None}


def first(mapping: dict[str, Any], *keys: str) -> Any:
    for key in keys:
        if key in mapping:
            return mapping[key]
    return None


def command_from_tool_input(value: Any) -> str | None:
    if isinstance(value, dict):
        command = value.get("command")
        if isinstance(command, str):
            return command
    return None


def preview_value(value: Any, limit: int) -> str:
    if isinstance(value, str):
        text = value
    else:
        text = json.dumps(value, ensure_ascii=False, sort_keys=True)
    text = text.replace("\r", "\\r")
    if len(text) <= limit:
        return text
    return text[: max(0, limit - 3)] + "..."


def event_matches(event_name: str, events_spec: str) -> bool:
    events = {item.strip() for item in events_spec.split(",") if item.strip()}
    return not events or "*" in events or event_name in events


def severity_for(summary: dict[str, Any]) -> str:
    event_name = summary.get("hookEventName")
    if event_name == "AbnormalStop":
        return "error"
    if event_name == "RequestError":
        return "warning" if summary.get("willRetry") else "error"
    if event_name in {"PreToolUse", "PermissionRequest", "UserPromptSubmit"}:
        return "info"
    return "info"


def title_for(summary: dict[str, Any]) -> str:
    event_name = str(summary.get("hookEventName") or "Hook")
    if event_name == "RequestError":
        if summary.get("willRetry"):
            attempt = summary.get(
                "nextRetryAttempt") or summary.get("retryAttempt")
            maximum = summary.get("maxRetries")
            suffix = f" retry {attempt}/{maximum}" if attempt is not None and maximum is not None else " retrying"
            return "Codex request error:" + suffix
        return "Codex request failed"
    if event_name == "AbnormalStop":
        return "Codex abnormal stop"
    if event_name in {"PreToolUse", "PostToolUse", "PermissionRequest"}:
        tool_name = summary.get("toolName") or "tool"
        return f"Codex {event_name}: {tool_name}"
    if event_name in {"SubagentStart", "SubagentStop"}:
        agent_type = summary.get("agentType") or "subagent"
        return f"Codex {event_name}: {agent_type}"
    return f"Codex {event_name}"


def message_for(summary: dict[str, Any]) -> str:
    event_name = summary.get("hookEventName")
    if event_name in {"RequestError", "AbnormalStop"}:
        parts = [
            summary.get("provider"),
            summary.get("model"),
            summary.get("requestType"),
            summary.get("requestSubtype"),
            summary.get("endpointPath"),
        ]
        context = " ".join(str(part) for part in parts if part)
        error = ": ".join(
            str(part) for part in [summary.get("errorKind"), summary.get("errorMessage")] if part
        )
        return " - ".join(part for part in [context, error] if part)
    if event_name in {"PreToolUse", "PostToolUse", "PermissionRequest"}:
        return str(summary.get("toolCommand") or summary.get("toolInputPreview") or summary.get("cwd") or "")
    if event_name == "UserPromptSubmit":
        return str(summary.get("promptPreview") or "")
    if event_name in {"Stop", "SubagentStop"}:
        return str(summary.get("lastAssistantMessagePreview") or summary.get("cwd") or "")
    return str(summary.get("cwd") or summary.get("model") or "")


def truthy(value: str | None) -> bool:
    if value is None:
        return False
    return value.strip().lower() in {"1", "true", "yes", "on"}


def log(enabled: bool, message: str) -> None:
    if enabled:
        print(message, file=sys.stderr)


if __name__ == "__main__":
    raise SystemExit(main())
