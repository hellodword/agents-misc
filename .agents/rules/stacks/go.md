---
id: stack.go
kind: stack
triggers:
  - 'Go'
  - 'gofmt'
  - 'go test'
  - 'go-sqlite3'
  - 'slog'
  - 'race detector'
---

# Go Rules
## Applicability

Use these defaults only for new projects, greenfield scaffolding, or when the existing repository has no clear convention.

Do not introduce this stack, package manager, framework, database, toolchain, workflow, or directory structure into an existing project merely because it is preferred here.

Prefer the current local convention when it is coherent and working.

- Use Go modules.
- Use `gofmt`.
- Prefer the standard library first.
- SQLite default:
  - use `database/sql`;
  - use `github.com/mattn/go-sqlite3`;
  - use native SQL;
  - do not use ORM by default.
- YAML default for project-developed application config files: `github.com/goccy/go-yaml`.
- Logging default:
  - use `log/slog`;
  - use JSON logs for services and machine-consumed CLIs;
  - use `gopkg.in/natefinch/lumberjack.v2` when file log rotation is required.
- If `github.com/mattn/go-sqlite3` is used, ensure the Nix dev shell provides CGO, a C compiler, and SQLite development support.
- Use `context.Context` for request-scoped and cancelable work.
- Keep migrations as SQL files for non-trivial persistence.
- Do not install Go tools globally.
- Add tools through Nix or project-local commands.

## Race detector

- Add a `test-race` just recipe for non-trivial Go projects.
- Run `go test -race` on touched packages when concurrency or shared mutable state is involved.
- Keep race tests narrow by default.
