---
id: core.backup-import-export
kind: core
triggers:
  - "backup"
  - "restore"
  - "import"
  - "export"
  - "destructive migration"
  - "SQLite backup"
summary: Protect user data through backup, import, export, and restore discipline.
companions: []
---

# Backup, Import, and Export Rules

## Backup

For SQLite apps with real user data, provide a backup/export path before destructive migrations.

Preferred backup techniques:

- SQLite Online Backup API for live app-managed backup;
- `VACUUM INTO` for compact backup snapshots when appropriate;
- sqlite CLI `.backup` when using CLI workflows.

Do not rely on plain file copy for live WAL-mode databases unless the app is stopped and all related files are handled correctly.

Default backup location:

    tmp/backups/

Do not commit backups.

## Export

Exports should be explicit user-facing artifacts, not accidental DB copies.

Document:

- export format;
- schema/version;
- encoding;
- timezone behavior;
- locale behavior;
- included/excluded fields;
- sensitive data handling.

## Import

Imports must validate:

- file type;
- version;
- required fields;
- constraints;
- duplicates;
- size limits;
- text encoding;
- timezone and locale assumptions.

Use transactions for imports.

Prefer dry-run validation for destructive imports.

## Restore

Restore commands must say whether they overwrite current data.

For real user data, require an existing backup or explicit confirmation outside the agent's assumptions.

## Aggressive early-stage mode

In explicitly requested aggressive mode, dev/test data may be reset, but the reset command must be documented and real user data must not be silently overwritten.
