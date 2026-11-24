# Agent Rules Kit

Agent Rules Kit is a compact instruction payload for coding agents. It uses the open [AGENTS.md format](https://agents.md/) for shared instructions and the open [Agent Skills specification](https://agentskills.io/specification) for on-demand workflows.

## Repository map

This repository maintains the shared Agent Rules Kit alongside standalone engineering tools:

| Component                                                | Purpose                                                             | Authoritative inputs                   | Primary maintenance entry                                  |
| -------------------------------------------------------- | ------------------------------------------------------------------- | -------------------------------------- | ---------------------------------------------------------- |
| [Agent Rules Kit](AGENTS.md)                             | Shared agent rules and reusable workflow skills                     | `AGENTS.md`, `.agents/**`              | [Maintenance and validation](#maintenance-and-validation)  |
| [VM Test MCP](tools/vm-test-mcp/README.md)               | Disposable Windows/Linux labs and sandboxed NixOS VM tests          | `tools/vm-test-mcp/**`                 | `just --justfile tools/vm-test-mcp/justfile test`          |
| [Codex Config Atlas](tools/codex-config-atlas/README.md) | Versioned upstream schema registry, CLI, and static comparison site | tracked schema registry and Web source | [Maintenance SOP](tools/codex-config-atlas/MAINTENANCE.md) |
| [Tokscale Sandbox](tools/tokscale-sandbox/README.md)     | Offline `runsc`-isolated token analytics across local coding agents | `tools/tokscale-sandbox/**`            | [Component guide](tools/tokscale-sandbox/README.md)        |
| [AGY Meter](tools/agy-meter/README.md)                   | Real-time TTFT/TPS status line and SQLite telemetry for `agy`       | `tools/agy-meter/**`                   | [Component guide](tools/agy-meter/README.md)               |

Atlas data and site trees are generated Nix outputs; its tracked schema registry and Web source are the durable inputs.

## Distribution boundary

Only [AGENTS.md](AGENTS.md) and `.agents/**` belong to the distributed payload. Consuming repositories provide both at their root and normally mount or copy them read-only. External distribution chooses and pins a Git commit, version, or tag; this repository does not maintain a payload version or consumer lock.

Keep every entry in the distributed payload as a real file or directory (no symbolic links), with all local links resolving within `AGENTS.md` and `.agents/**`. Root [LICENSE](LICENSE) is the authoritative MIT license text for this kit; keep [.agents/LICENSE.agent-rules-kit](.agents/LICENSE.agent-rules-kit) as a byte-identical real-file copy. It licenses this kit rather than the consuming project or independently added rules and skills.

Repository maintenance lives in this guide, component guides under `tools/**`, Nix definitions under `nix/`, and `.github/workflows/`. When work touches another component or shared Nix/CI definitions, identify the affected outputs or workflow consumers and apply their component guides and validation.

## Loading model

### Consumer projects

Consuming projects establish project context from their existing code, configuration, and documentation. Neither a `README.md` nor a fixed documentation layout is required. The [loading policy](AGENTS.md#targeted-loading) routes shared rules and workflow skills on demand through [.agents/rules/index.md](.agents/rules/index.md).

### This repository

Use the [repository map](#repository-map) to locate the affected component's guide, then read its contract, development, and validation sections before changing behavior. Apply the authoring and boundary rules below when modifying `AGENTS.md`, `.agents/**`, or kit documentation.

## What belongs where

- A **rule** holds concise defaults or constraints for a recognizable technical boundary. Keep shared rules as flat Markdown files under `.agents/rules/`.
- A **skill** captures task-specific workflow decisions under `.agents/skills/<name>/SKILL.md`. Give each `SKILL.md` only `name` and `description` YAML frontmatter, matching `name` to its directory with lowercase letters, digits, and single hyphens (`[a-z0-9-]+`). State both the task the skill helps with and any exclusion needed to prevent misrouting.
- Supporting files live beside their owning skill under `assets/`, `references/`, or `scripts/` and are linked directly from `SKILL.md`:
  - `assets/`: templates or reusable source files consumed by the workflow;
  - `references/`: longer conditional guidance that `SKILL.md` directs the agent to read when needed;
  - `scripts/`: durable helper executables invoked by the workflow.

Route every non-index rule and skill directly from [.agents/rules/index.md](.agents/rules/index.md). Do not introduce hidden metadata, recursive instruction loading, global template catalogs, or duplicate inventories. Keep each substantive preference in one place without repeating rules or generic coding common sense across skills. Use human-readable structures for reports intended for people; define machine contracts only for real program consumers.

[AGENTS.md](AGENTS.md#safety-and-authorization) owns global safety and authorization, [dependencies](.agents/rules/dependencies.md) owns dependency-update operations, [Nix](.agents/rules/nix.md) owns environment and output policy, and [formatting](.agents/rules/formatting.md) owns formatter policy.

## Reference map for earlier entrypoints

| Earlier entry                                      | Current home or behavior                                                                                                                     |
| -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `defaults.md`                                      | [New project preferences](.agents/rules/new-project.md); environment and database details live in their own routes                           |
| `data.md`                                          | [Database and driver preferences](.agents/rules/databases.md), including SQLite/PostgreSQL selection and driver-specific connection defaults |
| `architecture.md`, `converge-codebase`             | Ordinary architecture and cleanup work uses task scope and repository evidence                                                               |
| `environment.md`, `environment-troubleshooting`    | [Nix development environments](.agents/rules/nix.md)                                                                                         |
| `security.md`                                      | Operational protection and authorization live in [AGENTS.md](AGENTS.md#safety-and-authorization)                                             |
| `compatibility-review`                             | Consumer-based decisions in [contracts](.agents/rules/contracts.md)                                                                          |
| `generated-artifacts-review`                       | Role-based ownership and proportionate validation in [generated assets](.agents/rules/generated-artifacts.md)                                |
| `standalone-final-execution-plan` and its template | [long-task-plan](.agents/skills/long-task-plan/SKILL.md), triggered by an explicit request to plan autonomous long execution                 |
| `nix-browser-e2e` and its helper                   | Browser policy in the [Nix rule](.agents/rules/nix.md#project-owned-playwright-browsers); projects use native Playwright configuration       |

The [long-task plan](.agents/skills/long-task-plan/SKILL.md) keeps completion evidence and authorization stable while allowing implementation details to adapt to discoveries. The [NixOS VM test skill](.agents/skills/nixos-vm-test/SKILL.md) submits locally evaluated derivations to a prepared HTTP MCP executor on host KVM, and the [VirtualBox VM test skill](.agents/skills/virtualbox-vm-test/SKILL.md) runs scenarios in independent Windows/Linux linked clones; [VM Test MCP](tools/vm-test-mcp/README.md) and its [administrator guide](tools/vm-test-mcp/preparation.md) own executor setup and operations.

## Maintenance and validation

- When modifying `AGENTS.md`, `.agents/**`, or kit documentation, review affected routes, skill descriptions, relative links, `LICENSE` / `.agents/LICENSE.agent-rules-kit` parity, and distribution boundaries together. Walk through representative tasks when changing routing or authorization behavior; treat static walkthroughs as structural verification rather than measured model improvement.
- Validate workflow changes from the development shell (which supplies both `actionlint` and `shellcheck` for embedded scripts, plus `ruff`):
  ```sh
  nix develop .#dev --command actionlint .github/workflows/ci.yml
  ```
- The [CI workflow](.github/workflows/ci.yml) checks formatting, runs the four Atlas checks, and builds the site in a digest-pinned Nix container on pushes to `master` and manual dispatches, sharing one Nix store and exporting the site to GitHub Pages. Public binary caches live in [flake.nix](flake.nix), and CI reads the shared cache through a loopback `oranc` proxy configured by [nix/cache-config.nix](nix/cache-config.nix) with signature verification.
- Format changed files with `nix fmt`. For Nix output or check wiring changes, run `nix flake check --no-build --no-write-lock-file` and build the affected checks.
- Review the final diff and `git status --short --ignored`. Preserve and disclose relevant pre-existing ignored work, and follow the [temporary workspace and retention policy](AGENTS.md#temporary-workspace-and-retention).

## Supported Markdown links

Distributed Markdown uses single-line `[text](path)` links or `![description](path)` images. Local targets are plain paths relative to the source document, optionally followed by `#fragment`, and resolve to real files or directories within the payload. Put Markdown link examples in inline code or fenced code blocks.
