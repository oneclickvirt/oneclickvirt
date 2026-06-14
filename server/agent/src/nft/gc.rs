use crate::error::ApiError;
use rusqlite::Connection;
use serde_json::Value;
use std::collections::HashSet;
use tracing::{debug, info, warn};

use super::counter::ensure_counter;
use super::{
    SCOPES, Scope, counter_name_in, counter_name_out, ensure_base_objects, is_not_found,
    remove_counter_by_name, run_nft,
};

fn list_existing_managed_counters(scope: Scope) -> Result<HashSet<String>, ApiError> {
    let out = run_nft(&["-j", "list", "table", scope.family, scope.table])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if is_not_found(&stderr) {
            return Ok(HashSet::new());
        }
        return Err(ApiError::internal(format!(
            "failed listing nft table json: {}",
            stderr.trim()
        )));
    }

    let json: Value = serde_json::from_slice(&out.stdout)
        .map_err(|e| ApiError::internal(format!("failed parsing nft table json: {e}")))?;
    let mut set = HashSet::new();
    if let Some(items) = json.get("nftables").and_then(Value::as_array) {
        for item in items {
            if let Some(name) = item
                .get("counter")
                .and_then(|c| c.get("name"))
                .and_then(Value::as_str)
            {
                set.insert(name.to_owned());
            }
        }
    }
    Ok(set)
}

fn expected_counters_from_db_for_scope(
    conn: &Connection,
    scope: Scope,
) -> Result<HashSet<String>, ApiError> {
    let mut stmt = conn
        .prepare("SELECT monitor_id, interface FROM interface_states")
        .map_err(|e| ApiError::internal(format!("prepare expected counters query error: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok((row.get::<_, i64>(0)?, row.get::<_, String>(1)?))
        })
        .map_err(|e| ApiError::internal(format!("expected counters query error: {e}")))?;

    let mut expected = HashSet::new();
    for row in rows {
        let (monitor_id, interface) =
            row.map_err(|e| ApiError::internal(format!("expected counters row error: {e}")))?;
        expected.insert(counter_name_in(scope, monitor_id, &interface));
        expected.insert(counter_name_out(scope, monitor_id, &interface));
    }
    Ok(expected)
}

pub fn garbage_collect_orphans(conn: &Connection) -> Result<usize, ApiError> {
    let mut total_removed = 0usize;
    let mut success_scopes = 0usize;
    let mut last_error: Option<String> = None;

    for scope in SCOPES {
        match (|| -> Result<usize, ApiError> {
            ensure_base_objects(scope)?;
            let existing = list_existing_managed_counters(scope)?;
            let expected = expected_counters_from_db_for_scope(conn, scope)?;
            let mut removed = 0usize;
            for counter in existing.difference(&expected) {
                remove_counter_by_name(scope, counter)?;
                removed += 1;
            }
            Ok(removed)
        })() {
            Ok(removed) => {
                success_scopes += 1;
                total_removed += removed;
            }
            Err(err) => {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    error = %err.message,
                    "orphan GC skipped for scope due to error"
                );
                last_error = Some(err.message);
            }
        }
    }

    if total_removed > 0 {
        info!(total_removed, "garbage-collected orphan nft counters/rules");
    }
    if success_scopes > 0 {
        Ok(total_removed)
    } else {
        Err(ApiError::internal(format!(
            "orphan GC failed in all scopes: {}",
            last_error.unwrap_or_else(|| "unknown error".to_string())
        )))
    }
}

pub fn bootstrap_from_db(conn: &Connection) -> Result<(), ApiError> {
    let mut success_scopes = 0usize;
    let mut last_error: Option<String> = None;
    for scope in SCOPES {
        match ensure_base_objects(scope) {
            Ok(()) => success_scopes += 1,
            Err(err) => {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    error = %err.message,
                    "failed to ensure base objects for scope during bootstrap"
                );
                last_error = Some(err.message);
            }
        }
    }
    if success_scopes == 0 {
        return Err(ApiError::internal(format!(
            "bootstrap failed in all scopes: {}",
            last_error.unwrap_or_else(|| "unknown error".to_string())
        )));
    }

    let mut stmt = conn
        .prepare("SELECT s.monitor_id, s.interface, m.inner_ip FROM interface_states s LEFT JOIN monitors m ON s.monitor_id = m.id")
        .map_err(|e| ApiError::internal(format!("prepare bootstrap query error: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok((
                row.get::<_, i64>(0)?,
                row.get::<_, String>(1)?,
                row.get::<_, Option<String>>(2)?,
            ))
        })
        .map_err(|e| ApiError::internal(format!("bootstrap query error: {e}")))?;

    let mut count = 0usize;
    for row in rows {
        let (monitor_id, interface, inner_ip) =
            row.map_err(|e| ApiError::internal(format!("bootstrap row error: {e}")))?;
        if let Err(err) = ensure_counter(monitor_id, &interface, inner_ip.as_deref()) {
            warn!(
                monitor_id,
                interface,
                error = %err.message,
                "bootstrap failed to ensure nft counter"
            );
        } else {
            count += 1;
        }
    }
    debug!(count, "nft bootstrap ensured counters");
    Ok(())
}
