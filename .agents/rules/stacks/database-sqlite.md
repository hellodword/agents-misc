# SQLite Rules

- Default database: SQLite.
- Use native SQL by default.
- Do not introduce an ORM by default.
- Use migrations for non-trivial persistent schemas.
- Enable foreign keys for every application connection that needs relational integrity.
- Use transactions for multi-step writes.
- Use parameterized SQL.
- Keep schema docs or migration comments for non-obvious constraints.
- Do not commit real database files.
- Put local databases under ignored paths.
- Prefer synthetic fixtures for tests.
- Use WAL mode when the project benefits from concurrent read/write behavior and it is documented.
- Check PRAGMA results when changing journal mode or other connection-level behavior.

## Default local database location

For local development:

    tmp/app.sqlite

Default logical DSN for project docs/config:

    sqlite://tmp/app.sqlite?mode=rwc

## Go go-sqlite3 DSN

    file:tmp/app.sqlite?cache=shared&mode=rwc&_foreign_keys=1&_busy_timeout=5000&_journal_mode=WAL

## Rust SQLx DSN

Prefer this DSN in config:

    sqlite://tmp/app.sqlite

Then configure SQLite behavior in code with connection options:

- create database when appropriate;
- enable foreign keys;
- set WAL when appropriate;
- set busy timeout to 5 seconds;
- apply migrations before serving traffic.
