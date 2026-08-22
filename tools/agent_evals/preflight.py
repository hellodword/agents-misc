"""Composite preflight for runtime, prompt, and tool-surface isolation."""

from __future__ import annotations

from collections.abc import Sequence
from pathlib import Path
from typing import Any

from .common import SCHEMA_VERSION, EvalCase, Policy
from .isolation import (
    _codex_version,
    _load_runtime_contract,
    _probe_tool_surface,
    _verify_behavior_prompt_sources,
    _verify_judge_prompt_sources,
    _verify_prompt_sources,
)
from .prompts import _preflight_prompts
from .runtime import _payload_sha256, _prepare_runtime


def _perform_preflight(
    *,
    source_root: Path,
    codex_bin: Path,
    model: str,
    reasoning_effort: str,
    judge_model: str,
    judge_reasoning_effort: str,
    policy: Policy,
    timeout: int,
    cases: Sequence[EvalCase],
) -> dict[str, Any]:
    codex_version = _codex_version(codex_bin, timeout)
    # Fail on unsupported versions before constructing any runtime with credentials.
    _load_runtime_contract(source_root, codex_version)
    runtime = _prepare_runtime(
        source_root=source_root,
        codex_bin=codex_bin,
        model=model,
        reasoning_effort=reasoning_effort,
        policy=policy,
        timeout=min(timeout, 30),
    )
    try:
        prompt_sources = _verify_prompt_sources(
            codex_bin, runtime, source_root, min(timeout, 30)
        )
    finally:
        runtime.cleanup()
    behavior_runtime = _prepare_runtime(
        source_root=source_root,
        codex_bin=codex_bin,
        model=model,
        reasoning_effort=reasoning_effort,
        policy=policy,
        timeout=min(timeout, 30),
        include_skill_instructions=False,
    )
    try:
        behavior_prompt_sources = _verify_behavior_prompt_sources(
            codex_bin, behavior_runtime, source_root, min(timeout, 30)
        )
    finally:
        behavior_runtime.cleanup()
    judge_runtime = _prepare_runtime(
        source_root=source_root,
        codex_bin=codex_bin,
        model=judge_model,
        reasoning_effort=judge_reasoning_effort,
        policy=policy,
        timeout=min(timeout, 30),
        include_skill_instructions=False,
        include_payload=False,
    )
    try:
        judge_prompt_sources = _verify_judge_prompt_sources(
            codex_bin, judge_runtime, source_root, min(timeout, 30)
        )
    finally:
        judge_runtime.cleanup()
    prompt_contract = _preflight_prompts(source_root, cases)
    tool_surface = _probe_tool_surface(
        source_root=source_root,
        codex_bin=codex_bin,
        codex_version=codex_version,
        model=model,
        reasoning_effort=reasoning_effort,
        policy=policy,
        timeout=timeout,
    )
    judge_tool_surface = (
        tool_surface
        if (judge_model, judge_reasoning_effort) == (model, reasoning_effort)
        else _probe_tool_surface(
            source_root=source_root,
            codex_bin=codex_bin,
            codex_version=codex_version,
            model=judge_model,
            reasoning_effort=judge_reasoning_effort,
            policy=policy,
            timeout=timeout,
        )
    )
    return {
        "schema_version": SCHEMA_VERSION,
        "status": "passed",
        "agent": {"name": "codex", "version": codex_version},
        "subject": {
            "model": model,
            "reasoning_effort": reasoning_effort,
            "judge_model": judge_model,
            "judge_reasoning_effort": judge_reasoning_effort,
        },
        "payload_sha256": _payload_sha256(source_root),
        "policy": policy.public(),
        "checks": {
            "prompt_sources": prompt_sources,
            "behavior_prompt_sources": behavior_prompt_sources,
            "judge_prompt_sources": judge_prompt_sources,
            "prompt_expectations": prompt_contract,
            "tool_surface": tool_surface,
            "judge_tool_surface": judge_tool_surface,
        },
    }
