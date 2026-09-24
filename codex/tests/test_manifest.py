from __future__ import annotations

import tempfile
import textwrap
import unittest
from pathlib import Path

from codex.scripts.workflow import PatchError, load_manifest, validate_patch_directory


UPSTREAM = """
url = "https://example.invalid/codex"
ref = "rust-v1.0.0"
revision = "0123456789abcdef0123456789abcdef01234567"
worktree = ".work/codex/rust-v1.0.0/src"
generate_commands = [["python3", "-c", "pass"]]
regression_commands = [["python3", "-c", "pass"]]
validation_command = ["python3", "-c", "pass"]
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
                'validation_command = ["python3", "-c", "pass"]',
                'validation_command = ["cargo", "check"]',
            )
        )
        self.assert_manifest_error(
            upstream=UPSTREAM.replace(
                'generate_commands = [["python3", "-c", "pass"]]',
                'generate_commands = [["cargo", "run", "-p", "generator"]]',
            )
        )

    def test_repository_manifest_has_direct_generators_and_final_lock_owner(
        self,
    ) -> None:
        manifest = load_manifest(require_patch_files=False)
        commands = manifest.upstream.generate_commands
        self.assertEqual(commands[0][:3], ("cargo", "tree", "--offline"))
        self.assertEqual(
            commands[1][:4], ("cargo", "metadata", "--locked", "--offline")
        )
        self.assertFalse(any("just" in command for command in commands))
        self.assertFalse(any("generate-proto.sh" in command for command in commands))
        serialized_commands = "\n".join(" ".join(command) for command in commands)
        self.assertIn("codex-rs/target/agents-misc-proto", serialized_commands)
        cargo_commands = [command for command in commands[1:] if command[0] == "cargo"]
        self.assertTrue(all("--locked" in command for command in cargo_commands))
        schema_commands = [
            command
            for command in commands
            if "schema_fixtures_tests::write_schema_fixtures_from_env" in command
        ]
        self.assertEqual(len(schema_commands), 2)
        self.assertTrue(
            all(
                "CODEX_APP_SERVER_SCHEMA_ROOT=schema" in command
                for command in schema_commands
            )
        )
        self.assertEqual(manifest.patches[-1].file, "0007-generated-contracts.patch")
        self.assertIn("codex-rs/Cargo.lock", manifest.patches[-1].generated_files)

        recovery_tests = manifest.patches[4].tests
        integration_filters = [
            command[-1]
            for command in recovery_tests
            if command[:7]
            == (
                "cargo",
                "test",
                "--locked",
                "-p",
                "codex-core",
                "--test",
                "all",
            )
        ]
        self.assertEqual(
            integration_filters,
            [
                "request_error_precedes_persistent_wait_and_cancel_does_not_abnormally_stop",
                "pre_sampling_legacy_remote_compact_reports_fallback_failure_and_returns_original_error",
                "sampling_internal_http_retries_emit_one_visible_stop_decision",
            ],
        )
        self.assertNotIn("compact", integration_filters)

    def test_repository_patch_directory_matches_series_after_refresh(self) -> None:
        manifest = load_manifest(require_patch_files=False)
        expected = [patch.file for patch in manifest.patches]
        if all((manifest.patch_dir / name).is_file() for name in expected):
            self.assertEqual(
                [path.name for path in validate_patch_directory(manifest)], expected
            )


if __name__ == "__main__":
    unittest.main()
