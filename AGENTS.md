# Agent Rules

## Purpose and priority

This file is the shared entrypoint for coding agents. Shared rules and workflows live under `.agents/`. The [Agent Rules Kit license notice](.agents/LICENSE.agent-rules-kit) covers this shared payload, not the consuming project or independently added rules and skills.

Apply instructions in this order:

1. Platform, system, and developer instructions.
2. Explicit user instructions for the current task, within the safety and authorization boundaries below.
3. Safety and authorization rules in this file.
4. Explicit project requirements within their declared scope.
5. Established local repository conventions in the touched area.
6. Routed shared rules, then generic shared defaults.
7. Greenfield defaults, only when the repository or scoped subproject is new and unconstrained.

Higher priority wins; within one priority, narrower scope wins. Judge repository evidence by authority, relevance, and recency: a declared toolchain, lockfile, formatter config, CI command, or schema is authoritative for the behavior it owns. When an explicit requirement and current implementation diverge, investigate the consumer and owning configuration before treating the requirement as obsolete.

Make reversible implementation choices autonomously after inspecting the codebase. Ask the user only when an unresolved choice materially alters the requested outcome, compatibility obligations, or required authorization. Report material assumptions.

## Targeted loading

1. Read this file first, then establish project context from existing code, configuration, and documentation before changing or relying on project behavior. Maintain existing project documentation as part of authorized work when changes alter confirmed facts, decisions, or navigation.
2. Confirm at the start of a repository task that [.agents/rules/index.md](.agents/rules/index.md) has been read, and load the routes matching active work. Load additional routes when work enters a new boundary. Across context compactions in the same session, continue from the established routing summary when `.agents/` and active boundaries are unchanged.
3. Read a skill-local asset or reference when its `SKILL.md` directs you to. Read instruction files before relying on them rather than inferring rules from filenames.

## Engineering discipline

- Verify contracts, authoritative configuration, and changeable external facts (package names, versions, CLI flags, action majors, advisories) through official documentation, lockfiles, registries, or local tool output rather than memory.
- Validate touched boundaries from focused checks to broader project gates. Attribute failures to introduced changes, pre-existing defects, or environment limits with concrete evidence.
- **Architectural stop-loss:** When resource limits (memory, timeouts, concurrency), exclusion lists, or local workarounds are adjusted repeatedly across recent changes or within the task while cross-boundary failures continue, stop stacking local tweaks. Trace the end-to-end build graph, runtime telemetry, and component contracts to verify ownership boundaries before making further edits.

## Writing and content

- Describe current behavior and design directly in documentation, comments, and identifiers, integrating revisions into the owning sections. Include migration or compatibility notes only when readers or consumers need them to act accurately.
- Document reproducible mechanism or environment pitfalls and their causes directly; omit comparative "patch-history" narration against superseded or unchosen designs.
- In non-English technical writing, keep standard English engineering terms (`derivation`, `closure`, `cgroup`, `lease`, `schema`, `idempotency`, `backdoor`, `patch`, `bounding box`, etc.) intact where literal translation reduces precision.
- Design user-facing product interfaces around domain concepts and user tasks, placing file paths, protocol/storage settings, and low-level diagnostics in operator or advanced views rather than primary flows.

## Safety and authorization

- Never expose or commit secrets, credentials, private keys, real user data, or machine-private state. Keep local runtime state and temporary review captures out of commits; synthetic fixtures, visual baselines, and required generated sources may be tracked after verifying they contain no sensitive data.
- Destructive real-data work, breaking external or durable contracts, weakening authentication or authorization, adding dependencies with incompatible license obligations, publishing, deploying, pushing, releasing, or creating external resources requires explicit authorization for that exact operation (plus a verified recovery path for real data). New CI workflows, additional Nix product outputs, and third-party GitHub Actions follow their routed authorization rules.
- Never weaken, skip, or delete assertions merely to hide failures. Never mutate host or global state (`curl|sh` installers, host package managers, global installs) by default. Preserve tracked `.vscode/**` and `.devcontainer/**` configuration when relevant without adding unrequested machine-local editor or container files. Preserve unrelated user changes in the working tree.
- Treat dependency lockfiles (`Cargo.lock`, `flake.lock`, `package-lock.json`, and equivalents) as tool-generated data: update them exclusively through the repository's documented command or declared dependency tool, never via text editors, patches, or ad hoc scripts. If the declared command fails or is unavailable, diagnose or report the blocker; manual content edits are permitted only when an explicit project contract or official documentation published by the dependency-tool maintainer requires that exact operation for that lockfile.
- Task authorization includes ordinary reversible implementation, tests, documentation, project dev-shell tools, and necessary adaptations to existing CI. Reuse granted authorization within its stated scope without asking again.
- Create local commits only when the user requests it (which applies across follow-up turns and plans in the same session unless narrowed), the task enables auto-commit, an approved execution plan defines commit checkpoints, or project policy directly enables it. Stage explicit task file paths only (`git add -- <file>...`); never use `git add .`, `-A`, `--all`, or `-f`.
- When a Git-backed Nix flake must see new durable, non-secret, non-ignored files before committing, mark explicit file paths with `git add -N -- <file>...` (never directories, globs, or `-f`), leave intent-to-add in place, and disclose those paths in the report.

## Temporary workspace and retention

- Prefer the host harness's isolated session scratch directory when available. Place large build caches and heavy intermediate outputs in `$TMPDIR` or user cache directories outside the working tree.
- When tools or container mounts require workspace-relative paths, use an established ignored scratch directory or `tmp/agent/<id>/` after confirming `/tmp/` is untracked and ignored in root `.gitignore` (adding `/tmp/` to `.gitignore` is ordinary setup work when root `tmp/` contains no tracked files and obscures no user files).
- Remove task-owned temporary intermediates once their purpose ends while preserving active work, user files, shared caches, deliverables, and failure evidence needed for reproduction or recovery.

## Final report

Summarize changed files and user-visible behavior, validation commands and outcomes, known limitations or unverified boundaries, and commit status (including exact uncommitted `git add -N` paths when present).
