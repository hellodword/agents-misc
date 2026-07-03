---
name: nix-just-workflow
description: Use this when adding or running build, test, lint, format, codegen, migration, package, flake output, dev shell, or pure patch commands through Nix and Just.
---

# Nix and Just Workflow

## Purpose

Run project commands reproducibly while keeping Just as a thin, documented convenience layer.

## When to use

Use this skill when working on:

- `flake.nix`;
- files under `nix/`;
- `justfile`;
- Nix packages;
- Nix apps;
- Nix devShells;
- Nix checks;
- Nix formatter output;
- project build/test/lint/format/codegen commands;
- pure patch apply/refresh/build commands.

## Workflow

1. Classify the command:
   - bootstrap command;
   - project command;
   - environment capability command.
2. Run bootstrap commands directly when needed.
3. Before initializing or updating `nixpkgs`, read `.agents/references/nixpkgs-devcontainer-alignment.md`.
4. Use the devcontainer nixpkgs revision when it is available and verified; otherwise use `github:NixOS/nixpkgs/nixos-unstable`.
5. Run project commands through `nix develop .#<env> --command ...`.
6. Use `nix develop path:$PWD#<env> --command ...` when flake source tracking hides new files.
7. Put stable repeated commands in `justfile`.
8. Add a short documentation comment above every durable just recipe.
9. Keep just recipes thin.
10. Move complex logic into checked-in scripts.
11. Keep `flake.nix` thin and move reusable Nix output definitions into `./nix/`.
12. If a project tool is missing and edits are allowed, update `flake.nix`.
13. If edits are not allowed, report the missing package.
14. Do not install global tools.

## Flake organization

Use this default structure when it is useful:

    flake.nix
    nix/
      lib.nix
      packages.nix
      apps.nix
      dev-shells.nix
      checks.nix
      formatter.nix
    scripts/

Do not create empty files just to match the structure.

Output responsibilities:

- `packages`: buildable artifacts.
- `apps`: runnable wrappers for `nix run`.
- `devShells`: development environments.
- `checks`: stable `nix flake check` validations.
- `formatter`: stable `nix fmt` formatter when useful.

## Validation

Report:

- command class;
- command used;
- Nix shell used;
- nixpkgs source decision;
- whether devcontainer nixpkgs rev was found;
- whether `flake.lock` records the intended rev;
- just recipes added or changed;
- missing package changes;
- any environment capability discovered outside Nix.
