---
id: toolchain.nix-just
kind: toolchain
triggers:
  - "Nix"
  - "Just"
  - "justfile"
  - "flake.nix"
  - "treefmt"
  - "dev shell"
  - "nixpkgs"
summary: Use Nix for reproducible environments and Just for documented project commands.
load_with:
  rules:
    - toolchain.flake-organization
    - core.scripts
  skills:
    - nix-just-workflow
  references:
    - nixpkgs-devcontainer-alignment
---

# Nix and Just Rules

## Applicability

Use these defaults only for new projects, greenfield scaffolding, or repositories without a clear existing convention.

Prefer coherent local conventions.

## Defaults

- Default system: `x86_64-linux`.
- Default `nixpkgs` input in `flake.nix`: `github:NixOS/nixpkgs/nixos-unstable`.
- Prefer `flake.nix` and `flake.lock`.
- Prefer a default dev shell named `dev`.
- Prefer `treefmt-nix` for multi-language formatters when a project already uses Nix.
- Prefer a root `treefmt.nix` and expose it through the flake `formatter` output.

## Ordinary projects and pure Nix projects

For ordinary projects, Nix is the reproducible environment and command entrypoint, while `justfile` is the human-friendly command menu.

For ordinary projects:

- prefer a root `justfile`;
- every durable just recipe must have a documentation comment;
- stable recipes should call Nix;
- recipes should stay thin.

For pure Nix projects, the project itself is primarily Nix.

A project is pure Nix only when the user explicitly says so or the repository clearly states that its core product is Nix-based, such as:

- a Nix package set;
- NixOS modules;
- Home Manager modules;
- overlays;
- flake templates;
- Nix library functions;
- Nix-based development environment distribution;
- Nix-based infrastructure or deployment modules.

For pure Nix projects:

- a `justfile` is not required;
- flake outputs are the primary public interface;
- `nix flake show`, `nix build`, `nix run`, `nix develop`, `nix flake check`, and `nix fmt` should be first-class;
- add `justfile` only when the user explicitly wants a command menu or when it materially improves local usability.

## Devcontainer nixpkgs alignment

Before initializing or updating the repository `nixpkgs` input, read `.agents/references/nixpkgs-devcontainer-alignment.md`.

Summary requirements:

- use `github:NixOS/nixpkgs/nixos-unstable` as the durable `flake.nix` input unless the project already has another convention;
- prefer the exact nixpkgs revision exposed through `$DEVCONTAINER_FLAKE_INPUTS` when available by running `nix flake update nixpkgs --override-input nixpkgs "github:NixOS/nixpkgs/<rev>"`;
- verify that `.nodes.nixpkgs.locked.rev` in `flake.lock` matches the intended revision after updating;
- when no devcontainer revision is available, let `flake.lock` resolve from `github:NixOS/nixpkgs/nixos-unstable`;
- do not rewrite `flake.nix` to `github:NixOS/nixpkgs/<rev>` merely because `--override-input` was used;
- do not install `jq` or other helpers globally.

## Treefmt and formatter defaults

For new or unconstrained Nix projects that need formatting across multiple file types:

- use `treefmt-nix` as the flake formatter integration;
- keep formatter configuration in root `treefmt.nix`;
- set `projectRootFile = "flake.nix"` unless local convention has a better root marker;
- enable `programs.nixfmt` for Nix files;
- enable `programs.prettier` for JSON, JSONC, Markdown, HTML, CSS, JavaScript, TypeScript, JSX, TSX, Vue, and adjacent web formats;
- keep Prettier options in root `.prettierrc.json` when project style needs explicit defaults;
- keep editor baseline whitespace behavior in root `.editorconfig`;
- for ordinary projects, expose a commented `fmt` recipe that calls `nix fmt`;
- for pure Nix projects, make `nix fmt` first-class even when no `justfile` exists.

When seeding a shared default, use:

- `.agents/templates/treefmt.nix`;
- `.agents/templates/.prettierrc.json`;
- `.agents/templates/.editorconfig`.

Do not add independent formatter commands that bypass the flake formatter unless the project already has that convention or the command is intentionally narrower than `nix fmt`.

## Command classes

There are three command classes:

1. Bootstrap commands.
2. Project commands.
3. Environment capability commands.

Bootstrap commands may run directly:

- `git`
- `nix`
- `just`
- `pwd`
- `env`
- `command`
- `type`
- `test`
- `df`
- `stat`
- `nproc`
- `jq` when reading `$DEVCONTAINER_FLAKE_INPUTS`
- minimal shell builtins needed to enter the project environment

The `just` executable itself is an outer bootstrap tool. Do not add `just` to `flake.nix` solely because the root `justfile` exists.

Project commands must run through Nix.

For ordinary projects, project commands usually run through commented just recipes that call Nix:

- build
- test
- race test
- lint
- format
- package
- codegen
- migrations
- app/server commands
- durable validation commands
- pure patch apply/refresh/build scripts

For pure Nix projects, project commands should primarily be expressed through flake outputs:

- `packages`
- `apps`
- `devShells`
- `checks`
- `formatter`
- `templates`
- `overlays`
- `nixosModules`
- `homeManagerModules`

Environment capability commands are discovered from the current environment and are not automatically added to `flake.nix`:

- Chromium-family browsers used for exploratory/E2E checks
- existing CDP endpoints
- display/session capabilities
- upstream project tools already provided by a pure patch worktree

## One-off command form

Use:

    nix develop .#dev --command <command> ...

For named shells:

    nix develop .#<env> --command <command> ...

When Git-tracked flake source behavior hides newly created files, use:

    nix develop path:$PWD#<env> --command <command> ...

## Justfile role

Use `justfile` as a convenience layer over Nix, not as a second build system.

Every durable recipe must have a short documentation comment immediately above it so `just --list` is useful.

Good ordinary-project recipe shape:

    # Run Go unit tests.
    test:
      nix develop .#dev --command go test ./...

    # Run Go tests with the race detector on all packages.
    test-race:
      nix develop .#dev --command go test -race ./...

    # Format configured project files through the flake formatter.
    fmt:
      nix fmt

    # Run stable flake checks.
    check:
      nix flake check

Pure Nix projects do not require a justfile. When a pure Nix project has a justfile, it should document and forward to flake-native commands rather than becoming the primary interface.

Good pure-Nix optional recipe shape:

    # Show exported flake outputs.
    show:
      nix flake show

    # Build the default package.
    build:
      nix build .#default

    # Run the default app.
    run:
      nix run .#default

    # Run all stable flake checks.
    check:
      nix flake check

    # Format Nix files through the flake formatter.
    fmt:
      nix fmt

Move logic into checked-in scripts when it needs:

- loops;
- functions;
- case statements;
- JSON/YAML/TOML parsing;
- retries;
- cleanup traps;
- temp-state orchestration;
- more than about 10 lines;
- non-trivial branching.

## Flake organization

Read `.agents/rules/toolchain/flake-organization.md` before creating or reorganizing `flake.nix`, `./nix/`, `./scripts/`, or flake outputs.

Keep `flake.nix` thin.

Keep reusable Nix code under `./nix/`.

Keep durable non-trivial orchestration under `./scripts/`.

Expose stable commands through outputs and, for ordinary projects, commented just recipes.

For pure Nix projects, expose stable commands through outputs first. Add just recipes only when explicitly useful.

## Missing packages

If a project command needs a missing package:

- update `flake.nix` when edits are allowed;
- otherwise stop and report the missing package/tool.

Do not install global tools.
