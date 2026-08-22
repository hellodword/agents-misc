#!/usr/bin/env python3
"""Validate repository-owned GitHub workflow structure and release gates."""

from __future__ import annotations

import argparse
import re
import sys
from pathlib import Path
from typing import Any

import yaml


WORKFLOWS = {
    "build-codex.yml": (("build", "verify"),),
    "build-agents-viewer.yml": (("build", "verify"),),
    "publish-codex-config-atlas-pages.yml": (
        ("build", "verify"),
        ("deploy", "verify"),
    ),
}
VIEWER_RUNNERS = {
    "ubuntu-24.04",
    "ubuntu-24.04-arm",
    "windows-2025",
    "windows-11-arm",
    "macos-15",
}
LOCAL_NIX_ACTION = "./.github/actions/setup-nix"
OFFICIAL_ACTION = re.compile(
    r"^(?:actions/[a-z0-9-]+|github/codeql-action/[a-z0-9-]+)@v[1-9][0-9]*$"
)


def _load_yaml(path: Path, errors: list[str]) -> dict[str, Any] | None:
    try:
        value = yaml.safe_load(path.read_text(encoding="utf-8"))
    except (OSError, UnicodeDecodeError, yaml.YAMLError) as exc:
        errors.append(f"{path}: invalid workflow YAML: {exc}")
        return None
    if not isinstance(value, dict):
        errors.append(f"{path}: workflow must be a mapping")
        return None
    return value


def _needs(job: dict[str, Any]) -> list[str]:
    value = job.get("needs", [])
    if isinstance(value, str):
        return [value]
    if isinstance(value, list) and all(isinstance(item, str) for item in value):
        return value
    return []


def _has_ancestor(
    jobs: dict[str, Any], start: str, target: str, seen: set[str] | None = None
) -> bool:
    if start == target:
        return True
    visited = set() if seen is None else seen
    if start in visited:
        return False
    visited.add(start)
    job = jobs.get(start)
    if not isinstance(job, dict):
        return False
    return any(_has_ancestor(jobs, parent, target, visited) for parent in _needs(job))


def _steps(job: dict[str, Any]) -> list[dict[str, Any]]:
    value = job.get("steps", [])
    if not isinstance(value, list):
        return []
    return [step for step in value if isinstance(step, dict)]


def _check_action_refs(path: Path, jobs: dict[str, Any], errors: list[str]) -> None:
    for job_name, job in jobs.items():
        if not isinstance(job, dict):
            errors.append(f"{path}: job {job_name!r} must be a mapping")
            continue
        for step in _steps(job):
            uses = step.get("uses")
            if not isinstance(uses, str):
                continue
            if uses == LOCAL_NIX_ACTION:
                continue
            if not OFFICIAL_ACTION.fullmatch(uses):
                errors.append(
                    f"{path}: job {job_name!r} action {uses!r} must use an allowed official major tag"
                )


def _check_nix_setup(path: Path, jobs: dict[str, Any], errors: list[str]) -> None:
    for job_name, job in jobs.items():
        if not isinstance(job, dict):
            continue
        steps = _steps(job)
        nix_runs = [
            index
            for index, step in enumerate(steps)
            if isinstance(step.get("run"), str)
            and re.search(r"(^|\s)nix(?:\s|$)", step["run"])
        ]
        setup = [
            index
            for index, step in enumerate(steps)
            if step.get("uses") == LOCAL_NIX_ACTION
        ]
        if nix_runs and (not setup or min(setup) > min(nix_runs)):
            errors.append(
                f"{path}: job {job_name!r} must use {LOCAL_NIX_ACTION} before Nix commands"
            )
        if any("nixos.org/nix/install" in str(step.get("run", "")) for step in steps):
            errors.append(
                f"{path}: job {job_name!r} duplicates the Nix installer inline"
            )


def _check_viewer_matrix(path: Path, jobs: dict[str, Any], errors: list[str]) -> None:
    build = jobs.get("build")
    if not isinstance(build, dict):
        return
    strategy = build.get("strategy", {})
    matrix = strategy.get("matrix", {}) if isinstance(strategy, dict) else {}
    include = matrix.get("include", []) if isinstance(matrix, dict) else []
    runners = {
        item.get("runner")
        for item in include
        if isinstance(item, dict) and isinstance(item.get("runner"), str)
    }
    if runners != VIEWER_RUNNERS:
        errors.append(
            f"{path}: native Viewer runner matrix mismatch: expected={sorted(VIEWER_RUNNERS)}, actual={sorted(runners)}"
        )


def _check_composite_action(root: Path, errors: list[str]) -> None:
    path = root / ".github" / "actions" / "setup-nix" / "action.yml"
    value = _load_yaml(path, errors)
    if value is None:
        return
    runs = value.get("runs")
    if not isinstance(runs, dict) or runs.get("using") != "composite":
        errors.append(f"{path}: setup-nix must be a composite action")
        return
    text = path.read_text(encoding="utf-8")
    for guard in (
        "runner.environment == 'github-hosted'",
        "GITHUB_ACTIONS:-",
        "lsb_release -is",
    ):
        if guard not in text:
            errors.append(f"{path}: missing GitHub-hosted Ubuntu guard {guard!r}")


def check_repository(root: Path) -> list[str]:
    errors: list[str] = []
    workflow_dir = root / ".github" / "workflows"
    for filename, dependencies in WORKFLOWS.items():
        path = workflow_dir / filename
        value = _load_yaml(path, errors)
        if value is None:
            continue
        jobs = value.get("jobs")
        if not isinstance(jobs, dict):
            errors.append(f"{path}: jobs must be a mapping")
            continue
        for job_name, required_ancestor in dependencies:
            if not _has_ancestor(jobs, job_name, required_ancestor):
                errors.append(
                    f"{path}: job {job_name!r} must depend on verification job {required_ancestor!r}"
                )
        _check_action_refs(path, jobs, errors)
        _check_nix_setup(path, jobs, errors)
        if filename == "build-agents-viewer.yml":
            _check_viewer_matrix(path, jobs, errors)
    _check_composite_action(root, errors)
    return errors


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    errors = check_repository(args.root.resolve())
    for error in errors:
        print(f"error: {error}", file=sys.stderr)
    if errors:
        return 1
    print("GitHub workflow checks passed")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
