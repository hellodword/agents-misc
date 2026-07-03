---
id: core.data-migrations
kind: core
triggers:
  - 'migration'
  - 'schema change'
  - 'SQLite migration'
  - 'data reset'
  - 'schema version'
summary: Make schema and persisted data changes explicit, validated, and recoverable.
load_with:
  rules:
    - core.backup-import-export
    - stack.database-sqlite
    - core.testing
  skills:
    - sqlite-migration-backup
  templates:
    - migration-plan
---

# Data Migration Rules

## Default migration strategy

Use explicit migrations for durable SQLite schemas.

A migration should have:

- stable filename ordering;
- clear purpose;
- transaction where SQLite supports it;
- forward migration SQL;
- rollback or recovery note when practical;
- compatibility note when existing data shape changes.

## Migration table

Use a schema version table when the project does not already have a migration tool.

Minimum table intent:

- migration identifier;
- applied timestamp;
- checksum when practical.

## Migration safety

Before destructive changes:

- verify the data is disposable dev/test data; or
- write a preserving migration; or
- document backup/restore steps.

Ask the user only when the operation is destructive, irreversible, affects real user data, or cannot be proven to target disposable data.

Use transactions for multi-step writes.

Make migrations idempotent only when the migration tool or style supports it cleanly. Do not hide partial failure.

## Default SQLite connection behavior

- local database path: `tmp/app.sqlite`;
- foreign keys: enabled;
- busy timeout: 5000 ms;
- WAL: enabled when useful and verified;
- local DB files: ignored;
- real DB files: never committed.

## Aggressive early-stage mode

Use only when the user explicitly says one of:

- aggressive mode;
- early-stage aggressive mode;
- 可以破坏兼容;
- 可以重置数据;
- 可以不保留历史包袱;
- 早期激进更新.

Allowed in aggressive mode:

- delete and recreate local dev database;
- squash migrations;
- rewrite seed data;
- replace schema with no backward compatibility;
- drop legacy columns/tables without preserving data.

Still required:

- document reset command;
- keep generated schema/docs/tests synchronized;
- do not touch real user data;
- do not commit database files;
- do not perform unrelated rewrites.
