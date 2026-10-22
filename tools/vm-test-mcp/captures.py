"""Persistent task-owned NIC tracing and performance samples."""

import asyncio
import copy
import json
import os
import re
import secrets
import time
from pathlib import Path

from common import Failure, make_directory, open_file, utc
from virtualbox import POWERED_OFF_STATES

METRICS = "CPU/*,RAM/*,Net/*,Disk/*,Guest/*"
ACTIVE = {"starting", "running", "stopping", "unconfirmed"}
KINDS = {"packet_capture", "metrics"}


class Captures:
    def __init__(self, controller):
        self.ctl = controller
        self.watchers = {}

    def directory(self, task, capture, segment=None):
        path = self.ctl.directory(task["id"]) / ("capture-" + capture["id"])
        if segment is not None:
            path /= f"part-{segment['number']:04d}"
        return path

    def get(self, task, capture_id, kind):
        capture = task.get("captures", {}).get(capture_id)
        if not capture or capture["kind"] != kind:
            raise Failure("Unknown capture for this task", "unknown_capture")
        return capture

    def describe(self, capture):
        result = copy.deepcopy(
            {
                key: value
                for key, value in capture.items()
                if key not in {"next_sample", "sample_origin"}
            }
        )
        for segment in result["segments"]:
            for item in segment["files"]:
                item.pop("identity", None)
        return result

    def start_watcher(self, task, capture):
        key = (task["id"], capture["id"])
        self.watchers[key] = asyncio.create_task(self.watch(task, capture))

    async def close(self):
        jobs = list(self.watchers.values())
        for job in jobs:
            job.cancel()
        await asyncio.gather(*jobs, return_exceptions=True)
        self.watchers.clear()

    async def start(self, task, kind, options):
        if kind not in KINDS:
            raise Failure("Unsupported capture kind")
        backend = self.ctl.desktop.backend(task)
        for capture in task.get("captures", {}).values():
            if (
                capture["kind"] == kind
                and capture["state"] in ACTIVE
                and (
                    kind != "packet_capture"
                    or capture["options"]["adapter"] == options["adapter"]
                )
            ):
                raise Failure("A capture already owns this resource", "capture_busy")
        if kind == "packet_capture":
            backend.nic(await backend.vm.info(), options["adapter"])
            if (await backend.trace(options["adapter"]))["active"]:
                raise Failure("An existing trace owns this adapter", "capture_busy")
        capture = {
            "id": secrets.token_hex(12),
            "kind": kind,
            "state": "starting",
            "options": options,
            "created_at": utc(),
            "segments": [],
            "errors": [],
        }
        task.setdefault("captures", {})[capture["id"]] = capture
        make_directory(self.directory(task, capture))
        self.ctl.save(task)
        try:
            if kind == "packet_capture":
                segment = self.segment(task, capture)
                path = self.directory(task, capture, segment) / "traffic.pcap"
                await backend.control(f"nictracefile{options['adapter']}", path)
                await backend.control(f"nictrace{options['adapter']}", "on")
                state = await backend.trace(options["adapter"])
                if not state["active"] or Path(state["filename"]) != path:
                    raise Failure("NIC trace start could not be confirmed")
                segment["started_at"] = utc()
            else:
                self.segment(task, capture)
                await backend.vm.command(
                    [
                        "metrics",
                        "setup",
                        "--period",
                        str(options["interval_seconds"]),
                        "--samples",
                        "1",
                        backend.vm.target,
                        METRICS,
                    ]
                )
                capture["next_sample"] = time.time() + options["interval_seconds"]
            capture["state"] = "running"
            self.identify_files(task, capture)
            self.ctl.save(task)
            self.start_watcher(task, capture)
        except (Failure, OSError, ValueError, TimeoutError) as error:
            capture["state"] = "unconfirmed"
            capture["errors"].append(str(error))
            self.ctl.save(task)
            raise
        return self.describe(capture)

    def segment(self, task, capture):
        segment = {"number": len(capture["segments"]), "started_at": utc(), "files": []}
        make_directory(self.directory(task, capture, segment))
        capture["segments"].append(segment)
        self.ctl.save(task)
        return segment

    def identify_files(self, task, capture):
        for segment in capture["segments"]:
            directory = self.directory(task, capture, segment)
            known = {item["name"]: item for item in segment["files"]}
            for path in directory.iterdir():
                with open_file(path) as stream:
                    info = os.fstat(stream.fileno())
                identity = [info.st_dev, info.st_ino]
                previous = known.get(path.name)
                if previous and previous["identity"] != identity:
                    raise Failure("Capture file was replaced", "artifact_changed")
                if previous is None:
                    previous = {"name": path.name, "identity": identity}
                    segment["files"].append(previous)
                previous["bytes"] = info.st_size

    async def publish(self, task, capture, segment):
        if capture["kind"] not in KINDS:
            raise Failure("Unsupported capture kind")
        self.identify_files(task, capture)
        if not segment["files"]:
            raise Failure("Capture produced no evidence file")
        for item in segment["files"]:
            if "artifact" in item:
                continue
            path = self.directory(task, capture, segment) / item["name"]
            with open_file(path) as stream:
                info = os.fstat(stream.fileno())
                if [info.st_dev, info.st_ino] != item["identity"]:
                    raise Failure("Capture file was replaced", "artifact_changed")
                signature = stream.read(24)
            kind = capture["kind"]
            if kind == "packet_capture" and (
                len(signature) < 24
                or signature[:4]
                not in {
                    b"\xd4\xc3\xb2\xa1",
                    b"\xa1\xb2\xc3\xd4",
                    b"\x4d\x3c\xb2\xa1",
                    b"\xa1\xb2\x3c\x4d",
                }
            ):
                raise Failure("NIC trace is not a PCAP file")
            artifact = await self.ctl.transfers.artifact(path, task["id"])
            artifact.update(
                kind=kind,
                capture_id=capture["id"],
                segment=segment["number"],
                name=item["name"],
            )
            item["artifact"] = artifact
            task["artifacts"].append(artifact)
            self.ctl.save(task)

    async def stop_backend(self, task, capture):
        if capture["kind"] not in KINDS:
            raise Failure("Unsupported capture kind")
        if not capture["segments"]:
            return
        backend = self.ctl.desktop.backend(task)
        info = await backend.vm.info()
        if info["VMState"] in POWERED_OFF_STATES:
            return
        kind = capture["kind"]
        if kind == "packet_capture":
            adapter = capture["options"]["adapter"]
            state = await backend.trace(adapter)
            path = (
                self.directory(task, capture, capture["segments"][-1]) / "traffic.pcap"
            )
            if state["active"]:
                if Path(state["filename"]) != path:
                    raise Failure("Active trace has a different owner")
                await backend.control(f"nictrace{adapter}", "off")
                if (await backend.trace(adapter))["active"]:
                    raise Failure("NIC trace stop could not be confirmed")
        else:
            await backend.vm.command(["metrics", "disable", backend.vm.target, METRICS])

    async def stop(self, task, capture, reason="requested", powered_off=False):
        if capture["state"] not in ACTIVE:
            return self.describe(capture)
        capture.update(state="stopping", stop_reason=reason)
        self.ctl.save(task)
        try:
            if not powered_off:
                await self.stop_backend(task, capture)
        except (Failure, OSError, TimeoutError, asyncio.CancelledError) as error:
            capture["state"] = "unconfirmed"
            capture["errors"].append(
                str(error) or "Capture stop was interrupted before confirmation"
            )
            self.ctl.save(task)
            raise
        capture["stopped_at"] = utc()
        for segment in capture["segments"]:
            segment.setdefault("stopped_at", utc())
            try:
                await self.publish(task, capture, segment)
            except (Failure, OSError, ValueError) as error:
                capture["errors"].append(str(error))
        capture["state"] = "stopped"
        self.ctl.save(task)
        return self.describe(capture)

    async def sample(self, task, capture):
        backend = self.ctl.desktop.backend(task)
        data, total = await backend.vm.command(
            ["metrics", "query", backend.vm.target, METRICS]
        )
        if total > 1024 * 1024:
            raise Failure("Metrics query exceeded the response limit")
        metrics = []
        for line in data.decode(errors="replace").splitlines():
            match = re.search(
                r"\s((?:CPU|RAM|Net|Disk|Guest)/\S+)\s+(-?[\d.]+)\s*(\S*)\s*$", line
            )
            if match:
                metrics.append(
                    {"name": match[1], "value": float(match[2]), "unit": match[3]}
                )
        observed = time.monotonic()
        row = {
            "observed_at": utc(),
            "elapsed_seconds": round(
                observed - capture.setdefault("sample_origin", observed),
                3,
            ),
            "metrics": metrics,
        }
        path = self.directory(task, capture, capture["segments"][0]) / "metrics.jsonl"
        encoded = (json.dumps(row) + "\n").encode()
        with open_file(path, "ab") as stream:
            if os.fstat(stream.fileno()).st_size + len(encoded) > min(
                self.ctl.settings["log_bytes"], self.ctl.settings["upload_bytes"]
            ):
                raise Failure("Metrics evidence limit reached", "capture_limit")
            stream.write(encoded)
        capture["sample_count"] = capture.get("sample_count", 0) + 1
        capture["next_sample"] = time.time() + capture["options"]["interval_seconds"]

    async def watch(self, task, capture):
        try:
            while capture["state"] in ACTIVE:
                await asyncio.sleep(0.5)
                async with self.ctl.desktop.lock(task["id"]):
                    if capture["state"] != "running" or task["state"] not in {
                        "preparing",
                        "ready",
                        "executing",
                    }:
                        continue
                    try:
                        if (
                            capture["kind"] == "metrics"
                            and time.time() >= capture["next_sample"]
                        ):
                            await self.sample(task, capture)
                        self.identify_files(task, capture)
                        if any(
                            f["bytes"] >= self.ctl.settings["upload_bytes"]
                            for s in capture["segments"]
                            for f in s["files"]
                        ):
                            await self.stop(task, capture, "file_limit")
                        elif time.time() >= task["deadline"]:
                            await self.stop(task, capture, "task_deadline")
                        self.ctl.save(task)
                    except (Failure, OSError, ValueError, TimeoutError) as error:
                        capture["errors"].append(str(error))
                        await self.stop(task, capture, "capture_error")
        except (Failure, OSError, ValueError, TimeoutError) as error:
            capture["errors"].append(str(error))
            capture["state"] = "unconfirmed"
            self.ctl.save(task)

    async def cleanup(self, task, powered_off=False):
        jobs = [job for (owner, _), job in self.watchers.items() if owner == task["id"]]
        for job in jobs:
            job.cancel()
        await asyncio.gather(*jobs, return_exceptions=True)
        async with self.ctl.desktop.lock(task["id"]):
            for capture in task.get("captures", {}).values():
                if capture["state"] in ACTIVE:
                    try:
                        async with asyncio.timeout(
                            self.ctl.settings["command_timeout"]
                        ):
                            await self.stop(task, capture, "task_cleanup", powered_off)
                    except (Failure, OSError, ValueError, TimeoutError) as error:
                        capture["state"] = "unconfirmed"
                        capture["errors"].append(
                            str(error) or "Capture cleanup exceeded its deadline"
                        )
                        self.ctl.save(task)

    async def recover(self, task):
        async with self.ctl.desktop.lock(task["id"]):
            for capture in task.get("captures", {}).values():
                if capture["state"] in ACTIVE:
                    try:
                        await self.stop(task, capture, "controller_restart")
                    except (Failure, OSError, ValueError, TimeoutError) as error:
                        capture["errors"].append(str(error))
                        self.ctl.save(task)

    def read_metrics(self, task, capture, offset, limit):
        from tasks import page

        page([], offset, limit)
        if not capture["segments"]:
            return page([], offset, limit)
        path = self.directory(task, capture, capture["segments"][0]) / "metrics.jsonl"
        rows = []
        total = 0
        try:
            with open_file(path) as stream:
                for line in stream:
                    if offset <= total < offset + limit:
                        rows.append(json.loads(line))
                    total += 1
        except FileNotFoundError:
            pass
        return {
            "items": rows,
            "total": total,
            "next_offset": offset + len(rows) if offset + len(rows) < total else None,
        }
