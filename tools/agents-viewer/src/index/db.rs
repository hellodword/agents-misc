use std::path::Path;
use std::str::FromStr as _;
use std::time::Duration;

use anyhow::{Context as _, Result, bail};
use sha2::{Digest as _, Sha256};
use sqlx::sqlite::{SqliteConnectOptions, SqliteJournalMode, SqlitePoolOptions, SqliteSynchronous};
use sqlx::{ConnectOptions as _, Executor as _, SqlitePool};
use tracing::log::LevelFilter;

use crate::permissions::{open_secure_file, validate_cache_file};
use crate::rollout::PARSER_VERSION;

use super::recovery;

const MICROS_PER_DAY: i64 = 86_400_000_000;
const SCHEMA_SQL: &str = include_str!("../../schema.sql");
const SCHEMA_SIGNATURE_KEY: &str = "schema_signature";

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct InitialIndexPolicy {
    pub days: i64,
    pub cutoff_micros: Option<i64>,
}

impl InitialIndexPolicy {
    pub fn new(days: i64, now_micros: i64) -> Result<Self> {
        if days < -1 {
            bail!("initial index days must be -1 or non-negative");
        }
        let cutoff_micros = match days {
            -1 => None,
            0 => Some(now_micros),
            _ => Some(
                now_micros
                    .checked_sub(
                        days.checked_mul(MICROS_PER_DAY)
                            .context("index window overflow")?,
                    )
                    .context("index cutoff overflow")?,
            ),
        };
        Ok(Self {
            days,
            cutoff_micros,
        })
    }

    #[must_use]
    pub const fn all() -> Self {
        Self {
            days: -1,
            cutoff_micros: None,
        }
    }

    #[must_use]
    pub fn includes(self, created_at_micros: i64) -> bool {
        self.cutoff_micros
            .is_none_or(|cutoff| created_at_micros >= cutoff)
    }
}

#[derive(Clone)]
pub struct Database {
    pool: SqlitePool,
}

pub struct OpenedDatabase {
    pub database: Database,
    pub bootstrap_required: bool,
}

impl Database {
    pub async fn open_or_recover(path: &Path, source_fingerprint: &str) -> Result<Self> {
        Ok(
            Self::open_or_recover_with_disposition(path, source_fingerprint)
                .await?
                .database,
        )
    }

    pub async fn open_or_recover_with_disposition(
        path: &Path,
        source_fingerprint: &str,
    ) -> Result<OpenedDatabase> {
        if path.exists() {
            validate_cache_file(path).context("validate existing database permissions")?;
        } else {
            drop(open_secure_file(path, true).context("create secure database file")?);
        }

        let pool = match connect_and_check(path).await {
            Ok(pool) => pool,
            Err(corrupt_error) => {
                recovery::preserve_database_family(path, "corrupt")
                    .context("preserve corrupt database")?;
                drop(open_secure_file(path, true).context("create replacement database")?);
                connect_and_check(path)
                    .await
                    .with_context(|| format!("database recovery failed after: {corrupt_error:#}"))?
            }
        };
        let database = match Self::initialize(pool.clone(), source_fingerprint).await {
            Ok(database) => database,
            Err(incompatible_error) => {
                pool.close().await;
                recovery::preserve_database_family(path, "incompatible")
                    .context("preserve incompatible database")?;
                drop(open_secure_file(path, true).context("create replacement database")?);
                let replacement = connect_and_check(path).await?;
                Self::initialize(replacement, source_fingerprint)
                    .await
                    .with_context(|| {
                        format!("database recovery failed after: {incompatible_error:#}")
                    })?
            }
        };
        let bootstrap_required = database.bootstrap_required().await?;
        Ok(OpenedDatabase {
            database,
            bootstrap_required,
        })
    }

    pub(super) async fn open_strict(path: &Path, source_fingerprint: &str) -> Result<Self> {
        validate_cache_file(path).context("validate rebuilt database permissions")?;
        let pool = connect_and_check(path).await?;
        Self::initialize(pool, source_fingerprint).await
    }

    async fn initialize(pool: SqlitePool, source_fingerprint: &str) -> Result<Self> {
        let expected_signature = schema_signature();
        if recovery::has_application_schema(&pool).await? {
            let stored_signature =
                sqlx::query_scalar::<_, String>("SELECT value FROM app_meta WHERE key = ?")
                    .bind(SCHEMA_SIGNATURE_KEY)
                    .fetch_optional(&pool)
                    .await
                    .context("read SQLite schema signature")?;
            if stored_signature.as_deref() != Some(expected_signature.as_str()) {
                bail!("database schema does not match this agents-viewer build");
            }
        } else {
            let mut transaction = pool.begin().await?;
            sqlx::raw_sql(SCHEMA_SQL)
                .execute(&mut *transaction)
                .await
                .context("initialize SQLite schema")?;
            sqlx::query("INSERT INTO app_meta(key, value) VALUES (?, ?)")
                .bind(SCHEMA_SIGNATURE_KEY)
                .bind(&expected_signature)
                .execute(&mut *transaction)
                .await?;
            transaction.commit().await?;
        }

        let fts_enabled =
            sqlx::query_scalar::<_, i64>("SELECT sqlite_compileoption_used('ENABLE_FTS5')")
                .fetch_one(&pool)
                .await
                .context("probe SQLite FTS5")?;
        if fts_enabled != 1 {
            pool.close().await;
            bail!("SQLite was built without ENABLE_FTS5");
        }

        let previous_parser_version = sqlx::query_scalar::<_, String>(
            "SELECT value FROM app_meta WHERE key = 'parser_version'",
        )
        .fetch_optional(&pool)
        .await?;
        let parser_changed = previous_parser_version
            .as_deref()
            .is_some_and(|version| version != PARSER_VERSION.to_string());
        let mut transaction = pool.begin().await?;
        for (key, value) in [
            ("parser_version", PARSER_VERSION.to_string()),
            ("source_fingerprint", source_fingerprint.to_owned()),
        ] {
            sqlx::query(
                "INSERT INTO app_meta(key, value) VALUES (?, ?) \
                 ON CONFLICT(key) DO UPDATE SET value = excluded.value",
            )
            .bind(key)
            .bind(value)
            .execute(&mut *transaction)
            .await?;
        }
        if parser_changed {
            sqlx::query(
                "UPDATE source_files SET scan_state = 'pending', checkpoint_offset = 0, \
                    checkpoint_line = 0, checkpoint_hash = NULL",
            )
            .execute(&mut *transaction)
            .await?;
        }
        cleanup_staging(&mut transaction).await?;
        transaction.commit().await?;

        Ok(Self { pool })
    }

    #[must_use]
    pub fn pool(&self) -> &SqlitePool {
        &self.pool
    }

    pub async fn close(self) {
        self.pool.close().await;
    }

    pub async fn bootstrap_required(&self) -> Result<bool> {
        let complete = sqlx::query_scalar::<_, String>(
            "SELECT value FROM app_meta WHERE key = 'bootstrap_complete'",
        )
        .fetch_optional(&self.pool)
        .await?;
        Ok(complete.as_deref() != Some("1"))
    }

    pub async fn mark_bootstrap_complete(&self) -> Result<()> {
        sqlx::query(
            "INSERT INTO app_meta(key, value) VALUES ('bootstrap_complete', '1') \
             ON CONFLICT(key) DO UPDATE SET value = excluded.value",
        )
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    pub async fn optimize(&self) -> Result<()> {
        sqlx::query("PRAGMA optimize").execute(&self.pool).await?;
        sqlx::query("PRAGMA wal_checkpoint(PASSIVE)")
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    pub async fn resolve_index_policy(
        &self,
        requested_days: i64,
        requested_max_event_bytes: usize,
        now_micros: i64,
    ) -> Result<InitialIndexPolicy> {
        let mut transaction = self.pool.begin().await?;
        let stored_days = meta_value(&mut transaction, "initial_index_days").await?;
        let stored_cutoff = meta_value(&mut transaction, "initial_index_cutoff_micros").await?;
        let stored_max_bytes = meta_value(&mut transaction, "max_event_bytes").await?;
        let indexed_files = sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM source_files")
            .fetch_one(&mut *transaction)
            .await?;

        let policy = match (stored_days, stored_cutoff) {
            (Some(days), cutoff) => {
                let days = days
                    .parse::<i64>()
                    .context("invalid stored initial_index_days")?;
                if days != requested_days {
                    bail!(
                        "initial_index_days changed from {days} to {requested_days}; run agents-viewer --rebuild-index to apply the new fixed window"
                    );
                }
                let cutoff_micros = match (days, cutoff) {
                    (-1, None) => None,
                    (-1, Some(value)) if value.is_empty() => None,
                    (-1, Some(_)) => bail!("invalid stored cutoff for all-history index"),
                    (_, Some(value)) => Some(
                        value
                            .parse::<i64>()
                            .context("invalid stored initial_index_cutoff_micros")?,
                    ),
                    (_, None) => bail!("stored initial index cutoff is missing"),
                };
                InitialIndexPolicy {
                    days,
                    cutoff_micros,
                }
            }
            (None, None) if indexed_files == 0 => {
                let policy = InitialIndexPolicy::new(requested_days, now_micros)?;
                set_meta(
                    &mut transaction,
                    "initial_index_days",
                    &policy.days.to_string(),
                )
                .await?;
                if let Some(cutoff) = policy.cutoff_micros {
                    set_meta(
                        &mut transaction,
                        "initial_index_cutoff_micros",
                        &cutoff.to_string(),
                    )
                    .await?;
                }
                policy
            }
            (None, None) => {
                if requested_days != -1 {
                    bail!(
                        "existing index predates fixed initial_index_days metadata; run agents-viewer --rebuild-index to apply the configured window"
                    );
                }
                let policy = InitialIndexPolicy::all();
                set_meta(&mut transaction, "initial_index_days", "-1").await?;
                policy
            }
            _ => {
                bail!(
                    "stored initial index policy is incomplete; run agents-viewer --rebuild-index"
                )
            }
        };

        if let Some(stored) = stored_max_bytes {
            let stored = stored
                .parse::<usize>()
                .context("invalid stored max_event_bytes")?;
            if stored != requested_max_event_bytes {
                bail!(
                    "max_event_bytes changed from {stored} to {requested_max_event_bytes}; run agents-viewer --rebuild-index to apply it consistently"
                );
            }
        } else if indexed_files != 0 {
            bail!(
                "existing index predates max_event_bytes metadata; run agents-viewer --rebuild-index to apply the configured limit"
            );
        } else {
            set_meta(
                &mut transaction,
                "max_event_bytes",
                &requested_max_event_bytes.to_string(),
            )
            .await?;
        }
        transaction.commit().await?;
        Ok(policy)
    }
}

async fn meta_value(
    transaction: &mut sqlx::Transaction<'_, sqlx::Sqlite>,
    key: &str,
) -> Result<Option<String>> {
    Ok(
        sqlx::query_scalar::<_, String>("SELECT value FROM app_meta WHERE key = ?")
            .bind(key)
            .fetch_optional(&mut **transaction)
            .await?,
    )
}

async fn set_meta(
    transaction: &mut sqlx::Transaction<'_, sqlx::Sqlite>,
    key: &str,
    value: &str,
) -> Result<()> {
    sqlx::query("INSERT INTO app_meta(key, value) VALUES (?, ?)")
        .bind(key)
        .bind(value)
        .execute(&mut **transaction)
        .await?;
    Ok(())
}

async fn connect(path: &Path) -> Result<SqlitePool> {
    let url = format!("sqlite://{}", path.to_string_lossy());
    let options = SqliteConnectOptions::from_str(&url)?
        .create_if_missing(true)
        .journal_mode(SqliteJournalMode::Wal)
        .synchronous(SqliteSynchronous::Normal)
        .busy_timeout(Duration::from_millis(5_000))
        .foreign_keys(true)
        .pragma("auto_vacuum", "INCREMENTAL")
        .log_statements(LevelFilter::Off);
    SqlitePoolOptions::new()
        .max_connections(5)
        .after_connect(|connection, _metadata| {
            Box::pin(async move {
                connection.execute("PRAGMA foreign_keys=ON").await?;
                connection.execute("PRAGMA busy_timeout=5000").await?;
                Ok(())
            })
        })
        .connect_with(options)
        .await
        .context("open SQLite database")
}

async fn connect_and_check(path: &Path) -> Result<SqlitePool> {
    let pool = connect(path).await?;
    // Keep cache startup independent of database size. A full integrity check scans every
    // indexed payload page and made opening a multi-gigabyte, already-valid cache take minutes.
    // Checking sqlite_schema still catches structural corruption before baseline validation; corrupt
    // headers also fail while opening the connection. Runtime read failures remain surfaced.
    let integrity = match sqlx::query_scalar::<_, String>("PRAGMA integrity_check('sqlite_schema')")
        .fetch_one(&pool)
        .await
        .context("run SQLite integrity check")
    {
        Ok(value) => value,
        Err(error) => {
            pool.close().await;
            return Err(error);
        }
    };
    if integrity != "ok" {
        pool.close().await;
        bail!("SQLite integrity check failed: {integrity}");
    }
    Ok(pool)
}

fn schema_signature() -> String {
    format!("{:x}", Sha256::digest(SCHEMA_SQL.as_bytes()))
}

async fn cleanup_staging(transaction: &mut sqlx::Transaction<'_, sqlx::Sqlite>) -> Result<()> {
    for statement in [
        "DELETE FROM staged_entry_raw_refs",
        "DELETE FROM staged_diagnostics",
        "DELETE FROM staged_entries",
        "DELETE FROM staged_raw_records",
        "DELETE FROM staged_sessions",
    ] {
        sqlx::query(statement).execute(&mut **transaction).await?;
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use tempfile::TempDir;

    use super::*;

    #[tokio::test]
    async fn initial_policy_is_fixed_and_config_mismatch_requires_rebuild() {
        let temp = TempDir::new_in(".").unwrap();
        let cache = temp.path().join("cache");
        crate::permissions::prepare_cache_directory(&cache).unwrap();
        let database = Database::open_or_recover(&cache.join("index.sqlite3"), "source")
            .await
            .unwrap();
        let first = database
            .resolve_index_policy(7, 32 * 1024 * 1024, 9 * MICROS_PER_DAY)
            .await
            .unwrap();
        assert_eq!(first.cutoff_micros, Some(2 * MICROS_PER_DAY));
        let later = database
            .resolve_index_policy(7, 32 * 1024 * 1024, 99 * MICROS_PER_DAY)
            .await
            .unwrap();
        assert_eq!(later, first);
        let error = database
            .resolve_index_policy(0, 32 * 1024 * 1024, 99 * MICROS_PER_DAY)
            .await
            .unwrap_err();
        assert!(format!("{error:#}").contains("--rebuild-index"));
        let error = database
            .resolve_index_policy(7, 16 * 1024 * 1024, 99 * MICROS_PER_DAY)
            .await
            .unwrap_err();
        assert!(format!("{error:#}").contains("--rebuild-index"));
        database.close().await;
    }

    #[test]
    fn zero_and_all_windows_have_explicit_semantics() {
        let zero = InitialIndexPolicy::new(0, 123).unwrap();
        assert!(!zero.includes(122));
        assert!(zero.includes(123));
        assert!(InitialIndexPolicy::all().includes(i64::MIN));
    }
}
