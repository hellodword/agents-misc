# Database and Driver Preferences

Apply when selecting a database/driver or configuring connections; preserve existing stacks and do not reconfigure connections for query-only edits.

## Selection

- Prefer SQLite for single-instance local workloads with modest write concurrency on a local filesystem; prefer PostgreSQL for multi-instance shared access, high write concurrency, or standalone database services.
- Greenfield driver defaults (preferring explicit SQL; adjust when CGO, target, or runtime constraints require):
  - Go + SQLite: `database/sql` with `github.com/mattn/go-sqlite3` (requires CGO)
  - Go + PostgreSQL: `pgx` (`pgxpool` when pooling is needed)
  - Rust sync SQLite: `rusqlite`; Rust async SQLite/PostgreSQL: `sqlx`

## SQLite connection initialization

- Apply connection-local pragmas to every opened connection (including pool members): enable foreign keys, prefer `WAL` for writable local databases, and start with a `5000ms` busy timeout (`_foreign_keys=on`, `_journal_mode=WAL`, `_busy_timeout=5000` for `go-sqlite3`; use each driver's native API rather than copying DSN syntax across drivers).
