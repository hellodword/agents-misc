"""Checked-in eval case, oracle, JSONL, and route-schema contracts."""

from __future__ import annotations

import json
from collections.abc import Sequence
from pathlib import Path
from typing import Any

from .common import (
    CASE_FIELDS,
    EVAL_FILES,
    EVAL_ID_PATTERN,
    ORACLE_OPTIONAL_FIELDS,
    ORACLE_REQUIRED_FIELDS,
    BehaviorOracle,
    EvalCase,
    EvalInputError,
    _atomic_write_json,
    _read_json_object,
    _read_utf8,
)


def _route_output_values(source_root: Path) -> tuple[list[str], list[str]]:
    rules_root = source_root / ".agents" / "rules"
    skills_root = source_root / ".agents" / "skills"
    rules = [
        str(path.relative_to(source_root))
        for path in sorted(rules_root.glob("*.md"))
        if path.is_file() and not path.is_symlink()
    ]
    skills = [
        path.parent.name
        for path in sorted(skills_root.glob("*/SKILL.md"))
        if path.is_file() and not path.is_symlink()
    ]
    if not rules or not skills:
        raise EvalInputError(
            "route output schema requires at least one rule and one skill source"
        )
    return rules, skills


def _write_model_output_schema(
    source: Path,
    destination: Path,
    *,
    route_rules: Sequence[str] | None = None,
    route_skills: Sequence[str] | None = None,
) -> None:
    schema = _read_json_object(source)
    # API structured-output schemas do not need dialect metadata. Keep the
    # checked-in files self-describing while sending only the model contract.
    schema.pop("$schema", None)
    schema.pop("$id", None)
    if (route_rules is None) != (route_skills is None):
        raise EvalInputError(
            "route rule and skill schema values must be supplied together"
        )
    if route_rules is not None and route_skills is not None:
        try:
            properties = schema["properties"]
            rule_items = properties["selected_rules"]["items"]
            skill_items = properties["selected_skills"]["items"]
        except (KeyError, TypeError) as exc:
            raise EvalInputError(
                f"invalid route output schema structure: {source}"
            ) from exc
        rule_items["enum"] = list(route_rules)
        skill_items["enum"] = list(route_skills)
    _atomic_write_json(destination, schema, mode=0o600)


def _string_tuple(record: dict[str, Any], field: str, location: str) -> tuple[str, ...]:
    value = record.get(field)
    if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
        raise EvalInputError(f"{location}: {field} must be an array of strings")
    if len(value) != len(set(value)):
        raise EvalInputError(f"{location}: {field} contains duplicates")
    return tuple(value)


def _parse_case_record(record: Any, location: str) -> tuple[str, str]:
    if not isinstance(record, dict) or set(record) != CASE_FIELDS:
        raise EvalInputError(
            f"{location}: eval case fields must be exactly {sorted(CASE_FIELDS)}"
        )
    eval_id = record["id"]
    task = record["task"]
    if not isinstance(eval_id, str) or not EVAL_ID_PATTERN.fullmatch(eval_id):
        raise EvalInputError(f"{location}: id must use lowercase kebab-case")
    if not isinstance(task, str) or not task.strip():
        raise EvalInputError(f"{location}: task must be a non-empty string")
    return eval_id, task


def _parse_oracle_record(record: Any, location: str) -> dict[str, Any]:
    if not isinstance(record, dict):
        raise EvalInputError(f"{location}: eval oracle must be an object")
    fields = set(record)
    if (
        not ORACLE_REQUIRED_FIELDS <= fields
        or fields - ORACLE_REQUIRED_FIELDS - ORACLE_OPTIONAL_FIELDS
    ):
        raise EvalInputError(
            f"{location}: eval oracle fields must contain "
            f"{sorted(ORACLE_REQUIRED_FIELDS)} and only optional "
            f"{sorted(ORACLE_OPTIONAL_FIELDS)}"
        )
    eval_id = record["id"]
    if not isinstance(eval_id, str) or not EVAL_ID_PATTERN.fullmatch(eval_id):
        raise EvalInputError(f"{location}: id must use lowercase kebab-case")
    behavior_value = record.get("behavior")
    behavior: BehaviorOracle | None = None
    if behavior_value is not None:
        if not isinstance(behavior_value, dict) or set(behavior_value) != {
            "summary",
            "criteria",
            "prohibitions",
        }:
            raise EvalInputError(
                f"{location}: behavior fields must be exactly criteria, prohibitions, summary"
            )
        summary = behavior_value["summary"]
        if not isinstance(summary, str) or not summary.strip():
            raise EvalInputError(f"{location}: behavior summary must be non-empty")
        criteria = _string_tuple(behavior_value, "criteria", location)
        prohibitions = _string_tuple(behavior_value, "prohibitions", location)
        if not criteria or any(not item.strip() for item in criteria + prohibitions):
            raise EvalInputError(
                f"{location}: behavior criteria must be non-empty and all rubrics must contain text"
            )
        behavior = BehaviorOracle(summary, criteria, prohibitions)
    baseline = (
        _string_tuple(record, "baseline_disabled_skills", location)
        if "baseline_disabled_skills" in record
        else ()
    )
    if "baseline_disabled_skills" in record and not baseline:
        raise EvalInputError(
            f"{location}: baseline_disabled_skills must not be empty when present"
        )
    return {
        "id": eval_id,
        "expected_rules": _string_tuple(record, "expected_rules", location),
        "forbidden_rules": _string_tuple(record, "forbidden_rules", location),
        "expected_skills": _string_tuple(record, "expected_skills", location),
        "forbidden_skills": _string_tuple(record, "forbidden_skills", location),
        "behavior": behavior,
        "baseline_disabled_skills": baseline,
    }


def _load_jsonl(path: Path) -> list[tuple[Any, str]]:
    records: list[tuple[Any, str]] = []
    text = _read_utf8(path)
    if not text.strip():
        raise EvalInputError(f"{path}: JSONL file must not be empty")
    for line_number, line in enumerate(text.splitlines(), start=1):
        location = f"{path}:{line_number}"
        if not line.strip():
            raise EvalInputError(f"{location}: blank JSONL line")
        try:
            records.append((json.loads(line), location))
        except json.JSONDecodeError as exc:
            raise EvalInputError(f"{location}: invalid JSON: {exc}") from exc
    return records


def _load_eval_cases(
    source_root: Path, corpora: Sequence[str] | None, ids: Sequence[str] | None
) -> list[EvalCase]:
    selected_files = set(corpora or (Path(name).stem for name in EVAL_FILES))
    requested_ids = set(ids or ())
    cases: list[EvalCase] = []
    seen_ids: set[str] = set()
    for filename in EVAL_FILES:
        corpus = Path(filename).stem
        if corpus not in selected_files:
            continue
        case_path = source_root / "tests" / "evals" / filename
        oracle_path = source_root / "tests" / "evals" / "oracles" / filename
        case_records = _load_jsonl(case_path)
        oracle_records = _load_jsonl(oracle_path)
        if len(case_records) != len(oracle_records):
            raise EvalInputError(
                f"{case_path} and {oracle_path}: case/oracle record counts differ"
            )
        for (case_record, case_location), (oracle_record, oracle_location) in zip(
            case_records, oracle_records, strict=True
        ):
            eval_id, task = _parse_case_record(case_record, case_location)
            oracle = _parse_oracle_record(oracle_record, oracle_location)
            if eval_id != oracle["id"]:
                raise EvalInputError(
                    f"{case_location} and {oracle_location}: case/oracle IDs differ"
                )
            case = EvalCase(
                corpus=corpus,
                id=eval_id,
                task=task,
                expected_rules=oracle["expected_rules"],
                forbidden_rules=oracle["forbidden_rules"],
                expected_skills=oracle["expected_skills"],
                forbidden_skills=oracle["forbidden_skills"],
                behavior=oracle["behavior"],
                baseline_disabled_skills=oracle["baseline_disabled_skills"],
            )
            if case.id in seen_ids:
                raise EvalInputError(f"duplicate eval id: {case.id}")
            seen_ids.add(case.id)
            if not requested_ids or case.id in requested_ids:
                cases.append(case)
    valid_rules = {
        str(path.relative_to(source_root))
        for path in (source_root / ".agents" / "rules").glob("*.md")
    }
    valid_skills = {
        path.parent.name
        for path in (source_root / ".agents" / "skills").glob("*/SKILL.md")
    }
    for case in cases:
        unknown_rules = (
            set(case.expected_rules) | set(case.forbidden_rules)
        ) - valid_rules
        unknown_skills = (
            set(case.expected_skills) | set(case.forbidden_skills)
        ) - valid_skills
        if unknown_rules:
            raise EvalInputError(
                f"eval {case.id} contains unknown rule paths: {sorted(unknown_rules)}"
            )
        if unknown_skills:
            raise EvalInputError(
                f"eval {case.id} contains unknown skills: {sorted(unknown_skills)}"
            )
        if set(case.expected_rules) & set(case.forbidden_rules):
            raise EvalInputError(f"eval {case.id} expects and forbids the same rule")
        if set(case.expected_skills) & set(case.forbidden_skills):
            raise EvalInputError(f"eval {case.id} expects and forbids the same skill")
        if set(case.baseline_disabled_skills) - set(case.expected_skills):
            raise EvalInputError(
                f"eval {case.id} baseline skills must be a subset of expected skills"
            )
        if case.corpus == "routing" and (
            case.behavior is not None or case.baseline_disabled_skills
        ):
            raise EvalInputError(f"routing eval {case.id} must be route-only")
        if case.corpus == "safety":
            if case.behavior is None:
                raise EvalInputError(
                    f"safety eval {case.id} requires a behavior oracle"
                )
            if case.baseline_disabled_skills:
                raise EvalInputError(
                    f"safety eval {case.id} must not define a baseline"
                )
        if case.corpus == "skills":
            if case.expected_skills and (
                case.behavior is None or not case.baseline_disabled_skills
            ):
                raise EvalInputError(
                    f"positive skill eval {case.id} requires behavior and baseline skills"
                )
            if not case.expected_skills and (
                case.behavior is not None or case.baseline_disabled_skills
            ):
                raise EvalInputError(
                    f"negative skill eval {case.id} must be route-only"
                )

    missing = requested_ids - {case.id for case in cases}
    if missing:
        raise EvalInputError(f"unknown or filtered eval ids: {sorted(missing)}")
    if not cases:
        raise EvalInputError("eval selection is empty")
    return cases
