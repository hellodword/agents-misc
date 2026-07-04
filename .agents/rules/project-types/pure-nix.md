---
id: project-type.pure-nix
kind: project-type
triggers:
  - 'pure Nix'
  - 'flake outputs'
  - 'NixOS module'
  - 'Home Manager module'
  - 'overlay'
  - 'template'
summary: Apply defaults for projects whose primary product is Nix outputs.
load_with:
  rules:
    - toolchain.flake-organization
  skills:
    - pure-nix-project
  references:
    - nixpkgs-devcontainer-alignment
---

# Pure Nix Project Rules
## Applicability

Use these defaults only for new projects, greenfield scaffolding, or repositories without a clear existing convention.

Prefer coherent local conventions.

## Purpose

Use this rule for projects whose core product is Nix itself, not merely projects that use Nix as a build or development environment entrypoint.

A pure Nix project may provide:

- a Nix package set;
- overlays;
- NixOS modules;
- Home Manager modules;
- flake templates;
- Nix libraries;
- Nix-based development environments;
- Nix-based infrastructure or deployment modules.

Treat a project as pure Nix only when the user explicitly says so or the repository clearly states it.

## Primary interface

For pure Nix projects, flake outputs are the primary public interface.

A `justfile` is not required.

Preferred commands:

    nix flake show
    nix build .#<package>
    nix run .#<app>
    nix develop .#<shell>
    nix flake check
    nix fmt

Use a justfile only when the user explicitly wants one or when it materially improves local command discovery.

If a justfile exists, every durable recipe must have a documentation comment and should forward to flake-native commands.

## Repository layout

Recommended layout:

    .
    ├── flake.nix
    ├── flake.lock
    ├── nix/
    │   ├── lib.nix
    │   ├── packages.nix
    │   ├── apps.nix
    │   ├── dev-shells.nix
    │   ├── checks.nix
    │   ├── formatter.nix
    │   ├── overlays/
    │   ├── modules/
    │   ├── nixos-modules/
    │   ├── home-manager-modules/
    │   └── templates/
    ├── tests/
    └── scripts/

Use only the files and directories that are needed.

Do not create empty structure just for symmetry.

## `flake.nix`

Keep `flake.nix` as the assembly entry point.

It should contain:

- description;
- inputs;
- supported systems;
- output assembly;
- imports from `./nix/`.

It should avoid:

- long imperative scripts;
- stateful orchestration;
- large unrelated derivation bodies;
- duplicated per-system logic;
- machine-specific paths.

## `./nix/`

Put reusable Nix logic under `./nix/`.

Pure Nix product logic may live under `./nix/`, including modules, overlays, templates, package definitions, and library functions.

Keep files focused and composable.

Prefer small module interfaces over passing the entire flake scope everywhere.

## Outputs

Expose relevant outputs explicitly.

Common pure Nix outputs:

- `packages.${system}.default`
- `packages.${system}.<name>`
- `apps.${system}.default`
- `apps.${system}.<name>`
- `devShells.${system}.dev`
- `devShells.${system}.<name>`
- `checks.${system}.<name>`
- `formatter.${system}`
- `templates.<name>`
- `overlays.<name>`
- `nixosModules.<name>`
- `homeManagerModules.<name>`

Do not expose outputs that are unstable, experimental, or undocumented unless their experimental status is clear.

## nixpkgs input

Default `nixpkgs` input in `flake.nix`:

    github:NixOS/nixpkgs/nixos-unstable

Before initializing or updating `nixpkgs`, read `.agents/references/nixpkgs-devcontainer-alignment.md`.

Use a devcontainer revision by running `nix flake update nixpkgs --override-input nixpkgs "github:NixOS/nixpkgs/<rev>"` when it is available and verified. Do not rewrite `flake.nix` to `github:NixOS/nixpkgs/<rev>` unless the project already uses revision URLs there.

## Formatting

Pure Nix projects should normally expose:

    formatter.${system}

The formatter should support:

    nix fmt

Follow existing repository formatter conventions when present.

## Checks

Pure Nix projects should prefer `nix flake check` as the main stable validation.

Good checks include:

- package builds;
- module evaluation;
- overlay evaluation;
- template evaluation;
- formatting checks;
- Nix library tests;
- generated output reproducibility checks.

Avoid checks that require:

- secrets;
- display servers;
- host services;
- global tools;
- long-running external builds;
- non-deterministic network access.

## Scripts

Use scripts for imperative orchestration only.

Prefer Python when scripts need parsing, retries, cleanup, subprocess orchestration, or more than about 10 lines of meaningful logic.

Scripts used by flake apps or checks must be reproducible through the flake environment.

Do not replace Nix product logic with scripts just to avoid writing Nix.

## Documentation

Document the public flake interface.

Minimum README content:

- what the project exports;
- supported systems;
- how to inspect outputs;
- how to build packages;
- how to run apps;
- how to enter dev shells;
- how to run checks;
- how to format;
- how to consume modules, overlays, or templates when provided.

## Validation

Default validation:

    nix flake show
    nix flake check
    nix fmt

If newly created files are hidden by Git flake source tracking, use:

    nix develop path:$PWD#dev --command <command> ...

If a check is too expensive or requires external state, document the limitation and keep it out of `checks` by default.

## Commit behavior

Follow the repository automatic commit policy.

A pure Nix refactor may be multiple normal execution steps when it changes independently verifiable outputs, such as packages, modules, checks, and templates.

Do not count formatting, validation, repair before validation passes, or checkpoint acceptance as separate normal execution steps.
