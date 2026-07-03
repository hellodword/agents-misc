---
id: stack.rust
kind: stack
triggers:
  - 'Rust'
  - 'Cargo'
  - 'cargo fmt'
  - 'cargo clippy'
  - 'SQLx'
  - 'tracing'
---

# Rust Rules
## Applicability

Use these defaults only for new projects, greenfield scaffolding, or when the existing repository has no clear convention.

Do not introduce this stack, package manager, framework, database, toolchain, workflow, or directory structure into an existing project merely because it is preferred here.

Prefer the current local convention when it is coherent and working.

- Use Cargo.
- Use `cargo fmt`.
- Use `cargo clippy` for linting when available.
- Run commands through Nix/Just.
- Do not use global `cargo install`.
- Prefer standard library first.
- Prefer `thiserror` for library/domain error types.
- Prefer `anyhow` for application/CLI top-level errors.
- Prefer `serde` and `serde_json` when serialization is needed.
- Prefer `tracing` and `tracing-subscriber` for service/application logging.
- Avoid `unsafe` unless required, isolated, and documented.

## Nix toolchain

Default Rust flake input:

    rust-overlay.url = "github:oxalica/rust-overlay";

Default toolchain expression:

    rust-bin.selectLatestNightlyWith (toolchain: toolchain.default)

Add components or targets only when the project needs them.

## SQLite

- Default Rust SQLite stack: `sqlx` + SQLite.
- Use native SQL.
- Do not introduce ORM by default.
- Service and long-term projects:
  - prefer SQLx checked macros;
  - provide schema access through `DATABASE_URL` or SQLx offline metadata;
  - commit required SQLx offline metadata when used.
- Small CLI/prototype projects:
  - SQLx runtime queries are acceptable when checked macros add too much setup.
- Keep migrations explicit.
- Prefer transactions for multi-step writes.
- Do not commit local database files.

## Cargo.lock

Commit `Cargo.lock` by default.
