from __future__ import annotations

import argparse

from common import add_repo_root_argument, fetch_upstream, json_stdout, load_manifest, main_wrapper


def main() -> int:
    parser = argparse.ArgumentParser(description="Fetch the one pinned Codex upstream revision")
    add_repo_root_argument(parser)
    args = parser.parse_args()
    manifest = load_manifest(args.repo_root)
    resolved = fetch_upstream(manifest)
    json_stdout(
        {
            "ref": manifest.upstream.ref,
            "revision": resolved,
            "worktree": str(manifest.worktree),
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
