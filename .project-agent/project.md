# agents-misc Project Context

This repository maintains the shared Agent Rules Kit and the other components listed in the [repository map](../README.md#repository-map). This project entry and its maintenance rules apply only to this repository and are excluded from the distributed payload.

Use the repository map to locate the affected component's guide, then read its relevant contract, development, and validation sections. Follow further references when needed for the task. When work reaches another component, load that component's guidance.

Read [the rules-kit maintenance rules](rules/maintenance.md) when changing `AGENTS.md`, `.agents/**`, `scripts/check-agent-rules.py`, `tests/test_check_agent_rules.py`, rules-kit documentation, repository agent guidance, or the Just recipes and Nix checks that validate the kit.

For shared files such as `justfile`, Nix definitions, and CI workflows, identify the recipes, outputs, or workflow consumers affected by the changed behavior. Apply the component guides and validation relevant to those consumers.
