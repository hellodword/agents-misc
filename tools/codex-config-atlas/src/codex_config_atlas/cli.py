from __future__ import annotations

import argparse
import json
import re
import shutil
import sys
import tempfile
import urllib.request
from pathlib import Path
from typing import Any

from .atomic_output import replace_directory
from .build_data import build_data
from .build_site import build_site
from .defaults_diff import build_defaults_diff, render_defaults_diff_markdown
from .registry import (
    SCHEMA_FILE_IN_UPSTREAM,
    ensure_supported_version,
    entry_for_version,
    json_dump,
    json_load,
    load_manifest,
    metadata_path_for_version,
    parse_version,
    save_manifest,
    schema_path_for_version,
    schema_url_for_version,
    sha256_bytes,
    tag_for_version,
    upsert_manifest_entry,
    utc_now_rfc3339,
    validate_manifest,
)
from .schema_diff import build_schema_diff, render_schema_diff_markdown
from .schema_normalize import defaults_from_fields, normalize_schema
from .toml_generate import generate_toml


GLOBAL_OPTION_NAMES = {
    "--current-version",
    "--current-tag",
    "--min-version",
}
NETWORK_TIMEOUT_SECONDS = 30.0
MAX_SCHEMA_BYTES = 8 * 1024 * 1024
MAX_GITHUB_METADATA_BYTES = 1024 * 1024
GITHUB_API_ROOT = "https://api.github.com/repos/openai/codex"


def _add_shared_options(parser: argparse.ArgumentParser) -> None:
    parser.add_argument("--current-version")
    parser.add_argument("--current-tag")
    parser.add_argument("--min-version", default="0.129.0")


def _normalize_global_args(argv: list[str]) -> list[str]:
    normalized: list[str] = []
    remainder: list[str] = []
    index = 0
    while index < len(argv):
        token = argv[index]
        option_name = token.split("=", 1)[0]
        if option_name in GLOBAL_OPTION_NAMES:
            if "=" in token:
                normalized.append(token)
                index += 1
                continue
            normalized.append(token)
            if index + 1 < len(argv):
                normalized.append(argv[index + 1])
                index += 2
            else:
                index += 1
            continue

        remainder.append(token)
        index += 1

    return normalized + remainder


def _write_output(text: str, out_path: str | None) -> None:
    if out_path:
        Path(out_path).write_text(text)
    else:
        sys.stdout.write(text)


def _schemas_dir_from_args(args: argparse.Namespace) -> Path:
    if getattr(args, "schemas", None):
        return Path(args.schemas).resolve()
    if getattr(args, "repo", None):
        return (
            Path(args.repo).resolve() / "tools" / "codex-config-atlas" / "schemas"
        ).resolve()
    raise ValueError("either --schemas or --repo is required")


def _fetch_limited(url: str, *, timeout_seconds: float, max_bytes: int) -> bytes:
    request = urllib.request.Request(
        url,
        headers={
            "Accept": "application/vnd.github+json",
            "User-Agent": "codex-config-atlas/0.1.0",
        },
    )
    with urllib.request.urlopen(request, timeout=timeout_seconds) as response:  # noqa: S310
        content_length = response.headers.get("Content-Length")
        if content_length is not None:
            try:
                declared_size = int(content_length)
            except ValueError as exc:
                raise ValueError(f"invalid Content-Length from {url}") from exc
            if declared_size < 0 or declared_size > max_bytes:
                raise ValueError(
                    f"response from {url} exceeds maximum {max_bytes} bytes"
                )

        payload = bytearray()
        while True:
            chunk = response.read(min(64 * 1024, max_bytes + 1 - len(payload)))
            if not chunk:
                break
            payload.extend(chunk)
            if len(payload) > max_bytes:
                raise ValueError(
                    f"response from {url} exceeds maximum {max_bytes} bytes"
                )
        return bytes(payload)


def _fetch_json(url: str, *, timeout_seconds: float) -> dict[str, Any]:
    payload = _fetch_limited(
        url,
        timeout_seconds=timeout_seconds,
        max_bytes=MAX_GITHUB_METADATA_BYTES,
    )
    try:
        value = json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid JSON response from {url}") from exc
    if not isinstance(value, dict):
        raise ValueError(f"expected JSON object from {url}")
    return value


def _checked_sha(value: Any, description: str) -> str:
    if not isinstance(value, str) or re.fullmatch(r"[0-9a-f]{40}", value) is None:
        raise ValueError(f"invalid {description}: {value!r}")
    return value


def _resolve_tag_commit(tag: str, *, timeout_seconds: float) -> str:
    expected_ref = f"refs/tags/{tag}"
    ref_payload = _fetch_json(
        f"{GITHUB_API_ROOT}/git/ref/tags/{tag}", timeout_seconds=timeout_seconds
    )
    if ref_payload.get("ref") != expected_ref:
        raise ValueError(
            f"tag response mismatch: {ref_payload.get('ref')!r} != {expected_ref!r}"
        )
    ref_object = ref_payload.get("object")
    if not isinstance(ref_object, dict):
        raise ValueError(f"tag response is missing object for {tag}")
    object_type = ref_object.get("type")
    object_sha = _checked_sha(ref_object.get("sha"), f"tag object SHA for {tag}")
    if object_type == "commit":
        return object_sha
    if object_type != "tag":
        raise ValueError(f"unsupported tag object type for {tag}: {object_type!r}")

    tag_payload = _fetch_json(
        f"{GITHUB_API_ROOT}/git/tags/{object_sha}", timeout_seconds=timeout_seconds
    )
    if tag_payload.get("tag") != tag:
        raise ValueError(
            f"annotated tag response mismatch: {tag_payload.get('tag')!r} != {tag!r}"
        )
    target = tag_payload.get("object")
    if not isinstance(target, dict) or target.get("type") != "commit":
        raise ValueError(f"annotated tag {tag} does not target a commit")
    return _checked_sha(target.get("sha"), f"commit SHA for {tag}")


def _fetch_schema(commit_sha: str, *, timeout_seconds: float, max_bytes: int) -> bytes:
    url = (
        f"https://raw.githubusercontent.com/openai/codex/{commit_sha}/"
        f"{SCHEMA_FILE_IN_UPSTREAM}"
    )
    return _fetch_limited(url, timeout_seconds=timeout_seconds, max_bytes=max_bytes)


def _write_schema_file(path: Path, schema_bytes: bytes) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(schema_bytes)


def _handle_current(args: argparse.Namespace) -> int:
    if not args.current_version:
        raise ValueError("--current-version is required for the current command")
    sys.stdout.write(f"{args.current_version}\n")
    return 0


def _handle_sync_schema(args: argparse.Namespace) -> int:
    schemas_dir = _schemas_dir_from_args(args)
    schemas_dir.parent.mkdir(parents=True, exist_ok=True)

    version = args.version or args.current_version
    if not version:
        raise ValueError("sync-schema requires --version or --current-version")
    ensure_supported_version(version, args.min_version)
    timeout_seconds = args.timeout_seconds
    max_bytes = args.max_bytes
    if timeout_seconds <= 0:
        raise ValueError("--timeout-seconds must be greater than zero")
    if max_bytes <= 0:
        raise ValueError("--max-bytes must be greater than zero")

    if schemas_dir.exists():
        installed_manifest = load_manifest(schemas_dir, min_version=args.min_version)
        validate_manifest(
            schemas_dir,
            installed_manifest,
            min_version=args.min_version,
        )

    tag = tag_for_version(version)
    commit_sha = _resolve_tag_commit(tag, timeout_seconds=timeout_seconds)
    expected_commit = args.expected_commit
    if expected_commit is not None:
        _checked_sha(expected_commit, "--expected-commit")
        if commit_sha != expected_commit:
            raise ValueError(
                f"resolved commit mismatch for {tag}: {commit_sha} != {expected_commit}"
            )

    url = schema_url_for_version(version)
    schema_bytes = _fetch_schema(
        commit_sha, timeout_seconds=timeout_seconds, max_bytes=max_bytes
    )
    schema_sha = sha256_bytes(schema_bytes)
    expected_sha = args.expected_sha256
    if expected_sha is not None and schema_sha != expected_sha:
        raise ValueError(
            f"schema hash mismatch for {version}: {schema_sha} != {expected_sha}"
        )
    try:
        schema = json.loads(schema_bytes)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"downloaded schema JSON is invalid for {version}") from exc
    if not isinstance(schema, dict):
        raise ValueError(f"downloaded schema root must be an object for {version}")

    candidate = Path(
        tempfile.mkdtemp(prefix=f".{schemas_dir.name}-sync-", dir=schemas_dir.parent)
    )
    retained_old: Path | None = None
    try:
        if schemas_dir.exists():
            shutil.copytree(schemas_dir, candidate, dirs_exist_ok=True, symlinks=True)

        manifest = load_manifest(candidate, min_version=args.min_version)
        existing_entry = entry_for_version(manifest, version)
        existing_metadata: dict[str, Any] | None = None
        if existing_entry is not None:
            existing_metadata = json_load(candidate / existing_entry.metadata_path)
            if existing_metadata["commitSha"] != commit_sha:
                raise ValueError(
                    f"registered commit mismatch for {tag}: "
                    f"{existing_metadata['commitSha']} != {commit_sha}"
                )

        schema_path = schema_path_for_version(candidate, version)
        metadata_path = metadata_path_for_version(candidate, version)
        if schema_path.exists():
            existing_sha = sha256_bytes(schema_path.read_bytes())
            if existing_sha != schema_sha:
                raise ValueError(
                    f"schema already exists for {version} but contents changed: "
                    f"{existing_sha} != {schema_sha}"
                )
        _write_schema_file(schema_path, schema_bytes)

        fetched_at = utc_now_rfc3339()
        if existing_metadata is not None:
            fetched_at = existing_metadata["fetchedAt"]

        metadata = {
            "version": version,
            "tag": tag,
            "commitSha": commit_sha,
            "schemaUrl": url,
            "schemaFile": SCHEMA_FILE_IN_UPSTREAM,
            "schemaSha256": schema_sha,
            "fetchedAt": fetched_at,
        }
        json_dump(metadata_path, metadata)

        manifest = upsert_manifest_entry(manifest, version)
        save_manifest(candidate, manifest)
        validate_manifest(candidate, manifest, min_version=args.min_version)

        retained_old = replace_directory(candidate, schemas_dir)
    finally:
        if candidate.exists() and candidate != retained_old:
            shutil.rmtree(candidate)

    sys.stdout.write(
        f"synced {version} -> {schema_path_for_version(schemas_dir, version)}\n"
    )
    if retained_old is not None:
        print(
            f"warning: installed registry but old registry remains at {retained_old}",
            file=sys.stderr,
        )
    return 0


def _handle_check_registry(args: argparse.Namespace) -> int:
    schemas_dir = Path(args.schemas).resolve()
    manifest = load_manifest(schemas_dir, min_version=args.min_version)
    validate_manifest(
        schemas_dir,
        manifest,
        current_version=args.current_version,
        min_version=args.min_version,
    )
    sys.stdout.write(
        f"registry ok: {len(manifest['versions'])} versions, current={args.current_version}\n"
    )
    return 0


def _load_version_inputs(
    schemas_dir: Path, version: str
) -> tuple[dict[str, Any], dict[str, Any]]:
    manifest = load_manifest(schemas_dir)
    entry = entry_for_version(manifest, version)
    if entry is None:
        raise ValueError(f"version {version} is not present in schema registry")
    schema = json_load(schemas_dir / entry.schema_path)
    metadata = json_load(schemas_dir / entry.metadata_path)
    return schema, metadata


def _handle_gen_toml(args: argparse.Namespace) -> int:
    schemas_dir = Path(args.schemas).resolve()
    schema, metadata = _load_version_inputs(schemas_dir, args.version)
    fields = normalize_schema(schema)
    output = generate_toml(
        args.version,
        tag_for_version(args.version),
        metadata["schemaUrl"],
        fields,
        args.mode,
    )
    _write_output(output, args.out)
    return 0


def _handle_diff(args: argparse.Namespace) -> int:
    schemas_dir = Path(args.schemas).resolve()
    from_schema, _ = _load_version_inputs(schemas_dir, args.from_version)
    to_schema, _ = _load_version_inputs(schemas_dir, args.to_version)
    payload = build_schema_diff(
        args.from_version,
        args.to_version,
        normalize_schema(from_schema),
        normalize_schema(to_schema),
    )
    if args.format == "json":
        _write_output(
            json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
            args.out,
        )
    else:
        _write_output(render_schema_diff_markdown(payload), args.out)
    return 0


def _handle_diff_defaults(args: argparse.Namespace) -> int:
    schemas_dir = Path(args.schemas).resolve()
    from_schema, _ = _load_version_inputs(schemas_dir, args.from_version)
    to_schema, _ = _load_version_inputs(schemas_dir, args.to_version)
    payload = build_defaults_diff(
        args.from_version,
        args.to_version,
        defaults_from_fields(normalize_schema(from_schema)),
        defaults_from_fields(normalize_schema(to_schema)),
    )
    if args.format == "json":
        _write_output(
            json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n",
            args.out,
        )
    else:
        _write_output(render_defaults_diff_markdown(payload), args.out)
    return 0


def _handle_build_data(args: argparse.Namespace) -> int:
    if not args.current_version:
        raise ValueError("build-data requires --current-version")
    build_data(
        schemas_dir=Path(args.schemas).resolve(),
        current_version=args.current_version,
        min_version=args.min_version,
        out_dir=Path(args.out).resolve(),
    )
    return 0


def _handle_build_site(args: argparse.Namespace) -> int:
    build_site(
        static_dir=Path(args.static).resolve(),
        data_dir=Path(args.data).resolve(),
        out_dir=Path(args.out).resolve(),
    )
    return 0


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(prog="codex-config-atlas")
    _add_shared_options(parser)

    subparsers = parser.add_subparsers(dest="command", required=True)

    current = subparsers.add_parser("current")
    current.set_defaults(handler=_handle_current)

    sync_schema = subparsers.add_parser("sync-schema")
    sync_schema.add_argument("--repo")
    sync_schema.add_argument("--schemas")
    sync_schema.add_argument("--version")
    sync_schema.add_argument(
        "--timeout-seconds", type=float, default=NETWORK_TIMEOUT_SECONDS
    )
    sync_schema.add_argument("--max-bytes", type=int, default=MAX_SCHEMA_BYTES)
    sync_schema.add_argument("--expected-commit")
    sync_schema.add_argument("--expected-sha256")
    sync_schema.set_defaults(handler=_handle_sync_schema)

    check_registry = subparsers.add_parser("check-registry")
    check_registry.add_argument("--schemas", required=True)
    check_registry.set_defaults(handler=_handle_check_registry)

    gen_toml = subparsers.add_parser("gen-toml")
    gen_toml.add_argument("--schemas", required=True)
    gen_toml.add_argument("--version", required=True)
    gen_toml.add_argument("--mode", choices=["default", "reference"], required=True)
    gen_toml.add_argument("--out")
    gen_toml.set_defaults(handler=_handle_gen_toml)

    diff = subparsers.add_parser("diff")
    diff.add_argument("--schemas", required=True)
    diff.add_argument("--from", dest="from_version", required=True)
    diff.add_argument("--to", dest="to_version", required=True)
    diff.add_argument("--format", choices=["json", "markdown"], default="markdown")
    diff.add_argument("--out")
    diff.set_defaults(handler=_handle_diff)

    diff_defaults = subparsers.add_parser("diff-defaults")
    diff_defaults.add_argument("--schemas", required=True)
    diff_defaults.add_argument("--from", dest="from_version", required=True)
    diff_defaults.add_argument("--to", dest="to_version", required=True)
    diff_defaults.add_argument(
        "--format", choices=["json", "markdown"], default="markdown"
    )
    diff_defaults.add_argument("--out")
    diff_defaults.set_defaults(handler=_handle_diff_defaults)

    build_data_parser = subparsers.add_parser("build-data")
    build_data_parser.add_argument("--schemas", required=True)
    build_data_parser.add_argument("--out", required=True)
    build_data_parser.set_defaults(handler=_handle_build_data)

    build_site_parser = subparsers.add_parser("build-site")
    build_site_parser.add_argument("--static", required=True)
    build_site_parser.add_argument("--data", required=True)
    build_site_parser.add_argument("--out", required=True)
    build_site_parser.set_defaults(handler=_handle_build_site)

    return parser


def main(argv: list[str] | None = None) -> int:
    try:
        parser = build_parser()
        parse_argv = _normalize_global_args(
            list(argv if argv is not None else sys.argv[1:])
        )
        args = parser.parse_args(parse_argv)
        if args.current_tag and args.current_version:
            expected_tag = tag_for_version(args.current_version)
            if args.current_tag != expected_tag:
                raise ValueError(
                    f"--current-tag mismatch: {args.current_tag} != {expected_tag}"
                )
        if args.current_version and parse_version(args.current_version) < parse_version(
            args.min_version
        ):
            raise ValueError("--current-version is below --min-version")
        return args.handler(args)
    except Exception as exc:  # noqa: BLE001
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
