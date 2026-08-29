---
name: nix-workflow
description: Implement or validate Nix flakes, Just commands, flake outputs, treefmt, dev shells, pure Nix products, and reproducible project workflows. Use when Nix/Just is already adopted or explicitly requested; do not introduce Nix as an unrelated side effect or apply hosted-runner recipes locally.
---

# Nix Workflow

1. Classify the repository as an ordinary application using Nix for its development environment, an explicitly Nix-managed project, a pure Nix product, or a pure patch workspace.
2. Identify the user request, applicable project instruction, or pure Nix product boundary that authorizes each proposed flake output. Preserve established output and shell contracts without treating them as authority for unrelated interfaces.
3. For an ordinary application without an isolation contract, provide one `devShells.<system>.default` containing all normal development tools and use it through `nix develop`.
4. Review every proposed Just recipe for stable project-level semantics, repeated multi-contributor use, and meaningful environment or argument standardization. Keep direct and one-off commands out of the command menu.
5. Limit recipes to simple linear invocations of the development shell, an explicitly adopted Nix output, a tool, or a script. Put parsing, branching, retries, cleanup, complex quoting, and stateful orchestration in checked-in scripts; do not compose recipes or run Just through `nix develop`.
6. Read [the layout reference](references/layout.md) when creating or reorganizing flake outputs, `nix/`, scripts, Just recipes, checks, or formatter wiring.
7. Read [the GitHub Actions Nix reference](references/github-actions-nix.md) only when a project-owned GitHub-hosted Ubuntu workflow needs Nix installation, heavy disk preparation, the documented container-store workaround, or reviewed input-cache inheritance.
8. Keep `flake.nix` as input and output wiring, put reusable logic under `nix/`, and never create modules or outputs for layout symmetry. Never use a Git `path:` source to bypass source filtering; use exact intent-to-add only under the shared Nix rule and report it.
9. Run the narrowest shell, recipe, formatter, or output validation first. Use `nix flake show` for output-interface changes and `nix flake check` only when broad flake or check wiring requires it.
10. Report the repository class, authorizing evidence, preserved and changed interfaces, command surface, nixpkgs decision when relevant, validation, formatter churn, intent-to-add, and limitations.
