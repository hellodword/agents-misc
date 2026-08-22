from __future__ import annotations

import argparse

from common import add_repo_root_argument, json_stdout, load_manifest, main_wrapper, refresh_patches


def main() -> int:
    parser = argparse.ArgumentParser(
        description="Regenerate and atomically refresh the pinned Codex patch series"
    )
    add_repo_root_argument(parser)
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="Generate and fully validate candidates without replacing checked-in patches",
    )
    args = parser.parse_args()
    manifest = load_manifest(args.repo_root, require_patch_files=False)
    json_stdout(refresh_patches(manifest, dry_run=args.dry_run))
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
