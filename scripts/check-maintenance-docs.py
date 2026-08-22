#!/usr/bin/env python3
"""Validate maintenance README links and safely exercise documented commands."""

from __future__ import annotations

import argparse
import os
import re
import shlex
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path
from typing import Callable, Sequence
from urllib.parse import unquote, urlsplit


MAINTENANCE_DOCS = (
    "README.md",
    "codex/README.md",
    "tools/codex-config-atlas/README.md",
    "tools/agents-viewer/README.md",
    "tools/codex-hooks/README.md",
    "tests/evals/README.md",
)
SHELL_FENCES = {"bash", "sh", "shell"}
LINK_RE = re.compile(r"!?\[[^\]]*\]\(([^)]+)\)")
ENV_ASSIGNMENT_RE = re.compile(r"^[A-Za-z_][A-Za-z0-9_]*=.*$")
HEADING_RE = re.compile(r"^#{1,6}\s+(.+?)\s*#*\s*$")


@dataclass(frozen=True)
class DocumentedCommand:
    document: Path
    line: int
    text: str


@dataclass(frozen=True)
class SmokeCommand:
    documented: DocumentedCommand
    argv: tuple[str, ...]
    environment: tuple[tuple[str, str], ...] = ()


Runner = Callable[..., subprocess.CompletedProcess[str]]


def _github_slug(value: str) -> str:
    lowered = value.strip().lower()
    lowered = re.sub(r"[^\w\- ]", "", lowered, flags=re.UNICODE)
    return re.sub(r"[ ]+", "-", lowered)


def _heading_slugs(path: Path) -> set[str]:
    counts: dict[str, int] = {}
    slugs: set[str] = set()
    for line in path.read_text(encoding="utf-8").splitlines():
        match = HEADING_RE.match(line)
        if match is None:
            continue
        base = _github_slug(match.group(1))
        count = counts.get(base, 0)
        counts[base] = count + 1
        slugs.add(base if count == 0 else f"{base}-{count}")
    return slugs


def _check_links(root: Path, document: Path, errors: list[str]) -> None:
    text = document.read_text(encoding="utf-8")
    root_resolved = root.resolve()
    in_fence = False
    for line_number, line in enumerate(text.splitlines(), 1):
        if line.startswith("```"):
            in_fence = not in_fence
            continue
        if in_fence:
            continue
        for match in LINK_RE.finditer(line):
            raw_target = match.group(1).strip()
            if raw_target.startswith("<") and raw_target.endswith(">"):
                raw_target = raw_target[1:-1]
            target = raw_target.split(maxsplit=1)[0]
            parsed = urlsplit(target)
            if parsed.scheme:
                if parsed.scheme != "https" or not parsed.netloc:
                    errors.append(
                        f"{document.relative_to(root)}:{line_number}: "
                        f"external links must be absolute HTTPS URLs: {target!r}"
                    )
                continue
            if parsed.netloc or parsed.query:
                errors.append(
                    f"{document.relative_to(root)}:{line_number}: "
                    f"unsupported local link form: {target!r}"
                )
                continue
            relative_path = unquote(parsed.path)
            linked = (
                document if not relative_path else document.parent / relative_path
            ).resolve()
            try:
                linked.relative_to(root_resolved)
            except ValueError:
                errors.append(
                    f"{document.relative_to(root)}:{line_number}: "
                    f"local link escapes repository: {target!r}"
                )
                continue
            if not linked.exists():
                errors.append(
                    f"{document.relative_to(root)}:{line_number}: "
                    f"local link target does not exist: {target!r}"
                )
                continue
            if parsed.fragment:
                if not linked.is_file() or linked.suffix.lower() != ".md":
                    errors.append(
                        f"{document.relative_to(root)}:{line_number}: "
                        f"link fragment requires a Markdown file: {target!r}"
                    )
                elif unquote(parsed.fragment) not in _heading_slugs(linked):
                    errors.append(
                        f"{document.relative_to(root)}:{line_number}: "
                        f"Markdown heading does not exist: {target!r}"
                    )


def extract_shell_commands(
    root: Path, document: Path
) -> tuple[list[DocumentedCommand], list[str]]:
    commands: list[DocumentedCommand] = []
    errors: list[str] = []
    fence: str | None = None
    for line_number, line in enumerate(
        document.read_text(encoding="utf-8").splitlines(), 1
    ):
        if line.startswith("```"):
            language = line[3:].strip().lower()
            if fence is None:
                fence = language
            else:
                fence = None
            continue
        if fence not in SHELL_FENCES:
            continue
        stripped = line.strip()
        if not stripped or stripped.startswith("#"):
            continue
        if stripped.endswith("\\"):
            errors.append(
                f"{document.relative_to(root)}:{line_number}: "
                "documented shell commands must use one logical line"
            )
            continue
        commands.append(
            DocumentedCommand(
                document=document.relative_to(root),
                line=line_number,
                text=stripped,
            )
        )
    if fence is not None:
        errors.append(f"{document.relative_to(root)}: unclosed Markdown fence")
    return commands, errors


def _split_environment(tokens: list[str]) -> tuple[dict[str, str], list[str]]:
    environment: dict[str, str] = {}
    while tokens and ENV_ASSIGNMENT_RE.fullmatch(tokens[0]):
        name, value = tokens.pop(0).split("=", 1)
        environment[name] = value
    return environment, tokens


def _nix_build_smoke(tokens: list[str]) -> tuple[str, ...]:
    installables = [token for token in tokens[2:] if not token.startswith("-")]
    if not installables:
        raise ValueError("nix build command has no installable")
    return (
        "nix",
        "build",
        "--dry-run",
        "--no-link",
        "--accept-flake-config",
        *installables,
    )


def _nix_develop_smoke(tokens: list[str]) -> tuple[str, ...]:
    try:
        command_index = tokens.index("--command")
    except ValueError as exc:
        raise ValueError("nix develop command must use --command") from exc
    command = tokens[command_index + 1 :]
    if not command:
        raise ValueError("nix develop --command is empty")
    smoke = list(tokens)
    if (
        len(command) >= 2
        and command[0] == "python3"
        and command[1].endswith("codex_hook_notify_server.py")
        and "--help" not in command
    ):
        smoke = tokens[: command_index + 1] + command[:2] + ["--help"]
    safe = (
        "--help" in smoke
        or command[:2] == ["python3", "scripts/check-agent-rules.py"]
        or command[:3] == ["python3", "-m", "unittest"]
        or command[:2] in (["ruff", "check"], ["ruff", "format"])
    )
    if not safe:
        raise ValueError("nix develop command has no reviewed help/smoke mapping")
    return tuple(smoke)


def smoke_for(command: DocumentedCommand) -> SmokeCommand:
    try:
        tokens = shlex.split(command.text, comments=False, posix=True)
    except ValueError as exc:
        raise ValueError(f"invalid shell syntax: {exc}") from exc
    if "#" in tokens:
        tokens = tokens[: tokens.index("#")]
    environment, tokens = _split_environment(tokens)
    if not tokens:
        raise ValueError("empty shell command")
    if tokens[0] == "just":
        arguments = tokens[1:]
        if arguments and arguments[0] == "--":
            arguments = arguments[1:]
        if not arguments or arguments[0].startswith("-"):
            raise ValueError("just command does not name a recipe")
        argv = ("just", "--show", arguments[0])
    elif tokens[:2] == ["git", "status"]:
        argv = tuple(tokens)
    elif tokens[:2] == ["nix", "fmt"]:
        argv = ("nix", "fmt", "--", "--help")
    elif tokens[:3] == ["nix", "flake", "check"]:
        argv = (
            "nix",
            "flake",
            "check",
            "--no-build",
            "--accept-flake-config",
        )
    elif tokens[:2] == ["nix", "build"]:
        argv = _nix_build_smoke(tokens)
    elif tokens[:2] == ["nix", "develop"]:
        argv = _nix_develop_smoke(tokens)
    elif tokens[:2] == ["nix", "run"] and "--help" in tokens:
        argv = tuple(tokens)
    else:
        raise ValueError("command has no reviewed help/smoke mapping")
    return SmokeCommand(
        documented=command,
        argv=argv,
        environment=tuple(sorted(environment.items())),
    )


def check_repository(root: Path) -> tuple[list[str], list[SmokeCommand]]:
    errors: list[str] = []
    smokes: list[SmokeCommand] = []
    for relative in MAINTENANCE_DOCS:
        document = root / relative
        if not document.is_file():
            errors.append(f"{relative}: required maintenance document is missing")
            continue
        try:
            _check_links(root, document, errors)
            commands, extraction_errors = extract_shell_commands(root, document)
        except (OSError, UnicodeDecodeError) as exc:
            errors.append(f"{relative}: cannot read document: {exc}")
            continue
        errors.extend(extraction_errors)
        for command in commands:
            try:
                smokes.append(smoke_for(command))
            except ValueError as exc:
                errors.append(
                    f"{command.document}:{command.line}: {exc}: {command.text!r}"
                )
    return errors, smokes


def execute_smokes(
    root: Path,
    smokes: Sequence[SmokeCommand],
    *,
    runner: Runner = subprocess.run,
) -> list[str]:
    errors: list[str] = []
    seen: set[tuple[tuple[str, ...], tuple[tuple[str, str], ...]]] = set()
    for smoke in smokes:
        identity = (smoke.argv, smoke.environment)
        if identity in seen:
            continue
        seen.add(identity)
        location = f"{smoke.documented.document}:{smoke.documented.line}"
        print(f"docs smoke {location}: {shlex.join(smoke.argv)}", file=sys.stderr)
        environment = os.environ.copy()
        environment.update(smoke.environment)
        try:
            result = runner(
                smoke.argv,
                cwd=root,
                env=environment,
                text=True,
                stdout=subprocess.PIPE,
                stderr=subprocess.STDOUT,
                timeout=300,
                check=False,
            )
        except (OSError, subprocess.TimeoutExpired) as exc:
            errors.append(f"{location}: smoke execution failed: {exc}")
            continue
        if result.returncode != 0:
            output = result.stdout[-4000:] if result.stdout else ""
            errors.append(
                f"{location}: smoke exited {result.returncode}: "
                f"{shlex.join(smoke.argv)}\n{output}"
            )
    return errors


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path.cwd())
    parser.add_argument(
        "--execute-commands",
        action="store_true",
        help="run each extracted command through its reviewed help/smoke mapping",
    )
    return parser.parse_args()


def main() -> int:
    args = parse_args()
    root = args.root.resolve()
    errors, smokes = check_repository(root)
    if not errors and args.execute_commands:
        errors.extend(execute_smokes(root, smokes))
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        return 1
    print(
        f"maintenance docs valid: {len(MAINTENANCE_DOCS)} files, "
        f"{len(smokes)} documented commands",
        file=sys.stderr,
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
