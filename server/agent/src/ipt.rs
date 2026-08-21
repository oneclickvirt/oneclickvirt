use crate::{error::ApiError, traffic::parse_persisted_bindings};
use rusqlite::Connection;
use std::{collections::HashSet, env, fs, path::Path, process::Command, sync::OnceLock};
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
static HAS_IPTABLES: OnceLock<()> = OnceLock::new();
static HAS_IP6TABLES: OnceLock<()> = OnceLock::new();

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
    if HAS_IPTABLES.get().is_some() {
        return true;
    }
    let available = Command::new("iptables")
        .arg("--version")
        .output()
        .map(|output| output.status.success())
        .unwrap_or(false);
    if available {
        let _ = HAS_IPTABLES.set(());
    }
    available
}

fn has_ip6tables() -> bool {
    if HAS_IP6TABLES.get().is_some() {
        return true;
    }
    let available = Command::new("ip6tables")
        .arg("--version")
        .output()
        .map(|output| output.status.success())
        .unwrap_or(false);
    if available {
        let _ = HAS_IP6TABLES.set(());
    }
    available
}

const CHAIN_FORWARD: &str = "VM_TRAFFIC_FWD";
const CHAIN_FORWARD_V6: &str = "VM_TRAFFIC_FWD6";
const COUNT_RULE_COMMENT: &str = "oneclickvirt-traffic-count";

fn run_ipt(program: &str, args: &[&str]) -> Result<std::process::Output, ApiError> {
    Command::new(program)
        .args(["-w", "5"])
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
    ensure_jump_rule_shape(program, "FORWARD", chain, &[vec!["-j", chain]], true)
}

fn command_failure(program: &str, args: &[&str], output: &std::process::Output) -> ApiError {
    let stderr = String::from_utf8_lossy(&output.stderr);
    ApiError::internal(format!(
        "{program} {:?} failed with status {}: {}",
        args,
        output.status,
        stderr.trim()
    ))
}

fn rules_targeting_chain(
    program: &str,
    parent_chain: &str,
    target_chain: &str,
) -> Result<Vec<Vec<String>>, ApiError> {
    let args = ["-S", parent_chain];
    let output = run_ipt(program, &args)?;
    if !output.status.success() {
        return Err(command_failure(program, &args, &output));
    }
    Ok(parse_rules_targeting_chain(
        &String::from_utf8_lossy(&output.stdout),
        parent_chain,
        target_chain,
    ))
}

fn parse_rules_targeting_chain(
    output: &str,
    parent_chain: &str,
    target_chain: &str,
) -> Vec<Vec<String>> {
    let mut rules = Vec::new();
    for line in output.lines() {
        let tokens = line.split_whitespace().collect::<Vec<_>>();
        if tokens.len() < 4 || tokens[0] != "-A" || tokens[1] != parent_chain {
            continue;
        }
        let targets_chain = tokens.windows(2).any(|window| {
            (window[0] == "-j" || window[0] == "--jump") && window[1] == target_chain
        });
        if targets_chain {
            rules.push(
                tokens[2..]
                    .iter()
                    .map(|token| (*token).to_string())
                    .collect(),
            );
        }
    }
    rules
}

fn remove_jump_rules_to_chain(
    program: &str,
    parent_chain: &str,
    target_chain: &str,
) -> Result<usize, ApiError> {
    let rules = rules_targeting_chain(program, parent_chain, target_chain)?;
    for rule in &rules {
        let mut owned_args = vec!["-D".to_string(), parent_chain.to_string()];
        owned_args.extend(rule.iter().cloned());
        let args = owned_args.iter().map(String::as_str).collect::<Vec<_>>();
        let output = run_ipt(program, &args)?;
        if !output.status.success() {
            return Err(command_failure(program, &args, &output));
        }
    }
    Ok(rules.len())
}

fn ensure_jump_rule_shape(
    program: &str,
    parent_chain: &str,
    target_chain: &str,
    expected_rules: &[Vec<&str>],
    insert: bool,
) -> Result<(), ApiError> {
    let actual = rules_targeting_chain(program, parent_chain, target_chain)?;
    let mut healthy = actual.len() == expected_rules.len();
    if healthy {
        for rule in expected_rules {
            if !rule_exists(program, parent_chain, rule)? {
                healthy = false;
                break;
            }
        }
    }
    if healthy {
        return Ok(());
    }
    remove_jump_rules_to_chain(program, parent_chain, target_chain)?;
    if insert {
        for rule in expected_rules.iter().rev() {
            let mut add_args = vec!["-I", parent_chain];
            add_args.extend_from_slice(rule);
            let output = run_ipt(program, &add_args)?;
            if !output.status.success() {
                return Err(command_failure(program, &add_args, &output));
            }
        }
    } else {
        for rule in expected_rules {
            let mut add_args = vec!["-A", parent_chain];
            add_args.extend_from_slice(rule);
            let output = run_ipt(program, &add_args)?;
            if !output.status.success() {
                return Err(command_failure(program, &add_args, &output));
            }
        }
    }
    Ok(())
}

fn add_rule_if_missing(program: &str, chain: &str, args: &[&str]) -> Result<(), ApiError> {
    if !rule_exists(program, chain, args)? {
        let mut add_args = vec!["-A", chain];
        add_args.extend_from_slice(args);
        let output = run_ipt(program, &add_args)?;
        if !output.status.success() {
            return Err(command_failure(program, &add_args, &output));
        }
    }
    Ok(())
}

fn rule_exists(program: &str, chain: &str, args: &[&str]) -> Result<bool, ApiError> {
    let mut check_args = vec!["-C", chain];
    check_args.extend_from_slice(args);
    let output = run_ipt(program, &check_args)?;
    if output.status.success() {
        return Ok(true);
    }
    if output.status.code() == Some(1) {
        return Ok(false);
    }
    Err(command_failure(program, &check_args, &output))
}

fn chain_rule_count(program: &str, chain: &str) -> Result<usize, ApiError> {
    let args = ["-S", chain];
    let output = run_ipt(program, &args)?;
    if !output.status.success() {
        return Err(command_failure(program, &args, &output));
    }
    Ok(String::from_utf8_lossy(&output.stdout)
        .lines()
        .filter(|line| line.trim_start().starts_with("-A "))
        .count())
}

fn ensure_chain_rule_shape(
    program: &str,
    chain: &str,
    expected_rules: &[Vec<&str>],
) -> Result<(), ApiError> {
    let mut healthy = chain_rule_count(program, chain)? == expected_rules.len();
    if healthy {
        for rule in expected_rules {
            if !rule_exists(program, chain, rule)? {
                healthy = false;
                break;
            }
        }
    }
    if !healthy {
        let args = ["-F", chain];
        let output = run_ipt(program, &args)?;
        if !output.status.success() {
            return Err(command_failure(program, &args, &output));
        }
    }
    Ok(())
}

fn setup_v4_chain(
    _monitor_id: i64,
    interface: &str,
    cin: &str,
    cout: &str,
    addresses: &[String],
) -> Result<(), ApiError> {
    ensure_forward_jump_ipt("iptables", CHAIN_FORWARD)?;
    ensure_chain_ipt("iptables", cin)?;
    ensure_chain_ipt("iptables", cout)?;

    let aliases = interface_aliases(interface);
    let excludes = exclude_v4();

    let expected_in_jumps = aliases
        .iter()
        .map(|alias| vec!["-o", alias.as_str(), "-j", cin])
        .collect::<Vec<_>>();
    let expected_out_jumps = aliases
        .iter()
        .map(|alias| vec!["-i", alias.as_str(), "-j", cout])
        .collect::<Vec<_>>();
    ensure_jump_rule_shape("iptables", CHAIN_FORWARD, cin, &expected_in_jumps, false)?;
    ensure_jump_rule_shape("iptables", CHAIN_FORWARD, cout, &expected_out_jumps, false)?;

    let mut expected_in = excludes
        .iter()
        .map(|cidr| vec!["-s", cidr.as_str(), "-j", "RETURN"])
        .collect::<Vec<_>>();
    let mut expected_out = excludes
        .iter()
        .map(|cidr| vec!["-d", cidr.as_str(), "-j", "RETURN"])
        .collect::<Vec<_>>();
    let family_addresses = addresses
        .iter()
        .filter(|address| !address.contains(':'))
        .map(String::as_str)
        .collect::<Vec<_>>();
    if family_addresses.is_empty() {
        expected_in.push(vec!["-m", "comment", "--comment", COUNT_RULE_COMMENT]);
        expected_out.push(vec!["-m", "comment", "--comment", COUNT_RULE_COMMENT]);
    } else {
        for ip in &family_addresses {
            expected_in.push(vec![
                "-d",
                *ip,
                "-m",
                "comment",
                "--comment",
                COUNT_RULE_COMMENT,
            ]);
            expected_out.push(vec![
                "-s",
                *ip,
                "-m",
                "comment",
                "--comment",
                COUNT_RULE_COMMENT,
            ]);
        }
    }
    ensure_chain_rule_shape("iptables", cin, &expected_in)?;
    ensure_chain_rule_shape("iptables", cout, &expected_out)?;

    for cidr in excludes.iter() {
        add_rule_if_missing("iptables", cin, &["-s", cidr, "-j", "RETURN"])?;
    }
    for cidr in excludes.iter() {
        add_rule_if_missing("iptables", cout, &["-d", cidr, "-j", "RETURN"])?;
    }
    if family_addresses.is_empty() {
        add_rule_if_missing(
            "iptables",
            cin,
            &["-m", "comment", "--comment", COUNT_RULE_COMMENT],
        )?;
        add_rule_if_missing(
            "iptables",
            cout,
            &["-m", "comment", "--comment", COUNT_RULE_COMMENT],
        )?;
    } else {
        for ip in family_addresses {
            add_rule_if_missing(
                "iptables",
                cin,
                &["-d", ip, "-m", "comment", "--comment", COUNT_RULE_COMMENT],
            )?;
            add_rule_if_missing(
                "iptables",
                cout,
                &["-s", ip, "-m", "comment", "--comment", COUNT_RULE_COMMENT],
            )?;
        }
    }

    Ok(())
}

fn setup_v6_chain(
    _monitor_id: i64,
    interface: &str,
    cin6: &str,
    cout6: &str,
    addresses: &[String],
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

    let expected_in_jumps = aliases
        .iter()
        .map(|alias| vec!["-o", alias.as_str(), "-j", cin6])
        .collect::<Vec<_>>();
    let expected_out_jumps = aliases
        .iter()
        .map(|alias| vec!["-i", alias.as_str(), "-j", cout6])
        .collect::<Vec<_>>();
    ensure_jump_rule_shape(
        "ip6tables",
        CHAIN_FORWARD_V6,
        cin6,
        &expected_in_jumps,
        false,
    )?;
    ensure_jump_rule_shape(
        "ip6tables",
        CHAIN_FORWARD_V6,
        cout6,
        &expected_out_jumps,
        false,
    )?;

    let mut expected_in = excludes
        .iter()
        .map(|cidr| vec!["-s", cidr.as_str(), "-j", "RETURN"])
        .collect::<Vec<_>>();
    let mut expected_out = excludes
        .iter()
        .map(|cidr| vec!["-d", cidr.as_str(), "-j", "RETURN"])
        .collect::<Vec<_>>();
    let family_addresses = addresses
        .iter()
        .filter(|address| address.contains(':'))
        .map(String::as_str)
        .collect::<Vec<_>>();
    if family_addresses.is_empty() {
        expected_in.push(vec!["-m", "comment", "--comment", COUNT_RULE_COMMENT]);
        expected_out.push(vec!["-m", "comment", "--comment", COUNT_RULE_COMMENT]);
    } else {
        for ip in &family_addresses {
            expected_in.push(vec![
                "-d",
                *ip,
                "-m",
                "comment",
                "--comment",
                COUNT_RULE_COMMENT,
            ]);
            expected_out.push(vec![
                "-s",
                *ip,
                "-m",
                "comment",
                "--comment",
                COUNT_RULE_COMMENT,
            ]);
        }
    }
    ensure_chain_rule_shape("ip6tables", cin6, &expected_in)?;
    ensure_chain_rule_shape("ip6tables", cout6, &expected_out)?;

    for cidr in excludes.iter() {
        add_rule_if_missing("ip6tables", cin6, &["-s", cidr, "-j", "RETURN"])?;
    }
    for cidr in excludes.iter() {
        add_rule_if_missing("ip6tables", cout6, &["-d", cidr, "-j", "RETURN"])?;
    }
    if family_addresses.is_empty() {
        add_rule_if_missing(
            "ip6tables",
            cin6,
            &["-m", "comment", "--comment", COUNT_RULE_COMMENT],
        )?;
        add_rule_if_missing(
            "ip6tables",
            cout6,
            &["-m", "comment", "--comment", COUNT_RULE_COMMENT],
        )?;
    } else {
        for ip in family_addresses {
            add_rule_if_missing(
                "ip6tables",
                cin6,
                &["-d", ip, "-m", "comment", "--comment", COUNT_RULE_COMMENT],
            )?;
            add_rule_if_missing(
                "ip6tables",
                cout6,
                &["-s", ip, "-m", "comment", "--comment", COUNT_RULE_COMMENT],
            )?;
        }
    }

    Ok(())
}

/// Ensure per-monitor iptables/ip6tables chains and rules exist.
pub fn ensure_counter(
    monitor_id: i64,
    interface: &str,
    addresses: &[String],
    families: &[String],
) -> Result<(), ApiError> {
    let expects_v4 = families.is_empty() || families.iter().any(|family| family == "ipv4");
    let expects_v6 = families.is_empty() || families.iter().any(|family| family == "ipv6");
    if expects_v4 && !has_iptables() {
        return Err(ApiError::internal("iptables not available"));
    }
    if expects_v6 && !has_ip6tables() {
        return Err(ApiError::internal(
            "ip6tables not available for an IPv6 traffic binding",
        ));
    }
    if !crate::traffic::interface_exists(interface) {
        return Err(ApiError::internal(format!(
            "interface {interface} does not exist"
        )));
    }

    let cin = chain_name_in(monitor_id, interface);
    let cout = chain_name_out(monitor_id, interface);
    let cin6 = chain_name_in6(monitor_id, interface);
    let cout6 = chain_name_out6(monitor_id, interface);

    if has_iptables() {
        setup_v4_chain(monitor_id, interface, &cin, &cout, addresses)?;
    }
    if has_ip6tables() {
        setup_v6_chain(monitor_id, interface, &cin6, &cout6, addresses)?;
    }

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

    Ok(parse_chain_bytes(&String::from_utf8_lossy(&out.stdout)))
}

fn parse_chain_bytes(output: &str) -> u64 {
    let mut total_all: u64 = 0;
    let mut total_return: u64 = 0;
    for line in output.lines() {
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
    total_all.saturating_sub(total_return)
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
        let in_exists = chain_exists_ipt("iptables", &cin);
        let out_exists = chain_exists_ipt("iptables", &cout);
        if in_exists && out_exists {
            has_any = true;
            bytes_in = bytes_in.saturating_add(read_chain_bytes("iptables", &cin).unwrap_or(0));
            bytes_out = bytes_out.saturating_add(read_chain_bytes("iptables", &cout).unwrap_or(0));
        }
    }

    // IPv6
    if has_ip6tables() {
        let cin6 = chain_name_in6(monitor_id, interface);
        let cout6 = chain_name_out6(monitor_id, interface);
        let in_exists = chain_exists_ipt("ip6tables", &cin6);
        let out_exists = chain_exists_ipt("ip6tables", &cout6);
        if in_exists && out_exists {
            has_any = true;
            bytes_in = bytes_in.saturating_add(read_chain_bytes("ip6tables", &cin6).unwrap_or(0));
            bytes_out =
                bytes_out.saturating_add(read_chain_bytes("ip6tables", &cout6).unwrap_or(0));
        }
    }

    if has_any {
        Some((bytes_in, bytes_out))
    } else {
        None
    }
}

pub fn bootstrap_from_db(conn: &Connection, batch_size: usize) -> Result<(), ApiError> {
    if !has_iptables() {
        return Err(ApiError::internal(
            "iptables not available, cannot bootstrap",
        ));
    }
    ensure_forward_jump_ipt("iptables", CHAIN_FORWARD)?;
    if has_ip6tables() {
        ensure_forward_jump_ipt("ip6tables", CHAIN_FORWARD_V6)?;
    }

    let mut stmt = conn
        .prepare("SELECT id, interfaces, bindings, inner_ip FROM monitors ORDER BY id LIMIT ?1")
        .map_err(|e| ApiError::internal(format!("prepare bootstrap query error: {e}")))?;
    let rows = stmt
        .query_map([batch_size.max(1) as i64], |row| {
            Ok((
                row.get::<_, i64>(0)?,
                row.get::<_, String>(1)?,
                row.get::<_, String>(2)?,
                row.get::<_, Option<String>>(3)?,
            ))
        })
        .map_err(|e| ApiError::internal(format!("bootstrap query error: {e}")))?;

    let mut count = 0usize;
    for row in rows {
        let (monitor_id, interfaces_json, bindings_json, inner_ip) =
            row.map_err(|e| ApiError::internal(format!("bootstrap row error: {e}")))?;
        for binding in
            parse_persisted_bindings(&bindings_json, &interfaces_json, inner_ip.as_deref())
        {
            if let Err(err) = ensure_counter(
                monitor_id,
                &binding.interface,
                &binding.addresses,
                &binding.families,
            ) {
                let _ = conn.execute(
                    "DELETE FROM interface_states WHERE monitor_id = ?1 AND interface = ?2",
                    rusqlite::params![monitor_id, binding.interface],
                );
                warn!(
                    monitor_id,
                    interface = binding.interface,
                    error = %err.message,
                    "bootstrap failed to ensure iptables counter"
                );
                continue;
            }
            if let Some((base_in, base_out)) = read_external_bytes(monitor_id, &binding.interface) {
                conn.execute(
                    "INSERT INTO interface_states \
                     (monitor_id, interface, last_counter_in, last_counter_out) \
                     VALUES (?1, ?2, ?3, ?4) \
                     ON CONFLICT(monitor_id, interface) DO NOTHING",
                    rusqlite::params![monitor_id, binding.interface, base_in, base_out],
                )
                .map_err(|e| ApiError::internal(format!("bootstrap activate state error: {e}")))?;
                count += 1;
            } else {
                let _ = conn.execute(
                    "DELETE FROM interface_states WHERE monitor_id = ?1 AND interface = ?2",
                    rusqlite::params![monitor_id, binding.interface],
                );
            }
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
    let mut last_error: Option<String> = None;

    // Cleanup IPv4 orphans
    if has_iptables() {
        let existing_in = collect_existing_chains("iptables", "VM_IN_");
        let existing_out = collect_existing_chains("iptables", "VM_OUT_");
        let existing: HashSet<String> = existing_in.union(&existing_out).cloned().collect();

        for chain in existing.difference(&expected_v4) {
            let result = (|| -> Result<(), ApiError> {
                remove_jump_rules_to_chain("iptables", CHAIN_FORWARD, chain)?;
                for action in ["-F", "-X"] {
                    let args = [action, chain.as_str()];
                    let output = run_iptables(&args)?;
                    if !output.status.success() {
                        return Err(command_failure("iptables", &args, &output));
                    }
                }
                Ok(())
            })();
            match result {
                Ok(()) => removed += 1,
                Err(err) => {
                    last_error = Some(err.message.clone());
                    warn!(chain, error = %err.message, "failed to garbage-collect orphan iptables chain");
                }
            }
        }
    }

    // Cleanup IPv6 orphans
    if has_ip6tables() {
        let existing_in6 = collect_existing_chains("ip6tables", "VM6_IN_");
        let existing_out6 = collect_existing_chains("ip6tables", "VM6_OUT_");
        let existing6: HashSet<String> = existing_in6.union(&existing_out6).cloned().collect();

        for chain in existing6.difference(&expected_v6) {
            let result = (|| -> Result<(), ApiError> {
                remove_jump_rules_to_chain("ip6tables", CHAIN_FORWARD_V6, chain)?;
                for action in ["-F", "-X"] {
                    let args = [action, chain.as_str()];
                    let output = run_ip6tables(&args)?;
                    if !output.status.success() {
                        return Err(command_failure("ip6tables", &args, &output));
                    }
                }
                Ok(())
            })();
            match result {
                Ok(()) => removed += 1,
                Err(err) => {
                    last_error = Some(err.message.clone());
                    warn!(chain, error = %err.message, "failed to garbage-collect orphan ip6tables chain");
                }
            }
        }
    }

    if removed > 0 {
        info!(
            removed,
            "garbage-collected orphan iptables/ip6tables chains"
        );
    }
    if removed == 0 {
        if let Some(error) = last_error {
            return Err(ApiError::internal(format!(
                "iptables orphan GC made no progress: {error}"
            )));
        }
    }
    Ok(removed)
}

// ---- Block Rules (abuse blocking via iptables string match) ----

const BLOCK_CHAIN: &str = "ABUSE_BLOCK";
const BLOCK_CHAIN_V6: &str = "ABUSE_BLOCK6";
const BLOCK_RULES_FILE: &str = "/opt/oneclickvirt/agent/block_rules.json";

#[derive(serde::Serialize, serde::Deserialize)]
struct PersistedBlockRules {
    strings: Vec<String>,
    ip_version: String,
}

/// Create ABUSE_BLOCK chain and add jumps from FORWARD and OUTPUT.
fn ensure_block_chains_ipt(program: &str, chain: &str) -> Result<(), ApiError> {
    ensure_chain_ipt(program, chain)?;

    // Add jump from FORWARD
    let fwd = run_ipt(program, &["-C", "FORWARD", "-j", chain]);
    if !matches!(fwd, Ok(o) if o.status.success()) {
        let out = run_ipt(program, &["-I", "FORWARD", "-j", chain])?;
        if !out.status.success() {
            return Err(ApiError::internal(format!(
                "failed to add FORWARD jump to {chain}: {}",
                String::from_utf8_lossy(&out.stderr).trim()
            )));
        }
    }

    // Add jump from OUTPUT
    let outp = run_ipt(program, &["-C", "OUTPUT", "-j", chain]);
    if !matches!(outp, Ok(o) if o.status.success()) {
        let out = run_ipt(program, &["-I", "OUTPUT", "-j", chain])?;
        if !out.status.success() {
            return Err(ApiError::internal(format!(
                "failed to add OUTPUT jump to {chain}: {}",
                String::from_utf8_lossy(&out.stderr).trim()
            )));
        }
    }

    Ok(())
}

/// Flush all rules in the block chain (keep the chain itself, preserving FORWARD/OUTPUT jumps).
fn flush_block_chain_ipt(program: &str, chain: &str) {
    if chain_exists_ipt(program, chain) {
        let _ = run_ipt(program, &["-F", chain]);
    }
}

/// Remove ABUSE_BLOCK chain and its jumps entirely.
fn remove_block_chains_ipt(program: &str, chain: &str) {
    if chain_exists_ipt(program, chain) {
        let _ = run_ipt(program, &["-D", "FORWARD", "-j", chain]);
        let _ = run_ipt(program, &["-D", "OUTPUT", "-j", chain]);
        let _ = run_ipt(program, &["-F", chain]);
        let _ = run_ipt(program, &["-X", chain]);
    }
}

/// Apply string-match block rules using iptables `-m string --algo bm`.
/// ip_version: "both" (default), "ipv4", "ipv6"
pub fn apply_block_rules(strings: &[String], ip_version: &str) -> Result<usize, ApiError> {
    if strings.is_empty() {
        return Ok(0);
    }

    let use_v4 = ip_version != "ipv6" && has_iptables();
    let use_v6 = ip_version != "ipv4" && has_ip6tables();

    if use_v4 {
        ensure_block_chains_ipt("iptables", BLOCK_CHAIN)?;
        flush_block_chain_ipt("iptables", BLOCK_CHAIN);
    }
    if use_v6 {
        ensure_block_chains_ipt("ip6tables", BLOCK_CHAIN_V6)?;
        flush_block_chain_ipt("ip6tables", BLOCK_CHAIN_V6);
    }

    let mut count = 0usize;
    for s in strings {
        if s.is_empty() {
            continue;
        }
        if s.len() > 128 {
            continue;
        }
        // Only allow printable ASCII to prevent iptables argument issues
        if !s.chars().all(|c| c.is_ascii_graphic() || c == ' ') {
            continue;
        }

        if use_v4 {
            let _ = run_iptables(&[
                "-A",
                BLOCK_CHAIN,
                "-m",
                "string",
                "--algo",
                "bm",
                "--string",
                s,
                "-j",
                "DROP",
            ]);
        }
        if use_v6 {
            let _ = run_ip6tables(&[
                "-A",
                BLOCK_CHAIN_V6,
                "-m",
                "string",
                "--algo",
                "bm",
                "--string",
                s,
                "-j",
                "DROP",
            ]);
        }
        count += 1;
    }

    // Persist for restart recovery
    let persisted = PersistedBlockRules {
        strings: strings.to_vec(),
        ip_version: ip_version.to_string(),
    };
    if let Ok(json) = serde_json::to_string(&persisted) {
        let _ = std::fs::write(std::path::Path::new(BLOCK_RULES_FILE), json);
    }

    info!(count, ip_version, "applied abuse block rules via iptables");
    Ok(count)
}

/// Remove all iptables block rules and the ABUSE_BLOCK chain.
pub fn remove_block_rules() -> Result<(), ApiError> {
    if has_iptables() {
        remove_block_chains_ipt("iptables", BLOCK_CHAIN);
    }
    if has_ip6tables() {
        remove_block_chains_ipt("ip6tables", BLOCK_CHAIN_V6);
    }
    let _ = std::fs::remove_file(std::path::Path::new(BLOCK_RULES_FILE));
    info!("removed abuse block rules (iptables)");
    Ok(())
}

/// Get current block rules from the persisted file.
pub fn get_block_rules() -> (Vec<String>, String) {
    if let Ok(content) = std::fs::read_to_string(std::path::Path::new(BLOCK_RULES_FILE)) {
        if let Ok(p) = serde_json::from_str::<PersistedBlockRules>(&content) {
            return (p.strings, p.ip_version);
        }
        // Fallback: old plain-array format
        if let Ok(strings) = serde_json::from_str::<Vec<String>>(&content) {
            return (strings, "both".to_string());
        }
    }
    (Vec::new(), "both".to_string())
}

/// Restore block rules from the persisted file on startup.
pub fn restore_block_rules() {
    let (strings, ip_version) = get_block_rules();
    if strings.is_empty() {
        return;
    }
    match apply_block_rules(&strings, &ip_version) {
        Ok(count) => info!(
            count,
            ip_version, "restored persisted block rules on startup (iptables)"
        ),
        Err(e) => warn!(error = %e.message, "failed to restore block rules on startup (iptables)"),
    }
}

#[cfg(test)]
mod tests {
    use super::{parse_chain_bytes, parse_rules_targeting_chain};

    #[test]
    fn parses_counter_only_rule_bytes_after_exclusions() {
        let output = r#"
Chain VM_IN_1 (1 references)
 pkts bytes target     prot opt in     out     source               destination
   10  1000 RETURN     all  --  *      *       10.0.0.0/8           0.0.0.0/0
    5   500            all  --  *      *       0.0.0.0/0            203.0.113.2        /* oneclickvirt-traffic-count */
"#;
        assert_eq!(parse_chain_bytes(output), 500);
    }

    #[test]
    fn parses_interface_scoped_jump_rules_for_gc() {
        let output = r#"
-N VM_TRAFFIC_FWD
-A VM_TRAFFIC_FWD -o veth-old -j VM_IN_7_deadbeef
-A VM_TRAFFIC_FWD -i veth-old -j VM_OUT_7_deadbeef
-A VM_TRAFFIC_FWD -o veth-live -j VM_IN_8_feedface
"#;
        assert_eq!(
            parse_rules_targeting_chain(output, "VM_TRAFFIC_FWD", "VM_IN_7_deadbeef"),
            vec![vec![
                "-o".to_string(),
                "veth-old".to_string(),
                "-j".to_string(),
                "VM_IN_7_deadbeef".to_string(),
            ]]
        );
    }
}
