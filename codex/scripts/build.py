from __future__ import annotations

import argparse

from common import (
    add_repo_root_argument,
    ensure_pinned_head,
    json_stdout,
    load_manifest,
    main_wrapper,
    require_git_worktree,
    run_upstream,
)


def main() -> int:
    parser = argparse.ArgumentParser(description="Run the manifest-defined Codex validation command")
    add_repo_root_argument(parser)
    args = parser.parse_args()
    manifest = load_manifest(args.repo_root)
    require_git_worktree(manifest.worktree)
    ensure_pinned_head(manifest.worktree, manifest.upstream)
    run_upstream(manifest, manifest.upstream.validation_command, cwd=manifest.worktree)
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
