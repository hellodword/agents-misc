---
id: route-map
kind: index
triggers:
  - route
  - routing
  - rules
  - context loading
load_with: []
---

# Agent Rule Route Map

Load only the smallest relevant rule files. Do not preload the full `.agents/rules/` tree.

When a route points to a relevant rule path, read that file before editing. Do not infer unseen rule contents from the route label.

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
- Architecture: `.agents/rules/core/architecture.md`
- Code quality: `.agents/rules/core/code-quality.md`
- Testing and Go race validation: `.agents/rules/core/testing.md`
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
