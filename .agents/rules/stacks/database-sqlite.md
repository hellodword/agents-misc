---
id: stack.database-sqlite
kind: stack
triggers:
  - "SQLite"
  - "database"
  - "DSN"
  - "WAL"
  - "foreign keys"
  - "SQL migrations"
summary: Apply SQLite defaults for native SQL, migrations, local database files, and validation.
companions:
  conditional_rules:
    - id: core.data-migrations
      when: schema, migration, or persisted data shape changes
    - id: core.backup-import-export
      when: backup, restore, import, export, reset, or destructive behavior is involved
    - id: core.compatibility
      when: durable schema or file format changes
    - id: core.testing
      when: validation is needed
  skills:
    - id: sqlite-migration-backup
      when: migration, backup, restore, import, or export workflow guidance is needed
---

# SQLite Rules

Do not introduce SQLite into an existing project that already has a clear storage strategy unless the user asks or the task requires a local embedded database.

- Prefer SQLite for local/default persistence.
- Use native SQL by default.
- Avoid introducing an ORM by default.
- Keep migrations explicit.
- Store durable schema/migration files in a stable directory such as `migrations/` or the existing project location.
- Do not commit local database files.
- Put disposable local databases under ignored paths such as `tmp/`, `.work/`, or another project-ignored state directory.
- Use transactions for multi-step writes.
- Enable foreign key enforcement where the driver requires it.
- Prefer integer primary keys or stable text ids based on product needs.
- Store timestamps in a consistent format and timezone policy.

## Go

Default Go SQLite stack:

- `database/sql`;
- `github.com/mattn/go-sqlite3`;
- native SQL;
- `sqlc` only when query volume or type-safety needs justify generated code.

## Rust

Default Rust SQLite stack:

- `sqlx` + SQLite;
- native SQL;
- checked macros for service or long-term projects when setup is worthwhile;
- runtime queries for small CLI/prototype projects when checked macros add too much setup.

## Validation

- Test migrations from an empty database.
- Test migration behavior from the previous schema when a previous schema exists.
- Test backup/restore or import/export behavior when user data is involved.
- Avoid destructive migration behavior unless explicitly requested and backed up.
