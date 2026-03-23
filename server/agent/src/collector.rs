use crate::{
    app_state::AppState,
    db::{AUTO_CLEANUP_SECONDS, cleanup_stale_monitors, now_ts},
    error::ApiError,
    nft::{garbage_collect_orphans, read_external_bytes},
};
use rusqlite::{Connection, params};
use std::time::Duration;
use tracing::{debug, error, info};

pub fn normalize_interface_name(raw: &str) -> Option<String> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return None;
    }

    // ip link often shows veth as "name@ifX", while /sys/class/net uses only "name".
    let base = trimmed.split('@').next().unwrap_or("").trim();
    if base.is_empty() {
        None
    } else {
        Some(base.to_owned())
    }
}

fn collect_once(conn: &Connection) -> Result<(), ApiError> {
    let now = now_ts();

    let mut monitor_stmt = conn
        .prepare("SELECT id, total_bytes FROM monitors")
        .map_err(|e| ApiError::internal(format!("prepare monitor query error: {e}")))?;
    let monitor_rows = monitor_stmt
        .query_map([], |row| Ok((row.get::<_, i64>(0)?, row.get::<_, u64>(1)?)))
        .map_err(|e| ApiError::internal(format!("monitor query error: {e}")))?;

    let mut monitors: Vec<(i64, u64)> = Vec::new();
    for row in monitor_rows {
        monitors.push(row.map_err(|e| ApiError::internal(format!("monitor row error: {e}")))?);
    }

    for (monitor_id, total_bytes) in monitors {
        let mut state_stmt = conn
            .prepare("SELECT interface, last_counter FROM interface_states WHERE monitor_id = ?1")
            .map_err(|e| ApiError::internal(format!("prepare state query error: {e}")))?;
        let state_rows = state_stmt
            .query_map(params![monitor_id], |row| {
                Ok((row.get::<_, String>(0)?, row.get::<_, u64>(1)?))
            })
            .map_err(|e| ApiError::internal(format!("state query error: {e}")))?;

        let mut increment: u64 = 0;
        let mut updated_states: Vec<(String, u64)> = Vec::new();
        let mut has_readable_interface = false;

        for row in state_rows {
            let (interface, last_counter) =
                row.map_err(|e| ApiError::internal(format!("state row error: {e}")))?;
            if let Some(current_counter) = read_external_bytes(monitor_id, &interface) {
                has_readable_interface = true;
                if current_counter >= last_counter {
                    increment = increment.saturating_add(current_counter - last_counter);
                } else {
                    increment = increment.saturating_add(current_counter);
                }
                updated_states.push((interface, current_counter));
            }
        }

        for (interface, new_counter) in updated_states {
            conn.execute(
                "UPDATE interface_states SET last_counter = ?1 WHERE monitor_id = ?2 AND interface = ?3",
                params![new_counter, monitor_id, interface],
            )
            .map_err(|e| ApiError::internal(format!("update state error: {e}")))?;
        }

        if has_readable_interface {
            let new_total = total_bytes.saturating_add(increment);
            conn.execute(
                "UPDATE monitors SET total_bytes = ?1, updated_at = ?2 WHERE id = ?3",
                params![new_total, now, monitor_id],
            )
            .map_err(|e| ApiError::internal(format!("update monitor total error: {e}")))?;
            debug!(monitor_id, increment, new_total, "collector updated traffic stats");
        }
    }

    Ok(())
}

pub fn start_collector(state: AppState) {
    tokio::spawn(async move {
        let mut ticks: u64 = 0;
        loop {
            ticks = ticks.saturating_add(1);
            {
                let conn = state.conn.lock().await;
                if let Err(err) = collect_once(&conn) {
                    error!(error = %err.message, "collector iteration failed");
                }
                match cleanup_stale_monitors(&conn, AUTO_CLEANUP_SECONDS) {
                    Ok(deleted) => {
                        if deleted > 0 {
                            info!(deleted, max_age_seconds = AUTO_CLEANUP_SECONDS, "auto cleanup removed stale monitors");
                            if let Err(err) = garbage_collect_orphans(&conn) {
                                error!(error = %err.message, "orphan nft GC after auto cleanup failed");
                            }
                        }
                    }
                    Err(err) => {
                        error!(error = %err.message, "auto cleanup failed");
                    }
                }
                if ticks % 60 == 0 {
                    match garbage_collect_orphans(&conn) {
                        Ok(removed) => {
                            if removed > 0 {
                                info!(removed, "periodic orphan nft GC removed stale rules");
                            }
                        }
                        Err(err) => {
                            error!(error = %err.message, "periodic orphan nft GC failed");
                        }
                    }
                }
            }
            tokio::time::sleep(Duration::from_secs(2)).await;
        }
    });
}
