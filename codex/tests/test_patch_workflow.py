from __future__ import annotations

import dataclasses
import hashlib
import json
import shutil
import subprocess
import tempfile
import textwrap
import unittest
from pathlib import Path
from unittest.mock import patch

from codex.scripts import common
from codex.scripts.common import (
    MaintenanceManifest,
    PatchError,
    apply_patch_series,
    apply_patches,
    changed_tree_digest,
    declared_generated_paths,
    fetch_upstream,
    generated_tree_digest,
    load_manifest,
    patch_paths,
    refresh_patches,
    run_generation_sequence,
    validate_cargo_lock_compatibility,
    validate_binary_patches,
    validate_zstd_fixture_tree,
    zstd_semantic_diff,
    validate_patch_directory,
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
        command(
            ["git", "config", "user.email", "fixture@example.invalid"],
            self.upstream_repo,
        )
        (self.upstream_repo / "src").mkdir()
        (self.upstream_repo / "generated").mkdir()
        (self.upstream_repo / "src" / "feature.txt").write_text("base\n")
        (self.upstream_repo / "src" / "delete.txt").write_text("delete me\n")
        (self.upstream_repo / "src" / "link-target.txt").write_text("link target\n")
        (self.upstream_repo / "generated" / "schema.txt").write_text("schema:base\n")
        command(
            [
                "git",
                "add",
                "--",
                "src/feature.txt",
                "src/delete.txt",
                "src/link-target.txt",
                "generated/schema.txt",
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

    def state(self) -> tuple[dict[str, str], str, bytes]:
        status = command(
            ["git", "status", "--porcelain=v1"], self.manifest.worktree, capture=True
        )
        index = Path(
            command(
                ["git", "rev-parse", "--git-path", "index"],
                self.manifest.worktree,
                capture=True,
            )
        )
        if not index.is_absolute():
            index = self.manifest.worktree / index
        return file_hashes(self.manifest.patch_dir), status, index.read_bytes()

    def test_refresh_is_deterministic_and_preserves_worktree_and_real_index(
        self,
    ) -> None:
        before_status = command(
            ["git", "status", "--porcelain=v1"], self.manifest.worktree, capture=True
        )
        before_index = self.state()[2]
        result = refresh_patches(self.manifest, dry_run=False)
        first = file_hashes(self.manifest.patch_dir)
        self.assertEqual(
            result["patches"], ["0001-feature.patch", "0002-generated-contract.patch"]
        )
        self.assertEqual(
            before_status,
            command(
                ["git", "status", "--porcelain=v1"],
                self.manifest.worktree,
                capture=True,
            ),
        )
        self.assertEqual(before_index, self.state()[2])
        refresh_patches(self.manifest, dry_run=False)
        self.assertEqual(first, file_hashes(self.manifest.patch_dir))

    def test_refresh_uses_one_checkout_and_one_cumulative_apply(self) -> None:
        with (
            patch.object(common, "_clone_base", wraps=common._clone_base) as clone_mock,
            patch.object(
                common, "apply_patch_series", wraps=apply_patch_series
            ) as apply_mock,
        ):
            result = refresh_patches(self.manifest, dry_run=False)

        self.assertEqual(clone_mock.call_count, 1)
        self.assertEqual(apply_mock.call_count, 1)
        self.assertEqual(result["patchSha256"], result["installedSha256"])
        self.assertEqual(
            result["installedSha256"], file_hashes(self.manifest.patch_dir)
        )

    def test_refresh_atomically_replaces_pristine_tracked_predecessor_series(
        self,
    ) -> None:
        refresh_patches(self.manifest, dry_run=False)
        command(
            [
                "git",
                "add",
                "--",
                self.manifest.patch_dir.relative_to(self.root).as_posix(),
            ],
            self.root,
        )
        command(["git", "commit", "--quiet", "-m", "track predecessor"], self.root)
        transitioned = MaintenanceManifest(
            root=self.manifest.root,
            upstream=self.manifest.upstream,
            patches=(
                dataclasses.replace(
                    self.manifest.patches[0], file="0001-feature-v2.patch"
                ),
                dataclasses.replace(
                    self.manifest.patches[1],
                    file="0002-generated-contract-v2.patch",
                ),
            ),
        )

        with patch.object(
            common, "_exchange_directories", wraps=common._exchange_directories
        ) as exchange_mock:
            result = refresh_patches(transitioned, dry_run=False)

        self.assertEqual(exchange_mock.call_count, 1)
        self.assertEqual(
            sorted(file_hashes(transitioned.patch_dir)),
            ["0001-feature-v2.patch", "0002-generated-contract-v2.patch"],
        )
        self.assertEqual(result["patchSha256"], result["installedSha256"])

    def test_refresh_rejects_dirty_tracked_predecessor_series(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        command(
            [
                "git",
                "add",
                "--",
                self.manifest.patch_dir.relative_to(self.root).as_posix(),
            ],
            self.root,
        )
        command(["git", "commit", "--quiet", "-m", "track predecessor"], self.root)
        transitioned = MaintenanceManifest(
            root=self.manifest.root,
            upstream=self.manifest.upstream,
            patches=(
                dataclasses.replace(
                    self.manifest.patches[0], file="0001-feature-v2.patch"
                ),
                dataclasses.replace(
                    self.manifest.patches[1],
                    file="0002-generated-contract-v2.patch",
                ),
            ),
        )
        predecessor = self.manifest.patch_dir / self.manifest.patches[0].file
        predecessor.write_text(predecessor.read_text() + "dirty\n")

        with self.assertRaisesRegex(PatchError, "unstaged changes"):
            refresh_patches(transitioned, dry_run=False)

    def test_dry_run_does_not_change_tracked_maintenance_files(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        before = command(["git", "status", "--porcelain=v1"], self.root, capture=True)
        hashes = file_hashes(self.manifest.patch_dir)
        result = refresh_patches(self.manifest, dry_run=True)
        self.assertEqual(
            before,
            command(["git", "status", "--porcelain=v1"], self.root, capture=True),
        )
        self.assertEqual(hashes, file_hashes(self.manifest.patch_dir))
        candidate = Path(result["candidatePath"])
        receipt = Path(result["receiptPath"])
        self.assertTrue(candidate.is_dir())
        self.assertTrue(receipt.is_file())
        self.assertEqual(hashes, file_hashes(candidate))
        self.assertEqual(
            command(
                [
                    "git",
                    "check-ignore",
                    "--quiet",
                    "--",
                    candidate.relative_to(self.root).as_posix(),
                ],
                self.root,
                capture=True,
            ),
            "",
        )

    def test_shell_wrapped_cargo_regression_uses_workspace_cwd(self) -> None:
        repository = self.base / "codex-source"
        workspace = repository / "codex-rs"
        workspace.mkdir(parents=True)
        (workspace / "Cargo.toml").write_text("[workspace]\n")
        completed = subprocess.CompletedProcess(["bash", "-c", "cargo test"], 0)

        with patch.object(common, "run", return_value=completed) as run_mock:
            result = common.run_upstream(
                self.manifest,
                ("bash", "-c", "cargo test"),
                cwd=repository,
            )

        self.assertIs(result, completed)
        self.assertEqual(run_mock.call_args.kwargs["cwd"], workspace)

    def test_clean_apply_reproduces_edited_and_generated_tree(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        with tempfile.TemporaryDirectory() as temporary:
            clone = Path(temporary) / "clone"
            command(
                [
                    "git",
                    "clone",
                    "--quiet",
                    "--no-hardlinks",
                    str(self.upstream_repo),
                    str(clone),
                ],
                self.base,
            )
            command(["git", "checkout", "--quiet", "--detach", self.revision], clone)
            with patch.object(
                common, "_clone_base", wraps=common._clone_base
            ) as clone_mock:
                apply_patches(clone, patch_paths(self.manifest), check_only=False)
            self.assertEqual(clone_mock.call_count, 1)
            self.assertEqual((clone / "src" / "feature.txt").read_text(), "edited\n")
            self.assertEqual(
                (clone / "generated" / "schema.txt").read_text(), "schema:edited\n"
            )

    def test_apply_second_patch_failure_preserves_complete_original_tree(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        target = self.base / "apply-second-failure"
        command(
            [
                "git",
                "clone",
                "--quiet",
                "--no-hardlinks",
                str(self.upstream_repo),
                str(target),
            ],
            self.base,
        )
        command(["git", "checkout", "--quiet", "--detach", self.revision], target)
        invalid_patch = self.base / "invalid-second.patch"
        invalid_patch.write_text("not a patch\n")

        with self.assertRaises(PatchError):
            apply_patches(
                target,
                [patch_paths(self.manifest)[0], invalid_patch],
                check_only=False,
            )

        self.assertEqual((target / "src" / "feature.txt").read_text(), "base\n")
        self.assertEqual(
            (target / "generated" / "schema.txt").read_text(), "schema:base\n"
        )
        self.assertEqual(
            command(["git", "status", "--short"], target, capture=True), ""
        )

    def test_apply_exchange_failure_preserves_complete_original_tree(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        target = self.base / "apply-exchange-failure"
        command(
            [
                "git",
                "clone",
                "--quiet",
                "--no-hardlinks",
                str(self.upstream_repo),
                str(target),
            ],
            self.base,
        )
        command(["git", "checkout", "--quiet", "--detach", self.revision], target)

        with (
            patch.object(
                common,
                "_exchange_directories",
                side_effect=InjectedFailure("injected exchange command failure"),
            ),
            self.assertRaisesRegex(InjectedFailure, "exchange command failure"),
        ):
            apply_patches(target, patch_paths(self.manifest), check_only=False)

        self.assertEqual((target / "src" / "feature.txt").read_text(), "base\n")
        self.assertEqual(
            (target / "generated" / "schema.txt").read_text(), "schema:base\n"
        )
        self.assertEqual(
            command(["git", "status", "--short"], target, capture=True), ""
        )

    def test_apply_exchange_commit_has_only_complete_old_or_new_trees(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        target = self.base / "apply-commit-points"
        command(
            [
                "git",
                "clone",
                "--quiet",
                "--no-hardlinks",
                str(self.upstream_repo),
                str(target),
            ],
            self.base,
        )
        command(["git", "checkout", "--quiet", "--detach", self.revision], target)
        real_exchange = common._exchange_directories

        def inspect_exchange(candidate: Path, installed: Path) -> None:
            self.assertEqual((installed / "src" / "feature.txt").read_text(), "base\n")
            self.assertEqual(
                (candidate / "src" / "feature.txt").read_text(), "edited\n"
            )
            real_exchange(candidate, installed)
            self.assertEqual(
                (installed / "src" / "feature.txt").read_text(), "edited\n"
            )
            self.assertEqual((candidate / "src" / "feature.txt").read_text(), "base\n")

        with patch.object(
            common, "_exchange_directories", side_effect=inspect_exchange
        ):
            apply_patches(target, patch_paths(self.manifest), check_only=False)

        self.assertEqual((target / "src" / "feature.txt").read_text(), "edited\n")
        self.assertEqual(
            (target / "generated" / "schema.txt").read_text(), "schema:edited\n"
        )

    def test_apply_cleanup_failure_keeps_new_tree_and_reports_old_path(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        target = self.base / "apply-cleanup-failure"
        command(
            [
                "git",
                "clone",
                "--quiet",
                "--no-hardlinks",
                str(self.upstream_repo),
                str(target),
            ],
            self.base,
        )
        command(["git", "checkout", "--quiet", "--detach", self.revision], target)

        with patch.object(
            common.shutil, "rmtree", side_effect=OSError("injected cleanup failure")
        ):
            apply_patches(target, patch_paths(self.manifest), check_only=False)

        retained = list(target.parent.glob(f".{target.name}.apply-candidate-*"))
        self.assertEqual(len(retained), 1)
        self.assertEqual((target / "src" / "feature.txt").read_text(), "edited\n")
        self.assertEqual((retained[0] / "src" / "feature.txt").read_text(), "base\n")
        common.shutil.rmtree(retained[0])

    def test_failures_preserve_patch_hashes_worktree_and_index(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        expected = self.state()
        for stage in ("generate", "apply", "validate", "replace", "installed"):
            with self.subTest(stage=stage):

                def fail(current: str, wanted: str = stage) -> None:
                    if current == wanted:
                        raise InjectedFailure(f"injected {wanted} failure")

                with self.assertRaises(InjectedFailure):
                    refresh_patches(self.manifest, dry_run=False, fault=fail)
                self.assertEqual(expected, self.state())
                if stage != "generate":
                    self.assertTrue(self.manifest.candidate_dir.is_dir())
                    shutil.rmtree(self.manifest.candidate_dir)
                self.assertFalse(self.manifest.backup_dir.exists())

    def test_install_failure_restores_previous_series_and_preserves_candidate(
        self,
    ) -> None:
        refresh_patches(self.manifest, dry_run=False)
        previous = file_hashes(self.manifest.patch_dir)
        (self.manifest.worktree / "src" / "feature.txt").write_text("edited again\n")
        updated = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                regression_commands=(("python3", "-c", "pass"),),
            ),
            patches=tuple(
                dataclasses.replace(spec, tests=(("python3", "-c", "pass"),))
                for spec in self.manifest.patches
            ),
        )

        def fail_after_install(stage: str) -> None:
            if stage == "installed":
                raise InjectedFailure("injected post-install failure")

        with self.assertRaisesRegex(InjectedFailure, "post-install failure"):
            refresh_patches(updated, dry_run=False, fault=fail_after_install)

        self.assertEqual(file_hashes(self.manifest.patch_dir), previous)
        self.assertTrue(self.manifest.candidate_dir.is_dir())
        self.assertNotEqual(file_hashes(self.manifest.candidate_dir), previous)
        self.assertFalse(self.manifest.backup_dir.exists())

    def test_restore_failure_preserves_installed_candidate_and_backup_paths(
        self,
    ) -> None:
        refresh_patches(self.manifest, dry_run=False)
        (self.manifest.worktree / "src" / "feature.txt").write_text("edited again\n")
        updated = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                regression_commands=(("python3", "-c", "pass"),),
            ),
            patches=tuple(
                dataclasses.replace(spec, tests=(("python3", "-c", "pass"),))
                for spec in self.manifest.patches
            ),
        )
        real_exchange = common._exchange_directories
        exchange_count = 0

        def fail_restore(candidate: Path, target: Path) -> None:
            nonlocal exchange_count
            exchange_count += 1
            if exchange_count == 2:
                raise InjectedFailure("injected rollback failure")
            real_exchange(candidate, target)

        def fail_after_install(stage: str) -> None:
            if stage == "installed":
                raise InjectedFailure("injected post-install failure")

        with (
            patch.object(common, "_exchange_directories", side_effect=fail_restore),
            self.assertRaisesRegex(
                PatchError, "automatic restoration also failed"
            ) as raised,
        ):
            refresh_patches(updated, dry_run=False, fault=fail_after_install)

        self.assertIn(str(self.manifest.patch_dir), str(raised.exception))
        self.assertIn(str(self.manifest.candidate_dir), str(raised.exception))
        self.assertIn(str(self.manifest.backup_dir), str(raised.exception))
        self.assertTrue(self.manifest.patch_dir.is_dir())
        self.assertTrue(self.manifest.candidate_dir.is_dir())
        self.assertTrue(self.manifest.backup_dir.is_dir())

    def test_refresh_exchange_command_failure_preserves_installed_patch_tree(
        self,
    ) -> None:
        refresh_patches(self.manifest, dry_run=False)
        expected = self.state()
        with (
            patch.object(
                common,
                "_exchange_directories",
                side_effect=InjectedFailure("injected exchange command failure"),
            ),
            self.assertRaisesRegex(InjectedFailure, "exchange command failure"),
        ):
            refresh_patches(self.manifest, dry_run=False)
        self.assertEqual(expected, self.state())

    def test_refresh_cleanup_failure_keeps_new_patch_tree_and_old_tree_path(
        self,
    ) -> None:
        refresh_patches(self.manifest, dry_run=False)
        previous = file_hashes(self.manifest.patch_dir)
        (self.manifest.worktree / "src" / "feature.txt").write_text("edited again\n")
        updated = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                regression_commands=(("python3", "-c", "pass"),),
            ),
            patches=tuple(
                dataclasses.replace(
                    patch_spec,
                    tests=(("python3", "-c", "pass"),),
                )
                for patch_spec in self.manifest.patches
            ),
        )
        real_rmtree = common.shutil.rmtree

        def fail_backup(path: Path, *args: object, **kwargs: object) -> None:
            if Path(path) == self.manifest.backup_dir:
                raise OSError("injected cleanup failure")
            real_rmtree(path, *args, **kwargs)

        with patch.object(common.shutil, "rmtree", side_effect=fail_backup):
            result = refresh_patches(updated, dry_run=False)

        current = file_hashes(self.manifest.patch_dir)
        self.assertNotEqual(current, previous)
        self.assertEqual(
            result["retainedRuntimePaths"], [str(self.manifest.backup_dir)]
        )
        self.assertEqual(file_hashes(self.manifest.backup_dir), previous)
        real_rmtree(self.manifest.backup_dir)

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

    def test_missing_exact_generated_file_is_bootstrapped(self) -> None:
        generated_path = "generated/new-schema.txt"
        generating = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                generate_commands=(
                    (
                        "python3",
                        "-c",
                        "from pathlib import Path; p=Path('generated/new-schema.txt'); "
                        "assert p.is_file(); p.write_text('schema:new\\n')",
                    ),
                ),
            ),
            patches=(
                self.manifest.patches[0],
                dataclasses.replace(
                    self.manifest.patches[1],
                    generated_files=(generated_path,),
                ),
            ),
        )

        first, second = run_generation_sequence(
            generating, self.manifest.worktree, twice=True
        )

        self.assertEqual(first, second)
        self.assertEqual(
            (self.manifest.worktree / generated_path).read_text(), "schema:new\n"
        )

    def test_generator_must_replace_bootstrapped_file(self) -> None:
        generated_path = "generated/new-schema.txt"
        not_generating = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                generate_commands=(
                    (
                        "python3",
                        "-c",
                        "from pathlib import Path; assert Path('generated/new-schema.txt').is_file()",
                    ),
                ),
            ),
            patches=(
                self.manifest.patches[0],
                dataclasses.replace(
                    self.manifest.patches[1],
                    generated_files=(generated_path,),
                ),
            ),
        )

        with self.assertRaisesRegex(
            PatchError, "did not replace bootstrapped generated file"
        ):
            run_generation_sequence(not_generating, self.manifest.worktree, twice=True)

    def test_generator_write_outside_generated_ownership_is_rejected(self) -> None:
        escaping = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                generate_commands=(
                    (
                        "python3",
                        "-c",
                        "from pathlib import Path; Path('src/feature.txt').write_text('escaped\\n')",
                    ),
                ),
            ),
            patches=self.manifest.patches,
        )
        with self.assertRaisesRegex(
            PatchError, "outside manifest-declared generated ownership"
        ):
            run_generation_sequence(escaping, self.manifest.worktree, twice=True)

    def test_alternate_lock_allows_only_local_workspace_version_normalization(
        self,
    ) -> None:
        pinned = b"""version = 4

[[package]]
name = "local"
version = "0.0.0"
dependencies = ["registry"]

[[package]]
name = "registry"
version = "1.2.3"
source = "registry+https://example.invalid/index"
checksum = "abc"
"""
        normalized = pinned.replace(
            b'name = "local"\nversion = "0.0.0"',
            b'name = "local"\nversion = "0.154.0"',
        )

        self.assertEqual(
            validate_cargo_lock_compatibility(
                pinned, normalized, workspace_version="0.154.0"
            ),
            1,
        )

    def test_alternate_lock_rejects_external_dependency_changes(self) -> None:
        pinned = b"""version = 4

[[package]]
name = "local"
version = "0.0.0"

[[package]]
name = "registry"
version = "1.2.3"
source = "registry+https://example.invalid/index"
checksum = "abc"
"""
        normalized = pinned.replace(b'checksum = "abc"', b'checksum = "changed"')

        with self.assertRaisesRegex(PatchError, "external dependencies"):
            validate_cargo_lock_compatibility(
                pinned, normalized, workspace_version="0.154.0"
            )

    def test_unowned_changed_path_is_rejected(self) -> None:
        (self.manifest.worktree / "unowned.txt").write_text("no owner\n")
        with self.assertRaisesRegex(PatchError, "no owner"):
            refresh_patches(self.manifest, dry_run=True)

    def test_owner_validation_covers_modified_added_deleted_and_symlink_paths(
        self,
    ) -> None:
        (self.manifest.worktree / "src" / "added.txt").write_text("added\n")
        (self.manifest.worktree / "src" / "delete.txt").unlink()
        (self.manifest.worktree / "src" / "link.txt").symlink_to("link-target.txt")
        expanded = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                regression_commands=(("python3", "-c", "pass"),),
            ),
            patches=(
                dataclasses.replace(
                    self.manifest.patches[0],
                    source_files=(),
                    source_prefixes=("src/",),
                    tests=(("python3", "-c", "pass"),),
                ),
                dataclasses.replace(
                    self.manifest.patches[1],
                    tests=(("python3", "-c", "pass"),),
                ),
            ),
        )

        refresh_patches(expanded, dry_run=False)

        patch_text = (expanded.patch_dir / "0001-feature.patch").read_text()
        self.assertIn("src/feature.txt", patch_text)
        self.assertIn("src/added.txt", patch_text)
        self.assertIn("deleted file mode", patch_text)
        self.assertIn("new file mode 120000", patch_text)

    def test_changed_tree_digest_includes_untracked_generated_files(self) -> None:
        before = changed_tree_digest(self.manifest.worktree)
        generated = self.manifest.worktree / "generated" / "new-schema.json"
        generated.write_text("{}\n")
        paths, digest = changed_tree_digest(self.manifest.worktree)
        self.assertIn("generated/new-schema.json", paths)
        self.assertNotEqual(before[1], digest)

    def test_generated_digest_covers_only_declared_generated_paths(self) -> None:
        paths, before = generated_tree_digest(self.manifest, self.manifest.worktree)
        self.assertEqual(paths, ("generated/schema.txt",))
        (self.manifest.worktree / "src" / "feature.txt").write_text(
            "another source edit\n"
        )
        self.assertEqual(
            generated_tree_digest(self.manifest, self.manifest.worktree)[1], before
        )
        self.assertEqual(
            declared_generated_paths(self.manifest, self.manifest.worktree), paths
        )

    def test_stale_patch_file_is_rejected_before_refresh_or_apply(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        stale = self.manifest.patch_dir / "0099-stale.patch"
        stale.write_text("stale\n")
        with self.assertRaisesRegex(PatchError, "not listed"):
            validate_patch_directory(self.manifest)
        stale.unlink()

    def test_stale_ignored_candidate_is_rejected_before_checkout(self) -> None:
        self.manifest.candidate_dir.mkdir(parents=True)
        with (
            patch.object(common, "_clone_base", wraps=common._clone_base) as clone_mock,
            self.assertRaisesRegex(PatchError, "stale refresh candidate"),
        ):
            refresh_patches(self.manifest, dry_run=True)
        clone_mock.assert_not_called()

    def test_generator_second_run_drift_is_rejected(self) -> None:
        generated = self.manifest.worktree / "generated" / "schema.txt"
        drifting = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                generate_commands=(
                    (
                        "python3",
                        "-c",
                        "from pathlib import Path; p=Path('generated/schema.txt'); "
                        "p.write_text(p.read_text() + 'drift\\n')",
                    ),
                ),
            ),
            patches=self.manifest.patches,
        )
        with self.assertRaisesRegex(PatchError, "not reproducible"):
            run_generation_sequence(drifting, self.manifest.worktree, twice=True)
        self.assertIn("drift", generated.read_text())

    def test_binary_patch_is_checked_and_applied(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        source = self.base / "binary-source"
        command(
            [
                "git",
                "clone",
                "--quiet",
                "--no-hardlinks",
                str(self.upstream_repo),
                str(source),
            ],
            self.base,
        )
        command(["git", "checkout", "--quiet", "--detach", self.revision], source)
        blob = source / "src" / "fixture.bin"
        blob.write_bytes(b"after\x00\x00\xff\x10")
        command(["git", "add", "--", "src/fixture.bin"], source)
        binary_patch = self.base / "binary.patch"
        diff = subprocess.run(
            ["git", "diff", "--cached", "--binary", "HEAD"],
            cwd=source,
            check=True,
            stdout=subprocess.PIPE,
        )
        binary_patch.write_bytes(diff.stdout)
        with patch.object(
            common, "_clone_base", side_effect=AssertionError("must not clone")
        ):
            self.assertEqual(validate_binary_patches([binary_patch]), ("binary.patch",))

    def test_zstd_fixture_semantic_review_ignores_json_key_order(self) -> None:
        expected = self.base / "expected.json.zst"
        actual = self.base / "actual.json.zst"
        expected_json = self.base / "expected.json"
        actual_json = self.base / "actual.json"
        expected_json.write_text('{"b": [1, 2], "a": {"z": true}}\n')
        actual_json.write_text('{"a": {"z": true}, "b": [1, 2]}\n')
        command(
            ["zstd", "--quiet", "--force", str(expected_json), "-o", str(expected)],
            self.base,
        )
        command(
            ["zstd", "--quiet", "--force", str(actual_json), "-o", str(actual)],
            self.base,
        )
        self.assertEqual(zstd_semantic_diff(expected, actual), ())
        self.assertEqual(
            validate_zstd_fixture_tree(self.base),
            ("actual.json.zst", "expected.json.zst"),
        )

    def test_zstd_fixture_semantic_review_reports_value_changes(self) -> None:
        expected = self.base / "expected.json.zst"
        actual = self.base / "actual.json.zst"
        expected_json = self.base / "expected.json"
        actual_json = self.base / "actual.json"
        expected_json.write_text('{"value": 1}\n')
        actual_json.write_text('{"value": 2}\n')
        command(
            ["zstd", "--quiet", "--force", str(expected_json), "-o", str(expected)],
            self.base,
        )
        command(
            ["zstd", "--quiet", "--force", str(actual_json), "-o", str(actual)],
            self.base,
        )
        self.assertTrue(zstd_semantic_diff(expected, actual))

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
