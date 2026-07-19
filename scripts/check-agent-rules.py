#!/usr/bin/env python3
"""Validate the deterministic structure of the Agent Rules Kit."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from pathlib import Path
from typing import Any, Iterable
from urllib.parse import unquote, urlsplit

import yaml
from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError


AGENTS_MAX_BYTES = 16 * 1024
DIALECT = "https://json-schema.org/draft/2020-12/schema"
RULE_NAMES = {
    "index.md",
    "defaults.md",
    "architecture.md",
    "environment.md",
    "scripts.md",
    "security.md",
    "contracts.md",
    "data.md",
    "dependencies.md",
    "testing.md",
    "formatting.md",
    "generated-artifacts.md",
    "cli.md",
    "go.md",
    "rust.md",
    "web.md",
    "flutter-rust.md",
    "nix.md",
    "github-actions.md",
}
SKILL_NAMES = {
    "ai-visual-review",
    "atomic-commit",
    "browser-e2e",
    "compatibility-review",
    "converge-codebase",
    "environment-troubleshooting",
    "generated-artifacts-review",
    "nix-workflow",
    "pure-patch-workflow",
    "sqlite-migration-backup",
    "standalone-final-execution-plan",
}
SKILL_NAME_PATTERN = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
MARKDOWN_LINK_PATTERN = re.compile(r"(?<!!)\[[^\]]*\]\(([^)]+)\)")
FENCED_BLOCK_PATTERN = re.compile(r"^\s*(```|~~~).*?^\s*\1\s*$", re.MULTILINE | re.DOTALL)
FRONTMATTER_PATTERN = re.compile(r"\A---\r?\n(.*?)\r?\n---(?:\r?\n|\Z)", re.DOTALL)
FORBIDDEN_PATHS = (
    ".agents/manifest.json",
    ".agents/templates",
    ".agents/references",
    ".project-agent/shared-rules.lock",
    ".project-agent/route-map.md",
    "schemas/agent-rules",
)
PAYLOAD_MARKERS = (
    "scripts/check-agent-rules.py",
    ".project-agent/rules/maintenance.md",
    "just check-agent-rules",
    "tests/evals/",
)
EVAL_FILES = ("routing.jsonl", "skills.jsonl", "safety.jsonl")
EVAL_SCHEMA_NAMES = {
    "behavior-result.schema.json",
    "eval-oracle.schema.json",
    "eval-record.schema.json",
    "judge-result.schema.json",
    "route-result.schema.json",
    "run-summary.schema.json",
    "runtime-contract.schema.json",
}
EVAL_ID_PATTERN = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")


def _read_text(path: Path, errors: list[str]) -> str | None:
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        errors.append(f"{path}: must be readable UTF-8: {exc}")
        return None


def _read_json(path: Path, errors: list[str]) -> Any | None:
    text = _read_text(path, errors)
    if text is None:
        return None
    try:
        return json.loads(text)
    except json.JSONDecodeError as exc:
        errors.append(f"{path}: invalid JSON: {exc}")
        return None


def _markdown_targets(text: str) -> list[str]:
    without_fences = FENCED_BLOCK_PATTERN.sub("", text)
    targets: list[str] = []
    for match in MARKDOWN_LINK_PATTERN.finditer(without_fences):
        raw = match.group(1).strip()
        if raw.startswith("<") and raw.endswith(">"):
            raw = raw[1:-1].strip()
        if " " in raw and not raw.startswith(("http://", "https://")):
            raw = raw.split(" ", 1)[0]
        targets.append(raw)
    return targets


def _local_target(root: Path, source: Path, target: str) -> Path | None:
    if not target or target.startswith(("#", "//")):
        return None
    parts = urlsplit(target)
    if parts.scheme or parts.netloc:
        return None
    path_text = unquote(parts.path)
    if not path_text:
        return None
    if path_text.startswith("/"):
        return root / path_text.lstrip("/")
    return source.parent / path_text


def _duplicates(values: Iterable[str]) -> list[str]:
    return sorted(value for value, count in Counter(values).items() if count > 1)


def _validate_agents_entrypoint(root: Path, errors: list[str]) -> None:
    path = root / "AGENTS.md"
    try:
        data = path.read_bytes()
    except OSError as exc:
        errors.append(f"{path}: cannot read: {exc}")
        return
    try:
        data.decode("utf-8")
    except UnicodeDecodeError as exc:
        errors.append(f"{path}: must be UTF-8: {exc}")
    if len(data) > AGENTS_MAX_BYTES:
        errors.append(
            f"{path}: exceeds {AGENTS_MAX_BYTES} UTF-8 bytes: {len(data)}"
        )


def _validate_rules(root: Path, errors: list[str]) -> None:
    directory = root / ".agents" / "rules"
    if not directory.is_dir():
        errors.append(f"{directory}: rule directory is missing")
        return

    entries = list(directory.iterdir())
    for entry in sorted(entries):
        if entry.is_dir():
            errors.append(f"{entry}: rule directories are not allowed; keep rules flat")
        elif entry.suffix != ".md":
            errors.append(f"{entry}: only direct Markdown rule files are allowed")

    actual_names = {entry.name for entry in entries if entry.is_file()}
    if actual_names != RULE_NAMES:
        missing = sorted(RULE_NAMES - actual_names)
        extra = sorted(actual_names - RULE_NAMES)
        errors.append(
            f"{directory}: rule set mismatch: missing={missing}, extra={extra}"
        )

    for name in sorted(RULE_NAMES & actual_names):
        path = directory / name
        text = _read_text(path, errors)
        if text is None:
            continue
        if FRONTMATTER_PATTERN.match(text):
            errors.append(f"{path}: rule YAML frontmatter is not allowed")
        without_fences = FENCED_BLOCK_PATTERN.sub("", text)
        h1_count = len(re.findall(r"^# [^#\n].*$", without_fences, re.MULTILINE))
        if h1_count != 1:
            errors.append(f"{path}: rule must contain exactly one Markdown H1")

    index_path = directory / "index.md"
    index_text = _read_text(index_path, errors) if index_path.is_file() else None
    if index_text is None:
        return
    linked = {
        candidate.resolve()
        for target in _markdown_targets(index_text)
        if (candidate := _local_target(root, index_path, target)) is not None
    }
    expected = {
        (directory / name).resolve() for name in RULE_NAMES if name != "index.md"
    }
    expected.update(
        (root / ".agents" / "skills" / name / "SKILL.md").resolve()
        for name in SKILL_NAMES
    )
    for missing in sorted(expected - linked):
        errors.append(f"{index_path}: missing routed Markdown link: {missing}")


def _parse_skill_frontmatter(
    path: Path, errors: list[str]
) -> tuple[dict[str, Any], str] | None:
    text = _read_text(path, errors)
    if text is None:
        return None
    match = FRONTMATTER_PATTERN.match(text)
    if not match:
        errors.append(f"{path}: missing YAML frontmatter")
        return None
    try:
        value = yaml.safe_load(match.group(1))
    except yaml.YAMLError as exc:
        errors.append(f"{path}: invalid YAML frontmatter: {exc}")
        return None
    if not isinstance(value, dict):
        errors.append(f"{path}: frontmatter must be an object")
        return None
    return value, text


def _validate_skills(root: Path, errors: list[str]) -> None:
    directory = root / ".agents" / "skills"
    if not directory.is_dir():
        errors.append(f"{directory}: skill directory is missing")
        return

    actual_names = {entry.name for entry in directory.iterdir() if entry.is_dir()}
    non_directories = [entry for entry in directory.iterdir() if not entry.is_dir()]
    for entry in sorted(non_directories):
        errors.append(f"{entry}: only skill directories are allowed")
    if actual_names != SKILL_NAMES:
        missing = sorted(SKILL_NAMES - actual_names)
        extra = sorted(actual_names - SKILL_NAMES)
        errors.append(
            f"{directory}: skill set mismatch: missing={missing}, extra={extra}"
        )

    parsed_names: list[str] = []
    for directory_name in sorted(SKILL_NAMES & actual_names):
        skill_dir = directory / directory_name
        path = skill_dir / "SKILL.md"
        parsed = _parse_skill_frontmatter(path, errors)
        if parsed is None:
            continue
        metadata, text = parsed
        extra_fields = sorted(set(metadata) - {"name", "description"})
        missing_fields = sorted({"name", "description"} - set(metadata))
        if extra_fields or missing_fields:
            errors.append(
                f"{path}: frontmatter fields must be exactly name and description; "
                f"missing={missing_fields}, extra={extra_fields}"
            )

        name = metadata.get("name")
        if not isinstance(name, str) or not name:
            errors.append(f"{path}: name must be a non-empty string")
        else:
            parsed_names.append(name)
            if len(name) > 64 or not SKILL_NAME_PATTERN.fullmatch(name):
                errors.append(f"{path}: invalid Agent Skills name: {name!r}")
            if name != directory_name:
                errors.append(
                    f"{path}: skill directory {directory_name!r} must match name {name!r}"
                )

        description = metadata.get("description")
        if not isinstance(description, str) or not description.strip():
            errors.append(f"{path}: description must be a non-empty string")
        elif len(description) > 1024:
            errors.append(
                f"{path}: description exceeds 1024 characters: {len(description)}"
            )

        if len(text.splitlines()) >= 500:
            errors.append(f"{path}: SKILL.md must stay below 500 lines")

        allowed_entries = {"SKILL.md", "assets", "references", "scripts"}
        for entry in sorted(skill_dir.iterdir()):
            if entry.name not in allowed_entries:
                errors.append(f"{entry}: unsupported skill-local entry")

        linked = {
            candidate.resolve()
            for target in _markdown_targets(text)
            if (candidate := _local_target(root, path, target)) is not None
        }
        for owner_dir_name in ("assets", "references", "scripts"):
            owner_dir = skill_dir / owner_dir_name
            if not owner_dir.exists():
                continue
            if not owner_dir.is_dir():
                errors.append(f"{owner_dir}: skill-local owner path must be a directory")
                continue
            for item in sorted(owner_dir.rglob("*")):
                if item.is_dir():
                    if item != owner_dir:
                        errors.append(f"{item}: nested skill-local directories are not allowed")
                    continue
                if item.parent != owner_dir:
                    continue
                if item.resolve() not in linked:
                    errors.append(f"{item}: orphan skill-local file; link it from {path}")

    for duplicate in _duplicates(parsed_names):
        errors.append(f"duplicate skill name: {duplicate}")


def _validate_markdown_links(root: Path, errors: list[str]) -> None:
    candidates = [root / "AGENTS.md", root / "README.md"]
    agents = root / ".agents"
    if agents.is_dir():
        candidates.extend(agents.rglob("*.md"))
    for path in sorted(set(candidates)):
        if not path.is_file():
            continue
        text = _read_text(path, errors)
        if text is None:
            continue
        for target in _markdown_targets(text):
            local = _local_target(root, path, target)
            if local is not None and not local.exists():
                errors.append(f"{path}: broken local Markdown link: {target}")


def _validate_forbidden_paths(root: Path, errors: list[str]) -> None:
    for relative in FORBIDDEN_PATHS:
        path = root / relative
        if path.exists():
            errors.append(f"{path}: deleted metadata or global catalog path is forbidden")


def _validate_schemas(root: Path, errors: list[str]) -> None:
    schema_paths = list((root / ".agents").rglob("*.schema.json"))
    eval_schema_dir = root / "tests" / "evals" / "schemas"
    schema_paths.extend(eval_schema_dir.glob("*.schema.json"))
    present_eval_schemas = {path.name for path in schema_paths if path.parent == eval_schema_dir}
    missing_eval_schemas = EVAL_SCHEMA_NAMES - present_eval_schemas
    if missing_eval_schemas:
        errors.append(f"{eval_schema_dir}: missing eval schemas: {sorted(missing_eval_schemas)}")
    for path in sorted(schema_paths):
        value = _read_json(path, errors)
        if not isinstance(value, dict):
            if value is not None:
                errors.append(f"{path}: schema must be a JSON object")
            continue
        if value.get("$schema") != DIALECT:
            errors.append(f"{path}: $schema must declare {DIALECT}")
        try:
            Draft202012Validator.check_schema(value)
        except SchemaError as exc:
            errors.append(f"{path}: invalid Draft 2020-12 schema: {exc.message}")

    runtime_schema_path = eval_schema_dir / "runtime-contract.schema.json"
    runtime_contract_path = root / "tests" / "evals" / "codex-runtime-contract.json"
    schema = _read_json(runtime_schema_path, errors)
    contract = _read_json(runtime_contract_path, errors)
    if isinstance(schema, dict) and isinstance(contract, dict):
        try:
            Draft202012Validator.check_schema(schema)
        except SchemaError:
            return
        for failure in Draft202012Validator(schema).iter_errors(contract):
            location = "/".join(str(part) for part in failure.absolute_path) or "<root>"
            errors.append(
                f"{runtime_contract_path}: runtime contract schema failure at "
                f"{location}: {failure.message}"
            )


def _validate_payload_boundary(root: Path, errors: list[str]) -> None:
    candidates = [root / "AGENTS.md"]
    agents = root / ".agents"
    if agents.is_dir():
        candidates.extend(path for path in agents.rglob("*") if path.is_file())
    for path in candidates:
        try:
            content = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError):
            continue
        for marker in PAYLOAD_MARKERS:
            if marker in content:
                errors.append(
                    f"{path}: distributed payload leaks maintenance-only marker: {marker}"
                )


def _string_list(
    record: dict[str, Any], field: str, path: Path, line_number: int, errors: list[str]
) -> list[str]:
    value = record.get(field)
    if not isinstance(value, list) or any(not isinstance(item, str) for item in value):
        errors.append(f"{path}:{line_number}: {field} must be an array of strings")
        return []
    if len(value) != len(set(value)):
        errors.append(f"{path}:{line_number}: {field} contains duplicates")
    return value


def _validate_evals(root: Path, errors: list[str]) -> None:
    eval_dir = root / "tests" / "evals"
    oracle_dir = eval_dir / "oracles"
    case_schema = _read_json(eval_dir / "schemas" / "eval-record.schema.json", errors)
    oracle_schema = _read_json(eval_dir / "schemas" / "eval-oracle.schema.json", errors)
    case_validator = (
        Draft202012Validator(case_schema) if isinstance(case_schema, dict) else None
    )
    oracle_validator = (
        Draft202012Validator(oracle_schema)
        if isinstance(oracle_schema, dict)
        else None
    )
    all_ids: list[str] = []
    all_oracle_ids: list[str] = []
    covered_rules: set[str] = set()
    skill_file_positive: set[str] = set()
    skill_file_negative: set[str] = set()

    valid_rule_paths = {f".agents/rules/{name}" for name in RULE_NAMES}
    for filename in EVAL_FILES:
        case_path = eval_dir / filename
        oracle_path = oracle_dir / filename
        case_text = _read_text(case_path, errors)
        oracle_text = _read_text(oracle_path, errors)
        if case_text is None or oracle_text is None:
            continue
        if not case_text.strip():
            errors.append(f"{case_path}: eval file must not be empty")
            continue
        if not oracle_text.strip():
            errors.append(f"{oracle_path}: oracle file must not be empty")
            continue

        case_ids: list[str] = []
        for line_number, line in enumerate(case_text.splitlines(), start=1):
            if not line.strip():
                errors.append(
                    f"{case_path}:{line_number}: blank JSONL lines are not allowed"
                )
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                errors.append(f"{case_path}:{line_number}: invalid JSON: {exc}")
                continue
            if not isinstance(record, dict):
                errors.append(f"{case_path}:{line_number}: eval case must be an object")
                continue
            if case_validator is not None:
                for failure in case_validator.iter_errors(record):
                    location = "/".join(
                        str(part) for part in failure.absolute_path
                    ) or "<root>"
                    errors.append(
                        f"{case_path}:{line_number}: case schema failure at "
                        f"{location}: {failure.message}"
                    )
            if set(record) != {"id", "task"}:
                errors.append(
                    f"{case_path}:{line_number}: eval case fields must be exactly "
                    "['id', 'task']"
                )
            record_id = record.get("id")
            if not isinstance(record_id, str) or not EVAL_ID_PATTERN.fullmatch(record_id):
                errors.append(f"{case_path}:{line_number}: id must use lowercase kebab-case")
            else:
                case_ids.append(record_id)
                all_ids.append(record_id)
            task = record.get("task")
            if not isinstance(task, str) or not task.strip():
                errors.append(
                    f"{case_path}:{line_number}: task must be a non-empty string"
                )

        oracle_ids: list[str] = []
        for line_number, line in enumerate(oracle_text.splitlines(), start=1):
            if not line.strip():
                errors.append(
                    f"{oracle_path}:{line_number}: blank JSONL lines are not allowed"
                )
                continue
            try:
                record = json.loads(line)
            except json.JSONDecodeError as exc:
                errors.append(f"{oracle_path}:{line_number}: invalid JSON: {exc}")
                continue
            if not isinstance(record, dict):
                errors.append(f"{oracle_path}:{line_number}: eval oracle must be an object")
                continue
            if oracle_validator is not None:
                for failure in oracle_validator.iter_errors(record):
                    location = "/".join(
                        str(part) for part in failure.absolute_path
                    ) or "<root>"
                    errors.append(
                        f"{oracle_path}:{line_number}: oracle schema failure at "
                        f"{location}: {failure.message}"
                    )
            required = {
                "id",
                "expected_rules",
                "forbidden_rules",
                "expected_skills",
                "forbidden_skills",
            }
            optional = {"behavior", "baseline_disabled_skills"}
            if not required <= set(record) or set(record) - required - optional:
                errors.append(
                    f"{oracle_path}:{line_number}: eval oracle fields must contain "
                    f"{sorted(required)} and only optional {sorted(optional)}"
                )
            record_id = record.get("id")
            if not isinstance(record_id, str) or not EVAL_ID_PATTERN.fullmatch(record_id):
                errors.append(f"{oracle_path}:{line_number}: id must use lowercase kebab-case")
            else:
                oracle_ids.append(record_id)
                all_oracle_ids.append(record_id)

            expected_rules = _string_list(
                record, "expected_rules", oracle_path, line_number, errors
            )
            forbidden_rules = _string_list(
                record, "forbidden_rules", oracle_path, line_number, errors
            )
            expected_skills = _string_list(
                record, "expected_skills", oracle_path, line_number, errors
            )
            forbidden_skills = _string_list(
                record, "forbidden_skills", oracle_path, line_number, errors
            )
            for rule in expected_rules:
                if rule not in valid_rule_paths:
                    errors.append(f"{oracle_path}:{line_number}: unknown rule path: {rule}")
                else:
                    covered_rules.add(rule)
            for rule in forbidden_rules:
                if rule not in valid_rule_paths:
                    errors.append(f"{oracle_path}:{line_number}: unknown rule path: {rule}")
            rule_overlap = set(expected_rules) & set(forbidden_rules)
            if rule_overlap:
                errors.append(
                    f"{oracle_path}:{line_number}: rules cannot be expected and forbidden: "
                    f"{sorted(rule_overlap)}"
                )
            for name in expected_skills + forbidden_skills:
                if name not in SKILL_NAMES:
                    errors.append(
                        f"{oracle_path}:{line_number}: unknown skill name: {name}"
                    )
            skill_overlap = set(expected_skills) & set(forbidden_skills)
            if skill_overlap:
                errors.append(
                    f"{oracle_path}:{line_number}: skills cannot be expected and forbidden: "
                    f"{sorted(skill_overlap)}"
                )

            behavior = record.get("behavior")
            if behavior is not None:
                location = f"{oracle_path}:{line_number}:behavior"
                if not isinstance(behavior, dict) or set(behavior) != {
                    "summary",
                    "criteria",
                    "prohibitions",
                }:
                    errors.append(
                        f"{location}: fields must be exactly criteria, prohibitions, summary"
                    )
                else:
                    summary = behavior["summary"]
                    if not isinstance(summary, str) or not summary.strip():
                        errors.append(f"{location}: summary must be a non-empty string")
                    criteria = _string_list(
                        behavior, "criteria", oracle_path, line_number, errors
                    )
                    prohibitions = _string_list(
                        behavior, "prohibitions", oracle_path, line_number, errors
                    )
                    if not criteria or any(not item.strip() for item in criteria):
                        errors.append(
                            f"{location}: criteria must contain non-empty strings"
                        )
                    if any(not item.strip() for item in prohibitions):
                        errors.append(
                            f"{location}: prohibitions must contain non-empty strings"
                        )

            baseline = record.get("baseline_disabled_skills")
            baseline_skills: list[str] = []
            if baseline is not None:
                baseline_skills = _string_list(
                    record,
                    "baseline_disabled_skills",
                    oracle_path,
                    line_number,
                    errors,
                )
                for name in baseline_skills:
                    if name not in SKILL_NAMES:
                        errors.append(
                            f"{oracle_path}:{line_number}: unknown baseline skill: {name}"
                        )
                if not baseline_skills or not set(baseline_skills) <= set(expected_skills):
                    errors.append(
                        f"{oracle_path}:{line_number}: baseline_disabled_skills must be "
                        "a non-empty subset of expected_skills"
                    )

            if filename == "skills.jsonl":
                skill_file_positive.update(expected_skills)
                skill_file_negative.update(forbidden_skills)
                if expected_skills and (behavior is None or not baseline_skills):
                    errors.append(
                        f"{oracle_path}:{line_number}: positive skill eval requires "
                        "behavior and baseline_disabled_skills"
                    )
                if not expected_skills and (behavior is not None or baseline is not None):
                    errors.append(
                        f"{oracle_path}:{line_number}: negative skill eval must be "
                        "route-only"
                    )
            elif filename == "safety.jsonl":
                if behavior is None:
                    errors.append(
                        f"{oracle_path}:{line_number}: safety eval requires behavior"
                    )
                if baseline is not None:
                    errors.append(
                        f"{oracle_path}:{line_number}: safety eval must not define a baseline"
                    )
            elif behavior is not None or baseline is not None:
                errors.append(
                    f"{oracle_path}:{line_number}: routing eval must be route-only"
                )

        if case_ids != oracle_ids:
            errors.append(
                f"{case_path} and {oracle_path}: case/oracle IDs must match in order; "
                f"cases={case_ids}, oracles={oracle_ids}"
            )

    for duplicate in _duplicates(all_ids):
        errors.append(f"duplicate eval id: {duplicate}")
    for duplicate in _duplicates(all_oracle_ids):
        errors.append(f"duplicate eval oracle id: {duplicate}")
    missing_rules = sorted(valid_rule_paths - covered_rules)
    if missing_rules:
        errors.append(f"eval corpus missing rule coverage: {missing_rules}")
    for name in sorted(SKILL_NAMES):
        if name not in skill_file_positive:
            errors.append(f"skills eval missing positive coverage: {name}")
        if name not in skill_file_negative:
            errors.append(f"skills eval missing negative coverage: {name}")


def check_repository(root: Path) -> list[str]:
    root = root.resolve()
    errors: list[str] = []
    _validate_agents_entrypoint(root, errors)
    _validate_forbidden_paths(root, errors)
    _validate_rules(root, errors)
    _validate_skills(root, errors)
    _validate_markdown_links(root, errors)
    _validate_schemas(root, errors)
    _validate_payload_boundary(root, errors)
    _validate_evals(root, errors)
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root",
        type=Path,
        default=Path.cwd(),
        help="repository root (default: current directory)",
    )
    args = parser.parse_args(argv)
    errors = check_repository(args.root)
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        print(
            f"agent rules check failed with {len(errors)} error(s)",
            file=sys.stderr,
        )
        return 1
    print("agent rules check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
