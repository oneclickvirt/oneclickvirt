use crate::error::ApiError;
use rusqlite::Connection;
use serde_json::Value;
use std::{collections::HashSet, sync::OnceLock};
use tracing::{info, warn};

use super::{
    SCOPE_INET, Scope, binding_config_tag, check_nft_script, counter_name_in, counter_name_out,
    escape_quoted, exclude_v4, exclude_v6, find_rule_refs_by_counter, fnv1a_64, is_not_found,
    nft_set_literal, remove_rules_by_counter, run_nft, run_nft_script,
};

const FAMILY: &str = "netdev";
const TABLE: &str = "vm_traffic_monitor_device";
const HOOK_PRIORITY: i64 = -1;
const CHAIN_PREFIX_IN: &str = "vmi_";
const CHAIN_PREFIX_OUT: &str = "vmo_";
static NETDEV_HOOKS_SUPPORTED: OnceLock<bool> = OnceLock::new();

const SCOPE_DEVICE: Scope = Scope {
    family: FAMILY,
    table: TABLE,
    chain: "",
    hook: "",
    tag: "netdev",
};

fn chain_names(monitor_id: i64, interface: &str) -> (String, String) {
    let identity = format!("{monitor_id}:{interface}");
    let hash = fnv1a_64(identity.as_bytes());
    (
        format!("{CHAIN_PREFIX_IN}{hash:x}"),
        format!("{CHAIN_PREFIX_OUT}{hash:x}"),
    )
}

pub(super) fn supported(interface: &str) -> bool {
    *NETDEV_HOOKS_SUPPORTED.get_or_init(|| {
        let iface = escape_quoted(interface);
        let probe_table = format!("ocv_nd_probe_{}", std::process::id());
        let script = format!(
            "add table {FAMILY} {probe_table}\n\
             add chain {FAMILY} {probe_table} ingress {{ type filter hook ingress device \"{iface}\" priority {HOOK_PRIORITY}; policy accept; }}\n\
             add chain {FAMILY} {probe_table} egress {{ type filter hook egress device \"{iface}\" priority {HOOK_PRIORITY}; policy accept; }}\n\
             delete table {FAMILY} {probe_table}\n"
        );
        match check_nft_script(&script) {
            Ok(()) => true,
            Err(err) => {
                warn!(
                    interface,
                    error = %err.message,
                    "netdev ingress/egress hooks unavailable; using forward-chain traffic counters"
                );
                false
            }
        }
    })
}

fn ensure_table() -> Result<(), ApiError> {
    let out = run_nft(&["list", "table", FAMILY, TABLE])?;
    if out.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&out.stderr);
    if !is_not_found(&stderr) {
        return Err(ApiError::internal(format!(
            "failed listing nft device table: {}",
            stderr.trim()
        )));
    }
    run_nft_script(&format!("add table {FAMILY} {TABLE}\n"))?;
    info!(
        family = FAMILY,
        table = TABLE,
        "created nft device traffic table"
    );
    Ok(())
}

fn list_table_json() -> Result<Option<Value>, ApiError> {
    let out = run_nft(&["-j", "list", "table", FAMILY, TABLE])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if is_not_found(&stderr) {
            return Ok(None);
        }
        return Err(ApiError::internal(format!(
            "failed listing nft device table json: {}",
            stderr.trim()
        )));
    }
    serde_json::from_slice(&out.stdout)
        .map(Some)
        .map_err(|e| ApiError::internal(format!("failed parsing nft device table json: {e}")))
}

fn counter_bytes(json: &Value, name: &str) -> Option<u64> {
    json.get("nftables")
        .and_then(Value::as_array)
        .and_then(|items| {
            items.iter().find_map(|item| {
                let counter = item.get("counter")?;
                (counter.get("name").and_then(Value::as_str) == Some(name))
                    .then(|| counter.get("bytes").and_then(Value::as_u64))
                    .flatten()
            })
        })
}

fn chain_exists(json: &Value, name: &str) -> bool {
    json.get("nftables")
        .and_then(Value::as_array)
        .is_some_and(|items| {
            items.iter().any(|item| {
                item.get("chain")
                    .and_then(|chain| chain.get("name"))
                    .and_then(Value::as_str)
                    == Some(name)
            })
        })
}

fn chain_matches(json: &Value, name: &str, hook: &str, interface: &str) -> bool {
    json.get("nftables")
        .and_then(Value::as_array)
        .and_then(|items| {
            items.iter().find_map(|item| {
                let chain = item.get("chain")?;
                (chain.get("name").and_then(Value::as_str) == Some(name)).then_some(chain)
            })
        })
        .is_some_and(|chain| {
            chain.get("family").and_then(Value::as_str) == Some(FAMILY)
                && chain.get("table").and_then(Value::as_str) == Some(TABLE)
                && chain.get("type").and_then(Value::as_str) == Some("filter")
                && chain.get("hook").and_then(Value::as_str) == Some(hook)
                && chain.get("dev").and_then(Value::as_str) == Some(interface)
                && chain.get("prio").and_then(Value::as_i64) == Some(HOOK_PRIORITY)
                && chain.get("policy").and_then(Value::as_str) == Some("accept")
        })
}

fn expected_rule_count(addresses: &[String], families: &[String]) -> usize {
    let expects_v4 = families.is_empty() || families.iter().any(|family| family == "ipv4");
    let expects_v6 = families.is_empty() || families.iter().any(|family| family == "ipv6");
    let v4_count = addresses
        .iter()
        .filter(|address| !address.contains(':'))
        .count();
    let v6_count = addresses
        .iter()
        .filter(|address| address.contains(':'))
        .count();
    (if expects_v4 { v4_count.max(1) * 2 } else { 0 })
        + (if expects_v6 { v6_count.max(1) * 2 } else { 0 })
}

pub(super) fn build_rules(
    chain_in: &str,
    chain_out: &str,
    counter_in: &str,
    counter_out: &str,
    addresses: &[String],
    families: &[String],
) -> String {
    let config_tag = binding_config_tag(addresses, families);
    let comment_in = format!("vmtm:{counter_in}:{config_tag}");
    let comment_out = format!("vmtm:{counter_out}:{config_tag}");
    let private_v4_set = nft_set_literal(exclude_v4());
    let private_v6_set = nft_set_literal(exclude_v6());
    let expects_v4 = families.is_empty() || families.iter().any(|family| family == "ipv4");
    let expects_v6 = families.is_empty() || families.iter().any(|family| family == "ipv6");
    let v4_addresses = addresses
        .iter()
        .filter(|address| !address.contains(':'))
        .collect::<Vec<_>>();
    let v6_addresses = addresses
        .iter()
        .filter(|address| address.contains(':'))
        .collect::<Vec<_>>();
    let mut script = String::new();

    if expects_v4 && v4_addresses.is_empty() {
        script.push_str(&format!(
            "add rule {FAMILY} {TABLE} {chain_in} ip saddr != {private_v4_set} counter name {counter_in} comment \"{comment_in}\"\n\
             add rule {FAMILY} {TABLE} {chain_out} ip daddr != {private_v4_set} counter name {counter_out} comment \"{comment_out}\"\n"
        ));
    }
    if expects_v6 && v6_addresses.is_empty() {
        script.push_str(&format!(
            "add rule {FAMILY} {TABLE} {chain_in} ip6 saddr != {private_v6_set} counter name {counter_in} comment \"{comment_in}\"\n\
             add rule {FAMILY} {TABLE} {chain_out} ip6 daddr != {private_v6_set} counter name {counter_out} comment \"{comment_out}\"\n"
        ));
    }
    for ip in v4_addresses {
        script.push_str(&format!(
            "add rule {FAMILY} {TABLE} {chain_in} ip saddr != {private_v4_set} ip daddr {ip} counter name {counter_in} comment \"{comment_in}\"\n\
             add rule {FAMILY} {TABLE} {chain_out} ip saddr {ip} ip daddr != {private_v4_set} counter name {counter_out} comment \"{comment_out}\"\n"
        ));
    }
    for ip in v6_addresses {
        script.push_str(&format!(
            "add rule {FAMILY} {TABLE} {chain_in} ip6 saddr != {private_v6_set} ip6 daddr {ip} counter name {counter_in} comment \"{comment_in}\"\n\
             add rule {FAMILY} {TABLE} {chain_out} ip6 saddr {ip} ip6 daddr != {private_v6_set} counter name {counter_out} comment \"{comment_out}\"\n"
        ));
    }
    script
}

pub(super) fn ensure_counter(
    monitor_id: i64,
    interface: &str,
    addresses: &[String],
    families: &[String],
    seed_in: u64,
    seed_out: u64,
) -> Result<(), ApiError> {
    ensure_table()?;
    let json = list_table_json()?.ok_or_else(|| ApiError::internal("nft device table missing"))?;
    let (chain_in, chain_out) = chain_names(monitor_id, interface);
    let counter_in = counter_name_in(SCOPE_INET, monitor_id, interface);
    let counter_out = counter_name_out(SCOPE_INET, monitor_id, interface);
    let current_in = counter_bytes(&json, &counter_in);
    let current_out = counter_bytes(&json, &counter_out);
    let refs_in = find_rule_refs_by_counter(SCOPE_DEVICE, &counter_in)?;
    let refs_out = find_rule_refs_by_counter(SCOPE_DEVICE, &counter_out)?;
    let config_tag = binding_config_tag(addresses, families);
    let comment_in = format!("vmtm:{counter_in}:{config_tag}");
    let comment_out = format!("vmtm:{counter_out}:{config_tag}");
    let rules_healthy = refs_in.len() + refs_out.len() == expected_rule_count(addresses, families)
        && refs_in
            .iter()
            .all(|(chain, _, line)| chain == &chain_in && line.contains(&comment_in))
        && refs_out
            .iter()
            .all(|(chain, _, line)| chain == &chain_out && line.contains(&comment_out));
    let chains_healthy = chain_matches(&json, &chain_in, "egress", interface)
        && chain_matches(&json, &chain_out, "ingress", interface);
    if current_in.is_some() && current_out.is_some() && chains_healthy && rules_healthy {
        return Ok(());
    }

    warn!(
        monitor_id,
        interface,
        counter_in_exists = current_in.is_some(),
        counter_out_exists = current_out.is_some(),
        chains_healthy,
        rules_healthy,
        "nft device counter state not healthy, reconciling"
    );

    let mut script = String::new();
    for chain in [&chain_in, &chain_out] {
        if chain_exists(&json, chain) {
            script.push_str(&format!(
                "flush chain {FAMILY} {TABLE} {chain}\ndelete chain {FAMILY} {TABLE} {chain}\n"
            ));
        }
    }
    if current_in.is_none() {
        script.push_str(&format!(
            "add counter {FAMILY} {TABLE} {counter_in} {{ packets 0 bytes {seed_in}; }}\n"
        ));
    }
    if current_out.is_none() {
        script.push_str(&format!(
            "add counter {FAMILY} {TABLE} {counter_out} {{ packets 0 bytes {seed_out}; }}\n"
        ));
    }
    let iface = escape_quoted(interface);
    script.push_str(&format!(
        "add chain {FAMILY} {TABLE} {chain_in} {{ type filter hook egress device \"{iface}\" priority {HOOK_PRIORITY}; policy accept; }}\n\
         add chain {FAMILY} {TABLE} {chain_out} {{ type filter hook ingress device \"{iface}\" priority {HOOK_PRIORITY}; policy accept; }}\n"
    ));
    script.push_str(&build_rules(
        &chain_in,
        &chain_out,
        &counter_in,
        &counter_out,
        addresses,
        families,
    ));
    run_nft_script(&script)
}

pub(super) fn read_external_bytes(
    monitor_id: i64,
    interface: &str,
) -> Result<Option<(u64, u64)>, ApiError> {
    let counter_in = counter_name_in(SCOPE_INET, monitor_id, interface);
    let counter_out = counter_name_out(SCOPE_INET, monitor_id, interface);
    Ok(query_counter_bytes(&counter_in)?.zip(query_counter_bytes(&counter_out)?))
}

fn query_counter_bytes(name: &str) -> Result<Option<u64>, ApiError> {
    let out = run_nft(&["-j", "list", "counter", FAMILY, TABLE, name])?;
    if !out.status.success() {
        let stderr = String::from_utf8_lossy(&out.stderr);
        if is_not_found(&stderr) {
            return Ok(None);
        }
        return Err(ApiError::internal(format!(
            "failed listing nft device counter {name}: {}",
            stderr.trim()
        )));
    }
    let json: Value = serde_json::from_slice(&out.stdout).map_err(|e| {
        ApiError::internal(format!("failed parsing nft device counter {name}: {e}"))
    })?;
    counter_bytes(&json, name)
        .map(Some)
        .ok_or_else(|| ApiError::internal(format!("nft device counter {name} has no byte value")))
}

fn delete_counter(name: &str) -> Result<(), ApiError> {
    let _ = remove_rules_by_counter(SCOPE_DEVICE, name)?;
    let out = run_nft(&["delete", "counter", FAMILY, TABLE, name])?;
    if out.status.success() {
        return Ok(());
    }
    let stderr = String::from_utf8_lossy(&out.stderr);
    if is_not_found(&stderr) {
        return Ok(());
    }
    Err(ApiError::internal(format!(
        "failed deleting nft device counter {name}: {}",
        stderr.trim()
    )))
}

fn delete_chain(name: &str) -> Result<bool, ApiError> {
    let script =
        format!("flush chain {FAMILY} {TABLE} {name}\ndelete chain {FAMILY} {TABLE} {name}\n");
    match run_nft_script(&script) {
        Ok(()) => Ok(true),
        Err(err) if is_not_found(&err.message) => Ok(false),
        Err(err) => Err(err),
    }
}

pub(super) fn remove_counter(monitor_id: i64, interface: &str) -> Result<(), ApiError> {
    if list_table_json()?.is_none() {
        return Ok(());
    }
    let counter_in = counter_name_in(SCOPE_INET, monitor_id, interface);
    let counter_out = counter_name_out(SCOPE_INET, monitor_id, interface);
    delete_counter(&counter_in)?;
    delete_counter(&counter_out)?;
    let (chain_in, chain_out) = chain_names(monitor_id, interface);
    let _ = delete_chain(&chain_in)?;
    let _ = delete_chain(&chain_out)?;
    Ok(())
}

fn existing_objects(json: &Value) -> (HashSet<String>, HashSet<String>) {
    let mut counters = HashSet::new();
    let mut chains = HashSet::new();
    if let Some(items) = json.get("nftables").and_then(Value::as_array) {
        for item in items {
            if let Some(name) = item
                .get("counter")
                .and_then(|counter| counter.get("name"))
                .and_then(Value::as_str)
            {
                counters.insert(name.to_owned());
            }
            if let Some(name) = item
                .get("chain")
                .and_then(|chain| chain.get("name"))
                .and_then(Value::as_str)
                && (name.starts_with(CHAIN_PREFIX_IN) || name.starts_with(CHAIN_PREFIX_OUT))
            {
                chains.insert(name.to_owned());
            }
        }
    }
    (counters, chains)
}

pub(super) fn garbage_collect_orphans(conn: &Connection) -> Result<usize, ApiError> {
    let Some(json) = list_table_json()? else {
        return Ok(0);
    };
    let mut expected_counters = HashSet::new();
    let mut expected_chains = HashSet::new();
    let mut stmt = conn
        .prepare("SELECT monitor_id, interface FROM interface_states")
        .map_err(|e| ApiError::internal(format!("prepare nft device GC query error: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok((row.get::<_, i64>(0)?, row.get::<_, String>(1)?))
        })
        .map_err(|e| ApiError::internal(format!("nft device GC query error: {e}")))?;
    for row in rows {
        let (monitor_id, interface) =
            row.map_err(|e| ApiError::internal(format!("nft device GC row error: {e}")))?;
        expected_counters.insert(counter_name_in(SCOPE_INET, monitor_id, &interface));
        expected_counters.insert(counter_name_out(SCOPE_INET, monitor_id, &interface));
        let (chain_in, chain_out) = chain_names(monitor_id, &interface);
        expected_chains.insert(chain_in);
        expected_chains.insert(chain_out);
    }

    let (existing_counters, existing_chains) = existing_objects(&json);
    let mut removed = 0usize;
    for counter in existing_counters.difference(&expected_counters) {
        delete_counter(counter)?;
        removed += 1;
    }
    for chain in existing_chains.difference(&expected_chains) {
        if delete_chain(chain)? {
            removed += 1;
        }
    }
    if removed > 0 {
        info!(removed, "garbage-collected orphan nft device objects");
    }
    Ok(removed)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn device_rules_count_guest_ingress_as_outbound_and_egress_as_inbound() {
        let script = build_rules(
            "guest_in",
            "guest_out",
            "counter_in",
            "counter_out",
            &["172.18.0.2".to_string()],
            &["ipv4".to_string()],
        );
        assert!(script.contains("guest_in ip saddr != { 0.0.0.0/8, 10.0.0.0/8"));
        assert!(script.contains("ip daddr 172.18.0.2 counter name counter_in"));
        assert!(script.contains("guest_out ip saddr 172.18.0.2"));
        assert!(script.contains("counter name counter_out"));
    }

    #[test]
    fn device_rules_cover_dual_stack_fallbacks() {
        let script = build_rules(
            "guest_in",
            "guest_out",
            "counter_in",
            "counter_out",
            &[],
            &["ipv4".to_string(), "ipv6".to_string()],
        );
        assert_eq!(script.matches("counter name counter_in").count(), 2);
        assert_eq!(script.matches("counter name counter_out").count(), 2);
        assert!(script.contains("guest_in ip6 saddr !="));
        assert!(script.contains("guest_out ip6 daddr !="));
    }

    #[test]
    fn per_binding_chain_names_are_stable_and_directional() {
        let first = chain_names(7, "veth123");
        let second = chain_names(7, "veth123");
        assert_eq!(first, second);
        assert_ne!(first.0, first.1);
        assert!(first.0.starts_with(CHAIN_PREFIX_IN));
        assert!(first.1.starts_with(CHAIN_PREFIX_OUT));
    }
}
