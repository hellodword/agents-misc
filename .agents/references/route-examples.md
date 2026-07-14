# Route Examples

Use these examples to choose a small initial shared rule set. Project-local routing from `.project-agent/route-map.md` comes first when it exists.

## Bug fix in Go HTTP handler

Load:

- `.project-agent/route-map.md` when present
- `.agents/rules/core/working-model.md`
- `.agents/rules/stacks/go.md`
- `.agents/rules/stacks/backend-api.md`
- `.agents/rules/core/testing.md`
- `.agents/skills/validation-selection/SKILL.md` when validation choice is not obvious

## Add SQLite migration

Load:

- `.project-agent/route-map.md` when present
- project database contracts when routed
- `.agents/rules/core/data-migrations.md`
- `.agents/rules/core/compatibility.md`
- `.agents/rules/stacks/database-sqlite.md`
- `.agents/rules/core/backup-import-export.md` when backup, restore, import, export, or user-owned data recovery is involved
- `.agents/skills/sqlite-migration-backup/SKILL.md` when workflow guidance is needed

## Change CLI JSON output

Load:

- `.project-agent/route-map.md` when present
- project CLI contracts when routed
- `.agents/rules/project-types/cli.md`
- `.agents/rules/core/cli-stability.md`
- `.agents/rules/core/compatibility.md`
- `.agents/skills/cli-contract/SKILL.md` when defining or changing the contract

## Add a dependency

Load:

- `.project-agent/route-map.md` when present
- `.agents/rules/core/dependencies.md`
- `.agents/rules/core/licensing.md`
- `.agents/rules/core/security.md` when install scripts, telemetry, binary downloads, native extensions, supply-chain risk, or secret-bearing workflows are involved
- relevant stack or toolchain rule

## Frontend UI state change

Load:

- `.project-agent/route-map.md` when present
- `.agents/rules/project-types/frontend-only.md` when the repository is frontend-only
- `.agents/rules/stacks/frontend-typescript.md`
- `.agents/rules/core/ui-ux-i18n-a11y.md`
- `.agents/rules/toolchain/browser-e2e.md` only when browser validation is useful

## Explicit AI visual review

Load:

- `.agents/rules/toolchain/ai-visual-review.md`
- `.agents/rules/toolchain/agent-tool-adapters.md`
- `.agents/skills/ai-visual-review/SKILL.md`
- `.agents/templates/visual-review-finding.schema.json` when producing structured findings
- `.agents/templates/visual-review-synthesis.schema.json` when producing synthesis

## Nix and Just command workflow

Load:

- `.agents/rules/toolchain/nix.md`
- `.agents/rules/toolchain/formatting.md` when formatting is involved
- `.agents/rules/core/scripts.md` when durable scripts change
- `.agents/skills/nix-workflow/SKILL.md` when adding or restructuring durable commands
- `.agents/references/nixpkgs-devcontainer-alignment.md` before initializing or updating `nixpkgs`
- `.agents/references/nix-layout.md` only when longer layout examples are needed
