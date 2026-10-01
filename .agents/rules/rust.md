# Rust

- Use Cargo, `cargo fmt`, and `cargo clippy` with warnings treated according to project policy.
- In a greenfield Nix toolchain, use rust-overlay's `rust-bin.stable.latest.default`; the project `flake.lock` pins the resolved toolchain inputs.
- Use `thiserror` for library and domain error types and `anyhow` at application or CLI boundaries.
- Add `serde` only for an actual serialization boundary and `tracing` for application logging.
- After the user selects Rust for a web service, use axum with tokio and document the JSON HTTP contract.
- Prefer TOML for new configuration files. Choose unknown-field behavior according to the configuration contract.
- Keep unsafe code isolated, justified, and tested. Validate FFI inputs and ownership at the boundary.
- Run focused tests, formatting, and clippy; validate generated or FFI consumers when those boundaries change.
