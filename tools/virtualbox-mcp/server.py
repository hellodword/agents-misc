#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["mcp==2.3.0"]
# ///
"""A private HTTP MCP server controlling one VirtualBox VM on its host."""

import argparse
import asyncio
import contextlib
import hashlib
import hmac
import ipaddress
import json
import logging
import math
import os
import secrets
import shutil
import sys
import time
import uuid
from pathlib import Path
from typing import Literal

from mcp.server import MCPServer
from mcp.server.mcpserver import Context
from mcp.server.mcpserver.exceptions import ToolError
from mcp.server.transport_security import TransportSecuritySettings
from mcp.types import (
    CallToolResult,
    ElicitRequest,
    ElicitRequestFormParams,
    ElicitResult,
    InputRequiredResult,
    TextContent,
    ToolAnnotations,
)

MAX_OUTPUT = 1024 * 1024
CONFIRM_SECONDS = 120


async def read_window(stream, offset, limit):
    """Drain a pipe, retaining only the requested byte range."""
    data = bytearray()
    total = 0
    while chunk := await stream.read(65536):
        start = max(0, offset - total)
        if start < len(chunk) and len(data) < limit:
            data.extend(chunk[start : start + limit - len(data)])
        total += len(chunk)
    return bytes(data), total


class VirtualBox:
    def __init__(self, target, executable="VBoxManage", timeout=30):
        self.target = target
        self.executable = executable
        self.timeout = timeout
        self.mutation_lock = asyncio.Lock()

    async def command(self, arguments, *, offset=0, limit=MAX_OUTPUT):
        try:
            process = await asyncio.create_subprocess_exec(
                self.executable,
                *arguments,
                stdin=asyncio.subprocess.DEVNULL,
                stdout=asyncio.subprocess.PIPE,
                stderr=asyncio.subprocess.PIPE,
            )
        except OSError as error:
            raise ToolError(f"Could not start VBoxManage: {error}") from error
        try:
            async with asyncio.timeout(self.timeout):
                stdout, stderr, code = await asyncio.gather(
                    read_window(process.stdout, offset, limit),
                    read_window(process.stderr, 0, 8192),
                    process.wait(),
                )
        except (TimeoutError, asyncio.CancelledError) as error:
            # Kill only this CLI process, never a VM it may have started.
            if process.returncode is None:
                with contextlib.suppress(ProcessLookupError):
                    process.kill()
            with contextlib.suppress(TimeoutError):
                await asyncio.wait_for(process.wait(), 2)
            if isinstance(error, TimeoutError):
                raise ToolError(
                    "VBoxManage timed out. An operation may have taken effect; "
                    "inspect vm_info before retrying."
                ) from error
            raise
        if code:
            raise ToolError(
                f"VBoxManage {arguments[0]} exited {code}: "
                + stderr[0].decode("utf-8", errors="replace").strip()
            )
        return stdout

    async def resolve(self):
        info = await self.info()
        self.target = str(uuid.UUID(info["UUID"]))

    async def info(self) -> dict[str, str]:
        """Read this VM's state and machine-readable configuration."""
        data, total = await self.command(
            ["showvminfo", self.target, "--machinereadable"]
        )
        if total > MAX_OUTPUT:
            raise ToolError("VM information exceeded the output limit")
        info = {}
        for line in data.decode("utf-8").splitlines():
            key, separator, value = line.partition("=")
            if separator:
                key = key.strip('"')
                if "password" in key.lower():
                    continue
                try:
                    info[key] = json.loads(value) if value.startswith('"') else value
                except ValueError:
                    info[key] = value.strip('"')
        if not info.get("UUID") or not info.get("VMState"):
            raise ToolError("VBoxManage returned incomplete VM information")
        return info

    async def host_info(self) -> dict[str, object]:
        """Read the VirtualBox version and host OS, CPU and memory information."""
        version, details = await asyncio.gather(
            self.command(["--version"]), self.command(["list", "hostinfo"])
        )
        return {
            "virtualbox_version": version[0].decode().strip(),
            "host": details[0].decode("utf-8", errors="replace").strip(),
        }

    async def wait_for_state(
        self, state: str, timeout: float = 30
    ) -> dict[str, object]:
        """Wait for a VirtualBox state, e.g. running or poweroff.

        running describes the VM process; check SSH readiness separately.
        A reboot may remain running throughout this observation.
        """
        if not math.isfinite(timeout) or timeout < 0:
            raise ToolError("State wait timeout must be a finite, nonnegative number")
        started = time.monotonic()
        last = None
        reached = False
        try:
            async with asyncio.timeout(timeout):
                while True:
                    last = await self.info()
                    if last["VMState"] == state:
                        reached = True
                        break
                    await asyncio.sleep(0.5)
        except TimeoutError:
            pass
        return {
            "vm_uuid": self.target,
            "target_state": state,
            "reached": reached,
            "observed_state": last["VMState"] if last else None,
            "elapsed_seconds": round(time.monotonic() - started, 3),
        }

    async def modify(self, arguments):
        """Observe configuration around one serialized modifyvm operation."""
        async with self.mutation_lock:
            before = await self.info()
            response = {"vm_uuid": self.target, "arguments": arguments}
            try:
                data, _ = await self.command(["modifyvm", self.target, *arguments])
                response.update(applied=True, output=data.decode(errors="replace"))
            except ToolError as error:
                response.update(applied=None, error=str(error))
            try:
                after = await self.info()
                response["changes"] = {
                    key: {"before": before.get(key), "after": after.get(key)}
                    for key in sorted(before.keys() | after.keys())
                    if before.get(key) != after.get(key)
                }
                response["observed_state"] = after["VMState"]
            except ToolError as error:
                response["readback_error"] = str(error)
            return response

    async def mutate(self, arguments):
        try:
            async with asyncio.timeout(self.timeout):
                async with self.mutation_lock:
                    data, _ = await self.command(arguments)
        except TimeoutError as error:
            raise ToolError(
                "VBoxManage timed out. The operation may have taken effect; "
                "inspect vm_info before retrying."
            ) from error
        return data.decode("utf-8", errors="replace")

    async def power(
        self, action: Literal["start", "shutdown", "poweroff", "reboot", "reset"]
    ) -> dict[str, object]:
        """start: headless; shutdown: ACPI; reboot: Guest Additions.

        poweroff cuts power immediately; reset is a hard reboot. Both can lose
        unsaved guest data. Never escalate a graceful operation automatically.
        """
        actions = {
            "shutdown": "acpipowerbutton",
            "poweroff": "poweroff",
            "reboot": "reboot",
            "reset": "reset",
        }
        if action == "start":
            command = ["startvm", self.target, "--type", "headless"]
        elif action in actions:
            command = ["controlvm", self.target, actions[action]]
        else:
            raise ToolError("Unknown power action")
        output = await self.mutate(command)
        return {
            "vm_uuid": self.target,
            "action": action,
            "command_succeeded": True,
            "output": output,
            "note": "Use vm_info to observe state. A successful ACPI/guest request does not prove shutdown or reboot completed.",
        }

    async def read_log(
        self,
        index: int = 0,
        offset: int = 0,
        limit: int = 65536,
        tail: bool = False,
        generation: str | None = None,
    ) -> dict[str, object]:
        """Read a byte range of a VirtualBox log, never an arbitrary host file.

        index: 0 = VBox.log, 1 = VBoxHardening.log, 2 = VBox.log.1, etc.
        tail reads the last limit bytes. Pass generation and next_offset to
        continue; a rotated log restarts at offset zero and reports rotated.
        """
        if not 0 <= index <= 99 or offset < 0 or not 1 <= limit <= 262144:
            raise ToolError("Require index 0..99, offset >= 0, limit 1..262144")
        info = await self.info()
        name = (
            "VBox.log"
            if index == 0
            else "VBoxHardening.log"
            if index == 1
            else f"VBox.log.{index - 1}"
        )
        try:
            path = Path(info["LogFldr"]) / name
            with path.open("rb") as log:
                stat = os.fstat(log.fileno())
                # VBox records a session timestamp near the start. Inode identity
                # also distinguishes replacement on hosts which expose it.
                lines = log.read(4096).splitlines()
                header = next(
                    (line for line in lines if b"Log opened" in line),
                    lines[0] if lines else b"",
                )
                identity = f"{stat.st_dev}:{stat.st_ino}:".encode() + header
                current = hashlib.sha256(identity).hexdigest()[:24]
                total = log.seek(0, 2)
                rotated = generation is not None and (
                    generation != current or offset > total
                )
                start = (
                    max(0, total - limit)
                    if tail
                    else min(0 if rotated else offset, total)
                )
                log.seek(start)
                data = log.read(min(limit, total - start))
        except (OSError, KeyError) as error:
            raise ToolError(f"Could not read VM log {name}: {error}") from error
        next_offset = start + len(data)
        return {
            "vm_uuid": self.target,
            "index": index,
            "offset": start,
            "text": data.decode("utf-8", errors="replace"),
            "next_offset": next_offset,
            "total_bytes": total,
            "has_more": next_offset < total,
            "generation": current,
            "rotated": rotated,
        }


def result(data):
    return CallToolResult(
        content=[TextContent(type="text", text=json.dumps(data, ensure_ascii=False))],
        structured_content=data,
    )


class Modification:
    def __init__(self, vm):
        self.vm = vm
        self.pending = {}

    async def modify_vm(
        self, arguments: list[str], ctx: Context
    ) -> CallToolResult | InputRequiredResult:
        """Apply modifyvm options only after fresh human approval of these arguments.

        Pass options and values as separate strings, e.g. ["--cpus", "2"].
        Do not include the command or VM. No change occurs without confirmation.
        """
        if not arguments or len(arguments) > 128 or not arguments[0].startswith("--"):
            raise ToolError("Supply 1..128 modifyvm arguments starting with an option")
        if sum(map(len, arguments)) > 8192 or any(
            any(char in arg for char in "\x00\r\n") for arg in arguments
        ):
            raise ToolError("Arguments exceed 8192 characters or contain NUL/newline")
        caps = ctx.client_capabilities
        if (
            not caps
            or caps.elicitation is None
            or (caps.elicitation.form is None and caps.elicitation.url is not None)
        ):
            raise ToolError(
                "modify_vm requires a client supporting human form elicitation"
            )

        command = ["modifyvm", self.vm.target, *arguments]
        message = (
            "Apply this one VM configuration change?\nExact command arguments: "
            + json.dumps(["VBoxManage", *command], ensure_ascii=False)
            + "\nApproval expires in two minutes and authorizes one execution only."
        )
        schema = {
            "type": "object",
            "properties": {
                "confirm": {
                    "type": "boolean",
                    "title": "Approve this exact change",
                    "default": False,
                }
            },
            "required": ["confirm"],
        }
        if ctx.protocol_version and ctx.protocol_version < "2026-07-28":
            try:
                async with asyncio.timeout(CONFIRM_SECONDS):
                    answer = await ctx.session.elicit_form(
                        message,
                        schema,
                        related_request_id=ctx.request_context.request_id,
                    )
            except TimeoutError as error:
                raise ToolError("Confirmation timed out; no change was made") from error
        else:
            now = time.monotonic()
            self.pending = {k: v for k, v in self.pending.items() if v[1] > now}
            if not ctx.request_state:
                if ctx.input_responses or len(self.pending) >= 64:
                    raise ToolError(
                        "Unexpected response or too many pending confirmations"
                    )
                key = secrets.token_urlsafe(32)
                self.pending[key] = (tuple(command), now + CONFIRM_SECONDS)
                return InputRequiredResult(
                    request_state=key,
                    input_requests={
                        "approval": ElicitRequest(
                            params=ElicitRequestFormParams(
                                message=message,
                                requested_schema=schema,
                            )
                        )
                    },
                )
            # Pop before executing: expired, changed and replayed requests fail closed.
            pending = self.pending.pop(ctx.request_state, None)
            if pending is None or pending[0] != tuple(command):
                raise ToolError(
                    "Confirmation expired, was reused, or arguments changed"
                )
            answers = ctx.input_responses or {}
            if set(answers) != {"approval"}:
                raise ToolError("Missing confirmation response")
            answer = answers["approval"]
        if (
            not isinstance(answer, ElicitResult)
            or answer.action != "accept"
            or (answer.content or {}).get("confirm") is not True
        ):
            return result({"applied": False, "reason": "User did not approve"})
        return result(await self.vm.modify(arguments))


async def strict_tool_arguments(ctx, call_next):
    """Reject unknown tool inputs rather than silently ignoring caller mistakes."""
    allowed = {
        "vm_info": set(),
        "host_info": set(),
        "wait_for_state": {"state", "timeout"},
        "vm_power": {"action"},
        "read_log": {"index", "offset", "limit", "tail", "generation"},
        "modify_vm": {"arguments"},
    }
    if ctx.method == "tools/call" and isinstance(ctx.params, dict):
        name = ctx.params.get("name")
        arguments = ctx.params.get("arguments", {})
        if isinstance(name, str) and name in allowed and isinstance(arguments, dict):
            unknown = set(arguments) - allowed[name]
            if unknown:
                return CallToolResult(
                    is_error=True,
                    content=[
                        TextContent(
                            type="text",
                            text=f"Unknown tool arguments: {', '.join(sorted(unknown))}",
                        )
                    ],
                )
    response = await call_next(ctx)
    if ctx.method == "tools/list" and isinstance(response, dict):
        for tool in response["tools"]:
            tool["inputSchema"]["additionalProperties"] = False
    return response


def make_server(vm):
    mcp = MCPServer(
        "virtualbox-mcp",
        version="0.1.0",
        log_level="WARNING",
        middleware=[strict_tool_arguments],
        instructions="Control only the configured VM. Every modify_vm call requires human confirmation; never auto-accept elicitation. After a timeout, inspect vm_info before retrying.",
    )
    readonly = ToolAnnotations(read_only_hint=True, open_world_hint=False)
    mutable = ToolAnnotations(
        read_only_hint=False, destructive_hint=True, open_world_hint=False
    )
    mcp.add_tool(vm.info, name="vm_info", annotations=readonly, structured_output=True)
    mcp.add_tool(vm.host_info, annotations=readonly, structured_output=True)
    mcp.add_tool(vm.wait_for_state, annotations=readonly, structured_output=True)
    mcp.add_tool(vm.power, name="vm_power", annotations=mutable, structured_output=True)
    mcp.add_tool(
        vm.read_log, name="read_log", annotations=readonly, structured_output=True
    )
    mcp.add_tool(
        Modification(vm).modify_vm, annotations=mutable, structured_output=False
    )
    return mcp


class BearerAuth:
    """Private single-operator bearer authentication, including MCP follow-ups."""

    def __init__(self, app, token):
        self.app = app
        self.expected = b"Bearer " + token.encode("ascii")

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        headers = dict(scope["headers"])
        if not hmac.compare_digest(headers.get(b"authorization", b""), self.expected):
            body = b'{"error":{"code":"unauthorized","message":"A valid bearer token is required"}}'
            await send(
                {
                    "type": "http.response.start",
                    "status": 401,
                    "headers": [
                        (b"content-type", b"application/json"),
                        (b"www-authenticate", b"Bearer"),
                    ],
                }
            )
            return await send({"type": "http.response.body", "body": body})
        return await self.app(scope, receive, send)


def make_app(vm, token, host="127.0.0.1"):
    formatted_host = f"[{host}]" if ":" in host else host
    security = TransportSecuritySettings(
        allowed_hosts=[f"{formatted_host}:*", "localhost:*", "127.0.0.1:*", "[::1]:*"],
        allowed_origins=[],
    )
    app = make_server(vm).streamable_http_app(
        host=host,
        max_request_body_size=65536,
        max_sessions=32,
        session_idle_timeout=300,
        transport_security=security,
    )
    return BearerAuth(app, token) if token else app


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--vm", required=True, help="VM name or UUID; fixed to UUID at startup"
    )
    parser.add_argument(
        "--token", default="", help="Optional bearer token; empty allows direct access"
    )
    parser.add_argument("--host", default="127.0.0.1", help="Concrete host IP to bind")
    parser.add_argument("--port", type=int, default=8765)
    parser.add_argument(
        "--vboxmanage", default="VBoxManage", help="Host VBoxManage executable"
    )
    parser.add_argument(
        "--command-timeout",
        type=float,
        default=30,
        help="Seconds per command, at most 60",
    )
    args = parser.parse_args(argv)
    try:
        address = ipaddress.ip_address(args.host)
        if address.is_unspecified or not 1 <= args.port <= 65535:
            raise ValueError("Use a concrete host IP and a port in 1..65535")
        if not 0 < args.command_timeout <= 60 or args.vm.startswith("-") or not args.vm:
            raise ValueError("Invalid VM or command timeout (must be in (0, 60])")
    except ValueError as error:
        parser.error(str(error))
    try:
        executable = shutil.which(args.vboxmanage)
        if not executable:
            raise RuntimeError("VBoxManage was not found")
        vm = VirtualBox(args.vm, executable, args.command_timeout)
        asyncio.run(vm.resolve())
        # Uvicorn is the HTTP runtime already required by the official MCP SDK.
        import uvicorn

        uvicorn.run(
            make_app(vm, args.token, args.host),
            host=args.host,
            port=args.port,
            timeout_keep_alive=30,
            timeout_graceful_shutdown=5,
            access_log=False,
        )
    except (OSError, ValueError, RuntimeError, TimeoutError, ToolError) as error:
        logging.error("%s", error)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
