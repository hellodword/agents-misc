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

import argparse
import json
import os
import sys
import time
import urllib.error
import urllib.parse
import urllib.request
from dataclasses import dataclass
from typing import Any

DEFAULT_SERVER_URL = "http://172.17.0.1:8765/hook"
DEFAULT_TIMEOUT_SEC = 1.5
DEFAULT_PREVIEW_LIMIT = 500
DEFAULT_MAX_STDIN_BYTES = 2 * 1024 * 1024
DEFAULT_MAX_REQUEST_BYTES = 2 * 1024 * 1024
MODEL_REQUEST_EVENTS = {"RequestError", "AbnormalStop"}
MODEL_REQUEST_OPERATIONS = {"sampling", "localCompact", "remoteCompact"}
MODEL_REQUEST_NEXT_ACTIONS = {"retry", "fallback", "stop"}
MODEL_REQUEST_ERROR_CATEGORIES = {
    "transport",
    "timeout",
    "rateLimit",
    "usageLimit",
    "contextWindow",
    "policy",
    "sandbox",
    "invalidRequest",
    "server",
    "internal",
    "other",
}
REQUEST_ERROR_FIELDS = {
    "attempt",
    "cwd",
    "endpointPath",
    "error",
    "hookEventName",
    "model",
    "nextAction",
    "operation",
    "provider",
    "sessionId",
    "transcriptPath",
    "turnId",
}
ABNORMAL_STOP_FIELDS = REQUEST_ERROR_FIELDS | {
    "approvalPolicy",
    "goalMode",
    "reason",
    "sandboxMode",
}


@dataclass(frozen=True)
class Options:
    url: str
    timeout: float
    events: str
    preview_limit: int
    max_stdin_bytes: int
    max_request_bytes: int
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
            raise TypeError("hook stdin JSON must be an object")
        normalized = normalize_hook_payload(payload, options.preview_limit)
    except (TypeError, UnicodeError, ValueError) as exc:
        log(verbose or strict, f"codex hook forwarder: invalid stdin: {exc}")
        return 1 if strict else 0

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
        post_json(
            options.url,
            message,
            options.timeout,
            options.max_request_bytes,
        )
    except (OSError, RuntimeError, TypeError, ValueError, urllib.error.URLError) as exc:
        log(verbose or strict, f"codex hook forwarder: delivery failed: {exc}")
        return 1 if strict else 0

    log(verbose, f"codex hook forwarder: delivered {event_name}")
    return 0


def load_options() -> Options:
    parser = argparse.ArgumentParser(
        description=(
            "Forward Codex hook stdin to CODEX_HOOK_SERVER_URL. Runtime options "
            "are configured with CODEX_HOOK_FORWARDER_* environment variables."
        )
    )
    parser.parse_args()

    options = Options(
        url=os.environ.get("CODEX_HOOK_SERVER_URL", DEFAULT_SERVER_URL),
        timeout=float_env("CODEX_HOOK_FORWARDER_TIMEOUT", DEFAULT_TIMEOUT_SEC),
        events=os.environ.get("CODEX_HOOK_FORWARDER_EVENTS", "*"),
        preview_limit=int_env(
            "CODEX_HOOK_FORWARDER_PREVIEW_LIMIT", DEFAULT_PREVIEW_LIMIT
        ),
        max_stdin_bytes=int_env(
            "CODEX_HOOK_FORWARDER_MAX_STDIN_BYTES", DEFAULT_MAX_STDIN_BYTES
        ),
        max_request_bytes=int_env(
            "CODEX_HOOK_FORWARDER_MAX_REQUEST_BYTES", DEFAULT_MAX_REQUEST_BYTES
        ),
        include_raw=truthy(os.environ.get("CODEX_HOOK_FORWARDER_INCLUDE_RAW")),
        strict=truthy(os.environ.get("CODEX_HOOK_FORWARDER_STRICT")),
        verbose=truthy(os.environ.get("CODEX_HOOK_FORWARDER_VERBOSE")),
    )
    validate_options(options)
    return options


def validate_options(options: Options) -> None:
    parsed = urllib.parse.urlsplit(options.url)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        raise ValueError("CODEX_HOOK_SERVER_URL must be an absolute HTTP(S) URL")
    if options.timeout <= 0:
        raise ValueError("CODEX_HOOK_FORWARDER_TIMEOUT must be greater than zero")
    if options.preview_limit < 0:
        raise ValueError("CODEX_HOOK_FORWARDER_PREVIEW_LIMIT must not be negative")
    if options.max_stdin_bytes <= 0:
        raise ValueError(
            "CODEX_HOOK_FORWARDER_MAX_STDIN_BYTES must be greater than zero"
        )
    if options.max_request_bytes <= 0:
        raise ValueError(
            "CODEX_HOOK_FORWARDER_MAX_REQUEST_BYTES must be greater than zero"
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


def post_json(
    url: str,
    payload: dict[str, Any],
    timeout: float,
    max_request_bytes: int,
) -> None:
    body = json.dumps(payload, ensure_ascii=False, separators=(",", ":")).encode(
        "utf-8"
    )
    if len(body) > max_request_bytes:
        raise ValueError(f"forwarded request exceeded {max_request_bytes} bytes")
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


def normalize_hook_payload(
    payload: dict[str, Any], preview_limit: int
) -> dict[str, Any]:
    event_name = payload.get("hookEventName")
    if event_name in MODEL_REQUEST_EVENTS:
        validate_model_request_payload(payload)
        return dict(payload)

    summary: dict[str, Any] = {
        "hookEventName": event_name,
        "sessionId": payload.get("sessionId"),
        "turnId": payload.get("turnId"),
        "agentId": payload.get("agentId"),
        "agentType": payload.get("agentType"),
        "transcriptPath": payload.get("transcriptPath"),
        "agentTranscriptPath": payload.get("agentTranscriptPath"),
        "cwd": payload.get("cwd"),
        "model": payload.get("model"),
        "permissionMode": payload.get("permissionMode"),
        "source": payload.get("source"),
        "trigger": payload.get("trigger"),
        "toolName": payload.get("toolName"),
        "toolUseId": payload.get("toolUseId"),
        "provider": payload.get("provider"),
        "operation": payload.get("operation"),
        "endpointPath": payload.get("endpointPath"),
        "attempt": payload.get("attempt"),
        "nextAction": payload.get("nextAction"),
        "error": payload.get("error"),
        "goalMode": payload.get("goalMode"),
        "approvalPolicy": payload.get("approvalPolicy"),
        "sandboxMode": payload.get("sandboxMode"),
        "reason": payload.get("reason"),
        "stopHookActive": payload.get("stopHookActive"),
    }

    tool_input = payload.get("toolInput")
    tool_response = payload.get("toolResponse")
    prompt = payload.get("prompt")
    last_assistant_message = payload.get("lastAssistantMessage")
    codex_error_info = payload.get("codexErrorInfo")

    if tool_input is not None:
        summary["toolInputPreview"] = preview_value(tool_input, preview_limit)
        command = command_from_tool_input(tool_input)
        if command:
            summary["toolCommand"] = command
    if tool_response is not None:
        summary["toolResponsePreview"] = preview_value(tool_response, preview_limit)
    if prompt is not None:
        summary["promptPreview"] = preview_value(prompt, preview_limit)
    if last_assistant_message is not None:
        summary["lastAssistantMessagePreview"] = preview_value(
            last_assistant_message, preview_limit
        )
    if codex_error_info is not None:
        summary["codexErrorInfoPreview"] = preview_value(
            codex_error_info, preview_limit
        )

    return {key: value for key, value in summary.items() if value is not None}


def validate_model_request_payload(payload: dict[str, Any]) -> None:
    event_name = payload.get("hookEventName")
    expected_fields = (
        REQUEST_ERROR_FIELDS if event_name == "RequestError" else ABNORMAL_STOP_FIELDS
    )
    missing = sorted(expected_fields - set(payload))
    unknown = sorted(set(payload) - expected_fields)
    if missing:
        raise ValueError(f"{event_name} is missing fields: {', '.join(missing)}")
    if unknown:
        raise ValueError(f"{event_name} has unknown fields: {', '.join(unknown)}")
    for field in ["sessionId", "turnId", "cwd", "model", "provider", "endpointPath"]:
        if not isinstance(payload.get(field), str):
            raise TypeError(f"{event_name} {field} must be a string")
    transcript_path = payload.get("transcriptPath")
    if transcript_path is not None and not isinstance(transcript_path, str):
        raise ValueError(f"{event_name} transcriptPath must be a string or null")
    if payload.get("endpointPath") not in {"/responses", "/responses/compact"}:
        raise ValueError(f"{event_name} endpointPath is invalid")
    operation = payload.get("operation")
    if operation not in MODEL_REQUEST_OPERATIONS:
        raise ValueError(f"{event_name} operation is invalid")
    next_action = payload.get("nextAction")
    if next_action not in MODEL_REQUEST_NEXT_ACTIONS:
        raise ValueError(f"{event_name} nextAction is invalid")
    attempt = payload.get("attempt")
    if isinstance(attempt, bool) or not isinstance(attempt, int) or attempt < 0:
        raise ValueError(f"{event_name} attempt must be a non-negative integer")
    error = payload.get("error")
    if not isinstance(error, dict):
        raise TypeError(f"{event_name} error must be an object")
    if set(error) != {"category", "message"}:
        raise ValueError(f"{event_name} error must contain only category and message")
    if error.get("category") not in MODEL_REQUEST_ERROR_CATEGORIES:
        raise ValueError(f"{event_name} error.category is invalid")
    if not isinstance(error.get("message"), str):
        raise TypeError(f"{event_name} error.message must be a string")
    if event_name == "AbnormalStop":
        if not isinstance(payload.get("goalMode"), bool):
            raise ValueError("AbnormalStop goalMode must be a boolean")
        for field in ["approvalPolicy", "sandboxMode"]:
            if not isinstance(payload.get(field), str):
                raise TypeError(f"AbnormalStop {field} must be a string")
        if payload.get("reason") != "requestError":
            raise ValueError("AbnormalStop reason is invalid")


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
        return "warning" if summary.get("nextAction") != "stop" else "error"
    if event_name in {"PreToolUse", "PermissionRequest", "UserPromptSubmit"}:
        return "info"
    return "info"


def title_for(summary: dict[str, Any]) -> str:
    event_name = str(summary.get("hookEventName") or "Hook")
    if event_name == "RequestError":
        next_action = summary.get("nextAction")
        if next_action in {"retry", "fallback"}:
            return f"Codex request error: {next_action} after attempt {summary.get('attempt')}"
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
            summary.get("operation"),
            summary.get("endpointPath"),
        ]
        context = " ".join(str(part) for part in parts if part)
        error_value = summary.get("error")
        error_fields = error_value if isinstance(error_value, dict) else {}
        error = ": ".join(
            str(part)
            for part in [error_fields.get("category"), error_fields.get("message")]
            if part
        )
        return " - ".join(part for part in [context, error] if part)
    if event_name in {"PreToolUse", "PostToolUse", "PermissionRequest"}:
        return str(
            summary.get("toolCommand")
            or summary.get("toolInputPreview")
            or summary.get("cwd")
            or ""
        )
    if event_name == "UserPromptSubmit":
        return str(summary.get("promptPreview") or "")
    if event_name in {"Stop", "SubagentStop"}:
        return str(
            summary.get("lastAssistantMessagePreview") or summary.get("cwd") or ""
        )
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
