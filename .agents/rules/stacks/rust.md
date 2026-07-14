---
id: stack.rust
kind: stack
triggers:
  - "Rust"
  - "Cargo"
  - "cargo fmt"
  - "cargo clippy"
  - "SQLx"
  - "tracing"
summary: Apply Rust defaults for stable toolchains, Cargo, linting, errors, logging, SQLite, and lockfiles.
companions: {}
---

# Rust Rules

- Use Cargo, `cargo fmt`, and `cargo clippy` when available.
- Run project commands through the established project workflow; use Nix/Just only when the project adopts it.
- Do not use global `cargo install`.
- Prefer the standard library first; use `thiserror` for library/domain errors, `anyhow` at application/CLI boundaries, `serde` for serialization, and `tracing` for application logging when needed.
- Avoid `unsafe` unless required, isolated, and documented.

## Toolchain

Preserve `rust-toolchain.toml`, an existing flake pin, project documentation, or an upstream/dependency requirement. For a greenfield Nix project, use rust-overlay with stable Rust:

```nix
rust-bin.stable.latest.default
```

A `rust-toolchain.toml`, existing flake pin, project documentation, or dependency/upstream requirement is toolchain evidence. Use nightly only when such evidence requires it or the user explicitly requests it. Pin a dated nightly rather than a moving latest-nightly selector. Add components and targets only when needed.

## SQLite

- Default Rust SQLite stack: SQLx with native SQL, not an ORM.
- Use checked SQLx macros and committed offline metadata when the project requires compile-time query verification or offline builds. Otherwise use runtime queries and validate them with integration tests.
- Keep migrations explicit, use transactions for multi-step writes, and never commit local database files.

## Cargo.lock

Commit `Cargo.lock` for applications and binaries. For libraries, follow the repository's publishing and lockfile convention; if none exists, decide based on whether the lockfile is part of the tested/released artifact rather than applying an application default.
