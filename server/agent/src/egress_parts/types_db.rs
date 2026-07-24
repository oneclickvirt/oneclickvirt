use crate::{app_state::AppState, db::now_ts, error::ApiError};
use axum::{Json, extract::State};
use base64::{Engine as _, engine::general_purpose::STANDARD as BASE64};
use rusqlite::{OptionalExtension, params};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use std::{
    collections::{BTreeSet, HashMap, HashSet},
    env,
    fs::{self, OpenOptions},
    io::{Read, Write},
    net::{IpAddr, Ipv4Addr, Ipv6Addr},
    os::unix::fs::{OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    str::FromStr,
    sync::OnceLock,
    time::{Duration, Instant},
};
use tokio::sync::Mutex as TokioMutex;
use tracing::{info, warn};
use utoipa::ToSchema;

const APPLY_ENV: &str = "ONECLICKVIRT_EGRESS_APPLY";
const AUTO_INSTALL_ENV: &str = "ONECLICKVIRT_EGRESS_AUTO_INSTALL";
const EGRESS_MODES: &[&str] = &["native", "gateway", "cni"];
const TUNNEL_TYPES: &[&str] = &["wireguard", "ipsec", "gateway"];
const TABLE_FAMILY: &str = "inet";
const TABLE_NAME: &str = "oneclickvirt_egress";
const BOOT_TABLE_NAME: &str = "oneclickvirt_egress_boot";
const PROBE_RULE_PRIORITY_BASE: u32 = 10_000;
const RULE_PRIORITY_BASE: u32 = 20_000;
const MAX_ROUTE_TABLE: u32 = 9_999;
const MAX_MARK: u32 = 0x00ff_ffff;
const MAX_STATE_PROFILES: usize = 10_000;
const MAX_STATE_BINDINGS: usize = 100_000;
const WIREGUARD_HANDSHAKE_MAX_AGE_SECS: i64 = 180;
const PUBLIC_IPV4_PROBE_URL: &str = "https://api.ipify.org";
const PUBLIC_IPV6_PROBE_URL: &str = "https://api6.ipify.org";
const MARK_NAMESPACE: u32 = 0x4f00_0000;
const COMMAND_TIMEOUT: Duration = Duration::from_secs(30);
const INSTALL_TIMEOUT: Duration = Duration::from_secs(180);

static RECONCILE_LOCK: OnceLock<TokioMutex<()>> = OnceLock::new();

fn reconcile_lock() -> &'static TokioMutex<()> {
    RECONCILE_LOCK.get_or_init(|| TokioMutex::new(()))
}

fn env_enabled(name: &str) -> bool {
    env::var(name)
        .map(|value| {
            matches!(
                value.trim().to_ascii_lowercase().as_str(),
                "1" | "true" | "yes" | "on"
            )
        })
        .unwrap_or(false)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash)]
enum Family {
    V4,
    V6,
}

impl Family {
    fn ip_flag(self) -> &'static str {
        match self {
            Self::V4 => "-4",
            Self::V6 => "-6",
        }
    }

    fn nft_prefix(self) -> &'static str {
        match self {
            Self::V4 => "ip",
            Self::V6 => "ip6",
        }
    }

    fn max_prefix(self) -> u8 {
        match self {
            Self::V4 => 32,
            Self::V6 => 128,
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Hash)]
struct IpNetwork {
    addr: IpAddr,
    prefix: u8,
}

impl IpNetwork {
    fn family(&self) -> Family {
        if self.addr.is_ipv4() {
            Family::V4
        } else {
            Family::V6
        }
    }

    fn bits(&self) -> u128 {
        match self.addr {
            IpAddr::V4(value) => u32::from(value) as u128,
            IpAddr::V6(value) => u128::from(value),
        }
    }

    fn canonical_bits(&self) -> u128 {
        mask_bits(self.bits(), self.prefix, self.family())
    }

    fn end_bits(&self) -> u128 {
        let width = self.family().max_prefix();
        let start = self.canonical_bits();
        if self.prefix == 0 {
            match self.family() {
                Family::V4 => u32::MAX as u128,
                Family::V6 => u128::MAX,
            }
        } else {
            start | (low_mask(width - self.prefix))
        }
    }

    fn host_string(&self) -> String {
        format!("{}/{}", self.addr, self.prefix)
    }
}

impl std::fmt::Display for IpNetwork {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let addr = match self.family() {
            Family::V4 => IpAddr::V4(Ipv4Addr::from(self.canonical_bits() as u32)),
            Family::V6 => IpAddr::V6(Ipv6Addr::from(self.canonical_bits())),
        };
        write!(f, "{addr}/{}", self.prefix)
    }
}

fn low_mask(bits: u8) -> u128 {
    if bits == 0 {
        0
    } else if bits >= 128 {
        u128::MAX
    } else {
        (1u128 << bits) - 1
    }
}

fn mask_bits(value: u128, prefix: u8, family: Family) -> u128 {
    let width = family.max_prefix();
    if prefix == 0 {
        return 0;
    }
    let mask = if prefix >= width {
        low_mask(width)
    } else {
        low_mask(width) << (width - prefix)
    };
    value & mask
}

fn parse_network(
    raw: &str,
    field: &str,
    allow_host_without_prefix: bool,
) -> Result<IpNetwork, ApiError> {
    let value = clean_required(raw, field, 160)?;
    let (address, prefix) = match value.split_once('/') {
        Some((address, prefix)) => (address.trim(), Some(prefix.trim())),
        None if allow_host_without_prefix => (value.as_str(), None),
        None => {
            return Err(ApiError::bad_request(format!(
                "{field} must include a prefix"
            )));
        }
    };
    let ip =
        IpAddr::from_str(address).map_err(|_| ApiError::bad_request(format!("invalid {field}")))?;
    let family = if ip.is_ipv4() { Family::V4 } else { Family::V6 };
    let prefix = match prefix {
        Some(value) => value
            .parse::<u8>()
            .map_err(|_| ApiError::bad_request(format!("invalid {field} prefix")))?,
        None => family.max_prefix(),
    };
    if prefix > family.max_prefix() || ip.is_unspecified() || ip.is_multicast() {
        return Err(ApiError::bad_request(format!("invalid {field}")));
    }
    Ok(IpNetwork { addr: ip, prefix })
}

fn clean_required(value: &str, field: &str, max: usize) -> Result<String, ApiError> {
    let value = value.trim();
    if value.is_empty() || value.len() > max || value.chars().any(char::is_control) {
        return Err(ApiError::bad_request(format!("invalid {field}")));
    }
    Ok(value.to_string())
}

fn validate_id(value: &str, field: &str) -> Result<String, ApiError> {
    let value = clean_required(value, field, 128)?;
    if !value
        .bytes()
        .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b':' | b'-'))
    {
        return Err(ApiError::bad_request(format!("invalid {field}")));
    }
    Ok(value)
}

fn validate_interface(value: &str, field: &str) -> Result<String, ApiError> {
    let value = clean_required(value, field, 15)?;
    if !value
        .bytes()
        .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'_' | b'-'))
    {
        return Err(ApiError::bad_request(format!("invalid {field}")));
    }
    Ok(value)
}

fn validate_endpoint(value: &str) -> Result<String, ApiError> {
    let value = clean_required(value, "wireguard endpoint", 320)?;
    let (host, port) = if let Some(rest) = value.strip_prefix('[') {
        let (host, port) = rest
            .split_once("]:")
            .ok_or_else(|| ApiError::bad_request("invalid wireguard endpoint"))?;
        (host, port)
    } else {
        value
            .rsplit_once(':')
            .ok_or_else(|| ApiError::bad_request("wireguard endpoint must include port"))?
    };
    if host.is_empty()
        || port.parse::<u16>().ok().filter(|p| *p != 0).is_none()
        || !host
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'.' | b'-' | b':' | b'%'))
    {
        return Err(ApiError::bad_request("invalid wireguard endpoint"));
    }
    Ok(value)
}

fn validate_key(raw: &str, field: &str) -> Result<String, ApiError> {
    let value = clean_required(raw, field, 128)?;
    let decoded = BASE64
        .decode(value.as_bytes())
        .map_err(|_| ApiError::bad_request(format!("invalid {field}")))?;
    if decoded.len() != 32 {
        return Err(ApiError::bad_request(format!("invalid {field}")));
    }
    Ok(value)
}

fn validate_vec_networks(
    values: Option<Vec<String>>,
    field: &str,
    default: &[&str],
    preserve_host: bool,
    allow_default_route: bool,
) -> Result<Vec<String>, ApiError> {
    let values = values.unwrap_or_else(|| default.iter().map(|v| (*v).to_string()).collect());
    if values.is_empty() || values.len() > 64 {
        return Err(ApiError::bad_request(format!("invalid {field}")));
    }
    values
        .iter()
        .map(|value| {
            let network = if allow_default_route {
                let (address, prefix) = value
                    .trim()
                    .split_once('/')
                    .ok_or_else(|| ApiError::bad_request(format!("invalid {field}")))?;
                let address = IpAddr::from_str(address.trim())
                    .map_err(|_| ApiError::bad_request(format!("invalid {field}")))?;
                if prefix.trim() == "0" && address.is_unspecified() {
                    IpNetwork {
                        addr: address,
                        prefix: 0,
                    }
                } else {
                    parse_network(value, field, false)?
                }
            } else {
                parse_network(value, field, false)?
            };
            Ok(if preserve_host {
                network.host_string()
            } else {
                network.to_string()
            })
        })
        .collect()
}

fn normalize_mode(raw: &str) -> Result<String, ApiError> {
    let value = clean_required(raw, "mode", 16)?.to_ascii_lowercase();
    if !EGRESS_MODES.contains(&value.as_str()) {
        return Err(ApiError::bad_request(
            "mode must be native, gateway, or cni",
        ));
    }
    Ok(value)
}

fn normalize_tunnel_type(raw: Option<String>) -> Result<String, ApiError> {
    let value = raw
        .filter(|v| !v.trim().is_empty())
        .unwrap_or_else(|| "wireguard".to_string())
        .trim()
        .to_ascii_lowercase();
    if !TUNNEL_TYPES.contains(&value.as_str()) {
        return Err(ApiError::bad_request(
            "tunnel_type must be wireguard, ipsec, or gateway",
        ));
    }
    Ok(value)
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct HostCapabilities {
    pub supported: bool,
    pub mode: String,
    pub running_as_root: bool,
    pub ip_available: bool,
    pub nft_available: bool,
    pub wireguard_available: bool,
    pub curl_available: bool,
    pub ipv4_forwarding: bool,
    pub ipv6_forwarding: bool,
    pub apply_enabled: bool,
    pub auto_install_enabled: bool,
    pub package_manager: Option<String>,
    pub missing_dependencies: Vec<String>,
    pub checked_at: i64,
    pub reasons: Vec<String>,
}

#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct WireGuardConfigRequest {
    pub managed: Option<bool>,
    pub private_key: Option<String>,
    pub peer_public_key: Option<String>,
    pub preshared_key: Option<String>,
    pub endpoint: Option<String>,
    pub addresses: Option<Vec<String>>,
    pub allowed_ips: Option<Vec<String>>,
    pub persistent_keepalive: Option<u16>,
    pub mtu: Option<u16>,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct WireGuardStatus {
    pub managed: bool,
    pub peer_public_key: Option<String>,
    pub endpoint: Option<String>,
    pub addresses: Vec<String>,
    pub allowed_ips: Vec<String>,
    pub persistent_keepalive: u16,
    pub mtu: u16,
    pub private_key_configured: bool,
    pub preshared_key_configured: bool,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct EgressProfile {
    pub id: String,
    pub mode: String,
    pub tunnel_type: String,
    pub tunnel_interface: String,
    pub gateway: Option<String>,
    pub route_table: u32,
    pub mark: u32,
    pub public_ipv4: Option<String>,
    pub public_ipv6: Option<String>,
    pub enabled: bool,
    pub fail_closed: bool,
    pub status: String,
    pub last_error: Option<String>,
    pub updated_at: i64,
    pub wireguard: Option<WireGuardStatus>,
    pub tunnel_ready: bool,
    pub last_handshake_at: Option<i64>,
}

#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct EgressProfileRequest {
    pub id: String,
    pub mode: String,
    pub tunnel_type: Option<String>,
    pub tunnel_interface: String,
    pub gateway: Option<String>,
    pub route_table: Option<u32>,
    pub mark: Option<u32>,
    pub public_ipv4: Option<String>,
    pub public_ipv6: Option<String>,
    pub enabled: Option<bool>,
    pub fail_closed: Option<bool>,
    pub wireguard: Option<WireGuardConfigRequest>,
}

#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct EgressProfileDeleteRequest {
    pub id: String,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ListProfilesResponse {
    pub profiles: Vec<EgressProfile>,
    pub total: usize,
}

#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct EgressBindingRequest {
    pub instance_id: String,
    pub profile_id: String,
    /// A single IPv4/IPv6 host or CIDR.  Newlines/diagnostic output are rejected.
    pub source: String,
    /// Optional complete source set for dual-stack instances.  `source` is
    /// retained as the backward-compatible primary address.
    pub sources: Option<Vec<String>>,
    pub interface: Option<String>,
    pub interface_v4: Option<String>,
    pub interface_v6: Option<String>,
    pub enabled: Option<bool>,
}

#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct EgressBindingDeleteRequest {
    pub instance_id: String,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct EgressBinding {
    pub instance_id: String,
    pub profile_id: String,
    pub source: String,
    pub sources: Vec<String>,
    pub interface: Option<String>,
    pub interface_v4: Option<String>,
    pub interface_v6: Option<String>,
    pub enabled: bool,
    pub state: String,
    pub last_error: Option<String>,
    pub fail_closed_enforced: Option<bool>,
    pub updated_at: i64,
    pub traffic_bytes_in: u64,
    pub traffic_bytes_out: u64,
    pub traffic_bytes_dropped: u64,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ListBindingsResponse {
    pub bindings: Vec<EgressBinding>,
    pub total: usize,
}

#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct ReplaceStateRequest {
    #[serde(default)]
    pub profiles: Vec<EgressProfileRequest>,
    #[serde(default)]
    pub bindings: Vec<EgressBindingRequest>,
    pub apply: Option<bool>,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ReplaceStateResponse {
    pub profile_count: usize,
    pub binding_count: usize,
    pub reconcile: ReconcileResponse,
}

#[derive(Debug)]
struct NormalizedReplaceState {
    profiles: Vec<(EgressProfile, Option<WireGuardConfig>)>,
    bindings: Vec<BindingRow>,
    apply: bool,
}

#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct ReconcileRequest {
    pub apply: Option<bool>,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct RoutePlan {
    pub instance_id: String,
    pub profile_id: String,
    pub status: String,
    pub commands: Vec<String>,
    pub error: Option<String>,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct ReconcileResponse {
    pub applied: bool,
    pub fail_closed: bool,
    pub capabilities: HostCapabilities,
    pub plans: Vec<RoutePlan>,
    pub errors: Vec<String>,
}

#[derive(Debug, Clone, Deserialize, ToSchema)]
pub struct DependencyEnsureRequest {
    pub package_set: Option<String>,
}

#[derive(Debug, Clone, Serialize, ToSchema)]
pub struct DependencyEnsureResponse {
    pub attempted: bool,
    pub installed: bool,
    pub package_set: String,
    pub package_manager: Option<String>,
    pub capabilities: HostCapabilities,
    pub message: String,
}

struct ProfileRow {
    profile: EgressProfile,
}

#[derive(Debug, Clone)]
struct BindingRow {
    binding: EgressBinding,
    networks: Vec<IpNetwork>,
}

#[derive(Debug, Clone)]
struct RuntimeProfile {
    profile_id: String,
    route_table: u32,
    mark: u32,
    tunnel_interface: String,
    has_v4: bool,
    has_v6: bool,
    managed_interface: bool,
    probe_sources: Vec<String>,
}

#[derive(Debug, Clone)]
struct WireGuardConfig {
    status: WireGuardStatus,
    private_key: Option<String>,
    preshared_key: Option<String>,
}

fn default_wg_status() -> WireGuardStatus {
    WireGuardStatus {
        managed: false,
        peer_public_key: None,
        endpoint: None,
        addresses: Vec::new(),
        allowed_ips: Vec::new(),
        persistent_keepalive: 25,
        mtu: 1420,
        private_key_configured: false,
        preshared_key_configured: false,
    }
}

fn wg_json(values: &[String]) -> String {
    serde_json::to_string(values).unwrap_or_else(|_| "[]".to_string())
}

fn wg_from_json(raw: String) -> Vec<String> {
    serde_json::from_str(&raw).unwrap_or_default()
}

fn add_column_if_missing(
    conn: &rusqlite::Connection,
    table: &str,
    column: &str,
    definition: &str,
) -> Result<(), ApiError> {
    let mut stmt = conn
        .prepare(&format!("PRAGMA table_info({table})"))
        .map_err(|e| ApiError::internal(format!("inspect {table} schema error: {e}")))?;
    let mut rows = stmt
        .query([])
        .map_err(|e| ApiError::internal(format!("read {table} schema error: {e}")))?;
    let mut found = false;
    while let Some(row) = rows
        .next()
        .map_err(|e| ApiError::internal(format!("read {table} schema row error: {e}")))?
    {
        let name: String = row.get(1).unwrap_or_default();
        if name == column {
            found = true;
            break;
        }
    }
    drop(rows);
    drop(stmt);
    if !found {
        conn.execute(
            &format!("ALTER TABLE {table} ADD COLUMN {column} {definition}"),
            [],
        )
        .map_err(|e| ApiError::internal(format!("migrate {table}.{column} error: {e}")))?;
    }
    Ok(())
}

/// Initialize all egress tables in one bounded schema batch.  Existing agent
/// installations are upgraded with a bounded set of `PRAGMA table_info`
/// checks, never one query per binding/profile.
pub fn init_db(conn: &rusqlite::Connection) -> Result<(), ApiError> {
    conn.execute_batch(
        r#"
        CREATE TABLE IF NOT EXISTS egress_profiles (
            id TEXT PRIMARY KEY NOT NULL,
            mode TEXT NOT NULL,
            tunnel_type TEXT NOT NULL DEFAULT 'wireguard',
            tunnel_interface TEXT NOT NULL,
            gateway TEXT NOT NULL DEFAULT '',
            route_table INTEGER NOT NULL DEFAULT 0,
            mark INTEGER NOT NULL DEFAULT 0,
            public_ipv4 TEXT NOT NULL DEFAULT '',
            public_ipv6 TEXT NOT NULL DEFAULT '',
            enabled INTEGER NOT NULL DEFAULT 0,
            fail_closed INTEGER NOT NULL DEFAULT 1,
            status TEXT NOT NULL DEFAULT 'pending',
            last_error TEXT NOT NULL DEFAULT '',
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL,
            wg_managed INTEGER NOT NULL DEFAULT 0,
            wg_peer_public_key TEXT NOT NULL DEFAULT '',
            wg_endpoint TEXT NOT NULL DEFAULT '',
            wg_addresses TEXT NOT NULL DEFAULT '[]',
            wg_allowed_ips TEXT NOT NULL DEFAULT '[]',
            wg_keepalive INTEGER NOT NULL DEFAULT 25,
            wg_mtu INTEGER NOT NULL DEFAULT 1420,
            wg_private_key_present INTEGER NOT NULL DEFAULT 0,
            wg_preshared_key_present INTEGER NOT NULL DEFAULT 0
        );
        CREATE TABLE IF NOT EXISTS egress_bindings (
            instance_id TEXT PRIMARY KEY NOT NULL,
            profile_id TEXT NOT NULL,
            source TEXT NOT NULL,
            interface TEXT NOT NULL DEFAULT '',
            interface_v4 TEXT NOT NULL DEFAULT '',
            interface_v6 TEXT NOT NULL DEFAULT '',
            enabled INTEGER NOT NULL DEFAULT 1,
            state TEXT NOT NULL DEFAULT 'pending',
            last_error TEXT NOT NULL DEFAULT '',
            created_at INTEGER NOT NULL,
            updated_at INTEGER NOT NULL,
            traffic_bytes_in INTEGER NOT NULL DEFAULT 0,
            traffic_bytes_out INTEGER NOT NULL DEFAULT 0,
			traffic_bytes_dropped INTEGER NOT NULL DEFAULT 0,
			sources_json TEXT NOT NULL DEFAULT '[]',
			fail_closed_enforced INTEGER DEFAULT NULL,
            FOREIGN KEY(profile_id) REFERENCES egress_profiles(id) ON DELETE CASCADE
        );
        CREATE INDEX IF NOT EXISTS idx_egress_bindings_profile ON egress_bindings(profile_id);
        CREATE TABLE IF NOT EXISTS egress_runtime_profiles (
            profile_id TEXT PRIMARY KEY NOT NULL,
            route_table INTEGER NOT NULL,
            mark INTEGER NOT NULL,
            tunnel_interface TEXT NOT NULL,
            has_v4 INTEGER NOT NULL DEFAULT 0,
            has_v6 INTEGER NOT NULL DEFAULT 0,
            managed_interface INTEGER NOT NULL DEFAULT 0,
            probe_sources_json TEXT NOT NULL DEFAULT '[]',
            updated_at INTEGER NOT NULL
        );
    "#,
    )
    .map_err(|e| ApiError::internal(format!("egress table init error: {e}")))?;

    // Migrate databases created by the previous egress skeleton.
    for (table, column, definition) in [
        (
            "egress_profiles",
            "wg_managed",
            "INTEGER NOT NULL DEFAULT 0",
        ),
        (
            "egress_profiles",
            "wg_peer_public_key",
            "TEXT NOT NULL DEFAULT ''",
        ),
        ("egress_profiles", "wg_endpoint", "TEXT NOT NULL DEFAULT ''"),
        (
            "egress_profiles",
            "wg_addresses",
            "TEXT NOT NULL DEFAULT '[]'",
        ),
        (
            "egress_profiles",
            "wg_allowed_ips",
            "TEXT NOT NULL DEFAULT '[]'",
        ),
        (
            "egress_profiles",
            "wg_keepalive",
            "INTEGER NOT NULL DEFAULT 25",
        ),
        ("egress_profiles", "wg_mtu", "INTEGER NOT NULL DEFAULT 1420"),
        (
            "egress_profiles",
            "wg_private_key_present",
            "INTEGER NOT NULL DEFAULT 0",
        ),
        (
            "egress_profiles",
            "wg_preshared_key_present",
            "INTEGER NOT NULL DEFAULT 0",
        ),
        (
            "egress_bindings",
            "traffic_bytes_in",
            "INTEGER NOT NULL DEFAULT 0",
        ),
        (
            "egress_bindings",
            "traffic_bytes_out",
            "INTEGER NOT NULL DEFAULT 0",
        ),
        (
            "egress_bindings",
            "traffic_bytes_dropped",
            "INTEGER NOT NULL DEFAULT 0",
        ),
        (
            "egress_bindings",
            "sources_json",
            "TEXT NOT NULL DEFAULT '[]'",
        ),
        (
            "egress_bindings",
            "interface_v4",
            "TEXT NOT NULL DEFAULT ''",
        ),
        (
            "egress_bindings",
            "interface_v6",
            "TEXT NOT NULL DEFAULT ''",
        ),
        ("egress_bindings", "fail_closed_enforced", "INTEGER"),
        (
            "egress_runtime_profiles",
            "probe_sources_json",
            "TEXT NOT NULL DEFAULT '[]'",
        ),
    ] {
        add_column_if_missing(conn, table, column, definition)?;
    }
    Ok(())
}
