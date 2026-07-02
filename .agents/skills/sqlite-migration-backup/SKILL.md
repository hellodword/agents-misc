---
name: sqlite-migration-backup
description: Use this when changing SQLite schema, migrations, local database config, backup/export/import behavior, or data reset behavior.
---

# SQLite Migration and Backup

## Purpose

Keep SQLite data changes explicit, reversible when practical, and safe for local or real user data.

## Workflow

1. Identify whether the database is disposable dev data or real user data.
2. Use default local DB path `tmp/app.sqlite` unless the project defines another path.
3. Use migrations for durable schemas.
4. Enable foreign keys for application connections.
5. Use transactions for multi-step writes.
6. Add backup/export guidance before destructive migrations.
7. Prefer Online Backup API, sqlite `.backup`, or `VACUUM INTO` for backups.
8. Do not commit database files or backups.
9. In aggressive early-stage mode, document reset command and data loss.
10. Update schema docs, tests, and examples.

## Output

Report:

- schema change;
- migration files;
- backup requirement;
- reset behavior;
- validation commands;
- compatibility mode.
