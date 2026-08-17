use crate::{
    app_state::AppState,
    db::{AUTO_CLEANUP_SECONDS, cleanup_old_resource_metrics, cleanup_stale_monitors, now_ts},
    error::ApiError,
    ipt,
    models::TrafficBinding,
    nft, resource,
    traffic::{interface_exists, parse_persisted_bindings},
};
use rusqlite::{Connection, params, params_from_iter};
use std::{
    collections::{HashMap, HashSet},
    time::Duration,
};
use tracing::{debug, error, info, warn};

pub fn normalize_interface_name(raw: &str) -> Option<String> {
    let trimmed = raw.trim();
    if trimmed.is_empty() {
        return None;
    }

    let base = trimmed.split('@').next().unwrap_or("").trim();
    if base.is_empty() {
        return None;
    }
    if !base
        .chars()
        .all(|c| c.is_ascii_alphanumeric() || c == '-' || c == '_' || c == '.')
    {
        return None;
    }
    if base.len() > 15 {
        return None;
    }
    Some(base.to_owned())
}

#[derive(Debug)]
struct TrafficStateSnapshot {
    row_id: i64,
    monitor_id: i64,
    interface: String,
    last_counter_in: u64,
    last_counter_out: u64,
}

#[derive(Debug)]
struct TrafficReading {
    snapshot: TrafficStateSnapshot,
    current_in: u64,
    current_out: u64,
}

#[derive(Debug)]
struct DesiredMonitor {
    id: i64,
    bindings: Vec<TrafficBinding>,
}

#[derive(Debug)]
struct CounterActivation {
    monitor_id: i64,
    binding: TrafficBinding,
    base_in: u64,
    base_out: u64,
}

#[derive(Debug)]
struct CounterStateChange {
    monitor_id: i64,
    interface: String,
    expected_in: u64,
    expected_out: u64,
    base_in: u64,
    base_out: u64,
    increment_in: u64,
    increment_out: u64,
    deactivate: bool,
}

fn counter_increment(previous: u64, current: u64) -> u64 {
    if current >= previous {
        current - previous
    } else {
        current
    }
}

fn read_counter(use_ipt: bool, monitor_id: i64, interface: &str) -> Option<(u64, u64)> {
    if use_ipt {
        ipt::read_external_bytes(monitor_id, interface)
    } else {
        nft::read_external_bytes(monitor_id, interface)
    }
}

fn settled_state_change(
    state: &TrafficStateSnapshot,
    before: Option<(u64, u64)>,
    after: Option<(u64, u64)>,
    deactivate: bool,
) -> CounterStateChange {
    let (before_in, before_out) = before.unwrap_or((state.last_counter_in, state.last_counter_out));
    let (base_in, base_out) = after.unwrap_or((before_in, before_out));
    let mut increment_in = counter_increment(state.last_counter_in, before_in);
    let mut increment_out = counter_increment(state.last_counter_out, before_out);
    if after.is_some() {
        if base_in >= before_in {
            increment_in = increment_in.saturating_add(base_in - before_in);
        }
        if base_out >= before_out {
            increment_out = increment_out.saturating_add(base_out - before_out);
        }
    }
    CounterStateChange {
        monitor_id: state.monitor_id,
        interface: state.interface.clone(),
        expected_in: state.last_counter_in,
        expected_out: state.last_counter_out,
        base_in,
        base_out,
        increment_in,
        increment_out,
        deactivate,
    }
}

fn load_traffic_batch(
    conn: &Connection,
    cursor: i64,
    batch_size: usize,
) -> Result<Vec<TrafficStateSnapshot>, ApiError> {
    let mut stmt = conn
        .prepare(
            "SELECT rowid, monitor_id, interface, last_counter_in, last_counter_out \
             FROM interface_states WHERE rowid > ?1 ORDER BY rowid LIMIT ?2",
        )
        .map_err(|e| ApiError::internal(format!("prepare traffic batch query error: {e}")))?;
    let rows = stmt
        .query_map(params![cursor, batch_size.max(1) as i64], |row| {
            Ok(TrafficStateSnapshot {
                row_id: row.get(0)?,
                monitor_id: row.get(1)?,
                interface: row.get(2)?,
                last_counter_in: row.get(3)?,
                last_counter_out: row.get(4)?,
            })
        })
        .map_err(|e| ApiError::internal(format!("traffic batch query error: {e}")))?;
    rows.collect::<Result<Vec<_>, _>>()
        .map_err(|e| ApiError::internal(format!("traffic batch row error: {e}")))
}

async fn collect_traffic_batch(
    state: &AppState,
    use_ipt: bool,
    cursor: i64,
) -> Result<i64, ApiError> {
    let _operation_guard = state.traffic_operation_lock.lock().await;
    let batch_size = state.traffic_collect_batch_size.max(1);
    let snapshots = {
        let conn = state.conn.lock().await;
        let mut rows = load_traffic_batch(&conn, cursor, batch_size)?;
        if rows.is_empty() && cursor > 0 {
            rows = load_traffic_batch(&conn, 0, batch_size)?;
        }
        rows
    };
    if snapshots.is_empty() {
        return Ok(0);
    }

    let next_cursor = snapshots.last().map(|row| row.row_id).unwrap_or(0);
    let mut readings = Vec::with_capacity(snapshots.len());
    for snapshot in snapshots {
        let current = if use_ipt {
            ipt::read_external_bytes(snapshot.monitor_id, &snapshot.interface)
        } else {
            nft::read_external_bytes(snapshot.monitor_id, &snapshot.interface)
        };
        if let Some((current_in, current_out)) = current {
            readings.push(TrafficReading {
                snapshot,
                current_in,
                current_out,
            });
        }
    }
    if readings.is_empty() {
        return Ok(next_cursor);
    }

    let now = now_ts();
    let mut conn = state.conn.lock().await;
    let tx = conn.transaction().map_err(|e| {
        ApiError::internal(format!("begin traffic collection transaction error: {e}"))
    })?;
    let mut increments: HashMap<i64, (u64, u64)> = HashMap::new();
    {
        let mut update_state = tx
            .prepare(
                "UPDATE interface_states SET last_counter_in = ?1, last_counter_out = ?2 \
                 WHERE monitor_id = ?3 AND interface = ?4 \
                   AND last_counter_in = ?5 AND last_counter_out = ?6",
            )
            .map_err(|e| ApiError::internal(format!("prepare traffic state update error: {e}")))?;
        for reading in readings {
            let changed = update_state
                .execute(params![
                    reading.current_in,
                    reading.current_out,
                    reading.snapshot.monitor_id,
                    reading.snapshot.interface,
                    reading.snapshot.last_counter_in,
                    reading.snapshot.last_counter_out,
                ])
                .map_err(|e| ApiError::internal(format!("update traffic state error: {e}")))?;
            if changed == 0 {
                continue;
            }
            let increment_in = if reading.current_in >= reading.snapshot.last_counter_in {
                reading.current_in - reading.snapshot.last_counter_in
            } else {
                reading.current_in
            };
            let increment_out = if reading.current_out >= reading.snapshot.last_counter_out {
                reading.current_out - reading.snapshot.last_counter_out
            } else {
                reading.current_out
            };
            let entry = increments.entry(reading.snapshot.monitor_id).or_default();
            entry.0 = entry.0.saturating_add(increment_in);
            entry.1 = entry.1.saturating_add(increment_out);
        }
    }
    for (monitor_id, (increment_in, increment_out)) in increments {
        tx.execute(
            "UPDATE monitors SET total_bytes = total_bytes + ?1, \
                                 total_bytes_in = total_bytes_in + ?2, \
                                 total_bytes_out = total_bytes_out + ?3, \
                                 updated_at = ?4 WHERE id = ?5",
            params![
                increment_in.saturating_add(increment_out),
                increment_in,
                increment_out,
                now,
                monitor_id,
            ],
        )
        .map_err(|e| ApiError::internal(format!("update traffic totals error: {e}")))?;
        debug!(
            monitor_id,
            increment_in, increment_out, "collector updated traffic stats"
        );
    }
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit traffic collection error: {e}")))?;
    Ok(next_cursor)
}

fn load_resource_batch(
    conn: &Connection,
    cursor: i64,
    batch_size: usize,
) -> Result<Vec<(i64, Option<String>, Option<String>)>, ApiError> {
    let mut stmt = conn
        .prepare(
            "SELECT id, provider_kind, instance_name FROM monitors \
             WHERE id > ?1 AND provider_kind IS NOT NULL AND instance_name IS NOT NULL \
             ORDER BY id LIMIT ?2",
        )
        .map_err(|e| ApiError::internal(format!("prepare resource batch query error: {e}")))?;
    let rows = stmt
        .query_map(params![cursor, batch_size.max(1) as i64], |row| {
            Ok((row.get(0)?, row.get(1)?, row.get(2)?))
        })
        .map_err(|e| ApiError::internal(format!("resource batch query error: {e}")))?;
    rows.collect::<Result<Vec<_>, _>>()
        .map_err(|e| ApiError::internal(format!("resource batch row error: {e}")))
}

async fn collect_resource_batch(state: &AppState, cursor: i64) -> Result<i64, ApiError> {
    let batch_size = state.resource_collect_batch_size.max(1);
    let monitors = {
        let conn = state.conn.lock().await;
        let mut rows = load_resource_batch(&conn, cursor, batch_size)?;
        if rows.is_empty() && cursor > 0 {
            rows = load_resource_batch(&conn, 0, batch_size)?;
        }
        rows
    };
    if monitors.is_empty() {
        return Ok(0);
    }
    let next_cursor = monitors.last().map(|row| row.0).unwrap_or(0);
    let snapshots = resource::collect_all_resources(&monitors);
    if snapshots.is_empty() {
        return Ok(next_cursor);
    }

    let now = now_ts();
    let mut conn = state.conn.lock().await;
    let tx = conn.transaction().map_err(|e| {
        ApiError::internal(format!("begin resource collection transaction error: {e}"))
    })?;
    {
        let mut insert = tx
            .prepare(
                "INSERT INTO resource_metrics \
                 (monitor_id, timestamp, cpu_percent, memory_used, memory_total, disk_used, disk_total) \
                 VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7)",
            )
            .map_err(|e| ApiError::internal(format!("prepare resource metric insert error: {e}")))?;
        for (monitor_id, snapshot) in &snapshots {
            insert
                .execute(params![
                    monitor_id,
                    now,
                    snapshot.cpu_percent,
                    snapshot.memory_used,
                    snapshot.memory_total,
                    snapshot.disk_used,
                    snapshot.disk_total,
                ])
                .map_err(|e| ApiError::internal(format!("insert resource metric error: {e}")))?;
        }
    }
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit resource collection error: {e}")))?;
    debug!(count = snapshots.len(), "collected resource metrics");
    Ok(next_cursor)
}

fn load_desired_batch(
    conn: &Connection,
    cursor: i64,
    batch_size: usize,
) -> Result<Vec<DesiredMonitor>, ApiError> {
    let mut stmt = conn
        .prepare(
            "SELECT id, interfaces, bindings, inner_ip FROM monitors \
             WHERE id > ?1 ORDER BY id LIMIT ?2",
        )
        .map_err(|e| ApiError::internal(format!("prepare reconcile query error: {e}")))?;
    let rows = stmt
        .query_map(params![cursor, batch_size.max(1) as i64], |row| {
            let interfaces_json: String = row.get(1)?;
            let bindings_json: String = row.get(2)?;
            let inner_ip: Option<String> = row.get(3)?;
            Ok(DesiredMonitor {
                id: row.get(0)?,
                bindings: parse_persisted_bindings(
                    &bindings_json,
                    &interfaces_json,
                    inner_ip.as_deref(),
                ),
            })
        })
        .map_err(|e| ApiError::internal(format!("reconcile query error: {e}")))?;
    rows.collect::<Result<Vec<_>, _>>()
        .map_err(|e| ApiError::internal(format!("reconcile row error: {e}")))
}

fn load_active_interfaces(
    conn: &Connection,
    monitor_ids: &[i64],
) -> Result<HashMap<i64, HashMap<String, TrafficStateSnapshot>>, ApiError> {
    if monitor_ids.is_empty() {
        return Ok(HashMap::new());
    }
    let placeholders = std::iter::repeat_n("?", monitor_ids.len())
        .collect::<Vec<_>>()
        .join(",");
    let sql = format!(
        "SELECT rowid, monitor_id, interface, last_counter_in, last_counter_out \
         FROM interface_states WHERE monitor_id IN ({placeholders})"
    );
    let mut stmt = conn
        .prepare(&sql)
        .map_err(|e| ApiError::internal(format!("prepare active interfaces query error: {e}")))?;
    let rows = stmt
        .query_map(params_from_iter(monitor_ids.iter()), |row| {
            Ok(TrafficStateSnapshot {
                row_id: row.get(0)?,
                monitor_id: row.get(1)?,
                interface: row.get(2)?,
                last_counter_in: row.get(3)?,
                last_counter_out: row.get(4)?,
            })
        })
        .map_err(|e| ApiError::internal(format!("active interfaces query error: {e}")))?;
    let mut active = HashMap::<i64, HashMap<String, TrafficStateSnapshot>>::new();
    for row in rows {
        let state =
            row.map_err(|e| ApiError::internal(format!("active interface row error: {e}")))?;
        active
            .entry(state.monitor_id)
            .or_default()
            .insert(state.interface.clone(), state);
    }
    Ok(active)
}

async fn reconcile_traffic_batch(
    state: &AppState,
    use_ipt: bool,
    cursor: i64,
) -> Result<i64, ApiError> {
    let _operation_guard = state.traffic_operation_lock.lock().await;
    let batch_size = state.traffic_reconcile_batch_size.max(1);
    let (monitors, active_by_monitor) = {
        let conn = state.conn.lock().await;
        let mut rows = load_desired_batch(&conn, cursor, batch_size)?;
        if rows.is_empty() && cursor > 0 {
            rows = load_desired_batch(&conn, 0, batch_size)?;
        }
        let ids = rows.iter().map(|monitor| monitor.id).collect::<Vec<_>>();
        let active = load_active_interfaces(&conn, &ids)?;
        (rows, active)
    };
    if monitors.is_empty() {
        return Ok(0);
    }
    let next_cursor = monitors.last().map(|monitor| monitor.id).unwrap_or(0);

    let mut activations = Vec::new();
    let mut state_changes = Vec::new();
    for monitor in &monitors {
        let desired_interfaces = monitor
            .bindings
            .iter()
            .map(|binding| binding.interface.as_str())
            .collect::<HashSet<_>>();
        if let Some(active_interfaces) = active_by_monitor.get(&monitor.id) {
            for (interface, active_state) in active_interfaces {
                if !desired_interfaces.contains(interface.as_str()) {
                    let before = read_counter(use_ipt, monitor.id, interface);
                    let remove_result = if use_ipt {
                        ipt::remove_counter(monitor.id, interface)
                    } else {
                        nft::remove_counter(monitor.id, interface)
                    };
                    if let Err(err) = remove_result {
                        warn!(
                            monitor_id = monitor.id,
                            interface,
                            error = %err.message,
                            "failed removing stale traffic counter during reconciliation"
                        );
                    }
                    state_changes.push(settled_state_change(active_state, before, None, true));
                }
            }
        }

        for binding in &monitor.bindings {
            let active_state = active_by_monitor
                .get(&monitor.id)
                .and_then(|states| states.get(&binding.interface));
            let before =
                active_state.and_then(|_| read_counter(use_ipt, monitor.id, &binding.interface));
            if !interface_exists(&binding.interface) {
                let _ = if use_ipt {
                    ipt::remove_counter(monitor.id, &binding.interface)
                } else {
                    nft::remove_counter(monitor.id, &binding.interface)
                };
                if let Some(active_state) = active_state {
                    state_changes.push(settled_state_change(active_state, before, None, true));
                }
                continue;
            }
            let ensure_result = if use_ipt {
                ipt::ensure_counter(
                    monitor.id,
                    &binding.interface,
                    &binding.addresses,
                    &binding.families,
                )
            } else {
                nft::ensure_counter(
                    monitor.id,
                    &binding.interface,
                    &binding.addresses,
                    &binding.families,
                )
            };
            if let Err(err) = ensure_result {
                warn!(
                    monitor_id = monitor.id,
                    interface = binding.interface,
                    error = %err.message,
                    "traffic counter reconciliation failed"
                );
                if let Some(active_state) = active_state {
                    let after = read_counter(use_ipt, monitor.id, &binding.interface);
                    state_changes.push(settled_state_change(
                        active_state,
                        before,
                        after,
                        after.is_none(),
                    ));
                }
                continue;
            }
            let current = read_counter(use_ipt, monitor.id, &binding.interface);
            if let Some((base_in, base_out)) = current {
                if let Some(active_state) = active_state {
                    state_changes.push(settled_state_change(
                        active_state,
                        before,
                        Some((base_in, base_out)),
                        false,
                    ));
                } else {
                    activations.push(CounterActivation {
                        monitor_id: monitor.id,
                        binding: binding.clone(),
                        base_in,
                        base_out,
                    });
                }
            } else if let Some(active_state) = active_state {
                state_changes.push(settled_state_change(active_state, before, None, true));
            }
        }
    }

    let mut conn = state.conn.lock().await;
    let tx = conn.transaction().map_err(|e| {
        ApiError::internal(format!("begin traffic reconcile transaction error: {e}"))
    })?;
    let mut increments = HashMap::<i64, (u64, u64)>::new();
    {
        let mut update_state = tx
            .prepare(
                "UPDATE interface_states SET last_counter_in = ?1, last_counter_out = ?2 \
                 WHERE monitor_id = ?3 AND interface = ?4 \
                   AND last_counter_in = ?5 AND last_counter_out = ?6",
            )
            .map_err(|e| {
                ApiError::internal(format!("prepare reconcile state update error: {e}"))
            })?;
        let mut delete_state = tx
            .prepare(
                "DELETE FROM interface_states WHERE monitor_id = ?1 AND interface = ?2 \
                   AND last_counter_in = ?3 AND last_counter_out = ?4",
            )
            .map_err(|e| ApiError::internal(format!("prepare deactivate state error: {e}")))?;
        for change in state_changes {
            let changed = if change.deactivate {
                delete_state.execute(params![
                    change.monitor_id,
                    change.interface,
                    change.expected_in,
                    change.expected_out,
                ])
            } else {
                update_state.execute(params![
                    change.base_in,
                    change.base_out,
                    change.monitor_id,
                    change.interface,
                    change.expected_in,
                    change.expected_out,
                ])
            }
            .map_err(|e| ApiError::internal(format!("apply reconciled state error: {e}")))?;
            if changed > 0 {
                let entry = increments.entry(change.monitor_id).or_default();
                entry.0 = entry.0.saturating_add(change.increment_in);
                entry.1 = entry.1.saturating_add(change.increment_out);
            }
        }
    }
    {
        let mut insert_state = tx
            .prepare(
                "INSERT INTO interface_states \
                 (monitor_id, interface, last_counter_in, last_counter_out) \
                 VALUES (?1, ?2, ?3, ?4) ON CONFLICT(monitor_id, interface) DO NOTHING",
            )
            .map_err(|e| ApiError::internal(format!("prepare activate state error: {e}")))?;
        for activation in activations {
            insert_state
                .execute(params![
                    activation.monitor_id,
                    activation.binding.interface,
                    activation.base_in,
                    activation.base_out,
                ])
                .map_err(|e| ApiError::internal(format!("activate interface state error: {e}")))?;
        }
    }
    let now = now_ts();
    for (monitor_id, (increment_in, increment_out)) in increments {
        tx.execute(
            "UPDATE monitors SET total_bytes = total_bytes + ?1, \
                                 total_bytes_in = total_bytes_in + ?2, \
                                 total_bytes_out = total_bytes_out + ?3, \
                                 updated_at = ?4 WHERE id = ?5",
            params![
                increment_in.saturating_add(increment_out),
                increment_in,
                increment_out,
                now,
                monitor_id,
            ],
        )
        .map_err(|e| ApiError::internal(format!("settle reconciled traffic error: {e}")))?;
    }
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit traffic reconciliation error: {e}")))?;
    Ok(next_cursor)
}

fn garbage_collect_orphans(use_ipt: bool) -> Result<usize, ApiError> {
    let conn = Connection::open("traffic.db")
        .map_err(|e| ApiError::internal(format!("open GC sqlite connection error: {e}")))?;
    if use_ipt {
        ipt::garbage_collect_orphans(&conn)
    } else {
        nft::garbage_collect_orphans(&conn)
    }
}

pub fn start_collector(state: AppState) {
    let traffic_interval = state.traffic_collect_interval.max(1);
    let resource_interval = state.resource_collect_interval.max(10);
    let reconcile_interval = state.traffic_reconcile_interval.max(10);
    let use_ipt = state.traffic_collect_method == "ipt";
    let resource_ticks = resource_interval.div_ceil(traffic_interval).max(1);
    let reconcile_ticks = reconcile_interval.div_ceil(traffic_interval).max(1);
    let gc_ticks = 300u64.div_ceil(traffic_interval).max(1);

    info!(
        traffic_interval_secs = traffic_interval,
        traffic_batch_size = state.traffic_collect_batch_size,
        resource_interval_secs = resource_interval,
        resource_batch_size = state.resource_collect_batch_size,
        reconcile_interval_secs = reconcile_interval,
        reconcile_batch_size = state.traffic_reconcile_batch_size,
        use_ipt,
        "collector started"
    );

    tokio::spawn(async move {
        let mut ticks = 0u64;
        let mut traffic_cursor = 0i64;
        let mut resource_cursor = 0i64;
        let mut reconcile_cursor = 0i64;
        loop {
            ticks = ticks.saturating_add(1);
            match collect_traffic_batch(&state, use_ipt, traffic_cursor).await {
                Ok(cursor) => traffic_cursor = cursor,
                Err(err) => error!(error = %err.message, "collector iteration failed"),
            }

            if ticks % resource_ticks == 0 {
                match collect_resource_batch(&state, resource_cursor).await {
                    Ok(cursor) => resource_cursor = cursor,
                    Err(err) => error!(error = %err.message, "resource collection failed"),
                }
                let conn = state.conn.lock().await;
                if let Err(err) = cleanup_old_resource_metrics(&conn) {
                    error!(error = %err.message, "resource metrics cleanup failed");
                }
            }

            if ticks == 1 || ticks % reconcile_ticks == 0 {
                match reconcile_traffic_batch(&state, use_ipt, reconcile_cursor).await {
                    Ok(cursor) => reconcile_cursor = cursor,
                    Err(err) => error!(error = %err.message, "traffic reconciliation failed"),
                }
            }

            let deleted = {
                let conn = state.conn.lock().await;
                match cleanup_stale_monitors(&conn, AUTO_CLEANUP_SECONDS) {
                    Ok(deleted) => deleted,
                    Err(err) => {
                        error!(error = %err.message, "auto cleanup failed");
                        0
                    }
                }
            };
            if deleted > 0 {
                info!(
                    deleted,
                    max_age_seconds = AUTO_CLEANUP_SECONDS,
                    "auto cleanup removed stale monitors"
                );
            }
            if deleted > 0 || ticks % gc_ticks == 0 {
                let _operation_guard = state.traffic_operation_lock.lock().await;
                match garbage_collect_orphans(use_ipt) {
                    Ok(removed) if removed > 0 => {
                        info!(removed, "periodic orphan GC removed stale rules")
                    }
                    Ok(_) => {}
                    Err(err) => error!(error = %err.message, "periodic orphan GC failed"),
                }
            }

            tokio::time::sleep(Duration::from_secs(traffic_interval)).await;
        }
    });
}

#[cfg(test)]
mod tests {
    use super::{TrafficStateSnapshot, settled_state_change};

    fn state(interface: &str, last_in: u64, last_out: u64) -> TrafficStateSnapshot {
        TrafficStateSnapshot {
            row_id: 1,
            monitor_id: 7,
            interface: interface.to_string(),
            last_counter_in: last_in,
            last_counter_out: last_out,
        }
    }

    #[test]
    fn reconciliation_settles_bytes_across_healthy_rule_check() {
        let change = settled_state_change(
            &state("veth-test", 100, 200),
            Some((150, 260)),
            Some((170, 290)),
            false,
        );
        assert_eq!(change.increment_in, 70);
        assert_eq!(change.increment_out, 90);
        assert_eq!((change.base_in, change.base_out), (170, 290));
    }

    #[test]
    fn reconciliation_preserves_pre_reset_bytes_without_double_counting() {
        let change = settled_state_change(
            &state("veth-test", 100, 200),
            Some((150, 260)),
            Some((5, 7)),
            false,
        );
        assert_eq!(change.increment_in, 50);
        assert_eq!(change.increment_out, 60);
        assert_eq!((change.base_in, change.base_out), (5, 7));
    }

    #[test]
    fn routed_pve_ipv6_reconciliation_settles_the_second_tap() {
        let change = settled_state_change(
            &state("tap102i1", 1_040, 1_040),
            Some((1_560, 1_820)),
            Some((1_560, 1_820)),
            false,
        );
        assert_eq!(change.interface, "tap102i1");
        assert_eq!(change.increment_in, 520);
        assert_eq!(change.increment_out, 780);
        assert_eq!(change.increment_in + change.increment_out, 1_300);
    }

    #[test]
    fn routed_pve_ipv6_counter_recreation_keeps_pre_reset_bytes() {
        let change = settled_state_change(
            &state("veth100i1", 2_000, 3_000),
            Some((2_120, 3_180)),
            Some((37, 53)),
            false,
        );
        assert_eq!(change.interface, "veth100i1");
        // Bytes observed before the reset are settled once; the recreated
        // counter becomes the fresh baseline for the next collection pass.
        assert_eq!(change.increment_in, 120);
        assert_eq!(change.increment_out, 180);
        assert_eq!((change.base_in, change.base_out), (37, 53));
    }
}
