---
name: sqlite-migration-backup
description: Use this when changing SQLite schema, migrations, local database config, backup/export/import behavior, or data reset behavior.
---

# SQLite Migration and Backup

## Purpose

Keep SQLite data changes explicit and recoverable, and prevent disposable-state assumptions from reaching real data.

## Workflow

1. Classify the target as disposable development/test state only from explicit project or fixture evidence; otherwise treat it as real data.
2. Preserve the existing database path and journal contract. For greenfield disposable local state, `tmp/app.sqlite` is an available default.
3. Use durable migrations, enable foreign keys, and use transactions for multi-step writes.
4. Use WAL only when concurrent reads/writes require it and the target filesystem supports it.
5. Before destructive real-data work, verify a recovery path and obtain explicit authorization for the exact operation. A backup alone is not authorization.
6. Select Online Backup API for live app-managed backup, `VACUUM INTO` for a suitable compact snapshot, or sqlite `.backup` for operator/CLI workflows.
7. Keep real backups in a user-controlled location outside the overwrite target; use ignored project temp paths only for proven-disposable development/test backups.
8. Record a specific exception or confirmed aggressive scope before resetting proven-disposable development/test data.
9. Never commit databases or backups. Update schema docs, tests, examples, and recovery/reset instructions.

## Output

Report data classification evidence, schema and migration changes, backup/recovery path, authorization scope/evidence, reset behavior, validation commands, and compatibility mode.
