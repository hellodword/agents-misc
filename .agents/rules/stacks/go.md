---
id: stack.go
kind: stack
triggers:
  - "Go"
  - "gofmt"
  - "go test"
  - "go-sqlite3"
  - "slog"
  - "race detector"
summary: Apply Go defaults for modules, formatting, SQLite, logging, and race validation.
companions:
  conditional_rules:
    - id: core.testing
      when: Go validation or race testing is involved
  skills:
    - id: validation-selection
      when: selecting Go validation commands is non-obvious
---

# Go Rules

- Use Go modules.
- Use `gofmt`.
- Prefer the standard library first.
- When SQLite is already selected, the greenfield Go SQLite default is:
  - use `database/sql`;
  - use `github.com/mattn/go-sqlite3`;
  - use native SQL;
  - do not use ORM by default.
- When YAML is selected for a greenfield project-developed application config, use `github.com/goccy/go-yaml`.
- Preserve existing logging. For greenfield applications:
  - use `log/slog`;
  - use JSON logs for services and machine-consumed CLIs;
  - use `gopkg.in/natefinch/lumberjack.v2` when file log rotation is required.
- Use `context.Context` for request-scoped and cancelable work.
- Keep migrations as SQL files for non-trivial persistence.
- Do not install Go tools globally.
- Add tools through the established reproducible project workflow or project-local commands; introduce Nix only when separately selected.

## Race detector

- Add a durable race-test entrypoint using the project's existing command system when the project needs one.
- Run `go test -race` on touched packages when concurrency or shared mutable state is involved.
- Keep race tests narrow by default.
