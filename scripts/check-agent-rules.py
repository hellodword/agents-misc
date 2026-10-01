#!/usr/bin/env python3
"""Validate the structure and local references of the distributed Agent Rules Kit."""

from __future__ import annotations

import argparse
import os
import re
import sys
from pathlib import Path
from urllib.parse import urlsplit

import yaml


SKILL_NAME_PATTERN = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
MARKDOWN_LINK_PATTERN = re.compile(
    r"(?P<definition>^[ \t]*(?:>[ \t]*)*\[[^\]\n]+\]:[^\n]*)"
    r"|!?\[[^\]\n]*\](?:\((?P<target>[^)\n]*)(?P<close>\)|$)"
    r"|[ \t]*(?:\n[ \t]*)?(?P<reference>\[[^\]\n]*\]))",
    re.MULTILINE,
)
FENCE_PATTERN = re.compile(r"^[ \t]*(?:>[ \t]*)*(`{3,}|~{3,})(.*)$")
CODE_PATTERN = re.compile(
    r"\\[\\`\[\]]|(?<!`)(?P<ticks>`+)(?!`)(?:(?!\n[ \t]*\n)[\s\S])*?(?<!`)(?P=ticks)(?!`)"
)
UNSUPPORTED_TARGET_PATTERN = re.compile(r"[\s()<>\"'\\`\[\]\x00-\x1f\x7f]")
FRONTMATTER_PATTERN = re.compile(r"\A---\r?\n(.*?)\r?\n---(?:\r?\n|\Z)", re.DOTALL)
RESOURCE_DIRS = {"assets", "references", "scripts"}


def _payload_paths(root: Path, errors: list[str]) -> set[Path]:
    paths: set[Path] = set()
    pending = [root / ".agents", root / "AGENTS.md"]
    while pending:
        path = pending.pop()
        try:
            if path.is_symlink():
                errors.append(
                    f"{path}: symlinks are not allowed; use a real file or directory"
                )
                continue
            if path.is_dir():
                pending.extend(reversed(sorted(path.iterdir())))
            elif not path.is_file():
                errors.append(f"{path}: expected an existing file or directory")
                continue
            paths.add(path)
        except OSError as exc:
            errors.append(f"{path}: cannot inspect payload: {exc}")
    return paths


def _read_text(path: Path, errors: list[str]) -> str | None:
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        errors.append(f"{path}: must be readable UTF-8: {exc}")
        return None


def _prose(text: str) -> str:
    """Mask code examples without changing source line numbers."""
    lines: list[str] = []
    fence = ""
    for line in text.splitlines(keepends=True):
        match = FENCE_PATTERN.match(line)
        if fence:
            if (
                match
                and match[1][0] == fence[0]
                and len(match[1]) >= len(fence)
                and not match[2].strip()
            ):
                fence = ""
            lines.append("\n" if line.endswith("\n") else "")
        elif match and not (match[1][0] == "`" and "`" in match[2]):
            fence = match[1]
            lines.append("\n" if line.endswith("\n") else "")
        else:
            lines.append(line)
    return CODE_PATTERN.sub(
        lambda match: re.sub(r"[^\n]", " ", match[0]), "".join(lines)
    )


def _local_links(
    root: Path, source: Path, text: str, paths: set[Path], errors: list[str]
) -> set[Path]:
    linked: set[Path] = set()
    if source.name == "SKILL.md":
        text = FRONTMATTER_PATTERN.sub(
            lambda match: "\n" * match[0].count("\n"), text, count=1
        )
    text = _prose(text)
    for match in MARKDOWN_LINK_PATTERN.finditer(text):
        line_number = text.count("\n", 0, match.start()) + 1
        location = f"{source}:{line_number}"
        target = match["target"]
        if target is None:
            errors.append(
                f"{location}: reference-style links are unsupported; use [text](path)"
            )
            continue
        try:
            parts = urlsplit(target.lstrip().removeprefix("<"))
        except ValueError as exc:
            errors.append(f"{location}: invalid link {target!r}: {exc}")
            continue
        if parts.scheme == "file":
            errors.append(f"{location}: file URI is not a distributable link: {target}")
            continue
        if parts.scheme or parts.netloc:
            continue
        if (
            not match["close"]
            or not target
            or UNSUPPORTED_TARGET_PATTERN.search(target)
            or "%" in parts.path
            or parts.query
        ):
            errors.append(
                f"{location}: unsupported local link; use [text](path) with a plain path and optional #fragment"
            )
            continue
        if not parts.path:
            continue
        path = Path(
            os.path.normpath(
                root / parts.path.lstrip("/")
                if parts.path.startswith("/")
                else source.parent / parts.path
            )
        )
        if path != root / "AGENTS.md" and not path.is_relative_to(root / ".agents"):
            errors.append(f"{location}: path escapes the distributed payload: {target}")
        elif path not in paths:
            errors.append(f"{location}: missing local target: {target}")
        else:
            linked.add(path)
    return linked


def _validate_metadata(path: Path, text: str, errors: list[str]) -> None:
    match = FRONTMATTER_PATTERN.match(text)
    if match is None:
        errors.append(f"{path}: missing YAML frontmatter")
        return
    try:
        metadata = yaml.safe_load(match.group(1))
    except yaml.YAMLError as exc:
        errors.append(f"{path}: invalid YAML frontmatter: {exc}")
        return
    if not isinstance(metadata, dict):
        errors.append(f"{path}: frontmatter must be an object")
        return
    if set(metadata) != {"name", "description"}:
        errors.append(
            f"{path}: frontmatter fields must be exactly name and description"
        )
    name = metadata.get("name")
    if (
        not isinstance(name, str)
        or len(name) > 64
        or not SKILL_NAME_PATTERN.fullmatch(name)
    ):
        errors.append(f"{path}: invalid skill name")
    elif name != path.parent.name:
        errors.append(f"{path}: skill name must match its directory")
    description = metadata.get("description")
    if (
        not isinstance(description, str)
        or not description.strip()
        or len(description) > 1024
    ):
        errors.append(
            f"{path}: description must be a non-empty string of at most 1024 characters"
        )


def check_repository(root: Path) -> list[str]:
    root = root.resolve()
    errors: list[str] = []
    paths = _payload_paths(root, errors)
    documents: dict[Path, str] = {}
    for path in sorted(paths):
        if path.is_file() and path.suffix == ".md":
            text = _read_text(path, errors)
            if text is not None:
                documents[path] = text
    entrypoint = root / "AGENTS.md"
    if not documents.get(entrypoint, "").strip():
        errors.append(f"{entrypoint}: entrypoint must be a non-empty Markdown file")

    agents = root / ".agents"
    rules = agents / "rules"
    skills = agents / "skills"
    for directory in (agents, rules, skills):
        if directory not in paths or not directory.is_dir():
            errors.append(f"{directory}: required directory is missing")
    for path in sorted(path for path in paths if path.parent == agents):
        if path not in (rules, skills):
            errors.append(f"{path}: payload entries must belong to rules or skills")

    links = {
        path: _local_links(root, path, text, paths, errors)
        for path, text in documents.items()
    }
    index = rules / "index.md"
    if not documents.get(index, "").strip():
        errors.append(f"{index}: index must be a non-empty Markdown file")
    routed: set[Path] = set()
    for path in sorted(path for path in paths if path.parent == rules):
        if not path.is_file() or path.suffix != ".md":
            errors.append(f"{path}: rules must be flat Markdown files")
        elif path != index:
            routed.add(path)

    for directory in sorted(path for path in paths if path.parent == skills):
        if not directory.is_dir():
            errors.append(f"{directory}: expected a skill directory")
            continue
        skill = directory / "SKILL.md"
        if skill not in documents:
            errors.append(f"{skill}: readable SKILL.md is required")
            continue
        _validate_metadata(skill, documents[skill], errors)
        routed.add(skill)
        for path in sorted(path for path in paths if path.parent == directory):
            if path != skill and (path.name not in RESOURCE_DIRS or not path.is_dir()):
                errors.append(f"{path}: unsupported skill-local entry")
        for path in sorted(path for path in paths if path.is_relative_to(directory)):
            if path.is_file() and path != skill and path not in links.get(skill, set()):
                errors.append(
                    f"{path}: orphan skill resource; link it directly from {skill}"
                )
    for missing in sorted(routed - links.get(index, set())):
        errors.append(f"{index}: missing routed Markdown link: {missing}")
    return errors


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--root",
        type=Path,
        default=Path.cwd(),
        help="repository root (default: current directory)",
    )
    args = parser.parse_args(argv)
    errors = check_repository(args.root)
    if errors:
        for error in errors:
            print(f"error: {error}", file=sys.stderr)
        print(f"agent rules check failed with {len(errors)} error(s)", file=sys.stderr)
        return 1
    print("agent rules check passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
