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
- `.agents/rules/stacks/database-sqlite.md`
- `.agents/rules/core/backup-import-export.md`
- `.agents/skills/sqlite-migration-backup/SKILL.md` when workflow guidance is needed

## Change CLI JSON output

Load:

- `.project-agent/route-map.md` when present
- project CLI contracts when routed
- `.agents/rules/project-types/cli.md`
- `.agents/rules/core/cli-stability.md`
- `.agents/rules/core/compatibility.md`
- `.agents/skills/cli-contract/SKILL.md` when defining or changing the contract

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
- `.agents/templates/visual-review-finding.schema.json`
- `.agents/templates/visual-review-synthesis.schema.json`

## Nix and Just command workflow

Load:

- `.agents/rules/toolchain/nix-just.md`
- `.agents/rules/toolchain/flake-organization.md` when flake outputs or `nix/` layout changes
- `.agents/skills/nix-just-workflow/SKILL.md` when adding or restructuring durable commands
- `.agents/references/nixpkgs-devcontainer-alignment.md` before initializing or updating `nixpkgs`

## Ruleset maintenance

Load:

- `.agents/rules/core/agent-rules-authoring.md`
- `.agents/rules/core/repo-hygiene.md`
- `.agents/rules/route-map.md`
