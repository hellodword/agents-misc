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

### Consumer projects

Consuming projects may provide `.project-agent/project.md` as a small, project-owned entrypoint. It is optional: projects without it use existing code, configuration, and documentation. Neither a README nor a fixed documentation tree is required.

An entry may state scoped requirements directly and link existing authoritative sources. For a project that already has imported sources and an API contract, an entry might contain:

```md
# Project context

Imported source snapshots are read-only. Apply changes through their owning import process.

For public API work, read the [HTTP contract](../contracts/http.md).
```

Agents read the entrypoint when present and follow task-relevant references through further project documents as needed for requirements or evidence. Preserve each requirement's scope, revisit guidance when work reaches a new boundary, and avoid bulk scans, cycles, or repeated reads. Shared rules and skills retain their explicit routing through [.agents/rules/index.md](.agents/rules/index.md).

Keep descriptive facts distinct from requirements. Read versions, dependency choices, and executable commands from their authoritative configuration or tooling instead of copying them into the entrypoint. A stale version summary does not override current configuration; an explicit compatibility requirement still constrains implementation when the two disagree.

Agents may create or maintain the entry when current work confirms useful, durable requirements, decisions, facts, or navigation. Temporary progress and investigations belong in task records. Project instructions may record established authorization; editing them does not itself authorize another operation or widen the specific exception sources in `AGENTS.md`.

The shared kit records personal preferences and operational boundaries. Prefer repository evidence and proportionate validation over generic tutorials, fixed procedure counts, or repeated approval requests. In the usual Nix-based devcontainer, an agent may add required tools or create a minimal project development shell. Additional Nix product outputs still require an explicit user or project choice.

### This repository

The local [.project-agent/project.md](.project-agent/project.md) covers all components in `agents-misc`. It reuses the existing [repository map](#repository-map) to locate component guides and applies the rules-kit maintenance instructions only to that boundary. Shared Just, Nix, and CI files use the guidance and validation of their affected consumers. This repository's README navigation is a local choice; consumers choose their own documentation layout.

## What belongs where

- A **rule** holds concise defaults or constraints for a recognizable technical boundary. Rules are flat Markdown files, routed directly from the index.
- A **skill** captures reusable practices that need task-specific guidance. Each `SKILL.md` uses only the required `name` and `description` frontmatter.
- A skill-local **asset** is a template or reusable source file consumed by that workflow.
- A skill-local **reference** is longer optional guidance that the workflow tells the agent when to read.

Keep supporting files beside their owning skill and link them directly from `SKILL.md`. Do not create global template or reference catalogs. Keep each substantive preference in one rule; skills supply workflow decisions instead of repeating those defaults.

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

Old entrypoints have no forwarding aliases. The kit supplies no dedicated SQLite migration or backup workflow; projects decide those methods. Database and library choices are preferences with justified alternatives, and dependency quality matters more than dependency count.

The long-task plan keeps completion evidence and authorization stable while allowing implementation choices to change. It records discoveries and continuation state, detects unproductive retries, and distinguishes incomplete or unverified work from completion. Starting a Goal or receiving an implementation task alone does not trigger planning.

## Maintenance and validation

Read [.project-agent/project.md](.project-agent/project.md) before modifying the kit. Invoke Just from the repository root; recipes enter their declared Nix environment as needed and may also be invoked from an entered shell.

For rules-kit changes, run `just check-agent-rules` or its equivalent Nix check:

```sh
nix build --no-link .#checks.x86_64-linux.agent-rules
```

The checker validates the distributed entrypoint, directory layout, skill metadata, index coverage, local links, and resource ownership. It scans every distributed Markdown file and rejects symlinks. Local references must remain inside `AGENTS.md` and `.agents/**`. It does not validate consumer overlay links, project document chains, or instruction semantics. Its tests use a minimal synthetic payload and do not depend on the other repository components.

Use the [supported Markdown link syntax](#supported-markdown-links) for distributed files. Review project navigation separately when changing it.

Workflow validation is independent: use `just check-workflows` or `nix build --no-link .#checks.x86_64-linux.github-workflows` when changing that boundary. Choose the local entrypoint or the equivalent Nix check instead of repeating the same tests through both.

Format the changed files with the repository formatter. For Nix output or check wiring changes, evaluate the flake with `nix flake check --no-build --no-write-lock-file` and build the affected checks. A full flake check is for changes that need the repository-wide delivery gate; routine rules-kit edits use the focused check above. Accept flake-provided configuration only under the applicable trust policy.

Review the final diff and `git status --short --ignored`. Keep task-created diagnostics out of the payload and follow the [temporary workspace and retention policy](AGENTS.md#temporary-workspace-and-retention).

## Supported Markdown links

These conventions apply to the distributed Markdown files scanned by `scripts/check-agent-rules.py`:

- Use single-line `[text](path)` links or `![description](path)` images. Local targets are plain paths, optionally followed by `#fragment`.
- Write paths directly, without whitespace, parentheses, angle brackets, quotes, backslash escapes, percent encoding, query strings, or link titles. Paths are relative to the source document; a leading `/` refers to the repository root. References must point to an existing distributed file or directory.
- Reference-style definitions, full references such as `[text][id]`, and collapsed references such as `[text][]` are errors; rewrite them as inline links.
- Put Markdown examples in backtick-delimited inline code or backtick/tilde fenced code blocks so they are excluded from link checking. Skill frontmatter is validated as metadata rather than scanned for links.
- External URLs are not fetched, fragment identifiers are not checked against headings, and native HTML links are outside the checker scope. `file:` URLs are rejected.
