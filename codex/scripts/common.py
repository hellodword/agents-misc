from __future__ import annotations

import argparse
import difflib
import hashlib
import json
import os
import re
import shutil
import subprocess
import sys
import tempfile
import tomllib
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path, PurePosixPath
from typing import Any, Callable, Iterable, Iterator, Mapping, Sequence


DEFAULT_REPO_ROOT = Path(__file__).resolve().parents[2]
CODEX_DIR = "codex"
UPSTREAM_FILE = "upstream.toml"
SERIES_FILE = "series.toml"
PATCHES_DIR = "patches"
REVISION_RE = re.compile(r"[0-9a-f]{40}")
PATCH_FILE_RE = re.compile(r"(?P<number>[0-9]{4})-[a-z0-9]+(?:-[a-z0-9]+)*\.patch")
UPSTREAM_FIELDS = {
    "url",
    "ref",
    "revision",
    "worktree",
    "generate_commands",
    "regression_commands",
    "validation_command",
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


class PatchError(RuntimeError):
    pass


@dataclass(frozen=True)
class Upstream:
    url: str
    ref: str
    revision: str
    worktree: str
    generate_commands: tuple[tuple[str, ...], ...]
    regression_commands: tuple[tuple[str, ...], ...]
    validation_command: tuple[str, ...]


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
    def candidate_dir(self) -> Path:
        return self.runtime_root / "candidate"

    @property
    def backup_dir(self) -> Path:
        return self.runtime_root / "backup"

    @property
    def receipt_dir(self) -> Path:
        return self.worktree.parent / "validation"

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
                f"changed path has multiple owners in codex/{SERIES_FILE}: {path}: {names}"
            )
        return owners[0]


FaultHook = Callable[[str], None]
_V8_ENV_CACHE: dict[tuple[Path, str], dict[str, str]] = {}
_CARGO_LOCK_ENV_CACHE: dict[tuple[Path, str], dict[str, str]] = {}
_HUNK_HEADER_RE = re.compile(
    r"^(?P<prefix>@@ -(?P<old_start>\d+)(?:,(?P<old_count>\d+))? "
    r"\+(?P<new_start>\d+)(?:,(?P<new_count>\d+))? @@)(?P<suffix>.*\n?)$"
)


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
    command = tuple(_string(part, source, f"{field}[]") for part in value)
    return command


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
                    f"overlapping path ownership in {source}: {left_owner}:{left} and {right_owner}:{right}"
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
        regression_commands=_commands(
            upstream_data["regression_commands"], upstream_path, "regression_commands"
        ),
        validation_command=_command(
            upstream_data["validation_command"], upstream_path, "validation_command"
        ),
    )
    if not upstream.ref.startswith("rust-v"):
        raise PatchError(f"{upstream_path}: ref must be a rust-v tag: {upstream.ref}")
    if not REVISION_RE.fullmatch(upstream.revision):
        raise PatchError(
            f"{upstream_path}: revision must be a lowercase 40-character Git commit"
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
        if not match:
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
    manifest = MaintenanceManifest(root=root, upstream=upstream, patches=tuple(patches))
    if require_patch_files:
        missing = [
            patch.file
            for patch in patches
            if not (manifest.patch_dir / patch.file).is_file()
        ]
        if missing:
            raise PatchError(
                f"missing patch file(s) in {manifest.patch_dir}: {', '.join(missing)}"
            )
    return manifest


def validate_patch_directory(
    manifest: MaintenanceManifest,
    *,
    directory: Path | None = None,
    require_exact: bool = True,
) -> tuple[Path, ...]:
    """Validate that the installed patch directory matches the manifest exactly.

    A stale patch is dangerous in a pure-patch workspace: Git may silently apply
    a file that is not represented by ``series.toml`` when a caller globs the
    directory.  Keep the check in one helper so Just, refresh, Nix, and tests
    all enforce the same boundary.
    """

    patch_dir = (directory or manifest.patch_dir).resolve()
    expected = tuple(patch.file for patch in manifest.patches)
    missing = [name for name in expected if not (patch_dir / name).is_file()]
    if missing:
        raise PatchError(f"missing patch file(s) in {patch_dir}: {', '.join(missing)}")
    if require_exact:
        actual = sorted(
            path.name for path in patch_dir.glob("*.patch") if path.is_file()
        )
        stale = sorted(set(actual) - set(expected))
        if stale:
            raise PatchError(
                f"patch directory contains files not listed in {CODEX_DIR}/{SERIES_FILE}: "
                f"{', '.join(stale)}"
            )
    return tuple(patch_dir / name for name in expected)


def _validate_refresh_source_patch_directory(
    manifest: MaintenanceManifest,
) -> tuple[Path, ...]:
    """Accept an exact current series or one pristine Git-tracked predecessor.

    A manifest update can rename, split, or combine patches before refresh has
    atomically installed the new directory. In that transition, validating the
    installed directory against the new manifest is necessarily impossible.
    Permit the predecessor only when Git proves every entry is an unchanged,
    tracked patch file with no staged, untracked, symlink, or nested debris.
    """

    try:
        return validate_patch_directory(manifest)
    except PatchError as manifest_error:
        patch_dir = manifest.patch_dir
        relative = patch_dir.relative_to(manifest.root).as_posix()
        if patch_dir.is_symlink() or not patch_dir.is_dir():
            raise PatchError(
                f"installed patch path is not a real directory: {patch_dir}"
            ) from manifest_error

        for diff_args, description in (
            (["diff", "--quiet", "--exit-code"], "unstaged"),
            (["diff", "--cached", "--quiet", "--exit-code"], "staged"),
        ):
            result = run(
                ["git", *diff_args, "--", relative],
                cwd=manifest.root,
                check=False,
                capture=True,
            )
            if result.returncode == 1:
                raise PatchError(
                    f"installed predecessor patch directory has {description} changes: "
                    f"{patch_dir}; preserve user changes and restore or commit them before refresh"
                ) from manifest_error
            if result.returncode != 0:
                raise PatchError(
                    f"could not inspect {description} predecessor patch changes in {patch_dir}"
                ) from manifest_error

        untracked = run(
            ["git", "ls-files", "--others", "--exclude-standard", "-z", "--", relative],
            cwd=manifest.root,
            capture=True,
        ).stdout.split("\0")
        untracked = sorted(path for path in untracked if path)
        if untracked:
            raise PatchError(
                "installed predecessor patch directory contains untracked files: "
                + ", ".join(untracked)
            ) from manifest_error

        entries = sorted(patch_dir.iterdir(), key=lambda path: path.name)
        invalid = [
            path
            for path in entries
            if path.is_symlink()
            or not path.is_file()
            or PATCH_FILE_RE.fullmatch(path.name) is None
        ]
        if invalid:
            raise PatchError(
                "installed predecessor patch directory contains non-patch, symlink, or nested entries: "
                + ", ".join(str(path) for path in invalid)
            ) from manifest_error
        if not entries:
            raise PatchError(
                f"installed predecessor patch directory is empty: {patch_dir}"
            ) from manifest_error

        tracked = run(
            ["git", "ls-files", "-z", "--", relative],
            cwd=manifest.root,
            capture=True,
        ).stdout.split("\0")
        tracked_set = {path for path in tracked if path}
        actual_set = {path.relative_to(manifest.root).as_posix() for path in entries}
        if tracked_set != actual_set:
            raise PatchError(
                "installed predecessor patch directory is not exactly Git-tracked: "
                f"tracked={sorted(tracked_set)} actual={sorted(actual_set)}"
            ) from manifest_error

        eprint(
            "transition: accepting pristine Git-tracked predecessor patch directory "
            f"for atomic replacement: {patch_dir}"
        )
        return tuple(entries)


def validate_generated_only_ownership(patch: PatchSpec) -> None:
    """Require a patch to contain generated ownership and no handwritten paths."""

    if patch.source_files or patch.source_prefixes:
        raise PatchError(
            f"generated-only patch {patch.file} declares handwritten ownership: "
            f"{list(patch.source_files) + list(patch.source_prefixes)}"
        )
    if not patch.generated_files and not patch.generated_prefixes:
        raise PatchError(
            f"generated-only patch {patch.file} declares no generated ownership"
        )


def validate_generated_changes(
    manifest: MaintenanceManifest, changed: Iterable[str]
) -> None:
    """Validate that paths owned by generated-only patches are generated paths."""

    changed_paths = tuple(changed)
    for patch in manifest.patches:
        if patch.generated_files or patch.generated_prefixes:
            # A patch may intentionally mix source and generated ownership during
            # a transition.  Only enforce the strict rule when it is source-free.
            if not patch.source_files and not patch.source_prefixes:
                validate_generated_only_ownership(patch)
    for path in changed_paths:
        owner = manifest.owner_for(path)
        if not owner.source_files and not owner.source_prefixes:
            if not (
                path in owner.generated_files
                or any(path.startswith(prefix) for prefix in owner.generated_prefixes)
            ):
                raise PatchError(
                    f"generated-only patch {owner.file} owns non-generated path: {path}"
                )


def run(
    args: Sequence[str],
    *,
    cwd: Path | None = None,
    check: bool = True,
    capture: bool = False,
    env: Mapping[str, str] | None = None,
) -> subprocess.CompletedProcess[str]:
    command = [str(arg) for arg in args]
    eprint(f"+ {' '.join(command)}")
    process_env = os.environ.copy()
    if env:
        process_env.update(env)
    completed = subprocess.run(
        command,
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
        raise PatchError(
            f"command failed with exit {completed.returncode}: {' '.join(command)}"
        )
    return completed


def jobs_limit() -> int:
    available = max(1, (os.cpu_count() or 2) - 1)
    configured = os.environ.get("CODEX_CARGO_JOBS")
    if configured is None:
        return available
    try:
        requested = int(configured)
    except ValueError as exc:
        raise PatchError("CODEX_CARGO_JOBS must be an integer") from exc
    if requested < 1:
        raise PatchError("CODEX_CARGO_JOBS must be at least 1")
    return requested


def _cargo_workspace_root(cwd: Path) -> Path | None:
    candidates = (cwd, cwd / "codex-rs")
    return next(
        (
            candidate
            for candidate in candidates
            if (candidate / "Cargo.toml").is_file()
            and (candidate / "Cargo.lock").is_file()
        ),
        None,
    )


def _cargo_lock_input_digest(workspace: Path, cargo_version: str) -> str:
    digest = hashlib.sha256()
    digest.update(cargo_version.encode())
    digest.update(b"\0")
    inputs = [workspace / "Cargo.lock"]
    for directory, names, files in os.walk(workspace):
        names[:] = sorted(name for name in names if name not in {".git", "target"})
        if "Cargo.toml" in files:
            inputs.append(Path(directory) / "Cargo.toml")
    for path in sorted(set(inputs)):
        digest.update(path.relative_to(workspace).as_posix().encode())
        digest.update(b"\0")
        digest.update(path.read_bytes())
        digest.update(b"\0")
    return digest.hexdigest()


def validate_cargo_lock_compatibility(
    pinned_bytes: bytes,
    normalized_bytes: bytes,
    *,
    workspace_version: str,
) -> int:
    """Allow only Cargo's local workspace-version normalization.

    Release tags can carry a workspace version newer than the local package
    entries in their checked-in lockfile.  An alternate ignored lock may
    normalize those local entries, but registry/git packages and dependency
    edges must remain byte-semantically identical.
    """

    try:
        pinned = tomllib.loads(pinned_bytes.decode())
        normalized = tomllib.loads(normalized_bytes.decode())
    except (UnicodeDecodeError, tomllib.TOMLDecodeError) as exc:
        raise PatchError(
            f"could not parse Cargo lockfile for compatibility: {exc}"
        ) from exc
    pinned_metadata = {key: value for key, value in pinned.items() if key != "package"}
    normalized_metadata = {
        key: value for key, value in normalized.items() if key != "package"
    }
    if pinned_metadata != normalized_metadata:
        raise PatchError("alternate Cargo lock changed non-package lock metadata")

    def split_packages(
        lock: Mapping[str, Any],
    ) -> tuple[
        dict[tuple[str, str, str], Mapping[str, Any]], dict[str, Mapping[str, Any]]
    ]:
        external: dict[tuple[str, str, str], Mapping[str, Any]] = {}
        local: dict[str, Mapping[str, Any]] = {}
        for package in lock.get("package", []):
            if not isinstance(package, dict):
                raise PatchError("Cargo lock package entry must be a table")
            name = str(package.get("name", ""))
            version = str(package.get("version", ""))
            source = package.get("source")
            if source is None:
                if not name or name in local:
                    raise PatchError(
                        f"Cargo lock has an invalid or duplicate local package: {name}"
                    )
                local[name] = package
            else:
                key = (name, version, str(source))
                if key in external:
                    raise PatchError(
                        f"Cargo lock has a duplicate external package: {key}"
                    )
                external[key] = package
        return external, local

    pinned_external, pinned_local = split_packages(pinned)
    normalized_external, normalized_local = split_packages(normalized)
    if pinned_external != normalized_external:
        raise PatchError("alternate Cargo lock changed external dependencies")
    if pinned_local.keys() != normalized_local.keys():
        raise PatchError("alternate Cargo lock changed the local workspace package set")

    changed_versions = 0
    for name in sorted(pinned_local):
        pinned_package = dict(pinned_local[name])
        normalized_package = dict(normalized_local[name])
        pinned_version = pinned_package.pop("version", None)
        normalized_version = normalized_package.pop("version", None)
        if pinned_package != normalized_package:
            raise PatchError(
                f"alternate Cargo lock changed local dependency metadata for {name}"
            )
        if normalized_version != workspace_version:
            raise PatchError(
                f"alternate Cargo lock has unexpected local version for {name}: "
                f"{normalized_version}; expected {workspace_version}"
            )
        if pinned_version != normalized_version:
            if pinned_version != "0.0.0":
                raise PatchError(
                    f"alternate Cargo lock changed non-development local version for {name}: "
                    f"{pinned_version} -> {normalized_version}"
                )
            changed_versions += 1
    return changed_versions


def cargo_lock_env(
    manifest: MaintenanceManifest,
    cwd: Path,
) -> dict[str, str]:
    workspace = _cargo_workspace_root(cwd)
    if workspace is None:
        return {}
    cargo_version = run(["cargo", "-Vv"], capture=True).stdout
    input_digest = _cargo_lock_input_digest(workspace, cargo_version)
    key = (workspace.resolve(), input_digest)
    cached = _CARGO_LOCK_ENV_CACHE.get(key)
    if cached is not None:
        return cached

    cargo_toml = tomllib.loads((workspace / "Cargo.toml").read_text())
    try:
        workspace_version = str(cargo_toml["workspace"]["package"]["version"])
    except (KeyError, TypeError) as exc:
        raise PatchError(
            "Cargo workspace is missing workspace.package.version"
        ) from exc
    pinned_lock = workspace / "Cargo.lock"
    pinned_bytes = pinned_lock.read_bytes()
    cache_root = manifest.worktree.parent / "cargo-lock"
    _check_ignored_path(manifest, cache_root)
    cache_dir = cache_root / input_digest
    alternate_lock = cache_dir / "Cargo.lock"
    receipt = cache_dir / "receipt.json"
    cache_dir.mkdir(parents=True, exist_ok=True)

    cache_valid = False
    if alternate_lock.is_file() and receipt.is_file():
        try:
            receipt_data = json.loads(receipt.read_text())
            normalized_bytes = alternate_lock.read_bytes()
            changed_versions = validate_cargo_lock_compatibility(
                pinned_bytes,
                normalized_bytes,
                workspace_version=workspace_version,
            )
            cache_valid = receipt_data == {
                "cargoVersion": cargo_version.strip(),
                "changedWorkspaceVersions": changed_versions,
                "inputDigest": input_digest,
                "normalizedSha256": hashlib.sha256(normalized_bytes).hexdigest(),
                "pinnedSha256": hashlib.sha256(pinned_bytes).hexdigest(),
                "workspaceVersion": workspace_version,
            }
        except (OSError, ValueError, PatchError, json.JSONDecodeError):
            cache_valid = False

    if not cache_valid:
        temporary = Path(tempfile.mkdtemp(prefix=".prepare-", dir=cache_dir))
        temporary_lock = temporary / "Cargo.lock"
        shutil.copyfile(pinned_lock, temporary_lock)
        prepare_environment = os.environ.copy()
        prepare_environment.update(
            {
                "CARGO_NET_OFFLINE": "true",
                "CARGO_RESOLVER_INCOMPATIBLE_RUST_VERSIONS": "fallback",
                "CARGO_RESOLVER_LOCKFILE_PATH": str(temporary_lock),
                "CARGO_UNSTABLE_LOCKFILE_PATH": "true",
            }
        )
        try:
            run(
                ["cargo", "tree", "--offline", "-p", "codex-config-schema"],
                cwd=workspace,
                env=prepare_environment,
                capture=True,
            )
            normalized_bytes = temporary_lock.read_bytes()
            changed_versions = validate_cargo_lock_compatibility(
                pinned_bytes,
                normalized_bytes,
                workspace_version=workspace_version,
            )
            if pinned_lock.read_bytes() != pinned_bytes:
                raise PatchError(
                    "alternate Cargo lock preparation changed project Cargo.lock"
                )
            run(
                [
                    "cargo",
                    "tree",
                    "--locked",
                    "--offline",
                    "-p",
                    "codex-config-schema",
                ],
                cwd=workspace,
                env=prepare_environment,
                capture=True,
            )
            receipt_data = {
                "cargoVersion": cargo_version.strip(),
                "changedWorkspaceVersions": changed_versions,
                "inputDigest": input_digest,
                "normalizedSha256": hashlib.sha256(normalized_bytes).hexdigest(),
                "pinnedSha256": hashlib.sha256(pinned_bytes).hexdigest(),
                "workspaceVersion": workspace_version,
            }
            os.replace(temporary_lock, alternate_lock)
            temporary_receipt = temporary / "receipt.json"
            temporary_receipt.write_text(
                json.dumps(receipt_data, indent=2, sort_keys=True) + "\n"
            )
            os.replace(temporary_receipt, receipt)
        finally:
            shutil.rmtree(temporary, ignore_errors=True)

    result = {
        "CARGO_RESOLVER_LOCKFILE_PATH": str(alternate_lock),
        "CARGO_UNSTABLE_LOCKFILE_PATH": "true",
    }
    _CARGO_LOCK_ENV_CACHE[key] = result
    return result


def upstream_build_env(
    manifest: MaintenanceManifest,
    cwd: Path,
) -> dict[str, str]:
    environment = {
        "CARGO_TARGET_DIR": str(manifest.worktree.parent / "cargo-target"),
        # Keep lockfile generation reproducible with the pinned Nix Rust toolchain;
        # Cargo otherwise selects registry releases requiring a newer compiler.
        "CARGO_RESOLVER_INCOMPATIBLE_RUST_VERSIONS": "fallback",
        # Match the pinned upstream Just test runner. Several deeply nested
        # integration fixtures overflow Rust's smaller default test-thread stack.
        "RUST_MIN_STACK": "8388608",
    }
    environment.update(cargo_lock_env(manifest, cwd))
    return environment


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
    key = (source_root, target)
    cached = _V8_ENV_CACHE.get(key)
    if cached is not None:
        return cached
    cache_root = manifest.worktree.parent / "rusty-v8"
    program = (
        "import json, sys; from pathlib import Path; "
        "sys.path.insert(0, sys.argv[1]); "
        "from scripts.codex_package.targets import TARGET_SPECS; "
        "from scripts.codex_package.v8 import fetch_codex_v8_artifacts; "
        "pair=fetch_codex_v8_artifacts(TARGET_SPECS[sys.argv[2]], cache_root=Path(sys.argv[3])); "
        "print(json.dumps({'archive': str(pair.archive), 'binding': str(pair.binding)}))"
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
) -> subprocess.CompletedProcess[str]:
    args = list(command)
    command_cwd = cwd
    if (
        args
        and args[0] == "cargo"
        and not (cwd / "Cargo.toml").is_file()
        and (cwd / "codex-rs" / "Cargo.toml").is_file()
    ):
        command_cwd = cwd / "codex-rs"
    if (
        len(args) >= 2
        and args[0] in {"bash", "sh"}
        and args[1] == "-c"
        and not (cwd / "Cargo.toml").is_file()
        and (cwd / "codex-rs" / "Cargo.toml").is_file()
    ):
        # Shell-wrapped regressions use the same crate-root convention as
        # direct Cargo commands when the caller supplied the repository root.
        command_cwd = cwd / "codex-rs"
    if (
        len(args) >= 2
        and args[0] == "cargo"
        and args[1] not in {"generate-lockfile", "metadata"}
        and "--jobs" not in args
        and "-j" not in args
    ):
        args[2:2] = ["--jobs", str(jobs_limit())]
    environment = upstream_build_env(manifest, command_cwd)
    if len(args) >= 2 and args[0] == "cargo" and args[1] == "generate-lockfile":
        # Resolve from the Nix-provided local index/cache so a checked-in lock
        # is reproducible even when the public registry advances between runs.
        environment["CARGO_NET_OFFLINE"] = "true"
    environment.update(_codex_v8_env(manifest, command_cwd))
    return run(args, cwd=command_cwd, env=environment)


def git_output(
    src: Path, args: Sequence[str], *, env: Mapping[str, str] | None = None
) -> str:
    return run(["git", *args], cwd=src, capture=True, env=env).stdout.strip()


def require_git_worktree(src: Path) -> None:
    result = run(
        ["git", "rev-parse", "--is-inside-work-tree"],
        cwd=src,
        check=False,
        capture=True,
    )
    if result.returncode != 0 or result.stdout.strip() != "true":
        raise PatchError(
            f"missing upstream checkout: {src}; run `just codex-fetch` first"
        )


def ensure_clean(src: Path) -> None:
    status = git_output(src, ["status", "--short"])
    if status:
        raise PatchError(
            f"upstream checkout has local changes:\n{status}\nrecovery: refresh or discard them explicitly"
        )


def ensure_real_index_clean(src: Path) -> None:
    result = run(
        ["git", "diff", "--cached", "--quiet", "--exit-code"], cwd=src, check=False
    )
    if result.returncode not in (0, 1):
        raise PatchError(f"could not inspect the upstream Git index in {src}")
    if result.returncode == 1:
        raise PatchError(
            "upstream checkout has staged changes; recovery: unstage them before maintenance"
        )


def ensure_pinned_head(src: Path, upstream: Upstream) -> None:
    head = git_output(src, ["rev-parse", "HEAD"])
    if head != upstream.revision:
        raise PatchError(
            f"upstream checkout HEAD is {head}, expected {upstream.revision}; "
            "recovery: run `just codex-fetch`"
        )


def _check_ignored_path(manifest: MaintenanceManifest, path: Path) -> None:
    try:
        relative = path.relative_to(manifest.root).as_posix()
    except ValueError as exc:
        raise PatchError(
            f"runtime path escapes the maintenance repository: {path}"
        ) from exc
    result = run(
        ["git", "check-ignore", "--quiet", "--", relative],
        cwd=manifest.root,
        check=False,
    )
    if result.returncode != 0:
        raise PatchError(f"runtime path is not ignored by Git: {relative}")


def _check_worktree_ignored(manifest: MaintenanceManifest) -> None:
    _check_ignored_path(manifest, manifest.worktree)


def _write_receipt(
    manifest: MaintenanceManifest,
    operation: str,
    payload: Mapping[str, Any],
) -> Path:
    _check_ignored_path(manifest, manifest.receipt_dir)
    manifest.receipt_dir.mkdir(parents=True, exist_ok=True)
    timestamp = datetime.now(UTC).strftime("%Y%m%dT%H%M%S.%fZ")
    receipt = manifest.receipt_dir / f"{timestamp}-{operation}.json"
    temporary = receipt.with_suffix(".json.tmp")
    temporary.write_text(json.dumps(payload, indent=2, sort_keys=True) + "\n")
    os.replace(temporary, receipt)
    return receipt


def fetch_upstream(manifest: MaintenanceManifest) -> str:
    _check_worktree_ignored(manifest)
    src = manifest.worktree
    src.parent.mkdir(parents=True, exist_ok=True)
    if not (src / ".git").exists():
        src.mkdir(parents=True, exist_ok=True)
        run(["git", "init"], cwd=src)
        run(["git", "remote", "add", "origin", manifest.upstream.url], cwd=src)
    require_git_worktree(src)
    origin = git_output(src, ["remote", "get-url", "origin"])
    if origin != manifest.upstream.url:
        raise PatchError(
            f"unexpected origin URL in {src}: {origin}; expected {manifest.upstream.url}"
        )
    ensure_clean(src)
    ensure_real_index_clean(src)
    run(
        [
            "git",
            "fetch",
            "--depth=1",
            "--force",
            "origin",
            f"refs/tags/{manifest.upstream.ref}:refs/tags/{manifest.upstream.ref}",
        ],
        cwd=src,
    )
    resolved = git_output(
        src, ["rev-parse", f"refs/tags/{manifest.upstream.ref}^{{commit}}"]
    )
    if resolved != manifest.upstream.revision:
        raise PatchError(
            f"tag {manifest.upstream.ref} resolves to {resolved}, expected {manifest.upstream.revision}; "
            "recovery: do not update patches until upstream.toml is reviewed"
        )
    run(["git", "checkout", "--detach", manifest.upstream.revision], cwd=src)
    ensure_pinned_head(src, manifest.upstream)
    return resolved


def _clone_base(src: Path, destination: Path, revision: str) -> None:
    run(
        [
            "git",
            "-c",
            "protocol.file.allow=always",
            "clone",
            "--quiet",
            "--no-hardlinks",
            str(src),
            str(destination),
        ]
    )
    run(["git", "checkout", "--quiet", "--detach", revision], cwd=destination)


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
    paths = sorted({path for path in [*tracked, *untracked] if path})
    return paths


def _normalize_generated_diff(diff: str) -> str:
    """Remove Git's synthetic EOF context while preserving hunk counts."""

    lines = diff.splitlines(keepends=True)
    removed = 0
    while lines and lines[-1] == " \n":
        lines.pop()
        removed += 1
    if not removed:
        return diff

    for index in range(len(lines) - 1, -1, -1):
        match = _HUNK_HEADER_RE.match(lines[index])
        if match is None:
            continue
        old_count = int(match.group("old_count") or "1") - removed
        new_count = int(match.group("new_count") or "1") - removed
        if old_count < 0 or new_count < 0:
            raise PatchError("generated diff has an invalid synthetic EOF context")

        def format_range(start: str, count: int) -> str:
            return start if count == 1 else f"{start},{count}"

        lines[index] = (
            f"@@ -{format_range(match.group('old_start'), old_count)} "
            f"+{format_range(match.group('new_start'), new_count)} @@"
            f"{match.group('suffix')}"
        )
        return "".join(lines)

    raise PatchError("generated diff ended with context but contained no hunk header")


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
    return _normalize_generated_diff(
        run(command, cwd=src, capture=True, env=env).stdout
    )


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
    """Hash every tracked or untracked change, including generated new files."""
    paths = tuple(_changed_paths(src))
    return paths, _tree_digest(src, paths)


def _iter_tree_paths(root: Path) -> Iterator[Path]:
    """Yield deterministic file and symlink paths while ignoring VCS/build state."""

    for path in sorted(root.rglob("*")):
        relative = path.relative_to(root)
        if ".git" in relative.parts or "target" in relative.parts:
            continue
        if path.is_file() or path.is_symlink():
            yield path


def _is_declared_generated(manifest: MaintenanceManifest, path: str) -> bool:
    return any(patch.owns_generated(path) for patch in manifest.patches)


def declared_generated_paths(
    manifest: MaintenanceManifest, root: Path
) -> tuple[str, ...]:
    """Return every exact or prefix-owned generated path, including deletions."""

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


def _non_generated_change_digest(
    manifest: MaintenanceManifest,
    root: Path,
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
    manifest: MaintenanceManifest,
    worktree: Path,
) -> tuple[str, ...]:
    """Create empty compile-time inputs for newly declared exact outputs.

    Some generators compile consumers that use ``include_str!`` before writing
    a newly introduced generated file.  Exact generated-file declarations make
    that bootstrap boundary explicit.  The caller must prove that the first
    generator pass replaced every empty placeholder.
    """

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


def run_generation_sequence(
    manifest: MaintenanceManifest,
    worktree: Path,
    *,
    twice: bool = True,
    fault: FaultHook | None = None,
) -> tuple[str, str | None]:
    """Run the declared generators and optionally prove a second run is stable.

    The returned pair is ``(first_digest, second_digest)``. The digest covers
    only manifest-declared generated files and prefixes. Any generator change
    outside that boundary is rejected independently.
    """

    fault_hook = fault or (lambda _stage: None)
    before_non_generated = _non_generated_change_digest(manifest, worktree)
    before_non_generated_paths = _path_digest_map(worktree, before_non_generated[0])
    bootstrapped = _bootstrap_missing_generated_files(manifest, worktree)
    for command in manifest.upstream.generate_commands:
        fault_hook("generate")
        run_upstream(manifest, command, cwd=worktree)
    incomplete_bootstraps = [
        path
        for path in bootstrapped
        if not (worktree / path).is_file() or not (worktree / path).read_bytes()
    ]
    if incomplete_bootstraps:
        raise PatchError(
            "generator did not replace bootstrapped generated file(s): "
            + ", ".join(incomplete_bootstraps)
        )
    after_first_non_generated = _non_generated_change_digest(manifest, worktree)
    if after_first_non_generated != before_non_generated:
        after_non_generated_paths = _path_digest_map(
            worktree, after_first_non_generated[0]
        )
        changed_paths = sorted(
            path
            for path in before_non_generated_paths.keys()
            | after_non_generated_paths.keys()
            if before_non_generated_paths.get(path)
            != after_non_generated_paths.get(path)
        )
        raise PatchError(
            "generator changed path(s) outside manifest-declared generated ownership: "
            + ", ".join(changed_paths)
        )
    _, first = generated_tree_digest(manifest, worktree)
    if not twice:
        return first, None
    for command in manifest.upstream.generate_commands:
        fault_hook("generate")
        run_upstream(manifest, command, cwd=worktree)
    after_second_non_generated = _non_generated_change_digest(manifest, worktree)
    if after_second_non_generated != before_non_generated:
        after_non_generated_paths = _path_digest_map(
            worktree, after_second_non_generated[0]
        )
        changed_paths = sorted(
            path
            for path in before_non_generated_paths.keys()
            | after_non_generated_paths.keys()
            if before_non_generated_paths.get(path)
            != after_non_generated_paths.get(path)
        )
        raise PatchError(
            "generator changed path(s) outside manifest-declared generated ownership: "
            + ", ".join(changed_paths)
        )
    _, second = generated_tree_digest(manifest, worktree)
    if first != second:
        raise PatchError(
            "generator sequence is not reproducible: the second run changed the generated tree "
            f"({first} -> {second})"
        )
    return first, second


def _canonical_json(value: Any) -> Any:
    """Return a recursively key-sorted JSON value for semantic comparisons."""

    if isinstance(value, dict):
        return {key: _canonical_json(value[key]) for key in sorted(value)}
    if isinstance(value, list):
        return [_canonical_json(item) for item in value]
    return value


def _read_zstd_json(path: Path) -> Any:
    if not path.is_file():
        raise PatchError(f"missing zstd fixture: {path}")
    result = run(
        ["zstd", "--quiet", "--decompress", "--stdout", str(path)], capture=True
    )
    try:
        return _canonical_json(json.loads(result.stdout))
    except json.JSONDecodeError as exc:
        raise PatchError(f"zstd fixture is not valid JSON: {path}") from exc


def zstd_semantic_diff(expected: Path, actual: Path) -> tuple[str, ...]:
    """Compare two zstd-compressed JSON fixtures after decompression.

    Byte identity is still required for generated trees. This helper gives the
    maintenance and Nix gates a stable semantic review for binary schema hunks.
    """

    expected_json = json.dumps(
        _read_zstd_json(expected), sort_keys=True, indent=2
    ).splitlines()
    actual_json = json.dumps(
        _read_zstd_json(actual), sort_keys=True, indent=2
    ).splitlines()
    return tuple(
        difflib.unified_diff(
            expected_json,
            actual_json,
            fromfile=str(expected),
            tofile=str(actual),
            lineterm="",
        )
    )


def assert_zstd_semantically_equal(expected: Path, actual: Path) -> None:
    diff = zstd_semantic_diff(expected, actual)
    if diff:
        raise PatchError("zstd JSON semantic diff detected:\n" + "\n".join(diff))


def assert_zstd_fixture_trees_semantically_equal(
    expected_root: Path, actual_root: Path
) -> None:
    """Compare every matching ``*.json.zst`` fixture in two trees."""

    expected_paths = {
        path.relative_to(expected_root).as_posix(): path
        for path in _iter_tree_paths(expected_root)
        if path.name.endswith(".json.zst")
    }
    actual_paths = {
        path.relative_to(actual_root).as_posix(): path
        for path in _iter_tree_paths(actual_root)
        if path.name.endswith(".json.zst")
    }
    if set(expected_paths) != set(actual_paths):
        raise PatchError(
            "zstd fixture path set differs: "
            f"expected={sorted(expected_paths)} actual={sorted(actual_paths)}"
        )
    for relative in sorted(expected_paths):
        assert_zstd_semantically_equal(expected_paths[relative], actual_paths[relative])


def validate_zstd_fixture_tree(root: Path) -> tuple[str, ...]:
    """Decompress and validate every JSON zstd fixture in ``root``."""

    paths = tuple(
        path.relative_to(root).as_posix()
        for path in _iter_tree_paths(root)
        if path.name.endswith(".json.zst")
    )
    for relative in paths:
        _read_zstd_json(root / relative)
    return paths


def run_manifest_validation(
    manifest: MaintenanceManifest,
    worktree: Path,
    *,
    include_patch_tests: bool = True,
) -> tuple[tuple[str, ...], ...]:
    """Run validation, focused patch tests, and manifest regressions once each."""

    run_upstream(manifest, manifest.upstream.validation_command, cwd=worktree)
    seen: set[tuple[str, ...]] = set()
    if include_patch_tests:
        for patch in manifest.patches:
            for command in patch.tests:
                if command in seen:
                    continue
                seen.add(command)
                run_upstream(manifest, command, cwd=worktree)
    for command in manifest.upstream.regression_commands:
        if command in seen:
            continue
        seen.add(command)
        run_upstream(manifest, command, cwd=worktree)
    return tuple(sorted(seen))


def patch_contains_binary_data(patch: Path) -> bool:
    payload = patch.read_bytes()
    marker_present = b"GIT binary patch" in payload
    header_present = (
        re.search(rb"(?m)^GIT binary patch\n(?:literal|delta) [0-9]+\n", payload)
        is not None
    )
    if marker_present != header_present:
        raise PatchError(f"malformed Git binary patch header: {patch}")
    return header_present


def validate_binary_patches(patches: Sequence[Path]) -> tuple[str, ...]:
    """Identify patches containing real Git binary patch headers without cloning."""

    return tuple(patch.name for patch in patches if patch_contains_binary_data(patch))


def apply_patch_series(
    checkout: Path,
    patches: Sequence[Path],
    *,
    fault: FaultHook | None = None,
) -> tuple[str, ...]:
    """Check and cumulatively apply one series to an existing clean checkout."""

    binary = validate_binary_patches(patches)
    fault_hook = fault or (lambda _stage: None)
    for patch in patches:
        fault_hook("apply")
        run(["git", "apply", "--check", "--binary", str(patch)], cwd=checkout)
        run(["git", "apply", "--binary", str(patch)], cwd=checkout)
    return binary


def apply_patches(
    src: Path,
    patches: Sequence[Path],
    *,
    check_only: bool,
) -> tuple[str, ...]:
    revision = git_output(src, ["rev-parse", "HEAD"])
    before_status = run(
        ["git", "status", "--porcelain=v1", "-z"], cwd=src, capture=True
    ).stdout
    index_path = Path(git_output(src, ["rev-parse", "--git-path", "index"]))
    if not index_path.is_absolute():
        index_path = src / index_path
    before_index = index_path.read_bytes()
    candidate = Path(
        tempfile.mkdtemp(prefix=f".{src.name}.apply-candidate-", dir=src.parent)
    )
    retained_old: Path | None = None
    try:
        candidate.rmdir()
        _clone_base(src, candidate, revision)
        binary = apply_patch_series(candidate, patches)

        if check_only:
            return binary

        if git_output(src, ["rev-parse", "HEAD"]) != revision:
            raise PatchError(
                "upstream checkout HEAD changed while applying candidate patches"
            )
        after_status = run(
            ["git", "status", "--porcelain=v1", "-z"], cwd=src, capture=True
        ).stdout
        if after_status != before_status:
            raise PatchError(
                "upstream checkout changed while applying candidate patches"
            )
        if index_path.read_bytes() != before_index:
            raise PatchError(
                "upstream Git index changed while applying candidate patches"
            )

        retained_old = _replace_directory_atomically(candidate, src)
        if retained_old is not None:
            eprint(
                f"warning: applied patches but old worktree remains at {retained_old}"
            )
        return binary
    finally:
        if candidate.exists() and candidate != retained_old:
            shutil.rmtree(candidate)


def patch_paths(
    manifest: MaintenanceManifest, *, directory: Path | None = None
) -> list[Path]:
    base = directory or manifest.patch_dir
    return [base / patch.file for patch in manifest.patches]


def _validate_candidate(
    manifest: MaintenanceManifest,
    candidate: Path,
    checkout: Path,
    expected_paths: Sequence[str],
    expected_digest: str,
    fault: FaultHook,
) -> tuple[tuple[str, ...], tuple[tuple[str, ...], ...]]:
    validate_patch_directory(manifest, directory=candidate)
    run(["git", "reset", "--hard", manifest.upstream.revision], cwd=checkout)
    run(["git", "clean", "-ffdx"], cwd=checkout)
    binary = apply_patch_series(
        checkout,
        patch_paths(manifest, directory=candidate),
        fault=fault,
    )
    replay_paths, replay_digest = changed_tree_digest(checkout)
    validate_generated_changes(manifest, replay_paths)
    if tuple(expected_paths) != replay_paths or expected_digest != replay_digest:
        raise PatchError(
            "candidate patches do not reproduce the generated editing tree"
        )
    validate_zstd_fixture_tree(checkout)
    fault("validate")
    seen = run_manifest_validation(manifest, checkout)
    return binary, seen


def _exchange_directories(candidate: Path, target: Path) -> None:
    completed = run(
        ["mv", "-T", "--exchange", "--no-copy", "--", str(candidate), str(target)],
        check=False,
        capture=True,
    )
    if completed.returncode != 0:
        detail = completed.stderr.strip() or completed.stdout.strip() or "no diagnostic"
        raise PatchError(
            f"atomic directory exchange failed for {candidate} and {target}: {detail}; "
            "both directories were preserved"
        )


def _replace_directory_atomically(candidate: Path, target: Path) -> Path | None:
    if not target.exists():
        try:
            os.replace(candidate, target)
        except OSError as exc:
            raise PatchError(
                f"atomic directory rename failed for {candidate} to {target}: {exc}; "
                "the candidate was preserved"
            ) from exc
        return None

    _exchange_directories(candidate, target)
    try:
        shutil.rmtree(candidate)
    except OSError:
        return candidate
    return None


def _patch_hashes(manifest: MaintenanceManifest, directory: Path) -> dict[str, str]:
    validate_patch_directory(manifest, directory=directory)
    return {
        patch.file: hashlib.sha256((directory / patch.file).read_bytes()).hexdigest()
        for patch in manifest.patches
    }


def _install_patch_directory(
    manifest: MaintenanceManifest,
    candidate: Path,
    expected_hashes: Mapping[str, str],
    fault: FaultHook,
) -> tuple[dict[str, str], tuple[Path, ...]]:
    target = manifest.patch_dir
    backup = manifest.backup_dir
    if backup.exists():
        raise PatchError(f"stale refresh backup blocks installation: {backup}")

    fault("replace")
    target_existed = target.exists()
    if target_existed:
        _exchange_directories(candidate, target)
        try:
            os.replace(candidate, backup)
        except OSError as exc:
            try:
                _exchange_directories(candidate, target)
            except Exception as restore_exc:
                raise PatchError(
                    "could not move the previous patch tree to its ignored backup and rollback failed; "
                    f"installed={target} candidate={candidate} backup={backup}: {restore_exc}"
                ) from exc
            raise PatchError(
                f"could not create ignored patch backup at {backup}; the installed tree was restored"
            ) from exc
    else:
        target.parent.mkdir(parents=True, exist_ok=True)
        os.replace(candidate, target)

    try:
        shutil.copytree(target, candidate)
        fault("installed")
        installed_hashes = _patch_hashes(manifest, target)
        if installed_hashes != dict(expected_hashes):
            raise PatchError(
                "installed patch hashes differ from the validated candidate"
            )
    except Exception as install_exc:
        try:
            if target_existed:
                _exchange_directories(backup, target)
                shutil.rmtree(backup)
            else:
                shutil.rmtree(target)
        except Exception as restore_exc:
            raise PatchError(
                "patch installation failed and automatic restoration also failed; "
                f"installed={target} candidate={candidate} backup={backup}: {restore_exc}"
            ) from install_exc
        raise

    retained_paths: list[Path] = []
    for runtime_path in (candidate, backup):
        if not runtime_path.exists():
            continue
        try:
            shutil.rmtree(runtime_path)
        except OSError:
            retained_paths.append(runtime_path)
            eprint(
                f"warning: installed patches but runtime tree remains at {runtime_path}"
            )
    return installed_hashes, tuple(retained_paths)


@contextmanager
def maintenance_sandbox(manifest: MaintenanceManifest, name: str) -> Iterator[Path]:
    if not re.fullmatch(r"[a-z][a-z0-9-]*", name):
        raise PatchError(f"invalid maintenance sandbox name: {name}")
    _check_ignored_path(manifest, manifest.runtime_root)
    manifest.runtime_root.mkdir(parents=True, exist_ok=True)
    sandbox = manifest.runtime_root / name
    if sandbox.exists():
        raise PatchError(
            f"stale maintenance workspace blocks operation: {sandbox}; "
            "recovery: verify no workflow is running, then remove this ignored tool-owned directory"
        )
    sandbox.mkdir()
    try:
        yield sandbox
    finally:
        shutil.rmtree(sandbox, ignore_errors=True)


@contextmanager
def _refresh_sandbox(manifest: MaintenanceManifest) -> Iterator[Path]:
    for path in (
        manifest.runtime_root,
        manifest.candidate_dir,
        manifest.backup_dir,
        manifest.receipt_dir,
    ):
        _check_ignored_path(manifest, path)
    if manifest.candidate_dir.exists():
        raise PatchError(
            f"stale refresh candidate blocks maintenance: {manifest.candidate_dir}; "
            "recovery: inspect its receipt and patches, then remove it explicitly"
        )
    if manifest.backup_dir.exists():
        raise PatchError(
            f"stale refresh backup blocks maintenance: {manifest.backup_dir}; "
            "recovery: verify the installed series before removing the backup"
        )
    with maintenance_sandbox(manifest, "refresh-workspace") as sandbox:
        yield sandbox


def refresh_patches(
    manifest: MaintenanceManifest,
    *,
    dry_run: bool,
    fault: FaultHook | None = None,
) -> dict[str, Any]:
    src = manifest.worktree
    if manifest.patch_dir.exists():
        _validate_refresh_source_patch_directory(manifest)
    require_git_worktree(src)
    ensure_pinned_head(src, manifest.upstream)
    ensure_real_index_clean(src)
    before_status = run(
        ["git", "status", "--porcelain=v1", "-z"], cwd=src, capture=True
    ).stdout
    index_path = Path(git_output(src, ["rev-parse", "--git-path", "index"]))
    if not index_path.is_absolute():
        index_path = src / index_path
    before_index = index_path.read_bytes()
    fault_hook = fault or (lambda _stage: None)

    with _refresh_sandbox(manifest) as temporary:
        working = temporary / "working"
        _clone_base(src, working, manifest.upstream.revision)
        source_paths = _changed_paths(src)
        if not source_paths:
            raise PatchError("upstream worktree has no changes to refresh")
        for path in source_paths:
            manifest.owner_for(path)
        source_diff = _temporary_index_diff(
            src, source_paths, temporary / "source.index"
        )
        applied = subprocess.run(
            ["git", "apply", "--binary", "-"],
            cwd=working,
            input=source_diff,
            text=True,
            check=False,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
        )
        if applied.returncode != 0:
            raise PatchError(
                f"could not copy editing tree into refresh sandbox: {applied.stderr.strip()}"
            )

        first_generation_digest, second_generation_digest = run_generation_sequence(
            manifest,
            working,
            twice=True,
            fault=fault_hook,
        )
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
        candidate = manifest.candidate_dir
        candidate.mkdir()
        try:
            for patch in manifest.patches:
                text = _temporary_index_diff(
                    working,
                    changed,
                    temporary / f"{patch.file}.index",
                    selected=owners[patch.file],
                )
                if not text:
                    raise PatchError(f"refresh produced an empty patch: {patch.file}")
                (candidate / patch.file).write_text(text)
            binary_patches, seen = _validate_candidate(
                manifest,
                candidate,
                working,
                expected_paths,
                expected_digest,
                fault_hook,
            )
            patch_hashes = _patch_hashes(manifest, candidate)
            retained_runtime_paths: tuple[Path, ...] = ()
            installed_hashes: dict[str, str] | None = None
            if not dry_run:
                installed_hashes, retained_runtime_paths = _install_patch_directory(
                    manifest,
                    candidate,
                    patch_hashes,
                    fault_hook,
                )
            result: dict[str, Any] = {
                "ref": manifest.upstream.ref,
                "revision": manifest.upstream.revision,
                "dryRun": dry_run,
                "worktree": str(src),
                "patches": [patch.file for patch in manifest.patches],
                "patchSha256": patch_hashes,
                "changedPaths": changed,
                "binaryPatches": list(binary_patches),
                "generationDigest": first_generation_digest,
                "secondGenerationDigest": second_generation_digest,
                "targetedCommands": len(seen),
                "candidatePath": str(candidate) if dry_run else None,
                "installedSha256": installed_hashes,
                "retainedRuntimePaths": [str(path) for path in retained_runtime_paths],
            }
            receipt = _write_receipt(
                manifest,
                "refresh-dry-run" if dry_run else "refresh-install",
                result,
            )
            result["receiptPath"] = str(receipt)
            return result
        except Exception:
            if candidate.exists():
                eprint(f"recovery: preserved refresh candidate at {candidate}")
            if manifest.backup_dir.exists():
                eprint(
                    f"recovery: preserved previous patch backup at {manifest.backup_dir}"
                )
            raise
        finally:
            after_status = run(
                ["git", "status", "--porcelain=v1", "-z"], cwd=src, capture=True
            ).stdout
            after_index = index_path.read_bytes()
            if after_status != before_status:
                raise PatchError(
                    "refresh changed the editing worktree; recovery: inspect the upstream checkout"
                )
            if after_index != before_index:
                raise PatchError(
                    "refresh changed the real Git index; recovery: inspect and unstage unexpected entries"
                )


def manifest_order(manifest: MaintenanceManifest) -> list[str]:
    return [patch.file for patch in manifest.patches]


def add_repo_root_argument(parser: argparse.ArgumentParser) -> None:
    parser.add_argument(
        "--repo-root",
        type=Path,
        default=DEFAULT_REPO_ROOT,
        help="Repository root containing codex/upstream.toml and codex/series.toml",
    )


def main_wrapper(fn: Callable[[], int]) -> int:
    try:
        return fn()
    except PatchError as exc:
        eprint(f"error: {exc}")
        eprint(
            "recovery: fix the reported contract or restore the last committed patch set, then rerun the command"
        )
        return 1
    except Exception as exc:  # noqa: BLE001
        eprint(f"error: unexpected maintenance failure: {exc}")
        eprint(
            "recovery: the original patch set, editing worktree, and Git index were preserved; inspect and retry"
        )
        return 1
