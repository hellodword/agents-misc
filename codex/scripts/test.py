from __future__ import annotations

import argparse
import filecmp

from common import (
    CODEX_ROOT,
    add_ref_argument,
    apply_series,
    checkout_ref,
    ensure_clean,
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


def main() -> int:
    parser = argparse.ArgumentParser(description="Validate a Codex patch series")
    add_ref_argument(parser)
    parser.add_argument("--cargo-check", action="store_true", help="Also run the narrow cargo check")
    args = parser.parse_args()

    upstream = load_upstream()
    src = worktree_path(args.ref, upstream)
    directory = patch_dir(args.ref, upstream)
    saved_schema = directory / "config.schema.json"
    require_git_worktree(src)
    ensure_clean(src)
    checkout_ref(src, args.ref)
    patches = read_series(args.ref, upstream)
    apply_series(src, patches, check_only=True, index=True)

    applied = False
    try:
        apply_series(src, patches, check_only=False, index=True)
        applied = True
        run(list(upstream.get("schemaCommand", ["just", "write-config-schema"])), cwd=src, env=upstream_build_env(src))
        generated_schema = schema_file(src, upstream)
        if not saved_schema.exists():
            raise RuntimeError(f"missing saved schema: {saved_schema}")
        if not filecmp.cmp(generated_schema, saved_schema, shallow=False):
            raise RuntimeError(f"generated schema differs from saved schema: {saved_schema}")
        if args.cargo_check:
            run(["python3", str(CODEX_ROOT / "scripts" / "build.py"), "--ref", args.ref])
    finally:
        if applied:
            run(["git", "reset", "--hard", "HEAD"], cwd=src, check=False)

    json_stdout(
        {
            "ref": args.ref,
            "worktree": str(src),
            "patchDir": str(directory),
            "patches": [patch.name for patch in patches],
            "schema": str(saved_schema),
            "cargoCheck": args.cargo_check,
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
