# Agent Rules

## Purpose

This file is the default project entrypoint for coding agents.

It intentionally stays small. Detailed shared rules live under `.agents/` and must be loaded only when relevant.

Project-specific facts, contracts, architecture, and mandatory constraints belong in the project overlay described below, not in the shared rule files.

## Priority

User instructions override this file.

Project overlay instructions override shared defaults when they are more specific and local to the touched area.

Existing local project conventions override generic defaults when they are clear, working, and local to the touched area.

Task-specific loaded rules override root defaults for their scope.

When rules conflict, choose the narrower, safer, and more local rule.

Do not infer the contents of an unseen referenced rule from its path or name alone.

## Operating Loop

For each task:

1. Restate the requested outcome in one sentence.
2. Inspect the smallest set of project files needed to classify the task.
3. Discover project overlay files.
4. Select the smallest relevant shared and project rule set.
5. Read referenced rule, contract, architecture, and workflow files before relying on them.
6. Make the smallest semantic change that satisfies the task.
7. Run narrow validation for the touched behavior.
8. Report changed files, validation, limitations, and commit status.

Do not stop for missing information when a safe local assumption allows progress.

Ask only when the action is destructive, irreversible, security-sensitive, costly, or changes public behavior beyond the request.

## Project Overlay Discovery

For each task, check for project-local overlay files before using shared routing.

Preferred overlay layout:

- `.project-agent/project.md`: short project summary, non-negotiable rules, and default validation entrypoints.
- `.project-agent/route-map.md`: project-specific task/path routing to rules, contracts, architecture docs, and validation commands.
- `.project-agent/rules/mandatory.md`: constraints that must be loaded before product code changes.
- `.project-agent/rules/**`: focused project rules.
- `.project-agent/workflows/**`: project-specific reusable procedures.
- `.project-agent/shared-rules.lock`: expected shared rules kit identity and version.
- `contracts/**`: durable product contracts.
- `docs/architecture/**`: architecture facts and boundaries.
- `docs/adr/**`: accepted architecture decisions.

Overlay loading order:

1. If `.project-agent/project.md` exists, read it before editing.
2. If `.project-agent/rules/mandatory.md` exists, read it before product code changes.
3. If `.project-agent/route-map.md` exists, use it before `.agents/rules/route-map.md`.
4. If `.project-agent/shared-rules.lock` and `.agents/manifest.json` both exist, compare their kit name, version, and schema values. Continue when they differ, but report the mismatch.
5. Load project contracts and architecture docs only when the overlay route or touched files make them relevant.

Project overlay files are project facts. Shared `.agents/**` files are reusable defaults. Do not copy project-specific contracts, architecture decisions, or mandatory product rules into shared defaults.

If no project overlay exists, continue with shared defaults and report that no project overlay was present when it matters to the result.

## Context Loading

Read this file first.

Do not preload `.agents/rules/**`, `.agents/skills/**`, `.agents/templates/**`, `.agents/references/**`, `.project-agent/**`, `contracts/**`, or `docs/**`.

For each task:

1. Identify the task type and the touched area.
2. Apply project overlay discovery.
3. If project routing is available, follow it first.
4. If the shared route is obvious, read only the smallest matching shared rule files.
5. If the shared route is unclear, read `.agents/rules/route-map.md`.
6. When a referenced rule, contract, architecture, workflow, template, or reference path is relevant to the task, use the file-read tool to open that file before making changes.
7. Load skills only when a reusable shared workflow is needed.
8. Load templates only when producing that artifact.
9. Load references only when a rule or skill explicitly points to them.

## Rule Loading Budget

Prefer:

- zero or one project-type rule;
- zero to two stack rules;
- one to three core concern rules;
- zero to two toolchain rules;
- project overlay files only when the touched area or overlay route requires them;
- contracts and architecture docs only when changing or relying on their behavior;
- skills only for reusable workflows;
- templates only when producing that artifact.

If more rules are needed, state why in the final report.

## Universal Safety

Do not commit secrets, user data, local databases, logs, coverage, screenshots, browser profiles, machine-specific files, or temporary artifacts.

Do not run global installs, host package managers, curl/wget-to-shell installers, or system-level environment changes.

Do not use `git add .`, `git add -A`, `git add --all`, or equivalent bulk staging.

Prefer narrow validation for the behavior touched by the task.

Do not weaken, split, or delete tests just to hide failures.

Verify that large docs, fixtures, tests, generated files, snapshots, or dependencies are the smallest useful scope. Ask the user only when the action is destructive, irreversible, security-sensitive, costly, or changes public behavior beyond the request.

## Environment Defaults

Assume development usually happens inside a VS Code devcontainer.

Do not read or write `.vscode/**` or `.devcontainer/**` by default. If the user explicitly asks for editor or container diagnostics, inspect only the narrow files needed for the diagnosis.

Treat `.vscode/**` and `.devcontainer/**` as local environment files and keep them ignored by Git.

Do not rely on privileged containers, KVM, host browser policy, host package managers, systemd, Docker daemon access, GPU access, USB devices, Android emulator access, or extra kernel capabilities unless the project already proves they are available.

## New Project Defaults

Use product and stack defaults only for new projects, greenfield scaffolding, or repositories with no clear convention.

Do not introduce a preferred stack into an existing project merely because it is listed as a default here.

Default preferences for new or unconstrained work:

- Nix + Just for ordinary project commands.
- SQLite for local/default persistence.
- Go backend + TypeScript frontend for full-stack web products.
- React + Vite + shadcn/ui for SPA-style product UI.
- Next.js + shadcn/ui for SSR, SEO, App Router, or server-integrated React apps.
- Go or Rust for CLI projects, with Python or Node.js when ecosystem fit strongly favors them.
- Flutter + Rust bridge for cross-platform clients when native, system, or performance logic benefits from Rust.
- MIT license for new non-patch projects.
- English and Simplified Chinese UI for user-facing products.

Do not add deployment, release, publishing, cloud auth, or GitHub Actions unless the user explicitly asks.

## Git Defaults

The default branch for new repositories is `master`. For existing repositories, detect and preserve the current default branch.

Automatic commit mode is active only when the user explicitly requests commits, the task prompt says auto-commit, or the repository has an explicit agent auto-commit policy.

For multi-step implementation without explicit automatic commit permission, after each verified step report the changed files, validation run, suggested commit message, and exact files that would be staged.

Before committing, run `git status --short`. If unrelated user changes are present and cannot be cleanly separated, do not commit automatically; report the intended staging paths and defer the commit.

Use Conventional Commit headers with only these types: `feat`, `fix`, `chore`, `docs`, `refactor`, and `test`.

## Toolchain Defaults

Prefer `flake.nix`.

Prefer `justfile` for ordinary projects, where Nix is the reproducible command environment and Just is the human-friendly command menu.

Pure Nix projects are an explicit exception: flake outputs are the primary public interface, and a `justfile` is not required.

Use project formatting rules. Run narrow formatting on touched files. Do not run repository-wide formatting unless the task is formatting-focused or the project already requires it.

## Routes

For project routing, prefer `.project-agent/route-map.md` when it exists.

For shared routing, use `.agents/rules/route-map.md`.

## Final Response Contract

After changes, report:

- files changed;
- validation commands run;
- validation result;
- known limitations;
- project overlay files used;
- shared rules version status when `.project-agent/shared-rules.lock` exists;
- commit mode;
- exact files that would be staged when commits are deferred;
- suggested Conventional Commit message.
