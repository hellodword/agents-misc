from __future__ import annotations

import os
import shutil
import subprocess
import tempfile
import warnings
from collections.abc import Callable
from pathlib import Path


def replace_directory(candidate: Path, target: Path) -> Path | None:
    if not target.exists():
        os.replace(candidate, target)
        return None

    completed = subprocess.run(
        [
            "mv",
            "-T",
            "--exchange",
            "--no-copy",
            "--",
            str(candidate),
            str(target),
        ],
        check=False,
        text=True,
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    if completed.returncode != 0:
        detail = completed.stderr.strip() or completed.stdout.strip() or "no diagnostic"
        raise RuntimeError(
            f"atomic directory exchange failed for {candidate} and {target}: "
            f"{detail}; both directories were preserved"
        )
    try:
        shutil.rmtree(candidate)
    except OSError:
        return candidate
    return None


def build_directory_atomically(
    target: Path,
    populate: Callable[[Path], None],
) -> Path | None:
    target = target.resolve()
    target.parent.mkdir(parents=True, exist_ok=True)
    candidate = Path(
        tempfile.mkdtemp(prefix=f".{target.name}-generate-", dir=target.parent)
    )
    retained_old: Path | None = None
    try:
        populate(candidate)
        retained_old = replace_directory(candidate, target)
        if retained_old is not None:
            warnings.warn(
                f"generated output installed but old output remains at {retained_old}",
                RuntimeWarning,
                stacklevel=2,
            )
        return retained_old
    finally:
        if candidate.exists() and candidate != retained_old:
            shutil.rmtree(candidate)
