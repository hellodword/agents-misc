use std::path::{Path, PathBuf};

use anyhow::{Context as _, Result};
use chrono::Utc;
use sqlx::SqlitePool;

pub async fn has_application_schema(pool: &SqlitePool) -> Result<bool> {
    let count = sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*) FROM sqlite_master WHERE name NOT LIKE 'sqlite_%'",
    )
    .fetch_one(pool)
    .await?;
    Ok(count > 0)
}

pub async fn replace_database_atomically(
    database: &Path,
    rebuilt: &Path,
    source_fingerprint: &str,
) -> Result<super::Database> {
    let previous = database.with_extension("sqlite3.previous");
    if previous.exists() {
        std::fs::remove_file(&previous).context("remove stale previous index")?;
    }
    std::fs::rename(database, &previous).context("move current index to previous")?;
    if let Err(error) = std::fs::rename(rebuilt, database).context("activate rebuilt index") {
        std::fs::rename(&previous, database)
            .context("restore current index after rename failure")?;
        return Err(error);
    }
    match super::Database::open_strict(database, source_fingerprint).await {
        Ok(reopened) => {
            if let Err(error) = std::fs::remove_file(&previous) {
                reopened.close().await;
                let failed = database.with_extension("sqlite3.failed-new");
                let _ = std::fs::rename(database, failed);
                std::fs::rename(&previous, database)
                    .context("restore previous index after cleanup failure")?;
                return Err(error).context("remove previous index after rebuild");
            }
            Ok(reopened)
        }
        Err(error) => {
            let failed = database.with_extension("sqlite3.failed-new");
            let _ = std::fs::rename(database, failed);
            std::fs::rename(&previous, database)
                .context("restore previous index after validation failure")?;
            Err(error).context("validate activated rebuilt index")
        }
    }
}

pub fn preserve_database_family(database: &Path, classification: &str) -> Result<Vec<PathBuf>> {
    let stamp = Utc::now().format("%Y%m%dT%H%M%S%.6fZ");
    let mut preserved = Vec::new();
    for suffix in ["", "-wal", "-shm"] {
        let source = PathBuf::from(format!("{}{}", database.display(), suffix));
        if source.exists() {
            let destination = PathBuf::from(format!(
                "{}.{}-{}{}",
                database.display(),
                classification,
                stamp,
                suffix
            ));
            std::fs::rename(&source, &destination).with_context(|| {
                format!("preserve {} as {}", source.display(), destination.display())
            })?;
            preserved.push(destination);
        }
    }
    Ok(preserved)
}

pub async fn integrity_check(pool: &SqlitePool) -> Result<bool> {
    Ok(sqlx::query_scalar::<_, String>("PRAGMA integrity_check")
        .fetch_one(pool)
        .await?
        == "ok")
}
