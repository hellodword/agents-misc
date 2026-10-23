#!/usr/bin/env python3
"""Inspect the actual Live ISO without mounting it or starting a VM."""

import ctypes
import pathlib
import subprocess
import sys
import tempfile


def run(*args: str) -> str:
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout


def main() -> None:
    iso, system = sys.argv[1:]
    with tempfile.TemporaryDirectory() as directory:
        root = pathlib.Path(directory)
        run("xorriso", "-osirrox", "on", "-indev", iso, "-extract", "/", directory)
        assert (root / "nix-store.squashfs").is_file(), "missing compressed store"
        assert (root / "EFI/BOOT/BOOTX64.EFI").is_file(), "missing EFI loader"
        grub = (root / "EFI/BOOT/grub.cfg").read_text()
        assert "VMTESTROUTER" in grub and "init=" + system + "/init" in grub
        # The default entry reads squashfs on demand rather than copying to RAM.
        first_boot = next(
            line for line in grub.splitlines() if line.strip().startswith("linux ")
        )
        assert "copytoram" not in first_boot
        listing = run("unsquashfs", "-ll", str(root / "nix-store.squashfs"))
        for name in ("vm-test-router-helper", "VBoxService", "dnsmasq", "nft"):
            assert name in listing, f"missing runtime component: {name}"
        config = pathlib.Path(system) / "etc/systemd/system/vm-test-seed.service"
        assert config.is_file(), "missing seed initialization unit"
        service = (
            pathlib.Path(system) / "etc/systemd/system/virtualbox.service"
        ).read_text()
        assert "vm-test-seed.service" in service
        executable = next(
            line.removeprefix("ExecStart=").split()[0]
            for line in service.splitlines()
            if line.startswith("ExecStart=")
        )
        runpath = run("patchelf", "--print-rpath", executable).strip().split(":")
        for library, symbol in (
            ("libpam.so.0", "pam_start"),
            ("libcrypt.so", "crypt_r"),
        ):
            candidates = [pathlib.Path(directory) / library for directory in runpath]
            found = next((file for file in candidates if file.is_file()), None)
            assert found is not None, f"Guest Control cannot find {library} in RUNPATH"
            assert getattr(ctypes.CDLL(str(found)), symbol), f"missing {symbol}"
        rules = pathlib.Path(system) / "etc/udev/rules.d"
        assert any(
            'SUBSYSTEM=="misc", KERNEL=="vboxguest", TAG+="systemd"' in rule.read_text()
            for rule in rules.glob("*.rules")
        ), "Guest Control device is missing its systemd udev tag"
    print("Live ISO EFI, squashfs, first boot and runtime content checks passed")


if __name__ == "__main__":
    main()
