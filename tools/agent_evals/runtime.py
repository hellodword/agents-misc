"""Isolated filesystem, process, policy, and Codex runtime construction."""

from __future__ import annotations

import contextlib
import hashlib
import json
import os
import shutil
import signal
import stat
import subprocess
import tempfile
import time
import tomllib
from collections.abc import Sequence
from pathlib import Path
from typing import Any

from .auth import _validate_owned_regular_file
from .common import (
    APPROVAL_POLICIES,
    DISABLED_FEATURES,
    EVAL_ID_PATTERN,
    MAX_PROCESS_OUTPUT_BYTES,
    SAFE_ENV_KEYS,
    SANDBOX_MODES,
    SKILL_ENTRY_PATTERN,
    EvalInputError,
    EvalRuntimeError,
    Policy,
    ProcessResult,
    Runtime,
    _atomic_write_json,
    _is_relative_to,
    _read_utf8,
    _toml_string,
    _toml_string_array,
)


def _load_policy(
    config_path: Path, approval_override: str, sandbox_override: str
) -> Policy:
    config_path = config_path.expanduser()
    config: dict[str, Any] = {}
    if config_path.exists():
        _validate_owned_regular_file(config_path, "policy config")
        try:
            config = tomllib.loads(_read_utf8(config_path))
        except tomllib.TOMLDecodeError as exc:
            raise EvalInputError(
                f"policy config is invalid TOML: {config_path}: {exc}"
            ) from exc
        if not isinstance(config, dict):
            raise EvalInputError(f"policy config must be a TOML table: {config_path}")

    inherited_approval = config.get("approval_policy", "on-request")
    inherited_sandbox = config.get("sandbox_mode", "read-only")
    approval = (
        inherited_approval if approval_override == "inherit" else approval_override
    )
    sandbox = inherited_sandbox if sandbox_override == "inherit" else sandbox_override
    if approval not in APPROVAL_POLICIES[1:]:
        raise EvalInputError(
            "inherited approval_policy must be one of "
            + ", ".join(APPROVAL_POLICIES[1:])
        )
    if sandbox not in SANDBOX_MODES[1:]:
        raise EvalInputError(
            "inherited sandbox_mode must be one of " + ", ".join(SANDBOX_MODES[1:])
        )

    workspace: dict[str, Any] = {}
    raw_workspace = config.get("sandbox_workspace_write", {})
    if sandbox == "workspace-write" and sandbox_override == "inherit":
        if not isinstance(raw_workspace, dict):
            raise EvalInputError("sandbox_workspace_write must be a TOML table")
        allowed = {
            "exclude_slash_tmp",
            "exclude_tmpdir_env_var",
            "network_access",
            "writable_roots",
        }
        unknown = set(raw_workspace) - allowed
        if unknown:
            raise EvalInputError(
                f"sandbox_workspace_write contains unsupported fields: {sorted(unknown)}"
            )
        for key in ("exclude_slash_tmp", "exclude_tmpdir_env_var", "network_access"):
            value = raw_workspace.get(key, False)
            if not isinstance(value, bool):
                raise EvalInputError(f"sandbox_workspace_write.{key} must be a boolean")
            workspace[key] = value
        roots = raw_workspace.get("writable_roots", [])
        if not isinstance(roots, list) or any(
            not isinstance(item, str) for item in roots
        ):
            raise EvalInputError(
                "sandbox_workspace_write.writable_roots must be strings"
            )
        if any(not Path(item).is_absolute() for item in roots):
            raise EvalInputError(
                "sandbox_workspace_write.writable_roots must contain absolute paths"
            )
        workspace["writable_roots"] = roots

    return Policy(
        approval_policy=approval,
        sandbox_mode=sandbox,
        sandbox_workspace_write=workspace,
        approval_source=(
            str(config_path)
            if approval_override == "inherit" and "approval_policy" in config
            else ("safe-default" if approval_override == "inherit" else "command-line")
        ),
        sandbox_source=(
            str(config_path)
            if sandbox_override == "inherit" and "sandbox_mode" in config
            else ("safe-default" if sandbox_override == "inherit" else "command-line")
        ),
    )


def _render_config(
    *,
    model: str,
    reasoning_effort: str,
    policy: Policy,
    model_catalog_path: Path,
    include_skill_instructions: bool,
    disabled_skill_paths: Sequence[Path] = (),
    provider: tuple[str, str] | None = None,
) -> str:
    lines = [
        f"model = {_toml_string(model)}",
        f"model_catalog_json = {_toml_string(str(model_catalog_path))}",
        f"model_reasoning_effort = {_toml_string(reasoning_effort)}",
        'model_reasoning_summary = "none"',
        'model_verbosity = "low"',
        'personality = "none"',
        f"approval_policy = {_toml_string(policy.approval_policy)}",
        f"sandbox_mode = {_toml_string(policy.sandbox_mode)}",
        'web_search = "disabled"',
        "project_root_markers = []",
    ]
    if provider is not None:
        provider_name, base_url = provider
        lines.append(f"model_provider = {_toml_string(provider_name)}")
    if policy.sandbox_mode == "workspace-write" and policy.sandbox_workspace_write:
        workspace = policy.sandbox_workspace_write
        lines.extend(
            [
                "",
                "[sandbox_workspace_write]",
                f"exclude_slash_tmp = {str(workspace['exclude_slash_tmp']).lower()}",
                "exclude_tmpdir_env_var = "
                + str(workspace["exclude_tmpdir_env_var"]).lower(),
                f"network_access = {str(workspace['network_access']).lower()}",
                "writable_roots = " + _toml_string_array(workspace["writable_roots"]),
            ]
        )
    lines.extend(["", "[shell_environment_policy]", 'inherit = "none"'])
    lines.extend(["", "[features]"])
    lines.extend(f"{name} = false" for name in DISABLED_FEATURES)
    lines.extend(
        [
            "",
            "[skills]",
            f"include_instructions = {str(include_skill_instructions).lower()}",
        ]
    )
    for path in disabled_skill_paths:
        lines.extend(
            [
                "",
                "[[skills.config]]",
                f"path = {_toml_string(str(path))}",
                "enabled = false",
            ]
        )
    if provider is not None:
        provider_name, base_url = provider
        lines.extend(
            [
                "",
                f"[model_providers.{provider_name}]",
                'name = "Agent eval tool-surface probe"',
                f"base_url = {_toml_string(base_url)}",
                'env_key = "AGENT_EVAL_FAKE_KEY"',
                "requires_openai_auth = false",
                'wire_api = "responses"',
                "request_max_retries = 0",
                "stream_max_retries = 0",
            ]
        )
    return "\n".join(lines) + "\n"


def _isolated_environment(
    runtime: Runtime, additions: dict[str, str] | None = None
) -> dict[str, str]:
    environment = {key: os.environ[key] for key in SAFE_ENV_KEYS if key in os.environ}
    environment.update(
        {
            "CI": "1",
            "CODEX_HOME": str(runtime.codex_home),
            "HOME": str(runtime.home),
            "TERM": "dumb",
            "XDG_CACHE_HOME": str(runtime.home / ".cache"),
            "XDG_CONFIG_HOME": str(runtime.home / ".config"),
            "XDG_DATA_HOME": str(runtime.home / ".local" / "share"),
            "XDG_STATE_HOME": str(runtime.home / ".local" / "state"),
        }
    )
    if additions:
        environment.update(additions)
    return environment


def _run_owned_process(
    argv: Sequence[str],
    *,
    cwd: Path,
    environment: dict[str, str],
    timeout: int,
    stdin: str | None = None,
) -> ProcessResult:
    started = time.monotonic()
    try:
        process = subprocess.Popen(
            list(argv),
            cwd=cwd,
            env=environment,
            stdin=subprocess.PIPE if stdin is not None else subprocess.DEVNULL,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            start_new_session=True,
        )
    except OSError as exc:
        raise EvalRuntimeError(f"cannot start {argv[0]}: {exc}") from exc
    timed_out = False
    try:
        stdout, stderr = process.communicate(input=stdin, timeout=timeout)
    except subprocess.TimeoutExpired:
        timed_out = True
        with contextlib.suppress(ProcessLookupError):
            os.killpg(process.pid, signal.SIGTERM)
        try:
            stdout, stderr = process.communicate(timeout=2)
        except subprocess.TimeoutExpired:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGKILL)
            stdout, stderr = process.communicate()
    for name, value in (("stdout", stdout), ("stderr", stderr)):
        if len(value.encode("utf-8", errors="replace")) > MAX_PROCESS_OUTPUT_BYTES:
            raise EvalRuntimeError(f"{argv[0]} {name} exceeded the output limit")
    return ProcessResult(
        returncode=process.returncode,
        stdout=stdout,
        stderr=stderr,
        duration_seconds=time.monotonic() - started,
        timed_out=timed_out,
    )


def _copy_payload(source_root: Path, destination: Path) -> None:
    source_root = source_root.resolve()
    agents_file = source_root / "AGENTS.md"
    agents_dir = source_root / ".agents"
    if not agents_file.is_file() or not agents_dir.is_dir():
        raise EvalInputError("repository root must contain AGENTS.md and .agents/")
    destination.mkdir(mode=0o755)

    sources = [agents_file, agents_dir, *sorted(agents_dir.rglob("*"))]
    for source in sources:
        try:
            info = source.lstat()
        except OSError as exc:
            raise EvalInputError(
                f"cannot inspect payload path {source}: {exc}"
            ) from exc
        if stat.S_ISLNK(info.st_mode):
            raise EvalInputError(f"payload must not contain symlinks: {source}")
        relative = source.relative_to(source_root)
        target = destination / relative
        if stat.S_ISDIR(info.st_mode):
            target.mkdir(mode=0o755, exist_ok=True)
            continue
        if not stat.S_ISREG(info.st_mode):
            raise EvalInputError(
                f"payload path must be a regular file or directory: {source}"
            )
        _read_utf8(source)
        target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        shutil.copyfile(source, target, follow_symlinks=False)
        target.chmod(0o444)

    top_level = {path.name for path in destination.iterdir()}
    if top_level != {"AGENTS.md", ".agents"}:
        raise EvalRuntimeError(
            f"synthetic repository has unexpected entries: {sorted(top_level)}"
        )


def _snapshot_eval_source(source_root: Path, destination: Path) -> None:
    _copy_payload(source_root, destination)
    evals_root = source_root / "tests" / "evals"
    schema_root = evals_root / "schemas"
    for directory in (source_root / "tests", evals_root, schema_root):
        try:
            info = directory.lstat()
        except OSError as exc:
            raise EvalInputError(
                f"cannot inspect eval runtime input directory {directory}: {exc}"
            ) from exc
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
            raise EvalInputError(
                "eval runtime input directory must be a non-symlink directory: "
                f"{directory}"
            )
    sources = [
        evals_root / "codex-runtime-contract.json",
        *sorted(schema_root.glob("*.json")),
    ]
    if len(sources) == 1:
        raise EvalInputError(
            f"eval schema directory contains no JSON schemas: {schema_root}"
        )
    for source in sources:
        try:
            info = source.lstat()
        except OSError as exc:
            raise EvalInputError(
                f"cannot inspect eval runtime input {source}: {exc}"
            ) from exc
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
            raise EvalInputError(
                f"eval runtime input must be a regular non-symlink file: {source}"
            )
        _read_utf8(source)
        target = destination / source.relative_to(source_root)
        target.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
        shutil.copyfile(source, target, follow_symlinks=False)
        target.chmod(0o444)


def _payload_sha256(source_root: Path) -> str:
    source_root = source_root.resolve()
    paths = [source_root / "AGENTS.md"]
    agents_dir = source_root / ".agents"
    if not paths[0].is_file() or not agents_dir.is_dir():
        raise EvalInputError("repository root must contain AGENTS.md and .agents/")
    paths.extend(path for path in sorted(agents_dir.rglob("*")) if path.is_file())
    digest = hashlib.sha256()
    for path in paths:
        info = path.lstat()
        if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
            raise EvalInputError(
                f"payload digest requires regular non-symlink files: {path}"
            )
        relative = str(path.relative_to(source_root)).encode("utf-8")
        data = path.read_bytes()
        digest.update(len(relative).to_bytes(8, "big"))
        digest.update(relative)
        digest.update(len(data).to_bytes(8, "big"))
        digest.update(data)
    return digest.hexdigest()


def _write_restricted_model_catalog(
    codex_bin: Path,
    runtime: Runtime,
    model: str,
    reasoning_effort: str,
    timeout: int,
) -> None:
    result = _run_owned_process(
        [str(codex_bin), "debug", "models", "--bundled"],
        cwd=runtime.fixture,
        environment=_isolated_environment(runtime),
        timeout=timeout,
    )
    if result.timed_out or result.returncode != 0:
        raise EvalRuntimeError(
            "could not read the bundled Codex model catalog: "
            + result.stderr.strip()[:1000]
        )
    try:
        catalog = json.loads(result.stdout)
    except json.JSONDecodeError as exc:
        raise EvalRuntimeError(
            f"bundled Codex model catalog is invalid JSON: {exc}"
        ) from exc
    restricted = _restrict_model_catalog(catalog, model, reasoning_effort)
    _atomic_write_json(runtime.model_catalog_path, restricted, mode=0o600)


def _restrict_model_catalog(
    catalog: Any, model: str, reasoning_effort: str
) -> dict[str, Any]:
    models = catalog.get("models") if isinstance(catalog, dict) else None
    if not isinstance(models, list):
        raise EvalRuntimeError("bundled Codex model catalog has no models array")
    selected = next(
        (
            item
            for item in models
            if isinstance(item, dict) and item.get("slug") == model
        ),
        None,
    )
    if selected is None:
        raise EvalInputError(
            f"model {model!r} is not advertised by this Codex binary's bundled catalog"
        )
    levels = selected.get("supported_reasoning_levels")
    if not isinstance(levels, list):
        raise EvalRuntimeError(
            f"model {model!r} has no supported_reasoning_levels array"
        )
    supported_efforts = {
        item.get("effort")
        for item in levels
        if isinstance(item, dict) and isinstance(item.get("effort"), str)
    }
    if supported_efforts and reasoning_effort not in supported_efforts:
        raise EvalInputError(
            f"model {model!r} does not advertise reasoning effort {reasoning_effort!r}; "
            f"supported values: {sorted(supported_efforts)}"
        )

    # The field is optional in Codex's catalog contract. Null removes the
    # apply_patch capability while preserving the binary's own instructions and
    # every other model property byte-for-byte.
    restricted_model = dict(selected)
    restricted_model["apply_patch_tool_type"] = None
    return {"models": [restricted_model]}


def _extract_skill_entries(prompt_value: Any) -> list[tuple[str, str, str]]:
    text = _flatten_prompt_text(prompt_value)
    return [tuple(match.groups()) for match in SKILL_ENTRY_PATTERN.finditer(text)]


def _flatten_prompt_text(value: Any) -> str:
    parts: list[str] = []

    def visit(item: Any) -> None:
        if isinstance(item, str):
            parts.append(item)
        elif isinstance(item, dict):
            for nested in item.values():
                visit(nested)
        elif isinstance(item, list):
            for nested in item:
                visit(nested)

    visit(value)
    return "\n".join(parts)


def _debug_prompt(
    codex_bin: Path, runtime: Runtime, prompt: str, timeout: int
) -> tuple[Any, ProcessResult]:
    result = _run_owned_process(
        [str(codex_bin), "debug", "prompt-input", prompt],
        cwd=runtime.fixture,
        environment=_isolated_environment(runtime),
        timeout=timeout,
    )
    if result.timed_out:
        raise EvalRuntimeError("codex debug prompt-input timed out")
    if result.returncode != 0:
        raise EvalRuntimeError(
            "codex debug prompt-input failed: " + result.stderr.strip()[:1000]
        )
    try:
        return json.loads(result.stdout), result
    except json.JSONDecodeError as exc:
        raise EvalRuntimeError(
            f"codex debug prompt-input returned invalid JSON: {exc}"
        ) from exc


def _prepare_runtime(
    *,
    source_root: Path,
    codex_bin: Path,
    model: str,
    reasoning_effort: str,
    policy: Policy,
    timeout: int,
    provider: tuple[str, str] | None = None,
    include_skill_instructions: bool = True,
    include_payload: bool = True,
    disabled_skill_names: Sequence[str] = (),
) -> Runtime:
    temporary = tempfile.TemporaryDirectory(prefix="agent-evals-runtime-")
    root = Path(temporary.name)
    runtime = Runtime(
        temporary=temporary,
        root=root,
        home=root / "home",
        codex_home=root / "codex-home",
        fixture=root / "fixture",
        config_path=root / "codex-home" / "config.toml",
        model_catalog_path=root / "model-catalog.json",
    )
    try:
        runtime.home.mkdir(mode=0o700)
        runtime.codex_home.mkdir(mode=0o700)
        if include_payload:
            _copy_payload(source_root, runtime.fixture)
        else:
            runtime.fixture.mkdir(mode=0o755)
        _write_restricted_model_catalog(
            codex_bin,
            runtime,
            model,
            reasoning_effort,
            min(timeout, 30),
        )
        runtime.config_path.write_text(
            _render_config(
                model=model,
                reasoning_effort=reasoning_effort,
                policy=policy,
                model_catalog_path=runtime.model_catalog_path,
                include_skill_instructions=True,
                provider=provider,
            ),
            encoding="utf-8",
        )
        runtime.config_path.chmod(0o600)

        preliminary, _ = _debug_prompt(
            codex_bin, runtime, "source-discovery-probe", timeout
        )
        fixture_skills = (runtime.fixture / ".agents" / "skills").resolve()
        external: set[Path] = set()
        for _name, locator_type, locator in _extract_skill_entries(preliminary):
            if locator_type != "file":
                continue
            candidate = Path(locator).resolve()
            if not include_payload or not _is_relative_to(candidate, fixture_skills):
                external.add(candidate)
        runtime.external_skill_paths = tuple(sorted(external))
        payload_disabled: list[Path] = []
        for name in disabled_skill_names:
            if not EVAL_ID_PATTERN.fullmatch(name):
                raise EvalInputError(f"invalid disabled skill name: {name}")
            path = (
                runtime.fixture / ".agents" / "skills" / name / "SKILL.md"
            ).resolve()
            if (
                not include_payload
                or not path.is_file()
                or not _is_relative_to(path, fixture_skills)
            ):
                raise EvalInputError(f"cannot disable missing payload skill: {name}")
            payload_disabled.append(path)
        runtime.config_path.write_text(
            _render_config(
                model=model,
                reasoning_effort=reasoning_effort,
                policy=policy,
                model_catalog_path=runtime.model_catalog_path,
                include_skill_instructions=include_skill_instructions,
                disabled_skill_paths=(
                    *runtime.external_skill_paths,
                    *payload_disabled,
                ),
                provider=provider,
            ),
            encoding="utf-8",
        )
        runtime.config_path.chmod(0o600)
        return runtime
    except Exception:
        runtime.cleanup()
        raise
