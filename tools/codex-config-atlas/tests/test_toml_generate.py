from __future__ import annotations

import unittest
import tomllib
import io
from pathlib import Path
import tempfile
from unittest.mock import patch

import tomlkit

from codex_config_atlas.toml_generate import generate_toml
from codex_config_atlas.toml_example import generate_example_toml


MISSING = object()


def field(
    path: str,
    *,
    types: list[str] | None = None,
    enum: list[object] | None = None,
    description: str | None = None,
    default: object = MISSING,
) -> dict[str, object]:
    return {
        "path": path,
        "tomlPath": path,
        "renderTomlPath": path,
        "kind": "scalar",
        "types": types or ["string"],
        "required": "never",
        "hasDefault": default is not MISSING,
        "default": None if default is MISSING else default,
        "enum": enum,
        "description": description,
        "deprecated": False,
        "schemaPointer": "#/properties/value",
        "additionalPropertiesMode": None,
        "mapKey": None,
    }


class TomlGenerationTests(unittest.TestCase):
    def test_merges_duplicate_branches_in_stable_order(self) -> None:
        branches = [
            field(
                "section.value",
                types=["string"],
                enum=["z"],
                description="Zulu branch",
                default="shared",
            ),
            field(
                "section.value",
                types=["integer", "string"],
                enum=["a"],
                description="Alpha branch",
                default="shared",
            ),
        ]

        forward = generate_toml(
            "1", "tag", "https://example.invalid", branches, "reference"
        )
        reverse = generate_toml(
            "1", "tag", "https://example.invalid", list(reversed(branches)), "reference"
        )

        self.assertEqual(forward, reverse)
        self.assertEqual(forward.count('value = "shared"'), 1)
        self.assertIn("# type: integer | string", forward)
        self.assertIn('# enum: "a", "z"', forward)
        self.assertLess(forward.index("Alpha branch"), forward.index("Zulu branch"))
        tomlkit.parse(forward)

    def test_conflicting_branch_defaults_are_not_presented_as_universal(self) -> None:
        branches = [
            field("value", default="left"),
            field("value", default="right"),
        ]

        default_output = generate_toml(
            "1", "tag", "https://example.invalid", branches, "default"
        )
        reference_output = generate_toml(
            "1", "tag", "https://example.invalid", branches, "reference"
        )

        self.assertNotIn("value =", default_output)
        self.assertEqual(reference_output.count('# value = "..."'), 1)
        tomlkit.parse(reference_output)

    def test_only_expected_serialization_errors_use_default_note(self) -> None:
        output = generate_toml(
            "1",
            "tag",
            "https://example.invalid",
            [field("value", default=None)],
            "default",
        )
        self.assertIn("# default: null", output)

        with (
            patch(
                "codex_config_atlas.toml_generate._toml_literal",
                side_effect=RuntimeError("unexpected renderer failure"),
            ),
            self.assertRaisesRegex(RuntimeError, "unexpected renderer failure"),
        ):
            generate_toml(
                "1",
                "tag",
                "https://example.invalid",
                [field("value", default="value")],
                "default",
            )

    def test_rejects_unknown_mode(self) -> None:
        with self.assertRaisesRegex(ValueError, "unsupported TOML generation mode"):
            generate_toml("1", "tag", "https://example.invalid", [], "unknown")


class ExampleGenerationTests(unittest.TestCase):
    def test_cli_example_mode_writes_file_without_stdout(self):
        from codex_config_atlas.cli import main

        with (
            tempfile.TemporaryDirectory() as temporary,
            patch("sys.stdout", new_callable=io.StringIO) as stdout,
        ):
            destination = Path(temporary) / "example.toml"
            result = main(
                [
                    "gen-toml",
                    "--schemas",
                    str(Path(__file__).parents[1] / "schemas"),
                    "--version",
                    "0.129.0",
                    "--mode",
                    "example",
                    "--out",
                    str(destination),
                ]
            )
            self.assertEqual(result, 0)
            self.assertEqual(stdout.getvalue(), "")
            output = destination.read_text()
            self.assertIn("# Version: 0.129.0", output)
            self.assertEqual(tomllib.loads(output), {})

    def render(self, schema):
        return generate_example_toml(
            "1.0.0", "rust-v1.0.0", "https://example.invalid/schema.json", schema
        )

    def test_optional_defaults_and_required_children_stay_inactive(self):
        schema = {
            "required": ["name", "required_table"],
            "properties": {
                "name": {"type": "string", "default": "test"},
                "enabled": {"type": "boolean", "default": True},
                "optional_table": {
                    "type": "object",
                    "required": ["secret"],
                    "properties": {"secret": {"type": "string"}},
                },
                "required_table": {
                    "type": "object",
                    "required": ["value"],
                    "properties": {
                        "value": {"type": "integer", "default": 3},
                        "unused": {"type": "boolean", "default": False},
                    },
                },
            },
        }
        output = self.render(schema)
        self.assertEqual(
            tomllib.loads(output), {"name": "test", "required_table": {"value": 3}}
        )
        self.assertIn("# enabled = true", output)
        self.assertIn('# optional_table.secret = "..."', output)
        self.assertIn("# required_table.unused = false", output)

    def test_keeps_ref_and_property_descriptions_and_choice_metadata(self):
        schema = {
            "definitions": {
                "Mode": {
                    "description": "Definition description.",
                    "oneOf": [
                        {
                            "type": "string",
                            "enum": ["ask"],
                            "description": "Ask for approval.",
                        },
                        {
                            "type": "string",
                            "enum": ["never"],
                            "description": "Never ask.",
                            "default": "never",
                        },
                        {
                            "type": "object",
                            "description": "Granular controls.",
                            "required": ["flag"],
                            "properties": {
                                "flag": {
                                    "type": "boolean",
                                    "description": "A branch-only flag.",
                                }
                            },
                        },
                    ],
                }
            },
            "properties": {
                "mode": {
                    "allOf": [{"$ref": "#/definitions/Mode"}],
                    "description": "Property description.",
                },
                "unset": {"type": "string", "default": None},
            },
        }
        output = self.render(schema)
        for text in (
            "Property description.",
            "Definition description.",
            "Ask for approval.",
            "Never ask.",
            "A branch-only flag.",
            'Branch default (JSON): "never"',
            "Default (JSON): null",
        ):
            self.assertIn(text, output)
        self.assertIn('# mode = "ask"', output)
        self.assertIn("# mode.flag = false", output)
        self.assertEqual(tomllib.loads(output), {})

    def test_quotes_paths_and_renders_inline_objects_and_arrays(self):
        value = [{"strange.key": "line 1\nline 2", "enabled": False}, {"value": [1, 2]}]
        schema = {
            "required": ["a.b"],
            "properties": {
                "a.b": {
                    "type": "array",
                    "default": value,
                    "items": {
                        "type": "object",
                        "properties": {"strange.key": {"type": "string"}},
                    },
                }
            },
        }
        output = self.render(schema)
        self.assertEqual(tomllib.loads(output), {"a.b": value})
        self.assertIn('# Item field: "a.b"[]."strange.key"', output)

    def test_preserves_object_defaults_array_item_docs_and_named_entries(self):
        schema = {
            "properties": {
                "servers": {
                    "type": "object",
                    "default": {},
                    "description": "Server definitions.",
                    "additionalProperties": {
                        "type": "object",
                        "properties": {
                            "args": {
                                "type": "array",
                                "default": [],
                                "items": {
                                    "type": "string",
                                    "enum": ["one", "two"],
                                    "description": "Argument choice.",
                                },
                            },
                        },
                    },
                }
            }
        }
        output = self.render(schema)
        for text in (
            "Server definitions.",
            "Default (JSON): {}",
            "Default (JSON): []",
            "Argument choice.",
            'Allowed values (JSON): "one", "two"',
            "# servers.example.args = []",
        ):
            self.assertIn(text, output)
        self.assertEqual(tomllib.loads(output), {})

    def test_recursion_is_bounded_and_property_order_does_not_change_output(self):
        node = {
            "type": "object",
            "properties": {
                "label": {"type": "string"},
                "child": {"$ref": "#/definitions/Node"},
            },
        }
        schema = {
            "definitions": {"Node": node},
            "properties": {
                "tree": {"$ref": "#/definitions/Node"},
                "flag": {"type": "boolean"},
            },
        }
        output = self.render(schema)
        schema["properties"] = dict(reversed(list(schema["properties"].items())))
        node["properties"] = dict(reversed(list(node["properties"].items())))
        self.assertEqual(output, self.render(schema))
        self.assertIn("Recursive reference", output)
        self.assertEqual(tomllib.loads(output), {})

    def test_json_numeric_bounds_produce_toml_integers(self):
        output = self.render(
            {
                "required": ["count"],
                "properties": {
                    "count": {"type": "integer", "minimum": 1.0},
                },
            }
        )
        self.assertIs(type(tomllib.loads(output)["count"]), int)
        self.assertEqual(tomllib.loads(output)["count"], 1)


if __name__ == "__main__":
    unittest.main()
