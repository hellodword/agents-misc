from __future__ import annotations

import argparse

from common import (
    add_repo_root_argument,
    apply_patches,
    ensure_clean,
    ensure_pinned_head,
    ensure_real_index_clean,
    json_stdout,
    load_manifest,
    main_wrapper,
    patch_paths,
    require_git_worktree,
)


def main() -> int:
    parser = argparse.ArgumentParser(description="Apply or check the pinned Codex patch series")
    add_repo_root_argument(parser)
    parser.add_argument("--check", action="store_true", help="Check cumulative applicability without changing files")
    args = parser.parse_args()
    manifest = load_manifest(args.repo_root)
    src = manifest.worktree
    require_git_worktree(src)
    ensure_clean(src)
    ensure_real_index_clean(src)
    ensure_pinned_head(src, manifest.upstream)
    patches = patch_paths(manifest)
    apply_patches(src, patches, check_only=args.check)
    json_stdout(
        {
            "ref": manifest.upstream.ref,
            "revision": manifest.upstream.revision,
            "mode": "check" if args.check else "apply",
            "worktree": str(src),
            "patches": [patch.name for patch in patches],
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
