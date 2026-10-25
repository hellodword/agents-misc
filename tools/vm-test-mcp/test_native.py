import json
import os
import shutil
import subprocess
import tempfile
import unittest
from pathlib import Path


@unittest.skipUnless(
    os.environ.get("VM_TEST_SDK_DIR") and os.environ.get("VM_TEST_NATIVE_BINARY"),
    "run just --justfile tools/vm-test-mcp/justfile test-helper for native ABI tests",
)
class NativeTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.root = Path(__file__).resolve().parent
        cls.scratch = tempfile.TemporaryDirectory()
        cls.addClassCleanup(cls.scratch.cleanup)
        cls.directory = Path(cls.scratch.name)
        sdk = Path(os.environ["VM_TEST_SDK_DIR"]).resolve() / "bindings" / "c"
        sanitize = bool(os.environ.get("VM_TEST_NATIVE_SANITIZE"))
        flags = ["-fsanitize=address,undefined"] if sanitize else []
        subprocess.run(
            [
                shutil.which("cc"),
                "-shared",
                "-fPIC",
                "-std=c11",
                "-Wall",
                "-Wextra",
                "-Werror",
                *flags,
                "-I",
                str(sdk / "include"),
                "-I",
                str(sdk / "glue"),
                str(cls.root / "tests" / "native_fake.c"),
                "-o",
                str(cls.directory / "VBoxXPCOMC.so"),
            ],
            check=True,
        )
        cls.binary = Path(os.environ["VM_TEST_NATIVE_BINARY"])

    def call(
        self,
        action="probe",
        *,
        screen=0,
        x=0,
        y=0,
        end_x=0,
        end_y=0,
        button=1,
        dz=0,
        dw=0,
        relative=0,
        fail="",
        version="7002020",
        session="1",
        names=(),
        owned_name="",
        active_name="",
    ):
        arguments = list(map(str, (screen, x, y, end_x, end_y, button, dz, dw, relative)))
        if action == "session":
            arguments = []
        elif action == "guest-sessions":
            arguments = ["synthetic", *names]
        return subprocess.run(
            [
                str(self.binary),
                str(self.directory),
                "11111111-2222-4333-8444-555555555555",
                action,
                *arguments,
            ],
            env={
                **os.environ,
                "VM_TEST_NATIVE_FAIL": fail,
                "VM_TEST_NATIVE_VERSION": version,
                "VM_TEST_NATIVE_SESSION": session,
                "VM_TEST_NATIVE_OWNED_NAME": owned_name,
                "VM_TEST_NATIVE_ACTIVE_NAME": active_name,
            },
            capture_output=True,
            text=True,
            timeout=10,
            check=False,
        )

    def test_absolute_screen_coordinates_drag_and_release(self):
        result = self.call("click", screen=1, x=10, y=20)
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertIn("ABS -1909 21 0 0 1", result.stderr)
        self.assertIn("ABS -1909 21 0 0 0", result.stderr)
        self.assertIn("REFS 0 LOCKS 0", result.stderr)
        drag = self.call("drag", x=0, y=0, end_x=100, end_y=50)
        self.assertEqual(drag.returncode, 0, drag.stderr)
        self.assertIn("ABS 101 51 0 0 1", drag.stderr)
        relative = self.call("scroll", relative=1, dz=-2)
        self.assertEqual(relative.returncode, 0, relative.stderr)
        self.assertIn("REL 0 0 -2 0 0", relative.stderr)

    def test_failed_delivery_releases_buttons_and_objects(self):
        result = self.call("click", fail="send")
        self.assertEqual(result.returncode, 1)
        self.assertIn("ABS 1 1 0 0 0", result.stderr)
        self.assertIn("REFS 0 LOCKS 0", result.stderr)

    def test_runtime_version_is_separate_from_interface_compatibility(self):
        for version in ("7002020", "7002022", "8000000"):
            with self.subTest(version=version):
                result = self.call(version=version)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(
                    json.loads(result.stdout)["runtime_version"], int(version)
                )
        for interface in ("IVirtualBox", "IMachine", "IConsole", "IMouse", "IDisplay"):
            with self.subTest(interface=interface):
                result = self.call("click", fail=interface)
                self.assertEqual(result.returncode, 1)
                self.assertIn("REFS 0 LOCKS 0", result.stderr)
                self.assertNotIn("ABS ", result.stderr)
        table = self.call(fail="table")
        self.assertEqual(table.returncode, 1)

    def test_session_observation_never_locks_or_accesses_input(self):
        for number, state in (("1", "unlocked"), ("2", "locked"), ("3", "spawning"), ("4", "unlocking"), ("0", "unknown")):
            with self.subTest(state=state):
                result = self.call("session", session=number, fail="IConsole")
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)["state"], state)
                self.assertIn("REFS 0 LOCKS 0", result.stderr)
        failed = self.call("session", fail="session_read")
        self.assertEqual(failed.returncode, 1)
        self.assertEqual(failed.stdout, "")
        self.assertIn("REFS 0 LOCKS 0", failed.stderr)

    def test_guest_sessions_require_exact_name_user_and_dead_owner(self):
        suffix = " VBoxManage Guest Control [11111111-2222-4333-8444-555555555555] - run"
        owned = "[1000000000]" + suffix
        active = f"[{os.getpid()}]" + suffix
        result = self.call("guest-sessions", names=(owned, active), owned_name=owned, active_name=active)
        self.assertEqual(result.returncode, 0, result.stderr)
        observed = json.loads(result.stdout)
        self.assertEqual(observed["before"], 3)
        self.assertEqual(observed["after"], 2)
        self.assertEqual(observed["remaining"], [False, True])
        self.assertEqual(observed["live_owner"], 1)
        self.assertIn("CLOSE_GUEST 0", result.stderr)
        self.assertNotIn("CLOSE_GUEST 1", result.stderr)
        self.assertNotIn("CLOSE_GUEST 2", result.stderr)
        self.assertIn("REFS 0 LOCKS 0", result.stderr)
        for failure in ("guest_close", "guest_user"):
            with self.subTest(failure=failure):
                result = self.call("guest-sessions", names=(owned,), owned_name=owned, fail=failure)
                self.assertEqual(result.returncode, 0, result.stderr)
                self.assertEqual(json.loads(result.stdout)["remaining"], [True])
                self.assertIn("REFS 0 LOCKS 0", result.stderr)
        for failure in ("IGuest", "IGuestSession", "guest_list", "guest_name"):
            with self.subTest(failure=failure):
                result = self.call("guest-sessions", names=(owned,), owned_name=owned, fail=failure)
                self.assertEqual(result.returncode, 1)
                self.assertEqual(result.stdout, "")
                self.assertIn("REFS 0 LOCKS 0", result.stderr)


if __name__ == "__main__":
    unittest.main()
