# Rust

- Validate with `cargo fmt` and `cargo clippy` under the project's warning policy; in greenfield Nix setups, use `rust-overlay`'s `rust-bin.stable.latest.default` pinned by `flake.lock`.
- Default to `thiserror` for library/domain errors, `anyhow` at binary boundaries, `tracing` for structured logging, TOML for new config files, and `axum` + `tokio` when Rust is chosen for HTTP services.
- Keep `unsafe` blocks minimal, documented, and tested; validate ownership and invariants across FFI boundaries.
