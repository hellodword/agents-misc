from __future__ import annotations

import argparse

from common import (
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
    worktree_path,
)


def main() -> int:
    parser = argparse.ArgumentParser(description="Apply or check a Codex patch series")
    add_ref_argument(parser)
    parser.add_argument("--check", action="store_true", help="Check applicability without changing files")
    args = parser.parse_args()

    upstream = load_upstream()
    src = worktree_path(args.ref, upstream)
    require_git_worktree(src)
    ensure_clean(src)
    checkout_ref(src, args.ref)
    patches = read_series(args.ref, upstream)
    apply_series(src, patches, check_only=args.check)
    json_stdout(
        {
            "ref": args.ref,
            "mode": "check" if args.check else "apply",
            "worktree": str(src),
            "patchDir": str(patch_dir(args.ref, upstream)),
            "patches": [patch.name for patch in patches],
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
