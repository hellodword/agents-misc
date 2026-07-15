---
name: sqlite-migration-backup
description: Execute safe SQLite schema, migration, backup, restore, import/export, path, and reset changes using the shared data contract. Use when those SQLite behaviors change; do not trigger for an unspecified database, read-only query work, or destructive real-data work without exact authorization and recovery.
---

# SQLite Migration and Backup

1. Read the shared SQLite data rule and classify the target as disposable only from explicit project/fixture evidence; otherwise treat it as real data.
2. Identify the current path, journal, connection, migration ledger, and backup/restore contracts.
3. Add the next immutable four-digit migration and embedded SQL. Calculate SHA-256, reject gaps or checksum drift, and migrate before serving.
4. Use a transaction when supported. Stop on failure without rebuilding or silently resetting.
5. For destructive real-data work, verify restore to a disposable target and obtain user authorization for the exact operation. A backup alone is not authorization.
6. Choose the Online Backup API for live app-managed backup, `VACUUM INTO` for a suitable compact snapshot, or SQLite CLI `.backup` for operator workflows. Never rely on a plain file copy under WAL.
7. Put real backups in a user-controlled location outside the overwrite target. Keep proven-disposable test backups under ignored temporary paths.
8. Define import validation, conflict, transaction, and partial-failure behavior; make exports deterministic and appropriately redacted.
9. Test an empty database, the previous supported schema, checksum/gap failures, and backup restoration.
10. Report classification evidence, migration, recovery path, authorization, reset behavior, commands, contract classification, and limitations.
