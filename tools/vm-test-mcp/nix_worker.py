#!/usr/bin/env python3
"""Low-privilege Nix worker. Only its Unix socket is used by the controller."""

import argparse
import asyncio
import base64
import contextlib
import json
import math
import os
import re
import signal
import socket
import stat
import struct
import sys
from pathlib import Path

from common import (
    DRV,
    STORE,
    Failure,
    atomic_file,
    atomic_json,
    bounded,
    directory_fd,
    identifier,
    load_json,
    lock_directory,
    make_directory,
    open_file,
    open_node,
    read_config,
    remove_tree,
    terminate,
    utc,
    window,
)

TERMINAL = {"succeeded", "failed", "cancelled", "cleanup_unknown"}


class Worker:
    def __init__(self, config):
        self.config = config
        self.root = Path(config["state_dir"])
        self.lock_handle = lock_directory(self.root)
        self.jobs = {}
        self.running = {}
        self.processes = {}
        self.cancelled = set()
        self.lock = asyncio.Lock()
        for path in self.root.glob("*/state.json"):
            job = load_json(path)
            if job["state"] not in TERMINAL:
                job.update(
                    state="cleanup_unknown",
                    cleanup_confirmed=False,
                    error="Worker restarted during execution; administrator must verify daemon workers",
                )
                atomic_json(path, job)
            self.jobs[job["id"]] = job

    def save(self, job):
        job["updated_at"] = utc()
        atomic_json(self.root / job["id"] / "state.json", job)

    def get(self, key):
        identifier(key)
        if key not in self.jobs:
            raise Failure("Unknown Nix task")
        return self.jobs[key]

    def environment(self):
        return {
            key: value
            for key, value in os.environ.items()
            if key
            in {
                "PATH",
                "HOME",
                "USER",
                "LOGNAME",
                "LANG",
                "TMPDIR",
                "SSL_CERT_FILE",
                "NIX_SSL_CERT_FILE",
            }
        } | {
            "NIX_REMOTE": "daemon",
            "NIX_CONFIG": "experimental-features = nix-command flakes\n",
        }

    async def execute(self, job, args, capture=False):
        directory = self.root / job["id"]
        process = await asyncio.create_subprocess_exec(
            self.config["nix"],
            *args,
            stdin=asyncio.subprocess.DEVNULL,
            stdout=asyncio.subprocess.PIPE,
            stderr=asyncio.subprocess.PIPE,
            env=self.environment(),
            start_new_session=True,
        )
        self.processes[job["id"]] = process
        job.update(client_pid=process.pid, client_started_at=utc(), phase=args[0])
        self.save(job)
        result = bytearray()

        async def drain(stream, is_result=False):
            with open_file(directory / "run.log", "ab") as log:
                while block := await stream.read(65536):
                    if is_result:
                        if len(result) + len(block) > 1024 * 1024:
                            raise Failure("Nix result exceeds 1 MiB")
                        result.extend(block)
                    else:
                        available = max(
                            0, self.config["log_bytes"] - os.fstat(log.fileno()).st_size
                        )
                        log.write(block[:available])
                        log.flush()

        try:
            code, _, _ = await asyncio.gather(
                process.wait(), drain(process.stdout, capture), drain(process.stderr)
            )
        except BaseException:
            await terminate(process)
            job["client_interrupted"] = True
            raise
        finally:
            self.processes.pop(job["id"], None)
        job["exit_code"] = code
        if code:
            if code < 0:
                job["client_interrupted"] = True
            raise Failure(f"Nix {args[0]} exited {code}; inspect task_log")
        return bytes(result)

    async def submit(self, key, drv, cache_id, timeout):
        identifier(key)
        if not DRV.fullmatch(drv) or not re.fullmatch(r"[a-zA-Z0-9_-]{43}", cache_id):
            raise Failure("Expected a store derivation and service cache identifier")
        bounded(timeout, self.config["task_timeout"], "timeout")
        request = {"drv": drv, "cache_id": cache_id, "timeout": timeout}
        async with self.lock:
            if key in self.jobs:
                job = self.get(key)
                if job.get("request") is None and job["state"] == "cancelled":
                    return job
                if job["request"] != request:
                    raise Failure("Task identifier already refers to different inputs")
                return job
            if any(not job.get("cleanup_confirmed") for job in self.jobs.values()):
                raise Failure("A Nix task still owns virtualization resources")
            directory = self.root / key
            make_directory(directory)
            with atomic_file(directory / "run.log"):
                pass
            job = {
                "id": key,
                "request": request,
                "state": "running",
                "cleanup_confirmed": False,
                "created_at": utc(),
                "outputs": {},
            }
            self.jobs[key] = job
            self.save(job)
            self.running[key] = asyncio.create_task(self.run(job))
            return job

    async def run(self, job):
        key = job["id"]
        request = job["request"]
        began_build = False
        try:
            async with asyncio.timeout(request["timeout"]):
                if key in self.cancelled:
                    raise asyncio.CancelledError
                cache = (
                    self.config["cache_base_url"].rstrip("/")
                    + "/files/"
                    + request["cache_id"]
                )
                await self.execute(
                    job,
                    [
                        "copy",
                        "--from",
                        cache,
                        "--to",
                        "daemon",
                        "--derivation",
                        request["drv"],
                    ],
                )
                if key in self.cancelled:
                    raise asyncio.CancelledError
                began_build = True
                output = await self.execute(
                    job,
                    [
                        "build",
                        "--store",
                        "daemon",
                        "--builders",
                        "",
                        "--out-link",
                        str(self.root / key / "result"),
                        "--json",
                        "--keep-failed",
                        "--timeout",
                        str(math.ceil(request["timeout"])),
                        "-L",
                        request["drv"] + "^*",
                    ],
                    capture=True,
                )
                results = json.loads(output)
                for index, item in enumerate(results):
                    for name, path in item["outputs"].items():
                        if not STORE.fullmatch(path):
                            raise Failure("Unexpected Nix output path")
                        job["outputs"][f"{index}-{identifier(name)}"] = path
                job.update(state="succeeded", cleanup_confirmed=True)
        except asyncio.CancelledError:
            job.update(
                state="cancelled" if not began_build else "cleanup_unknown",
                cleanup_confirmed=not began_build,
                error="Cancellation requested; verify daemon workers before releasing an interrupted build",
            )
        except TimeoutError:
            job.update(
                state="cleanup_unknown" if began_build else "failed",
                cleanup_confirmed=not began_build,
                error="Overall task deadline exceeded",
            )
        except (Failure, OSError, ValueError, KeyError) as error:
            # A normal Nix response includes its builder teardown. Forced client
            # termination does not establish daemon worker cleanup.
            interrupted = key in self.cancelled or job.get("client_interrupted", False)
            job.update(
                state="cleanup_unknown" if interrupted and began_build else "failed",
                cleanup_confirmed=not (interrupted and began_build),
                error=str(error),
            )
        finally:
            self.save(job)
            self.running.pop(key, None)

    async def cancel(self, key):
        identifier(key)
        async with self.lock:
            if key not in self.jobs:
                directory = self.root / key
                make_directory(directory)
                with atomic_file(directory / "run.log"):
                    pass
                job = {
                    "id": key,
                    "request": None,
                    "state": "cancelled",
                    "cleanup_confirmed": True,
                    "created_at": utc(),
                    "outputs": {},
                }
                self.jobs[key] = job
                self.save(job)
            job = self.get(key)
        if key in self.running:
            running = self.running[key]
            self.cancelled.add(key)
            process = self.processes.get(key)
            if process and process.returncode is None:
                with contextlib.suppress(ProcessLookupError):
                    os.killpg(process.pid, signal.SIGINT)
            try:
                await asyncio.wait_for(
                    asyncio.shield(running), self.config["cleanup_timeout"]
                )
            except TimeoutError:
                running.cancel()
                with contextlib.suppress(asyncio.CancelledError):
                    await running
        return job

    def artifact(self, key, root, path):
        job = self.get(key)
        if root not in job["outputs"]:
            raise Failure("Unknown output root")
        base = Path(job["outputs"][root])
        if path == "":
            return base
        relative = Path(path)
        if relative.is_absolute() or not relative.parts or ".." in relative.parts:
            raise Failure("Expected a relative artifact path")
        return base / relative

    async def dispatch(self, request, peer_uid):
        operation = request.pop("operation", None)
        fields = {
            "ping": set(),
            "submit": {"id", "drv", "cache_id", "timeout"},
            "status": {"id"},
            "cancel": {"id"},
            "log": {"id", "offset", "limit"},
            "list": {"id", "root", "path"},
            "read": {"id", "root", "path", "offset", "limit"},
            "reconcile": {"id"},
            "retire": {"id"},
        }
        if operation not in fields or set(request) != fields[operation]:
            raise Failure("Unknown worker operation or fields")
        if operation == "ping":
            return {"tasks": list(self.jobs.values()), "uid": os.getuid()}
        if operation == "submit":
            return await self.submit(
                request["id"], request["drv"], request["cache_id"], request["timeout"]
            )
        key = request["id"]
        identifier(key)
        if operation == "cancel":
            return await self.cancel(key)
        if operation == "retire" and key not in self.jobs:
            return {"retired": key}
        job = self.get(key)
        if operation == "status":
            return job
        if operation == "retire":
            if not job.get("cleanup_confirmed") or key in self.running:
                raise Failure("Only completed, cleaned-up jobs can expire")
            remove_tree(self.root / key)
            del self.jobs[key]
            return {"retired": key}
        if operation == "reconcile":
            if peer_uid != os.getuid() or key in self.running:
                raise Failure(
                    "Reconciliation requires the worker administrator and an inactive job"
                )
            job.update(state="cancelled", cleanup_confirmed=True, reconciled_at=utc())
            self.save(job)
            return job
        if operation == "log":
            return window(
                self.root / key / "run.log", request["offset"], request["limit"]
            )
        path = self.artifact(key, request["root"], request["path"])
        if operation == "list":
            entries = []
            with open_node(path) as fd:
                info = os.fstat(fd)
                if stat.S_ISREG(info.st_mode):
                    return {"kind": "file", "size": info.st_size}
                if not stat.S_ISDIR(info.st_mode):
                    raise Failure("Expected a regular file or directory")
                with os.scandir(fd) as iterator:
                    for index, entry in enumerate(iterator):
                        if index >= 2000:
                            raise Failure(
                                "Directory exceeds 2000 entries; select a narrower output"
                            )
                        mode = entry.stat(follow_symlinks=False).st_mode
                        if stat.S_ISREG(mode) or stat.S_ISDIR(mode):
                            entries.append(
                                {
                                    "name": entry.name,
                                    "kind": "directory"
                                    if stat.S_ISDIR(mode)
                                    else "file",
                                }
                            )
            return {
                "kind": "directory",
                "entries": sorted(entries, key=lambda entry: entry["name"]),
            }
        bounded(request["limit"], 262144, "limit", 1)
        bounded(request["offset"], 2**63 - 1, "offset", 0)
        with open_file(path) as stream:
            size = os.fstat(stream.fileno()).st_size
            stream.seek(request["offset"])
            data = stream.read(request["limit"])
        return {
            "data": base64.b64encode(data).decode(),
            "size": size,
            "next_offset": request["offset"] + len(data),
        }

    async def connection(self, reader, writer):
        try:
            peer = writer.get_extra_info("socket").getsockopt(
                socket.SOL_SOCKET, socket.SO_PEERCRED, 12
            )
            _, uid, _ = struct.unpack("3i", peer)
            async with asyncio.timeout(90):
                line = await reader.readline()
                result = await self.dispatch(json.loads(line), uid)
            response = {"result": result}
        except (Failure, OSError, ValueError, TimeoutError) as error:
            response = {"error": str(error)}
        writer.write(json.dumps(response).encode() + b"\n")
        try:
            await writer.drain()
        finally:
            writer.close()
            await writer.wait_closed()

    async def serve(self):
        path = Path(self.config["socket"])
        with directory_fd(path.parent, create=True):
            pass
        path.unlink(missing_ok=True)
        server = await asyncio.start_unix_server(
            self.connection, path=path, limit=65536
        )
        path.chmod(0o660)
        try:
            async with server:
                await server.serve_forever()
        finally:
            for key in list(self.running):
                await self.cancel(key)
            path.unlink(missing_ok=True)


class WorkerClient:
    def __init__(self, path):
        self.path = path

    async def call(self, operation, **data):
        async with asyncio.timeout(90):
            reader, writer = await asyncio.open_unix_connection(
                self.path, limit=4 * 1024**2
            )
            try:
                writer.write(
                    json.dumps({"operation": operation, **data}).encode() + b"\n"
                )
                await writer.drain()
                reply = json.loads(await reader.readline())
            finally:
                writer.close()
                await writer.wait_closed()
        if "error" in reply:
            raise Failure(reply["error"])
        return reply["result"]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--config",
        required=True,
        help="Shared TOML configuration for the controller and Nix worker",
    )
    parser.add_argument(
        "--reconcile",
        metavar="TASK_ID",
        help="Administrator confirms this task's daemon workers have stopped",
    )
    args = parser.parse_args()
    try:
        config = read_config(args.config)["nix"]
        if not config:
            raise Failure("Enable the [nix] section in the shared configuration")
        if args.reconcile:
            print(
                json.dumps(
                    asyncio.run(
                        WorkerClient(config["socket"]).call(
                            "reconcile", id=args.reconcile
                        )
                    )
                )
            )
        else:
            if os.geteuid() == 0:
                raise Failure(
                    "Run the worker as an ordinary account outside Nix trusted-users"
                )
            worker = Worker(config)
            try:
                asyncio.run(worker.serve())
            finally:
                worker.lock_handle.close()
    except KeyboardInterrupt:
        return 0
    except (Failure, OSError, ValueError) as error:
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
