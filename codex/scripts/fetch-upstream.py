from __future__ import annotations

import argparse

from common import add_ref_argument, fetch_ref, json_stdout, load_upstream, main_wrapper, worktree_path


def main() -> int:
    parser = argparse.ArgumentParser(description="Fetch the Codex upstream ref into .work")
    add_ref_argument(parser)
    args = parser.parse_args()

    upstream = load_upstream()
    src = worktree_path(args.ref, upstream)
    checkout_ref = fetch_ref(src, args.ref, upstream)
    json_stdout(
        {
            "ref": args.ref,
            "checkoutRef": checkout_ref,
            "worktree": str(src),
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
