"""VirtualBox CLI adapter for registered test guests."""

import asyncio
import contextlib
import hashlib
import json
import math
import os
import time
import uuid
from pathlib import Path, PureWindowsPath
from typing import Literal

from common import Failure as ToolError
from common import open_file

MAX_OUTPUT = 1024 * 1024


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
    def __init__(self, target, executable="VBoxManage", timeout=30, profile=None):
        self.target = target
        self.profile = profile or {}
        self.executable = executable
        self.timeout = timeout
        self.mutation_lock = asyncio.Lock()

    async def command(self, arguments, *, offset=0, limit=MAX_OUTPUT, timeout=None):
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
            async with asyncio.timeout(timeout or self.timeout):
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
                + stderr[0]
                .decode("utf-8", errors="replace")
                .strip()
                .replace(self.profile.get("password") or "\x00", "[redacted]")
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

        running describes the VM process; check guest readiness separately.
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
        """start: configured frontend; shutdown/reboot: Guest Additions.

        poweroff cuts power immediately; reset is a hard reboot. Both can lose
        unsaved guest data. Never escalate a graceful operation automatically.
        """
        actions = {
            "shutdown": "shutdown",
            "poweroff": "poweroff",
            "reboot": "reboot",
            "reset": "reset",
        }
        if action == "start":
            info = await self.info()
            if any("SharedFolderPath" in key for key in info):
                raise ToolError(
                    "The registered VM exposes host shared folders; prepare an isolated test VM"
                )
            command = [
                "startvm",
                self.target,
                "--type",
                self.profile.get("frontend", "gui"),
            ]
            for key, value in self.profile.get("startup_env", {}).items():
                command.extend(["--putenv", f"{key}={value}"])
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
            with open_file(path) as log:
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

    def guest_path(self, task_id, *parts):
        return str(PureWindowsPath(self.profile["workspace"], task_id, *parts))

    async def guest(self, operation, *args, timeout=None):
        auth = [
            "--username",
            self.profile["username"],
            "--password",
            self.profile["password"],
        ]
        if self.profile.get("domain"):
            auth += ["--domain", self.profile["domain"]]
        return await self.command(
            ["guestcontrol", self.target, operation, *auth, *args], timeout=timeout
        )

    async def python(self, *args, background=False, timeout=None):
        options = [] if background else ["--wait-stdout", "--wait-stderr"]
        return await self.guest(
            "start" if background else "run",
            "--exe",
            self.profile["python"],
            *options,
            "--",
            *args,
            timeout=timeout,
        )

    async def copy_to(self, source, destination):
        await self.guest("copyto", "--target-directory", destination, str(source))

    async def copy_from(self, source, destination):
        await self.guest("copyfrom", "--target-directory", str(destination), source)

    async def ready(self, timeout):
        # Short queries keep readiness independent of Guest Additions event callbacks.
        last = {}
        try:
            async with asyncio.timeout(timeout):
                while True:
                    last = await self.info()
                    if (
                        last["VMState"] == "running"
                        and last.get("GuestAdditionsRunLevel") == "3"
                    ):
                        break
                    await asyncio.sleep(1)
        except TimeoutError as error:
            raise ToolError(
                f"Guest desktop was not ready within {timeout:g}s: "
                f"VMState={last.get('VMState', 'unknown')}, "
                f"GuestAdditionsRunLevel={last.get('GuestAdditionsRunLevel', 'unknown')}"
            ) from error
        await self.python(
            "-c", "import sys; assert sys.version_info >= (3, 11); print(sys.version)"
        )

    async def restore(self):
        await self.mutate(
            ["snapshot", self.target, "restore", self.profile["baseline"]]
        )
        info = await self.info()
        if info["VMState"] != "poweroff":
            raise ToolError("The registered baseline must be a powered-off snapshot")

    async def screenshot(self, destination):
        await self.command(
            ["controlvm", self.target, "screenshotpng", str(destination)]
        )

    async def running_vms(self):
        data, _ = await self.command(["list", "runningvms"])
        return [
            line.rsplit("{", 1)[1].rstrip("}")
            for line in data.decode().splitlines()
            if "{" in line
        ]
