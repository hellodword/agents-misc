from __future__ import annotations

import hashlib
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from unittest.mock import patch

import tomlkit

from codex_config_atlas.atomic_output import build_directory_atomically
from codex_config_atlas.build_data import build_data
from codex_config_atlas.build_site import build_site
from codex_config_atlas.registry import (
    json_load,
    list_entries,
    load_manifest,
    validate_manifest,
)
from codex_config_atlas.schema_diff import build_schema_diff
from codex_config_atlas.schema_normalize import normalize_schema
from codex_config_atlas.toml_generate import generate_toml


TOOL_ROOT = Path(__file__).parents[1]
SCHEMAS = TOOL_ROOT / "schemas"
STATIC = TOOL_ROOT / "web"
CURRENT_VERSION = "0.148.0"
MIN_VERSION = "0.129.0"


def tree_digest(root: Path) -> tuple[list[str], str]:
    paths: list[str] = []
    digest = hashlib.sha256()
    for path in sorted(root.rglob("*"), key=lambda item: item.as_posix()):
        relative = path.relative_to(root).as_posix()
        paths.append(relative)
        digest.update(relative.encode())
        if path.is_file():
            digest.update(path.read_bytes())
    return paths, digest.hexdigest()


class HistoricalGenerationTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls) -> None:
        cls.manifest = validate_manifest(
            SCHEMAS,
            load_manifest(SCHEMAS, min_version=MIN_VERSION),
            current_version=CURRENT_VERSION,
            min_version=MIN_VERSION,
        )
        cls.entries = list_entries(cls.manifest)
        cls.fields = {
            entry.version: normalize_schema(json_load(SCHEMAS / entry.schema_path))
            for entry in cls.entries
        }

    def test_every_historical_schema_generates_stable_parseable_toml(self) -> None:
        for entry in self.entries:
            metadata = json_load(SCHEMAS / entry.metadata_path)
            for mode in ("default", "reference"):
                with self.subTest(version=entry.version, mode=mode):
                    first = generate_toml(
                        entry.version,
                        entry.tag,
                        metadata["schemaUrl"],
                        self.fields[entry.version],
                        mode,
                    )
                    second = generate_toml(
                        entry.version,
                        entry.tag,
                        metadata["schemaUrl"],
                        list(reversed(self.fields[entry.version])),
                        mode,
                    )
                    self.assertEqual(first, second)
                    tomlkit.parse(first)

    def test_every_historical_version_pair_can_be_compared(self) -> None:
        pair_count = 0
        for from_index, from_entry in enumerate(self.entries):
            for to_entry in self.entries[from_index:]:
                with self.subTest(
                    from_version=from_entry.version, to_version=to_entry.version
                ):
                    payload = build_schema_diff(
                        from_entry.version,
                        to_entry.version,
                        self.fields[from_entry.version],
                        self.fields[to_entry.version],
                    )
                    self.assertEqual(
                        sum(payload["summary"].values()), len(payload["changes"])
                    )
                    pair_count += 1
        self.assertEqual(pair_count, len(self.entries) * (len(self.entries) + 1) // 2)

    def test_data_and_site_are_reproducible_and_failure_atomic(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            data = root / "data"
            site = root / "site"

            build_data(SCHEMAS, CURRENT_VERSION, MIN_VERSION, data)
            first_data = tree_digest(data)
            build_data(SCHEMAS, CURRENT_VERSION, MIN_VERSION, data)
            self.assertEqual(tree_digest(data), first_data)

            build_site(STATIC, data, site)
            first_site = tree_digest(site)
            build_site(STATIC, data, site)
            self.assertEqual(tree_digest(site), first_site)

            from codex_config_atlas import build_data as build_data_module

            real_write = build_data_module._write_version_payload
            write_count = 0

            def interrupt_data_write(*args: object, **kwargs: object) -> None:
                nonlocal write_count
                write_count += 1
                if write_count == 2:
                    raise OSError("injected data write failure")
                real_write(*args, **kwargs)

            with (
                patch.object(
                    build_data_module,
                    "_write_version_payload",
                    side_effect=interrupt_data_write,
                ),
                self.assertRaisesRegex(OSError, "injected data write failure"),
            ):
                build_data(SCHEMAS, CURRENT_VERSION, MIN_VERSION, data)
            self.assertEqual(tree_digest(data), first_data)

            with (
                patch(
                    "codex_config_atlas.build_site.shutil.copy2",
                    side_effect=OSError("injected site write failure"),
                ),
                self.assertRaisesRegex(OSError, "injected site write failure"),
            ):
                build_site(STATIC, data, site)
            self.assertEqual(tree_digest(site), first_site)

            leftovers = [
                path.name
                for path in root.iterdir()
                if "-generate-" in path.name or "-backup-" in path.name
            ]
            self.assertEqual(leftovers, [])

    def test_atomic_exchange_failure_preserves_previous_output(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            output = root / "output"
            output.mkdir()
            (output / "value").write_text("before")
            original = tree_digest(output)
            def populate(candidate: Path) -> None:
                (candidate / "value").write_text("after")

            with (
                patch(
                    "codex_config_atlas.atomic_output.subprocess.run",
                    return_value=subprocess.CompletedProcess(
                        args=["mv"], returncode=1, stdout="", stderr="unsupported"
                    ),
                ),
                self.assertRaisesRegex(RuntimeError, "atomic directory exchange failed"),
            ):
                build_directory_atomically(output, populate)

            self.assertEqual(tree_digest(output), original)

    def test_old_output_cleanup_failure_keeps_committed_new_tree(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            root = Path(temporary_directory)
            output = root / "output"
            output.mkdir()
            (output / "value").write_text("before")

            def populate(candidate: Path) -> None:
                (candidate / "value").write_text("after")

            real_rmtree = shutil.rmtree

            def fail_old_output(path: Path, *args: object, **kwargs: object) -> None:
                if "-generate-" in Path(path).name:
                    raise OSError("injected cleanup failure")
                real_rmtree(path, *args, **kwargs)

            with (
                patch(
                    "codex_config_atlas.atomic_output.shutil.rmtree",
                    side_effect=fail_old_output,
                ),
                self.assertWarnsRegex(RuntimeWarning, "old output remains at") as warning,
            ):
                retained = build_directory_atomically(output, populate)

            self.assertIsNotNone(retained)
            assert retained is not None
            self.assertIn(str(retained), str(warning.warning))
            self.assertEqual((output / "value").read_text(), "after")
            self.assertEqual((retained / "value").read_text(), "before")
            real_rmtree(retained)


if __name__ == "__main__":
    unittest.main()
