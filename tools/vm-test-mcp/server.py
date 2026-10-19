#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["mcp==2.3.0"]
# ///
"""HTTP MCP for single-layer Windows and NixOS VM testing."""

import argparse
import asyncio
import contextlib
import functools
import hmac
import inspect
import ipaddress
import json
import logging
import secrets
import sys
import time
from typing import Literal
from urllib.parse import urlsplit

from common import Failure, read_config
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
from pydantic import BaseModel, ConfigDict, Field
from tasks import Controller
from transfers import reply


def result(data):
    return CallToolResult(
        content=[TextContent(type="text", text=json.dumps(data, ensure_ascii=False))],
        structured_content=data,
    )


class VMChanges(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)
    cpus: int | None = Field(default=None, ge=1, le=256)
    memory_mb: int | None = Field(default=None, ge=128, le=1048576)
    vram_mb: int | None = Field(default=None, ge=1, le=256)
    graphics_controller: Literal["vmsvga", "vboxsvga", "vboxvga", "none"] | None = None
    accelerate_3d: bool | None = None
    paravirt_provider: (
        Literal["default", "legacy", "minimal", "hyperv", "kvm", "none"] | None
    ) = None
    nested_paging: bool | None = None
    vpid: bool | None = None

    def arguments(self):
        flags = {
            "cpus": "--cpus",
            "memory_mb": "--memory",
            "vram_mb": "--vram",
            "graphics_controller": "--graphicscontroller",
            "accelerate_3d": "--accelerate-3d",
            "paravirt_provider": "--paravirt-provider",
            "nested_paging": "--nested-paging",
            "vpid": "--x86-vtx-vpid",
        }
        arguments = []
        for name, value in self.model_dump(exclude_none=True).items():
            arguments.extend(
                [
                    flags[name],
                    ("on" if value else "off")
                    if isinstance(value, bool)
                    else str(value),
                ]
            )
        if not arguments:
            raise Failure("Choose at least one VM setting")
        return arguments


class Confirmation:
    def __init__(self):
        self.pending = {}

    async def run(self, command, ctx, apply):
        caps = ctx.client_capabilities
        if (
            not caps
            or caps.elicitation is None
            or (caps.elicitation.form is None and caps.elicitation.url is not None)
        ):
            raise ToolError("This operation requires human form elicitation")
        binding = json.dumps(command, sort_keys=True)
        message = (
            "Approve this one operation?\n"
            + binding
            + "\nApproval expires in two minutes and authorizes one execution."
        )
        schema = {
            "type": "object",
            "properties": {
                "confirm": {
                    "type": "boolean",
                    "title": "Approve this operation",
                    "default": False,
                }
            },
            "required": ["confirm"],
        }
        if ctx.protocol_version and ctx.protocol_version < "2026-07-28":
            async with asyncio.timeout(120):
                answer = await ctx.session.elicit_form(
                    message, schema, related_request_id=ctx.request_context.request_id
                )
        else:
            now = time.monotonic()
            self.pending = {
                key: value for key, value in self.pending.items() if value[1] > now
            }
            if not ctx.request_state:
                if ctx.input_responses or len(self.pending) >= 64:
                    raise ToolError(
                        "Unexpected response or too many pending confirmations"
                    )
                key = secrets.token_urlsafe(32)
                self.pending[key] = (binding, now + 120)
                return InputRequiredResult(
                    request_state=key,
                    input_requests={
                        "approval": ElicitRequest(
                            params=ElicitRequestFormParams(
                                message=message, requested_schema=schema
                            )
                        )
                    },
                )
            pending = self.pending.pop(ctx.request_state, None)
            if pending is None or pending[0] != binding:
                raise ToolError(
                    "Confirmation expired, was reused, or operation changed"
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
        return result(await apply())


class Tools:
    def __init__(self, controller):
        self.controller = controller
        self.confirmation = Confirmation()

    async def targets(self) -> dict[str, object]:
        """List registered VM targets, the Nix executor and current tasks."""
        ctl = self.controller
        return {
            "vms": [
                {
                    "id": key,
                    "uuid": vm.target,
                    "baseline": vm.profile["baseline"],
                    "interruptible": vm.profile.get("interruptible", False),
                }
                for key, vm in ctl.vms.items()
            ],
            "nix_enabled": ctl.worker is not None,
            "tasks": [
                {key: task[key] for key in ("id", "kind", "state", "cleanup_confirmed")}
                for task in ctl.tasks.values()
            ],
        }

    async def host_info(self) -> dict[str, object]:
        """Read virtualization version and host CPU/memory summaries."""
        ctl = self.controller
        return (
            await next(iter(ctl.vms.values())).host_info()
            if ctl.vms
            else {"nix_enabled": ctl.worker is not None}
        )

    async def vm_info(self, target: str) -> dict[str, object]:
        """Read configuration and power state of a registered VirtualBox VM."""
        return await self.controller.vm(target).info()

    async def vm_power(
        self,
        target: str,
        action: Literal["start", "shutdown", "reboot", "poweroff", "reset"],
        ctx: Context,
    ) -> CallToolResult | InputRequiredResult:
        """Operate a registered VM; poweroff and reset require human confirmation."""
        vm = self.controller.vm(target)

        async def apply():
            return await self.controller.power(target, action)

        if action in {"poweroff", "reset"}:
            return await self.confirmation.run(
                {"vm_uuid": vm.target, "power": action}, ctx, apply
            )
        return result(await apply())

    async def modify_vm(
        self, target: str, changes: VMChanges, ctx: Context
    ) -> CallToolResult | InputRequiredResult:
        """Confirm typed VirtualBox configuration changes and return readback differences."""
        vm = self.controller.vm(target)
        arguments = changes.arguments()

        async def apply():
            async with self.controller.gate:
                if self.controller.active():
                    raise Failure("Finish the active task before changing VM settings")
                return await vm.modify(arguments)

        return await self.confirmation.run(
            ["VBoxManage", "modifyvm", vm.target, *arguments], ctx, apply
        )

    async def read_log(
        self,
        target: str,
        index: int = 0,
        offset: int = 0,
        limit: int = 65536,
        tail: bool = False,
        generation: str | None = None,
    ) -> dict[str, object]:
        """Read a bounded page of a registered VM's VirtualBox logs."""
        return await self.controller.vm(target).read_log(
            index, offset, limit, tail, generation
        )

    async def wait_for_state(
        self, target: str, state: str, timeout: float = 30
    ) -> dict[str, object]:
        """Observe a VM power state within the configured shutdown wait limit."""
        if timeout > self.controller.settings["shutdown_timeout"]:
            raise Failure("State wait exceeds shutdown_timeout")
        return await self.controller.vm(target).wait_for_state(state, timeout)

    async def transfer_create(
        self, kind: Literal["file", "cache"] = "file"
    ) -> dict[str, object]:
        """Allocate an HTTP upload URL or a Nix binary cache URL."""
        return self.controller.transfers.create(kind)

    async def task_begin(
        self,
        kind: Literal["windows", "nixos"],
        request_id: str,
        target: str | None = None,
        cache_id: str | None = None,
        drv: str | None = None,
        timeout: float | None = None,
    ) -> dict[str, object]:
        """Queue a test task; retry the same request_id with identical inputs."""
        return await self.controller.begin(
            kind, request_id, target, cache_id, drv, timeout
        )

    async def task_status(self, task_id: str) -> dict[str, object]:
        """Read persistent task, command and cleanup state."""
        return self.controller.task(task_id)

    async def task_log(
        self,
        task_id: str,
        command_id: str | None = None,
        offset: int = 0,
        limit: int = 65536,
    ) -> dict[str, object]:
        """Read task or guest-command output by byte offset."""
        return await self.controller.task_log(task_id, command_id, offset, limit)

    async def task_finish(self, task_id: str) -> dict[str, object]:
        """Collect evidence and gracefully close a Windows task; cancel a running Nix task."""
        return await self.controller.finish(task_id)

    async def task_cancel(self, task_id: str) -> dict[str, object]:
        """Request bounded cleanup; poll task_status for confirmation."""
        return await self.controller.finish(task_id, cancel=True)

    async def guest_upload(
        self, task_id: str, transfer_id: str, destination: str
    ) -> dict[str, object]:
        """Copy an uploaded file to an absolute path in the ready Windows guest."""
        return await self.controller.upload(task_id, transfer_id, destination)

    async def guest_exec(
        self,
        task_id: str,
        request_id: str,
        argv: list[str],
        cwd: str | None = None,
        env: dict[str, str] | None = None,
        timeout: float | None = None,
    ) -> dict[str, object]:
        """Start one supervised guest command; poll task_status for its actual exit code."""
        return await self.controller.exec_guest(
            task_id, request_id, argv, cwd, env, timeout
        )

    async def guest_collect(
        self, task_id: str, request_id: str, guest_path: str
    ) -> dict[str, object]:
        """Archive a guest file/directory in the guest and register it as an artifact."""
        return await self.controller.collect(task_id, request_id, guest_path)

    async def screenshot(self, task_id: str) -> dict[str, object]:
        """Capture the task's VM display and return an artifact download URL."""
        return await self.controller.screenshot(task_id)

    async def artifacts(
        self, task_id: str, root: str | None = None, path: str = ""
    ) -> dict[str, object]:
        """List task artifacts or one directory within a recorded Nix output root."""
        return await self.controller.outputs(task_id, root, path)

    async def artifact_download(
        self, task_id: str, root: str, path: str
    ) -> dict[str, object]:
        """Export a regular file from a recorded Nix output root to an HTTP URL."""
        return await self.controller.download(task_id, root, path)


def make_server(controller):
    tools = Tools(controller)
    methods = {
        name: method
        for name, method in inspect.getmembers(tools, inspect.ismethod)
        if not name.startswith("_")
    }
    allowed = {
        name: set(inspect.signature(method).parameters) - {"ctx"}
        for name, method in methods.items()
    }

    async def strict(ctx, call_next):
        if ctx.method == "tools/call" and isinstance(ctx.params, dict):
            name = ctx.params.get("name")
            args = ctx.params.get("arguments", {})
            if name in allowed and isinstance(args, dict) and set(args) - allowed[name]:
                return CallToolResult(
                    is_error=True,
                    content=[TextContent(type="text", text="Unknown tool arguments")],
                )
        response = await call_next(ctx)
        if ctx.method == "tools/list" and isinstance(response, dict):
            for tool in response["tools"]:
                tool["inputSchema"]["additionalProperties"] = False
        return response

    server = MCPServer(
        "vm-test-mcp",
        version="0.2.0",
        log_level="WARNING",
        middleware=[strict],
        instructions="Use registered targets and persistent task IDs. NixOS nodes use host KVM; Windows guests use VirtualBox. Observe cleanup_confirmed before switching. Present modify_vm and hard power confirmations to the human.",
    )
    reads = {
        "targets",
        "host_info",
        "vm_info",
        "read_log",
        "wait_for_state",
        "task_status",
        "task_log",
        "artifacts",
    }
    for name, method in methods.items():

        @functools.wraps(method)
        async def guarded(*args, _method=method, **kwargs):
            try:
                return await _method(*args, **kwargs)
            except (Failure, OSError, ValueError, TimeoutError) as error:
                raise ToolError(str(error)) from error

        server.add_tool(
            guarded,
            name=name,
            annotations=ToolAnnotations(
                read_only_hint=name in reads,
                destructive_hint=name not in reads,
                open_world_hint=False,
            ),
            structured_output=name not in {"modify_vm", "vm_power"},
        )
    return server


class Router:
    def __init__(self, app, controller):
        self.app = app
        self.controller = controller

    async def __call__(self, scope, receive, send):
        if scope["type"] != "http":
            return await self.app(scope, receive, send)
        headers = dict(scope["headers"])
        if b"origin" in headers:
            return await reply(
                send, 403, "origin", "Use an MCP or file-transfer client"
            )
        if scope["path"].startswith("/files/"):
            return await self.controller.transfers.http(scope, receive, send)
        token = self.controller.settings["token"]
        if token and not hmac.compare_digest(
            headers.get(b"authorization", b""), b"Bearer " + token.encode("ascii")
        ):
            return await reply(
                send, 401, "unauthorized", "A valid bearer token is required"
            )
        return await self.app(scope, receive, send)


def make_app(controller):
    host = controller.settings["host"]
    formatted = f"[{host}]" if ":" in host else host
    security = TransportSecuritySettings(
        allowed_hosts=[f"{formatted}:*", "localhost:*", "127.0.0.1:*", "[::1]:*"],
        allowed_origins=[],
    )
    app = make_server(controller).streamable_http_app(
        host=host,
        max_request_body_size=65536,
        max_sessions=32,
        session_idle_timeout=300,
        transport_security=security,
    )
    original = app.router.lifespan_context

    @contextlib.asynccontextmanager
    async def lifespan(application):
        await controller.start()
        try:
            async with original(application) as state:
                yield state
        finally:
            await controller.close()

    app.router.lifespan_context = lifespan
    return Router(app, controller)


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--config",
        required=True,
        help="Shared TOML configuration for the controller and Nix worker",
    )
    args = parser.parse_args(argv)
    try:
        config = read_config(args.config)
        settings = config["service"]
        address = ipaddress.ip_address(settings["host"])
        url = urlsplit(settings["public_url"])
        if (
            address.is_unspecified
            or not 1 <= settings["port"] <= 65535
            or url.scheme not in {"http", "https"}
            or not url.netloc
            or url.query
            or url.fragment
            or url.path not in {"", "/"}
        ):
            raise Failure(
                "Use a concrete bind IP, valid port and absolute public_url origin"
            )
        if not settings["token"].isascii():
            raise Failure("token must be ASCII")
        import uvicorn

        uvicorn.run(
            make_app(Controller(config)),
            host=settings["host"],
            port=settings["port"],
            timeout_keep_alive=30,
            timeout_graceful_shutdown=5,
            access_log=False,
        )
    except (Failure, OSError, ValueError) as error:
        logging.getLogger(__name__).error("%s", error)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
