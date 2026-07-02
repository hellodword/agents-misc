---
name: nix-just-workflow
description: Use this when adding or running build, test, lint, format, codegen, migration, package, or pure patch commands through Nix and Just.
---

# Nix and Just Workflow

## Purpose

Run project commands reproducibly while keeping Just as a thin convenience layer.

## Workflow

1. Classify the command:
   - bootstrap command;
   - project command;
   - environment capability command.
2. Run bootstrap commands directly when needed.
3. Run project commands through `nix develop .#<env> --command ...`.
4. Use `nix develop path:$PWD#<env> --command ...` when flake source tracking hides new files.
5. Put stable repeated commands in `justfile`.
6. Keep just recipes thin.
7. Move complex logic into checked-in scripts.
8. If a project tool is missing and edits are allowed, update `flake.nix`.
9. If edits are not allowed, report the missing package.
10. Do not install global tools.

## Validation

Report:

- command class;
- command used;
- Nix shell used;
- missing package changes;
- any environment capability discovered outside Nix.
