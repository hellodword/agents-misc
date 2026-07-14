# Agent Rules Kit

This repository provides a reusable, project-scoped `AGENTS.md` rule system for coding agents. A project opts in by making `AGENTS.md` and `.agents/` available at its root; these instructions are not intended as universal global defaults.

## Structure

- `AGENTS.md`: compact entrypoint, priority, safety, and overlay discovery protocol.
- `.agents/manifest.json`: kit identity and versioned metadata dimensions.
- `.agents/rules/`: focused shared rules loaded on demand.
- `.agents/skills/`: reusable workflows loaded only when routed or relevant.
- `.agents/templates/`: consumer-facing artifact templates and output schemas, including the shared-rules lock schema.
- `.agents/references/`: examples and longer reference material.
- `.project-agent/`: this repository's maintenance overlay; it is not part of the distributed kit.
- `schemas/agent-rules/`: central-maintenance schemas for kit metadata.
- `scripts/check-agent-rules.py`: structural validator for the rules kit.

## Project overlay

Project facts, architecture, contracts, mandatory constraints, and project-specific workflows belong in a local overlay rather than shared defaults:

```text
.project-agent/
  project.md
  route-map.md
  shared-rules.lock
  rules/
    mandatory.md
  workflows/
contracts/
docs/
  architecture/
  adr/
```

Agents read `AGENTS.md` first, discover overlay entrypoints by path existence, prefer project routing, and load only the rules and documents relevant to the current task. If no overlay exists, shared defaults still apply.

## Shared-rules lock

The kit identity and version dimensions live in `.agents/manifest.json`. A consuming project records its expected values in `.project-agent/shared-rules.lock`:

```json
{
  "schema_version": "<lock-schema-version>",
  "expected_name": "<kit-name>",
  "expected_version": "<kit-version>",
  "expected_manifest_schema_version": "<manifest-schema-version>",
  "expected_rules_schema_version": "<rules-schema-version>",
  "expected_skills_schema_version": "<skills-schema-version>",
  "expected_overlay_discovery_version": "<overlay-discovery-version>",
  "expected_companion_metadata_version": "<companion-metadata-version>"
}
```

Replace every placeholder before validating or using the lock. Validate it against `.agents/templates/shared-rules-lock.schema.json` when a validator is available. A missing, malformed, or mismatched lock is advisory: agents report the exact issue, continue otherwise safe local work, and never rewrite the lock automatically.

## Loading model

Use `.project-agent/route-map.md` first when present, then `.agents/rules/route-map.md`. Keep each task's rule set small. `required_rules` are mandatory one-hop imports. Conditional rules, skills, templates, and references load only when their `when` condition applies. Companion loading never recurses.

## Validation

Run the structural checker after changing rules, skills, schemas, references, routing, or manifest metadata:

```sh
just check-agent-rules
```

The checker validates frontmatter, identities, companion references, route coverage, the manifest, and JSON Schema dialects and structure.

## Maintenance

When changing the kit:

1. Follow this repository's `.project-agent/` maintenance overlay; do not place checker or schema-maintenance instructions in the distributed payload.
2. Give every rule frontmatter fields `id`, `kind`, `triggers`, `summary`, and `companions`.
3. Give every skill frontmatter fields `name` and `description`.
4. Add rules and skills to `.agents/rules/route-map.md`.
5. Put reusable workflows under `.agents/skills/`, consumer-facing artifact structures under `.agents/templates/`, central metadata schemas under `schemas/agent-rules/`, and longer consumer guidance under `.agents/references/`.
6. Update `.agents/manifest.json` and lock documentation when versioned contracts change.
7. Run the structural checker and the narrow validation relevant to the change.
