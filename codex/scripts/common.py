from __future__ import annotations

import argparse
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
        return path in self.files or any(path.startswith(prefix) for prefix in self.prefixes)


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

    def owner_for(self, path: str) -> PatchSpec:
        owners = [patch for patch in self.patches if patch.owns(path)]
        if not owners:
            raise PatchError(
                f"changed path has no owner in codex/{SERIES_FILE}: {path}; "
                "declare it as an exact file or explicit directory prefix"
            )
        if len(owners) != 1:
            names = ", ".join(owner.file for owner in owners)
            raise PatchError(f"changed path has multiple owners in codex/{SERIES_FILE}: {path}: {names}")
        return owners[0]


FaultHook = Callable[[str], None]
_V8_ENV_CACHE: dict[tuple[Path, str], dict[str, str]] = {}


def eprint(message: str) -> None:
    print(message, file=sys.stderr)


def json_stdout(payload: Mapping[str, Any]) -> None:
    print(json.dumps(payload, sort_keys=True))


def _strict_fields(data: Mapping[str, Any], expected: set[str], source: Path, context: str) -> None:
    unknown = sorted(set(data) - expected)
    if unknown:
        raise PatchError(f"unknown {context} field(s) in {source}: {', '.join(unknown)}")
    missing = sorted(expected - set(data))
    if missing:
        raise PatchError(f"missing {context} field(s) in {source}: {', '.join(missing)}")


def _string(value: Any, source: Path, field: str) -> str:
    if not isinstance(value, str) or not value.strip():
        raise PatchError(f"{source}: {field} must be a non-empty string")
    return value


def _command(value: Any, source: Path, field: str) -> tuple[str, ...]:
    if not isinstance(value, list) or not value:
        raise PatchError(f"{source}: {field} must be a non-empty array of command arguments")
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
    if parsed.is_absolute() or candidate.startswith("/") or ".." in parsed.parts or "." in parsed.parts:
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


def load_manifest(root: Path = DEFAULT_REPO_ROOT, *, require_patch_files: bool = True) -> MaintenanceManifest:
    root = root.resolve()
    upstream_path = root / CODEX_DIR / UPSTREAM_FILE
    series_path = root / CODEX_DIR / SERIES_FILE
    upstream_data = _read_toml(upstream_path)
    _strict_fields(upstream_data, UPSTREAM_FIELDS, upstream_path, "upstream")
    upstream = Upstream(
        url=_string(upstream_data["url"], upstream_path, "url"),
        ref=_string(upstream_data["ref"], upstream_path, "ref"),
        revision=_string(upstream_data["revision"], upstream_path, "revision"),
        worktree=_path(upstream_data["worktree"], upstream_path, "worktree", prefix=False),
        generate_commands=_commands(
            upstream_data["generate_commands"], upstream_path, "generate_commands"
        ),
        regression_commands=_commands(
            upstream_data["regression_commands"], upstream_path, "regression_commands"
        ),
        validation_command=_command(upstream_data["validation_command"], upstream_path, "validation_command"),
    )
    if not upstream.ref.startswith("rust-v"):
        raise PatchError(f"{upstream_path}: ref must be a rust-v tag: {upstream.ref}")
    if not REVISION_RE.fullmatch(upstream.revision):
        raise PatchError(f"{upstream_path}: revision must be a lowercase 40-character Git commit")

    series_data = _read_toml(series_path)
    _strict_fields(series_data, SERIES_FIELDS, series_path, "series")
    raw_patches = series_data["patch"]
    if not isinstance(raw_patches, list) or not raw_patches:
        raise PatchError(f"{series_path}: patch must contain at least one [[patch]] table")

    patches: list[PatchSpec] = []
    for index, raw_patch in enumerate(raw_patches, start=1):
        if not isinstance(raw_patch, dict):
            raise PatchError(f"{series_path}: patch #{index} must be a table")
        _strict_fields(raw_patch, PATCH_FIELDS, series_path, f"patch #{index}")
        filename = _path(raw_patch["file"], series_path, f"patch #{index}.file", prefix=False)
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
                intent=_string(raw_patch["intent"], series_path, f"patch #{index}.intent"),
                behavior=_string(raw_patch["behavior"], series_path, f"patch #{index}.behavior"),
                source_files=_paths(
                    raw_patch["source_files"], series_path, f"patch #{index}.source_files", prefix=False
                ),
                source_prefixes=_paths(
                    raw_patch["source_prefixes"], series_path, f"patch #{index}.source_prefixes", prefix=True
                ),
                generated_files=_paths(
                    raw_patch["generated_files"], series_path, f"patch #{index}.generated_files", prefix=False
                ),
                generated_prefixes=_paths(
                    raw_patch["generated_prefixes"],
                    series_path,
                    f"patch #{index}.generated_prefixes",
                    prefix=True,
                ),
                tests=_commands(raw_patch["tests"], series_path, f"patch #{index}.tests"),
            )
        )

    filenames = [patch.file for patch in patches]
    if len(filenames) != len(set(filenames)):
        raise PatchError(f"{series_path}: duplicate patch filename")
    _validate_ownership(patches, series_path)
    manifest = MaintenanceManifest(root=root, upstream=upstream, patches=tuple(patches))
    if require_patch_files:
        missing = [patch.file for patch in patches if not (manifest.patch_dir / patch.file).is_file()]
        if missing:
            raise PatchError(f"missing patch file(s) in {manifest.patch_dir}: {', '.join(missing)}")
    return manifest


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
        raise PatchError(f"command failed with exit {completed.returncode}: {' '.join(command)}")
    return completed


def jobs_limit() -> int:
    return max(1, (os.cpu_count() or 2) - 1)


def upstream_build_env(manifest: MaintenanceManifest) -> dict[str, str]:
    return {
        "CARGO_TARGET_DIR": str(manifest.worktree.parent / "target"),
        # Match the pinned upstream Just test runner. Several deeply nested
        # integration fixtures overflow Rust's smaller default test-thread stack.
        "RUST_MIN_STACK": "8388608",
    }


def _codex_v8_env(manifest: MaintenanceManifest, cwd: Path) -> dict[str, str]:
    archive = os.environ.get("RUSTY_V8_ARCHIVE")
    binding = os.environ.get("RUSTY_V8_SRC_BINDING_PATH")
    if archive and binding:
        return {}
    if archive or binding:
        raise PatchError("RUSTY_V8_ARCHIVE and RUSTY_V8_SRC_BINDING_PATH must be set together")

    source_root = cwd.parent if cwd.name == "codex-rs" else cwd
    helper = source_root / "scripts" / "codex_package" / "v8.py"
    if not helper.is_file():
        return {}
    rustc = run(["rustc", "-vV"], capture=True)
    target_lines = [line for line in rustc.stdout.splitlines() if line.startswith("host: ")]
    if len(target_lines) != 1:
        raise PatchError("could not determine the Rust host target for Codex V8 artifacts")
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
        raise PatchError("upstream V8 artifact helper did not produce both verified files")
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
    if args and args[0] == "cargo" and not (cwd / "Cargo.toml").is_file() and (cwd / "codex-rs" / "Cargo.toml").is_file():
        command_cwd = cwd / "codex-rs"
    if len(args) >= 2 and args[0] == "cargo" and "--jobs" not in args and "-j" not in args:
        args[2:2] = ["--jobs", str(jobs_limit())]
    environment = upstream_build_env(manifest)
    environment.update(_codex_v8_env(manifest, command_cwd))
    return run(args, cwd=command_cwd, env=environment)


def git_output(src: Path, args: Sequence[str], *, env: Mapping[str, str] | None = None) -> str:
    return run(["git", *args], cwd=src, capture=True, env=env).stdout.strip()


def require_git_worktree(src: Path) -> None:
    result = run(["git", "rev-parse", "--is-inside-work-tree"], cwd=src, check=False, capture=True)
    if result.returncode != 0 or result.stdout.strip() != "true":
        raise PatchError(f"missing upstream checkout: {src}; run `just codex-fetch` first")


def ensure_clean(src: Path) -> None:
    status = git_output(src, ["status", "--short"])
    if status:
        raise PatchError(f"upstream checkout has local changes:\n{status}\nrecovery: refresh or discard them explicitly")


def ensure_real_index_clean(src: Path) -> None:
    result = run(["git", "diff", "--cached", "--quiet", "--exit-code"], cwd=src, check=False)
    if result.returncode not in (0, 1):
        raise PatchError(f"could not inspect the upstream Git index in {src}")
    if result.returncode == 1:
        raise PatchError("upstream checkout has staged changes; recovery: unstage them before maintenance")


def ensure_pinned_head(src: Path, upstream: Upstream) -> None:
    head = git_output(src, ["rev-parse", "HEAD"])
    if head != upstream.revision:
        raise PatchError(
            f"upstream checkout HEAD is {head}, expected {upstream.revision}; "
            "recovery: run `just codex-fetch`"
        )


def _check_worktree_ignored(manifest: MaintenanceManifest) -> None:
    relative = manifest.worktree.relative_to(manifest.root).as_posix()
    result = run(
        ["git", "check-ignore", "--quiet", "--", relative],
        cwd=manifest.root,
        check=False,
    )
    if result.returncode != 0:
        raise PatchError(f"configured worktree is not ignored by Git: {relative}")


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
        raise PatchError(f"unexpected origin URL in {src}: {origin}; expected {manifest.upstream.url}")
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
    resolved = git_output(src, ["rev-parse", f"refs/tags/{manifest.upstream.ref}^{{commit}}"])
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
        ["git", "-c", "protocol.file.allow=always", "clone", "--quiet", "--no-hardlinks", str(src), str(destination)]
    )
    run(["git", "checkout", "--quiet", "--detach", revision], cwd=destination)


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


def _temporary_index_diff(src: Path, paths: Sequence[str], index: Path, *, selected: Sequence[str] | None = None) -> str:
    env = {"GIT_INDEX_FILE": str(index)}
    run(["git", "read-tree", "HEAD"], cwd=src, env=env)
    if paths:
        run(["git", "add", "--", *paths], cwd=src, env=env)
    command = ["git", "diff", "--cached", "--binary", "--full-index", "--no-renames", "HEAD"]
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
            raise PatchError(f"owned path is not a file, symlink, or deletion: {relative}")
        digest.update(b"\0")
    return digest.hexdigest()


def changed_tree_digest(src: Path) -> tuple[tuple[str, ...], str]:
    """Hash every tracked or untracked change, including generated new files."""
    paths = tuple(_changed_paths(src))
    return paths, _tree_digest(src, paths)


def apply_patches(src: Path, patches: Sequence[Path], *, check_only: bool) -> None:
    if check_only:
        with tempfile.TemporaryDirectory(prefix="codex-apply-check-") as temporary:
            clone = Path(temporary) / "src"
            revision = git_output(src, ["rev-parse", "HEAD"])
            _clone_base(src, clone, revision)
            for patch in patches:
                run(["git", "apply", "--check", str(patch)], cwd=clone)
                run(["git", "apply", str(patch)], cwd=clone)
        return

    revision = git_output(src, ["rev-parse", "HEAD"])
    before_status = run(["git", "status", "--porcelain=v1", "-z"], cwd=src, capture=True).stdout
    index_path = Path(git_output(src, ["rev-parse", "--git-path", "index"]))
    if not index_path.is_absolute():
        index_path = src / index_path
    before_index = index_path.read_bytes()
    candidate = Path(tempfile.mkdtemp(prefix=f".{src.name}.apply-candidate-", dir=src.parent))
    retained_old: Path | None = None
    try:
        candidate.rmdir()
        _clone_base(src, candidate, revision)
        for patch in patches:
            run(["git", "apply", "--check", str(patch)], cwd=candidate)
            run(["git", "apply", str(patch)], cwd=candidate)

        if git_output(src, ["rev-parse", "HEAD"]) != revision:
            raise PatchError("upstream checkout HEAD changed while applying candidate patches")
        after_status = run(
            ["git", "status", "--porcelain=v1", "-z"], cwd=src, capture=True
        ).stdout
        if after_status != before_status:
            raise PatchError("upstream checkout changed while applying candidate patches")
        if index_path.read_bytes() != before_index:
            raise PatchError("upstream Git index changed while applying candidate patches")

        retained_old = _replace_directory_atomically(candidate, src)
        if retained_old is not None:
            eprint(f"warning: applied patches but old worktree remains at {retained_old}")
    finally:
        if candidate.exists() and candidate != retained_old:
            shutil.rmtree(candidate)


def patch_paths(manifest: MaintenanceManifest, *, directory: Path | None = None) -> list[Path]:
    base = directory or manifest.patch_dir
    return [base / patch.file for patch in manifest.patches]


def _validate_candidate(
    manifest: MaintenanceManifest,
    candidate: Path,
    expected: Path,
    changed: Sequence[str],
    temporary: Path,
    fault: FaultHook,
) -> None:
    validation = temporary / "validation"
    _clone_base(manifest.worktree, validation, manifest.upstream.revision)
    for patch in patch_paths(manifest, directory=candidate):
        fault("apply")
        run(["git", "apply", "--check", str(patch)], cwd=validation)
        run(["git", "apply", str(patch)], cwd=validation)
    if _tree_digest(validation, changed) != _tree_digest(expected, changed):
        raise PatchError("candidate patches do not reproduce the generated editing tree")
    fault("validate")
    run_upstream(manifest, manifest.upstream.validation_command, cwd=validation)
    seen: set[tuple[str, ...]] = set()
    for patch in manifest.patches:
        for command in patch.tests:
            if command in seen:
                continue
            seen.add(command)
            run_upstream(manifest, command, cwd=validation)
    for command in manifest.upstream.regression_commands:
        if command in seen:
            continue
        seen.add(command)
        run_upstream(manifest, command, cwd=validation)


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


def _replace_patch_directory(target: Path, candidate: Path, fault: FaultHook) -> Path | None:
    fault("replace")
    return _replace_directory_atomically(candidate, target)


@contextmanager
def _refresh_sandbox(manifest: MaintenanceManifest) -> Iterator[Path]:
    sandbox = manifest.worktree.parent / "refresh-sandbox"
    if sandbox.exists():
        raise PatchError(
            f"stale refresh sandbox blocks maintenance: {sandbox}; "
            "recovery: verify no refresh is running, then remove this ignored tool-owned directory"
        )
    sandbox.mkdir()
    try:
        yield sandbox
    finally:
        shutil.rmtree(sandbox, ignore_errors=True)


def refresh_patches(
    manifest: MaintenanceManifest,
    *,
    dry_run: bool,
    fault: FaultHook | None = None,
) -> dict[str, Any]:
    src = manifest.worktree
    require_git_worktree(src)
    ensure_pinned_head(src, manifest.upstream)
    ensure_real_index_clean(src)
    before_status = run(["git", "status", "--porcelain=v1", "-z"], cwd=src, capture=True).stdout
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
        source_diff = _temporary_index_diff(src, source_paths, temporary / "source.index")
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
            raise PatchError(f"could not copy editing tree into refresh sandbox: {applied.stderr.strip()}")

        for command in manifest.upstream.generate_commands:
            fault_hook("generate")
            run_upstream(manifest, command, cwd=working)
        changed = _changed_paths(working)
        if not changed:
            raise PatchError("generation produced no patchable changes")
        owners: dict[str, list[str]] = {patch.file: [] for patch in manifest.patches}
        for path in changed:
            owners[manifest.owner_for(path).file].append(path)
        empty = [name for name, paths in owners.items() if not paths]
        if empty:
            raise PatchError(f"refresh would produce empty patch file(s): {', '.join(empty)}")

        manifest.patch_dir.parent.mkdir(parents=True, exist_ok=True)
        candidate = Path(
            tempfile.mkdtemp(prefix=f".{manifest.upstream.ref}.candidate-", dir=manifest.patch_dir.parent)
        )
        retained_old: Path | None = None
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
            _validate_candidate(manifest, candidate, working, changed, temporary, fault_hook)
            patch_hashes = {
                patch.file: hashlib.sha256((candidate / patch.file).read_bytes()).hexdigest()
                for patch in manifest.patches
            }
            if not dry_run:
                retained_old = _replace_patch_directory(manifest.patch_dir, candidate, fault_hook)
                if retained_old is not None:
                    eprint(
                        "warning: refreshed patches but old patch directory remains at "
                        f"{retained_old}"
                    )
            return {
                "ref": manifest.upstream.ref,
                "revision": manifest.upstream.revision,
                "dryRun": dry_run,
                "worktree": str(src),
                "patches": [patch.file for patch in manifest.patches],
                "patchSha256": patch_hashes,
                "changedPaths": changed,
            }
        finally:
            if candidate.exists() and candidate != retained_old:
                shutil.rmtree(candidate)
            after_status = run(["git", "status", "--porcelain=v1", "-z"], cwd=src, capture=True).stdout
            after_index = index_path.read_bytes()
            if after_status != before_status:
                raise PatchError("refresh changed the editing worktree; recovery: inspect the upstream checkout")
            if after_index != before_index:
                raise PatchError("refresh changed the real Git index; recovery: inspect and unstage unexpected entries")


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
        eprint("recovery: fix the reported contract or restore the last committed patch set, then rerun the command")
        return 1
    except Exception as exc:  # noqa: BLE001
        eprint(f"error: unexpected maintenance failure: {exc}")
        eprint("recovery: the original patch set, editing worktree, and Git index were preserved; inspect and retry")
        return 1
