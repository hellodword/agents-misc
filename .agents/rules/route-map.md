---
id: route-map
kind: index
triggers:
  - route
  - routing
  - rules
  - context loading
summary: Map task types to the smallest relevant shared rule files.
load_with: []
---

# Agent Rule Route Map

Load only the smallest relevant rule files. Do not preload the full `.agents/rules/` tree.

When a route points to a relevant rule path, read that file before editing. Do not infer unseen rule contents from the route label.

## Project overlay first

Before using this shared route map, check for `.project-agent/route-map.md`.

Project-local routing, contracts, architecture docs, and mandatory rules override shared default routing when they are more specific.

Use shared routing only for reusable defaults and cross-project workflows.

## Rule Bundles

Use this matrix after project-local routing. Project-local routing wins.

| Task                                                                       | Required shared rules                                                                                                                                                 | Optional workflow skills                                                                             | Templates/references                                                                                                                                                            |
| -------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| API, config, schema, protocol, FFI, or public contract change              | `.agents/rules/core/config-schema-protocol-api.md`; `.agents/rules/core/compatibility.md`; `.agents/rules/core/testing.md`                                            | `.agents/skills/compatibility-review/SKILL.md`                                                       | `.agents/templates/api-contract.md`; `.agents/templates/config-contract.md`; `.agents/templates/protocol.md`; `.agents/templates/schema.md` only when producing those artifacts |
| SQLite schema, migration, backup, import, export, or reset behavior        | `.agents/rules/core/data-migrations.md`; `.agents/rules/core/backup-import-export.md`; `.agents/rules/stacks/database-sqlite.md`; `.agents/rules/core/testing.md`     | `.agents/skills/sqlite-migration-backup/SKILL.md`                                                    | `.agents/templates/migration-plan.md` when producing a migration plan                                                                                                           |
| CLI behavior, flags, args, stdout, stderr, exit codes, config, or env vars | `.agents/rules/project-types/cli.md`; `.agents/rules/core/cli-stability.md`; `.agents/rules/core/compatibility.md`; `.agents/rules/core/testing.md`                   | `.agents/skills/cli-contract/SKILL.md`                                                               | `.agents/templates/cli-contract.md` when producing or updating a CLI contract                                                                                                   |
| UI behavior, copy, layout, i18n, a11y, or responsive behavior              | `.agents/rules/core/ui-ux-i18n-a11y.md`; relevant frontend stack rule; `.agents/rules/core/testing.md`                                                                | `.agents/skills/browser-e2e/SKILL.md` only when browser validation is useful                         | `.agents/templates/i18n-a11y-checklist.md` when producing a checklist                                                                                                           |
| Explicit visual review, screenshot review, or visual QA                    | `.agents/rules/toolchain/ai-visual-review.md`; `.agents/rules/toolchain/agent-tool-adapters.md`; relevant UI/frontend rule                                            | `.agents/skills/ai-visual-review/SKILL.md`                                                           | `.agents/templates/visual-review-finding.schema.json`; `.agents/templates/visual-review-synthesis.schema.json`; `.agents/templates/visual-review-rubric.md`                     |
| Commit requested or explicit auto-commit policy applies                    | `.agents/rules/core/git.md`; `.agents/rules/core/repo-hygiene.md`                                                                                                     | `.agents/skills/atomic-commit/SKILL.md`; `.agents/skills/repo-hygiene-review/SKILL.md`               | none                                                                                                                                                                            |
| Shared ruleset maintenance                                                 | `.agents/rules/core/agent-rules-authoring.md`; `.agents/rules/core/repo-hygiene.md`                                                                                   | `.agents/skills/generated-artifacts-review/SKILL.md` when generated artifacts or schemas are changed | affected templates, schemas, and references                                                                                                                                     |
| Nix, Just, flake outputs, or command workflow changes                      | `.agents/rules/toolchain/nix-just.md`; `.agents/rules/toolchain/flake-organization.md` when flake outputs change; `.agents/rules/core/scripts.md` when scripts change | `.agents/skills/nix-just-workflow/SKILL.md`                                                          | `.agents/references/nixpkgs-devcontainer-alignment.md`                                                                                                                          |
| Pure Nix project behavior                                                  | `.agents/rules/project-types/pure-nix.md`; `.agents/rules/toolchain/flake-organization.md`                                                                            | `.agents/skills/pure-nix-project/SKILL.md`                                                           | `.agents/references/nixpkgs-devcontainer-alignment.md`                                                                                                                          |
| Pure patch workflow                                                        | `.agents/rules/project-types/pure-patch.md`; `.agents/rules/toolchain/nix-just.md`; `.agents/rules/core/repo-hygiene.md`                                              | `.agents/skills/pure-patch-workflow/SKILL.md`                                                        | `.agents/references/nixpkgs-devcontainer-alignment.md`                                                                                                                          |

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

- Nix and Just command workflow: `.agents/rules/toolchain/nix-just.md`
- Flake file organization and outputs: `.agents/rules/toolchain/flake-organization.md`
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
