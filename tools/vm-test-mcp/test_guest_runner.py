"""File conflict regressions; Windows cases exercise the real Job Object."""

import contextlib
import ctypes
import json
import subprocess
import sys
import tempfile
import time
import unittest
from pathlib import Path

from guest_runner import read_json, read_status, write_json


@unittest.skipUnless(
    sys.platform == "win32", "requires Windows file sharing and Job Objects"
)
class WindowsSupervisorTests(unittest.TestCase):
    def setUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.directory = Path(self.scratch.name) / "command"
        self.directory.mkdir()
        self.runner = Path(__file__).with_name("guest_runner.py")
        self.kernel = ctypes.WinDLL("kernel32", use_last_error=True)
        from ctypes import wintypes as w

        for name, args, result in (
            (
                "CreateFileW",
                [w.LPCWSTR, w.DWORD, w.DWORD, w.LPVOID, w.DWORD, w.DWORD, w.HANDLE],
                w.HANDLE,
            ),
            ("OpenProcess", [w.DWORD, w.BOOL, w.DWORD], w.HANDLE),
            ("WaitForSingleObject", [w.HANDLE, w.DWORD], w.DWORD),
            ("CloseHandle", [w.HANDLE], w.BOOL),
        ):
            function = getattr(self.kernel, name)
            function.argtypes, function.restype = args, result

    @contextlib.contextmanager
    def lock(self, path, share):
        handle = self.kernel.CreateFileW(str(path), 0x80000000, share, None, 3, 0, None)
        if handle == ctypes.c_void_p(-1).value:
            raise ctypes.WinError(ctypes.get_last_error())
        try:
            yield
        finally:
            self.kernel.CloseHandle(handle)

    def wait_file(self, name):
        deadline = time.monotonic() + 8
        while True:
            try:
                return read_json(self.directory / name)
            except (FileNotFoundError, ValueError):
                if time.monotonic() >= deadline:
                    raise
                time.sleep(0.02)

    def launch(self):
        driver = (
            "import json,os,pathlib,subprocess,sys\n"
            "child=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)'])\n"
            "pathlib.Path('started.json').write_text(json.dumps([os.getpid(),child.pid]))\n"
            "for line in sys.stdin.buffer: sys.stdout.buffer.write(line); sys.stdout.buffer.flush()\n"
        )
        write_json(
            self.directory / "command.json",
            {
                "argv": [sys.executable, "-u", "-c", driver],
                "cwd": str(self.directory),
                "stdin": True,
                "timeout": 15,
                "log_bytes": 65536,
            },
        )
        process = subprocess.Popen(
            [sys.executable, str(self.runner), str(self.directory)],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )

        def stop():
            if process.poll() is None:
                process.kill()
            process.communicate()

        self.addCleanup(stop)
        return process

    def message(self, sequence, text="", eof=False):
        write_json(
            self.directory / f"input-{sequence:08d}.json",
            {"text": text, "eof": eof},
        )

    def result(self, process):
        _, error = process.communicate(timeout=18)
        self.assertEqual(process.returncode, 0, error.decode(errors="replace"))
        result = json.loads(
            subprocess.check_output(
                [sys.executable, str(self.runner), "status", str(self.directory)],
                timeout=5,
            )
        )
        self.assertTrue(result["cleanup_confirmed"], result)
        return result

    def process_handles(self, pids):
        handles = []
        for pid in pids:
            handle = self.kernel.OpenProcess(0x100000, False, pid)
            if not handle:
                raise ctypes.WinError(ctypes.get_last_error())
            handles.append(handle)
            self.addCleanup(self.kernel.CloseHandle, handle)
        return handles

    def test_status_conflict_recovers_and_eof_cleans_descendants(self):
        process = self.launch()
        handles = self.process_handles(self.wait_file("started.json"))
        with self.lock(self.directory / "status.json", share=1):
            self.message(1, "序号1 🐍\n")
            time.sleep(0.4)
            self.assertEqual(read_status(self.directory)["state"], "running")
        self.message(2, "序号2 é\n", eof=True)
        result = self.result(process)
        self.assertEqual((result["state"], result["exit_code"]), ("finished", 0))
        self.assertEqual(result["stdin"]["written_sequence"], 2)
        self.assertIsNone(result["stdin"]["error"])
        self.assertEqual(
            (self.directory / "run.log").read_bytes(), "序号1 🐍\n序号2 é\n".encode()
        )
        for handle in handles:
            self.assertEqual(self.kernel.WaitForSingleObject(handle, 0), 0)

    def test_persistent_status_conflict_still_records_cleanup(self):
        process = self.launch()
        handles = self.process_handles(self.wait_file("started.json"))
        with self.lock(self.directory / "status.json", share=1):
            result = self.result(process)
            self.assertEqual(result["state"], "failed")
            self.assertIsNone(result["exit_code"])
            self.assertIn("status.json", result["error"])
            for handle in handles:
                self.assertEqual(self.kernel.WaitForSingleObject(handle, 0), 0)

    def test_input_conflicts_recover_or_report_delivery_error(self):
        for permanent in (False, True):
            with self.subTest(permanent=permanent):
                self.directory = Path(self.scratch.name) / str(permanent)
                self.directory.mkdir()
                # Publish before starting, so the first read sees the held handle.
                self.message(1, "消息1\n")
                self.message(2, "消息2\n", eof=True)
                with self.lock(self.directory / "input-00000001.json", share=0):
                    process = self.launch()
                    self.wait_file("started.json")
                    if permanent:
                        result = self.result(process)
                    else:
                        time.sleep(0.4)
                if not permanent:
                    result = self.result(process)
                self.assertEqual(result["exit_code"], 0)
                stdin = result["stdin"]
                self.assertTrue(stdin["closed"])
                if permanent:
                    self.assertEqual(stdin["written_sequence"], 0)
                    self.assertIn("input-00000001.json", stdin["error"])
                    self.assertEqual((self.directory / "run.log").read_bytes(), b"")
                else:
                    self.assertEqual(stdin["written_sequence"], 2)
                    self.assertIsNone(stdin["error"])
                    self.assertEqual(
                        (self.directory / "run.log").read_bytes(),
                        "消息1\n消息2\n".encode(),
                    )


if __name__ == "__main__":
    unittest.main()
