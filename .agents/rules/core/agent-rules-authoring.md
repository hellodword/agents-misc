---
id: core.agent-rules-authoring
kind: core
triggers:
  - "AGENTS.md"
  - "agent rules"
  - "route-map"
  - "ruleset"
  - "SKILL.md"
summary: Maintain the agent rules kit without bloating the root entrypoint or duplicating rules.
load_with:
  rules:
    - core.repo-hygiene
  templates:
    - shared-rules-lock.schema
  references:
    - project-overlay
---

# Agent Rule Authoring Rules

Use this rule when editing `AGENTS.md`, `.agents/rules/**`, `.agents/skills/**`, `.agents/templates/**`, `.agents/references/**`, or `.agents/manifest.json`.

## Shared kit boundaries

- Keep `AGENTS.md` small and actionable.
- Keep shared defaults reusable across projects.
- Do not move project-specific architecture, contracts, mandatory product rules, or validation commands into shared defaults.
- Put project-specific facts under `.project-agent/**`, `contracts/**`, `docs/architecture/**`, or `docs/adr/**` in the consuming project.
- Keep overlay discovery stable and documented in `AGENTS.md`, `README.md`, `.agents/rules/route-map.md`, and `.agents/references/project-overlay.md`.

## Rules

- Each shared rule must have frontmatter with `id`, `kind`, `triggers`, `summary`, and `load_with`.
- Use `load_with` only for meaningful companion rules, skills, templates, or references.
- Do not fill `load_with` with empty or speculative companions just for symmetry.
- Add new rules to `.agents/rules/route-map.md`.
- Prefer route-map bundles for common task combinations.
- Prefer examples under `.agents/references/` when guidance is too long for a rule.

## Skills, templates, and references

- Put reusable shared workflows under `.agents/skills/`.
- Keep skill descriptions strict enough that ordinary coding tasks do not load high-cost or answer-structure workflows by accident.
- Put artifact structures and JSON schemas under `.agents/templates/`.
- Include `schema_version` in durable JSON schemas and outputs.
- Put command shapes and long examples under `.agents/references/`.
- Keep command examples as shapes unless the command is known to be stable in the current environment.

## Version metadata

- Keep `.agents/manifest.json` synchronized when the shared kit identity, schema version, or overlay discovery protocol changes.
- Keep `.agents/templates/shared-rules-lock.schema.json` aligned with `.agents/manifest.json` fields that consuming projects may lock.
- When version metadata changes, update `README.md` and `.agents/references/project-overlay.md` examples.
