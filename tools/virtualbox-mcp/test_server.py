import asyncio
import contextlib
import json
import os
import signal
import socket
import sys
import tempfile
import unittest
from pathlib import Path
from types import SimpleNamespace
from unittest.mock import patch

# These are already dependencies of the MCP SDK; tests add no packages.
import httpx2
import uvicorn
from mcp import Client
from mcp.client.streamable_http import streamable_http_client
from mcp.server.mcpserver.exceptions import ToolError
from mcp.shared.exceptions import MCPError
from mcp.types import ElicitResult, InputRequiredResult

from server import Modification, VirtualBox, make_app, read_window

VM_UUID = "11111111-2222-4333-8444-555555555555"
TOKEN = "synthetic-test-token-0123456789abcdef"


class RecordingVM(VirtualBox):
    def __init__(self, log_dir=None):
        super().__init__("test-vm")
        self.calls = []
        self.config = {
            "UUID": VM_UUID,
            "name": "test-vm",
            "VMState": "running",
            "cpus": "4",
            "memory": "16384",
            "nested-hw-virt": "on",
            "vtxvpid": "on",
            "LogFldr": str(log_dir or "."),
            "teleporterpassword": "must-not-return",
        }

    async def command(self, arguments, *, offset=0, limit=1024 * 1024):
        self.calls.append(list(arguments))
        if "--machinereadable" in arguments:
            data = "\n".join(
                f"{key}={json.dumps(value)}" for key, value in self.config.items()
            ).encode()
        elif arguments == ["--version"]:
            data = b"7.2.synthetic\n"
        elif arguments == ["list", "hostinfo"]:
            data = b"Processor count: 8\nOperating system: Linux\nMemory size: 32768 MByte\n"
        else:
            if arguments[0] == "modifyvm":
                self.config[arguments[2].removeprefix("--")] = arguments[3]
            elif arguments[0] == "startvm":
                self.config["VMState"] = "running"
            elif arguments[0] == "controlvm" and arguments[2] in {
                "acpipowerbutton",
                "poweroff",
            }:
                self.config["VMState"] = "poweroff"
            data = b"command accepted"
        return data[offset : offset + limit], len(data)

    @property
    def mutations(self):
        return [
            args
            for args in self.calls
            if args[0] in {"modifyvm", "controlvm", "startvm"}
        ]


class HTTPTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.logs = tempfile.TemporaryDirectory()
        self.addCleanup(self.logs.cleanup)
        self.log = Path(self.logs.name) / "VBox.log"
        self.log.write_bytes(b"first line\nsecond line\nthird line\n")
        self.vm = RecordingVM(self.logs.name)
        await self.vm.resolve()
        self.socket = socket.socket()
        self.socket.bind(("127.0.0.1", 0))
        self.url = f"http://127.0.0.1:{self.socket.getsockname()[1]}/mcp"
        self.http_server = uvicorn.Server(
            uvicorn.Config(
                make_app(self.vm, TOKEN),
                log_level="critical",
                access_log=False,
                timeout_graceful_shutdown=1,
            )
        )
        self.task = asyncio.create_task(self.http_server.serve(sockets=[self.socket]))
        async with asyncio.timeout(5):
            while not self.http_server.started:
                if self.task.done():
                    await self.task
                await asyncio.sleep(0.01)

    async def asyncTearDown(self):
        self.http_server.should_exit = True
        await asyncio.wait_for(self.task, 5)
        self.socket.close()

    @contextlib.asynccontextmanager
    async def client(self, *, mode="auto", callback=None):
        async with httpx2.AsyncClient(
            headers={"Authorization": f"Bearer {TOKEN}"},
            trust_env=False,
        ) as http:
            transport = streamable_http_client(self.url, http_client=http)
            async with Client(
                transport,
                mode=mode,
                elicitation_callback=callback,
                read_timeout_seconds=5,
            ) as client:
                yield client

    async def test_http_auth_origin_and_body_bounds(self):
        async with httpx2.AsyncClient(trust_env=False) as http:
            response = await http.post(self.url, json={})
            self.assertEqual(response.status_code, 401)
            headers = {"Authorization": f"Bearer {TOKEN}"}
            response = await http.post(
                self.url, json={}, headers={**headers, "Origin": "https://evil.invalid"}
            )
            self.assertEqual(response.status_code, 403)
            response = await http.post(
                self.url, json={}, headers={**headers, "Host": "evil.invalid"}
            )
            self.assertEqual(response.status_code, 421)
            response = await http.post(
                self.url,
                content=b"x" * 70000,
                headers={**headers, "Content-Type": "application/json"},
            )
            self.assertEqual(response.status_code, 413)
        self.assertEqual(self.vm.mutations, [])

    async def test_tools_power_and_paged_logs(self):
        async with self.client() as client:
            tools = await client.list_tools()
            self.assertEqual(
                {tool.name for tool in tools.tools},
                {
                    "vm_info",
                    "host_info",
                    "wait_for_state",
                    "vm_power",
                    "read_log",
                    "modify_vm",
                },
            )
            self.assertTrue(
                all(
                    tool.input_schema.get("additionalProperties") is False
                    for tool in tools.tools
                )
            )
            info = await client.call_tool("vm_info")
            self.assertEqual(info.structured_content["UUID"], VM_UUID)
            self.assertEqual(info.structured_content["nested-hw-virt"], "on")
            self.assertNotIn("teleporterpassword", info.structured_content)
            for action in ("start", "shutdown", "poweroff", "reboot", "reset"):
                reply = await client.call_tool("vm_power", {"action": action})
                self.assertFalse(reply.is_error, reply)
            first = (
                await client.call_tool("read_log", {"limit": 11})
            ).structured_content
            self.assertEqual(first["text"], "first line\n")
            second = (
                await client.call_tool(
                    "read_log",
                    {"offset": first["next_offset"], "generation": first["generation"]},
                )
            ).structured_content
            self.assertEqual(second["text"], "second line\nthird line\n")
            self.assertFalse(second["has_more"])
            with self.log.open("ab") as log:
                log.write(b"appended\n")
            appended = (
                await client.call_tool(
                    "read_log",
                    {
                        "offset": second["next_offset"],
                        "generation": second["generation"],
                    },
                )
            ).structured_content
            self.assertEqual(appended["text"], "appended\n")
            self.assertFalse(appended["rotated"])
            tail = (
                await client.call_tool("read_log", {"tail": True, "limit": 9})
            ).structured_content
            self.assertEqual(tail["text"], "appended\n")
            self.log.rename(self.log.with_name("VBox.log.1"))
            self.log.write_bytes(b"new session\n")
            rotated = (
                await client.call_tool(
                    "read_log",
                    {
                        "offset": appended["next_offset"],
                        "generation": appended["generation"],
                    },
                )
            ).structured_content
            self.assertTrue(rotated["rotated"])
            self.assertEqual(rotated["text"], "new session\n")
            previous = (
                await client.call_tool("read_log", {"index": 2})
            ).structured_content
            self.assertTrue(previous["text"].endswith("appended\n"))
            host = (await client.call_tool("host_info")).structured_content
            self.assertEqual(host["virtualbox_version"], "7.2.synthetic")
            self.assertIn("Processor count: 8", host["host"])

            async def transition():
                await asyncio.sleep(0.05)
                self.vm.config["VMState"] = "running"

            change = asyncio.create_task(transition())
            observed = (
                await client.call_tool(
                    "wait_for_state", {"state": "running", "timeout": 2}
                )
            ).structured_content
            await change
            self.assertTrue(observed["reached"])
            observed = (
                await client.call_tool(
                    "wait_for_state", {"state": "poweroff", "timeout": 0.05}
                )
            ).structured_content
            self.assertFalse(observed["reached"])
            self.assertEqual(observed["observed_state"], "running")
        self.assertEqual(
            self.vm.mutations,
            [
                ["startvm", VM_UUID, "--type", "headless"],
                ["controlvm", VM_UUID, "acpipowerbutton"],
                ["controlvm", VM_UUID, "poweroff"],
                ["controlvm", VM_UUID, "reboot"],
                ["controlvm", VM_UUID, "reset"],
            ],
        )

    async def test_confirmation_accept_decline_cancel_and_repeat(self):
        for mode in ("legacy", "auto"):
            messages = []
            answer = ElicitResult(action="decline")

            async def confirm(_ctx, params):
                messages.append(params.message)
                return answer

            async with self.client(mode=mode, callback=confirm) as client:
                for answer in (
                    ElicitResult(action="decline"),
                    ElicitResult(action="cancel"),
                    ElicitResult(action="accept", content={"confirm": False}),
                ):
                    count = len(self.vm.mutations)
                    reply = await client.call_tool(
                        "modify_vm", {"arguments": ["--cpus", "2"]}
                    )
                    self.assertFalse(reply.is_error, reply)
                    self.assertFalse(reply.structured_content["applied"])
                    self.assertEqual(len(self.vm.mutations), count)
                answer = ElicitResult(action="accept", content={"confirm": True})
                self.vm.config["cpus"] = "4"
                for _ in range(2):
                    reply = await client.call_tool(
                        "modify_vm", {"arguments": ["--cpus", "2"]}
                    )
                    self.assertTrue(reply.structured_content["applied"])
                    self.assertEqual(
                        reply.structured_content["changes"],
                        {"cpus": {"before": "4", "after": "2"}} if _ == 0 else {},
                    )
                    self.assertEqual(
                        reply.structured_content["observed_state"], "running"
                    )
                self.assertEqual(len(messages), 5)
                self.assertTrue(
                    all(
                        VM_UUID in msg and '["VBoxManage", "modifyvm"' in msg
                        for msg in messages
                    )
                )
        self.assertEqual(self.vm.mutations, [["modifyvm", VM_UUID, "--cpus", "2"]] * 4)

    async def test_missing_confirmation_capability_and_parameter_bypass(self):
        for mode in ("legacy", "auto"):
            async with self.client(mode=mode) as client:
                reply = await client.call_tool(
                    "modify_vm", {"arguments": ["--cpus", "2"]}
                )
                self.assertTrue(reply.is_error)

        async def must_not_ask(_ctx, _params):
            self.fail("Unknown confirmed argument reached the approval handler")

        async with self.client(callback=must_not_ask) as client:
            reply = await client.call_tool(
                "modify_vm", {"arguments": ["--cpus", "2"], "confirmed": True}
            )
            self.assertTrue(reply.is_error)
        self.assertEqual(self.vm.mutations, [])

    async def test_approved_change_reads_back_after_cli_failure(self):
        original = self.vm.command

        async def partially_apply(arguments, **kwargs):
            reply = await original(arguments, **kwargs)
            if arguments[0] == "modifyvm":
                raise ToolError("synthetic error after applying memory")
            return reply

        async def confirm(_ctx, _params):
            return ElicitResult(action="accept", content={"confirm": True})

        with patch.object(self.vm, "command", side_effect=partially_apply):
            async with self.client(callback=confirm) as client:
                reply = await client.call_tool(
                    "modify_vm", {"arguments": ["--memory", "8192"]}
                )
        observed = reply.structured_content
        self.assertIsNone(observed["applied"])
        self.assertIn("after applying memory", observed["error"])
        self.assertEqual(
            observed["changes"], {"memory": {"before": "16384", "after": "8192"}}
        )
        self.assertEqual(observed["observed_state"], "running")

    async def test_modern_confirmation_is_bound_and_single_use(self):
        async def unused_callback(_ctx, _params):
            self.fail("Manual continuation must not invoke the callback")

        args = {"arguments": ["--cpus", "2"]}
        answers = {"approval": ElicitResult(action="accept", content={"confirm": True})}
        async with self.client(callback=unused_callback) as client:
            ask = await client.session.call_tool(
                "modify_vm", args, allow_input_required=True
            )
            self.assertIsInstance(ask, InputRequiredResult)
            self.assertEqual(self.vm.mutations, [])
            with self.assertRaisesRegex(MCPError, "Invalid or expired requestState"):
                await client.session.call_tool(
                    "modify_vm",
                    {"arguments": ["--cpus", "8"]},
                    input_responses=answers,
                    request_state=ask.request_state,
                )
            ask = await client.session.call_tool(
                "modify_vm", args, allow_input_required=True
            )
            reply = await client.session.call_tool(
                "modify_vm",
                args,
                input_responses=answers,
                request_state=ask.request_state,
            )
            self.assertFalse(reply.is_error)
            replay = await client.session.call_tool(
                "modify_vm",
                args,
                input_responses=answers,
                request_state=ask.request_state,
            )
            self.assertTrue(replay.is_error)
        self.assertEqual(self.vm.mutations, [["modifyvm", VM_UUID, "--cpus", "2"]])


class ProcessTests(unittest.IsolatedAsyncioTestCase):
    async def test_cli_http_with_and_without_token(self):
        for token in (TOKEN, ""):
            with self.subTest(authenticated=bool(token)):
                await self.cli_session(token)

    async def cli_session(self, token):
        component = Path(__file__).parent
        scratch = component / "tmp"
        scratch.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(dir=scratch) as directory:
            fake = Path(directory) / "VBoxManage"
            fake.write_text(
                f"#!{sys.executable}\n"
                "import sys\n"
                "if sys.argv[1] != 'showvminfo': sys.exit(99)\n"
                f"print('UUID=\"{VM_UUID}\"')\n"
                "print('name=\"test-vm\"')\n"
                "print('VMState=\"running\"')\n"
            )
            fake.chmod(0o700)
            with socket.socket() as probe:
                probe.bind(("127.0.0.1", 0))
                port = probe.getsockname()[1]
            process = await asyncio.create_subprocess_exec(
                sys.executable,
                "-B",
                str(component / "server.py"),
                "--vm",
                "test-vm",
                "--vboxmanage",
                str(fake),
                "--token",
                token,
                "--port",
                str(port),
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
            try:
                url = f"http://127.0.0.1:{port}/mcp"
                async with httpx2.AsyncClient(trust_env=False) as http:
                    async with asyncio.timeout(5):
                        while True:
                            try:
                                response = await http.get(url)
                                if token:
                                    self.assertEqual(response.status_code, 401)
                                break
                            except httpx2.ConnectError:
                                await asyncio.sleep(0.02)
                    if token:
                        http.headers["Authorization"] = f"Bearer {token}"
                    async with Client(
                        streamable_http_client(url, http_client=http)
                    ) as client:
                        reply = await client.call_tool("vm_info")
                        self.assertEqual(reply.structured_content["UUID"], VM_UUID)
            finally:
                if process.returncode is None:
                    process.terminate()
                try:
                    stdout, stderr = await asyncio.wait_for(process.communicate(), 5)
                except TimeoutError:
                    process.kill()
                    await process.communicate()
                    self.fail("CLI server did not shut down within five seconds")
            # Uvicorn re-raises SIGTERM after completing graceful shutdown.
            self.assertEqual(process.returncode, -signal.SIGTERM, stderr.decode())
            self.assertIn(b"Application shutdown complete", stderr)
            self.assertEqual(stdout, b"")
            self.assertNotIn(TOKEN.encode(), stderr)

    async def test_pipe_window_is_bounded_and_byte_offsets_are_exact(self):
        stream = asyncio.StreamReader()
        stream.feed_data(b"a" * 200000 + b"last")
        stream.feed_eof()
        data, total = await read_window(stream, 199998, 6)
        self.assertEqual((data, total), (b"aalast", 200004))

    async def test_subprocess_arguments_failures_and_timeout(self):
        vm = VirtualBox(VM_UUID, sys.executable, timeout=3)
        arguments = ["value with spaces", "$(touch should-not-exist); echo bad"]
        data, _ = await vm.command(
            ["-c", "import json,sys; print(json.dumps(sys.argv[1:]))", *arguments]
        )
        self.assertEqual(json.loads(data), arguments)
        with self.assertRaisesRegex(ToolError, "exited 7.*synthetic failure"):
            await vm.command(
                [
                    "-c",
                    "import sys; print('synthetic failure', file=sys.stderr); sys.exit(7)",
                ]
            )
        vm.timeout = 0.1
        with self.assertRaisesRegex(ToolError, "timed out"):
            await vm.command(["-c", "import time; time.sleep(20)"])

    async def test_cancellation_reaps_only_the_command(self):
        scratch = Path(__file__).parent / "tmp"
        scratch.mkdir(exist_ok=True)
        with tempfile.TemporaryDirectory(dir=scratch) as directory:
            marker = Path(directory) / "pid"
            code = f"import os,pathlib,time; pathlib.Path({str(marker)!r}).write_text(str(os.getpid())); time.sleep(20)"
            vm = VirtualBox(VM_UUID, sys.executable, timeout=5)
            task = asyncio.create_task(vm.command(["-c", code]))
            async with asyncio.timeout(3):
                while not marker.exists():
                    await asyncio.sleep(0.01)
            pid = int(marker.read_text())
            task.cancel()
            with self.assertRaises(asyncio.CancelledError):
                await task
            with self.assertRaises(ProcessLookupError):
                os.kill(pid, 0)

    async def test_expired_confirmation_cannot_modify(self):
        vm = RecordingVM()
        modifier = Modification(vm)
        caps = SimpleNamespace(elicitation=SimpleNamespace(form={}, url=None))
        ctx = SimpleNamespace(
            client_capabilities=caps,
            protocol_version="2026-07-28",
            request_state=None,
            input_responses=None,
        )
        with patch("server.time.monotonic", return_value=100):
            ask = await modifier.modify_vm(["--cpus", "2"], ctx)
        ctx.request_state = ask.request_state
        ctx.input_responses = {
            "approval": ElicitResult(action="accept", content={"confirm": True})
        }
        with patch("server.time.monotonic", return_value=221):
            with self.assertRaisesRegex(ToolError, "expired"):
                await modifier.modify_vm(["--cpus", "2"], ctx)
        self.assertEqual(vm.mutations, [])


if __name__ == "__main__":
    unittest.main()
