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

