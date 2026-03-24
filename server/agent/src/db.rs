use crate::error::ApiError;
use rusqlite::{Connection, params};

pub const AUTO_CLEANUP_SECONDS: i64 = 30 * 24 * 60 * 60;

pub fn init_db(conn: &Connection) -> Result<(), ApiError> {
    conn.execute("PRAGMA foreign_keys = ON", [])
        .map_err(|e| ApiError::internal(format!("sqlite pragma error: {e}")))?;

    conn.execute_batch(
        r#"
        CREATE TABLE IF NOT EXISTS monitors (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            interfaces TEXT NOT NULL,
            total_bytes INTEGER NOT NULL DEFAULT 0,
            updated_at INTEGER NOT NULL
        );

        CREATE TABLE IF NOT EXISTS interface_states (
            monitor_id INTEGER NOT NULL,
            interface TEXT NOT NULL,
            last_counter INTEGER NOT NULL DEFAULT 0,
            PRIMARY KEY (monitor_id, interface),
            FOREIGN KEY(monitor_id) REFERENCES monitors(id) ON DELETE CASCADE
        );
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
