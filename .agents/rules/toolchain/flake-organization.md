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

The goal is to keep the flake readable, reproducible, and easy for agents to extend without turning `flake.nix` into a large unstructured file.

## Root layout

Recommended root layout:

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
- large derivation bodies;
- long per-language tool lists when they can be moved to `./nix/dev-shells.nix`;
- complex code generation orchestration;
- product logic;
- pure patch apply/refresh/build algorithms.

Default nixpkgs branch:

    github:NixOS/nixpkgs/nixos-unstable

Before pinning or updating nixpkgs inside a devcontainer, apply the devcontainer alignment rule from `.agents/rules/toolchain/nix-just.md`.

## `./nix/`

Use `./nix/` for reusable Nix code.

Recommended file responsibilities:

- `nix/lib.nix`: small local helper functions.
- `nix/packages.nix`: `packages.${system}` definitions.
- `nix/apps.nix`: `apps.${system}` definitions.
- `nix/dev-shells.nix`: `devShells.${system}` definitions.
- `nix/checks.nix`: `checks.${system}` definitions.
- `nix/formatter.nix`: `formatter.${system}` definition.

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
- reusable derivations.

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
- pure patch helper commands.

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

Do not add host-specific tools just because they are useful in one environment.

Do not add Chromium-family browsers to the dev shell only for exploratory E2E. Add browser dependencies only when browser testing is a durable project requirement and the Nix package works in the target environment.

### `checks`

Use `checks.${system}` for stable validations intended for `nix flake check`.

Good checks:

- package builds;
- unit test derivations;
- formatting checks when stable;
- codegen reproducibility checks;
- lightweight integration checks that do not require secrets, network, display, or host services.

Do not put long-running, flaky, credentialed, browser-display-dependent, or host-specific checks in `checks` by default.

### `formatter`

Use `formatter.${system}` when the formatter is stable and useful for the project.

Formatter output should support:

    nix fmt

Do not add a formatter output if the project has no stable formatting command.

## Justfile integration

Expose stable commands through commented just recipes.

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

Private helper recipes may be prefixed with `_`, but they should still stay simple.

## Complexity boundary

Move logic out of `flake.nix` and `justfile` when it needs:

- loops;
- branching;
- parsing structured data;
- retries;
- cleanup traps;
- stateful orchestration;
- long command sequences;
- environment probing.

Durable complex logic belongs in `./scripts/`.

Nix should pin tools and compose outputs.

Just should make commands discoverable.

Scripts should implement orchestration.

## Validation

After creating or reorganizing flake outputs, validate with the narrowest relevant commands:

    nix flake show
    nix flake check

When newly created files are invisible because of Git flake source tracking, use:

    nix develop path:$PWD#dev --command <command> ...

If output changes are intended to be durable, wrap common commands in commented just recipes.
