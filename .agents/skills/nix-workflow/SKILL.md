---
name: nix-workflow
description: Implement or validate Nix flakes, Just commands, flake outputs, treefmt, dev shells, pure Nix products, and reproducible project workflows. Use when Nix/Just is already adopted or explicitly requested; do not introduce Nix as an unrelated side effect or apply hosted-runner recipes locally.
---

# Nix Workflow

1. Classify the repository as an ordinary application, pure Nix product, or pure patch workspace.
2. Identify whether the change affects an environment, Just recipe, flake output, formatter, script, package, app, or check.
3. Read [the layout reference](references/layout.md) when creating or reorganizing flake outputs, `nix/`, scripts, Just recipes, checks, or formatter wiring.
4. Read [the GitHub Actions Nix reference](references/github-actions-nix.md) only when a project-owned GitHub-hosted Ubuntu workflow needs Nix installation, heavy disk preparation, the documented container-store workaround, or reviewed input-cache inheritance.
5. Preserve public output and shell names unless the task changes them. Keep `flake.nix` as input/output wiring and put reusable logic under `nix/`.
6. Keep Just recipes thin and documented. Move parsing, branching, retries, cleanup, or stateful orchestration into checked-in scripts.
7. Expose durable validations through flake checks and multi-language formatting through treefmt-nix.
8. Never use a Git `path:` source to bypass source filtering. Use exact intent-to-add only under the shared Nix rule and report it.
9. Run the narrowest output/shell/recipe validation first. Use `nix flake show` for output-interface changes and `nix flake check` for broad wiring.
10. Report repository class, changed interface, command, nixpkgs decision when relevant, validation, formatter churn, intent-to-add, and limitations.
