# Nix and Just Rules

## Defaults

- Default system: `x86_64-linux`.
- Default nixpkgs input: `github:NixOS/nixpkgs/nixos-unstable`.
- Prefer `flake.nix` and `flake.lock`.
- Prefer a default dev shell named `dev`.

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

Good recipe shape:

    test:
      nix develop .#dev --command go test ./...

    test-race:
      nix develop .#dev --command go test -race ./...

    fmt:
      nix develop .#dev --command gofmt -w ./cmd ./internal

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

## Missing packages

If a project command needs a missing package:

- update `flake.nix` when edits are allowed;
- otherwise stop and report the missing package/tool.

Do not install global tools.
