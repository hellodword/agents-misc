from __future__ import annotations

import argparse
import hashlib
import io
import os
import subprocess
import tempfile
import unittest
import urllib.error
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path
from unittest.mock import patch

from codex_config_atlas.cli import (
    _fetch_limited,
    _handle_sync_schema,
    _resolve_tag_commit,
    main,
)
from codex_config_atlas.registry import (
    SCHEMA_FILE_IN_UPSTREAM,
    json_dump,
    json_load,
    schema_url_for_commit,
    sha256_bytes,
)
from codex_config_atlas.upstream import (
    github_request_headers,
    list_upstream_stable_versions,
)

VERSION = "0.148.0"
TAG = f"rust-v{VERSION}"
COMMIT_A = "a" * 40
COMMIT_B = "b" * 40
SCHEMA = b'{"title":"CodexConfig","type":"object"}\n'


def sync_args(schemas: Path, **overrides: object) -> argparse.Namespace:
    values: dict[str, object] = {
        "schemas": str(schemas),
        "repo": None,
        "version": VERSION,
        "current_version": None,
        "min_version": "0.129.0",
        "timeout_seconds": 2.5,
        "max_bytes": 1024,
        "expected_commit": None,
        "expected_sha256": None,
    }
    values.update(overrides)
    return argparse.Namespace(**values)


def seed_registry(
    schemas: Path,
    *,
    commit_sha: str = COMMIT_A,
    schema: bytes = SCHEMA,
) -> None:
    version_dir = schemas / TAG
    version_dir.mkdir(parents=True)
    (version_dir / "config.schema.json").write_bytes(schema)
    json_dump(
        version_dir / "metadata.json",
        {
            "version": VERSION,
            "tag": TAG,
            "commitSha": commit_sha,
            "schemaUrl": schema_url_for_commit(commit_sha),
            "schemaFile": SCHEMA_FILE_IN_UPSTREAM,
            "schemaSha256": sha256_bytes(schema),
            "fetchedAt": "2026-08-19T10:00:00Z",
        },
    )
    json_dump(
        schemas / "manifest.json",
        {
            "minVersion": "0.129.0",
            "versions": [
                {
                    "version": VERSION,
                    "tag": TAG,
                    "schemaPath": f"{TAG}/config.schema.json",
                    "metadataPath": f"{TAG}/metadata.json",
                }
            ],
        },
    )


def tree_digest(root: Path) -> str:
    digest = hashlib.sha256()
    if not root.exists():
        return "missing"
    for path in sorted(root.rglob("*"), key=lambda item: item.as_posix()):
        relative = path.relative_to(root).as_posix()
        digest.update(relative.encode())
        if path.is_symlink():
            digest.update(b"L")
            digest.update(os.readlink(path).encode())
        elif path.is_file():
            digest.update(b"F")
            digest.update(path.read_bytes())
        else:
            digest.update(b"D")
    return digest.hexdigest()


class FakeResponse:
    def __init__(
        self,
        chunks: list[bytes | BaseException],
        *,
        content_length: str | None = None,
    ) -> None:
        self.chunks = iter(chunks)
        self.headers = {}
        if content_length is not None:
            self.headers["Content-Length"] = content_length

    def __enter__(self) -> FakeResponse:
        return self

    def __exit__(self, *args: object) -> None:
        return None

    def read(self, _size: int) -> bytes:
        item = next(self.chunks, b"")
        if isinstance(item, BaseException):
            raise item
        return item


class LimitedFetchTests(unittest.TestCase):
    def test_passes_timeout_and_accepts_exact_limit(self) -> None:
        response = FakeResponse([b"ab", b"cd", b""], content_length="4")
        with patch(
            "codex_config_atlas.cli.urllib.request.urlopen", return_value=response
        ) as urlopen:
            payload = _fetch_limited(
                "https://example.invalid/schema", timeout_seconds=1.25, max_bytes=4
            )

        self.assertEqual(payload, b"abcd")
        self.assertEqual(urlopen.call_args.kwargs["timeout"], 1.25)

    def test_rejects_declared_and_streamed_oversize_responses(self) -> None:
        cases = [
            FakeResponse([], content_length="6"),
            FakeResponse([b"abc", b"def", b""]),
        ]
        for response in cases:
            with self.subTest(response=response):
                with (
                    patch(
                        "codex_config_atlas.cli.urllib.request.urlopen",
                        return_value=response,
                    ),
                    self.assertRaisesRegex(ValueError, "exceeds maximum 5 bytes"),
                ):
                    _fetch_limited(
                        "https://example.invalid/schema",
                        timeout_seconds=1,
                        max_bytes=5,
                    )

    def test_propagates_download_interruption(self) -> None:
        response = FakeResponse([b"ab", TimeoutError("read timed out")])
        with (
            patch(
                "codex_config_atlas.cli.urllib.request.urlopen",
                return_value=response,
            ),
            self.assertRaisesRegex(TimeoutError, "read timed out"),
        ):
            _fetch_limited(
                "https://example.invalid/schema", timeout_seconds=1, max_bytes=5
            )


class TagResolutionTests(unittest.TestCase):
    def test_resolves_lightweight_and_annotated_tags_to_commits(self) -> None:
        lightweight = {
            "ref": f"refs/tags/{TAG}",
            "object": {"type": "commit", "sha": COMMIT_A},
        }
        annotated_ref = {
            "ref": f"refs/tags/{TAG}",
            "object": {"type": "tag", "sha": COMMIT_A},
        }
        annotated_tag = {
            "tag": TAG,
            "object": {"type": "commit", "sha": COMMIT_B},
        }

        with patch("codex_config_atlas.cli._fetch_json", return_value=lightweight):
            self.assertEqual(_resolve_tag_commit(TAG, timeout_seconds=1), COMMIT_A)
        with patch(
            "codex_config_atlas.cli._fetch_json",
            side_effect=[annotated_ref, annotated_tag],
        ):
            self.assertEqual(_resolve_tag_commit(TAG, timeout_seconds=1), COMMIT_B)

    def test_rejects_wrong_tag_response(self) -> None:
        payload = {
            "ref": "refs/tags/rust-v0.999.0",
            "object": {"type": "commit", "sha": COMMIT_A},
        }
        with (
            patch("codex_config_atlas.cli._fetch_json", return_value=payload),
            self.assertRaisesRegex(ValueError, "tag response mismatch"),
        ):
            _resolve_tag_commit(TAG, timeout_seconds=1)


class SyncSchemaTests(unittest.TestCase):
    def test_resync_is_idempotent_and_preserves_provenance(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            seed_registry(schemas)

            with (
                patch(
                    "codex_config_atlas.cli._resolve_tag_commit",
                    return_value=COMMIT_A,
                ),
                patch("codex_config_atlas.cli._fetch_schema", return_value=SCHEMA),
                patch(
                    "codex_config_atlas.cli.utc_now_rfc3339",
                    return_value="2026-08-20T10:00:00Z",
                ),
            ):
                _handle_sync_schema(sync_args(schemas))
                first_digest = tree_digest(schemas)
                _handle_sync_schema(sync_args(schemas))

            metadata = json_load(schemas / TAG / "metadata.json")
            self.assertEqual(tree_digest(schemas), first_digest)
            self.assertEqual(metadata["commitSha"], COMMIT_A)
            self.assertEqual(metadata["fetchedAt"], "2026-08-19T10:00:00Z")

    def test_new_registry_records_resolved_commit_and_expected_hash(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            args = sync_args(
                schemas,
                expected_commit=COMMIT_B,
                expected_sha256=sha256_bytes(SCHEMA),
            )
            with (
                patch(
                    "codex_config_atlas.cli._resolve_tag_commit",
                    return_value=COMMIT_B,
                ),
                patch("codex_config_atlas.cli._fetch_schema", return_value=SCHEMA),
            ):
                _handle_sync_schema(args)

            metadata = json_load(schemas / TAG / "metadata.json")
            self.assertEqual(metadata["commitSha"], COMMIT_B)
            self.assertEqual(metadata["schemaUrl"], schema_url_for_commit(COMMIT_B))
            self.assertEqual(metadata["schemaSha256"], sha256_bytes(SCHEMA))

    def test_network_failures_leave_installed_tree_unchanged(self) -> None:
        failures = [
            TimeoutError("network timeout"),
            ValueError("response exceeds maximum 1024 bytes"),
            EOFError("download interrupted"),
        ]
        for failure in failures:
            with self.subTest(failure=failure):
                with tempfile.TemporaryDirectory() as temporary_directory:
                    schemas = Path(temporary_directory) / "schemas"
                    seed_registry(schemas)
                    original = tree_digest(schemas)
                    with (
                        patch(
                            "codex_config_atlas.cli._resolve_tag_commit",
                            return_value=COMMIT_A,
                        ),
                        patch(
                            "codex_config_atlas.cli._fetch_schema",
                            side_effect=failure,
                        ),
                        self.assertRaises(type(failure)),
                    ):
                        _handle_sync_schema(sync_args(schemas))
                    self.assertEqual(tree_digest(schemas), original)

    def test_wrong_tag_response_leaves_installed_tree_unchanged(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            seed_registry(schemas)
            original = tree_digest(schemas)
            wrong_ref = {
                "ref": "refs/tags/rust-v0.999.0",
                "object": {"type": "commit", "sha": COMMIT_A},
            }
            with (
                patch("codex_config_atlas.cli._fetch_json", return_value=wrong_ref),
                self.assertRaisesRegex(ValueError, "tag response mismatch"),
            ):
                _handle_sync_schema(sync_args(schemas))
            self.assertEqual(tree_digest(schemas), original)

    def test_commit_and_hash_mismatches_leave_tree_unchanged(self) -> None:
        cases = [
            ({"expected_commit": COMMIT_B}, "resolved commit mismatch"),
            ({"expected_sha256": "sha256:" + "0" * 64}, "schema hash mismatch"),
        ]
        for overrides, message in cases:
            with self.subTest(message=message):
                with tempfile.TemporaryDirectory() as temporary_directory:
                    schemas = Path(temporary_directory) / "schemas"
                    seed_registry(schemas)
                    original = tree_digest(schemas)
                    with (
                        patch(
                            "codex_config_atlas.cli._resolve_tag_commit",
                            return_value=COMMIT_A,
                        ),
                        patch(
                            "codex_config_atlas.cli._fetch_schema",
                            return_value=SCHEMA,
                        ),
                        self.assertRaisesRegex(ValueError, message),
                    ):
                        _handle_sync_schema(sync_args(schemas, **overrides))
                    self.assertEqual(tree_digest(schemas), original)

    def test_registered_commit_mismatch_leaves_tree_unchanged(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            seed_registry(schemas, commit_sha=COMMIT_A)
            original = tree_digest(schemas)
            with (
                patch(
                    "codex_config_atlas.cli._resolve_tag_commit",
                    return_value=COMMIT_B,
                ),
                patch("codex_config_atlas.cli._fetch_schema", return_value=SCHEMA),
                self.assertRaisesRegex(ValueError, "registered commit mismatch"),
            ):
                _handle_sync_schema(sync_args(schemas))
            self.assertEqual(tree_digest(schemas), original)

    def test_path_traversal_is_rejected_before_network(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            seed_registry(schemas)
            manifest_path = schemas / "manifest.json"
            manifest = json_load(manifest_path)
            manifest["versions"][0]["schemaPath"] = "../outside.json"
            json_dump(manifest_path, manifest)
            original = tree_digest(schemas)

            with (
                patch("codex_config_atlas.cli._resolve_tag_commit") as resolver,
                self.assertRaisesRegex(ValueError, "registry path mismatch"),
            ):
                _handle_sync_schema(sync_args(schemas))
            resolver.assert_not_called()
            self.assertEqual(tree_digest(schemas), original)

    def test_candidate_hash_mismatch_leaves_tree_unchanged(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            seed_registry(schemas)
            original = tree_digest(schemas)

            def corrupt_metadata(path: Path, payload: object) -> None:
                if path.name == "metadata.json":
                    payload = {
                        **payload,
                        "schemaSha256": "sha256:" + "0" * 64,
                    }
                json_dump(path, payload)

            with (
                patch(
                    "codex_config_atlas.cli._resolve_tag_commit",
                    return_value=COMMIT_A,
                ),
                patch("codex_config_atlas.cli._fetch_schema", return_value=SCHEMA),
                patch(
                    "codex_config_atlas.cli.json_dump",
                    side_effect=corrupt_metadata,
                ),
                self.assertRaisesRegex(ValueError, "schemaSha256 mismatch"),
            ):
                _handle_sync_schema(sync_args(schemas))
            self.assertEqual(tree_digest(schemas), original)

    def test_write_interruption_leaves_tree_unchanged(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            seed_registry(schemas)
            original = tree_digest(schemas)

            def interrupted_write(path: Path, payload: bytes) -> None:
                path.parent.mkdir(parents=True, exist_ok=True)
                path.write_bytes(payload[:5])
                raise OSError("injected write interruption")

            with (
                patch(
                    "codex_config_atlas.cli._resolve_tag_commit",
                    return_value=COMMIT_A,
                ),
                patch("codex_config_atlas.cli._fetch_schema", return_value=SCHEMA),
                patch(
                    "codex_config_atlas.cli._write_schema_file",
                    side_effect=interrupted_write,
                ),
                self.assertRaisesRegex(OSError, "injected write interruption"),
            ):
                _handle_sync_schema(sync_args(schemas))
            self.assertEqual(tree_digest(schemas), original)

    def test_exchange_failure_preserves_original_tree(self) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            seed_registry(schemas)
            original = tree_digest(schemas)
            with (
                patch(
                    "codex_config_atlas.cli._resolve_tag_commit",
                    return_value=COMMIT_A,
                ),
                patch("codex_config_atlas.cli._fetch_schema", return_value=SCHEMA),
                patch(
                    "codex_config_atlas.atomic_output.subprocess.run",
                    return_value=subprocess.CompletedProcess(
                        args=["mv"], returncode=1, stdout="", stderr="unsupported"
                    ),
                ),
                self.assertRaisesRegex(
                    RuntimeError, "atomic directory exchange failed"
                ),
            ):
                _handle_sync_schema(sync_args(schemas))

            self.assertEqual(tree_digest(schemas), original)


class UpstreamDiscoveryAndReleaseSyncTests(unittest.TestCase):
    def test_github_token_header_is_scoped_exclusively_to_github_api(self) -> None:
        with patch.dict(
            os.environ,
            {"GITHUB_TOKEN": "ghs_test_token_value", "GH_TOKEN": ""},
            clear=False,
        ):
            api_headers = github_request_headers(
                "https://api.github.com/repos/openai/codex/releases?per_page=50&page=1"
            )
            raw_headers = github_request_headers(
                "https://raw.githubusercontent.com/openai/codex/deadbeef/schema.json"
            )
        self.assertEqual(
            api_headers.get("Authorization"), "Bearer ghs_test_token_value"
        )
        self.assertNotIn("Authorization", raw_headers)

    def test_rate_limit_error_surfaces_actionable_guidance(self) -> None:
        http_err = urllib.error.HTTPError(
            url="https://api.github.com/repos/openai/codex/releases",
            code=403,
            msg="rate limit exceeded",
            hdrs=None,  # type: ignore[arg-type]
            fp=None,
        )
        try:
            with (
                patch(
                    "codex_config_atlas.cli.urllib.request.urlopen",
                    side_effect=http_err,
                ),
                self.assertRaisesRegex(
                    RuntimeError, "set GITHUB_TOKEN or GH_TOKEN for authenticated quota"
                ),
            ):
                _fetch_limited(
                    "https://api.github.com/repos/openai/codex/releases",
                    timeout_seconds=1.0,
                    max_bytes=1024,
                )
        finally:
            http_err.close()

    def test_paginates_and_filters_upstream_stable_versions(self) -> None:
        pages = [
            (
                [
                    {
                        "tag_name": "rust-v0.150.0",
                        "draft": False,
                        "prerelease": False,
                        "published_at": "2026-09-02T00:00:00Z",
                    },
                    {
                        "tag_name": "rust-v0.151.0-alpha.1",
                        "draft": False,
                        "prerelease": False,
                        "published_at": "2026-09-03T00:00:00Z",
                    },
                    {
                        "tag_name": "rust-v0.151.0",
                        "draft": True,
                        "prerelease": False,
                        "published_at": "2026-09-03T00:00:00Z",
                    },
                    {
                        "tag_name": "rust-v0.152.0",
                        "draft": False,
                        "prerelease": True,
                        "published_at": "2026-09-04T00:00:00Z",
                    },
                    {
                        "tag_name": "v0.153.0",
                        "draft": False,
                        "prerelease": False,
                        "published_at": "2026-09-05T00:00:00Z",
                    },
                ],
                {
                    "Link": (
                        "<https://api.github.com/repos/openai/codex/releases"
                        '?per_page=25&page=2>; rel="next"'
                    )
                },
            ),
            (
                [
                    {
                        "tag_name": "rust-v0.128.0",
                        "draft": False,
                        "prerelease": False,
                        "published_at": "2026-07-01T00:00:00Z",
                    },
                    {
                        "tag_name": "rust-v0.149.0",
                        "draft": False,
                        "prerelease": False,
                        "published_at": "2026-09-01T00:00:00Z",
                    },
                ],
                {
                    "Link": (
                        "<https://api.github.com/repos/openai/codex/releases"
                        '?per_page=25&page=1>; rel="prev"'
                    )
                },
            ),
        ]
        calls: list[str] = []

        def fake_fetch_page(
            url: str, _timeout: float, _max_bytes: int
        ) -> tuple[list[object], dict[str, str]]:
            calls.append(url)
            return pages[len(calls) - 1]

        discovered = list_upstream_stable_versions(
            min_version="0.129.0",
            timeout_seconds=2.0,
            fetch_page=fake_fetch_page,
        )
        self.assertEqual(discovered, ["0.149.0", "0.150.0"])
        self.assertEqual(len(calls), 2)

    def test_sync_releases_supports_dry_run_and_stops_on_incompatible_schema(
        self,
    ) -> None:
        valid_v149 = (
            b'{"title":"CodexConfig","type":"object",'
            b'"properties":{"model":{"type":"string","default":"gpt-5"}}}\n'
        )
        incompatible_v150 = (
            b'{"title":"CodexConfig","type":"object",'
            b'"required":["model"],'
            b'"properties":{"model":{"type":"string"}}}\n'
        )
        commit_149 = "c" * 40
        commit_150 = "d" * 40

        with tempfile.TemporaryDirectory() as temporary_directory:
            schemas = Path(temporary_directory) / "schemas"
            seed_registry(schemas)

            with patch(
                "codex_config_atlas.cli.list_upstream_stable_versions",
                return_value=["0.148.0", "0.149.0", "0.150.0"],
            ):
                dry_out = io.StringIO()
                with redirect_stdout(dry_out):
                    code = main(
                        ["sync-releases", "--schemas", str(schemas), "--dry-run"]
                    )
                self.assertEqual(code, 0)
                self.assertEqual(
                    dry_out.getvalue().splitlines(),
                    [
                        "missing 0.149.0 (rust-v0.149.0)",
                        "missing 0.150.0 (rust-v0.150.0)",
                    ],
                )
                self.assertFalse((schemas / "rust-v0.149.0").exists())

                def fake_resolve(tag: str, *, timeout_seconds: float) -> str:
                    return commit_149 if tag == "rust-v0.149.0" else commit_150

                def fake_schema(
                    commit_sha: str, *, timeout_seconds: float, max_bytes: int
                ) -> bytes:
                    return valid_v149 if commit_sha == commit_149 else incompatible_v150

                out = io.StringIO()
                err = io.StringIO()
                with (
                    patch(
                        "codex_config_atlas.cli._resolve_tag_commit",
                        side_effect=fake_resolve,
                    ),
                    patch(
                        "codex_config_atlas.cli._fetch_schema",
                        side_effect=fake_schema,
                    ),
                    redirect_stdout(out),
                    redirect_stderr(err),
                ):
                    code = main(["sync-releases", "--schemas", str(schemas)])

                self.assertEqual(code, 1)
                self.assertIn("ESCALATION REQUIRED for Codex 0.150.0", err.getvalue())
                self.assertIn("preserved synced versions [0.149.0]", err.getvalue())
                manifest = json_load(schemas / "manifest.json")
                self.assertEqual(
                    [entry["version"] for entry in manifest["versions"]],
                    ["0.148.0", "0.149.0"],
                )
                self.assertTrue(
                    (schemas / "rust-v0.149.0" / "config.schema.json").is_file()
                )
                self.assertFalse((schemas / "rust-v0.150.0").exists())

    def test_sync_schema_normalizes_rust_v_prefix_and_marks_intent_to_add(
        self,
    ) -> None:
        with tempfile.TemporaryDirectory() as temporary_directory:
            repo = Path(temporary_directory) / "repo"
            schemas = repo / "tools" / "codex-config-atlas" / "schemas"
            seed_registry(schemas)
            subprocess.run(["git", "init", "-q", str(repo)], check=True)
            subprocess.run(
                [
                    "git",
                    "-C",
                    str(repo),
                    "add",
                    "--",
                    "tools/codex-config-atlas/schemas",
                ],
                check=True,
            )

            commit_149 = "e" * 40
            schema_149 = (
                b'{"title":"CodexConfig","type":"object",'
                b'"properties":{"approval_policy":{"type":"string","default":"on-request"}}}\n'
            )
            out = io.StringIO()
            with (
                patch(
                    "codex_config_atlas.cli._resolve_tag_commit",
                    return_value=commit_149,
                ),
                patch(
                    "codex_config_atlas.cli._fetch_schema",
                    return_value=schema_149,
                ),
                redirect_stdout(out),
            ):
                code = main(
                    [
                        "sync-schema",
                        "--repo",
                        str(repo),
                        "--version",
                        "rust-v0.149.0",
                        "--intent-to-add",
                    ]
                )

            expected_schema_rel = (
                "tools/codex-config-atlas/schemas/rust-v0.149.0/config.schema.json"
            )
            expected_meta_rel = (
                "tools/codex-config-atlas/schemas/rust-v0.149.0/metadata.json"
            )
            self.assertEqual(code, 0)
            self.assertIn(f"intent-to-add: {expected_schema_rel}", out.getvalue())
            status = subprocess.run(
                [
                    "git",
                    "-C",
                    str(repo),
                    "status",
                    "--porcelain=v1",
                    "--",
                    "tools/codex-config-atlas/schemas/rust-v0.149.0",
                ],
                check=True,
                text=True,
                capture_output=True,
            ).stdout.splitlines()
            self.assertEqual(
                status,
                [
                    f" A {expected_schema_rel}",
                    f" A {expected_meta_rel}",
                ],
            )


if __name__ == "__main__":
    unittest.main()
