# Nix Development Environments and Outputs

Prefer project Nix development shells (`nix develop .#<shell> --command ...`) so build, validation, and missing-tool additions remain reproducible without mutating the host.

## Environments and Just entrypoints

- Add missing execution tools to the appropriate project `devShell` and retry the command; creating a minimal flake shell is authorized when none exists. Start greenfield projects with `devShells.<system>.default` and `github:NixOS/nixpkgs/nixos-unstable` pinned in `flake.lock`, adding named shells only for real toolchain or platform isolation.
- Keep `just` recipes as thin outer entrypoints (`just <recipe>`): enter `nix develop` only for recipes that need shell dependencies, and place non-trivial multi-step logic in reusable scripts under `scripts/` using structured argument lists.
- A development shell does not authorize adding `packages`, `apps`, `checks`, `formatter`, modules, or overlays; expose those outputs only when explicitly adopted by the project or user.

## Source handling and cross-repo locks

- Never use a `path:` flake reference to bypass Git source filtering. To expose new durable, non-secret, non-ignored files to a Git-backed flake before committing, mark explicit file paths with `git add -N -- <file>...` (never directories, globs, or `-f`) and report them.
- When updating a flake input locked to a local commit in a sibling repository that has not been pushed to its remote URL, keep the public URL in `flake.nix` and resolve objects locally via transient Git config:
  `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0="url.file:///abs/path/to/repo.insteadOf" GIT_CONFIG_VALUE_0="https://github.com/owner/repo.git" nix flake update <input>`

## Project-owned Playwright browsers

- Preserve the project's configured browser source. For greenfield Nix Playwright setups on supported platforms, pass locked `pkgs.chromium` explicitly via native Playwright configuration without silent fallback downloads (branded Chrome requires explicit product need and unfree authorization). Run a real browser smoke flow when provisioning or changing browser/Playwright wiring; use `chromiumSandbox: false` only when container limitations require it and project policy permits.

## Remote NixOS VM tests

- Reuse working local VM flows when available; when local KVM is unavailable or remote execution is selected, evaluate against the project's locked `nixpkgs` (`qemu.forceAccel = true`) and submit through a prepared HTTP MCP executor using the [nixos-vm-test](../skills/nixos-vm-test/SKILL.md) skill, keeping build compilation on the host daemon and guest CPU/RAM in the test definition.

## Validation

- Distinguish **configuration evaluation** (`nix flake check --no-build --no-write-lock-file`, which cheaply catches cross-module option and wiring errors across all outputs without booting VMs) from **derivation builds and VM test execution**. Keep `flake.nix` and `devShell` evaluation pure—consuming `nixpkgs` attributes directly rather than Import-From-Derivation on unbuilt store paths—so `nix flake check --no-build --no-write-lock-file` succeeds before building toolchains.
- Routine code, doc, or dev-shell edits use focused shell checks; validate adopted Nix outputs and affected consumers when their inputs or wiring change. On dirty Git trees, batch multiple target attributes into a single `nix build` invocation to avoid repeated store source copies.
