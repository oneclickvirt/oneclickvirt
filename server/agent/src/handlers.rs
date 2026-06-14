use crate::{
    app_state::AppState,
    collector::normalize_interface_name,
    db::{cleanup_stale_monitors, now_ts},
    error::{ApiError, ErrorResponse},
    ipt,
    models::{
        AddDomainProxyRequest, AddDomainProxyResponse, AddRequest, AddResponse,
        ApplyBlockRulesRequest, ApplyBlockRulesResponse, BatchInfoRequest, BatchInfoResponse,
        CleanupRequest, CleanupResponse, DeleteRequest, DeleteResponse, DomainProxyItem,
        GetBlockRulesResponse, InfoRequest, InfoResponse, InterfaceInput,
        ListDomainProxiesResponse, ListMonitorItem, ListMonitorsResponse, RemoveBlockRulesResponse,
        RemoveDomainProxyRequest, RemoveDomainProxyResponse, ResourceDataPoint,
        ResourceQueryRequest, ResourceQueryResponse, UpdateRequest, UpdateResponse,
    },
    nft,
};
use axum::{
    Json,
    extract::{Query, State},
};
use rusqlite::{OptionalExtension, params, params_from_iter};
use std::collections::HashSet;
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

fn clean_interfaces(items: Vec<String>) -> Result<Vec<String>, ApiError> {
    let mut seen = HashSet::new();
    let mut cleaned = Vec::new();

    for item in items {
        if let Some(iface) = normalize_interface_name(&item) {
            if seen.insert(iface.clone()) {
                cleaned.push(iface);
            }
        }
    }

    if cleaned.is_empty() {
        return Err(ApiError::bad_request(
            "interface/new_interface must contain at least one valid interface",
        ));
    }
    Ok(cleaned)
}

fn validate_inner_ip(raw: Option<&str>) -> Result<Option<String>, ApiError> {
    let Some(ip) = raw else {
        return Ok(None);
    };
    let trimmed = ip.trim();
    if trimmed.is_empty() {
        return Ok(None);
    }
    if trimmed.parse::<std::net::IpAddr>().is_err() {
        return Err(ApiError::bad_request("inner_ip must be a valid IP address"));
    }
    Ok(Some(trimmed.to_owned()))
}

#[derive(Debug)]
struct CounterSnapshot {
    interface: String,
    base_in: u64,
    base_out: u64,
}

fn ensure_interface_counters(
    monitor_id: i64,
    interfaces: &[String],
    inner_ip: Option<&str>,
    use_ipt: bool,
) -> (Vec<CounterSnapshot>, Vec<String>) {
    let mut snapshots = Vec::new();
    let mut errors = Vec::new();

    for interface in interfaces {
        let ensure_result = if use_ipt {
            ipt::ensure_counter(monitor_id, interface, inner_ip)
        } else {
            nft::ensure_counter(monitor_id, interface, inner_ip)
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

fn serialize_snapshot_interfaces(snapshots: &[CounterSnapshot]) -> Result<String, ApiError> {
    let interfaces: Vec<String> = snapshots.iter().map(|s| s.interface.clone()).collect();
    serde_json::to_string(&interfaces)
        .map_err(|e| ApiError::internal(format!("serialize interfaces error: {e}")))
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
    let interfaces = clean_interfaces(payload.interface.into_vec())?;
    let now = now_ts();
    let provider_kind = payload.provider_kind.clone();
    let instance_name = payload.instance_name.clone();
    let inner_ip_raw = payload.inner_ip.clone();

    let inner_ip = validate_inner_ip(inner_ip_raw.as_deref())?;

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
                provider_kind,
                instance_name,
                inner_ip: inner_ip_raw,
            };
            let Json(resp) = update_monitor(State(state), Json(update_payload)).await?;
            return Ok(Json(AddResponse {
                id: resp.id,
                interface: resp.interface,
            }));
        }
    }

    let use_ipt = state.traffic_collect_method == "ipt";
    let requested_interfaces_json = serde_json::to_string(&interfaces)
        .map_err(|e| ApiError::internal(format!("serialize interfaces error: {e}")))?;
    let id = {
        let conn = state.conn.lock().await;
        conn.execute(
            "INSERT INTO monitors (interfaces, total_bytes, provider_kind, instance_name, inner_ip, updated_at) VALUES (?1, 0, ?2, ?3, ?4, ?5)",
            params![requested_interfaces_json, provider_kind.as_deref(), instance_name.as_deref(), inner_ip.as_deref(), now],
        )
        .map_err(|e| ApiError::internal(format!("insert monitor error: {e}")))?;
        conn.last_insert_rowid()
    };

    // Do not hold the SQLite mutex while invoking nft/iptables.  Each interface is
    // reconciled independently so a broken IPv6-only/secondary NIC does not prevent
    // the valid IPv4 NIC from being monitored.
    let (snapshots, counter_errors) =
        ensure_interface_counters(id, &interfaces, inner_ip.as_deref(), use_ipt);
    if snapshots.is_empty() {
        let conn = state.conn.lock().await;
        let _ = conn.execute("DELETE FROM monitors WHERE id = ?1", params![id]);
        return Err(ApiError::internal(format!(
            "failed to ensure counters for all interfaces: {}",
            counter_errors.join("; ")
        )));
    }

    let active_interfaces_json = serialize_snapshot_interfaces(&snapshots)?;
    {
        let mut conn = state.conn.lock().await;
        let tx = conn
            .transaction()
            .map_err(|e| ApiError::internal(format!("begin add monitor transaction error: {e}")))?;
        tx.execute(
            "UPDATE monitors SET interfaces = ?1, updated_at = ?2 WHERE id = ?3",
            params![active_interfaces_json, now, id],
        )
        .map_err(|e| ApiError::internal(format!("update active monitor interfaces error: {e}")))?;
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

    let active_interfaces: Vec<String> = snapshots.iter().map(|s| s.interface.clone()).collect();
    if !counter_errors.is_empty() {
        warn!(id, errors = ?counter_errors, "monitor added with partial interface coverage");
    }

    info!(id, interfaces = ?active_interfaces, inner_ip = ?inner_ip, "monitor added");
    Ok(Json(AddResponse {
        id,
        interface: active_interfaces,
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
    let interfaces = clean_interfaces(payload.new_interface.into_vec())?;
    let now = now_ts();
    let id = payload.id;

    let inner_ip = validate_inner_ip(payload.inner_ip.as_deref())?;

    let use_ipt = state.traffic_collect_method == "ipt";
    let old_interfaces = {
        let conn = state.conn.lock().await;
        let exists: Option<i64> = conn
            .query_row(
                "SELECT id FROM monitors WHERE id = ?1",
                params![id],
                |row| row.get(0),
            )
            .optional()
            .map_err(|e| ApiError::internal(format!("query monitor error: {e}")))?;
        if exists.is_none() {
            warn!(id, "update failed: monitor not found");
            return Err(ApiError::not_found(format!("monitor id {id} not found")));
        }

        let mut old_stmt = conn
            .prepare("SELECT interface FROM interface_states WHERE monitor_id = ?1")
            .map_err(|e| ApiError::internal(format!("prepare old interfaces query error: {e}")))?;
        let old_rows = old_stmt
            .query_map(params![id], |row| row.get::<_, String>(0))
            .map_err(|e| ApiError::internal(format!("old interfaces query error: {e}")))?;
        let mut old_interfaces: Vec<String> = Vec::new();
        for row in old_rows {
            old_interfaces.push(
                row.map_err(|e| ApiError::internal(format!("old interface row error: {e}")))?,
            );
        }
        old_interfaces
    };

    // The control plane always sends the current inner_ip value. Empty/invalid values
    // clear per-IP filtering so dedicated-interface monitoring does not keep stale IP rules.
    let effective_inner_ip = inner_ip.as_deref();

    // Reconcile counters before mutating the DB. If every requested interface fails,
    // keep the old monitor intact so a transient IPv6/secondary-NIC error does not
    // break the already working IPv4 monitor.
    let (snapshots, counter_errors) =
        ensure_interface_counters(id, &interfaces, effective_inner_ip, use_ipt);
    if snapshots.is_empty() {
        return Err(ApiError::internal(format!(
            "failed to ensure counters for all interfaces: {}",
            counter_errors.join("; ")
        )));
    }

    let active_interfaces_json = serialize_snapshot_interfaces(&snapshots)?;
    {
        let mut conn = state.conn.lock().await;
        let tx = conn.transaction().map_err(|e| {
            ApiError::internal(format!("begin update monitor transaction error: {e}"))
        })?;
        tx.execute(
            "UPDATE monitors SET interfaces = ?1, updated_at = ?2, provider_kind = COALESCE(?4, provider_kind), instance_name = COALESCE(?5, instance_name), inner_ip = ?6 WHERE id = ?3",
            params![active_interfaces_json, now, id, payload.provider_kind.as_deref(), payload.instance_name.as_deref(), inner_ip.as_deref()],
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

    let active_interfaces: Vec<String> = snapshots.iter().map(|s| s.interface.clone()).collect();
    let new_set: HashSet<String> = active_interfaces.iter().cloned().collect();
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

    if !counter_errors.is_empty() {
        warn!(id, errors = ?counter_errors, "monitor updated with partial interface coverage");
    }

    info!(id, interfaces = ?active_interfaces, "monitor interfaces updated");
    Ok(Json(UpdateResponse {
        id,
        interface: active_interfaces,
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
    let (affected, old_interfaces) = {
        let conn = state.conn.lock().await;
        let old_interfaces = {
            let mut old_stmt = conn
                .prepare("SELECT interface FROM interface_states WHERE monitor_id = ?1")
                .map_err(|e| {
                    ApiError::internal(format!("prepare delete interfaces query error: {e}"))
                })?;
            let old_rows = old_stmt
                .query_map(params![id], |row| row.get::<_, String>(0))
                .map_err(|e| ApiError::internal(format!("delete interfaces query error: {e}")))?;
            let mut old_interfaces: Vec<String> = Vec::new();
            for row in old_rows {
                old_interfaces.push(
                    row.map_err(|e| {
                        ApiError::internal(format!("delete interface row error: {e}"))
                    })?,
                );
            }
            old_interfaces
        };

        let affected = conn
            .execute("DELETE FROM monitors WHERE id = ?1", params![id])
            .map_err(|e| ApiError::internal(format!("delete monitor error: {e}")))?;
        (affected, old_interfaces)
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
    let conn = state.conn.lock().await;
    let deleted = if max_age_seconds == 0 {
        conn.execute("DELETE FROM monitors", [])
            .map_err(|e| ApiError::internal(format!("cleanup all monitors error: {e}")))?
    } else {
        cleanup_stale_monitors(&conn, max_age_seconds)?
    };
    let gc_result = if use_ipt {
        ipt::garbage_collect_orphans(&conn)
    } else {
        nft::garbage_collect_orphans(&conn)
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
    let conn = state.conn.lock().await;
    let mut stmt = conn
        .prepare(
            "SELECT id, interfaces, provider_kind, instance_name, total_bytes, total_bytes_in, total_bytes_out, updated_at FROM monitors ORDER BY id",
        )
        .map_err(|e| ApiError::internal(format!("prepare list query error: {e}")))?;

    let rows = stmt
        .query_map([], |row| {
            let interfaces_json: String = row.get(1)?;
            let interfaces: Vec<String> =
                serde_json::from_str(&interfaces_json).unwrap_or_default();
            Ok(ListMonitorItem {
                id: row.get(0)?,
                interface: interfaces,
                provider_kind: row.get(2)?,
                instance_name: row.get(3)?,
                total_bytes: row.get(4)?,
                total_bytes_in: row.get(5)?,
                total_bytes_out: row.get(6)?,
                updated_at: row.get(7)?,
            })
        })
        .map_err(|e| ApiError::internal(format!("list query error: {e}")))?;

    let mut monitors = Vec::new();
    for row in rows {
        monitors.push(row.map_err(|e| ApiError::internal(format!("list row error: {e}")))?);
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

    // Validate and parse SSL cert if provided
    let mut ssl_cert = req.ssl_cert.unwrap_or_default();
    let mut ssl_key = req.ssl_key.unwrap_or_default();
    if enable_ssl && (ssl_cert.is_empty() || ssl_key.is_empty()) {
        return Err(ApiError::bad_request(
            "ssl_cert and ssl_key are required when enable_ssl is true",
        ));
    }
    if enable_ssl && !ssl_cert.is_empty() && !ssl_key.is_empty() {
        // Validate cert/key pair by parsing
        match crate::proxy::parse_certified_key(&ssl_cert, &ssl_key) {
            Ok(ck) => {
                // Add to in-memory cert store
                if let Ok(mut store) = state.cert_store.write() {
                    store.insert(req.domain.clone(), std::sync::Arc::new(ck));
                }
                info!(domain = %req.domain, "domain SSL certificate loaded");
            }
            Err(e) => {
                return Err(ApiError::bad_request(format!(
                    "invalid SSL certificate: {e}"
                )));
            }
        }
    } else if !enable_ssl {
        // Remove cert from store if SSL is disabled
        if let Ok(mut store) = state.cert_store.write() {
            store.remove(&req.domain);
        }
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
