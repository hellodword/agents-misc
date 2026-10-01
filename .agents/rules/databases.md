# Database and Driver Preferences

Use these preferences when choosing a database or driver, establishing a data layer, or changing connection configuration. Preserve an existing stack and settings unless the task requires changing them. Query-only changes do not call for connection reconfiguration.

## Database choice

- Prefer SQLite for local applications and a single application instance with modest concurrent writes and a suitable local filesystem.
- Prefer PostgreSQL when multiple application instances share the database, substantial concurrent writes are expected, or the database needs to be an independent service.
- Decide from deployment, concurrency, and operational needs rather than codebase size. These preferences do not prescribe migration or backup tooling.

## Preferred drivers

| Language and workload                              | Starting choice                                  |
| -------------------------------------------------- | ------------------------------------------------ |
| Go with SQLite                                     | `database/sql` and `github.com/mattn/go-sqlite3` |
| Go with PostgreSQL                                 | `pgx`, with `pgxpool` when pooling is needed     |
| Rust with synchronous SQLite access                | `rusqlite`                                       |
| Rust with asynchronous SQLite or PostgreSQL access | SQLx                                             |

Prefer explicit SQL where it fits the application. Choose a different library when the existing stack, target, runtime, CGO constraints, or required features justify it. In particular, `go-sqlite3` requires CGO and a C toolchain. Verify current package names, features, and connection options before adopting them.

## SQLite connection defaults

- Enable foreign keys on each connection. Prefer WAL for a writable database on a supported local filesystem when its concurrency model fits.
- Start with a 5-second busy timeout, adjusting it to the workload and latency requirements.
- For `go-sqlite3`, the corresponding DSN options are `_foreign_keys=on`, `_journal_mode=WAL`, and `_busy_timeout=5000` where applicable. For other drivers, use their documented options or connection initialization; do not copy DSN syntax between libraries.
- Apply connection-local settings to each newly opened connection, including connections created by a pool. Choose pool limits from workload and driver semantics; Go idle limits and SQLx minimum connections are different controls. A synchronous local process may need only one connection.
- PostgreSQL pooling, TLS, and timeouts follow deployment requirements and driver semantics; there is no shared numeric default.
