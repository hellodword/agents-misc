from __future__ import annotations

import importlib.util
import shutil
import tempfile
import unittest
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parents[1]
SCRIPT = REPO_ROOT / "scripts" / "check-workflows.py"
SPEC = importlib.util.spec_from_file_location("check_workflows", SCRIPT)
assert SPEC and SPEC.loader
CHECKER = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(CHECKER)


class CheckWorkflowsTests(unittest.TestCase):
    def setUp(self) -> None:
        self.temporary = tempfile.TemporaryDirectory()
        self.root = Path(self.temporary.name)
        shutil.copytree(
            REPO_ROOT / ".github",
            self.root / ".github",
            copy_function=shutil.copyfile,
        )
        # copytree preserves read-only Nix store directory modes.
        (self.root / ".github" / "workflows").chmod(0o755)

    def tearDown(self) -> None:
        self.temporary.cleanup()

    def workflow(self, name: str) -> Path:
        return self.root / ".github" / "workflows" / name

    def replace(self, name: str, before: str, after: str) -> None:
        path = self.workflow(name)
        text = path.read_text(encoding="utf-8")
        self.assertIn(before, text)
        path.write_text(text.replace(before, after, 1), encoding="utf-8")

    def errors(self) -> list[str]:
        return CHECKER.check_repository(self.root)

    def test_valid_workflows_pass(self) -> None:
        self.assertEqual([], self.errors())

    def test_missing_agent_rules_workflow_fails(self) -> None:
        self.workflow("check-agent-rules.yml").unlink()
        self.assertTrue(
            any(
                "check-agent-rules.yml: invalid workflow YAML" in error
                for error in self.errors()
            )
        )

    def test_agent_rules_workflow_requires_nix_setup_before_commands(self) -> None:
        path = self.workflow("check-agent-rules.yml")
        original = path.read_text(encoding="utf-8")
        setup = "      - uses: ./.github/actions/setup-nix\n"
        self.assertIn(setup, original)
        for replacement in (
            original.replace(setup, ""),
            original.replace(setup, "") + "\n" + setup,
        ):
            with self.subTest(workflow=replacement):
                path.write_text(replacement, encoding="utf-8")
                self.assertTrue(
                    any(
                        "check-agent-rules.yml: job 'verify' must use" in error
                        for error in self.errors()
                    )
                )

    def test_codex_cache_job_without_verification_fails(self) -> None:
        self.replace("build-codex.yml", "    needs: verify\n", "")
        self.assertTrue(
            any("job 'build' must depend" in error for error in self.errors())
        )

    def test_pages_deploy_without_verified_ancestor_fails(self) -> None:
        self.replace(
            "publish-codex-config-atlas-pages.yml",
            "    needs: build\n",
            "    needs: []\n",
        )
        self.assertTrue(
            any("job 'deploy' must depend" in error for error in self.errors())
        )

    def test_action_commit_sha_is_rejected(self) -> None:
        self.replace(
            "build-codex.yml",
            "actions/checkout@v7",
            "actions/checkout@0123456789abcdef0123456789abcdef01234567",
        )
        self.assertTrue(any("official major tag" in error for error in self.errors()))

    def test_inline_nix_installer_is_rejected(self) -> None:
        self.replace(
            "build-codex.yml",
            "          set -euo pipefail\n",
            "          set -euo pipefail\n          curl https://nixos.org/nix/install\n",
        )
        self.assertTrue(any("installer inline" in error for error in self.errors()))

    def test_missing_native_viewer_runner_fails(self) -> None:
        self.replace(
            "build-agents-viewer.yml",
            "          - artifact: agents-viewer-windows-aarch64\n"
            "            runner: windows-11-arm\n"
            "            executable: tools/agents-viewer/target/release/agents-viewer.exe\n",
            "",
        )
        self.assertTrue(
            any("runner matrix mismatch" in error for error in self.errors())
        )


if __name__ == "__main__":
    unittest.main()
