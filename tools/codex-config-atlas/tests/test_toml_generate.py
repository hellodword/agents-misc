from __future__ import annotations

import unittest
from unittest.mock import patch

import tomlkit

from codex_config_atlas.toml_generate import generate_toml


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


if __name__ == "__main__":
    unittest.main()
