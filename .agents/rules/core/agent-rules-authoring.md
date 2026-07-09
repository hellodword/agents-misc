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
companions:
  required_rules:
    - core.repo-hygiene
  templates:
    - id: shared-rules-lock.schema
      when: shared-rules lock structure changes
  references:
    - id: project-overlay
      when: overlay protocol changes
---

# Agent Rule Authoring Rules

Use this rule when editing `AGENTS.md`, `.agents/rules/**`, `.agents/skills/**`, `.agents/templates/**`, `.agents/references/**`, `.agents/evals/**`, or `.agents/manifest.json`.

## Shared kit boundaries

- Keep `AGENTS.md` small, actionable, and focused on priority, safety, loading, and universal defaults.
- Keep shared defaults reusable across projects.
- Do not move project-specific architecture, contracts, mandatory product rules, or validation commands into shared defaults.
- Put project-specific facts under `.project-agent/**`, `contracts/**`, `docs/architecture/**`, or `docs/adr/**` in the consuming project.
- Keep overlay discovery stable and documented in `AGENTS.md`, `.agents/rules/route-map.md`, and `.agents/references/project-overlay.md`.

## Rules

- Each shared rule must have frontmatter with `id`, `kind`, `triggers`, `summary`, and `companions`.
- Use `companions` only for meaningful required or conditional companion rules, skills, templates, or references.
- Do not fill `companions` with empty or speculative entries just for symmetry.
- Companion entries are advisory and condition-driven; root context-loading rules decide whether to open them.
- Avoid companion cycles. If a relationship is naturally mutual, express it through the route map rather than recursive frontmatter.
- Add new rules to `.agents/rules/route-map.md`.
- Prefer route-map bundles for common task combinations.
- Prefer examples under `.agents/references/` when guidance is too long for a rule.
- Keep rules focused on decisions; move long recipes, directory trees, and command examples into references or skills.

## Skills, templates, and references

- Put reusable shared workflows under `.agents/skills/`.
- Keep skill descriptions strict enough that ordinary coding tasks do not load high-cost or answer-structure workflows by accident.
- Put artifact structures and JSON schemas under `.agents/templates/`.
- Include `schema_version` in durable JSON schemas and outputs.
- Put command shapes and long examples under `.agents/references/`.
- Put regression cases for rule selection, conflicts, safety, and final-report shape under `.agents/evals/`.
- Keep command examples as shapes unless the command is known to be stable in the current environment.

## Evals

Maintain `.agents/evals/**` when routing, priority, safety, or final-report rules change.

Eval cases should cover expected rules, forbidden rules, expected behavior, and conflict handling. Keep cases small enough that they can be read without loading the full rules tree.

## Version metadata

- Keep `.agents/manifest.json` synchronized when the shared kit identity, schema version, or overlay discovery protocol changes.
- Keep `.agents/templates/shared-rules-lock.schema.json` aligned with `.agents/manifest.json` fields that consuming projects may lock.
- When version metadata changes, update `.agents/references/project-overlay.md` examples.
