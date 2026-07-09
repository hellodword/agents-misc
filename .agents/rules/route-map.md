---
id: route-map
kind: index
triggers:
  - route
  - routing
  - rules
  - context loading
summary: Map task types to the smallest relevant shared rule files.
companions: []
---

# Agent Rule Route Map

Load only the smallest relevant rule files. Do not preload the full `.agents/rules/` tree.

When a route points to a rule path, read that file before editing. Do not infer unseen rule contents from the route label.

Companion metadata in rules is advisory and non-recursive. Prefer this route map for common multi-rule bundles.

## Project overlay first

Before using this shared route map, discover `.project-agent/route-map.md` by path existence. When it exists and routing is needed, read it before this file.

Project-local routing, contracts, architecture docs, and mandatory rules override shared default routing only within their declared scope and only below safety invariants.

Use shared routing only for reusable defaults and cross-project workflows.

## Rule bundles

Use this matrix after project-local routing. Load conditional rules only when their condition applies.

| Task                                                                                   | Initial shared rules                                                                                                                                               | Conditional rules                                                                                                                                                                                                                                                                                               | Skills, templates, and references                                                                                                                                                                                                         |
| -------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| API, config, schema, protocol, FFI, or public contract change                          | `.agents/rules/core/config-schema-protocol-api.md`; `.agents/rules/core/compatibility.md`; `.agents/rules/core/testing.md`                                         | `.agents/rules/stacks/backend-api.md` for HTTP/server behavior; `.agents/rules/core/security.md` for auth, permission, untrusted input, external access, or security boundaries; `.agents/rules/core/data-privacy.md` for PII, user data, telemetry, retention, export, deletion, or privacy-sensitive behavior | `.agents/skills/compatibility-review/SKILL.md` when compatibility risk is non-trivial; contract templates only when producing those artifacts                                                                                             |
| SQLite schema, migration, backup, import, export, or reset behavior                    | `.agents/rules/core/data-migrations.md`; `.agents/rules/core/compatibility.md`; `.agents/rules/stacks/database-sqlite.md`; `.agents/rules/core/testing.md`         | `.agents/rules/core/backup-import-export.md` for backup, restore, import, export, or user-owned data recovery                                                                                                                                                                                                   | `.agents/skills/sqlite-migration-backup/SKILL.md` when workflow guidance is needed; `.agents/templates/migration-plan.md` only when producing a migration plan                                                                            |
| Dependency addition, package manager, lockfile, vendoring, or third-party asset change | `.agents/rules/core/dependencies.md`; `.agents/rules/core/licensing.md`                                                                                            | `.agents/rules/core/security.md` for install scripts, telemetry, binary downloads, native extensions, supply-chain risk, or secret-bearing workflows; relevant stack/toolchain rule                                                                                                                             | relevant package manager, stack, or toolchain docs only when needed to verify current facts                                                                                                                                               |
| CLI behavior, flags, args, stdout, stderr, exit codes, config, or env vars             | `.agents/rules/project-types/cli.md`; `.agents/rules/core/cli-stability.md`; `.agents/rules/core/compatibility.md`; `.agents/rules/core/testing.md`                | stack rule for implementation language; `.agents/rules/core/config-schema-protocol-api.md` when CLI config is durable                                                                                                                                                                                           | `.agents/skills/cli-contract/SKILL.md` and `.agents/templates/cli-contract.md` only when defining or updating a CLI contract                                                                                                              |
| UI behavior, copy, layout, i18n, a11y, or responsive behavior                          | `.agents/rules/core/ui-ux-i18n-a11y.md`; relevant frontend stack rule; `.agents/rules/core/testing.md`                                                             | `.agents/rules/toolchain/browser-e2e.md` when browser validation is useful; `.agents/rules/core/data-privacy.md` when user data is displayed or collected                                                                                                                                                       | `.agents/skills/browser-e2e/SKILL.md` only for reusable browser workflow; `.agents/templates/i18n-a11y-checklist.md` only when producing a checklist                                                                                      |
| Explicit visual review, screenshot review, visual QA, or AI image editing              | `.agents/rules/toolchain/ai-visual-review.md`; `.agents/rules/toolchain/agent-tool-adapters.md`; relevant UI/frontend rule                                         | `.agents/rules/toolchain/browser-e2e.md` when screenshots must be captured through browser automation                                                                                                                                                                                                           | `.agents/skills/ai-visual-review/SKILL.md`; visual-review templates only when producing structured findings, synthesis, or rubric artifacts                                                                                               |
| Commit requested or explicit auto-commit policy applies                                | `.agents/rules/core/git.md`; `.agents/rules/core/repo-hygiene.md`                                                                                                  | `.agents/rules/core/generated-artifacts.md` when generated files may be staged; relevant task rules for commit boundary                                                                                                                                                                                         | `.agents/skills/atomic-commit/SKILL.md`; `.agents/skills/repo-hygiene-review/SKILL.md` when staging risk is non-trivial                                                                                                                   |
| Shared ruleset maintenance                                                             | `.agents/rules/core/agent-rules-authoring.md`; `.agents/rules/core/repo-hygiene.md`                                                                                | `.agents/rules/core/generated-artifacts.md` when schemas/templates/generated artifacts change                                                                                                                                                                                                                   | affected templates, references, and `.agents/evals/**`; generated-artifacts review skill when useful                                                                                                                                      |
| Nix, Just, flake outputs, treefmt, or command workflow changes                         | `.agents/rules/toolchain/nix.md`; `.agents/rules/toolchain/formatting.md` when formatting is involved; `.agents/rules/core/scripts.md` when durable scripts change | relevant stack rule for tools added to dev shells; `.agents/rules/core/dependencies.md` when adding packages or flake inputs                                                                                                                                                                                    | `.agents/skills/nix-workflow/SKILL.md` when adding or restructuring durable commands; `.agents/references/nixpkgs-devcontainer-alignment.md` before initializing/updating nixpkgs; `.agents/references/nix-layout.md` for longer examples |
| Pure Nix project behavior                                                              | `.agents/rules/project-types/pure-nix.md`; `.agents/rules/toolchain/nix.md`                                                                                        | `.agents/rules/core/compatibility.md` when changing public flake outputs; `.agents/rules/core/scripts.md` when adding imperative orchestration                                                                                                                                                                  | `.agents/skills/nix-workflow/SKILL.md`; `.agents/references/nix-layout.md` for output/layout examples                                                                                                                                     |
| Pure patch workflow                                                                    | `.agents/rules/project-types/pure-patch.md`; `.agents/rules/toolchain/nix.md`; `.agents/rules/core/repo-hygiene.md`                                                | `.agents/rules/core/scripts.md` when patch scripts change; `.agents/rules/core/licensing.md` when upstream license handling changes                                                                                                                                                                             | `.agents/skills/pure-patch-workflow/SKILL.md`; `.agents/references/nixpkgs-devcontainer-alignment.md`                                                                                                                                     |

## Project type

- Full-stack Go backend + TypeScript frontend: `.agents/rules/project-types/fullstack-go-web.md`
- CLI project: `.agents/rules/project-types/cli.md`
- Frontend-only project: `.agents/rules/project-types/frontend-only.md`
- Cross-platform client with Flutter + Rust bridge: `.agents/rules/project-types/flutter-rust-bridge.md`
- Pure patch project: `.agents/rules/project-types/pure-patch.md`
- Pure Nix project: `.agents/rules/project-types/pure-nix.md`

## Core concerns

- Work planning and task slicing: `.agents/rules/core/working-model.md`
- Environment and devcontainer limits: `.agents/rules/core/environment.md`
- Git, commit policy, and commit boundaries: `.agents/rules/core/git.md`
- Repo hygiene: `.agents/rules/core/repo-hygiene.md`
- Agent rules authoring: `.agents/rules/core/agent-rules-authoring.md`
- Architecture: `.agents/rules/core/architecture.md`
- Code quality: `.agents/rules/core/code-quality.md`
- Testing and validation selection: `.agents/rules/core/testing.md`
- Security: `.agents/rules/core/security.md`
- Dependencies: `.agents/rules/core/dependencies.md`
- Licensing: `.agents/rules/core/licensing.md`
- Generated artifacts: `.agents/rules/core/generated-artifacts.md`
- Compatibility: `.agents/rules/core/compatibility.md`
- Data migrations: `.agents/rules/core/data-migrations.md`
- Config, schema, protocol, API contracts: `.agents/rules/core/config-schema-protocol-api.md`
- UI/UX, i18n, a11y: `.agents/rules/core/ui-ux-i18n-a11y.md`
- Time, locale, Unicode: `.agents/rules/core/time-locale-unicode.md`
- Backup, import, export: `.agents/rules/core/backup-import-export.md`
- CLI stability: `.agents/rules/core/cli-stability.md`
- Observability: `.agents/rules/core/observability.md`
- Performance: `.agents/rules/core/performance.md`
- Data and privacy: `.agents/rules/core/data-privacy.md`
- Repository scripts: `.agents/rules/core/scripts.md`

## Toolchain

- Nix, Just, flake outputs, and treefmt: `.agents/rules/toolchain/nix.md`
- Codex, OpenCode, and generic agent tool adapters: `.agents/rules/toolchain/agent-tool-adapters.md`
- Command discovery: `.agents/rules/toolchain/command-discovery.md`
- Formatting: `.agents/rules/toolchain/formatting.md`
- Browser E2E: `.agents/rules/toolchain/browser-e2e.md`
- AI visual review: `.agents/rules/toolchain/ai-visual-review.md`
- Playwright MCP: `.agents/rules/toolchain/playwright-mcp.md`
- GitHub Actions: `.agents/rules/toolchain/github-actions.md`

## Stacks

- Frontend TypeScript: `.agents/rules/stacks/frontend-typescript.md`
- shadcn React: `.agents/rules/stacks/shadcn-react.md`
- Vue: `.agents/rules/stacks/vue.md`
- Backend API: `.agents/rules/stacks/backend-api.md`
- SQLite: `.agents/rules/stacks/database-sqlite.md`
- Go: `.agents/rules/stacks/go.md`
- Rust: `.agents/rules/stacks/rust.md`
- Flutter: `.agents/rules/stacks/flutter.md`
- Python CLI fallback: `.agents/rules/stacks/python-cli.md`
- Node.js CLI fallback: `.agents/rules/stacks/nodejs-cli.md`

## References

- Project overlay structure and lock format: `.agents/references/project-overlay.md`
- Route examples: `.agents/references/route-examples.md`
- Agent tool adapter command shapes: `.agents/references/agent-tool-adapter-examples.md`
- Nixpkgs and devcontainer alignment: `.agents/references/nixpkgs-devcontainer-alignment.md`
- Nix layout and output examples: `.agents/references/nix-layout.md`
