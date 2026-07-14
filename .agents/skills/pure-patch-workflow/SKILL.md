---
name: pure-patch-workflow
description: Use this when maintaining patches against an upstream project without committing the patched upstream source tree.
---

# Pure Patch Workflow

## Purpose

Maintain reproducible patch sets against upstream revisions.

## Workflow

1. Identify upstream name, URL, and exact revision or tag.
2. Record upstream metadata in `<upstream>/upstream.yaml`.
3. Fetch only the needed upstream source into `.work/<upstream>/<rev>/src`: use shallow Git when Git metadata is needed, otherwise use a recorded immutable archive.
4. Do not commit `.work/`.
5. Create or update the Nix dev shell for the patch workspace.
6. Before initializing or updating `nixpkgs`, read `.agents/references/nixpkgs-devcontainer-alignment.md`; keep `flake.nix` on `github:NixOS/nixpkgs/nixos-unstable` and use `--override-input` only through `nix flake update`.
7. Add just recipes that call Nix and then upstream-native commands.
8. Keep patches under `<upstream>/patches/<rev>/`.
9. Keep a `series` file for patch order.
10. Default build jobs to `max(1, nproc - 1)`; lower only for an upstream/project limit or after an observed resource/stability failure.
11. Avoid deleting build caches or triggering full rebuilds while iterating patches.
12. When upgrading upstream, create a new patch directory and preserve the old one.

## Validation

Report:

- upstream revision;
- worktree path;
- patch directory;
- series file;
- build/test command;
- nixpkgs source decision;
- jobs limit;
- cache status;
- validation limitations.
