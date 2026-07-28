use crate::{collector::normalize_interface_name, error::ApiError, models::TrafficBinding};
use std::{collections::HashMap, net::IpAddr, path::Path};

const IPV4: &str = "ipv4";
const IPV6: &str = "ipv6";

#[derive(Default)]
struct BindingParts {
    addresses: Vec<String>,
    families: Vec<String>,
}

pub fn interface_exists(interface: &str) -> bool {
    Path::new("/sys/class/net").join(interface).exists()
}

pub fn normalize_bindings(
    requested: Vec<TrafficBinding>,
    legacy_interfaces: Vec<String>,
    legacy_inner_ip: Option<&str>,
) -> Result<Vec<TrafficBinding>, ApiError> {
    let source = if requested.is_empty() {
        legacy_interfaces
            .into_iter()
            .map(|interface| TrafficBinding {
                interface,
                addresses: legacy_inner_ip
                    .map(str::trim)
                    .filter(|value| !value.is_empty())
                    .map(|value| vec![value.to_owned()])
                    .unwrap_or_default(),
                families: Vec::new(),
            })
            .collect()
    } else {
        requested
    };

    let mut order = Vec::new();
    let mut merged: HashMap<String, BindingParts> = HashMap::new();
    for binding in source {
        let Some(interface) = normalize_interface_name(&binding.interface) else {
            continue;
        };
        if !merged.contains_key(&interface) {
            order.push(interface.clone());
        }
        let parts = merged.entry(interface).or_default();
        for family in binding.families {
            let normalized = family.trim().to_ascii_lowercase();
            if normalized != IPV4 && normalized != IPV6 {
                return Err(ApiError::bad_request(
                    "bindings families must contain only ipv4 or ipv6",
                ));
            }
            if !parts.families.iter().any(|value| value == &normalized) {
                parts.families.push(normalized);
            }
        }
        for raw in binding.addresses {
            let trimmed = raw.trim();
            if trimmed.is_empty() {
                continue;
            }
            let ip = trimmed.parse::<IpAddr>().map_err(|_| {
                ApiError::bad_request("bindings addresses must be valid IP addresses")
            })?;
            let normalized = ip.to_string();
            let family = if ip.is_ipv4() { IPV4 } else { IPV6 };
            if !parts.families.iter().any(|value| value == family) {
                parts.families.push(family.to_string());
            }
            if !parts.addresses.iter().any(|value| value == &normalized) {
                parts.addresses.push(normalized);
            }
        }
    }

    if order.is_empty() {
        return Err(ApiError::bad_request(
            "bindings/interface must contain at least one valid interface",
        ));
    }

    let mut result = Vec::with_capacity(order.len());
    for interface in order {
        let mut parts = merged.remove(&interface).unwrap_or_default();
        parts.addresses.sort();
        if parts.families.is_empty() {
            parts.families = vec![IPV4.to_string(), IPV6.to_string()];
        } else {
            parts.families.sort();
        }
        result.push(TrafficBinding {
            interface,
            addresses: parts.addresses,
            families: parts.families,
        });
    }
    Ok(result)
}

pub fn parse_persisted_bindings(
    bindings_json: &str,
    interfaces_json: &str,
    inner_ip: Option<&str>,
) -> Vec<TrafficBinding> {
    let requested = serde_json::from_str::<Vec<TrafficBinding>>(bindings_json).unwrap_or_default();
    let interfaces = serde_json::from_str::<Vec<String>>(interfaces_json).unwrap_or_default();
    normalize_bindings(requested, interfaces, inner_ip).unwrap_or_default()
}

pub fn serialize_bindings(bindings: &[TrafficBinding]) -> Result<String, ApiError> {
    serde_json::to_string(bindings)
        .map_err(|e| ApiError::internal(format!("serialize traffic bindings error: {e}")))
}

pub fn binding_interfaces(bindings: &[TrafficBinding]) -> Vec<String> {
    bindings
        .iter()
        .map(|binding| binding.interface.clone())
        .collect()
}

pub fn legacy_inner_ip(bindings: &[TrafficBinding]) -> Option<String> {
    bindings.iter().find_map(|binding| {
        binding.addresses.iter().find_map(|address| {
            address
                .parse::<IpAddr>()
                .ok()
                .filter(|ip| ip.is_ipv4())
                .map(|ip| ip.to_string())
        })
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn merges_dual_stack_addresses_on_one_interface() {
        let bindings = normalize_bindings(
            vec![
                TrafficBinding {
                    interface: "veth0@if2".to_string(),
                    addresses: vec!["10.0.0.2".to_string()],
                    families: vec!["ipv4".to_string()],
                },
                TrafficBinding {
                    interface: "veth0".to_string(),
                    addresses: vec!["2001:db8::2".to_string()],
                    families: vec!["ipv6".to_string()],
                },
            ],
            Vec::new(),
            None,
        )
        .expect("bindings should normalize");
        assert_eq!(bindings.len(), 1);
        assert_eq!(bindings[0].interface, "veth0");
        assert_eq!(bindings[0].addresses, vec!["10.0.0.2", "2001:db8::2"]);
        assert_eq!(bindings[0].families, vec!["ipv4", "ipv6"]);
    }

    #[test]
    fn legacy_payload_maps_inner_ip_to_each_interface() {
        let bindings = normalize_bindings(
            Vec::new(),
            vec!["veth4".to_string(), "veth6".to_string()],
            Some("10.0.0.2"),
        )
        .expect("legacy payload should normalize");
        assert_eq!(bindings.len(), 2);
        assert_eq!(bindings[0].addresses, vec!["10.0.0.2"]);
        assert_eq!(bindings[0].families, vec!["ipv4"]);
    }
}
