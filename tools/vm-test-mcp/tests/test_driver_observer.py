"""Exercise lifecycle observation against the project's locked NixOS driver."""

import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


class DriverLifecycle(unittest.TestCase):
    def test_driver_receipts_and_original_outcomes(self):
        driver = shutil.which("nixos-test-driver")
        self.assertIsNotNone(driver, "the locked nixos-test-driver is required")
        observer = Path(os.environ.get("VM_TEST_OBSERVER", Path(__file__).resolve().parents[1] / "internal" / "submit"))
        cases = [
            ("passed", "print('script completed')", 30, True),
            ("failed", "raise AssertionError('controlled script failure')", 30, True),
            ("driver_failed", None, 30, False),
            ("timed_out", "import time\ntime.sleep(60)", 1, True),
        ]
        for outcome, script, deadline, started in cases:
            with self.subTest(outcome=outcome), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                output = root / "evidence"
                output.mkdir()
                test_script = root / "script.py"
                if script is not None:
                    test_script.write_text(script)
                config = root / "driver.json"
                config.write_text(json.dumps({
                    "vms": {}, "containers": {}, "vlans": [],
                    "global_timeout": deadline, "enable_ssh_backdoor": False,
                    "test_script": str(test_script),
                }))
                env = dict(os.environ)
                env.update(
                    VM_TEST_EVIDENCE=str(output),
                    VM_TEST_RUN_ID="a" * 32,
                    PYTHONPATH=str(observer),
                    PYTHONDONTWRITEBYTECODE="1",
                    LOGFILE=str(output / "native-driver.xml"),
                )
                result = subprocess.run(
                    [driver, "--config", str(config), "-o", str(output)],
                    cwd=root, env=env, capture_output=True, text=True, timeout=45,
                )
                receipt_path = output / "driver-result.json"
                self.assertTrue(receipt_path.exists(), result.stderr)
                receipt = json.loads(receipt_path.read_text())
                self.assertEqual(receipt["outcome"], outcome, result.stderr)
                self.assertEqual(receipt["test_started"], started)
                self.assertEqual(receipt["phase"], "finished")
                self.assertEqual(receipt["test_run_id"], "a" * 32)
                self.assertEqual(result.returncode == 0, outcome == "passed", result.stderr)
                self.assertTrue((output / "driver-diagnostics.json").exists())
                if outcome in ("failed", "driver_failed"):
                    self.assertTrue((output / "driver-error.log").stat().st_size)


if __name__ == "__main__":
    unittest.main()
