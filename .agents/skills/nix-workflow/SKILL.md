---
name: nix-workflow
description: Maintain explicitly adopted Nix product outputs, substantial Nix/Just workflow or layout changes, and pure Nix products. Use for these project interfaces; routine missing tools, minimal dev-shell setup, and ordinary shell additions use the Nix rule alone.
---

# Nix Workflow

1. Trace the affected environment or product interface from its flake wiring to the commands and consumers it serves. Apply the Nix rule's adoption and environment policy.
2. Read [the layout reference](references/layout.md) when creating or reorganizing product outputs, `nix/`, scripts, Just recipes, checks, or formatter wiring. Keep source-workflow commands and packaged-product consumers distinct where their inputs or build paths differ.
3. Check that changed recipes and outputs use the intended sources, tools, and validation commands. Keep equivalent local and Nix validation entrypoints aligned, without making users run both.
4. Validate the affected interface and report consumer-visible changes or limitations. Broader evaluation follows from changed wiring and project gates.
