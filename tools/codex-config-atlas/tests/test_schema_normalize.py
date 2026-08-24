from __future__ import annotations

from copy import deepcopy
import unittest

from codex_config_atlas.schema_normalize import SchemaResolver, normalize_schema


def required_states(schema: dict[str, object]) -> dict[str, str]:
    return {field["path"]: field["required"] for field in normalize_schema(schema)}


class SchemaRequiredStateTests(unittest.TestCase):
    def test_counts_missing_properties_across_feasible_object_branches(self) -> None:
        schema = {
            "oneOf": [
                {
                    "type": "object",
                    "properties": {
                        "all": {"type": "string"},
                        "some": {"type": "string"},
                        "optional": {"type": "string"},
                    },
                    "required": ["all", "some"],
                },
                {
                    "type": "object",
                    "properties": {"all": {"type": "string"}},
                    "required": ["all"],
                },
                {"type": "null"},
            ]
        }

        self.assertEqual(
            required_states(schema),
            {"all": "always", "optional": "never", "some": "conditional"},
        )

    def test_nested_any_of_one_of_and_all_of_keep_joint_constraints(self) -> None:
        schema = {
            "type": "object",
            "properties": {
                "config": {
                    "type": "object",
                    "allOf": [
                        {
                            "properties": {"joint": {"type": "boolean"}},
                            "required": ["joint"],
                        },
                        {
                            "anyOf": [
                                {
                                    "properties": {
                                        "all": {"type": "string"},
                                        "some": {"type": "string"},
                                    },
                                    "required": ["all", "some"],
                                },
                                {
                                    "oneOf": [
                                        {
                                            "type": "object",
                                            "properties": {"all": {"type": "string"}},
                                            "required": ["all"],
                                        },
                                        {"type": "null"},
                                    ]
                                },
                            ]
                        },
                    ],
                }
            },
            "required": ["config"],
        }

        self.assertEqual(
            required_states(schema),
            {
                "config": "always",
                "config.all": "always",
                "config.joint": "always",
                "config.some": "conditional",
            },
        )

    def test_all_optional_branches_are_never_required(self) -> None:
        schema = {
            "anyOf": [
                {"type": "object", "properties": {"value": {"type": "string"}}},
                {"type": "object"},
            ]
        }

        self.assertEqual(required_states(schema), {"value": "never"})

    def test_invalid_required_fails_without_mutating_input(self) -> None:
        schema = {
            "type": "object",
            "properties": {"value": {"type": "string"}},
            "required": "value",
        }
        original = deepcopy(schema)

        with self.assertRaisesRegex(ValueError, "required must be an array of strings"):
            normalize_schema(schema)

        self.assertEqual(schema, original)

    def test_broken_reference_fails_without_mutating_input(self) -> None:
        schema = {
            "type": "object",
            "properties": {"value": {"$ref": "#/definitions/missing"}},
            "definitions": {},
        }
        original = deepcopy(schema)

        with self.assertRaises(KeyError):
            normalize_schema(schema)

        self.assertEqual(schema, original)

    def test_all_of_intersects_types_enums_properties_and_defaults(self) -> None:
        schema = {
            "type": "object",
            "properties": {
                "value": {
                    "allOf": [
                        {
                            "type": ["string", "null"],
                            "enum": ["a", "b", None],
                            "default": "a",
                            "deprecated": False,
                        },
                        {
                            "type": "string",
                            "const": "b",
                            "default": "b",
                            "deprecated": True,
                        },
                        {"default": "a"},
                    ]
                },
                "table": {
                    "allOf": [
                        {
                            "type": "object",
                            "properties": {
                                "left": {"type": "string"},
                                "shared": {"type": ["integer", "number"]},
                            },
                            "required": ["left"],
                        },
                        {
                            "properties": {
                                "right": {"type": "string"},
                                "shared": {"type": "number"},
                            },
                            "required": ["right"],
                        },
                    ]
                },
            },
        }

        fields = {field["path"]: field for field in normalize_schema(schema)}

        self.assertEqual(fields["value"]["types"], ["string"])
        self.assertEqual(fields["value"]["enum"], ["b"])
        self.assertFalse(fields["value"]["hasDefault"])
        self.assertTrue(fields["value"]["deprecated"])
        self.assertEqual(fields["table.left"]["required"], "always")
        self.assertEqual(fields["table.right"]["required"], "always")
        self.assertEqual(fields["table.shared"]["types"], ["number"])

    def test_all_of_conjoins_typed_additional_properties(self) -> None:
        schema = {
            "type": "object",
            "properties": {
                "labels": {
                    "type": "object",
                    "allOf": [
                        {"additionalProperties": {"type": ["string", "null"]}},
                        {"additionalProperties": {"type": "string"}},
                    ],
                }
            },
        }

        fields = {field["path"]: field for field in normalize_schema(schema)}

        self.assertEqual(fields["labels"]["additionalPropertiesMode"], "typed")
        self.assertEqual(fields["labels.<name>"]["types"], ["string"])

    def test_unsatisfiable_all_of_reports_the_branch_pointer(self) -> None:
        schema = {
            "type": "object",
            "properties": {
                "value": {"allOf": [{"type": "string"}, {"type": "integer"}]}
            },
        }

        with self.assertRaisesRegex(
            ValueError, r"unsatisfiable type intersection at #/properties/value/allOf/1"
        ):
            normalize_schema(schema)

    def test_distinct_unmodeled_constraints_remain_conjuncts(self) -> None:
        resolver = SchemaResolver({})

        resolved, _ = resolver.resolve_node(
            {"allOf": [{"not": {"const": "a"}}, {"not": {"const": "b"}}]},
            "#",
        )

        self.assertEqual(resolved["not"], {"const": "a"})
        self.assertEqual(resolved["allOf"], [{"not": {"const": "b"}}])

    def test_self_and_mutual_references_stop_after_the_recursive_edge(self) -> None:
        schema = {
            "type": "object",
            "properties": {"root": {"$ref": "#/$defs/A"}},
            "$defs": {
                "A": {
                    "type": "object",
                    "properties": {"b": {"$ref": "#/$defs/B"}},
                },
                "B": {
                    "type": "object",
                    "properties": {
                        "a": {"$ref": "#/$defs/A"},
                        "self": {"$ref": "#/$defs/B"},
                    },
                },
            },
        }

        paths = [field["path"] for field in normalize_schema(schema)]

        self.assertEqual(paths, ["root", "root.b", "root.b.a", "root.b.self"])


if __name__ == "__main__":
    unittest.main()
