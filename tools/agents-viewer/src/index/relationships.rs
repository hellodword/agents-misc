use std::collections::HashMap;

use anyhow::Result;
use sqlx::Row as _;

use super::Database;

#[derive(Clone, Debug)]
struct RelationRow {
    id: String,
    parent_thread_id: Option<String>,
    parent_relation: Option<String>,
    cwd: Option<String>,
    proposed_plan_hash: Option<String>,
    proposed_plan_at_micros: Option<i64>,
    handoff_plan_hash: Option<String>,
    handoff_at_micros: Option<i64>,
}

pub async fn reconcile_plan_handoffs(database: &Database) -> Result<Vec<String>> {
    let rows = sqlx::query(
        "SELECT id, parent_thread_id, parent_relation, cwd, proposed_plan_hash, \
            proposed_plan_at_micros, handoff_plan_hash, handoff_at_micros FROM sessions",
    )
    .fetch_all(database.pool())
    .await?
    .into_iter()
    .map(|row| RelationRow {
        id: row.get("id"),
        parent_thread_id: row.get("parent_thread_id"),
        parent_relation: row.get("parent_relation"),
        cwd: row.get("cwd"),
        proposed_plan_hash: row.get("proposed_plan_hash"),
        proposed_plan_at_micros: row.get("proposed_plan_at_micros"),
        handoff_plan_hash: row.get("handoff_plan_hash"),
        handoff_at_micros: row.get("handoff_at_micros"),
    })
    .collect::<Vec<_>>();

    let mut proposed = HashMap::<(&str, &str), Vec<(&str, i64)>>::new();
    for row in &rows {
        if let (Some(hash), Some(cwd), Some(at)) = (
            row.proposed_plan_hash.as_deref(),
            row.cwd.as_deref().filter(|cwd| !cwd.is_empty()),
            row.proposed_plan_at_micros,
        ) {
            proposed.entry((hash, cwd)).or_default().push((&row.id, at));
        }
    }

    let mut changes = Vec::new();
    for row in &rows {
        if matches!(row.parent_relation.as_deref(), Some("parent" | "fork")) {
            continue;
        }
        let desired_parent = match (
            row.handoff_plan_hash.as_deref(),
            row.cwd.as_deref().filter(|cwd| !cwd.is_empty()),
            row.handoff_at_micros,
        ) {
            (Some(hash), Some(cwd), Some(handoff_at)) => proposed
                .get(&(hash, cwd))
                .into_iter()
                .flatten()
                .filter(|(id, proposed_at)| *id != row.id && *proposed_at <= handoff_at)
                .max_by(|left, right| left.1.cmp(&right.1).then_with(|| right.0.cmp(left.0)))
                .map(|(id, _)| (*id).to_owned()),
            _ => None,
        };
        let desired_relation = desired_parent.as_ref().map(|_| "planHandoff");
        if row.parent_thread_id != desired_parent
            || row.parent_relation.as_deref() != desired_relation
        {
            changes.push((row.id.clone(), desired_parent));
        }
    }

    if changes.is_empty() {
        return Ok(Vec::new());
    }
    let mut transaction = database.pool().begin().await?;
    for (id, parent) in &changes {
        sqlx::query("UPDATE sessions SET parent_thread_id = ?, parent_relation = ? WHERE id = ?")
            .bind(parent)
            .bind(parent.as_ref().map(|_| "planHandoff"))
            .bind(id)
            .execute(&mut *transaction)
            .await?;
    }
    transaction.commit().await?;
    Ok(changes.into_iter().map(|(id, _)| id).collect())
}
