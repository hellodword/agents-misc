"""Build the native input helper against the installed or selected official SDK."""

import argparse
import os
import platform
import shutil
import subprocess
import sys
from pathlib import Path

from common import Failure, read_config
from native_paths import sdk_directory


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "sdk_dir", nargs="?", default="", help="Official SDK root containing bindings/c"
    )
    parser.add_argument("--config", type=Path, default=Path("config.toml"))
    parser.add_argument("--test", action="store_true", help="Run native ABI tests")
    parser.add_argument(
        "--sanitize",
        action="store_true",
        help="Enable address and undefined-behavior checks",
    )
    args = parser.parse_args()
    try:
        if platform.system() != "Linux" or platform.machine() not in {
            "x86_64",
            "amd64",
        }:
            raise ValueError("The input helper build targets Linux amd64")
        selected = (
            Path(args.sdk_dir).resolve()
            if args.sdk_dir
            else sdk_directory(read_config(args.config)["service"])
        )
        sdk = selected / "bindings" / "c"
        glue = sdk / "glue" / "VBoxCAPIGlue.c"
        if not glue.is_file() or not (sdk / "glue" / "VBoxCAPIGlue.h").is_file():
            raise ValueError("SDK must include the official C headers and glue")
        root = Path(__file__).resolve().parent
        output = root / "build"
        output.mkdir(exist_ok=True)
        name = "vbox-input-helper" + ("-sanitize" if args.sanitize else "")
        compiler = shutil.which("cc")
        if not compiler:
            raise ValueError("A C compiler is required")
        flags = [
            "-std=c11",
            "-D_POSIX_C_SOURCE=200809L",
            "-Wall",
            "-Wextra",
            "-Werror",
            "-O2",
        ]
        if args.sanitize:
            flags += ["-g", "-fsanitize=address,undefined", "-fno-omit-frame-pointer"]
        subprocess.run(
            [
                compiler,
                *flags,
                "-I",
                str(sdk / "include"),
                "-I",
                str(sdk / "glue"),
                str(root / "native_input.c"),
                str(glue),
                "-ldl",
                "-o",
                str(output / name),
            ],
            check=True,
        )
        print(output / name)
        if args.test:
            subprocess.run(
                [sys.executable, "-B", "-m", "unittest", "-v", "test_native"],
                cwd=root,
                env={
                    **os.environ,
                    "VM_TEST_SDK_DIR": str(selected),
                    "VM_TEST_NATIVE_SANITIZE": "1" if args.sanitize else "",
                },
                check=True,
            )
        return 0
    except (OSError, ValueError, Failure, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(main())
