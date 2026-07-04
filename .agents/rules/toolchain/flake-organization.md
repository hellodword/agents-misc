---
id: toolchain.flake-organization
kind: toolchain
triggers:
  - 'flake organization'
  - 'nix directory'
  - 'flake outputs'
  - 'apps'
  - 'checks'
  - 'devShells'
summary: Organize flake inputs, outputs, packages, apps, checks, and dev shells consistently.
load_with:
  references:
    - nixpkgs-devcontainer-alignment
---

# Flake Organization Rules

## Purpose

Use this rule when creating or reorganizing:

- `./flake.nix`
- `./nix/`
- `./scripts/`
- `packages`
- `apps`
- `devShells`
- `checks`
- `formatter`
- `templates`
- `overlays`
- `nixosModules`
- `homeManagerModules`

The goal is to keep the flake readable, reproducible, and easy for agents to extend without turning `flake.nix` into a large unstructured file.

## Ordinary projects and pure Nix projects

In ordinary projects, Nix is primarily the reproducible toolchain, build, test, package, and development environment entrypoint.

In pure Nix projects, Nix is the core product language and the flake outputs are the public interface.

A project should be treated as pure Nix only when the user explicitly says so or the repository clearly states that its main product is Nix-based.

Pure Nix examples:

- Nix package collections;
- overlays;
- NixOS modules;
- Home Manager modules;
- flake templates;
- Nix libraries;
- development-environment distributions;
- Nix-based infrastructure or deployment modules.

Ordinary projects should prefer a commented `justfile` as a human command menu.

Pure Nix projects do not require a `justfile`. If a pure Nix project has a `justfile`, it should remain an optional convenience layer over flake-native commands.

## Root layout

Recommended ordinary-project layout:

    .
    ├── flake.nix
    ├── flake.lock
    ├── justfile
    ├── nix/
    │   ├── lib.nix
    │   ├── packages.nix
    │   ├── apps.nix
    │   ├── dev-shells.nix
    │   ├── checks.nix
    │   └── formatter.nix
    └── scripts/
        └── <durable-script>.<ext>

Recommended pure-Nix layout:

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
        └── <durable-script>.<ext>

Use only the files that are needed.

Do not create empty Nix modules just to follow the layout.

## `./flake.nix`

`./flake.nix` is the entry point.

It should contain:

- `description`;
- `inputs`;
- supported systems;
- small helper wiring;
- output assembly;
- imports from `./nix/`.

It should not contain:

- long shell scripts;
- large unrelated derivation bodies;
- long per-language tool lists when they can be moved to `./nix/dev-shells.nix`;
- complex code generation orchestration;
- stateful workflow algorithms;
- pure patch apply/refresh/build algorithms.

For pure Nix projects, product-level Nix output wiring belongs in `flake.nix`, but reusable logic should still be split into `./nix/`.

Default `nixpkgs` input in `flake.nix`:

    github:NixOS/nixpkgs/nixos-unstable

Before lockfile pinning or updating nixpkgs, apply `.agents/references/nixpkgs-devcontainer-alignment.md`.

When a specific nixpkgs revision is required for the current environment, run `nix flake update nixpkgs --override-input nixpkgs "github:NixOS/nixpkgs/<rev>"`. Do not rewrite the durable `flake.nix` input to `github:NixOS/nixpkgs/<rev>` unless the project already has that convention.

## `./nix/`

Use `./nix/` for reusable Nix code.

Recommended file responsibilities:

- `nix/lib.nix`: small local helper functions.
- `nix/packages.nix`: `packages.${system}` definitions.
- `nix/apps.nix`: `apps.${system}` definitions.
- `nix/dev-shells.nix`: `devShells.${system}` definitions.
- `nix/checks.nix`: `checks.${system}` definitions.
- `nix/formatter.nix`: `formatter.${system}` definition.
- `nix/overlays/`: overlay definitions.
- `nix/modules/`: project-specific Nix modules.
- `nix/nixos-modules/`: NixOS module definitions.
- `nix/home-manager-modules/`: Home Manager module definitions.
- `nix/templates/`: template definitions and template assets.

Keep module interfaces simple.

Pass only the values each module needs, such as:

- `pkgs`;
- `system`;
- `self`;
- relevant inputs;
- local helper functions.

Avoid passing the entire flake scope everywhere when a smaller argument set is enough.

## `./scripts/`

Use `./scripts/` for durable repository scripts.

Prefer Python over shell when the logic needs:

- JSON/YAML/TOML parsing;
- non-trivial filesystem traversal;
- retries;
- cleanup;
- subprocess orchestration;
- structured errors;
- more than about 10 lines of meaningful logic.

Shell is acceptable for tiny wrappers and simple command sequencing.

Scripts used by flake outputs must be reproducible:

- required interpreters and tools must come from `flake.nix`;
- required inputs must be tracked;
- output paths must be stable;
- scripts must not rely on global installs.

Pure Nix projects should not move Nix product logic into scripts merely to avoid writing Nix. Scripts should handle imperative orchestration, not replace Nix modules or flake outputs.

## Outputs

### `packages`

Use `packages.${system}` for buildable derivations.

Defaults:

- `packages.${system}.default`: main build artifact when the project has one.
- `packages.${system}.<name>`: additional named artifacts.

Use packages for:

- compiled applications;
- generated static outputs intended as build products;
- packaged CLI binaries;
- reusable derivations;
- pure Nix package exports.

Do not use packages for:

- ad-hoc developer commands;
- scripts that primarily orchestrate local state;
- browser capability discovery;
- long-running local services unless they produce a derivation.

### `apps`

Use `apps.${system}` for `nix run`.

Defaults:

- `apps.${system}.default`: main runnable app when the project has one.
- `apps.${system}.<name>`: named runnable tools.

Use apps for:

- running the project CLI;
- invoking durable repository scripts;
- project-local codegen commands;
- migration commands when they are safe and documented;
- pure patch helper commands;
- pure Nix maintenance commands that are safe to run through `nix run`.

Apps should be thin wrappers around packages or scripts.

### `devShells`

Use `devShells.${system}` for development environments.

Defaults:

- `devShells.${system}.dev`: default development shell.
- `devShells.${system}.<name>`: additional shells only when they materially differ.

A dev shell should include:

- language toolchains;
- build tools;
- formatters;
- linters;
- test tools;
- project-local codegen tools;
- native libraries required by project dependencies.

Pure Nix projects should include Nix formatters, linters, and test tools in the dev shell when they are part of the project workflow.

Do not add host-specific tools just because they are useful in one environment.

Do not add Chromium-family browsers to the dev shell only for exploratory E2E. Add browser dependencies only when browser testing is a durable project requirement and the Nix package works in the target environment.

### `checks`

Use `checks.${system}` for stable validations intended for `nix flake check`.

Good checks:

- package builds;
- unit test derivations;
- formatting checks when stable;
- codegen reproducibility checks;
- Nix module evaluation checks;
- overlay evaluation checks;
- flake template checks;
- lightweight integration checks that do not require secrets, network, display, or host services.

Do not put long-running, flaky, credentialed, browser-display-dependent, or host-specific checks in `checks` by default.

### `formatter`

Use `formatter.${system}` when the formatter is stable and useful for the project.

Formatter output should support:

    nix fmt

Pure Nix projects should normally expose a formatter.

Do not add a formatter output if the project has no stable formatting command.

### `templates`

Use `templates.<name>` when the project intentionally provides reusable flake templates.

Each template should include:

- a clear description;
- minimal files;
- no secrets;
- no machine-specific paths;
- a documented validation command.

### `overlays`

Use `overlays.<name>` when the project intentionally exports overlays.

Overlays should be documented and evaluated by checks when practical.

### `nixosModules` and `homeManagerModules`

Use module outputs when the project intentionally exports NixOS or Home Manager modules.

Modules should include:

- options with descriptions;
- sensible defaults;
- examples when practical;
- evaluation checks when practical.

## Justfile integration

Ordinary projects should expose stable commands through commented just recipes.

Each recipe should call Nix.

Example:

    # Enter the default development shell.
    dev:
      nix develop .#dev

    # Run the default app through the flake app output.
    run:
      nix run .#default

    # Build the default package.
    build:
      nix build .#default

    # Run stable flake checks.
    check:
      nix flake check

    # Run the project test command in the dev shell.
    test:
      nix develop .#dev --command just _test

Pure Nix projects do not require a justfile.

When a pure Nix project does include a justfile, it should stay optional and forward to flake-native commands:

    # Show flake outputs.
    show:
      nix flake show

    # Run stable flake checks.
    check:
      nix flake check

    # Format Nix files through the flake formatter.
    fmt:
      nix fmt

Private helper recipes may be prefixed with `_`, but they should still stay simple.

## Complexity boundary

Move imperative workflow logic out of `flake.nix` and `justfile` when it needs:

- loops;
- branching;
- parsing structured data;
- retries;
- cleanup traps;
- stateful orchestration;
- long command sequences;
- environment probing.

Durable complex imperative logic belongs in `./scripts/`.

Nix should pin tools, compose outputs, evaluate modules, and define reproducible build logic.

Just should make ordinary-project commands discoverable when a justfile is used.

Scripts should implement imperative orchestration.

## Validation

After creating or reorganizing flake outputs, validate with the narrowest relevant commands:

    nix flake show
    nix flake check

When newly created files are invisible because of Git flake source tracking, use:

    nix develop path:$PWD#dev --command <command> ...

For pure Nix projects, validation should usually include:

    nix flake show
    nix flake check
    nix fmt

If output changes are intended to be durable in an ordinary project, wrap common commands in commented just recipes.

If output changes are intended to be durable in a pure Nix project, ensure the flake outputs themselves are discoverable and documented.
