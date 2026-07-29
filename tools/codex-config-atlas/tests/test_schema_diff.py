from __future__ import annotations

import unittest

from codex_config_atlas.schema_diff import build_schema_diff


MISSING = object()


def field(
    path: str,
    *,
    default: object = MISSING,
    description: str = "Shared description",
    types: list[str] | None = None,
) -> dict[str, object]:
    return {
        "path": path,
        "kind": "scalar",
        "types": types or ["string"],
        "required": False,
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


if __name__ == "__main__":
    unittest.main()
