#!/usr/bin/env python3
"""Evaluate a fresh NixOS test locally and upload its derivation closure."""

import argparse
import json
import re
import subprocess
import sys
from urllib.parse import urlsplit


def prepare(test, run_id, cache_url):
    if not re.fullmatch(r"[a-zA-Z0-9][a-zA-Z0-9_-]{0,79}", run_id):
        raise ValueError(
            "run-id must contain 1..80 letters, digits, underscores or hyphens"
        )
    url = urlsplit(cache_url)
    if (
        url.scheme not in {"http", "https"}
        or not url.netloc
        or url.query
        or url.fragment
    ):
        raise ValueError("Use the cache URL returned by transfer_create")
    expression = (
        "t: let fresh = t.overrideTestDerivation (old: { name = old.name + "
        + json.dumps("-" + run_id)
        + "; }); in { drv = fresh.drvPath; system = t.system; "
        "forceAccel = t.config.qemu.forceAccel or false; "
        "containers = builtins.attrNames (t.config.containers or {}); }"
    )
    evaluated = subprocess.run(
        [
            "nix",
            "eval",
            "--store",
            "auto",
            "--no-update-lock-file",
            "--json",
            "--apply",
            expression,
            test,
        ],
        check=True,
        stdout=subprocess.PIPE,
        text=True,
    )
    metadata = json.loads(evaluated.stdout)
    if metadata.get("forceAccel") is not True or metadata.get("containers") != []:
        raise ValueError(
            "Select a QEMU test exposing config.qemu.forceAccel=true and no nspawn containers"
        )
    subprocess.run(
        [
            "nix",
            "copy",
            "--derivation",
            "--from",
            "auto",
            "--to",
            cache_url,
            metadata["drv"],
        ],
        check=True,
        stdout=sys.stderr,
    )
    return {"request_id": run_id, "drv": metadata["drv"], "system": metadata["system"]}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "test", help="Complete flake test attribute, e.g. .#checks.x86_64-linux.vm"
    )
    parser.add_argument(
        "--run-id",
        required=True,
        help="Fresh task ID; reuse it when retrying the same submission",
    )
    parser.add_argument(
        "--cache-url",
        required=True,
        help="HTTP binary cache allocated by transfer_create(kind=cache)",
    )
    args = parser.parse_args()
    try:
        print(json.dumps(prepare(args.test, args.run_id, args.cache_url)))
    except (OSError, ValueError, subprocess.CalledProcessError) as error:
        print(str(error), file=sys.stderr)
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
