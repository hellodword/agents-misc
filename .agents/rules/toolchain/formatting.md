---
id: toolchain.formatting
kind: toolchain
triggers:
  - 'formatting'
  - 'formatter'
  - 'Prettier'
  - 'gofmt'
  - 'nix fmt'
  - 'touched files'
summary: Use project formatting narrowly and avoid unrelated repository-wide churn.
load_with: []
---

# Formatting Rules

- Go: `gofmt`.
- Rust: `cargo fmt`.
- Dart/Flutter: `dart format`.
- JSON, JSONC, Markdown, HTML, YAML, JavaScript, TypeScript, Vue: Prettier.
- Format only touched files by default.
- Do not run repository-wide formatting unless:
  - the task is formatting-focused;
  - the repo already requires it;
  - the change generated many files that must be consistently formatted.
- Do not mix formatting-only changes with semantic changes unless formatting is limited to touched files.
