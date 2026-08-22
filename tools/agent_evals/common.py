"""Shared contracts and bounded file helpers for agent evaluations."""

from __future__ import annotations

import argparse
import contextlib
import dataclasses
import json
import os
import re
import sys
import tempfile
from collections.abc import Sequence
from pathlib import Path
from typing import Any

SCHEMA_VERSION = 1
EVAL_FILES = ("routing.jsonl", "skills.jsonl", "safety.jsonl")
CASE_FIELDS = {"id", "task"}
ORACLE_REQUIRED_FIELDS = {
    "id",
    "expected_rules",
    "forbidden_rules",
    "expected_skills",
    "forbidden_skills",
}
ORACLE_OPTIONAL_FIELDS = {"behavior", "baseline_disabled_skills"}
APPROVAL_POLICIES = ("inherit", "untrusted", "on-request", "never")
SANDBOX_MODES = ("inherit", "read-only", "workspace-write", "danger-full-access")
REASONING_EFFORTS = ("minimal", "low", "medium", "high", "xhigh", "max", "ultra")
USAGE_FIELDS = (
    "input_tokens",
    "cached_input_tokens",
    "output_tokens",
    "reasoning_output_tokens",
)
EVAL_ID_PATTERN = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
MARKDOWN_LINK_PATTERN = re.compile(r"(?<!!)\[[^\]]*\]\(([^)]+)\)")
SKILL_ENTRY_PATTERN = re.compile(
    r"^- ([a-z0-9]+(?:-[a-z0-9]+)*):.*"
    r"\((file|environment resource|orchestrator resource|custom resource): ([^)]+)\)$",
    re.MULTILINE,
)
MAX_TEXT_BYTES = 2 * 1024 * 1024
MAX_PROCESS_OUTPUT_BYTES = 16 * 1024 * 1024
MAX_HTTP_REQUEST_BYTES = 16 * 1024 * 1024

# Every feature that can add an execution, network, browser, plugin, MCP, or
# delegation surface is disabled. The versioned preflight contract catches any
# stock tool that remains or any new tool introduced by a Codex upgrade.
DISABLED_FEATURES = (
    "apply_patch_freeform",
    "apps",
    "auth_elicitation",
    "browser_use",
    "browser_use_external",
    "browser_use_full_cdp_access",
    "code_mode",
    "code_mode_host",
    "code_mode_only",
    "collaboration_modes",
    "computer_use",
    "default_mode_request_user_input",
    "deferred_executor",
    "enable_fanout",
    "enable_mcp_apps",
    "exec_permission_approvals",
    "goals",
    "hooks",
    "image_generation",
    "in_app_browser",
    "js_repl",
    "js_repl_tools_only",
    "memories",
    "multi_agent",
    "multi_agent_mode",
    "multi_agent_v2",
    "plugin_hooks",
    "plugin_sharing",
    "plugins",
    "remote_control",
    "remote_plugin",
    "request_permissions_tool",
    "request_rule",
    "search_tool",
    "shell_tool",
    "standalone_web_search",
    "tool_call_mcp_elicitation",
    "tool_search",
    "tool_suggest",
    "unified_exec",
    "workspace_dependencies",
)
SAFE_ENV_KEYS = (
    "ALL_PROXY",
    "HTTPS_PROXY",
    "HTTP_PROXY",
    "LANG",
    "LC_ALL",
    "NO_PROXY",
    "PATH",
    "SSL_CERT_DIR",
    "SSL_CERT_FILE",
    "TZ",
    "all_proxy",
    "https_proxy",
    "http_proxy",
    "no_proxy",
)


class EvalInputError(Exception):
    """The caller or checked-in eval contract is invalid."""


class EvalRuntimeError(Exception):
    """An isolated runtime or Codex invocation failed."""


@dataclasses.dataclass(frozen=True)
class Policy:
    approval_policy: str
    sandbox_mode: str
    sandbox_workspace_write: dict[str, Any]
    approval_source: str
    sandbox_source: str

    def public(self) -> dict[str, Any]:
        return {
            "approval_policy": self.approval_policy,
            "sandbox_mode": self.sandbox_mode,
            "approval_source": self.approval_source,
            "sandbox_source": self.sandbox_source,
        }


@dataclasses.dataclass(frozen=True)
class BehaviorOracle:
    summary: str
    criteria: tuple[str, ...]
    prohibitions: tuple[str, ...]


@dataclasses.dataclass(frozen=True)
class EvalCase:
    corpus: str
    id: str
    task: str
    expected_rules: tuple[str, ...]
    forbidden_rules: tuple[str, ...]
    expected_skills: tuple[str, ...]
    forbidden_skills: tuple[str, ...]
    behavior: BehaviorOracle | None
    baseline_disabled_skills: tuple[str, ...]


@dataclasses.dataclass(frozen=True)
class ProcessResult:
    returncode: int
    stdout: str
    stderr: str
    duration_seconds: float
    timed_out: bool = False


@dataclasses.dataclass
class Runtime:
    temporary: tempfile.TemporaryDirectory[str]
    root: Path
    home: Path
    codex_home: Path
    fixture: Path
    config_path: Path
    model_catalog_path: Path
    external_skill_paths: tuple[Path, ...] = ()

    def cleanup(self) -> None:
        self.temporary.cleanup()


def _diagnostic(message: str) -> None:
    print(message, file=sys.stderr, flush=True)


def _positive_int(value: str) -> int:
    parsed = int(value)
    if parsed <= 0:
        raise argparse.ArgumentTypeError("must be greater than zero")
    return parsed


def _trial_count(value: int | None, certify: bool) -> int:
    repeat = value if value is not None else (3 if certify else 1)
    if certify and repeat < 3:
        raise EvalInputError("--certify requires --repeat 3 or greater")
    return repeat


def _toml_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=False)


def _toml_string_array(values: Sequence[str]) -> str:
    return "[" + ", ".join(_toml_string(value) for value in values) + "]"


def _is_relative_to(path: Path, base: Path) -> bool:
    try:
        path.relative_to(base)
    except ValueError:
        return False
    return True


def _read_utf8(path: Path, *, limit: int = MAX_TEXT_BYTES) -> str:
    try:
        data = path.read_bytes()
    except OSError as exc:
        raise EvalInputError(f"cannot read {path}: {exc}") from exc
    if len(data) > limit:
        raise EvalInputError(f"{path} exceeds the {limit}-byte input limit")
    try:
        return data.decode("utf-8")
    except UnicodeDecodeError as exc:
        raise EvalInputError(f"{path} must be UTF-8: {exc}") from exc


def _read_json_object(path: Path, *, runtime: bool = False) -> dict[str, Any]:
    error_type = EvalRuntimeError if runtime else EvalInputError
    try:
        value = json.loads(_read_utf8(path))
    except json.JSONDecodeError as exc:
        raise error_type(f"{path} contains invalid JSON: {exc}") from exc
    except EvalInputError as exc:
        raise error_type(str(exc)) from exc
    if not isinstance(value, dict):
        raise error_type(f"{path} must contain a JSON object")
    return value


def _atomic_write_bytes(path: Path, data: bytes, mode: int) -> None:
    descriptor, temporary_name = tempfile.mkstemp(
        prefix=f".{path.name}.", dir=path.parent
    )
    temporary = Path(temporary_name)
    try:
        os.fchmod(descriptor, mode)
        with os.fdopen(descriptor, "wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        path.chmod(mode)
    finally:
        with contextlib.suppress(FileNotFoundError):
            temporary.unlink()


def _atomic_write_json(path: Path, value: Any, mode: int = 0o644) -> None:
    data = (
        json.dumps(value, ensure_ascii=False, indent=2, sort_keys=True) + "\n"
    ).encode()
    _atomic_write_bytes(path, data, mode)
