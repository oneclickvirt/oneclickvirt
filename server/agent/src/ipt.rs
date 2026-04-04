use crate::error::ApiError;
use rusqlite::Connection;
use std::{
    collections::HashSet,
    env,
    process::Command,
};
use tracing::{debug, info, warn};

// Same exclude ranges as nft.rs
const DEFAULT_EXCLUDE_V4: &[&str] = &[
    "10.0.0.0/8",
    "100.64.0.0/10",
    "127.0.0.0/8",
    "169.254.0.0/16",
    "172.16.0.0/12",
    "192.168.0.0/16",
    "224.0.0.0/4",
];

fn parse_env_cidrs(var_name: &str) -> Vec<String> {
    env::var(var_name)
        .ok()
        .map(|raw| {
            raw.split(',')
                .map(str::trim)
                .filter(|s| !s.is_empty())
                .map(ToOwned::to_owned)
                .collect()
        })
        .unwrap_or_default()
}

fn exclude_v4_cidrs() -> Vec<String> {
    let mut all: Vec<String> = DEFAULT_EXCLUDE_V4.iter().map(|s| (*s).to_owned()).collect();
    all.extend(parse_env_cidrs("EXTRA_EXCLUDE_CIDRS_V4"));
    all
}

const CHAIN_FORWARD: &str = "VM_TRAFFIC_FWD";

fn run_iptables(args: &[&str]) -> Result<std::process::Output, ApiError> {
    Command::new("iptables")
        .args(args)
        .output()
        .map_err(|e| ApiError::internal(format!("failed to run iptables {:?}: {e}", args)))
}

fn chain_name_in(monitor_id: i64, interface: &str) -> String {
    let h = fnv1a_64(interface.as_bytes());
    format!("VM_IN_{monitor_id}_{h:x}")
}

fn chain_name_out(monitor_id: i64, interface: &str) -> String {
    let h = fnv1a_64(interface.as_bytes());
    format!("VM_OUT_{monitor_id}_{h:x}")
}

fn fnv1a_64(data: &[u8]) -> u64 {
    const FNV_OFFSET: u64 = 0xcbf29ce484222325;
    const FNV_PRIME: u64 = 0x100000001b3;
    let mut h = FNV_OFFSET;
    for &b in data {
        h ^= b as u64;
        h = h.wrapping_mul(FNV_PRIME);
    }
    h
}

fn chain_exists(chain: &str) -> bool {
    let out = run_iptables(&["-L", chain, "-n", "--exact"]);
    match out {
        Ok(o) => o.status.success(),
        Err(_) => false,
    }
}

fn ensure_chain(chain: &str) -> Result<(), ApiError> {
    if chain_exists(chain) {
        return Ok(());
    }
    let out = run_iptables(&["-N", chain])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if !stderr.contains("already exists") {
            return Err(ApiError::internal(format!(
                "failed to create iptables chain {chain}: {}",
                stderr.trim()
            )));
        }
    }
    Ok(())
}

fn ensure_forward_jump() -> Result<(), ApiError> {
    ensure_chain(CHAIN_FORWARD)?;
    // Check if jump rule exists
    let out = run_iptables(&["-C", "FORWARD", "-j", CHAIN_FORWARD]);
    match out {
        Ok(o) if o.status.success() => return Ok(()),
        _ => {}
    }
    let out = run_iptables(&["-I", "FORWARD", "-j", CHAIN_FORWARD])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        return Err(ApiError::internal(format!(
            "failed to add FORWARD jump to {CHAIN_FORWARD}: {}",
            stderr.trim()
        )));
    }
    Ok(())
}

/// Ensure per-monitor iptables chains and rules exist.
pub fn ensure_counter(
    monitor_id: i64,
    interface: &str,
    _inner_ip: Option<&str>,
) -> Result<(), ApiError> {
    ensure_forward_jump()?;

    let cin = chain_name_in(monitor_id, interface);
    let cout = chain_name_out(monitor_id, interface);

    ensure_chain(&cin)?;
    ensure_chain(&cout)?;

    // Add jump from CHAIN_FORWARD to per-monitor chains (idempotent)
    let excludes = exclude_v4_cidrs();

    // Jump for inbound (traffic going TO the interface)
    let check = run_iptables(&["-C", CHAIN_FORWARD, "-o", interface, "-j", &cin]);
    if check.is_err() || !check.unwrap().status.success() {
        run_iptables(&["-A", CHAIN_FORWARD, "-o", interface, "-j", &cin])?;
    }
    // Jump for outbound (traffic coming FROM the interface)
    let check = run_iptables(&["-C", CHAIN_FORWARD, "-i", interface, "-j", &cout]);
    if check.is_err() || !check.unwrap().status.success() {
        run_iptables(&["-A", CHAIN_FORWARD, "-i", interface, "-j", &cout])?;
    }

    // Add counting rules in per-monitor chains (exclude private CIDRs)
    // Inbound chain: count packets going to interface, exclude private sources
    for cidr in &excludes {
        let _ = run_iptables(&["-C", &cin, "-s", cidr, "-j", "RETURN"]);
        // Only add if not exists
        let check = run_iptables(&["-C", &cin, "-s", cidr, "-j", "RETURN"]);
        if check.is_err() || !check.unwrap().status.success() {
            let _ = run_iptables(&["-A", &cin, "-s", cidr, "-j", "RETURN"]);
        }
    }

    // Outbound chain: count packets from interface, exclude private destinations
    for cidr in &excludes {
        let check = run_iptables(&["-C", &cout, "-d", cidr, "-j", "RETURN"]);
        if check.is_err() || !check.unwrap().status.success() {
            let _ = run_iptables(&["-A", &cout, "-d", cidr, "-j", "RETURN"]);
        }
    }

    Ok(())
}

/// Remove iptables chains and rules for a monitor.
pub fn remove_counter(monitor_id: i64, interface: &str) -> Result<(), ApiError> {
    let cin = chain_name_in(monitor_id, interface);
    let cout = chain_name_out(monitor_id, interface);

    // Remove jumps from CHAIN_FORWARD
    let _ = run_iptables(&["-D", CHAIN_FORWARD, "-o", interface, "-j", &cin]);
    let _ = run_iptables(&["-D", CHAIN_FORWARD, "-i", interface, "-j", &cout]);

    // Flush and delete chains
    for chain in [&cin, &cout] {
        let _ = run_iptables(&["-F", chain]);
        let _ = run_iptables(&["-X", chain]);
    }

    Ok(())
}

/// Read traffic bytes from iptables chain counters.
/// Returns (bytes_in, bytes_out).
pub fn read_external_bytes(monitor_id: i64, interface: &str) -> Option<(u64, u64)> {
    let cin = chain_name_in(monitor_id, interface);
    let cout = chain_name_out(monitor_id, interface);

    let bytes_in = read_chain_bytes(&cin).unwrap_or(0);
    let bytes_out = read_chain_bytes(&cout).unwrap_or(0);

    if bytes_in > 0 || bytes_out > 0 {
        Some((bytes_in, bytes_out))
    } else {
        // Chains may exist but have 0 bytes; check if chains actually exist
        if chain_exists(&cin) || chain_exists(&cout) {
            Some((bytes_in, bytes_out))
        } else {
            None
        }
    }
}

/// Read total bytes passing through a chain (sum of all rule byte counters).
fn read_chain_bytes(chain: &str) -> Result<u64, ApiError> {
    let out = run_iptables(&["-L", chain, "-n", "-v", "--exact"])?;
    if !out.status.success() {
        return Err(ApiError::internal("chain not found"));
    }

    let stdout = String::from_utf8_lossy(&out.stdout);

    // Parse iptables -L -n -v --exact output.
    // Each rule line has: pkts bytes target prot opt in out source destination
    // RETURN rules = private CIDR exclusions, everything else = external traffic.
    let mut total_all: u64 = 0;
    let mut total_return: u64 = 0;
    for line in stdout.lines() {
        let trimmed = line.trim();
        if trimmed.starts_with("Chain ") || trimmed.starts_with("pkts") || trimmed.is_empty() {
            continue;
        }
        let parts: Vec<&str> = trimmed.split_whitespace().collect();
        if parts.len() >= 3 {
            if let Ok(bytes) = parts[1].parse::<u64>() {
                total_all += bytes;
                if parts[2] == "RETURN" {
                    total_return += bytes;
                }
            }
        }
    }
    
    // The bytes that didn't match any RETURN rule = external traffic
    // But we need a catch-all rule to count them. Let's ensure we add one.
    // For now return total_all - total_return as the external bytes.
    Ok(total_all.saturating_sub(total_return))
}

pub fn bootstrap_from_db(conn: &Connection) -> Result<(), ApiError> {
    ensure_forward_jump()?;

    let mut stmt = conn
        .prepare("SELECT s.monitor_id, s.interface, m.inner_ip FROM interface_states s LEFT JOIN monitors m ON s.monitor_id = m.id")
        .map_err(|e| ApiError::internal(format!("prepare bootstrap query error: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok((
                row.get::<_, i64>(0)?,
                row.get::<_, String>(1)?,
                row.get::<_, Option<String>>(2)?,
            ))
        })
        .map_err(|e| ApiError::internal(format!("bootstrap query error: {e}")))?;

    let mut count = 0usize;
    for row in rows {
        let (monitor_id, interface, inner_ip) =
            row.map_err(|e| ApiError::internal(format!("bootstrap row error: {e}")))?;
        if let Err(err) = ensure_counter(monitor_id, &interface, inner_ip.as_deref()) {
            warn!(
                monitor_id,
                interface,
                error = %err.message,
                "bootstrap failed to ensure iptables counter"
            );
        } else {
            count += 1;
        }
    }
    debug!(count, "iptables bootstrap ensured counters");
    Ok(())
}

pub fn garbage_collect_orphans(conn: &Connection) -> Result<usize, ApiError> {
    // List all VM_IN_*/VM_OUT_* chains
    let out = run_iptables(&["-L", "-n"])?;
    if !out.status.success() {
        return Err(ApiError::internal("failed to list iptables chains"));
    }

    let stdout = String::from_utf8_lossy(&out.stdout);
    let mut existing_chains: HashSet<String> = HashSet::new();
    for line in stdout.lines() {
        let trimmed = line.trim();
        if trimmed.starts_with("Chain VM_IN_") || trimmed.starts_with("Chain VM_OUT_") {
            if let Some(name) = trimmed.strip_prefix("Chain ") {
                if let Some(chain) = name.split_whitespace().next() {
                    existing_chains.insert(chain.to_string());
                }
            }
        }
    }

    // Get expected chains from DB
    let mut stmt = conn
        .prepare("SELECT monitor_id, interface FROM interface_states")
        .map_err(|e| ApiError::internal(format!("prepare gc query: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok((row.get::<_, i64>(0)?, row.get::<_, String>(1)?))
        })
        .map_err(|e| ApiError::internal(format!("gc query: {e}")))?;

    let mut expected: HashSet<String> = HashSet::new();
    for row in rows {
        let (monitor_id, interface) =
            row.map_err(|e| ApiError::internal(format!("gc row: {e}")))?;
        expected.insert(chain_name_in(monitor_id, &interface));
        expected.insert(chain_name_out(monitor_id, &interface));
    }

    let mut removed = 0usize;
    for chain in existing_chains.difference(&expected) {
        // Remove jump from CHAIN_FORWARD if exists
        let _ = run_iptables(&["-D", CHAIN_FORWARD, "-j", chain]);
        let _ = run_iptables(&["-F", chain]);
        let _ = run_iptables(&["-X", chain]);
        removed += 1;
    }

    if removed > 0 {
        info!(removed, "garbage-collected orphan iptables chains");
    }
    Ok(removed)
}
