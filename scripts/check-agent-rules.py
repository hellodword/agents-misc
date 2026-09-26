#!/usr/bin/env python3
"""Validate the structure and local references of the distributed Agent Rules Kit."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path
from urllib.parse import unquote, urlsplit

import yaml


SKILL_NAME_PATTERN = re.compile(r"^[a-z0-9]+(?:-[a-z0-9]+)*$")
MARKDOWN_LINK_PATTERN = re.compile(r"!?\[[^\]]*\]\((<[^>]*>|[^)\n]*)\)")
FENCED_BLOCK_PATTERN = re.compile(
    r"^\s*(```|~~~).*?^\s*\1\s*$", re.MULTILINE | re.DOTALL
)
FRONTMATTER_PATTERN = re.compile(r"\A---\r?\n(.*?)\r?\n---(?:\r?\n|\Z)", re.DOTALL)
RESOURCE_DIRS = {"assets", "references", "scripts"}


def _contained_path(root: Path, path: Path, errors: list[str]) -> Path | None:
    try:
        resolved = path.resolve(strict=True)
    except (OSError, RuntimeError, ValueError) as exc:
        errors.append(f"{path}: cannot resolve payload path: {exc}")
        return None
    if resolved != root / "AGENTS.md" and not resolved.is_relative_to(root / ".agents"):
        errors.append(f"{path}: path escapes the distributed payload")
        return None
    return resolved


def _payload_paths(root: Path, errors: list[str]) -> set[Path]:
    paths: set[Path] = set()

    def visit(path: Path, parents: frozenset[Path]) -> None:
        resolved = _contained_path(root, path, errors)
        if resolved is None:
            return
        paths.add(path)
        if path.is_dir():
            if resolved in parents:
                errors.append(f"{path}: directory symlink cycle")
                return
            try:
                children = sorted(path.iterdir())
            except OSError as exc:
                errors.append(f"{path}: cannot list directory: {exc}")
                return
            for child in children:
                visit(child, parents | {resolved})
        elif not path.is_file():
            errors.append(f"{path}: expected a file or directory")

    visit(root / "AGENTS.md", frozenset())
    visit(root / ".agents", frozenset())
    return paths


def _read_text(path: Path, errors: list[str]) -> str | None:
    try:
        return path.read_text(encoding="utf-8")
    except (OSError, UnicodeDecodeError) as exc:
        errors.append(f"{path}: must be readable UTF-8: {exc}")
        return None


def _local_links(root: Path, source: Path, text: str, errors: list[str]) -> set[Path]:
    linked: set[Path] = set()
    for match in MARKDOWN_LINK_PATTERN.finditer(FENCED_BLOCK_PATTERN.sub("", text)):
        target = match.group(1).strip()
        if target.startswith("<") and ">" in target:
            target = target[1 : target.index(">")]
        else:
            target = target.split(maxsplit=1)[0] if target else ""
        try:
            parts = urlsplit(target)
        except ValueError as exc:
            errors.append(f"{source}: invalid Markdown link {target!r}: {exc}")
            continue
        if parts.scheme == "file":
            errors.append(f"{source}: file URI is not a distributable link: {target}")
            continue
        if parts.scheme or parts.netloc or not parts.path:
            continue
        path_text = unquote(parts.path)
        path = (
            root / path_text.lstrip("/")
            if path_text.startswith("/")
            else source.parent / path_text
        )
        link_errors: list[str] = []
        resolved = _contained_path(root, path, link_errors)
        errors.extend(f"{source}: {error}" for error in link_errors)
        if resolved is not None:
            linked.add(resolved)
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
        path: _local_links(root, path, text, errors) for path, text in documents.items()
    }
    index = rules / "index.md"
    if not documents.get(index, "").strip():
        errors.append(f"{index}: index must be a non-empty Markdown file")
    routed: set[Path] = set()
    for path in sorted(path for path in paths if path.parent == rules):
        if not path.is_file() or path.suffix != ".md":
            errors.append(f"{path}: rules must be flat Markdown files")
        elif path != index:
            routed.add(path.resolve())

    for directory in sorted(path for path in paths if path.parent == skills):
        if not directory.is_dir():
            errors.append(f"{directory}: expected a skill directory")
            continue
        skill = directory / "SKILL.md"
        if skill not in documents:
            errors.append(f"{skill}: readable SKILL.md is required")
            continue
        _validate_metadata(skill, documents[skill], errors)
        routed.add(skill.resolve())
        for path in sorted(path for path in paths if path.parent == directory):
            if path != skill and (path.name not in RESOURCE_DIRS or not path.is_dir()):
                errors.append(f"{path}: unsupported skill-local entry")
        for path in sorted(path for path in paths if path.is_relative_to(directory)):
            if (
                path.is_file()
                and path != skill
                and path.resolve() not in links.get(skill, set())
            ):
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
