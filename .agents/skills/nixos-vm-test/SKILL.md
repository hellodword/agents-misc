---
name: nixos-vm-test
description: Run and diagnose NixOS QEMU VM tests through a prepared HTTP MCP executor when remote execution is selected or local KVM is unavailable. Evaluate with the project's locked nixpkgs and collect sandbox test results; environment provisioning and ordinary Nix builds have separate workflows.
---

# NixOS VM Tests

1. Identify the project test, locked nixpkgs input, execution system and prepared MCP executor. Preserve a suitable project-local flow. Apply the [VM test conventions](../../rules/vm-tests.md) and read [execution](references/execution.md) for submission and diagnosis.
2. Use `targets` to check the executor and existing tasks. This workflow does not provision the host. For a separately authorized setup task, read [manual preparation](references/preparation.md).
3. Evaluate locally and transfer inputs through a service-issued binary cache. When an entrypoint is useful, copy [the Python input helper](assets/nix-vm-test.py) into the project's scripts directory. It needs Python 3.11+ and Nix with flakes. It retains the project's pinned framework and gives the top-level test a fresh run identity.
4. Submit the derivation as a NixOS task. Nodes run directly on host KVM inside the Nix test build. Follow logs and assertions, then retrieve useful output files. Diagnose by changing the test and submitting a fresh run.
5. For initial acceptance, use [the multi-node test module](assets/vm-smoke.nix) with the project's locked nixpkgs. Confirm actual multi-vCPU guest startup and network assertions, alongside administrator-confirmed daemon sandbox policy.
6. Report the task ID, execution system, assertion result, logs, artifacts and cleanup state. Keep unresolved failures attributable to the test, execution environment or framework.
