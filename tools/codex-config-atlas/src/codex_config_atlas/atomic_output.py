from __future__ import annotations

import os
import shutil
import tempfile
import uuid
import warnings
from collections.abc import Callable
from pathlib import Path


def replace_directory(candidate: Path, target: Path) -> Path | None:
    backup: Path | None = None
    if target.exists():
        backup = target.parent / f".{target.name}-backup-{uuid.uuid4().hex}"
        os.replace(target, backup)

    try:
        os.replace(candidate, target)
    except BaseException:
        if backup is not None and backup.exists():
            try:
                os.replace(backup, target)
            except BaseException as restore_error:
                raise RuntimeError(
                    f"failed to install candidate and restore output; recover {backup} to {target}"
                ) from restore_error
        raise

    if backup is None:
        return None
    try:
        shutil.rmtree(backup)
    except OSError:
        return backup
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
    try:
        populate(candidate)
        backup = replace_directory(candidate, target)
        if backup is not None:
            warnings.warn(
                f"generated output installed but old backup remains at {backup}",
                RuntimeWarning,
                stacklevel=2,
            )
        return backup
    finally:
        if candidate.exists():
            shutil.rmtree(candidate)
