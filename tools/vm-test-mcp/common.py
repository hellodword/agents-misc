"""Shared process, state and configuration primitives (standard library only)."""

import asyncio
import contextlib
import datetime
import fcntl
import json
import math
import os
import re
import secrets
import shutil
import signal
import stat
from pathlib import Path

import tomllib


class Failure(RuntimeError):
    def __init__(self, message, code="operation_failed", **details):
        super().__init__(message)
        self.code = code
        self.details = details

    def response(self):
        return {
            "error": {"code": self.code, "message": str(self), "details": self.details}
        }


ID = re.compile(r"[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}\Z")
STORE_NAME = r"[0-9abcdfghijklmnpqrsvwxyz]{32}-[a-zA-Z0-9+._?=-]+"
DRV = re.compile(r"/nix/store/" + STORE_NAME + r"\.drv\Z")
STORE = re.compile(r"/nix/store/" + STORE_NAME + r"\Z")


def identifier(value):
    if not isinstance(value, str) or not ID.fullmatch(value):
        raise Failure(
            "Use an identifier of 1..80 letters, digits, underscores or hyphens"
        )
    return value


def utc():
    return datetime.datetime.now(datetime.timezone.utc).isoformat()


def atomic_json(path, data):
    with atomic_file(path) as stream:
        stream.write(json.dumps(data, ensure_ascii=False).encode("utf-8"))


def load_json(path):
    with open_file(path) as stream:
        return json.load(stream)


def lock_directory(path):
    with directory_fd(path, create=True) as directory:
        info = os.fstat(directory)
        if info.st_uid != os.geteuid() or info.st_mode & 0o077:
            raise Failure(
                f"State directory must belong to this user with mode 0700: {path}"
            )
        fd = os.open(
            "service.lock",
            os.O_WRONLY | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK,
            0o600,
            dir_fd=directory,
        )
    if not stat.S_ISREG(os.fstat(fd).st_mode):
        os.close(fd)
        raise Failure("Expected a regular service lock")
    handle = os.fdopen(fd, "ab")
    try:
        fcntl.flock(handle, fcntl.LOCK_EX | fcntl.LOCK_NB)
    except BlockingIOError as error:
        handle.close()
        raise Failure("Another service owns this state directory") from error
    return handle


def bounded(value, maximum, name, minimum=0.001):
    if (
        isinstance(value, bool)
        or not isinstance(value, (int, float))
        or not math.isfinite(value)
        or not minimum <= value <= maximum
    ):
        raise Failure(
            f"{name} must be in {minimum}..{maximum}",
            "out_of_range",
            parameter=name,
            minimum=minimum,
            maximum=maximum,
        )
    return value


def window(path, offset=0, limit=65536):
    if offset < 0 or not 1 <= limit <= 262144:
        raise Failure("Require offset >= 0 and limit 1..262144")
    with open_file(path) as stream:
        size = os.fstat(stream.fileno()).st_size
        stream.seek(min(offset, size))
        data = stream.read(limit)
    return {
        "text": data.decode(errors="replace"),
        "offset": min(offset, size),
        "next_offset": min(offset, size) + len(data),
        "total_bytes": size,
    }


@contextlib.contextmanager
def directory_fd(path, *, create=False):
    """Walk directories using held descriptors, including every ancestor."""
    path = Path(path)
    if ".." in path.parts:
        raise Failure("Expected a path without parent traversal")
    flags = os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW
    fd = os.open(path.anchor or ".", flags)
    try:
        for part in path.parts[1:] if path.is_absolute() else path.parts:
            if create:
                with contextlib.suppress(FileExistsError):
                    os.mkdir(part, mode=0o700, dir_fd=fd)
            child = os.open(part, flags, dir_fd=fd)
            os.close(fd)
            fd = child
        yield fd
    finally:
        os.close(fd)


def make_directory(path, *, exist_ok=False):
    path = Path(path)
    with directory_fd(path.parent) as parent:
        try:
            os.mkdir(path.name, mode=0o700, dir_fd=parent)
        except FileExistsError:
            if not exist_ok:
                raise
        fd = os.open(
            path.name, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW, dir_fd=parent
        )
        os.close(fd)


@contextlib.contextmanager
def open_node(path, flags=os.O_RDONLY):
    path = Path(path)
    with directory_fd(path.parent) as parent:
        fd = os.open(
            path.name, flags | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600, dir_fd=parent
        )
    try:
        yield fd
    finally:
        os.close(fd)


@contextlib.contextmanager
def open_file(path, mode="rb"):
    flags = {"rb": os.O_RDONLY, "ab": os.O_WRONLY | os.O_CREAT | os.O_APPEND}[mode]
    with open_node(path, flags) as fd:
        if not stat.S_ISREG(os.fstat(fd).st_mode):
            raise Failure("Expected a regular file")
        with os.fdopen(fd, mode, closefd=False) as stream:
            yield stream


@contextlib.contextmanager
def atomic_file(path):
    path = Path(path)
    with directory_fd(path.parent) as parent:
        temporary = ".write-" + secrets.token_hex(16)
        fd = os.open(
            temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600, dir_fd=parent
        )
        try:
            with os.fdopen(fd, "wb") as stream:
                yield stream
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temporary, path.name, src_dir_fd=parent, dst_dir_fd=parent)
        finally:
            with contextlib.suppress(FileNotFoundError):
                os.unlink(temporary, dir_fd=parent)


@contextlib.contextmanager
def staging_directory(parent):
    """Private names for tools that require host paths instead of file descriptors."""
    with directory_fd(parent) as fd:
        name = ".staging-" + secrets.token_hex(16)
        os.mkdir(name, mode=0o700, dir_fd=fd)
        try:
            yield Path(parent) / name
        finally:
            shutil.rmtree(name, dir_fd=fd)


def remove_tree(path):
    path = Path(path)
    with directory_fd(path.parent) as parent:
        shutil.rmtree(path.name, dir_fd=parent)


async def copy_file(source, destination, limit):
    size = 0
    with open_file(source) as incoming, atomic_file(destination) as outgoing:
        while block := incoming.read(1024 * 1024):
            size += len(block)
            if size > limit:
                raise Failure("File exceeds configured size limit")
            outgoing.write(block)
            await asyncio.sleep(0)
    return size


async def terminate(process, grace=5):
    if process.returncode is None:
        with contextlib.suppress(ProcessLookupError):
            os.killpg(process.pid, signal.SIGTERM)
        try:
            await asyncio.wait_for(process.wait(), grace)
        except TimeoutError:
            with contextlib.suppress(ProcessLookupError):
                os.killpg(process.pid, signal.SIGKILL)
            await asyncio.wait_for(process.wait(), 5)


DEFAULTS = {
    "host": "127.0.0.1",
    "port": 8765,
    "token": "",
    "public_url": "http://127.0.0.1:8765",
    "command_timeout": 30,
    "shutdown_timeout": 120,
    "task_timeout": 3600,
    "upload_bytes": 8 * 1024**3,
    "log_bytes": 64 * 1024**2,
    "retention_days": 7,
    "vboxmanage": "VBoxManage",
}
NIX_DEFAULTS = {
    "socket": "/run/vm-test-nix/worker.sock",
    "nix": "nix",
    "cleanup_timeout": 20,
}
VM_KEYS = {
    "vm",
    "baseline",
    "interruptible",
    "username",
    "password",
    "domain",
    "python",
    "workspace",
    "frontend",
    "startup_env",
    "modify_vm",
    "webcam",
}
VM_NUMERIC_LIMITS = {
    "cpus": (1, 256),
    "memory_mb": (128, 1048576),
    "vram_mb": (1, 256),
}
VM_ALLOWED_VALUES = {
    "graphics_controller": ["vmsvga", "vboxsvga", "vboxvga", "none"],
    "accelerate_3d": [False, True],
    "paravirt_provider": ["default", "legacy", "minimal", "hyperv", "kvm", "none"],
    "nested_paging": [False, True],
    "vpid": [False, True],
}


def policy_table(value, keys, path):
    if not isinstance(value, dict) or value.keys() - keys:
        raise Failure(f"{path} must be a table with keys: {', '.join(sorted(keys))}")
    if "enabled" in value and type(value["enabled"]) is not bool:
        raise Failure(f"{path}.enabled must be a boolean")
    return value


def vm_policy(profile, path):
    """Resolve independent per-VM capability defaults and validate overrides."""
    settings = policy_table(
        profile.get("modify_vm", {}),
        {"enabled"} | VM_NUMERIC_LIMITS.keys() | VM_ALLOWED_VALUES.keys(),
        f"{path}.modify_vm",
    )
    modify = {"enabled": settings.get("enabled", True)}
    default_max = {"cpus": 4, "memory_mb": 16384}
    for name, (minimum, maximum) in VM_NUMERIC_LIMITS.items():
        location = f"{path}.modify_vm.{name}"
        rule = {
            "enabled": True,
            "min": minimum,
            "max": default_max.get(name, maximum),
        } | policy_table(settings.get(name, {}), {"enabled", "min", "max"}, location)
        for bound in ("min", "max"):
            if type(rule[bound]) is not int:
                raise Failure(f"{location}.{bound} must be an integer")
            bounded(rule[bound], maximum, f"{location}.{bound}", minimum)
        if rule["min"] > rule["max"]:
            raise Failure(f"{location}.min must not exceed {location}.max")
        modify[name] = rule
    for name, choices in VM_ALLOWED_VALUES.items():
        location = f"{path}.modify_vm.{name}"
        rule = {"enabled": True, "allowed": list(choices)} | policy_table(
            settings.get(name, {}), {"enabled", "allowed"}, location
        )
        if not isinstance(rule["allowed"], list) or any(
            type(value) is not type(choices[0]) or value not in choices
            for value in rule["allowed"]
        ):
            raise Failure(f"{location}.allowed must be a list of values from {choices}")
        modify[name] = rule
    webcam = {"enabled": True} | policy_table(
        profile.get("webcam", {}), {"enabled"}, f"{path}.webcam"
    )
    return {"modify_vm": modify, "webcam": webcam}


def read_config(path):
    with open(path, "rb") as stream:
        config = tomllib.load(stream)
    if set(config) - {"service", "vms", "nix"}:
        raise Failure("Unknown top-level configuration section")
    service = config.get("service", {})
    if set(service) - (DEFAULTS.keys() | {"state_dir"}):
        raise Failure("Unknown service configuration key")
    explicit_public_url = "public_url" in service
    service = DEFAULTS | service
    if not explicit_public_url:
        host = service["host"]
        authority = f"[{host}]" if ":" in host else host
        service["public_url"] = f"http://{authority}:{service['port']}"
    if "state_dir" not in service or not Path(service["state_dir"]).is_absolute():
        raise Failure("service.state_dir must be an absolute private directory")
    for name in (
        "command_timeout",
        "shutdown_timeout",
        "task_timeout",
        "upload_bytes",
        "log_bytes",
        "retention_days",
    ):
        bounded(service[name], 2**63 - 1, name)
    vms = config.get("vms", {})
    for name, vm in vms.items():
        identifier(name)
        if (
            set(vm) - VM_KEYS
            or not {"vm", "baseline", "username", "password", "python", "workspace"}
            <= vm.keys()
        ):
            raise Failure(f"Invalid VM registration: {name}")
        if (
            not vm["vm"]
            or vm["vm"].startswith("-")
            or not vm["baseline"]
            or vm["baseline"].startswith("-")
        ):
            raise Failure("VM and baseline must identify registered resources")
        if vm.get("frontend", "gui") not in {"gui", "headless", "separate"}:
            raise Failure("frontend must be gui, headless or separate")
        vm.update(vm_policy(vm, f"vms.{name}"))
    nix = {}
    if "nix" in config:
        settings = config["nix"]
        inherited = {
            "cache_base_url": service["public_url"],
            "task_timeout": service["task_timeout"],
            "log_bytes": service["log_bytes"],
        }
        if set(settings) - (NIX_DEFAULTS.keys() | inherited.keys() | {"state_dir"}):
            raise Failure("Unknown Nix configuration key")
        nix = NIX_DEFAULTS | inherited | settings
        for name in ("state_dir", "socket"):
            if name not in nix or not Path(nix[name]).is_absolute():
                raise Failure(f"nix.{name} must be an absolute path")
        for name in ("task_timeout", "log_bytes", "cleanup_timeout"):
            bounded(nix[name], 2**63 - 1, f"nix.{name}")
    return {"service": service, "vms": vms, "nix": nix}
