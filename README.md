# Agent Rules Kit

Agent Rules Kit is a small, project-scoped instruction payload for coding agents. It uses the open [AGENTS.md format](https://agents.md/) for shared instructions and the open [Agent Skills specification](https://agentskills.io/specification) for on-demand workflows.

## Repository map

This repository also maintains several independent Codex tools. Each component
has one authoritative input boundary and one primary verification entrypoint:

| Component                                                | Purpose                                                                   | Authoritative inputs                                            | Primary maintenance entry                                            |
| -------------------------------------------------------- | ------------------------------------------------------------------------- | --------------------------------------------------------------- | -------------------------------------------------------------------- |
| [Agent Rules Kit](AGENTS.md)                             | Shared agent rules and reusable workflow skills                           | `AGENTS.md`, `.agents/**`                                       | `just check-agent-rules`                                             |
| [Codex patches](codex/README.md)                         | Reproducible patches over one pinned upstream Codex revision              | `codex/upstream.toml`, `codex/series.toml`, current patch files | `just codex-test`                                                    |
| [Codex hook helpers](tools/codex-hooks/README.md)        | Bounded local forwarding and notification helpers for patched hook events | helper source and the patch-owned hook schemas                  | `nix build --no-link .#checks.x86_64-linux.codex-hooks`              |
| [Codex Config Atlas](tools/codex-config-atlas/README.md) | Versioned upstream schema registry, CLI, and static comparison site       | tracked schema registry and Web source                          | `nix build --no-link .#checks.x86_64-linux.codex-config-atlas-tests` |
| [Agents Viewer](tools/agents-viewer/README.md)           | Read-only local rollout index, API, and Web reader                        | rollout fixtures, Rust DTOs, Rust/Web source                    | `just agents-viewer-verify`                                          |

Generated ownership is explicit. Codex generated contracts live exclusively
inside the final patch in `codex/series.toml`; Atlas data and site trees are
Nix outputs; Viewer's `web/src/generated/api.ts` is the only checked-in
Rust-to-TypeScript binding. No other generated tree is a durable source input.

## Distribution boundary

Only [AGENTS.md](AGENTS.md) and `.agents/**` are distributed. Consuming repositories provide both at their root and normally mount or copy them read-only. The external distribution system chooses and records a Git commit, version, or tag; this repository does not maintain a payload version or consumer lock.

Repository maintenance files such as `.project-agent/**`, the checker, tests, Just recipes, and Nix checks are not part of the payload. The separately maintained `codex/**`, `tools/**`, and `.github/**` trees are outside the distributed rules-kit payload; their own guides define their contracts.

## Loading model

A consumer has one fixed overlay entrypoint: `.project-agent/project.md`. That file links requirements directly and states when they apply:

```md
# Project instructions

| When                       | Read                                  |
| -------------------------- | ------------------------------------- |
| Any tracked product change | [Mandatory rules](rules/mandatory.md) |
| Public API work            | [HTTP contract](../contracts/http.md) |
```

Agents read the project entrypoint, follow only the directly applicable links, then route shared guidance through [.agents/rules/index.md](.agents/rules/index.md). There is no recursive metadata or hidden dependency traversal.

## What belongs where

- A **rule** holds concise defaults or constraints for a recognizable technical boundary. Rules are flat Markdown files, routed directly from the index.
- A **skill** captures reusable practices that need task-specific guidance. Each `SKILL.md` uses only the required `name` and `description` frontmatter.
- A skill-local **asset** is a template or reusable source file consumed by that workflow.
- A skill-local **reference** is longer optional guidance that the workflow tells the agent when to read.

Keep supporting files beside their owning skill and link them directly from `SKILL.md`. Do not create global template or reference catalogs.

## Maintenance and validation

Read [.project-agent/project.md](.project-agent/project.md) before modifying the kit. Invoke Just from the repository root, outside a development shell; recipes enter the declared Nix environment and call tools directly.

For rules-kit changes, run `just check-agent-rules` or its equivalent Nix check:

```sh
nix build --no-link .#checks.x86_64-linux.agent-rules
```

The checker validates the distributed entrypoint, directory layout, skill metadata, index coverage, local links, and resource ownership. It scans every distributed Markdown file and rejects symlinks. Local references must remain inside `AGENTS.md` and `.agents/**`. It does not evaluate instruction quality. Its tests use a minimal synthetic payload and do not depend on the other repository components.

Use single-line `[text](path)` or `![description](path)` links with plain local paths and optional `#fragments`. Reference-style links and complex local targets (including spaces, parentheses, encoded paths, and link titles) are reported with their source line. Backtick inline code and backtick/tilde fenced examples are skipped. The checker does not fetch external URLs, match fragments to headings, or inspect native HTML links. It rejects `file:` URLs. The [maintenance rules](.project-agent/rules/maintenance.md) define the supported writing conventions.

Workflow validation is independent: use `just check-workflows` or `nix build --no-link .#checks.x86_64-linux.github-workflows` when changing that boundary. Choose the local entrypoint or the equivalent Nix check instead of repeating the same tests through both.

Format the changed files with the repository formatter. For Nix output or check wiring changes, evaluate the flake with `nix flake check --no-build --no-write-lock-file` and build the affected checks. Use a full `nix flake check --accept-flake-config` when the change needs the repository-wide delivery gate.

Review the final diff and `git status --short --ignored`. Keep task-created diagnostics out of the payload and clean them up. Preserve pre-existing ignored work.
