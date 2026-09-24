from __future__ import annotations

import hashlib
import json
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

from codex.scripts.workflow import (
    PatchError,
    apply_patches,
    fetch_upstream,
    load_manifest,
    refresh_patches,
    validate_cargo_lock_compatibility,
    validate_patch_directory,
    verify_patches,
)


def command(args: list[str], cwd: Path, *, capture: bool = False) -> str:
    completed = subprocess.run(
        args,
        cwd=cwd,
        check=True,
        text=True,
        stdout=subprocess.PIPE if capture else None,
    )
    return completed.stdout.strip() if capture else ""


def patch_hashes(directory: Path) -> dict[str, str]:
    return {
        path.name: hashlib.sha256(path.read_bytes()).hexdigest()
        for path in sorted(directory.glob("*.patch"))
    }


class PatchWorkflowTests(unittest.TestCase):
    def setUp(self) -> None:
        temporary = tempfile.TemporaryDirectory()
        self.addCleanup(temporary.cleanup)
        self.base = Path(temporary.name)
        self.upstream_repo = self.base / "upstream"
        self.root = self.base / "maintenance"
        self._create_upstream()
        self._create_maintenance_repo()
        self.manifest = load_manifest(self.root, require_patch_files=False)
        fetch_upstream(self.manifest)
        (self.manifest.worktree / "src" / "feature.txt").write_text("edited\n")

    def _create_upstream(self) -> None:
        self.upstream_repo.mkdir()
        command(["git", "init", "--quiet"], self.upstream_repo)
        command(["git", "config", "user.name", "Fixture"], self.upstream_repo)
        command(
            ["git", "config", "user.email", "fixture@example.invalid"],
            self.upstream_repo,
        )
        (self.upstream_repo / "src").mkdir()
        (self.upstream_repo / "generated").mkdir()
        (self.upstream_repo / "src" / "feature.txt").write_text("base\n")
        (self.upstream_repo / "generated" / "schema.txt").write_text("schema:base\n")
        (self.upstream_repo / "generated" / "schema.bin").write_bytes(b"\0base\n")
        command(
            [
                "git",
                "add",
                "--",
                "src/feature.txt",
                "generated/schema.txt",
                "generated/schema.bin",
            ],
            self.upstream_repo,
        )
        command(["git", "commit", "--quiet", "-m", "fixture"], self.upstream_repo)
        command(["git", "tag", "rust-v1.0.0"], self.upstream_repo)
        self.revision = command(
            ["git", "rev-parse", "HEAD"], self.upstream_repo, capture=True
        )

    def _create_maintenance_repo(self) -> None:
        (self.root / "codex").mkdir(parents=True)
        (self.root / ".gitignore").write_text(".work/\n")
        generate = (
            "from pathlib import Path; "
            "value=Path('src/feature.txt').read_text().strip(); "
            "Path('generated/schema.txt').write_text(f'schema:{value}\\n'); "
            "Path('generated/schema.bin').write_bytes(b'\\0'+value.encode()+b'\\n')"
        )
        validate = (
            "from pathlib import Path; "
            "value=Path('src/feature.txt').read_text().strip(); "
            "assert Path('generated/schema.txt').read_text() == f'schema:{value}\\n'; "
            "assert Path('generated/schema.bin').read_bytes() == b'\\0'+value.encode()+b'\\n'"
        )
        feature_test = (
            "from pathlib import Path; "
            "assert Path('src/feature.txt').read_text() == 'edited\\n'"
        )
        (self.root / "codex" / "upstream.toml").write_text(
            textwrap.dedent(
                f"""
                url = "{self.upstream_repo.as_posix()}"
                ref = "rust-v1.0.0"
                revision = "{self.revision}"
                worktree = ".work/codex/rust-v1.0.0/src"
                generate_commands = [["python3", "-c", {json.dumps(generate)}]]
                regression_commands = [["python3", "-c", {json.dumps(feature_test)}]]
                validation_command = ["python3", "-c", {json.dumps(validate)}]
                """
            ).lstrip()
        )
        (self.root / "codex" / "series.toml").write_text(
            textwrap.dedent(
                f"""
                [[patch]]
                file = "0001-feature.patch"
                intent = "Change the fixture feature."
                behavior = "The fixture exposes the edited value."
                source_files = ["src/feature.txt"]
                source_prefixes = []
                generated_files = []
                generated_prefixes = []
                tests = [["python3", "-c", {json.dumps(feature_test)}]]

                [[patch]]
                file = "0002-generated-contract.patch"
                intent = "Regenerate fixture contracts."
                behavior = "Text and binary contracts follow the feature."
                source_files = []
                source_prefixes = []
                generated_files = ["generated/schema.txt", "generated/schema.bin"]
                generated_prefixes = []
                tests = [["python3", "-c", {json.dumps(validate)}]]
                """
            ).lstrip()
        )
        command(["git", "init", "--quiet"], self.root)
        command(["git", "config", "user.name", "Fixture"], self.root)
        command(["git", "config", "user.email", "fixture@example.invalid"], self.root)
        command(
            [
                "git",
                "add",
                "--",
                ".gitignore",
                "codex/upstream.toml",
                "codex/series.toml",
            ],
            self.root,
        )
        command(["git", "commit", "--quiet", "-m", "fixture"], self.root)

    def clean_editing_checkout(self) -> None:
        (self.manifest.worktree / "src" / "feature.txt").write_text("base\n")
        self.assertEqual(
            command(["git", "status", "--short"], self.manifest.worktree, capture=True),
            "",
        )

    def test_refresh_produces_binary_cumulative_series_and_verify_replays_it(
        self,
    ) -> None:
        result = refresh_patches(self.manifest, dry_run=False)
        self.assertEqual(result["patchSha256"], result["installedSha256"])
        self.assertIn("0002-generated-contract.patch", result["binaryPatches"])
        self.assertEqual(
            [path.name for path in validate_patch_directory(self.manifest)],
            ["0001-feature.patch", "0002-generated-contract.patch"],
        )
        verified = verify_patches(self.manifest)
        self.assertEqual(
            verified["generationDigest"], verified["secondGenerationDigest"]
        )

    def test_refresh_dry_run_changes_no_tracked_or_patch_files(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        before_status = command(
            ["git", "status", "--porcelain=v1"], self.root, capture=True
        )
        before_hashes = patch_hashes(self.manifest.patch_dir)
        result = refresh_patches(self.manifest, dry_run=True)
        self.assertEqual(
            before_status,
            command(["git", "status", "--porcelain=v1"], self.root, capture=True),
        )
        self.assertEqual(before_hashes, patch_hashes(self.manifest.patch_dir))
        self.assertNotIn("candidatePath", result)
        self.assertNotIn("receiptPath", result)

    def test_formal_apply_preserves_official_manifest_origin(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        self.clean_editing_checkout()
        git_config = self.manifest.worktree / ".git" / "config"
        before_config = git_config.read_bytes()
        result = apply_patches(self.manifest, check_only=False)
        self.assertEqual(result["origin"], self.manifest.upstream.url)
        self.assertEqual(git_config.read_bytes(), before_config)
        self.assertEqual(
            (self.manifest.worktree / "src" / "feature.txt").read_text(), "edited\n"
        )
        self.assertEqual(
            (self.manifest.worktree / "generated" / "schema.bin").read_bytes(),
            b"\0edited\n",
        )

    def test_apply_check_and_invalid_later_patch_preserve_clean_checkout(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        self.clean_editing_checkout()
        before = command(
            ["git", "status", "--porcelain=v1"], self.manifest.worktree, capture=True
        )
        apply_patches(self.manifest, check_only=True)
        self.assertEqual(
            before,
            command(
                ["git", "status", "--porcelain=v1"],
                self.manifest.worktree,
                capture=True,
            ),
        )
        second = self.manifest.patch_dir / "0002-generated-contract.patch"
        second.write_text("not a patch\n")
        with self.assertRaises((PatchError, subprocess.CalledProcessError)):
            apply_patches(self.manifest, check_only=False)
        self.assertEqual(
            before,
            command(
                ["git", "status", "--porcelain=v1"],
                self.manifest.worktree,
                capture=True,
            ),
        )

    def test_fetch_validates_dirty_checkout_without_overwriting_it(self) -> None:
        marker = self.manifest.worktree / "src" / "feature.txt"
        self.assertEqual(marker.read_text(), "edited\n")
        result = fetch_upstream(self.manifest)
        self.assertTrue(result["dirty"])
        self.assertFalse(result["created"])
        self.assertEqual(marker.read_text(), "edited\n")

    def test_refresh_rejects_preexisting_patch_directory_changes(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        command(["git", "add", "--", "codex/patches/rust-v1.0.0"], self.root)
        command(["git", "commit", "--quiet", "-m", "track patches"], self.root)
        first = self.manifest.patch_dir / "0001-feature.patch"
        first.write_text(first.read_text() + "dirty\n")
        with self.assertRaisesRegex(PatchError, "unstaged changes"):
            refresh_patches(self.manifest, dry_run=False)
        self.assertTrue(first.read_text().endswith("dirty\n"))

    def test_refresh_rejects_files_outside_the_manifest_patch_set(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        (self.manifest.patch_dir / "9999-unowned.patch").write_text("not owned\n")
        with self.assertRaisesRegex(PatchError, "untracked files"):
            refresh_patches(self.manifest, dry_run=False)


class CargoLockContractTests(unittest.TestCase):
    PINNED = b"""version = 4

[[package]]
name = "local"
version = "0.0.0"
dependencies = ["external"]

[[package]]
name = "external"
version = "1.2.3"
source = "registry+https://example.invalid/index"
checksum = "abc"
"""
    GENERATED = PINNED.replace(b'version = "0.0.0"', b'version = "1.0.0"', 1)

    def test_only_workspace_versions_may_change(self) -> None:
        self.assertEqual(
            validate_cargo_lock_compatibility(
                self.PINNED, self.GENERATED, workspace_version="1.0.0"
            ),
            1,
        )

    def test_external_checksum_or_edges_are_rejected(self) -> None:
        bad = self.GENERATED.replace(b'checksum = "abc"', b'checksum = "changed"')
        with self.assertRaisesRegex(PatchError, "external"):
            validate_cargo_lock_compatibility(
                self.PINNED, bad, workspace_version="1.0.0"
            )


if __name__ == "__main__":
    unittest.main()
