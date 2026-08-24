from __future__ import annotations

from copy import deepcopy
from dataclasses import dataclass
from typing import Any

from .json_value import canonical_json_key


PLACEHOLDER_KEY = "<name>"
EXAMPLE_KEY = "example"
REQUIRED_ALWAYS = "always"
REQUIRED_CONDITIONAL = "conditional"
REQUIRED_NEVER = "never"
_DEFAULT_CONFLICT = "__atlas_default_conflict__"


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


def _type_intersection(left: list[str], right: list[str]) -> list[str]:
    intersection: list[str] = []
    for left_type in left:
        for right_type in right:
            if left_type == right_type:
                candidate = left_type
            elif {left_type, right_type} == {"integer", "number"}:
                candidate = "integer"
            else:
                continue
            if candidate not in intersection:
                intersection.append(candidate)
    if "number" in intersection and "integer" in intersection:
        intersection.remove("integer")
    return intersection


def _value_matches_types(value: Any, types: list[str]) -> bool:
    if not types:
        return True
    return any(
        (
            schema_type == "null"
            and value is None
            or schema_type == "boolean"
            and isinstance(value, bool)
            or schema_type == "integer"
            and isinstance(value, int)
            and not isinstance(value, bool)
            or schema_type == "number"
            and isinstance(value, (int, float))
            and not isinstance(value, bool)
            or schema_type == "string"
            and isinstance(value, str)
            or schema_type == "array"
            and isinstance(value, list)
            or schema_type == "object"
            and isinstance(value, dict)
        )
        for schema_type in types
    )


def _constrained_values(schema: dict[str, Any], pointer: str) -> list[Any] | None:
    values: list[Any] | None = None
    if "enum" in schema:
        if not isinstance(schema["enum"], list):
            raise ValueError(f"enum must be an array at {pointer}")
        values = _unique(list(schema["enum"]))
    if "const" in schema:
        constant = schema["const"]
        if values is not None and canonical_json_key(constant) not in {
            canonical_json_key(value) for value in values
        }:
            raise ValueError(f"unsatisfiable enum/const intersection at {pointer}")
        values = [deepcopy(constant)]
    if values == []:
        raise ValueError(f"unsatisfiable enum/const intersection at {pointer}")
    return values


@dataclass(frozen=True)
class _ResolvedNode:
    node: Any
    origin_pointer: str
    ref_stack: tuple[str, ...]
    recursive: bool = False


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
        resolved = self._resolve_node(node, pointer, ())
        return self._public_node(resolved.node), resolved.origin_pointer

    def _public_node(self, node: Any) -> Any:
        if isinstance(node, list):
            return [self._public_node(item) for item in node]
        if not isinstance(node, dict):
            return deepcopy(node)
        return {
            key: self._public_node(value)
            for key, value in node.items()
            if key != _DEFAULT_CONFLICT
        }

    def _resolve_node(
        self, node: Any, pointer: str, ref_stack: tuple[str, ...]
    ) -> _ResolvedNode:
        if not isinstance(node, dict):
            return _ResolvedNode(deepcopy(node), pointer, ref_stack)

        current = deepcopy(node)
        origin_pointer = pointer
        active_refs = ref_stack
        recursive = False

        if "$ref" in current:
            ref = current.pop("$ref")
            if not isinstance(ref, str):
                raise ValueError(f"$ref must be a string at {pointer}")
            target_node = self.resolve_pointer(ref)
            if ref in ref_stack:
                # Keep the recursive field itself useful (type, description, and so on), but
                # do not dereference its descendants again.
                target = deepcopy(target_node)
                target.pop("$ref", None)
                resolved_target = _ResolvedNode(target, ref, ref_stack, True)
            else:
                resolved_target = self._resolve_node(
                    target_node, ref, ref_stack + (ref,)
                )
            current = self._conjoin_schema(resolved_target.node, current, pointer)
            origin_pointer = resolved_target.origin_pointer
            active_refs = resolved_target.ref_stack
            recursive = resolved_target.recursive

        if "allOf" in current:
            merged: dict[str, Any] = {}
            all_of = current.pop("allOf")
            if not isinstance(all_of, list):
                raise ValueError(f"allOf must be an array at {pointer}")
            merged_pointer = origin_pointer
            branch_refs: list[str] = list(active_refs)
            for index, branch in enumerate(all_of):
                branch_pointer = _pointer_join(pointer, "allOf", index)
                resolved_branch = self._resolve_node(
                    branch, branch_pointer, active_refs
                )
                merged = self._conjoin_schema(
                    merged, resolved_branch.node, branch_pointer
                )
                if merged_pointer == origin_pointer:
                    merged_pointer = resolved_branch.origin_pointer
                for ref in resolved_branch.ref_stack:
                    if ref not in branch_refs:
                        branch_refs.append(ref)
                recursive = recursive or resolved_branch.recursive
            current = self._conjoin_schema(merged, current, pointer)
            origin_pointer = merged_pointer
            active_refs = tuple(branch_refs)

        return _ResolvedNode(current, origin_pointer, active_refs, recursive)

    def _conjoin_schema(self, base: Any, overlay: Any, pointer: str) -> dict[str, Any]:
        if base is False or overlay is False:
            raise ValueError(f"unsatisfiable schema intersection at {pointer}")
        if base is True:
            return deepcopy(overlay) if isinstance(overlay, dict) else {}
        if overlay is True:
            return deepcopy(base) if isinstance(base, dict) else {}
        if not isinstance(base, dict) or not isinstance(overlay, dict):
            raise ValueError(
                f"schema conjunct must be an object or boolean at {pointer}"
            )

        merged: dict[str, Any] = {}
        modeled = {
            "type",
            "enum",
            "const",
            "required",
            "properties",
            "additionalProperties",
            "deprecated",
            "default",
            "definitions",
            "$defs",
            "allOf",
            _DEFAULT_CONFLICT,
        }

        left_types = _normalize_types(base.get("type"))
        right_types = _normalize_types(overlay.get("type"))
        if "type" in base and not left_types:
            raise ValueError(f"unsatisfiable type intersection at {pointer}")
        if "type" in overlay and not right_types:
            raise ValueError(f"unsatisfiable type intersection at {pointer}")
        if left_types and right_types:
            types = _type_intersection(left_types, right_types)
            if not types:
                raise ValueError(f"unsatisfiable type intersection at {pointer}")
        else:
            types = left_types or right_types
        if types:
            merged["type"] = types[0] if len(types) == 1 else types

        left_values = _constrained_values(base, pointer)
        right_values = _constrained_values(overlay, pointer)
        if left_values is not None and right_values is not None:
            right_keys = {canonical_json_key(value) for value in right_values}
            values = [
                deepcopy(value)
                for value in left_values
                if canonical_json_key(value) in right_keys
            ]
        else:
            values = deepcopy(left_values if left_values is not None else right_values)
        if values is not None:
            values = [value for value in values if _value_matches_types(value, types)]
            if not values:
                raise ValueError(f"unsatisfiable enum/const intersection at {pointer}")
            merged["enum"] = _unique(values)

        required: list[str] = []
        for schema in (base, overlay):
            if "required" not in schema:
                continue
            value = schema["required"]
            if not isinstance(value, list) or not all(
                isinstance(item, str) for item in value
            ):
                raise ValueError(f"required must be an array of strings at {pointer}")
            required.extend(value)
        if required:
            merged["required"] = _unique(required)

        left_properties = base.get("properties", {})
        right_properties = overlay.get("properties", {})
        if not isinstance(left_properties, dict) or not isinstance(
            right_properties, dict
        ):
            raise ValueError(f"properties must be an object at {pointer}")
        properties: dict[str, Any] = {}
        for name in sorted(set(left_properties) | set(right_properties)):
            if name in left_properties and name in right_properties:
                properties[name] = self._conjoin_schema(
                    left_properties[name],
                    right_properties[name],
                    _pointer_join(pointer, "properties", name),
                )
            elif name in left_properties:
                properties[name] = deepcopy(left_properties[name])
            else:
                properties[name] = deepcopy(right_properties[name])
        if properties or "properties" in base or "properties" in overlay:
            merged["properties"] = properties

        if "additionalProperties" in base or "additionalProperties" in overlay:
            left_additional = base.get("additionalProperties", True)
            right_additional = overlay.get("additionalProperties", True)
            additional_pointer = _pointer_join(pointer, "additionalProperties")
            if left_additional is False or right_additional is False:
                merged["additionalProperties"] = False
            elif left_additional is True:
                merged["additionalProperties"] = deepcopy(right_additional)
            elif right_additional is True:
                merged["additionalProperties"] = deepcopy(left_additional)
            elif isinstance(left_additional, dict) and isinstance(
                right_additional, dict
            ):
                merged["additionalProperties"] = self._conjoin_schema(
                    left_additional, right_additional, additional_pointer
                )
            else:
                raise ValueError(
                    f"additionalProperties must be a schema or boolean at {additional_pointer}"
                )

        if "deprecated" in base or "deprecated" in overlay:
            merged["deprecated"] = bool(
                base.get("deprecated", False) or overlay.get("deprecated", False)
            )

        if (
            base.get(_DEFAULT_CONFLICT) is True
            or overlay.get(_DEFAULT_CONFLICT) is True
        ):
            merged[_DEFAULT_CONFLICT] = True
        elif "default" in base and "default" in overlay:
            if canonical_json_key(base["default"]) == canonical_json_key(
                overlay["default"]
            ):
                merged["default"] = deepcopy(base["default"])
            else:
                merged[_DEFAULT_CONFLICT] = True
        elif "default" in base:
            merged["default"] = deepcopy(base["default"])
        elif "default" in overlay:
            merged["default"] = deepcopy(overlay["default"])

        for definition_key in ("definitions", "$defs"):
            left_definitions = base.get(definition_key, {})
            right_definitions = overlay.get(definition_key, {})
            if not isinstance(left_definitions, dict) or not isinstance(
                right_definitions, dict
            ):
                raise ValueError(f"{definition_key} must be an object at {pointer}")
            definitions: dict[str, Any] = {}
            for name in sorted(set(left_definitions) | set(right_definitions)):
                if name in left_definitions and name in right_definitions:
                    if left_definitions[name] == right_definitions[name]:
                        definitions[name] = deepcopy(left_definitions[name])
                    else:
                        definitions[name] = self._conjoin_schema(
                            left_definitions[name],
                            right_definitions[name],
                            _pointer_join(pointer, definition_key, name),
                        )
                elif name in left_definitions:
                    definitions[name] = deepcopy(left_definitions[name])
                else:
                    definitions[name] = deepcopy(right_definitions[name])
            if definitions or definition_key in base or definition_key in overlay:
                merged[definition_key] = definitions

        preserved = [
            deepcopy(branch)
            for schema in (base, overlay)
            for branch in schema.get("allOf", [])
        ]
        annotation_keys = {"description", "title", "$comment", "examples"}
        for key in sorted((set(base) | set(overlay)) - modeled):
            if key in base and key in overlay:
                if base[key] == overlay[key] or key in annotation_keys:
                    merged[key] = deepcopy(base[key])
                else:
                    # Keep both constraints. The first remains directly visible to the
                    # normalizer and every other distinct constraint stays as a conjunct.
                    merged[key] = deepcopy(base[key])
                    preserved.append({key: deepcopy(overlay[key])})
            elif key in base:
                merged[key] = deepcopy(base[key])
            else:
                merged[key] = deepcopy(overlay[key])
        if preserved:
            merged["allOf"] = preserved
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
    ref_stack: tuple[str, ...] = (),
) -> tuple[dict[str, Any], str, tuple[str, ...], bool]:
    resolution = resolver._resolve_node(node, pointer, ref_stack)
    resolved = resolution.node
    origin_pointer = resolution.origin_pointer
    if not isinstance(resolved, dict):
        return (
            _empty_summary(object_feasible=resolved is True),
            origin_pointer,
            resolution.ref_stack,
            resolution.recursive,
        )

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
            branch_summary, _, _, _ = _summarize_node(
                resolver,
                branch,
                _pointer_join(pointer, branch_key, index),
                resolution.ref_stack,
            )
            branch_summaries.append(branch_summary)
            summary = _merge_variant_metadata(summary, branch_summary)

        for name, state in _aggregate_variant_required_states(branch_summaries).items():
            current = summary["required_states"].get(name, REQUIRED_NEVER)
            summary["required_states"][name] = _combine_required_states(current, state)

    summary["types"] = _unique(summary["types"])
    summary["enum"] = _unique(summary["enum"])
    summary["object_feasible"] = _object_is_feasible(summary)
    return (
        summary,
        origin_pointer,
        resolution.ref_stack,
        resolution.recursive,
    )


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

    root_summary, _, root_ref_stack, _ = _summarize_node(resolver, schema, "#")
    root_properties = root_summary["properties"]
    fields: dict[str, dict[str, Any]] = {}

    def visit(
        node: Any,
        pointer: str,
        parts: list[str],
        required: str,
        ref_stack: tuple[str, ...],
    ) -> None:
        summary, origin_pointer, active_refs, recursive = _summarize_node(
            resolver, node, pointer, ref_stack
        )
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

        if recursive:
            return

        for property_name in sorted(summary["properties"]):
            child_pointer = _pointer_join(pointer, "properties", property_name)
            visit(
                summary["properties"][property_name],
                child_pointer,
                parts + [property_name],
                summary["required_states"].get(property_name, REQUIRED_NEVER),
                active_refs,
            )

        additional = summary["additional_properties"]
        if isinstance(additional, dict):
            visit(
                additional,
                _pointer_join(pointer, "additionalProperties"),
                parts + [PLACEHOLDER_KEY],
                REQUIRED_NEVER,
                active_refs,
            )

    for property_name in sorted(root_properties):
        visit(
            root_properties[property_name],
            _pointer_join(root_pointer, "properties", property_name),
            [property_name],
            root_summary["required_states"].get(property_name, REQUIRED_NEVER),
            root_ref_stack,
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
