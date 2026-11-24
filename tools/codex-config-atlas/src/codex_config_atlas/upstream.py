from __future__ import annotations

import os
import re
import subprocess
from collections.abc import Callable, Mapping
from pathlib import Path
from typing import Any
from urllib.parse import urlsplit

import tomlkit
import tomllib

from .defaults_diff import build_defaults_diff
from .json_value import canonical_json_key
from .registry import (
    entry_for_version,
    json_load,
    list_entries,
    metadata_path_for_version,
    parse_version,
    schema_path_for_version,
    validate_manifest,
)
from .schema_diff import build_schema_diff
from .schema_normalize import defaults_from_fields, normalize_schema
from .toml_example import generate_example_toml
from .toml_generate import generate_toml

GITHUB_API_HOST = "api.github.com"
GITHUB_RELEASES_URL = "https://api.github.com/repos/openai/codex/releases"
GITHUB_GIT_REPO_URL = "https://github.com/openai/codex.git"
RELEASES_PER_PAGE = 25
MAX_RELEASES_PAGES = 200
MAX_RELEASES_PAGE_BYTES = 16 * 1024 * 1024
STABLE_TAG_RE = re.compile(
    r"^rust-v((?:0|[1-9]\d*)\.(?:0|[1-9]\d*)\.(?:0|[1-9]\d*))$"
)
HEX_SHA_RE = re.compile(r"^[0-9a-f]{40}$")


def resolve_tags_git_ls_remote(
    tags: list[str],
    *,
    timeout_seconds: float,
    repo_url: str = GITHUB_GIT_REPO_URL,
) -> dict[str, str]:
    if not tags:
        return {}
    refspecs: list[str] = []
    for tag in tags:
        refspecs.append(f"refs/tags/{tag}")
        refspecs.append(f"refs/tags/{tag}^{{}}")
    try:
        completed = subprocess.run(
            ["git", "ls-remote", "--", repo_url, *refspecs],
            check=False,
            text=True,
            capture_output=True,
            timeout=timeout_seconds,
            env={**os.environ, "GIT_TERMINAL_PROMPT": "0"},
        )
    except (OSError, subprocess.SubprocessError):
        return {}
    if completed.returncode != 0:
        return {}

    direct: dict[str, str] = {}
    peeled: dict[str, str] = {}
    for line in completed.stdout.splitlines():
        parts = line.strip().split("\t", 1)
        if len(parts) != 2:
            continue
        sha, ref = parts
        if HEX_SHA_RE.fullmatch(sha) is None or not ref.startswith("refs/tags/"):
            continue
        name = ref.removeprefix("refs/tags/")
        if name.endswith("^{}"):
            peeled[name[:-3]] = sha
        else:
            direct[name] = sha

    resolved: dict[str, str] = {}
    for tag in tags:
        commit = peeled.get(tag) or direct.get(tag)
        if commit:
            resolved[tag] = commit
    return resolved


class SchemaCompatibilityError(ValueError):
    """Raised when an upstream schema violates normalization or generation contracts."""


def normalize_version_arg(value: str) -> str:
    stripped = value.strip()
    if stripped.startswith("rust-v") and len(stripped) > len("rust-v"):
        return stripped.removeprefix("rust-v")
    return stripped


def github_request_headers(url: str) -> dict[str, str]:
    headers = {
        "Accept": "application/vnd.github+json",
        "User-Agent": "codex-config-atlas/0.1.0",
    }
    if urlsplit(url).hostname == GITHUB_API_HOST:
        token = (
            os.environ.get("GITHUB_TOKEN") or os.environ.get("GH_TOKEN") or ""
        ).strip()
        if token and "\r" not in token and "\n" not in token:
            headers["Authorization"] = f"Bearer {token}"
    return headers


def has_next_page_link(link_header: str | None) -> bool:
    if not link_header:
        return False
    return any(
        re.search(r';\s*rel="next"', part) is not None
        for part in link_header.split(",")
    )


def list_upstream_stable_versions(
    *,
    min_version: str,
    timeout_seconds: float,
    fetch_page: Callable[[str, float, int], tuple[list[Any], Mapping[str, str]]],
    known_versions: set[str] | None = None,
) -> list[str]:
    min_parsed = parse_version(min_version)
    discovered: set[str] = set()
    settled_pages = 0

    for page in range(1, MAX_RELEASES_PAGES + 1):
        url = f"{GITHUB_RELEASES_URL}?per_page={RELEASES_PER_PAGE}&page={page}"
        items, headers = fetch_page(url, timeout_seconds, MAX_RELEASES_PAGE_BYTES)
        if not items:
            break

        page_new_supported = 0
        page_known_or_older = 0
        for item in items:
            if not isinstance(item, dict):
                raise ValueError(f"invalid release entry on page {page} from {url}")
            if (
                item.get("draft")
                or item.get("prerelease")
                or not item.get("published_at")
            ):
                continue
            tag_name = item.get("tag_name")
            if not isinstance(tag_name, str):
                continue
            match = STABLE_TAG_RE.fullmatch(tag_name)
            if match is None:
                continue
            version = match.group(1)
            if parse_version(version) >= min_parsed:
                discovered.add(version)
                if known_versions is not None and version in known_versions:
                    page_known_or_older += 1
                else:
                    page_new_supported += 1
            else:
                page_known_or_older += 1

        if page_new_supported == 0 and page_known_or_older > 0:
            settled_pages += 1
            if settled_pages >= 2:
                break
        else:
            settled_pages = 0

        link_header = headers.get("Link") or headers.get("link")
        if link_header is not None:
            if not has_next_page_link(link_header):
                break
        elif len(items) < RELEASES_PER_PAGE:
            break
    else:
        raise ValueError(
            f"upstream release pagination exceeded {MAX_RELEASES_PAGES} pages"
        )

    return sorted(discovered, key=parse_version)


def preflight_candidate_version(
    schemas_dir: Path,
    manifest: dict[str, Any],
    version: str,
    *,
    min_version: str,
) -> None:
    validated = validate_manifest(schemas_dir, manifest, min_version=min_version)
    entry = entry_for_version(validated, version)
    if entry is None:
        raise ValueError(f"candidate manifest is missing version {version}")

    schema = json_load(schemas_dir / entry.schema_path)
    metadata = json_load(schemas_dir / entry.metadata_path)

    try:
        if schema.get("required"):
            raise SchemaCompatibilityError(
                f"root schema declares required fields {schema['required']!r}, "
                "breaking the opt-in configuration example contract"
            )

        fields = normalize_schema(schema)
        for mode in ("default", "reference"):
            forward = generate_toml(
                version, entry.tag, metadata["schemaUrl"], fields, mode
            )
            reverse = generate_toml(
                version,
                entry.tag,
                metadata["schemaUrl"],
                list(reversed(fields)),
                mode,
            )
            if forward != reverse:
                raise SchemaCompatibilityError(
                    f"{mode} TOML output depends on field iteration order"
                )
            tomlkit.parse(forward)

        example = generate_example_toml(
            version, entry.tag, metadata["schemaUrl"], schema
        )
        if tomllib.loads(example) != {}:
            raise SchemaCompatibilityError(
                "example.toml contains uncommented active TOML assignments"
            )
        for prop_name in schema.get("properties", {}):
            if f"# --- {prop_name} ---" not in example:
                raise SchemaCompatibilityError(
                    "example.toml is missing section header for "
                    f"top-level property {prop_name!r}"
                )
        for field in fields:
            if (
                field["hasDefault"]
                and canonical_json_key(field["default"]) not in example
            ):
                raise SchemaCompatibilityError(
                    f"example.toml is missing canonical default for {field['path']}"
                )

        entries = list_entries(validated)
        index = next(i for i, item in enumerate(entries) if item.version == version)
        neighbors = []
        if index > 0:
            neighbors.append(entries[index - 1])
        if index + 1 < len(entries):
            neighbors.append(entries[index + 1])

        for neighbor in neighbors:
            neighbor_schema = json_load(schemas_dir / neighbor.schema_path)
            neighbor_fields = normalize_schema(neighbor_schema)
            if parse_version(neighbor.version) < parse_version(version):
                from_ver, to_ver = neighbor.version, version
                left_fields, right_fields = neighbor_fields, fields
            else:
                from_ver, to_ver = version, neighbor.version
                left_fields, right_fields = fields, neighbor_fields

            diff_payload = build_schema_diff(
                from_ver, to_ver, left_fields, right_fields
            )
            if sum(diff_payload["summary"].values()) != len(diff_payload["changes"]):
                raise SchemaCompatibilityError(
                    "schema diff summary count mismatch between "
                    f"{from_ver} and {to_ver}"
                )
            build_defaults_diff(
                from_ver,
                to_ver,
                defaults_from_fields(left_fields),
                defaults_from_fields(right_fields),
            )
    except SchemaCompatibilityError:
        raise
    except Exception as exc:
        raise SchemaCompatibilityError(f"{type(exc).__name__}: {exc}") from exc


def mark_intent_to_add(schemas_dir: Path, version: str) -> list[str]:
    resolved_schemas = schemas_dir.resolve()
    top = subprocess.run(
        ["git", "-C", str(resolved_schemas), "rev-parse", "--show-toplevel"],
        check=False,
        text=True,
        capture_output=True,
    )
    if top.returncode != 0:
        raise ValueError(
            "--intent-to-add requires the schemas directory to be inside a Git worktree"
        )
    repo_root = Path(top.stdout.strip()).resolve()

    targets = [
        schema_path_for_version(resolved_schemas, version),
        metadata_path_for_version(resolved_schemas, version),
    ]
    rel_paths: list[str] = []
    for target in targets:
        if target.is_symlink() or not target.is_file():
            raise ValueError(f"expected regular file for intent-to-add: {target}")
        rel = target.resolve().relative_to(repo_root).as_posix()
        ignored = subprocess.run(
            ["git", "-C", str(repo_root), "check-ignore", "-q", "--", rel],
            check=False,
            capture_output=True,
        )
        if ignored.returncode == 0:
            raise ValueError(f"refusing intent-to-add on Git-ignored path: {rel}")
        rel_paths.append(rel)

    added = subprocess.run(
        ["git", "-C", str(repo_root), "add", "-N", "--", *rel_paths],
        check=False,
        text=True,
        capture_output=True,
    )
    if added.returncode != 0:
        detail = added.stderr.strip() or added.stdout.strip() or "git add -N failed"
        raise RuntimeError(f"git add -N failed for {version}: {detail}")
    return rel_paths


def format_escalation_message(
    version: str,
    exc: SchemaCompatibilityError,
    preserved_versions: list[str],
) -> str:
    preserved = ", ".join(preserved_versions) if preserved_versions else "none"
    return (
        f"ESCALATION REQUIRED for Codex {version} (rust-v{version}): {exc}\n"
        f"status: preserved synced versions [{preserved}]; "
        f"discarded candidate {version} (registry remains valid).\n"
        "action: STOP routine maintenance. Do NOT edit src/, web/, tests/, or nix/. "
        f"Report this error and version {version} to a maintainer."
    )
