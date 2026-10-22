import asyncio
import base64
import contextlib
import json
import os
import shutil
import socket
import sys
import tempfile
import time
import unittest
from pathlib import Path, PureWindowsPath
from unittest.mock import patch
from urllib.parse import urlsplit

import httpx2
import uvicorn
from common import DEFAULTS, Failure, atomic_json, load_json, terminate, vm_policy
from guest_runner import InputPipe, read_status, retry_file
from mcp import Client
from mcp.client.streamable_http import streamable_http_client
from server import make_app
from tasks import Controller
from virtualbox import VirtualBox, read_window

UUID = "11111111-2222-4333-8444-555555555555"
TOKEN = "synthetic-token"
PNG = base64.b64decode(
    "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aV1sAAAAASUVORK5CYII="
)


class RecordingVM(VirtualBox):
    """Synthetic VBox boundary; guest commands run real isolated POSIX processes."""

    def __init__(self, directory, target=UUID):
        self.directory = Path(directory)
        self.guest_root = self.directory / "guest"
        self.guest_root.mkdir(exist_ok=True)
        super().__init__(
            target,
            profile={
                "vm": target,
                "baseline": "ready",
                "interruptible": True,
                "username": "tester",
                "password": "synthetic",
                "python": sys.executable,
                "workspace": r"C:\vm-tests",
                **vm_policy({}, "vms.windows"),
            },
        )
        self.config = {
            "UUID": target,
            "VMState": "poweroff",
            "cpus": "4",
            "LogFldr": str(directory),
            "teleporterpassword": "omit-this",
        }
        self.calls, self.children, self.jobs = [], [], []
        self.sessions = {}
        self.next_session_id = 1
        self.host_webcams = [(".1", 'Camera "A"', "/dev/video0")]
        self.attached_webcams = []
        self.shutdown_hangs = False
        self.poweroff_hangs = False
        (self.directory / "VBox.log").write_bytes(b"first line\nsecond line\n")

    async def command(self, arguments, *, offset=0, limit=1024 * 1024, timeout=None):
        self.calls.append(arguments)
        if arguments[0] == "showvminfo":
            data = "\n".join(
                f"{k}={json.dumps(v)}" for k, v in self.config.items()
            ).encode()
        elif arguments[0] == "modifyvm":
            for flag, value in zip(arguments[2::2], arguments[3::2], strict=True):
                self.config[flag.removeprefix("--")] = value
            data = b"changed"
        elif arguments == ["--version"]:
            data = b"7.2.synthetic"
        elif arguments == ["list", "hostinfo"]:
            data = b"Processor count: 8"
        elif arguments == ["list", "webcams"]:
            data = (
                f"Video Input Devices: {len(self.host_webcams)}\n"
                + "".join(
                    f'{alias} "{name}"\n{reference}\n'
                    for alias, name, reference in self.host_webcams
                )
            ).encode()
        elif arguments == ["list", "runningvms"]:
            data = (
                f'"fixture" {{{self.target}}}\n'
                if self.config["VMState"] == "running"
                else ""
            ).encode()
        elif arguments[:2] == ["guestcontrol", self.target]:
            if arguments[2] == "start":
                if len(self.sessions) >= 32:
                    raise Failure("Guest session limit reached")
                identity = self.next_session_id
                self.next_session_id += 1
                self.sessions[identity] = {
                    "name": f"[{identity + 1000}] VBoxManage Guest Control [{self.target}] - start",
                    "pid": identity + 100,
                }
                self.jobs.append(
                    asyncio.create_task(self.run_guest(self.local(arguments[-1])))
                )
                data = f"Creating guest session as user 'tester'...\n[{identity + 100} - Session {identity}]\nGuest session detached\n".encode()
            elif arguments[2:4] == ["list", "all"]:
                listing = "\n".join(
                    f"Session #{index} ID={identity} User=tester Status=[started] Name={session['name']}\n"
                    f"  Process #0 PID={session['pid']} Status=[started] Command=python"
                    for index, (identity, session) in enumerate(self.sessions.items())
                )
                data = (
                    listing + f"\n\nTotal guest sessions: {len(self.sessions)}\n"
                    if self.sessions
                    else "No active guest sessions found\n"
                ).encode()
            elif arguments[2:4] == ["closesession", "--session-id"]:
                del self.sessions[int(arguments[4])]
                data = b""
            else:
                raise AssertionError(arguments)
        elif arguments[0] == "startvm":
            self.config["VMState"] = "running"
            data = b"started"
        elif arguments[0] == "controlvm":
            if arguments[2:4] == ["webcam", "list"]:
                data = "".join(x + "\n" for x in self.attached_webcams).encode()
                return data[offset : offset + limit], len(data)
            if arguments[2:4] == ["webcam", "attach"]:
                self.attached_webcams.append(arguments[4] or "default")
            elif arguments[2:4] == ["webcam", "detach"]:
                self.attached_webcams.remove(arguments[4] or "default")
            elif arguments[2] in {"shutdown", "poweroff"} and (
                not getattr(self, arguments[2] + "_hangs")
            ):
                for child in self.children:
                    await terminate(child, grace=0.1)
                self.config["VMState"] = "poweroff"
                self.attached_webcams.clear()
                self.sessions.clear()
            data = b"accepted"
        elif arguments[0] == "snapshot":
            shutil.rmtree(self.guest_root)
            self.guest_root.mkdir()
            self.config["VMState"] = "poweroff"
            data = b"restored"
        else:
            raise AssertionError(arguments)
        return data[offset : offset + limit], len(data)

    def local(self, path):
        return self.guest_root.joinpath(*PureWindowsPath(path).parts[1:])

    async def ready(self, timeout):
        return None

    async def python(self, *args, timeout=None):
        if args[0] == "-c":
            path = self.local(args[-1])
            if "stat().st_size" in args[1]:
                data = str(path.stat().st_size).encode()
                return data, len(data)
            if ".parent.mkdir" in args[1]:
                path = path.parent
            path.mkdir(parents=True, exist_ok=True)
            return b"", 0
        if args[-1] == "probe":
            return b'{"interactive":true,"session_id":1}', 35
        if len(args) > 1 and args[1] == "publish":
            retry_file(lambda: self.local(args[2]).replace(self.local(args[3])))
            return b"", 0
        if len(args) > 1 and args[1] == "status":
            try:
                data = json.dumps(read_status(self.local(args[2]))).encode()
                return data, len(data)
            except OSError as error:
                raise Failure(str(error)) from error
        raise AssertionError(args)

    async def run_guest(self, directory):
        spec = load_json(directory / "command.json")
        state = {"state": "running", "cleanup_confirmed": False}
        atomic_json(directory / "status.json", state)
        argv = spec["argv"]
        if len(argv) > 2 and argv[2] == "pack":
            argv = [
                sys.executable,
                str(Path(__file__).with_name("guest_runner.py")),
                "pack",
                str(self.local(argv[3])),
                str(self.local(argv[4])),
            ]
        with (directory / "run.log").open("wb") as output:
            input_pipe = InputPipe(directory) if spec.get("stdin") else None
            process = await asyncio.create_subprocess_exec(
                *argv,
                stdin=input_pipe.reader if input_pipe else asyncio.subprocess.DEVNULL,
                stdout=output,
                stderr=output,
                cwd=directory.parent,
                start_new_session=True,
            )
            if input_pipe:
                input_pipe.start()
            self.children.append(process)
            started, reason = time.monotonic(), "exited"
            while process.returncode is None:
                if input_pipe:
                    state["stdin"] = dict(input_pipe.state)
                    atomic_json(directory / "status.json", state)
                if (
                    directory.parent / "cancel"
                ).exists() or time.monotonic() - started > spec["timeout"]:
                    reason = (
                        "cancelled"
                        if (directory.parent / "cancel").exists()
                        else "timeout"
                    )
                    await terminate(process, grace=0.1)
                    break
                await asyncio.sleep(0.01)
            await process.wait()
            if input_pipe:
                input_pipe.close()
                state["stdin"] = dict(input_pipe.state)
        state.update(
            state="finished",
            exit_code=process.returncode if reason == "exited" else None,
            reason=reason,
            cleanup_confirmed=True,
        )
        atomic_json(directory / "result.json", state)

    async def copy_to(self, source, destination):
        path = self.local(destination)
        shutil.copyfile(source, path)

    async def copy_from(self, source, destination):
        try:
            shutil.copyfile(self.local(source), destination)
        except FileNotFoundError as error:
            raise Failure("Guest file not yet present") from error

    async def screenshot(self, destination, screen=0):
        Path(destination).write_bytes(PNG)


class HTTPTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.scratch = tempfile.TemporaryDirectory()
        self.addCleanup(self.scratch.cleanup)
        self.root = Path(self.scratch.name)
        self.vm = RecordingVM(self.root)
        self.socket = socket.socket()
        self.socket.bind(("127.0.0.1", 0))
        self.origin = f"http://127.0.0.1:{self.socket.getsockname()[1]}"
        self.config = {
            "service": DEFAULTS
            | {
                "state_dir": str(self.root / "state"),
                "public_url": self.origin,
                "token": TOKEN,
                "shutdown_timeout": 0.1,
                "task_timeout": 30,
            },
            "vms": {"windows": self.vm.profile},
            "nix": {},
        }
        self.controller = Controller(self.config, {"windows": self.vm})
        self.http = uvicorn.Server(
            uvicorn.Config(
                make_app(self.controller),
                log_level="critical",
                access_log=False,
                timeout_graceful_shutdown=1,
            )
        )
        self.serving = asyncio.create_task(self.http.serve(sockets=[self.socket]))
        async with asyncio.timeout(5):
            while not self.http.started:
                if self.serving.done():
                    await self.serving
                await asyncio.sleep(0.01)

    async def asyncTearDown(self):
        self.http.should_exit = True
        await asyncio.wait_for(self.serving, 5)
        for process in self.vm.children:
            await terminate(process, grace=0.1)
        await asyncio.gather(*self.vm.jobs, return_exceptions=True)
        self.socket.close()

    @contextlib.asynccontextmanager
    async def client(self):
        async def record_response(response):
            if hasattr(
                self, "status_wire_sizes"
            ) and "application/json" in response.headers.get("content-type", ""):
                request = json.loads(response.request.content or b"{}")
                if (
                    request.get("method") == "tools/call"
                    and request.get("params", {}).get("name") == "task_status"
                ):
                    self.status_wire_sizes.append(len(await response.aread()))

        async with (
            httpx2.AsyncClient(
                headers={"Authorization": f"Bearer {TOKEN}"},
                trust_env=False,
                event_hooks={"response": [record_response]},
            ) as http,
            Client(
                streamable_http_client(self.origin + "/mcp", http_client=http),
                read_timeout_seconds=5,
            ) as client,
        ):
            yield client

    async def call(self, client, name, **kwargs):
        reply = await client.call_tool(name, kwargs)
        self.assertFalse(reply.is_error, reply)
        return reply.structured_content

    async def wait_state(self, client, key, state):
        async with asyncio.timeout(8):
            while True:
                task = await self.call(client, "task_status", task_id=key)
                if task["state"] == state:
                    commands = await self.call(
                        client, "task_commands", task_id=key, limit=100
                    )
                    task["commands"] = {c["id"]: c for c in commands["items"]}
                    return task
                if task["state"] == "cleanup_unknown" and state != "cleanup_unknown":
                    self.fail(task)
                await asyncio.sleep(0.03)

    @unittest.skipUnless(
        shutil.which("nix"), "Nix CLI is required for native cache interoperability"
    )
    async def test_native_nix_cache_roundtrip(self):
        async def nix(*args):
            process = await asyncio.create_subprocess_exec(
                "nix",
                *args,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            out, err = await asyncio.wait_for(process.communicate(), 30)
            self.assertEqual(process.returncode, 0, err.decode())
            return out.decode().strip()

        source = self.root / "cache-source"
        source.write_text("synthetic content-addressed input")
        path = await nix("store", "add-file", str(source))
        expression = (
            '(derivation { name = "vm-test-cache-fixture"; system = "x86_64-linux"; builder = "/bin/sh"; input = builtins.storePath '
            + json.dumps(path)
            + "; }).drvPath"
        )
        drv = await nix("eval", "--impure", "--raw", "--expr", expression)
        async with self.client() as client:
            cache = await self.call(client, "transfer_create", kind="cache")
            await nix("copy", "--derivation", "--to", cache["url"], drv)
            await self.controller.transfers.seal(cache["id"], "cache")
            destination = self.root / "roundtrip"
            await nix(
                "copy",
                "--derivation",
                "--from",
                cache["url"],
                "--to",
                destination.as_uri(),
                drv,
            )
            self.assertTrue(
                (destination / (Path(path).name[:32] + ".narinfo")).is_file()
            )
            self.assertTrue(
                (destination / (Path(drv).name[:32] + ".narinfo")).is_file()
            )

    async def test_windows_transfer_execution_artifacts_and_retry(self):
        async with self.client() as client, httpx2.AsyncClient(trust_env=False) as http:
            tools = await client.list_tools()
            self.assertTrue(
                all(
                    tool.input_schema.get("additionalProperties") is False
                    for tool in tools.tools
                )
            )
            schemas = {tool.name: tool.input_schema for tool in tools.tools}
            for name in ("task_begin", "vm_power"):
                self.assertFalse(schemas[name]["properties"]["gui"]["default"])
            for name in ("vm_keyboard", "vm_mouse"):
                self.assertIn("input_interval_seconds", schemas[name]["properties"])
            self.assertIn("command_id", schemas["task_status"]["properties"])
            self.assertIn("stdin", schemas["guest_exec"]["properties"])
            self.assertIn("guest_input", schemas)
            self.assertIn("task_commands", schemas)
            self.assertIn("offset", schemas["artifacts"]["properties"])
            service = (await self.call(client, "targets"))["service"]
            self.assertEqual(service["version"], client.server_info.version)
            args = {"kind": "windows", "request_id": "desktop", "target": "windows"}
            first = await self.call(client, "task_begin", **args)
            self.assertEqual(
                first["id"], (await self.call(client, "task_begin", **args))["id"]
            )
            await self.wait_state(client, "desktop", "ready")
            upload = await self.call(client, "transfer_create")
            self.assertEqual(
                (await http.put(upload["url"], content=b"project input")).status_code,
                200,
            )
            await self.call(
                client,
                "guest_upload",
                task_id="desktop",
                transfer_id=upload["id"],
                destination=self.vm.guest_path("desktop", "input.txt"),
            )
            self.assertEqual(
                (await http.put(upload["url"], content=b"overwrite")).status_code, 409
            )
            run = {
                "task_id": "desktop",
                "request_id": "assert",
                "argv": [
                    sys.executable,
                    "-c",
                    "from pathlib import Path; assert Path('input.txt').read_text() == 'project input'; Path('report.txt').write_text('evidence'); print('assertion output'); raise SystemExit(7)",
                ],
            }
            original_copy = self.vm.copy_from

            async def guest_status_with_host_fields(source, destination):
                await original_copy(source, destination)
                if source.endswith("\\status.json"):
                    atomic_json(
                        destination,
                        load_json(destination)
                        | {
                            "id": "../outside",
                            "target": "other-vm",
                            "request": {"argv": ["host-command"]},
                        },
                    )

            with patch.object(
                self.vm, "copy_from", side_effect=guest_status_with_host_fields
            ):
                await self.call(client, "guest_exec", **run)
                task = await self.wait_state(client, "desktop", "ready")
            self.assertEqual((task["id"], task["target"]), ("desktop", "windows"))
            self.assertEqual(task["commands"]["assert"]["id"], "assert")
            self.assertEqual(task["commands"]["assert"]["request"]["argv"], run["argv"])
            self.assertEqual(task["commands"]["assert"]["exit_code"], 7)
            self.assertEqual(task["outcome"]["failed_command_ids"], ["assert"])
            self.assertEqual(task["outcome"]["command_failure_kinds"], {"exit_code": 1})
            count = len(self.vm.children)
            await self.call(client, "guest_exec", **run)
            self.assertEqual(len(self.vm.children), count)
            log = await self.call(
                client, "task_log", task_id="desktop", command_id="assert", limit=9
            )
            rest = await self.call(
                client,
                "task_log",
                task_id="desktop",
                command_id="assert",
                offset=log["next_offset"],
            )
            self.assertEqual(log["text"] + rest["text"], "assertion output\n")
            shot = await self.call(client, "screenshot", task_id="desktop")
            self.assertTrue(
                (await http.get(shot["url"])).content.startswith(b"\x89PNG")
            )
            private = self.root / "private.txt"
            private.write_text("synthetic private content")

            async def linked_screenshot(destination, screen=0):
                Path(destination).symlink_to(private)

            with patch.object(self.vm, "screenshot", side_effect=linked_screenshot):
                self.assertTrue(
                    (
                        await client.call_tool("screenshot", {"task_id": "desktop"})
                    ).is_error
                )
            self.assertEqual(private.read_text(), "synthetic private content")
            self.assertEqual((await http.get(shot["url"])).content, PNG)
            await self.call(
                client,
                "guest_collect",
                task_id="desktop",
                request_id="reports",
                guest_path=self.vm.guest_path("desktop", "report.txt"),
            )
            task = await self.wait_state(client, "desktop", "ready")
            self.assertTrue(
                (
                    await http.get(task["commands"]["reports"]["artifact"]["url"])
                ).content.startswith(b"PK")
            )
            await self.call(client, "task_finish", task_id="desktop")
            self.assertTrue(
                (await self.wait_state(client, "desktop", "failed"))[
                    "cleanup_confirmed"
                ]
            )
            self.assertEqual(
                len([call for call in self.vm.calls if call[0] == "snapshot"]), 1
            )

    async def test_disconnect_timeout_and_controller_recovery(self):
        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                request_id="recover",
                target="windows",
            )
            await self.wait_state(client, "recover", "ready")
            await self.call(
                client,
                "guest_exec",
                task_id="recover",
                request_id="long",
                argv=[
                    sys.executable,
                    "-c",
                    "import time; print('started', flush=True); time.sleep(30)",
                ],
                timeout=0.7,
            )
            async with asyncio.timeout(3):
                while not self.controller.task("recover")["commands"]["long"].get(
                    "guest_session"
                ):
                    await asyncio.sleep(0.01)
            self.assertEqual(len(self.vm.sessions), 1)
        await self.controller.close()
        replacement = Controller(self.config, {"windows": self.vm})
        await replacement.start()
        try:
            async with asyncio.timeout(5):
                while replacement.task("recover")["state"] != "ready":
                    await asyncio.sleep(0.03)
            command = replacement.task("recover")["commands"]["long"]
            self.assertEqual(command["reason"], "timeout")
            self.assertTrue(command["cleanup_confirmed"])
            self.assertEqual(
                replacement.status("recover")["outcome"]["command_failure_kinds"],
                {"timeout": 1},
            )
            self.assertIsNotNone(self.vm.children[0].returncode)
            self.assertEqual(self.vm.sessions, {})
        finally:
            await replacement.close()

    async def test_completed_commands_release_only_their_guest_sessions(self):
        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="sessions",
            )
            await self.wait_state(client, "sessions", "ready")
            self.controller.task("sessions")["deadline"] = time.time() + 120
            other = {"name": "administrator diagnostic session", "pid": 999}
            self.vm.sessions[999] = other
            for index in range(35):
                await self.call(
                    client,
                    "guest_exec",
                    task_id="sessions",
                    request_id=f"command-{index}",
                    argv=[sys.executable, "-c", "print('complete')"],
                )
                result = await self.wait_state(client, "sessions", "ready")
                self.assertEqual(result["commands"][f"command-{index}"]["exit_code"], 0)
                self.assertTrue(
                    result["commands"][f"command-{index}"]["cleanup_confirmed"]
                )
                self.assertEqual(self.vm.sessions, {999: other})
            await self.call(client, "task_finish", task_id="sessions")
            result = await self.wait_state(client, "sessions", "succeeded")
            self.assertTrue(result["cleanup_confirmed"])

    async def test_controller_recovers_an_interrupted_session_close(self):
        closing = asyncio.Event()
        original = self.vm.close_guest_session

        async def interrupted(owned):
            await original(owned)
            closing.set()
            await asyncio.Future()

        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="session-close",
            )
            await self.wait_state(client, "session-close", "ready")
            with patch.object(self.vm, "close_guest_session", side_effect=interrupted):
                await self.call(
                    client,
                    "guest_exec",
                    task_id="session-close",
                    request_id="one",
                    argv=[sys.executable, "-c", "print('complete')"],
                )
                await asyncio.wait_for(closing.wait(), 5)
                status = self.controller.status("session-close", "one")
                self.assertEqual(status["command"]["state"], "finished")
                self.assertFalse(status["command"]["cleanup_confirmed"])
                self.assertEqual(status["state"], "executing")
                await self.controller.close()
        replacement = Controller(self.config, {"windows": self.vm})
        await replacement.start()
        try:
            async with asyncio.timeout(5):
                while replacement.task("session-close")["state"] != "ready":
                    await asyncio.sleep(0.03)
            self.assertTrue(
                replacement.status("session-close", "one")["command"][
                    "cleanup_confirmed"
                ]
            )
            self.assertEqual(self.vm.sessions, {})
        finally:
            await replacement.close()

    async def test_changed_session_identity_requires_vm_cleanup(self):
        original = self.vm.close_guest_session

        async def replaced(owned):
            self.vm.sessions[owned["id"]]["name"] = "another session"
            await original(owned)

        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="changed-session",
            )
            await self.wait_state(client, "changed-session", "ready")
            with patch.object(self.vm, "close_guest_session", side_effect=replaced):
                await self.call(
                    client,
                    "guest_exec",
                    task_id="changed-session",
                    request_id="one",
                    argv=[sys.executable, "-c", "print('complete')"],
                )
                result = await self.wait_state(
                    client, "changed-session", "cleanup_unknown"
                )
            self.assertEqual(len(self.vm.sessions), 1)
            self.assertFalse(result["commands"]["one"]["cleanup_confirmed"])
            self.assertIn("identity changed", result["outcome"]["failure"]["message"])
            await self.call(client, "task_finish", task_id="changed-session")
            result = await self.wait_state(client, "changed-session", "failed")
            self.assertTrue(result["cleanup_confirmed"])
            self.assertEqual(self.vm.sessions, {})

    async def test_unconfirmed_poweroff_retains_queue_and_test_outcome(self):
        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                request_id="stuck",
                target="windows",
            )
            await self.wait_state(client, "stuck", "ready")
            await self.call(
                client,
                "guest_exec",
                task_id="stuck",
                request_id="failure",
                argv=[sys.executable, "-c", "raise SystemExit(7)"],
            )
            await self.wait_state(client, "stuck", "ready")
            self.vm.shutdown_hangs = True
            self.vm.poweroff_hangs = True
            await self.call(
                client,
                "task_begin",
                kind="windows",
                request_id="next",
                target="windows",
            )
            await self.call(client, "task_finish", task_id="stuck")
            unresolved = await self.wait_state(client, "stuck", "cleanup_unknown")
            self.assertEqual(
                unresolved["outcome"]["cleanup_error"]["phase"], "shutdown"
            )
            self.assertIsNone(unresolved["outcome"]["failure"])
            self.assertEqual(
                (await self.call(client, "task_status", task_id="next"))["state"],
                "queued",
            )
            self.vm.poweroff_hangs = False
            stopping, release = asyncio.Event(), asyncio.Event()
            original = self.vm.power

            async def hold_poweroff(action):
                if action == "poweroff":
                    stopping.set()
                    await release.wait()
                return await original(action)

            with patch.object(self.vm, "power", side_effect=hold_poweroff):
                recovery = asyncio.create_task(
                    self.call(client, "vm_power", target="windows", action="shutdown")
                )
                await asyncio.wait_for(stopping.wait(), 3)
                repeated_finish = asyncio.create_task(
                    self.call(client, "task_finish", task_id="stuck")
                )
                try:
                    await asyncio.wait({repeated_finish}, timeout=0.05)
                    self.assertEqual(
                        self.controller.task("stuck")["state"], "cleanup_unknown"
                    )
                finally:
                    release.set()
                    recovered_power, _ = await asyncio.gather(recovery, repeated_finish)
            self.assertEqual(
                [attempt["action"] for attempt in recovered_power["attempts"]],
                ["shutdown", "poweroff"],
            )
            recovered = await self.call(client, "task_status", task_id="stuck")
            self.assertEqual(recovered["state"], "failed")
            self.assertEqual(
                recovered["outcome"]["command_failure_kinds"], {"exit_code": 1}
            )
            self.assertTrue(recovered["cleanup_confirmed"])
            self.assertIsNone(recovered["outcome"]["cleanup_error"])
            await self.wait_state(client, "next", "ready")
            await self.call(client, "task_cancel", task_id="next")
            self.assertTrue(
                (await self.wait_state(client, "next", "cancelled"))[
                    "cleanup_confirmed"
                ]
            )

    async def test_aborted_vm_releases_queue_and_retains_failed_outcome(self):
        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                request_id="aborted",
                target="windows",
            )
            await self.wait_state(client, "aborted", "ready")
            self.vm.poweroff_hangs = True
            await self.call(client, "task_finish", task_id="aborted")
            await self.wait_state(client, "aborted", "cleanup_unknown")
            await self.call(
                client,
                "task_begin",
                kind="windows",
                request_id="after-abort",
                target="windows",
            )
            self.vm.config["VMState"] = "aborted"
            exact = await self.call(
                client,
                "wait_for_state",
                target="windows",
                state="poweroff",
                timeout=0.01,
            )
            self.assertFalse(exact["reached"])
            self.assertEqual(exact["observed_state"], "aborted")
            reply = await self.call(
                client, "vm_power", target="windows", action="poweroff"
            )
            self.assertEqual(reply["attempts"], [])
            self.assertTrue(reply["observation"]["reached"])
            self.assertEqual(reply["observation"]["observed_state"], "aborted")
            final = await self.call(client, "task_status", task_id="aborted")
            self.assertEqual(final["state"], "failed")
            self.assertTrue(final["cleanup_confirmed"])
            self.assertEqual(final["outcome"]["failure"]["phase"], "vm_runtime")
            self.assertIsNone(final["outcome"]["cleanup_error"])
            await self.wait_state(client, "after-abort", "ready")
            self.vm.poweroff_hangs = False
            await self.call(client, "task_finish", task_id="after-abort")
            await self.wait_state(client, "after-abort", "succeeded")

    async def test_shutdown_observes_failures_and_uses_bounded_poweroff_fallback(self):
        async with self.client() as client:
            original = self.vm.command

            async def unavailable(arguments, **kwargs):
                if arguments[:3] == ["controlvm", UUID, "shutdown"]:
                    self.vm.calls.append(arguments)
                    raise Failure("Guest session limit reached")
                return await original(arguments, **kwargs)

            self.vm.config["VMState"] = "running"
            with patch.object(self.vm, "command", side_effect=unavailable):
                reply = await self.call(
                    client, "vm_power", target="windows", action="shutdown"
                )
            self.assertEqual(
                [a["action"] for a in reply["attempts"]], ["shutdown", "poweroff"]
            )
            self.assertIn("Guest session limit", reply["graceful_error"])
            self.assertTrue(reply["observation"]["reached"])

            self.vm.config["VMState"] = "running"
            self.vm.shutdown_hangs = True
            reply = await self.call(
                client, "vm_power", target="windows", action="shutdown"
            )
            self.assertEqual(
                [a["action"] for a in reply["attempts"]], ["shutdown", "poweroff"]
            )
            self.assertIn("timeout", reply["graceful_error"])

            self.vm.config["VMState"] = "running"
            self.vm.shutdown_hangs = False

            async def interrupted(arguments, **kwargs):
                output = await original(arguments, **kwargs)
                if arguments[:3] == ["controlvm", UUID, "shutdown"]:
                    raise Failure("CLI interrupted after shutdown")
                return output

            with patch.object(self.vm, "command", side_effect=interrupted):
                reply = await self.call(
                    client, "vm_power", target="windows", action="shutdown"
                )
            self.assertEqual([a["action"] for a in reply["attempts"]], ["shutdown"])
            self.assertFalse(reply["attempts"][0]["command_succeeded"])
            self.assertTrue(reply["observation"]["reached"])
            for action in ("shutdown", "poweroff"):
                reply = await self.call(
                    client, "vm_power", target="windows", action=action
                )
                self.assertEqual(reply["attempts"], [])
                self.assertTrue(reply["observation"]["reached"])

            async def abort_during_shutdown(action):
                self.assertEqual(action, "shutdown")
                self.vm.config["VMState"] = "aborted"

            self.vm.config["VMState"] = "running"
            with patch.object(self.vm, "power", side_effect=abort_during_shutdown):
                reply = await self.call(
                    client, "vm_power", target="windows", action="shutdown"
                )
            self.assertEqual([a["action"] for a in reply["attempts"]], ["shutdown"])
            self.assertTrue(reply["observation"]["reached"])
            self.assertEqual(reply["observation"]["observed_state"], "aborted")

    async def test_task_cleanup_bounds_evidence_and_does_not_need_guest_control(self):
        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="stop",
            )
            await self.wait_state(client, "stop", "ready")
            await self.call(
                client,
                "guest_exec",
                task_id="stop",
                request_id="driver",
                argv=[sys.executable, "-c", "import time; time.sleep(30)"],
            )
            async with asyncio.timeout(3):
                while not self.vm.children:
                    await asyncio.sleep(0.01)
            self.vm.shutdown_hangs = True
            self.config["service"]["command_timeout"] = 0.05

            async def stuck_screenshot(_destination, screen=0):
                await asyncio.Event().wait()

            with (
                patch.object(self.vm, "screenshot", side_effect=stuck_screenshot),
                patch.object(
                    self.controller,
                    "event",
                    side_effect=OSError("Log storage unavailable"),
                ),
                patch.object(
                    self.vm,
                    "copy_to",
                    side_effect=Failure("Guest session limit reached"),
                ) as copy,
            ):
                await self.call(client, "task_cancel", task_id="stop")
                final = await self.wait_state(client, "stop", "cancelled")
            copy.assert_not_awaited()
            self.assertTrue(final["cleanup_confirmed"])
            self.assertEqual(final["commands"]["driver"]["reason"], "cancelled")
            self.assertIsNotNone(self.vm.children[0].returncode)
            self.assertEqual(
                [call for call in self.vm.calls if call[0] == "controlvm"],
                [["controlvm", UUID, "poweroff"]],
            )

    async def test_baseline_start_and_queued_cancellation_respect_task_ownership(self):
        self.vm.profile["interruptible"] = False
        self.vm.config["cpus"] = "8"
        async with self.client() as client:
            await self.call(
                client, "modify_vm", target="windows", changes={"vram_mb": 128}
            )
            self.assertEqual(self.vm.config["cpus"], "8")
            await self.call(client, "vm_power", target="windows", action="start")
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="baseline",
            )
            await self.wait_state(client, "baseline", "ready")
            self.assertEqual(self.vm.config["cpus"], "8")
            transitions = [
                c[0] if c[0] != "controlvm" else c[2]
                for c in self.vm.calls
                if c[0] in {"startvm", "snapshot", "controlvm"}
            ]
            self.assertEqual(
                transitions, ["startvm", "poweroff", "snapshot", "startvm"]
            )
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="queued",
            )
            before = len(self.vm.calls)
            await self.call(client, "task_cancel", task_id="queued")
            queued = await self.wait_state(client, "queued", "cancelled")
            self.assertTrue(queued["cleanup_confirmed"])
            for tool, args in (
                ("vm_power", {"action": "poweroff"}),
                ("modify_vm", {"changes": {"cpus": 2}}),
            ):
                denied = await client.call_tool(tool, {"target": "windows", **args})
                self.assertTrue(denied.is_error)
            self.assertEqual(len(self.vm.calls), before)
            self.assertEqual(self.vm.config["VMState"], "running")
            await self.call(client, "task_finish", task_id="baseline")
            self.assertTrue(
                (await self.wait_state(client, "baseline", "succeeded"))[
                    "cleanup_confirmed"
                ]
            )

    async def test_quiesce_respects_idle_policy_and_unregistered_vms(self):
        self.vm.config["VMState"] = "running"
        self.vm.profile["interruptible"] = False
        with self.assertRaisesRegex(Failure, "not idle-interruptible"):
            await self.controller.quiesce()
        self.assertEqual(self.vm.config["VMState"], "running")
        self.vm.profile["interruptible"] = True
        with patch.object(self.vm, "running_vms", return_value=[UUID, "unregistered"]):
            with self.assertRaisesRegex(Failure, "unregistered"):
                await self.controller.quiesce()
        self.assertEqual(self.vm.config["VMState"], "running")
        self.vm.shutdown_hangs = True
        await self.controller.quiesce()
        self.assertEqual(
            [call for call in self.vm.calls if call[0] == "controlvm"],
            [["controlvm", UUID, "shutdown"], ["controlvm", UUID, "poweroff"]],
        )
        self.assertEqual(self.vm.config["VMState"], "poweroff")

        self.vm.config["VMState"] = "aborted"
        self.vm.profile["interruptible"] = False
        await self.controller.quiesce()
        self.assertEqual(self.vm.config["VMState"], "aborted")

    async def test_compact_status_paged_history_and_discoverable_limits(self):
        async with self.client() as client:
            limits = (await self.call(client, "targets"))["limits"]
            invalid = await client.call_tool(
                "task_begin",
                {
                    "kind": "windows",
                    "target": "windows",
                    "request_id": "oversized",
                    "timeout": 10800,
                },
            )
            self.assertTrue(invalid.is_error)
            self.assertEqual(
                invalid.structured_content["error"]["details"]["maximum"],
                limits["windows_task_seconds"],
            )
            self.assertNotIn("oversized", self.controller.tasks)
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="history",
            )
            await self.wait_state(client, "history", "ready")
            task = self.controller.task("history")
            measurements = []
            self.status_wire_sizes = []
            for count in (1, 21, 100):
                task["commands"] = {
                    f"cmd-{i:03}": {
                        "id": f"cmd-{i:03}",
                        "request": {"argv": ["test", "x" * 2000]},
                        "state": "finished",
                        "exit_code": 1 if i % 2 else 0,
                        "reason": "exited",
                        "cleanup_confirmed": True,
                    }
                    for i in range(count)
                }
                task["artifacts"] = [
                    {"id": f"artifact-{i:03}", "kind": "artifact"} for i in range(count)
                ]
                reply = await client.call_tool("task_status", {"task_id": "history"})
                status = reply.structured_content
                self.assertEqual(status["command_count"], count)
                self.assertEqual(status["outcome"]["failed_command_count"], count // 2)
                self.assertLessEqual(len(status["outcome"]["failed_command_ids"]), 5)
                self.assertNotIn("commands", status)
                self.assertNotIn("artifacts", status)
                measurements.append(
                    (
                        count,
                        len(json.dumps(status, separators=(",", ":")).encode()),
                        len(reply.model_dump_json(by_alias=True).encode()),
                    )
                )
            self.assertLess(max(n for _, n, _ in measurements), 1600)
            self.assertLess(max(n for _, _, n in measurements), 3600)
            self.assertEqual(len(self.status_wire_sizes), 3)
            self.assertLess(max(self.status_wire_sizes), 3600)
            print(
                "status bytes (commands, structured UTF-8, MCP result UTF-8):",
                measurements,
            )
            print("status HTTP JSON-RPC response body bytes:", self.status_wire_sizes)
            commands, artifacts = [], []
            for name, output in (("task_commands", commands), ("artifacts", artifacts)):
                offset = 0
                while offset is not None:
                    result = await self.call(
                        client, name, task_id="history", offset=offset, limit=17
                    )
                    self.assertEqual(result["total"], 100)
                    output.extend(result["items"])
                    offset = result["next_offset"]
            self.assertEqual([c["id"] for c in commands], list(task["commands"]))
            self.assertEqual(len({a["id"] for a in artifacts}), 100)
            selected = await self.call(
                client, "task_status", task_id="history", command_id="cmd-099"
            )
            self.assertEqual(
                selected["command"]["exit_code"], commands[-1]["exit_code"]
            )
            await self.call(client, "task_finish", task_id="history")
            final = await self.wait_state(client, "history", "failed")
            self.assertTrue(final["cleanup_confirmed"])
            self.assertIsNone(final["outcome"]["failure"])

    async def test_interactive_input_upload_and_reconnect(self):
        async with self.client() as client, httpx2.AsyncClient(trust_env=False) as http:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="interactive",
            )
            await self.wait_state(client, "interactive", "ready")
            await self.call(
                client,
                "guest_exec",
                task_id="interactive",
                request_id="driver",
                stdin=True,
                argv=[
                    sys.executable,
                    "-u",
                    "-c",
                    "import sys; total=0\nfor line in sys.stdin:\n total+=int(line); print(total,flush=True)\nprint('eof',flush=True)",
                ],
            )
            for tool, parameters in (
                (
                    "guest_exec",
                    {
                        "request_id": "second",
                        "argv": [sys.executable, "-c", "print(1)"],
                    },
                ),
                (
                    "guest_collect",
                    {
                        "request_id": "collect",
                        "guest_path": self.vm.guest_path("interactive"),
                    },
                ),
            ):
                reply = await client.call_tool(
                    tool, {"task_id": "interactive", **parameters}
                )
                self.assertEqual(reply.structured_content["error"]["code"], "task_busy")
                self.assertEqual(
                    reply.structured_content["error"]["details"]["current_command_id"],
                    "driver",
                )
            transfer = await self.call(client, "transfer_create")
            await http.put(transfer["url"], content=b"input while running")
            destination = self.vm.guest_path("interactive", "new", "nested", "data.txt")
            await self.call(
                client,
                "guest_upload",
                task_id="interactive",
                transfer_id=transfer["id"],
                destination=destination,
            )
            self.assertEqual(
                self.vm.local(destination).read_bytes(), b"input while running"
            )
            await self.call(
                client,
                "guest_upload",
                task_id="interactive",
                transfer_id=transfer["id"],
                destination=destination,
            )
            with patch.object(self.vm, "copy_to", side_effect=Failure("copy failed")):
                failed = await client.call_tool(
                    "guest_upload",
                    {
                        "task_id": "interactive",
                        "transfer_id": transfer["id"],
                        "destination": destination,
                    },
                )
                self.assertEqual(
                    failed.structured_content["error"]["code"], "guest_upload_failed"
                )
            await self.call(client, "screenshot", task_id="interactive")
            args = {
                "task_id": "interactive",
                "command_id": "driver",
                "request_id": "first",
                "text": "2\n",
            }
            publishing, release = asyncio.Event(), asyncio.Event()
            original_copy = self.vm.copy_to

            async def delayed_input(source, destination):
                await original_copy(source, destination)
                if destination.endswith("input-00000001.json.upload"):
                    publishing.set()
                    await release.wait()

            patcher = patch.object(self.vm, "copy_to", side_effect=delayed_input)
            patcher.start()
            self.addCleanup(patcher.stop)
            first = await self.call(client, "guest_input", **args)
            self.assertEqual(first["sequence"], 1)
            self.assertEqual(
                (await self.call(client, "guest_input", **args))["sequence"], 1
            )
            conflict = await client.call_tool("guest_input", args | {"text": "9\n"})
            self.assertEqual(
                conflict.structured_content["error"]["code"], "request_conflict"
            )
        async with self.client() as client:
            await asyncio.wait_for(publishing.wait(), 5)
            await self.call(
                client,
                "guest_input",
                task_id="interactive",
                command_id="driver",
                request_id="second",
                text="3\n",
            )
            await self.call(
                client,
                "guest_input",
                task_id="interactive",
                command_id="driver",
                request_id="close",
                eof=True,
            )
            release.set()
            final = await self.wait_state(client, "interactive", "ready")
            status = final["commands"]["driver"]
            self.assertEqual(status["exit_code"], 0)
            self.assertEqual(status["stdin"]["written_sequence"], 3)
            self.assertTrue(status["stdin"]["closed"])
            self.assertEqual(
                (await self.call(client, "guest_input", **args))["state"], "written"
            )
            log = await self.call(
                client, "task_log", task_id="interactive", command_id="driver"
            )
            self.assertEqual(log["text"], "2\n5\neof\n")
            closed = await client.call_tool(
                "guest_input", args | {"request_id": "after-exit"}
            )
            self.assertEqual(closed.structured_content["error"]["code"], "stdin_closed")
            await self.call(client, "task_finish", task_id="interactive")
            await self.wait_state(client, "interactive", "succeeded")

    async def test_input_recovery_and_blocked_reader_cleanup(self):
        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="stdin-recovery",
            )
            await self.wait_state(client, "stdin-recovery", "ready")
            await self.call(
                client,
                "guest_exec",
                task_id="stdin-recovery",
                request_id="reader",
                stdin=True,
                argv=[
                    sys.executable,
                    "-u",
                    "-c",
                    "import sys; print(sys.stdin.read(),end='')",
                ],
            )
        async with asyncio.timeout(5):
            while not self.vm.children:
                await asyncio.sleep(0.02)
        await self.controller.close()
        replacement = Controller(self.config, {"windows": self.vm})
        await replacement.start()
        try:
            await replacement.input("stdin-recovery", "reader", "text", "retained\n")
            await replacement.input("stdin-recovery", "reader", "end", "", eof=True)
            async with asyncio.timeout(6):
                while replacement.task("stdin-recovery")["state"] != "ready":
                    await asyncio.sleep(0.03)
            self.assertEqual(
                (await replacement.task_log("stdin-recovery", "reader"))["text"],
                "retained\n",
            )
            await replacement.exec_guest(
                "stdin-recovery",
                "blocked",
                [sys.executable, "-c", "import time; time.sleep(30)"],
                timeout=1.5,
                stdin=True,
            )
            for i in range(4):
                await replacement.input(
                    "stdin-recovery", "blocked", f"chunk-{i}", "x" * 16384
                )
            with self.assertRaises(Failure) as full:
                await replacement.input("stdin-recovery", "blocked", "full", "x")
            self.assertEqual(full.exception.code, "out_of_range")
            async with asyncio.timeout(7):
                while replacement.task("stdin-recovery")["state"] != "ready":
                    await asyncio.sleep(0.03)
            status = replacement.status("stdin-recovery", "blocked")["command"]
            self.assertEqual(status["reason"], "timeout")
            self.assertTrue(status["cleanup_confirmed"])
            self.assertTrue(status["stdin"]["closed"])
            await replacement.exec_guest(
                "stdin-recovery",
                "cancelled",
                [sys.executable, "-c", "import time; time.sleep(30)"],
                stdin=True,
            )
            await replacement.finish("stdin-recovery", cancel=True)
            async with asyncio.timeout(5):
                while not replacement.task("stdin-recovery")["cleanup_confirmed"]:
                    await asyncio.sleep(0.03)
            status = replacement.status("stdin-recovery", "cancelled")
            self.assertEqual(status["outcome"]["stop_reason"], "cancelled")
            self.assertEqual(status["command"]["reason"], "cancelled")
            self.assertTrue(status["command"]["cleanup_confirmed"])
        finally:
            await replacement.close()

    async def test_input_delivery_results_include_unwritten_messages(self):
        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="delivery",
            )
            await self.wait_state(client, "delivery", "ready")
            original = Path.read_text
            for mode in ("temporary", "permanent", "early-exit", "eof-only"):
                with self.subTest(mode=mode):
                    attempts = 0

                    def read(path, *args, **kwargs):
                        nonlocal attempts
                        if (
                            path.name == "input-00000001.json"
                            and self.vm.guest_root in path.parents
                        ):
                            attempts += 1
                            if mode == "permanent" or (
                                mode == "temporary" and attempts <= 2
                            ):
                                raise PermissionError(
                                    "synthetic input sharing conflict"
                                )
                        return original(path, *args, **kwargs)

                    early = mode in {"early-exit", "eof-only"}
                    with patch.object(Path, "read_text", read):
                        # Accept while starting, before launch has a chance to run.
                        await self.controller.exec_guest(
                            "delivery",
                            mode,
                            [
                                sys.executable,
                                "-u",
                                "-c",
                                "print('exited')"
                                if early
                                else "import sys; sys.stdout.buffer.write(sys.stdin.buffer.read())",
                            ],
                            stdin=True,
                        )
                        receipt = await self.controller.input(
                            "delivery",
                            mode,
                            "first",
                            "" if mode == "eof-only" else "消息1 é\n",
                            eof=True,
                        )
                        self.assertEqual(receipt["state"], "accepted")
                        final = await self.wait_state(client, "delivery", "ready")
                    command = final["commands"][mode]
                    self.assertEqual(command["exit_code"], 0)
                    self.assertTrue(command["cleanup_confirmed"])
                    self.assertEqual(command["stdin"]["accepted_sequence"], 1)
                    self.assertTrue(command["stdin"]["closed"])
                    delivered = mode == "temporary"
                    self.assertEqual(
                        command["stdin"]["written_sequence"], int(delivered)
                    )
                    self.assertEqual(
                        command["failure_kind"], None if delivered else "stdin"
                    )
                    self.assertEqual(
                        command["stdin"]["pending_bytes"],
                        0
                        if delivered or mode == "eof-only"
                        else len("消息1 é\n".encode()),
                    )
                    retry = await self.call(
                        client,
                        "guest_input",
                        task_id="delivery",
                        command_id=mode,
                        request_id="first",
                        text="" if mode == "eof-only" else "消息1 é\n",
                        eof=True,
                    )
                    self.assertEqual(
                        retry["state"], "written" if delivered else "unconfirmed"
                    )
                    log = await self.call(
                        client, "task_log", task_id="delivery", command_id=mode
                    )
                    self.assertEqual(
                        log["text"],
                        "消息1 é\n" if delivered else "exited\n" if early else "",
                    )
                    if not delivered:
                        self.assertTrue(command["stdin"]["error"])
            await self.call(client, "task_finish", task_id="delivery")
            final = await self.wait_state(client, "delivery", "failed")
            self.assertTrue(final["cleanup_confirmed"])
            self.assertEqual(final["outcome"]["failed_command_count"], 3)
            self.assertEqual(final["outcome"]["command_failure_kinds"], {"stdin": 3})

    async def test_task_deadline_keeps_its_reason_after_cleanup(self):
        async with self.client() as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="deadline",
                timeout=1.5,
            )
            await self.wait_state(client, "deadline", "ready")
            final = await self.wait_state(client, "deadline", "failed")
            self.assertTrue(final["cleanup_confirmed"])
            self.assertEqual(final["outcome"]["stop_reason"], "task_timeout")
            self.assertIsNone(final["outcome"]["failure"])
            self.assertIsNone(final["outcome"]["cleanup_error"])

    async def test_webcam_policy_device_selection_and_readback(self):
        async with self.client() as client:
            catalog = await self.call(client, "webcams", target="windows")
            camera = catalog["available"][0]
            self.assertEqual(camera["name"], 'Camera "A"')
            self.assertEqual(catalog["attached"], [])
            await self.call(
                client,
                "task_begin",
                kind="windows",
                target="windows",
                request_id="camera",
            )
            await self.wait_state(client, "camera", "ready")
            await self.call(
                client,
                "guest_exec",
                task_id="camera",
                request_id="driver",
                argv=[sys.executable, "-c", "import time; time.sleep(30)"],
            )
            args = {"target": "windows", "action": "attach", "camera_id": camera["id"]}
            self.vm.profile["webcam"]["enabled"] = False
            denied = await client.call_tool("vm_webcam", args)
            self.assertEqual(
                denied.structured_content["error"]["code"], "policy_denied"
            )
            self.assertEqual(self.vm.attached_webcams, [])
            self.vm.profile["webcam"]["enabled"] = True
            invalid = await client.call_tool(
                "vm_webcam", args | {"camera_id": "/etc/passwd"}
            )
            self.assertEqual(
                invalid.structured_content["error"]["code"], "unknown_webcam"
            )
            attached = await self.call(client, "vm_webcam", **args)
            self.assertTrue(attached["applied"])
            self.assertTrue(attached["observed_attached"])
            self.assertIn(
                ["controlvm", UUID, "webcam", "attach", "/dev/video0"], self.vm.calls
            )
            self.vm.profile["webcam"]["enabled"] = False
            denied = await client.call_tool("vm_webcam", args | {"action": "detach"})
            self.assertEqual(
                denied.structured_content["error"]["code"], "policy_denied"
            )
            self.assertEqual(self.vm.attached_webcams, ["/dev/video0"])
            self.vm.profile["webcam"]["enabled"] = True
            # A physically unplugged device can remain attached in VirtualBox.
            self.vm.host_webcams.clear()
            catalog = await self.call(client, "webcams", target="windows")
            self.assertEqual(catalog["available"], [])
            self.assertEqual(catalog["attached"][0]["id"], camera["id"])
            detached = await self.call(
                client, "vm_webcam", **(args | {"action": "detach"})
            )
            self.assertTrue(detached["applied"])
            self.assertFalse(detached["observed_attached"])

            self.vm.host_webcams = [(".2", 'Camera "A"', "/dev/video0")]
            original = self.vm.command

            async def interrupted(arguments, **kwargs):
                output = await original(arguments, **kwargs)
                if arguments[2:4] == ["webcam", "attach"]:
                    raise Failure("CLI interrupted after attaching")
                return output

            with patch.object(self.vm, "command", side_effect=interrupted):
                attached = await self.call(client, "vm_webcam", **args)
            self.assertTrue(attached["applied"])
            self.assertFalse(attached["command_succeeded"])
            self.assertEqual(len(self.vm.attached_webcams), 1)
            self.assertIn("after attaching", attached["error"])
            await self.call(client, "vm_webcam", **(args | {"action": "detach"}))
            await self.call(client, "task_cancel", task_id="camera")
            final = await self.wait_state(client, "camera", "cancelled")
            self.assertTrue(final["cleanup_confirmed"])

    async def test_typed_configuration_policy_and_readback(self):
        async with self.client() as client:
            target = (await self.call(client, "targets"))["vms"][0]
            self.assertTrue(target["webcam"]["enabled"])
            self.assertEqual(
                target["modify_vm"]["cpus"], {"enabled": True, "min": 1, "max": 4}
            )
            self.assertEqual(target["modify_vm"]["memory_mb"]["max"], 16384)
            reply = await self.call(
                client, "modify_vm", target="windows", changes={"cpus": 2}
            )
            self.assertEqual(reply["changes"]["cpus"], {"before": "4", "after": "2"})
            before = len(self.vm.calls)
            invalid = await client.call_tool(
                "modify_vm",
                {
                    "target": "windows",
                    "changes": {"cpus": 3, "uart_mode": "/host/file"},
                },
            )
            self.assertTrue(invalid.is_error)
            self.assertEqual(len(self.vm.calls), before)

            for changes, field in (
                ({"cpus": 3, "memory_mb": 16385}, "memory_mb"),
                ({"cpus": 5}, "cpus"),
            ):
                denied = await client.call_tool(
                    "modify_vm", {"target": "windows", "changes": changes}
                )
                self.assertEqual(
                    denied.structured_content["error"]["code"], "policy_denied"
                )
                self.assertEqual(
                    denied.structured_content["error"]["details"]["field"], field
                )
                self.assertEqual(len(self.vm.calls), before)
                self.assertEqual(self.vm.config["cpus"], "2")

            original = self.vm.command

            async def partial_change(arguments, **kwargs):
                response = await original(arguments, **kwargs)
                if arguments[0] == "modifyvm":
                    raise Failure("synthetic failure after applying the setting")
                return response

            with patch.object(self.vm, "command", side_effect=partial_change):
                reply = await self.call(
                    client, "modify_vm", target="windows", changes={"cpus": 3}
                )
            self.assertIsNone(reply["applied"])
            self.assertEqual(reply["changes"]["cpus"], {"before": "2", "after": "3"})
            self.assertIn("after applying", reply["error"])

            await self.call(
                client,
                "modify_vm",
                target="windows",
                changes={
                    "cpus": 4,
                    "memory_mb": 16384,
                    "vram_mb": 256,
                    "graphics_controller": "vboxsvga",
                    "accelerate_3d": True,
                    "paravirt_provider": "hyperv",
                    "nested_paging": True,
                    "vpid": False,
                },
            )
            self.assertIn(
                [
                    "modifyvm",
                    UUID,
                    "--cpus",
                    "4",
                    "--memory",
                    "16384",
                    "--vram",
                    "256",
                    "--graphicscontroller",
                    "vboxsvga",
                    "--accelerate-3d",
                    "on",
                    "--paravirt-provider",
                    "hyperv",
                    "--nested-paging",
                    "on",
                    "--x86-vtx-vpid",
                    "off",
                ],
                self.vm.calls,
            )

    async def test_independent_vm_policies_and_field_restrictions(self):
        directory = self.root / "other"
        directory.mkdir()
        other = RecordingVM(directory, "22222222-2222-4333-8444-555555555555")
        other.profile.update(
            vm_policy(
                {
                    "modify_vm": {
                        "cpus": {"min": 2, "max": 3},
                        "memory_mb": {"enabled": False},
                        "accelerate_3d": {"allowed": [False]},
                        "graphics_controller": {"allowed": ["vboxsvga"]},
                        "vpid": {"allowed": []},
                    },
                    "webcam": {"enabled": False},
                },
                "vms.other",
            )
        )
        self.controller.vms["other"] = other
        async with self.client() as client:
            targets = {
                vm["id"]: vm for vm in (await self.call(client, "targets"))["vms"]
            }
            self.assertEqual(targets["windows"]["modify_vm"]["cpus"]["max"], 4)
            self.assertEqual(targets["other"]["modify_vm"]["cpus"]["max"], 3)
            self.assertFalse(targets["other"]["webcam"]["enabled"])
            before_denials = list(other.calls)
            for changes in (
                {"cpus": 1},
                {"cpus": 4},
                {"cpus": 2, "memory_mb": 1024},
                {"accelerate_3d": True},
                {"graphics_controller": "none"},
                {"vpid": False},
            ):
                denied = await client.call_tool(
                    "modify_vm", {"target": "other", "changes": changes}
                )
                self.assertEqual(
                    denied.structured_content["error"]["code"], "policy_denied"
                )
            self.assertEqual(other.calls, before_denials)
            await self.call(
                client,
                "modify_vm",
                target="other",
                changes={
                    "cpus": 2,
                    "accelerate_3d": False,
                    "graphics_controller": "vboxsvga",
                },
            )
            self.assertEqual(other.config["cpus"], "2")
            await self.call(client, "modify_vm", target="other", changes={"cpus": 3})
            self.assertEqual(other.config["cpus"], "3")
            other.profile["modify_vm"]["enabled"] = False
            before = len(other.calls)
            denied = await client.call_tool(
                "modify_vm", {"target": "other", "changes": {"cpus": 2}}
            )
            self.assertEqual(
                denied.structured_content["error"]["code"], "policy_denied"
            )
            self.assertEqual(len(other.calls), before)
            await self.call(client, "modify_vm", target="windows", changes={"cpus": 4})
            self.assertEqual(self.vm.config["cpus"], "4")

    async def test_http_auth_cache_boundary_and_log_rotation(self):
        async with httpx2.AsyncClient(trust_env=False) as http, self.client() as client:
            self.assertEqual(
                (await http.post(self.origin + "/mcp", json={})).status_code, 401
            )
            self.config["service"]["token"] = ""
            self.assertNotEqual((await http.get(self.origin + "/mcp")).status_code, 401)
            self.config["service"]["token"] = TOKEN
            cache = await self.call(client, "transfer_create", kind="cache")
            self.assertIn(
                b"StoreDir: /nix/store",
                (await http.get(cache["url"] + "/nix-cache-info")).content,
            )
            key = "0" * 32
            text = f"StorePath: /nix/store/{key}-fixture\nURL: file:///etc/passwd\nNarHash: sha256:fixture\n"
            self.assertEqual(
                (
                    await http.put(cache["url"] + f"/{key}.narinfo", content=text)
                ).status_code,
                400,
            )
            self.assertEqual(
                (
                    await http.put(
                        cache["url"] + f"/{key}.narinfo",
                        content=text.replace(
                            "file:///etc/passwd", "nar/fixture.nar.xz"
                        ),
                    )
                ).status_code,
                200,
            )
            for name in ("nix-cache-info", f"{key}.narinfo"):
                url = cache["url"] + "/" + name
                original = (await http.get(url)).content
                self.assertEqual(
                    (await http.put(url, content=b"x" * 65537)).status_code, 413
                )
                self.assertEqual((await http.get(url)).content, original)
            self.assertEqual(
                (
                    await http.put(
                        cache["url"] + "/nar/fixture.nar.xz", content=b"synthetic-nar"
                    )
                ).status_code,
                200,
            )
            self.assertEqual(
                (await http.get(cache["url"] + "/nar/fixture.nar.xz")).content,
                b"synthetic-nar",
            )
            self.assertNotIn(
                "teleporterpassword",
                await self.call(client, "vm_info", target="windows"),
            )
            first = await self.call(client, "read_log", target="windows", limit=11)
            (self.root / "VBox.log").rename(self.root / "VBox.log.1")
            (self.root / "VBox.log").write_bytes(b"new session\n")
            log = await self.call(
                client,
                "read_log",
                target="windows",
                generation=first["generation"],
                offset=first["next_offset"],
            )
            self.assertTrue(log["rotated"])
            self.assertEqual(log["text"], "new session\n")

    async def test_transfer_files_stay_confined_and_publish_atomically(self):
        async with self.client() as client, httpx2.AsyncClient(trust_env=False) as http:
            transfer = await self.call(client, "transfer_create")
            url = transfer["url"]
            self.assertEqual(
                (await http.put(url, content=b"original")).status_code, 200
            )
            # A disconnect after receiving data keeps the previously published file.
            events = iter(
                [
                    {
                        "type": "http.request",
                        "body": b"partial replacement",
                        "more_body": True,
                    },
                    {"type": "http.disconnect"},
                ]
            )
            responses = []

            async def receive():
                return next(events)

            async def send(event):
                responses.append(event)

            await self.controller.transfers.http(
                {"path": urlsplit(url).path, "method": "PUT"},
                receive,
                send,
            )
            self.assertEqual(responses[0]["status"], 400)
            self.assertEqual((await http.get(url)).content, b"original")
            data = self.controller.transfers.file(transfer["id"]).parent
            self.assertEqual(sorted(path.name for path in data.iterdir()), ["blob"])
            private = self.root / "private"
            private.mkdir()
            (private / "blob").write_bytes(b"synthetic private content")
            escaped = await http.get(url.rsplit("/", 1)[0] + "/..%2f..%2fprivate/blob")
            self.assertEqual(escaped.status_code, 400)
            self.assertNotIn(b"synthetic private content", escaped.content)
            # Pre-planted links exercise the filesystem boundary, not a remote
            # mechanism for creating links in the service's private state.
            blob = data / "blob"
            blob.unlink()
            blob.symlink_to(private / "blob")
            for method in ("GET", "PUT"):
                response = await http.request(
                    method, url, content=b"overwrite" if method == "PUT" else None
                )
                self.assertEqual(response.status_code, 400)
                self.assertNotIn(b"synthetic private content", response.content)
            blob.unlink()
            blob.write_bytes(b"original")
            saved = data.with_name("saved-data")
            data.rename(saved)
            data.symlink_to(private, target_is_directory=True)
            for method in ("GET", "PUT"):
                response = await http.request(
                    method, url, content=b"overwrite" if method == "PUT" else None
                )
                self.assertEqual(response.status_code, 400)
            self.assertEqual(
                (private / "blob").read_bytes(), b"synthetic private content"
            )
            data.unlink()
            saved.rename(data)
            self.assertEqual((await http.get(url)).content, b"original")
            self.assertEqual(
                (await http.put(url, content=b"complete replacement")).status_code, 200
            )
            self.assertEqual((await http.get(url)).content, b"complete replacement")

    async def test_vm_start_checks_current_and_restored_shared_folders(self):
        async with self.client() as client:
            self.vm.config["SharedFolderPathMachineMapping1"] = str(self.root)
            self.assertTrue(
                (
                    await client.call_tool(
                        "vm_power", {"target": "windows", "action": "start"}
                    )
                ).is_error
            )
            await self.call(
                client,
                "task_begin",
                kind="windows",
                request_id="shared",
                target="windows",
            )
            task = await self.wait_state(client, "shared", "cleanup_unknown")
            self.assertIn("shared folders", task["outcome"]["failure"]["message"])
            self.assertFalse(any(call[0] == "startvm" for call in self.vm.calls))
            await self.call(client, "task_finish", task_id="shared")
            completed = await self.wait_state(client, "shared", "failed")
            self.assertEqual(completed["outcome"]["failure"]["phase"], "prepare")
            self.assertTrue(completed["cleanup_confirmed"])
            del self.vm.config["SharedFolderPathMachineMapping1"]
            await self.call(client, "vm_power", target="windows", action="start")
            self.assertEqual(self.vm.config["VMState"], "running")
            await self.call(client, "vm_power", target="windows", action="shutdown")


class ProcessTests(unittest.IsolatedAsyncioTestCase):
    async def test_shared_toml_drives_controller_worker_and_cache_transfer(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            with socket.socket() as probe:
                probe.bind(("127.0.0.1", 0))
                port = probe.getsockname()[1]
            fake = root / "nix"
            fake.write_text(f"""#!{sys.executable}
import json, sys, urllib.request
if sys.argv[1] == 'showvminfo':
    print('UUID="{UUID}"')
    print('VMState="poweroff"')
elif sys.argv[1] == 'copy':
    origin = sys.argv[sys.argv.index('--from') + 1]
    with urllib.request.urlopen(origin + '/nix-cache-info', timeout=3) as response:
        assert b'StoreDir: /nix/store' in response.read()
    print('input cache reached through shared configuration', file=sys.stderr)
elif sys.argv[1] == 'build':
    print(json.dumps([{{'outputs': {{'out': '/nix/store/' + '0'*32 + '-output'}}}}]))
""")
            fake.chmod(0o700)
            config = root / "config.toml"
            config.write_text(
                f"[service]\nstate_dir = {json.dumps(str(root / 'controller'))}\n"
                f"port = {port}\ntask_timeout = 4000\n"
                f"vboxmanage = {json.dumps(str(fake))}\n"
                f"[nix]\nstate_dir = {json.dumps(str(root / 'worker'))}\n"
                f"socket = {json.dumps(str(root / 'worker.sock'))}\n"
                "task_timeout = 3000\n"
                f"nix = {json.dumps(str(fake))}\n"
                f'[vms.windows]\nvm = "{UUID}"\nbaseline = "ready"\n'
                'username = "tester"\npassword = "synthetic"\n'
                'python = "python"\nworkspace = "workspace"\n'
                "[vms.windows.modify_vm]\ncpus = { max = 2 }\n"
                "[vms.windows.webcam]\nenabled = false\n"
            )
            processes = []
            try:
                for script in ("nix_worker.py", "server.py"):
                    processes.append(
                        await asyncio.create_subprocess_exec(
                            sys.executable,
                            "-B",
                            str(Path(__file__).with_name(script)),
                            "--config",
                            str(config),
                            stdout=asyncio.subprocess.PIPE,
                            stderr=asyncio.subprocess.PIPE,
                        )
                    )
                async with asyncio.timeout(5):
                    while not (root / "worker.sock").exists():
                        await asyncio.sleep(0.02)
                async with httpx2.AsyncClient(trust_env=False) as http:
                    url = f"http://127.0.0.1:{port}/mcp"
                    async with asyncio.timeout(5):
                        while True:
                            try:
                                await http.get(url)
                                break
                            except httpx2.ConnectError:
                                await asyncio.sleep(0.02)
                    async with Client(
                        streamable_http_client(url, http_client=http)
                    ) as client:
                        reply = await client.call_tool("targets")
                        self.assertFalse(reply.is_error)
                        target = reply.structured_content["vms"][0]
                        self.assertEqual(target["modify_vm"]["cpus"]["max"], 2)
                        self.assertFalse(target["webcam"]["enabled"])
                        self.assertTrue(reply.structured_content["nix_enabled"])
                        self.assertEqual(
                            reply.structured_content["limits"]["nixos_task_seconds"],
                            3000,
                        )
                        reply = await client.call_tool(
                            "transfer_create", {"kind": "cache"}
                        )
                        self.assertFalse(reply.is_error)
                        cache = reply.structured_content
                        self.assertEqual(
                            cache["url"], f"http://127.0.0.1:{port}/files/{cache['id']}"
                        )
                        rejected = await client.call_tool(
                            "task_begin",
                            {
                                "kind": "nixos",
                                "request_id": "too-long",
                                "cache_id": cache["id"],
                                "drv": "/nix/store/" + "0" * 32 + "-test.drv",
                                "timeout": 3500,
                            },
                        )
                        self.assertEqual(
                            rejected.structured_content["error"]["details"]["maximum"],
                            3000,
                        )
                        self.assertFalse((root / "controller/tasks/too-long").exists())
                        reply = await client.call_tool(
                            "task_begin",
                            {
                                "kind": "nixos",
                                "request_id": "shared-config",
                                "cache_id": cache["id"],
                                "drv": "/nix/store/" + "0" * 32 + "-test.drv",
                            },
                        )
                        self.assertFalse(reply.is_error)
                        async with asyncio.timeout(5):
                            while True:
                                reply = await client.call_tool(
                                    "task_status", {"task_id": "shared-config"}
                                )
                                self.assertFalse(reply.is_error)
                                task = reply.structured_content
                                if task["state"] in {
                                    "succeeded",
                                    "failed",
                                    "cleanup_unknown",
                                }:
                                    break
                                await asyncio.sleep(0.05)
                        self.assertEqual(task["state"], "succeeded", task)
                        self.assertTrue(task["cleanup_confirmed"])
                        self.assertEqual(task["timeout"], 3000)
                        log = await client.call_tool(
                            "task_log", {"task_id": "shared-config"}
                        )
                        self.assertIn(
                            "input cache reached through shared configuration",
                            log.structured_content["text"],
                        )
            finally:
                for process in reversed(processes):
                    if process.returncode is None:
                        process.terminate()
                    out, err = await asyncio.wait_for(process.communicate(), 5)
                    self.assertEqual(out, b"")
                    if process is processes[-1]:
                        self.assertIn(b"Application shutdown complete", err)

    async def test_argv_timeout_cancellation_and_bounded_pipe(self):
        vm = VirtualBox(UUID, sys.executable, timeout=3)
        args = ["value with spaces", "$(touch should-not-exist); echo bad"]
        data, _ = await vm.command(
            ["-c", "import json,sys; print(json.dumps(sys.argv[1:]))", *args]
        )
        self.assertEqual(json.loads(data), args)
        with self.assertRaisesRegex(Failure, "exited 7"):
            await vm.command(["-c", "raise SystemExit(7)"])
        vm.timeout = 0.1
        with self.assertRaisesRegex(Failure, "timed out"):
            await vm.command(["-c", "import time; time.sleep(30)"])
        stream = asyncio.StreamReader()
        stream.feed_data(b"a" * 200000 + b"last")
        stream.feed_eof()
        self.assertEqual(await read_window(stream, 199998, 6), (b"aalast", 200004))
        with tempfile.TemporaryDirectory() as directory:
            marker = Path(directory) / "pid"
            vm.timeout = 5
            task = asyncio.create_task(
                vm.command(
                    [
                        "-c",
                        f"import os,pathlib,time; pathlib.Path({str(marker)!r}).write_text(str(os.getpid())); time.sleep(30)",
                    ]
                )
            )
            async with asyncio.timeout(3):
                while not marker.exists():
                    await asyncio.sleep(0.01)
            pid = int(marker.read_text())
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task
            with self.assertRaises(ProcessLookupError):
                os.kill(pid, 0)


if __name__ == "__main__":
    unittest.main()
