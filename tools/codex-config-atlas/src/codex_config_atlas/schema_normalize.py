from __future__ import annotations

from copy import deepcopy
from typing import Any

from .json_value import canonical_json_key


PLACEHOLDER_KEY = "<name>"
EXAMPLE_KEY = "example"
REQUIRED_ALWAYS = "always"
REQUIRED_CONDITIONAL = "conditional"
REQUIRED_NEVER = "never"


def _unique(values: list[Any]) -> list[Any]:
    seen: set[str] = set()
    unique: list[Any] = []
    for value in values:
        key = canonical_json_key(value)
        if key not in seen:
            seen.add(key)
            unique.append(value)
    return unique


def _json_pointer_escape(segment: str) -> str:
    return segment.replace("~", "~0").replace("/", "~1")


def _pointer_join(pointer: str, *segments: str | int) -> str:
    current = pointer
    for segment in segments:
        current = f"{current}/{_json_pointer_escape(str(segment))}"
    return current


def _path_join(parts: list[str]) -> str:
    return ".".join(parts)


def _render_toml_path(path: str) -> str:
    return path.replace(PLACEHOLDER_KEY, EXAMPLE_KEY)


def _normalize_types(value: Any) -> list[str]:
    if value is None:
        return []
    if isinstance(value, list):
        return [item for item in value if isinstance(item, str)]
    if isinstance(value, str):
        return [value]
    return []


class SchemaResolver:
    def __init__(self, root_schema: dict[str, Any]) -> None:
        self.root_schema = root_schema

    def resolve_pointer(self, pointer: str) -> dict[str, Any]:
        if not pointer.startswith("#"):
            raise ValueError(f"unsupported schema pointer: {pointer}")

        node: Any = self.root_schema
        if pointer == "#":
            return node

        for raw_segment in pointer[2:].split("/"):
            segment = raw_segment.replace("~1", "/").replace("~0", "~")
            if isinstance(node, list):
                node = node[int(segment)]
            else:
                node = node[segment]
        if not isinstance(node, dict):
            raise ValueError(f"schema pointer does not resolve to an object: {pointer}")
        return node

    def resolve_node(self, node: Any, pointer: str) -> tuple[Any, str]:
        if not isinstance(node, dict):
            return node, pointer

        current = deepcopy(node)
        origin_pointer = pointer

        if "$ref" in current:
            ref = current.pop("$ref")
            target, target_pointer = self.resolve_node(self.resolve_pointer(ref), ref)
            current = self._merge_schema(target, current)
            origin_pointer = target_pointer

        if "allOf" in current:
            merged: dict[str, Any] = {}
            all_of = current.pop("allOf")
            merged_pointer = origin_pointer
            for index, branch in enumerate(all_of):
                resolved_branch, branch_pointer = self.resolve_node(
                    branch, _pointer_join(pointer, "allOf", index)
                )
                if isinstance(resolved_branch, dict):
                    merged = self._merge_schema(merged, resolved_branch)
                if merged_pointer == origin_pointer:
                    merged_pointer = branch_pointer
            current = self._merge_schema(merged, current)
            origin_pointer = merged_pointer

        return current, origin_pointer

    def _merge_schema(
        self, base: dict[str, Any], overlay: dict[str, Any]
    ) -> dict[str, Any]:
        merged = deepcopy(base)
        for key, value in overlay.items():
            if key in {"properties", "definitions"}:
                current = deepcopy(merged.get(key, {}))
                for nested_key, nested_value in value.items():
                    current[nested_key] = deepcopy(nested_value)
                merged[key] = current
            elif key == "required":
                merged[key] = _unique(list(merged.get(key, [])) + list(value))
            elif key == "type":
                merged[key] = _unique(
                    _normalize_types(merged.get(key)) + _normalize_types(value)
                )
            elif key == "enum":
                merged[key] = _unique(list(merged.get(key, [])) + list(value))
            elif key == "deprecated":
                merged[key] = bool(merged.get(key, False) or value)
            elif key == "additionalProperties":
                if merged.get(key) is False or value is False:
                    merged[key] = False
                else:
                    merged[key] = deepcopy(value)
            else:
                merged[key] = deepcopy(value)
        return merged


def _empty_summary(*, object_feasible: bool) -> dict[str, Any]:
    return {
        "types": [],
        "enum": [],
        "description": None,
        "deprecated": False,
        "has_default": False,
        "default": None,
        "properties": {},
        "required_states": {},
        "additional_properties": None,
        "object_feasible": object_feasible,
    }


def _merge_variant_metadata(
    base: dict[str, Any], variant: dict[str, Any]
) -> dict[str, Any]:
    return {
        "types": _unique(base["types"] + variant["types"]),
        "enum": _unique(base["enum"] + variant["enum"]),
        "description": base["description"] or variant["description"],
        "deprecated": base["deprecated"] or variant["deprecated"],
        "has_default": base["has_default"] or variant["has_default"],
        "default": base["default"] if base["has_default"] else variant["default"],
        "properties": {**base["properties"], **variant["properties"]},
        "required_states": dict(base["required_states"]),
        "additional_properties": base["additional_properties"]
        or variant["additional_properties"],
        "object_feasible": base["object_feasible"] or variant["object_feasible"],
    }


def _object_is_feasible(summary: dict[str, Any]) -> bool:
    types = summary["types"]
    if types and "object" not in types:
        return False
    enum = summary["enum"]
    if enum and not any(isinstance(value, dict) for value in enum):
        return False
    return True


def _combine_required_states(left: str, right: str) -> str:
    if REQUIRED_ALWAYS in {left, right}:
        return REQUIRED_ALWAYS
    if REQUIRED_CONDITIONAL in {left, right}:
        return REQUIRED_CONDITIONAL
    return REQUIRED_NEVER


def _aggregate_variant_required_states(
    branches: list[dict[str, Any]],
) -> dict[str, str]:
    feasible = [branch for branch in branches if branch["object_feasible"]]
    if not feasible:
        return {}

    names = set().union(
        *(
            set(branch["properties"]) | set(branch["required_states"])
            for branch in feasible
        )
    )
    states: dict[str, str] = {}
    for name in names:
        branch_states = [
            branch["required_states"].get(name, REQUIRED_NEVER) for branch in feasible
        ]
        if all(state == REQUIRED_ALWAYS for state in branch_states):
            states[name] = REQUIRED_ALWAYS
        elif all(state == REQUIRED_NEVER for state in branch_states):
            states[name] = REQUIRED_NEVER
        else:
            states[name] = REQUIRED_CONDITIONAL
    return states


def _summarize_node(
    resolver: SchemaResolver,
    node: Any,
    pointer: str,
) -> tuple[dict[str, Any], str]:
    resolved, origin_pointer = resolver.resolve_node(node, pointer)
    if not isinstance(resolved, dict):
        return _empty_summary(object_feasible=resolved is True), origin_pointer

    properties = deepcopy(resolved.get("properties", {}))
    required = resolved.get("required", [])
    if not isinstance(required, list) or not all(
        isinstance(item, str) for item in required
    ):
        raise ValueError(f"required must be an array of strings at {pointer}")
    required_names = set(required)
    property_names = set(properties) | required_names

    summary = {
        "types": _normalize_types(resolved.get("type")),
        "enum": list(resolved.get("enum", [])),
        "description": resolved.get("description"),
        "deprecated": bool(resolved.get("deprecated", False)),
        "has_default": "default" in resolved,
        "default": resolved.get("default"),
        "properties": properties,
        "required_states": {
            name: REQUIRED_ALWAYS if name in required_names else REQUIRED_NEVER
            for name in property_names
        },
        "additional_properties": resolved.get("additionalProperties"),
        "object_feasible": False,
    }

    if "const" in resolved:
        summary["enum"] = _unique(summary["enum"] + [resolved["const"]])

    for branch_key in ("anyOf", "oneOf"):
        if branch_key not in resolved:
            continue
        branch_summaries = []
        for index, branch in enumerate(resolved[branch_key]):
            branch_summary, _ = _summarize_node(
                resolver, branch, _pointer_join(pointer, branch_key, index)
            )
            branch_summaries.append(branch_summary)
            summary = _merge_variant_metadata(summary, branch_summary)

        for name, state in _aggregate_variant_required_states(branch_summaries).items():
            current = summary["required_states"].get(name, REQUIRED_NEVER)
            summary["required_states"][name] = _combine_required_states(current, state)

    summary["types"] = _unique(summary["types"])
    summary["enum"] = _unique(summary["enum"])
    summary["object_feasible"] = _object_is_feasible(summary)
    return summary, origin_pointer


def _kind_for_summary(summary: dict[str, Any]) -> str:
    object_like = (
        "object" in summary["types"]
        or bool(summary["properties"])
        or isinstance(summary["additional_properties"], dict)
    )
    if object_like:
        if isinstance(summary["additional_properties"], dict):
            return "map"
        return "table"
    if "array" in summary["types"]:
        return "array"
    return "scalar"


def normalize_schema(schema: dict[str, Any]) -> list[dict[str, Any]]:
    resolver = SchemaResolver(schema)
    resolved_root, root_pointer = resolver.resolve_node(schema, "#")
    if not isinstance(resolved_root, dict):
        raise ValueError("schema root must be an object")

    root_summary, _ = _summarize_node(resolver, schema, "#")
    root_properties = root_summary["properties"]
    fields: dict[str, dict[str, Any]] = {}

    def visit(
        node: dict[str, Any], pointer: str, parts: list[str], required: str
    ) -> None:
        summary, origin_pointer = _summarize_node(resolver, node, pointer)
        path = _path_join(parts)
        field = {
            "path": path,
            "tomlPath": path,
            "renderTomlPath": _render_toml_path(path),
            "kind": _kind_for_summary(summary),
            "types": summary["types"],
            "required": required,
            "hasDefault": summary["has_default"],
            "default": summary["default"],
            "enum": summary["enum"] or None,
            "description": summary["description"],
            "deprecated": summary["deprecated"],
            "schemaPointer": origin_pointer,
            "additionalPropertiesMode": (
                "typed"
                if isinstance(summary["additional_properties"], dict)
                else "allow_any"
                if summary["additional_properties"] is True
                else "forbid"
                if summary["additional_properties"] is False
                else None
            ),
            "mapKey": PLACEHOLDER_KEY if PLACEHOLDER_KEY in parts else None,
        }
        fields[path] = field

        for property_name in sorted(summary["properties"]):
            child_pointer = _pointer_join(pointer, "properties", property_name)
            visit(
                summary["properties"][property_name],
                child_pointer,
                parts + [property_name],
                summary["required_states"].get(property_name, REQUIRED_NEVER),
            )

        additional = summary["additional_properties"]
        if isinstance(additional, dict):
            visit(
                additional,
                _pointer_join(pointer, "additionalProperties"),
                parts + [PLACEHOLDER_KEY],
                REQUIRED_NEVER,
            )

    for property_name in sorted(root_properties):
        visit(
            root_properties[property_name],
            _pointer_join(root_pointer, "properties", property_name),
            [property_name],
            root_summary["required_states"].get(property_name, REQUIRED_NEVER),
        )

    return sorted(fields.values(), key=lambda item: item["path"].split("."))


def defaults_from_fields(fields: list[dict[str, Any]]) -> list[dict[str, Any]]:
    defaults = []
    for field in fields:
        if not field["hasDefault"]:
            continue
        defaults.append(
            {
                "path": field["path"],
                "tomlPath": field["tomlPath"],
                "renderTomlPath": field["renderTomlPath"],
                "kind": field["kind"],
                "types": field["types"],
                "value": field["default"],
                "schemaPointer": field["schemaPointer"],
            }
        )
    return defaults
