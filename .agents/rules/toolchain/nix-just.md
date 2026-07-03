---
id: toolchain.nix-just
kind: toolchain
triggers:
  - 'Nix'
  - 'Just'
  - 'justfile'
  - 'flake.nix'
  - 'dev shell'
  - 'nixpkgs'
---

# Nix and Just Rules

## Applicability

Use these defaults only for new projects, greenfield scaffolding, or when the existing repository has no clear convention.

Do not introduce this stack, package manager, framework, database, toolchain, workflow, or directory structure into an existing project merely because it is preferred here.

Prefer the current local convention when it is coherent and working.

## Defaults

Use these defaults only for new projects, greenfield scaffolding, or repositories with no clear convention.

Do not introduce Nix or Just into an existing project merely because they are preferred here.

- Default system: `x86_64-linux`.
- Default nixpkgs branch: `nixos-unstable`.
- Prefer `flake.nix` and `flake.lock`.
- Prefer a default dev shell named `dev`.

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

Before initializing or updating the repository `nixpkgs` input inside a devcontainer, check whether the current devcontainer exposes an exact nixpkgs revision through `$DEVCONTAINER_FLAKE_INPUTS`.

Use:

    devcontainer_nixpkgs_rev=""
    if [ -n "${DEVCONTAINER_FLAKE_INPUTS:-}" ] && [ -r "$DEVCONTAINER_FLAKE_INPUTS" ]; then
      devcontainer_nixpkgs_rev="$(jq -r '.inputs.nixpkgs.rev // empty' "$DEVCONTAINER_FLAKE_INPUTS")"
    fi

If `devcontainer_nixpkgs_rev` is non-empty, align to it with:

    nix flake update nixpkgs --override-input nixpkgs "github:NixOS/nixpkgs/${devcontainer_nixpkgs_rev}"

After running the command, verify the result:

    jq -r '.nodes.nixpkgs.locked.rev // empty' flake.lock

Success means the locked rev matches `devcontainer_nixpkgs_rev`.

If the lockfile does not record the intended revision, stop and report the mismatch. Do not claim that the repository is aligned.

If `$DEVCONTAINER_FLAKE_INPUTS` is unset, unreadable, lacks `.inputs.nixpkgs.rev`, or the extracted value is empty, use:

    github:NixOS/nixpkgs/nixos-unstable

Do not install `jq` globally. If `jq` is unavailable before the dev shell exists, use an already available `python3` fallback only if it is present; otherwise report an environment blocker.

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

    # Format Go source files.
    fmt:
      nix develop .#dev --command gofmt -w ./cmd ./internal

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
