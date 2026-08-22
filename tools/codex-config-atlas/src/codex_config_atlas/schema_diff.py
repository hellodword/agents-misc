from __future__ import annotations

import json
from typing import Any


CATEGORY_LABELS = {
    "breakingLike": "Breaking-like",
    "review": "Needs review",
    "behavior": "Behavior",
    "compatible": "Compatible",
    "documentation": "Documentation",
}

PROFILE_PATH_PREFIX = "profiles.<name>."
FIELD_SEMANTIC_KEYS = (
    "kind",
    "types",
    "required",
    "hasDefault",
    "default",
    "enum",
    "description",
    "deprecated",
    "additionalPropertiesMode",
)


def _field_index(fields: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    for field in fields:
        _required_state(field)
    return {field["path"]: field for field in fields}


def _required_state(field: dict[str, Any]) -> str:
    state = field.get("required")
    if state not in {"always", "conditional", "never"}:
        raise ValueError(
            f"field {field.get('path', '<unknown>')} has invalid required state: {state!r}"
        )
    return state


def _added_field_category(required: str) -> str:
    if required == "always":
        return "breakingLike"
    if required == "conditional":
        return "review"
    return "compatible"


def _required_change_category(before: str, after: str) -> str:
    if after == "always":
        return "breakingLike"
    if "conditional" in {before, after}:
        return "review"
    return "compatible"


def _change_without_path(change: dict[str, Any]) -> dict[str, Any]:
    return {key: value for key, value in change.items() if key != "path"}


def _field_semantics(field: dict[str, Any] | None) -> dict[str, Any] | None:
    if field is None:
        return None
    return {key: field.get(key) for key in FIELD_SEMANTIC_KEYS}


def _is_duplicate_profile_change(
    change: dict[str, Any],
    base_changes: list[dict[str, Any]],
    before: dict[str, dict[str, Any]],
    after: dict[str, dict[str, Any]],
    base_path: str,
) -> bool:
    if not any(
        _change_without_path(candidate) == _change_without_path(change)
        for candidate in base_changes
    ):
        return False

    path = change["path"]
    kind = change["kind"]
    if kind == "field_added":
        return _field_semantics(after.get(path)) == _field_semantics(
            after.get(base_path)
        )
    if kind == "field_removed":
        return _field_semantics(before.get(path)) == _field_semantics(
            before.get(base_path)
        )
    if kind == "description_changed":
        profile_descriptions = (
            (before.get(path) or {}).get("description") or "",
            (after.get(path) or {}).get("description") or "",
        )
        base_descriptions = (
            (before.get(base_path) or {}).get("description") or "",
            (after.get(base_path) or {}).get("description") or "",
        )
        return profile_descriptions == base_descriptions
    return True


def _suppress_duplicate_profile_changes(
    changes: list[dict[str, Any]],
    before: dict[str, dict[str, Any]],
    after: dict[str, dict[str, Any]],
) -> list[dict[str, Any]]:
    changes_by_path: dict[str, list[dict[str, Any]]] = {}
    for change in changes:
        changes_by_path.setdefault(change["path"], []).append(change)

    visible_changes = []
    for change in changes:
        path = change["path"]
        if not path.startswith(PROFILE_PATH_PREFIX):
            visible_changes.append(change)
            continue

        base_path = path.removeprefix(PROFILE_PATH_PREFIX)
        if not _is_duplicate_profile_change(
            change,
            changes_by_path.get(base_path, []),
            before,
            after,
            base_path,
        ):
            visible_changes.append(change)
    return visible_changes


def _additional_properties_rank(mode: str | None) -> int:
    order = {
        None: 0,
        "forbid": 0,
        "typed": 1,
        "allow_any": 2,
    }
    return order.get(mode, 0)


def _types(field: dict[str, Any]) -> set[str]:
    return set(field.get("types") or [])


def _enum(field: dict[str, Any]) -> set[Any]:
    return set(field.get("enum") or [])


def build_schema_diff(
    from_version: str,
    to_version: str,
    from_fields: list[dict[str, Any]],
    to_fields: list[dict[str, Any]],
) -> dict[str, Any]:
    before = _field_index(from_fields)
    after = _field_index(to_fields)
    changes: list[dict[str, Any]] = []

    all_paths = sorted(set(before) | set(after))
    for path in all_paths:
        left = before.get(path)
        right = after.get(path)
        if left is None:
            changes.append(
                {
                    "kind": "field_added",
                    "category": _added_field_category(right["required"]),
                    "path": path,
                    "to": {
                        "types": right["types"],
                        "hasDefault": right["hasDefault"],
                        "required": right["required"],
                    },
                }
            )
            continue
        if right is None:
            changes.append(
                {
                    "kind": "field_removed",
                    "category": "breakingLike",
                    "path": path,
                    "from": {
                        "types": left["types"],
                        "hasDefault": left["hasDefault"],
                        "required": left["required"],
                    },
                }
            )
            continue

        left_types = _types(left)
        right_types = _types(right)
        if left_types != right_types:
            if right_types < left_types:
                category = "breakingLike"
                kind = "type_narrowed"
            elif left_types < right_types:
                category = "compatible"
                kind = "type_widened"
            else:
                category = "breakingLike"
                kind = "type_changed"
            changes.append(
                {
                    "kind": kind,
                    "category": category,
                    "path": path,
                    "from": sorted(left_types),
                    "to": sorted(right_types),
                }
            )

        left_enum = _enum(left)
        right_enum = _enum(right)
        removed_enum = sorted(left_enum - right_enum)
        if removed_enum:
            changes.append(
                {
                    "kind": "enum_values_removed",
                    "category": "breakingLike",
                    "path": path,
                    "values": removed_enum,
                }
            )
        added_enum = sorted(right_enum - left_enum)
        if added_enum:
            changes.append(
                {
                    "kind": "enum_values_added",
                    "category": "compatible",
                    "path": path,
                    "values": added_enum,
                }
            )

        if left["required"] != right["required"]:
            changes.append(
                {
                    "kind": "required_changed",
                    "category": _required_change_category(
                        left["required"], right["required"]
                    ),
                    "path": path,
                    "from": left["required"],
                    "to": right["required"],
                }
            )

        left_mode = left.get("additionalPropertiesMode")
        right_mode = right.get("additionalPropertiesMode")
        if left_mode != right_mode:
            if _additional_properties_rank(right_mode) < _additional_properties_rank(
                left_mode
            ):
                category = "breakingLike"
                kind = "additional_properties_restricted"
            else:
                category = "compatible"
                kind = "additional_properties_relaxed"
            changes.append(
                {
                    "kind": kind,
                    "category": category,
                    "path": path,
                    "from": left_mode,
                    "to": right_mode,
                }
            )

        if left["hasDefault"] and right["hasDefault"]:
            if left["default"] != right["default"]:
                changes.append(
                    {
                        "kind": "default_changed",
                        "category": "behavior",
                        "path": path,
                        "from": left["default"],
                        "to": right["default"],
                    }
                )
        elif left["hasDefault"] and not right["hasDefault"]:
            changes.append(
                {
                    "kind": "default_removed",
                    "category": "behavior",
                    "path": path,
                    "from": left["default"],
                }
            )
        elif not left["hasDefault"] and right["hasDefault"]:
            changes.append(
                {
                    "kind": "default_added",
                    "category": "behavior",
                    "path": path,
                    "to": right["default"],
                }
            )

        if (left.get("description") or "") != (right.get("description") or ""):
            changes.append(
                {
                    "kind": "description_changed",
                    "category": "documentation",
                    "path": path,
                }
            )

        if bool(left.get("deprecated")) != bool(right.get("deprecated")):
            changes.append(
                {
                    "kind": "deprecated_changed",
                    "category": "documentation",
                    "path": path,
                    "from": bool(left.get("deprecated")),
                    "to": bool(right.get("deprecated")),
                }
            )

    changes = _suppress_duplicate_profile_changes(changes, before, after)
    summary = {
        "breakingLike": sum(item["category"] == "breakingLike" for item in changes),
        "review": sum(item["category"] == "review" for item in changes),
        "behavior": sum(item["category"] == "behavior" for item in changes),
        "compatible": sum(item["category"] == "compatible" for item in changes),
        "documentation": sum(item["category"] == "documentation" for item in changes),
    }
    return {
        "from": from_version,
        "to": to_version,
        "summary": summary,
        "changes": changes,
    }


def render_schema_diff_markdown(payload: dict[str, Any]) -> str:
    lines = [f"# Codex config schema diff: {payload['from']} -> {payload['to']}", ""]
    for category, title in CATEGORY_LABELS.items():
        items = [item for item in payload["changes"] if item["category"] == category]
        if not items:
            continue
        lines.append(f"## {title}")
        lines.append("")
        for item in items:
            path = item["path"]
            kind = item["kind"]
            if kind == "field_removed":
                lines.append(f"- Removed field: `{path}`")
            elif kind == "field_added":
                lines.append(f"- Added field: `{path}`")
            elif kind == "default_changed":
                lines.append(f"- Default changed: `{path}`")
                lines.append(f"  - From: `{json.dumps(item['from'])}`")
                lines.append(f"  - To: `{json.dumps(item['to'])}`")
            elif kind == "default_added":
                lines.append(f"- Default added: `{path}`")
                lines.append(f"  - To: `{json.dumps(item['to'])}`")
            elif kind == "default_removed":
                lines.append(f"- Default removed: `{path}`")
                lines.append(f"  - From: `{json.dumps(item['from'])}`")
            elif kind in {"type_narrowed", "type_widened", "type_changed"}:
                lines.append(f"- Type change: `{path}`")
                lines.append(f"  - From: `{', '.join(item['from'])}`")
                lines.append(f"  - To: `{', '.join(item['to'])}`")
            elif kind == "enum_values_added":
                lines.append(f"- Enum values added: `{path}`")
                lines.append(f"  - Values: `{', '.join(map(str, item['values']))}`")
            elif kind == "enum_values_removed":
                lines.append(f"- Enum values removed: `{path}`")
                lines.append(f"  - Values: `{', '.join(map(str, item['values']))}`")
            elif kind == "description_changed":
                lines.append(f"- Description changed: `{path}`")
            elif kind == "deprecated_changed":
                lines.append(f"- Deprecated changed: `{path}`")
            elif kind == "required_changed":
                lines.append(f"- Required state changed: `{path}`")
                lines.append(f"  - From: `{item['from']}`")
                lines.append(f"  - To: `{item['to']}`")
            elif kind == "additional_properties_restricted":
                lines.append(f"- Additional properties restricted: `{path}`")
            elif kind == "additional_properties_relaxed":
                lines.append(f"- Additional properties relaxed: `{path}`")
            else:
                lines.append(f"- {kind}: `{path}`")
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"
