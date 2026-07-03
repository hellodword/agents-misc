# Agent Rules Kit

This repository provides a reusable `AGENTS.md`-based rule system for coding agents.

The kit is intended for project-scoped use. A project opts in by making this kit's `AGENTS.md` and `.agents/` available at the project root. Do not install these instructions as global defaults unless every project should inherit them.

## Structure

- `AGENTS.md`: shared agent entrypoint and overlay discovery protocol.
- `.agents/manifest.json`: shared kit identity and schema metadata.
- `.agents/rules/`: durable shared task rules loaded on demand.
- `.agents/skills/`: reusable shared workflows loaded only when needed.
- `.agents/templates/`: artifact templates and JSON schemas.
- `.agents/references/`: examples, command shapes, and longer reference material.

## Project overlay

Project-specific facts do not belong in the shared rules kit.

Use this project-local layout when a project has its own architecture, contracts, validation rules, or mandatory constraints:

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

Recommended responsibilities:

- `.project-agent/project.md`: short project summary, non-negotiable rules, and default validation entrypoints.
- `.project-agent/route-map.md`: project-specific task/path routing to rules, contracts, architecture docs, and validation commands.
- `.project-agent/rules/mandatory.md`: constraints that must be loaded before product code changes.
- `.project-agent/rules/**`: focused project rules.
- `.project-agent/workflows/**`: project-specific reusable procedures.
- `.project-agent/shared-rules.lock`: expected shared kit identity and version.
- `contracts/**`: durable product contracts.
- `docs/architecture/**`: architecture facts and boundaries.
- `docs/adr/**`: accepted architecture decisions.

## Overlay discovery

Agents should read `AGENTS.md` first.

For each task, agents should:

1. Check whether `.project-agent/project.md` exists.
2. Check whether `.project-agent/rules/mandatory.md` exists before product code changes.
3. Check whether `.project-agent/route-map.md` exists and use it before shared routing.
4. Compare `.project-agent/shared-rules.lock` with `.agents/manifest.json` when both exist.
5. Load shared rules from `.agents/rules/route-map.md` only after project-local routing has been considered.
6. Load contracts and architecture docs only when the route or touched files require them.

If no project overlay exists, agents continue with shared defaults.

## Shared rules version lock

The kit identity lives in `.agents/manifest.json`.

A project can declare the expected kit in `.project-agent/shared-rules.lock`:

```json
{
  "schema_version": "1",
  "expected_name": "agent-rules-kit",
  "expected_version": "0.2.0",
  "expected_rules_schema_version": "1",
  "expected_overlay_discovery_version": "1"
}
```

Agents should compare the lock with `.agents/manifest.json` and report mismatches. The comparison is advisory; it should not block safe local work by itself.

## Rule loading

Prefer a small rule set for each task:

- zero or one project-type rule;
- zero to two stack rules;
- one to three core concern rules;
- zero to two toolchain rules;
- project overlay files only when the touched area or overlay route requires them;
- contracts and architecture docs only when changing or relying on their behavior;
- skills only for reusable shared workflows;
- templates only when producing that artifact.

For unclear shared tasks, use `.agents/rules/route-map.md` to choose the smallest relevant rule set.

## Maintenance

When adding or changing a shared rule:

1. Keep `AGENTS.md` small and actionable.
2. Add frontmatter with `id`, `kind`, `triggers`, `summary`, and `load_with`.
3. Use `load_with` only for meaningful companion rules, skills, templates, or references.
4. Add the rule to `.agents/rules/route-map.md`.
5. Put reusable shared workflows under `.agents/skills/`.
6. Put artifact structures under `.agents/templates/`.
7. Put examples, command shapes, and longer guidance under `.agents/references/`.
8. Keep task-specific defaults narrow and defer to project overlay and local conventions.
