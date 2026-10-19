import asyncio
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
from common import DEFAULTS, Failure, atomic_json, load_json, terminate
from mcp import Client
from mcp.client.streamable_http import streamable_http_client
from mcp.shared.exceptions import MCPError
from mcp.types import ElicitResult, InputRequiredResult
from server import make_app
from tasks import Controller
from virtualbox import VirtualBox, read_window

UUID = "11111111-2222-4333-8444-555555555555"
TOKEN = "synthetic-token"


class RecordingVM(VirtualBox):
    """Synthetic VBox boundary; guest commands run real isolated POSIX processes."""

    def __init__(self, directory):
        self.directory = Path(directory)
        self.guest_root = self.directory / "guest"
        self.guest_root.mkdir(exist_ok=True)
        super().__init__(
            UUID,
            profile={
                "vm": UUID,
                "baseline": "ready",
                "interruptible": True,
                "frontend": "gui",
                "username": "tester",
                "password": "synthetic",
                "python": sys.executable,
                "workspace": r"C:\vm-tests",
            },
        )
        self.config = {
            "UUID": UUID,
            "VMState": "poweroff",
            "cpus": "4",
            "LogFldr": str(directory),
            "teleporterpassword": "omit-this",
        }
        self.calls, self.children, self.jobs = [], [], []
        self.shutdown_hangs = False
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
        elif arguments == ["list", "runningvms"]:
            data = (
                f'"fixture" {{{UUID}}}\n' if self.config["VMState"] == "running" else ""
            ).encode()
        elif arguments[0] == "startvm":
            self.config["VMState"] = "running"
            data = b"started"
        elif arguments[0] == "controlvm":
            if arguments[2] in {"shutdown", "poweroff"} and (
                not self.shutdown_hangs or arguments[2] == "poweroff"
            ):
                for child in self.children:
                    await terminate(child, grace=0.1)
                self.config["VMState"] = "poweroff"
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

    async def python(self, *args, background=False, timeout=None):
        if args[0] == "-c":
            self.local(args[-1]).mkdir(parents=True, exist_ok=True)
            return b"", 0
        if args[-1] == "probe":
            return b'{"interactive":true,"session_id":1}', 35
        self.jobs.append(asyncio.create_task(self.run_guest(self.local(args[-1]))))
        return b"started", 7

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
            process = await asyncio.create_subprocess_exec(
                *argv,
                stdout=output,
                stderr=output,
                cwd=directory.parent,
                start_new_session=True,
            )
            self.children.append(process)
            started, reason = time.monotonic(), "exited"
            while process.returncode is None:
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
        state.update(
            state="finished",
            exit_code=process.returncode if reason == "exited" else None,
            reason=reason,
            cleanup_confirmed=True,
        )
        atomic_json(directory / "status.json", state)

    async def copy_to(self, source, destination):
        path = self.local(destination)
        path.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(source, path)

    async def copy_from(self, source, destination):
        try:
            shutil.copyfile(self.local(source), destination)
        except FileNotFoundError as error:
            raise Failure("Guest file not yet present") from error

    async def screenshot(self, destination):
        Path(destination).write_bytes(b"\x89PNG\r\nfixture")


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
    async def client(self, callback=None, mode="auto"):
        async with (
            httpx2.AsyncClient(
                headers={"Authorization": f"Bearer {TOKEN}"}, trust_env=False
            ) as http,
            Client(
                streamable_http_client(self.origin + "/mcp", http_client=http),
                elicitation_callback=callback,
                mode=mode,
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

            async def linked_screenshot(destination):
                Path(destination).symlink_to(private)

            with patch.object(self.vm, "screenshot", side_effect=linked_screenshot):
                self.assertTrue(
                    (
                        await client.call_tool("screenshot", {"task_id": "desktop"})
                    ).is_error
                )
            self.assertEqual(private.read_text(), "synthetic private content")
            self.assertEqual(
                (await http.get(shot["url"])).content, b"\x89PNG\r\nfixture"
            )
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
            self.assertIsNotNone(self.vm.children[0].returncode)
        finally:
            await replacement.close()

    async def test_shutdown_failure_keeps_queue_until_confirmed_poweroff(self):
        async def approve(_ctx, _params):
            return ElicitResult(action="accept", content={"confirm": True})

        async with self.client(callback=approve) as client:
            await self.call(
                client,
                "task_begin",
                kind="windows",
                request_id="stuck",
                target="windows",
            )
            await self.wait_state(client, "stuck", "ready")
            self.vm.shutdown_hangs = True
            await self.call(
                client,
                "task_begin",
                kind="windows",
                request_id="next",
                target="windows",
            )
            await self.call(client, "task_finish", task_id="stuck")
            await self.wait_state(client, "stuck", "cleanup_unknown")
            self.assertEqual(
                (await self.call(client, "task_status", task_id="next"))["state"],
                "queued",
            )
            await self.call(client, "vm_power", target="windows", action="poweroff")
            self.vm.shutdown_hangs = False
            await self.wait_state(client, "next", "ready")
            await self.call(client, "task_cancel", task_id="next")
            self.assertTrue(
                (await self.wait_state(client, "next", "cancelled"))[
                    "cleanup_confirmed"
                ]
            )

    async def test_typed_configuration_confirmation_and_readback(self):
        answer = ElicitResult(action="decline")

        async def confirm(_ctx, _params):
            return answer

        for mode in ("auto", "legacy"):
            answer = ElicitResult(action="decline")

            async with self.client(callback=confirm, mode=mode) as client:
                self.assertFalse(
                    (
                        await self.call(
                            client, "modify_vm", target="windows", changes={"cpus": 2}
                        )
                    )["applied"]
                )
                answer = ElicitResult(action="accept", content={"confirm": True})
                self.vm.config["cpus"] = "4"
                reply = await self.call(
                    client, "modify_vm", target="windows", changes={"cpus": 2}
                )
                self.assertEqual(
                    reply["changes"]["cpus"], {"before": "4", "after": "2"}
                )
                invalid = await client.call_tool(
                    "modify_vm",
                    {
                        "target": "windows",
                        "changes": {"cpus": 2, "uart_mode": "/host/file"},
                    },
                )
                self.assertTrue(invalid.is_error)

        original = self.vm.command

        async def partial_change(arguments, **kwargs):
            response = await original(arguments, **kwargs)
            if arguments[0] == "modifyvm":
                raise Failure("synthetic failure after applying the setting")
            return response

        async with self.client(callback=confirm) as client:
            with patch.object(self.vm, "command", side_effect=partial_change):
                reply = await self.call(
                    client, "modify_vm", target="windows", changes={"cpus": 3}
                )
            self.assertIsNone(reply["applied"])
            self.assertEqual(reply["changes"]["cpus"], {"before": "2", "after": "3"})
            self.assertIn("after applying", reply["error"])

        async def unused(_ctx, _params):
            self.fail("Manual continuation called callback")

        async with self.client(callback=unused) as client:
            args = {"target": "windows", "changes": {"cpus": 2}}
            ask = await client.session.call_tool(
                "modify_vm", args, allow_input_required=True
            )
            self.assertIsInstance(ask, InputRequiredResult)
            answers = {
                "approval": ElicitResult(action="accept", content={"confirm": True})
            }
            with self.assertRaises(MCPError):
                await client.session.call_tool(
                    "modify_vm",
                    {"target": "windows", "changes": {"cpus": 3}},
                    input_responses=answers,
                    request_state=ask.request_state,
                )
            ask = await client.session.call_tool(
                "modify_vm", args, allow_input_required=True
            )
            self.assertFalse(
                (
                    await client.session.call_tool(
                        "modify_vm",
                        args,
                        input_responses=answers,
                        request_state=ask.request_state,
                    )
                ).is_error
            )
            self.assertTrue(
                (
                    await client.session.call_tool(
                        "modify_vm",
                        args,
                        input_responses=answers,
                        request_state=ask.request_state,
                    )
                ).is_error
            )

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
            self.assertIn("shared folders", task["error"])
            self.assertFalse(any(call[0] == "startvm" for call in self.vm.calls))
            await self.call(client, "task_cancel", task_id="shared")
            await self.wait_state(client, "shared", "cancelled")
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
if sys.argv[1] == 'copy':
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
                f"[nix]\nstate_dir = {json.dumps(str(root / 'worker'))}\n"
                f"socket = {json.dumps(str(root / 'worker.sock'))}\n"
                f"nix = {json.dumps(str(fake))}\n"
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
                        self.assertEqual(reply.structured_content["vms"], [])
                        self.assertTrue(reply.structured_content["nix_enabled"])
                        reply = await client.call_tool(
                            "transfer_create", {"kind": "cache"}
                        )
                        self.assertFalse(reply.is_error)
                        cache = reply.structured_content
                        self.assertEqual(
                            cache["url"], f"http://127.0.0.1:{port}/files/{cache['id']}"
                        )
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
                        self.assertEqual(task["worker"]["request"]["timeout"], 4000)
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
