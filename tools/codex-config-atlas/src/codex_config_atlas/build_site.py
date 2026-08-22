from __future__ import annotations

import shutil
from pathlib import Path

from .atomic_output import build_directory_atomically


def build_site(static_dir: Path, data_dir: Path, out_dir: Path) -> None:
    static_dir = static_dir.resolve()
    data_dir = data_dir.resolve()
    if not static_dir.is_dir():
        raise ValueError(f"static directory does not exist: {static_dir}")
    if not data_dir.is_dir():
        raise ValueError(f"data directory does not exist: {data_dir}")

    def populate(candidate: Path) -> None:
        for item in sorted(static_dir.iterdir(), key=lambda path: path.name):
            target = candidate / item.name
            if item.is_dir():
                shutil.copytree(item, target)
            else:
                shutil.copy2(item, target)

        shutil.copytree(data_dir, candidate / "data")
        (candidate / ".nojekyll").write_text("")

    build_directory_atomically(out_dir, populate)
