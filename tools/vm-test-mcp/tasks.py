"""Persistent, serialized VM test tasks independent of MCP connections."""

import asyncio
import base64
import contextlib
import datetime
import json
import os
import time
from pathlib import Path, PureWindowsPath

from common import (
    Failure,
    atomic_file,
    atomic_json,
    bounded,
    copy_file,
    identifier,
    load_json,
    lock_directory,
    make_directory,
    open_file,
    remove_tree,
    staging_directory,
    utc,
    window,
)
from nix_worker import WorkerClient
from transfers import Transfers
from virtualbox import VirtualBox

FINISHED = {"succeeded", "failed", "cancelled"}
INPUT_BYTES = 16 * 1024
INPUT_PENDING_BYTES = 64 * 1024


def command_failure(command):
    if command["state"] in {"starting", "running"}:
        return None
    reason = command.get("reason")
    if reason in {
        "timeout",
        "output_limit",
        "cancelled",
        "task_finished",
        "task_timeout",
    }:
        return reason
    if command.get("error") or command["state"] == "failed":
        return "executor"
    if command.get("exit_code") != 0:
        return "exit_code"
    return None


def page(items, offset, limit):
    bounded(offset, 2**63 - 1, "offset", 0)
    bounded(limit, 100, "limit", 1)
    selected = items[offset : offset + limit]
    following = offset + len(selected)
    return {
        "items": selected,
        "total": len(items),
        "next_offset": following if following < len(items) else None,
    }


def input_status(command):
    messages = command.get("input_requests", {})
    written = command.get("stdin", {}).get("written_sequence", 0)
    return {
        "enabled": command["request"].get("stdin", False),
        "accepted_sequence": len(messages),
        "written_sequence": written,
        "eof_queued": any(m["eof"] for m in messages.values()),
        "closed": command.get("stdin", {}).get("closed", False),
        "error": command.get("stdin", {}).get("error"),
        "pending_bytes": sum(
            len(m["text"].encode("utf-8"))
            for m in messages.values()
            if m["sequence"] > written
        ),
    }


def command_status(command, detail=False):
    result = {
        key: command.get(key)
        for key in ("id", "state", "exit_code", "reason", "error", "cleanup_confirmed")
    }
    result["failure_kind"] = command_failure(command)
    result["stdin"] = input_status(command)
    if detail:
        result.update(request=command["request"], artifact=command.get("artifact"))
    return result


class Controller:
    def __init__(self, config, vms=None, worker=None):
        self.config = config
        self.settings = config["service"]
        self.root = Path(self.settings["state_dir"])
        self.lock_handle = lock_directory(self.root)
        make_directory(self.root / "tasks", exist_ok=True)
        self.transfers = Transfers(
            self.root / "transfers",
            self.settings["public_url"],
            self.settings["upload_bytes"],
        )
        self.vms = (
            vms
            if vms is not None
            else {
                key: VirtualBox(
                    profile["vm"],
                    self.settings["vboxmanage"],
                    self.settings["command_timeout"],
                    profile,
                )
                for key, profile in config["vms"].items()
            }
        )
        self.worker = worker or (
            WorkerClient(config["nix"]["socket"])
            if config.get("nix", {}).get("socket")
            else None
        )
        self.tasks = {
            path.parent.name: load_json(path)
            for path in (self.root / "tasks").glob("*/state.json")
        }
        self.runners = {}
        self.operations = {}
        self.gate = asyncio.Lock()
        self.loop = None

    def directory(self, key):
        return self.root / "tasks" / identifier(key)

    def task(self, key):
        identifier(key)
        if key not in self.tasks:
            raise Failure("Unknown task")
        return self.tasks[key]

    def status(self, key, command_id=None):
        task = self.task(key)
        commands = list(task["commands"].values())
        failed = [c for c in commands if command_failure(c)]
        kinds = {}
        for command in failed:
            kind = command_failure(command)
            kinds[kind] = kinds.get(kind, 0) + 1
        active = next(
            (c for c in commands if c["state"] in {"starting", "running"}), None
        )
        result = {
            field: task.get(field)
            for field in (
                "id",
                "kind",
                "target",
                "state",
                "cleanup_confirmed",
                "created_at",
                "updated_at",
                "guest_directory",
                "desktop",
            )
        }
        result.update(
            timeout=task["timeout"],
            remaining_seconds=max(0, round(task["deadline"] - time.time(), 1))
            if "deadline" in task and task["state"] not in FINISHED
            else None,
            command_count=len(commands),
            artifact_count=len(task["artifacts"]),
            current_command=command_status(active) if active else None,
            outcome={
                "failed_command_count": len(failed),
                "failed_command_ids": [c["id"] for c in failed[:5]],
                "command_failure_kinds": kinds,
                "failure": task.get("failure"),
                "stop_reason": task.get("stop_reason"),
                "cleanup_error": task.get("cleanup_error"),
            },
        )
        if task["kind"] == "nixos":
            worker = task.get("worker", {})
            result["worker"] = {
                field: worker.get(field)
                for field in ("state", "phase", "exit_code", "failure_kind", "error")
            }
        if command_id is not None:
            if command_id not in task["commands"]:
                raise Failure(
                    "Unknown guest command", "unknown_command", command_id=command_id
                )
            result["command"] = command_status(task["commands"][command_id])
        return result

    def commands(self, key, offset=0, limit=50):
        return page(
            [
                command_status(c, detail=True)
                for c in self.task(key)["commands"].values()
            ],
            offset,
            limit,
        )

    def fail(self, task, phase, error, cleanup=False):
        task["cleanup_error" if cleanup else "failure"] = {
            "kind": "cleanup" if cleanup else "executor",
            "phase": phase,
            "message": str(error),
        }
        task["state"] = "cleanup_unknown"
        self.save(task)

    def vm(self, key):
        if key not in self.vms:
            raise Failure("Unknown registered VM")
        return self.vms[key]

    def save(self, task):
        task["updated_at"] = utc()
        atomic_json(self.directory(task["id"]) / "state.json", task)

    def event(self, task, message):
        path = self.directory(task["id"]) / "run.log"
        with open_file(path, "ab") as stream:
            stream.write(
                f"{utc()} {message}\n".encode()[
                    : max(
                        0,
                        self.settings["log_bytes"] - os.fstat(stream.fileno()).st_size,
                    )
                ]
            )

    def active(self):
        return [
            task
            for task in self.tasks.values()
            if task["state"] != "queued" and not task.get("cleanup_confirmed", False)
        ]

    async def start(self):
        for vm in self.vms.values():
            await vm.resolve()
        for task in self.active():
            if task["kind"] == "nixos":
                self.runners[task["id"]] = asyncio.create_task(self.resume_nix(task))
            elif task.get("vm_uuid") != self.vm(task["target"]).target:
                self.fail(task, "recovery", "Registered VM identity changed")
            elif task["state"] in {"ready", "executing"}:
                for operation in task["commands"].values():
                    if operation["state"] in {"starting", "running"}:
                        self.operations[(task["id"], operation["id"])] = (
                            asyncio.create_task(self.watch_command(task, operation))
                        )
            else:
                self.fail(
                    task,
                    "recovery",
                    "Controller restarted during a VM transition; inspect evidence and finish the task",
                )
        self.loop = asyncio.create_task(self.schedule())

    async def close(self):
        jobs = [
            task
            for task in [self.loop, *self.runners.values(), *self.operations.values()]
            if task is not None
        ]
        for job in jobs:
            job.cancel()
        await asyncio.gather(*jobs, return_exceptions=True)
        self.lock_handle.close()

    async def quiesce(self, except_target=None):
        if self.worker:
            status = await self.worker.call("ping")
            if any(not job.get("cleanup_confirmed") for job in status["tasks"]):
                raise Failure("Nix worker reports active or unconfirmed work")
        if self.vms:
            running = set(await next(iter(self.vms.values())).running_vms())
            unknown = running - {vm.target for vm in self.vms.values()}
            if unknown:
                raise Failure(
                    "An unregistered VirtualBox VM is running; the administrator must finish it before switching"
                )
        for key, vm in self.vms.items():
            if key == except_target:
                continue
            info = await vm.info()
            if info["VMState"] == "poweroff":
                continue
            if not vm.profile.get("interruptible", False):
                raise Failure(f"Registered VM {key} is not idle-interruptible")
            await self.shutdown(vm)

    async def shutdown(self, vm):
        if (await vm.info())["VMState"] == "poweroff":
            return
        await vm.power("shutdown")
        observed = await vm.wait_for_state(
            "poweroff", self.settings["shutdown_timeout"]
        )
        if not observed["reached"]:
            raise Failure(
                "Graceful shutdown did not complete; VM occupancy is retained"
            )

    async def begin(
        self, kind, request_id, target=None, cache_id=None, drv=None, timeout=None
    ):
        identifier(request_id)
        maximum = self.settings["task_timeout"]
        if kind == "nixos":
            maximum = min(
                maximum, self.config.get("nix", {}).get("task_timeout", maximum)
            )
        timeout = maximum if timeout is None else timeout
        bounded(timeout, maximum, "timeout")
        if kind == "windows":
            self.vm(target)
            if cache_id is not None or drv is not None:
                raise Failure("Windows tasks use a registered VM target")
        elif kind == "nixos":
            if not self.worker or target is not None or not cache_id or not drv:
                raise Failure(
                    "NixOS tasks require a configured worker, cache_id and drv"
                )
        else:
            raise Failure("Task kind must be windows or nixos")
        request = {
            "kind": kind,
            "target": target,
            "cache_id": cache_id,
            "drv": drv,
            "timeout": timeout,
        }
        async with self.gate:
            if request_id in self.tasks:
                task = self.task(request_id)
                if task["request"] != request:
                    raise Failure(
                        "Request identifier already refers to different inputs"
                    )
                return task
            if kind == "nixos":
                await self.transfers.seal(cache_id, "cache")
            directory = self.directory(request_id)
            make_directory(directory)
            with atomic_file(directory / "run.log"):
                pass
            task = {
                "id": request_id,
                **request,
                "request": request,
                "state": "queued",
                "cleanup_confirmed": False,
                "created_at": utc(),
                "commands": {},
                "artifacts": [],
                "inputs": [],
            }
            self.tasks[request_id] = task
            self.save(task)
            return task

    async def schedule(self):
        last_prune = 0
        while True:
            async with self.gate:
                for task in self.active():
                    runner = self.runners.get(task["id"])
                    if (
                        task["kind"] == "nixos"
                        and task["state"] == "cleanup_unknown"
                        and (runner is None or runner.done())
                    ):
                        self.runners[task["id"]] = asyncio.create_task(
                            self.resume_nix(task)
                        )
                    if task["state"] not in {
                        "finishing",
                        "cleanup_unknown",
                    } and time.time() > task.get("deadline", float("inf")):
                        await self.finish(task["id"], stop_reason="task_timeout")
                if not self.active():
                    queued = next(
                        (
                            task
                            for task in self.tasks.values()
                            if task["state"] == "queued"
                        ),
                        None,
                    )
                    if queued:
                        queued.update(
                            state="preparing", deadline=time.time() + queued["timeout"]
                        )
                        self.save(queued)
                        self.runners[queued["id"]] = asyncio.create_task(
                            self.prepare(queued)
                        )
            if time.monotonic() - last_prune > 3600:
                await self.prune()
                last_prune = time.monotonic()
            await asyncio.sleep(0.5)

    async def prepare(self, task):
        try:
            await self.quiesce()
            if task["kind"] == "nixos":
                status = await self.worker.call(
                    "submit",
                    id=task["id"],
                    drv=task["drv"],
                    cache_id=task["cache_id"],
                    timeout=task["timeout"],
                )
                task.update(state="executing", worker=status)
                self.save(task)
                await self.resume_nix(task)
                return
            vm = self.vm(task["target"])
            task["vm_uuid"] = vm.target
            self.save(task)
            self.event(task, "Restoring registered powered-off baseline")
            await vm.restore()
            self.event(task, "Starting registered Windows VM")
            await vm.power("start")
            self.event(task, "Waiting for Guest Additions desktop readiness")
            await vm.ready(min(task["timeout"], self.settings["shutdown_timeout"]))
            self.event(
                task, "Preparing guest workspace and checking interactive session"
            )
            guest_dir = vm.guest_path(task["id"])
            await vm.python(
                "-c",
                "import pathlib,sys; pathlib.Path(sys.argv[1]).mkdir(parents=True,exist_ok=True)",
                guest_dir,
            )
            await self.send_guest(
                vm,
                Path(__file__).with_name("guest_runner.py"),
                vm.guest_path(task["id"], "guest_runner.py"),
            )
            output, _ = await vm.python(
                vm.guest_path(task["id"], "guest_runner.py"), "probe"
            )
            desktop = json.loads(output)
            if not desktop.get("interactive"):
                raise Failure(
                    "Guest command does not run in an interactive desktop; configure the test user's login session"
                )
            task.update(state="ready", guest_directory=guest_dir, desktop=desktop)
            self.event(task, "Guest and interactive desktop ready")
            self.save(task)
        except (Failure, OSError, ValueError, TimeoutError) as error:
            self.fail(task, "prepare", error)
            self.event(task, str(error))

    async def resume_nix(self, task):
        try:
            while True:
                status = await self.worker.call("status", id=task["id"])
                task["worker"] = status
                task["state"] = (
                    "executing" if status["state"] == "running" else status["state"]
                )
                task["cleanup_confirmed"] = status["cleanup_confirmed"]
                if task.get("failure", {}).get("phase") == "worker":
                    task.pop("failure")
                if status.get("error"):
                    task["failure"] = {
                        "kind": status.get("failure_kind", "executor"),
                        "phase": status.get("phase"),
                        "message": status["error"],
                    }
                    if status.get("failure_kind") in {"cancelled", "task_timeout"}:
                        task.setdefault("stop_reason", status["failure_kind"])
                if (
                    task.get("stop_reason") == "task_timeout"
                    and status["cleanup_confirmed"]
                ):
                    task["state"] = "failed"
                self.save(task)
                if status["state"] in FINISHED:
                    return
                await asyncio.sleep(1)
        except (Failure, OSError, ValueError, TimeoutError) as error:
            self.fail(task, "worker", error)

    def guest_task(self, key, states=("ready",)):
        task = self.task(key)
        if task["kind"] != "windows" or task["state"] not in states:
            active = next(
                (
                    c["id"]
                    for c in task["commands"].values()
                    if c["state"] in {"starting", "running"}
                ),
                None,
            )
            raise Failure(
                "Wait for a Windows task in " + ", ".join(sorted(states)),
                "task_busy" if task["state"] == "executing" else "task_state",
                state=task["state"],
                current_command_id=active,
                allowed_states=sorted(states),
            )
        return task, self.vm(task["target"])

    def input_receipt(self, command, request_id):
        message = command["input_requests"][request_id]
        written = input_status(command)["written_sequence"]
        state = (
            "written"
            if message["sequence"] <= written
            else "unconfirmed"
            if command["state"] not in {"starting", "running"}
            else "accepted"
        )
        return {
            "command_id": command["id"],
            "request_id": request_id,
            "sequence": message["sequence"],
            "state": state,
            "eof": message["eof"],
        }

    async def input(self, key, command_id, request_id, text, eof=False):
        task = self.task(key)
        identifier(request_id)
        if command_id not in task["commands"]:
            raise Failure(
                "Unknown guest command", "unknown_command", command_id=command_id
            )
        command = task["commands"][command_id]
        messages = command.setdefault("input_requests", {})
        if request_id in messages:
            previous = messages[request_id]
            if (previous["text"], previous["eof"]) != (text, eof):
                raise Failure(
                    "Input request identifier refers to different content",
                    "request_conflict",
                )
            return self.input_receipt(command, request_id)
        status = input_status(command)
        if (
            command["state"] not in {"starting", "running"}
            or not status["enabled"]
            or status["eof_queued"]
            or status["closed"]
        ):
            raise Failure(
                "This command has no open input stream",
                "stdin_closed",
                command_id=command_id,
            )
        self.guest_task(key, states={"executing"})
        size = len(text.encode("utf-8"))
        bounded(size, INPUT_BYTES, "input UTF-8 bytes", 0 if eof else 1)
        bounded(
            status["pending_bytes"] + size,
            INPUT_PENDING_BYTES,
            "pending input bytes",
            0,
        )
        messages[request_id] = {
            "sequence": len(messages) + 1,
            "text": text,
            "eof": eof,
            "published": False,
        }
        self.save(task)
        return self.input_receipt(command, request_id)

    async def publish_input(self, task, command):
        vm = self.vm(task["target"])
        for message in list(command.get("input_requests", {}).values()):
            if message["published"]:
                continue
            name = f"input-{message['sequence']:08d}.json"
            directory = self.directory(task["id"]) / ("cmd-" + command["id"])
            atomic_json(
                directory / name, {"text": message["text"], "eof": message["eof"]}
            )
            destination = vm.guest_path(task["id"], "cmd-" + command["id"], name)
            await self.send_guest(vm, directory / name, destination + ".upload")
            await vm.python(
                vm.guest_path(task["id"], "guest_runner.py"),
                "publish",
                destination + ".upload",
                destination,
            )
            message["published"] = True
            self.save(task)

    async def exec_guest(
        self,
        key,
        request_id,
        argv,
        cwd=None,
        env=None,
        timeout=None,
        collect=None,
        stdin=False,
    ):
        task = self.task(key)
        identifier(request_id)
        if not argv or not all(
            isinstance(arg, str) and "\0" not in arg for arg in argv
        ):
            raise Failure("argv must be a nonempty string array")
        env = env or {}
        if any(
            not isinstance(k, str)
            or not isinstance(v, str)
            or any(c in k for c in "=\0")
            or "\0" in v
            for k, v in env.items()
        ):
            raise Failure("Invalid guest environment")
        request = {
            "argv": argv,
            "cwd": cwd,
            "env": env,
            "timeout": task["timeout"] if timeout is None else timeout,
            "collect": collect,
            "stdin": stdin,
        }
        if request_id in task["commands"]:
            operation = task["commands"][request_id]
            if operation["request"] != request:
                raise Failure("Command identifier already refers to different inputs")
            return operation
        self.guest_task(key)
        bounded(request["timeout"], task["timeout"], "command timeout")
        directory = self.directory(key) / ("cmd-" + request_id)
        make_directory(directory)
        command = {
            "id": request_id,
            "request": request,
            "state": "starting",
            "cleanup_confirmed": False,
        }
        task["commands"][request_id] = command
        task["state"] = "executing"
        self.save(task)
        spec = request | {
            "timeout": min(request["timeout"], max(1, task["deadline"] - time.time())),
            "log_bytes": self.settings["log_bytes"],
        }
        atomic_json(directory / "command.json", spec)
        with atomic_file(directory / "run.log"):
            pass
        self.operations[(key, request_id)] = asyncio.create_task(
            self.launch_command(task, command)
        )
        return command

    async def launch_command(self, task, command):
        key, request_id = task["id"], command["id"]
        vm = self.vm(task["target"])
        directory = self.directory(key) / ("cmd-" + request_id)
        try:
            guest_dir = vm.guest_path(key, "cmd-" + request_id)
            await vm.python(
                "-c",
                "import pathlib,sys; pathlib.Path(sys.argv[1]).mkdir(parents=True,exist_ok=True)",
                guest_dir,
            )
            await self.send_guest(
                vm,
                directory / "command.json",
                vm.guest_path(key, "cmd-" + request_id, "command.json"),
            )
            await vm.python(
                vm.guest_path(key, "guest_runner.py"), guest_dir, background=True
            )
            command["state"] = "running"
            self.save(task)
            await self.watch_command(task, command)
        except (Failure, OSError, TimeoutError) as error:
            command.update(state="failed", error=str(error))
            self.fail(task, "launch", error)

    async def send_guest(self, vm, source, destination):
        with staging_directory(self.root) as directory:
            temporary = directory / "input"
            await copy_file(source, temporary, self.settings["upload_bytes"])
            await vm.copy_to(temporary, destination)

    async def fetch_guest(self, vm, source, destination, limit):
        with staging_directory(destination.parent) as directory:
            temporary = directory / "output"
            copying = asyncio.create_task(vm.copy_from(source, temporary))
            try:
                while not copying.done():
                    with contextlib.suppress(FileNotFoundError):
                        with open_file(temporary) as stream:
                            if os.fstat(stream.fileno()).st_size > limit:
                                raise Failure(
                                    "Guest file exceeds configured size limit"
                                )
                    await asyncio.wait({copying}, timeout=0.05)
                await copying
                await copy_file(temporary, destination, limit)
            finally:
                if not copying.done():
                    copying.cancel()
                await asyncio.gather(copying, return_exceptions=True)

    async def watch_command(self, task, operation):
        vm = self.vm(task["target"])
        directory = self.directory(task["id"]) / ("cmd-" + operation["id"])
        try:
            while True:
                try:
                    await self.fetch_guest(
                        vm,
                        vm.guest_path(
                            task["id"], "cmd-" + operation["id"], "status.json"
                        ),
                        directory / "status.json",
                        65536,
                    )
                except Failure:
                    if time.time() > task["deadline"]:
                        raise
                    await asyncio.sleep(1)
                    continue
                status = load_json(directory / "status.json")
                with contextlib.suppress(Failure):
                    await self.fetch_guest(
                        vm,
                        vm.guest_path(task["id"], "cmd-" + operation["id"], "run.log"),
                        directory / "run.log",
                        self.settings["log_bytes"],
                    )
                operation.update(
                    {
                        key: status[key]
                        for key in (
                            "state",
                            "exit_code",
                            "cleanup_confirmed",
                            "reason",
                            "error",
                            "stdin",
                        )
                        if key in status
                    }
                )
                self.save(task)
                if status["state"] != "running":
                    if status.get("cleanup_confirmed"):
                        if (
                            operation["request"].get("collect")
                            and status.get("exit_code") == 0
                        ):
                            local = directory / "artifact.zip"
                            await self.fetch_guest(
                                vm,
                                operation["request"]["collect"],
                                local,
                                self.settings["upload_bytes"],
                            )
                            artifact = await self.transfers.artifact(local, task["id"])
                            task["artifacts"].append(artifact)
                            operation["artifact"] = artifact
                        if task["state"] == "executing":
                            task["state"] = "ready"
                    else:
                        self.fail(
                            task,
                            "command",
                            "Guest process cleanup was not confirmed",
                            cleanup=True,
                        )
                    self.save(task)
                    return
                await self.publish_input(task, operation)
                await asyncio.sleep(1)
        except (Failure, OSError, ValueError, TimeoutError) as error:
            self.fail(task, "command", error)

    async def upload(self, key, transfer_id, destination):
        task, vm = self.guest_task(key, states={"ready", "executing"})
        if not PureWindowsPath(destination).is_absolute() or "\0" in destination:
            raise Failure("Destination must be an absolute guest path")
        await self.transfers.seal(transfer_id, "file")
        if transfer_id not in task.setdefault("inputs", []):
            task["inputs"].append(transfer_id)
            self.save(task)
        source = self.transfers.file(transfer_id)
        with open_file(source) as stream:
            size = os.fstat(stream.fileno()).st_size
        try:
            await vm.python(
                "-c",
                "import pathlib,sys; pathlib.Path(sys.argv[1]).parent.mkdir(parents=True,exist_ok=True)",
                destination,
            )
            await self.send_guest(vm, source, destination)
            result, _ = await vm.python(
                "-c",
                "import pathlib,sys; print(pathlib.Path(sys.argv[1]).stat().st_size)",
                destination,
            )
            if int(result) != size:
                raise Failure("Guest file size differs from uploaded input")
        except (Failure, OSError, ValueError, TimeoutError) as error:
            raise Failure(
                f"Upload to guest path {destination} failed: {error}",
                "guest_upload_failed",
                destination=destination,
            ) from error
        return {"task_id": key, "guest_path": destination, "bytes": size}

    async def collect(self, key, request_id, guest_path):
        _, vm = self.guest_task(key)
        output = vm.guest_path(key, identifier(request_id) + ".zip")
        return await self.exec_guest(
            key,
            request_id,
            [
                vm.profile["python"],
                vm.guest_path(key, "guest_runner.py"),
                "pack",
                guest_path,
                output,
            ],
            collect=output,
        )

    async def screenshot(self, key):
        task, vm = self.guest_task(
            key, states={"ready", "executing", "finishing", "cleanup_unknown"}
        )
        path = self.directory(key) / "screenshot.png"
        with staging_directory(path.parent) as directory:
            temporary = directory / "screenshot.png"
            await vm.screenshot(temporary)
            await copy_file(temporary, path, self.settings["upload_bytes"])
        artifact = await self.transfers.artifact(path, key)
        task["artifacts"].append(artifact)
        self.save(task)
        return artifact

    async def evidence(self, task):
        vm = self.vm(task["target"])
        with contextlib.suppress(Failure, OSError, TimeoutError):
            await self.screenshot(task["id"])
        with contextlib.suppress(Failure, OSError, TimeoutError):
            log = await vm.read_log(tail=True, limit=262144)
            path = self.directory(task["id"]) / "VBox.log"
            with atomic_file(path) as stream:
                stream.write(log["text"].encode("utf-8"))
            task["artifacts"].append(await self.transfers.artifact(path, task["id"]))
            self.save(task)

    async def finish(self, key, cancel=False, stop_reason=None):
        task = self.task(key)
        if task["state"] in FINISHED or task["state"] == "finishing":
            return task
        if task["state"] == "queued":
            task.update(
                state="cancelled", cleanup_confirmed=True, stop_reason="cancelled"
            )
            self.save(task)
            return task
        task["state"] = "finishing"
        if cancel or stop_reason:
            task["stop_reason"] = stop_reason or "cancelled"
        self.save(task)
        prior = self.runners.get(key)

        async def finish_after_observer():
            if prior:
                prior.cancel()
                await asyncio.gather(prior, return_exceptions=True)
            for (owner, _), operation in list(self.operations.items()):
                if owner == key:
                    operation.cancel()
                    await asyncio.gather(operation, return_exceptions=True)
            await self.finish_run(task)

        self.runners[key] = asyncio.create_task(finish_after_observer())
        return task

    async def finish_run(self, task):
        try:
            if task["kind"] == "nixos":
                status = await self.worker.call("cancel", id=task["id"])
                task.update(
                    state=status["state"],
                    worker=status,
                    cleanup_confirmed=status["cleanup_confirmed"],
                )
                if status.get("error"):
                    task["failure"] = {
                        "kind": status.get("failure_kind", "executor"),
                        "phase": status.get("phase"),
                        "message": status["error"],
                    }
                if (
                    task.get("stop_reason") == "task_timeout"
                    and task["cleanup_confirmed"]
                ):
                    task["state"] = "failed"
            else:
                vm = self.vm(task["target"])
                await self.evidence(task)
                if (await vm.info())["VMState"] != "poweroff":
                    if any(
                        not op.get("cleanup_confirmed")
                        for op in task["commands"].values()
                    ):
                        marker = self.directory(task["id"]) / "cancel"
                        with atomic_file(marker):
                            pass
                        with contextlib.suppress(Failure, TimeoutError):
                            await self.send_guest(
                                vm, marker, vm.guest_path(task["id"], "cancel")
                            )
                    await self.shutdown(vm)
                for (key, _), running in list(self.operations.items()):
                    if key == task["id"]:
                        running.cancel()
                        await asyncio.gather(running, return_exceptions=True)
                for op in task["commands"].values():
                    if not op.get("cleanup_confirmed"):
                        if op["state"] in {"starting", "running"}:
                            op.update(
                                state="finished",
                                reason=task.get("stop_reason") or "task_finished",
                            )
                        op["cleanup_confirmed"] = True
                        if op["request"].get("stdin"):
                            op.setdefault("stdin", {})["closed"] = True
                failed = task.get("failure") or any(
                    command_failure(op) for op in task["commands"].values()
                )
                task.update(
                    state="cancelled"
                    if task.get("stop_reason") == "cancelled"
                    else "failed"
                    if failed or task.get("stop_reason") == "task_timeout"
                    else "succeeded",
                    cleanup_confirmed=True,
                )
            task.pop("cleanup_error", None)
            self.save(task)
        except (Failure, OSError, ValueError, TimeoutError) as error:
            self.fail(task, "shutdown", error, cleanup=True)

    async def task_log(self, key, command_id=None, offset=0, limit=65536):
        task = self.task(key)
        if task["kind"] == "nixos":
            return await self.worker.call("log", id=key, offset=offset, limit=limit)
        if command_id is not None and command_id not in task["commands"]:
            raise Failure("Unknown guest command")
        directory = (
            self.directory(key) / ("cmd-" + command_id)
            if command_id
            else self.directory(key)
        )
        return window(directory / "run.log", offset, limit)

    async def outputs(self, key, root=None, path="", offset=0, limit=50):
        task = self.task(key)
        if root is None:
            return {
                **page(task["artifacts"], offset, limit),
                "nix_outputs": task.get("worker", {}).get("outputs", {}),
            }
        if task["kind"] != "nixos":
            raise Failure("Output roots belong to Nix tasks")
        return await self.worker.call("list", id=key, root=root, path=path)

    async def download(self, key, root, path):
        task = self.task(key)
        if task["kind"] != "nixos":
            raise Failure("Output roots belong to Nix tasks")
        async with self.gate:
            with staging_directory(self.directory(key)) as directory:
                temporary = directory / "output"
                with atomic_file(temporary) as stream:
                    offset = 0
                    while True:
                        chunk = await self.worker.call(
                            "read",
                            id=key,
                            root=root,
                            path=path,
                            offset=offset,
                            limit=262144,
                        )
                        if chunk["size"] > self.settings["upload_bytes"]:
                            raise Failure("Artifact exceeds upload_bytes quota")
                        data = base64.b64decode(chunk["data"], validate=True)
                        stream.write(data)
                        offset = chunk["next_offset"]
                        if offset >= chunk["size"]:
                            break
                        if not data:
                            raise Failure("Artifact changed during download")
                artifact = await self.transfers.artifact(temporary, key)
            task["artifacts"].append(artifact)
            self.save(task)
            return artifact

    async def power(self, target, action):
        async with self.gate:
            vm = self.vm(target)
            occupying = self.active()
            if occupying and not (
                action in {"shutdown", "poweroff", "reset"}
                and all(
                    task["target"] == target and task["state"] == "cleanup_unknown"
                    for task in occupying
                )
            ):
                raise Failure("Finish the active task before changing VM power")
            if action == "start":
                await self.quiesce(except_target=target)
            result = await vm.power(action)
            if action in {"shutdown", "poweroff"}:
                observed = await vm.wait_for_state(
                    "poweroff", self.settings["shutdown_timeout"]
                )
                result["observation"] = observed
                if observed["reached"]:
                    for task in occupying:
                        task.update(
                            state="cancelled",
                            cleanup_confirmed=True,
                            stop_reason="cancelled",
                        )
                        task.pop("cleanup_error", None)
                        for command in task["commands"].values():
                            if not command.get("cleanup_confirmed"):
                                command.update(
                                    state="finished",
                                    reason="cancelled",
                                    cleanup_confirmed=True,
                                )
                        self.save(task)
            return result

    async def prune(self):
        cutoff = datetime.datetime.now(datetime.timezone.utc) - datetime.timedelta(
            days=self.settings["retention_days"]
        )
        for key, task in list(self.tasks.items()):
            if (
                task["state"] in FINISHED
                and task.get("cleanup_confirmed")
                and datetime.datetime.fromisoformat(task["updated_at"]) < cutoff
            ):
                if task["kind"] == "nixos" and task.get("worker"):
                    try:
                        await self.worker.call("retire", id=key)
                    except (Failure, OSError, TimeoutError):
                        continue
                remove_tree(self.directory(key))
                del self.tasks[key]
                self.runners.pop(key, None)
                for operation_key in list(self.operations):
                    if operation_key[0] == key:
                        self.operations.pop(operation_key)
        live = set(self.tasks)
        used_caches = {task.get("cache_id") for task in self.tasks.values()} | {
            key for task in self.tasks.values() for key in task.get("inputs", [])
        }
        for path in self.transfers.root.glob("*/meta.json"):
            meta = load_json(path)
            if (
                meta["task_id"] not in live
                and meta["id"] not in used_caches
                and datetime.datetime.fromisoformat(meta["created_at"]) < cutoff
            ):
                remove_tree(path.parent)
