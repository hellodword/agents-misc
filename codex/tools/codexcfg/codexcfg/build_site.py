from __future__ import annotations

import shutil
from pathlib import Path


def build_site(static_dir: Path, data_dir: Path, out_dir: Path) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)

    for item in static_dir.iterdir():
        target = out_dir / item.name
        if item.is_dir():
            shutil.copytree(item, target, dirs_exist_ok=True)
        else:
            shutil.copy2(item, target)

    shutil.copytree(data_dir, out_dir / "data", dirs_exist_ok=True)
    (out_dir / ".nojekyll").write_text("")
