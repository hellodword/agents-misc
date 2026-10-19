"""Capability-scoped HTTP files and standard Nix binary caches."""

import asyncio
import json
import os
import re
import secrets
from pathlib import Path

from common import (
    STORE_NAME,
    Failure,
    atomic_file,
    atomic_json,
    copy_file,
    load_json,
    make_directory,
    open_file,
    remove_tree,
    utc,
)

HASH = r"[0-9abcdfghijklmnpqrsvwxyz]{32}"
CACHE_INFO = b"StoreDir: /nix/store\nWantMassQuery: 0\nPriority: 50\n"
CACHE_FILE = re.compile(
    r"(?:nix-cache-info|"
    + HASH
    + r"\.narinfo|nar/[a-zA-Z0-9_-]+\.nar(?:\.(?:xz|bz2|zst|gz))?)\Z"
)


class QuotaExceeded(Failure):
    pass


def check_narinfo(data, filename):
    fields = {}
    for line in data.decode("utf-8").splitlines():
        key, separator, value = line.partition(": ")
        if not separator or (key in fields and key != "Sig"):
            raise Failure("Malformed narinfo")
        fields[key] = value
    path = fields.get("StorePath", "")
    if (
        not re.fullmatch(r"/nix/store/" + STORE_NAME, path)
        or Path(path).name[:32] != filename[:32]
    ):
        raise Failure("narinfo StorePath does not match its key")
    url = fields.get("URL", "")
    if not url.startswith("nar/") or not CACHE_FILE.fullmatch(url):
        raise Failure("narinfo URL must name a NAR inside this transfer")
    for name in fields.get("References", "").split() + (
        [fields["Deriver"]]
        if fields.get("Deriver") and fields["Deriver"] != "unknown-deriver"
        else []
    ):
        if not re.fullmatch(STORE_NAME, name):
            raise Failure("Invalid store reference in narinfo")
    return fields


class Transfers:
    def __init__(self, root, public_url, quota):
        self.root = Path(root)
        make_directory(self.root, exist_ok=True)
        self.public_url = public_url.rstrip("/")
        self.quota = quota
        self.locks = {}

    def create(self, kind="file", task_id=None):
        if kind not in {"file", "cache", "artifact"}:
            raise Failure("Unknown transfer kind")
        key = secrets.token_urlsafe(32)
        directory = self.root / key
        make_directory(directory)
        make_directory(directory / "data")
        meta = {
            "id": key,
            "kind": kind,
            "task_id": task_id,
            "sealed": kind == "artifact",
            "created_at": utc(),
            "bytes": len(CACHE_INFO) if kind == "cache" else 0,
        }
        atomic_json(directory / "meta.json", meta)
        if kind == "cache":
            with atomic_file(directory / "data" / "nix-cache-info") as stream:
                stream.write(CACHE_INFO)
        return self.describe(meta)

    def metadata(self, key):
        if not re.fullmatch(r"[a-zA-Z0-9_-]{43}", key):
            raise Failure("Unknown transfer")
        try:
            return load_json(self.root / key / "meta.json")
        except FileNotFoundError as error:
            raise Failure("Unknown transfer") from error

    def describe(self, meta):
        base = f"{self.public_url}/files/{meta['id']}"
        return meta | {"url": base if meta["kind"] == "cache" else base + "/blob"}

    def file(self, key, name="blob"):
        meta = self.metadata(key)
        if meta["kind"] == "cache":
            if not CACHE_FILE.fullmatch(name):
                raise Failure("Unknown cache object")
        elif name != "blob":
            raise Failure("Unknown file object")
        return self.root / key / "data" / name

    def lock(self, key):
        return self.locks.setdefault(key, asyncio.Lock())

    async def seal(self, key, kind):
        async with self.lock(key):
            meta = self.metadata(key)
            if meta["kind"] != kind:
                raise Failure("Transfer kind does not match this operation")
            if kind == "file":
                try:
                    with open_file(self.file(key)):
                        pass
                except FileNotFoundError as error:
                    raise Failure("Upload the file before using it") from error
            meta["sealed"] = True
            atomic_json(self.root / key / "meta.json", meta)
            return meta

    async def artifact(self, source, task_id):
        meta = self.create("artifact", task_id)
        destination = self.file(meta["id"])
        try:
            size = await copy_file(source, destination, self.quota)
            record = self.metadata(meta["id"])
            record["bytes"] = size
            atomic_json(self.root / meta["id"] / "meta.json", record)
            return self.describe(record)
        except BaseException:
            remove_tree(self.root / meta["id"])
            raise

    async def http(self, scope, receive, send):
        """URLs contain an unguessable per-transfer capability, usable by nix copy."""
        try:
            parts = scope["path"].split("/", 3)
            if len(parts) != 4 or parts[1] != "files":
                raise Failure("Unknown transfer path")
            key, name = parts[2:]
            path = self.file(key, name)
            method = scope["method"]
            if method == "PUT":
                async with self.lock(key):
                    meta = self.metadata(key)
                    if meta["sealed"]:
                        return await reply(
                            send, 409, "transfer_sealed", "Transfer is sealed"
                        )
                    make_directory(path.parent, exist_ok=True)
                    size = 0
                    previous = 0
                    try:
                        with open_file(path) as existing:
                            previous = os.fstat(existing.fileno()).st_size
                    except FileNotFoundError:
                        pass
                    metadata_limit = (
                        len(CACHE_INFO)
                        if name == "nix-cache-info"
                        else 65536
                        if name.endswith(".narinfo")
                        else None
                    )
                    metadata = bytearray()
                    base_size = max(0, meta["bytes"] - previous)
                    with atomic_file(path) as stream:
                        async with asyncio.timeout(600):
                            while True:
                                event = await receive()
                                if event["type"] == "http.disconnect":
                                    raise Failure("Upload disconnected")
                                data = event.get("body", b"")
                                size += len(data)
                                if base_size + size > self.quota or (
                                    metadata_limit is not None and size > metadata_limit
                                ):
                                    raise QuotaExceeded(
                                        "Transfer or metadata size limit exceeded"
                                    )
                                if metadata_limit is not None:
                                    metadata.extend(data)
                                stream.write(data)
                                if not event.get("more_body", False):
                                    break
                        if name.endswith(".narinfo"):
                            check_narinfo(metadata, name)
                        elif name == "nix-cache-info" and metadata != CACHE_INFO:
                            raise Failure("The service owns nix-cache-info")
                    meta["bytes"] = base_size + size
                    atomic_json(self.root / key / "meta.json", meta)
                return await reply(send, 200)
            if method not in {"GET", "HEAD"}:
                return await reply(send, 405, "method", "Use GET, HEAD or PUT")
            with open_file(path) as stream:
                length = os.fstat(stream.fileno()).st_size
                await send(
                    {
                        "type": "http.response.start",
                        "status": 200,
                        "headers": [
                            (b"content-type", b"application/octet-stream"),
                            (b"content-length", str(length).encode()),
                        ],
                    }
                )
                if method == "GET":
                    while block := stream.read(65536):
                        await send(
                            {
                                "type": "http.response.body",
                                "body": block,
                                "more_body": True,
                            }
                        )
                await send({"type": "http.response.body", "body": b""})
        except FileNotFoundError:
            await reply(send, 404, "not_found", "Transfer object not found")
        except QuotaExceeded as error:
            await reply(send, 413, "quota", str(error))
        except TimeoutError:
            await reply(send, 408, "timeout", "Upload timed out")
        except (Failure, ValueError, OSError) as error:
            await reply(send, 400, "invalid_transfer", str(error))


async def reply(send, status, code=None, message=None):
    body = (
        json.dumps({"error": {"code": code, "message": message}}).encode()
        if code
        else b""
    )
    await send(
        {
            "type": "http.response.start",
            "status": status,
            "headers": [(b"content-type", b"application/json")],
        }
    )
    await send({"type": "http.response.body", "body": body})
