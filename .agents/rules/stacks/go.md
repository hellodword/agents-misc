# Go Rules

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
