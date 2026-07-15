# Formatting

- Preserve an existing formatter and its authoritative configuration.
- For a greenfield project, use the ecosystem defaults: `gofmt`, `cargo fmt`, `dart format`, and Prettier defaults as applicable.
- Do not create a root Prettier configuration or EditorConfig merely to restate default behavior.
- In every Nix project, expose formatting and a formatting check through treefmt-nix and the flake formatter.
- Treat `nix fmt` as a mutating command. Review its diff and revert only unrelated formatter churn without disturbing user work.
- Run the narrowest formatter when repository-wide formatting would touch unrelated areas and the project provides a supported narrow command.
- Do not mix a semantic refactor with broad formatting churn unless the formatter necessarily changes the touched files.
