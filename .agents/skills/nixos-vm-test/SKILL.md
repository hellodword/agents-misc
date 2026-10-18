---
name: nixos-vm-test
description: Run and debug NixOS QEMU VM tests on a prepared SSH executor when the project selects remote execution or local KVM is unavailable. Check prerequisites and use password or private-key authentication; exclude executor provisioning, ordinary Nix builds, and systemd-nspawn tests.
---

# Remote NixOS VM Tests

1. Identify the selected test, its locked nixpkgs input, execution system, and authorized SSH target. Preserve an existing suitable project entrypoint. Apply the Nix rule's remote execution policy; an unavailable executor is a reported prerequisite, not permission to provision one.
2. Read [the execution reference](references/execution.md) for connection inputs, mode semantics, preflight limits, and result retrieval. Confirm the test uses QEMU nodes and forces hardware acceleration before running it.
3. When a project needs an entrypoint, copy [the Python script template](assets/nix-vm-test.py) into its `scripts/` directory and adapt only the necessary project interfaces. Supply Python 3, OpenSSH, Nix, and, for password authentication, `sshpass` through the project environment. Keep any Just recipe thin. Adding this entrypoint does not by itself adopt new flake product outputs.
4. Run preflight, then the appropriate test mode. For initial executor acceptance, use [the smoke test module](assets/vm-smoke.nix) with the project's locked nixpkgs in an authorized check or isolated test fixture. Give the acceptance test a fresh name and retain evidence that it actually ran.
5. Report the selected executor, mode, exit status, execution or cache evidence, and remote result/log locations. Distinguish SSH-user configuration, administrator-confirmed daemon policy, and demonstrated VM execution. Preserve useful failure artifacts and identify any cleanup that could not complete.
