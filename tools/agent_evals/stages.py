"""Codex stage execution, event parsing, redaction, and behavior judging."""

from __future__ import annotations

import contextlib
import json
from collections.abc import Callable, Sequence
from pathlib import Path
from typing import Any

from .auth import (
    _copy_vault_to_runtime,
    _redact,
    _secret_values,
    _sync_runtime_auth,
    _validate_chatgpt_auth_file,
)
from .cases import _route_output_values, _write_model_output_schema
from .common import (
    USAGE_FIELDS,
    EvalCase,
    EvalInputError,
    EvalRuntimeError,
    Policy,
    ProcessResult,
    Runtime,
    _atomic_write_json,
    _read_utf8,
)
from .prompts import _assert_no_expectation_leak, _behavior_prompt, _judge_prompt
from .runtime import _isolated_environment, _prepare_runtime, _run_owned_process
from .scoring import _score_judge


def _tool_call_in_events(events: Sequence[Any]) -> bool:
    suspicious_types = {
        "command_execution",
        "computer_action",
        "dynamic_tool_call",
        "file_change",
        "function_call",
        "image_generation",
        "image_view",
        "mcp_tool_call",
        "plan_update",
        "request_user_input",
        "tool_call",
        "view_image",
        "web_search",
    }

    def visit(value: Any) -> bool:
        if isinstance(value, dict):
            item_type = value.get("type")
            if isinstance(item_type, str) and item_type.lower() in suspicious_types:
                return True
            if any(key in value for key in ("tool_call_id", "tool_name")):
                return True
            return any(visit(item) for item in value.values())
        if isinstance(value, list):
            return any(visit(item) for item in value)
        return False

    for event in events:
        if isinstance(event, dict):
            event_type = event.get("type")
            item = event.get("item")
            if (
                isinstance(event_type, str)
                and event_type.startswith("item.")
                and isinstance(item, dict)
                and item.get("type") not in {"agent_message", "error", "reasoning"}
            ):
                return True
        if visit(event):
            return True
    return False


def _item_error_messages(events: Sequence[Any]) -> list[str]:
    messages: list[str] = []
    for event in events:
        if not isinstance(event, dict):
            continue
        event_type = event.get("type")
        item = event.get("item")
        if (
            isinstance(event_type, str)
            and event_type.startswith("item.")
            and isinstance(item, dict)
            and item.get("type") == "error"
        ):
            message = item.get("message")
            messages.append(
                message if isinstance(message, str) and message else "error item"
            )
    return messages


def _event_failure_messages(events: Sequence[Any]) -> list[str]:
    messages = _item_error_messages(events)
    for event in events:
        if not isinstance(event, dict):
            continue
        event_type = event.get("type")
        if event_type not in {"error", "turn.failed", "turn.cancelled"}:
            continue
        message: Any = event.get("message")
        if not isinstance(message, str) or not message:
            error = event.get("error")
            if isinstance(error, dict):
                message = error.get("message")
        messages.append(
            message if isinstance(message, str) and message else str(event_type)
        )
    return messages


def _summarize_event_messages(messages: Sequence[str], limit: int = 1000) -> str:
    unique = list(dict.fromkeys(messages))
    summary = "; ".join(unique)
    return summary if len(summary) <= limit else summary[: limit - 3] + "..."


def _failed_event_in_events(events: Sequence[Any]) -> bool:
    return bool(_event_failure_messages(events))


def _parse_events(text: str) -> list[Any]:
    events: list[Any] = []
    for line_number, line in enumerate(text.splitlines(), start=1):
        if not line.strip():
            continue
        try:
            events.append(json.loads(line))
        except json.JSONDecodeError as exc:
            raise EvalRuntimeError(
                f"Codex event line {line_number} is invalid JSON: {exc}"
            ) from exc
    return events


def _turn_usage(events: Sequence[Any]) -> dict[str, int]:
    completed = [
        event
        for event in events
        if isinstance(event, dict) and event.get("type") == "turn.completed"
    ]
    if len(completed) != 1:
        raise EvalRuntimeError(
            f"Codex stage emitted {len(completed)} turn.completed events; expected 1"
        )
    usage = completed[0].get("usage")
    if not isinstance(usage, dict):
        raise EvalRuntimeError("Codex turn.completed event has no usage object")
    missing = set(USAGE_FIELDS) - set(usage)
    if missing:
        raise EvalRuntimeError(
            f"Codex turn.completed usage is missing fields: {sorted(missing)}"
        )
    result: dict[str, int] = {}
    for field in USAGE_FIELDS:
        value = usage[field]
        if isinstance(value, bool) or not isinstance(value, int) or value < 0:
            raise EvalRuntimeError(
                f"Codex turn.completed usage field {field} must be a non-negative integer"
            )
        result[field] = value
    return result


def _run_codex_stage(
    *,
    codex_bin: Path,
    runtime: Runtime,
    model: str,
    prompt: str,
    schema_path: Path,
    output_path: Path,
    events_path: Path,
    stderr_path: Path,
    timeout: int,
    secrets_to_redact: set[str],
    check_expectation_leak: bool = True,
) -> tuple[dict[str, Any], dict[str, int], ProcessResult]:
    if check_expectation_leak:
        _assert_no_expectation_leak(prompt)
    result = _run_owned_process(
        [
            str(codex_bin),
            "exec",
            "--ephemeral",
            "--ignore-rules",
            "--strict-config",
            "--skip-git-repo-check",
            "--output-schema",
            str(schema_path),
            "--json",
            "-o",
            str(output_path),
            "--model",
            model,
            "-C",
            str(runtime.fixture),
            "-",
        ],
        cwd=runtime.fixture,
        environment=_isolated_environment(runtime),
        timeout=timeout,
        stdin=prompt,
    )
    runtime_auth = runtime.codex_home / "auth.json"
    if runtime_auth.exists():
        with contextlib.suppress(EvalInputError):
            secrets_to_redact.update(
                _secret_values(
                    _validate_chatgpt_auth_file(runtime_auth, "runtime credential file")
                )
            )
    safe_stdout = _redact(result.stdout, secrets_to_redact, runtime.root)
    safe_stderr = _redact(result.stderr, secrets_to_redact, runtime.root)
    events_path.write_text(safe_stdout, encoding="utf-8")
    stderr_path.write_text(safe_stderr, encoding="utf-8")
    if result.timed_out:
        raise EvalRuntimeError(f"Codex stage timed out after {timeout} seconds")
    if result.returncode != 0:
        raise EvalRuntimeError(
            f"Codex stage exited with {result.returncode}; see {stderr_path}"
        )
    events = _parse_events(safe_stdout)
    event_failures = _event_failure_messages(events)
    if event_failures:
        raise EvalRuntimeError(
            "Codex emitted an error event during the eval stage: "
            + _summarize_event_messages(event_failures)
        )
    if _tool_call_in_events(events):
        raise EvalRuntimeError(
            "Codex attempted a tool call during a no-tool eval stage"
        )
    usage = _turn_usage(events)
    try:
        final = json.loads(
            _redact(_read_utf8(output_path), secrets_to_redact, runtime.root)
        )
    except json.JSONDecodeError as exc:
        raise EvalRuntimeError(f"Codex final output is invalid JSON: {exc}") from exc
    if not isinstance(final, dict):
        raise EvalRuntimeError("Codex final output must be a JSON object")
    _atomic_write_json(output_path, final)
    return final, usage, result


def _run_fresh_codex_stage(
    *,
    source_root: Path,
    codex_bin: Path,
    model: str,
    reasoning_effort: str,
    policy: Policy,
    vault: Path,
    prompt_builder: Callable[[Runtime], str],
    schema_filename: str,
    output_path: Path,
    events_path: Path,
    stderr_path: Path,
    timeout: int,
    secrets_to_redact: set[str],
    include_skill_instructions: bool,
    include_payload: bool = True,
    check_expectation_leak: bool = True,
    disabled_skill_names: Sequence[str] = (),
) -> tuple[dict[str, Any], dict[str, int], ProcessResult]:
    runtime = _prepare_runtime(
        source_root=source_root,
        codex_bin=codex_bin,
        model=model,
        reasoning_effort=reasoning_effort,
        policy=policy,
        timeout=min(timeout, 30),
        include_skill_instructions=include_skill_instructions,
        include_payload=include_payload,
        disabled_skill_names=disabled_skill_names,
    )
    auth_loaded = False
    try:
        secrets_to_redact.update(_copy_vault_to_runtime(vault, runtime))
        auth_loaded = True
        schema_path = runtime.root / schema_filename
        route_schema_values: dict[str, Sequence[str]] = {}
        if schema_filename == "route-result.schema.json":
            route_rules, route_skills = _route_output_values(source_root)
            route_schema_values = {
                "route_rules": route_rules,
                "route_skills": route_skills,
            }
        _write_model_output_schema(
            source_root / "tests" / "evals" / "schemas" / schema_filename,
            schema_path,
            **route_schema_values,
        )
        result = _run_codex_stage(
            codex_bin=codex_bin,
            runtime=runtime,
            model=model,
            prompt=prompt_builder(runtime),
            schema_path=schema_path,
            output_path=output_path,
            events_path=events_path,
            stderr_path=stderr_path,
            timeout=timeout,
            secrets_to_redact=secrets_to_redact,
            check_expectation_leak=check_expectation_leak,
        )
    except Exception:
        if auth_loaded:
            with contextlib.suppress(EvalInputError):
                secrets_to_redact.update(_sync_runtime_auth(runtime, vault))
        raise
    else:
        secrets_to_redact.update(_sync_runtime_auth(runtime, vault))
        return result
    finally:
        runtime.cleanup()


def _run_behavior_evaluation(
    *,
    source_root: Path,
    codex_bin: Path,
    model: str,
    reasoning_effort: str,
    judge_model: str,
    judge_reasoning_effort: str,
    policy: Policy,
    vault: Path,
    case: EvalCase,
    route: dict[str, Any],
    case_dir: Path,
    prefix: str,
    timeout: int,
    secrets_to_redact: set[str],
    disabled_skill_names: Sequence[str] = (),
) -> dict[str, Any]:
    subject, subject_usage, subject_process = _run_fresh_codex_stage(
        source_root=source_root,
        codex_bin=codex_bin,
        model=model,
        reasoning_effort=reasoning_effort,
        policy=policy,
        vault=vault,
        prompt_builder=lambda runtime: _behavior_prompt(case, runtime.fixture, route),
        schema_filename="behavior-result.schema.json",
        output_path=case_dir / f"{prefix}.final.json",
        events_path=case_dir / f"{prefix}.events.jsonl",
        stderr_path=case_dir / f"{prefix}.stderr.txt",
        timeout=timeout,
        secrets_to_redact=secrets_to_redact,
        include_skill_instructions=False,
        disabled_skill_names=disabled_skill_names,
    )
    if set(subject) != {"response"}:
        raise EvalRuntimeError("behavior response fields do not match the schema")
    response = subject["response"]
    if not isinstance(response, str) or not response.strip():
        raise EvalRuntimeError("behavior response must contain non-empty text")

    judged, judge_usage, judge_process = _run_fresh_codex_stage(
        source_root=source_root,
        codex_bin=codex_bin,
        model=judge_model,
        reasoning_effort=judge_reasoning_effort,
        policy=policy,
        vault=vault,
        prompt_builder=lambda _runtime: _judge_prompt(case, response),
        schema_filename="judge-result.schema.json",
        output_path=case_dir / f"{prefix}.judge.final.json",
        events_path=case_dir / f"{prefix}.judge.events.jsonl",
        stderr_path=case_dir / f"{prefix}.judge.stderr.txt",
        timeout=timeout,
        secrets_to_redact=secrets_to_redact,
        include_skill_instructions=False,
        include_payload=False,
        check_expectation_leak=False,
    )
    score = _score_judge(case, judged)
    _atomic_write_json(case_dir / f"{prefix}.score.json", score)
    return {
        "status": "passed" if score["passed"] else "failed",
        "duration_seconds": round(subject_process.duration_seconds, 3),
        "usage": subject_usage,
        "judge_duration_seconds": round(judge_process.duration_seconds, 3),
        "judge_usage": judge_usage,
        "score": score,
    }
