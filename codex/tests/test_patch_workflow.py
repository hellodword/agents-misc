from __future__ import annotations

import dataclasses
import hashlib
import json
import os
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path

from codex.scripts.common import (
    MaintenanceManifest,
    PatchError,
    apply_patches,
    changed_tree_digest,
    fetch_upstream,
    load_manifest,
    patch_paths,
    refresh_patches,
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


def file_hashes(directory: Path) -> dict[str, str]:
    return {
        path.name: hashlib.sha256(path.read_bytes()).hexdigest()
        for path in sorted(directory.glob("*.patch"))
    }


class InjectedFailure(PatchError):
    pass


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
        command(["git", "config", "user.email", "fixture@example.invalid"], self.upstream_repo)
        (self.upstream_repo / "src").mkdir()
        (self.upstream_repo / "generated").mkdir()
        (self.upstream_repo / "src" / "feature.txt").write_text("base\n")
        (self.upstream_repo / "generated" / "schema.txt").write_text("schema:base\n")
        command(["git", "add", "--", "src/feature.txt", "generated/schema.txt"], self.upstream_repo)
        command(["git", "commit", "--quiet", "-m", "fixture"], self.upstream_repo)
        command(["git", "tag", "rust-v1.0.0"], self.upstream_repo)
        self.revision = command(["git", "rev-parse", "HEAD"], self.upstream_repo, capture=True)

    def _create_maintenance_repo(self) -> None:
        (self.root / "codex").mkdir(parents=True)
        (self.root / ".gitignore").write_text(".work/\n")
        generate = (
            "from pathlib import Path; "
            "value=Path('src/feature.txt').read_text().strip(); "
            "Path('generated/schema.txt').write_text(f'schema:{value}\\n')"
        )
        validate = (
            "from pathlib import Path; "
            "assert Path('generated/schema.txt').read_text() == "
            "f\"schema:{Path('src/feature.txt').read_text().strip()}\\n\""
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
                intent = "Regenerate the fixture schema."
                behavior = "The schema follows the edited feature."
                source_files = []
                source_prefixes = []
                generated_files = ["generated/schema.txt"]
                generated_prefixes = []
                tests = [["python3", "-c", {json.dumps(validate)}]]
                """
            ).lstrip()
        )
        command(["git", "init", "--quiet"], self.root)
        command(["git", "config", "user.name", "Fixture"], self.root)
        command(["git", "config", "user.email", "fixture@example.invalid"], self.root)
        command(
            ["git", "add", "--", ".gitignore", "codex/upstream.toml", "codex/series.toml"],
            self.root,
        )
        command(["git", "commit", "--quiet", "-m", "fixture"], self.root)

    def state(self) -> tuple[dict[str, str], str, bytes]:
        status = command(["git", "status", "--porcelain=v1"], self.manifest.worktree, capture=True)
        index = Path(
            command(["git", "rev-parse", "--git-path", "index"], self.manifest.worktree, capture=True)
        )
        if not index.is_absolute():
            index = self.manifest.worktree / index
        return file_hashes(self.manifest.patch_dir), status, index.read_bytes()

    def test_refresh_is_deterministic_and_preserves_worktree_and_real_index(self) -> None:
        before_status = command(["git", "status", "--porcelain=v1"], self.manifest.worktree, capture=True)
        before_index = self.state()[2]
        result = refresh_patches(self.manifest, dry_run=False)
        first = file_hashes(self.manifest.patch_dir)
        self.assertEqual(result["patches"], ["0001-feature.patch", "0002-generated-contract.patch"])
        self.assertEqual(before_status, command(["git", "status", "--porcelain=v1"], self.manifest.worktree, capture=True))
        self.assertEqual(before_index, self.state()[2])
        refresh_patches(self.manifest, dry_run=False)
        self.assertEqual(first, file_hashes(self.manifest.patch_dir))

    def test_dry_run_does_not_change_tracked_maintenance_files(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        before = command(["git", "status", "--porcelain=v1"], self.root, capture=True)
        hashes = file_hashes(self.manifest.patch_dir)
        refresh_patches(self.manifest, dry_run=True)
        self.assertEqual(before, command(["git", "status", "--porcelain=v1"], self.root, capture=True))
        self.assertEqual(hashes, file_hashes(self.manifest.patch_dir))

    def test_clean_apply_reproduces_edited_and_generated_tree(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        with tempfile.TemporaryDirectory() as temporary:
            clone = Path(temporary) / "clone"
            command(["git", "clone", "--quiet", "--no-hardlinks", str(self.upstream_repo), str(clone)], self.base)
            command(["git", "checkout", "--quiet", "--detach", self.revision], clone)
            apply_patches(clone, patch_paths(self.manifest), check_only=False)
            self.assertEqual((clone / "src" / "feature.txt").read_text(), "edited\n")
            self.assertEqual((clone / "generated" / "schema.txt").read_text(), "schema:edited\n")

    def test_failures_preserve_patch_hashes_worktree_and_index(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        expected = self.state()
        for stage in ("generate", "apply", "validate", "replace"):
            with self.subTest(stage=stage):
                def fail(current: str, wanted: str = stage) -> None:
                    if current == wanted:
                        raise InjectedFailure(f"injected {wanted} failure")

                with self.assertRaises(InjectedFailure):
                    refresh_patches(self.manifest, dry_run=False, fault=fail)
                self.assertEqual(expected, self.state())

    def test_validation_command_failure_is_atomic(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        expected = self.state()
        failing_upstream = dataclasses.replace(
            self.manifest.upstream,
            validation_command=("python3", "-c", "raise SystemExit(23)"),
        )
        failing = MaintenanceManifest(
            root=self.manifest.root,
            upstream=failing_upstream,
            patches=self.manifest.patches,
        )
        with self.assertRaises(PatchError):
            refresh_patches(failing, dry_run=False)
        self.assertEqual(expected, self.state())

    def test_generator_partial_write_failure_is_atomic(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        expected = self.state()
        failing_upstream = dataclasses.replace(
            self.manifest.upstream,
            generate_commands=(
                (
                    "python3",
                    "-c",
                    "from pathlib import Path; Path('generated/schema.txt').write_text('partial'); raise SystemExit(19)",
                ),
                (
                    "python3",
                    "-c",
                    "raise AssertionError('later generator must not run after failure')",
                ),
            ),
        )
        failing = MaintenanceManifest(
            root=self.manifest.root,
            upstream=failing_upstream,
            patches=self.manifest.patches,
        )
        with self.assertRaises(PatchError):
            refresh_patches(failing, dry_run=False)
        self.assertEqual(expected, self.state())

    def test_unowned_changed_path_is_rejected(self) -> None:
        (self.manifest.worktree / "unowned.txt").write_text("no owner\n")
        with self.assertRaisesRegex(PatchError, "no owner"):
            refresh_patches(self.manifest, dry_run=True)

    def test_changed_tree_digest_includes_untracked_generated_files(self) -> None:
        before = changed_tree_digest(self.manifest.worktree)
        generated = self.manifest.worktree / "generated" / "new-schema.json"
        generated.write_text("{}\n")
        paths, digest = changed_tree_digest(self.manifest.worktree)
        self.assertIn("generated/new-schema.json", paths)
        self.assertNotEqual(before[1], digest)

    def test_wrong_tag_revision_is_rejected(self) -> None:
        wrong = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                revision="0" * 40,
                worktree=".work/codex/wrong-revision/src",
            ),
            patches=self.manifest.patches,
        )
        with self.assertRaisesRegex(PatchError, "resolves to"):
            fetch_upstream(wrong)


if __name__ == "__main__":
    unittest.main()
