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
summary: Apply Rust defaults for Cargo, linting, logging, errors, SQLite, and lockfiles.
load_with: []
---

# Rust Rules
## Applicability

Use these defaults only for new projects, greenfield scaffolding, or repositories without a clear existing convention.

Prefer coherent local conventions.

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

When this rule applies and the project has no explicit Rust toolchain convention, the default Rust toolchain MUST be nightly from rust-overlay.

Use this exact default expression:

    rust-bin.selectLatestNightlyWith (toolchain: toolchain.default)

Default means required fallback, not preference. Do not replace this with `rust-bin.stable.*` merely because the project is a normal CLI, uses Cargo, or does not obviously require nightly.

Existing project convention overrides this default only when one of these exists:

- `rust-toolchain.toml` or `rust-toolchain`;
- an existing `flake.nix` pinning a Rust toolchain;
- project documentation that explicitly requires stable, beta, nightly, or a specific Rust version;
- dependency or upstream build documentation that requires a specific compiler.

Cargo edition, including edition 2024, is not by itself a project toolchain convention.

If latest nightly fails to build because of compiler or dependency incompatibility, do not silently switch to stable. First prefer pinning a known-working nightly toolchain. Switch to stable only when the project explicitly requires stable or the user approves that deviation, and report the reason.

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
