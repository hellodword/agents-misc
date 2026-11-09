# Agent Rules Kit

Agent Rules Kit is a small, project-scoped instruction payload for coding agents. It uses the open [AGENTS.md format](https://agents.md/) for shared instructions and the open [Agent Skills specification](https://agentskills.io/specification) for on-demand workflows.

## Repository map

This repository contains the following components and maintenance entrypoints:

| Component                                                | Purpose                                                             | Authoritative inputs                   | Primary maintenance entry                                            |
| -------------------------------------------------------- | ------------------------------------------------------------------- | -------------------------------------- | -------------------------------------------------------------------- |
| [Agent Rules Kit](AGENTS.md)                             | Shared agent rules and reusable workflow skills                     | `AGENTS.md`, `.agents/**`              | [Maintenance guide](.project-agent/rules/maintenance.md)             |
| [VM Test MCP](tools/vm-test-mcp/README.md)               | Disposable Windows/Linux labs and sandboxed NixOS VM tests          | `tools/vm-test-mcp/**`                 | `just --justfile tools/vm-test-mcp/justfile test`                    |
| [Codex Config Atlas](tools/codex-config-atlas/README.md) | Versioned upstream schema registry, CLI, and static comparison site | tracked schema registry and Web source | `nix build --no-link .#checks.x86_64-linux.codex-config-atlas-tests` |

Atlas data and site trees are generated Nix outputs. Its tracked schema registry
and Web source are the durable inputs.

## Distribution boundary

Only [AGENTS.md](AGENTS.md) and `.agents/**` are distributed. Consuming repositories provide both at their root and normally mount or copy them read-only. The external distribution system chooses and records a Git commit, version, or tag; this repository does not maintain a payload version or consumer lock.

The payload includes the [Agent Rules Kit MIT license notice](.agents/LICENSE.agent-rules-kit), a real-file copy of this repository's authoritative [LICENSE](LICENSE). It covers this kit, not the consuming project or independently added rules and skills. Keep the notice with redistributed copies; do not replace a consuming project's license with it.

Repository maintenance lives in `.project-agent/**`, Nix definitions, and CI workflows. The separately maintained `tools/**` and `.github/**` trees have their own component contracts.

## Loading model

### Consumer projects

Consuming projects may provide `.project-agent/project.md` as a small, project-owned entrypoint. It is optional: projects without it use existing code, configuration, and documentation. Neither a README nor a fixed documentation tree is required.

An entry may state scoped requirements directly and link existing authoritative sources. For a project that already has imported sources and an API contract, an entry might contain:

```md
# Project context

Imported source snapshots are read-only. Apply changes through their owning import process.

For public API work, read the [HTTP contract](../contracts/http.md).
```

The [project-context policy](AGENTS.md#project-context) defines how to maintain that entrypoint and distinguish requirements from descriptive facts. The [loading policy](AGENTS.md#targeted-loading) applies to every repository task, including read-only work, and routes shared rules and skills through [.agents/rules/index.md](.agents/rules/index.md).

### This repository

The local [.project-agent/project.md](.project-agent/project.md) covers all components in `agents-misc`. It reuses the existing [repository map](#repository-map) to locate component guides and applies the rules-kit maintenance instructions only to that boundary. Shared Nix and CI files use the guidance and validation of their affected consumers. This repository's README navigation is a local choice; consumers choose their own documentation layout.

## What belongs where

- A **rule** holds concise defaults or constraints for a recognizable technical boundary. Rules are flat Markdown files, routed directly from the index.
- A **skill** captures reusable practices that need task-specific guidance. Each `SKILL.md` uses only the required `name` and `description` frontmatter.
- A skill-local **asset** is a template or reusable source file consumed by that workflow.
- A skill-local **reference** is longer optional guidance that the workflow tells the agent when to read.

Keep supporting files beside their owning skill and link them directly from `SKILL.md`. Do not create global template or reference catalogs. Keep each substantive preference in one rule; skills supply workflow decisions instead of repeating those defaults.

[AGENTS.md](AGENTS.md#safety-and-authorization) owns global safety and authorization, [dependencies](.agents/rules/dependencies.md) owns dependency-update operations, [Nix](.agents/rules/nix.md) owns environment and output policy, and [formatting](.agents/rules/formatting.md) owns formatter policy. The [maintenance rules](.project-agent/rules/maintenance.md) define this repository's structural conventions.

## Changes to earlier guidance

| Earlier entry                                      | Current home or behavior                                                                                                                     |
| -------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `defaults.md`                                      | [New project preferences](.agents/rules/new-project.md); environment and database details live in their own routes                           |
| `data.md`                                          | [Database and driver preferences](.agents/rules/databases.md), including SQLite/PostgreSQL selection and driver-specific connection defaults |
| `architecture.md`, `converge-codebase`             | Removed; ordinary architecture and cleanup work uses task scope and repository evidence                                                      |
| `environment.md`, `environment-troubleshooting`    | [Nix development environments](.agents/rules/nix.md); ordinary missing tools need no troubleshooting skill                                   |
| `security.md`                                      | Generic security tutorial removed; operational protection and authorization remain in [AGENTS.md](AGENTS.md)                                 |
| `compatibility-review`                             | Consumer-based decisions in [contracts](.agents/rules/contracts.md)                                                                          |
| `generated-artifacts-review`                       | Role-based ownership and proportionate validation in [generated assets](.agents/rules/generated-artifacts.md)                                |
| `standalone-final-execution-plan` and its template | [long-task-plan](.agents/skills/long-task-plan/SKILL.md), triggered by an explicit request to plan autonomous long execution                 |
| `nix-browser-e2e` and its helper                   | Browser policy in the [Nix rule](.agents/rules/nix.md#project-owned-playwright-browsers); projects use native Playwright configuration       |

Old entrypoints have no forwarding aliases. The kit supplies no dedicated SQLite migration or backup workflow; projects decide those methods. Database and library choices are preferences with justified alternatives, and dependency quality matters more than dependency count.

The long-task plan keeps completion evidence and authorization stable while allowing implementation choices to change. It records discoveries and continuation state, detects unproductive retries, and distinguishes incomplete or unverified work from completion. Starting a Goal or receiving an implementation task alone does not trigger planning.

The [NixOS VM test skill](.agents/skills/nixos-vm-test/SKILL.md) submits locally evaluated inputs to a prepared HTTP MCP executor for sandboxed tests on host KVM. The [VirtualBox VM test skill](.agents/skills/virtualbox-vm-test/SKILL.md) runs project scripts in independent Windows/Linux baseline clones. [VM Test MCP](tools/vm-test-mcp/README.md) provides task scheduling, transfers, logs and artifacts; its [administrator guide](tools/vm-test-mcp/preparation.md) covers host and baseline preparation for both workflows.

## Maintenance and validation

Read [.project-agent/project.md](.project-agent/project.md) before modifying the kit. Review affected routes, skill descriptions, local links, and distribution boundaries using the [maintenance guide](.project-agent/rules/maintenance.md). Walk through representative tasks when changing routing or authorization behavior.

Validate workflow changes with actionlint and ShellCheck from the development shell:

```sh
nix develop .#dev --command actionlint .github/workflows/ci.yml
```

The [CI workflow](.github/workflows/ci.yml) checks formatting, runs the four Atlas checks, and builds the site in a Nix container on pushes to `master` and manual dispatches. The build shares one Nix store and exports the site for GitHub Pages deployment.

Public binary caches are declared in [flake.nix](flake.nix). CI reads the shared cache through a loopback `oranc` proxy configured by [nix/cache-config.nix](nix/cache-config.nix), with signature verification. The proxy package is pinned by `flake.lock`. Cache availability affects build reuse; source builds remain available.

Format changed files with the repository formatter. For Nix output or check wiring changes, evaluate the flake with `nix flake check --no-build --no-write-lock-file` and build the affected checks. Component guides define their validation commands. Accept flake-provided configuration under the applicable trust policy.

Review the final diff and `git status --short --ignored`. Keep task-created diagnostics out of the payload and follow the [temporary workspace and retention policy](AGENTS.md#temporary-workspace-and-retention).

## Supported Markdown links

Distributed Markdown uses single-line `[text](path)` links or `![description](path)` images. Local targets are plain paths relative to the source document, optionally followed by `#fragment`, and resolve to real files or directories within the payload. Put Markdown examples in inline code or fenced code blocks.
