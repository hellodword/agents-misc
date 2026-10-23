#!/usr/bin/env python3
"""Retain notices for the exact vendored Go dependency graph in the package."""

import pathlib
import shutil
import sys

source, destination = map(pathlib.Path, sys.argv[1:])
destination.mkdir(parents=True, exist_ok=True)
shutil.copyfile(source / "modules.txt", destination / "modules.txt")
for file in source.rglob("*"):
    if file.is_file() and file.name.lower().startswith(
        ("license", "copying", "notice")
    ):
        target = destination / file.relative_to(source)
        target.parent.mkdir(parents=True, exist_ok=True)
        shutil.copyfile(file, target)
