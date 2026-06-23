from __future__ import annotations

import shutil
from pathlib import Path
from typing import Any

from .registry import (
    RegistryEntry,
    ensure_supported_version,
    json_dump,
    json_load,
    list_entries,
    load_manifest,
    parse_version,
    validate_manifest,
)
from .schema_normalize import normalize_schema


def _version_dir(out_dir: Path, version: str) -> Path:
    return out_dir / "versions" / version


def _write_version_payload(
    out_dir: Path,
    entry: RegistryEntry,
    metadata: dict[str, Any],
    fields: list[dict[str, Any]],
) -> None:
    version_dir = _version_dir(out_dir, entry.version)
    version_dir.mkdir(parents=True, exist_ok=True)

    json_dump(
        version_dir / "fields.json",
        {
            "version": entry.version,
            "tag": entry.tag,
            "schemaUrl": metadata["schemaUrl"],
            "fields": fields,
        },
    )


def _clear_generated_outputs(out_dir: Path) -> None:
    versions_dir = out_dir / "versions"
    diffs_dir = out_dir / "diffs"
    current_file = out_dir / "current.json"

    if versions_dir.exists():
        shutil.rmtree(versions_dir)
    if diffs_dir.exists():
        shutil.rmtree(diffs_dir)
    if current_file.exists():
        current_file.unlink()


def build_data(
    schemas_dir: Path,
    current_version: str,
    min_version: str,
    out_dir: Path,
) -> None:
    ensure_supported_version(current_version, min_version)
    manifest = load_manifest(schemas_dir, min_version=min_version)
    manifest = validate_manifest(
        schemas_dir, manifest, current_version=current_version, min_version=min_version
    )
    entries = [
        entry
        for entry in list_entries(manifest)
        if parse_version(entry.version) >= parse_version(min_version)
    ]

    out_dir.mkdir(parents=True, exist_ok=True)
    _clear_generated_outputs(out_dir)

    versions_dir = out_dir / "versions"
    versions_dir.mkdir(parents=True, exist_ok=True)

    versions_payload = []
    saw_current_version = False

    for entry in entries:
        schema_file = schemas_dir / entry.schema_path
        metadata_file = schemas_dir / entry.metadata_path
        schema = json_load(schema_file)
        metadata = json_load(metadata_file)
        fields = normalize_schema(schema)
        _write_version_payload(out_dir, entry, metadata, fields)

        version_item = {
            "version": entry.version,
            "tag": entry.tag,
            "schemaUrl": metadata["schemaUrl"],
            "current": entry.version == current_version,
        }
        versions_payload.append(version_item)
        if entry.version == current_version:
            saw_current_version = True

    if not saw_current_version:
        raise ValueError(f"current version {current_version} was not found in manifest")

    json_dump(
        out_dir / "versions.json",
        {
            "minVersion": min_version,
            "current": current_version,
            "versions": versions_payload,
        },
    )
