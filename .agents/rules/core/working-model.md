---
id: core.working-model
kind: core
triggers:
  - "planning"
  - "task slicing"
  - "incremental work"
  - "handoff"
  - "scope"
summary: Plan work in small, verifiable slices and keep progress reports actionable.
load_with: []
---

# Working Model

- Work in small, verifiable, semantic increments.
- Prefer understanding current repo conventions before introducing new ones.
- Avoid broad rewrites unless the task requires them.
- For ambiguous product behavior, list local alternatives and choose the least surprising default when progress is possible.
- Do not create root-level planning documents by default.
- Temporary plans and scratch notes belong under `tmp/`.
- Durable decisions belong in docs or ADRs only when the decision has long-term architectural impact.
- For solo full-stack development, prefer simple, observable, locally reproducible solutions over distributed or enterprise-heavy patterns.
- Prefer boring, debuggable architecture.
- Optimize for future patchability and task handoff to another agent session.
- Keep route, rule, skill, template, and reference files separated by purpose.
- Do not duplicate the same rule across many files unless the duplication prevents serious misuse.
