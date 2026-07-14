from __future__ import annotations

import shutil
from pathlib import Path


def build_site(static_dir: Path, data_dir: Path, out_dir: Path) -> None:
    out_dir.mkdir(parents=True, exist_ok=True)

    for item in static_dir.iterdir():
        target = out_dir / item.name
        if item.is_dir():
            if target.exists():
                shutil.rmtree(target)
            shutil.copytree(item, target)
        else:
            shutil.copy2(item, target)

    data_target = out_dir / "data"
    if data_target.exists():
        shutil.rmtree(data_target)
    shutil.copytree(data_dir, data_target)
    (out_dir / ".nojekyll").write_text("")
