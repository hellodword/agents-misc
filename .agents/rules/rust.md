# Rust

- Use Cargo, `cargo fmt`, and `cargo clippy` with warnings treated according to project policy.
- In a greenfield Nix toolchain, use rust-overlay's `rust-bin.stable.latest.default`; the project `flake.lock` pins the resolved toolchain inputs.
- Use `thiserror` for library and domain error types and `anyhow` at application or CLI boundaries.
- Add `serde` only for an actual serialization boundary and `tracing` for application logging.
- After the user selects Rust for a web service, use axum with tokio and document the JSON HTTP contract.
- Parse TOML configuration strictly and reject unknown fields unless the contract explicitly permits them.
- For a synchronous CLI or desktop SQLite process, use rusqlite with one connection.
- For axum/tokio or another explicitly concurrent SQLite service, use a SQLx pool with maximum and idle connections set to 4.
- For both SQLite paths, enable foreign keys, WAL, and a 5-second busy timeout on applicable connections and follow the migration contract.
- Keep unsafe code isolated, justified, and tested. Validate FFI inputs and ownership at the boundary.
- Run focused tests, formatting, and clippy; validate generated or FFI consumers when those boundaries change.
