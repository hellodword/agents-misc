"""Task-owned desktop operations and fixed VirtualBox command adapters."""

import asyncio
import hashlib
import json
import re
import struct
import sys
import time

from common import (
    DESKTOP_CAPABILITIES,
    Failure,
    bounded,
    identifier,
    open_file,
    staging_directory,
    utc,
)
from native_paths import library_directory
from virtualbox import POWERED_OFF_STATES, VirtualBox

LIVE_STATES = {"preparing", "ready", "executing"}
KEYS = {
    "ESC": (0x01,),
    "BACKSPACE": (0x0E,),
    "TAB": (0x0F,),
    "ENTER": (0x1C,),
    "CTRL": (0x1D,),
    "SHIFT": (0x2A,),
    "ALT": (0x38,),
    "SPACE": (0x39,),
    "CAPSLOCK": (0x3A,),
    "WIN": (0xE0, 0x5B),
    "DELETE": (0xE0, 0x53),
    "HOME": (0xE0, 0x47),
    "END": (0xE0, 0x4F),
    "UP": (0xE0, 0x48),
    "DOWN": (0xE0, 0x50),
    "LEFT": (0xE0, 0x4B),
    "RIGHT": (0xE0, 0x4D),
    "PAGEUP": (0xE0, 0x49),
    "PAGEDOWN": (0xE0, 0x51),
    "INSERT": (0xE0, 0x52),
    "MINUS": (0x0C,),
    "EQUAL": (0x0D,),
    "LEFTBRACKET": (0x1A,),
    "RIGHTBRACKET": (0x1B,),
    "SEMICOLON": (0x27,),
    "QUOTE": (0x28,),
    "BACKQUOTE": (0x29,),
    "BACKSLASH": (0x2B,),
    "COMMA": (0x33,),
    "PERIOD": (0x34,),
    "SLASH": (0x35,),
}
for text, start in (
    ("1234567890", 0x02),
    ("QWERTYUIOP", 0x10),
    ("ASDFGHJKL", 0x1E),
    ("ZXCVBNM", 0x2C),
):
    KEYS.update({key: (start + i,) for i, key in enumerate(text)})
KEYS.update({f"F{i + 1}": (0x3B + i,) for i in range(10)})
KEYS.update({"F11": (0x57,), "F12": (0x58,)})


def scancodes(chords):
    if not 1 <= len(chords) <= 64:
        raise Failure("Use 1..64 key chords")
    result, releases = [], []
    for chord in chords:
        if not 1 <= len(chord) <= 8 or len(set(chord)) != len(chord):
            raise Failure("Each chord needs 1..8 distinct named keys")
        names = [name.upper() for name in chord]
        if len(set(names)) != len(names):
            raise Failure("Each chord needs distinct named keys")
        if any(name not in KEYS for name in names):
            raise Failure("Unknown physical key; use a named key", keys=sorted(KEYS))
        for name in names:
            result.extend(KEYS[name])
        for name in reversed(names):
            release = [*KEYS[name][:-1], KEYS[name][-1] | 0x80]
            result.extend(release)
            releases.extend(release)
    return [f"{v:02x}" for v in result], [f"{v:02x}" for v in releases]


def png_size(path):
    with open_file(path) as stream:
        header = stream.read(24)
    if (
        len(header) != 24
        or header[:8] != b"\x89PNG\r\n\x1a\n"
        or header[12:16] != b"IHDR"
    ):
        raise Failure("Screenshot is not a PNG with an IHDR")
    width, height = struct.unpack(">II", header[16:24])
    if not width or not height:
        raise Failure("The selected display has no visible framebuffer")
    return {"width": width, "height": height}


class Backend:
    def __init__(self, vm, settings):
        self.vm, self.settings = vm, settings

    async def control(self, *args):
        return await self.vm.mutate(["controlvm", self.vm.target, *map(str, args)])

    async def helper(
        self,
        action,
        screen=0,
        x=0,
        y=0,
        end_x=0,
        end_y=0,
        button=0,
        dz=0,
        dw=0,
        relative=False,
    ):
        return await self.native(
            [
                action,
                *map(str, (screen, x, y, end_x, end_y, button, dz, dw, int(relative))),
            ]
        )

    async def native(self, arguments):
        if not self.settings["input_helper"]:
            raise Failure(
                "Configure service.input_helper",
                "capability_unavailable",
            )
        library = library_directory(self.settings)
        runner = VirtualBox(
            self.vm.target, self.settings["input_helper"], self.vm.timeout
        )
        data, total = await runner.command(
            [
                str(library),
                self.vm.target,
                *arguments,
            ]
        )
        if total > 65536:
            raise Failure("Input helper exceeded its response limit")
        try:
            return json.loads(data)
        except ValueError as error:
            raise Failure("Input helper returned an invalid response") from error

    async def screen(self, screen, directory):
        info = await self.vm.info()
        if not 0 <= screen < int(info.get("monitorcount", "1")):
            raise Failure("Select an existing display", "unknown_display")
        with staging_directory(directory) as temporary:
            path = temporary / "display.png"
            await self.vm.screenshot(path, screen)
            return {"screen": screen, **png_size(path)}

    @staticmethod
    def nic(info, adapter):
        if info.get(f"nic{adapter}", "none") in {"none", "null"}:
            raise Failure("Select an enabled VM network adapter", "unknown_adapter")

    async def keyboard(self, chords):
        codes, releases = scancodes(chords)
        try:
            await self.control("keyboardputscancode", *codes)
        finally:
            await self.control("keyboardputscancode", *releases)
        return {"delivered": True, "chords": len(chords)}

    async def release(self, keys, mouse):
        errors = []
        if keys:
            try:
                await self.control("keyboardputscancode", *keys)
            except (Failure, OSError) as error:
                errors.append(str(error))
        if mouse:
            try:
                await self.helper("release")
            except (Failure, OSError) as error:
                errors.append(str(error))
        return errors

    async def link(self, adapter, connected):
        self.nic(await self.vm.info(), adapter)
        if connected is not None:
            await self.control(f"setlinkstate{adapter}", "on" if connected else "off")
        info = await self.vm.info()
        observed = info.get(f"cableconnected{adapter}") == "on"
        return {
            "adapter": adapter,
            "connected": observed,
            "applied": None if connected is None else observed == connected,
        }

    async def audio(self, input, output):
        for name, value in (("audioin", input), ("audioout", output)):
            if value is not None:
                await self.control(name, "on" if value else "off")
        info = await self.vm.info()
        return {
            "input": info.get("audio_in") == "on",
            "output": info.get("audio_out") == "on",
        }

    async def sessions(self):
        data, total = await self.vm.command(
            ["guestcontrol", self.vm.target, "list", "all"]
        )
        if total > 1024 * 1024:
            raise Failure("Guest session diagnostics exceeded the response limit")
        # Names and executable arguments may contain credentials. Only export IDs/status.
        items = []
        for line in data.decode(errors="replace").splitlines():
            match = re.search(r"(?:ID|PID)=(\d+).*?Status=\[([^]]+)\]", line)
            if match and ("Session #" in line or "Process #" in line):
                items.append(
                    {
                        "kind": "process" if "PID=" in line else "session",
                        "id": int(match[1]),
                        "status": match[2],
                    }
                )
        return items

    async def trace(self, adapter):
        data, total = await self.vm.command(["showvminfo", self.vm.target])
        if total > 1024 * 1024:
            raise Failure("VM information exceeded the response limit")
        match = re.search(
            rf"^NIC {adapter}:.*?Trace: (on|off) \(file: (.*?)\), Type:",
            data.decode(errors="replace"),
            re.MULTILINE,
        )
        if not match:
            raise Failure("Cannot observe the selected NIC's trace state")
        return {"active": match[1] == "on", "filename": match[2]}


class Desktop:
    def __init__(self, controller):
        self.ctl = controller
        self.locks = {}
        # Monotonic timestamps cannot survive a process restart. A recovered
        # task waits one full interval before its first new native action.
        self.input_finished = {key: None for key in controller.tasks}

    def lock(self, task_id):
        return self.locks.setdefault(task_id, asyncio.Lock())

    def backend(self, task):
        return Backend(self.ctl.vm(task["target"]), self.ctl.settings)

    async def live(self, task_id, capability):
        task, vm = self.ctl.guest_task(task_id, LIVE_STATES)
        if time.time() >= task.get("deadline", 0):
            raise Failure("Task deadline reached")
        if task.get("vm_uuid") != vm.target or not task.get("vm_started"):
            raise Failure("Wait until this task has started its VM", "task_state")
        self.ctl.check_policy(task["target"], capability)
        if (await vm.info())["VMState"] != "running":
            raise Failure("The task's VM is not running", "task_state")
        return task

    async def capabilities(self, vm):
        result = {
            name: {
                "policy": vm.profile[name],
                "available": None,
                "reason": "Requires a running task",
            }
            for name in DESKTOP_CAPABILITIES
        }
        try:
            info = await vm.info()
            running = info["VMState"] == "running"
            has_nic = any(
                re.fullmatch(r"nic\d+", k) and v not in {"none", "null"}
                for k, v in info.items()
            )
            for name, entry in result.items():
                entry.update(
                    available=running and entry["policy"]["enabled"],
                    reason=None if running else "VM is not running",
                )
                if not entry["policy"]["enabled"]:
                    entry["reason"] = "Disabled by configuration"
            for name, supported in (
                ("audio", info.get("audio", "none") != "none"),
                ("network_link", has_nic),
                ("packet_capture", has_nic),
            ):
                if running and not supported:
                    result[name].update(
                        available=False,
                        reason="Required VM device or backend is unavailable",
                    )
            if running and vm.profile["mouse"]["enabled"]:
                try:
                    details = await Backend(vm, self.ctl.settings).helper("probe")
                    result["mouse"]["details"] = details
                    result["mouse"]["available"] = bool(
                        details.get("absolute_supported")
                        or details.get("relative_supported")
                    )
                except (Failure, OSError, TimeoutError) as error:
                    result["mouse"].update(available=False, reason=str(error))
        except (Failure, OSError) as error:
            for entry in result.values():
                entry.update(available=False, reason=str(error))
        return result

    async def execute(
        self,
        task_id,
        request_id,
        capability,
        values,
        action,
        *,
        input_interval_seconds=None,
    ):
        identifier(request_id)
        native = capability in {"keyboard", "mouse"}
        if input_interval_seconds is not None:
            bounded(
                input_interval_seconds, sys.float_info.max, "input_interval_seconds", 0
            )
            values = {**values, "input_interval_seconds": input_interval_seconds}
        fingerprint = hashlib.sha256(
            json.dumps([capability, values], sort_keys=True).encode()
        ).hexdigest()

        task = self.ctl.task(task_id)
        receipts = task.setdefault("desktop_requests", {})

        def existing():
            if request_id in receipts:
                receipt = receipts[request_id]
                if receipt["fingerprint"] != fingerprint:
                    raise Failure(
                        "Request identifier already refers to different inputs"
                    )
                return self.receipt(receipt)

        # Completed receipts remain readable even after task cleanup.
        prior = existing()
        if prior and not self.lock(task_id).locked():
            return prior
        interval = input_interval_seconds
        if native and interval is None:
            interval = self.ctl.vm(task["target"]).profile.get(
                "input_interval_seconds", 1.0
            )
        started = time.monotonic()
        deadline = started + min(
            self.ctl.settings["command_timeout"],
            task.get("deadline", time.time()) - time.time(),
        )
        receipt = {
            "request_id": request_id,
            "operation": capability,
            "fingerprint": fingerprint,
            "state": "unconfirmed",
            "created_at": utc(),
        }
        if native:
            receipt.update(
                input_interval_seconds=interval,
                waited_seconds=0.0,
                delivery_started=False,
            )
        lock = self.lock(task_id)
        acquired = False
        try:
            async with asyncio.timeout_at(deadline):
                await lock.acquire()
                acquired = True
                if native:
                    receipt["waited_seconds"] = time.monotonic() - started
                prior = existing()
                if prior:
                    return prior
                await self.live(task_id, capability)
                receipts[request_id] = receipt
                self.ctl.save(task)
                if native:
                    previous = self.input_finished.get(task_id, started - interval)
                    delay = (
                        interval
                        if previous is None
                        else max(0, previous + interval - time.monotonic())
                    )
                    wait_start = time.monotonic()
                    try:
                        if delay:
                            await asyncio.sleep(delay)
                    finally:
                        receipt["waited_seconds"] += time.monotonic() - wait_start
                    # Cleanup can begin while input is sleeping.
                    if (
                        task["state"] not in LIVE_STATES
                        or time.time() >= task["deadline"]
                    ):
                        raise Failure("Task stopped while waiting for input")
                if capability == "keyboard":
                    _, releases = scancodes(values["chords"])
                    task["held_keys"] = releases
                elif capability == "mouse":
                    task["mouse_used"] = True
                if native:
                    receipt["delivery_started"] = True
                self.ctl.save(task)
                try:
                    receipt["result"] = await action(task, self.backend(task))
                finally:
                    if native:
                        self.input_finished[task_id] = time.monotonic()
                        task["input_finished_at"] = utc()
                receipt["state"] = "delivered" if native else "completed"
                if capability == "keyboard":
                    task["held_keys"] = []
        except TimeoutError:
            if not acquired and native:
                receipt["waited_seconds"] = time.monotonic() - started
            receipt["error"] = (
                f"{capability} operation exceeded its execution deadline; the outcome is unconfirmed"
            )
        except (Failure, OSError, ValueError) as error:
            # Validation before acceptance keeps its normal tool-error contract.
            if receipts.get(request_id) is not receipt:
                raise
            receipt["error"] = str(error)
        finally:
            try:
                if "error" in receipt or receipts.get(request_id) is receipt:
                    # An identical request may have completed while queued.
                    if request_id not in receipts:
                        receipts[request_id] = receipt
                    receipt["completed_at"] = utc()
                    self.ctl.save(task)
            finally:
                if acquired:
                    lock.release()
        # Another caller can accept this ID while this request is waiting for
        # the lock. Check its inputs even when this caller's wait timed out.
        return existing() or self.receipt(receipt)

    @staticmethod
    def receipt(receipt):
        return {key: value for key, value in receipt.items() if key != "fingerprint"}

    async def cleanup(self, task):
        async with self.lock(task["id"]):
            backend = self.backend(task)
            if not task.get("held_keys") and not task.get("mouse_used"):
                return
            errors = (
                []
                if (await backend.vm.info())["VMState"] in POWERED_OFF_STATES
                else await backend.release(
                    task.get("held_keys", []), task.get("mouse_used", False)
                )
            )
            task["input_cleanup_errors"] = errors
            if not errors:
                task["held_keys"] = []
                task["mouse_used"] = False
            self.ctl.save(task)

    async def display(self, task, backend, screen, width, height):
        directory = self.ctl.directory(task["id"])
        if width is None and height is None:
            return await backend.screen(screen, directory)
        if width is None or height is None:
            raise Failure("Specify both width and height")
        result = {
            "requested": {"screen": screen, "width": width, "height": height},
            "applied": False,
        }
        await backend.screen(screen, directory)
        await backend.control("setvideomodehint", width, height, 32, screen)
        until = min(
            task["deadline"],
            time.time() + min(10, self.ctl.settings["command_timeout"] / 2),
        )
        while True:
            result["observed"] = await backend.screen(screen, directory)
            result["applied"] = result["observed"] == result["requested"]
            if result["applied"] or time.time() >= until:
                break
            await asyncio.sleep(0.25)
        return result
