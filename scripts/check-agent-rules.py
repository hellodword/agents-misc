#!/usr/bin/env python3
"""Validate the structural contracts of the Agent Rules Kit."""

from __future__ import annotations

import argparse
import json
import re
import sys
from collections import Counter
from pathlib import Path
from typing import Any, Iterable

import yaml
from jsonschema import Draft202012Validator
from jsonschema.exceptions import SchemaError


DIALECT = "https://json-schema.org/draft/2020-12/schema"
EXACT_AGENT_PATH = re.compile(r"\.agents/[A-Za-z0-9_.*\-/]+")
MAINTENANCE_ONLY_MARKERS = (
    "scripts/check-agent-rules.py",
    "schemas/agent-rules/",
    ".project-agent/rules/agent-rules-kit.md",
    "just check-agent-rules",
    ".agents/schemas/",
)


def _read_json(path: Path, errors: list[str]) -> Any | None:
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        errors.append(f"{path}: invalid JSON: {exc}")
        return None


def _frontmatter(path: Path, errors: list[str]) -> dict[str, Any] | None:
    try:
        text = path.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"{path}: cannot read: {exc}")
        return None
    match = re.match(r"\A---\r?\n(.*?)\r?\n---(?:\r?\n|\Z)", text, re.DOTALL)
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
    return value


def _validate(instance: Any, schema: dict[str, Any], path: Path, errors: list[str]) -> bool:
    validator = Draft202012Validator(schema)
    validation_errors = sorted(
        validator.iter_errors(instance), key=lambda item: list(item.absolute_path)
    )
    for error in validation_errors:
        location = ".".join(str(part) for part in error.absolute_path)
        suffix = f" at {location}" if location else ""
        errors.append(f"{path}: schema validation failed{suffix}: {error.message}")
    return not validation_errors


def _asset_id(path: Path) -> str:
    if path.name.endswith(".md"):
        return path.name[: -len(".md")]
    if path.name.endswith(".json"):
        return path.name[: -len(".json")]
    return path.name


def _asset_ids(directory: Path) -> set[str]:
    if not directory.is_dir():
        return set()
    return {_asset_id(path) for path in directory.iterdir() if path.is_file()}


def _duplicates(values: Iterable[str]) -> list[str]:
    return sorted(value for value, count in Counter(values).items() if count > 1)


def _lock_manifest_fields(manifest: dict[str, Any]) -> dict[str, str]:
    fields = {
        "expected_name": "name",
        "expected_version": "version",
        "expected_manifest_schema_version": "schema_version",
    }
    fields.update(
        (f"expected_{key}", key)
        for key in manifest
        if key.endswith("_version") and key != "schema_version"
    )
    return fields


def _validate_exact_agent_paths(root: Path, errors: list[str]) -> None:
    candidates = [root / "AGENTS.md", root / "README.md"]
    candidates.extend((root / ".agents").rglob("*.md"))
    for path in candidates:
        if not path.is_file():
            continue
        text = path.read_text(encoding="utf-8")
        for raw in EXACT_AGENT_PATH.findall(text):
            referenced = raw.rstrip(".")
            if "*" in referenced or referenced.endswith("/"):
                continue
            if not (root / referenced).exists():
                errors.append(f"{path}: referenced path does not exist: {referenced}")


def _validate_distributed_payload_boundary(root: Path, errors: list[str]) -> None:
    candidates = [root / "AGENTS.md"]
    candidates.extend(path for path in (root / ".agents").rglob("*") if path.is_file())
    for path in candidates:
        if not path.is_file():
            continue
        try:
            content = path.read_text(encoding="utf-8")
        except (OSError, UnicodeDecodeError):
            continue
        for marker in MAINTENANCE_ONLY_MARKERS:
            if marker in content:
                errors.append(f"{path}: distributed payload leaks maintenance-only marker: {marker}")


def check_repository(root: Path) -> list[str]:
    root = root.resolve()
    errors: list[str] = []
    agents = root / ".agents"
    schemas_dir = root / "schemas" / "agent-rules"
    lock_schema_path = agents / "templates" / "shared-rules-lock.schema.json"

    schemas: dict[str, dict[str, Any]] = {}
    for name in [
        "manifest.schema.json",
        "rule-frontmatter.schema.json",
        "skill-frontmatter.schema.json",
    ]:
        path = schemas_dir / name
        value = _read_json(path, errors)
        if isinstance(value, dict):
            schemas[name] = value
        elif value is not None:
            errors.append(f"{path}: schema must be a JSON object")

    schema_paths = set(schemas_dir.glob("*.schema.json"))
    schema_paths.update(agents.rglob("*.schema.json"))
    for path in sorted(schema_paths):
        value = _read_json(path, errors)
        if not isinstance(value, dict):
            continue
        if value.get("$schema") != DIALECT:
            errors.append(f"{path}: $schema must declare {DIALECT}")
        try:
            Draft202012Validator.check_schema(value)
        except SchemaError as exc:
            errors.append(f"{path}: invalid Draft 2020-12 schema: {exc.message}")

    manifest_path = agents / "manifest.json"
    manifest = _read_json(manifest_path, errors)
    manifest_schema = schemas.get("manifest.schema.json")
    manifest_valid = False
    if manifest is not None and manifest_schema is not None:
        manifest_valid = _validate(manifest, manifest_schema, manifest_path, errors)

    rule_schema = schemas.get("rule-frontmatter.schema.json")
    rules: list[tuple[Path, dict[str, Any]]] = []
    for path in sorted((agents / "rules").rglob("*.md")):
        data = _frontmatter(path, errors)
        if data is None:
            continue
        if rule_schema is not None:
            _validate(data, rule_schema, path, errors)
        rules.append((path, data))

    skill_schema = schemas.get("skill-frontmatter.schema.json")
    skills: list[tuple[Path, dict[str, Any]]] = []
    for path in sorted((agents / "skills").glob("*/SKILL.md")):
        data = _frontmatter(path, errors)
        if data is None:
            continue
        if skill_schema is not None:
            _validate(data, skill_schema, path, errors)
        name = data.get("name")
        if isinstance(name, str) and path.parent.name != name:
            errors.append(f"{path}: skill directory {path.parent.name!r} must match name {name!r}")
        skills.append((path, data))

    rule_ids = [data["id"] for _, data in rules if isinstance(data.get("id"), str)]
    skill_names = [data["name"] for _, data in skills if isinstance(data.get("name"), str)]
    for duplicate in _duplicates(rule_ids):
        errors.append(f"duplicate rule id: {duplicate}")
    for duplicate in _duplicates(skill_names):
        errors.append(f"duplicate skill name: {duplicate}")

    known = {
        "required_rules": set(rule_ids),
        "conditional_rules": set(rule_ids),
        "skills": set(skill_names),
        "templates": _asset_ids(agents / "templates"),
        "references": _asset_ids(agents / "references"),
    }
    for path, data in rules:
        companions = data.get("companions")
        if not isinstance(companions, dict):
            continue
        for category, entries in companions.items():
            if not isinstance(entries, list) or category not in known:
                continue
            ids = entries if category == "required_rules" else [entry.get("id") for entry in entries if isinstance(entry, dict)]
            for companion_id in ids:
                if isinstance(companion_id, str) and companion_id not in known[category]:
                    errors.append(f"{path}: unresolved {category} companion: {companion_id}")

    route_path = agents / "rules" / "route-map.md"
    try:
        route_text = route_path.read_text(encoding="utf-8")
    except OSError as exc:
        errors.append(f"{route_path}: cannot read route map: {exc}")
        route_text = ""
    for path, _ in rules:
        if path == route_path:
            continue
        relative = path.relative_to(root).as_posix()
        if relative not in route_text:
            errors.append(f"{route_path}: missing rule route coverage: {relative}")
    for path, _ in skills:
        relative = path.relative_to(root).as_posix()
        if relative not in route_text:
            errors.append(f"{route_path}: missing skill route coverage: {relative}")

    lock_schema_value = _read_json(lock_schema_path, errors)
    lock_schema = lock_schema_value if isinstance(lock_schema_value, dict) else None
    if lock_schema_value is not None and lock_schema is None:
        errors.append(f"{lock_schema_path}: schema must be a JSON object")

    project_lock_path = root / ".project-agent" / "shared-rules.lock"
    project_lock: Any | None = None
    project_lock_valid = False
    if not project_lock_path.is_file():
        errors.append(f"{project_lock_path}: required upstream lock is missing")
    elif lock_schema is not None:
        project_lock = _read_json(project_lock_path, errors)
        if project_lock is not None:
            project_lock_valid = _validate(project_lock, lock_schema, project_lock_path, errors)

    if isinstance(manifest, dict) and lock_schema is not None:
        lock_manifest_fields = _lock_manifest_fields(manifest)
        expected_fields = {"schema_version", *lock_manifest_fields}
        properties = set(lock_schema.get("properties", {}))
        required = set(lock_schema.get("required", []))
        properties_match = properties == expected_fields
        required_match = required == expected_fields
        if not properties_match:
            errors.append(
                f"{lock_schema_path}: lock properties do not match manifest dimensions: expected {sorted(expected_fields)}, got {sorted(properties)}"
            )
        if not required_match:
            errors.append(
                f"{lock_schema_path}: lock required fields do not match manifest dimensions: expected {sorted(expected_fields)}, got {sorted(required)}"
            )
        if (
            properties_match
            and required_match
            and manifest_valid
            and project_lock_valid
            and isinstance(project_lock, dict)
        ):
            for lock_field in sorted(lock_manifest_fields):
                manifest_field = lock_manifest_fields[lock_field]
                lock_value = project_lock[lock_field]
                manifest_value = manifest[manifest_field]
                if lock_value != manifest_value:
                    errors.append(
                        f"{project_lock_path}: lock mismatch: {lock_field}={lock_value!r} "
                        f"does not match manifest.{manifest_field}={manifest_value!r}"
                    )

    _validate_exact_agent_paths(root, errors)
    _validate_distributed_payload_boundary(root, errors)
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path.cwd(), help="repository root (default: current directory)")
    args = parser.parse_args(argv)
    errors = check_repository(args.root)
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        print(f"agent rules check failed with {len(errors)} error(s)", file=sys.stderr)
        return 1
    print("agent rules check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
