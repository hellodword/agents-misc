from __future__ import annotations

import hashlib
import json
from dataclasses import dataclass
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

from packaging.version import Version

SCHEMA_FILE = "config.schema.json"
METADATA_FILE = "metadata.json"
MANIFEST_FILE = "manifest.json"
SCHEMA_FILE_IN_UPSTREAM = "codex-rs/core/config.schema.json"


@dataclass(frozen=True)
class RegistryEntry:
    version: str
    tag: str
    schema_path: str
    metadata_path: str

    @property
    def version_dir(self) -> str:
        return Path(self.schema_path).parent.as_posix()


def parse_version(version: str) -> Version:
    return Version(version)


def ensure_supported_version(version: str, min_version: str) -> None:
    if parse_version(version) < parse_version(min_version):
        raise ValueError(f"version {version} is below minimum supported version {min_version}")


def tag_for_version(version: str) -> str:
    return f"rust-v{version}"


def schema_url_for_version(version: str) -> str:
    return (
        "https://raw.githubusercontent.com/openai/codex/refs/tags/"
        f"{tag_for_version(version)}/{SCHEMA_FILE_IN_UPSTREAM}"
    )


def version_dir_name(version: str) -> str:
    return tag_for_version(version)


def manifest_path(schemas_dir: Path) -> Path:
    return schemas_dir / MANIFEST_FILE


def schema_path_for_version(schemas_dir: Path, version: str) -> Path:
    return schemas_dir / version_dir_name(version) / SCHEMA_FILE


def metadata_path_for_version(schemas_dir: Path, version: str) -> Path:
    return schemas_dir / version_dir_name(version) / METADATA_FILE


def sha256_bytes(data: bytes) -> str:
    return f"sha256:{hashlib.sha256(data).hexdigest()}"


def utc_now_rfc3339() -> str:
    return datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")


def json_dump(path: Path, payload: Any) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2, sort_keys=True) + "\n")


def json_load(path: Path) -> Any:
    return json.loads(path.read_text())


def default_manifest(min_version: str) -> dict[str, Any]:
    return {"minVersion": min_version, "versions": []}


def normalize_manifest(manifest: dict[str, Any], min_version: str | None = None) -> dict[str, Any]:
    versions = manifest.get("versions", [])
    normalized = []
    for item in versions:
        version = item["version"]
        normalized.append(
            {
                "version": version,
                "tag": item.get("tag", tag_for_version(version)),
                "schemaPath": item.get(
                    "schemaPath", f"{version_dir_name(version)}/{SCHEMA_FILE}"
                ),
                "metadataPath": item.get(
                    "metadataPath", f"{version_dir_name(version)}/{METADATA_FILE}"
                ),
            }
        )

    normalized.sort(key=lambda item: parse_version(item["version"]))
    result = {
        "minVersion": manifest.get("minVersion", min_version),
        "versions": normalized,
    }
    if result["minVersion"] is None:
        raise ValueError("manifest is missing minVersion")
    return result


def load_manifest(schemas_dir: Path, min_version: str | None = None) -> dict[str, Any]:
    path = manifest_path(schemas_dir)
    if not path.exists():
        if min_version is None:
            raise FileNotFoundError(path)
        return default_manifest(min_version)

    manifest = json_load(path)
    return normalize_manifest(manifest, min_version=min_version)


def save_manifest(schemas_dir: Path, manifest: dict[str, Any]) -> None:
    json_dump(manifest_path(schemas_dir), normalize_manifest(manifest))


def list_entries(manifest: dict[str, Any]) -> list[RegistryEntry]:
    return [
        RegistryEntry(
            version=item["version"],
            tag=item["tag"],
            schema_path=item["schemaPath"],
            metadata_path=item["metadataPath"],
        )
        for item in manifest["versions"]
    ]


def entry_for_version(manifest: dict[str, Any], version: str) -> RegistryEntry | None:
    for entry in list_entries(manifest):
        if entry.version == version:
            return entry
    return None


def upsert_manifest_entry(manifest: dict[str, Any], version: str) -> dict[str, Any]:
    entries = [item for item in manifest.get("versions", []) if item["version"] != version]
    entries.append(
        {
            "version": version,
            "tag": tag_for_version(version),
            "schemaPath": f"{version_dir_name(version)}/{SCHEMA_FILE}",
            "metadataPath": f"{version_dir_name(version)}/{METADATA_FILE}",
        }
    )
    manifest = {
        "minVersion": manifest["minVersion"],
        "versions": sorted(entries, key=lambda item: parse_version(item["version"])),
    }
    return manifest


def validate_manifest(
    schemas_dir: Path,
    manifest: dict[str, Any],
    current_version: str | None = None,
    min_version: str | None = None,
) -> dict[str, Any]:
    normalized = normalize_manifest(manifest, min_version=min_version)
    versions = normalized["versions"]
    seen: set[str] = set()
    ordered = [item["version"] for item in versions]
    if ordered != sorted(ordered, key=parse_version):
        raise ValueError("manifest versions are not sorted by semantic version")

    manifest_min_version = normalized["minVersion"]
    if min_version is not None and manifest_min_version != min_version:
        raise ValueError(
            f"manifest minVersion {manifest_min_version} does not match expected {min_version}"
        )

    for item in versions:
        version = item["version"]
        if version in seen:
            raise ValueError(f"duplicate version in manifest: {version}")
        seen.add(version)
        ensure_supported_version(version, manifest_min_version)

        expected_tag = tag_for_version(version)
        if item["tag"] != expected_tag:
            raise ValueError(f"manifest tag mismatch for {version}: {item['tag']} != {expected_tag}")

        schema_file = schemas_dir / item["schemaPath"]
        metadata_file = schemas_dir / item["metadataPath"]
        if not schema_file.is_file():
            raise ValueError(f"missing schema file for {version}: {schema_file}")
        if not metadata_file.is_file():
            raise ValueError(f"missing metadata file for {version}: {metadata_file}")

        metadata = json_load(metadata_file)
        schema_bytes = schema_file.read_bytes()
        expected_url = schema_url_for_version(version)
        if metadata.get("version") != version:
            raise ValueError(f"metadata version mismatch for {version}")
        if metadata.get("tag") != expected_tag:
            raise ValueError(f"metadata tag mismatch for {version}")
        if metadata.get("schemaUrl") != expected_url:
            raise ValueError(f"metadata schemaUrl mismatch for {version}")
        if metadata.get("schemaFile") != SCHEMA_FILE_IN_UPSTREAM:
            raise ValueError(f"metadata schemaFile mismatch for {version}")
        if metadata.get("schemaSha256") != sha256_bytes(schema_bytes):
            raise ValueError(f"metadata schemaSha256 mismatch for {version}")

    if current_version is not None and current_version not in seen:
        raise ValueError(
            f"current Codex version {current_version} is not present in {manifest_path(schemas_dir)}"
        )

    return normalized
