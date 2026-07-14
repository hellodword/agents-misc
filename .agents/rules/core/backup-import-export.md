---
id: core.backup-import-export
kind: core
triggers:
  - "backup"
  - "restore"
  - "data import"
  - "data export"
  - "destructive migration"
  - "SQLite backup"
summary: Protect user data through explicit backup, import, export, restore, and destructive-operation authorization.
companions:
  conditional_rules:
    - id: core.compatibility
      when: incompatible backup, import, export, restore, reset, or migration behavior is proposed
---

# Backup, Import, and Export Rules

Treat data as real unless project evidence proves it is disposable development/test state. An ignored or temporary-looking path is not proof.

## Backup

Before a destructive operation on real data, require both:

1. a verified backup or recovery path; and
2. explicit user authorization for the exact destructive operation.

A backup is not authorization. Authorization without a viable recovery path is not sufficient.

Choose the SQLite technique by operating conditions:

- Online Backup API for a live, app-managed backup;
- `VACUUM INTO` for an application-requested compact snapshot when its locking and disk-space behavior is acceptable;
- sqlite CLI `.backup` for an explicit operator or CLI workflow.

Do not plain-copy a live WAL-mode database unless the application is stopped and every required database file is handled correctly.

Use an ignored `tmp/backups/`-style location only for proven-disposable development/test backups. Put real backups in a user-controlled location outside the data path that the operation may overwrite. Never commit backups.

## Export

Exports are explicit user-facing artifacts, not accidental database copies. Document format and version, encoding, timezone and locale behavior, included/excluded fields, and sensitive-data handling.

## Import

Validate file type, version, required fields, constraints, duplicates, size limits, encoding, and timezone/locale assumptions. Normalize only when the import contract defines normalization. Use transactions and provide dry-run validation when an import may overwrite or delete data.

## Restore

State whether restore overwrites current data. For real data, verify the source backup and require explicit authorization for the exact overwrite. Do not infer authorization from the existence of a backup or from a general request to inspect recovery behavior.

Compatibility authorization under `core.compatibility` may cover a proven-disposable development/test reset only when its exact scope is recorded. It never implicitly covers real data.
