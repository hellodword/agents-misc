---
id: stack.python-cli
kind: stack
triggers:
  - 'Python CLI'
  - 'uv'
  - 'pyproject.toml'
  - 'argparse'
  - 'virtual environment'
---

# Python CLI Rules
## Applicability

Use these defaults only for new projects, greenfield scaffolding, or when the existing repository has no clear convention.

Do not introduce this stack, package manager, framework, database, toolchain, workflow, or directory structure into an existing project merely because it is preferred here.

Prefer the current local convention when it is coherent and working.

Use Python for CLI projects only when ecosystem fit justifies it.

Use these defaults only for new projects, greenfield scaffolding, or repositories with no clear convention.

- Default package/environment manager: uv.
- Default virtual environment path: `./.venv`.
- Commit `pyproject.toml`.
- Commit `uv.lock`.
- Do not commit `.venv/`.
- Do not install packages globally.
- Prefer `uv run` for project commands.
- Prefer `argparse` for simple CLIs.
- Use third-party CLI frameworks only when command complexity justifies them.
- Keep stdout machine-readable when output may be piped.
- Send diagnostics to stderr.
- Avoid hidden network access.
- Add tests for argument parsing, filesystem behavior, and error cases.
