from __future__ import annotations

import argparse

from common import (
    add_ref_argument,
    command_exists,
    jobs_limit,
    json_stdout,
    main_wrapper,
    run,
    upstream_build_env,
    worktree_path,
)


DEFAULT_PACKAGES = [
    "codex-protocol",
    "codex-config",
    "codex-hooks",
    "codex-core",
    "codex-app-server-protocol",
    "codex-analytics",
    "codex-app-server",
    "codex-tui",
]


def main() -> int:
    parser = argparse.ArgumentParser(description="Run the narrow Codex cargo check")
    add_ref_argument(parser)
    parser.add_argument("--package", action="append", dest="packages", help="Cargo package to check")
    args = parser.parse_args()

    if not command_exists("cargo"):
        raise RuntimeError("cargo is not available in this environment")

    src = worktree_path(args.ref)
    packages = args.packages or DEFAULT_PACKAGES
    command = ["cargo", "check"]
    for package in packages:
        command.extend(["-p", package])
    command.extend(["--jobs", str(jobs_limit())])
    run(command, cwd=src / "codex-rs", env=upstream_build_env(src))
    json_stdout(
        {
            "ref": args.ref,
            "worktree": str(src),
            "packages": packages,
            "jobs": jobs_limit(),
        }
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main_wrapper(main))
