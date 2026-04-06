mod block;
mod counter;
mod gc;

use crate::error::ApiError;
use std::{
    env,
    io::Write,
    process::{Command, Stdio},
    sync::OnceLock,
};
use tracing::{info, warn};

// Non-public/special-use ranges (broader than RFC1918) to avoid counting internal traffic.
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
static CONFIG_TAG: OnceLock<String> = OnceLock::new();
const RULES_VERSION: &str = "v2";

#[derive(Copy, Clone)]
struct Scope {
    family: &'static str,
    table: &'static str,
    chain: &'static str,
    hook: &'static str,
    tag: &'static str,
}

const SCOPE_INET: Scope = Scope {
    family: "inet",
    table: "vm_traffic_monitor",
    chain: "forward",
    hook: "forward",
    tag: "inet",
};

/// Only use inet scope. Bridge scope sees the same packets as inet forward,
/// causing double-counting. A single inet forward chain is sufficient for all
/// container/VM traffic monitoring.
const SCOPES: [Scope; 1] = [SCOPE_INET];

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

fn nft_set_literal(cidrs: &[String]) -> String {
    format!("{{ {} }}", cidrs.join(", "))
}

fn current_config_tag() -> &'static String {
    CONFIG_TAG.get_or_init(|| {
        let combined = format!(
            "{RULES_VERSION}|{},{}",
            exclude_v4().join(","),
            exclude_v6().join(",")
        );
        format!("{:x}", fnv1a_64(combined.as_bytes()))
    })
}

fn run_nft(args: &[&str]) -> Result<std::process::Output, ApiError> {
    Command::new("nft")
        .args(args)
        .output()
        .map_err(|e| ApiError::internal(format!("failed to run nft {:?}: {e}", args)))
}

fn run_nft_script(script: &str) -> Result<(), ApiError> {
    let mut child = Command::new("nft")
        .args(["-f", "-"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .map_err(|e| ApiError::internal(format!("failed to spawn nft script: {e}")))?;

    if let Some(stdin) = child.stdin.as_mut() {
        stdin
            .write_all(script.as_bytes())
            .map_err(|e| ApiError::internal(format!("failed to write nft script: {e}")))?;
    }

    let output = child
        .wait_with_output()
        .map_err(|e| ApiError::internal(format!("failed waiting nft script: {e}")))?;
    if output.status.success() {
        return Ok(());
    }

    let stderr = String::from_utf8_lossy(&output.stderr);
    Err(ApiError::internal(format!(
        "nft script failed: {}",
        stderr.trim()
    )))
}

fn is_not_found(stderr: &str) -> bool {
    stderr.contains("No such file or directory") || stderr.contains("No such file")
}

fn ensure_base_objects(scope: Scope) -> Result<(), ApiError> {
    let out = run_nft(&["list", "table", scope.family, scope.table])?;
    if out.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&out.stderr);
    if !is_not_found(&stderr) {
        return Err(ApiError::internal(format!(
            "failed listing nft table {} {}: {}",
            scope.family,
            scope.table,
            stderr.trim()
        )));
    }

    let script = format!(
        "add table {} {}\nadd chain {} {} {} {{ type filter hook {} priority 0; policy accept; }}\n",
        scope.family, scope.table, scope.family, scope.table, scope.chain, scope.hook
    );
    run_nft_script(&script)?;
    info!(
        family = scope.family,
        table = scope.table,
        "created nft runtime table/chain"
    );
    Ok(())
}

fn escape_quoted(raw: &str) -> String {
    raw.replace('\\', "\\\\").replace('\"', "\\\"")
}

/// Stable FNV-1a 64-bit hash (deterministic across Rust versions unlike DefaultHasher).
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

fn counter_name(scope: Scope, monitor_id: i64, interface: &str) -> String {
    let h = fnv1a_64(interface.as_bytes());
    let prefix = if scope.tag == "inet" { "i" } else { "b" };
    format!("{prefix}m{monitor_id}_{h:x}")
}

fn counter_name_in(scope: Scope, monitor_id: i64, interface: &str) -> String {
    format!("{}_in", counter_name(scope, monitor_id, interface))
}

fn counter_name_out(scope: Scope, monitor_id: i64, interface: &str) -> String {
    format!("{}_out", counter_name(scope, monitor_id, interface))
}

fn interface_aliases(interface: &str) -> Vec<String> {
    let mut aliases = Vec::new();
    aliases.push(interface.to_string());

    let master_link = format!("/sys/class/net/{interface}/master");
    if let Ok(target) = std::fs::read_link(master_link) {
        if let Some(name) = std::path::Path::new(&target).file_name().and_then(|x| x.to_str()) {
            if !name.is_empty() && !aliases.iter().any(|v| v == name) {
                aliases.push(name.to_string());
            }
        }
    }
    aliases
}

fn expected_rule_count(scope: Scope, alias_count: usize, has_inner_ip: bool) -> usize {
    match scope.tag {
        "inet" => {
            if has_inner_ip {
                // Per-IP: 2 rules per alias (inbound to _in counter + outbound to _out counter)
                alias_count * 2
            } else {
                // Fallback: 4 rules per alias (IPv4 in/out + IPv6 in/out)
                alias_count * 4
            }
        }
        _ => 0,
    }
}

fn list_table_with_handles_text(scope: Scope) -> Result<String, ApiError> {
    let out = run_nft(&["-a", "list", "table", scope.family, scope.table])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if is_not_found(&stderr) {
            return Ok(String::new());
        }
        return Err(ApiError::internal(format!(
            "failed listing nft table with handles: {}",
            stderr.trim()
        )));
    }

    Ok(String::from_utf8_lossy(&out.stdout).to_string())
}

fn parse_chain_start(line: &str) -> Option<String> {
    let t = line.trim();
    if !t.starts_with("chain ") {
        return None;
    }
    let rest = &t["chain ".len()..];
    let name = rest.split_whitespace().next()?;
    Some(name.to_owned())
}

fn parse_handle(line: &str) -> Option<i64> {
    let (_, h) = line.rsplit_once("# handle ")?;
    h.trim().parse::<i64>().ok()
}

fn find_rule_refs_by_counter(
    scope: Scope,
    counter: &str,
) -> Result<Vec<(String, i64, String)>, ApiError> {
    let text = list_table_with_handles_text(scope)?;
    let mut refs = Vec::new();
    let mut current_chain: Option<String> = None;
    let needle_quoted = format!("counter name \"{counter}\"");
    let needle_plain = format!("counter name {counter}");

    for line in text.lines() {
        if let Some(chain) = parse_chain_start(line) {
            current_chain = Some(chain);
            continue;
        }
        let trimmed = line.trim();
        if trimmed == "}" {
            current_chain = None;
            continue;
        }
        let Some(chain) = current_chain.as_ref() else {
            continue;
        };
        if !(trimmed.contains(&needle_quoted) || trimmed.contains(&needle_plain)) {
            continue;
        }
        if let Some(handle) = parse_handle(trimmed) {
            refs.push((chain.clone(), handle, trimmed.to_string()));
        }
    }
    Ok(refs)
}

fn remove_rules_by_counter(scope: Scope, counter: &str) -> Result<usize, ApiError> {
    let refs = find_rule_refs_by_counter(scope, counter)?;
    let mut removed = 0usize;
    let mut last_error: Option<String> = None;
    for (chain, handle, _) in &refs {
        let out = run_nft(&[
            "delete",
            "rule",
            scope.family,
            scope.table,
            chain,
            "handle",
            &handle.to_string(),
        ])?;
        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr);
            if is_not_found(&stderr) {
                removed += 1; // already gone
            } else {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    chain,
                    handle,
                    counter,
                    stderr = stderr.trim().to_string(),
                    "failed to delete nft rule, continuing with remaining rules"
                );
                last_error = Some(format!(
                    "failed deleting nft rule handle {handle} in chain {chain}: {}",
                    stderr.trim()
                ));
            }
        } else {
            removed += 1;
        }
    }
    if removed == 0 && !refs.is_empty() {
        return Err(ApiError::internal(
            last_error.unwrap_or_else(|| "all rule deletions failed".to_string()),
        ));
    }
    Ok(removed)
}

fn remove_counter_by_name(scope: Scope, counter: &str) -> Result<(), ApiError> {
    ensure_base_objects(scope)?;
    let _ = remove_rules_by_counter(scope, counter)?;

    let out = run_nft(&["delete", "counter", scope.family, scope.table, counter])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if stderr.contains("Device or resource busy") {
            warn!(
                counter,
                "counter still busy after rule cleanup, skipping hard delete"
            );
            return Ok(());
        }
        if !is_not_found(&stderr) {
            return Err(ApiError::internal(format!(
                "failed deleting nft counter {counter}: {}",
                stderr.trim()
            )));
        }
    }
    Ok(())
}

// Re-export public API
pub use block::{apply_block_rules, get_block_rules, remove_block_rules, restore_block_rules};
pub use counter::{ensure_counter, read_external_bytes, remove_counter};
pub use gc::{bootstrap_from_db, garbage_collect_orphans};

#[cfg(test)]
mod tests {
    use super::counter::build_rules_for_counter;
    use super::*;

    #[test]
    fn per_ip_rules_use_vm_interface_as_iif_for_outbound_and_oif_for_inbound() {
        let script = build_rules_for_counter(
            SCOPE_INET,
            "counter_in",
            "counter_out",
            "veth123",
            Some("172.16.0.2"),
        )
        .expect("script should build");

        assert!(script.contains("oifname \"veth123\" ip saddr != {"));
        assert!(script.contains("ip daddr 172.16.0.2 counter name counter_in"));
        assert!(script.contains("iifname \"veth123\" ip saddr 172.16.0.2"));
        assert!(script.contains("ip daddr != {"));
    }

    #[test]
    fn fallback_rules_keep_same_directionality() {
        let script =
            build_rules_for_counter(SCOPE_INET, "counter_in", "counter_out", "tap101i0", None)
                .expect("script should build");

        assert!(script.contains("oifname \"tap101i0\" ip daddr !="));
        assert!(script.contains("iifname \"tap101i0\" ip saddr !="));
        assert!(script.contains("oifname \"tap101i0\" ip6 daddr !="));
        assert!(script.contains("iifname \"tap101i0\" ip6 saddr !="));
    }
}
