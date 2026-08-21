use crate::error::ApiError;
use serde_json::Value;
use tracing::{debug, warn};

use super::{
    SCOPES, Scope, binding_config_tag, counter_name_in, counter_name_out, ensure_base_objects,
    escape_quoted, exclude_v4, exclude_v6, expected_rule_count, find_rule_refs_by_counter,
    interface_aliases, is_not_found, nft_set_literal, remove_rules_by_counter, run_nft,
    run_nft_script,
};

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

pub(super) fn build_rules_for_counter(
    scope: Scope,
    counter_in: &str,
    counter_out: &str,
    interface: &str,
    addresses: &[String],
    families: &[String],
) -> Result<String, ApiError> {
    let aliases = interface_aliases(interface);
    if aliases.is_empty() {
        return Err(ApiError::internal("interface aliases cannot be empty"));
    }

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

    for name in aliases {
        let iface = escape_quoted(&name);
        if expects_v4 && v4_addresses.is_empty() {
            script.push_str(&format!(
                "add rule {} {} {} oifname \"{}\" ip daddr != {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} iifname \"{}\" ip saddr != {} counter name {} comment \"{}\"\n",
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v4_set,
                counter_in,
                comment_in,
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v4_set,
                counter_out,
                comment_out,
            ));
        }
        if expects_v6 && v6_addresses.is_empty() {
            script.push_str(&format!(
                "add rule {} {} {} oifname \"{}\" ip6 daddr != {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} iifname \"{}\" ip6 saddr != {} counter name {} comment \"{}\"\n",
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v6_set,
                counter_in,
                comment_in,
                scope.family,
                scope.table,
                scope.chain,
                iface,
                private_v6_set,
                counter_out,
                comment_out,
            ));
        }

        for ip in &v4_addresses {
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
        for ip in &v6_addresses {
            // Per-IP filtering for IPv6 inner_ip
            script.push_str(&format!(
                "add rule {} {} {} oifname \"{}\" ip6 saddr != {} ip6 daddr {} counter name {} comment \"{}\"\n\
                 add rule {} {} {} iifname \"{}\" ip6 saddr {} ip6 daddr != {} counter name {} comment \"{}\"\n",
                scope.family, scope.table, scope.chain, iface, private_v6_set, ip, counter_in, comment_in,
                scope.family, scope.table, scope.chain, iface, ip, private_v6_set, counter_out, comment_out,
            ));
        }
    }
    Ok(script)
}

fn add_rules_for_counter(
    scope: Scope,
    counter_in: &str,
    counter_out: &str,
    interface: &str,
    addresses: &[String],
    families: &[String],
) -> Result<(), ApiError> {
    let script = build_rules_for_counter(
        scope,
        counter_in,
        counter_out,
        interface,
        addresses,
        families,
    )?;
    run_nft_script(&script)
}

fn ensure_counter_in_scope(
    scope: Scope,
    monitor_id: i64,
    interface: &str,
    addresses: &[String],
    families: &[String],
) -> Result<(), ApiError> {
    if !crate::traffic::interface_exists(interface) {
        return Err(ApiError::internal(format!(
            "interface {interface} does not exist"
        )));
    }
    ensure_base_objects(scope)?;
    let alias_count = interface_aliases(interface).len();
    let expected_rules = expected_rule_count(scope, alias_count, addresses, families);
    let counter_in = counter_name_in(scope, monitor_id, interface);
    let counter_out = counter_name_out(scope, monitor_id, interface);
    let counter_in_exists = query_counter_bytes(scope, &counter_in)?.is_some();
    let counter_out_exists = query_counter_bytes(scope, &counter_out)?.is_some();
    // Rules referencing either _in or _out counter
    let refs_in = find_rule_refs_by_counter(scope, &counter_in)?;
    let refs_out = find_rule_refs_by_counter(scope, &counter_out)?;
    let rule_count = refs_in.len() + refs_out.len();
    let config_tag = binding_config_tag(addresses, families);
    let expected_comment_in = format!("vmtm:{counter_in}:{config_tag}");
    let expected_comment_out = format!("vmtm:{counter_out}:{config_tag}");
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
    add_rules_for_counter(
        scope,
        &counter_in,
        &counter_out,
        interface,
        addresses,
        families,
    )?;
    Ok(())
}

pub fn ensure_counter(
    monitor_id: i64,
    interface: &str,
    addresses: &[String],
    families: &[String],
) -> Result<(), ApiError> {
    let mut success = 0usize;
    let mut last_error: Option<String> = None;

    for scope in SCOPES {
        match ensure_counter_in_scope(scope, monitor_id, interface, addresses, families) {
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
        super::remove_counter_by_name(scope, &ci)?;
        super::remove_counter_by_name(scope, &co)?;
    }
    Ok(())
}

pub fn read_external_bytes(monitor_id: i64, interface: &str) -> Option<(u64, u64)> {
    let mut total_in = 0u64;
    let mut total_out = 0u64;
    let mut has_any_counter = false;
    let mut missing_counter_scopes = 0usize;

    for scope in SCOPES {
        let ci = counter_name_in(scope, monitor_id, interface);
        let co = counter_name_out(scope, monitor_id, interface);
        let mut scope_in = None;
        let mut scope_out = None;
        match query_counter_bytes(scope, &ci) {
            Ok(Some(bytes)) => {
                scope_in = Some(bytes);
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
                scope_out = Some(bytes);
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
        // A scope is readable only when both directions exist. Returning a
        // partial pair would keep an interface active while silently dropping
        // one direction until a later reconciliation pass.
        if let (Some(bytes_in), Some(bytes_out)) = (scope_in, scope_out) {
            total_in = total_in.saturating_add(bytes_in);
            total_out = total_out.saturating_add(bytes_out);
            has_any_counter = true;
        } else {
            missing_counter_scopes += 1;
        }
    }

    if has_any_counter {
        return Some((total_in, total_out));
    }

    if missing_counter_scopes > 0 {
        debug!(
            monitor_id,
            interface, "nft counter missing; periodic reconciler will restore it"
        );
    }

    None
}
