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

    def test_single_entrypoint_documents_all_commands(self) -> None:
        result = self.run_cli("--help")
        self.assertEqual(result.returncode, 0, result.stderr)
        for command in ("fetch", "apply", "refresh", "verify"):
            self.assertIn(command, result.stdout)
        self.assertIn("--repo-root", result.stdout)

    def test_subcommand_flags_are_scoped(self) -> None:
        self.assertIn("--check", self.run_cli("apply", "--help").stdout)
        self.assertIn("--dry-run", self.run_cli("refresh", "--help").stdout)
        result = self.run_cli("fetch", "--check")
        self.assertEqual(result.returncode, 2)
        self.assertIn("unrecognized arguments", result.stderr)

    def test_error_includes_recovery(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            result = self.run_cli("--repo-root", temporary, "apply", "--check")
        self.assertEqual(result.returncode, 1)
        self.assertIn("error:", result.stderr)
        self.assertIn("recovery:", result.stderr)

    def test_only_two_maintenance_modules_remain(self) -> None:
        scripts = {
            path.name
            for path in (ROOT / "codex" / "scripts").glob("*.py")
            if path.is_file() and path.name != "__init__.py"
        }
        self.assertEqual(scripts, {"maintain.py", "workflow.py"})


if __name__ == "__main__":
    unittest.main()
