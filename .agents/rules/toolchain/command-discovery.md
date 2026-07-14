---
id: toolchain.command-discovery
kind: toolchain
triggers:
  - "command discovery"
  - "just --list"
  - "README commands"
  - "available scripts"
  - "validation command"
summary: Discover project commands safely before inventing new workflows.
companions: {}
---

# Command Discovery Rules

Do not assume a conventional Linux filesystem layout.

Prefer `PATH` discovery over hard-coded paths.

Use `command -v <name>` or `type -P <name>`.

Nix-based devcontainers may expose user-profile binaries through paths like:

- `$HOME/.nix-profile/bin`
- `/home/vscode/.nix-profile/bin`
- `/run/current-system/sw/bin`

These paths should work through `PATH` when the environment is configured correctly. Check explicit paths only as diagnostics after `PATH` probing fails.

Browser executable discovery belongs in the project's E2E configuration or helper, not in agent command probing.
