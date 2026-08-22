"""Credential validation, vault locking, atomic updates, and redaction."""

from __future__ import annotations

import contextlib
import fcntl
import os
import re
import stat
from collections.abc import Iterator
from pathlib import Path
from typing import Any

from .common import (
    SCHEMA_VERSION,
    EvalInputError,
    EvalRuntimeError,
    Runtime,
    _atomic_write_bytes,
    _read_json_object,
)


def _validate_owned_regular_file(path: Path, label: str) -> os.stat_result:
    try:
        info = path.lstat()
    except OSError as exc:
        raise EvalInputError(f"cannot inspect {label} {path}: {exc}") from exc
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISREG(info.st_mode):
        raise EvalInputError(f"{label} must be a regular, non-symlink file: {path}")
    if hasattr(os, "getuid") and info.st_uid != os.getuid():
        raise EvalInputError(f"{label} must be owned by the current user: {path}")
    return info


def _validate_private_file(path: Path, label: str) -> dict[str, Any]:
    info = _validate_owned_regular_file(path, label)
    if stat.S_IMODE(info.st_mode) & 0o077:
        raise EvalInputError(
            f"{label} permissions must not grant group or other access: {path}"
        )
    value = _read_json_object(path)
    if not value:
        raise EvalInputError(f"{label} must not be an empty JSON object: {path}")
    return value


def _validate_chatgpt_auth_file(path: Path, label: str) -> dict[str, Any]:
    value = _validate_private_file(path, label)
    if value.get("auth_mode") != "chatgpt":
        raise EvalInputError(f"{label} must use Codex ChatGPT authentication: {path}")
    tokens = value.get("tokens")
    if not isinstance(tokens, dict) or not tokens:
        raise EvalInputError(
            f"{label} must contain a non-empty ChatGPT tokens object: {path}"
        )
    if value.get("OPENAI_API_KEY") not in (None, ""):
        raise EvalInputError(f"{label} must not select API-key authentication: {path}")
    return value


def _ensure_private_dir(path: Path) -> None:
    created = not path.exists()
    try:
        path.mkdir(parents=True, mode=0o700, exist_ok=True)
        info = path.lstat()
    except OSError as exc:
        raise EvalInputError(f"cannot create state directory {path}: {exc}") from exc
    if stat.S_ISLNK(info.st_mode) or not stat.S_ISDIR(info.st_mode):
        raise EvalInputError(f"state directory must be a non-symlink directory: {path}")
    if hasattr(os, "getuid") and info.st_uid != os.getuid():
        raise EvalInputError(
            f"state directory must be owned by the current user: {path}"
        )
    if created:
        path.chmod(0o700)
        info = path.stat()
    if stat.S_IMODE(info.st_mode) & 0o077:
        raise EvalInputError(
            f"state directory permissions must not grant group or other access: {path}"
        )


@contextlib.contextmanager
def _credential_lock(state_dir: Path) -> Iterator[None]:
    flags = os.O_CREAT | os.O_RDWR
    if hasattr(os, "O_NOFOLLOW"):
        flags |= os.O_NOFOLLOW
    try:
        descriptor = os.open(state_dir / "auth.lock", flags, 0o600)
    except OSError as exc:
        raise EvalRuntimeError(f"cannot open credential lock: {exc}") from exc
    try:
        os.fchmod(descriptor, 0o600)
        info = os.fstat(descriptor)
        if not stat.S_ISREG(info.st_mode):
            raise EvalRuntimeError("credential lock must be a regular file")
        if hasattr(os, "getuid") and info.st_uid != os.getuid():
            raise EvalRuntimeError("credential lock must be owned by the current user")
        fcntl.flock(descriptor, fcntl.LOCK_EX)
        yield
    finally:
        fcntl.flock(descriptor, fcntl.LOCK_UN)
        os.close(descriptor)


def _auth_init(source: Path, state_dir: Path, replace: bool) -> dict[str, Any]:
    source = source.expanduser()
    state_dir = state_dir.expanduser()
    _validate_chatgpt_auth_file(source, "source credential file")
    _ensure_private_dir(state_dir)
    destination = state_dir / "auth.json"
    with _credential_lock(state_dir):
        destination_present = os.path.lexists(destination)
        if destination_present and not replace:
            raise EvalInputError(
                f"credential vault already exists at {destination}; pass --replace to replace it"
            )
        if destination_present:
            info = _validate_owned_regular_file(destination, "credential vault")
            if stat.S_IMODE(info.st_mode) & 0o077:
                raise EvalInputError(
                    "credential vault permissions must not grant group or other access: "
                    f"{destination}"
                )
        _atomic_write_bytes(destination, source.read_bytes(), 0o600)
        _validate_chatgpt_auth_file(destination, "credential vault")
    return {
        "schema_version": SCHEMA_VERSION,
        "status": "initialized",
        "credential_vault": str(destination),
        "replaced": destination_present,
    }


def _secret_values(value: Any) -> set[str]:
    found: set[str] = set()
    if isinstance(value, dict):
        for item in value.values():
            found.update(_secret_values(item))
    elif isinstance(value, list):
        for item in value:
            found.update(_secret_values(item))
    elif isinstance(value, str) and len(value) >= 8:
        found.add(value)
    return found


def _redact(text: str, secret_values: set[str], runtime_root: Path) -> str:
    result = text.replace(str(runtime_root), "<isolated-runtime>")
    for value in sorted(secret_values, key=len, reverse=True):
        result = result.replace(value, "<redacted>")
    result = re.sub(
        r"(?i)(authorization\s*:\s*bearer\s+)[^\s]+", r"\1<redacted>", result
    )
    return result


def _copy_vault_to_runtime(vault: Path, runtime: Runtime) -> set[str]:
    credentials = _validate_chatgpt_auth_file(vault, "credential vault")
    destination = runtime.codex_home / "auth.json"
    _atomic_write_bytes(destination, vault.read_bytes(), 0o600)
    return _secret_values(credentials)


def _sync_runtime_auth(runtime: Runtime, vault: Path) -> set[str]:
    source = runtime.codex_home / "auth.json"
    credentials = _validate_chatgpt_auth_file(source, "runtime credential file")
    _atomic_write_bytes(vault, source.read_bytes(), 0o600)
    return _secret_values(credentials)
