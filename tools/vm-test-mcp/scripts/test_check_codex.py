"""Exercise acceptance-run ownership with real local child processes."""

import importlib.util
import json
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "check_codex", Path(__file__).with_name("check-codex.py")
)
check = importlib.util.module_from_spec(spec)
spec.loader.exec_module(check)


class AcceptanceLifecycleTests(unittest.TestCase):
    def test_processes_exit_before_runtime_removal(self):
        for outcome in (None, RuntimeError, KeyboardInterrupt):
            with (
                self.subTest(outcome=outcome),
                tempfile.TemporaryDirectory() as directory,
            ):
                work = Path(directory)
                try:
                    with check.AcceptanceRun(work, {}) as run:
                        profile = run.runtime / "profile"
                        profile.mkdir()
                        (profile / "cache").write_bytes(b"temporary runtime")
                        process = run.start(
                            "client",
                            [
                                sys.executable,
                                "-u",
                                "-c",
                                (
                                    "import pathlib, sys, tempfile; "
                                    "assert pathlib.Path(tempfile.gettempdir()) == pathlib.Path.cwd() / 'tmp'; "
                                    "tempfile.NamedTemporaryFile(delete=False).close(); print('ready'); sys.stdin.read(); "
                                    "assert pathlib.Path('profile/cache').read_bytes(); print('released')"
                                ),
                            ],
                            stdin=subprocess.PIPE,
                            cwd=run.runtime,
                        )
                        if outcome:
                            raise outcome("synthetic interruption")
                except (RuntimeError, KeyboardInterrupt):
                    if outcome is None:
                        raise
                self.assertEqual(process.returncode, 0)
                self.assertFalse(run.runtime.exists())
                self.assertIn("released", (work / "client.log").read_text())
                report = json.loads((work / "result.json").read_text())
                self.assertEqual(report["cleanup"]["state"], "complete")
                self.assertEqual("error" in report, outcome is not None)

    def test_client_initialization_failure_is_owned(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            binary = work / "synthetic-codex"
            binary.write_text(
                f"#!{sys.executable}\nimport sys\nprint('initializing', file=sys.stderr)\nsys.stdin.read()\n"
            )
            binary.chmod(0o700)
            with (
                self.assertRaisesRegex(RuntimeError, "initialization failure"),
                check.AcceptanceRun(work, {}) as run,
                patch.object(
                    check.Client,
                    "call",
                    side_effect=RuntimeError("initialization failure"),
                ),
            ):
                check.Client(run, "client", str(binary), "http://127.0.0.1", 660)
            self.assertFalse(run.runtime.exists())
            self.assertEqual(run.processes[0][1].returncode, 0)
            self.assertIn("initializing", (work / "client.log").read_text())
            self.assertEqual(
                json.loads((work / "result.json").read_text())["cleanup"]["state"],
                "complete",
            )

    def test_unconfirmed_exit_retains_recovery_and_stops_other_children(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            actual_stop = check.stop_process
            pending = None

            def stop(process):
                if process is pending:
                    raise RuntimeError("synthetic unconfirmed exit")
                return actual_stop(process)

            try:
                with (
                    patch.object(check, "stop_process", side_effect=stop),
                    self.assertRaisesRegex(RuntimeError, "cleanup is pending"),
                    check.AcceptanceRun(work, {}) as run,
                ):
                    stopped = run.start(
                        "stopped",
                        [sys.executable, "-c", "import sys; sys.stdin.read()"],
                        stdin=subprocess.PIPE,
                    )
                    pending = run.start(
                        "pending",
                        [sys.executable, "-c", "import sys; sys.stdin.read()"],
                        stdin=subprocess.PIPE,
                    )
                self.assertEqual(stopped.returncode, 0)
                self.assertIsNone(pending.poll())
                self.assertTrue(run.runtime.exists())
                report = json.loads((work / "result.json").read_text())
                self.assertEqual(report["cleanup"]["state"], "pending")
                self.assertEqual(report["cleanup"]["runtime"], str(run.runtime))
                self.assertIn("pending", report["cleanup"]["errors"][0])
            finally:
                if pending:
                    actual_stop(pending)

    def test_forced_stop_reaps_the_process_before_removal(self):
        with tempfile.TemporaryDirectory() as directory:
            work = Path(directory)
            actual_stop = check.stop_process
            with (
                patch.object(
                    check,
                    "stop_process",
                    side_effect=lambda p: actual_stop(p, timeout=0.2),
                ),
                check.AcceptanceRun(work, {}) as run,
            ):
                process = run.start(
                    "stubborn",
                    [
                        sys.executable,
                        "-u",
                        "-c",
                        "import signal, time; signal.signal(signal.SIGTERM, signal.SIG_IGN); print('ready'); time.sleep(60)",
                    ],
                )
                deadline = time.monotonic() + 3
                while "ready" not in (work / "stubborn.log").read_text():
                    if time.monotonic() > deadline:
                        self.fail("synthetic child did not start")
                    time.sleep(0.01)
            self.assertIsNotNone(process.returncode)
            self.assertFalse(run.runtime.exists())


if __name__ == "__main__":
    unittest.main()
