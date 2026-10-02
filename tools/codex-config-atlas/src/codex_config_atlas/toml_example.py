"""Annotated, opt-in TOML examples from the source schema, without flattening unions."""

from __future__ import annotations

import math
import re
import textwrap
from typing import Any

from tomlkit import item
from tomlkit.exceptions import ConvertError, InvalidStringError

from .json_value import canonical_json_key
from .schema_normalize import SchemaResolver


_MISSING = object()
_CONSTRAINTS = (
    "format",
    "minimum",
    "maximum",
    "exclusiveMinimum",
    "exclusiveMaximum",
    "multipleOf",
    "minLength",
    "maxLength",
    "pattern",
    "minItems",
    "maxItems",
    "uniqueItems",
    "minProperties",
    "maxProperties",
)


def _key(value: str) -> str:
    return value if re.fullmatch(r"[A-Za-z0-9_-]+", value) else item(value).as_string()


def _path(parts: tuple[str, ...]) -> str:
    return ".".join(_key(part) for part in parts)


def _literal(value: Any) -> str:
    # tomlkit.item(dict/list[dict]) produces tables / arrays of tables, which
    # cannot be used on the right-hand side of an assignment.
    if value is None:
        raise ValueError("TOML has no null value")
    if isinstance(value, dict):
        return (
            "{ "
            + ", ".join(
                f"{_key(key)} = {_literal(value[key])}" for key in sorted(value)
            )
            + " }"
            if value
            else "{}"
        )
    if isinstance(value, list):
        return "[" + ", ".join(_literal(element) for element in value) + "]"
    return item(value).as_string()


def _representable(value: Any) -> bool:
    try:
        _literal(value)
        return True
    except (ValueError, ConvertError, InvalidStringError):
        return False


def _comments(text: str) -> list[str]:
    lines = []
    for line in text.splitlines():
        wrapped = textwrap.wrap(
            line,
            width=88,
            break_long_words=False,
            break_on_hyphens=False,
            replace_whitespace=False,
            drop_whitespace=True,
        )
        lines.extend(f"# {part}".rstrip() for part in wrapped or [""])
    return lines


class _Example:
    def __init__(self, schema: dict[str, Any]) -> None:
        self.resolver = SchemaResolver(schema)

    def descriptions(self, node: Any, refs: tuple[str, ...] = ()) -> list[str]:
        """Keep property AND definition descriptions; union choices stay separate."""
        if not isinstance(node, dict):
            return []
        values = [node["description"]] if node.get("description") else []
        ref = node.get("$ref")
        if ref and ref not in refs:
            values += self.descriptions(
                self.resolver.resolve_pointer(ref), refs + (ref,)
            )
        for branch in node.get("allOf", []):
            values += self.descriptions(branch, refs)
        return list(dict.fromkeys(values))

    def resolve(self, node: Any, refs: tuple[str, ...]):
        return self.resolver._resolve_node(node, "#", refs)

    def sample(self, node: Any, refs: tuple[str, ...] = ()) -> Any:
        resolved = self.resolve(node, refs)
        node = resolved.node
        if not isinstance(node, dict) or resolved.recursive:
            return _MISSING
        for value in ([node["default"]] if "default" in node else []) + node.get(
            "examples", []
        ):
            if _representable(value):
                return value
        values = [node["const"]] if "const" in node else node.get("enum", [])
        if values:
            return next((value for value in values if _representable(value)), _MISSING)
        for branch in node.get("oneOf", node.get("anyOf", [])):
            value = self.sample(branch, resolved.ref_stack)
            if value is not _MISSING:
                return value
        types = node.get("type", [])
        types = [types] if isinstance(types, str) else types
        if "object" in types or "properties" in node or "additionalProperties" in node:
            result = {}
            properties = node.get("properties", {})
            for key in sorted(node.get("required", [])):
                value = self.sample(properties.get(key, {}), resolved.ref_stack)
                if value is not _MISSING:
                    result[key] = value
            return result
        if "array" in types:
            value = self.sample(node.get("items", {}), resolved.ref_stack)
            return [value] if value is not _MISSING else []
        if "string" in types:
            return "..."
        if "boolean" in types:
            return False
        if "integer" in types:
            minimum = math.ceil(node.get("minimum", 0))
            if "exclusiveMinimum" in node:
                minimum = max(minimum, math.floor(node["exclusiveMinimum"]) + 1)
            maximum = math.floor(node.get("maximum", minimum))
            if "exclusiveMaximum" in node:
                maximum = min(maximum, math.ceil(node["exclusiveMaximum"]) - 1)
            return min(minimum, maximum)
        if "number" in types:
            return node.get("minimum", node.get("maximum", 0.0))
        return _MISSING

    def annotations(
        self, original: Any, required: bool | None, refs: tuple[str, ...]
    ) -> list[str]:
        resolved = self.resolve(original, refs)
        node = resolved.node
        lines = []
        for description in self.descriptions(original):
            if lines:
                lines.append("#")
            lines += _comments(description)
        if not isinstance(node, dict):
            return lines + [
                "# Unconstrained value." if node else "# This value is forbidden."
            ]
        types = node.get("type", [])
        types = [types] if isinstance(types, str) else types
        label = " | ".join(types) or (
            "union"
            if "oneOf" in node or "anyOf" in node
            else "object"
            if "properties" in node
            else "any"
        )
        presence = (
            ""
            if required is None
            else f" {'Required' if required else 'Optional'} in this object."
        )
        lines += _comments(f"Type: {label}.{presence}")
        if node.get("deprecated"):
            lines.append("# Deprecated by this schema.")
        if "default" in node:
            lines.append(f"# Default (JSON): {canonical_json_key(node['default'])}")
            if not _representable(node["default"]):
                lines.append(
                    "# TOML cannot express this default; omit the key to keep it."
                )
        if "examples" in node:
            lines += _comments(
                f"Schema examples (JSON): {canonical_json_key(node['examples'])}"
            )
        values = [node["const"]] if "const" in node else node.get("enum", [])
        if values:
            lines += _comments(
                "Allowed values (JSON): "
                + ", ".join(canonical_json_key(v) for v in values)
            )
        for constraint in _CONSTRAINTS:
            if constraint in node:
                lines += _comments(
                    f"{constraint}: {canonical_json_key(node[constraint])}"
                )
        for branch_kind in ("oneOf", "anyOf"):
            if branch_kind not in node:
                continue
            lines.append(
                "# Choose one alternative:"
                if branch_kind == "oneOf"
                else "# Allowed alternatives:"
            )
            for index, branch in enumerate(node[branch_kind], 1):
                choice = self.resolve(branch, resolved.ref_stack).node
                if not isinstance(choice, dict):
                    continue
                choice_values = (
                    [choice["const"]] if "const" in choice else choice.get("enum", [])
                )
                title = ", ".join(
                    canonical_json_key(v) for v in choice_values
                ) or canonical_json_key(choice.get("type", "object"))
                lines += _comments(f"  {index}. {title}")
                for description in self.descriptions(branch):
                    lines += _comments(f"     {description}")
                if "default" in choice:
                    lines += _comments(
                        f"     Branch default (JSON): {canonical_json_key(choice['default'])}"
                    )
        return lines

    def item_docs(self, original: Any, label: str, refs: tuple[str, ...]) -> list[str]:
        resolved = self.resolve(original, refs)
        node = resolved.node
        if not isinstance(node, dict) or resolved.recursive:
            return [f"# {label}: recursive or unconstrained item; see the schema."]
        lines = [f"# Item: {label}"] + self.annotations(original, None, refs)
        for key, child in sorted(node.get("properties", {}).items()):
            lines += ["#", f"# Item field: {label}.{_key(key)}"]
            lines += self.annotations(
                child, key in node.get("required", []), resolved.ref_stack
            )
            child_node = self.resolve(child, resolved.ref_stack).node
            if isinstance(child_node, dict) and any(
                k in child_node
                for k in (
                    "properties",
                    "items",
                    "oneOf",
                    "anyOf",
                    "additionalProperties",
                )
            ):
                lines += self.item_docs(
                    child, f"{label}.{_key(key)}", resolved.ref_stack
                )
        if isinstance(node.get("additionalProperties"), dict):
            lines += self.item_docs(
                node["additionalProperties"], f'{label}."<name>"', resolved.ref_stack
            )
        if isinstance(node.get("items"), dict):
            lines += self.item_docs(node["items"], f"{label}[]", resolved.ref_stack)
        for kind in ("oneOf", "anyOf"):
            for index, branch in enumerate(node.get(kind, []), 1):
                lines += self.item_docs(
                    branch, f"{label} (alternative {index})", resolved.ref_stack
                )
        return lines

    def walk(
        self,
        original: Any,
        parts: tuple[str, ...],
        required: bool,
        active_parent: bool,
        refs: tuple[str, ...] = (),
    ) -> list[str]:
        resolved = self.resolve(original, refs)
        node = resolved.node
        lines = self.annotations(original, required, refs)
        active = required and active_parent
        if required and not active_parent:
            lines.append(
                "# Required only when the optional parent / this alternative is configured."
            )
        if resolved.recursive:
            return lines + [
                "# Recursive reference; add further children using the same schema.",
                "",
            ]
        if not isinstance(node, dict):
            return lines + [""]
        properties = node.get("properties", {})
        additional = node.get("additionalProperties")
        if properties or isinstance(additional, dict):
            if active and not node.get("required"):
                lines += [
                    "# Replace this empty object when enabling child keys below.",
                    f"{_path(parts)} = {{}}",
                ]
            lines.append("")
            for name, child in sorted(properties.items()):
                lines += self.walk(
                    child,
                    parts + (name,),
                    name in node.get("required", []),
                    active,
                    resolved.ref_stack,
                )
            if isinstance(additional, dict):
                name = "example"
                while name in properties:
                    name += "_entry"
                lines += [
                    f"# Named entries: replace {_key(name)} in {_path(parts + (name,))} with your own key."
                ]
                lines += self.walk(
                    additional, parts + (name,), False, False, resolved.ref_stack
                )
            return lines

        value = self.sample(original, refs)
        if value is not _MISSING:
            if "default" not in node or not _representable(node["default"]):
                lines.append("# Example value; replace it to suit your setup.")
            assignment = f"{_path(parts)} = {_literal(value)}"
            lines += (
                assignment.splitlines()
                if active
                else [f"# {line}" for line in assignment.splitlines()]
            )
        else:
            lines.append(
                "# No TOML value can be inferred; consult the schema before setting this key."
            )
        if isinstance(node.get("items"), dict):
            sample = self.sample(node["items"], resolved.ref_stack)
            if sample is not _MISSING:
                lines += _comments(f"Item example: {_literal(sample)}")
            lines += self.item_docs(
                node["items"], f"{_path(parts)}[]", resolved.ref_stack
            )
        lines.append("")
        for kind in ("oneOf", "anyOf"):
            for index, branch in enumerate(node.get(kind, []), 1):
                choice = self.resolve(branch, resolved.ref_stack).node
                if isinstance(choice, dict) and (
                    "properties" in choice or "additionalProperties" in choice
                ):
                    lines += [
                        f"# Alternative {index} for {_path(parts)}: use these child keys INSTEAD of the assignment above."
                    ]
                    lines += self.walk(
                        branch, parts, required, False, resolved.ref_stack
                    )
        return lines


def generate_example_toml(
    version: str, tag: str, schema_url: str, schema: dict[str, Any]
) -> str:
    renderer = _Example(schema)
    root = renderer.resolve(schema, ()).node
    output = [
        f"#:schema {schema_url}",
        "",
        "# Codex configuration example",
        f"# Version: {version}",
        f"# Tag: {tag}",
        "# Generated from the versioned schema; descriptions and defaults are schema data.",
        "# Optional settings (including their defaults) remain commented out.",
        "# Dotted keys are independent of table headers: uncomment only the keys you need.",
        "# Required children of optional objects remain commented until you enable that object.",
        "# Values without defaults are illustrative, not inferred Codex runtime defaults.",
        "# Alternatives are mutually exclusive examples; do not enable them all.",
        "# TOML has no null; omit null-default keys. Replace named 'example' entries.",
        "",
    ]
    for name, node in sorted(root.get("properties", {}).items()):
        output += [f"# --- {_key(name)} ---"]
        output += renderer.walk(node, (name,), name in root.get("required", []), True)
    return "\n".join(output).rstrip() + "\n"
