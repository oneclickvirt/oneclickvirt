use crate::{
    app_state::AppState,
    db::{cleanup_stale_monitors, now_ts},
    error::{ApiError, ErrorResponse},
    ipt,
    models::{
        AddDomainProxyRequest, AddDomainProxyResponse, AddRequest, AddResponse,
        ApplyBlockRulesRequest, ApplyBlockRulesResponse, BatchDeleteRequest, BatchDeleteResponse,
        BatchInfoRequest, BatchInfoResponse, BatchResourceItem, BatchResourceQueryRequest,
        BatchResourceQueryResponse, CleanupRequest, CleanupResponse, DeleteRequest, DeleteResponse,
        DomainProxyItem, GetBlockRulesResponse, InfoRequest, InfoResponse, InterfaceInput,
        ListDomainProxiesResponse, ListMonitorItem, ListMonitorsResponse, RemoveBlockRulesResponse,
        RemoveDomainProxyRequest, RemoveDomainProxyResponse, ResourceDataPoint,
        ResourceQueryRequest, ResourceQueryResponse, TrafficBinding, UpdateRequest, UpdateResponse,
    },
    nft,
    traffic::{
        binding_interfaces, interface_exists, legacy_inner_ip, normalize_bindings,
        parse_persisted_bindings, serialize_bindings,
    },
};
use axum::{
    Json,
    extract::{Query, State},
};
use rusqlite::{Connection, OptionalExtension, params, params_from_iter};
use std::collections::{HashMap, HashSet};
use tracing::{debug, info, warn};

#[derive(serde::Deserialize)]
pub struct InfoQuery {
    pub human: Option<u8>,
}

fn human_bytes(bytes: u64) -> String {
    const KB: f64 = 1024.0;
    const MB: f64 = KB * 1024.0;
    const GB: f64 = MB * 1024.0;

    let b = bytes as f64;
    if b >= GB {
        format!("{:.2}G", b / GB)
    } else if b >= MB {
        format!("{:.2}M", b / MB)
    } else if b >= KB {
        format!("{:.2}K", b / KB)
    } else {
        format!("{bytes}B")
    }
}

#[derive(Debug)]
struct CounterSnapshot {
    interface: String,
    base_in: u64,
    base_out: u64,
}

#[derive(Debug)]
struct StoredCounterState {
    interface: String,
    last_counter_in: u64,
    last_counter_out: u64,
}

fn read_pending_counter_increments(
    monitor_id: i64,
    states: &[StoredCounterState],
    use_ipt: bool,
) -> (u64, u64, HashMap<String, (u64, u64)>) {
    let mut increment_in = 0u64;
    let mut increment_out = 0u64;
    let mut current_by_interface = HashMap::with_capacity(states.len());
    for state in states {
        let current = if use_ipt {
            ipt::read_external_bytes(monitor_id, &state.interface)
        } else {
            nft::read_external_bytes(monitor_id, &state.interface)
        };
        let Some((current_in, current_out)) = current else {
            continue;
        };
        current_by_interface.insert(state.interface.clone(), (current_in, current_out));
        increment_in = increment_in.saturating_add(if current_in >= state.last_counter_in {
            current_in - state.last_counter_in
        } else {
            current_in
        });
        increment_out = increment_out.saturating_add(if current_out >= state.last_counter_out {
            current_out - state.last_counter_out
        } else {
            current_out
        });
    }
    (increment_in, increment_out, current_by_interface)
}

fn ensure_interface_counters(
    monitor_id: i64,
    bindings: &[TrafficBinding],
    use_ipt: bool,
) -> (Vec<CounterSnapshot>, Vec<String>) {
    let mut snapshots = Vec::new();
    let mut errors = Vec::new();

    for binding in bindings {
        let interface = &binding.interface;
        let ensure_result = if use_ipt {
            ipt::ensure_counter(monitor_id, interface, &binding.addresses, &binding.families)
        } else {
            nft::ensure_counter(monitor_id, interface, &binding.addresses, &binding.families)
        };

        if let Err(err) = ensure_result {
            warn!(
                monitor_id,
                interface,
                error = %err.message,
                "failed to ensure traffic counter for interface; continuing with remaining interfaces"
            );
            errors.push(format!("{interface}: {}", err.message));
            continue;
        }

        let (base_in, base_out) = if use_ipt {
            ipt::read_external_bytes(monitor_id, interface).unwrap_or((0, 0))
        } else {
            nft::read_external_bytes(monitor_id, interface).unwrap_or((0, 0))
        };

        snapshots.push(CounterSnapshot {
            interface: interface.clone(),
            base_in,
            base_out,
        });
    }

    (snapshots, errors)
}

fn counter_health(
    bindings: &[TrafficBinding],
    snapshots: &[CounterSnapshot],
    errors: &[String],
) -> (bool, Vec<String>, Option<String>) {
    let active = snapshots
        .iter()
        .map(|snapshot| snapshot.interface.as_str())
        .collect::<HashSet<_>>();
    let mut missing_interfaces = bindings
        .iter()
        .filter(|binding| !active.contains(binding.interface.as_str()))
        .map(|binding| binding.interface.clone())
        .collect::<Vec<_>>();
    missing_interfaces.sort();
    missing_interfaces.dedup();
    let healthy = !bindings.is_empty() && missing_interfaces.is_empty() && errors.is_empty();
    let health_error = if healthy {
        None
    } else if !errors.is_empty() {
        Some(errors.join("; "))
    } else if bindings.is_empty() {
        Some("monitor has no valid desired traffic bindings".to_string())
    } else {
        Some(format!(
            "missing or inactive interfaces: {}",
            missing_interfaces.join(",")
        ))
    };
    (healthy, missing_interfaces, health_error)
}

fn parse_max_update_time_to_seconds(raw: &str) -> Result<i64, ApiError> {
    let s = raw.trim();
    if s.is_empty() {
        return Err(ApiError::bad_request("max_update_time cannot be empty"));
    }

    let mut chars = s.chars().peekable();
    let mut total: i64 = 0;
    let mut consumed_any = false;

    while chars.peek().is_some() {
        let mut num = String::new();
        while let Some(c) = chars.peek() {
            if c.is_ascii_digit() {
                num.push(*c);
                chars.next();
            } else {
                break;
            }
        }

        if num.is_empty() {
            return Err(ApiError::bad_request(
                "invalid max_update_time format, expected like 3d / 12h / 30m / 45s",
            ));
        }

        let value = num
            .parse::<i64>()
            .map_err(|_| ApiError::bad_request("invalid number in max_update_time"))?;
        let unit = chars
            .next()
            .ok_or_else(|| ApiError::bad_request("missing unit in max_update_time"))?;

        let factor = match unit {
            'd' | 'D' => 24 * 60 * 60,
            'h' | 'H' => 60 * 60,
            'm' | 'M' => 60,
            's' | 'S' => 1,
            _ => {
                return Err(ApiError::bad_request(
                    "invalid unit in max_update_time, use d/h/m/s",
                ));
            }
        };

        total = total.saturating_add(value.saturating_mul(factor));
        consumed_any = true;
    }

    if !consumed_any || total < 0 {
        return Err(ApiError::bad_request("invalid max_update_time"));
    }

    Ok(total)
}

#[utoipa::path(
    post,
    path = "/api/v1/add",
    request_body = AddRequest,
    responses(
        (status = 200, description = "Add monitor", body = AddResponse),
        (status = 400, description = "Bad request", body = ErrorResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "VM Traffic"
)]
pub async fn add_monitor(
    State(state): State<AppState>,
    Json(payload): Json<AddRequest>,
) -> Result<Json<AddResponse>, ApiError> {
    let bindings = normalize_bindings(
        payload.bindings.clone(),
        payload.interface.into_vec(),
        payload.inner_ip.as_deref(),
    )?;
    let interfaces = binding_interfaces(&bindings);
    let now = now_ts();
    let provider_kind = payload.provider_kind.clone();
    let instance_name = payload.instance_name.clone();
    let inner_ip = legacy_inner_ip(&bindings);
    let operation_guard = state.traffic_operation_lock.lock().await;

    // Make add idempotent for controller reconciliation.  If the controller DB was
    // rebuilt or the local mapping was lost, a sync may call /add for an instance
    // that the agent already knows about.  Reusing the existing agent-side monitor
    // avoids duplicate nft/iptables counters and keeps repeated sync attempts cheap.
    if let (Some(provider_kind_key), Some(instance_name_key)) =
        (provider_kind.clone(), instance_name.clone())
    {
        let existing_id: Option<i64> = {
            let conn = state.conn.lock().await;
            conn.query_row(
                "SELECT id FROM monitors WHERE provider_kind = ?1 AND instance_name = ?2 ORDER BY id DESC LIMIT 1",
                params![provider_kind_key.as_str(), instance_name_key.as_str()],
                |row| row.get(0),
            )
            .optional()
            .map_err(|e| ApiError::internal(format!("query existing monitor error: {e}")))?
        };

        if let Some(id) = existing_id {
            debug!(
                id,
                provider_kind = %provider_kind_key,
                instance_name = %instance_name_key,
                "add monitor resolved to existing monitor; updating instead"
            );
            let update_payload = UpdateRequest {
                id,
                new_interface: InterfaceInput::Many(interfaces),
                bindings,
                provider_kind,
                instance_name,
                inner_ip,
            };
            drop(operation_guard);
            let Json(resp) = update_monitor(State(state), Json(update_payload)).await?;
            return Ok(Json(AddResponse {
                id: resp.id,
                interface: resp.interface,
                bindings: resp.bindings,
                healthy: resp.healthy,
                missing_interfaces: resp.missing_interfaces,
                health_error: resp.health_error,
            }));
        }
    }

    let use_ipt = state.traffic_collect_method == "ipt";
    let requested_interfaces_json = serde_json::to_string(&interfaces)
        .map_err(|e| ApiError::internal(format!("serialize interfaces error: {e}")))?;
    let requested_bindings_json = serialize_bindings(&bindings)?;
    let id = {
        let conn = state.conn.lock().await;
        conn.execute(
            "INSERT INTO monitors (interfaces, bindings, total_bytes, provider_kind, instance_name, inner_ip, updated_at) VALUES (?1, ?2, 0, ?3, ?4, ?5, ?6)",
            params![requested_interfaces_json, requested_bindings_json, provider_kind.as_deref(), instance_name.as_deref(), inner_ip.as_deref(), now],
        )
        .map_err(|e| ApiError::internal(format!("insert monitor error: {e}")))?;
        conn.last_insert_rowid()
    };

    // Do not hold the SQLite mutex while invoking nft/iptables.  Each interface is
    // reconciled independently so a broken IPv6-only/secondary NIC does not prevent
    // the valid IPv4 NIC from being monitored.
    let (snapshots, counter_errors) = ensure_interface_counters(id, &bindings, use_ipt);

    {
        let mut conn = state.conn.lock().await;
        let tx = conn
            .transaction()
            .map_err(|e| ApiError::internal(format!("begin add monitor transaction error: {e}")))?;
        for snapshot in &snapshots {
            tx.execute(
                "INSERT INTO interface_states (monitor_id, interface, last_counter_in, last_counter_out) VALUES (?1, ?2, ?3, ?4)",
                params![id, snapshot.interface.as_str(), snapshot.base_in, snapshot.base_out],
            )
            .map_err(|e| ApiError::internal(format!("insert interface state error: {e}")))?;
        }
        tx.commit().map_err(|e| {
            ApiError::internal(format!("commit add monitor transaction error: {e}"))
        })?;
    }

    let (healthy, missing_interfaces, health_error) =
        counter_health(&bindings, &snapshots, &counter_errors);
    if !healthy {
        warn!(id, errors = ?counter_errors, "monitor added unhealthy; awaiting controller reconciliation");
    }

    info!(id, interfaces = ?interfaces, bindings = ?bindings, "monitor added");
    Ok(Json(AddResponse {
        id,
        interface: interfaces,
        bindings,
        healthy,
        missing_interfaces,
        health_error,
    }))
}

#[utoipa::path(
    post,
    path = "/api/v1/update",
    request_body = UpdateRequest,
    responses(
        (status = 200, description = "Update monitor interfaces", body = UpdateResponse),
        (status = 400, description = "Bad request", body = ErrorResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 404, description = "Monitor not found", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "VM Traffic"
)]
pub async fn update_monitor(
    State(state): State<AppState>,
    Json(payload): Json<UpdateRequest>,
) -> Result<Json<UpdateResponse>, ApiError> {
    let bindings = normalize_bindings(
        payload.bindings.clone(),
        payload.new_interface.into_vec(),
        payload.inner_ip.as_deref(),
    )?;
    let interfaces = binding_interfaces(&bindings);
    let now = now_ts();
    let id = payload.id;
    let inner_ip = legacy_inner_ip(&bindings);

    let use_ipt = state.traffic_collect_method == "ipt";
    let _operation_guard = state.traffic_operation_lock.lock().await;
    let (old_interfaces, old_states) = {
        let conn = state.conn.lock().await;
        let old_monitor: Option<(String, String, Option<String>)> = conn
            .query_row(
                "SELECT interfaces, bindings, inner_ip FROM monitors WHERE id = ?1",
                params![id],
                |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)),
            )
            .optional()
            .map_err(|e| ApiError::internal(format!("query monitor error: {e}")))?;
        let Some((old_interfaces_json, old_bindings_json, old_inner_ip)) = old_monitor else {
            warn!(id, "update failed: monitor not found");
            return Err(ApiError::not_found(format!("monitor id {id} not found")));
        };
        let old_interfaces = binding_interfaces(&parse_persisted_bindings(
            &old_bindings_json,
            &old_interfaces_json,
            old_inner_ip.as_deref(),
        ));
        let mut stmt = conn
            .prepare(
                "SELECT interface, last_counter_in, last_counter_out \
                 FROM interface_states WHERE monitor_id = ?1",
            )
            .map_err(|e| ApiError::internal(format!("prepare old state query error: {e}")))?;
        let rows = stmt
            .query_map(params![id], |row| {
                Ok(StoredCounterState {
                    interface: row.get(0)?,
                    last_counter_in: row.get(1)?,
                    last_counter_out: row.get(2)?,
                })
            })
            .map_err(|e| ApiError::internal(format!("old state query error: {e}")))?;
        let old_states = rows
            .collect::<Result<Vec<_>, _>>()
            .map_err(|e| ApiError::internal(format!("old state row error: {e}")))?;
        (old_interfaces, old_states)
    };

    let (mut pending_in, mut pending_out, before_by_interface) =
        read_pending_counter_increments(id, &old_states, use_ipt);
    let (snapshots, counter_errors) = ensure_interface_counters(id, &bindings, use_ipt);
    for snapshot in &snapshots {
        let Some((before_in, before_out)) = before_by_interface.get(&snapshot.interface) else {
            continue;
        };
        if snapshot.base_in >= *before_in {
            pending_in = pending_in.saturating_add(snapshot.base_in - before_in);
        }
        if snapshot.base_out >= *before_out {
            pending_out = pending_out.saturating_add(snapshot.base_out - before_out);
        }
    }

    let interfaces_json = serde_json::to_string(&interfaces)
        .map_err(|e| ApiError::internal(format!("serialize interfaces error: {e}")))?;
    let bindings_json = serialize_bindings(&bindings)?;
    {
        let mut conn = state.conn.lock().await;
        let tx = conn.transaction().map_err(|e| {
            ApiError::internal(format!("begin update monitor transaction error: {e}"))
        })?;
        tx.execute(
            "UPDATE monitors SET interfaces = ?1, bindings = ?2, updated_at = ?3, \
                                 provider_kind = COALESCE(?5, provider_kind), \
                                 instance_name = COALESCE(?6, instance_name), inner_ip = ?7, \
                                 total_bytes = total_bytes + ?8, \
                                 total_bytes_in = total_bytes_in + ?9, \
                                 total_bytes_out = total_bytes_out + ?10 \
             WHERE id = ?4",
            params![
                interfaces_json,
                bindings_json,
                now,
                id,
                payload.provider_kind.as_deref(),
                payload.instance_name.as_deref(),
                inner_ip.as_deref(),
                pending_in.saturating_add(pending_out),
                pending_in,
                pending_out,
            ],
        )
        .map_err(|e| ApiError::internal(format!("update monitor error: {e}")))?;
        tx.execute(
            "DELETE FROM interface_states WHERE monitor_id = ?1",
            params![id],
        )
        .map_err(|e| ApiError::internal(format!("delete old interface states error: {e}")))?;
        for snapshot in &snapshots {
            tx.execute(
                "INSERT INTO interface_states (monitor_id, interface, last_counter_in, last_counter_out) VALUES (?1, ?2, ?3, ?4)",
                params![id, snapshot.interface.as_str(), snapshot.base_in, snapshot.base_out],
            )
            .map_err(|e| ApiError::internal(format!("insert new interface state error: {e}")))?;
        }
        tx.commit().map_err(|e| {
            ApiError::internal(format!("commit update monitor transaction error: {e}"))
        })?;
    }

    let new_set: HashSet<String> = interfaces.iter().cloned().collect();
    for old in old_interfaces {
        if !new_set.contains(&old) {
            if let Err(err) = if use_ipt {
                ipt::remove_counter(id, &old)
            } else {
                nft::remove_counter(id, &old)
            } {
                warn!(id, interface = old, error = %err.message, "failed to remove old counter rules after update");
            }
        }
    }

    let (healthy, missing_interfaces, health_error) =
        counter_health(&bindings, &snapshots, &counter_errors);
    if !healthy {
        warn!(id, errors = ?counter_errors, "monitor updated unhealthy; awaiting controller reconciliation");
    }

    info!(id, interfaces = ?interfaces, bindings = ?bindings, "monitor interfaces updated");
    Ok(Json(UpdateResponse {
        id,
        interface: interfaces,
        bindings,
        healthy,
        missing_interfaces,
        health_error,
    }))
}

#[utoipa::path(
    post,
    path = "/api/v1/delete",
    request_body = DeleteRequest,
    responses(
        (status = 200, description = "Delete monitor", body = DeleteResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "VM Traffic"
)]
pub async fn delete_monitor(
    State(state): State<AppState>,
    Json(payload): Json<DeleteRequest>,
) -> Result<Json<DeleteResponse>, ApiError> {
    let id = payload.id;
    let _operation_guard = state.traffic_operation_lock.lock().await;
    let (affected, old_interfaces) = {
        let conn = state.conn.lock().await;
        let mut old_interfaces = HashSet::new();
        let desired: Option<(String, String, Option<String>)> = conn
            .query_row(
                "SELECT interfaces, bindings, inner_ip FROM monitors WHERE id = ?1",
                params![id],
                |row| Ok((row.get(0)?, row.get(1)?, row.get(2)?)),
            )
            .optional()
            .map_err(|e| ApiError::internal(format!("query delete monitor error: {e}")))?;
        if let Some((interfaces_json, bindings_json, inner_ip)) = desired {
            for binding in
                parse_persisted_bindings(&bindings_json, &interfaces_json, inner_ip.as_deref())
            {
                old_interfaces.insert(binding.interface);
            }
        }
        {
            let mut old_stmt = conn
                .prepare("SELECT interface FROM interface_states WHERE monitor_id = ?1")
                .map_err(|e| {
                    ApiError::internal(format!("prepare delete interfaces query error: {e}"))
                })?;
            let old_rows = old_stmt
                .query_map(params![id], |row| row.get::<_, String>(0))
                .map_err(|e| ApiError::internal(format!("delete interfaces query error: {e}")))?;
            for row in old_rows {
                old_interfaces.insert(
                    row.map_err(|e| {
                        ApiError::internal(format!("delete interface row error: {e}"))
                    })?,
                );
            }
        }

        let affected = conn
            .execute("DELETE FROM monitors WHERE id = ?1", params![id])
            .map_err(|e| ApiError::internal(format!("delete monitor error: {e}")))?;
        (affected, old_interfaces.into_iter().collect::<Vec<_>>())
    };

    let use_ipt = state.traffic_collect_method == "ipt";
    if affected > 0 {
        for interface in old_interfaces {
            if let Err(err) = if use_ipt {
                ipt::remove_counter(id, &interface)
            } else {
                nft::remove_counter(id, &interface)
            } {
                warn!(id, interface, error = %err.message, "failed to remove counter rules after delete");
            }
        }
        info!(id, "monitor deleted");
    } else {
        warn!(id, "delete requested but monitor not found");
    }
    Ok(Json(DeleteResponse {
        id,
        deleted: affected > 0,
    }))
}

#[utoipa::path(
    post,
    path = "/api/v1/delete/batch",
    request_body = BatchDeleteRequest,
    responses(
        (status = 200, description = "Delete monitors in one bounded request", body = BatchDeleteResponse),
        (status = 400, description = "Bad request", body = ErrorResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "VM Traffic"
)]
pub async fn batch_delete_monitors(
    State(state): State<AppState>,
    Json(payload): Json<BatchDeleteRequest>,
) -> Result<Json<BatchDeleteResponse>, ApiError> {
    let mut ids = payload.ids;
    ids.sort_unstable();
    ids.dedup();
    if ids.len() > 100 {
        return Err(ApiError::bad_request(
            "batch delete supports at most 100 monitor ids",
        ));
    }
    if ids.is_empty() {
        return Ok(Json(BatchDeleteResponse {
            deleted_ids: Vec::new(),
            total: 0,
        }));
    }

    let _operation_guard = state.traffic_operation_lock.lock().await;
    let placeholders = std::iter::repeat_n("?", ids.len())
        .collect::<Vec<_>>()
        .join(",");
    let query = format!(
        "SELECT m.id, m.interfaces, m.bindings, m.inner_ip, s.interface \
         FROM monitors m LEFT JOIN interface_states s ON s.monitor_id = m.id \
         WHERE m.id IN ({placeholders}) ORDER BY m.id"
    );
    let (deleted_ids, interfaces_by_id) = {
        let mut conn = state.conn.lock().await;
        let mut interfaces_by_id = HashMap::<i64, HashSet<String>>::new();
        {
            let mut stmt = conn.prepare(&query).map_err(|e| {
                ApiError::internal(format!("prepare batch delete query error: {e}"))
            })?;
            let rows = stmt
                .query_map(params_from_iter(ids.iter()), |row| {
                    Ok((
                        row.get::<_, i64>(0)?,
                        row.get::<_, String>(1)?,
                        row.get::<_, String>(2)?,
                        row.get::<_, Option<String>>(3)?,
                        row.get::<_, Option<String>>(4)?,
                    ))
                })
                .map_err(|e| ApiError::internal(format!("batch delete query error: {e}")))?;
            for row in rows {
                let (id, interfaces_json, bindings_json, inner_ip, active_interface) =
                    row.map_err(|e| ApiError::internal(format!("batch delete row error: {e}")))?;
                let interfaces = interfaces_by_id.entry(id).or_default();
                for binding in
                    parse_persisted_bindings(&bindings_json, &interfaces_json, inner_ip.as_deref())
                {
                    interfaces.insert(binding.interface);
                }
                if let Some(interface) = active_interface {
                    interfaces.insert(interface);
                }
            }
        }

        let mut deleted_ids = interfaces_by_id.keys().copied().collect::<Vec<_>>();
        deleted_ids.sort_unstable();
        if !deleted_ids.is_empty() {
            let tx = conn.transaction().map_err(|e| {
                ApiError::internal(format!("begin batch delete transaction error: {e}"))
            })?;
            let delete_placeholders = std::iter::repeat_n("?", deleted_ids.len())
                .collect::<Vec<_>>()
                .join(",");
            tx.execute(
                &format!("DELETE FROM monitors WHERE id IN ({delete_placeholders})"),
                params_from_iter(deleted_ids.iter()),
            )
            .map_err(|e| ApiError::internal(format!("batch delete monitors error: {e}")))?;
            tx.commit().map_err(|e| {
                ApiError::internal(format!("commit batch delete transaction error: {e}"))
            })?;
        }
        (deleted_ids, interfaces_by_id)
    };

    let use_ipt = state.traffic_collect_method == "ipt";
    for id in &deleted_ids {
        if let Some(interfaces) = interfaces_by_id.get(id) {
            for interface in interfaces {
                if let Err(err) = if use_ipt {
                    ipt::remove_counter(*id, interface)
                } else {
                    nft::remove_counter(*id, interface)
                } {
                    warn!(monitor_id = *id, interface, error = %err.message, "failed to remove counter rules after batch delete");
                }
            }
        }
    }

    let total = deleted_ids.len();
    Ok(Json(BatchDeleteResponse { deleted_ids, total }))
}

#[utoipa::path(
    post,
    path = "/api/v1/info",
    params(
        ("human" = Option<u8>, Query, description = "Set to 1 to include human readable traffic like K/M/G")
    ),
    request_body = InfoRequest,
    responses(
        (status = 200, description = "Get monitor traffic info", body = InfoResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 404, description = "Monitor not found", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "VM Traffic"
)]
pub async fn info_monitor(
    State(state): State<AppState>,
    Query(query): Query<InfoQuery>,
    Json(payload): Json<InfoRequest>,
) -> Result<Json<InfoResponse>, ApiError> {
    let conn = state.conn.lock().await;
    let row = conn
        .query_row(
            "SELECT id, interfaces, total_bytes, total_bytes_in, total_bytes_out, updated_at FROM monitors WHERE id = ?1",
            params![payload.id],
            |row| {
                let interfaces_json: String = row.get(1)?;
                let interfaces: Vec<String> =
                    serde_json::from_str(&interfaces_json).unwrap_or_default();
                let used_traffic: u64 = row.get(2)?;
                let used_traffic_in: u64 = row.get(3)?;
                let used_traffic_out: u64 = row.get(4)?;
                let used_traffic_human = if query.human == Some(1) {
                    Some(human_bytes(used_traffic))
                } else {
                    None
                };
                Ok(InfoResponse {
                    id: row.get(0)?,
                    interface: interfaces,
                    used_traffic,
                    used_traffic_in,
                    used_traffic_out,
                    used_traffic_human,
                    last_update_time: row.get(5)?,
                })
            },
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("query monitor info error: {e}")))?;

    row.map(Json).ok_or_else(|| {
        debug!(id = payload.id, "info requested for missing monitor");
        ApiError::not_found(format!("monitor id {} not found", payload.id))
    })
}

#[utoipa::path(
    post,
    path = "/api/v1/batch-info",
    request_body = BatchInfoRequest,
    responses(
        (status = 200, description = "Get traffic info for multiple monitors", body = BatchInfoResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "VM Traffic"
)]
pub async fn batch_info_monitor(
    State(state): State<AppState>,
    Json(payload): Json<BatchInfoRequest>,
) -> Result<Json<BatchInfoResponse>, ApiError> {
    let mut seen = HashSet::new();
    let ids: Vec<i64> = payload
        .ids
        .into_iter()
        .filter(|id| *id > 0 && seen.insert(*id))
        .collect();

    if ids.is_empty() {
        return Ok(Json(BatchInfoResponse {
            monitors: Vec::new(),
            total: 0,
        }));
    }
    if ids.len() > 1000 {
        return Err(ApiError::bad_request(
            "batch info query supports at most 1000 monitor ids",
        ));
    }

    let placeholders = std::iter::repeat("?")
        .take(ids.len())
        .collect::<Vec<_>>()
        .join(",");
    let sql = format!(
        "SELECT id, interfaces, total_bytes, total_bytes_in, total_bytes_out, updated_at \
         FROM monitors WHERE id IN ({placeholders}) ORDER BY id"
    );

    let conn = state.conn.lock().await;
    let mut stmt = conn
        .prepare(&sql)
        .map_err(|e| ApiError::internal(format!("prepare batch info query error: {e}")))?;
    let rows = stmt
        .query_map(params_from_iter(ids.iter()), |row| {
            let interfaces_json: String = row.get(1)?;
            let interfaces: Vec<String> =
                serde_json::from_str(&interfaces_json).unwrap_or_default();
            Ok(InfoResponse {
                id: row.get(0)?,
                interface: interfaces,
                used_traffic: row.get(2)?,
                used_traffic_in: row.get(3)?,
                used_traffic_out: row.get(4)?,
                used_traffic_human: None,
                last_update_time: row.get(5)?,
            })
        })
        .map_err(|e| ApiError::internal(format!("batch info query error: {e}")))?;

    let mut monitors = Vec::new();
    for row in rows {
        monitors.push(row.map_err(|e| ApiError::internal(format!("batch info row error: {e}")))?);
    }

    let total = monitors.len();
    Ok(Json(BatchInfoResponse { monitors, total }))
}

#[utoipa::path(
    post,
    path = "/api/v1/cleanup",
    request_body = CleanupRequest,
    responses(
        (status = 200, description = "Cleanup stale monitor records", body = CleanupResponse),
        (status = 400, description = "Bad request", body = ErrorResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "VM Traffic"
)]
pub async fn cleanup_monitor(
    State(state): State<AppState>,
    Json(payload): Json<CleanupRequest>,
) -> Result<Json<CleanupResponse>, ApiError> {
    let max_age_seconds = parse_max_update_time_to_seconds(&payload.max_update_time)?;
    let use_ipt = state.traffic_collect_method == "ipt";
    let _operation_guard = state.traffic_operation_lock.lock().await;
    let deleted = {
        let conn = state.conn.lock().await;
        if max_age_seconds == 0 {
            conn.execute("DELETE FROM monitors", [])
                .map_err(|e| ApiError::internal(format!("cleanup all monitors error: {e}")))?
        } else {
            cleanup_stale_monitors(&conn, max_age_seconds)?
        }
    };
    let gc_conn = Connection::open("traffic.db")
        .map_err(|e| ApiError::internal(format!("open cleanup GC database error: {e}")))?;
    let gc_result = if use_ipt {
        ipt::garbage_collect_orphans(&gc_conn)
    } else {
        nft::garbage_collect_orphans(&gc_conn)
    };
    if let Err(err) = gc_result {
        warn!(error = %err.message, "cleanup finished but orphan GC failed");
    }
    info!(deleted, max_age_seconds, "manual cleanup finished");

    Ok(Json(CleanupResponse {
        deleted,
        max_update_seconds: max_age_seconds,
    }))
}

#[utoipa::path(
    post,
    path = "/api/v1/resources",
    request_body = ResourceQueryRequest,
    responses(
        (status = 200, description = "Get resource monitoring history", body = ResourceQueryResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 404, description = "Monitor not found", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "Resource Monitoring"
)]
pub async fn query_resources(
    State(state): State<AppState>,
    Json(payload): Json<ResourceQueryRequest>,
) -> Result<Json<ResourceQueryResponse>, ApiError> {
    let limit = payload.limit.unwrap_or(288).min(2880);
    let conn = state.conn.lock().await;

    let exists: Option<i64> = conn
        .query_row(
            "SELECT id FROM monitors WHERE id = ?1",
            params![payload.id],
            |row| row.get(0),
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("query monitor error: {e}")))?;
    if exists.is_none() {
        return Err(ApiError::not_found(format!(
            "monitor id {} not found",
            payload.id
        )));
    }

    let mut stmt = conn
        .prepare(
            "SELECT timestamp, cpu_percent, memory_used, memory_total, disk_used, disk_total \
             FROM resource_metrics WHERE monitor_id = ?1 ORDER BY timestamp DESC LIMIT ?2",
        )
        .map_err(|e| ApiError::internal(format!("prepare resource query error: {e}")))?;

    let rows = stmt
        .query_map(params![payload.id, limit], |row| {
            Ok(ResourceDataPoint {
                timestamp: row.get(0)?,
                cpu_percent: row.get(1)?,
                memory_used: row.get(2)?,
                memory_total: row.get(3)?,
                disk_used: row.get(4)?,
                disk_total: row.get(5)?,
            })
        })
        .map_err(|e| ApiError::internal(format!("resource query error: {e}")))?;

    let mut data = Vec::new();
    for row in rows {
        data.push(row.map_err(|e| ApiError::internal(format!("resource row error: {e}")))?);
    }

    // Return in chronological order
    data.reverse();

    Ok(Json(ResourceQueryResponse {
        id: payload.id,
        data,
    }))
}

#[utoipa::path(
    post,
    path = "/api/v1/resources/batch",
    request_body = BatchResourceQueryRequest,
    responses(
        (status = 200, description = "Get latest resource metrics in one query", body = BatchResourceQueryResponse),
        (status = 400, description = "Bad request", body = ErrorResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "Resource Monitoring"
)]
pub async fn batch_query_resources(
    State(state): State<AppState>,
    Json(payload): Json<BatchResourceQueryRequest>,
) -> Result<Json<BatchResourceQueryResponse>, ApiError> {
    let mut ids = payload.ids;
    ids.sort_unstable();
    ids.dedup();
    if ids.len() > 1000 {
        return Err(ApiError::bad_request(
            "batch resource query supports at most 1000 monitor ids",
        ));
    }
    if ids.is_empty() {
        return Ok(Json(BatchResourceQueryResponse {
            resources: Vec::new(),
            total: 0,
        }));
    }

    let placeholders = std::iter::repeat_n("?", ids.len())
        .collect::<Vec<_>>()
        .join(",");
    let sql = format!(
        "SELECT monitor_id, timestamp, cpu_percent, memory_used, memory_total, disk_used, disk_total \
         FROM ( \
             SELECT monitor_id, timestamp, cpu_percent, memory_used, memory_total, disk_used, disk_total, \
                    ROW_NUMBER() OVER (PARTITION BY monitor_id ORDER BY timestamp DESC, id DESC) AS row_num \
             FROM resource_metrics WHERE monitor_id IN ({placeholders}) \
         ) latest WHERE row_num = 1 ORDER BY monitor_id"
    );
    let conn = state.conn.lock().await;
    let mut stmt = conn
        .prepare(&sql)
        .map_err(|e| ApiError::internal(format!("prepare batch resource query error: {e}")))?;
    let rows = stmt
        .query_map(params_from_iter(ids.iter()), |row| {
            Ok(BatchResourceItem {
                id: row.get(0)?,
                data: ResourceDataPoint {
                    timestamp: row.get(1)?,
                    cpu_percent: row.get(2)?,
                    memory_used: row.get(3)?,
                    memory_total: row.get(4)?,
                    disk_used: row.get(5)?,
                    disk_total: row.get(6)?,
                },
            })
        })
        .map_err(|e| ApiError::internal(format!("batch resource query error: {e}")))?;
    let resources = rows
        .collect::<Result<Vec<_>, _>>()
        .map_err(|e| ApiError::internal(format!("batch resource row error: {e}")))?;
    let total = resources.len();
    Ok(Json(BatchResourceQueryResponse { resources, total }))
}

#[utoipa::path(
    get,
    path = "/api/v1/list",
    responses(
        (status = 200, description = "List all monitors", body = ListMonitorsResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "VM Traffic"
)]
pub async fn list_monitors(
    State(state): State<AppState>,
) -> Result<Json<ListMonitorsResponse>, ApiError> {
    let mut monitors = {
        let conn = state.conn.lock().await;
        let mut stmt = conn
            .prepare(
                "SELECT m.id, m.interfaces, m.bindings, m.inner_ip, m.provider_kind, m.instance_name, \
                        m.total_bytes, m.total_bytes_in, m.total_bytes_out, m.updated_at, s.interface \
                 FROM monitors m LEFT JOIN interface_states s ON s.monitor_id = m.id \
                 ORDER BY m.id, s.interface",
            )
            .map_err(|e| ApiError::internal(format!("prepare list query error: {e}")))?;
        let rows = stmt
            .query_map([], |row| {
                Ok((
                    row.get::<_, i64>(0)?,
                    row.get::<_, String>(1)?,
                    row.get::<_, String>(2)?,
                    row.get::<_, Option<String>>(3)?,
                    row.get::<_, Option<String>>(4)?,
                    row.get::<_, Option<String>>(5)?,
                    row.get::<_, u64>(6)?,
                    row.get::<_, u64>(7)?,
                    row.get::<_, u64>(8)?,
                    row.get::<_, i64>(9)?,
                    row.get::<_, Option<String>>(10)?,
                ))
            })
            .map_err(|e| ApiError::internal(format!("list query error: {e}")))?;

        let mut monitors = Vec::new();
        let mut index_by_id = HashMap::new();
        for row in rows {
            let (
                id,
                interfaces_json,
                bindings_json,
                inner_ip,
                provider_kind,
                instance_name,
                total_bytes,
                total_bytes_in,
                total_bytes_out,
                updated_at,
                active_interface,
            ) = row.map_err(|e| ApiError::internal(format!("list row error: {e}")))?;
            let index = if let Some(index) = index_by_id.get(&id).copied() {
                index
            } else {
                let bindings =
                    parse_persisted_bindings(&bindings_json, &interfaces_json, inner_ip.as_deref());
                let index = monitors.len();
                monitors.push(ListMonitorItem {
                    id,
                    interface: binding_interfaces(&bindings),
                    provider_kind,
                    instance_name,
                    total_bytes,
                    total_bytes_in,
                    total_bytes_out,
                    updated_at,
                    bindings,
                    active_interfaces: Vec::new(),
                    missing_interfaces: Vec::new(),
                    healthy: false,
                    health_error: None,
                });
                index_by_id.insert(id, index);
                index
            };
            if let Some(interface) = active_interface {
                monitors[index].active_interfaces.push(interface);
            }
        }
        monitors
    };

    for monitor in &mut monitors {
        monitor.active_interfaces.sort();
        monitor.active_interfaces.dedup();
        let active: HashSet<&str> = monitor
            .active_interfaces
            .iter()
            .map(String::as_str)
            .collect();
        monitor.missing_interfaces = monitor
            .bindings
            .iter()
            .filter(|binding| {
                !active.contains(binding.interface.as_str())
                    || !interface_exists(&binding.interface)
            })
            .map(|binding| binding.interface.clone())
            .collect();
        monitor.missing_interfaces.sort();
        monitor.missing_interfaces.dedup();
        monitor.healthy = !monitor.bindings.is_empty() && monitor.missing_interfaces.is_empty();
        if !monitor.healthy {
            monitor.health_error = Some(if monitor.bindings.is_empty() {
                "monitor has no valid desired traffic bindings".to_string()
            } else {
                format!(
                    "missing or inactive interfaces: {}",
                    monitor.missing_interfaces.join(",")
                )
            });
        }
    }

    let total = monitors.len();
    Ok(Json(ListMonitorsResponse { monitors, total }))
}

// ---- Block Rules Handlers ----

#[utoipa::path(
    post,
    path = "/api/v1/block-rules",
    request_body = ApplyBlockRulesRequest,
    responses(
        (status = 200, description = "Block rules applied", body = ApplyBlockRulesResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "Block Rules"
)]
pub async fn apply_block_rules(
    State(state): State<AppState>,
    Json(req): Json<ApplyBlockRulesRequest>,
) -> Result<Json<ApplyBlockRulesResponse>, ApiError> {
    let ip_version = req.ip_version.as_deref().unwrap_or("both");
    let count = if state.traffic_collect_method == "ipt" {
        ipt::apply_block_rules(&req.strings, ip_version)?
    } else {
        nft::apply_block_rules(&req.strings, ip_version)?
    };
    Ok(Json(ApplyBlockRulesResponse { applied: count }))
}

#[utoipa::path(
    delete,
    path = "/api/v1/block-rules",
    responses(
        (status = 200, description = "All block rules removed", body = RemoveBlockRulesResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "Block Rules"
)]
pub async fn remove_block_rules(
    State(state): State<AppState>,
) -> Result<Json<RemoveBlockRulesResponse>, ApiError> {
    if state.traffic_collect_method == "ipt" {
        ipt::remove_block_rules()?;
    } else {
        nft::remove_block_rules()?;
    }
    Ok(Json(RemoveBlockRulesResponse { removed: true }))
}

#[utoipa::path(
    get,
    path = "/api/v1/block-rules",
    responses(
        (status = 200, description = "Current block rules", body = GetBlockRulesResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "Block Rules"
)]
pub async fn get_block_rules(
    State(state): State<AppState>,
) -> Result<Json<GetBlockRulesResponse>, ApiError> {
    let (strings, ip_version) = if state.traffic_collect_method == "ipt" {
        ipt::get_block_rules()
    } else {
        nft::get_block_rules()
    };
    let count = strings.len();
    Ok(Json(GetBlockRulesResponse {
        strings,
        count,
        ip_version,
    }))
}

// ---- Domain Proxy Handlers ----

/// Validate domain name format (simple check)
fn validate_domain(domain: &str) -> Result<(), ApiError> {
    if domain.is_empty() || domain.len() > 253 {
        return Err(ApiError::bad_request("invalid domain length"));
    }
    let re =
        regex::Regex::new(r"^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$").unwrap();
    if !re.is_match(domain) {
        return Err(ApiError::bad_request("invalid domain format"));
    }
    Ok(())
}

#[utoipa::path(
    post,
    path = "/api/v1/domain-proxy",
    request_body = AddDomainProxyRequest,
    responses(
        (status = 200, description = "Domain proxy added", body = AddDomainProxyResponse),
        (status = 400, description = "Bad request", body = ErrorResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "Domain Proxy"
)]
pub async fn add_domain_proxy(
    State(state): State<AppState>,
    Json(mut req): Json<AddDomainProxyRequest>,
) -> Result<Json<AddDomainProxyResponse>, ApiError> {
    req.domain = req.domain.trim().to_lowercase();
    req.internal_ip = req.internal_ip.trim().to_string();
    validate_domain(&req.domain)?;

    let protocol = req
        .protocol
        .as_deref()
        .unwrap_or("http")
        .trim()
        .to_lowercase();
    if protocol != "http" && protocol != "https" {
        return Err(ApiError::bad_request("protocol must be http or https"));
    }
    if req.internal_port == 0 {
        return Err(ApiError::bad_request("invalid port"));
    }

    let enable_ssl = req.enable_ssl.unwrap_or(false);

    // Validate and parse SSL cert if provided.  Do not mutate the in-memory
    // certificate store until SQLite has accepted the route, otherwise a DB
    // write failure can leave a certificate without a matching proxy route.
    let mut ssl_cert = req.ssl_cert.unwrap_or_default();
    let mut ssl_key = req.ssl_key.unwrap_or_default();
    let mut parsed_cert = None;
    if enable_ssl && (ssl_cert.is_empty() || ssl_key.is_empty()) {
        return Err(ApiError::bad_request(
            "ssl_cert and ssl_key are required when enable_ssl is true",
        ));
    }
    if enable_ssl && !ssl_cert.is_empty() && !ssl_key.is_empty() {
        // Validate cert/key pair by parsing
        match crate::proxy::parse_certified_key(&ssl_cert, &ssl_key) {
            Ok(ck) => {
                parsed_cert = Some(std::sync::Arc::new(ck));
            }
            Err(e) => {
                return Err(ApiError::bad_request(format!(
                    "invalid SSL certificate: {e}"
                )));
            }
        }
    } else if !enable_ssl {
        ssl_cert.clear();
        ssl_key.clear();
    }

    // Save to DB
    let conn = state.conn.lock().await;
    conn.execute(
        "INSERT OR REPLACE INTO domain_proxies (domain, internal_ip, internal_port, protocol, enable_ssl, ssl_cert, ssl_key, created_at) VALUES (?1, ?2, ?3, ?4, ?5, ?6, ?7, ?8)",
        rusqlite::params![req.domain, req.internal_ip, req.internal_port, protocol.as_str(), enable_ssl as i32, ssl_cert, ssl_key, now_ts()],
    ).map_err(|e| ApiError::internal(format!("save domain proxy error: {e}")))?;
    drop(conn);

    // Add to in-memory proxy routes
    let target = crate::proxy::ProxyTarget {
        internal_ip: req.internal_ip.clone(),
        internal_port: req.internal_port,
        protocol,
    };
    crate::proxy::add_route(&state.proxy_routes, req.domain.clone(), target).await;

    if let Ok(mut store) = state.cert_store.write() {
        if let Some(ck) = parsed_cert {
            store.insert(req.domain.clone(), ck);
            info!(domain = %req.domain, "domain SSL certificate loaded");
        } else if !enable_ssl {
            store.remove(&req.domain);
        }
    }

    info!(domain = %req.domain, ip = %req.internal_ip, port = req.internal_port, "domain proxy added");
    Ok(Json(AddDomainProxyResponse {
        domain: req.domain,
        status: "active".into(),
    }))
}

#[utoipa::path(
    delete,
    path = "/api/v1/domain-proxy",
    request_body = RemoveDomainProxyRequest,
    responses(
        (status = 200, description = "Domain proxy removed", body = RemoveDomainProxyResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "Domain Proxy"
)]
pub async fn remove_domain_proxy(
    State(state): State<AppState>,
    Json(mut req): Json<RemoveDomainProxyRequest>,
) -> Result<Json<RemoveDomainProxyResponse>, ApiError> {
    req.domain = req.domain.trim().to_lowercase();
    // Remove from DB first
    let conn = state.conn.lock().await;
    let deleted = conn
        .execute(
            "DELETE FROM domain_proxies WHERE domain = ?1",
            rusqlite::params![req.domain],
        )
        .map_err(|e| ApiError::internal(format!("delete domain proxy error: {e}")))?;
    drop(conn);

    // Remove from in-memory proxy routes
    let removed = crate::proxy::remove_route(&state.proxy_routes, &req.domain).await;

    // Remove from cert store
    if let Ok(mut store) = state.cert_store.write() {
        store.remove(&req.domain);
    }

    info!(domain = %req.domain, "domain proxy removed");
    Ok(Json(RemoveDomainProxyResponse {
        domain: req.domain,
        removed: deleted > 0 || removed,
    }))
}

#[cfg(test)]
mod tests {
    use super::{CounterSnapshot, counter_health};
    use crate::models::TrafficBinding;

    #[test]
    fn counter_health_reports_missing_binding() {
        let bindings = vec![
            TrafficBinding {
                interface: "veth4".to_string(),
                addresses: vec!["10.0.0.2".to_string()],
                families: vec!["ipv4".to_string()],
            },
            TrafficBinding {
                interface: "veth6".to_string(),
                addresses: vec!["2001:db8::2".to_string()],
                families: vec!["ipv6".to_string()],
            },
        ];
        let snapshots = vec![CounterSnapshot {
            interface: "veth4".to_string(),
            base_in: 0,
            base_out: 0,
        }];
        let (healthy, missing, error) = counter_health(&bindings, &snapshots, &[]);
        assert!(!healthy);
        assert_eq!(missing, vec!["veth6".to_string()]);
        assert!(error.unwrap_or_default().contains("veth6"));
    }
}

#[utoipa::path(
    get,
    path = "/api/v1/domain-proxy",
    responses(
        (status = 200, description = "List domain proxies", body = ListDomainProxiesResponse),
        (status = 401, description = "Unauthorized", body = ErrorResponse),
        (status = 500, description = "Internal server error", body = ErrorResponse)
    ),
    security(
        ("token_auth" = [])
    ),
    tag = "Domain Proxy"
)]
pub async fn list_domain_proxies(
    State(state): State<AppState>,
) -> Result<Json<ListDomainProxiesResponse>, ApiError> {
    let conn = state.conn.lock().await;
    let mut stmt = conn
        .prepare("SELECT domain, internal_ip, internal_port, protocol, enable_ssl, ssl_cert, created_at FROM domain_proxies ORDER BY created_at")
        .map_err(|e| ApiError::internal(format!("prepare domain proxy query: {e}")))?;

    let rows = stmt
        .query_map([], |row| {
            let ssl_cert: String = row.get(5)?;
            Ok(DomainProxyItem {
                domain: row.get(0)?,
                internal_ip: row.get(1)?,
                internal_port: row.get(2)?,
                protocol: row.get(3)?,
                enable_ssl: row.get::<_, i32>(4)? != 0,
                has_cert: !ssl_cert.is_empty(),
                created_at: row.get(6)?,
            })
        })
        .map_err(|e| ApiError::internal(format!("domain proxy query: {e}")))?;

    let mut proxies = Vec::new();
    for row in rows {
        proxies.push(row.map_err(|e| ApiError::internal(format!("domain proxy row: {e}")))?);
    }

    let total = proxies.len();
    Ok(Json(ListDomainProxiesResponse { proxies, total }))
}
