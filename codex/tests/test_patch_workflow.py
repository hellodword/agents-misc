from __future__ import annotations

import dataclasses
import hashlib
import json
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
    apply_patches,
    changed_tree_digest,
    fetch_upstream,
    load_manifest,
    patch_paths,
    refresh_patches,
    run_generation_sequence,
    terminal_marker_hits,
    validate_binary_patches,
    validate_zstd_fixture_tree,
    zstd_semantic_diff,
    validate_patch_directory,
    audit_overload_producer_coverage,
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
            command(["git", "clone", "--quiet", "--no-hardlinks", str(self.upstream_repo), str(clone)], self.base)
            command(["git", "checkout", "--quiet", "--detach", self.revision], clone)
            apply_patches(clone, patch_paths(self.manifest), check_only=False)
            self.assertEqual((clone / "src" / "feature.txt").read_text(), "edited\n")
            self.assertEqual((clone / "generated" / "schema.txt").read_text(), "schema:edited\n")

    def test_apply_second_patch_failure_preserves_complete_original_tree(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        target = self.base / "apply-second-failure"
        command(
            ["git", "clone", "--quiet", "--no-hardlinks", str(self.upstream_repo), str(target)],
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
        self.assertEqual((target / "generated" / "schema.txt").read_text(), "schema:base\n")
        self.assertEqual(command(["git", "status", "--short"], target, capture=True), "")

    def test_apply_exchange_failure_preserves_complete_original_tree(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        target = self.base / "apply-exchange-failure"
        command(
            ["git", "clone", "--quiet", "--no-hardlinks", str(self.upstream_repo), str(target)],
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
        self.assertEqual((target / "generated" / "schema.txt").read_text(), "schema:base\n")
        self.assertEqual(command(["git", "status", "--short"], target, capture=True), "")

    def test_apply_exchange_commit_has_only_complete_old_or_new_trees(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        target = self.base / "apply-commit-points"
        command(
            ["git", "clone", "--quiet", "--no-hardlinks", str(self.upstream_repo), str(target)],
            self.base,
        )
        command(["git", "checkout", "--quiet", "--detach", self.revision], target)
        real_exchange = common._exchange_directories

        def inspect_exchange(candidate: Path, installed: Path) -> None:
            self.assertEqual((installed / "src" / "feature.txt").read_text(), "base\n")
            self.assertEqual((candidate / "src" / "feature.txt").read_text(), "edited\n")
            real_exchange(candidate, installed)
            self.assertEqual((installed / "src" / "feature.txt").read_text(), "edited\n")
            self.assertEqual((candidate / "src" / "feature.txt").read_text(), "base\n")

        with patch.object(common, "_exchange_directories", side_effect=inspect_exchange):
            apply_patches(target, patch_paths(self.manifest), check_only=False)

        self.assertEqual((target / "src" / "feature.txt").read_text(), "edited\n")
        self.assertEqual((target / "generated" / "schema.txt").read_text(), "schema:edited\n")

    def test_apply_cleanup_failure_keeps_new_tree_and_reports_old_path(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        target = self.base / "apply-cleanup-failure"
        command(
            ["git", "clone", "--quiet", "--no-hardlinks", str(self.upstream_repo), str(target)],
            self.base,
        )
        command(["git", "checkout", "--quiet", "--detach", self.revision], target)

        with patch.object(common.shutil, "rmtree", side_effect=OSError("injected cleanup failure")):
            apply_patches(target, patch_paths(self.manifest), check_only=False)

        retained = list(target.parent.glob(f".{target.name}.apply-candidate-*"))
        self.assertEqual(len(retained), 1)
        self.assertEqual((target / "src" / "feature.txt").read_text(), "edited\n")
        self.assertEqual((retained[0] / "src" / "feature.txt").read_text(), "base\n")
        common.shutil.rmtree(retained[0])

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

    def test_refresh_exchange_command_failure_preserves_installed_patch_tree(self) -> None:
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

    def test_refresh_cleanup_failure_keeps_new_patch_tree_and_old_tree_path(self) -> None:
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

        def fail_old_candidate(path: Path, *args: object, **kwargs: object) -> None:
            if ".candidate-" in Path(path).name:
                raise OSError("injected cleanup failure")
            real_rmtree(path, *args, **kwargs)

        with patch.object(common.shutil, "rmtree", side_effect=fail_old_candidate):
            refresh_patches(updated, dry_run=False)

        current = file_hashes(self.manifest.patch_dir)
        self.assertNotEqual(current, previous)
        retained = list(self.manifest.patch_dir.parent.glob(f".{self.manifest.upstream.ref}.candidate-*"))
        self.assertEqual(len(retained), 1)
        self.assertEqual(file_hashes(retained[0]), previous)
        real_rmtree(retained[0])

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

    def test_stale_patch_file_is_rejected_before_refresh_or_apply(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        stale = self.manifest.patch_dir / "0099-stale.patch"
        stale.write_text("stale\n")
        with self.assertRaisesRegex(PatchError, "not listed"):
            validate_patch_directory(self.manifest)
        stale.unlink()

    def test_generator_second_run_drift_is_rejected(self) -> None:
        counter = self.manifest.worktree / "generated" / "counter.txt"
        drifting = MaintenanceManifest(
            root=self.manifest.root,
            upstream=dataclasses.replace(
                self.manifest.upstream,
                generate_commands=(
                    (
                        "python3",
                        "-c",
                        "from pathlib import Path; p=Path('generated/counter.txt'); "
                        "p.write_text(str(int(p.read_text() or '0') + 1) if p.exists() else '1')",
                    ),
                ),
            ),
            patches=self.manifest.patches,
        )
        with self.assertRaisesRegex(PatchError, "not reproducible"):
            run_generation_sequence(drifting, self.manifest.worktree, twice=True)
        self.assertTrue(counter.is_file())

    def test_binary_patch_is_checked_and_applied(self) -> None:
        refresh_patches(self.manifest, dry_run=False)
        source = self.base / "binary-source"
        command(
            ["git", "clone", "--quiet", "--no-hardlinks", str(self.upstream_repo), str(source)],
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
        target = self.base / "binary-target"
        command(
            ["git", "clone", "--quiet", "--no-hardlinks", str(source), str(target)],
            self.base,
        )
        command(["git", "checkout", "--quiet", "--detach", self.revision], target)
        self.assertEqual(validate_binary_patches(target, [binary_patch]), ("binary.patch",))

    def test_terminal_marker_gate_reports_source_hits(self) -> None:
        source = self.base / "markers"
        source.mkdir()
        (source / "example.rs").write_text("let _ = terminal_wait;\n")
        hits = terminal_marker_hits(source)
        self.assertEqual(len(hits), 1)
        self.assertIn("terminal_wait", hits[0])

    def test_zstd_fixture_semantic_review_ignores_json_key_order(self) -> None:
        expected = self.base / "expected.json.zst"
        actual = self.base / "actual.json.zst"
        expected_json = self.base / "expected.json"
        actual_json = self.base / "actual.json"
        expected_json.write_text('{"b": [1, 2], "a": {"z": true}}\n')
        actual_json.write_text('{"a": {"z": true}, "b": [1, 2]}\n')
        command(["zstd", "--quiet", "--force", str(expected_json), "-o", str(expected)], self.base)
        command(["zstd", "--quiet", "--force", str(actual_json), "-o", str(actual)], self.base)
        self.assertEqual(zstd_semantic_diff(expected, actual), ())
        self.assertEqual(validate_zstd_fixture_tree(self.base), ("actual.json.zst", "expected.json.zst"))

    def test_zstd_fixture_semantic_review_reports_value_changes(self) -> None:
        expected = self.base / "expected.json.zst"
        actual = self.base / "actual.json.zst"
        expected_json = self.base / "expected.json"
        actual_json = self.base / "actual.json"
        expected_json.write_text('{"value": 1}\n')
        actual_json.write_text('{"value": 2}\n')
        command(["zstd", "--quiet", "--force", str(expected_json), "-o", str(expected)], self.base)
        command(["zstd", "--quiet", "--force", str(actual_json), "-o", str(actual)], self.base)
        self.assertTrue(zstd_semantic_diff(expected, actual))

    def test_overload_producer_coverage_requires_shared_adapter(self) -> None:
        source = self.base / "producers"
        source.mkdir()
        (source / "sampling.rs").write_text("model_request_failure_policy();\n")
        (source / "compact.rs").write_text("return Err(overloaded);\n")
        with self.assertRaisesRegex(PatchError, "compact"):
            audit_overload_producer_coverage(
                source,
                producer_files={"sampling": "sampling.rs", "compact": "compact.rs"},
            )

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
