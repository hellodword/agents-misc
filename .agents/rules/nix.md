# Nix Development Environments and Outputs

The usual working environment is a Nix-based devcontainer. Prefer project development shells for tools that build, run, and validate the project so work can proceed without changing the host.

## Autonomous environment work

- Add missing project execution tools to the appropriate project shell and retry the affected command. If no flake exists, creating a minimal development shell is authorized as part of the task; retain useful project configuration.
- Start a new ordinary project with `devShells.<system>.default`. Preserve existing shell names and structure. Add a named shell when real toolchain or environment isolation requires it, without creating shells for symmetry.
- Prefer existing locked inputs when adequate. For a new flake, prefer `github:NixOS/nixpkgs/nixos-unstable` and let the generated `flake.lock` pin it. Update inputs through Nix when needed; do not align them to an unrelated container revision.
- Run project execution tools through `nix develop --command <command>` or inside an entered shell. Select an established named shell explicitly, for example `nix develop .#dev --command <command>`.
- Diagnose host, device, container, display, filesystem, or permission limitations when a project tool declaration cannot solve them. Missing project execution tools do not need a separate troubleshooting workflow.

## Just command entrypoints

- When introducing a Nix/Just workflow, use Just as an outer command entrypoint supplied by the surrounding environment, separate from project development-shell dependencies. Preserve established project conventions unless the task changes them.
- Invoke recipes directly as `just <recipe>` and keep them as thin wrappers. A recipe that needs the project environment calls `nix develop --command <tool-or-script>`; commands and scripts that need no Nix environment run directly.
- If Just is unavailable, report the external prerequisite and continue through the underlying commands where practical. Installation follows the existing authorization policy.

## Nix product outputs require an explicit choice

- A development shell does not authorize adding packages, apps, checks, formatter, modules, or overlays. Add these only when the user or project explicitly adopts the interface; pure Nix products may expose outputs required by their stated product scope.
- An existing output authorizes maintaining that interface, not unrelated outputs. Preserve its consumer contract.
- Keep flake wiring understandable. Extract reusable logic under `nix/` when useful; do not create empty structure.

## Project-owned Playwright browsers

- Preserve an existing browser source and project policy; ordinary test runs do not call for a migration. For a new Playwright setup in a Nix project, prefer Chromium from locked nixpkgs when the target platform supports it. Branded Google Chrome requires a product need and authorization for the unfree dependency.
- When using a Nix browser, set an explicit executable path through the project's native Playwright configuration and preserve its existing configuration interface. Fail clearly when that executable is unavailable; do not silently fall back to another browser. No shared helper or new environment-variable convention is required.
- Run an actual project browser smoke flow on initial provisioning and relevant browser, Playwright, or launch-configuration changes. Separately pinned versions do not establish compatibility; resolve mismatches through the owning inputs or project configuration.
- Follow the framework and project sandbox settings. For a trusted local E2E flow with an actual sandbox launch failure, inspect project requirements and container limitations; use native `chromiumSandbox: false` in project configuration when needed. If the project explicitly requires an enabled sandbox, fix the environment or report the blocker. Container detection alone does not justify overriding that policy.

## Remote NixOS VM tests

- Preserve a working project-local VM test flow. When remote execution is selected or local KVM is unavailable, use a prepared HTTP MCP executor and the VM test rule.
- Evaluate with the project's locked nixpkgs in the client environment. Transfer derivations through the service-issued binary cache and submit a sandbox test build to the physical host's Nix daemon.
- Require `qemu.forceAccel = true`. Initial acceptance combines administrator-confirmed daemon sandbox policy with a fresh multi-node test that starts its guests and passes assertions on host KVM.
- Compilation scheduling belongs to the host daemon; node CPUs and memory belong to the test definition. Collect logs and recorded outputs, and use a fresh top-level run identity for each new test execution.

## Source handling

- Do not use a `path:` flake reference to bypass Git source filtering. If a required durable file is untracked, use the root rule's exact-path intent-to-add procedure and report it.

## Focused validation

- For a shell tool change, verify the tool and retry the command in that shell. Routine code, documentation, or tool additions do not call for `nix build` or a full `nix flake check`.
- For an adopted product output, validate the affected output. Evaluate broader flake/check wiring only when that boundary changes or project policy requires it; choose targeted builds before a full repository gate.
