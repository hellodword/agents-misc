from __future__ import annotations

import tempfile
import textwrap
import unittest
from pathlib import Path

from codex.scripts.workflow import (
    PatchError,
    _cargo_test_list_command,
    _listed_rust_test_count,
    commands_for_selector,
    load_manifest,
    upstream_build_env,
)


UPSTREAM = """
url = "https://example.invalid/codex"
ref = "rust-v1.0.0"
revision = "0123456789abcdef0123456789abcdef01234567"
worktree = ".work/codex/rust-v1.0.0/src"
generate_commands = [["python3", "-c", "pass"]]
integration_commands = [["python3", "-c", "pass"]]
"""

SOURCE_PATCH = """
[[patch]]
file = "0001-source.patch"
intent = "Keep one behavior explicit."
behavior = "A visible behavior remains deterministic."
source_files = ["src/example.rs"]
source_prefixes = []
generated_files = []
generated_prefixes = []
tests = [["python3", "-c", "pass"]]
"""

GENERATED_PATCH = """
[[patch]]
file = "0002-generated.patch"
intent = "Own generated output."
behavior = "Generated output is reproducible."
source_files = []
source_prefixes = []
generated_files = ["schema/example.json"]
generated_prefixes = []
tests = [["python3", "-c", "pass"]]
"""


class ManifestTests(unittest.TestCase):
    def make_root(
        self,
        upstream: str = UPSTREAM,
        series: str = SOURCE_PATCH + GENERATED_PATCH,
    ) -> Path:
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
        self,
        *,
        upstream: str = UPSTREAM,
        series: str = SOURCE_PATCH + GENERATED_PATCH,
    ) -> None:
        with self.assertRaises(PatchError):
            load_manifest(self.make_root(upstream, series), require_patch_files=False)

    def test_valid_manifest_loads_in_declared_order(self) -> None:
        manifest = load_manifest(self.make_root(), require_patch_files=False)
        self.assertEqual(
            [patch.file for patch in manifest.patches],
            ["0001-source.patch", "0002-generated.patch"],
        )
        self.assertEqual(
            commands_for_selector(manifest, "0001"),
            (("python3", "-c", "pass"),),
        )
        self.assertEqual(
            commands_for_selector(manifest, "integration"),
            (("python3", "-c", "pass"),),
        )
        with self.assertRaises(PatchError):
            commands_for_selector(manifest, "unknown")
        build_env = upstream_build_env(manifest)
        self.assertEqual(build_env["CARGO_INCREMENTAL"], "1")
        self.assertEqual(
            build_env["CARGO_TARGET_DIR"],
            str(manifest.worktree.parent / "cargo-target"),
        )

    def test_unknown_and_missing_fields_are_rejected(self) -> None:
        self.assert_manifest_error(upstream=UPSTREAM + 'legacy = "no"\n')
        self.assert_manifest_error(
            series=(SOURCE_PATCH + GENERATED_PATCH).replace(
                'behavior = "A visible behavior remains deterministic."\n', ""
            )
        )

    def test_paths_and_numbers_are_strict(self) -> None:
        self.assert_manifest_error(
            series=(SOURCE_PATCH + GENERATED_PATCH).replace(
                "src/example.rs", "src/../example.rs"
            )
        )
        self.assert_manifest_error(
            series=(SOURCE_PATCH + GENERATED_PATCH).replace(
                "0001-source.patch", "0003-source.patch"
            )
        )
        self.assert_manifest_error(
            upstream=UPSTREAM.replace(
                ".work/codex/rust-v1.0.0/src", ".work/codex/other/src"
            )
        )

    def test_ownership_must_be_unique(self) -> None:
        duplicate = GENERATED_PATCH.replace(
            "0002-generated.patch", "0003-generated-copy.patch"
        )
        self.assert_manifest_error(series=SOURCE_PATCH + GENERATED_PATCH + duplicate)

    def test_only_final_patch_can_own_generated_paths(self) -> None:
        mixed = SOURCE_PATCH.replace(
            "generated_files = []", 'generated_files = ["schema/early.json"]'
        )
        self.assert_manifest_error(series=mixed + GENERATED_PATCH)
        self.assert_manifest_error(series=SOURCE_PATCH)

    def test_upstream_just_and_unlocked_cargo_gates_are_rejected(self) -> None:
        self.assert_manifest_error(
            upstream=UPSTREAM.replace(
                'generate_commands = [["python3", "-c", "pass"]]',
                'generate_commands = [["just", "generate"]]',
            )
        )
        self.assert_manifest_error(
            upstream=UPSTREAM.replace(
                'integration_commands = [["python3", "-c", "pass"]]',
                'integration_commands = [["cargo", "check"]]',
            )
        )
        self.assert_manifest_error(
            upstream=UPSTREAM.replace(
                'generate_commands = [["python3", "-c", "pass"]]',
                'generate_commands = [["cargo", "run", "-p", "generator"]]',
            )
        )

    def test_cargo_test_listing_preserves_filters_and_detects_zero_matches(
        self,
    ) -> None:
        self.assertEqual(
            _cargo_test_list_command(
                (
                    "env",
                    "RUST_MIN_STACK=8388608",
                    "cargo",
                    "test",
                    "--locked",
                    "-p",
                    "codex-core",
                    "filter",
                    "--",
                    "--exact",
                )
            ),
            (
                "env",
                "RUST_MIN_STACK=8388608",
                "cargo",
                "test",
                "--locked",
                "-p",
                "codex-core",
                "filter",
                "--",
                "--list",
                "--exact",
            ),
        )
        self.assertIsNone(_cargo_test_list_command(("cargo", "check", "--locked")))
        self.assertEqual(
            _listed_rust_test_count(
                "3 tests, 0 benchmarks\n0 tests, 0 benchmarks\n1 test, 0 benchmarks\n"
            ),
            4,
        )
        self.assertEqual(_listed_rust_test_count("0 tests, 0 benchmarks\n"), 0)


if __name__ == "__main__":
    unittest.main()
