---
name: pure-nix-project
description: Use this when the project core is Nix itself, such as package sets, overlays, NixOS modules, Home Manager modules, flake templates, Nix libraries, or Nix-based environment distributions. Do not use merely because an ordinary project has a flake.nix.
---

# Pure Nix Project

## Purpose

Design, modify, and validate projects whose primary product is expressed through Nix and flake outputs.

## When to use

Use this skill when the user explicitly says the project is pure Nix or the repository clearly shows that the core product is one or more of:

- Nix packages;
- overlays;
- NixOS modules;
- Home Manager modules;
- flake templates;
- Nix libraries;
- Nix-based environment distributions.

Do not use this skill only because the repository has `flake.nix`.

For ordinary Go, Rust, Node.js, Python, Flutter, or frontend projects that merely use Nix as a reproducible toolchain, use `nix-just-workflow` instead.

## Principles

- Flake outputs are the primary public interface.
- A `justfile` is not required.
- `flake.nix` should stay as the assembly entry point.
- Reusable Nix logic should live under `./nix/`.
- Product Nix logic may live under `./nix/`.
- Imperative orchestration should live in scripts, not in `flake.nix`.
- Stable validations should be exposed through `checks`.
- Formatting should be exposed through `formatter` when practical.

## Workflow

1. Verify the project is pure Nix.
2. Identify the public flake outputs the project should expose:
   - `packages`;
   - `apps`;
   - `devShells`;
   - `checks`;
   - `formatter`;
   - `templates`;
   - `overlays`;
   - `nixosModules`;
   - `homeManagerModules`.
3. Inspect existing `flake.nix`, `flake.lock`, and `./nix/`.
4. Before initializing or updating `nixpkgs`, read `.agents/references/nixpkgs-devcontainer-alignment.md` and align to the devcontainer revision when available and verified.
5. Keep `flake.nix` thin.
6. Move reusable Nix logic into focused files under `./nix/`.
7. Add or update checks for exported outputs.
8. Add or update formatter output when useful.
9. Avoid adding `justfile` unless the user explicitly wants one.
10. If a justfile exists, keep recipes documented and flake-native.
11. Validate with `nix flake show`.
12. Validate with `nix flake check`.
13. Validate formatting with `nix fmt` when `formatter` exists.

## Validation

Minimum validation:

    nix flake show
    nix flake check

When formatter exists:

    nix fmt

When specific outputs changed, also validate them directly:

    nix build .#<package>
    nix run .#<app>
    nix develop .#<shell> --command <command>

If newly created files are hidden by Git flake source tracking, use:

    nix develop path:$PWD#dev --command <command> ...

## Output

Report:

- why the project is classified as pure Nix;
- flake outputs changed;
- nixpkgs source decision;
- whether `flake.lock` records the intended nixpkgs revision when it changed;
- files under `./nix/` changed;
- whether `justfile` is absent by design or present as optional convenience;
- validation commands run;
- validation limitations;
- proposed commit message when commit policy requires or allows a commit.
