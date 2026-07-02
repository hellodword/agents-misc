# Agent Rules

## Scope

This repository uses a compact root `AGENTS.md` plus on-demand rules, skills, templates, and references under `.agents/`.

Read this file first. Do not preload `.agents/rules/**`, `.agents/skills/**`, `.agents/templates/**`, or `.agents/references/**`.

User instructions override these defaults. Existing project conventions override generic defaults when they are clear, working, and local to the touched area.

## Context Loading

Load only the smallest matching rule files for the current task.

Use `.agents/rules/route-map.md` only when routing is unclear.

Common routes:

- Full-stack Go + web frontend work: `.agents/rules/project-types/fullstack-go-web.md`
- CLI work: `.agents/rules/project-types/cli.md`
- Frontend-only work: `.agents/rules/project-types/frontend-only.md`
- Flutter + Rust bridge work: `.agents/rules/project-types/flutter-rust-bridge.md`
- Pure patch work: `.agents/rules/project-types/pure-patch.md`
- Nix/Just work: `.agents/rules/toolchain/nix-just.md`
- Command discovery work: `.agents/rules/toolchain/command-discovery.md`
- Browser E2E work: `.agents/rules/toolchain/browser-e2e.md`
- AI visual review work: `.agents/rules/toolchain/ai-visual-review.md`
- Playwright MCP work: `.agents/rules/toolchain/playwright-mcp.md`
- GitHub Actions work: `.agents/rules/toolchain/github-actions.md`
- Frontend TypeScript work: `.agents/rules/stacks/frontend-typescript.md`
- shadcn/React work: `.agents/rules/stacks/shadcn-react.md`
- Vue work: `.agents/rules/stacks/vue.md`
- Go work: `.agents/rules/stacks/go.md`
- Rust work: `.agents/rules/stacks/rust.md`
- Flutter work: `.agents/rules/stacks/flutter.md`
- SQLite work: `.agents/rules/stacks/database-sqlite.md`
- Compatibility-sensitive work: `.agents/rules/core/compatibility.md`
- Migration work: `.agents/rules/core/data-migrations.md`
- Generated artifact work: `.agents/rules/core/generated-artifacts.md`
- Script work: `.agents/rules/core/scripts.md`
- Security-sensitive work: `.agents/rules/core/security.md`
- Testing work: `.agents/rules/core/testing.md`
- UI/UX/i18n/a11y work: `.agents/rules/core/ui-ux-i18n-a11y.md`

Use skills under `.agents/skills/**/SKILL.md` for reusable workflows, not for always-on rules.

## Environment Defaults

Assume development usually happens inside a VS Code devcontainer.

Do not read or write `.vscode/**` or `.devcontainer/**` by default. If the user explicitly asks for editor or container diagnostics, read-only access is allowed.

Treat `.vscode/**` and `.devcontainer/**` as local environment files and keep them ignored by Git.

Do not rely on privileged containers, KVM, host browser policy, host package managers, systemd, Docker daemon access, GPU access, USB devices, Android emulator access, or extra kernel capabilities unless the project already proves they are available.

Do not perform system-level or global environment changes.

Do not run global installs, host package managers, or curl/wget-to-shell installers.

## Git Defaults

The default branch for new repositories is `master`. For existing repositories, detect and preserve the current default branch.

Commit is the smallest unit of agent progress. After each independent, verifiable, semantically complete task, create a non-interactive commit unless the user or current mode forbids commits.

Never run `git add .`, `git add -A`, `git add --all`, or equivalent bulk staging.

Stage only explicit file paths that belong to the current task.

Never stage ignored paths.

Use Conventional Commit headers with only these types:

- `feat`
- `fix`
- `chore`
- `docs`
- `refactor`
- `test`

Use `type(scope): subject` or `type: subject`.

The subject must be an English imperative phrase and must not end with a period.

A multi-line commit message is allowed. Use the body for key changes, validation, migrations, generated artifacts, or documentation sync.

If `$AI_COMMIT_COAUTHOR` is non-blank, append `Co-authored-by: $AI_COMMIT_COAUTHOR` as the final line.

## Toolchain Defaults

Prefer `flake.nix` and `justfile`.

Default Nix system: `x86_64-linux`.

Default nixpkgs input: `github:NixOS/nixpkgs/nixos-unstable`.

Use `just` as the outer command convenience layer. The `just` executable itself is a bootstrap tool and is not required to be provided by this repository's `flake.nix`.

Stable repeated commands should be exposed as just recipes. Those recipes should call Nix.

For one-off project commands, use `nix develop .#<env> --command <command> ...`.

When flake source tracking hides newly created files, use `nix develop path:$PWD#<env> --command <command> ...`.

If a required project tool is missing from `flake.nix` and edits are allowed, update `flake.nix` before retrying. If edits are not allowed, stop and report the missing package.

Keep just recipes simple. Move branching, loops, parsing, retries, cleanup orchestration, and long shell logic into project scripts.

## Product Defaults

Prefer SQLite for default/local persistence.

Prefer schema, protocol, and API documentation before implementation when behavior crosses module, process, storage, FFI, or network boundaries.

Use Markdown contract first for API documentation.

YAML preference applies only to project-developed application configuration file design when multiple formats are equally valid. It does not apply to toolchain-defined configuration files.

Prefer MIT license for new non-patch projects.

Prefer internationalized UI by default with English and Simplified Chinese.

For AI-assisted frontend work, prefer TypeScript and the current shadcn ecosystem when it fits the product. Do not force Vue or Vite when shadcn/React/Next/TanStack/Vite best practices are a better fit.

For new frontend framework selection:

- use React + Vite + shadcn/ui for SPA-style product UI;
- use Next.js + shadcn/ui for SSR, SEO, App Router, or server-integrated React apps;
- use a framework such as TanStack Start only when its routing/data model clearly fits the project;
- keep Vue when the project already uses Vue or the user asks for Vue.

For Vue projects, use `vue-i18n` by default unless the project already uses a different i18n approach.

Prefer Go backend + TypeScript frontend for full-stack web projects.

Prefer Go or Rust for CLI projects. Use Python or Node.js for CLI projects when ecosystem fit strongly favors them.

For Python CLI projects, use `uv` with a root `.venv/`.

For Node.js CLI projects, default to npm and a bundled JavaScript CLI artifact. Use native single executable packaging only when the user explicitly requires running without Node.js.

Prefer Flutter + Rust bridge for cross-platform client projects when native/system/performance logic benefits from Rust.

Flutter projects do not include web support by default and do not assume Android emulator availability by default.

Do not add deployment, release, publishing, cloud auth, or GitHub Actions unless the user explicitly asks.

## Lockfiles

Commit lockfiles by default for application and CLI projects, including `flake.lock`, `package-lock.json`, `Cargo.lock`, `uv.lock`, and `pubspec.lock`.

For library packages, follow ecosystem convention and existing repository policy.

## Repository Hygiene

New files must have a long-term home.

Temporary drafts, command output, run logs, screenshots, browser traces, coverage, databases, archives, local upstream checkouts, and generated experiments go under ignored paths such as `tmp/` or `.work/`.

Do not commit real tokens, secrets, deployment config, user uploads, local databases, logs, coverage output, or machine-specific files.

Before adding large docs, fixtures, tests, generated files, snapshots, or dependencies, confirm they are the smallest useful scope for the current task.

## Validation

Prefer narrow tests that cover the behavior touched by this task.

Escalate to broader checks only when shared contracts, cross-module behavior, database migrations, public APIs, security boundaries, FFI boundaries, generated artifacts, or user workflows are affected.

For Go code involving concurrency, shared mutable state, HTTP handlers, background workers, caches, database access, or cancellation, include a narrow `go test -race` validation when practical.

Do not weaken, split, or delete tests just to bypass failures.

Treat failures as implementation issues, unclear contracts, environment blockers, or tests that need legitimate correction.

## Formatting

Use project formatting rules.

Default formatters:

- Go: `gofmt`
- Rust: `cargo fmt`
- Dart/Flutter: `dart format`
- JSON, JSONC, Markdown, HTML, YAML, JavaScript, TypeScript, Vue: Prettier

Run narrow formatting on touched files. Do not run repository-wide formatting unless the task is formatting-focused or the project already requires it.
