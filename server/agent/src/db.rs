use crate::error::ApiError;
use rusqlite::{Connection, params};

pub const AUTO_CLEANUP_SECONDS: i64 = 30 * 24 * 60 * 60;
pub const RESOURCE_RETENTION_SECONDS: i64 = 24 * 60 * 60; // 24 hours

pub fn init_db(conn: &Connection) -> Result<(), ApiError> {
    conn.execute("PRAGMA foreign_keys = ON", [])
        .map_err(|e| ApiError::internal(format!("sqlite pragma error: {e}")))?;

    conn.execute_batch(
        r#"
        CREATE TABLE IF NOT EXISTS monitors (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            interfaces TEXT NOT NULL,
            total_bytes INTEGER NOT NULL DEFAULT 0,
            provider_kind TEXT,
            instance_name TEXT,
            updated_at INTEGER NOT NULL
        );

        CREATE TABLE IF NOT EXISTS interface_states (
            monitor_id INTEGER NOT NULL,
            interface TEXT NOT NULL,
            last_counter INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY (monitor_id, interface),
            FOREIGN KEY(monitor_id) REFERENCES monitors(id) ON DELETE CASCADE
        );

        CREATE TABLE IF NOT EXISTS resource_metrics (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            monitor_id INTEGER NOT NULL,
            timestamp INTEGER NOT NULL,
            cpu_percent REAL NOT NULL DEFAULT 0.0,
            memory_used INTEGER NOT NULL DEFAULT 0,
            memory_total INTEGER NOT NULL DEFAULT 0,
            disk_used INTEGER NOT NULL DEFAULT 0,
            disk_total INTEGER NOT NULL DEFAULT 0,
            FOREIGN KEY(monitor_id) REFERENCES monitors(id) ON DELETE CASCADE
        );

        CREATE INDEX IF NOT EXISTS idx_resource_metrics_monitor_ts
            ON resource_metrics(monitor_id, timestamp);
        "#,
    )
    .map_err(|e| ApiError::internal(format!("sqlite table init error: {e}")))?;

    Ok(())
}

pub fn now_ts() -> i64 {
    use std::time::{SystemTime, UNIX_EPOCH};
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs() as i64)
        .unwrap_or(0)
}

pub fn cleanup_stale_monitors(conn: &Connection, max_age_seconds: i64) -> Result<usize, ApiError> {
    if max_age_seconds <= 0 {
        return Err(ApiError::bad_request("max age must be greater than 0"));
    }

    let threshold = now_ts().saturating_sub(max_age_seconds);
    conn.execute(
        "DELETE FROM monitors WHERE updated_at < ?1",
        params![threshold],
    )
    .map_err(|e| ApiError::internal(format!("cleanup stale monitors error: {e}")))
}

pub fn cleanup_old_resource_metrics(conn: &Connection) -> Result<usize, ApiError> {
    let threshold = now_ts().saturating_sub(RESOURCE_RETENTION_SECONDS);
    conn.execute(
        "DELETE FROM resource_metrics WHERE timestamp < ?1",
        params![threshold],
    )
    .map_err(|e| ApiError::internal(format!("cleanup old resource metrics error: {e}")))
}
