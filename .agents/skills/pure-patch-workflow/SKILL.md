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
3. Fetch only the needed upstream source into `.work/<upstream>/<rev>/src`.
4. Do not commit `.work/`.
5. Create or update the Nix dev shell for the patch workspace.
6. Before initializing or updating `nixpkgs`, read `.agents/references/nixpkgs-devcontainer-alignment.md`.
7. Add just recipes that call Nix and then upstream-native commands.
8. Keep patches under `<upstream>/patches/<rev>/`.
9. Keep a `series` file for patch order.
10. Use build jobs no higher than `max(1, nproc - 1)`.
11. If the first full build is very expensive, provide the exact command and ask the user to run it manually.
12. Avoid deleting build caches or triggering full rebuilds while iterating patches.
13. When upgrading upstream, create a new patch directory and preserve the old one.

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
