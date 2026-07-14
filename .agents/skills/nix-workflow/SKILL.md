---
name: nix-workflow
description: Use this when adding, restructuring, or validating Nix, Just, flake outputs, treefmt-nix, dev shells, pure Nix products, or reproducible project commands.
---

# Nix Workflow

## Purpose

Work through an already adopted or explicitly requested Nix-backed command workflow without introducing Nix as an unrelated side effect.

## Workflow

1. Classify the repository as ordinary application, pure Nix, or pure patch.
2. Identify whether the task touches environment, command menu, flake output, formatter, script, or validation behavior.
3. Read `.agents/references/nixpkgs-devcontainer-alignment.md` before initializing or updating `nixpkgs`.
4. Read `.agents/references/nix-layout.md` when creating or reorganizing flake outputs, `./nix/`, scripts, checks, or formatter layout.
5. Keep ordinary commands in thin documented just recipes.
6. Keep complex command logic in checked-in scripts.
7. Keep `flake.nix` as the assembly entry point. Move logic into `./nix/` when more than one output/module consumes it or when keeping it inline obscures the public output wiring.
8. Expose stable validations through `checks` when they are durable enough for `nix flake check`.
9. Expose formatting through `formatter`, preferably backed by `treefmt-nix` for multi-language projects.
10. Run the narrowest validation for the output or command touched.

## Ordinary application defaults

- Nix is the reproducible command environment.
- Just is the human-friendly command menu.
- Use `nix develop .#<env> --command ...` for project tools.
- Do not use a `path:` flake reference to bypass Git source filtering. For a verified durable, non-secret, non-temporary, non-ignored file required by the flake, run only `git add -N -- <file>`, leave intent-to-add in place, and report it. Legitimate non-Git path flakes remain allowed.

## Pure Nix defaults

- Flake outputs are the product interface.
- A `justfile` is not required.
- Validate changed packages, apps, shells, modules, templates, or overlays directly.
- Run `nix flake show` when the public flake interface changes and `nix flake check` when checks or broad flake wiring changes.

## Report

Include:

- repository classification;
- output, shell, recipe, script, or formatter changed;
- nixpkgs source decision when relevant;
- command run;
- validation result;
- limitations.
