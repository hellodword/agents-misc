from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
MAINTAIN = ROOT / "codex" / "scripts" / "maintain.py"


class CliTests(unittest.TestCase):
    def run_cli(self, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(MAINTAIN), *args],
            cwd=ROOT,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def test_help_does_not_require_a_checkout(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            result = self.run_cli("--repo-root", temporary, "--help")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("--repo-root", result.stdout)

    def test_subcommand_flags_are_scoped(self) -> None:
        self.assertIn("--check", self.run_cli("apply", "--help").stdout)
        self.assertIn("--dry-run", self.run_cli("refresh", "--help").stdout)
        result = self.run_cli("fetch", "--check")
        self.assertEqual(result.returncode, 2)
        self.assertIn("unrecognized arguments", result.stderr)

    def test_missing_manifest_reports_failure_on_stderr(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            result = self.run_cli("--repo-root", temporary, "apply", "--check")
        self.assertEqual(result.returncode, 1)
        self.assertEqual(result.stdout, "")
        self.assertIn("codex/upstream.toml", result.stderr)


if __name__ == "__main__":
    unittest.main()
