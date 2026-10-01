---
name: nix-workflow
description: Maintain explicitly adopted Nix product outputs, substantial Nix/Just workflow or layout changes, pure Nix products, and the provided GitHub-hosted Nix recipes. Use for these project interfaces; routine missing tools, minimal dev-shell setup, and ordinary shell additions use the Nix rule alone. Do not apply hosted-runner recipes locally.
---

# Nix Workflow

1. Identify the environment or product interface being changed and its consumers. Existing outputs establish their own scope; new product outputs require the user or project to adopt them.
2. Preserve established shell and output structure. Use a default shell as the starting point for new ordinary projects; introduce named shells only for useful isolation. Prefer adequate locked inputs and update them through Nix when needed.
3. Read [the layout reference](references/layout.md) when creating or reorganizing product outputs, `nix/`, scripts, Just recipes, checks, or formatter wiring. Create structure for actual behavior and keep shared commands understandable.
4. Read [the GitHub Actions Nix reference](references/github-actions-nix.md) only when a project-owned GitHub-hosted Ubuntu workflow needs Nix installation, heavy disk preparation, the documented container-store workaround, or explicit binary-cache configuration. Its runner and destructive-operation conditions are mandatory.
5. Validate the changed shell, recipe, or output first. Broader flake evaluation or builds follow from changed wiring and project gates, not merely from using Nix.
6. Report changed interfaces, validation, and material limitations. Include input changes, formatter churn, or exact intent-to-add paths when relevant.
