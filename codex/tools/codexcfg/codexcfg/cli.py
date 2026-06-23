from __future__ import annotations

import argparse
import json
import sys
import urllib.request
from pathlib import Path
from typing import Any

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
        return (Path(args.repo).resolve() / "codex" / "schemas").resolve()
    raise ValueError("either --schemas or --repo is required")


def _fetch_schema(url: str) -> bytes:
    request = urllib.request.Request(url, headers={"User-Agent": "codexcfg/0.1.0"})
    with urllib.request.urlopen(request) as response:  # noqa: S310
        return response.read()


def _handle_current(args: argparse.Namespace) -> int:
    if not args.current_version:
        raise ValueError("--current-version is required for the current command")
    sys.stdout.write(f"{args.current_version}\n")
    return 0


def _handle_sync_schema(args: argparse.Namespace) -> int:
    schemas_dir = _schemas_dir_from_args(args)
    schemas_dir.mkdir(parents=True, exist_ok=True)

    version = args.version or args.current_version
    if not version:
        raise ValueError("sync-schema requires --version or --current-version")
    ensure_supported_version(version, args.min_version)

    url = schema_url_for_version(version)
    schema_bytes = _fetch_schema(url)
    schema_sha = sha256_bytes(schema_bytes)

    schema_path = schema_path_for_version(schemas_dir, version)
    metadata_path = metadata_path_for_version(schemas_dir, version)
    if schema_path.exists():
        existing_sha = sha256_bytes(schema_path.read_bytes())
        if existing_sha != schema_sha:
            raise ValueError(
                f"schema already exists for {version} but contents changed: {existing_sha} != {schema_sha}"
            )
    else:
        schema_path.parent.mkdir(parents=True, exist_ok=True)
        schema_path.write_bytes(schema_bytes)

    metadata = {
        "version": version,
        "tag": tag_for_version(version),
        "schemaUrl": url,
        "schemaFile": SCHEMA_FILE_IN_UPSTREAM,
        "schemaSha256": schema_sha,
        "fetchedAt": utc_now_rfc3339(),
    }
    json_dump(metadata_path, metadata)

    manifest = load_manifest(schemas_dir, min_version=args.min_version)
    manifest = upsert_manifest_entry(manifest, version)
    save_manifest(schemas_dir, manifest)
    validate_manifest(schemas_dir, manifest, min_version=args.min_version)

    sys.stdout.write(f"synced {version} -> {schema_path}\n")
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


def _load_version_inputs(schemas_dir: Path, version: str) -> tuple[dict[str, Any], dict[str, Any]]:
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
    output = generate_toml(args.version, tag_for_version(args.version), metadata["schemaUrl"], fields, args.mode)
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
        _write_output(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n", args.out)
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
        _write_output(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n", args.out)
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
    parser = argparse.ArgumentParser(prog="codexcfg")
    _add_shared_options(parser)

    subparsers = parser.add_subparsers(dest="command", required=True)

    current = subparsers.add_parser("current")
    current.set_defaults(handler=_handle_current)

    sync_schema = subparsers.add_parser("sync-schema")
    sync_schema.add_argument("--repo")
    sync_schema.add_argument("--schemas")
    sync_schema.add_argument("--version")
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
    diff_defaults.add_argument("--format", choices=["json", "markdown"], default="markdown")
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
        parse_argv = _normalize_global_args(list(argv if argv is not None else sys.argv[1:]))
        args = parser.parse_args(parse_argv)
        if args.current_tag and args.current_version:
            expected_tag = tag_for_version(args.current_version)
            if args.current_tag != expected_tag:
                raise ValueError(f"--current-tag mismatch: {args.current_tag} != {expected_tag}")
        if args.current_version and parse_version(args.current_version) < parse_version(args.min_version):
            raise ValueError("--current-version is below --min-version")
        return args.handler(args)
    except Exception as exc:  # noqa: BLE001
        print(f"error: {exc}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
