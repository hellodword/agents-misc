from __future__ import annotations

import json
from typing import Any


CATEGORY_LABELS = {
    "breakingLike": "Breaking-like",
    "behavior": "Behavior",
    "compatible": "Compatible",
    "documentation": "Documentation",
}


def _field_index(fields: list[dict[str, Any]]) -> dict[str, dict[str, Any]]:
    return {field["path"]: field for field in fields}


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
                    "category": "breakingLike" if right["required"] else "compatible",
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

        if not left["required"] and right["required"]:
            changes.append(
                {
                    "kind": "required_became_true",
                    "category": "breakingLike",
                    "path": path,
                }
            )
        elif left["required"] and not right["required"]:
            changes.append(
                {
                    "kind": "required_became_false",
                    "category": "compatible",
                    "path": path,
                }
            )

        left_mode = left.get("additionalPropertiesMode")
        right_mode = right.get("additionalPropertiesMode")
        if left_mode != right_mode:
            if _additional_properties_rank(right_mode) < _additional_properties_rank(left_mode):
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

    summary = {
        "breakingLike": sum(item["category"] == "breakingLike" for item in changes),
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
            elif kind == "required_became_true":
                lines.append(f"- Field became required: `{path}`")
            elif kind == "required_became_false":
                lines.append(f"- Field became optional: `{path}`")
            elif kind == "additional_properties_restricted":
                lines.append(f"- Additional properties restricted: `{path}`")
            elif kind == "additional_properties_relaxed":
                lines.append(f"- Additional properties relaxed: `{path}`")
            else:
                lines.append(f"- {kind}: `{path}`")
        lines.append("")
    return "\n".join(lines).rstrip() + "\n"
