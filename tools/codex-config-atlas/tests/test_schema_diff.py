from __future__ import annotations

from contextlib import redirect_stdout
from copy import deepcopy
import io
import json
from pathlib import Path
import unittest
from unittest.mock import patch

from codex_config_atlas.cli import main as cli_main
from codex_config_atlas.json_value import canonical_json_key
from codex_config_atlas.schema_diff import build_schema_diff


MISSING = object()
GOLDEN_PATH = Path(__file__).parent / "fixtures" / "schema-diff-golden.json"


def field(
    path: str,
    *,
    default: object = MISSING,
    description: str = "Shared description",
    types: list[str] | None = None,
    required: str = "never",
) -> dict[str, object]:
    return {
        "path": path,
        "kind": "scalar",
        "types": types or ["string"],
        "required": required,
        "hasDefault": default is not MISSING,
        "default": None if default is MISSING else default,
        "enum": None,
        "description": description,
        "deprecated": False,
        "additionalPropertiesMode": None,
    }


class SchemaDiffProfileDeduplicationTests(unittest.TestCase):
    def test_suppresses_semantically_identical_profile_change(self) -> None:
        before = [
            field("feature.value", default=1),
            field("profiles.<name>.feature.value", default=1),
        ]
        after = [
            field("feature.value", default=2),
            field("profiles.<name>.feature.value", default=2),
        ]

        payload = build_schema_diff("1", "2", before, after)

        self.assertEqual(
            [(change["path"], change["kind"]) for change in payload["changes"]],
            [("feature.value", "default_changed")],
        )
        self.assertEqual(payload["summary"]["behavior"], 1)

    def test_keeps_profile_change_when_new_value_differs(self) -> None:
        before = [
            field("feature.value", default=1),
            field("profiles.<name>.feature.value", default=1),
        ]
        after = [
            field("feature.value", default=2),
            field("profiles.<name>.feature.value", default=3),
        ]

        payload = build_schema_diff("1", "2", before, after)

        self.assertEqual(
            [(change["path"], change["to"]) for change in payload["changes"]],
            [
                ("feature.value", 2),
                ("profiles.<name>.feature.value", 3),
            ],
        )

    def test_keeps_profile_description_change_when_text_differs(self) -> None:
        before = [
            field("feature.value", description="Root before"),
            field("profiles.<name>.feature.value", description="Profile before"),
        ]
        after = [
            field("feature.value", description="Root after"),
            field("profiles.<name>.feature.value", description="Profile after"),
        ]

        payload = build_schema_diff("1", "2", before, after)

        self.assertEqual(
            [change["path"] for change in payload["changes"]],
            ["feature.value", "profiles.<name>.feature.value"],
        )

    def test_added_field_requires_complete_semantic_match(self) -> None:
        identical = build_schema_diff(
            "1",
            "2",
            [],
            [
                field("feature.value"),
                field("profiles.<name>.feature.value"),
            ],
        )
        distinct = build_schema_diff(
            "1",
            "2",
            [],
            [
                field("feature.value", description="Root"),
                field("profiles.<name>.feature.value", description="Profile"),
            ],
        )

        self.assertEqual(
            [change["path"] for change in identical["changes"]],
            ["feature.value"],
        )
        self.assertEqual(
            [change["path"] for change in distinct["changes"]],
            ["feature.value", "profiles.<name>.feature.value"],
        )

    def test_keeps_profile_only_change(self) -> None:
        payload = build_schema_diff(
            "1",
            "2",
            [field("profiles.<name>.feature.value", default=1)],
            [field("profiles.<name>.feature.value", default=2)],
        )

        self.assertEqual(
            [change["path"] for change in payload["changes"]],
            ["profiles.<name>.feature.value"],
        )


class SchemaDiffRequiredStateTests(unittest.TestCase):
    def test_only_transition_to_always_is_directly_breaking(self) -> None:
        cases = [
            ("never", "always", "breakingLike"),
            ("conditional", "always", "breakingLike"),
            ("never", "conditional", "review"),
            ("always", "conditional", "review"),
            ("conditional", "never", "review"),
            ("always", "never", "compatible"),
        ]

        for before, after, category in cases:
            with self.subTest(before=before, after=after):
                payload = build_schema_diff(
                    "1",
                    "2",
                    [field("value", required=before)],
                    [field("value", required=after)],
                )
                self.assertEqual(
                    payload["changes"],
                    [
                        {
                            "kind": "required_changed",
                            "category": category,
                            "path": "value",
                            "from": before,
                            "to": after,
                        }
                    ],
                )
                self.assertEqual(payload["summary"][category], 1)

    def test_added_conditional_field_requires_review(self) -> None:
        payload = build_schema_diff(
            "1", "2", [], [field("value", required="conditional")]
        )

        self.assertEqual(payload["changes"][0]["category"], "review")

    def test_rejects_removed_boolean_contract(self) -> None:
        with self.assertRaisesRegex(ValueError, "invalid required state"):
            build_schema_diff(
                "1", "2", [field("value")], [field("value", required=False)]
            )


class SchemaDiffGoldenTests(unittest.TestCase):
    def test_canonical_json_keys_and_diff_payloads(self) -> None:
        fixture = json.loads(GOLDEN_PATH.read_text())

        for item in fixture["canonicalValues"]:
            with self.subTest(value=item["value"]):
                self.assertEqual(canonical_json_key(item["value"]), item["key"])

        for case in fixture["diffCases"]:
            with self.subTest(case=case["name"]):
                from_fields = [field_from_golden(item) for item in case["fromFields"]]
                to_fields = [field_from_golden(item) for item in case["toFields"]]
                payload = build_schema_diff(
                    case["from"],
                    case["to"],
                    from_fields,
                    to_fields,
                )
                self.assertEqual(payload, case["expected"])

                output = io.StringIO()
                schemas = [
                    (schema_from_golden(case["fromFields"]), {}),
                    (schema_from_golden(case["toFields"]), {}),
                ]
                with (
                    patch(
                        "codex_config_atlas.cli._load_version_inputs",
                        side_effect=schemas,
                    ),
                    redirect_stdout(output),
                ):
                    exit_code = cli_main(
                        [
                            "diff",
                            "--schemas",
                            ".",
                            "--from",
                            case["from"],
                            "--to",
                            case["to"],
                            "--format",
                            "json",
                        ]
                    )
                self.assertEqual(exit_code, 0)
                self.assertEqual(json.loads(output.getvalue()), case["expected"])

    def test_rejects_non_json_enum_numbers(self) -> None:
        for value in (float("nan"), float("inf"), float("-inf")):
            with self.subTest(value=value):
                with self.assertRaisesRegex(ValueError, "JSON numbers must be finite"):
                    canonical_json_key(value)

    def test_failed_enum_diff_does_not_mutate_inputs(self) -> None:
        before = [field("choice")]
        after = [{**field("choice"), "enum": [float("inf")]}]
        original_before = deepcopy(before)
        original_after = deepcopy(after)

        with self.assertRaisesRegex(ValueError, "JSON numbers must be finite"):
            build_schema_diff("1", "2", before, after)

        self.assertEqual(before, original_before)
        self.assertEqual(after, original_after)


def field_from_golden(item: dict[str, object]) -> dict[str, object]:
    return {
        **field(
            str(item["path"]),
            types=item.get("types") or ["string"],
            required=str(item.get("required", "never")),
        ),
        "enum": item.get("enum"),
    }


def schema_from_golden(items: list[dict[str, object]]) -> dict[str, object]:
    properties: dict[str, object] = {}
    always: list[str] = []
    conditional: list[str] = []
    for item in items:
        path = str(item["path"])
        types = item.get("types") or ["string"]
        property_schema: dict[str, object] = {
            "type": types[0] if len(types) == 1 else types,
        }
        if "enum" in item:
            property_schema["enum"] = item["enum"]
        properties[path] = property_schema
        state = item.get("required", "never")
        if state == "always":
            always.append(path)
        elif state == "conditional":
            conditional.append(path)

    schema: dict[str, object] = {"type": "object", "properties": properties}
    if always:
        schema["required"] = always
    if conditional:
        schema["anyOf"] = [
            {"type": "object", "required": conditional},
            {"type": "object"},
        ]
    return schema


if __name__ == "__main__":
    unittest.main()
