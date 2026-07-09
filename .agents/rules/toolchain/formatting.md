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
companions: []
---

# Formatting Rules

- Go: `gofmt`.
- Rust: `cargo fmt`.
- Dart/Flutter: `dart format`.
- JSON, JSONC, Markdown, HTML, YAML, JavaScript, TypeScript, Vue: Prettier.
- Nix projects with multi-language formatting needs: prefer `treefmt-nix` through the flake `formatter` output; this may format more than Nix files.
- Treat `nix fmt` as a mutating formatter entrypoint unless the project exposes a separate check-only command.
- For validation, prefer formatter check commands or flake checks when available.
- Format only touched files by default when the formatter supports it.
- Do not run repository-wide formatting unless:
  - the task is formatting-focused;
  - the repo already requires it;
  - the change generated many files that must be consistently formatted.
- Do not mix formatting-only changes with semantic changes unless formatting is limited to touched files.
- When seeding shared formatter defaults, use `.agents/templates/treefmt.nix`, `.agents/templates/.prettierrc.json`, and `.agents/templates/.editorconfig`.
