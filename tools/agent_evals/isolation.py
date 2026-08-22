"""Prompt-source and versioned no-execution-tool isolation checks."""

from __future__ import annotations

import http.server
import json
import os
import shutil
import threading
from pathlib import Path
from typing import Any

from .common import (
    MAX_HTTP_REQUEST_BYTES,
    SAFE_ENV_KEYS,
    SCHEMA_VERSION,
    EvalInputError,
    EvalRuntimeError,
    Policy,
    Runtime,
    _is_relative_to,
    _read_json_object,
    _read_utf8,
)
from .runtime import (
    _debug_prompt,
    _extract_skill_entries,
    _flatten_prompt_text,
    _isolated_environment,
    _prepare_runtime,
    _run_owned_process,
)
from .stages import _item_error_messages, _parse_events, _summarize_event_messages


def _loopback_environment(runtime: Runtime) -> dict[str, str]:
    environment = _isolated_environment(
        runtime, {"AGENT_EVAL_FAKE_KEY": "non-secret-probe-value"}
    )
    for key in (
        "ALL_PROXY",
        "HTTPS_PROXY",
        "HTTP_PROXY",
        "all_proxy",
        "https_proxy",
        "http_proxy",
    ):
        environment.pop(key, None)
    environment["NO_PROXY"] = "127.0.0.1,localhost"
    environment["no_proxy"] = "127.0.0.1,localhost"
    return environment


def _verify_prompt_sources(
    codex_bin: Path, runtime: Runtime, source_root: Path, timeout: int
) -> dict[str, Any]:
    value, _ = _debug_prompt(
        codex_bin, runtime, "prompt-source-isolation-probe", timeout
    )
    serialized = _flatten_prompt_text(value)
    entries = _extract_skill_entries(value)
    fixture_skills = (runtime.fixture / ".agents" / "skills").resolve()
    expected_paths = {
        path.resolve()
        for path in (runtime.fixture / ".agents" / "skills").glob("*/SKILL.md")
    }
    actual_paths: set[Path] = set()
    non_file_entries: list[str] = []
    for name, locator_type, locator in entries:
        if locator_type != "file":
            non_file_entries.append(f"{name}:{locator_type}:{locator}")
            continue
        candidate = Path(locator).resolve()
        if not _is_relative_to(candidate, fixture_skills):
            raise EvalRuntimeError(
                f"prompt contains an external skill source: {candidate}"
            )
        actual_paths.add(candidate)
    if non_file_entries:
        raise EvalRuntimeError(
            f"prompt contains non-file skill sources: {sorted(non_file_entries)}"
        )
    if actual_paths != expected_paths:
        raise EvalRuntimeError(
            "prompt skill sources differ from the synthetic payload: "
            f"missing={sorted(str(path) for path in expected_paths - actual_paths)}, "
            f"unexpected={sorted(str(path) for path in actual_paths - expected_paths)}"
        )

    agents_text = _read_utf8(source_root / "AGENTS.md").strip()
    if agents_text not in serialized:
        raise EvalRuntimeError(
            "synthetic AGENTS.md content is absent from model-visible input"
        )
    source_root_text = str(source_root.resolve())
    if (
        source_root_text != str(runtime.fixture.resolve())
        and source_root_text in serialized
    ):
        raise EvalRuntimeError(
            "model-visible input contains the maintenance repository path"
        )
    if "# Agent Rules Kit Upstream" in serialized:
        raise EvalRuntimeError(
            "model-visible input contains the project maintenance overlay"
        )

    return {
        "status": "passed",
        "agents_md": "AGENTS.md",
        "skill_count": len(actual_paths),
        "skill_sources": sorted(
            str(path.relative_to(runtime.fixture)) for path in actual_paths
        ),
        "disabled_external_skill_count": len(runtime.external_skill_paths),
    }


def _verify_behavior_prompt_sources(
    codex_bin: Path, runtime: Runtime, source_root: Path, timeout: int
) -> dict[str, Any]:
    value, _ = _debug_prompt(
        codex_bin, runtime, "behavior-prompt-source-isolation-probe", timeout
    )
    serialized = _flatten_prompt_text(value)
    entries = _extract_skill_entries(value)
    if entries:
        raise EvalRuntimeError(
            "behavior-stage prompt must not contain automatic skill metadata"
        )
    agents_text = _read_utf8(source_root / "AGENTS.md").strip()
    if agents_text not in serialized:
        raise EvalRuntimeError(
            "synthetic AGENTS.md content is absent from behavior-stage input"
        )
    if "# Agent Rules Kit Upstream" in serialized:
        raise EvalRuntimeError(
            "behavior-stage input contains the project maintenance overlay"
        )
    return {"status": "passed", "automatic_skill_count": 0}


def _verify_judge_prompt_sources(
    codex_bin: Path, runtime: Runtime, source_root: Path, timeout: int
) -> dict[str, Any]:
    value, _ = _debug_prompt(
        codex_bin, runtime, "judge-prompt-source-isolation-probe", timeout
    )
    serialized = _flatten_prompt_text(value)
    entries = _extract_skill_entries(value)
    if entries:
        raise EvalRuntimeError("judge-stage prompt must not contain skill metadata")
    agents_text = _read_utf8(source_root / "AGENTS.md").strip()
    if agents_text and agents_text in serialized:
        raise EvalRuntimeError(
            "judge-stage input contains the tested AGENTS.md payload"
        )
    if any(runtime.fixture.iterdir()):
        raise EvalRuntimeError("judge-stage fixture must be empty")
    if "# Agent Rules Kit Upstream" in serialized:
        raise EvalRuntimeError(
            "judge-stage input contains the project maintenance overlay"
        )
    return {
        "status": "passed",
        "automatic_skill_count": 0,
        "fixture_entry_count": 0,
    }


class _CaptureHandler(http.server.BaseHTTPRequestHandler):
    requests: list[dict[str, Any]]

    def do_POST(self) -> None:  # noqa: N802 - BaseHTTPRequestHandler API
        try:
            length = int(self.headers.get("Content-Length", "0"))
        except ValueError:
            length = 0
        if length <= 0 or length > MAX_HTTP_REQUEST_BYTES:
            self.send_error(413)
            return
        raw = self.rfile.read(length)
        try:
            value = json.loads(raw)
        except json.JSONDecodeError:
            value = {"invalid_json": True}
        if isinstance(value, dict):
            self.requests.append(value)
        self.send_response(400)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(b'{"error":{"message":"intentional tool-surface probe stop"}}')

    def log_message(self, _format: str, *_args: object) -> None:
        return


def _load_runtime_contract(
    source_root: Path, codex_version: str
) -> set[tuple[str, str]]:
    path = source_root / "tests" / "evals" / "codex-runtime-contract.json"
    value = _read_json_object(path)
    if value.get("schema_version") != SCHEMA_VERSION:
        raise EvalInputError(f"unsupported runtime contract schema in {path}")
    versions = value.get("codex_versions")
    if set(value) != {"$schema", "schema_version", "codex_versions"}:
        raise EvalInputError(f"invalid top-level runtime contract fields in {path}")
    if not isinstance(versions, dict) or codex_version not in versions:
        raise EvalRuntimeError(
            f"Codex version {codex_version!r} has no reviewed tool-surface contract"
        )
    version = versions[codex_version]
    if not isinstance(version, dict) or set(version) != {"allowed_tools"}:
        raise EvalInputError(f"invalid Codex version contract for {codex_version}")
    tools = version["allowed_tools"]
    if not isinstance(tools, list):
        raise EvalInputError("runtime allowed_tools must be an array")
    result: set[tuple[str, str]] = set()
    for item in tools:
        if not isinstance(item, dict) or set(item) != {"type", "name"}:
            raise EvalInputError(
                "each runtime allowed tool must contain only type and name"
            )
        tool_type = item["type"]
        name = item["name"]
        if not isinstance(tool_type, str) or not isinstance(name, str):
            raise EvalInputError("runtime tool type and name must be strings")
        result.add((tool_type, name))
    if len(result) != len(tools):
        raise EvalInputError("runtime allowed_tools contains duplicates")
    return result


def _validate_tool_surface_request(
    request: dict[str, Any], allowed: set[tuple[str, str]]
) -> set[tuple[str, str]]:
    if request.get("invalid_json") is True:
        raise EvalRuntimeError("Responses request body was not valid JSON")
    raw_tools = request.get("tools", [])
    if not isinstance(raw_tools, list):
        raise EvalRuntimeError("Responses request tools must be an array when present")
    actual: set[tuple[str, str]] = set()
    for item in raw_tools:
        if not isinstance(item, dict):
            raise EvalRuntimeError("Responses request contained a non-object tool")
        tool_type = item.get("type")
        name = item.get("name")
        if not isinstance(tool_type, str) or not isinstance(name, str):
            raise EvalRuntimeError(
                "Responses request contained a tool without type/name"
            )
        tool = (tool_type, name)
        if tool in actual:
            raise EvalRuntimeError(
                f"Responses request contained a duplicate tool: {tool}"
            )
        actual.add(tool)
    unexpected = actual - allowed
    if unexpected:
        raise EvalRuntimeError(
            "Codex tool surface exceeds the reviewed allowlist: "
            f"unexpected={sorted(unexpected)}, allowed={sorted(allowed)}"
        )
    return actual


def _probe_tool_surface(
    *,
    source_root: Path,
    codex_bin: Path,
    codex_version: str,
    model: str,
    reasoning_effort: str,
    policy: Policy,
    timeout: int,
) -> dict[str, Any]:
    captured: list[dict[str, Any]] = []
    handler = type("CaptureHandler", (_CaptureHandler,), {"requests": captured})
    server = http.server.ThreadingHTTPServer(("127.0.0.1", 0), handler)
    server_thread = threading.Thread(target=server.serve_forever, daemon=True)
    server_thread.start()
    address = f"http://127.0.0.1:{server.server_address[1]}/v1"
    runtime: Runtime | None = None
    try:
        runtime = _prepare_runtime(
            source_root=source_root,
            codex_bin=codex_bin,
            model=model,
            reasoning_effort=reasoning_effort,
            policy=policy,
            timeout=min(timeout, 30),
            provider=("agent_eval_probe", address),
        )
        result = _run_owned_process(
            [
                str(codex_bin),
                "exec",
                "--ephemeral",
                "--ignore-rules",
                "--strict-config",
                "--skip-git-repo-check",
                "--json",
                "-C",
                str(runtime.fixture),
                "tool-surface-probe",
            ],
            cwd=runtime.fixture,
            environment=_loopback_environment(runtime),
            timeout=min(timeout, 30),
        )
        if result.timed_out:
            raise EvalRuntimeError("Codex tool-surface probe timed out")
        if not captured:
            raise EvalRuntimeError(
                "Codex tool-surface probe sent no Responses request: "
                + result.stderr.strip()[:1000]
            )
        probe_item_errors = _item_error_messages(_parse_events(result.stdout))
        if probe_item_errors:
            raise EvalRuntimeError(
                "Codex tool-surface probe emitted error items: "
                + _summarize_event_messages(probe_item_errors)
            )
    finally:
        server.shutdown()
        server.server_close()
        server_thread.join(timeout=2)
        if runtime is not None:
            runtime.cleanup()

    request = captured[0]
    allowed = _load_runtime_contract(source_root, codex_version)
    actual = _validate_tool_surface_request(request, allowed)
    return {
        "status": "passed",
        "tools": [
            {"type": tool_type, "name": name} for tool_type, name in sorted(actual)
        ],
    }


def _resolve_codex_binary(value: str) -> Path:
    candidate = shutil.which(value)
    if candidate is None:
        raise EvalInputError(f"Codex executable was not found: {value}")
    path = Path(candidate).resolve()
    if not path.is_file():
        raise EvalInputError(f"Codex executable is not a regular file: {path}")
    return path


def _codex_version(codex_bin: Path, timeout: int) -> str:
    result = _run_owned_process(
        [str(codex_bin), "--version"],
        cwd=Path.cwd(),
        environment={
            key: os.environ[key] for key in SAFE_ENV_KEYS if key in os.environ
        },
        timeout=min(timeout, 30),
    )
    version = result.stdout.strip()
    if result.returncode != 0 or result.timed_out or not version or "\n" in version:
        raise EvalRuntimeError("could not determine a stable Codex version")
    return version
