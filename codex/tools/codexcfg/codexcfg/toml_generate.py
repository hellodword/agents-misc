from __future__ import annotations

from collections import defaultdict
from typing import Any

from tomlkit import item as toml_item


def _header(version: str, tag: str, schema_url: str, mode: str) -> list[str]:
    return [
        f"#:schema {schema_url}",
        "",
        "# Generated from Codex config schema.",
        f"# Version: {version}",
        f"# Tag: {tag}",
        f"# Mode: {mode}",
    ]


def _comment_lines(text: str | None) -> list[str]:
    if not text:
        return []
    return [f"# {line}".rstrip() for line in text.splitlines()]


def _type_label(field: dict[str, Any]) -> str:
    types = field.get("types") or []
    return " | ".join(types) if types else "unknown"


def _toml_literal(value: Any) -> str:
    return toml_item(value).as_string()


def _placeholder_value(field: dict[str, Any]) -> Any:
    enum_values = field.get("enum") or []
    if enum_values:
        return enum_values[0]

    types = field.get("types") or []
    if "string" in types:
        return "..."
    if "integer" in types:
        return 0
    if "number" in types:
        return 0.0
    if "boolean" in types:
        return False
    if "array" in types:
        return ["..."]
    return "..."


def _render_assignment(field: dict[str, Any], value: Any, commented: bool) -> str:
    assignment = f"{field['renderTomlPath'].split('.')[-1]} = {_toml_literal(value)}"
    return f"# {assignment}" if commented else assignment


def _render_default_note(field: dict[str, Any]) -> list[str]:
    default = field["default"]
    if default is None:
        return [f"# default: null", "# Omit this key to preserve the schema default behavior."]
    return [f"# default: {default!r}", "# Default value is not directly representable in TOML."]


def _leaf_fields(fields: list[dict[str, Any]]) -> list[dict[str, Any]]:
    return [field for field in fields if field["kind"] not in {"table", "map"}]


def generate_toml(
    version: str,
    tag: str,
    schema_url: str,
    fields: list[dict[str, Any]],
    mode: str,
) -> str:
    if mode not in {"default", "reference"}:
        raise ValueError(f"unsupported TOML generation mode: {mode}")

    header = _header(version, tag, schema_url, mode)
    sections: dict[str, list[list[str]]] = defaultdict(list)

    for field in _leaf_fields(fields):
        if mode == "default" and not field["hasDefault"]:
            continue

        render_path = field["renderTomlPath"]
        section = ".".join(render_path.split(".")[:-1])
        lines: list[str] = []

        if mode == "reference":
            lines.extend(_comment_lines(field.get("description")))
            lines.append(f"# type: {_type_label(field)}")
            if field.get("enum"):
                values = ", ".join(str(value) for value in field["enum"])
                lines.append(f"# enum: {values}")

        if field["hasDefault"]:
            default = field["default"]
            try:
                lines.append(_render_assignment(field, default, commented=False))
            except Exception:
                if mode == "default":
                    lines.extend(_render_default_note(field))
                else:
                    lines.extend(_render_default_note(field))
                    lines.append(_render_assignment(field, _placeholder_value(field), commented=True))
        else:
            placeholder = _placeholder_value(field)
            lines.append(_render_assignment(field, placeholder, commented=(mode == "reference")))

        sections[section].append(lines)

    output = header[:]
    if not sections:
        output.extend(["", "# This schema does not declare TOML defaults."])
        return "\n".join(output) + "\n"

    ordered_sections = sorted(sections)
    for section in ordered_sections:
        output.append("")
        if section:
            output.append(f"[{section}]")
            output.append("")
        for block in sections[section]:
            output.extend(block)
            output.append("")

    while output and output[-1] == "":
        output.pop()
    return "\n".join(output) + "\n"
