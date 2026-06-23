from __future__ import annotations

import shutil
from pathlib import Path
from typing import Any

from .defaults_diff import build_defaults_diff
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
from .schema_diff import build_schema_diff
from .schema_normalize import defaults_from_fields, normalize_schema
from .toml_generate import generate_toml


def _version_dir(out_dir: Path, version: str) -> Path:
    return out_dir / "versions" / version


def _write_version_payload(
    out_dir: Path,
    entry: RegistryEntry,
    metadata: dict[str, Any],
    fields: list[dict[str, Any]],
    defaults: list[dict[str, Any]],
) -> None:
    version_dir = _version_dir(out_dir, entry.version)
    version_dir.mkdir(parents=True, exist_ok=True)

    schema_url = metadata["schemaUrl"]
    shutil.copyfile(Path(metadata["_schemaFileOnDisk"]), version_dir / "config.schema.json")
    json_dump(
        version_dir / "fields.json",
        {
            "version": entry.version,
            "tag": entry.tag,
            "schemaUrl": schema_url,
            "fields": fields,
        },
    )
    json_dump(
        version_dir / "defaults.json",
        {
            "version": entry.version,
            "tag": entry.tag,
            "schemaUrl": schema_url,
            "defaults": defaults,
        },
    )
    (version_dir / "default.config.toml").write_text(
        generate_toml(entry.version, entry.tag, schema_url, fields, mode="default")
    )
    (version_dir / "reference.config.toml").write_text(
        generate_toml(entry.version, entry.tag, schema_url, fields, mode="reference")
    )


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
    versions_dir = out_dir / "versions"
    diffs_dir = out_dir / "diffs"
    versions_dir.mkdir(parents=True, exist_ok=True)
    diffs_dir.mkdir(parents=True, exist_ok=True)

    normalized_by_version: dict[str, list[dict[str, Any]]] = {}
    defaults_by_version: dict[str, list[dict[str, Any]]] = {}

    versions_payload = []
    current_payload = None

    for entry in entries:
        schema_file = schemas_dir / entry.schema_path
        metadata_file = schemas_dir / entry.metadata_path
        schema = json_load(schema_file)
        metadata = json_load(metadata_file)
        metadata["_schemaFileOnDisk"] = str(schema_file)
        fields = normalize_schema(schema)
        defaults = defaults_from_fields(fields)
        normalized_by_version[entry.version] = fields
        defaults_by_version[entry.version] = defaults
        _write_version_payload(out_dir, entry, metadata, fields, defaults)

        version_item = {
            "version": entry.version,
            "tag": entry.tag,
            "schemaUrl": metadata["schemaUrl"],
            "current": entry.version == current_version,
        }
        versions_payload.append(version_item)
        if entry.version == current_version:
            current_payload = {
                "version": entry.version,
                "tag": entry.tag,
                "schemaUrl": metadata["schemaUrl"],
                "schema": f"versions/{entry.version}/config.schema.json",
                "fields": f"versions/{entry.version}/fields.json",
                "defaults": f"versions/{entry.version}/defaults.json",
                "defaultToml": f"versions/{entry.version}/default.config.toml",
                "referenceToml": f"versions/{entry.version}/reference.config.toml",
            }

    if current_payload is None:
        raise ValueError(f"current version {current_version} was not found in manifest")

    json_dump(
        out_dir / "versions.json",
        {
            "minVersion": min_version,
            "current": current_version,
            "versions": versions_payload,
        },
    )
    json_dump(out_dir / "current.json", current_payload)

    versions = [entry.version for entry in entries]
    for from_version in versions:
        for to_version in versions:
            if from_version == to_version:
                continue
            schema_diff = build_schema_diff(
                from_version,
                to_version,
                normalized_by_version[from_version],
                normalized_by_version[to_version],
            )
            defaults_diff = build_defaults_diff(
                from_version,
                to_version,
                defaults_by_version[from_version],
                defaults_by_version[to_version],
            )
            json_dump(diffs_dir / f"{from_version}..{to_version}.json", schema_diff)
            json_dump(
                diffs_dir / f"defaults-{from_version}..{to_version}.json",
                defaults_diff,
            )
