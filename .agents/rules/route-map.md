---
id: route-map
kind: index
triggers:
  - route
  - routing
  - shared rules
  - context loading
summary: Select the smallest shared rule set from concrete task and repository evidence.
companions: {}
---

# Agent Rule Route Map

Follow project routing first. Shared rules are reusable defaults below the project overlay and established local conventions.

Frontmatter triggers are search hints, not automatic matches. Select a rule from the task's intended behavior, touched paths, authoritative configuration, dependencies, and project docs. A generic word alone must not select a specialized tool or stack.

After selecting a rule, load its `required_rules` exactly one hop before acting. Evaluate other companion conditions without recursive loading.

## Exclusions that prevent common misrouting

- A generic database task does not imply SQLite; load SQLite only from explicit user intent or SQLite files, drivers, configuration, or documentation.
- A formatting task does not imply Nix; load Nix only when the project already uses Nix/Just or the task explicitly adds or changes them.
- A generic CI task does not imply GitHub Actions; load it only for `.github/workflows/**` or an explicit GitHub Actions request.
- A screenshot request does not imply browser E2E. Use browser E2E for browser behavior or durable flows, and AI visual review only for an explicit image-based review request.
- A generic schema mention does not decide between database migration, JSON/config/API contract, or generated-artifact rules; identify the boundary first.
- Generic import/export syntax does not imply data backup. Load backup/import/export only for persisted or user-owned data movement.
- A generic template does not imply a Nix flake template.
- Compiler, linker, feature, or test flags do not imply a CLI public contract.

## Deterministic task routes

| Evidence and intended behavior                                                                   | Initial rules                                                                       | Add only when condition is true                                                                       |
| ------------------------------------------------------------------------------------------------ | ----------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------- |
| Public API, config, JSON Schema, protocol, FFI, event, or generated contract changes             | `.agents/rules/core/config-schema-protocol-api.md`                                  | backend API, security, privacy, migration, CLI, or generated-artifact rules for the affected boundary |
| Persisted database schema, migration, reset, or data-shape change                                | `.agents/rules/core/data-migrations.md`, `.agents/rules/core/testing.md`            | SQLite only with SQLite evidence; backup/privacy rules for user data or destructive behavior          |
| SQLite query, connection, WAL, foreign-key, driver, or migration work                            | `.agents/rules/stacks/database-sqlite.md`                                           | migration, backup, compatibility, testing, and SQLite workflow companions by their conditions         |
| CLI command, argument, public flag, stdout/stderr, exit code, config, or environment contract    | `.agents/rules/project-types/cli.md`, `.agents/rules/core/cli-stability.md`         | selected implementation language, compatibility, testing, and CLI contract skill                      |
| Dependency, lockfile, vendored code, third-party source, or third-party asset                    | `.agents/rules/core/dependencies.md`, `.agents/rules/core/licensing.md`             | security for install scripts, telemetry, downloads, native code, or supply-chain risk                 |
| Authentication, authorization, secrets, permissions, untrusted input, uploads, or injection risk | `.agents/rules/core/security.md`                                                    | privacy for PII/user data and contract rules for public behavior                                      |
| Architecture, ownership, dependency direction, module boundary, or cross-component design        | `.agents/rules/core/architecture.md`                                                | contract, migration, security, or performance rules for changed boundaries                            |
| Refactor, error handling, maintainability, or pure domain logic                                  | `.agents/rules/core/code-quality.md`                                                | architecture or testing only when their boundary is affected                                          |
| Test creation, bug regression, failure attribution, race validation, or validation selection     | `.agents/rules/core/testing.md`                                                     | validation-selection skill when the boundary-to-test mapping is unclear                               |
| Git, commit, branch, staging, or status work                                                     | `.agents/rules/core/git.md`                                                         | atomic-commit only when commit mode is active; hygiene review for non-trivial file boundaries         |
| Environment, devcontainer, PATH, filesystem, permission, or missing-tool blocker                 | `.agents/rules/core/environment.md`, `.agents/rules/toolchain/command-discovery.md` | environment-troubleshooting skill; Nix only if already adopted or explicitly requested                |
| Nix, Just, flake input/output, dev shell, Nix check, or treefmt-nix work                         | `.agents/rules/toolchain/nix.md`                                                    | formatting, scripts, dependencies, nix-workflow, and Nix references by condition                      |
| Formatting with an existing formatter, or greenfield formatter setup                             | `.agents/rules/toolchain/formatting.md`                                             | Nix only when already adopted or explicitly requested                                                 |
| Browser-specific behavior or a durable browser user flow                                         | `.agents/rules/toolchain/browser-e2e.md`, `.agents/rules/core/testing.md`           | environment rule when browser/display/container capability blocks execution                           |
| Explicit screenshot review, visual QA, design critique, or screenshot-based review mockup/edit   | `.agents/rules/toolchain/ai-visual-review.md`                                       | its required adapter rule and visual-review skill/templates                                           |
| Explicit additional-agent workflow or platform-required Codex/OpenCode adapter use               | `.agents/rules/toolchain/agent-tool-adapters.md`                                    | adapter reference when verified command shapes are needed                                             |
| `.github/workflows/**` or explicit GitHub Actions request                                        | `.agents/rules/toolchain/github-actions.md`                                         | security, dependencies, and testing for the workflow behavior                                         |
| Performance, benchmark, cache, latency, memory, pagination, or streaming change                  | `.agents/rules/core/performance.md`                                                 | relevant stack/testing rules for the measured boundary                                                |
| Logging, tracing, metrics, diagnostics, or error context                                         | `.agents/rules/core/observability.md`                                               | security/privacy when logged context may be sensitive                                                 |
| UI behavior, accessibility, responsive behavior, copy, locale, time formatting, or i18n          | `.agents/rules/core/ui-ux-i18n-a11y.md`                                             | frontend stack, time/locale, component testing, or E2E only for touched behavior                      |
| Generated bindings, clients, SQLx metadata, parser output, snapshots, or generated source files  | `.agents/rules/core/generated-artifacts.md`                                         | generated-artifacts-review skill and consumer validation                                              |
| Backup, restore, database import/export, or destructive recovery                                 | `.agents/rules/core/backup-import-export.md`                                        | migration, privacy, compatibility, and SQLite workflow as applicable                                  |
| Repository hygiene, temporary artifacts, fixtures, snapshots, or large files                     | `.agents/rules/core/repo-hygiene.md`                                                | generated-artifact or hygiene-review workflow when classification is non-trivial                      |
| Durable repository automation or scripts                                                         | `.agents/rules/core/scripts.md`                                                     | Nix/dependency rules only if the project uses them or adds third-party script dependencies            |
| Standalone, copyable, unattended final execution plan explicitly requested                       | `.agents/rules/core/working-model.md`                                               | standalone-final-execution-plan skill and template                                                    |
| Pure Nix product repository                                                                      | `.agents/rules/project-types/pure-nix.md`                                           | its required Nix rule and conditional compatibility/scripts guidance                                  |
| Pure patch repository or upstream patch series                                                   | `.agents/rules/project-types/pure-patch.md`                                         | pure-patch workflow and routed upstream/Nix references                                                |

## Project-type rules

- CLI: `.agents/rules/project-types/cli.md`
- Flutter + Rust bridge: `.agents/rules/project-types/flutter-rust-bridge.md`
- Frontend only: `.agents/rules/project-types/frontend-only.md`
- Full-stack Go web: `.agents/rules/project-types/fullstack-go-web.md`
- Pure Nix: `.agents/rules/project-types/pure-nix.md`
- Pure patch: `.agents/rules/project-types/pure-patch.md`

Load a project-type rule only when the repository or touched subproject clearly matches it. Do not load every project type represented in a monorepo.

## Stack rules

- Backend API: `.agents/rules/stacks/backend-api.md`
- SQLite: `.agents/rules/stacks/database-sqlite.md`
- Flutter: `.agents/rules/stacks/flutter.md`
- Frontend TypeScript: `.agents/rules/stacks/frontend-typescript.md`
- Go: `.agents/rules/stacks/go.md`
- Node.js CLI: `.agents/rules/stacks/nodejs-cli.md`
- Python CLI: `.agents/rules/stacks/python-cli.md`
- Rust: `.agents/rules/stacks/rust.md`
- shadcn React: `.agents/rules/stacks/shadcn-react.md`
- Vue: `.agents/rules/stacks/vue.md`

Load only stack rules for the touched area. If a greenfield language or framework choice remains unresolved after repository and user evidence, ask the user.

## Remaining core rules

- Compatibility: `.agents/rules/core/compatibility.md`
- Data privacy: `.agents/rules/core/data-privacy.md`
- Licensing: `.agents/rules/core/licensing.md`
- Time, locale, and Unicode: `.agents/rules/core/time-locale-unicode.md`

## Toolchain rules

- Agent tool adapters: `.agents/rules/toolchain/agent-tool-adapters.md`
- AI visual review: `.agents/rules/toolchain/ai-visual-review.md`
- Browser E2E: `.agents/rules/toolchain/browser-e2e.md`
- Command discovery: `.agents/rules/toolchain/command-discovery.md`
- Formatting: `.agents/rules/toolchain/formatting.md`
- GitHub Actions: `.agents/rules/toolchain/github-actions.md`
- Nix and Just: `.agents/rules/toolchain/nix.md`

## Skills

- AI visual review: `.agents/skills/ai-visual-review/SKILL.md`
- Atomic commit: `.agents/skills/atomic-commit/SKILL.md`
- Browser E2E: `.agents/skills/browser-e2e/SKILL.md`
- CLI contract: `.agents/skills/cli-contract/SKILL.md`
- Compatibility review: `.agents/skills/compatibility-review/SKILL.md`
- Environment troubleshooting: `.agents/skills/environment-troubleshooting/SKILL.md`
- Generated artifacts review: `.agents/skills/generated-artifacts-review/SKILL.md`
- Nix workflow: `.agents/skills/nix-workflow/SKILL.md`
- Pure patch workflow: `.agents/skills/pure-patch-workflow/SKILL.md`
- Repository hygiene review: `.agents/skills/repo-hygiene-review/SKILL.md`
- SQLite migration and backup: `.agents/skills/sqlite-migration-backup/SKILL.md`
- Standalone final execution plan: `.agents/skills/standalone-final-execution-plan/SKILL.md`
- Validation selection: `.agents/skills/validation-selection/SKILL.md`

## Consumer templates and references

- Templates: `.agents/templates/adr.md`, `.agents/templates/api-contract.md`, `.agents/templates/cli-contract.md`, `.agents/templates/config-contract.md`, `.agents/templates/final-execution-plan.md`, `.agents/templates/i18n-a11y-checklist.md`, `.agents/templates/migration-plan.md`, `.agents/templates/performance-budget.md`, `.agents/templates/protocol.md`, `.agents/templates/schema.md`, `.agents/templates/shared-rules-lock.schema.json`, `.agents/templates/test-plan.md`, `.agents/templates/threat-model.md`, `.agents/templates/treefmt.nix`, `.agents/templates/visual-review-finding.schema.json`, `.agents/templates/visual-review-rubric.md`, `.agents/templates/visual-review-synthesis.schema.json`
- References: `.agents/references/agent-tool-adapter-examples.md`, `.agents/references/final-execution-plan-examples.md`, `.agents/references/nix-layout.md`, `.agents/references/nixpkgs-devcontainer-alignment.md`, `.agents/references/playwright-system-browser.ts`, `.agents/references/project-overlay.md`, `.agents/references/route-examples.md`
