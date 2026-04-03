use crate::error::ApiError;
use rusqlite::Connection;
use serde_json::Value;
use std::{
    collections::HashSet,
    env, fs,
    io::Write,
    path::Path,
    process::{Command, Stdio},
    sync::OnceLock,
};
use tracing::{debug, info, warn};

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
    if let Ok(target) = fs::read_link(master_link) {
        if let Some(name) = Path::new(&target).file_name().and_then(|x| x.to_str()) {
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

fn query_counter_bytes(scope: Scope, counter: &str) -> Result<Option<u64>, ApiError> {
    let out = run_nft(&["-j", "list", "counter", scope.family, scope.table, counter])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if is_not_found(&stderr) {
            return Ok(None);
        }
        return Err(ApiError::internal(format!(
            "failed listing nft counter {} {} {}: {}",
            scope.family,
            scope.table,
            counter,
            stderr.trim()
        )));
    }

    let json: Value = serde_json::from_slice(&out.stdout)
        .map_err(|e| ApiError::internal(format!("failed parsing nft json for {counter}: {e}")))?;
    let bytes = json["nftables"]
        .as_array()
        .and_then(|items| {
            items.iter().find_map(|item| {
                item.get("counter")
                    .and_then(|counter_obj| counter_obj.get("bytes"))
                    .and_then(Value::as_u64)
            })
        })
        .ok_or_else(|| {
            ApiError::internal(format!(
                "nft counter json missing bytes field for {counter}"
            ))
        })?;

    Ok(Some(bytes))
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

fn build_rules_for_counter(
    scope: Scope,
    counter_in: &str,
    counter_out: &str,
    interface: &str,
    inner_ip: Option<&str>,
) -> Result<String, ApiError> {
    let aliases = interface_aliases(interface);
    if aliases.is_empty() {
        return Err(ApiError::internal("interface aliases cannot be empty"));
    }

    let comment_in = format!("vmtm:{counter_in}:{}", current_config_tag());
    let comment_out = format!("vmtm:{counter_out}:{}", current_config_tag());
    let private_v4_set = nft_set_literal(exclude_v4());
    let private_v6_set = nft_set_literal(exclude_v6());
    let mut script = String::new();

    // Determine if inner_ip is IPv4 or IPv6
    let is_ipv6 = inner_ip.map(|ip| ip.contains(':')).unwrap_or(false);

    for name in aliases {
        let iface = escape_quoted(&name);
        match inner_ip {
            Some(ip) if !is_ipv6 => {
                // Per-IP filtering for IPv4 inner_ip:
                // In inet/forward, packets from the VM enter via iifname=<vm iface> and
                // replies back to the VM leave via oifname=<vm iface>.
                // Inbound: traffic leaving toward inner_ip on the VM iface → _in counter
                // Outbound: traffic entering from inner_ip on the VM iface → _out counter
                script.push_str(&format!(
                    "add rule {} {} {} oifname \"{}\" ip saddr != {} ip daddr {} counter name {} comment \"{}\"\n\
                     add rule {} {} {} iifname \"{}\" ip saddr {} ip daddr != {} counter name {} comment \"{}\"\n",
                    scope.family, scope.table, scope.chain, iface, private_v4_set, ip, counter_in, comment_in,
                    scope.family, scope.table, scope.chain, iface, ip, private_v4_set, counter_out, comment_out,
                ));
            }
            Some(ip) => {
                // Per-IP filtering for IPv6 inner_ip
                script.push_str(&format!(
                    "add rule {} {} {} oifname \"{}\" ip6 saddr != {} ip6 daddr {} counter name {} comment \"{}\"\n\
                     add rule {} {} {} iifname \"{}\" ip6 saddr {} ip6 daddr != {} counter name {} comment \"{}\"\n",
                    scope.family, scope.table, scope.chain, iface, private_v6_set, ip, counter_in, comment_in,
                    scope.family, scope.table, scope.chain, iface, ip, private_v6_set, counter_out, comment_out,
                ));
            }
            None => {
                // No inner_ip: fallback to interface-based counting (exclude private ranges)
                // Same directionality as above:
                // oifname = inbound to the VM, iifname = outbound from the VM.
                script.push_str(&format!(
                    "add rule {} {} {} oifname \"{}\" ip daddr != {} counter name {} comment \"{}\"\n\
                     add rule {} {} {} iifname \"{}\" ip saddr != {} counter name {} comment \"{}\"\n\
                     add rule {} {} {} oifname \"{}\" ip6 daddr != {} counter name {} comment \"{}\"\n\
                     add rule {} {} {} iifname \"{}\" ip6 saddr != {} counter name {} comment \"{}\"\n",
                    scope.family, scope.table, scope.chain, iface, private_v4_set, counter_in, comment_in,
                    scope.family, scope.table, scope.chain, iface, private_v4_set, counter_out, comment_out,
                    scope.family, scope.table, scope.chain, iface, private_v6_set, counter_in, comment_in,
                    scope.family, scope.table, scope.chain, iface, private_v6_set, counter_out, comment_out,
                ));
            }
        }
    }
    Ok(script)
}

fn add_rules_for_counter(
    scope: Scope,
    counter_in: &str,
    counter_out: &str,
    interface: &str,
    inner_ip: Option<&str>,
) -> Result<(), ApiError> {
    let script = build_rules_for_counter(scope, counter_in, counter_out, interface, inner_ip)?;
    run_nft_script(&script)
}

fn ensure_counter_in_scope(
    scope: Scope,
    monitor_id: i64,
    interface: &str,
    inner_ip: Option<&str>,
) -> Result<(), ApiError> {
    ensure_base_objects(scope)?;
    let alias_count = interface_aliases(interface).len();
    let expected_rules = expected_rule_count(scope, alias_count, inner_ip.is_some());
    let counter_in = counter_name_in(scope, monitor_id, interface);
    let counter_out = counter_name_out(scope, monitor_id, interface);
    let counter_in_exists = query_counter_bytes(scope, &counter_in)?.is_some();
    let counter_out_exists = query_counter_bytes(scope, &counter_out)?.is_some();
    // Rules referencing either _in or _out counter
    let refs_in = find_rule_refs_by_counter(scope, &counter_in)?;
    let refs_out = find_rule_refs_by_counter(scope, &counter_out)?;
    let rule_count = refs_in.len() + refs_out.len();
    let expected_comment_in = format!("vmtm:{counter_in}:{}", current_config_tag());
    let expected_comment_out = format!("vmtm:{counter_out}:{}", current_config_tag());
    let comment_ok = refs_in
        .iter()
        .all(|(_, _, line)| line.contains(&expected_comment_in))
        && refs_out
            .iter()
            .all(|(_, _, line)| line.contains(&expected_comment_out));

    if counter_in_exists && counter_out_exists && rule_count == expected_rules && comment_ok {
        return Ok(());
    }

    warn!(
        family = scope.family,
        table = scope.table,
        monitor_id,
        interface,
        counter_in_exists,
        counter_out_exists,
        rule_count,
        expected_rules,
        comment_ok,
        alias_count,
        "nft counter/rules state not healthy, reconciling"
    );

    if !counter_in_exists {
        let script = format!(
            "add counter {} {} {}\n",
            scope.family, scope.table, counter_in
        );
        run_nft_script(&script)?;
    }
    if !counter_out_exists {
        let script = format!(
            "add counter {} {} {}\n",
            scope.family, scope.table, counter_out
        );
        run_nft_script(&script)?;
    }

    let _ = remove_rules_by_counter(scope, &counter_in)?;
    let _ = remove_rules_by_counter(scope, &counter_out)?;
    add_rules_for_counter(scope, &counter_in, &counter_out, interface, inner_ip)?;
    Ok(())
}

pub fn ensure_counter(
    monitor_id: i64,
    interface: &str,
    inner_ip: Option<&str>,
) -> Result<(), ApiError> {
    let mut success = 0usize;
    let mut last_error: Option<String> = None;

    for scope in SCOPES {
        match ensure_counter_in_scope(scope, monitor_id, interface, inner_ip) {
            Ok(()) => {
                success += 1;
            }
            Err(err) => {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    monitor_id,
                    interface,
                    error = %err.message,
                    "failed to ensure counter in scope"
                );
                last_error = Some(err.message);
            }
        }
    }

    if success > 0 {
        Ok(())
    } else {
        Err(ApiError::internal(format!(
            "failed to ensure counter in all scopes: {}",
            last_error.unwrap_or_else(|| "unknown error".to_string())
        )))
    }
}

pub fn remove_counter(monitor_id: i64, interface: &str) -> Result<(), ApiError> {
    for scope in SCOPES {
        let ci = counter_name_in(scope, monitor_id, interface);
        let co = counter_name_out(scope, monitor_id, interface);
        remove_counter_by_name(scope, &ci)?;
        remove_counter_by_name(scope, &co)?;
    }
    Ok(())
}

fn list_existing_managed_counters(scope: Scope) -> Result<HashSet<String>, ApiError> {
    let out = run_nft(&["-j", "list", "table", scope.family, scope.table])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if is_not_found(&stderr) {
            return Ok(HashSet::new());
        }
        return Err(ApiError::internal(format!(
            "failed listing nft table json: {}",
            stderr.trim()
        )));
    }

    let json: Value = serde_json::from_slice(&out.stdout)
        .map_err(|e| ApiError::internal(format!("failed parsing nft table json: {e}")))?;
    let mut set = HashSet::new();
    if let Some(items) = json.get("nftables").and_then(Value::as_array) {
        for item in items {
            if let Some(name) = item
                .get("counter")
                .and_then(|c| c.get("name"))
                .and_then(Value::as_str)
            {
                set.insert(name.to_owned());
            }
        }
    }
    Ok(set)
}

fn expected_counters_from_db_for_scope(
    conn: &Connection,
    scope: Scope,
) -> Result<HashSet<String>, ApiError> {
    let mut stmt = conn
        .prepare("SELECT monitor_id, interface FROM interface_states")
        .map_err(|e| ApiError::internal(format!("prepare expected counters query error: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok((row.get::<_, i64>(0)?, row.get::<_, String>(1)?))
        })
        .map_err(|e| ApiError::internal(format!("expected counters query error: {e}")))?;

    let mut expected = HashSet::new();
    for row in rows {
        let (monitor_id, interface) =
            row.map_err(|e| ApiError::internal(format!("expected counters row error: {e}")))?;
        expected.insert(counter_name_in(scope, monitor_id, &interface));
        expected.insert(counter_name_out(scope, monitor_id, &interface));
    }
    Ok(expected)
}

pub fn garbage_collect_orphans(conn: &Connection) -> Result<usize, ApiError> {
    let mut total_removed = 0usize;
    let mut success_scopes = 0usize;
    let mut last_error: Option<String> = None;

    for scope in SCOPES {
        match (|| -> Result<usize, ApiError> {
            ensure_base_objects(scope)?;
            let existing = list_existing_managed_counters(scope)?;
            let expected = expected_counters_from_db_for_scope(conn, scope)?;
            let mut removed = 0usize;
            for counter in existing.difference(&expected) {
                remove_counter_by_name(scope, counter)?;
                removed += 1;
            }
            Ok(removed)
        })() {
            Ok(removed) => {
                success_scopes += 1;
                total_removed += removed;
            }
            Err(err) => {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    error = %err.message,
                    "orphan GC skipped for scope due to error"
                );
                last_error = Some(err.message);
            }
        }
    }

    if total_removed > 0 {
        info!(total_removed, "garbage-collected orphan nft counters/rules");
    }
    if success_scopes > 0 {
        Ok(total_removed)
    } else {
        Err(ApiError::internal(format!(
            "orphan GC failed in all scopes: {}",
            last_error.unwrap_or_else(|| "unknown error".to_string())
        )))
    }
}

pub fn read_external_bytes(monitor_id: i64, interface: &str) -> Option<(u64, u64)> {
    let mut total_in = 0u64;
    let mut total_out = 0u64;
    let mut has_any_counter = false;
    let mut missing_counter_scopes = 0usize;

    for scope in SCOPES {
        let ci = counter_name_in(scope, monitor_id, interface);
        let co = counter_name_out(scope, monitor_id, interface);
        let mut scope_ok = false;
        match query_counter_bytes(scope, &ci) {
            Ok(Some(bytes)) => {
                total_in = total_in.saturating_add(bytes);
                scope_ok = true;
            }
            Ok(None) => {}
            Err(err) => {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    monitor_id,
                    interface,
                    error = %err.message,
                    "failed to read nft counter bytes (in)"
                );
            }
        }
        match query_counter_bytes(scope, &co) {
            Ok(Some(bytes)) => {
                total_out = total_out.saturating_add(bytes);
                scope_ok = true;
            }
            Ok(None) => {}
            Err(err) => {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    monitor_id,
                    interface,
                    error = %err.message,
                    "failed to read nft counter bytes (out)"
                );
            }
        }
        if scope_ok {
            has_any_counter = true;
        } else {
            missing_counter_scopes += 1;
        }
    }

    if has_any_counter {
        return Some((total_in, total_out));
    }

    if missing_counter_scopes > 0 {
        if let Err(err) = ensure_counter(monitor_id, interface, None) {
            warn!(
                monitor_id,
                interface,
                error = %err.message,
                "counter missing and reconciliation failed"
            );
            return None;
        }

        let mut retry_in = 0u64;
        let mut retry_out = 0u64;
        let mut retry_has_any = false;
        for scope in SCOPES {
            let ci = counter_name_in(scope, monitor_id, interface);
            let co = counter_name_out(scope, monitor_id, interface);
            match query_counter_bytes(scope, &ci) {
                Ok(Some(bytes)) => {
                    retry_in = retry_in.saturating_add(bytes);
                    retry_has_any = true;
                }
                Ok(None) => {}
                Err(err) => {
                    warn!(
                        family = scope.family,
                        table = scope.table,
                        monitor_id,
                        interface,
                        error = %err.message,
                        "failed to read nft counter bytes (in) after reconciliation"
                    );
                }
            }
            match query_counter_bytes(scope, &co) {
                Ok(Some(bytes)) => {
                    retry_out = retry_out.saturating_add(bytes);
                    retry_has_any = true;
                }
                Ok(None) => {}
                Err(err) => {
                    warn!(
                        family = scope.family,
                        table = scope.table,
                        monitor_id,
                        interface,
                        error = %err.message,
                        "failed to read nft counter bytes (out) after reconciliation"
                    );
                }
            }
        }
        if retry_has_any {
            return Some((retry_in, retry_out));
        }
    }

    None
}

pub fn bootstrap_from_db(conn: &Connection) -> Result<(), ApiError> {
    let mut success_scopes = 0usize;
    let mut last_error: Option<String> = None;
    for scope in SCOPES {
        match ensure_base_objects(scope) {
            Ok(()) => success_scopes += 1,
            Err(err) => {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    error = %err.message,
                    "failed to ensure base objects for scope during bootstrap"
                );
                last_error = Some(err.message);
            }
        }
    }
    if success_scopes == 0 {
        return Err(ApiError::internal(format!(
            "bootstrap failed in all scopes: {}",
            last_error.unwrap_or_else(|| "unknown error".to_string())
        )));
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
                "bootstrap failed to ensure nft counter"
            );
        } else {
            count += 1;
        }
    }
    debug!(count, "nft bootstrap ensured counters");
    Ok(())
}

#[cfg(test)]
mod tests {
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
