# SQLite Data and Migrations

Apply this rule only after repository evidence or the user selects SQLite.

## Data location and connections

- Use an explicitly documented, ignored `tmp/app.sqlite` only for disposable development data.
- Put test databases in the test framework's isolated temporary directory.
- Put real application data in the platform-appropriate per-user or service application-data directory.
- Treat an unknown database as real data. Never commit databases or backups.
- Enable `PRAGMA foreign_keys = ON`, WAL journal mode, and a 5000 ms busy timeout on every applicable connection. Verify the target filesystem supports WAL semantics.
- For a concurrent service, set maximum and idle connection counts to 4. For a synchronous Rust CLI or desktop process using rusqlite, use one connection.
- Default to `INTEGER PRIMARY KEY`. Use UUID or ULID only for public non-enumerability, offline creation, or cross-device merge requirements.

## Migration contract

- Name migrations `migrations/0001_description.sql` with four-digit, strictly increasing versions.
- Embed migration SQL in the application artifact.
- Never edit, reorder, renumber, or squash an applied migration.
- Maintain this ledger:

```sql
CREATE TABLE schema_migrations (
  version INTEGER PRIMARY KEY,
  name TEXT NOT NULL,
  checksum TEXT NOT NULL,
  applied_at TEXT NOT NULL
);
```

- Calculate `checksum` as SHA-256 of the embedded migration bytes.
- Write `applied_at` as UTC RFC 3339 with millisecond precision.
- Before serving requests, apply migrations in order. Run each migration transactionally when its statements support a transaction.
- Stop startup and report an actionable error on a checksum mismatch, version gap, or migration failure. Never rebuild automatically.
- Migrate forward only. Recover through a compensating migration or a previously verified backup/restore path.

## Backup, restore, import, and export

- Use the SQLite Online Backup API for live application-managed backup, `VACUUM INTO` for a suitable compact offline snapshot, or the SQLite CLI `.backup` command for operator workflows.
- Under WAL, never substitute a plain database-file copy for an online-consistent backup.
- Store real backups in a user-controlled location outside the overwrite target.
- Validate backup creation and restoration against a disposable target before relying on recovery.
- Define import conflict, transaction, validation, and partial-failure behavior in the contract. Keep exports deterministic and exclude secrets unless explicitly required.
- Obtain explicit authorization for the exact destructive real-data operation after verifying recovery. A backup does not grant authorization.
