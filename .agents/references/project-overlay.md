# Project Overlay Reference

Use project overlay files for project-specific facts that should not live in shared rules.

## Recommended layout

```text
.project-agent/
  project.md
  route-map.md
  shared-rules.lock
  rules/
    mandatory.md
    architecture-boundaries.md
    validation.md
    backend.md
    frontend.md
    database.md
  workflows/
    regenerate-api-client.md
    refresh-fixtures.md
contracts/
  api/
  cli/
  config/
  db/
docs/
  architecture/
  adr/
```

## `.project-agent/project.md`

Keep this short. Include only information that is broadly useful for most tasks.

Suggested sections:

```md
# Project Agent Overlay

## Project Summary

Describe the product, major stack, and important runtime assumptions.

## Non-Negotiable Rules

- Treat `contracts/**` as durable product contracts.
- Preserve architecture boundaries documented under `docs/architecture/**`.
- Do not change public API, CLI, config, database, event, or file-format behavior without loading the relevant contract.
- Do not weaken tests to hide failures.

## Main Validation

- `just test`
- `just lint`
- `just check`
```

## `.project-agent/route-map.md`

Use this file to connect project paths to project facts and shared defaults.

Example:

```md
# Project Route Map

Project routes override shared default routes.

## Always load for product code changes

- `.project-agent/project.md`
- `.project-agent/rules/mandatory.md`

## Backend API

When touching `internal/http/**`, `internal/api/**`, or `contracts/api/**`, load:

- `.project-agent/rules/backend.md`
- `contracts/api/http.md`
- `contracts/api/errors.md`
- `.agents/rules/stacks/go.md`
- `.agents/rules/stacks/backend-api.md`
- `.agents/rules/core/config-schema-protocol-api.md`
- `.agents/rules/core/compatibility.md`
- `.agents/rules/core/testing.md`

## SQLite and persistence

When touching `internal/storage/**`, `internal/db/**`, `migrations/**`, or `contracts/db/**`, load:

- `.project-agent/rules/database.md`
- `contracts/db/schema.md`
- `contracts/db/migrations.md`
- `.agents/rules/stacks/database-sqlite.md`
- `.agents/rules/core/data-migrations.md`
- `.agents/rules/core/backup-import-export.md`

## CLI behavior

When touching `cmd/**`, `internal/cli/**`, or `contracts/cli/**`, load:

- `.project-agent/rules/cli.md`
- `contracts/cli/commands.md`
- `contracts/cli/output.md`
- `.agents/rules/project-types/cli.md`
- `.agents/rules/core/cli-stability.md`
- `.agents/rules/core/compatibility.md`

## Architecture-sensitive changes

When changing module boundaries, public interfaces, dependency direction, or cross-package ownership, load:

- `.project-agent/rules/architecture-boundaries.md`
- `docs/architecture/overview.md`
- `docs/architecture/boundaries.md`
- relevant ADRs under `docs/adr/**`
```

## `.project-agent/shared-rules.lock`

Use this lock to record the expected shared rules kit identity.

```json
{
  "schema_version": "1",
  "expected_name": "agent-rules-kit",
  "expected_version": "0.2.0",
  "expected_rules_schema_version": "1",
  "expected_overlay_discovery_version": "1"
}
```

Agents should compare this file with `.agents/manifest.json` and report mismatches.
