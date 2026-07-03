---
id: core.scripts
kind: core
triggers:
  - 'script'
  - 'automation'
  - 'shell'
  - 'Python script'
  - 'just recipe'
  - 'idempotent'
summary: Keep repository scripts documented, narrow, reproducible, and project-local.
load_with: []
---

# Repository Script Rules

Repository scripts are not the same thing as the project's main product language.

Use scripts only when the logic has a long-term home.

One-off investigation scripts go under `tmp/`.

Durable scripts go under `scripts/` or an existing project-standard script location.

## Prefer Python when

Use Python instead of shell when the script needs any of these:

- more than about 10 lines of meaningful logic;
- JSON/YAML/TOML parsing;
- non-trivial filesystem traversal;
- path normalization;
- cross-platform path handling;
- structured error reporting;
- retries;
- cleanup behavior;
- subprocess orchestration;
- testability;
- data transformation;
- string parsing where shell quoting would be fragile.

Prefer Python standard library first for repository scripts.

If a durable repository script needs third-party Python packages, provide them through Nix or an explicit project Python environment.

Do not create a Python CLI product just because the repository has maintenance scripts.

## Shell is acceptable when

Use shell for:

- tiny wrappers;
- simple command sequencing;
- environment checks;
- one-liners;
- direct Nix/Just command wrappers;
- scripts with no complex quoting or parsing.

Shell scripts should produce clear errors.

## Just boundary

Keep `justfile` recipes thin.

A just recipe may call a Python script.

Do not embed complex Python-worthy logic directly in `justfile`.
