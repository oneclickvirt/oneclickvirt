//! Host-native, per-instance transparent egress routing.
//!
//! The agent owns one nftables table and a reserved set of policy-routing
//! rules.  Desired state is kept in SQLite; applying it is an explicit,
//! fail-closed operation.  All host commands in this module are constructed
//! from typed/validated values and are executed directly through `Command` --
//! user supplied text is never passed to a shell.

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

#[derive(Debug, Clone)]
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

#[derive(Debug, Clone)]
struct CommandResult {
    success: bool,
    stdout: String,
    stderr: String,
}

trait CommandExecutor {
    fn run(
        &self,
        program: &str,
        args: &[String],
        input: Option<&str>,
    ) -> Result<CommandResult, String>;
}

struct SystemExecutor;

impl CommandExecutor for SystemExecutor {
    fn run(
        &self,
        program: &str,
        args: &[String],
        input: Option<&str>,
    ) -> Result<CommandResult, String> {
        let mut command = Command::new(program);
        command
            .args(args)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        if input.is_some() {
            command.stdin(Stdio::piped());
        }
        let mut child = command
            .spawn()
            .map_err(|e| format!("failed to start {program}: {e}"))?;
        if let (Some(data), Some(stdin)) = (input, child.stdin.as_mut()) {
            stdin
                .write_all(data.as_bytes())
                .map_err(|e| format!("failed to write {program} input: {e}"))?;
        }
        drop(child.stdin.take());
        let mut stdout = child
            .stdout
            .take()
            .ok_or_else(|| format!("failed capturing {program} stdout"))?;
        let mut stderr = child
            .stderr
            .take()
            .ok_or_else(|| format!("failed capturing {program} stderr"))?;
        let stdout_reader = std::thread::spawn(move || {
            let mut data = Vec::new();
            let _ = stdout.read_to_end(&mut data);
            data
        });
        let stderr_reader = std::thread::spawn(move || {
            let mut data = Vec::new();
            let _ = stderr.read_to_end(&mut data);
            data
        });
        let started = Instant::now();
        let status = loop {
            if let Some(status) = child
                .try_wait()
                .map_err(|e| format!("failed waiting for {program}: {e}"))?
            {
                break status;
            }
            if started.elapsed() >= COMMAND_TIMEOUT {
                let _ = child.kill();
                let _ = child.wait();
                let _ = stdout_reader.join();
                let _ = stderr_reader.join();
                return Err(format!("{program} timed out"));
            }
            std::thread::sleep(Duration::from_millis(20));
        };
        let stdout = stdout_reader
            .join()
            .map_err(|_| format!("failed reading {program} stdout"))?;
        let stderr = stderr_reader
            .join()
            .map_err(|_| format!("failed reading {program} stderr"))?;
        Ok(CommandResult {
            success: status.success(),
            stdout: String::from_utf8_lossy(&stdout).to_string(),
            stderr: String::from_utf8_lossy(&stderr).to_string(),
        })
    }
}

fn command_path(command: &str) -> Option<PathBuf> {
    let path = Path::new(command);
    if path.components().count() > 1 {
        return path.is_file().then(|| path.to_path_buf());
    }
    env::var_os("PATH").and_then(|paths| {
        env::split_paths(&paths)
            .map(|path| path.join(command))
            .find(|path| path.is_file())
    })
}

fn command_available(command: &str) -> bool {
    command_path(command).is_some()
}

fn detect_package_manager() -> Option<String> {
    ["apt-get", "dnf", "yum", "apk", "pacman", "zypper"]
        .into_iter()
        .find(|name| command_available(name))
        .map(str::to_string)
}

fn sysctl_bool(path: &str) -> bool {
    fs::read_to_string(path)
        .ok()
        .map(|value| value.trim() == "1")
        .unwrap_or(false)
}

fn write_proc_flag(path: &Path, value: &str) -> Result<(), String> {
    if !path.exists() {
        return Ok(());
    }
    fs::write(path, value).map_err(|e| format!("write {}: {e}", path.display()))
}

fn ensure_kernel_prerequisites(profiles: &[ProfileRow], bindings: &[BindingRow]) -> Vec<String> {
    if unsafe { libc::geteuid() } != 0 {
        return vec!["kernel forwarding setup requires root".to_string()];
    }
    let mut errors = Vec::new();
    let has_v4 = bindings
        .iter()
        .filter(|row| row.binding.enabled)
        .flat_map(|row| &row.networks)
        .any(|network| network.family() == Family::V4);
    let has_v6 = bindings
        .iter()
        .filter(|row| row.binding.enabled)
        .flat_map(|row| &row.networks)
        .any(|network| network.family() == Family::V6);
    if has_v4 {
        for (path, value) in [
            (Path::new("/proc/sys/net/ipv4/ip_forward"), "1"),
            (Path::new("/proc/sys/net/ipv4/conf/all/src_valid_mark"), "1"),
        ] {
            if let Err(error) = write_proc_flag(path, value) {
                errors.push(error);
            }
        }
    }
    if has_v6 {
        if let Err(error) =
            write_proc_flag(Path::new("/proc/sys/net/ipv6/conf/all/forwarding"), "1")
        {
            errors.push(error);
        }
    }
    let profile_map: HashMap<&str, &EgressProfile> = profiles
        .iter()
        .map(|row| (row.profile.id.as_str(), &row.profile))
        .collect();
    let mut interfaces = HashSet::new();
    for binding in bindings.iter().filter(|row| row.binding.enabled) {
        for interface in [
            binding.binding.interface.as_deref(),
            binding.binding.interface_v4.as_deref(),
            binding.binding.interface_v6.as_deref(),
        ]
        .into_iter()
        .flatten()
        {
            interfaces.insert(interface.to_string());
        }
        if let Some(profile) = profile_map.get(binding.binding.profile_id.as_str()) {
            interfaces.insert(profile.tunnel_interface.clone());
        }
    }
    for interface in interfaces {
        // Both interface sources were validated against Linux IFNAMSIZ and a
        // strict character allow-list before reaching this path.
        let path = Path::new("/proc/sys/net/ipv4/conf")
            .join(interface)
            .join("rp_filter");
        if let Err(error) = write_proc_flag(&path, "0") {
            errors.push(error);
        }
    }
    errors
}

pub fn detect_capabilities() -> HostCapabilities {
    let running_as_root = unsafe { libc::geteuid() == 0 };
    let ip_available = command_available("ip");
    let nft_available = command_available("nft");
    let wireguard_available = command_available("wg");
    let curl_available = command_available("curl");
    let ipv4_forwarding = sysctl_bool("/proc/sys/net/ipv4/ip_forward");
    let ipv6_forwarding = sysctl_bool("/proc/sys/net/ipv6/conf/all/forwarding");
    let apply_enabled = env_enabled(APPLY_ENV);
    let auto_install_enabled = env_enabled(AUTO_INSTALL_ENV);
    let package_manager = detect_package_manager();
    let mut missing_dependencies = Vec::new();
    if !ip_available {
        missing_dependencies.push("iproute2".to_string());
    }
    if !nft_available {
        missing_dependencies.push("nftables".to_string());
    }
    if !wireguard_available {
        missing_dependencies.push("wireguard-tools".to_string());
    }
    if !curl_available {
        missing_dependencies.push("curl".to_string());
    }
    let mut reasons = Vec::new();
    if !running_as_root {
        reasons.push("agent must run as root to reconcile host networking".to_string());
    }
    if !ip_available {
        reasons.push("ip command is unavailable".to_string());
    }
    if !nft_available {
        reasons.push("nft command is unavailable".to_string());
    }
    if !curl_available {
        reasons.push("curl is unavailable for strict public egress verification".to_string());
    }
    if !ipv4_forwarding && !ipv6_forwarding {
        reasons.push("neither IPv4 nor IPv6 forwarding is enabled".to_string());
    }
    if !apply_enabled {
        reasons.push(format!("apply guard {APPLY_ENV}=true is not enabled"));
    }
    HostCapabilities {
        supported: running_as_root
            && ip_available
            && nft_available
            && curl_available
            && (ipv4_forwarding || ipv6_forwarding),
        mode: "native".to_string(),
        running_as_root,
        ip_available,
        nft_available,
        wireguard_available,
        curl_available,
        ipv4_forwarding,
        ipv6_forwarding,
        apply_enabled,
        auto_install_enabled,
        package_manager,
        missing_dependencies,
        checked_at: now_ts(),
        reasons,
    }
}

fn run_quiet_with_timeout(program: &str, args: &[&str], timeout: Duration) -> Result<(), ApiError> {
    let mut child = Command::new(program)
        .args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|e| ApiError::internal(format!("failed to start dependency installer: {e}")))?;
    let started = Instant::now();
    loop {
        if let Some(status) = child
            .try_wait()
            .map_err(|e| ApiError::internal(format!("dependency installer wait error: {e}")))?
        {
            return if status.success() {
                Ok(())
            } else {
                Err(ApiError::internal("dependency installation failed"))
            };
        }
        if started.elapsed() >= timeout {
            let _ = child.kill();
            let _ = child.wait();
            return Err(ApiError::internal("dependency installation timed out"));
        }
        std::thread::sleep(Duration::from_millis(100));
    }
}

fn dependency_command_plan(
    manager: &str,
    include_wireguard: bool,
) -> Result<Vec<Vec<String>>, ApiError> {
    let mut packages: Vec<&str> = match manager {
        "apt-get" | "apk" | "pacman" | "zypper" => vec!["iproute2", "nftables", "curl"],
        "dnf" | "yum" => vec!["iproute", "nftables", "curl"],
        _ => return Err(ApiError::bad_request("no supported package manager found")),
    };
    if include_wireguard {
        packages.push("wireguard-tools");
    }
    let mut plan = Vec::new();
    if manager == "apt-get" {
        plan.push(vec!["update".to_string(), "-qq".to_string()]);
    }
    let mut args: Vec<String> = match manager {
        "apt-get" => vec![
            "install".into(),
            "-y".into(),
            "--no-install-recommends".into(),
        ],
        "dnf" | "yum" => vec!["install".into(), "-y".into()],
        "apk" => vec!["add".into(), "--no-cache".into()],
        "pacman" => vec!["-Sy".into(), "--noconfirm".into()],
        "zypper" => vec![
            "--non-interactive".into(),
            "install".into(),
            "--no-recommends".into(),
        ],
        _ => unreachable!(),
    };
    args.extend(packages.into_iter().map(str::to_string));
    plan.push(args);
    Ok(plan)
}

fn install_dependency_set(package_set: &str) -> Result<String, ApiError> {
    if !env_enabled(AUTO_INSTALL_ENV) {
        return Err(ApiError::bad_request(format!(
            "{AUTO_INSTALL_ENV}=true is required"
        )));
    }
    if unsafe { libc::geteuid() } != 0 {
        return Err(ApiError::bad_request(
            "dependency installation requires root",
        ));
    }
    let manager = detect_package_manager()
        .ok_or_else(|| ApiError::bad_request("no supported package manager found"))?;
    let include_wireguard = match package_set {
        "native" => false,
        "wireguard" => true,
        _ => {
            return Err(ApiError::bad_request(
                "package_set must be native or wireguard",
            ));
        }
    };
    for args in dependency_command_plan(&manager, include_wireguard)? {
        let refs: Vec<&str> = args.iter().map(String::as_str).collect();
        run_quiet_with_timeout(&manager, &refs, INSTALL_TIMEOUT)?;
    }
    Ok(manager)
}

pub async fn capabilities(
    State(_state): State<AppState>,
) -> Result<Json<HostCapabilities>, ApiError> {
    Ok(Json(detect_capabilities()))
}

pub async fn ensure_dependencies(
    State(_state): State<AppState>,
    Json(req): Json<DependencyEnsureRequest>,
) -> Result<Json<DependencyEnsureResponse>, ApiError> {
    let package_set = req
        .package_set
        .unwrap_or_else(|| "wireguard".to_string())
        .trim()
        .to_ascii_lowercase();
    if !matches!(package_set.as_str(), "native" | "wireguard") {
        return Err(ApiError::bad_request(
            "package_set must be native or wireguard",
        ));
    }
    let requested = package_set.clone();
    let package_manager = tokio::task::spawn_blocking(move || install_dependency_set(&requested))
        .await
        .map_err(|e| ApiError::internal(format!("dependency installer task failed: {e}")))??;
    let capabilities = detect_capabilities();
    let installed = capabilities.ip_available
        && capabilities.nft_available
        && capabilities.curl_available
        && (package_set == "native" || capabilities.wireguard_available);
    let message = if installed {
        "dependencies are available"
    } else {
        "installer completed but required commands are still unavailable"
    };
    Ok(Json(DependencyEnsureResponse {
        attempted: true,
        installed,
        package_set,
        package_manager: Some(package_manager),
        capabilities,
        message: message.to_string(),
    }))
}

fn state_directory() -> Result<PathBuf, ApiError> {
    let path = env::var_os("ONECLICKVIRT_EGRESS_STATE_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("/var/lib/oneclickvirt/egress"));
    if !path.is_absolute() {
        return Err(ApiError::bad_request(
            "ONECLICKVIRT_EGRESS_STATE_DIR must be absolute",
        ));
    }
    Ok(path)
}

fn managed_sources_path() -> Result<PathBuf, ApiError> {
    Ok(state_directory()?.join("managed-sources"))
}

/// Persist the source identities that the boot quarantine helper must protect
/// before the Agent has restored its SQLite state. The same guard table is also
/// used as a staging barrier between binding PUT and full reconciliation. The
/// file is deliberately data-only (one canonical CIDR per line) and committed
/// with rename(2).
fn write_managed_sources(conn: &rusqlite::Connection) -> Result<(), ApiError> {
    let path = managed_sources_path()?;
    write_managed_sources_at_with_extra(conn, &path, &[])
}

#[cfg(test)]
fn write_managed_sources_at(conn: &rusqlite::Connection, path: &Path) -> Result<(), ApiError> {
    write_managed_sources_at_with_extra(conn, path, &[])
}

fn write_managed_sources_with_extra(
    conn: &rusqlite::Connection,
    extra: &[IpNetwork],
) -> Result<(), ApiError> {
    let path = managed_sources_path()?;
    write_managed_sources_at_with_extra(conn, &path, extra)
}

fn write_managed_sources_at_with_extra(
    conn: &rusqlite::Connection,
    path: &Path,
    extra: &[IpNetwork],
) -> Result<(), ApiError> {
    let mut networks = load_enabled_binding_networks(conn)?;
    networks.extend_from_slice(extra);
    write_managed_networks_at(path, &networks)
}

fn load_enabled_binding_networks(conn: &rusqlite::Connection) -> Result<Vec<IpNetwork>, ApiError> {
    let mut networks = Vec::new();
    let mut stmt = conn
        .prepare("SELECT sources_json FROM egress_bindings WHERE enabled=1")
        .map_err(|e| ApiError::internal(format!("prepare managed egress sources error: {e}")))?;
    let rows = stmt
        .query_map([], |row| row.get::<_, String>(0))
        .map_err(|e| ApiError::internal(format!("query managed egress sources error: {e}")))?;
    for row in rows {
        let encoded =
            row.map_err(|e| ApiError::internal(format!("read managed egress sources error: {e}")))?;
        let values: Vec<String> = serde_json::from_str(&encoded)
            .map_err(|e| ApiError::internal(format!("decode managed egress sources error: {e}")))?;
        for value in values {
            let network = parse_network(&value, "managed source", true)
                .map_err(|error| ApiError::internal(error.message))?;
            networks.push(network);
        }
    }
    Ok(networks)
}

fn write_managed_networks_at(path: &Path, networks: &[IpNetwork]) -> Result<(), ApiError> {
    let sources: BTreeSet<String> = networks.iter().map(ToString::to_string).collect();
    let directory = path
        .parent()
        .ok_or_else(|| ApiError::internal("invalid managed egress source path"))?;
    fs::create_dir_all(directory).map_err(|e| {
        ApiError::internal(format!("create managed egress state directory error: {e}"))
    })?;
    fs::set_permissions(directory, fs::Permissions::from_mode(0o700)).map_err(|e| {
        ApiError::internal(format!("secure managed egress state directory error: {e}"))
    })?;
    let temporary = directory.join(format!(
        ".managed-sources-{}-{}",
        std::process::id(),
        now_ts()
    ));
    let mut file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(&temporary)
        .map_err(|e| ApiError::internal(format!("create managed egress source file error: {e}")))?;
    let content = if sources.is_empty() {
        String::new()
    } else {
        let mut value = sources.into_iter().collect::<Vec<_>>().join("\n");
        value.push('\n');
        value
    };
    file.write_all(content.as_bytes())
        .and_then(|_| file.sync_all())
        .map_err(|e| ApiError::internal(format!("write managed egress source file error: {e}")))?;
    fs::rename(&temporary, &path)
        .map_err(|e| ApiError::internal(format!("commit managed egress source file error: {e}")))?;
    fs::set_permissions(&path, fs::Permissions::from_mode(0o600))
        .map_err(|e| ApiError::internal(format!("secure managed egress source file error: {e}")))?;
    Ok(())
}

fn clear_boot_quarantine(executor: &impl CommandExecutor) -> Result<(), String> {
    let args = vec![
        "delete".to_string(),
        "table".to_string(),
        TABLE_FAMILY.to_string(),
        BOOT_TABLE_NAME.to_string(),
    ];
    let result = executor.run("nft", &args, None)?;
    if result.success
        || result.stderr.contains("No such file")
        || result.stderr.contains("does not exist")
    {
        Ok(())
    } else {
        Err(concise_error(&result, "remove boot egress quarantine"))
    }
}

fn nft_table_named_exists(
    executor: &impl CommandExecutor,
    table_name: &str,
) -> Result<bool, String> {
    let args = vec![
        "list".to_string(),
        "table".to_string(),
        TABLE_FAMILY.to_string(),
        table_name.to_string(),
    ];
    let result = executor.run("nft", &args, None)?;
    Ok(result.success)
}

/// Build an additive transaction for the early-boot table. Existing rules are
/// deliberately retained: they may protect a binding whose PUT completed but
/// whose reconcile request was interrupted. Full reconciliation deletes the
/// table only after the primary data plane has been installed successfully.
fn build_staging_quarantine_script(networks: &[IpNetwork], table_exists: bool) -> String {
    let mut script = String::new();
    if !table_exists {
        script.push_str(&format!("add table {TABLE_FAMILY} {BOOT_TABLE_NAME}\n"));
        script.push_str(&format!("add chain {TABLE_FAMILY} {BOOT_TABLE_NAME} boot_forward {{ type filter hook forward priority -200; policy accept; }}\n"));
        script.push_str(&format!("add chain {TABLE_FAMILY} {BOOT_TABLE_NAME} boot_output {{ type filter hook output priority -200; policy accept; }}\n"));
        script.push_str(&format!("add chain {TABLE_FAMILY} {BOOT_TABLE_NAME} boot_input {{ type filter hook input priority -200; policy accept; }}\n"));
    }
    let mut values: Vec<(String, String)> = networks
        .iter()
        .map(|network| {
            (
                network.family().nft_prefix().to_string(),
                network.to_string(),
            )
        })
        .collect();
    values.sort();
    values.dedup();
    for (family, source) in values {
        for chain in ["boot_forward", "boot_output", "boot_input"] {
            script.push_str(&format!(
                "add rule {TABLE_FAMILY} {BOOT_TABLE_NAME} {chain} {family} saddr {source} counter drop\n"
            ));
        }
    }
    script
}

fn install_staging_quarantine(
    executor: &impl CommandExecutor,
    networks: &[IpNetwork],
) -> Result<(), String> {
    if networks.is_empty() {
        return Ok(());
    }
    let table_exists = nft_table_named_exists(executor, BOOT_TABLE_NAME)?;
    let script = build_staging_quarantine_script(networks, table_exists);
    apply_nft_script(executor, &script)
        .map_err(|error| format!("install binding fail-closed quarantine: {error}"))
}

fn secret_path(profile_id: &str, preshared: bool) -> Result<PathBuf, ApiError> {
    let suffix = if preshared { "psk" } else { "key" };
    Ok(state_directory()?.join(format!("wg-{profile_id}.{suffix}")))
}

fn atomic_write_secret(path: &Path, value: &str) -> Result<(), ApiError> {
    let directory = path
        .parent()
        .ok_or_else(|| ApiError::internal("invalid egress state directory"))?;
    fs::create_dir_all(directory)
        .map_err(|e| ApiError::internal(format!("create egress state directory error: {e}")))?;
    fs::set_permissions(directory, fs::Permissions::from_mode(0o700))
        .map_err(|e| ApiError::internal(format!("secure egress state directory error: {e}")))?;
    let temporary = directory.join(format!(".secret-{}-{}", std::process::id(), now_ts()));
    let mut file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(&temporary)
        .map_err(|e| ApiError::internal(format!("create egress secret error: {e}")))?;
    file.write_all(value.as_bytes())
        .and_then(|_| file.write_all(b"\n"))
        .and_then(|_| file.sync_all())
        .map_err(|e| ApiError::internal(format!("write egress secret error: {e}")))?;
    fs::rename(&temporary, path)
        .map_err(|e| ApiError::internal(format!("commit egress secret error: {e}")))?;
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))
        .map_err(|e| ApiError::internal(format!("secure egress secret error: {e}")))?;
    Ok(())
}

fn remove_profile_secrets(profile_id: &str) {
    for preshared in [false, true] {
        if let Ok(path) = secret_path(profile_id, preshared) {
            if let Err(error) = fs::remove_file(&path) {
                if error.kind() != std::io::ErrorKind::NotFound {
                    warn!(profile_id, path = %path.display(), error = %error, "failed removing egress secret");
                }
            }
        }
    }
}

#[derive(Debug)]
struct SecretUpdate {
    path: PathBuf,
    value: String,
}

#[derive(Debug)]
struct SecretBackup {
    path: PathBuf,
    value: Option<String>,
}

fn rollback_secret_updates(backups: &[SecretBackup]) {
    for backup in backups.iter().rev() {
        let result = match backup.value.as_deref() {
            Some(value) => atomic_write_secret(&backup.path, value.trim()),
            None => match fs::remove_file(&backup.path) {
                Ok(()) => Ok(()),
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
                Err(error) => Err(ApiError::internal(format!(
                    "remove staged egress secret error: {error}"
                ))),
            },
        };
        if let Err(error) = result {
            warn!(path = %backup.path.display(), error = %error.message, "failed rolling back staged egress secret");
        }
    }
}

fn apply_secret_updates(updates: &[SecretUpdate]) -> Result<Vec<SecretBackup>, ApiError> {
    let mut backups = Vec::with_capacity(updates.len());
    for update in updates {
        let previous = match fs::read_to_string(&update.path) {
            Ok(value) => Some(value),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
            Err(error) => {
                rollback_secret_updates(&backups);
                return Err(ApiError::internal(format!(
                    "read existing egress secret error: {error}"
                )));
            }
        };
        if let Err(error) = atomic_write_secret(&update.path, &update.value) {
            rollback_secret_updates(&backups);
            return Err(error);
        }
        backups.push(SecretBackup {
            path: update.path.clone(),
            value: previous,
        });
    }
    Ok(backups)
}

fn prepare_profile_storage(
    profile: &mut EgressProfile,
    requested: Option<&WireGuardConfig>,
    existing: Option<&EgressProfile>,
) -> Result<(Vec<SecretUpdate>, bool), ApiError> {
    let mut updates = Vec::new();
    let mut remove_secrets = profile.tunnel_type != "wireguard";
    if requested.is_none() && profile.tunnel_type == "wireguard" {
        profile.wireguard = existing.and_then(|value| value.wireguard.clone());
        return Ok((updates, remove_secrets));
    }
    if let Some(config) = requested {
        let mut status = config.status.clone();
        let existing_status = existing.and_then(|value| value.wireguard.as_ref());
        if let Some(private_key) = config.private_key.as_ref() {
            updates.push(SecretUpdate {
                path: secret_path(&profile.id, false)?,
                value: private_key.clone(),
            });
            status.private_key_configured = true;
        } else if status.managed {
            status.private_key_configured = existing_status
                .is_some_and(|value| value.private_key_configured)
                || secret_path(&profile.id, false)?.is_file();
        }
        if let Some(preshared_key) = config.preshared_key.as_ref() {
            updates.push(SecretUpdate {
                path: secret_path(&profile.id, true)?,
                value: preshared_key.clone(),
            });
            status.preshared_key_configured = true;
        } else if status.managed {
            status.preshared_key_configured = existing_status
                .is_some_and(|value| value.preshared_key_configured)
                || secret_path(&profile.id, true)?.is_file();
        }
        if !status.managed {
            status.private_key_configured = false;
            status.preshared_key_configured = false;
            remove_secrets = true;
        }
        profile.wireguard = Some(status);
    }
    Ok((updates, remove_secrets))
}

fn validate_host_ip(raw: Option<String>, field: &str) -> Result<Option<String>, ApiError> {
    raw.filter(|value| !value.trim().is_empty())
        .map(|value| {
            let network = parse_network(&value, field, true)?;
            if network.prefix != network.family().max_prefix() {
                return Err(ApiError::bad_request(format!(
                    "{field} must be a single address"
                )));
            }
            Ok(network.addr.to_string())
        })
        .transpose()
}

fn wireguard_from_request(req: WireGuardConfigRequest) -> Result<WireGuardConfig, ApiError> {
    let managed = req.managed.unwrap_or(true);
    let private_key = req
        .private_key
        .map(|value| validate_key(&value, "wireguard private key"))
        .transpose()?;
    let preshared_key = req
        .preshared_key
        .map(|value| validate_key(&value, "wireguard preshared key"))
        .transpose()?;
    let peer_public_key = req
        .peer_public_key
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_key(&value, "wireguard peer public key"))
        .transpose()?;
    let endpoint = req
        .endpoint
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_endpoint(&value))
        .transpose()?;
    let addresses = if managed {
        validate_vec_networks(req.addresses, "wireguard address", &[], true, false)?
    } else {
        req.addresses.unwrap_or_default()
    };
    let allowed_ips = if managed {
        validate_vec_networks(
            req.allowed_ips,
            "wireguard allowed IP",
            &["0.0.0.0/0", "::/0"],
            false,
            true,
        )?
    } else {
        req.allowed_ips.unwrap_or_default()
    };
    let persistent_keepalive = req.persistent_keepalive.unwrap_or(25);
    let mtu = req.mtu.unwrap_or(1420);
    if managed && peer_public_key.is_none() {
        return Err(ApiError::bad_request(
            "managed WireGuard requires peer_public_key",
        ));
    }
    if !(576..=9000).contains(&mtu) {
        return Err(ApiError::bad_request(
            "wireguard mtu must be between 576 and 9000",
        ));
    }
    Ok(WireGuardConfig {
        status: WireGuardStatus {
            managed,
            peer_public_key,
            endpoint,
            addresses,
            allowed_ips,
            persistent_keepalive,
            mtu,
            private_key_configured: private_key.is_some(),
            preshared_key_configured: preshared_key.is_some(),
        },
        private_key,
        preshared_key,
    })
}

fn profile_from_request(
    req: EgressProfileRequest,
) -> Result<(EgressProfile, Option<WireGuardConfig>), ApiError> {
    let id = validate_id(&req.id, "id")?;
    let mode = normalize_mode(&req.mode)?;
    let tunnel_type = normalize_tunnel_type(req.tunnel_type)?;
    let tunnel_interface = validate_interface(&req.tunnel_interface, "tunnel_interface")?;
    let gateway = validate_host_ip(req.gateway, "gateway")?;
    let public_ipv4 = validate_host_ip(req.public_ipv4, "public_ipv4")?;
    let public_ipv6 = validate_host_ip(req.public_ipv6, "public_ipv6")?;
    if public_ipv4
        .as_deref()
        .and_then(|value| value.parse::<IpAddr>().ok())
        .is_some_and(|ip| ip.is_ipv6())
    {
        return Err(ApiError::bad_request("public_ipv4 must be IPv4"));
    }
    if public_ipv6
        .as_deref()
        .and_then(|value| value.parse::<IpAddr>().ok())
        .is_some_and(|ip| ip.is_ipv4())
    {
        return Err(ApiError::bad_request("public_ipv6 must be IPv6"));
    }
    let route_table = req.route_table.unwrap_or(0);
    let mark = req.mark.unwrap_or(0);
    let enabled = req.enabled.unwrap_or(false);
    let fail_closed = req.fail_closed.unwrap_or(true);
    if enabled
        && (route_table == 0 || route_table > MAX_ROUTE_TABLE || (253..=255).contains(&route_table))
    {
        return Err(ApiError::bad_request(format!(
            "route_table must be 1..={MAX_ROUTE_TABLE} and not 253..=255"
        )));
    }
    if enabled && (mark == 0 || mark > MAX_MARK) {
        return Err(ApiError::bad_request(format!(
            "mark must be 1..={MAX_MARK}"
        )));
    }
    if enabled && !fail_closed {
        return Err(ApiError::bad_request("fail_closed must remain enabled"));
    }
    let wireguard = req.wireguard.map(wireguard_from_request).transpose()?;
    if wireguard.is_some() && tunnel_type != "wireguard" {
        return Err(ApiError::bad_request(
            "wireguard config requires tunnel_type=wireguard",
        ));
    }
    Ok((
        EgressProfile {
            id,
            mode,
            tunnel_type,
            tunnel_interface,
            gateway,
            route_table,
            mark,
            public_ipv4,
            public_ipv6,
            enabled,
            fail_closed,
            status: "pending".to_string(),
            last_error: None,
            updated_at: now_ts(),
            wireguard: wireguard.as_ref().map(|config| config.status.clone()),
            tunnel_ready: false,
            last_handshake_at: None,
        },
        wireguard,
    ))
}

fn binding_from_request(req: EgressBindingRequest) -> Result<BindingRow, ApiError> {
    let primary = parse_network(&req.source, "source", true)?;
    let mut networks = Vec::new();
    for value in req
        .sources
        .unwrap_or_default()
        .into_iter()
        .chain(std::iter::once(req.source.clone()))
    {
        let network = parse_network(&value, "source", true)?;
        if !networks
            .iter()
            .any(|existing: &IpNetwork| existing == &network)
        {
            networks.push(network);
        }
    }
    if networks.len() > 64 {
        return Err(ApiError::bad_request(
            "at most 64 binding sources are allowed",
        ));
    }
    for left in 0..networks.len() {
        for right in (left + 1)..networks.len() {
            if networks[left].family() == networks[right].family()
                && networks[left].canonical_bits() <= networks[right].end_bits()
                && networks[right].canonical_bits() <= networks[left].end_bits()
            {
                return Err(ApiError::bad_request("binding sources must not overlap"));
            }
        }
    }
    let sources: Vec<String> = networks.iter().map(ToString::to_string).collect();
    let interface = req
        .interface
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_interface(&value, "interface"))
        .transpose()?;
    let interface_v4 = req
        .interface_v4
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_interface(&value, "interface_v4"))
        .transpose()?
        .or_else(|| interface.clone());
    let interface_v6 = req
        .interface_v6
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_interface(&value, "interface_v6"))
        .transpose()?
        .or_else(|| interface.clone());
    let enabled = req.enabled.unwrap_or(true);
    let needs_v4 = networks
        .iter()
        .any(|network| network.family() == Family::V4);
    let needs_v6 = networks
        .iter()
        .any(|network| network.family() == Family::V6);
    if enabled && ((needs_v4 && interface_v4.is_none()) || (needs_v6 && interface_v6.is_none())) {
        return Err(ApiError::bad_request(
            "enabled native binding requires a host ingress interface for every source family",
        ));
    }
    Ok(BindingRow {
        binding: EgressBinding {
            instance_id: validate_id(&req.instance_id, "instance_id")?,
            profile_id: validate_id(&req.profile_id, "profile_id")?,
            source: primary.to_string(),
            sources,
            interface,
            interface_v4,
            interface_v6,
            enabled,
            state: "pending".to_string(),
            last_error: None,
            fail_closed_enforced: None,
            updated_at: now_ts(),
            traffic_bytes_in: 0,
            traffic_bytes_out: 0,
            traffic_bytes_dropped: 0,
        },
        networks,
    })
}

fn normalize_replace_state_request(
    req: ReplaceStateRequest,
) -> Result<NormalizedReplaceState, ApiError> {
    if req.profiles.len() > MAX_STATE_PROFILES || req.bindings.len() > MAX_STATE_BINDINGS {
        return Err(ApiError::bad_request("egress state batch is too large"));
    }
    let mut profile_ids = HashSet::with_capacity(req.profiles.len());
    let mut profiles = Vec::with_capacity(req.profiles.len());
    for request in req.profiles {
        let (profile, wireguard) = profile_from_request(request)?;
        if !profile_ids.insert(profile.id.clone()) {
            return Err(ApiError::bad_request(format!(
                "duplicate egress profile id {}",
                profile.id
            )));
        }
        profiles.push((profile, wireguard));
    }
    let mut binding_ids = HashSet::with_capacity(req.bindings.len());
    let mut bindings = Vec::with_capacity(req.bindings.len());
    for request in req.bindings {
        let binding = binding_from_request(request)?;
        if !binding_ids.insert(binding.binding.instance_id.clone()) {
            return Err(ApiError::bad_request(format!(
                "duplicate egress binding instance_id {}",
                binding.binding.instance_id
            )));
        }
        if !profile_ids.contains(&binding.binding.profile_id) {
            return Err(ApiError::bad_request(format!(
                "egress binding {} references a profile outside this state batch",
                binding.binding.instance_id
            )));
        }
        bindings.push(binding);
    }
    Ok(NormalizedReplaceState {
        profiles,
        bindings,
        apply: req.apply.unwrap_or(false),
    })
}

fn optional_string(value: String) -> Option<String> {
    (!value.is_empty()).then_some(value)
}

const PROFILE_SELECT: &str = "id, mode, tunnel_type, tunnel_interface, gateway, route_table, mark, public_ipv4, public_ipv6, enabled, fail_closed, status, last_error, updated_at, wg_managed, wg_peer_public_key, wg_endpoint, wg_addresses, wg_allowed_ips, wg_keepalive, wg_mtu, wg_private_key_present, wg_preshared_key_present";
const BINDING_SELECT: &str = "instance_id, profile_id, source, interface, interface_v4, interface_v6, enabled, state, last_error, updated_at, traffic_bytes_in, traffic_bytes_out, traffic_bytes_dropped, sources_json, fail_closed_enforced";

fn read_profile(row: &rusqlite::Row<'_>) -> rusqlite::Result<EgressProfile> {
    let tunnel_type: String = row.get(2)?;
    let managed = row.get::<_, i64>(14)? != 0;
    let peer_public_key = optional_string(row.get(15)?);
    let endpoint = optional_string(row.get(16)?);
    let addresses = wg_from_json(row.get(17)?);
    let allowed_ips = wg_from_json(row.get(18)?);
    let private_key_configured = row.get::<_, i64>(21)? != 0;
    let preshared_key_configured = row.get::<_, i64>(22)? != 0;
    let has_wg_config =
        managed || peer_public_key.is_some() || !addresses.is_empty() || private_key_configured;
    Ok(EgressProfile {
        id: row.get(0)?,
        mode: row.get(1)?,
        tunnel_type: tunnel_type.clone(),
        tunnel_interface: row.get(3)?,
        gateway: optional_string(row.get(4)?),
        route_table: row.get(5)?,
        mark: row.get(6)?,
        public_ipv4: optional_string(row.get(7)?),
        public_ipv6: optional_string(row.get(8)?),
        enabled: row.get::<_, i64>(9)? != 0,
        fail_closed: row.get::<_, i64>(10)? != 0,
        status: row.get(11)?,
        last_error: optional_string(row.get(12)?),
        updated_at: row.get(13)?,
        wireguard: (tunnel_type == "wireguard" && has_wg_config).then_some(WireGuardStatus {
            managed,
            peer_public_key,
            endpoint,
            addresses,
            allowed_ips,
            persistent_keepalive: row.get(19)?,
            mtu: row.get(20)?,
            private_key_configured,
            preshared_key_configured,
        }),
        tunnel_ready: false,
        last_handshake_at: None,
    })
}

fn read_binding(row: &rusqlite::Row<'_>) -> rusqlite::Result<EgressBinding> {
    let source: String = row.get(2)?;
    let mut sources = wg_from_json(row.get(13)?);
    if sources.is_empty() {
        sources.push(source.clone());
    }
    let interface = optional_string(row.get(3)?);
    let interface_v4 = optional_string(row.get(4)?).or_else(|| interface.clone());
    let interface_v6 = optional_string(row.get(5)?).or_else(|| interface.clone());
    Ok(EgressBinding {
        instance_id: row.get(0)?,
        profile_id: row.get(1)?,
        source,
        sources,
        interface,
        interface_v4,
        interface_v6,
        enabled: row.get::<_, i64>(6)? != 0,
        state: row.get(7)?,
        last_error: optional_string(row.get(8)?),
        fail_closed_enforced: row.get::<_, Option<i64>>(14)?.map(|value| value != 0),
        updated_at: row.get(9)?,
        traffic_bytes_in: row.get::<_, i64>(10)?.max(0) as u64,
        traffic_bytes_out: row.get::<_, i64>(11)?.max(0) as u64,
        traffic_bytes_dropped: row.get::<_, i64>(12)?.max(0) as u64,
    })
}

fn load_desired(
    conn: &rusqlite::Connection,
) -> Result<(Vec<ProfileRow>, Vec<BindingRow>), ApiError> {
    let mut profile_stmt = conn
        .prepare(&format!("SELECT {PROFILE_SELECT} FROM egress_profiles"))
        .map_err(|e| ApiError::internal(format!("prepare egress profiles error: {e}")))?;
    let profile_rows = profile_stmt
        .query_map([], read_profile)
        .map_err(|e| ApiError::internal(format!("query egress profiles error: {e}")))?;
    let mut profiles = Vec::new();
    for row in profile_rows {
        profiles.push(ProfileRow {
            profile: row
                .map_err(|e| ApiError::internal(format!("read egress profile error: {e}")))?,
        });
    }
    drop(profile_stmt);
    let mut binding_stmt = conn
        .prepare(&format!("SELECT {BINDING_SELECT} FROM egress_bindings"))
        .map_err(|e| ApiError::internal(format!("prepare egress bindings error: {e}")))?;
    let binding_rows = binding_stmt
        .query_map([], read_binding)
        .map_err(|e| ApiError::internal(format!("query egress bindings error: {e}")))?;
    let mut bindings = Vec::new();
    for row in binding_rows {
        let binding =
            row.map_err(|e| ApiError::internal(format!("read egress binding error: {e}")))?;
        let mut networks = Vec::new();
        for source in &binding.sources {
            networks.push(parse_network(source, "stored source", true)?);
        }
        bindings.push(BindingRow { binding, networks });
    }
    Ok((profiles, bindings))
}

fn load_runtime(conn: &rusqlite::Connection) -> Result<Vec<RuntimeProfile>, ApiError> {
    let mut stmt = conn.prepare("SELECT profile_id, route_table, mark, tunnel_interface, has_v4, has_v6, managed_interface, probe_sources_json FROM egress_runtime_profiles")
        .map_err(|e| ApiError::internal(format!("prepare egress runtime state error: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok(RuntimeProfile {
                profile_id: row.get(0)?,
                route_table: row.get(1)?,
                mark: row.get(2)?,
                tunnel_interface: row.get(3)?,
                has_v4: row.get::<_, i64>(4)? != 0,
                has_v6: row.get::<_, i64>(5)? != 0,
                managed_interface: row.get::<_, i64>(6)? != 0,
                probe_sources: wg_from_json(row.get(7)?),
            })
        })
        .map_err(|e| ApiError::internal(format!("query egress runtime state error: {e}")))?;
    let mut runtime = Vec::new();
    for row in rows {
        runtime.push(
            row.map_err(|e| ApiError::internal(format!("read egress runtime state error: {e}")))?,
        );
    }
    Ok(runtime)
}

#[derive(Debug, Clone, Default)]
struct HostInventory {
    interfaces: HashSet<String>,
    wireguard_interfaces: HashSet<String>,
    handshakes: HashMap<String, i64>,
}

fn host_inventory(executor: &impl CommandExecutor) -> HostInventory {
    let interfaces = fs::read_dir("/sys/class/net")
        .ok()
        .into_iter()
        .flatten()
        .filter_map(Result::ok)
        .filter_map(|entry| entry.file_name().into_string().ok())
        .collect();
    let mut inventory = HostInventory {
        interfaces,
        ..HostInventory::default()
    };
    if command_available("wg") {
        let args = vec!["show".to_string(), "interfaces".to_string()];
        if let Ok(result) = executor.run("wg", &args, None) {
            if result.success {
                inventory
                    .wireguard_interfaces
                    .extend(result.stdout.split_whitespace().map(str::to_string));
            }
        }
        let args = vec![
            "show".to_string(),
            "all".to_string(),
            "latest-handshakes".to_string(),
        ];
        if let Ok(result) = executor.run("wg", &args, None) {
            if result.success {
                for line in result.stdout.lines() {
                    let fields: Vec<&str> = line.split_whitespace().collect();
                    if fields.len() >= 3 {
                        if let Ok(timestamp) = fields[2].parse::<i64>() {
                            inventory
                                .handshakes
                                .entry(fields[0].to_string())
                                .and_modify(|current| *current = (*current).max(timestamp))
                                .or_insert(timestamp);
                        }
                    }
                }
            }
        }
    }
    inventory
}

fn enrich_profiles(profiles: &mut [EgressProfile], inventory: &HostInventory) {
    for profile in profiles {
        profile.last_handshake_at = inventory
            .handshakes
            .get(&profile.tunnel_interface)
            .copied()
            .filter(|value| *value > 0);
        profile.tunnel_ready = if profile.tunnel_type == "wireguard" {
            inventory
                .wireguard_interfaces
                .contains(&profile.tunnel_interface)
                && profile
                    .last_handshake_at
                    .is_some_and(recent_wireguard_handshake)
        } else {
            inventory.interfaces.contains(&profile.tunnel_interface)
        };
    }
}

fn recent_wireguard_handshake(timestamp: i64) -> bool {
    let now = now_ts();
    timestamp > 0
        && timestamp <= now.saturating_add(5)
        && now.saturating_sub(timestamp) <= WIREGUARD_HANDSHAKE_MAX_AGE_SECS
}

fn upsert_profile_row(
    conn: &rusqlite::Connection,
    profile: &EgressProfile,
) -> Result<(), ApiError> {
    let wg = profile.wireguard.clone().unwrap_or_else(default_wg_status);
    let now = now_ts();
    conn.execute(
        "INSERT INTO egress_profiles (id, mode, tunnel_type, tunnel_interface, gateway, route_table, mark, public_ipv4, public_ipv6, enabled, fail_closed, status, last_error, created_at, updated_at, wg_managed, wg_peer_public_key, wg_endpoint, wg_addresses, wg_allowed_ips, wg_keepalive, wg_mtu, wg_private_key_present, wg_preshared_key_present) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,'pending','',?12,?12,?13,?14,?15,?16,?17,?18,?19,?20,?21) ON CONFLICT(id) DO UPDATE SET mode=excluded.mode,tunnel_type=excluded.tunnel_type,tunnel_interface=excluded.tunnel_interface,gateway=excluded.gateway,route_table=excluded.route_table,mark=excluded.mark,public_ipv4=excluded.public_ipv4,public_ipv6=excluded.public_ipv6,enabled=excluded.enabled,fail_closed=excluded.fail_closed,status='pending',last_error='',updated_at=excluded.updated_at,wg_managed=excluded.wg_managed,wg_peer_public_key=excluded.wg_peer_public_key,wg_endpoint=excluded.wg_endpoint,wg_addresses=excluded.wg_addresses,wg_allowed_ips=excluded.wg_allowed_ips,wg_keepalive=excluded.wg_keepalive,wg_mtu=excluded.wg_mtu,wg_private_key_present=excluded.wg_private_key_present,wg_preshared_key_present=excluded.wg_preshared_key_present",
        params![
            &profile.id,
            &profile.mode,
            &profile.tunnel_type,
            &profile.tunnel_interface,
            profile.gateway.clone().unwrap_or_default(),
            profile.route_table,
            profile.mark,
            profile.public_ipv4.clone().unwrap_or_default(),
            profile.public_ipv6.clone().unwrap_or_default(),
            profile.enabled as i64,
            profile.fail_closed as i64,
            now,
            wg.managed as i64,
            wg.peer_public_key.clone().unwrap_or_default(),
            wg.endpoint.clone().unwrap_or_default(),
            wg_json(&wg.addresses),
            wg_json(&wg.allowed_ips),
            wg.persistent_keepalive,
            wg.mtu,
            wg.private_key_configured as i64,
            wg.preshared_key_configured as i64,
        ],
    )
    .map_err(|e| ApiError::internal(format!("save egress profile error: {e}")))?;
    Ok(())
}

fn upsert_binding_row(conn: &rusqlite::Connection, row: &BindingRow) -> Result<(), ApiError> {
    let now = now_ts();
    let enforcement = row.binding.enabled.then_some(1_i64);
    conn.execute(
        "INSERT INTO egress_bindings (instance_id,profile_id,source,interface,interface_v4,interface_v6,enabled,state,last_error,created_at,updated_at,sources_json,fail_closed_enforced) VALUES (?1,?2,?3,?4,?5,?6,?7,'pending','',?8,?8,?9,?10) ON CONFLICT(instance_id) DO UPDATE SET profile_id=excluded.profile_id,source=excluded.source,interface=excluded.interface,interface_v4=excluded.interface_v4,interface_v6=excluded.interface_v6,enabled=excluded.enabled,state='pending',last_error='',updated_at=excluded.updated_at,sources_json=excluded.sources_json,fail_closed_enforced=excluded.fail_closed_enforced",
        params![
            &row.binding.instance_id,
            &row.binding.profile_id,
            &row.binding.source,
            row.binding.interface.clone().unwrap_or_default(),
            row.binding.interface_v4.clone().unwrap_or_default(),
            row.binding.interface_v6.clone().unwrap_or_default(),
            row.binding.enabled as i64,
            now,
            wg_json(&row.binding.sources),
            enforcement,
        ],
    )
    .map_err(|e| ApiError::internal(format!("save egress binding error: {e}")))?;
    Ok(())
}

fn replace_desired_state_sql(
    conn: &rusqlite::Connection,
    profiles: &[EgressProfile],
    bindings: &[BindingRow],
) -> Result<(), ApiError> {
    let tx = conn
        .unchecked_transaction()
        .map_err(|e| ApiError::internal(format!("begin egress state transaction error: {e}")))?;
    tx.execute_batch(
        "CREATE TEMP TABLE IF NOT EXISTS egress_replace_profile_ids (id TEXT PRIMARY KEY NOT NULL);\
         CREATE TEMP TABLE IF NOT EXISTS egress_replace_binding_ids (id TEXT PRIMARY KEY NOT NULL);\
         DELETE FROM egress_replace_profile_ids;\
         DELETE FROM egress_replace_binding_ids;",
    )
    .map_err(|e| ApiError::internal(format!("prepare egress state replacement error: {e}")))?;
    for profile in profiles {
        upsert_profile_row(&tx, profile)?;
        tx.execute(
            "INSERT INTO egress_replace_profile_ids (id) VALUES (?1)",
            params![&profile.id],
        )
        .map_err(|e| ApiError::internal(format!("stage egress profile id error: {e}")))?;
    }
    for binding in bindings {
        upsert_binding_row(&tx, binding)?;
        tx.execute(
            "INSERT INTO egress_replace_binding_ids (id) VALUES (?1)",
            params![&binding.binding.instance_id],
        )
        .map_err(|e| ApiError::internal(format!("stage egress binding id error: {e}")))?;
    }
    tx.execute(
        "DELETE FROM egress_bindings WHERE instance_id NOT IN (SELECT id FROM egress_replace_binding_ids)",
        [],
    )
    .map_err(|e| ApiError::internal(format!("remove obsolete egress bindings error: {e}")))?;
    tx.execute(
        "DELETE FROM egress_profiles WHERE id NOT IN (SELECT id FROM egress_replace_profile_ids)",
        [],
    )
    .map_err(|e| ApiError::internal(format!("remove obsolete egress profiles error: {e}")))?;
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit egress state transaction error: {e}")))
}

pub async fn list_profiles(
    State(state): State<AppState>,
) -> Result<Json<ListProfilesResponse>, ApiError> {
    let mut profiles = {
        let conn = state.conn.lock().await;
        let mut stmt = conn
            .prepare(&format!(
                "SELECT {PROFILE_SELECT} FROM egress_profiles ORDER BY id"
            ))
            .map_err(|e| ApiError::internal(format!("prepare egress profile list error: {e}")))?;
        let rows = stmt
            .query_map([], read_profile)
            .map_err(|e| ApiError::internal(format!("query egress profile list error: {e}")))?;
        let mut result = Vec::new();
        for row in rows {
            result.push(
                row.map_err(|e| ApiError::internal(format!("read egress profile error: {e}")))?,
            );
        }
        result
    };
    enrich_profiles(&mut profiles, &host_inventory(&SystemExecutor));
    let total = profiles.len();
    Ok(Json(ListProfilesResponse { profiles, total }))
}

pub async fn put_profile(
    State(state): State<AppState>,
    Json(req): Json<EgressProfileRequest>,
) -> Result<Json<EgressProfile>, ApiError> {
    let (mut profile, requested_wg) = profile_from_request(req)?;
    let _guard = reconcile_lock().lock().await;
    let existing = {
        let conn = state.conn.lock().await;
        conn.query_row(
            &format!("SELECT {PROFILE_SELECT} FROM egress_profiles WHERE id = ?1"),
            params![&profile.id],
            read_profile,
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("read existing egress profile error: {e}")))?
    };
    if requested_wg.is_none() && profile.tunnel_type == "wireguard" {
        profile.wireguard = existing.as_ref().and_then(|value| value.wireguard.clone());
    }
    if let Some(config) = requested_wg.as_ref() {
        let mut status = config.status.clone();
        let existing_status = existing.as_ref().and_then(|value| value.wireguard.as_ref());
        if let Some(private_key) = config.private_key.as_deref() {
            atomic_write_secret(&secret_path(&profile.id, false)?, private_key)?;
            status.private_key_configured = true;
        } else if status.managed {
            status.private_key_configured = existing_status
                .is_some_and(|value| value.private_key_configured)
                || secret_path(&profile.id, false)?.is_file();
        }
        if let Some(preshared_key) = config.preshared_key.as_deref() {
            atomic_write_secret(&secret_path(&profile.id, true)?, preshared_key)?;
            status.preshared_key_configured = true;
        } else if status.managed {
            status.preshared_key_configured = existing_status
                .is_some_and(|value| value.preshared_key_configured)
                || secret_path(&profile.id, true)?.is_file();
        }
        if !status.managed {
            status.private_key_configured = false;
            status.preshared_key_configured = false;
        }
        profile.wireguard = Some(status);
    }
    {
        let conn = state.conn.lock().await;
        upsert_profile_row(&conn, &profile)?;
    }
    if profile.tunnel_type != "wireguard"
        || requested_wg
            .as_ref()
            .is_some_and(|config| !config.status.managed)
    {
        remove_profile_secrets(&profile.id);
    }
    profile = {
        let conn = state.conn.lock().await;
        conn.query_row(
            &format!("SELECT {PROFILE_SELECT} FROM egress_profiles WHERE id=?1"),
            params![&profile.id],
            read_profile,
        )
        .map_err(|e| ApiError::internal(format!("read saved egress profile error: {e}")))?
    };
    enrich_profiles(
        std::slice::from_mut(&mut profile),
        &host_inventory(&SystemExecutor),
    );
    Ok(Json(profile))
}

pub async fn delete_profile(
    State(state): State<AppState>,
    Json(req): Json<EgressProfileDeleteRequest>,
) -> Result<Json<Value>, ApiError> {
    let id = validate_id(&req.id, "id")?;
    let _guard = reconcile_lock().lock().await;
    let deleted = {
        let conn = state.conn.lock().await;
        let binding_count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM egress_bindings WHERE profile_id = ?1",
                params![&id],
                |row| row.get(0),
            )
            .map_err(|e| ApiError::internal(format!("check egress profile bindings error: {e}")))?;
        if binding_count > 0 {
            return Err(ApiError::bad_request(
                "delete all profile bindings before deleting the egress profile",
            ));
        }
        conn.execute("DELETE FROM egress_profiles WHERE id = ?1", params![&id])
            .map_err(|e| ApiError::internal(format!("delete egress profile error: {e}")))?
    };
    drop(_guard);
    if deleted > 0 {
        let reconciled = reconcile_state(state.clone(), true).await?;
        if !reconciled.applied {
            return Err(ApiError::internal(format!(
                "egress profile was removed from desired state but host cleanup is incomplete: {}",
                reconciled.errors.join("; ")
            )));
        }
        remove_profile_secrets(&id);
    } else {
        // A previous deletion may have removed desired state before host cleanup
        // completed. Repeating DELETE is an idempotent cleanup retry.
        let reconciled = reconcile_state(state.clone(), true).await?;
        if reconciled.applied {
            remove_profile_secrets(&id);
        }
    }
    Ok(Json(
        serde_json::json!({ "id": id, "deleted": deleted > 0 }),
    ))
}

pub async fn list_bindings(
    State(state): State<AppState>,
) -> Result<Json<ListBindingsResponse>, ApiError> {
    let mut bindings = {
        let conn = state.conn.lock().await;
        let mut stmt = conn
            .prepare(&format!(
                "SELECT {BINDING_SELECT} FROM egress_bindings ORDER BY instance_id"
            ))
            .map_err(|e| ApiError::internal(format!("prepare egress binding list error: {e}")))?;
        let rows = stmt
            .query_map([], read_binding)
            .map_err(|e| ApiError::internal(format!("query egress binding list error: {e}")))?;
        let mut result = Vec::new();
        for row in rows {
            result.push(
                row.map_err(|e| ApiError::internal(format!("read egress binding error: {e}")))?,
            );
        }
        result
    };
    add_live_counters(&SystemExecutor, &mut bindings);
    let total = bindings.len();
    Ok(Json(ListBindingsResponse { bindings, total }))
}

pub async fn put_binding(
    State(state): State<AppState>,
    Json(req): Json<EgressBindingRequest>,
) -> Result<Json<EgressBinding>, ApiError> {
    let row = binding_from_request(req)?;
    // Serialize the staging barrier with full reconcile. The SQLite write and
    // the nft transaction are intentionally kept in one operation: a caller
    // must never observe a successful binding PUT before its source is blocked.
    let _guard = reconcile_lock().lock().await;
    let path = managed_sources_path()?;
    let binding = {
        let conn = state.conn.lock().await;
        persist_binding_with_quarantine(&conn, &SystemExecutor, row, &path)?
    };
    Ok(Json(binding))
}

pub async fn replace_state(
    State(state): State<AppState>,
    Json(req): Json<ReplaceStateRequest>,
) -> Result<Json<ReplaceStateResponse>, ApiError> {
    // Parse and cross-reference the complete request before touching nft,
    // files, secrets, or SQLite. Replacement is authoritative and therefore
    // must never silently drop a malformed item.
    let normalized = normalize_replace_state_request(req)?;
    let profile_ids = normalized
        .profiles
        .iter()
        .map(|(profile, _)| profile.id.clone())
        .collect::<HashSet<_>>();
    let bindings = normalized.bindings;
    let apply = normalized.apply;
    let _guard = reconcile_lock().lock().await;
    let (existing_profiles, old_networks) = {
        let conn = state.conn.lock().await;
        let (profiles, bindings) = load_desired(&conn)?;
        let existing_profiles = profiles
            .into_iter()
            .map(|row| (row.profile.id.clone(), row.profile))
            .collect::<HashMap<_, _>>();
        let old_networks = bindings
            .into_iter()
            .filter(|row| row.binding.enabled)
            .flat_map(|row| row.networks)
            .collect::<Vec<_>>();
        (existing_profiles, old_networks)
    };

    let mut profiles = Vec::with_capacity(normalized.profiles.len());
    let mut secret_updates = Vec::new();
    let mut remove_secret_ids: HashSet<String> = existing_profiles
        .keys()
        .filter(|id| !profile_ids.contains(*id))
        .cloned()
        .collect();
    for (mut profile, requested_wireguard) in normalized.profiles {
        let existing = existing_profiles.get(&profile.id);
        let (mut updates, remove_secrets) =
            prepare_profile_storage(&mut profile, requested_wireguard.as_ref(), existing)?;
        secret_updates.append(&mut updates);
        if remove_secrets {
            remove_secret_ids.insert(profile.id.clone());
        }
        profiles.push(profile);
    }

    let new_networks = bindings
        .iter()
        .filter(|row| row.binding.enabled)
        .flat_map(|row| row.networks.iter().cloned())
        .collect::<Vec<_>>();
    let mut quarantine = old_networks.clone();
    quarantine.extend(new_networks.iter().cloned());
    quarantine.sort_by_key(|network| {
        (
            matches!(network.family(), Family::V6),
            network.canonical_bits(),
            network.prefix,
        )
    });
    quarantine.dedup();
    install_staging_quarantine(&SystemExecutor, &quarantine).map_err(ApiError::internal)?;
    let path = managed_sources_path()?;
    write_managed_networks_at(&path, &quarantine)?;

    let backups = apply_secret_updates(&secret_updates)?;
    let replace_result = {
        let conn = state.conn.lock().await;
        replace_desired_state_sql(&conn, &profiles, &bindings)
    };
    if let Err(error) = replace_result {
        rollback_secret_updates(&backups);
        return Err(error);
    }
    for id in remove_secret_ids {
        remove_profile_secrets(&id);
    }

    let reconcile_result = reconcile_state_locked(state.clone(), apply, false).await;
    let reconciled = match reconcile_result {
        Ok(response) => response,
        Err(error) => {
            // The database now contains the authoritative replacement. Keep
            // both retired and new identities durable until a retry proves the
            // old data plane has been removed.
            write_managed_networks_at(&path, &quarantine)?;
            return Err(error);
        }
    };
    if apply && reconciled.applied && reconciled.errors.is_empty() {
        write_managed_networks_at(&path, &new_networks)?;
    } else {
        write_managed_networks_at(&path, &quarantine)?;
    }
    Ok(Json(ReplaceStateResponse {
        profile_count: profiles.len(),
        binding_count: bindings.len(),
        reconcile: reconciled,
    }))
}

fn binding_networks(binding: &EgressBinding) -> Result<Vec<IpNetwork>, ApiError> {
    binding
        .sources
        .iter()
        .map(|source| parse_network(source, "stored source", true))
        .collect()
}

/// Stage a binding behind the high-priority boot/staging guard before making
/// it visible in SQLite. Existing source identities are included when updating
/// a binding so an address transition cannot briefly fall through to the host
/// default route. Host commands and durable file I/O happen before the short
/// SQL-only transaction.
fn persist_binding_with_quarantine(
    conn: &rusqlite::Connection,
    executor: &impl CommandExecutor,
    row: BindingRow,
    managed_sources: &Path,
) -> Result<EgressBinding, ApiError> {
    let profile_exists: Option<i64> = conn
        .query_row(
            "SELECT 1 FROM egress_profiles WHERE id = ?1",
            params![&row.binding.profile_id],
            |value| value.get(0),
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("check egress profile error: {e}")))?;
    if profile_exists.is_none() {
        return Err(ApiError::bad_request("egress profile does not exist"));
    }

    let previous = conn
        .query_row(
            &format!("SELECT {BINDING_SELECT} FROM egress_bindings WHERE instance_id=?1"),
            params![&row.binding.instance_id],
            read_binding,
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("read existing egress binding error: {e}")))?;
    let mut quarantine = if row.binding.enabled {
        row.networks.clone()
    } else {
        Vec::new()
    };
    if let Some(previous) = previous.as_ref().filter(|binding| binding.enabled) {
        quarantine.extend(binding_networks(previous)?);
    }
    quarantine.sort_by_key(|network| {
        (
            matches!(network.family(), Family::V6),
            network.canonical_bits(),
            network.prefix,
        )
    });
    quarantine.dedup();
    install_staging_quarantine(executor, &quarantine).map_err(ApiError::internal)?;
    let extra = if row.binding.enabled {
        row.networks.as_slice()
    } else {
        &[]
    };
    write_managed_sources_at_with_extra(conn, managed_sources, extra)?;

    let tx = conn
        .unchecked_transaction()
        .map_err(|e| ApiError::internal(format!("begin egress binding transaction error: {e}")))?;
    upsert_binding_row(&tx, &row)?;
    let binding = tx
        .query_row(
            &format!("SELECT {BINDING_SELECT} FROM egress_bindings WHERE instance_id=?1"),
            params![&row.binding.instance_id],
            read_binding,
        )
        .map_err(|e| ApiError::internal(format!("read saved egress binding error: {e}")))?;
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit egress binding transaction error: {e}")))?;
    Ok(binding)
}

pub async fn delete_binding(
    State(state): State<AppState>,
    Json(req): Json<EgressBindingDeleteRequest>,
) -> Result<Json<Value>, ApiError> {
    let instance_id = validate_id(&req.instance_id, "instance_id")?;
    let _guard = reconcile_lock().lock().await;
    let (deleted, retired_networks) = {
        let conn = state.conn.lock().await;
        let previous = conn
            .query_row(
                &format!("SELECT {BINDING_SELECT} FROM egress_bindings WHERE instance_id=?1"),
                params![&instance_id],
                read_binding,
            )
            .optional()
            .map_err(|e| {
                ApiError::internal(format!("read egress binding for deletion error: {e}"))
            })?;
        let retired_networks = previous
            .as_ref()
            .filter(|binding| binding.enabled)
            .map(binding_networks)
            .transpose()?
            .unwrap_or_default();
        install_staging_quarantine(&SystemExecutor, &retired_networks)
            .map_err(ApiError::internal)?;
        let deleted = conn
            .execute(
                "DELETE FROM egress_bindings WHERE instance_id = ?1",
                params![&instance_id],
            )
            .map_err(|e| ApiError::internal(format!("delete egress binding error: {e}")))?;
        (deleted, retired_networks)
    };
    drop(_guard);
    if deleted > 0 {
        let reconcile_result = reconcile_state(state.clone(), true).await;
        let cleanup_complete = reconcile_result
            .as_ref()
            .is_ok_and(|response| response.applied);
        if !cleanup_complete {
            let conn = state.conn.lock().await;
            write_managed_sources_with_extra(&conn, &retired_networks)?;
            return match reconcile_result {
                Ok(response) => Err(ApiError::internal(format!(
                    "egress binding was removed from desired state but host cleanup is incomplete; retired sources remain quarantined: {}",
                    response.errors.join("; ")
                ))),
                Err(error) => Err(ApiError::internal(format!(
                    "egress binding was removed from desired state but host cleanup failed; retired sources remain quarantined: {}",
                    error.message
                ))),
            };
        }
    }
    Ok(Json(
        serde_json::json!({ "instance_id": instance_id, "deleted": deleted > 0 }),
    ))
}

fn fnv1a(bytes: impl Iterator<Item = u8>) -> u64 {
    bytes.fold(0xcbf29ce484222325, |hash, byte| {
        (hash ^ byte as u64).wrapping_mul(0x100000001b3)
    })
}

fn counter_key(instance_id: &str) -> String {
    let forward = fnv1a(instance_id.bytes());
    let reverse = fnv1a(instance_id.bytes().rev());
    format!("{forward:016x}{reverse:016x}")
}

fn counter_name(direction: char, instance_id: &str) -> String {
    format!("ocv_{direction}_{}", counter_key(instance_id))
}

fn collect_nft_counters(value: &Value, counters: &mut HashMap<String, u64>) {
    match value {
        Value::Array(values) => values
            .iter()
            .for_each(|value| collect_nft_counters(value, counters)),
        Value::Object(object) => {
            if let Some(Value::Object(counter)) = object.get("counter") {
                if let (Some(name), Some(bytes)) = (
                    counter.get("name").and_then(Value::as_str),
                    counter.get("bytes").and_then(Value::as_u64),
                ) {
                    counters.insert(name.to_string(), bytes);
                }
            }
            object
                .values()
                .for_each(|value| collect_nft_counters(value, counters));
        }
        _ => {}
    }
}

fn read_nft_counters(executor: &impl CommandExecutor) -> HashMap<String, u64> {
    if !command_available("nft") {
        return HashMap::new();
    }
    let args = vec![
        "-j".to_string(),
        "list".to_string(),
        "counters".to_string(),
        "table".to_string(),
        TABLE_FAMILY.to_string(),
        TABLE_NAME.to_string(),
    ];
    let Ok(result) = executor.run("nft", &args, None) else {
        return HashMap::new();
    };
    if !result.success {
        return HashMap::new();
    }
    let Ok(value) = serde_json::from_str::<Value>(&result.stdout) else {
        return HashMap::new();
    };
    let mut counters = HashMap::new();
    collect_nft_counters(&value, &mut counters);
    counters
}

fn add_live_counters(executor: &impl CommandExecutor, bindings: &mut [EgressBinding]) {
    let counters = read_nft_counters(executor);
    for binding in bindings {
        binding.traffic_bytes_in = binding.traffic_bytes_in.saturating_add(
            *counters
                .get(&counter_name('i', &binding.instance_id))
                .unwrap_or(&0),
        );
        binding.traffic_bytes_out = binding.traffic_bytes_out.saturating_add(
            *counters
                .get(&counter_name('o', &binding.instance_id))
                .unwrap_or(&0),
        );
        binding.traffic_bytes_dropped = binding.traffic_bytes_dropped.saturating_add(
            *counters
                .get(&counter_name('d', &binding.instance_id))
                .unwrap_or(&0),
        );
    }
}

#[derive(Debug, Clone)]
struct NftBinding {
    instance_id: String,
    profile_id: String,
    network: IpNetwork,
    input_interface: Option<String>,
    tunnel_interface: Option<String>,
    effective_mark: Option<u32>,
    quarantine: bool,
}

#[derive(Debug, Clone)]
struct ProfileApplication {
    profile: EgressProfile,
    families: HashSet<Family>,
    binding_indices: Vec<usize>,
}

#[derive(Debug, Clone)]
struct PreparedReconcile {
    plans: Vec<RoutePlan>,
    nft_bindings: Vec<NftBinding>,
    applications: Vec<ProfileApplication>,
}

fn effective_mark(mark: u32) -> u32 {
    MARK_NAMESPACE | mark
}

fn plan_commands(profile: &EgressProfile, binding: &BindingRow) -> Vec<String> {
    let mark = effective_mark(profile.mark);
    let priority = RULE_PRIORITY_BASE + profile.route_table;
    let mut commands = Vec::new();
    for network in &binding.networks {
        let family = network.family();
        commands.extend([
            format!("nft {TABLE_FAMILY}/{TABLE_NAME} {} saddr {network} mark 0x{mark:08x}", family.nft_prefix()),
            format!("ip {} route replace table {} default dev {}", family.ip_flag(), profile.route_table, profile.tunnel_interface),
            format!("ip {} rule del priority {priority} fwmark 0x{mark:08x}/0xffffffff table {} (ignore not-found)", family.ip_flag(), profile.route_table),
            format!("ip {} rule add priority {priority} fwmark 0x{mark:08x}/0xffffffff table {}", family.ip_flag(), profile.route_table),
            format!("nft fail-closed {} saddr {network} oifname != {} drop", family.nft_prefix(), profile.tunnel_interface),
        ]);
    }
    commands
}

fn append_error(errors: &mut [Vec<String>], index: usize, message: impl Into<String>) {
    let message = message.into();
    if !errors[index].contains(&message) {
        errors[index].push(message);
    }
}

fn prepare_reconcile(
    profiles: &[ProfileRow],
    bindings: &[BindingRow],
    capabilities: &HostCapabilities,
    inventory: &HostInventory,
    apply_requested: bool,
) -> PreparedReconcile {
    let profile_map: HashMap<&str, &EgressProfile> = profiles
        .iter()
        .map(|row| (row.profile.id.as_str(), &row.profile))
        .collect();
    let mut errors = vec![Vec::<String>::new(); bindings.len()];
    let mut candidates = Vec::new();
    for (index, row) in bindings.iter().enumerate() {
        if !row.binding.enabled {
            continue;
        }
        candidates.push(index);
        for family in row
            .networks
            .iter()
            .map(IpNetwork::family)
            .collect::<HashSet<_>>()
        {
            let interface = match family {
                Family::V4 => row.binding.interface_v4.as_ref(),
                Family::V6 => row.binding.interface_v6.as_ref(),
            };
            if interface.is_none() {
                append_error(
                    &mut errors,
                    index,
                    format!(
                        "native egress requires a validated host ingress interface for {}",
                        match family {
                            Family::V4 => "IPv4",
                            Family::V6 => "IPv6",
                        }
                    ),
                );
            }
        }
        let Some(profile) = profile_map.get(row.binding.profile_id.as_str()).copied() else {
            append_error(&mut errors, index, "egress profile not found");
            continue;
        };
        if !profile.enabled {
            append_error(&mut errors, index, "profile is disabled");
        }
        if profile.mode != "native" {
            append_error(
                &mut errors,
                index,
                format!("mode {} requires an external adapter", profile.mode),
            );
        }
        if !profile.fail_closed {
            append_error(&mut errors, index, "fail_closed must remain enabled");
        }
        if profile.route_table == 0
            || profile.route_table > MAX_ROUTE_TABLE
            || (253..=255).contains(&profile.route_table)
        {
            append_error(&mut errors, index, "invalid route table");
        }
        if profile.mark == 0 || profile.mark > MAX_MARK {
            append_error(&mut errors, index, "invalid policy mark");
        }
        if !capabilities.running_as_root {
            append_error(&mut errors, index, "agent is not running as root");
        }
        if !capabilities.ip_available {
            append_error(&mut errors, index, "ip command is unavailable");
        }
        if !capabilities.nft_available {
            append_error(&mut errors, index, "nft command is unavailable");
        }
        if !capabilities.curl_available {
            append_error(
                &mut errors,
                index,
                "curl is unavailable for strict public egress verification",
            );
        }
        if apply_requested && !capabilities.apply_enabled {
            append_error(&mut errors, index, format!("{APPLY_ENV}=true is required"));
        }
        for network in &row.networks {
            match network.family() {
                Family::V4 if !capabilities.ipv4_forwarding => {
                    append_error(&mut errors, index, "IPv4 forwarding is disabled")
                }
                Family::V6 if !capabilities.ipv6_forwarding => {
                    append_error(&mut errors, index, "IPv6 forwarding is disabled")
                }
                _ => {}
            }
            let expected = match network.family() {
                Family::V4 => profile.public_ipv4.as_ref(),
                Family::V6 => profile.public_ipv6.as_ref(),
            };
            if expected.is_none() {
                append_error(
                    &mut errors,
                    index,
                    format!(
                        "strict health verification requires expected public {}",
                        match network.family() {
                            Family::V4 => "IPv4",
                            Family::V6 => "IPv6",
                        }
                    ),
                );
            }
        }
        match profile.tunnel_type.as_str() {
            "wireguard" => {
                if !capabilities.wireguard_available {
                    append_error(&mut errors, index, "wireguard-tools is unavailable");
                }
                let managed = profile.wireguard.as_ref().is_some_and(|wg| wg.managed);
                if managed {
                    let wg = profile.wireguard.as_ref().unwrap();
                    if !wg.private_key_configured {
                        append_error(
                            &mut errors,
                            index,
                            "managed WireGuard private key is not configured",
                        );
                    }
                    if wg.peer_public_key.is_none()
                        || wg.addresses.is_empty()
                        || wg.allowed_ips.is_empty()
                    {
                        append_error(
                            &mut errors,
                            index,
                            "managed WireGuard configuration is incomplete",
                        );
                    }
                } else if !inventory
                    .wireguard_interfaces
                    .contains(&profile.tunnel_interface)
                {
                    append_error(
                        &mut errors,
                        index,
                        format!(
                            "WireGuard interface {} is not configured",
                            profile.tunnel_interface
                        ),
                    );
                }
            }
            "gateway" => {
                if !inventory.interfaces.contains(&profile.tunnel_interface) {
                    append_error(
                        &mut errors,
                        index,
                        format!(
                            "gateway interface {} does not exist",
                            profile.tunnel_interface
                        ),
                    );
                }
            }
            "ipsec" => append_error(
                &mut errors,
                index,
                "native IPsec provisioning is unsupported; use a preconfigured gateway profile",
            ),
            _ => append_error(&mut errors, index, "unsupported tunnel type"),
        }
    }

    // Marks, route tables and managed interfaces are exclusive across profiles.
    for selector in ["mark", "table", "interface"] {
        let mut owners: HashMap<String, Vec<usize>> = HashMap::new();
        for &index in &candidates {
            if let Some(profile) = profile_map.get(bindings[index].binding.profile_id.as_str()) {
                let key = match selector {
                    "mark" => profile.mark.to_string(),
                    "table" => profile.route_table.to_string(),
                    _ => profile.tunnel_interface.clone(),
                };
                owners.entry(key).or_default().push(index);
            }
        }
        for indexes in owners.values().filter(|indexes| {
            let profiles: HashSet<&str> = indexes
                .iter()
                .map(|index| bindings[*index].binding.profile_id.as_str())
                .collect();
            profiles.len() > 1
        }) {
            for &index in indexes {
                append_error(
                    &mut errors,
                    index,
                    format!("conflicting egress profile {selector}"),
                );
            }
        }
    }

    // Detect source overlap in O(n log n), avoiding per-binding host/DB calls.
    let mut ranges: Vec<(Family, u128, u128, usize)> = candidates
        .iter()
        .flat_map(|index| {
            bindings[*index].networks.iter().map(|network| {
                (
                    network.family(),
                    network.canonical_bits(),
                    network.end_bits(),
                    *index,
                )
            })
        })
        .collect();
    ranges.sort_by_key(|item| (matches!(item.0, Family::V6), item.1, item.2));
    let mut previous: Option<(Family, u128, usize)> = None;
    for (family, start, end, index) in ranges {
        if let Some((previous_family, previous_end, previous_index)) = previous {
            if previous_family == family && start <= previous_end {
                append_error(
                    &mut errors,
                    index,
                    "source overlaps another enabled binding",
                );
                append_error(
                    &mut errors,
                    previous_index,
                    "source overlaps another enabled binding",
                );
            }
            if previous_family != family || end > previous_end {
                previous = Some((family, end, index));
            }
        } else {
            previous = Some((family, end, index));
        }
    }

    let mut applications: HashMap<String, ProfileApplication> = HashMap::new();
    let mut plans = Vec::with_capacity(bindings.len());
    let mut nft_bindings = Vec::new();
    for (index, row) in bindings.iter().enumerate() {
        if !row.binding.enabled {
            plans.push(RoutePlan {
                instance_id: row.binding.instance_id.clone(),
                profile_id: row.binding.profile_id.clone(),
                status: "disabled".to_string(),
                commands: Vec::new(),
                error: None,
            });
            continue;
        }
        let profile = profile_map.get(row.binding.profile_id.as_str()).copied();
        let blocked = !errors[index].is_empty();
        let commands = profile
            .map(|profile| plan_commands(profile, row))
            .unwrap_or_default();
        plans.push(RoutePlan {
            instance_id: row.binding.instance_id.clone(),
            profile_id: row.binding.profile_id.clone(),
            status: if blocked { "blocked" } else { "planned" }.to_string(),
            commands,
            error: blocked.then(|| errors[index].join("; ")),
        });
        for network in &row.networks {
            let input_interface = match network.family() {
                Family::V4 => row.binding.interface_v4.clone(),
                Family::V6 => row.binding.interface_v6.clone(),
            };
            nft_bindings.push(NftBinding {
                instance_id: row.binding.instance_id.clone(),
                profile_id: row.binding.profile_id.clone(),
                network: network.clone(),
                input_interface,
                tunnel_interface: profile
                    .filter(|_| !blocked)
                    .map(|profile| profile.tunnel_interface.clone()),
                effective_mark: profile
                    .filter(|_| !blocked)
                    .map(|profile| effective_mark(profile.mark)),
                quarantine: blocked,
            });
        }
        if !blocked {
            let profile = profile.unwrap();
            let application =
                applications
                    .entry(profile.id.clone())
                    .or_insert_with(|| ProfileApplication {
                        profile: profile.clone(),
                        families: HashSet::new(),
                        binding_indices: Vec::new(),
                    });
            application
                .families
                .extend(row.networks.iter().map(IpNetwork::family));
            application.binding_indices.push(index);
        }
    }
    PreparedReconcile {
        plans,
        nft_bindings,
        applications: applications.into_values().collect(),
    }
}

fn nft_table_exists(executor: &impl CommandExecutor) -> Result<bool, String> {
    let args = vec![
        "list".to_string(),
        "table".to_string(),
        TABLE_FAMILY.to_string(),
        TABLE_NAME.to_string(),
    ];
    let result = executor.run("nft", &args, None)?;
    Ok(result.success)
}

fn build_nft_script(bindings: &[NftBinding], table_exists: bool, quarantine_all: bool) -> String {
    let mut script = String::new();
    if table_exists {
        script.push_str(&format!("flush table {TABLE_FAMILY} {TABLE_NAME}\n"));
    } else {
        script.push_str(&format!("add table {TABLE_FAMILY} {TABLE_NAME}\n"));
    }
    let mut seen = HashSet::new();
    for binding in bindings {
        if seen.insert(binding.instance_id.as_str()) {
            for direction in ['i', 'o', 'd'] {
                script.push_str(&format!(
                    "add counter {TABLE_FAMILY} {TABLE_NAME} {}\n",
                    counter_name(direction, &binding.instance_id)
                ));
            }
        }
    }
    script.push_str(&format!("add chain {TABLE_FAMILY} {TABLE_NAME} classify_prerouting {{ type filter hook prerouting priority -150; policy accept; }}\n"));
    script.push_str(&format!("add chain {TABLE_FAMILY} {TABLE_NAME} enforce_forward {{ type filter hook forward priority 0; policy accept; }}\n"));
    script.push_str(&format!("add chain {TABLE_FAMILY} {TABLE_NAME} enforce_output {{ type filter hook output priority 0; policy accept; }}\n"));
    script.push_str(&format!("add chain {TABLE_FAMILY} {TABLE_NAME} enforce_input {{ type filter hook input priority 0; policy accept; }}\n"));
    for binding in bindings {
        let family = binding.network.family().nft_prefix();
        let source = binding.network.to_string();
        let out_counter = counter_name('o', &binding.instance_id);
        let in_counter = counter_name('i', &binding.instance_id);
        let drop_counter = counter_name('d', &binding.instance_id);
        if !quarantine_all && !binding.quarantine {
            let interface = binding
                .input_interface
                .as_ref()
                .expect("active nft binding has ingress interface");
            let mark = binding.effective_mark.expect("active nft binding has mark");
            let tunnel = binding
                .tunnel_interface
                .as_ref()
                .expect("active nft binding has tunnel");
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} classify_prerouting iifname \"{interface}\" {family} saddr {source} counter name {out_counter} meta mark set 0x{mark:08x} ct mark set meta mark\n"));
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_forward iifname \"{tunnel}\" {family} daddr {source} counter name {in_counter}\n"));
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_forward iifname \"{interface}\" {family} saddr {source} oifname \"{tunnel}\" accept\n"));
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_forward {family} saddr {source} oifname != \"{tunnel}\" counter name {drop_counter} drop\n"));
            // Packets originating in an instance are classified only on its
            // ingress interface. Host-local packets spoofing that source must
            // never inherit the instance route.
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_output {family} saddr {source} counter name {drop_counter} drop\n"));
            // Bound sources cannot consume host-local services. Keep only the
            // control traffic required to maintain L3 attachment.
            if family == "ip" {
                script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input ip saddr {source} udp sport 68 udp dport 67 accept\n"));
            } else {
                script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input ip6 saddr {source} udp sport 546 udp dport 547 accept\n"));
                script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input ip6 saddr {source} meta l4proto ipv6-icmp icmpv6 type {{ 133, 135, 136 }} accept\n"));
            }
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input {family} saddr {source} counter name {drop_counter} drop\n"));
        } else {
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_forward {family} saddr {source} counter name {drop_counter} drop\n"));
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_output {family} saddr {source} counter name {drop_counter} drop\n"));
            if family == "ip" {
                script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input ip saddr {source} udp sport 68 udp dport 67 accept\n"));
            } else {
                script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input ip6 saddr {source} udp sport 546 udp dport 547 accept\n"));
                script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input ip6 saddr {source} meta l4proto ipv6-icmp icmpv6 type {{ 133, 135, 136 }} accept\n"));
            }
            script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input {family} saddr {source} counter name {drop_counter} drop\n"));
        }
    }
    // Treat each managed ingress interface as an allowlist boundary. Source
    // rules for every binding are emitted first (including shared-interface
    // bindings), then any other IPv4/IPv6 identity arriving on that interface is
    // dropped so source spoofing cannot fall through to the host default route.
    let interfaces: BTreeSet<&str> = bindings
        .iter()
        .filter_map(|binding| binding.input_interface.as_deref())
        .collect();
    for interface in interfaces {
        script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_forward iifname \"{interface}\" meta nfproto ipv4 drop\n"));
        script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_forward iifname \"{interface}\" meta nfproto ipv6 drop\n"));
        script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input iifname \"{interface}\" meta nfproto ipv4 udp sport 68 udp dport 67 accept\n"));
        script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input iifname \"{interface}\" meta nfproto ipv6 udp sport 546 udp dport 547 accept\n"));
        script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input iifname \"{interface}\" meta nfproto ipv6 meta l4proto ipv6-icmp icmpv6 type {{ 133, 135, 136 }} accept\n"));
        script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input iifname \"{interface}\" meta nfproto ipv4 drop\n"));
        script.push_str(&format!("add rule {TABLE_FAMILY} {TABLE_NAME} enforce_input iifname \"{interface}\" meta nfproto ipv6 drop\n"));
    }
    script
}

fn concise_error(result: &CommandResult, context: &str) -> String {
    let detail = result
        .stderr
        .lines()
        .chain(result.stdout.lines())
        .map(str::trim)
        .find(|line| !line.is_empty())
        .unwrap_or("command failed")
        .chars()
        .take(300)
        .collect::<String>();
    format!("{context}: {detail}")
}

fn run_checked(
    executor: &impl CommandExecutor,
    program: &str,
    args: Vec<String>,
    context: &str,
) -> Result<(), String> {
    let result = executor.run(program, &args, None)?;
    if result.success {
        Ok(())
    } else {
        Err(concise_error(&result, context))
    }
}

fn apply_nft_script(executor: &impl CommandExecutor, script: &str) -> Result<(), String> {
    let check_args = vec!["-c".to_string(), "-f".to_string(), "-".to_string()];
    let checked = executor.run("nft", &check_args, Some(script))?;
    if !checked.success {
        return Err(concise_error(&checked, "nft validation failed"));
    }
    let apply_args = vec!["-f".to_string(), "-".to_string()];
    let applied = executor.run("nft", &apply_args, Some(script))?;
    if applied.success {
        Ok(())
    } else {
        Err(concise_error(&applied, "nft transaction failed"))
    }
}

fn write_wireguard_runtime_config(
    profile: &EgressProfile,
    status: &WireGuardStatus,
) -> Result<PathBuf, String> {
    let private_path = secret_path(&profile.id, false).map_err(|e| e.message)?;
    let private_key = fs::read_to_string(&private_path)
        .map_err(|e| format!("read WireGuard private key: {e}"))?;
    validate_key(private_key.trim(), "stored WireGuard private key").map_err(|e| e.message)?;
    let mut config = format!(
        "[Interface]\nPrivateKey = {}\n\n[Peer]\nPublicKey = {}\nAllowedIPs = {}\n",
        private_key.trim(),
        status
            .peer_public_key
            .as_deref()
            .ok_or("WireGuard peer public key is missing")?,
        status.allowed_ips.join(", ")
    );
    if status.preshared_key_configured {
        let path = secret_path(&profile.id, true).map_err(|e| e.message)?;
        let psk =
            fs::read_to_string(&path).map_err(|e| format!("read WireGuard preshared key: {e}"))?;
        validate_key(psk.trim(), "stored WireGuard preshared key").map_err(|e| e.message)?;
        config.push_str(&format!("PresharedKey = {}\n", psk.trim()));
    }
    if let Some(endpoint) = status.endpoint.as_deref() {
        config.push_str(&format!("Endpoint = {endpoint}\n"));
    }
    if status.persistent_keepalive > 0 {
        config.push_str(&format!(
            "PersistentKeepalive = {}\n",
            status.persistent_keepalive
        ));
    }
    let directory = state_directory().map_err(|e| e.message)?;
    fs::create_dir_all(&directory).map_err(|e| format!("create egress state directory: {e}"))?;
    let path = directory.join(format!(
        ".wg-runtime-{}-{}.conf",
        profile.id,
        std::process::id()
    ));
    atomic_write_secret(&path, config.trim_end()).map_err(|e| e.message)?;
    Ok(path)
}

fn configure_wireguard(
    executor: &impl CommandExecutor,
    profile: &EgressProfile,
) -> Result<(), String> {
    let Some(status) = profile.wireguard.as_ref().filter(|status| status.managed) else {
        return run_checked(
            executor,
            "wg",
            vec!["show".to_string(), profile.tunnel_interface.clone()],
            "WireGuard interface is unavailable",
        );
    };
    if !Path::new("/sys/class/net")
        .join(&profile.tunnel_interface)
        .exists()
    {
        run_checked(
            executor,
            "ip",
            vec![
                "link".to_string(),
                "add".to_string(),
                "dev".to_string(),
                profile.tunnel_interface.clone(),
                "type".to_string(),
                "wireguard".to_string(),
            ],
            "create WireGuard interface",
        )?;
    }
    let runtime_config = write_wireguard_runtime_config(profile, status)?;
    let set_result = run_checked(
        executor,
        "wg",
        vec![
            "setconf".to_string(),
            profile.tunnel_interface.clone(),
            runtime_config.display().to_string(),
        ],
        "configure WireGuard interface",
    );
    let _ = fs::remove_file(&runtime_config);
    set_result?;
    for family in [Family::V4, Family::V6] {
        // This interface is managed exclusively by one profile; replacing its
        // global addresses avoids retaining stale source addresses on updates.
        let _ = executor.run(
            "ip",
            &[
                family.ip_flag().to_string(),
                "address".to_string(),
                "flush".to_string(),
                "dev".to_string(),
                profile.tunnel_interface.clone(),
                "scope".to_string(),
                "global".to_string(),
            ],
            None,
        );
    }
    for address in &status.addresses {
        let network =
            parse_network(address, "stored WireGuard address", false).map_err(|e| e.message)?;
        run_checked(
            executor,
            "ip",
            vec![
                network.family().ip_flag().to_string(),
                "address".to_string(),
                "replace".to_string(),
                network.host_string(),
                "dev".to_string(),
                profile.tunnel_interface.clone(),
            ],
            "assign WireGuard address",
        )?;
    }
    run_checked(
        executor,
        "ip",
        vec![
            "link".to_string(),
            "set".to_string(),
            "dev".to_string(),
            profile.tunnel_interface.clone(),
            "mtu".to_string(),
            status.mtu.to_string(),
            "up".to_string(),
        ],
        "activate WireGuard interface",
    )?;
    run_checked(
        executor,
        "wg",
        vec!["show".to_string(), profile.tunnel_interface.clone()],
        "verify WireGuard interface",
    )
}

fn managed_probe_sources(profile: &EgressProfile, family: Family) -> Result<Vec<String>, String> {
    let Some(status) = profile.wireguard.as_ref().filter(|status| status.managed) else {
        return Ok(Vec::new());
    };
    status
        .addresses
        .iter()
        .map(|address| {
            parse_network(address, "stored WireGuard address", false).map_err(|e| e.message)
        })
        .filter_map(|result| match result {
            Ok(network) if network.family() == family => Some(Ok(network.addr.to_string())),
            Ok(_) => None,
            Err(error) => Some(Err(error)),
        })
        .collect()
}

fn configure_health_probe_rules(
    executor: &impl CommandExecutor,
    profile: &EgressProfile,
    family: Family,
) -> Result<(), String> {
    let probe_priority = PROBE_RULE_PRIORITY_BASE + profile.route_table;
    for source in managed_probe_sources(profile, family)? {
        let mut probe_rule = vec![
            family.ip_flag().to_string(),
            "rule".to_string(),
            "del".to_string(),
            "priority".to_string(),
            probe_priority.to_string(),
            "from".to_string(),
            source,
            "table".to_string(),
            profile.route_table.to_string(),
        ];
        let deleted = executor.run("ip", &probe_rule, None)?;
        if !deleted.success
            && !deleted.stderr.contains("No such")
            && !deleted.stderr.contains("Cannot find")
        {
            return Err(concise_error(
                &deleted,
                "remove previous egress health probe rule",
            ));
        }
        probe_rule[2] = "add".to_string();
        run_checked(
            executor,
            "ip",
            probe_rule,
            "configure egress health probe rule",
        )?;
    }
    Ok(())
}

fn configure_profile_routes(
    executor: &impl CommandExecutor,
    application: &ProfileApplication,
) -> Result<(), String> {
    let profile = &application.profile;
    if profile.tunnel_type == "wireguard" {
        configure_wireguard(executor, profile)?;
    }
    for family in &application.families {
        let mut route = vec![
            family.ip_flag().to_string(),
            "route".to_string(),
            "replace".to_string(),
            "table".to_string(),
            profile.route_table.to_string(),
            "default".to_string(),
        ];
        if let Some(gateway) = profile
            .gateway
            .as_deref()
            .and_then(|value| value.parse::<IpAddr>().ok())
            .filter(|ip| ip.is_ipv4() == matches!(family, Family::V4))
        {
            route.extend(["via".to_string(), gateway.to_string()]);
        }
        route.extend(["dev".to_string(), profile.tunnel_interface.clone()]);
        run_checked(executor, "ip", route, "configure egress default route")?;
        let mark = effective_mark(profile.mark);
        let priority = RULE_PRIORITY_BASE + profile.route_table;
        let mut rule = vec![
            family.ip_flag().to_string(),
            "rule".to_string(),
            "del".to_string(),
            "priority".to_string(),
            priority.to_string(),
            "fwmark".to_string(),
            format!("0x{mark:08x}/0xffffffff"),
            "table".to_string(),
            profile.route_table.to_string(),
        ];
        let deleted = executor.run("ip", &rule, None)?;
        if !deleted.success
            && !deleted.stderr.contains("No such")
            && !deleted.stderr.contains("Cannot find")
        {
            return Err(concise_error(
                &deleted,
                "remove previous egress policy rule",
            ));
        }
        rule[2] = "add".to_string();
        run_checked(executor, "ip", rule, "configure egress policy rule")?;
        configure_health_probe_rules(executor, profile, *family)?;
    }
    Ok(())
}

fn probe_bind_target(profile: &EgressProfile, family: Family) -> Result<String, String> {
    if profile
        .wireguard
        .as_ref()
        .is_some_and(|status| status.managed)
    {
        return managed_probe_sources(profile, family)?
            .into_iter()
            .next()
            .ok_or_else(|| {
                format!(
                    "managed WireGuard has no local {} address for health probing",
                    match family {
                        Family::V4 => "IPv4",
                        Family::V6 => "IPv6",
                    }
                )
            });
    }
    Ok(profile.tunnel_interface.clone())
}

fn probe_profile_public_ip(
    executor: &impl CommandExecutor,
    profile: &EgressProfile,
    family: Family,
) -> Result<IpAddr, String> {
    let (family_arg, url) = match family {
        Family::V4 => ("--ipv4", PUBLIC_IPV4_PROBE_URL),
        Family::V6 => ("--ipv6", PUBLIC_IPV6_PROBE_URL),
    };
    let bind_target = probe_bind_target(profile, family)?;
    let args = vec![
        "--silent".to_string(),
        "--show-error".to_string(),
        "--fail".to_string(),
        "--connect-timeout".to_string(),
        "5".to_string(),
        "--max-time".to_string(),
        "15".to_string(),
        family_arg.to_string(),
        "--interface".to_string(),
        bind_target,
        url.to_string(),
    ];
    let result = executor.run("curl", &args, None)?;
    if !result.success {
        return Err(concise_error(
            &result,
            &format!(
                "probe public {} through {}",
                match family {
                    Family::V4 => "IPv4",
                    Family::V6 => "IPv6",
                },
                profile.tunnel_interface
            ),
        ));
    }
    let output = result.stdout.trim();
    if output.is_empty() || output.chars().any(char::is_whitespace) {
        return Err("public egress probe returned a non-scalar address".to_string());
    }
    let observed = output
        .parse::<IpAddr>()
        .map_err(|_| "public egress probe returned an invalid address".to_string())?;
    if observed.is_ipv4() != matches!(family, Family::V4) {
        return Err("public egress probe returned the wrong address family".to_string());
    }
    Ok(observed)
}

fn verify_wireguard_handshake(
    executor: &impl CommandExecutor,
    profile: &EgressProfile,
) -> Result<(), String> {
    let result = executor.run(
        "wg",
        &[
            "show".to_string(),
            profile.tunnel_interface.clone(),
            "latest-handshakes".to_string(),
        ],
        None,
    )?;
    if !result.success {
        return Err(concise_error(&result, "read WireGuard latest handshake"));
    }
    let latest = result
        .stdout
        .lines()
        .filter_map(|line| line.split_whitespace().nth(1))
        .filter_map(|value| value.parse::<i64>().ok())
        .max();
    if !latest.is_some_and(recent_wireguard_handshake) {
        return Err(format!(
            "WireGuard interface {} has no recent handshake",
            profile.tunnel_interface
        ));
    }
    Ok(())
}

fn verify_profile_health(
    executor: &impl CommandExecutor,
    application: &ProfileApplication,
) -> Result<(), String> {
    let profile = &application.profile;
    // The public probe runs first so a newly configured WireGuard peer has
    // traffic that can establish its initial handshake.
    for family in &application.families {
        let expected = match family {
            Family::V4 => profile.public_ipv4.as_deref(),
            Family::V6 => profile.public_ipv6.as_deref(),
        }
        .ok_or_else(|| {
            format!(
                "expected public {} is required for strict egress verification",
                match family {
                    Family::V4 => "IPv4",
                    Family::V6 => "IPv6",
                }
            )
        })?
        .parse::<IpAddr>()
        .map_err(|_| "stored expected public address is invalid".to_string())?;
        let observed = probe_profile_public_ip(executor, profile, *family)?;
        if observed != expected {
            return Err(format!(
                "public egress identity mismatch for {}: expected {}, observed {}",
                match family {
                    Family::V4 => "IPv4",
                    Family::V6 => "IPv6",
                },
                expected,
                observed
            ));
        }
    }
    if profile.tunnel_type == "wireguard" {
        verify_wireguard_handshake(executor, profile)?;
    }
    Ok(())
}

fn cleanup_runtime_profile(
    executor: &impl CommandExecutor,
    runtime: &RuntimeProfile,
    delete_interface: bool,
) -> Result<(), String> {
    let mut errors = Vec::new();
    for (family, enabled) in [(Family::V4, runtime.has_v4), (Family::V6, runtime.has_v6)] {
        if !enabled {
            continue;
        }
        let probe_priority = PROBE_RULE_PRIORITY_BASE + runtime.route_table;
        for source in &runtime.probe_sources {
            let Ok(address) = source.parse::<IpAddr>() else {
                errors.push("stored egress health probe source is invalid".to_string());
                continue;
            };
            if address.is_ipv4() != matches!(family, Family::V4) {
                continue;
            }
            let probe_rule_args = vec![
                family.ip_flag().to_string(),
                "rule".to_string(),
                "del".to_string(),
                "priority".to_string(),
                probe_priority.to_string(),
                "from".to_string(),
                address.to_string(),
                "table".to_string(),
                runtime.route_table.to_string(),
            ];
            if let Ok(result) = executor.run("ip", &probe_rule_args, None)
                && !result.success
                && !result.stderr.contains("No such")
                && !result.stderr.contains("Cannot find")
            {
                errors.push(concise_error(
                    &result,
                    "remove stale egress health probe rule",
                ));
            }
        }
        let mark = effective_mark(runtime.mark);
        let priority = RULE_PRIORITY_BASE + runtime.route_table;
        let rule_args = vec![
            family.ip_flag().to_string(),
            "rule".to_string(),
            "del".to_string(),
            "priority".to_string(),
            priority.to_string(),
            "fwmark".to_string(),
            format!("0x{mark:08x}/0xffffffff"),
            "table".to_string(),
            runtime.route_table.to_string(),
        ];
        if let Ok(result) = executor.run("ip", &rule_args, None) {
            if !result.success
                && !result.stderr.contains("No such")
                && !result.stderr.contains("Cannot find")
            {
                errors.push(concise_error(&result, "remove stale egress rule"));
            }
        }
        let route_args = vec![
            family.ip_flag().to_string(),
            "route".to_string(),
            "del".to_string(),
            "table".to_string(),
            runtime.route_table.to_string(),
            "default".to_string(),
        ];
        if let Ok(result) = executor.run("ip", &route_args, None) {
            if !result.success
                && !result.stderr.contains("No such")
                && !result.stderr.contains("Cannot find")
            {
                errors.push(concise_error(&result, "remove stale egress route"));
            }
        }
    }
    if delete_interface && runtime.managed_interface {
        let args = vec![
            "link".to_string(),
            "delete".to_string(),
            "dev".to_string(),
            runtime.tunnel_interface.clone(),
        ];
        if let Ok(result) = executor.run("ip", &args, None) {
            if !result.success
                && !result.stderr.contains("does not exist")
                && !result.stderr.contains("Cannot find")
            {
                errors.push(concise_error(&result, "remove stale WireGuard interface"));
            }
        }
    }
    if errors.is_empty() {
        Ok(())
    } else {
        Err(errors.join("; "))
    }
}

#[derive(Debug)]
struct ApplyOutcome {
    fail_closed: bool,
    nft_replaced: bool,
    profile_errors: HashMap<String, String>,
    global_errors: Vec<String>,
    runtime: Vec<RuntimeProfile>,
    counters: HashMap<String, u64>,
}

fn apply_prepared(
    executor: &impl CommandExecutor,
    prepared: &PreparedReconcile,
    previous: &[RuntimeProfile],
) -> ApplyOutcome {
    let counters = read_nft_counters(executor);
    let mut outcome = ApplyOutcome {
        fail_closed: prepared.nft_bindings.is_empty(),
        nft_replaced: false,
        profile_errors: HashMap::new(),
        global_errors: Vec::new(),
        runtime: previous.to_vec(),
        counters,
    };
    let table_exists = match nft_table_exists(executor) {
        Ok(value) => value,
        Err(error) => {
            outcome
                .global_errors
                .push(format!("inspect nft egress table: {error}"));
            return outcome;
        }
    };
    if prepared.nft_bindings.is_empty() {
        // Absence of the table is already the correct atomic state.
        outcome.nft_replaced = true;
        if table_exists {
            let args = vec![
                "delete".to_string(),
                "table".to_string(),
                TABLE_FAMILY.to_string(),
                TABLE_NAME.to_string(),
            ];
            match run_checked(executor, "nft", args, "remove empty egress table") {
                Ok(()) => {}
                Err(error) => {
                    outcome.global_errors.push(error);
                    return outcome;
                }
            }
        }
    } else {
        // Install a quarantine table before touching routes or tunnel state.
        // The only transition out of quarantine is a later atomic nft replace
        // after every profile has configured (or been explicitly quarantined).
        let quarantine = build_nft_script(&prepared.nft_bindings, table_exists, true);
        if let Err(error) = apply_nft_script(executor, &quarantine) {
            outcome.global_errors.push(error);
            let emergency = build_nft_script(&prepared.nft_bindings, table_exists, true);
            match apply_nft_script(executor, &emergency) {
                Ok(()) => {
                    outcome.fail_closed = true;
                    outcome.nft_replaced = true;
                }
                Err(emergency_error) => outcome.global_errors.push(format!(
                    "emergency fail-closed nft transaction failed: {emergency_error}"
                )),
            }
            return outcome;
        }
        outcome.fail_closed = true;
        outcome.nft_replaced = true;
    }

    let desired_runtime: HashMap<String, RuntimeProfile> = prepared
        .applications
        .iter()
        .map(|application| {
            let profile = &application.profile;
            let probe_sources = [Family::V4, Family::V6]
                .into_iter()
                .filter(|family| application.families.contains(family))
                .flat_map(|family| managed_probe_sources(profile, family).unwrap_or_default())
                .collect();
            (
                profile.id.clone(),
                RuntimeProfile {
                    profile_id: profile.id.clone(),
                    route_table: profile.route_table,
                    mark: profile.mark,
                    tunnel_interface: profile.tunnel_interface.clone(),
                    has_v4: application.families.contains(&Family::V4),
                    has_v6: application.families.contains(&Family::V6),
                    managed_interface: profile.wireguard.as_ref().is_some_and(|wg| wg.managed),
                    probe_sources,
                },
            )
        })
        .collect();
    let desired_interfaces: HashSet<&str> = desired_runtime
        .values()
        .map(|runtime| runtime.tunnel_interface.as_str())
        .collect();
    let mut retained = Vec::new();
    for old in previous {
        let unchanged = desired_runtime.get(&old.profile_id).is_some_and(|new| {
            old.route_table == new.route_table
                && old.mark == new.mark
                && old.tunnel_interface == new.tunnel_interface
                && old.has_v4 == new.has_v4
                && old.has_v6 == new.has_v6
                && old.managed_interface == new.managed_interface
                && old.probe_sources == new.probe_sources
        });
        if unchanged {
            // Keep the last successfully-applied runtime until the replacement
            // attempt below has completed. A failed re-apply must not turn an
            // existing resource into untracked state.
            retained.push(old.clone());
            continue;
        }
        let delete_interface = !desired_interfaces.contains(old.tunnel_interface.as_str());
        if let Err(error) = cleanup_runtime_profile(executor, old, delete_interface) {
            outcome
                .global_errors
                .push(format!("cleanup profile {}: {error}", old.profile_id));
            retained.push(old.clone());
        }
    }
    for application in &prepared.applications {
        let runtime = desired_runtime
            .get(&application.profile.id)
            .expect("prepared runtime exists")
            .clone();
        let activation = configure_profile_routes(executor, application)
            .and_then(|_| verify_profile_health(executor, application));
        if let Err(error) = activation {
            // nftables is already fail-closed at this point. Roll back any route,
            // rule, or managed WireGuard interface created before the failure. If
            // rollback itself fails, retain the attempted runtime so a later
            // reconciliation can retry cleanup instead of orphaning host state.
            let cleanup_error = cleanup_runtime_profile(executor, &runtime, true).err();
            let message = match cleanup_error {
                Some(cleanup_error) => {
                    retained.retain(|old| old.profile_id != runtime.profile_id);
                    retained.push(runtime);
                    format!("{error}; rollback failed: {cleanup_error}")
                }
                None => error,
            };
            outcome
                .profile_errors
                .insert(application.profile.id.clone(), message);
            continue;
        }
        retained.retain(|old| old.profile_id != runtime.profile_id);
        retained.push(runtime);
    }
    if !prepared.nft_bindings.is_empty() {
        let mut active_bindings = prepared.nft_bindings.clone();
        for binding in &mut active_bindings {
            if outcome.profile_errors.contains_key(&binding.profile_id) {
                binding.quarantine = true;
                binding.tunnel_interface = None;
                binding.effective_mark = None;
            }
        }
        let active_script = build_nft_script(&active_bindings, true, false);
        if let Err(error) = apply_nft_script(executor, &active_script) {
            outcome
                .global_errors
                .push(format!("activate egress nft policy: {error}"));
            // Retain the already-installed quarantine barrier. A best-effort
            // re-apply handles executors/filesystems that partially replaced it.
            let emergency = build_nft_script(&prepared.nft_bindings, true, true);
            if let Err(emergency_error) = apply_nft_script(executor, &emergency) {
                outcome.global_errors.push(format!(
                    "emergency fail-closed nft transaction failed: {emergency_error}"
                ));
            }
        }
    }
    outcome.runtime = retained;
    outcome
}

fn outcome_for_dry_run() -> ApplyOutcome {
    ApplyOutcome {
        fail_closed: false,
        nft_replaced: false,
        profile_errors: HashMap::new(),
        global_errors: Vec::new(),
        runtime: Vec::new(),
        counters: HashMap::new(),
    }
}

fn counter_value(counters: &HashMap<String, u64>, direction: char, instance_id: &str) -> i64 {
    (*counters
        .get(&counter_name(direction, instance_id))
        .unwrap_or(&0))
    .min(i64::MAX as u64) as i64
}

fn persist_reconcile(
    conn: &rusqlite::Connection,
    prepared: &PreparedReconcile,
    outcome: &ApplyOutcome,
    profile_warnings: &HashMap<String, String>,
    apply_requested: bool,
    apply_enabled: bool,
) -> Result<(), ApiError> {
    let now = now_ts();
    let tx = conn
        .unchecked_transaction()
        .map_err(|e| ApiError::internal(format!("begin egress state transaction error: {e}")))?;
    let mut profile_states: HashMap<String, (String, String)> = HashMap::new();
    for plan in &prepared.plans {
        let mut status = plan.status.clone();
        let mut error = plan.error.clone().unwrap_or_default();
        if apply_requested {
            if !apply_enabled {
                status = "blocked".to_string();
                error = format!("{APPLY_ENV}=true is required");
            } else if !outcome.global_errors.is_empty() {
                status = "blocked".to_string();
                error = outcome.global_errors.join("; ");
            } else if outcome.profile_errors.contains_key(&plan.profile_id) {
                status = "blocked".to_string();
                error = outcome
                    .profile_errors
                    .get(&plan.profile_id)
                    .cloned()
                    .unwrap_or_default();
            } else if let Some(warning) = profile_warnings.get(&plan.profile_id) {
                status = "degraded".to_string();
                error = warning.clone();
            } else if status == "planned" {
                status = "applied".to_string();
            }
        }
        if plan.status == "disabled" {
            status = "disabled".to_string();
            error.clear();
        }
        let enforcement = if plan.status == "disabled" || !apply_enabled {
            None
        } else {
            Some(outcome.fail_closed as i64)
        };
        tx.execute("UPDATE egress_bindings SET state=?1,last_error=?2,updated_at=?3,traffic_bytes_in=traffic_bytes_in+?4,traffic_bytes_out=traffic_bytes_out+?5,traffic_bytes_dropped=traffic_bytes_dropped+?6,fail_closed_enforced=CASE WHEN ?7 THEN ?8 ELSE fail_closed_enforced END WHERE instance_id=?9",
			params![&status, &error, now, if outcome.nft_replaced { counter_value(&outcome.counters, 'i', &plan.instance_id) } else { 0 }, if outcome.nft_replaced { counter_value(&outcome.counters, 'o', &plan.instance_id) } else { 0 }, if outcome.nft_replaced { counter_value(&outcome.counters, 'd', &plan.instance_id) } else { 0 }, apply_requested, enforcement, &plan.instance_id])
            .map_err(|e| ApiError::internal(format!("update egress binding state error: {e}")))?;
        let entry = profile_states
            .entry(plan.profile_id.clone())
            .or_insert_with(|| ("disabled".to_string(), String::new()));
        let rank = |value: &str| match value {
            "blocked" => 5,
            "degraded" => 4,
            "applied" => 3,
            "planned" | "pending" => 2,
            "disabled" => 1,
            _ => 0,
        };
        if rank(&status) >= rank(&entry.0) {
            entry.0 = status;
            entry.1 = error;
        }
    }
    for (profile_id, (status, error)) in profile_states {
        tx.execute(
            "UPDATE egress_profiles SET status=?1,last_error=?2,updated_at=?3 WHERE id=?4",
            params![status, error, now, profile_id],
        )
        .map_err(|e| ApiError::internal(format!("update egress profile state error: {e}")))?;
    }
    if apply_requested && apply_enabled && outcome.nft_replaced {
        tx.execute("DELETE FROM egress_runtime_profiles", [])
            .map_err(|e| ApiError::internal(format!("replace egress runtime state error: {e}")))?;
        for runtime in &outcome.runtime {
            tx.execute("INSERT INTO egress_runtime_profiles (profile_id,route_table,mark,tunnel_interface,has_v4,has_v6,managed_interface,probe_sources_json,updated_at) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9)",
                params![&runtime.profile_id, runtime.route_table, runtime.mark, &runtime.tunnel_interface, runtime.has_v4 as i64, runtime.has_v6 as i64, runtime.managed_interface as i64, wg_json(&runtime.probe_sources), now])
                .map_err(|e| ApiError::internal(format!("write egress runtime state error: {e}")))?;
        }
    }
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit egress state transaction error: {e}")))
}

async fn reconcile_state(
    state: AppState,
    apply_requested: bool,
) -> Result<ReconcileResponse, ApiError> {
    let _guard = reconcile_lock().lock().await;
    reconcile_state_locked(state, apply_requested, true).await
}

async fn reconcile_state_locked(
    state: AppState,
    apply_requested: bool,
    persist_exact_sources: bool,
) -> Result<ReconcileResponse, ApiError> {
    let mut capabilities = detect_capabilities();
    if apply_requested
        && capabilities.auto_install_enabled
        && !capabilities.missing_dependencies.is_empty()
    {
        let package_set = {
            let conn = state.conn.lock().await;
            let (profiles, _) = load_desired(&conn)?;
            if profiles
                .iter()
                .any(|row| row.profile.enabled && row.profile.tunnel_type == "wireguard")
            {
                "wireguard"
            } else {
                "native"
            }
        };
        let package_set = package_set.to_string();
        match tokio::task::spawn_blocking(move || install_dependency_set(&package_set)).await {
            Ok(Ok(_)) => capabilities = detect_capabilities(),
            Ok(Err(error)) => capabilities.reasons.push(error.message),
            Err(error) => capabilities
                .reasons
                .push(format!("dependency installer task failed: {error}")),
        }
    }
    let (profiles, bindings, previous) = {
        let conn = state.conn.lock().await;
        let (profiles, bindings) = load_desired(&conn)?;
        let previous = load_runtime(&conn)?;
        (profiles, bindings, previous)
    };
    if apply_requested && capabilities.apply_enabled {
        let kernel_errors = ensure_kernel_prerequisites(&profiles, &bindings);
        capabilities = detect_capabilities();
        capabilities.reasons.extend(kernel_errors);
    }
    let inventory = host_inventory(&SystemExecutor);
    let prepared = prepare_reconcile(
        &profiles,
        &bindings,
        &capabilities,
        &inventory,
        apply_requested,
    );
    let mut outcome = if apply_requested && capabilities.apply_enabled {
        let prepared_for_apply = prepared.clone();
        tokio::task::spawn_blocking(move || {
            apply_prepared(&SystemExecutor, &prepared_for_apply, &previous)
        })
        .await
        .map_err(|e| ApiError::internal(format!("egress apply task failed: {e}")))?
    } else {
        outcome_for_dry_run()
    };
    if outcome.nft_replaced && outcome.profile_errors.is_empty() && outcome.global_errors.is_empty()
    {
        if let Err(error) = clear_boot_quarantine(&SystemExecutor) {
            outcome.global_errors.push(error);
        }
    }
    // Health failures are hard profile errors inside apply_prepared. A profile
    // is never activated merely because its interface exists.
    let profile_warnings: HashMap<String, String> = HashMap::new();
    {
        let conn = state.conn.lock().await;
        persist_reconcile(
            &conn,
            &prepared,
            &outcome,
            &profile_warnings,
            apply_requested,
            capabilities.apply_enabled,
        )?;
        if persist_exact_sources {
            write_managed_sources(&conn)?;
        }
    }
    let mut plans = prepared.plans;
    if apply_requested {
        for plan in &mut plans {
            if plan.status == "disabled" {
                continue;
            }
            if !capabilities.apply_enabled {
                plan.status = "blocked".to_string();
                plan.error = Some(format!("{APPLY_ENV}=true is required"));
            } else if let Some(error) = outcome.profile_errors.get(&plan.profile_id) {
                plan.status = "blocked".to_string();
                plan.error = Some(error.clone());
            } else if let Some(warning) = profile_warnings.get(&plan.profile_id) {
                plan.status = "degraded".to_string();
                plan.error = Some(warning.clone());
            } else if !outcome.global_errors.is_empty() {
                plan.status = "blocked".to_string();
                plan.error = Some(outcome.global_errors.join("; "));
            } else if plan.status == "planned" {
                plan.status = "applied".to_string();
            }
        }
    }
    let all_ok = plans
        .iter()
        .all(|plan| matches!(plan.status.as_str(), "applied" | "disabled"));
    let applied = apply_requested
        && capabilities.apply_enabled
        && outcome.nft_replaced
        && all_ok
        && outcome.global_errors.is_empty();
    let has_enabled = bindings.iter().any(|row| row.binding.enabled);
    let fail_closed = if apply_requested && capabilities.apply_enabled {
        outcome.fail_closed
    } else {
        !has_enabled
    };
    info!(
        apply_requested,
        applied,
        fail_closed,
        plans = plans.len(),
        errors = outcome.global_errors.len(),
        "egress state reconciled"
    );
    Ok(ReconcileResponse {
        applied,
        fail_closed,
        capabilities,
        plans,
        errors: outcome.global_errors,
    })
}

/// Reconcile persisted state after the agent has restarted.  This is called by
/// `main` only after the SQLite schema and AppState are ready.
pub async fn reconcile_startup(state: AppState) {
    let startup_networks = {
        let conn = state.conn.lock().await;
        let mut networks = Vec::new();
        match conn.prepare("SELECT sources_json FROM egress_bindings WHERE enabled=1") {
            Ok(mut stmt) => match stmt.query_map([], |row| row.get::<_, String>(0)) {
                Ok(rows) => {
                    for row in rows {
                        let Ok(encoded) = row else {
                            warn!("startup egress quarantine source row is unreadable");
                            return;
                        };
                        let Ok(values) = serde_json::from_str::<Vec<String>>(&encoded) else {
                            warn!("startup egress quarantine source list is corrupt");
                            return;
                        };
                        for value in values {
                            let Ok(network) = parse_network(&value, "startup managed source", true)
                            else {
                                warn!("startup egress quarantine contains an invalid source");
                                return;
                            };
                            networks.push(network);
                        }
                    }
                    networks
                }
                Err(error) => {
                    warn!(%error, "startup egress quarantine query failed");
                    return;
                }
            },
            Err(error) => {
                warn!(%error, "startup egress quarantine prepare failed");
                return;
            }
        }
    };
    if !startup_networks.is_empty() {
        if let Err(error) = install_staging_quarantine(&SystemExecutor, &startup_networks) {
            warn!(%error, "startup egress quarantine failed; reconciliation is not allowed to activate traffic");
            return;
        }
    }
    if !env_enabled(APPLY_ENV) {
        warn!("startup egress apply guard is disabled; managed sources remain quarantined");
        return;
    }
    if let Err(error) = reconcile_state(state, true).await {
        warn!(error = %error.message, "startup egress reconciliation failed");
    }
}

pub async fn reconcile(
    State(state): State<AppState>,
    Json(req): Json<ReconcileRequest>,
) -> Result<Json<ReconcileResponse>, ApiError> {
    Ok(Json(
        reconcile_state(state, req.apply.unwrap_or(false)).await?,
    ))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    fn capabilities() -> HostCapabilities {
        HostCapabilities {
            supported: true,
            mode: "native".to_string(),
            running_as_root: true,
            ip_available: true,
            nft_available: true,
            wireguard_available: true,
            curl_available: true,
            ipv4_forwarding: true,
            ipv6_forwarding: true,
            apply_enabled: true,
            auto_install_enabled: false,
            package_manager: Some("apt-get".to_string()),
            missing_dependencies: Vec::new(),
            checked_at: 0,
            reasons: Vec::new(),
        }
    }

    fn gateway_profile() -> ProfileRow {
        ProfileRow {
            profile: EgressProfile {
                id: "profile-1".to_string(),
                mode: "native".to_string(),
                tunnel_type: "gateway".to_string(),
                tunnel_interface: "tun0".to_string(),
                gateway: None,
                route_table: 100,
                mark: 7,
                public_ipv4: Some("192.0.2.10".to_string()),
                public_ipv6: Some("2001:db8::10".to_string()),
                enabled: true,
                fail_closed: true,
                status: "pending".to_string(),
                last_error: None,
                updated_at: 0,
                wireguard: None,
                tunnel_ready: true,
                last_handshake_at: None,
            },
        }
    }

    fn gateway_profile_request(id: &str) -> EgressProfileRequest {
        EgressProfileRequest {
            id: id.to_string(),
            mode: "native".to_string(),
            tunnel_type: Some("gateway".to_string()),
            tunnel_interface: "tun0".to_string(),
            gateway: None,
            route_table: Some(100),
            mark: Some(7),
            public_ipv4: Some("192.0.2.10".to_string()),
            public_ipv6: None,
            enabled: Some(true),
            fail_closed: Some(true),
            wireguard: None,
        }
    }

    fn managed_wireguard_profile() -> EgressProfile {
        let mut profile = gateway_profile().profile;
        profile.tunnel_type = "wireguard".to_string();
        profile.tunnel_interface = "wg-ocv".to_string();
        let mut status = default_wg_status();
        status.managed = true;
        status.addresses = vec![
            "10.250.0.2/24".to_string(),
            "2001:db8:250::2/64".to_string(),
        ];
        profile.wireguard = Some(status);
        profile
    }

    fn binding_request(instance_id: &str, profile_id: &str) -> EgressBindingRequest {
        EgressBindingRequest {
            instance_id: instance_id.to_string(),
            profile_id: profile_id.to_string(),
            source: "10.0.0.2".to_string(),
            sources: None,
            interface: Some("veth100".to_string()),
            interface_v4: None,
            interface_v6: None,
            enabled: Some(true),
        }
    }

    fn binding(instance_id: &str, source: &str, additional: &[&str]) -> BindingRow {
        binding_from_request(EgressBindingRequest {
            instance_id: instance_id.to_string(),
            profile_id: "profile-1".to_string(),
            source: source.to_string(),
            sources: Some(
                additional
                    .iter()
                    .map(|value| (*value).to_string())
                    .collect(),
            ),
            interface: Some("veth100".to_string()),
            interface_v4: Some("veth100".to_string()),
            interface_v6: Some("veth100".to_string()),
            enabled: Some(true),
        })
        .unwrap()
    }

    fn inventory() -> HostInventory {
        HostInventory {
            interfaces: HashSet::from(["tun0".to_string(), "veth100".to_string()]),
            ..HostInventory::default()
        }
    }

    #[test]
    fn rejects_control_characters_and_command_syntax() {
        assert!(parse_network("2001:db8::1/80\n64", "source", true).is_err());
        assert!(parse_network("2001:db8::1/80'", "source", true).is_err());
        assert!(validate_interface("wg0;reboot", "interface").is_err());
        assert!(validate_endpoint("vpn.example.com:51820\nPostUp=x").is_err());
        assert!(validate_key("not-a-wireguard-key", "key").is_err());
    }

    #[test]
    fn accepts_discrete_and_small_ipv6_networks() {
        assert_eq!(
            parse_network("2001:db8::42", "source", true)
                .unwrap()
                .to_string(),
            "2001:db8::42/128"
        );
        assert_eq!(
            parse_network("2001:db8:1:2:3::42/80", "source", true)
                .unwrap()
                .to_string(),
            "2001:db8:1:2:3::/80"
        );
        assert_eq!(
            parse_network("2001:db8::/127", "source", true)
                .unwrap()
                .to_string(),
            "2001:db8::/127"
        );
    }

    #[test]
    fn replacement_rejects_duplicates_and_missing_profile_references() {
        let duplicate_profiles = normalize_replace_state_request(ReplaceStateRequest {
            profiles: vec![
                gateway_profile_request("profile-1"),
                gateway_profile_request("profile-1"),
            ],
            bindings: Vec::new(),
            apply: Some(true),
        })
        .unwrap_err();
        assert!(
            duplicate_profiles
                .message
                .contains("duplicate egress profile")
        );

        let duplicate_bindings = normalize_replace_state_request(ReplaceStateRequest {
            profiles: vec![gateway_profile_request("profile-1")],
            bindings: vec![
                binding_request("instance-1", "profile-1"),
                binding_request("instance-1", "profile-1"),
            ],
            apply: Some(true),
        })
        .unwrap_err();
        assert!(
            duplicate_bindings
                .message
                .contains("duplicate egress binding")
        );

        let missing_profile = normalize_replace_state_request(ReplaceStateRequest {
            profiles: vec![gateway_profile_request("profile-1")],
            bindings: vec![binding_request("instance-1", "profile-2")],
            apply: Some(true),
        })
        .unwrap_err();
        assert!(missing_profile.message.contains("outside this state batch"));
    }

    #[test]
    fn empty_replacement_removes_all_desired_state_in_one_transaction() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        upsert_profile_row(&conn, &gateway_profile().profile).unwrap();
        upsert_binding_row(&conn, &binding("instance-1", "10.0.0.2", &[])).unwrap();

        replace_desired_state_sql(&conn, &[], &[]).unwrap();

        let profile_count: i64 = conn
            .query_row("SELECT COUNT(*) FROM egress_profiles", [], |row| row.get(0))
            .unwrap();
        let binding_count: i64 = conn
            .query_row("SELECT COUNT(*) FROM egress_bindings", [], |row| row.get(0))
            .unwrap();
        assert_eq!((profile_count, binding_count), (0, 0));
    }

    #[test]
    fn failed_replacement_transaction_preserves_previous_state() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        let previous = gateway_profile().profile;
        upsert_profile_row(&conn, &previous).unwrap();
        let mut first = previous.clone();
        first.tunnel_interface = "tun1".to_string();
        let mut duplicate = first.clone();
        duplicate.tunnel_interface = "tun2".to_string();

        assert!(replace_desired_state_sql(&conn, &[first, duplicate], &[]).is_err());
        let stored: String = conn
            .query_row(
                "SELECT tunnel_interface FROM egress_profiles WHERE id='profile-1'",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(stored, "tun0");
    }

    #[test]
    fn keeps_wireguard_interface_host_bits() {
        let values = validate_vec_networks(
            Some(vec![
                "10.8.0.2/24".to_string(),
                "2001:db8::2/64".to_string(),
            ]),
            "address",
            &[],
            true,
            false,
        )
        .unwrap();
        assert_eq!(values, vec!["10.8.0.2/24", "2001:db8::2/64"]);
    }

    #[test]
    fn dual_stack_binding_uses_one_profile_without_leak_family() {
        let row = binding("instance-1", "10.0.0.2", &["2001:db8::2"]);
        assert_eq!(row.networks.len(), 2);
        assert!(
            row.networks
                .iter()
                .any(|network| network.family() == Family::V4)
        );
        assert!(
            row.networks
                .iter()
                .any(|network| network.family() == Family::V6)
        );
        assert_eq!(row.binding.sources, vec!["2001:db8::2/128", "10.0.0.2/32"]);
    }

    #[test]
    fn nft_plan_classifies_forwarded_dual_stack_and_blocks_local_dns() {
        let profiles = vec![gateway_profile()];
        let mut row = binding("instance-1", "10.0.0.2", &["2001:db8::2"]);
        row.binding.interface_v6 = Some("veth200".to_string());
        let bindings = vec![row];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        assert_eq!(prepared.plans[0].status, "planned");
        let script = build_nft_script(&prepared.nft_bindings, false, false);
        assert!(script.contains("hook prerouting priority -150"));
        assert!(script.contains("hook forward priority 0"));
        assert!(script.contains("ip saddr 10.0.0.2/32"));
        assert!(script.contains("ip6 saddr 2001:db8::2/128"));
        assert!(script.contains("udp sport 68 udp dport 67 accept"));
        assert!(script.contains("udp sport 546 udp dport 547 accept"));
        assert!(script.contains("icmpv6 type { 133, 135, 136 } accept"));
        assert!(script.contains("enforce_input ip saddr 10.0.0.2/32"));
        assert!(!script.contains("classify_output"));
        assert!(script.contains("oifname != \"tun0\""));
        assert!(script.contains("enforce_forward iifname \"veth100\" meta nfproto ipv4 drop"));
        assert!(script.contains("classify_prerouting iifname \"veth200\" ip6 saddr"));
        assert!(script.contains("enforce_forward iifname \"veth200\" meta nfproto ipv6 drop"));
        assert!(script.contains("enforce_input iifname \"veth100\" meta nfproto ipv4 drop"));
        assert!(script.contains("enforce_input iifname \"veth200\" meta nfproto ipv6 drop"));
        assert!(script.contains("drop"));
    }

    #[test]
    fn overlapping_sources_fail_closed_for_both_bindings() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![
            binding("instance-1", "10.0.0.0/24", &[]),
            binding("instance-2", "10.0.0.4", &[]),
        ];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        assert!(prepared.plans.iter().all(|plan| plan.status == "blocked"));
        assert!(
            prepared
                .nft_bindings
                .iter()
                .all(|binding| binding.quarantine)
        );
    }

    #[derive(Default)]
    struct RecordingExecutor {
        calls: Mutex<Vec<(String, Vec<String>, Option<String>)>>,
    }

    impl CommandExecutor for RecordingExecutor {
        fn run(
            &self,
            program: &str,
            args: &[String],
            input: Option<&str>,
        ) -> Result<CommandResult, String> {
            self.calls.lock().unwrap().push((
                program.to_string(),
                args.to_vec(),
                input.map(str::to_string),
            ));
            let is_list = program == "nft" && args.iter().any(|arg| arg == "list");
            let stdout = if program == "curl" && args.iter().any(|arg| arg == "--ipv4") {
                "192.0.2.10\n".to_string()
            } else if program == "curl" && args.iter().any(|arg| arg == "--ipv6") {
                "2001:db8::10\n".to_string()
            } else {
                String::new()
            };
            Ok(CommandResult {
                success: !is_list,
                stdout,
                stderr: if is_list {
                    "No such file or directory".to_string()
                } else {
                    String::new()
                },
            })
        }
    }

    #[derive(Default)]
    struct PublicIpMismatchExecutor {
        calls: Mutex<Vec<(String, Vec<String>, Option<String>)>>,
    }

    impl CommandExecutor for PublicIpMismatchExecutor {
        fn run(
            &self,
            program: &str,
            args: &[String],
            input: Option<&str>,
        ) -> Result<CommandResult, String> {
            self.calls.lock().unwrap().push((
                program.to_string(),
                args.to_vec(),
                input.map(str::to_string),
            ));
            let is_list = program == "nft" && args.iter().any(|arg| arg == "list");
            let stdout = if program == "curl" {
                "198.51.100.99\n".to_string()
            } else {
                String::new()
            };
            Ok(CommandResult {
                success: !is_list,
                stdout,
                stderr: if is_list {
                    "No such file or directory".to_string()
                } else {
                    String::new()
                },
            })
        }
    }

    struct FailingStagingExecutor;

    impl CommandExecutor for FailingStagingExecutor {
        fn run(
            &self,
            program: &str,
            args: &[String],
            _input: Option<&str>,
        ) -> Result<CommandResult, String> {
            let is_list = program == "nft" && args.iter().any(|arg| arg == "list");
            Ok(CommandResult {
                success: is_list,
                stdout: String::new(),
                stderr: if is_list {
                    String::new()
                } else {
                    "injected staging quarantine failure".to_string()
                },
            })
        }
    }

    fn test_managed_sources_path(label: &str) -> PathBuf {
        env::temp_dir()
            .join(format!(
                "oneclickvirt-egress-binding-{label}-{}-{}",
                std::process::id(),
                now_ts()
            ))
            .join("managed-sources")
    }

    #[test]
    fn binding_put_stages_quarantine_before_commit_and_acknowledges_enforcement() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute(
            "INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)",
            [],
        )
        .unwrap();
        let path = test_managed_sources_path("success");
        let row = binding("instance-1", "10.0.0.2", &[]);
        let executor = RecordingExecutor::default();
        let saved = persist_binding_with_quarantine(&conn, &executor, row, &path).unwrap();

        assert_eq!(saved.state, "pending");
        assert_eq!(saved.fail_closed_enforced, Some(true));
        assert_eq!(fs::read_to_string(&path).unwrap(), "10.0.0.2/32\n");
        let count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM egress_bindings WHERE instance_id='instance-1'",
                [],
                |value| value.get(0),
            )
            .unwrap();
        assert_eq!(count, 1);
        let scripts: Vec<String> = executor
            .calls
            .lock()
            .unwrap()
            .iter()
            .filter(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .filter_map(|(_, _, input)| input.clone())
            .collect();
        assert_eq!(
            scripts.len(),
            1,
            "nft must apply the validated staging transaction"
        );
        assert!(scripts[0].contains("oneclickvirt_egress_boot"));
        assert!(scripts[0].contains("boot_forward ip saddr 10.0.0.2/32 counter drop"));
        assert!(scripts[0].contains("boot_output ip saddr 10.0.0.2/32 counter drop"));
        fs::remove_dir_all(path.parent().unwrap()).unwrap();
    }

    #[test]
    fn binding_put_does_not_commit_when_staging_quarantine_fails() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute(
            "INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)",
            [],
        )
        .unwrap();
        let path = test_managed_sources_path("failure");
        let row = binding("instance-1", "10.0.0.2", &[]);
        let error = persist_binding_with_quarantine(&conn, &FailingStagingExecutor, row, &path)
            .expect_err("failed quarantine must reject the binding PUT");
        assert!(error.message.contains("staging quarantine"));
        let count: i64 = conn
            .query_row("SELECT COUNT(*) FROM egress_bindings", [], |value| {
                value.get(0)
            })
            .unwrap();
        assert_eq!(count, 0);
        assert!(!path.exists());
    }

    #[test]
    fn binding_update_stages_old_and_new_sources_until_reconcile() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute(
            "INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)",
            [],
        )
        .unwrap();
        conn.execute(
            "INSERT INTO egress_bindings (instance_id,profile_id,source,interface,enabled,state,last_error,created_at,updated_at,sources_json,fail_closed_enforced) VALUES ('instance-1','profile-1','10.0.0.2/32','veth100',1,'applied','',1,1,'[\"10.0.0.2/32\"]',1)",
            [],
        )
        .unwrap();
        let path = test_managed_sources_path("update");
        let row = binding("instance-1", "10.0.0.3", &[]);
        let executor = RecordingExecutor::default();
        let saved = persist_binding_with_quarantine(&conn, &executor, row, &path).unwrap();
        assert_eq!(saved.fail_closed_enforced, Some(true));
        let scripts: Vec<String> = executor
            .calls
            .lock()
            .unwrap()
            .iter()
            .filter(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .filter_map(|(_, _, input)| input.clone())
            .collect();
        assert!(scripts[0].contains("saddr 10.0.0.2/32 counter drop"));
        assert!(scripts[0].contains("saddr 10.0.0.3/32 counter drop"));
        fs::remove_dir_all(path.parent().unwrap()).unwrap();
    }

    struct MidProfileFailureExecutor {
        calls: Mutex<Vec<(String, Vec<String>, Option<String>)>>,
        failed: Mutex<bool>,
        fail_cleanup: bool,
    }

    impl MidProfileFailureExecutor {
        fn new(fail_cleanup: bool) -> Self {
            Self {
                calls: Mutex::new(Vec::new()),
                failed: Mutex::new(false),
                fail_cleanup,
            }
        }
    }

    impl CommandExecutor for MidProfileFailureExecutor {
        fn run(
            &self,
            program: &str,
            args: &[String],
            input: Option<&str>,
        ) -> Result<CommandResult, String> {
            self.calls.lock().unwrap().push((
                program.to_string(),
                args.to_vec(),
                input.map(str::to_string),
            ));
            let is_nft_list = program == "nft" && args.iter().any(|arg| arg == "list");
            let is_rule_add =
                program == "ip" && args.windows(2).any(|window| window == ["rule", "add"]);
            let failed_before = *self.failed.lock().unwrap();
            let is_failed_cleanup = self.fail_cleanup
                && failed_before
                && program == "ip"
                && args.windows(2).any(|window| window == ["route", "del"]);
            if is_rule_add {
                *self.failed.lock().unwrap() = true;
            }
            let success = !(is_nft_list || is_rule_add || is_failed_cleanup);
            Ok(CommandResult {
                success,
                stdout: String::new(),
                stderr: if is_nft_list {
                    "No such file or directory".to_string()
                } else if is_rule_add {
                    "injected policy rule failure".to_string()
                } else if is_failed_cleanup {
                    "injected rollback failure".to_string()
                } else {
                    String::new()
                },
            })
        }
    }

    #[test]
    fn apply_installs_atomic_kill_switch_before_routes_and_uses_rule_del_add() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![binding("instance-1", "10.0.0.2", &["2001:db8::2"])];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        let executor = RecordingExecutor::default();
        let outcome = apply_prepared(&executor, &prepared, &[]);
        assert!(outcome.fail_closed);
        assert!(outcome.profile_errors.is_empty());
        let calls = executor.calls.lock().unwrap();
        let nft_apply = calls
            .iter()
            .position(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .unwrap();
        let first_ip = calls
            .iter()
            .position(|(program, _, _)| program == "ip")
            .unwrap();
        assert!(nft_apply < first_ip);
        let nft_scripts: Vec<&str> = calls
            .iter()
            .filter(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .filter_map(|(_, _, input)| input.as_deref())
            .collect();
        assert_eq!(nft_scripts.len(), 2);
        assert!(!nft_scripts[0].contains("meta mark set"));
        assert!(nft_scripts[0].contains("enforce_forward ip saddr 10.0.0.2/32"));
        assert!(nft_scripts[1].contains("meta mark set"));
        assert!(
            calls
                .iter()
                .all(|(program, _, _)| !matches!(program.as_str(), "sh" | "bash" | "zsh"))
        );
        assert!(calls.iter().any(|(program, args, _)| program == "ip"
            && args.windows(2).any(|window| window == ["rule", "del"])));
        assert!(calls.iter().any(|(program, args, _)| program == "ip"
            && args.windows(2).any(|window| window == ["rule", "add"])));
        assert!(!calls.iter().any(|(program, args, _)| program == "ip"
            && args.windows(2).any(|window| window == ["rule", "replace"])));
    }

    #[test]
    fn managed_wireguard_probe_rules_bind_and_cleanup_by_source() {
        let profile = managed_wireguard_profile();
        let executor = RecordingExecutor::default();
        configure_health_probe_rules(&executor, &profile, Family::V4).unwrap();
        configure_health_probe_rules(&executor, &profile, Family::V6).unwrap();
        assert_eq!(
            probe_profile_public_ip(&executor, &profile, Family::V4).unwrap(),
            "192.0.2.10".parse::<IpAddr>().unwrap()
        );
        assert_eq!(
            probe_profile_public_ip(&executor, &profile, Family::V6).unwrap(),
            "2001:db8::10".parse::<IpAddr>().unwrap()
        );

        let runtime = RuntimeProfile {
            profile_id: profile.id.clone(),
            route_table: profile.route_table,
            mark: profile.mark,
            tunnel_interface: profile.tunnel_interface.clone(),
            has_v4: true,
            has_v6: true,
            managed_interface: false,
            probe_sources: vec!["10.250.0.2".to_string(), "2001:db8:250::2".to_string()],
        };
        let cleanup_start = executor.calls.lock().unwrap().len();
        cleanup_runtime_profile(&executor, &runtime, false).unwrap();

        let calls = executor.calls.lock().unwrap();
        let priority = (PROBE_RULE_PRIORITY_BASE + profile.route_table).to_string();
        for (family, source) in [("-4", "10.250.0.2"), ("-6", "2001:db8:250::2")] {
            assert!(calls.iter().any(|(program, args, _)| {
                program == "ip"
                    && args
                        == &[
                            family,
                            "rule",
                            "add",
                            "priority",
                            priority.as_str(),
                            "from",
                            source,
                            "table",
                            "100",
                        ]
            }));
            assert!(calls[cleanup_start..].iter().any(|(program, args, _)| {
                program == "ip"
                    && args
                        == &[
                            family,
                            "rule",
                            "del",
                            "priority",
                            priority.as_str(),
                            "from",
                            source,
                            "table",
                            "100",
                        ]
            }));
        }
        assert!(calls.iter().any(|(program, args, _)| {
            program == "curl"
                && args
                    .windows(2)
                    .any(|window| window == ["--interface", "10.250.0.2"])
        }));
        assert!(calls.iter().any(|(program, args, _)| {
            program == "curl"
                && args
                    .windows(2)
                    .any(|window| window == ["--interface", "2001:db8:250::2"])
        }));
    }

    #[test]
    fn public_identity_mismatch_keeps_profile_quarantined() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![binding("instance-1", "10.0.0.2", &[])];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        let executor = PublicIpMismatchExecutor::default();
        let outcome = apply_prepared(&executor, &prepared, &[]);
        assert!(outcome.fail_closed);
        assert!(
            outcome
                .profile_errors
                .get("profile-1")
                .is_some_and(|error| error.contains("identity mismatch"))
        );
        let calls = executor.calls.lock().unwrap();
        let final_nft = calls
            .iter()
            .rev()
            .find(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .and_then(|(_, _, input)| input.as_deref())
            .unwrap();
        assert!(!final_nft.contains("meta mark set"));
        assert!(final_nft.contains("ip saddr 10.0.0.2/32 counter name"));
        assert!(final_nft.contains("drop"));
    }

    #[test]
    fn failed_profile_apply_rolls_back_before_omitting_runtime_state() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![binding("instance-1", "10.0.0.2", &[])];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        let executor = MidProfileFailureExecutor::new(false);
        let outcome = apply_prepared(&executor, &prepared, &[]);
        assert!(outcome.fail_closed);
        assert!(outcome.profile_errors.contains_key("profile-1"));
        assert!(outcome.runtime.is_empty());

        let calls = executor.calls.lock().unwrap();
        let failed_add = calls
            .iter()
            .position(|(program, args, _)| {
                program == "ip" && args.windows(2).any(|window| window == ["rule", "add"])
            })
            .unwrap();
        let cleanup_rule = calls
            .iter()
            .rposition(|(program, args, _)| {
                program == "ip" && args.windows(2).any(|window| window == ["rule", "del"])
            })
            .unwrap();
        let cleanup_route = calls
            .iter()
            .position(|(program, args, _)| {
                program == "ip" && args.windows(2).any(|window| window == ["route", "del"])
            })
            .unwrap();
        assert!(failed_add < cleanup_rule);
        assert!(cleanup_rule < cleanup_route);
        let final_nft = calls
            .iter()
            .rev()
            .find(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .and_then(|(_, _, input)| input.as_deref())
            .unwrap();
        assert!(!final_nft.contains("meta mark set"));
        assert!(final_nft.contains("enforce_forward ip saddr 10.0.0.2/32"));
    }

    #[test]
    fn failed_profile_rollback_retains_runtime_for_later_cleanup() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![binding("instance-1", "10.0.0.2", &[])];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        let executor = MidProfileFailureExecutor::new(true);
        let outcome = apply_prepared(&executor, &prepared, &[]);
        assert_eq!(outcome.runtime.len(), 1);
        assert_eq!(outcome.runtime[0].profile_id, "profile-1");
        assert!(
            outcome
                .profile_errors
                .get("profile-1")
                .is_some_and(|error| error.contains("rollback failed"))
        );
    }

    #[test]
    fn native_binding_without_ingress_interface_stays_quarantined() {
        let profiles = vec![gateway_profile()];
        let mut row = binding("instance-1", "10.0.0.2", &[]);
        row.binding.interface = None;
        row.binding.interface_v4 = None;
        let prepared = prepare_reconcile(&profiles, &[row], &capabilities(), &inventory(), true);
        assert_eq!(prepared.plans[0].status, "blocked");
        assert!(
            prepared.plans[0]
                .error
                .as_deref()
                .is_some_and(|error| error.contains("ingress interface"))
        );
        assert!(
            prepared
                .nft_bindings
                .iter()
                .all(|binding| binding.quarantine)
        );
    }

    #[test]
    fn dependency_plans_cover_supported_package_managers() {
        for manager in ["apt-get", "dnf", "yum", "apk", "pacman", "zypper"] {
            let plan = dependency_command_plan(manager, true).unwrap();
            let flattened = plan.concat();
            assert!(flattened.iter().any(|value| value == "nftables"));
            assert!(flattened.iter().any(|value| value == "wireguard-tools"));
            assert!(!flattened.iter().any(|value| value.contains(';')));
        }
        let apt = dependency_command_plan("apt-get", false).unwrap();
        assert_eq!(apt[0], ["update", "-qq"]);
        assert!(apt[1].starts_with(&[
            "install".to_string(),
            "-y".to_string(),
            "--no-install-recommends".to_string()
        ]));
        assert_eq!(
            dependency_command_plan("pacman", false).unwrap()[0][..2],
            ["-Sy", "--noconfirm"]
        );
        assert_eq!(
            dependency_command_plan("zypper", false).unwrap()[0][..2],
            ["--non-interactive", "install"]
        );
    }

    #[test]
    fn counter_snapshot_is_parsed_once_by_name() {
        let value: Value = serde_json::json!({"nftables":[{"counter":{"name":"ocv_o_deadbeef","packets":2,"bytes":1234}}]});
        let mut counters = HashMap::new();
        collect_nft_counters(&value, &mut counters);
        assert_eq!(counters.get("ocv_o_deadbeef"), Some(&1234));
    }

    #[test]
    fn database_migration_is_idempotent() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        init_db(&conn).unwrap();
        let sources_column: i64 = conn.query_row("SELECT COUNT(*) FROM pragma_table_info('egress_bindings') WHERE name='sources_json'", [], |row| row.get(0)).unwrap();
        let enforcement_column: i64 = conn.query_row("SELECT COUNT(*) FROM pragma_table_info('egress_bindings') WHERE name='fail_closed_enforced'", [], |row| row.get(0)).unwrap();
        let runtime_table: i64 = conn.query_row("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='egress_runtime_profiles'", [], |row| row.get(0)).unwrap();
        let probe_sources_column: i64 = conn.query_row("SELECT COUNT(*) FROM pragma_table_info('egress_runtime_profiles') WHERE name='probe_sources_json'", [], |row| row.get(0)).unwrap();
        assert_eq!(sources_column, 1);
        assert_eq!(enforcement_column, 1);
        assert_eq!(runtime_table, 1);
        assert_eq!(probe_sources_column, 1);
    }

    #[test]
    fn managed_sources_file_is_canonical_sorted_and_private() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute("INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)", []).unwrap();
        conn.execute("INSERT INTO egress_bindings (instance_id,profile_id,source,interface,enabled,created_at,updated_at,sources_json) VALUES ('instance-1','profile-1','10.0.0.2/32','veth100',1,1,1,'[\"2001:db8::2/128\",\"10.0.0.2/32\"]')", []).unwrap();
        let directory = env::temp_dir().join(format!(
            "oneclickvirt-egress-test-{}-{}",
            std::process::id(),
            now_ts()
        ));
        let path = directory.join("managed-sources");
        write_managed_sources_at(&conn, &path).unwrap();
        assert_eq!(
            fs::read_to_string(&path).unwrap(),
            "10.0.0.2/32\n2001:db8::2/128\n"
        );
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        conn.execute("UPDATE egress_bindings SET enabled=0", [])
            .unwrap();
        write_managed_sources_at(&conn, &path).unwrap();
        assert_eq!(fs::read_to_string(&path).unwrap(), "");
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn reconcile_persists_observed_fail_closed_enforcement() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute("INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)", []).unwrap();
        conn.execute("INSERT INTO egress_bindings (instance_id,profile_id,source,interface,enabled,created_at,updated_at,sources_json) VALUES ('instance-1','profile-1','10.0.0.2/32','veth100',1,1,1,'[\"10.0.0.2/32\"]')", []).unwrap();
        let prepared = PreparedReconcile {
            plans: vec![RoutePlan {
                instance_id: "instance-1".to_string(),
                profile_id: "profile-1".to_string(),
                status: "planned".to_string(),
                commands: Vec::new(),
                error: None,
            }],
            nft_bindings: Vec::new(),
            applications: Vec::new(),
        };
        let outcome = ApplyOutcome {
            fail_closed: true,
            nft_replaced: true,
            profile_errors: HashMap::new(),
            global_errors: Vec::new(),
            runtime: Vec::new(),
            counters: HashMap::new(),
        };
        persist_reconcile(&conn, &prepared, &outcome, &HashMap::new(), true, true).unwrap();
        let enforced: Option<i64> = conn
            .query_row(
                "SELECT fail_closed_enforced FROM egress_bindings WHERE instance_id='instance-1'",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(enforced, Some(1));
    }

    #[test]
    fn profile_serialization_never_contains_private_material() {
        let profile = gateway_profile().profile;
        let json = serde_json::to_string(&profile).unwrap();
        assert!(!json.contains("private_key"));
        assert!(!json.contains("preshared_key"));
    }
}
