from __future__ import annotations

import argparse
import shutil
from pathlib import Path

from common import (
    PATCH_NAME,
    add_ref_argument,
    check_series_against_index,
    ensure_no_staged_changes,
    json_stdout,
    load_upstream,
    main_wrapper,
    patch_dir,
    read_series,
    require_git_worktree,
    run,
    schema_file,
    upstream_build_env,
    worktree_path,
)

SPLIT_PATCH_NAMES = [
    "0001-openai-provider-network-overrides.patch",
    "0002-model-request-failure-hooks.patch",
    "0003-plan-mode-request-user-input-auto-resolution.patch",
]

MODEL_PROVIDER_PATCH_PATHS = frozenset(
    {
        "codex-rs/app-server/src/request_processors/thread_processor_tests.rs",
        "codex-rs/config/src/config_toml.rs",
        "codex-rs/config/src/thread_config.rs",
        "codex-rs/config/src/thread_config/proto/codex.thread_config.v1.proto",
        "codex-rs/config/src/thread_config/proto/codex.thread_config.v1.rs",
        "codex-rs/config/src/thread_config/remote.rs",
        "codex-rs/core/src/client.rs",
        "codex-rs/core/src/compact_tests.rs",
        "codex-rs/core/tests/responses_headers.rs",
        "codex-rs/core/tests/suite/client.rs",
        "codex-rs/core/tests/suite/client_websockets.rs",
        "codex-rs/core/tests/suite/stream_error_allows_next_turn.rs",
        "codex-rs/core/tests/suite/stream_no_completed.rs",
        "codex-rs/login/src/auth_env_telemetry.rs",
        "codex-rs/model-provider-info/src/lib.rs",
        "codex-rs/model-provider-info/src/model_provider_info_tests.rs",
        "codex-rs/model-provider-info/src/openai_overrides.rs",
        "codex-rs/model-provider/src/provider.rs",
    }
)

REQUEST_INPUT_PATCH_PATHS = frozenset(
    {
        "codex-rs/core/src/tools/handlers/request_user_input.rs",
        "codex-rs/core/src/tools/handlers/request_user_input_spec.rs",
        "codex-rs/core/src/tools/handlers/request_user_input_spec_tests.rs",
        "codex-rs/core/tests/suite/request_user_input.rs",
    }
)

SHARED_CONFIG_SCHEMA_PATH = "codex-rs/core/config.schema.json"


def _intent_to_add_untracked(src):
    untracked = run(["git", "ls-files", "--others", "--exclude-standard"], cwd=src, capture=True).stdout.splitlines()
    if untracked:
        run(["git", "add", "-N", "--", *untracked], cwd=src)
    return untracked


def _diff_blocks(diff: str) -> list[list[str]]:
    blocks: list[list[str]] = []
    current: list[str] = []
    for line in diff.splitlines(keepends=True):
        if line.startswith("diff --git ") and current:
            blocks.append(current)
            current = [line]
        else:
            current.append(line)
    if current:
        blocks.append(current)
    return blocks


def _diff_block_path(block: list[str]) -> str:
    header = block[0].strip()
    marker = " b/"
    if marker not in header:
        raise RuntimeError(f"cannot parse diff header: {header}")
    return header.split(marker, 1)[1]


def _split_hunks(block: list[str]) -> tuple[list[str], list[list[str]]]:
    header: list[str] = []
    hunks: list[list[str]] = []
    current: list[str] | None = None
    for line in block:
        if line.startswith("@@ "):
            if current is not None:
                hunks.append(current)
            current = [line]
        elif current is None:
            header.append(line)
        else:
            current.append(line)
    if current is not None:
        hunks.append(current)
    return header, hunks


def _series_names(series: Path) -> list[str]:
    if not series.exists():
        return []
    return [line.strip() for line in series.read_text().splitlines() if line.strip() and not line.strip().startswith("#")]


def _split_patch_texts(diff: str) -> dict[str, str]:
    patch_blocks: dict[str, list[list[str]]] = {name: [] for name in SPLIT_PATCH_NAMES}
    for block in _diff_blocks(diff):
        path = _diff_block_path(block)
        if path == SHARED_CONFIG_SCHEMA_PATH:
            header, hunks = _split_hunks(block)
            model_hunks: list[list[str]] = []
            hooks_hunks: list[list[str]] = []
            for hunk in hunks:
                if "compact_request_timeout_ms" in "".join(hunk):
                    model_hunks.append(hunk)
                else:
                    hooks_hunks.append(hunk)
            if model_hunks:
                patch_blocks[SPLIT_PATCH_NAMES[0]].append(header + [line for hunk in model_hunks for line in hunk])
            if hooks_hunks:
                patch_blocks[SPLIT_PATCH_NAMES[1]].append(header + [line for hunk in hooks_hunks for line in hunk])
        elif path in MODEL_PROVIDER_PATCH_PATHS:
            patch_blocks[SPLIT_PATCH_NAMES[0]].append(block)
        elif path in REQUEST_INPUT_PATCH_PATHS:
            patch_blocks[SPLIT_PATCH_NAMES[2]].append(block)
        else:
            patch_blocks[SPLIT_PATCH_NAMES[1]].append(block)

    patch_texts = {name: "".join(line for block in blocks for line in block) for name, blocks in patch_blocks.items()}
    empty = [name for name, text in patch_texts.items() if not text]
    if empty:
        raise RuntimeError(f"split refresh produced empty patch file(s): {', '.join(empty)}")
    return patch_texts


def _write_patch_files(directory: Path, series: Path, diff: str) -> list[Path]:
    if _series_names(series) == SPLIT_PATCH_NAMES:
        patch_texts = _split_patch_texts(diff)
        for name in SPLIT_PATCH_NAMES:
            (directory / name).write_text(patch_texts[name])
        series.write_text("".join(f"{name}\n" for name in SPLIT_PATCH_NAMES))
        return [directory / name for name in SPLIT_PATCH_NAMES]

    patch = directory / PATCH_NAME
    patch.write_text(diff)
    series.write_text(f"{PATCH_NAME}\n")
    return [patch]


def main() -> int:
    parser = argparse.ArgumentParser(description="Refresh the Codex patch and generated schema")
    add_ref_argument(parser)
    args = parser.parse_args()

    upstream = load_upstream()
    src = worktree_path(args.ref, upstream)
    directory = patch_dir(args.ref, upstream)
    directory.mkdir(parents=True, exist_ok=True)
    series = directory / "series"
    schema = directory / "config.schema.json"

    require_git_worktree(src)
    ensure_no_staged_changes(src)

    run(list(upstream.get("schemaCommand", ["just", "write-config-schema"])), cwd=src, env=upstream_build_env(src))
    generated_schema = schema_file(src, upstream)
    if not generated_schema.exists():
        raise RuntimeError(f"schema command did not create {generated_schema}")
    shutil.copyfile(generated_schema, schema)

    untracked = _intent_to_add_untracked(src)
    diff = run(["git", "diff", "--binary"], cwd=src, capture=True).stdout
    if not diff:
        raise RuntimeError("no upstream changes to write as a patch")
    patches = _write_patch_files(directory, series, diff)

    try:
        check_series_against_index(src, read_series(args.ref, upstream))
    finally:
        if untracked:
            run(["git", "reset", "-q", "--", *untracked], cwd=src, check=False)

    json_stdout(
        {
            "ref": args.ref,
            "patches": [str(patch) for patch in patches],
            "series": str(series),
            "schema": str(schema),
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
