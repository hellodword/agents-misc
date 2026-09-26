"""Strict pure-patch maintenance primitives for the pinned Codex checkout."""

from __future__ import annotations

import argparse
import fcntl
import hashlib
import json
import os
import re
import shutil
import shlex
import stat
import subprocess
import sys
import tomllib
from contextlib import contextmanager
from dataclasses import dataclass
from pathlib import Path, PurePosixPath
from typing import Any, Callable, Iterable, Iterator, Mapping, Sequence


DEFAULT_REPO_ROOT = Path(__file__).resolve().parents[2]
CODEX_DIR = "codex"
UPSTREAM_FILE = "upstream.toml"
SERIES_FILE = "series.toml"
PATCHES_DIR = "patches"
REVISION_RE = re.compile(r"[0-9a-f]{40}")
PATCH_FILE_RE = re.compile(r"(?P<number>[0-9]{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.patch")
ENV_ASSIGNMENT_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*=.*", re.DOTALL)
RUST_TEST_LIST_SUMMARY_RE = re.compile(
    r"(?m)^\s*(?P<tests>[0-9]+) tests?, [0-9]+ benchmarks?\s*$"
)
UPSTREAM_FIELDS = {
    "url",
    "ref",
    "revision",
    "worktree",
    "generate_commands",
    "integration_commands",
}
SERIES_FIELDS = {"patch"}
PATCH_FIELDS = {
    "file",
    "intent",
    "behavior",
    "source_files",
    "source_prefixes",
    "generated_files",
    "generated_prefixes",
    "tests",
}
_V8_ENV_CACHE: dict[tuple[Path, str], dict[str, str]] = {}


class PatchError(RuntimeError):
    """A recoverable maintenance-contract failure."""


@dataclass(frozen=True)
class Upstream:
    url: str
    ref: str
    revision: str
    worktree: str
    generate_commands: tuple[tuple[str, ...], ...]
    integration_commands: tuple[tuple[str, ...], ...]


@dataclass(frozen=True)
class PatchSpec:
    file: str
    intent: str
    behavior: str
    source_files: tuple[str, ...]
    source_prefixes: tuple[str, ...]
    generated_files: tuple[str, ...]
    generated_prefixes: tuple[str, ...]
    tests: tuple[tuple[str, ...], ...]

    @property
    def files(self) -> tuple[str, ...]:
        return self.source_files + self.generated_files

    @property
    def prefixes(self) -> tuple[str, ...]:
        return self.source_prefixes + self.generated_prefixes

    def owns(self, path: str) -> bool:
        return path in self.files or any(
            path.startswith(prefix) for prefix in self.prefixes
        )

    def owns_generated(self, path: str) -> bool:
        return path in self.generated_files or any(
            path.startswith(prefix) for prefix in self.generated_prefixes
        )


@dataclass(frozen=True)
class MaintenanceManifest:
    root: Path
    upstream: Upstream
    patches: tuple[PatchSpec, ...]

    @property
    def codex_root(self) -> Path:
        return self.root / CODEX_DIR

    @property
    def worktree(self) -> Path:
        return self.root / self.upstream.worktree

    @property
    def patch_dir(self) -> Path:
        return self.codex_root / PATCHES_DIR / self.upstream.ref

    @property
    def runtime_root(self) -> Path:
        return self.worktree.parent / "maintenance"

    @property
    def maintenance_workspace(self) -> Path:
        return self.runtime_root / "test-workspace"

    @property
    def maintenance_checkout(self) -> Path:
        return self.maintenance_workspace / "src"

    @property
    def install_receipt(self) -> Path:
        return self.runtime_root / "patch-install-receipt.json"

    def owner_for(self, path: str) -> PatchSpec:
        owners = [patch for patch in self.patches if patch.owns(path)]
        if not owners:
            raise PatchError(
                f"changed path has no owner in codex/{SERIES_FILE}: {path}; "
                "declare it as an exact file or explicit directory prefix"
            )
        if len(owners) != 1:
            names = ", ".join(owner.file for owner in owners)
            raise PatchError(
                f"changed path has multiple owners in codex/{SERIES_FILE}: "
                f"{path}: {names}"
            )
        return owners[0]


@dataclass(frozen=True)
class _GeneratedFileState:
    digest: bytes
    mode: int
    mtime_ns: int
    size: int


@dataclass(frozen=True)
class _GeneratedDirectoryState:
    digest: bytes
    mode: int
    mtime_ns: int


def eprint(message: str) -> None:
    print(message, file=sys.stderr)


def json_stdout(payload: Mapping[str, Any]) -> None:
    print(json.dumps(payload, sort_keys=True))


def _strict_fields(
    data: Mapping[str, Any], expected: set[str], source: Path, context: str
) -> None:
    unknown = sorted(set(data) - expected)
    if unknown:
        raise PatchError(
            f"unknown {context} field(s) in {source}: {', '.join(unknown)}"
        )
    missing = sorted(expected - set(data))
    if missing:
        raise PatchError(
            f"missing {context} field(s) in {source}: {', '.join(missing)}"
        )


def _string(value: Any, source: Path, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise PatchError(f"{source}: {field} must be a non-empty string")
    return value


def _command(value: Any, source: Path, field: str) -> tuple[str, ...]:
    if not isinstance(value, list) or not value:
        raise PatchError(
            f"{source}: {field} must be a non-empty array of command arguments"
        )
    return tuple(_string(part, source, f"{field}[]") for part in value)


def _commands(value: Any, source: Path, field: str) -> tuple[tuple[str, ...], ...]:
    if not isinstance(value, list) or not value:
        raise PatchError(f"{source}: {field} must contain at least one command")
    return tuple(_command(command, source, f"{field}[]") for command in value)


def _path(value: Any, source: Path, field: str, *, prefix: bool) -> str:
    raw = _string(value, source, field)
    if "\\" in raw:
        raise PatchError(f"{source}: {field} must use Git '/' separators: {raw}")
    if prefix and not raw.endswith("/"):
        raise PatchError(f"{source}: {field} directory prefix must end with '/': {raw}")
    if not prefix and raw.endswith("/"):
        raise PatchError(f"{source}: {field} exact file must not end with '/': {raw}")
    candidate = raw[:-1] if prefix else raw
    parsed = PurePosixPath(candidate)
    if (
        parsed.is_absolute()
        or candidate.startswith("/")
        or ".." in parsed.parts
        or "." in parsed.parts
    ):
        raise PatchError(f"{source}: {field} must be a contained relative path: {raw}")
    if not parsed.parts or any(not part for part in parsed.parts):
        raise PatchError(f"{source}: {field} is not a valid relative path: {raw}")
    normalized = parsed.as_posix()
    if normalized != candidate:
        raise PatchError(f"{source}: {field} is not normalized: {raw}")
    return normalized + "/" if prefix else normalized


def _paths(value: Any, source: Path, field: str, *, prefix: bool) -> tuple[str, ...]:
    if not isinstance(value, list):
        raise PatchError(f"{source}: {field} must be an array")
    paths = tuple(_path(item, source, f"{field}[]", prefix=prefix) for item in value)
    if len(paths) != len(set(paths)):
        raise PatchError(f"{source}: {field} contains duplicate paths")
    return paths


def _read_toml(path: Path) -> dict[str, Any]:
    if not path.is_file():
        raise PatchError(f"missing maintenance manifest: {path}")
    try:
        with path.open("rb") as handle:
            value = tomllib.load(handle)
    except (OSError, tomllib.TOMLDecodeError) as exc:
        raise PatchError(f"could not parse TOML manifest {path}: {exc}") from exc
    if not isinstance(value, dict):
        raise PatchError(f"TOML manifest must contain a table: {path}")
    return value


def _validate_ownership(patches: Sequence[PatchSpec], source: Path) -> None:
    entries: list[tuple[str, str, str]] = []
    for patch in patches:
        entries.extend((path, "file", patch.file) for path in patch.files)
        entries.extend((path, "prefix", patch.file) for path in patch.prefixes)
    for index, (left, left_kind, left_owner) in enumerate(entries):
        for right, right_kind, right_owner in entries[index + 1 :]:
            overlaps = (
                left == right
                or (left_kind == "prefix" and right.startswith(left))
                or (right_kind == "prefix" and left.startswith(right))
            )
            if overlaps:
                raise PatchError(
                    f"overlapping path ownership in {source}: "
                    f"{left_owner}:{left} and {right_owner}:{right}"
                )


def validate_generated_only_ownership(patch: PatchSpec) -> None:
    if patch.source_files or patch.source_prefixes:
        raise PatchError(
            f"generated-only patch {patch.file} declares handwritten ownership: "
            f"{list(patch.source_files) + list(patch.source_prefixes)}"
        )
    if not patch.generated_files and not patch.generated_prefixes:
        raise PatchError(
            f"generated-only patch {patch.file} declares no generated ownership"
        )


def _split_env_command(command: Sequence[str]) -> tuple[dict[str, str], list[str]]:
    args = list(command)
    overrides: dict[str, str] = {}
    if not args or args[0] != "env":
        return overrides, args
    index = 1
    while index < len(args) and ENV_ASSIGNMENT_RE.fullmatch(args[index]):
        key, value = args[index].split("=", 1)
        overrides[key] = value
        index += 1
    if index == 1 or index == len(args):
        raise PatchError("manifest env command must contain assignments and a command")
    return overrides, args[index:]


def _validate_manifest_commands(manifest: MaintenanceManifest, source: Path) -> None:
    for command in manifest.upstream.generate_commands:
        _, args = _split_env_command(command)
        if args[0] == "just":
            raise PatchError(f"{source}: generators must not invoke upstream Just")
        if args[0] == "cargo" and args[1:2] != ["tree"] and "--locked" not in args:
            raise PatchError(
                f"{source}: Cargo generator must use --locked after lock generation: "
                f"{shlex.join(args)}"
            )
    gated = list(manifest.upstream.integration_commands)
    gated.extend(command for patch in manifest.patches for command in patch.tests)
    for command in gated:
        _, args = _split_env_command(command)
        if args[0] == "cargo" and "--locked" not in args:
            raise PatchError(
                f"{source}: Cargo validation/test command must use --locked: "
                f"{shlex.join(args)}"
            )


def load_manifest(
    root: Path = DEFAULT_REPO_ROOT, *, require_patch_files: bool = True
) -> MaintenanceManifest:
    root = root.resolve()
    upstream_path = root / CODEX_DIR / UPSTREAM_FILE
    series_path = root / CODEX_DIR / SERIES_FILE
    upstream_data = _read_toml(upstream_path)
    _strict_fields(upstream_data, UPSTREAM_FIELDS, upstream_path, "upstream")
    upstream = Upstream(
        url=_string(upstream_data["url"], upstream_path, "url"),
        ref=_string(upstream_data["ref"], upstream_path, "ref"),
        revision=_string(upstream_data["revision"], upstream_path, "revision"),
        worktree=_path(
            upstream_data["worktree"], upstream_path, "worktree", prefix=False
        ),
        generate_commands=_commands(
            upstream_data["generate_commands"], upstream_path, "generate_commands"
        ),
        integration_commands=_commands(
            upstream_data["integration_commands"],
            upstream_path,
            "integration_commands",
        ),
    )
    if not upstream.ref.startswith("rust-v"):
        raise PatchError(f"{upstream_path}: ref must be a rust-v tag: {upstream.ref}")
    if not REVISION_RE.fullmatch(upstream.revision):
        raise PatchError(
            f"{upstream_path}: revision must be a lowercase 40-character Git commit"
        )
    expected_worktree = f".work/codex/{upstream.ref}/src"
    if upstream.worktree != expected_worktree:
        raise PatchError(
            f"{upstream_path}: worktree must be {expected_worktree}, "
            f"not {upstream.worktree}"
        )

    series_data = _read_toml(series_path)
    _strict_fields(series_data, SERIES_FIELDS, series_path, "series")
    raw_patches = series_data["patch"]
    if not isinstance(raw_patches, list) or not raw_patches:
        raise PatchError(
            f"{series_path}: patch must contain at least one [[patch]] table"
        )

    patches: list[PatchSpec] = []
    for index, raw_patch in enumerate(raw_patches, start=1):
        if not isinstance(raw_patch, dict):
            raise PatchError(f"{series_path}: patch #{index} must be a table")
        _strict_fields(raw_patch, PATCH_FIELDS, series_path, f"patch #{index}")
        filename = _path(
            raw_patch["file"], series_path, f"patch #{index}.file", prefix=False
        )
        match = PATCH_FILE_RE.fullmatch(filename)
        if match is None:
            raise PatchError(f"{series_path}: invalid patch filename: {filename}")
        if int(match.group("number")) != index:
            raise PatchError(
                f"{series_path}: patch numbering must be contiguous from 0001; "
                f"entry #{index} is {filename}"
            )
        patches.append(
            PatchSpec(
                file=filename,
                intent=_string(
                    raw_patch["intent"], series_path, f"patch #{index}.intent"
                ),
                behavior=_string(
                    raw_patch["behavior"], series_path, f"patch #{index}.behavior"
                ),
                source_files=_paths(
                    raw_patch["source_files"],
                    series_path,
                    f"patch #{index}.source_files",
                    prefix=False,
                ),
                source_prefixes=_paths(
                    raw_patch["source_prefixes"],
                    series_path,
                    f"patch #{index}.source_prefixes",
                    prefix=True,
                ),
                generated_files=_paths(
                    raw_patch["generated_files"],
                    series_path,
                    f"patch #{index}.generated_files",
                    prefix=False,
                ),
                generated_prefixes=_paths(
                    raw_patch["generated_prefixes"],
                    series_path,
                    f"patch #{index}.generated_prefixes",
                    prefix=True,
                ),
                tests=_commands(
                    raw_patch["tests"], series_path, f"patch #{index}.tests"
                ),
            )
        )

    filenames = [patch.file for patch in patches]
    if len(filenames) != len(set(filenames)):
        raise PatchError(f"{series_path}: duplicate patch filename")
    _validate_ownership(patches, series_path)
    validate_generated_only_ownership(patches[-1])
    for patch in patches[:-1]:
        if patch.generated_files or patch.generated_prefixes:
            raise PatchError(
                f"{series_path}: only the final patch may own generated paths: {patch.file}"
            )
    manifest = MaintenanceManifest(root=root, upstream=upstream, patches=tuple(patches))
    _validate_manifest_commands(manifest, series_path)
    if require_patch_files:
        validate_patch_directory(manifest)
    return manifest


def validate_patch_directory(
    manifest: MaintenanceManifest,
    *,
    directory: Path | None = None,
) -> tuple[Path, ...]:
    patch_dir = (directory or manifest.patch_dir).resolve()
    expected = tuple(patch.file for patch in manifest.patches)
    missing = [name for name in expected if not (patch_dir / name).is_file()]
    if missing:
        raise PatchError(f"missing patch file(s) in {patch_dir}: {', '.join(missing)}")
    actual = sorted(path.name for path in patch_dir.glob("*.patch") if path.is_file())
    stale = sorted(set(actual) - set(expected))
    if stale:
        raise PatchError(
            f"patch directory contains files not listed in {CODEX_DIR}/{SERIES_FILE}: "
            f"{', '.join(stale)}"
        )
    return tuple(patch_dir / name for name in expected)


def run(
    args: Sequence[str],
    *,
    cwd: Path | None = None,
    check: bool = True,
    capture: bool = False,
    env: Mapping[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    command = [str(arg) for arg in args]
    eprint(f"+ {shlex.join(command)}")
    process_env = os.environ.copy()
    if env:
        process_env.update(env)
    return subprocess.run(
        command,
        cwd=cwd,
        check=check,
        text=True,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.PIPE if capture else None,
        env=process_env,
    )


def jobs_limit() -> int:
    raw = os.environ.get("CODEX_CARGO_JOBS")
    if raw is None:
        return max(1, min(os.cpu_count() or 1, 4))
    try:
        requested = int(raw)
    except ValueError as exc:
        raise PatchError("CODEX_CARGO_JOBS must be an integer") from exc
    if requested < 1:
        raise PatchError("CODEX_CARGO_JOBS must be at least 1")
    return requested


def upstream_build_env(manifest: MaintenanceManifest) -> dict[str, str]:
    return {
        "CARGO_INCREMENTAL": "1",
        "CARGO_TARGET_DIR": str(manifest.worktree.parent / "cargo-target"),
        "CARGO_RESOLVER_INCOMPATIBLE_RUST_VERSIONS": "fallback",
        "RUST_MIN_STACK": "8388608",
    }


def _codex_v8_env(manifest: MaintenanceManifest, cwd: Path) -> dict[str, str]:
    archive = os.environ.get("RUSTY_V8_ARCHIVE")
    binding = os.environ.get("RUSTY_V8_SRC_BINDING_PATH")
    if archive and binding:
        return {}
    if archive or binding:
        raise PatchError(
            "RUSTY_V8_ARCHIVE and RUSTY_V8_SRC_BINDING_PATH must be set together"
        )
    source_root = cwd.parent if cwd.name == "codex-rs" else cwd
    helper = source_root / "scripts" / "codex_package" / "v8.py"
    if not helper.is_file():
        return {}
    rustc = run(["rustc", "-vV"], capture=True)
    target_lines = [
        line for line in rustc.stdout.splitlines() if line.startswith("host: ")
    ]
    if len(target_lines) != 1:
        raise PatchError(
            "could not determine the Rust host target for Codex V8 artifacts"
        )
    target = target_lines[0].removeprefix("host: ")
    key = (source_root.resolve(), target)
    cached = _V8_ENV_CACHE.get(key)
    if cached is not None:
        return cached
    cache_root = manifest.worktree.parent / "rusty-v8"
    program = (
        "import json, sys; from pathlib import Path; "
        "sys.path.insert(0, sys.argv[1]); "
        "from scripts.codex_package.targets import TARGET_SPECS; "
        "from scripts.codex_package.v8 import fetch_codex_v8_artifacts; "
        "pair=fetch_codex_v8_artifacts(TARGET_SPECS[sys.argv[2]], "
        "cache_root=Path(sys.argv[3])); "
        "print(json.dumps({'archive': str(pair.archive), "
        "'binding': str(pair.binding)}))"
    )
    resolved = run(
        ["python3", "-c", program, str(source_root), target, str(cache_root)],
        cwd=source_root,
        capture=True,
    )
    try:
        payload = json.loads(resolved.stdout)
        result = {
            "RUSTY_V8_ARCHIVE": str(payload["archive"]),
            "RUSTY_V8_SRC_BINDING_PATH": str(payload["binding"]),
        }
    except (KeyError, TypeError, json.JSONDecodeError) as exc:
        raise PatchError("upstream V8 artifact helper returned invalid output") from exc
    if not all(Path(path).is_file() for path in result.values()):
        raise PatchError(
            "upstream V8 artifact helper did not produce both verified files"
        )
    _V8_ENV_CACHE[key] = result
    return result


def run_upstream(
    manifest: MaintenanceManifest,
    command: Sequence[str],
    *,
    cwd: Path,
    capture_output: bool = False,
) -> subprocess.CompletedProcess[str]:
    overrides, args = _split_env_command(command)
    command_cwd = cwd
    is_cargo = bool(args) and args[0] == "cargo"
    if is_cargo and not (cwd / "Cargo.toml").is_file():
        if (cwd / "codex-rs" / "Cargo.toml").is_file():
            command_cwd = cwd / "codex-rs"
    if (
        len(args) >= 3
        and args[0] in {"bash", "sh"}
        and args[1] == "-c"
        and "cargo " in args[2]
        and (cwd / "codex-rs" / "Cargo.toml").is_file()
    ):
        command_cwd = cwd / "codex-rs"
    if (
        is_cargo
        and len(args) >= 2
        and args[1] not in {"generate-lockfile", "metadata", "tree"}
        and "--jobs" not in args
        and "-j" not in args
    ):
        args[2:2] = ["--jobs", str(jobs_limit())]
    environment = upstream_build_env(manifest)
    environment.update(overrides)
    if is_cargo:
        environment.update(_codex_v8_env(manifest, command_cwd))
    capture = capture_output or (
        is_cargo and len(args) >= 2 and args[1] in {"metadata", "tree"}
    )
    completed = run(
        args,
        cwd=command_cwd,
        env=environment,
        capture=capture,
        check=not capture,
    )
    if completed.returncode:
        detail = (completed.stderr or completed.stdout or "").strip()
        suffix = f": {detail}" if detail else ""
        raise PatchError(
            f"upstream command failed ({completed.returncode}): {shlex.join(args)}{suffix}"
        )
    return completed


def git_output(src: Path, args: Sequence[str]) -> str:
    return run(["git", *args], cwd=src, capture=True).stdout.strip()


def require_git_worktree(src: Path) -> None:
    if not src.is_dir():
        raise PatchError(
            f"missing upstream checkout: {src}; run `just codex-fetch` first"
        )
    if git_output(src, ["rev-parse", "--is-inside-work-tree"]) != "true":
        raise PatchError(f"not a Git worktree: {src}")


def ensure_clean(src: Path) -> None:
    status = run(
        ["git", "status", "--porcelain=v1", "--untracked-files=all"],
        cwd=src,
        capture=True,
    ).stdout
    if status:
        raise PatchError(f"upstream checkout must be clean before apply: {src}")


def ensure_real_index_clean(src: Path) -> None:
    result = run(
        ["git", "diff", "--cached", "--quiet", "--exit-code"],
        cwd=src,
        check=False,
    )
    if result.returncode == 1:
        raise PatchError(f"upstream checkout has staged changes: {src}")
    if result.returncode != 0:
        raise PatchError(f"could not inspect upstream Git index: {src}")


def ensure_pinned_head(src: Path, upstream: Upstream) -> None:
    actual = git_output(src, ["rev-parse", "HEAD"])
    if actual != upstream.revision:
        raise PatchError(
            f"upstream checkout is at {actual}, expected {upstream.revision}; "
            "run `just codex-fetch` only after preserving local work"
        )


def ensure_origin(src: Path, upstream: Upstream) -> None:
    actual = git_output(src, ["remote", "get-url", "origin"])
    if actual != upstream.url:
        raise PatchError(
            f"upstream origin is {actual}, expected {upstream.url}; "
            "repair the checkout origin explicitly before maintenance"
        )


def _index_path(src: Path) -> Path:
    path = Path(git_output(src, ["rev-parse", "--git-path", "index"]))
    return path if path.is_absolute() else src / path


def _checkout_fingerprint(src: Path) -> tuple[str, bytes, str, str]:
    status = run(
        ["git", "status", "--porcelain=v1", "-z", "--untracked-files=all"],
        cwd=src,
        capture=True,
    ).stdout
    return (
        status,
        _index_path(src).read_bytes(),
        git_output(src, ["rev-parse", "HEAD"]),
        git_output(src, ["remote", "get-url", "origin"]),
    )


def _check_ignored_path(manifest: MaintenanceManifest, path: Path) -> None:
    resolved = path.resolve()
    try:
        relative = resolved.relative_to(manifest.root)
    except ValueError as exc:
        raise PatchError(
            f"maintenance runtime path escapes the repository: {path}"
        ) from exc
    result = run(
        ["git", "check-ignore", "--quiet", "--", relative.as_posix()],
        cwd=manifest.root,
        check=False,
    )
    if result.returncode != 0:
        raise PatchError(f"maintenance runtime path is not ignored: {path}")


def _reset_maintenance_checkout(manifest: MaintenanceManifest) -> None:
    checkout = manifest.maintenance_checkout
    if not checkout.exists():
        checkout.parent.mkdir(parents=True, exist_ok=True)
        clone_pinned_checkout(manifest, checkout)
    require_git_worktree(checkout)
    run(["git", "reset", "--hard", manifest.upstream.revision], cwd=checkout)
    run(["git", "clean", "-ffdx"], cwd=checkout)
    ensure_pinned_head(checkout, manifest.upstream)


@contextmanager
def maintenance_workspace(manifest: MaintenanceManifest) -> Iterator[Path]:
    _check_ignored_path(manifest, manifest.runtime_root)
    manifest.runtime_root.mkdir(parents=True, exist_ok=True)
    lock_path = manifest.runtime_root / "workspace.lock"
    with lock_path.open("a+") as lock:
        fcntl.flock(lock.fileno(), fcntl.LOCK_EX)
        workspace = manifest.maintenance_workspace
        workspace.mkdir(parents=True, exist_ok=True)
        for path in workspace.iterdir():
            if path.name == "src":
                continue
            if path.is_dir() and not path.is_symlink():
                shutil.rmtree(path)
            else:
                path.unlink()
        _reset_maintenance_checkout(manifest)
        try:
            yield workspace
        finally:
            _reset_maintenance_checkout(manifest)


def fetch_upstream(manifest: MaintenanceManifest) -> dict[str, Any]:
    src = manifest.worktree
    if src.exists():
        require_git_worktree(src)
        ensure_origin(src, manifest.upstream)
        ensure_pinned_head(src, manifest.upstream)
        ensure_real_index_clean(src)
        dirty = bool(
            run(
                ["git", "status", "--porcelain=v1", "--untracked-files=all"],
                cwd=src,
                capture=True,
            ).stdout
        )
        return {
            "revision": manifest.upstream.revision,
            "created": False,
            "dirty": dirty,
        }

    src.parent.mkdir(parents=True, exist_ok=True)
    run(["git", "init", "--quiet", str(src)])
    run(["git", "remote", "add", "origin", manifest.upstream.url], cwd=src)
    run(
        [
            "git",
            "fetch",
            "--depth=1",
            "--no-tags",
            "origin",
            f"refs/tags/{manifest.upstream.ref}",
        ],
        cwd=src,
    )
    resolved = git_output(src, ["rev-parse", "FETCH_HEAD^{commit}"])
    if resolved != manifest.upstream.revision:
        raise PatchError(
            f"tag {manifest.upstream.ref} resolves to {resolved}, "
            f"expected {manifest.upstream.revision}"
        )
    run(
        ["git", "-c", "advice.detachedHead=false", "switch", "--detach", resolved],
        cwd=src,
    )
    ensure_origin(src, manifest.upstream)
    ensure_pinned_head(src, manifest.upstream)
    ensure_clean(src)
    return {"revision": resolved, "created": True, "dirty": False}


def _clone_base(src: Path, destination: Path, revision: str) -> None:
    run(
        [
            "git",
            "-c",
            "protocol.file.allow=always",
            "clone",
            "--quiet",
            "--no-hardlinks",
            "--no-checkout",
            str(src),
            str(destination),
        ]
    )
    run(
        ["git", "-c", "advice.detachedHead=false", "switch", "--detach", revision],
        cwd=destination,
    )


def clone_pinned_checkout(manifest: MaintenanceManifest, destination: Path) -> None:
    _clone_base(manifest.worktree, destination, manifest.upstream.revision)


def _changed_paths(src: Path) -> list[str]:
    tracked = run(
        ["git", "diff", "--name-only", "--no-renames", "-z", "HEAD"],
        cwd=src,
        capture=True,
    ).stdout.split("\0")
    untracked = run(
        ["git", "ls-files", "--others", "--exclude-standard", "-z"],
        cwd=src,
        capture=True,
    ).stdout.split("\0")
    return sorted({path for path in [*tracked, *untracked] if path})


def _temporary_index_diff(
    src: Path,
    paths: Sequence[str],
    index: Path,
    *,
    selected: Sequence[str] | None = None,
) -> str:
    env = {"GIT_INDEX_FILE": str(index)}
    run(["git", "read-tree", "HEAD"], cwd=src, env=env)
    if paths:
        run(["git", "add", "--", *paths], cwd=src, env=env)
    command = [
        "git",
        "diff",
        "--cached",
        "--binary",
        "--full-index",
        "--no-renames",
        "HEAD",
    ]
    if selected is not None:
        command.extend(["--", *selected])
    return run(command, cwd=src, capture=True, env=env).stdout


def _tree_digest(src: Path, paths: Iterable[str]) -> str:
    digest = hashlib.sha256()
    for relative in sorted(paths):
        path = src / relative
        digest.update(relative.encode())
        digest.update(b"\0")
        if path.is_symlink():
            digest.update(b"link\0")
            digest.update(os.readlink(path).encode())
        elif path.is_file():
            digest.update(b"file\0")
            digest.update(path.read_bytes())
        elif not path.exists():
            digest.update(b"missing\0")
        else:
            raise PatchError(
                f"owned path is not a file, symlink, or deletion: {relative}"
            )
        digest.update(b"\0")
    return digest.hexdigest()


def changed_tree_digest(src: Path) -> tuple[tuple[str, ...], str]:
    paths = tuple(_changed_paths(src))
    return paths, _tree_digest(src, paths)


def _is_declared_generated(manifest: MaintenanceManifest, path: str) -> bool:
    return any(patch.owns_generated(path) for patch in manifest.patches)


def declared_generated_paths(
    manifest: MaintenanceManifest, root: Path
) -> tuple[str, ...]:
    paths = {path for patch in manifest.patches for path in patch.generated_files}
    prefixes = tuple(
        prefix for patch in manifest.patches for prefix in patch.generated_prefixes
    )
    if prefixes:
        tracked = run(
            ["git", "ls-files", "-z", "--", *prefixes],
            cwd=root,
            capture=True,
        ).stdout.split("\0")
        paths.update(path for path in tracked if path)
        for prefix in prefixes:
            directory = root / prefix
            if not directory.is_dir():
                continue
            paths.update(
                path.relative_to(root).as_posix()
                for path in directory.rglob("*")
                if path.is_file() or path.is_symlink()
            )
    paths.update(
        path for path in _changed_paths(root) if _is_declared_generated(manifest, path)
    )
    return tuple(sorted(paths))


def generated_tree_digest(
    manifest: MaintenanceManifest, root: Path
) -> tuple[tuple[str, ...], str]:
    paths = declared_generated_paths(manifest, root)
    return paths, _tree_digest(root, paths)


def _generated_file_states(
    root: Path, generated_files: Iterable[str]
) -> dict[str, _GeneratedFileState]:
    states: dict[str, _GeneratedFileState] = {}
    for relative in generated_files:
        path = root / relative
        try:
            metadata = path.lstat()
        except FileNotFoundError:
            continue
        if not stat.S_ISREG(metadata.st_mode):
            continue
        states[relative] = _GeneratedFileState(
            digest=hashlib.sha256(path.read_bytes()).digest(),
            mode=stat.S_IMODE(metadata.st_mode),
            mtime_ns=metadata.st_mtime_ns,
            size=metadata.st_size,
        )
    return states


def _directory_tree_digest(path: Path) -> bytes:
    digest = hashlib.sha256()
    for child in sorted(path.rglob("*"), key=lambda item: item.as_posix()):
        relative = child.relative_to(path).as_posix()
        metadata = child.lstat()
        digest.update(relative.encode())
        digest.update(b"\0")
        digest.update(str(stat.S_IMODE(metadata.st_mode)).encode())
        digest.update(b"\0")
        if child.is_symlink():
            digest.update(b"symlink\0")
            digest.update(os.readlink(child).encode())
        elif stat.S_ISDIR(metadata.st_mode):
            digest.update(b"directory\0")
        elif stat.S_ISREG(metadata.st_mode):
            digest.update(b"file\0")
            digest.update(child.read_bytes())
        else:
            digest.update(b"other\0")
        digest.update(b"\0")
    return digest.digest()


def _generated_directory_states(
    manifest: MaintenanceManifest,
    root: Path,
) -> dict[str, _GeneratedDirectoryState]:
    directories: set[str] = set()
    for patch in manifest.patches:
        for prefix in patch.generated_prefixes:
            prefix_path = root / prefix
            if not prefix_path.is_dir() or prefix_path.is_symlink():
                continue
            directories.add(prefix.removesuffix("/"))

    states: dict[str, _GeneratedDirectoryState] = {}
    for relative in sorted(directories):
        path = root / relative
        try:
            metadata = path.lstat()
        except FileNotFoundError:
            continue
        if not stat.S_ISDIR(metadata.st_mode):
            continue
        states[relative] = _GeneratedDirectoryState(
            digest=_directory_tree_digest(path),
            mode=stat.S_IMODE(metadata.st_mode),
            mtime_ns=metadata.st_mtime_ns,
        )
    return states


@contextmanager
def _preserve_unchanged_generated_mtimes(
    manifest: MaintenanceManifest, root: Path
) -> Iterator[None]:
    generated_files = declared_generated_paths(manifest, root)
    before_files = _generated_file_states(root, generated_files)
    before_directories = _generated_directory_states(manifest, root)
    yield
    for relative, previous in before_files.items():
        path = root / relative
        try:
            metadata = path.lstat()
        except FileNotFoundError:
            continue
        if (
            not stat.S_ISREG(metadata.st_mode)
            or stat.S_IMODE(metadata.st_mode) != previous.mode
            or metadata.st_size != previous.size
            or hashlib.sha256(path.read_bytes()).digest() != previous.digest
        ):
            continue
        try:
            os.utime(
                path,
                ns=(metadata.st_atime_ns, previous.mtime_ns),
                follow_symlinks=False,
            )
        except OSError as exc:
            raise PatchError(
                f"could not preserve unchanged generated-file mtime: {relative}: {exc}"
            ) from exc
    for relative, previous in sorted(
        before_directories.items(),
        key=lambda item: len(PurePosixPath(item[0]).parts),
        reverse=True,
    ):
        path = root / relative
        try:
            metadata = path.lstat()
        except FileNotFoundError:
            continue
        if (
            not stat.S_ISDIR(metadata.st_mode)
            or stat.S_IMODE(metadata.st_mode) != previous.mode
            or _directory_tree_digest(path) != previous.digest
        ):
            continue
        try:
            os.utime(
                path,
                ns=(metadata.st_atime_ns, previous.mtime_ns),
                follow_symlinks=False,
            )
        except OSError as exc:
            raise PatchError(
                "could not preserve unchanged generated-directory mtime: "
                f"{relative}: {exc}"
            ) from exc


def _non_generated_change_digest(
    manifest: MaintenanceManifest, root: Path
) -> tuple[tuple[str, ...], str]:
    paths = tuple(
        path
        for path in _changed_paths(root)
        if not _is_declared_generated(manifest, path)
    )
    return paths, _tree_digest(root, paths)


def _path_digest_map(root: Path, paths: Iterable[str]) -> dict[str, str]:
    return {path: _tree_digest(root, (path,)) for path in paths}


def _bootstrap_missing_generated_files(
    manifest: MaintenanceManifest, worktree: Path
) -> tuple[str, ...]:
    missing: list[str] = []
    for path in sorted(
        path for patch in manifest.patches for path in patch.generated_files
    ):
        target = worktree / path
        if os.path.lexists(target):
            continue
        target.parent.mkdir(parents=True, exist_ok=True)
        target.touch(exist_ok=False)
        missing.append(path)
    return tuple(missing)


def _assert_non_generated_unchanged(
    manifest: MaintenanceManifest,
    worktree: Path,
    before: tuple[tuple[str, ...], str],
    before_paths: Mapping[str, str],
) -> None:
    after = _non_generated_change_digest(manifest, worktree)
    if after == before:
        return
    after_paths = _path_digest_map(worktree, after[0])
    changed = sorted(
        path
        for path in before_paths.keys() | after_paths.keys()
        if before_paths.get(path) != after_paths.get(path)
    )
    raise PatchError(
        "generator changed path(s) outside manifest-declared generated ownership: "
        + ", ".join(changed)
    )


def run_generation_sequence(
    manifest: MaintenanceManifest,
    worktree: Path,
    *,
    twice: bool = True,
) -> tuple[str, str | None]:
    before = _non_generated_change_digest(manifest, worktree)
    before_paths = _path_digest_map(worktree, before[0])
    bootstrapped = _bootstrap_missing_generated_files(manifest, worktree)
    for command in manifest.upstream.generate_commands:
        with _preserve_unchanged_generated_mtimes(manifest, worktree):
            run_upstream(manifest, command, cwd=worktree)
    incomplete = [
        path
        for path in bootstrapped
        if not (worktree / path).is_file() or not (worktree / path).read_bytes()
    ]
    if incomplete:
        raise PatchError(
            "generator did not replace bootstrapped generated file(s): "
            + ", ".join(incomplete)
        )
    _assert_non_generated_unchanged(manifest, worktree, before, before_paths)
    _, first = generated_tree_digest(manifest, worktree)
    if not twice:
        return first, None
    for command in manifest.upstream.generate_commands:
        with _preserve_unchanged_generated_mtimes(manifest, worktree):
            run_upstream(manifest, command, cwd=worktree)
    _assert_non_generated_unchanged(manifest, worktree, before, before_paths)
    _, second = generated_tree_digest(manifest, worktree)
    if first != second:
        raise PatchError(
            "generator sequence is not reproducible: the second run changed the "
            f"generated tree ({first} -> {second})"
        )
    return first, second


def validate_cargo_lock_compatibility(
    pinned_bytes: bytes,
    generated_bytes: bytes,
    *,
    workspace_version: str,
) -> int:
    try:
        pinned = tomllib.loads(pinned_bytes.decode())
        generated = tomllib.loads(generated_bytes.decode())
    except (UnicodeDecodeError, tomllib.TOMLDecodeError) as exc:
        raise PatchError(f"could not parse Cargo lockfile: {exc}") from exc
    pinned_metadata = {key: value for key, value in pinned.items() if key != "package"}
    generated_metadata = {
        key: value for key, value in generated.items() if key != "package"
    }
    if pinned_metadata != generated_metadata:
        raise PatchError("generated Cargo.lock changed non-package lock metadata")

    def split_packages(
        lock: Mapping[str, Any],
    ) -> tuple[
        dict[tuple[str, str, str], Mapping[str, Any]],
        dict[str, Mapping[str, Any]],
    ]:
        external: dict[tuple[str, str, str], Mapping[str, Any]] = {}
        local: dict[str, Mapping[str, Any]] = {}
        for package in lock.get("package", []):
            if not isinstance(package, dict):
                raise PatchError("Cargo.lock package entry must be a table")
            name = str(package.get("name", ""))
            version = str(package.get("version", ""))
            source = package.get("source")
            if source is None:
                if not name or name in local:
                    raise PatchError(
                        f"Cargo.lock has an invalid or duplicate local package: {name}"
                    )
                local[name] = package
            else:
                key = (name, version, str(source))
                if key in external:
                    raise PatchError(
                        f"Cargo.lock has a duplicate external package: {key}"
                    )
                external[key] = package
        return external, local

    pinned_external, pinned_local = split_packages(pinned)
    generated_external, generated_local = split_packages(generated)
    if pinned_external != generated_external:
        raise PatchError(
            "generated Cargo.lock changed external sources, checksums, or dependency edges"
        )
    if pinned_local.keys() != generated_local.keys():
        raise PatchError("generated Cargo.lock changed the local workspace package set")

    changed_versions = 0
    for name in sorted(pinned_local):
        pinned_package = dict(pinned_local[name])
        generated_package = dict(generated_local[name])
        pinned_version = pinned_package.pop("version", None)
        generated_version = generated_package.pop("version", None)
        if pinned_package != generated_package:
            raise PatchError(
                f"generated Cargo.lock changed local dependency metadata for {name}"
            )
        if generated_version != workspace_version:
            raise PatchError(
                f"generated Cargo.lock has unexpected local version for {name}: "
                f"{generated_version}; expected {workspace_version}"
            )
        if pinned_version != generated_version:
            if pinned_version != "0.0.0":
                raise PatchError(
                    "generated Cargo.lock changed non-development local version for "
                    f"{name}: {pinned_version} -> {generated_version}"
                )
            changed_versions += 1
    return changed_versions


def validate_cargo_lock_tree(
    manifest: MaintenanceManifest, worktree: Path
) -> int | None:
    relative = "codex-rs/Cargo.lock"
    if not manifest.patches[-1].owns_generated(relative):
        return None
    lock_path = worktree / relative
    cargo_toml = worktree / "codex-rs" / "Cargo.toml"
    if not lock_path.is_file() or not cargo_toml.is_file():
        raise PatchError(
            "generated Cargo.lock contract is declared but files are missing"
        )
    pinned = subprocess.run(
        ["git", "show", f"{manifest.upstream.revision}:{relative}"],
        cwd=worktree,
        check=True,
        stdout=subprocess.PIPE,
    ).stdout
    cargo = tomllib.loads(cargo_toml.read_text())
    try:
        workspace_version = str(cargo["workspace"]["package"]["version"])
    except (KeyError, TypeError) as exc:
        raise PatchError(
            "Cargo workspace is missing workspace.package.version"
        ) from exc
    changed = validate_cargo_lock_compatibility(
        pinned,
        lock_path.read_bytes(),
        workspace_version=workspace_version,
    )
    if changed == 0:
        raise PatchError(
            "generated Cargo.lock contains no workspace version normalization"
        )
    return changed


def _unique_commands(
    commands: Iterable[tuple[str, ...]],
) -> tuple[tuple[str, ...], ...]:
    seen: set[tuple[str, ...]] = set()
    unique: list[tuple[str, ...]] = []
    for command in commands:
        if command in seen:
            continue
        seen.add(command)
        unique.append(command)
    return tuple(unique)


def _patch_for_selector(manifest: MaintenanceManifest, selector: str) -> PatchSpec:
    matches = [
        patch
        for index, patch in enumerate(manifest.patches, start=1)
        if selector
        in {
            str(index),
            f"{index:04d}",
            patch.file,
            patch.file.removesuffix(".patch"),
        }
    ]
    if len(matches) != 1:
        choices = ", ".join(
            patch.file.removesuffix(".patch") for patch in manifest.patches
        )
        raise PatchError(
            f"unknown patch check selector {selector!r}; use integration or one of: {choices}"
        )
    return matches[0]


def commands_for_selector(
    manifest: MaintenanceManifest, selector: str
) -> tuple[tuple[str, ...], ...]:
    if selector == "integration":
        return _unique_commands(manifest.upstream.integration_commands)
    return _unique_commands(_patch_for_selector(manifest, selector).tests)


def _cargo_test_list_command(command: Sequence[str]) -> tuple[str, ...] | None:
    overrides, args = _split_env_command(command)
    if args[:2] != ["cargo", "test"]:
        return None
    if "--" in args:
        separator = args.index("--") + 1
        args.insert(separator, "--list")
    else:
        args.extend(("--", "--list"))
    if not overrides:
        return tuple(args)
    assignments = [f"{key}={value}" for key, value in overrides.items()]
    return ("env", *assignments, *args)


def _listed_rust_test_count(output: str) -> int:
    return sum(
        int(match.group("tests"))
        for match in RUST_TEST_LIST_SUMMARY_RE.finditer(output)
    )


def run_commands(
    manifest: MaintenanceManifest,
    worktree: Path,
    commands: Iterable[tuple[str, ...]],
) -> tuple[tuple[str, ...], ...]:
    selected = _unique_commands(commands)
    for command in selected:
        run_upstream(manifest, command, cwd=worktree)
        list_command = _cargo_test_list_command(command)
        if list_command is None:
            continue
        listed = run_upstream(
            manifest,
            list_command,
            cwd=worktree,
            capture_output=True,
        )
        if _listed_rust_test_count(listed.stdout) == 0:
            raise PatchError(
                "cargo test selector matched no tests: " + shlex.join(command)
            )
    return selected


def run_all_manifest_validation(
    manifest: MaintenanceManifest, worktree: Path
) -> tuple[tuple[str, ...], ...]:
    commands = [command for patch in manifest.patches for command in patch.tests]
    commands.extend(manifest.upstream.integration_commands)
    return run_commands(manifest, worktree, commands)


def apply_patch_series(
    checkout: Path,
    patches: Sequence[Path],
    *,
    manifest: MaintenanceManifest | None = None,
) -> None:
    for index, patch in enumerate(patches):
        numstat = run(
            ["git", "apply", "--numstat", "-z", str(patch)],
            cwd=checkout,
            capture=True,
        ).stdout
        touched = {
            record.rsplit("\t", 1)[-1] for record in numstat.split("\0") if record
        }
        if not touched:
            raise PatchError(f"patch has no changes: {patch.name}")
        if manifest is not None:
            expected = manifest.patches[index]
            wrong = sorted(
                path
                for path in touched
                if manifest.owner_for(path).file != expected.file
            )
            if wrong:
                raise PatchError(
                    f"patch {patch.name} changes path(s) owned by another patch: "
                    + ", ".join(wrong)
                )
        run(["git", "apply", "--check", "--binary", str(patch)], cwd=checkout)
        run(["git", "apply", "--binary", str(patch)], cwd=checkout)


def patch_paths(
    manifest: MaintenanceManifest, *, directory: Path | None = None
) -> list[Path]:
    base = directory or manifest.patch_dir
    return [base / patch.file for patch in manifest.patches]


def apply_patches(manifest: MaintenanceManifest, *, check_only: bool) -> dict[str, Any]:
    src = manifest.worktree
    require_git_worktree(src)
    ensure_pinned_head(src, manifest.upstream)
    ensure_origin(src, manifest.upstream)
    ensure_real_index_clean(src)
    if not check_only:
        ensure_clean(src)
    patches = list(validate_patch_directory(manifest))
    before = _checkout_fingerprint(src)
    with maintenance_workspace(manifest):
        apply_patch_series(manifest.maintenance_checkout, patches, manifest=manifest)
    if check_only:
        if _checkout_fingerprint(src) != before:
            raise PatchError("apply --check changed the upstream checkout")
        return {
            "mode": "check",
            "origin": before[3],
        }

    if _checkout_fingerprint(src) != before:
        raise PatchError("upstream checkout changed while validating the patch series")
    apply_patch_series(src, patches, manifest=manifest)
    ensure_origin(src, manifest.upstream)
    return {
        "mode": "apply",
        "origin": git_output(src, ["remote", "get-url", "origin"]),
    }


def check_patches(manifest: MaintenanceManifest, *, selector: str) -> dict[str, Any]:
    src = manifest.worktree
    require_git_worktree(src)
    ensure_pinned_head(src, manifest.upstream)
    ensure_origin(src, manifest.upstream)
    ensure_real_index_clean(src)
    patches = list(validate_patch_directory(manifest))
    selected = commands_for_selector(manifest, selector)
    if not selected:
        raise PatchError(f"check selector {selector!r} has no declared commands")
    before = _checkout_fingerprint(src)
    with maintenance_workspace(manifest):
        checkout = manifest.maintenance_checkout
        apply_patch_series(checkout, patches, manifest=manifest)
        commands = run_commands(manifest, checkout, selected)
    if _checkout_fingerprint(src) != before:
        raise PatchError("check changed the editing checkout")
    return {
        "selector": selector,
        "commands": [list(command) for command in commands],
    }


def _validate_refresh_source_patch_directory(
    manifest: MaintenanceManifest,
) -> tuple[Path, ...]:
    patch_dir = manifest.patch_dir
    if not patch_dir.exists():
        return ()
    if patch_dir.is_symlink() or not patch_dir.is_dir():
        raise PatchError(f"installed patch path is not a real directory: {patch_dir}")
    entries = tuple(sorted(patch_dir.iterdir(), key=lambda path: path.name))
    if not entries or any(
        path.is_symlink()
        or not path.is_file()
        or PATCH_FILE_RE.fullmatch(path.name) is None
        for path in entries
    ):
        raise PatchError(
            f"installed predecessor patch directory is not a flat patch set: {patch_dir}"
        )
    relative = patch_dir.relative_to(manifest.root).as_posix()
    for args, description in (
        (["diff", "--quiet", "--exit-code"], "unstaged"),
        (["diff", "--cached", "--quiet", "--exit-code"], "staged"),
    ):
        result = run(["git", *args, "--", relative], cwd=manifest.root, check=False)
        if result.returncode == 1:
            raise PatchError(
                f"installed predecessor patch directory has {description} changes: "
                f"{patch_dir}"
            )
        if result.returncode != 0:
            raise PatchError(f"could not inspect {description} changes in {patch_dir}")
    untracked = run(
        ["git", "ls-files", "--others", "--exclude-standard", "-z", "--", relative],
        cwd=manifest.root,
        capture=True,
    ).stdout.split("\0")
    if any(untracked):
        raise PatchError(
            "installed predecessor patch directory contains untracked files"
        )
    tracked = {
        path
        for path in run(
            ["git", "ls-files", "-z", "--", relative],
            cwd=manifest.root,
            capture=True,
        ).stdout.split("\0")
        if path
    }
    actual = {path.relative_to(manifest.root).as_posix() for path in entries}
    if tracked != actual:
        raise PatchError(
            "installed predecessor patch directory is not exactly Git-tracked"
        )
    return entries


def _patch_file_state(paths: Sequence[Path]) -> tuple[tuple[str, str], ...]:
    try:
        return tuple(
            (path.name, hashlib.sha256(path.read_bytes()).hexdigest()) for path in paths
        )
    except OSError as error:
        raise PatchError(
            f"could not fingerprint installed patch files: {error}"
        ) from error


def _copy_editing_tree(
    manifest: MaintenanceManifest,
    src: Path,
    destination: Path,
    index: Path,
) -> list[str]:
    paths = [
        path
        for path in _changed_paths(src)
        if not _is_declared_generated(manifest, path)
    ]
    if not paths:
        raise PatchError("upstream worktree has no handwritten changes to refresh")
    diff = _temporary_index_diff(src, paths, index)
    applied = subprocess.run(
        ["git", "apply", "--binary", "-"],
        cwd=destination,
        input=diff,
        text=True,
        check=False,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if applied.returncode != 0:
        raise PatchError(
            f"could not copy editing tree into refresh sandbox: {applied.stderr.strip()}"
        )
    return paths


def _patch_hashes(manifest: MaintenanceManifest, directory: Path) -> dict[str, str]:
    return {
        patch.file: hashlib.sha256((directory / patch.file).read_bytes()).hexdigest()
        for patch in manifest.patches
    }


def _file_digest(path: Path) -> str | None:
    if not path.exists():
        return None
    if path.is_symlink() or not path.is_file():
        raise PatchError(f"patch install path is not a regular file: {path}")
    return hashlib.sha256(path.read_bytes()).hexdigest()


def _recover_patch_install(manifest: MaintenanceManifest) -> dict[str, str] | None:
    receipt_path = manifest.install_receipt
    if not receipt_path.exists():
        return None
    try:
        receipt = json.loads(receipt_path.read_text())
        if receipt.get("version") != 1 or not isinstance(receipt.get("paths"), list):
            raise ValueError("unsupported receipt structure")
    except (OSError, ValueError, json.JSONDecodeError) as exc:
        raise PatchError(
            f"cannot safely interpret patch install receipt {receipt_path}: {exc}"
        ) from exc
    current_head = git_output(manifest.root, ["rev-parse", "HEAD"])
    if receipt.get("rootHead") != current_head:
        raise PatchError(
            "patch install receipt belongs to a different outer Git HEAD; "
            f"preserved at {receipt_path}"
        )

    entries: list[tuple[str, str | None, str | None, str | None]] = []
    active_prefix = manifest.patch_dir.relative_to(manifest.root).as_posix() + "/"
    for raw in receipt["paths"]:
        if not isinstance(raw, dict):
            raise PatchError(f"invalid patch install receipt entry in {receipt_path}")
        relative = raw.get("path")
        old = raw.get("old")
        new = raw.get("new")
        if (
            not isinstance(relative, str)
            or not relative.startswith(active_prefix)
            or (old is not None and not isinstance(old, str))
            or (new is not None and not isinstance(new, str))
        ):
            raise PatchError(f"invalid patch install receipt entry in {receipt_path}")
        current = _file_digest(manifest.root / relative)
        entries.append((relative, old, new, current))
    if not entries:
        raise PatchError(f"patch install receipt has no paths: {receipt_path}")

    if all(current == old for _, old, _, current in entries):
        receipt_path.unlink()
        return None
    if all(current == new for _, _, new, current in entries):
        validate_patch_directory(manifest)
        installed = _patch_hashes(manifest, manifest.patch_dir)
        receipt_path.unlink()
        return installed
    if not all(current in {old, new} for _, old, new, current in entries):
        raise PatchError(
            "patch files do not match either side of the interrupted install; "
            f"preserved with receipt {receipt_path}"
        )

    for relative, old, _, _ in entries:
        target = manifest.root / relative
        if old is None:
            target.unlink(missing_ok=True)
            continue
        restored = subprocess.run(
            ["git", "show", f"{current_head}:{relative}"],
            cwd=manifest.root,
            check=True,
            stdout=subprocess.PIPE,
        ).stdout
        if hashlib.sha256(restored).hexdigest() != old:
            raise PatchError(
                f"outer Git HEAD cannot restore interrupted patch path: {relative}"
            )
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(restored)
    receipt_path.unlink()
    return None


def _install_patch_files(
    manifest: MaintenanceManifest,
    candidate: Path,
    existing: Sequence[Path],
    workspace: Path,
) -> dict[str, str]:
    install_tree = workspace / "install-tree"
    install_tree.mkdir()
    run(["git", "init", "--quiet"], cwd=install_tree)
    relative_dir = manifest.patch_dir.relative_to(manifest.root)
    staged_dir = install_tree / relative_dir
    staged_dir.mkdir(parents=True)
    old_names = {path.name for path in existing}
    new_names = {patch.file for patch in manifest.patches}
    for path in existing:
        shutil.copyfile(path, staged_dir / path.name)
    old_relatives = [(relative_dir / name).as_posix() for name in sorted(old_names)]
    if old_relatives:
        run(["git", "add", "--", *old_relatives], cwd=install_tree)
    run(
        [
            "git",
            "-c",
            "user.name=Codex patch maintenance",
            "-c",
            "user.email=codex-patch-maintenance@invalid",
            "commit",
            "--quiet",
            "--allow-empty",
            "-m",
            "patch install baseline",
        ],
        cwd=install_tree,
    )
    shutil.rmtree(staged_dir)
    staged_dir.mkdir(parents=True)
    for patch in manifest.patches:
        shutil.copyfile(candidate / patch.file, staged_dir / patch.file)
    install_relatives = [
        (relative_dir / name).as_posix() for name in sorted(old_names | new_names)
    ]
    run(["git", "add", "--", *install_relatives], cwd=install_tree)
    install_diff = run(
        [
            "git",
            "diff",
            "--cached",
            "--binary",
            "--full-index",
            "--no-renames",
            "HEAD",
            "--",
            relative_dir.as_posix(),
        ],
        cwd=install_tree,
        capture=True,
    ).stdout
    if not install_diff:
        raise PatchError("candidate patch set is identical to the installed patch set")
    install_patch = workspace / "install.patch"
    install_patch.write_text(install_diff)

    records = []
    for name in sorted(old_names | new_names):
        relative = (relative_dir / name).as_posix()
        records.append(
            {
                "path": relative,
                "old": _file_digest(manifest.root / relative),
                "new": _file_digest(staged_dir / name),
            }
        )
    manifest.install_receipt.write_text(
        json.dumps(
            {
                "version": 1,
                "rootHead": git_output(manifest.root, ["rev-parse", "HEAD"]),
                "paths": records,
            },
            indent=2,
            sort_keys=True,
        )
        + "\n"
    )
    run(
        ["git", "apply", "--check", "--binary", str(install_patch)],
        cwd=manifest.root,
    )
    run(["git", "apply", "--binary", str(install_patch)], cwd=manifest.root)
    expected = {record["path"]: record["new"] for record in records}
    actual = {relative: _file_digest(manifest.root / relative) for relative in expected}
    if actual != expected:
        raise PatchError(
            "patch install stopped in a recoverable mixed state; rerun refresh "
            f"with receipt {manifest.install_receipt}"
        )
    manifest.install_receipt.unlink()
    validate_patch_directory(manifest)
    return _patch_hashes(manifest, manifest.patch_dir)


def refresh_patches(manifest: MaintenanceManifest, *, dry_run: bool) -> dict[str, Any]:
    src = manifest.worktree
    require_git_worktree(src)
    ensure_pinned_head(src, manifest.upstream)
    ensure_origin(src, manifest.upstream)
    ensure_real_index_clean(src)
    before_source = _checkout_fingerprint(src)
    before_root = run(
        ["git", "status", "--porcelain=v1", "-z", "--untracked-files=all"],
        cwd=manifest.root,
        capture=True,
    ).stdout
    if not dry_run:
        recovered = _recover_patch_install(manifest)
        if recovered is not None:
            return {
                "ref": manifest.upstream.ref,
                "revision": manifest.upstream.revision,
                "dryRun": False,
                "patches": [patch.file for patch in manifest.patches],
                "installedSha256": recovered,
                "recoveredInstall": True,
            }
    existing = () if dry_run else _validate_refresh_source_patch_directory(manifest)
    existing_state = _patch_file_state(existing)

    with maintenance_workspace(manifest) as workspace:
        working = manifest.maintenance_checkout
        source_paths = _copy_editing_tree(
            manifest,
            src,
            working,
            workspace / "source.index",
        )
        for path in source_paths:
            manifest.owner_for(path)
        first, second = run_generation_sequence(manifest, working, twice=True)
        changed = _changed_paths(working)
        if not changed:
            raise PatchError("generation produced no patchable changes")
        owners: dict[str, list[str]] = {patch.file: [] for patch in manifest.patches}
        for path in changed:
            owners[manifest.owner_for(path).file].append(path)
        empty = [name for name, paths in owners.items() if not paths]
        if empty:
            raise PatchError(
                f"refresh would produce empty patch file(s): {', '.join(empty)}"
            )
        expected_paths = tuple(changed)
        expected_digest = _tree_digest(working, expected_paths)
        candidate = workspace / "patches"
        candidate.mkdir()
        for patch in manifest.patches:
            text = _temporary_index_diff(
                working,
                changed,
                workspace / f"{patch.file}.index",
                selected=owners[patch.file],
            )
            if not text:
                raise PatchError(f"refresh produced an empty patch: {patch.file}")
            (candidate / patch.file).write_text(text)
        validate_patch_directory(manifest, directory=candidate)
        _reset_maintenance_checkout(manifest)
        apply_patch_series(
            working,
            patch_paths(manifest, directory=candidate),
            manifest=manifest,
        )
        replay_paths, replay_digest = changed_tree_digest(working)
        if expected_paths != replay_paths or expected_digest != replay_digest:
            raise PatchError(
                "candidate patches do not reproduce the generated editing tree"
            )
        lock_versions = validate_cargo_lock_tree(manifest, working)
        patch_hashes = _patch_hashes(manifest, candidate)
        installed = None
        if not dry_run:
            if _patch_file_state(existing) != existing_state:
                raise PatchError(
                    "installed patch files changed while refresh was validating its candidate"
                )
            installed = _install_patch_files(manifest, candidate, existing, workspace)

    if _checkout_fingerprint(src) != before_source:
        raise PatchError("refresh changed the editing worktree or its Git metadata")
    after_root = run(
        ["git", "status", "--porcelain=v1", "-z", "--untracked-files=all"],
        cwd=manifest.root,
        capture=True,
    ).stdout
    if dry_run and after_root != before_root:
        raise PatchError("refresh --dry-run changed tracked maintenance files")
    return {
        "ref": manifest.upstream.ref,
        "revision": manifest.upstream.revision,
        "dryRun": dry_run,
        "patches": [patch.file for patch in manifest.patches],
        "patchSha256": patch_hashes,
        "installedSha256": installed,
        "changedPaths": changed,
        "generationDigest": first,
        "secondGenerationDigest": second,
        "changedWorkspaceVersions": lock_versions,
    }


def verify_patches(manifest: MaintenanceManifest) -> dict[str, Any]:
    src = manifest.worktree
    require_git_worktree(src)
    ensure_pinned_head(src, manifest.upstream)
    ensure_origin(src, manifest.upstream)
    ensure_real_index_clean(src)
    patches = list(validate_patch_directory(manifest))
    before_source = _checkout_fingerprint(src)
    with maintenance_workspace(manifest):
        replay = manifest.maintenance_checkout
        apply_patch_series(replay, patches, manifest=manifest)
        lock_versions = validate_cargo_lock_tree(manifest, replay)
        before_generation = changed_tree_digest(replay)
        first, second = run_generation_sequence(manifest, replay, twice=True)
        if changed_tree_digest(replay) != before_generation:
            raise PatchError(
                "tracked generated artifacts drift after applying the patch series"
            )
        commands = run_all_manifest_validation(manifest, replay)
    if _checkout_fingerprint(src) != before_source:
        raise PatchError("verify changed the editing checkout")
    return {
        "ref": manifest.upstream.ref,
        "revision": manifest.upstream.revision,
        "patches": [patch.file for patch in manifest.patches],
        "changedWorkspaceVersions": lock_versions,
        "generationDigest": first,
        "secondGenerationDigest": second,
        "targetedCommands": len(commands),
    }


def manifest_order(manifest: MaintenanceManifest) -> list[str]:
    return [patch.file for patch in manifest.patches]


def add_repo_root_argument(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=DEFAULT_REPO_ROOT,
        help="repository root containing codex/upstream.toml and codex/series.toml",
    )


def main_wrapper(fn: Callable[[], int]) -> int:
    try:
        return fn()
    except PatchError as exc:
        eprint(f"error: {exc}")
        eprint(
            "recovery: preserve user changes, fix the reported contract, and rerun "
            "the same command"
        )
        return 1
    except Exception as exc:  # noqa: BLE001
        eprint(f"error: unexpected maintenance failure: {exc}")
        eprint(
            "recovery: inspect the unchanged source checkout and outer Git diff before retrying"
        )
        return 1
