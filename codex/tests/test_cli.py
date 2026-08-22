from __future__ import annotations

import subprocess
import sys
import tempfile
import unittest
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
SCRIPTS = (
    "apply-patches.py",
    "build.py",
    "fetch-upstream.py",
    "refresh-patches.py",
    "test.py",
)


class CliTests(unittest.TestCase):
    def run_script(self, script: str, *args: str) -> subprocess.CompletedProcess[str]:
        return subprocess.run(
            [sys.executable, str(ROOT / "codex" / "scripts" / script), *args],
            cwd=ROOT,
            check=False,
            text=True,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

    def test_all_commands_document_help(self) -> None:
        for script in SCRIPTS:
            with self.subTest(script=script):
                result = self.run_script(script, "--help")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertIn("usage:", result.stdout)
                self.assertIn("--repo-root", result.stdout)

    def test_error_includes_recovery(self) -> None:
        with tempfile.TemporaryDirectory() as temporary:
            result = self.run_script("apply-patches.py", "--repo-root", temporary, "--check")
        self.assertEqual(result.returncode, 1)
        self.assertIn("error:", result.stderr)
        self.assertIn("recovery:", result.stderr)

    def test_removed_ref_argument_is_not_accepted(self) -> None:
        result = self.run_script("fetch-upstream.py", "--ref", "rust-v0.147.0")
        self.assertEqual(result.returncode, 2)
        self.assertIn("unrecognized arguments", result.stderr)


if __name__ == "__main__":
    unittest.main()
