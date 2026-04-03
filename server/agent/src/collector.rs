use crate::{
    app_state::AppState,
    db::{AUTO_CLEANUP_SECONDS, cleanup_old_resource_metrics, cleanup_stale_monitors, now_ts},
    error::ApiError,
    nft::{garbage_collect_orphans, read_external_bytes},
    resource,
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
        return None;
    }

    // Validate: only allow alphanumeric, dash, underscore, dot (standard Linux interface names).
    // This prevents command injection via crafted interface names fed into nft scripts.
    if !base
        .chars()
        .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_' || c == '.')
    {
        return None;
    }

    // Linux interface names are max 15 chars
    if base.len() > 15 {
        return None;
    }

    Some(base.to_owned())
}

fn collect_once(conn: &Connection) -> Result<(), ApiError> {
    let now = now_ts();

    let mut monitor_stmt = conn
        .prepare("SELECT id, total_bytes, total_bytes_in, total_bytes_out FROM monitors")
        .map_err(|e| ApiError::internal(format!("prepare monitor query error: {e}")))?;
    let monitor_rows = monitor_stmt
        .query_map([], |row| Ok((row.get::<_, i64>(0)?, row.get::<_, u64>(1)?, row.get::<_, u64>(2)?, row.get::<_, u64>(3)?)))
        .map_err(|e| ApiError::internal(format!("monitor query error: {e}")))?;

    let mut monitors: Vec<(i64, u64, u64, u64)> = Vec::new();
    for row in monitor_rows {
        monitors.push(row.map_err(|e| ApiError::internal(format!("monitor row error: {e}")))?);
    }

    for (monitor_id, total_bytes, total_bytes_in, total_bytes_out) in monitors {
        let mut state_stmt = conn
            .prepare("SELECT interface, last_counter_in, last_counter_out FROM interface_states WHERE monitor_id = ?1")
            .map_err(|e| ApiError::internal(format!("prepare state query error: {e}")))?;
        let state_rows = state_stmt
            .query_map(params![monitor_id], |row| {
                Ok((row.get::<_, String>(0)?, row.get::<_, u64>(1)?, row.get::<_, u64>(2)?))
            })
            .map_err(|e| ApiError::internal(format!("state query error: {e}")))?;

        let mut increment_in: u64 = 0;
        let mut increment_out: u64 = 0;
        let mut updated_states: Vec<(String, u64, u64)> = Vec::new();
        let mut has_readable_interface = false;

        for row in state_rows {
            let (interface, last_counter_in, last_counter_out) =
                row.map_err(|e| ApiError::internal(format!("state row error: {e}")))?;
            if let Some((current_in, current_out)) = read_external_bytes(monitor_id, &interface) {
                has_readable_interface = true;
                if current_in >= last_counter_in {
                    increment_in = increment_in.saturating_add(current_in - last_counter_in);
                } else {
                    increment_in = increment_in.saturating_add(current_in);
                }
                if current_out >= last_counter_out {
                    increment_out = increment_out.saturating_add(current_out - last_counter_out);
                } else {
                    increment_out = increment_out.saturating_add(current_out);
                }
                updated_states.push((interface, current_in, current_out));
            }
        }

        for (interface, new_counter_in, new_counter_out) in updated_states {
            conn.execute(
                "UPDATE interface_states SET last_counter_in = ?1, last_counter_out = ?2 WHERE monitor_id = ?3 AND interface = ?4",
                params![new_counter_in, new_counter_out, monitor_id, interface],
            )
            .map_err(|e| ApiError::internal(format!("update state error: {e}")))?;
        }

        if has_readable_interface {
            let increment = increment_in.saturating_add(increment_out);
            let new_total = total_bytes.saturating_add(increment);
            let new_total_in = total_bytes_in.saturating_add(increment_in);
            let new_total_out = total_bytes_out.saturating_add(increment_out);
            conn.execute(
                "UPDATE monitors SET total_bytes = ?1, total_bytes_in = ?2, total_bytes_out = ?3, updated_at = ?4 WHERE id = ?5",
                params![new_total, new_total_in, new_total_out, now, monitor_id],
            )
            .map_err(|e| ApiError::internal(format!("update monitor total error: {e}")))?;
            debug!(monitor_id, increment_in, increment_out, new_total, "collector updated traffic stats");
        }
    }

    Ok(())
}

fn collect_resources(conn: &Connection) -> Result<(), ApiError> {
    let now = now_ts();

    let mut stmt = conn
        .prepare("SELECT id, provider_kind, instance_name FROM monitors WHERE provider_kind IS NOT NULL AND instance_name IS NOT NULL")
        .map_err(|e| ApiError::internal(format!("prepare resource monitor query error: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok((
                row.get::<_, i64>(0)?,
                row.get::<_, Option<String>>(1)?,
                row.get::<_, Option<String>>(2)?,
            ))
        })
        .map_err(|e| ApiError::internal(format!("resource monitor query error: {e}")))?;

    let mut monitors = Vec::new();
    for row in rows {
        monitors.push(row.map_err(|e| ApiError::internal(format!("resource monitor row error: {e}")))?);
    }

    let snapshots = resource::collect_all_resources(&monitors);

    for (monitor_id, snap) in &snapshots {
        conn.execute(
            "INSERT INTO resource_metrics (monitor_id, timestamp, cpu_percent, memory_used, memory_total, disk_used, disk_total) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)",
            params![monitor_id, now, snap.cpu_percent, snap.memory_used, snap.memory_total, snap.disk_used, snap.disk_total],
        )
        .map_err(|e| ApiError::internal(format!("insert resource metric error: {e}")))?;
    }

    if !snapshots.is_empty() {
        debug!(count = snapshots.len(), "collected resource metrics");
    }

    Ok(())
}

pub fn start_collector(state: AppState) {
    let traffic_interval = state.traffic_collect_interval.max(1);
    let resource_interval = state.resource_collect_interval.max(10);

    // Calculate how many traffic ticks equal one resource collection cycle
    let resource_ticks = (resource_interval / traffic_interval).max(1);

    info!(
        traffic_interval_secs = traffic_interval,
        resource_interval_secs = resource_interval,
        resource_ticks,
        "collector started"
    );

    tokio::spawn(async move {
        let mut ticks: u64 = 0;
        loop {
            ticks = ticks.saturating_add(1);
            {
                let conn = state.conn.lock().await;
                if let Err(err) = collect_once(&conn) {
                    error!(error = %err.message, "collector iteration failed");
                }
                // Collect resource metrics at configured interval
                if ticks % resource_ticks == 0 {
                    if let Err(err) = collect_resources(&conn) {
                        error!(error = %err.message, "resource collection failed");
                    }
                    // Clean up resource metrics older than 24 hours
                    if let Err(err) = cleanup_old_resource_metrics(&conn) {
                        error!(error = %err.message, "resource metrics cleanup failed");
                    }
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
            tokio::time::sleep(Duration::from_secs(traffic_interval)).await;
        }
    });
}
