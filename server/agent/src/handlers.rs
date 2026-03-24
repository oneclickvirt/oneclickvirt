use crate::{
    app_state::AppState,
    collector::normalize_interface_name,
    db::{cleanup_stale_monitors, now_ts},
    error::{ApiError, ErrorResponse},
    models::{
        AddRequest, AddResponse, CleanupRequest, CleanupResponse, DeleteRequest, DeleteResponse,
        InfoRequest, InfoResponse, UpdateRequest, UpdateResponse,
    },
    nft::{ensure_counter, garbage_collect_orphans, read_external_bytes, remove_counter},
};
use axum::{
    Json,
    extract::{Query, State},
};
use rusqlite::{OptionalExtension, params};
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

    if !consumed_any || total <= 0 {
        return Err(ApiError::bad_request(
            "max_update_time must be greater than 0",
        ));
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
    let interfaces_json = serde_json::to_string(&interfaces)
        .map_err(|e| ApiError::internal(format!("serialize interfaces error: {e}")))?;
    let now = now_ts();

    let conn = state.conn.lock().await;
    conn.execute(
        "INSERT INTO monitors (interfaces, total_bytes, updated_at) VALUES (?1, 0, ?2)",
        params![interfaces_json, now],
    )
    .map_err(|e| ApiError::internal(format!("insert monitor error: {e}")))?;
    let id = conn.last_insert_rowid();

    for interface in &interfaces {
        ensure_counter(id, interface)?;
        let base_counter = read_external_bytes(id, interface).unwrap_or(0);
        conn.execute(
            "INSERT INTO interface_states (monitor_id, interface, last_counter) VALUES (?1, ?2, ?3)",
            params![id, interface, base_counter],
        )
        .map_err(|e| ApiError::internal(format!("insert interface state error: {e}")))?;
    }

    info!(id, interfaces = ?interfaces, "monitor added");
    Ok(Json(AddResponse {
        id,
        interface: interfaces,
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
    let interfaces_json = serde_json::to_string(&interfaces)
        .map_err(|e| ApiError::internal(format!("serialize interfaces error: {e}")))?;
    let now = now_ts();
    let id = payload.id;

    let conn = state.conn.lock().await;
    let exists: Option<i64> = conn
        .query_row("SELECT id FROM monitors WHERE id = ?1", params![id], |row| {
            row.get(0)
        })
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
        old_interfaces
            .push(row.map_err(|e| ApiError::internal(format!("old interface row error: {e}")))?);
    }

    conn.execute(
        "UPDATE monitors SET interfaces = ?1, updated_at = ?2 WHERE id = ?3",
        params![interfaces_json, now, id],
    )
    .map_err(|e| ApiError::internal(format!("update monitor error: {e}")))?;
    conn.execute(
        "DELETE FROM interface_states WHERE monitor_id = ?1",
        params![id],
    )
    .map_err(|e| ApiError::internal(format!("delete old interface states error: {e}")))?;

    for interface in &interfaces {
        ensure_counter(id, interface)?;
        let base_counter = read_external_bytes(id, interface).unwrap_or(0);
        conn.execute(
            "INSERT INTO interface_states (monitor_id, interface, last_counter) VALUES (?1, ?2, ?3)",
            params![id, interface, base_counter],
        )
        .map_err(|e| ApiError::internal(format!("insert new interface state error: {e}")))?;
    }

    let new_set: HashSet<String> = interfaces.iter().cloned().collect();
    for old in old_interfaces {
        if !new_set.contains(&old) {
            if let Err(err) = remove_counter(id, &old) {
                warn!(id, interface = old, error = %err.message, "failed to remove old nft rules after update");
            }
        }
    }

    info!(id, interfaces = ?interfaces, "monitor interfaces updated");
    Ok(Json(UpdateResponse {
        id,
        interface: interfaces,
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
    let conn = state.conn.lock().await;
    let mut old_stmt = conn
        .prepare("SELECT interface FROM interface_states WHERE monitor_id = ?1")
        .map_err(|e| ApiError::internal(format!("prepare delete interfaces query error: {e}")))?;
    let old_rows = old_stmt
        .query_map(params![id], |row| row.get::<_, String>(0))
        .map_err(|e| ApiError::internal(format!("delete interfaces query error: {e}")))?;
    let mut old_interfaces: Vec<String> = Vec::new();
    for row in old_rows {
        old_interfaces.push(
            row.map_err(|e| ApiError::internal(format!("delete interface row error: {e}")))?,
        );
    }

    let affected = conn
        .execute("DELETE FROM monitors WHERE id = ?1", params![id])
        .map_err(|e| ApiError::internal(format!("delete monitor error: {e}")))?;

    if affected > 0 {
        for interface in old_interfaces {
            if let Err(err) = remove_counter(id, &interface) {
                warn!(id, interface, error = %err.message, "failed to remove nft rules after delete");
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
            "SELECT id, interfaces, total_bytes, updated_at FROM monitors WHERE id = ?1",
            params![payload.id],
            |row| {
                let interfaces_json: String = row.get(1)?;
                let interfaces: Vec<String> =
                    serde_json::from_str(&interfaces_json).unwrap_or_default();
                let used_traffic: u64 = row.get(2)?;
                let used_traffic_human = if query.human == Some(1) {
                    Some(human_bytes(used_traffic))
                } else {
                    None
                };
                Ok(InfoResponse {
                    id: row.get(0)?,
                    interface: interfaces,
                    used_traffic,
                    used_traffic_human,
                    last_update_time: row.get(3)?,
                })
            },
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("query monitor info error: {e}")))?;

    row.map(Json)
        .ok_or_else(|| {
            debug!(id = payload.id, "info requested for missing monitor");
            ApiError::not_found(format!("monitor id {} not found", payload.id))
        })
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
    let conn = state.conn.lock().await;
    let deleted = cleanup_stale_monitors(&conn, max_age_seconds)?;
    if let Err(err) = garbage_collect_orphans(&conn) {
        warn!(error = %err.message, "cleanup finished but nft orphan GC failed");
    }
    info!(deleted, max_age_seconds, "manual cleanup finished");

    Ok(Json(CleanupResponse {
        deleted,
        max_update_seconds: max_age_seconds,
    }))
}
