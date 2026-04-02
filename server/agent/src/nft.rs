use crate::error::ApiError;
use rusqlite::Connection;
use serde_json::Value;
use std::{
    collections::HashSet,
    env,
    fs,
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

const SCOPE_BRIDGE: Scope = Scope {
    family: "bridge",
    table: "vm_traffic_monitor_br",
    chain: "forward",
    hook: "forward",
    tag: "bridge",
};

const SCOPES: [Scope; 2] = [SCOPE_INET, SCOPE_BRIDGE];

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
        let combined = format!("{},{}", exclude_v4().join(","), exclude_v6().join(","));
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
    info!(family = scope.family, table = scope.table, "created nft runtime table/chain");
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

fn expected_rule_count(scope: Scope, alias_count: usize) -> usize {
    match scope.tag {
        "inet" => alias_count * 4,
        "bridge" => alias_count * 4,
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
            ApiError::internal(format!("nft counter json missing bytes field for {counter}"))
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

fn find_rule_refs_by_counter(scope: Scope, counter: &str) -> Result<Vec<(String, i64, String)>, ApiError> {
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
    for (chain, handle, _) in refs {
        let out = run_nft(&[
            "delete",
            "rule",
            scope.family,
            scope.table,
            &chain,
            "handle",
            &handle.to_string(),
        ])?;
        if !out.status.success() {
            let stderr = String::from_utf8_lossy(&out.stderr);
            if !is_not_found(&stderr) {
                return Err(ApiError::internal(format!(
                    "failed deleting nft rule handle {handle} in chain {chain}: {}",
                    stderr.trim()
                )));
            }
        } else {
            removed += 1;
        }
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
            warn!(counter, "counter still busy after rule cleanup, skipping hard delete");
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

fn add_rules_for_counter(scope: Scope, counter: &str, interface: &str) -> Result<(), ApiError> {
    let aliases = interface_aliases(interface);
    if aliases.is_empty() {
        return Err(ApiError::internal("interface aliases cannot be empty"));
    }

    let comment = format!("vmtm:{counter}:{}", current_config_tag());
    let private_v4_set = nft_set_literal(exclude_v4());
    let private_v6_set = nft_set_literal(exclude_v6());
    let mut script = String::new();
    for name in aliases {
        let iface = escape_quoted(&name);
        if scope.tag == "inet" {
            script.push_str(&format!(
                "add rule {} {} {} iifname \"{}\" ip daddr != {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} oifname \"{}\" ip saddr != {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} iifname \"{}\" ip6 daddr != {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} oifname \"{}\" ip6 saddr != {} counter name {} comment \"{}\"\n",
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v4_set,
                counter,
                comment,
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v4_set,
                counter,
                comment,
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v6_set,
                counter,
                comment,
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v6_set,
                counter,
                comment
            ));
        } else {
            script.push_str(&format!(
                "add rule {} {} {} iifname \"{}\" ether type ip ip daddr != {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} oifname \"{}\" ether type ip ip saddr != {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} iifname \"{}\" ether type ip6 ip6 daddr != {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} oifname \"{}\" ether type ip6 ip6 saddr != {} counter name {} comment \"{}\"\n",
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v4_set,
                counter,
                comment,
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v4_set,
                counter,
                comment,
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v6_set,
                counter,
                comment,
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v6_set,
                counter,
                comment
            ));
        }
    }
    run_nft_script(&script)
}

fn ensure_counter_in_scope(scope: Scope, monitor_id: i64, interface: &str) -> Result<(), ApiError> {
    ensure_base_objects(scope)?;
    let alias_count = interface_aliases(interface).len();
    let expected_rules = expected_rule_count(scope, alias_count);
    let counter = counter_name(scope, monitor_id, interface);
    let counter_exists = query_counter_bytes(scope, &counter)?.is_some();
    let refs = find_rule_refs_by_counter(scope, &counter)?;
    let rule_count = refs.len();
    let expected_comment = format!("vmtm:{counter}:{}", current_config_tag());
    let comment_ok = refs.iter().all(|(_, _, line)| line.contains(&expected_comment));

    if counter_exists && rule_count == expected_rules && comment_ok {
        return Ok(());
    }

    warn!(
        family = scope.family,
        table = scope.table,
        monitor_id,
        interface,
        counter,
        counter_exists,
        rule_count,
        expected_rules,
        comment_ok,
        alias_count,
        "nft counter/rules state not healthy, reconciling"
    );

    if !counter_exists {
        let script = format!("add counter {} {} {}\n", scope.family, scope.table, counter);
        run_nft_script(&script)?;
    }

    let _ = remove_rules_by_counter(scope, &counter)?;
    add_rules_for_counter(scope, &counter, interface)?;
    Ok(())
}

pub fn ensure_counter(monitor_id: i64, interface: &str) -> Result<(), ApiError> {
    let mut success = 0usize;
    let mut last_error: Option<String> = None;

    for scope in SCOPES {
        match ensure_counter_in_scope(scope, monitor_id, interface) {
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
        let counter = counter_name(scope, monitor_id, interface);
        remove_counter_by_name(scope, &counter)?;
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

fn expected_counters_from_db_for_scope(conn: &Connection, scope: Scope) -> Result<HashSet<String>, ApiError> {
    let mut stmt = conn
        .prepare("SELECT monitor_id, interface FROM interface_states")
        .map_err(|e| ApiError::internal(format!("prepare expected counters query error: {e}")))?;
    let rows = stmt
        .query_map([], |row| Ok((row.get::<_, i64>(0)?, row.get::<_, String>(1)?)))
        .map_err(|e| ApiError::internal(format!("expected counters query error: {e}")))?;

    let mut expected = HashSet::new();
    for row in rows {
        let (monitor_id, interface) =
            row.map_err(|e| ApiError::internal(format!("expected counters row error: {e}")))?;
        expected.insert(counter_name(scope, monitor_id, &interface));
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

pub fn read_external_bytes(monitor_id: i64, interface: &str) -> Option<u64> {
    let mut total = 0u64;
    let mut has_any_counter = false;
    let mut missing_counter_scopes = 0usize;

    for scope in SCOPES {
        let counter = counter_name(scope, monitor_id, interface);
        match query_counter_bytes(scope, &counter) {
            Ok(Some(bytes)) => {
                total = total.saturating_add(bytes);
                has_any_counter = true;
            }
            Ok(None) => {
                missing_counter_scopes += 1;
            }
            Err(err) => {
                warn!(
                    family = scope.family,
                    table = scope.table,
                    monitor_id,
                    interface,
                    error = %err.message,
                    "failed to read nft counter bytes"
                );
            }
        }
    }

    if has_any_counter {
        return Some(total);
    }

    if missing_counter_scopes > 0 {
        if let Err(err) = ensure_counter(monitor_id, interface) {
            warn!(
                monitor_id,
                interface,
                error = %err.message,
                "counter missing and reconciliation failed"
            );
            return None;
        }

        let mut retry_total = 0u64;
        let mut retry_has_any = false;
        for scope in SCOPES {
            let counter = counter_name(scope, monitor_id, interface);
            match query_counter_bytes(scope, &counter) {
                Ok(Some(bytes)) => {
                    retry_total = retry_total.saturating_add(bytes);
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
                        "failed to read nft counter bytes after reconciliation"
                    );
                }
            }
        }
        if retry_has_any {
            return Some(retry_total);
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
        .prepare("SELECT monitor_id, interface FROM interface_states")
        .map_err(|e| ApiError::internal(format!("prepare bootstrap query error: {e}")))?;
    let rows = stmt
        .query_map([], |row| Ok((row.get::<_, i64>(0)?, row.get::<_, String>(1)?)))
        .map_err(|e| ApiError::internal(format!("bootstrap query error: {e}")))?;

    let mut count = 0usize;
    for row in rows {
        let (monitor_id, interface) =
            row.map_err(|e| ApiError::internal(format!("bootstrap row error: {e}")))?;
        if let Err(err) = ensure_counter(monitor_id, &interface) {
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
