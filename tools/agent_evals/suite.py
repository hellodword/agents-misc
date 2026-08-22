"""Artifact containment and crash-resilient eval-suite orchestration."""

from __future__ import annotations

import datetime as dt
import os
import secrets
import tempfile
from collections.abc import Sequence
from pathlib import Path
from typing import Any

from .auth import (
    _credential_lock,
    _ensure_private_dir,
    _validate_chatgpt_auth_file,
)
from .common import (
    SCHEMA_VERSION,
    USAGE_FIELDS,
    EvalCase,
    EvalInputError,
    EvalRuntimeError,
    Policy,
    _atomic_write_json,
    _diagnostic,
    _is_relative_to,
    _read_utf8,
)
from .preflight import _perform_preflight
from .prompts import _routing_prompt
from .runtime import _snapshot_eval_source
from .scoring import _aggregate_case_results, _score_route
from .stages import _run_behavior_evaluation, _run_fresh_codex_stage


def _artifact_base(source_root: Path, value: Path | None) -> Path:
    ignore_path = source_root / ".gitignore"
    ignore_lines = {
        line.strip()
        for line in _read_utf8(ignore_path).splitlines()
        if line.strip() and not line.lstrip().startswith("#")
    }
    if "tmp/" not in ignore_lines:
        raise EvalInputError(
            f"{ignore_path} must ignore tmp/ before eval artifacts are written"
        )
    unresolved_permitted = source_root / "tmp" / "agent"
    for candidate in (source_root / "tmp", unresolved_permitted):
        if os.path.lexists(candidate) and candidate.is_symlink():
            raise EvalInputError(
                f"artifacts path must not contain symlinks: {candidate}"
            )
    permitted = unresolved_permitted.resolve()
    if value is None:
        selected = (permitted / "evals").resolve()
    else:
        requested = value.expanduser()
        selected = (
            requested if requested.is_absolute() else source_root / requested
        ).resolve()
    if not _is_relative_to(selected, permitted):
        raise EvalInputError(f"artifacts directory must stay under {permitted}")
    current = permitted
    for part in selected.relative_to(permitted).parts:
        current = current / part
        if current.exists() and current.is_symlink():
            raise EvalInputError(f"artifacts path must not contain symlinks: {current}")
    selected.mkdir(parents=True, exist_ok=True)
    return selected


def _run_suite_from_snapshot(
    *,
    repository_root: Path,
    source_root: Path,
    codex_bin: Path,
    model: str,
    reasoning_effort: str,
    judge_model: str,
    judge_reasoning_effort: str,
    policy: Policy,
    timeout: int,
    state_dir: Path,
    artifacts_dir: Path | None,
    cases: Sequence[EvalCase],
    repeat: int,
    certify: bool,
) -> tuple[dict[str, Any], int]:
    preflight = _perform_preflight(
        source_root=source_root,
        codex_bin=codex_bin,
        model=model,
        reasoning_effort=reasoning_effort,
        judge_model=judge_model,
        judge_reasoning_effort=judge_reasoning_effort,
        policy=policy,
        timeout=timeout,
        cases=cases,
    )
    state_dir = state_dir.expanduser()
    _ensure_private_dir(state_dir)
    vault = state_dir / "auth.json"
    if not vault.exists():
        raise EvalInputError(
            f"credential vault is missing at {vault}; run the auth-init subcommand first"
        )
    _validate_chatgpt_auth_file(vault, "credential vault")

    artifact_root = _artifact_base(repository_root, artifacts_dir)
    run_id = dt.datetime.now(dt.timezone.utc).strftime(
        "%Y%m%dT%H%M%SZ-"
    ) + secrets.token_hex(4)
    run_dir = artifact_root / run_id
    run_dir.mkdir(mode=0o755)
    results: list[dict[str, Any]] = []

    def aggregate_usage() -> dict[str, dict[str, int]]:
        subject = {"calls": 0, **dict.fromkeys(USAGE_FIELDS, 0)}
        judge = {"calls": 0, **dict.fromkeys(USAGE_FIELDS, 0)}

        def add(bucket: dict[str, int], usage: Any) -> None:
            if not isinstance(usage, dict):
                return
            bucket["calls"] += 1
            for field in USAGE_FIELDS:
                bucket[field] += usage[field]

        for item in results:
            route_result = item.get("route")
            if isinstance(route_result, dict):
                add(subject, route_result.get("usage"))
            for field in ("behavior", "baseline"):
                stage = item.get(field)
                if isinstance(stage, dict):
                    add(subject, stage.get("usage"))
                    add(judge, stage.get("judge_usage"))
        total = {
            field: subject[field] + judge[field] for field in ("calls", *USAGE_FIELDS)
        }
        return {"subject": subject, "judge": judge, "total": total}

    summary: dict[str, Any] = {
        "schema_version": SCHEMA_VERSION,
        "run_id": run_id,
        "status": "running",
        "agent": preflight["agent"],
        "subject": preflight["subject"],
        "payload_sha256": preflight["payload_sha256"],
        "policy": policy.public(),
        "repeat": repeat,
        "preflight": preflight["checks"],
        "totals": {"attempts": len(cases) * repeat, "passed": 0, "failed": 0},
        "usage": aggregate_usage(),
        "case_results": [],
        "results": results,
        "certification": {
            "requested": certify,
            "status": "not-requested",
            "dimensions": {},
            "warnings": [],
        },
        "artifacts_dir": str(run_dir),
    }
    _atomic_write_json(run_dir / "summary.json", summary)

    with _credential_lock(state_dir):
        secret_values: set[str] = set()
        for attempt in range(1, repeat + 1):
            for case in cases:
                _diagnostic(f"eval {case.id} attempt {attempt}/{repeat}")
                case_dir = run_dir / f"{case.id}--{attempt}"
                case_dir.mkdir(mode=0o755)
                result_record: dict[str, Any] = {
                    "id": case.id,
                    "corpus": case.corpus,
                    "attempt": attempt,
                    "status": "failed",
                    "route": None,
                    "behavior": {
                        "status": "skipped"
                        if case.behavior is not None
                        else "not-applicable"
                    },
                    "baseline": {
                        "status": (
                            "pending"
                            if certify and case.baseline_disabled_skills
                            else "not-requested"
                        )
                    },
                }
                errors: list[str] = []
                try:
                    route, route_usage, route_process = _run_fresh_codex_stage(
                        source_root=source_root,
                        codex_bin=codex_bin,
                        model=model,
                        reasoning_effort=reasoning_effort,
                        policy=policy,
                        vault=vault,
                        prompt_builder=lambda runtime, current=case: _routing_prompt(
                            current,
                            _read_utf8(
                                runtime.fixture / ".agents" / "rules" / "index.md"
                            ),
                        ),
                        schema_filename="route-result.schema.json",
                        output_path=case_dir / "route.final.json",
                        events_path=case_dir / "route.events.jsonl",
                        stderr_path=case_dir / "route.stderr.txt",
                        timeout=timeout,
                        secrets_to_redact=secret_values,
                        include_skill_instructions=True,
                    )
                    route_score = _score_route(case, route)
                    _atomic_write_json(case_dir / "route.score.json", route_score)
                    result_record["route"] = {
                        "status": "passed" if route_score["passed"] else "failed",
                        "duration_seconds": round(route_process.duration_seconds, 3),
                        "usage": route_usage,
                        "score": route_score,
                    }
                    if case.behavior is not None:
                        result_record["behavior"] = _run_behavior_evaluation(
                            source_root=source_root,
                            codex_bin=codex_bin,
                            model=model,
                            reasoning_effort=reasoning_effort,
                            judge_model=judge_model,
                            judge_reasoning_effort=judge_reasoning_effort,
                            policy=policy,
                            vault=vault,
                            case=case,
                            route=route,
                            case_dir=case_dir,
                            prefix="behavior",
                            timeout=timeout,
                            secrets_to_redact=secret_values,
                        )
                    if route_score["passed"] and (
                        case.behavior is None
                        or result_record["behavior"]["status"] == "passed"
                    ):
                        result_record["status"] = "passed"
                except (EvalInputError, EvalRuntimeError) as exc:
                    errors.append(str(exc))

                if certify and case.baseline_disabled_skills:
                    baseline_route = {
                        "selected_rules": list(case.expected_rules),
                        "selected_skills": [
                            name
                            for name in case.expected_skills
                            if name not in case.baseline_disabled_skills
                        ],
                    }
                    try:
                        result_record["baseline"] = _run_behavior_evaluation(
                            source_root=source_root,
                            codex_bin=codex_bin,
                            model=model,
                            reasoning_effort=reasoning_effort,
                            judge_model=judge_model,
                            judge_reasoning_effort=judge_reasoning_effort,
                            policy=policy,
                            vault=vault,
                            case=case,
                            route=baseline_route,
                            case_dir=case_dir,
                            prefix="baseline",
                            timeout=timeout,
                            secrets_to_redact=secret_values,
                            disabled_skill_names=case.baseline_disabled_skills,
                        )
                    except (EvalInputError, EvalRuntimeError) as exc:
                        result_record["baseline"] = {
                            "status": "error",
                            "error": str(exc),
                        }
                        errors.append(f"baseline: {exc}")
                if errors:
                    result_record["error"] = "; ".join(errors)
                results.append(result_record)
                summary["usage"] = aggregate_usage()
                summary["totals"][
                    "passed" if result_record["status"] == "passed" else "failed"
                ] += 1
                _atomic_write_json(case_dir / "result.json", result_record)
                _atomic_write_json(run_dir / "summary.json", summary)
                route_status = (
                    result_record["route"]["status"]
                    if isinstance(result_record["route"], dict)
                    else "error"
                )
                _diagnostic(
                    f"eval {case.id} attempt {attempt}/{repeat} result "
                    f"{result_record['status']} (route={route_status}, "
                    f"behavior={result_record['behavior']['status']}, "
                    f"baseline={result_record['baseline']['status']})"
                )

    case_results, dimensions, warnings = _aggregate_case_results(
        cases, results, repeat, certify
    )
    all_cases_passed = all(item["passed"] for item in case_results)
    summary["case_results"] = case_results
    summary["certification"] = {
        "requested": certify,
        "status": (
            "passed"
            if certify and all_cases_passed
            else ("failed" if certify else "not-requested")
        ),
        "dimensions": dimensions,
        "warnings": warnings,
    }
    summary["status"] = "passed" if all_cases_passed else "failed"
    _atomic_write_json(run_dir / "summary.json", summary)
    return summary, 0 if summary["status"] == "passed" else 1


def _run_suite(
    *,
    source_root: Path,
    codex_bin: Path,
    model: str,
    reasoning_effort: str,
    judge_model: str,
    judge_reasoning_effort: str,
    policy: Policy,
    timeout: int,
    state_dir: Path,
    artifacts_dir: Path | None,
    cases: Sequence[EvalCase],
    repeat: int,
    certify: bool,
) -> tuple[dict[str, Any], int]:
    with tempfile.TemporaryDirectory(prefix="agent-evals-suite-") as temporary:
        snapshot_root = Path(temporary) / "source"
        _snapshot_eval_source(source_root, snapshot_root)
        return _run_suite_from_snapshot(
            repository_root=source_root,
            source_root=snapshot_root,
            codex_bin=codex_bin,
            model=model,
            reasoning_effort=reasoning_effort,
            judge_model=judge_model,
            judge_reasoning_effort=judge_reasoning_effort,
            policy=policy,
            timeout=timeout,
            state_dir=state_dir,
            artifacts_dir=artifacts_dir,
            cases=cases,
            repeat=repeat,
            certify=certify,
        )
