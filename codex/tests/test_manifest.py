from __future__ import annotations

import tempfile
import textwrap
import unittest
from pathlib import Path

from codex.scripts.common import (
    PatchError,
    load_manifest,
    validate_generated_only_ownership,
    validate_patch_directory,
)


UPSTREAM = """
url = "https://example.invalid/codex"
ref = "rust-v1.0.0"
revision = "0123456789abcdef0123456789abcdef01234567"
worktree = ".work/codex/rust-v1.0.0/src"
generate_commands = [["python3", "-c", "pass"]]
regression_commands = [["python3", "-c", "pass"]]
validation_command = ["python3", "-c", "pass"]
"""

PATCH = """
[[patch]]
file = "0001-example.patch"
intent = "Keep one behavior explicit."
behavior = "A visible behavior remains deterministic."
source_files = ["src/example.rs"]
source_prefixes = []
generated_files = ["schema/example.json"]
generated_prefixes = []
tests = [["python3", "-c", "pass"]]
"""


class ManifestTests(unittest.TestCase):
    def make_root(self, upstream: str = UPSTREAM, series: str = PATCH) -> Path:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        root = Path(temporary.name)
        (root / "codex").mkdir()
        (root / "codex" / "upstream.toml").write_text(
            textwrap.dedent(upstream).lstrip()
        )
        (root / "codex" / "series.toml").write_text(textwrap.dedent(series).lstrip())
        return root

    def assert_manifest_error(
        self, upstream: str = UPSTREAM, series: str = PATCH
    ) -> None:
        with self.assertRaises(PatchError):
            load_manifest(self.make_root(upstream, series), require_patch_files=False)

    def test_valid_manifest_loads_in_declared_order(self) -> None:
        manifest = load_manifest(self.make_root(), require_patch_files=False)
        self.assertEqual(manifest.upstream.ref, "rust-v1.0.0")
        self.assertEqual(
            manifest.upstream.regression_commands,
            (("python3", "-c", "pass"),),
        )
        self.assertEqual(
            [patch.file for patch in manifest.patches], ["0001-example.patch"]
        )

    def test_unknown_upstream_field_is_rejected(self) -> None:
        self.assert_manifest_error(upstream=UPSTREAM + 'legacy = "no"\n')

    def test_missing_regression_commands_is_rejected(self) -> None:
        self.assert_manifest_error(
            upstream=UPSTREAM.replace(
                'regression_commands = [["python3", "-c", "pass"]]\n', ""
            )
        )

    def test_empty_regression_commands_is_rejected(self) -> None:
        self.assert_manifest_error(
            upstream=UPSTREAM.replace(
                'regression_commands = [["python3", "-c", "pass"]]',
                "regression_commands = []",
            )
        )

    def test_unknown_patch_field_is_rejected(self) -> None:
        self.assert_manifest_error(series=PATCH + 'legacy = "no"\n')

    def test_missing_field_is_rejected(self) -> None:
        self.assert_manifest_error(
            series=PATCH.replace(
                'behavior = "A visible behavior remains deterministic."\n', ""
            )
        )

    def test_duplicate_patch_is_rejected(self) -> None:
        self.assert_manifest_error(series=PATCH + PATCH)

    def test_non_contiguous_patch_number_is_rejected(self) -> None:
        self.assert_manifest_error(
            series=PATCH.replace("0001-example.patch", "0002-example.patch")
        )

    def test_absolute_path_is_rejected(self) -> None:
        self.assert_manifest_error(
            series=PATCH.replace("src/example.rs", "/src/example.rs")
        )

    def test_parent_path_is_rejected(self) -> None:
        self.assert_manifest_error(
            series=PATCH.replace("src/example.rs", "src/../example.rs")
        )

    def test_duplicate_exact_ownership_is_rejected(self) -> None:
        duplicate = PATCH + PATCH.replace(
            "0001-example.patch", "0002-second.patch"
        ).replace("schema/example.json", "schema/second.json")
        self.assert_manifest_error(series=duplicate)

    def test_file_under_another_patch_prefix_is_rejected(self) -> None:
        first = PATCH.replace(
            'source_files = ["src/example.rs"]', "source_files = []"
        ).replace("source_prefixes = []", 'source_prefixes = ["src/"]', 1)
        second = PATCH.replace("0001-example.patch", "0002-second.patch").replace(
            "schema/example.json", "schema/second.json"
        )
        self.assert_manifest_error(series=first + second)

    def test_wrong_revision_shape_is_rejected(self) -> None:
        self.assert_manifest_error(
            upstream=UPSTREAM.replace(
                "0123456789abcdef0123456789abcdef01234567", "0123456789abcdef"
            )
        )

    def test_repository_patch_directory_matches_series(self) -> None:
        manifest = load_manifest()
        self.assertEqual(
            [path.name for path in validate_patch_directory(manifest)],
            [patch.file for patch in manifest.patches],
        )

    def test_pinned_manifest_declares_explicit_reproducible_generators(self) -> None:
        manifest = load_manifest()
        self.assertEqual(
            manifest.upstream.generate_commands,
            (
                ("bash", "codex-rs/config/scripts/generate-proto.sh"),
                ("just", "write-config-schema"),
                (
                    "python3",
                    "codex-rs/app-server-protocol/scripts/write_schema_fixtures.py",
                ),
                (
                    "python3",
                    "codex-rs/app-server-protocol/scripts/write_schema_fixtures.py",
                    "--experimental",
                ),
                ("just", "write-hooks-schema"),
            ),
        )

    def test_generated_only_ownership_requires_generated_paths(self) -> None:
        manifest = load_manifest(self.make_root(), require_patch_files=False)
        patch = manifest.patches[0]
        with self.assertRaisesRegex(PatchError, "generated-only"):
            validate_generated_only_ownership(patch)


if __name__ == "__main__":
    unittest.main()
