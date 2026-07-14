---
id: core.data-migrations
kind: core
triggers:
  - "migration"
  - "database schema change"
  - "SQLite migration"
  - "data reset"
  - "schema version"
summary: Make schema and persisted data changes explicit, validated, and recoverable.
companions:
  required_rules:
    - core.compatibility
    - core.testing
  conditional_rules:
    - id: core.backup-import-export
      when: backup, restore, import, export, reset, destructive behavior, or user-owned data recovery is involved
    - id: core.data-privacy
      when: user data or PII is involved
  skills:
    - id: sqlite-migration-backup
      when: migration, backup, restore, import, or export workflow guidance is needed
  templates:
    - id: migration-plan
      when: producing a migration plan artifact
---

# Data Migration Rules

## Durable schemas

Use explicit, stably ordered migrations. Each migration needs a clear purpose, an atomic transaction where supported, a forward step, a compatibility note, and either rollback instructions or an explicit recovery path.

Use the project's migration ledger. When none exists, record at least a stable migration identifier and applied timestamp; add a checksum when the chosen migration design verifies immutable migration contents.

## Data classification and destructive changes

Ignored status, a `tmp/` path, or a local filename does not prove a database disposable. Require explicit project documentation, a test fixture/setup contract, or equivalent evidence that the target is development/test state. Otherwise treat it as real data.

Before a destructive operation:

- preserve data with a migration; or
- for proven-disposable development/test state, use a recorded specific exception or confirmed aggressive scope; or
- for real data, load `core.backup-import-export`, verify recovery, and obtain explicit authorization for the exact destructive operation.

Use transactions for multi-step writes. Use idempotent migrations only when the project migration mechanism defines that behavior; do not mask partial failure.

## Greenfield SQLite connection defaults

When SQLite is already chosen for a greenfield project:

- use an ignored project state path such as `tmp/app.sqlite` for explicitly disposable local development data;
- enable foreign-key enforcement;
- use a 5000 ms busy timeout unless product requirements differ;
- use WAL only when concurrent reads/writes need it and the target filesystem supports it; otherwise retain SQLite's default journal mode;
- never commit database files.

Any non-durable compatibility mode must record its exact scope and authorization evidence. Synchronize schema docs, tests, examples, and reset or recovery instructions.
