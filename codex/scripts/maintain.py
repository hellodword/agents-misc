#!/usr/bin/env python3
"""Single command-line entrypoint for the pinned Codex patch workflow."""

from __future__ import annotations

import argparse

from workflow import (
    add_repo_root_argument,
    apply_patches,
    check_patches,
    fetch_upstream,
    json_stdout,
    load_manifest,
    main_wrapper,
    refresh_patches,
    verify_patches,
)


def build_parser() -> argparse.ArgumentParser:
    parser = argparse.ArgumentParser(
        description="Maintain the manifest-declared Codex patch series"
    )
    add_repo_root_argument(parser)
    commands = parser.add_subparsers(dest="command", required=True)
    commands.add_parser("fetch", help="create or validate the pinned upstream checkout")
    apply = commands.add_parser("apply", help="apply the complete patch series")
    apply.add_argument(
        "--check",
        action="store_true",
        help="validate cumulative binary application in a temporary checkout",
    )
    refresh = commands.add_parser("refresh", help="regenerate and validate patches")
    refresh.add_argument(
        "--dry-run",
        action="store_true",
        help="validate candidate patch hashes without changing tracked files",
    )
    check = commands.add_parser(
        "check", help="run focused tests for one patch or the integration group"
    )
    check.add_argument("selector", help="patch number/name or integration")
    commands.add_parser("verify", help="replay, regenerate, and run all declared gates")
    return parser


def main() -> int:
    args = build_parser().parse_args()
    manifest = load_manifest(
        args.repo_root,
        require_patch_files=args.command not in {"fetch", "refresh"},
    )
    if args.command == "fetch":
        result = fetch_upstream(manifest)
    elif args.command == "apply":
        result = apply_patches(manifest, check_only=args.check)
    elif args.command == "refresh":
        result = refresh_patches(manifest, dry_run=args.dry_run)
    elif args.command == "check":
        result = check_patches(manifest, selector=args.selector)
    else:
        result = verify_patches(manifest)
    json_stdout(
        {
            "command": args.command,
            "ref": manifest.upstream.ref,
            "revision": manifest.upstream.revision,
            "worktree": str(manifest.worktree),
            **result,
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
