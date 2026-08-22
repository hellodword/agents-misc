from __future__ import annotations

from copy import deepcopy
import unittest

from codex_config_atlas.schema_normalize import normalize_schema


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


if __name__ == "__main__":
    unittest.main()
