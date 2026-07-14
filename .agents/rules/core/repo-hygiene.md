---
id: core.repo-hygiene
kind: core
triggers:
  - "repo hygiene"
  - "temporary files"
  - "ignored paths"
  - "large files"
  - "fixtures"
  - "snapshots"
summary: Keep temporary, generated, local, and sensitive artifacts out of durable changes.
companions: {}
---

# Repository Hygiene

- New files must have a durable purpose.
- One-off drafts, command output, browser traces, screenshots, databases, archives, coverage output, local upstream checkouts, and logs go under ignored paths such as `tmp/` or `.work/`.
- Do not commit local environment files, secrets, user uploads, databases, generated archives, or coverage artifacts.
- Before adding large docs, fixtures, generated files, snapshots, or dependencies, verify they are necessary for the current task.
- Prefer small synthetic fixtures over copied real-world dumps.
- Keep generated code clearly separated and documented.
- Do not create broad helper layers for one-off migrations or temporary compatibility code.
- Do not add root-level transient files such as `PLAN.md`, `IMPLEMENTATION.md`, or `NOTES.md`.
- In consuming projects, durable project-specific agent rules belong under `.project-agent/`; treat the shared `AGENTS.md` and `.agents/**` payload as centrally maintained and read-only.
- Pure patch upstream source trees belong under `.work/` and must not be staged.
