from __future__ import annotations

import argparse
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[2]
CODEX_ROOT = REPO_ROOT / "codex"
UPSTREAM_FILE = CODEX_ROOT / "upstream.yaml"
PATCH_NAME = "0001-agents-misc-codex-overrides.patch"


class PatchError(RuntimeError):
    pass


def eprint(message: str) -> None:
    print(message, file=sys.stderr)


def json_stdout(payload: dict[str, Any]) -> None:
    print(json.dumps(payload, sort_keys=True))


def safe_ref_name(ref: str) -> str:
    safe = re.sub(r"[^A-Za-z0-9._-]+", "-", ref).strip("-")
    if not safe:
        raise PatchError(f"ref has no filesystem-safe name: {ref!r}")
    return safe


def load_upstream() -> dict[str, Any]:
    if not UPSTREAM_FILE.exists():
        raise PatchError(f"missing upstream metadata: {UPSTREAM_FILE}")

    data: dict[str, Any] = {}
    current_key: str | None = None
    for raw_line in UPSTREAM_FILE.read_text().splitlines():
        line = raw_line.rstrip()
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if not line.startswith(" ") and ":" in line:
            key, value = line.split(":", 1)
            current_key = key
            value = value.strip()
            if value:
                data[key] = value
            else:
                data[key] = []
            continue
        if current_key and stripped.startswith("- "):
            value = stripped[2:].strip()
            if isinstance(data.get(current_key), list):
                data[current_key].append(value)

    for key in ("name", "url", "worktreeRoot", "schemaFile", "patchesRoot"):
        if not data.get(key):
            raise PatchError(f"{UPSTREAM_FILE} is missing {key}")
    return data


def worktree_path(ref: str, upstream: dict[str, Any] | None = None) -> Path:
    upstream = upstream or load_upstream()
    return REPO_ROOT / str(upstream["worktreeRoot"]) / safe_ref_name(ref) / "src"


def patch_dir(ref: str, upstream: dict[str, Any] | None = None) -> Path:
    upstream = upstream or load_upstream()
    return REPO_ROOT / str(upstream["patchesRoot"]) / safe_ref_name(ref)


def schema_file(src: Path, upstream: dict[str, Any] | None = None) -> Path:
    upstream = upstream or load_upstream()
    return src / str(upstream["schemaFile"])


def run(
    args: list[str],
    *,
    cwd: Path | None = None,
    check: bool = True,
    capture: bool = False,
    env: dict[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    eprint(f"+ {' '.join(args)}")
    process_env = os.environ.copy()
    if env:
        process_env.update(env)
    completed = subprocess.run(
        args,
        cwd=cwd,
        check=False,
        text=True,
        env=process_env,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
    )
    if check and completed.returncode != 0:
        if capture and completed.stdout:
            eprint(completed.stdout.rstrip())
        if capture and completed.stderr:
            eprint(completed.stderr.rstrip())
        raise PatchError(f"command failed with exit {completed.returncode}: {' '.join(args)}")
    return completed


def require_git_worktree(src: Path) -> None:
    if not (src / ".git").exists():
        raise PatchError(f"missing upstream checkout: {src}; run codex-fetch first")


def ensure_clean(src: Path) -> None:
    status = run(["git", "status", "--short"], cwd=src, capture=True).stdout.strip()
    if status:
        raise PatchError(f"upstream checkout has local changes:\n{status}")


def ensure_no_staged_changes(src: Path) -> None:
    result = run(["git", "diff", "--cached", "--quiet"], cwd=src, check=False)
    if result.returncode != 0:
        raise PatchError("upstream checkout has staged changes")


def fetch_ref(src: Path, ref: str, upstream: dict[str, Any]) -> str:
    src.parent.mkdir(parents=True, exist_ok=True)
    if not (src / ".git").exists():
        src.mkdir(parents=True, exist_ok=True)
        run(["git", "init"], cwd=src)
        run(["git", "remote", "add", "origin", str(upstream["url"])], cwd=src)

    remote_url = run(["git", "remote", "get-url", "origin"], cwd=src, capture=True).stdout.strip()
    if remote_url != upstream["url"]:
        raise PatchError(f"unexpected origin URL in {src}: {remote_url}")
    ensure_clean(src)

    safe = safe_ref_name(ref)
    candidates: list[tuple[list[str], str]] = []
    if re.fullmatch(r"[0-9a-fA-F]{7,40}", ref):
        candidates.append((["git", "fetch", "--depth=1", "origin", ref], "FETCH_HEAD"))
    if ref.startswith("refs/"):
        local_ref = f"refs/agents-misc/{safe}"
        candidates.append((["git", "fetch", "--depth=1", "origin", f"{ref}:{local_ref}"], local_ref))
    else:
        candidates.append(
            (["git", "fetch", "--depth=1", "origin", f"refs/tags/{ref}:refs/tags/{ref}"], f"refs/tags/{ref}")
        )
        candidates.append(
            (
                ["git", "fetch", "--depth=1", "origin", f"refs/heads/{ref}:refs/remotes/origin/{safe}"],
                f"refs/remotes/origin/{safe}",
            )
        )
        candidates.append((["git", "fetch", "--depth=1", "origin", ref], "FETCH_HEAD"))

    errors: list[str] = []
    for command, checkout_ref in candidates:
        result = run(command, cwd=src, check=False, capture=True)
        if result.returncode == 0:
            run(["git", "checkout", "--detach", checkout_ref], cwd=src)
            return checkout_ref
        errors.append((result.stderr or result.stdout or "").strip())

    raise PatchError(f"could not fetch ref {ref!r}:\n" + "\n".join(error for error in errors if error))


def checkout_ref(src: Path, ref: str) -> str:
    safe = safe_ref_name(ref)
    candidates = [
        f"refs/tags/{ref}",
        f"refs/remotes/origin/{safe}",
        f"refs/agents-misc/{safe}",
        ref,
    ]
    for candidate in candidates:
        result = run(["git", "rev-parse", "--verify", f"{candidate}^{{commit}}"], cwd=src, check=False, capture=True)
        if result.returncode == 0:
            run(["git", "checkout", "--detach", candidate], cwd=src)
            return candidate
    raise PatchError(f"ref is not available in {src}: {ref}; run codex-fetch first")


def read_series(ref: str, upstream: dict[str, Any] | None = None) -> list[Path]:
    directory = patch_dir(ref, upstream)
    series = directory / "series"
    if not series.exists():
        raise PatchError(f"missing patch series: {series}")

    patches: list[Path] = []
    for line_number, raw_line in enumerate(series.read_text().splitlines(), start=1):
        line = raw_line.strip()
        if not line or line.startswith("#"):
            continue
        path = Path(line)
        if path.is_absolute() or ".." in path.parts:
            raise PatchError(f"invalid patch path in {series}:{line_number}: {line}")
        patch = directory / path
        if not patch.exists():
            raise PatchError(f"series entry does not exist: {patch}")
        patches.append(patch)

    if not patches:
        raise PatchError(f"empty patch series: {series}")
    return patches


def apply_series(src: Path, patches: list[Path], *, check_only: bool, index: bool = False) -> None:
    command = ["git", "apply"]
    if check_only:
        command.append("--check")
    if index:
        command.append("--index")
    command.extend(str(patch) for patch in patches)
    run(command, cwd=src)


def check_series_against_index(src: Path, patches: list[Path]) -> None:
    run(["git", "apply", "--cached", "--check", *[str(patch) for patch in patches]], cwd=src)


def jobs_limit() -> int:
    count = os.cpu_count() or 2
    return max(1, count - 1)


def upstream_build_env(src: Path) -> dict[str, str]:
    return {"CARGO_TARGET_DIR": str(src.parent / "target")}


def command_exists(name: str) -> bool:
    return shutil.which(name) is not None


def add_ref_argument(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--ref", required=True, help="Upstream ref and patch directory name")


def main_wrapper(fn: Any) -> int:
    try:
        return fn()
    except PatchError as exc:
        eprint(f"error: {exc}")
        return 1
    except Exception as exc:  # noqa: BLE001
        eprint(f"error: {exc}")
        return 1
