---
id: stack.nodejs-cli
kind: stack
triggers:
  - "Node.js CLI"
  - "npm CLI"
  - "TypeScript CLI"
  - "package-lock"
  - "bundled JavaScript"
summary: Apply Node.js CLI defaults when npm, frontend, or browser tooling fit best.
companions: {}
---

# Node.js CLI Rules

Use Node.js for CLI projects when ecosystem fit justifies it, especially frontend/tooling/npm/browser automation workflows.

- Preserve an existing package manager and lockfile. For greenfield Node.js CLI work, use npm and commit `package-lock.json`.
- Do not install packages globally.
- Use project-local scripts through the established project workflow; use Nix/Just only when adopted.
- Prefer TypeScript for durable CLIs.
- For a greenfield CLI, use a bundled JavaScript artifact unless the user requires a native standalone executable.
- Native single executable packaging is allowed only when the user explicitly requires running without Node.js.
- Keep stdout machine-readable when output may be piped.
- Send diagnostics to stderr.
- Avoid postinstall scripts and binary downloads unless clearly justified.
- Add tests for argument parsing, filesystem behavior, and error cases.
