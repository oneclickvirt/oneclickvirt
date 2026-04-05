use crate::{
    app_state::AppState,
    collector::normalize_interface_name,
    db::{cleanup_stale_monitors, now_ts},
    error::{ApiError, ErrorResponse},
    ipt, nft,
    models::{
        AddDomainProxyRequest, AddDomainProxyResponse, AddRequest, AddResponse,
        ApplyBlockRulesRequest, ApplyBlockRulesResponse, CleanupRequest, CleanupResponse,
        DeleteRequest, DeleteResponse, DomainProxyItem, GetBlockRulesResponse, InfoRequest,
        InfoResponse, ListDomainProxiesResponse, ListMonitorItem, ListMonitorsResponse,
        RemoveBlockRulesResponse, RemoveDomainProxyRequest, RemoveDomainProxyResponse,
        ResourceDataPoint, ResourceQueryRequest, ResourceQueryResponse, UpdateRequest,
        UpdateResponse,
    },
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

    // Validate inner_ip if provided
    let inner_ip = payload.inner_ip.as_deref().and_then(|ip| {
        let trimmed = ip.trim();
        if trimmed.is_empty() {
            None
        } else {
            // Basic IP validation
            if trimmed.parse::<std::net::IpAddr>().is_ok() {
                Some(trimmed.to_owned())
            } else {
                None
            }
        }
    });

    let use_ipt = state.traffic_collect_method == "ipt";
    let conn = state.conn.lock().await;
    conn.execute(
        "INSERT INTO monitors (interfaces, total_bytes, provider_kind, instance_name, inner_ip, updated_at) VALUES (?1, 0, ?2, ?3, ?4, ?5)",
        params![interfaces_json, payload.provider_kind, payload.instance_name, inner_ip, now],
    )
    .map_err(|e| ApiError::internal(format!("insert monitor error: {e}")))?;
    let id = conn.last_insert_rowid();

    for interface in &interfaces {
        if use_ipt {
            ipt::ensure_counter(id, interface, inner_ip.as_deref())?;
        } else {
            nft::ensure_counter(id, interface, inner_ip.as_deref())?;
        }
        let (base_in, base_out) = if use_ipt {
            ipt::read_external_bytes(id, interface).unwrap_or((0, 0))
        } else {
            nft::read_external_bytes(id, interface).unwrap_or((0, 0))
        };
        conn.execute(
            "INSERT INTO interface_states (monitor_id, interface, last_counter_in, last_counter_out) VALUES (?1, ?2, ?3, ?4)",
            params![id, interface, base_in, base_out],
        )
        .map_err(|e| ApiError::internal(format!("insert interface state error: {e}")))?;
    }

    info!(id, interfaces = ?interfaces, inner_ip = ?inner_ip, "monitor added");
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

    // Validate inner_ip if provided
    let inner_ip = payload.inner_ip.as_deref().and_then(|ip| {
        let trimmed = ip.trim();
        if trimmed.is_empty() {
            None
        } else {
            if trimmed.parse::<std::net::IpAddr>().is_ok() {
                Some(trimmed.to_owned())
            } else {
                None
            }
        }
    });

let use_ipt = state.traffic_collect_method == "ipt";
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

    // Read old inner_ip to detect changes
    let old_inner_ip: Option<String> = conn
        .query_row("SELECT inner_ip FROM monitors WHERE id = ?1", params![id], |row| {
            row.get(0)
        })
        .optional()
        .map_err(|e| ApiError::internal(format!("query old inner_ip error: {e}")))?  
        .flatten();

    conn.execute(
        "UPDATE monitors SET interfaces = ?1, updated_at = ?2, provider_kind = COALESCE(?4, provider_kind), instance_name = COALESCE(?5, instance_name), inner_ip = COALESCE(?6, inner_ip) WHERE id = ?3",
        params![interfaces_json, now, id, payload.provider_kind, payload.instance_name, inner_ip],
    )
    .map_err(|e| ApiError::internal(format!("update monitor error: {e}")))?;
    conn.execute(
        "DELETE FROM interface_states WHERE monitor_id = ?1",
        params![id],
    )
    .map_err(|e| ApiError::internal(format!("delete old interface states error: {e}")))?;

    // Determine effective inner_ip for counter rules
    let effective_inner_ip = inner_ip.as_deref().or(old_inner_ip.as_deref());

    for interface in &interfaces {
        if use_ipt {
            ipt::ensure_counter(id, interface, effective_inner_ip)?;
        } else {
            nft::ensure_counter(id, interface, effective_inner_ip)?;
        }
        let (base_in, base_out) = if use_ipt {
            ipt::read_external_bytes(id, interface).unwrap_or((0, 0))
        } else {
            nft::read_external_bytes(id, interface).unwrap_or((0, 0))
        };
        conn.execute(
            "INSERT INTO interface_states (monitor_id, interface, last_counter_in, last_counter_out) VALUES (?1, ?2, ?3, ?4)",
            params![id, interface, base_in, base_out],
        )
        .map_err(|e| ApiError::internal(format!("insert new interface state error: {e}")))?;
    }

    let new_set: HashSet<String> = interfaces.iter().cloned().collect();
    for old in old_interfaces {
        if !new_set.contains(&old) {
            if let Err(err) = if use_ipt { ipt::remove_counter(id, &old) } else { nft::remove_counter(id, &old) } {
                warn!(id, interface = old, error = %err.message, "failed to remove old counter rules after update");
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

    let use_ipt = state.traffic_collect_method == "ipt";
    if affected > 0 {
        for interface in old_interfaces {
            if let Err(err) = if use_ipt { ipt::remove_counter(id, &interface) } else { nft::remove_counter(id, &interface) } {
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
    let use_ipt = state.traffic_collect_method == "ipt";
    let conn = state.conn.lock().await;
    let deleted = cleanup_stale_monitors(&conn, max_age_seconds)?;
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
            "SELECT id, interfaces, provider_kind, instance_name, total_bytes, updated_at FROM monitors ORDER BY id",
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
                updated_at: row.get(5)?,
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
    Ok(Json(GetBlockRulesResponse { strings, count, ip_version }))
}

// ---- Domain Proxy Handlers ----

/// Validate domain name format (simple check)
fn validate_domain(domain: &str) -> Result<(), ApiError> {
    if domain.is_empty() || domain.len() > 253 {
        return Err(ApiError::bad_request("invalid domain length"));
    }
    let re = regex::Regex::new(r"^([a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?\.)+[a-zA-Z]{2,}$")
        .unwrap();
    if !re.is_match(domain) {
        return Err(ApiError::bad_request("invalid domain format"));
    }
    Ok(())
}

/// Generate nginx server block config for a domain proxy
fn generate_nginx_config(domain: &str, internal_ip: &str, internal_port: u16, protocol: &str, enable_ssl: bool) -> String {
    let upstream = format!("{}://{}:{}", protocol, internal_ip, internal_port);

    if enable_ssl {
        format!(
            r#"# Auto-generated by oneclickvirt-agent — do not edit
server {{
    listen 80;
    listen [::]:80;
    server_name {domain};
    return 301 https://$host$request_uri;
}}

server {{
    listen 443 ssl;
    listen [::]:443 ssl;
    server_name {domain};

    ssl_certificate /etc/nginx/ssl/{domain}.crt;
    ssl_certificate_key /etc/nginx/ssl/{domain}.key;

    location / {{
        proxy_pass {upstream};
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }}
}}
"#,
            domain = domain,
            upstream = upstream,
        )
    } else {
        format!(
            r#"# Auto-generated by oneclickvirt-agent — do not edit
server {{
    listen 80;
    listen [::]:80;
    server_name {domain};

    location / {{
        proxy_pass {upstream};
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }}
}}
"#,
            domain = domain,
            upstream = upstream,
        )
    }
}

/// Write nginx config file and reload nginx
fn apply_nginx_config(domain: &str, config_content: &str) -> Result<(), ApiError> {
    let config_dir = std::path::Path::new("/etc/nginx/conf.d");
    if !config_dir.exists() {
        return Err(ApiError::internal("nginx conf.d directory not found".to_string()));
    }

    let config_path = config_dir.join(format!("ocv-{}.conf", domain));
    std::fs::write(&config_path, config_content)
        .map_err(|e| ApiError::internal(format!("failed to write nginx config: {e}")))?;

    // Test nginx config
    let test_output = std::process::Command::new("nginx")
        .args(["-t"])
        .output()
        .map_err(|e| ApiError::internal(format!("failed to test nginx config: {e}")))?;

    if !test_output.status.success() {
        // Remove the bad config
        let _ = std::fs::remove_file(&config_path);
        let stderr = String::from_utf8_lossy(&test_output.stderr);
        return Err(ApiError::internal(format!("nginx config test failed: {stderr}")));
    }

    // Reload nginx
    let reload_output = std::process::Command::new("nginx")
        .args(["-s", "reload"])
        .output()
        .map_err(|e| ApiError::internal(format!("failed to reload nginx: {e}")))?;

    if !reload_output.status.success() {
        let stderr = String::from_utf8_lossy(&reload_output.stderr);
        warn!("nginx reload warning: {}", stderr);
    }

    info!(domain, "nginx config applied and reloaded");
    Ok(())
}

/// Remove nginx config file and reload
fn remove_nginx_config(domain: &str) -> Result<(), ApiError> {
    let config_path = std::path::Path::new("/etc/nginx/conf.d").join(format!("ocv-{}.conf", domain));
    if config_path.exists() {
        std::fs::remove_file(&config_path)
            .map_err(|e| ApiError::internal(format!("failed to remove nginx config: {e}")))?;

        // Reload nginx
        let _ = std::process::Command::new("nginx")
            .args(["-s", "reload"])
            .output();

        info!(domain, "nginx config removed and reloaded");
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
    Json(req): Json<AddDomainProxyRequest>,
) -> Result<Json<AddDomainProxyResponse>, ApiError> {
    validate_domain(&req.domain)?;

    let protocol = req.protocol.as_deref().unwrap_or("http");
    if protocol != "http" && protocol != "https" {
        return Err(ApiError::bad_request("protocol must be http or https"));
    }
    if req.internal_port == 0 {
        return Err(ApiError::bad_request("invalid port"));
    }

    let enable_ssl = req.enable_ssl.unwrap_or(false);

    // Generate and apply nginx config
    let config = generate_nginx_config(&req.domain, &req.internal_ip, req.internal_port, protocol, enable_ssl);
    apply_nginx_config(&req.domain, &config)?;

    // Save to DB
    let conn = state.conn.lock().await;
    conn.execute(
        "INSERT OR REPLACE INTO domain_proxies (domain, internal_ip, internal_port, protocol, enable_ssl, created_at) VALUES (?1, ?2, ?3, ?4, ?5, ?6)",
        rusqlite::params![req.domain, req.internal_ip, req.internal_port, protocol, enable_ssl as i32, now_ts()],
    ).map_err(|e| ApiError::internal(format!("save domain proxy error: {e}")))?;

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
    Json(req): Json<RemoveDomainProxyRequest>,
) -> Result<Json<RemoveDomainProxyResponse>, ApiError> {
    // Remove nginx config
    remove_nginx_config(&req.domain)?;

    // Remove from DB
    let conn = state.conn.lock().await;
    let deleted = conn.execute(
        "DELETE FROM domain_proxies WHERE domain = ?1",
        rusqlite::params![req.domain],
    ).map_err(|e| ApiError::internal(format!("delete domain proxy error: {e}")))?;

    info!(domain = %req.domain, "domain proxy removed");
    Ok(Json(RemoveDomainProxyResponse {
        domain: req.domain,
        removed: deleted > 0,
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
        .prepare("SELECT domain, internal_ip, internal_port, protocol, enable_ssl, created_at FROM domain_proxies ORDER BY created_at")
        .map_err(|e| ApiError::internal(format!("prepare domain proxy query: {e}")))?;

    let rows = stmt.query_map([], |row| {
        Ok(DomainProxyItem {
            domain: row.get(0)?,
            internal_ip: row.get(1)?,
            internal_port: row.get(2)?,
            protocol: row.get(3)?,
            enable_ssl: row.get::<_, i32>(4)? != 0,
            created_at: row.get(5)?,
        })
    }).map_err(|e| ApiError::internal(format!("domain proxy query: {e}")))?;

    let mut proxies = Vec::new();
    for row in rows {
        proxies.push(row.map_err(|e| ApiError::internal(format!("domain proxy row: {e}")))?);
    }

    let total = proxies.len();
    Ok(Json(ListDomainProxiesResponse { proxies, total }))
}
