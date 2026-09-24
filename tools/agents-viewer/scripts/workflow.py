#!/usr/bin/env python3
"""Run the multi-stage Agents Viewer source verification workflows."""

from __future__ import annotations

import argparse
import shlex
import subprocess
import sys
from pathlib import Path
from typing import Sequence


ROOT = Path(__file__).resolve().parents[3]
CARGO_MANIFEST = "tools/agents-viewer/Cargo.toml"
WEB_ROOT = "tools/agents-viewer/web"
GENERATED_API = f"{WEB_ROOT}/src/generated/api.ts"


def run(command: Sequence[str]) -> None:
    print(f"+ {shlex.join(command)}", flush=True)
    subprocess.run(command, cwd=ROOT, check=True)


def verify() -> None:
    commands = (
        (
            "ruff",
            "format",
            "--check",
            "tools/agents-viewer/scripts/workflow.py",
        ),
        ("ruff", "check", "tools/agents-viewer/scripts/workflow.py"),
        (
            "cargo",
            "run",
            "--locked",
            "--manifest-path",
            CARGO_MANIFEST,
            "--bin",
            "export_types",
            "--",
            "--check",
            "--output",
            GENERATED_API,
        ),
        (
            "cargo",
            "fmt",
            "--manifest-path",
            CARGO_MANIFEST,
            "--all",
            "--",
            "--check",
        ),
        (
            "cargo",
            "clippy",
            "--locked",
            "--manifest-path",
            CARGO_MANIFEST,
            "--all-targets",
            "--",
            "-D",
            "warnings",
        ),
        ("cargo", "test", "--locked", "--manifest-path", CARGO_MANIFEST),
        ("npm", "--prefix", WEB_ROOT, "ci"),
        ("npm", "--prefix", WEB_ROOT, "run", "typecheck"),
        ("npm", "--prefix", WEB_ROOT, "run", "test"),
        ("npm", "--prefix", WEB_ROOT, "run", "build"),
        (
            "cargo",
            "clippy",
            "--locked",
            "--manifest-path",
            CARGO_MANIFEST,
            "--bin",
            "agents-viewer",
            "--features",
            "embedded-ui",
            "--",
            "-D",
            "warnings",
        ),
    )
    for command in commands:
        run(command)


def e2e(playwright_args: Sequence[str]) -> None:
    commands = (
        ("npm", "--prefix", WEB_ROOT, "ci"),
        ("npm", "--prefix", WEB_ROOT, "run", "build"),
        (
            "cargo",
            "build",
            "--locked",
            "--manifest-path",
            CARGO_MANIFEST,
            "--bin",
            "agents-viewer",
            "--features",
            "embedded-ui",
        ),
        (
            "npm",
            "--prefix",
            WEB_ROOT,
            "run",
            "e2e",
            "--",
            *playwright_args,
        ),
    )
    for command in commands:
        run(command)


def parse_args(argv: Sequence[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description="Run Agents Viewer verification workflows inside its Nix shell."
    )
    subparsers = parser.add_subparsers(dest="command", required=True)
    subparsers.add_parser("verify", help="run browser-independent source verification")
    e2e_parser = subparsers.add_parser(
        "e2e", help="build the embedded debug binary and run Playwright"
    )
    e2e_parser.add_argument("playwright_args", nargs=argparse.REMAINDER)
    return parser.parse_args(argv)


def main(argv: Sequence[str] | None = None) -> int:
    args = parse_args(sys.argv[1:] if argv is None else argv)
    try:
        if args.command == "verify":
            verify()
        else:
            playwright_args = args.playwright_args
            if playwright_args[:1] == ["--"]:
                playwright_args = playwright_args[1:]
            e2e(playwright_args)
    except subprocess.CalledProcessError as error:
        return error.returncode
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
