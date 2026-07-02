# Node.js CLI Rules

Use Node.js for CLI projects when ecosystem fit justifies it, especially frontend/tooling/npm/browser automation workflows.

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
