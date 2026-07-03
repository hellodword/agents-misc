# Agent Rules

## Priority

User instructions override this file.

Existing local project conventions override generic defaults when they are clear, working, and local to the touched area.

Task-specific loaded rules override root defaults for their scope.

When rules conflict, choose the narrower, safer, and more local rule.

Do not infer the contents of an unseen referenced rule from its path or name alone.

## Context Loading

Read this file first.

Do not preload `.agents/rules/**`, `.agents/skills/**`, `.agents/templates/**`, or `.agents/references/**`.

For each task:

1. Identify the task type and the touched area.
2. If the route is obvious, read only the smallest matching rule files.
3. If the route is unclear, read `.agents/rules/route-map.md`.
4. Load at most the relevant project-type rule, stack rule, core concern rule, and toolchain rule before editing.
5. When a referenced rule path is relevant to the task, use the file-read tool to open that file before making changes.
6. Load skills only when a reusable workflow is needed.
7. Load templates only when producing that artifact.
8. Load references only when a rule or skill explicitly points to them.

## Universal Safety

Do not commit secrets, user data, local databases, logs, coverage, screenshots, browser profiles, machine-specific files, or temporary artifacts.

Do not run global installs, host package managers, curl/wget-to-shell installers, or system-level environment changes.

Do not use `git add .`, `git add -A`, `git add --all`, or equivalent bulk staging.

Prefer narrow validation for the behavior touched by the task.

Do not weaken, split, or delete tests just to hide failures.

Verify that large docs, fixtures, tests, generated files, snapshots, or dependencies are the smallest useful scope. Ask the user only when the action is destructive, irreversible, security-sensitive, costly, or changes public behavior beyond the request.

## Environment Defaults

Assume development usually happens inside a VS Code devcontainer.

Do not read or write `.vscode/**` or `.devcontainer/**` by default. If the user explicitly asks for editor or container diagnostics, read-only access is allowed.

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

For routing, use `.agents/rules/route-map.md`.
