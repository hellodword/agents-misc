---
id: stack.nodejs-cli
kind: stack
triggers:
  - 'Node.js CLI'
  - 'npm CLI'
  - 'TypeScript CLI'
  - 'package-lock'
  - 'bundled JavaScript'
---

# Node.js CLI Rules
## Applicability

Use these defaults only for new projects, greenfield scaffolding, or when the existing repository has no clear convention.

Do not introduce this stack, package manager, framework, database, toolchain, workflow, or directory structure into an existing project merely because it is preferred here.

Prefer the current local convention when it is coherent and working.

Use Node.js for CLI projects when ecosystem fit justifies it, especially frontend/tooling/npm/browser automation workflows.

Use these defaults only for new projects, greenfield scaffolding, or repositories with no clear convention.

- Default package manager for new projects: npm.
- Commit `package-lock.json`.
- Do not install packages globally.
- Use project-local scripts through Nix/Just.
- Prefer TypeScript for durable CLIs.
- Default packaging for CLI projects: bundled JavaScript CLI artifact.
- Native single executable packaging is allowed only when the user explicitly requires running without Node.js.
- Keep stdout machine-readable when output may be piped.
- Send diagnostics to stderr.
- Avoid postinstall scripts and binary downloads unless clearly justified.
- Add tests for argument parsing, filesystem behavior, and error cases.
