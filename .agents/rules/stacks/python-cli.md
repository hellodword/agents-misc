# Python CLI Rules

Use Python for CLI projects only when ecosystem fit justifies it.

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
