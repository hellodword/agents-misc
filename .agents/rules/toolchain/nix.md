---
id: toolchain.nix
kind: toolchain
triggers:
  - "Nix"
  - "Just"
  - "flake.nix"
  - "flake outputs"
  - "devShells"
  - "checks"
  - "treefmt"
summary: Use Nix as the reproducible command environment and Just as a thin command menu for ordinary projects.
companions:
  conditional_rules:
    - id: toolchain.formatting
      when: formatter, treefmt, nix fmt, Prettier, gofmt, cargo fmt, or project-wide formatting is involved
    - id: core.scripts
      when: durable scripts or non-trivial command orchestration are added or changed
    - id: core.dependencies
      when: adding flake inputs, packages, language packages, or binary tools
  skills:
    - id: nix-workflow
      when: adding or restructuring durable build, test, lint, format, codegen, migration, package, or flake commands
  references:
    - id: nixpkgs-devcontainer-alignment
      when: initializing or updating nixpkgs
    - id: nix-layout
      when: longer layout, output, justfile, or treefmt examples are needed
  templates:
    - id: treefmt.nix
      when: seeding treefmt-nix formatting
    - id: .prettierrc.json
      when: seeding Prettier formatting
    - id: .editorconfig
      when: seeding editor formatting defaults
---

# Nix Toolchain Rules

Use this rule for `flake.nix`, `flake.lock`, `nix/**`, `justfile`, Nix apps/packages/dev shells/checks/formatters, treefmt-nix, and durable project command workflow.

## Roles

- Ordinary projects: Nix provides the reproducible environment; Just provides the human-friendly command menu.
- Pure Nix projects: flake outputs are the public interface; a `justfile` is optional.
- Scripts hold durable imperative orchestration that is too large or stateful for `flake.nix` or `justfile`.

## Flake defaults

- Prefer `flake.nix` for reproducible tools, builds, checks, and development shells.
- Keep `flake.nix` thin: inputs, supported systems, small wiring, and imports from `./nix/`.
- Put reusable Nix logic under `./nix/`.
- Do not create empty `nix/**` modules just to match a layout.
- Default `nixpkgs` input: `github:NixOS/nixpkgs/nixos-unstable`.
- Before initializing or updating `nixpkgs`, read `.agents/references/nixpkgs-devcontainer-alignment.md`.
- When a devcontainer-provided revision must be used, prefer `nix flake update nixpkgs --override-input nixpkgs "github:NixOS/nixpkgs/<rev>"` instead of rewriting the durable input URL to a revision URL.

## Command classes

Bootstrap commands may run directly when needed to enter or inspect the project environment: `git`, `nix`, `just`, `pwd`, `env`, `command`, `type`, `test`, `df`, `stat`, `nproc`, minimal shell builtins, and `jq` only when reading `$DEVCONTAINER_FLAKE_INPUTS`.

Project commands should run through Nix, usually with:

```sh
nix develop .#dev --command <command> ...
```

Use a named shell when appropriate:

```sh
nix develop .#<env> --command <command> ...
```

When Git-tracked flake source behavior hides newly created files, use:

```sh
nix develop path:$PWD#<env> --command <command> ...
```

Environment capability commands are discovered from the current environment and are not automatically added to `flake.nix`.

## Justfile

Use `justfile` as a convenience layer over Nix, not as a second build system.

- Add a short documentation comment above every durable recipe.
- Keep recipes thin and forward to Nix or checked-in scripts.
- Move logic into scripts when it needs loops, functions, branching, retries, parsing, cleanup traps, temp-state orchestration, or more than about 10 meaningful lines.
- Do not add `just` to `flake.nix` solely because a root `justfile` exists; `just` is an outer bootstrap tool.

## Formatting

- Prefer a flake `formatter` output.
- For multi-language projects, prefer treefmt-nix through `nix fmt`.
- Treat `nix fmt` as a mutating formatting step, not a read-only validation command.
- Review and report files changed by formatting.
- Do not add formatter commands that bypass the flake formatter unless they are intentionally narrower or already conventional in the repository.

## Validation

Use the narrowest relevant command:

- `nix flake show` when outputs or public flake interface changed;
- `nix flake check` when checks or broad flake wiring changed;
- `nix build .#<package>` for package changes;
- `nix run .#<app>` for runnable app changes;
- `nix develop .#<shell> --command <command>` for project command validation;
- `nix fmt` as a formatting step when formatter output or formatted files are involved.
