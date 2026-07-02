from __future__ import annotations

import argparse
import shutil

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


def _intent_to_add_untracked(src):
    untracked = run(["git", "ls-files", "--others", "--exclude-standard"], cwd=src, capture=True).stdout.splitlines()
    if untracked:
        run(["git", "add", "-N", "--", *untracked], cwd=src)
    return untracked


def main() -> int:
    parser = argparse.ArgumentParser(description="Refresh the Codex patch and generated schema")
    add_ref_argument(parser)
    args = parser.parse_args()

    upstream = load_upstream()
    src = worktree_path(args.ref, upstream)
    directory = patch_dir(args.ref, upstream)
    directory.mkdir(parents=True, exist_ok=True)
    patch = directory / PATCH_NAME
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
    patch.write_text(diff)
    series.write_text(f"{PATCH_NAME}\n")

    try:
        check_series_against_index(src, read_series(args.ref, upstream))
    finally:
        if untracked:
            run(["git", "reset", "-q", "--", *untracked], cwd=src, check=False)

    json_stdout(
        {
            "ref": args.ref,
            "patch": str(patch),
            "series": str(series),
            "schema": str(schema),
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
