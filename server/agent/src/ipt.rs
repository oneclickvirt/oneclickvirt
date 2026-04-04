use crate::error::ApiError;
use rusqlite::Connection;
use std::{
    collections::HashSet,
    env, fs,
    path::Path,
    process::Command,
    sync::OnceLock,
};
use tracing::{debug, info, warn};

// Full exclusion ranges matching nft.rs
const DEFAULT_EXCLUDE_V4: &[&str] = &[
    "0.0.0.0/8",
    "10.0.0.0/8",
    "100.64.0.0/10",
    "127.0.0.0/8",
    "169.254.0.0/16",
    "172.16.0.0/12",
    "192.0.0.0/24",
    "192.0.2.0/24",
    "192.88.99.0/24",
    "192.168.0.0/16",
    "198.18.0.0/15",
    "198.51.100.0/24",
    "203.0.113.0/24",
    "224.0.0.0/4",
    "240.0.0.0/4",
];

const DEFAULT_EXCLUDE_V6: &[&str] = &[
    "::/128",
    "::1/128",
    "fc00::/7",
    "fe80::/10",
    "ff00::/8",
    "2001:db8::/32",
];

static EXCLUDE_V4: OnceLock<Vec<String>> = OnceLock::new();
static EXCLUDE_V6: OnceLock<Vec<String>> = OnceLock::new();
static HAS_IPTABLES: OnceLock<bool> = OnceLock::new();
static HAS_IP6TABLES: OnceLock<bool> = OnceLock::new();

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

fn exclude_v4() -> &'static Vec<String> {
    EXCLUDE_V4.get_or_init(|| {
        let mut all: Vec<String> = DEFAULT_EXCLUDE_V4.iter().map(|s| (*s).to_owned()).collect();
        all.extend(parse_env_cidrs("EXTRA_EXCLUDE_CIDRS_V4"));
        all
    })
}

fn exclude_v6() -> &'static Vec<String> {
    EXCLUDE_V6.get_or_init(|| {
        let mut all: Vec<String> = DEFAULT_EXCLUDE_V6.iter().map(|s| (*s).to_owned()).collect();
        all.extend(parse_env_cidrs("EXTRA_EXCLUDE_CIDRS_V6"));
        all
    })
}

fn has_iptables() -> bool {
    *HAS_IPTABLES.get_or_init(|| {
        Command::new("iptables").arg("--version").output().map(|o| o.status.success()).unwrap_or(false)
    })
}

fn has_ip6tables() -> bool {
    *HAS_IP6TABLES.get_or_init(|| {
        Command::new("ip6tables").arg("--version").output().map(|o| o.status.success()).unwrap_or(false)
    })
}

const CHAIN_FORWARD: &str = "VM_TRAFFIC_FWD";
const CHAIN_FORWARD_V6: &str = "VM_TRAFFIC_FWD6";

fn run_ipt(program: &str, args: &[&str]) -> Result<std::process::Output, ApiError> {
    Command::new(program)
        .args(args)
        .output()
        .map_err(|e| ApiError::internal(format!("failed to run {program} {:?}: {e}", args)))
}

fn run_iptables(args: &[&str]) -> Result<std::process::Output, ApiError> {
    run_ipt("iptables", args)
}

fn run_ip6tables(args: &[&str]) -> Result<std::process::Output, ApiError> {
    run_ipt("ip6tables", args)
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

fn chain_name_in(monitor_id: i64, interface: &str) -> String {
    let h = fnv1a_64(interface.as_bytes());
    format!("VM_IN_{monitor_id}_{h:x}")
}

fn chain_name_out(monitor_id: i64, interface: &str) -> String {
    let h = fnv1a_64(interface.as_bytes());
    format!("VM_OUT_{monitor_id}_{h:x}")
}

// IPv6 chain names have a "6" suffix to avoid collision
fn chain_name_in6(monitor_id: i64, interface: &str) -> String {
    let h = fnv1a_64(interface.as_bytes());
    format!("VM6_IN_{monitor_id}_{h:x}")
}

fn chain_name_out6(monitor_id: i64, interface: &str) -> String {
    let h = fnv1a_64(interface.as_bytes());
    format!("VM6_OUT_{monitor_id}_{h:x}")
}

fn interface_aliases(interface: &str) -> Vec<String> {
    let mut aliases = Vec::new();
    aliases.push(interface.to_string());
    let master_link = format!("/sys/class/net/{interface}/master");
    if let Ok(target) = fs::read_link(master_link) {
        if let Some(name) = Path::new(&target).file_name().and_then(|x| x.to_str()) {
            if !name.is_empty() && !aliases.iter().any(|v| v == name) {
                aliases.push(name.to_string());
            }
        }
    }
    aliases
}

fn chain_exists_ipt(program: &str, chain: &str) -> bool {
    run_ipt(program, &["-L", chain, "-n", "--exact"])
        .map(|o| o.status.success())
        .unwrap_or(false)
}

fn ensure_chain_ipt(program: &str, chain: &str) -> Result<(), ApiError> {
    if chain_exists_ipt(program, chain) {
        return Ok(());
    }
    let out = run_ipt(program, &["-N", chain])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if !stderr.contains("already exists") {
            return Err(ApiError::internal(format!(
                "failed to create {program} chain {chain}: {}",
                stderr.trim()
            )));
        }
    }
    Ok(())
}

fn ensure_forward_jump_ipt(program: &str, chain: &str) -> Result<(), ApiError> {
    ensure_chain_ipt(program, chain)?;
    let out = run_ipt(program, &["-C", "FORWARD", "-j", chain]);
    match out {
        Ok(o) if o.status.success() => return Ok(()),
        _ => {}
    }
    let out = run_ipt(program, &["-I", "FORWARD", "-j", chain])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        return Err(ApiError::internal(format!(
            "failed to add FORWARD jump to {chain}: {}",
            stderr.trim()
        )));
    }
    Ok(())
}

fn add_rule_if_missing(program: &str, chain: &str, args: &[&str]) {
    let mut check_args = vec!["-C", chain];
    check_args.extend_from_slice(args);
    let check = run_ipt(program, &check_args);
    if check.is_err() || !check.unwrap().status.success() {
        let mut add_args = vec!["-A", chain];
        add_args.extend_from_slice(args);
        let _ = run_ipt(program, &add_args);
    }
}

fn setup_v4_chain(
    monitor_id: i64,
    interface: &str,
    cin: &str,
    cout: &str,
    inner_ip: Option<&str>,
) -> Result<(), ApiError> {
    ensure_forward_jump_ipt("iptables", CHAIN_FORWARD)?;
    ensure_chain_ipt("iptables", cin)?;
    ensure_chain_ipt("iptables", cout)?;

    let aliases = interface_aliases(interface);
    let excludes = exclude_v4();

    for alias in &aliases {
        // Jump from CHAIN_FORWARD to per-monitor chains
        add_rule_if_missing("iptables", CHAIN_FORWARD, &["-o", alias, "-j", cin]);
        add_rule_if_missing("iptables", CHAIN_FORWARD, &["-i", alias, "-j", cout]);
    }

    // Add exclusion RETURN rules
    match inner_ip {
        Some(ip) if !ip.contains(':') => {
            // Per-IP filtering: inbound chain counts traffic TO inner_ip from non-private sources
            for cidr in excludes.iter() {
                add_rule_if_missing("iptables", cin, &["-s", cidr, "-j", "RETURN"]);
            }
            // Only count traffic destined for this specific IP
            add_rule_if_missing("iptables", cin, &["!", "-d", ip, "-j", "RETURN"]);

            // Outbound chain: counts traffic FROM inner_ip to non-private destinations
            for cidr in excludes.iter() {
                add_rule_if_missing("iptables", cout, &["-d", cidr, "-j", "RETURN"]);
            }
            // Only count traffic from this specific IP
            add_rule_if_missing("iptables", cout, &["!", "-s", ip, "-j", "RETURN"]);
        }
        _ => {
            // Interface-based counting: exclude private ranges
            for cidr in excludes.iter() {
                add_rule_if_missing("iptables", cin, &["-s", cidr, "-j", "RETURN"]);
            }
            for cidr in excludes.iter() {
                add_rule_if_missing("iptables", cout, &["-d", cidr, "-j", "RETURN"]);
            }
        }
    }

    Ok(())
}

fn setup_v6_chain(
    monitor_id: i64,
    interface: &str,
    cin6: &str,
    cout6: &str,
    inner_ip: Option<&str>,
) -> Result<(), ApiError> {
    if !has_ip6tables() {
        debug!("ip6tables not available, skipping IPv6 traffic monitoring");
        return Ok(());
    }
    ensure_forward_jump_ipt("ip6tables", CHAIN_FORWARD_V6)?;
    ensure_chain_ipt("ip6tables", cin6)?;
    ensure_chain_ipt("ip6tables", cout6)?;

    let aliases = interface_aliases(interface);
    let excludes = exclude_v6();

    for alias in &aliases {
        add_rule_if_missing("ip6tables", CHAIN_FORWARD_V6, &["-o", alias, "-j", cin6]);
        add_rule_if_missing("ip6tables", CHAIN_FORWARD_V6, &["-i", alias, "-j", cout6]);
    }

    match inner_ip {
        Some(ip) if ip.contains(':') => {
            // Per-IPv6 filtering
            for cidr in excludes.iter() {
                add_rule_if_missing("ip6tables", cin6, &["-s", cidr, "-j", "RETURN"]);
            }
            add_rule_if_missing("ip6tables", cin6, &["!", "-d", ip, "-j", "RETURN"]);
            for cidr in excludes.iter() {
                add_rule_if_missing("ip6tables", cout6, &["-d", cidr, "-j", "RETURN"]);
            }
            add_rule_if_missing("ip6tables", cout6, &["!", "-s", ip, "-j", "RETURN"]);
        }
        _ => {
            for cidr in excludes.iter() {
                add_rule_if_missing("ip6tables", cin6, &["-s", cidr, "-j", "RETURN"]);
            }
            for cidr in excludes.iter() {
                add_rule_if_missing("ip6tables", cout6, &["-d", cidr, "-j", "RETURN"]);
            }
        }
    }

    Ok(())
}

/// Ensure per-monitor iptables/ip6tables chains and rules exist.
pub fn ensure_counter(
    monitor_id: i64,
    interface: &str,
    inner_ip: Option<&str>,
) -> Result<(), ApiError> {
    if !has_iptables() {
        return Err(ApiError::internal("iptables not available"));
    }

    let cin = chain_name_in(monitor_id, interface);
    let cout = chain_name_out(monitor_id, interface);
    let cin6 = chain_name_in6(monitor_id, interface);
    let cout6 = chain_name_out6(monitor_id, interface);

    setup_v4_chain(monitor_id, interface, &cin, &cout, inner_ip)?;
    setup_v6_chain(monitor_id, interface, &cin6, &cout6, inner_ip)?;

    Ok(())
}

/// Remove iptables/ip6tables chains and rules for a monitor.
pub fn remove_counter(monitor_id: i64, interface: &str) -> Result<(), ApiError> {
    let cin = chain_name_in(monitor_id, interface);
    let cout = chain_name_out(monitor_id, interface);
    let cin6 = chain_name_in6(monitor_id, interface);
    let cout6 = chain_name_out6(monitor_id, interface);

    let aliases = interface_aliases(interface);

    // Remove IPv4
    for alias in &aliases {
        let _ = run_iptables(&["-D", CHAIN_FORWARD, "-o", alias, "-j", &cin]);
        let _ = run_iptables(&["-D", CHAIN_FORWARD, "-i", alias, "-j", &cout]);
    }
    for chain in [&cin, &cout] {
        let _ = run_iptables(&["-F", chain]);
        let _ = run_iptables(&["-X", chain]);
    }

    // Remove IPv6
    if has_ip6tables() {
        for alias in &aliases {
            let _ = run_ip6tables(&["-D", CHAIN_FORWARD_V6, "-o", alias, "-j", &cin6]);
            let _ = run_ip6tables(&["-D", CHAIN_FORWARD_V6, "-i", alias, "-j", &cout6]);
        }
        for chain in [&cin6, &cout6] {
            let _ = run_ip6tables(&["-F", chain]);
            let _ = run_ip6tables(&["-X", chain]);
        }
    }

    Ok(())
}

/// Read total bytes passing through a chain (external traffic = total - RETURN bytes).
fn read_chain_bytes(program: &str, chain: &str) -> Result<u64, ApiError> {
    let out = run_ipt(program, &["-L", chain, "-n", "-v", "--exact"])?;
    if !out.status.success() {
        return Err(ApiError::internal("chain not found"));
    }

    let stdout = String::from_utf8_lossy(&out.stdout);
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
    Ok(total_all.saturating_sub(total_return))
}

/// Read traffic bytes from iptables+ip6tables chain counters.
/// Returns (bytes_in, bytes_out) combining IPv4 and IPv6.
pub fn read_external_bytes(monitor_id: i64, interface: &str) -> Option<(u64, u64)> {
    let cin = chain_name_in(monitor_id, interface);
    let cout = chain_name_out(monitor_id, interface);

    let mut bytes_in = 0u64;
    let mut bytes_out = 0u64;
    let mut has_any = false;

    // IPv4
    if has_iptables() {
        if chain_exists_ipt("iptables", &cin) || chain_exists_ipt("iptables", &cout) {
            has_any = true;
            bytes_in = bytes_in.saturating_add(read_chain_bytes("iptables", &cin).unwrap_or(0));
            bytes_out = bytes_out.saturating_add(read_chain_bytes("iptables", &cout).unwrap_or(0));
        }
    }

    // IPv6
    if has_ip6tables() {
        let cin6 = chain_name_in6(monitor_id, interface);
        let cout6 = chain_name_out6(monitor_id, interface);
        if chain_exists_ipt("ip6tables", &cin6) || chain_exists_ipt("ip6tables", &cout6) {
            has_any = true;
            bytes_in = bytes_in.saturating_add(read_chain_bytes("ip6tables", &cin6).unwrap_or(0));
            bytes_out = bytes_out.saturating_add(read_chain_bytes("ip6tables", &cout6).unwrap_or(0));
        }
    }

    if has_any {
        Some((bytes_in, bytes_out))
    } else {
        None
    }
}

pub fn bootstrap_from_db(conn: &Connection) -> Result<(), ApiError> {
    if !has_iptables() {
        return Err(ApiError::internal("iptables not available, cannot bootstrap"));
    }
    ensure_forward_jump_ipt("iptables", CHAIN_FORWARD)?;
    if has_ip6tables() {
        ensure_forward_jump_ipt("ip6tables", CHAIN_FORWARD_V6)?;
    }

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

fn collect_existing_chains(program: &str, prefix: &str) -> HashSet<String> {
    let out = match run_ipt(program, &["-L", "-n"]) {
        Ok(o) if o.status.success() => o,
        _ => return HashSet::new(),
    };
    let stdout = String::from_utf8_lossy(&out.stdout);
    let mut chains = HashSet::new();
    for line in stdout.lines() {
        let trimmed = line.trim();
        if trimmed.starts_with(&format!("Chain {prefix}")) {
            if let Some(name) = trimmed.strip_prefix("Chain ") {
                if let Some(chain) = name.split_whitespace().next() {
                    chains.insert(chain.to_string());
                }
            }
        }
    }
    chains
}

pub fn garbage_collect_orphans(conn: &Connection) -> Result<usize, ApiError> {
    let mut stmt = conn
        .prepare("SELECT monitor_id, interface FROM interface_states")
        .map_err(|e| ApiError::internal(format!("prepare gc query: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok((row.get::<_, i64>(0)?, row.get::<_, String>(1)?))
        })
        .map_err(|e| ApiError::internal(format!("gc query: {e}")))?;

    let mut expected_v4: HashSet<String> = HashSet::new();
    let mut expected_v6: HashSet<String> = HashSet::new();
    for row in rows {
        let (monitor_id, interface) =
            row.map_err(|e| ApiError::internal(format!("gc row: {e}")))?;
        expected_v4.insert(chain_name_in(monitor_id, &interface));
        expected_v4.insert(chain_name_out(monitor_id, &interface));
        expected_v6.insert(chain_name_in6(monitor_id, &interface));
        expected_v6.insert(chain_name_out6(monitor_id, &interface));
    }

    let mut removed = 0usize;

    // Cleanup IPv4 orphans
    if has_iptables() {
        let existing_in = collect_existing_chains("iptables", "VM_IN_");
        let existing_out = collect_existing_chains("iptables", "VM_OUT_");
        let existing: HashSet<String> = existing_in.union(&existing_out).cloned().collect();

        for chain in existing.difference(&expected_v4) {
            let _ = run_iptables(&["-D", CHAIN_FORWARD, "-j", chain]);
            let _ = run_iptables(&["-F", chain]);
            let _ = run_iptables(&["-X", chain]);
            removed += 1;
        }
    }

    // Cleanup IPv6 orphans
    if has_ip6tables() {
        let existing_in6 = collect_existing_chains("ip6tables", "VM6_IN_");
        let existing_out6 = collect_existing_chains("ip6tables", "VM6_OUT_");
        let existing6: HashSet<String> = existing_in6.union(&existing_out6).cloned().collect();

        for chain in existing6.difference(&expected_v6) {
            let _ = run_ip6tables(&["-D", CHAIN_FORWARD_V6, "-j", chain]);
            let _ = run_ip6tables(&["-F", chain]);
            let _ = run_ip6tables(&["-X", chain]);
            removed += 1;
        }
    }

    if removed > 0 {
        info!(removed, "garbage-collected orphan iptables/ip6tables chains");
    }
    Ok(removed)
}
