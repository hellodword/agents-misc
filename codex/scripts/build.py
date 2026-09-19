from __future__ import annotations

import argparse

from common import (
    add_repo_root_argument,
    ensure_pinned_head,
    json_stdout,
    load_manifest,
    main_wrapper,
    require_git_worktree,
    run_manifest_validation,
)


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Run the manifest-defined pinned Codex validation and regression commands"
    )
    add_repo_root_argument(parser)
    args = parser.parse_args()
    manifest = load_manifest(args.repo_root)
    require_git_worktree(manifest.worktree)
    ensure_pinned_head(manifest.worktree, manifest.upstream)
    run_manifest_validation(manifest, manifest.worktree, include_patch_tests=False)
    json_stdout(
        {
            "ref": manifest.upstream.ref,
            "revision": manifest.upstream.revision,
            "command": list(manifest.upstream.validation_command),
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
