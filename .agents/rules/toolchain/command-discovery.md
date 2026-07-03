---
id: toolchain.command-discovery
kind: toolchain
triggers:
  - 'command discovery'
  - 'just --list'
  - 'README commands'
  - 'available scripts'
  - 'validation command'
---

# Command Discovery Rules

Do not assume a conventional Linux filesystem layout.

Prefer `PATH` discovery over hard-coded paths.

Use `command -v <name>` or `type -P <name>`.

Do not assume browser paths such as `/usr/bin/chromium`.

Nix-based devcontainers may expose user-profile binaries through paths like:

- `$HOME/.nix-profile/bin`
- `/home/vscode/.nix-profile/bin`
- `/run/current-system/sw/bin`

These paths should work through `PATH` when the environment is configured correctly. Check explicit paths only as diagnostics after `PATH` probing fails.

For Chromium-family browsers, probe `PATH` in this order:

1. `google-chrome`
2. `microsoft-edge`
3. `chromium`

Do not install browsers automatically.

Do not add browsers to `flake.nix` just because an exploratory E2E check needs a local browser. Add browser dependencies to `flake.nix` only when browser testing is a durable project requirement and the Nix package works in the target environment.
