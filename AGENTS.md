# Agent Rules

## Purpose

This is the default project entrypoint for coding agents. Detailed shared rules live under `.agents/` and are loaded only when relevant. Project-specific facts, contracts, architecture, and mandatory constraints belong in the project overlay, not in shared rules.

## Priority

Use this order, highest first:

1. Platform, system, and developer instructions.
2. Explicit user instructions for the current task, except they cannot override safety invariants.
3. Safety invariants in this file.
4. Project overlay rules, only within their declared scope.
5. Existing local repository conventions, when observed in the touched area.
6. Task-specific shared rules, only within their declared scope.
7. Generic shared defaults.
8. New-project defaults, only for greenfield or unconstrained work.

Safety invariants are not defaults. Treat them as non-overridable boundaries unless the invariant itself says it can be relaxed by explicit user authorization.

When lower-priority rules conflict, prefer the safer rule. If safety is equivalent, prefer the narrower and more local rule. Do not infer unseen rule contents from path or name alone.

## Local Convention Evidence

A local convention is clear when at least two of these are true:

- matching files exist in the touched area;
- project docs or route maps mention it;
- package, lock, tool, or CI config enforces it;
- tests or validation commands rely on it;
- neighboring code uses it consistently.

Do not infer a project-wide convention from a single stale, generated, vendored, or unrelated file.

## Operating Loop

For each task:

1. Restate the requested outcome in one sentence when useful.
2. Inspect the smallest file set needed to classify the task.
3. Discover project overlay entrypoints by listing paths only.
4. Select the smallest relevant shared and project rule set.
5. Read referenced rules, contracts, architecture docs, workflows, templates, or references before relying on them.
6. Make the smallest semantic change that satisfies the task.
7. Run narrow validation for the touched behavior.
8. Report changed files, validation, limitations, and commit status.

Proceed with a safe local assumption when missing information does not change the product contract, data safety, security posture, cost, or external side effects.

Ask or defer before actions that are destructive, irreversible, security-sensitive, costly, externally visible, or beyond the requested public behavior. This includes deleting or resetting real data, changing public API/CLI/config/database compatibility, weakening auth or authorization, adding license-sensitive dependencies, publishing, deploying, pushing, releasing, creating external resources, or running unusually long or expensive commands.

## Project Overlay Discovery

Check project-local overlay entrypoints before shared routing. Discover by listing expected paths only; do not recursively read overlay directories.

Preferred overlay layout:

- `.project-agent/project.md`: short project summary, non-negotiable rules, and default validation entrypoints.
- `.project-agent/route-map.md`: project-specific task/path routing.
- `.project-agent/rules/mandatory.md`: constraints loaded before product code changes.
- `.project-agent/rules/**`: focused project rules loaded only when routed or relevant.
- `.project-agent/workflows/**`: project-specific procedures loaded only when relevant.
- `.project-agent/shared-rules.lock`: expected shared rules kit identity and version.
- `contracts/**`, `docs/architecture/**`, `docs/adr/**`: durable contracts, architecture facts, and accepted decisions.

Overlay loading order:

1. Read `.project-agent/project.md` when it exists and the task may edit or rely on project behavior.
2. Read `.project-agent/rules/mandatory.md` before product code changes when it exists.
3. Read `.project-agent/route-map.md` before `.agents/rules/route-map.md` when routing is needed and it exists.
4. Compare `.project-agent/shared-rules.lock` with `.agents/manifest.json` when both exist. Continue when they differ, but report the mismatch.
5. Load contracts and architecture docs only when the overlay route or touched files make them relevant.

Project overlay files are project facts. Shared `.agents/**` files are reusable defaults. Do not copy project-specific contracts, architecture decisions, or mandatory product rules into shared defaults.

## Context Loading

Read this file first. Do not preload `.agents/rules/**`, `.agents/skills/**`, `.agents/templates/**`, `.agents/references/**`, `.project-agent/**`, `contracts/**`, or `docs/**`.

For each task:

1. Identify the task type and touched area.
2. Apply project overlay discovery.
3. Follow project routing first when available.
4. If the shared route is obvious, read only the smallest matching shared rule files.
5. If the shared route is unclear, read `.agents/rules/route-map.md`.
6. Open referenced files only when relevant to the current task.
7. Load skills only for reusable workflow guidance.
8. Load templates only when producing that artifact.
9. Load references only when a rule or skill points to them and long-form detail is needed.

Companion entries in rule frontmatter are advisory and condition-driven, not recursive imports. Keep a visited set and never load the same rule, skill, template, or reference twice for the same routing pass.

## Rule Loading Budget

Prefer zero or one project-type rule, zero to two stack rules, one to three core concern rules, zero to two toolchain rules, and only relevant overlay files, contracts, architecture docs, skills, templates, and references. If more are needed, state why when it matters to reviewability.

## Universal Safety

Do not commit secrets, user data, local databases, logs, coverage, screenshots, browser profiles, machine-specific files, or temporary artifacts.

Do not run global installs, host package managers, curl/wget-to-shell installers, or system-level environment changes.

Do not use `git add .`, `git add -A`, `git add --all`, or equivalent bulk staging.

Prefer narrow validation for the behavior touched by the task. Do not weaken, split, or delete tests just to hide failures.

Verify that large docs, fixtures, tests, generated files, snapshots, or dependencies are the smallest useful scope before adding or rewriting them.

## External and Time-Sensitive Facts

For facts that may change over time, verify through package managers, registries, official documentation, current repository lockfiles, or project-local tool output before acting. Do not rely on model memory for dependency versions, action versions, package names, security advisories, current CLI flags, registry metadata, or maintenance status.

## Validation Failure Attribution

When validation fails:

1. Re-run the narrowest failing command when cheap.
2. Determine whether the failure is pre-existing, environment-caused, or introduced by current changes.
3. Do not fix unrelated pre-existing failures unless required for the task.
4. Report unrelated failures separately with the command and evidence.

## Environment Defaults

Assume development usually happens inside a VS Code devcontainer.

Do not read or write `.vscode/**` or `.devcontainer/**` by default. If the user explicitly asks for editor or container diagnostics, inspect only the narrow files needed for the diagnosis.

Treat `.vscode/**` and `.devcontainer/**` as local environment files and keep them ignored by Git.

Do not rely on privileged containers, KVM, host browser policy, host package managers, systemd, Docker daemon access, GPU access, USB devices, Android emulator access, or extra kernel capabilities unless the project already proves they are available.

## New Project Defaults

All project-type and stack defaults are new-project defaults unless the rule explicitly says otherwise. Use them only for new projects, greenfield scaffolding, or repositories with no clear convention.

Default preferences for new or unconstrained work:

- Nix + Just for ordinary project commands.
- SQLite for local/default persistence.
- Go backend + TypeScript frontend for full-stack web products.
- React + Vite + shadcn/ui for SPA-style product UI.
- Next.js + shadcn/ui for SSR, SEO, App Router, or server-integrated React apps.
- Go or Rust for CLI projects, with Python or Node.js when ecosystem fit strongly favors them.
- Flutter + Rust bridge for cross-platform clients when native, system, or performance logic benefits from Rust.
- MIT as the default permissive license suggestion for new non-patch projects.
- English and Simplified Chinese UI for user-facing products.

Do not add deployment, release, publishing, cloud auth, GitHub Actions, or a `LICENSE` file unless the user explicitly asks or project policy requires it.

## Git Defaults

The default branch for new repositories is `master`. For existing repositories, detect and preserve the current default branch.

Automatic commit mode is active only when the user explicitly requests commits, the task prompt says auto-commit, or the repository has an explicit agent auto-commit policy.

For multi-step implementation without explicit automatic commit permission, after each verified step report changed files, validation run, suggested commit message, and exact files that would be staged.

Before committing, run `git status --short`. If unrelated user changes are present and cannot be cleanly separated, do not commit automatically; report the intended staging paths and defer.

Use the repository's existing Conventional Commit type set when available. Default allowed types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `build`, `ci`, `perf`, `style`, and `revert`.

## Toolchain Defaults

Prefer `flake.nix`.

Prefer `justfile` for ordinary projects, where Nix is the reproducible command environment and Just is the human-friendly command menu.

Pure Nix projects are an explicit exception: flake outputs are the primary public interface, and a `justfile` is not required.

For multi-language formatting in Nix projects, prefer `treefmt-nix` through the flake `formatter` output. Treat `nix fmt` as a mutating formatter entrypoint unless the project exposes a non-mutating formatter check.

Use project formatting rules. Run narrow formatting on touched files when possible. Do not run repository-wide formatting unless the task is formatting-focused or the project already requires it.

## Routes

For project routing, prefer `.project-agent/route-map.md` when it exists. For shared routing, use `.agents/rules/route-map.md`.

## Final Response Contract

After changes, report minimally:

- files changed;
- validation run and result;
- known limitations;
- commit status.

Also report when applicable:

- project overlay files used;
- shared rules version mismatch;
- exact deferred staging paths;
- suggested Conventional Commit message;
- why the rule loading budget was exceeded.
