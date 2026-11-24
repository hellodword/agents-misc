from __future__ import annotations

import argparse
import json
import re
import shutil
import sys
import tempfile
import time
import urllib.error
import urllib.request
from collections.abc import Mapping
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

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
    schema_url_for_commit,
    sha256_bytes,
    tag_for_version,
    upsert_manifest_entry,
    utc_now_rfc3339,
    validate_manifest,
)
from .schema_diff import build_schema_diff, render_schema_diff_markdown
from .schema_normalize import defaults_from_fields, normalize_schema
from .toml_example import generate_example_toml
from .toml_generate import generate_toml
from .upstream import (
    GITHUB_API_HOST,
    SchemaCompatibilityError,
    format_escalation_message,
    github_request_headers,
    list_upstream_stable_versions,
    mark_intent_to_add,
    normalize_version_arg,
    preflight_candidate_version,
)

GLOBAL_OPTION_NAMES = {
    "--current-version",
    "--current-tag",
    "--min-version",
}
NETWORK_TIMEOUT_SECONDS = 30.0
MAX_SCHEMA_BYTES = 8 * 1024 * 1024
MAX_GITHUB_METADATA_BYTES = 1024 * 1024
GITHUB_API_ROOT = "https://api.github.com/repos/openai/codex"


class _EscalationReportedError(RuntimeError):
    """Internal marker after an escalation diagnostic has been written to stderr."""


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


def _fetch_limited_response(
    url: str, *, timeout_seconds: float, max_bytes: int
) -> tuple[bytes, Mapping[str, str]]:
    for attempt in range(3):
        request = urllib.request.Request(url, headers=github_request_headers(url))
        try:
            with urllib.request.urlopen(  # noqa: S310
                request, timeout=timeout_seconds
            ) as response:
                headers = dict(response.headers.items())
                content_length = response.headers.get("Content-Length")
                if content_length is not None:
                    try:
                        declared_size = int(content_length)
                    except ValueError as exc:
                        raise ValueError(
                            f"invalid Content-Length from {url}"
                        ) from exc
                    if declared_size < 0 or declared_size > max_bytes:
                        raise ValueError(
                            f"response from {url} exceeds maximum {max_bytes} bytes"
                        )

                payload = bytearray()
                while True:
                    chunk = response.read(
                        min(64 * 1024, max_bytes + 1 - len(payload))
                    )
                    if not chunk:
                        break
                    payload.extend(chunk)
                    if len(payload) > max_bytes:
                        raise ValueError(
                            f"response from {url} exceeds maximum {max_bytes} bytes"
                        )
                return bytes(payload), headers
        except urllib.error.HTTPError as exc:
            exc.close()
            if exc.code in {502, 503, 504} and attempt < 2:
                time.sleep(1.0 * (attempt + 1))
                continue
            if urlsplit(url).hostname == GITHUB_API_HOST and exc.code in {403, 429}:
                raise RuntimeError(
                    f"GitHub API rate limit or access error (HTTP {exc.code}) "
                    f"for {url}; set GITHUB_TOKEN or GH_TOKEN for authenticated "
                    "quota, or wait for reset"
                ) from exc
            raise
    raise RuntimeError(f"exhausted retries fetching {url}")


def _fetch_limited(url: str, *, timeout_seconds: float, max_bytes: int) -> bytes:
    payload, _ = _fetch_limited_response(
        url, timeout_seconds=timeout_seconds, max_bytes=max_bytes
    )
    return payload


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


def _fetch_json_list_page(
    url: str, timeout_seconds: float, max_bytes: int
) -> tuple[list[Any], Mapping[str, str]]:
    payload, headers = _fetch_limited_response(
        url, timeout_seconds=timeout_seconds, max_bytes=max_bytes
    )
    try:
        value = json.loads(payload)
    except (UnicodeDecodeError, json.JSONDecodeError) as exc:
        raise ValueError(f"invalid JSON response from {url}") from exc
    if not isinstance(value, list):
        raise ValueError(f"expected JSON array from {url}")
    return value, headers


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


def _sync_single_version(
    schemas_dir: Path,
    version: str,
    *,
    min_version: str,
    timeout_seconds: float,
    max_bytes: int,
    expected_commit: str | None = None,
    expected_sha256: str | None = None,
    intent_to_add: bool = False,
) -> Path | None:
    ensure_supported_version(version, min_version)
    if timeout_seconds <= 0:
        raise ValueError("--timeout-seconds must be greater than zero")
    if max_bytes <= 0:
        raise ValueError("--max-bytes must be greater than zero")

    if schemas_dir.exists():
        installed_manifest = load_manifest(schemas_dir, min_version=min_version)
        validate_manifest(schemas_dir, installed_manifest, min_version=min_version)

    tag = tag_for_version(version)
    commit_sha = _resolve_tag_commit(tag, timeout_seconds=timeout_seconds)
    if expected_commit is not None:
        _checked_sha(expected_commit, "--expected-commit")
        if commit_sha != expected_commit:
            raise ValueError(
                f"resolved commit mismatch for {tag}: {commit_sha} != {expected_commit}"
            )

    url = schema_url_for_commit(commit_sha)
    schema_bytes = _fetch_schema(
        commit_sha, timeout_seconds=timeout_seconds, max_bytes=max_bytes
    )
    schema_sha = sha256_bytes(schema_bytes)
    if expected_sha256 is not None and schema_sha != expected_sha256:
        raise ValueError(
            f"schema hash mismatch for {version}: {schema_sha} != {expected_sha256}"
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

        manifest = load_manifest(candidate, min_version=min_version)
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
        validate_manifest(candidate, manifest, min_version=min_version)
        preflight_candidate_version(
            candidate, manifest, version, min_version=min_version
        )

        retained_old = replace_directory(candidate, schemas_dir)
    finally:
        if candidate.exists() and candidate != retained_old:
            shutil.rmtree(candidate)

    sys.stdout.write(
        f"synced {version} -> {schema_path_for_version(schemas_dir, version)}\n"
    )
    if intent_to_add:
        for rel_path in mark_intent_to_add(schemas_dir, version):
            sys.stdout.write(f"intent-to-add: {rel_path}\n")
    if retained_old is not None:
        print(
            f"warning: installed registry but old registry remains at {retained_old}",
            file=sys.stderr,
        )
    return retained_old


def _default_current_version(schemas_dir: Path, min_version: str) -> str | None:
    if not schemas_dir.exists():
        return None
    manifest = load_manifest(schemas_dir, min_version=min_version)
    versions = manifest.get("versions") or []
    if not versions:
        return None
    return str(versions[-1]["version"])


def _handle_current(args: argparse.Namespace) -> int:
    current = args.current_version
    if not current and getattr(args, "schemas", None):
        current = _default_current_version(
            Path(args.schemas).resolve(), args.min_version
        )
    if not current:
        raise ValueError("--current-version or --schemas is required for current")
    sys.stdout.write(f"{current}\n")
    return 0


def _handle_sync_schema(args: argparse.Namespace) -> int:
    schemas_dir = _schemas_dir_from_args(args)
    schemas_dir.parent.mkdir(parents=True, exist_ok=True)

    raw_version = args.version or args.current_version
    if not raw_version:
        raise ValueError("sync-schema requires --version or --current-version")
    version = normalize_version_arg(raw_version)

    try:
        _sync_single_version(
            schemas_dir,
            version,
            min_version=args.min_version,
            timeout_seconds=args.timeout_seconds,
            max_bytes=args.max_bytes,
            expected_commit=args.expected_commit,
            expected_sha256=args.expected_sha256,
            intent_to_add=bool(getattr(args, "intent_to_add", False)),
        )
    except SchemaCompatibilityError as exc:
        print(format_escalation_message(version, exc, []), file=sys.stderr)
        raise
    return 0


def _handle_sync_releases(args: argparse.Namespace) -> int:
    schemas_dir = _schemas_dir_from_args(args)
    schemas_dir.parent.mkdir(parents=True, exist_ok=True)
    if args.timeout_seconds <= 0:
        raise ValueError("--timeout-seconds must be greater than zero")
    if args.max_bytes <= 0:
        raise ValueError("--max-bytes must be greater than zero")

    if schemas_dir.exists():
        manifest = load_manifest(schemas_dir, min_version=args.min_version)
        manifest = validate_manifest(
            schemas_dir, manifest, min_version=args.min_version
        )
    else:
        manifest = load_manifest(schemas_dir, min_version=args.min_version)

    registered = {item["version"] for item in manifest["versions"]}
    upstream_versions = list_upstream_stable_versions(
        min_version=args.min_version,
        timeout_seconds=args.timeout_seconds,
        fetch_page=_fetch_json_list_page,
    )
    missing = [v for v in upstream_versions if v not in registered]

    if not missing:
        latest = manifest["versions"][-1]["version"] if manifest["versions"] else "none"
        count = len(manifest["versions"])
        sys.stdout.write(
            f"up to date: {count} versions registered (latest: {latest})\n"
        )
        return 0

    if args.dry_run:
        for version in missing:
            sys.stdout.write(f"missing {version} ({tag_for_version(version)})\n")
        return 0

    synced: list[str] = []
    for version in missing:
        try:
            _sync_single_version(
                schemas_dir,
                version,
                min_version=args.min_version,
                timeout_seconds=args.timeout_seconds,
                max_bytes=args.max_bytes,
                intent_to_add=bool(args.intent_to_add),
            )
            synced.append(version)
        except SchemaCompatibilityError as exc:
            print(
                f"error: {format_escalation_message(version, exc, synced)}",
                file=sys.stderr,
            )
            raise _EscalationReportedError(version) from exc

    sys.stdout.write(
        f"completed: synced {len(synced)} release(s) ({synced[0]} -> {synced[-1]})\n"
    )
    return 0


def _handle_check_registry(args: argparse.Namespace) -> int:
    schemas_dir = Path(args.schemas).resolve()
    manifest = load_manifest(schemas_dir, min_version=args.min_version)
    current = args.current_version or _default_current_version(
        schemas_dir, args.min_version
    )
    validate_manifest(
        schemas_dir,
        manifest,
        current_version=current,
        min_version=args.min_version,
    )
    sys.stdout.write(
        f"registry ok: {len(manifest['versions'])} versions, current={current}\n"
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
    version = normalize_version_arg(args.version)
    schema, metadata = _load_version_inputs(schemas_dir, version)
    if args.mode == "example":
        output = generate_example_toml(
            version, metadata["tag"], metadata["schemaUrl"], schema
        )
    else:
        output = generate_toml(
            version,
            tag_for_version(version),
            metadata["schemaUrl"],
            normalize_schema(schema),
            args.mode,
        )
    _write_output(output, args.out)
    return 0


def _handle_diff(args: argparse.Namespace) -> int:
    schemas_dir = Path(args.schemas).resolve()
    from_version = normalize_version_arg(args.from_version)
    to_version = normalize_version_arg(args.to_version)
    from_schema, _ = _load_version_inputs(schemas_dir, from_version)
    to_schema, _ = _load_version_inputs(schemas_dir, to_version)
    payload = build_schema_diff(
        from_version,
        to_version,
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
    from_version = normalize_version_arg(args.from_version)
    to_version = normalize_version_arg(args.to_version)
    from_schema, _ = _load_version_inputs(schemas_dir, from_version)
    to_schema, _ = _load_version_inputs(schemas_dir, to_version)
    payload = build_defaults_diff(
        from_version,
        to_version,
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
    schemas_dir = Path(args.schemas).resolve()
    current = args.current_version or _default_current_version(
        schemas_dir, args.min_version
    )
    if not current:
        raise ValueError(
            "build-data requires --current-version or a non-empty manifest"
        )
    build_data(
        schemas_dir=schemas_dir,
        current_version=current,
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

    current = subparsers.add_parser(
        "current", help="Print the latest registered Codex version"
    )
    current.add_argument("--schemas")
    current.set_defaults(handler=_handle_current)

    sync_releases = subparsers.add_parser(
        "sync-releases",
        help=(
            "Discover and progressively sync all missing stable upstream "
            "Codex releases"
        ),
    )
    sync_releases.add_argument("--repo")
    sync_releases.add_argument("--schemas")
    sync_releases.add_argument("--dry-run", action="store_true")
    sync_releases.add_argument("--intent-to-add", action="store_true")
    sync_releases.add_argument(
        "--timeout-seconds", type=float, default=NETWORK_TIMEOUT_SECONDS
    )
    sync_releases.add_argument("--max-bytes", type=int, default=MAX_SCHEMA_BYTES)
    sync_releases.set_defaults(handler=_handle_sync_releases)

    sync_schema = subparsers.add_parser(
        "sync-schema", help="Fetch, preflight, and register one upstream Codex version"
    )
    sync_schema.add_argument("--repo")
    sync_schema.add_argument("--schemas")
    sync_schema.add_argument("--version")
    sync_schema.add_argument("--intent-to-add", action="store_true")
    sync_schema.add_argument(
        "--timeout-seconds", type=float, default=NETWORK_TIMEOUT_SECONDS
    )
    sync_schema.add_argument("--max-bytes", type=int, default=MAX_SCHEMA_BYTES)
    sync_schema.add_argument("--expected-commit")
    sync_schema.add_argument("--expected-sha256")
    sync_schema.set_defaults(handler=_handle_sync_schema)

    check_registry = subparsers.add_parser(
        "check-registry",
        help="Validate manifest ordering, metadata, and SHA-256 hashes",
    )
    check_registry.add_argument("--schemas", required=True)
    check_registry.set_defaults(handler=_handle_check_registry)

    gen_toml = subparsers.add_parser(
        "gen-toml", help="Render default, reference, or example TOML for a version"
    )
    gen_toml.add_argument("--schemas", required=True)
    gen_toml.add_argument("--version", required=True)
    gen_toml.add_argument(
        "--mode", choices=["default", "reference", "example"], required=True
    )
    gen_toml.add_argument("--out")
    gen_toml.set_defaults(handler=_handle_gen_toml)

    diff = subparsers.add_parser("diff", help="Render schema diff between two versions")
    diff.add_argument("--schemas", required=True)
    diff.add_argument("--from", dest="from_version", required=True)
    diff.add_argument("--to", dest="to_version", required=True)
    diff.add_argument("--format", choices=["json", "markdown"], default="markdown")
    diff.add_argument("--out")
    diff.set_defaults(handler=_handle_diff)

    diff_defaults = subparsers.add_parser(
        "diff-defaults", help="Render default-value diff between two versions"
    )
    diff_defaults.add_argument("--schemas", required=True)
    diff_defaults.add_argument("--from", dest="from_version", required=True)
    diff_defaults.add_argument("--to", dest="to_version", required=True)
    diff_defaults.add_argument(
        "--format", choices=["json", "markdown"], default="markdown"
    )
    diff_defaults.add_argument("--out")
    diff_defaults.set_defaults(handler=_handle_diff_defaults)

    build_data_parser = subparsers.add_parser(
        "build-data",
        help=(
            "Internal Nix build helper: generate version data outside "
            "the working tree"
        ),
    )
    build_data_parser.add_argument("--schemas", required=True)
    build_data_parser.add_argument("--out", required=True)
    build_data_parser.set_defaults(handler=_handle_build_data)

    build_site_parser = subparsers.add_parser(
        "build-site",
        help="Internal Nix build helper: assemble static site outside the working tree",
    )
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
    except _EscalationReportedError:
        return 1
    except Exception as exc:  # noqa: BLE001
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
