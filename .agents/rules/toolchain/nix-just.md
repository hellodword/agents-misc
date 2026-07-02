# Nix and Just Rules

## Defaults

- Default system: `x86_64-linux`.
- Default nixpkgs branch: `nixos-unstable`.
- Prefer `flake.nix` and `flake.lock`.
- Prefer a default dev shell named `dev`.

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

Project commands must run through Nix, usually via just recipes:

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

Good recipe shape:

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

Expose stable commands through outputs and just recipes.

## Missing packages

If a project command needs a missing package:

- update `flake.nix` when edits are allowed;
- otherwise stop and report the missing package/tool.

Do not install global tools.
