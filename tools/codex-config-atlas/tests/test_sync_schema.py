from __future__ import annotations

import argparse
import tempfile
import unittest
from pathlib import Path
from unittest.mock import patch

from codex_config_atlas.cli import _handle_sync_schema
from codex_config_atlas.registry import json_load


class SyncSchemaTests(unittest.TestCase):
    def test_resync_preserves_original_fetch_time(self) -> None:
        schema = b'{"title":"CodexConfig"}\n'
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory)
            args = argparse.Namespace(
                schemas=str(schemas),
                repo=None,
                version="0.148.0",
                current_version=None,
                min_version="0.129.0",
            )

            with (
                patch("codex_config_atlas.cli._fetch_schema", return_value=schema),
                patch(
                    "codex_config_atlas.cli.utc_now_rfc3339",
                    side_effect=["2026-08-19T10:00:00Z", "2026-08-20T10:00:00Z"],
                ),
            ):
                _handle_sync_schema(args)
                first_metadata = json_load(
                    schemas / "rust-v0.148.0" / "metadata.json"
                )
                _handle_sync_schema(args)
                second_metadata = json_load(
                    schemas / "rust-v0.148.0" / "metadata.json"
                )

        self.assertEqual(first_metadata, second_metadata)
        self.assertEqual(first_metadata["fetchedAt"], "2026-08-19T10:00:00Z")


if __name__ == "__main__":
    unittest.main()
