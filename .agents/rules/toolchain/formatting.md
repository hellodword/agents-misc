---
id: toolchain.formatting
kind: toolchain
triggers:
  - "formatting"
  - "formatter"
  - "Prettier"
  - "treefmt"
  - "gofmt"
  - "nix fmt"
  - "touched files"
summary: Use project formatting narrowly and avoid unrelated repository-wide churn.
companions: {}
---

# Formatting Rules

Use the formatter and invocation already established for the touched file type. Do not add Prettier, treefmt, Nix, or another formatter merely to format one change.

For greenfield formatter setup, use `gofmt` for Go, `cargo fmt` for Rust, `dart format` for Dart/Flutter, and Prettier for JSON/JSONC/Markdown/HTML/YAML/JavaScript/TypeScript/Vue. In an already adopted Nix project with multi-language formatting, treefmt-nix through the flake `formatter` is available.

- Treat `nix fmt` as a mutating formatter entrypoint unless the project exposes a separate check-only command.
- For validation, prefer formatter check commands or flake checks when available.
- Format only touched files by default when the formatter supports it.
- Do not run repository-wide formatting unless:
  - the task is formatting-focused;
  - the repo already requires it;
  - the change generated many files that must be consistently formatted.
- Do not mix formatting-only changes with semantic changes unless formatting is limited to touched files.
- When a greenfield task explicitly seeds those tools, the matching `.agents/templates/` files are available.
