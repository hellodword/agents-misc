#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = ["mcp==2.3.0"]
# ///
"""HTTP MCP for single-layer Windows and NixOS VM testing."""

import argparse
import contextlib
import functools
import hmac
import inspect
import ipaddress
import json
import logging
import sys
from typing import Literal
from urllib.parse import urlsplit

from common import Failure, bounded, read_config
from mcp.server import MCPServer
from mcp.server.transport_security import TransportSecuritySettings
from mcp.types import (
    CallToolResult,
    TextContent,
    ToolAnnotations,
)
from pydantic import BaseModel, ConfigDict, Field
from tasks import Controller, INPUT_BYTES, INPUT_PENDING_BYTES, page
from transfers import reply

VERSION = "0.8.0"


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


class Tools:
    def __init__(self, controller):
        self.controller = controller

    async def targets(self) -> dict[str, object]:
        """List registered VM targets, the Nix executor and current tasks."""
        ctl = self.controller
        return {
            "service": {"name": "vm-test-mcp", "version": VERSION},
            "vms": [
                {
                    "id": key,
                    "uuid": vm.target,
                    "baseline": vm.profile["baseline"],
                    "interruptible": vm.profile.get("interruptible", False),
                    "python": vm.profile["python"],
                    "automation": vm.profile.get("automation", {}),
                    "input_interval_seconds": vm.profile.get(
                        "input_interval_seconds", 1.0
                    ),
                    "modify_vm": vm.profile["modify_vm"],
                    "webcam": vm.profile["webcam"],
                    "capabilities": await ctl.desktop.capabilities(vm),
                }
                for key, vm in ctl.vms.items()
            ],
            "nix_enabled": ctl.worker is not None,
            "limits": {
                "windows_task_seconds": ctl.settings["task_timeout"],
                "nixos_task_seconds": min(
                    ctl.settings["task_timeout"],
                    ctl.config.get("nix", {}).get(
                        "task_timeout", ctl.settings["task_timeout"]
                    ),
                ),
                "command_seconds": "At most the task timeout; execution is bounded by the remaining task deadline",
                "upload_bytes": ctl.settings["upload_bytes"],
                "log_bytes": ctl.settings["log_bytes"],
                "stdin_message_bytes": INPUT_BYTES,
                "stdin_pending_bytes": INPUT_PENDING_BYTES,
                "page_items": 100,
            },
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

    async def webcams(self, target: str) -> dict[str, object]:
        """List host webcams and attachments on a registered VM; IDs select devices."""
        return await self.controller.vm(target).webcams()

    async def vm_webcam(
        self,
        target: str,
        action: Literal["attach", "detach"],
        camera_id: str,
    ) -> dict[str, object]:
        """Attach or detach an enumerated webcam within the target's policy."""
        return await self.controller.webcam(target, action, camera_id)

    async def vm_power(
        self,
        target: str,
        action: Literal["start", "shutdown", "reboot", "poweroff"],
        gui: bool = False,
    ) -> dict[str, object]:
        """Start headless unless gui=true; shutdown falls back to poweroff if needed."""
        return await self.controller.power(target, action, gui=gui)

    async def modify_vm(self, target: str, changes: VMChanges) -> dict[str, object]:
        """Apply typed settings within the target's policy and return readback differences."""
        vm = self.controller.vm(target)
        arguments = changes.arguments()
        async with self.controller.gate:
            self.controller.check_policy(
                target, "modify_vm", changes.model_dump(exclude_none=True)
            )
            if self.controller.active():
                raise Failure("Finish the active task before changing VM settings")
            return await vm.modify(arguments)

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
        gui: bool = False,
    ) -> dict[str, object]:
        """Queue a task; Windows starts headless unless gui=true. Retry identical inputs with the same ID."""
        task = await self.controller.begin(
            kind, request_id, target, cache_id, drv, timeout, gui=gui
        )
        return self.controller.status(task["id"])

    async def task_status(
        self, task_id: str, command_id: str | None = None
    ) -> dict[str, object]:
        """Poll a compact task outcome and optional command status, without history."""
        return self.controller.status(task_id, command_id)

    async def task_commands(
        self, task_id: str, offset: int = 0, limit: int = 50
    ) -> dict[str, object]:
        """Read a page of command requests and results; next_offset continues history."""
        return self.controller.commands(task_id, offset, limit)

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
        """Collect evidence and power off a Windows task's VM; cancel a running Nix task."""
        async with self.controller.gate:
            await self.controller.finish(task_id)
            return self.controller.status(task_id)

    async def task_cancel(self, task_id: str) -> dict[str, object]:
        """Request bounded cleanup; poll task_status for the observed cleanup state."""
        async with self.controller.gate:
            await self.controller.finish(task_id, cancel=True)
            return self.controller.status(task_id)

    async def guest_upload(
        self, task_id: str, transfer_id: str, destination: str
    ) -> dict[str, object]:
        """Upload while ready or executing; create guest parent directories and verify size."""
        return await self.controller.upload(task_id, transfer_id, destination)

    async def guest_exec(
        self,
        task_id: str,
        request_id: str,
        argv: list[str],
        cwd: str | None = None,
        env: dict[str, str] | None = None,
        timeout: float | None = None,
        stdin: bool = False,
    ) -> dict[str, object]:
        """Start one supervised command; stdin=true enables ordered guest_input messages."""
        command = await self.controller.exec_guest(
            task_id, request_id, argv, cwd, env, timeout, stdin=stdin
        )
        return self.controller.status(task_id, command["id"])["command"]

    async def guest_input(
        self,
        task_id: str,
        command_id: str,
        request_id: str,
        text: str = "",
        eof: bool = False,
    ) -> dict[str, object]:
        """Queue UTF-8 stdin for an existing command; retry the same ID/content; eof closes input."""
        return await self.controller.input(task_id, command_id, request_id, text, eof)

    async def guest_collect(
        self, task_id: str, request_id: str, guest_path: str
    ) -> dict[str, object]:
        """Archive a guest file/directory in the guest and register it as an artifact."""
        command = await self.controller.collect(task_id, request_id, guest_path)
        return self.controller.status(task_id, command["id"])["command"]

    async def screenshot(self, task_id: str, screen: int = 0) -> dict[str, object]:
        """Capture the task's VM display and return an artifact download URL."""
        return await self.controller.screenshot(task_id, screen)

    async def vm_keyboard(
        self,
        task_id: str,
        request_id: str,
        chords: list[list[str]],
        input_interval_seconds: float | None = None,
    ) -> dict[str, object]:
        """Send physical key chords, e.g. [[\"CTRL\",\"ALT\",\"DELETE\"]]; inspect the UI after delivery."""
        from desktop import scancodes

        scancodes(chords)

        async def apply(task, backend):
            return await backend.keyboard(chords)

        return await self.controller.desktop.execute(
            task_id,
            request_id,
            "keyboard",
            {"chords": chords},
            apply,
            input_interval_seconds=input_interval_seconds,
        )

    async def vm_mouse(
        self,
        task_id: str,
        request_id: str,
        action: Literal["move", "click", "drag", "scroll"],
        x: int = 0,
        y: int = 0,
        screen: int = 0,
        end_x: int = 0,
        end_y: int = 0,
        button: Literal["left", "right", "middle"] = "left",
        dz: int = 0,
        dw: int = 0,
        relative: bool = False,
        input_interval_seconds: float | None = None,
    ) -> dict[str, object]:
        """Use screenshot pixels or relative movement; end_x/end_y are drag destination or relative drag delta."""
        bounded(screen, 63, "screen", 0)
        for name, value in (("x", x), ("y", y), ("end_x", end_x), ("end_y", end_y)):
            bounded(value, 32767, name, -32768)
        bounded(dz, 100, "dz", -100)
        bounded(dw, 100, "dw", -100)
        values = dict(
            action=action,
            screen=screen,
            x=x,
            y=y,
            end_x=end_x,
            end_y=end_y,
            button={"left": 1, "right": 2, "middle": 4}[button],
            dz=dz,
            dw=dw,
            relative=relative,
        )

        async def apply(task, backend):
            return await backend.helper(**values)

        return await self.controller.desktop.execute(
            task_id,
            request_id,
            "mouse",
            values,
            apply,
            input_interval_seconds=input_interval_seconds,
        )

    async def vm_display(
        self,
        task_id: str,
        screen: int = 0,
        width: int | None = None,
        height: int | None = None,
        request_id: str | None = None,
    ) -> dict[str, object]:
        """Observe a display or request its resolution and check the actual size."""
        bounded(screen, 63, "screen", 0)
        ctl = self.controller
        if width is None and height is None:
            async with ctl.desktop.lock(task_id):
                task = await ctl.desktop.live(task_id, "display")
                return await ctl.desktop.backend(task).screen(
                    screen, ctl.directory(task_id)
                )
        bounded(width, 16384, "width", 1)
        bounded(height, 16384, "height", 1)

        async def apply(task, backend):
            return await ctl.desktop.display(task, backend, screen, width, height)

        return await ctl.desktop.execute(
            task_id,
            request_id,
            "display",
            dict(screen=screen, width=width, height=height),
            apply,
        )

    async def vm_network_link(
        self,
        task_id: str,
        adapter: int = 1,
        connected: bool | None = None,
        request_id: str | None = None,
    ) -> dict[str, object]:
        """Observe or change the cable connection of an existing virtual NIC."""
        bounded(adapter, 36, "adapter", 1)
        ctl = self.controller

        async def apply(task, backend):
            return await backend.link(adapter, connected)

        if connected is None:
            async with ctl.desktop.lock(task_id):
                task = await ctl.desktop.live(task_id, "network_link")
                return await apply(task, ctl.desktop.backend(task))
        return await ctl.desktop.execute(
            task_id,
            request_id,
            "network_link",
            dict(adapter=adapter, connected=connected),
            apply,
        )

    async def vm_audio(
        self,
        task_id: str,
        input: bool | None = None,
        output: bool | None = None,
        request_id: str | None = None,
    ) -> dict[str, object]:
        """Observe or independently toggle the VM audio input and playback switches."""
        ctl = self.controller

        async def apply(task, backend):
            return await backend.audio(input, output)

        if input is None and output is None:
            async with ctl.desktop.lock(task_id):
                task = await ctl.desktop.live(task_id, "audio")
                return await apply(task, ctl.desktop.backend(task))
        return await ctl.desktop.execute(
            task_id, request_id, "audio", dict(input=input, output=output), apply
        )

    async def guest_sessions(
        self, task_id: str, offset: int = 0, limit: int = 50
    ) -> dict[str, object]:
        """Read Guest Control session/process IDs and status without allocating a guest session."""
        ctl = self.controller
        task, vm = ctl.guest_task(
            task_id, {"preparing", "ready", "executing", "cleanup_unknown"}
        )
        ctl.check_policy(task["target"], "guest_sessions")
        if task.get("vm_uuid") != vm.target or not task.get("vm_started"):
            raise Failure("Wait until this task has started its VM")
        items = await ctl.desktop.backend(task).sessions()
        return {
            **page(items, offset, limit),
            "session_count": sum(x["kind"] == "session" for x in items),
            "process_count": sum(x["kind"] == "process" for x in items),
        }

    async def _capture(
        self, task_id, kind, action, request_id, capture_id, options, offset=0, limit=50
    ):
        ctl = self.controller
        task = ctl.task(task_id)
        if task["kind"] != "windows":
            raise Failure("Captures belong to Windows tasks")
        if action in {"status", "read"}:
            if capture_id is None and action == "status":
                return page(
                    [
                        ctl.captures.describe(c)
                        for c in task.get("captures", {}).values()
                        if c["kind"] == kind
                    ],
                    offset,
                    limit,
                )
            capture = ctl.captures.get(task, capture_id, kind)
            return (
                ctl.captures.read_metrics(task, capture, offset, limit)
                if action == "read"
                else ctl.captures.describe(capture)
            )

        async def apply(task, backend):
            if action == "start":
                defaults = backend.vm.profile[kind]
                resolved = {
                    name: defaults[name] if value is None else value
                    for name, value in options.items()
                }
                return await ctl.captures.start(task, kind, resolved)
            return await ctl.captures.stop(
                task, ctl.captures.get(task, capture_id, kind)
            )

        return await ctl.desktop.execute(
            task_id,
            request_id,
            kind,
            dict(action=action, capture_id=capture_id, **options),
            apply,
        )

    async def vm_packet_capture(
        self,
        task_id: str,
        action: Literal["start", "stop", "status"],
        request_id: str | None = None,
        capture_id: str | None = None,
        adapter: int = 1,
    ) -> dict[str, object]:
        """Trace one enabled virtual NIC into a service-owned PCAP artifact."""
        bounded(adapter, 36, "adapter", 1)
        return await self._capture(
            task_id,
            "packet_capture",
            action,
            request_id,
            capture_id,
            dict(adapter=adapter),
        )

    async def vm_metrics(
        self,
        task_id: str,
        action: Literal["start", "read", "stop", "status"],
        request_id: str | None = None,
        capture_id: str | None = None,
        interval_seconds: int | None = None,
        offset: int = 0,
        limit: int = 50,
    ) -> dict[str, object]:
        """Sample VM metrics on demand; read pages or stop to export timestamped JSONL evidence."""
        if interval_seconds is not None:
            bounded(interval_seconds, 3600, "interval_seconds", 1)
        return await self._capture(
            task_id,
            "metrics",
            action,
            request_id,
            capture_id,
            dict(interval_seconds=interval_seconds),
            offset,
            limit,
        )

    async def artifacts(
        self,
        task_id: str,
        root: str | None = None,
        path: str = "",
        offset: int = 0,
        limit: int = 50,
    ) -> dict[str, object]:
        """List task artifacts or one directory within a recorded Nix output root."""
        return await self.controller.outputs(task_id, root, path, offset, limit)

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
        name: set(inspect.signature(method).parameters)
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
        version=VERSION,
        log_level="WARNING",
        middleware=[strict],
        instructions="Use registered targets, their configured capabilities and persistent task IDs. NixOS nodes use host KVM; Windows guests use VirtualBox. Windows task cleanup powers off the guest; each new task restores its baseline. Observe cleanup_confirmed before switching.",
    )
    reads = {
        "targets",
        "host_info",
        "vm_info",
        "webcams",
        "read_log",
        "wait_for_state",
        "task_status",
        "task_commands",
        "task_log",
        "artifacts",
        "guest_sessions",
    }
    for name, method in methods.items():

        @functools.wraps(method)
        async def guarded(*args, _method=method, **kwargs):
            try:
                return await _method(*args, **kwargs)
            except (Failure, OSError, ValueError, TimeoutError) as error:
                failure = error if isinstance(error, Failure) else Failure(str(error))
                response = result(failure.response())
                response.is_error = True
                return response

        server.add_tool(
            guarded,
            name=name,
            annotations=ToolAnnotations(
                read_only_hint=name in reads,
                destructive_hint=name not in reads,
                open_world_hint=False,
            ),
            structured_output=True,
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
