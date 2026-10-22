"""Locate native input dependencies within the configured host installation."""

import shutil
from pathlib import Path

from common import Failure


def library_directory(settings):
    configured = settings.get("vbox_library_dir", "")
    if configured:
        candidates = [Path(configured)]
    else:
        executable = shutil.which(settings.get("vboxmanage", "VBoxManage"))
        if not executable:
            raise Failure("VBoxManage is unavailable", "capability_unavailable")
        directory = Path(executable).resolve().parent
        candidates = [directory] + [
            directory.parent / location / "virtualbox"
            for location in ("libexec", "lib", "lib64")
        ]
    for directory in candidates:
        if (directory / "VBoxXPCOMC.so").is_file():
            return directory.resolve()
    raise Failure(
        "Cannot locate VBoxXPCOMC.so beside VBoxManage; set service.vbox_library_dir",
        "capability_unavailable",
    )


def sdk_directory(settings):
    library = library_directory(settings)
    candidates = [
        library / "sdk",
        library.parent / "sdk",
        library.parent.parent / "sdk",
        library.parent.parent / "share" / "virtualbox" / "sdk",
    ]
    for directory in candidates:
        if (directory / "bindings" / "c" / "glue" / "VBoxCAPIGlue.c").is_file():
            return directory.resolve()
    raise Failure("Cannot locate the installed C SDK; pass its root as SDK_DIR")
