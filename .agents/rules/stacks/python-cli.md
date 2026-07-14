---
id: stack.python-cli
kind: stack
triggers:
  - "Python CLI"
  - "uv"
  - "pyproject.toml"
  - "argparse"
  - "virtual environment"
summary: Apply Python CLI defaults when Python is the best ecosystem fit.
companions: {}
---

# Python CLI Rules

Use Python for CLI projects only when ecosystem fit justifies it.

Preserve an existing Python package/environment manager and lockfile workflow. For greenfield Python CLI work, use uv, `./.venv`, `pyproject.toml`, and a committed `uv.lock`.

- Do not commit `.venv/`.
- Do not install packages globally.
- Prefer `uv run` for project commands.
- Prefer `argparse` for simple CLIs.
- Use third-party CLI frameworks only when command complexity justifies them.
- Keep stdout machine-readable when output may be piped.
- Send diagnostics to stderr.
- Avoid hidden network access.
- Add tests for argument parsing, filesystem behavior, and error cases.
