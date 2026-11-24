# Formatting

- Preserve the project's formatter and configuration; for greenfield code, use ecosystem defaults (`gofmt`, `cargo fmt`, `dart format`, Prettier defaults) without adding redundant config files.
- Expose a Nix `formatter` / `checks.<system>.formatting` (preferring `treefmt-nix` for multi-language repos) only when requested or already adopted by the project.
- Treat `nix fmt` and broad formatters as mutating commands: prefer narrow target paths when repository-wide formatting would touch unrelated files, inspect the diff, and keep formatting churn out of unrelated semantic changes unless required on touched files.
