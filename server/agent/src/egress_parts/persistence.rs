const PROFILE_SELECT: &str = "id, mode, tunnel_type, tunnel_interface, gateway, route_table, mark, public_ipv4, public_ipv6, enabled, fail_closed, status, last_error, updated_at, wg_managed, wg_peer_public_key, wg_endpoint, wg_addresses, wg_allowed_ips, wg_keepalive, wg_mtu, wg_private_key_present, wg_preshared_key_present";
const BINDING_SELECT: &str = "instance_id, profile_id, source, interface, interface_v4, interface_v6, enabled, state, last_error, updated_at, traffic_bytes_in, traffic_bytes_out, traffic_bytes_dropped, sources_json, fail_closed_enforced";

fn read_profile(row: &rusqlite::Row<'_>) -> rusqlite::Result<EgressProfile> {
    let tunnel_type: String = row.get(2)?;
    let managed = row.get::<_, i64>(14)? != 0;
    let peer_public_key = optional_string(row.get(15)?);
    let endpoint = optional_string(row.get(16)?);
    let addresses = wg_from_json(row.get(17)?);
    let allowed_ips = wg_from_json(row.get(18)?);
    let private_key_configured = row.get::<_, i64>(21)? != 0;
    let preshared_key_configured = row.get::<_, i64>(22)? != 0;
    let has_wg_config =
        managed || peer_public_key.is_some() || !addresses.is_empty() || private_key_configured;
    Ok(EgressProfile {
        id: row.get(0)?,
        mode: row.get(1)?,
        tunnel_type: tunnel_type.clone(),
        tunnel_interface: row.get(3)?,
        gateway: optional_string(row.get(4)?),
        route_table: row.get(5)?,
        mark: row.get(6)?,
        public_ipv4: optional_string(row.get(7)?),
        public_ipv6: optional_string(row.get(8)?),
        enabled: row.get::<_, i64>(9)? != 0,
        fail_closed: row.get::<_, i64>(10)? != 0,
        status: row.get(11)?,
        last_error: optional_string(row.get(12)?),
        updated_at: row.get(13)?,
        wireguard: (tunnel_type == "wireguard" && has_wg_config).then_some(WireGuardStatus {
            managed,
            peer_public_key,
            endpoint,
            addresses,
            allowed_ips,
            persistent_keepalive: row.get(19)?,
            mtu: row.get(20)?,
            private_key_configured,
            preshared_key_configured,
        }),
        tunnel_ready: false,
        last_handshake_at: None,
    })
}

fn read_binding(row: &rusqlite::Row<'_>) -> rusqlite::Result<EgressBinding> {
    let source: String = row.get(2)?;
    let mut sources = wg_from_json(row.get(13)?);
    if sources.is_empty() {
        sources.push(source.clone());
    }
    let interface = optional_string(row.get(3)?);
    let interface_v4 = optional_string(row.get(4)?).or_else(|| interface.clone());
    let interface_v6 = optional_string(row.get(5)?).or_else(|| interface.clone());
    Ok(EgressBinding {
        instance_id: row.get(0)?,
        profile_id: row.get(1)?,
        source,
        sources,
        interface,
        interface_v4,
        interface_v6,
        enabled: row.get::<_, i64>(6)? != 0,
        state: row.get(7)?,
        last_error: optional_string(row.get(8)?),
        fail_closed_enforced: row.get::<_, Option<i64>>(14)?.map(|value| value != 0),
        updated_at: row.get(9)?,
        traffic_bytes_in: row.get::<_, i64>(10)?.max(0) as u64,
        traffic_bytes_out: row.get::<_, i64>(11)?.max(0) as u64,
        traffic_bytes_dropped: row.get::<_, i64>(12)?.max(0) as u64,
    })
}

fn load_desired(
    conn: &rusqlite::Connection,
) -> Result<(Vec<ProfileRow>, Vec<BindingRow>), ApiError> {
    let mut profile_stmt = conn
        .prepare(&format!("SELECT {PROFILE_SELECT} FROM egress_profiles"))
        .map_err(|e| ApiError::internal(format!("prepare egress profiles error: {e}")))?;
    let profile_rows = profile_stmt
        .query_map([], read_profile)
        .map_err(|e| ApiError::internal(format!("query egress profiles error: {e}")))?;
    let mut profiles = Vec::new();
    for row in profile_rows {
        profiles.push(ProfileRow {
            profile: row
                .map_err(|e| ApiError::internal(format!("read egress profile error: {e}")))?,
        });
    }
    drop(profile_stmt);
    let mut binding_stmt = conn
        .prepare(&format!("SELECT {BINDING_SELECT} FROM egress_bindings"))
        .map_err(|e| ApiError::internal(format!("prepare egress bindings error: {e}")))?;
    let binding_rows = binding_stmt
        .query_map([], read_binding)
        .map_err(|e| ApiError::internal(format!("query egress bindings error: {e}")))?;
    let mut bindings = Vec::new();
    for row in binding_rows {
        let binding =
            row.map_err(|e| ApiError::internal(format!("read egress binding error: {e}")))?;
        let mut networks = Vec::new();
        for source in &binding.sources {
            networks.push(parse_network(source, "stored source", true)?);
        }
        bindings.push(BindingRow { binding, networks });
    }
    Ok((profiles, bindings))
}

fn load_runtime(conn: &rusqlite::Connection) -> Result<Vec<RuntimeProfile>, ApiError> {
    let mut stmt = conn.prepare("SELECT profile_id, route_table, mark, tunnel_interface, has_v4, has_v6, managed_interface, probe_sources_json FROM egress_runtime_profiles")
        .map_err(|e| ApiError::internal(format!("prepare egress runtime state error: {e}")))?;
    let rows = stmt
        .query_map([], |row| {
            Ok(RuntimeProfile {
                profile_id: row.get(0)?,
                route_table: row.get(1)?,
                mark: row.get(2)?,
                tunnel_interface: row.get(3)?,
                has_v4: row.get::<_, i64>(4)? != 0,
                has_v6: row.get::<_, i64>(5)? != 0,
                managed_interface: row.get::<_, i64>(6)? != 0,
                probe_sources: wg_from_json(row.get(7)?),
            })
        })
        .map_err(|e| ApiError::internal(format!("query egress runtime state error: {e}")))?;
    let mut runtime = Vec::new();
    for row in rows {
        runtime.push(
            row.map_err(|e| ApiError::internal(format!("read egress runtime state error: {e}")))?,
        );
    }
    Ok(runtime)
}

#[derive(Debug, Clone, Default)]
struct HostInventory {
    interfaces: HashSet<String>,
    wireguard_interfaces: HashSet<String>,
    handshakes: HashMap<String, i64>,
}

fn host_inventory(executor: &impl CommandExecutor) -> HostInventory {
    let interfaces = fs::read_dir("/sys/class/net")
        .ok()
        .into_iter()
        .flatten()
        .filter_map(Result::ok)
        .filter_map(|entry| entry.file_name().into_string().ok())
        .collect();
    let mut inventory = HostInventory {
        interfaces,
        ..HostInventory::default()
    };
    if command_available("wg") {
        let args = vec!["show".to_string(), "interfaces".to_string()];
        if let Ok(result) = executor.run("wg", &args, None) {
            if result.success {
                inventory
                    .wireguard_interfaces
                    .extend(result.stdout.split_whitespace().map(str::to_string));
            }
        }
        let args = vec![
            "show".to_string(),
            "all".to_string(),
            "latest-handshakes".to_string(),
        ];
        if let Ok(result) = executor.run("wg", &args, None) {
            if result.success {
                for line in result.stdout.lines() {
                    let fields: Vec<&str> = line.split_whitespace().collect();
                    if fields.len() >= 3 {
                        if let Ok(timestamp) = fields[2].parse::<i64>() {
                            inventory
                                .handshakes
                                .entry(fields[0].to_string())
                                .and_modify(|current| *current = (*current).max(timestamp))
                                .or_insert(timestamp);
                        }
                    }
                }
            }
        }
    }
    inventory
}

fn enrich_profiles(profiles: &mut [EgressProfile], inventory: &HostInventory) {
    for profile in profiles {
        profile.last_handshake_at = inventory
            .handshakes
            .get(&profile.tunnel_interface)
            .copied()
            .filter(|value| *value > 0);
        profile.tunnel_ready = if profile.tunnel_type == "wireguard" {
            inventory
                .wireguard_interfaces
                .contains(&profile.tunnel_interface)
                && profile
                    .last_handshake_at
                    .is_some_and(recent_wireguard_handshake)
        } else {
            inventory.interfaces.contains(&profile.tunnel_interface)
        };
    }
}

fn recent_wireguard_handshake(timestamp: i64) -> bool {
    let now = now_ts();
    timestamp > 0
        && timestamp <= now.saturating_add(5)
        && now.saturating_sub(timestamp) <= WIREGUARD_HANDSHAKE_MAX_AGE_SECS
}

fn upsert_profile_row(
    conn: &rusqlite::Connection,
    profile: &EgressProfile,
) -> Result<(), ApiError> {
    let wg = profile.wireguard.clone().unwrap_or_else(default_wg_status);
    let now = now_ts();
    conn.execute(
        "INSERT INTO egress_profiles (id, mode, tunnel_type, tunnel_interface, gateway, route_table, mark, public_ipv4, public_ipv6, enabled, fail_closed, status, last_error, created_at, updated_at, wg_managed, wg_peer_public_key, wg_endpoint, wg_addresses, wg_allowed_ips, wg_keepalive, wg_mtu, wg_private_key_present, wg_preshared_key_present) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9,?10,?11,'pending','',?12,?12,?13,?14,?15,?16,?17,?18,?19,?20,?21) ON CONFLICT(id) DO UPDATE SET mode=excluded.mode,tunnel_type=excluded.tunnel_type,tunnel_interface=excluded.tunnel_interface,gateway=excluded.gateway,route_table=excluded.route_table,mark=excluded.mark,public_ipv4=excluded.public_ipv4,public_ipv6=excluded.public_ipv6,enabled=excluded.enabled,fail_closed=excluded.fail_closed,status='pending',last_error='',updated_at=excluded.updated_at,wg_managed=excluded.wg_managed,wg_peer_public_key=excluded.wg_peer_public_key,wg_endpoint=excluded.wg_endpoint,wg_addresses=excluded.wg_addresses,wg_allowed_ips=excluded.wg_allowed_ips,wg_keepalive=excluded.wg_keepalive,wg_mtu=excluded.wg_mtu,wg_private_key_present=excluded.wg_private_key_present,wg_preshared_key_present=excluded.wg_preshared_key_present",
        params![
            &profile.id,
            &profile.mode,
            &profile.tunnel_type,
            &profile.tunnel_interface,
            profile.gateway.clone().unwrap_or_default(),
            profile.route_table,
            profile.mark,
            profile.public_ipv4.clone().unwrap_or_default(),
            profile.public_ipv6.clone().unwrap_or_default(),
            profile.enabled as i64,
            profile.fail_closed as i64,
            now,
            wg.managed as i64,
            wg.peer_public_key.clone().unwrap_or_default(),
            wg.endpoint.clone().unwrap_or_default(),
            wg_json(&wg.addresses),
            wg_json(&wg.allowed_ips),
            wg.persistent_keepalive,
            wg.mtu,
            wg.private_key_configured as i64,
            wg.preshared_key_configured as i64,
        ],
    )
    .map_err(|e| ApiError::internal(format!("save egress profile error: {e}")))?;
    Ok(())
}

fn upsert_binding_row(conn: &rusqlite::Connection, row: &BindingRow) -> Result<(), ApiError> {
    let now = now_ts();
    let enforcement = row.binding.enabled.then_some(1_i64);
    conn.execute(
        "INSERT INTO egress_bindings (instance_id,profile_id,source,interface,interface_v4,interface_v6,enabled,state,last_error,created_at,updated_at,sources_json,fail_closed_enforced) VALUES (?1,?2,?3,?4,?5,?6,?7,'pending','',?8,?8,?9,?10) ON CONFLICT(instance_id) DO UPDATE SET profile_id=excluded.profile_id,source=excluded.source,interface=excluded.interface,interface_v4=excluded.interface_v4,interface_v6=excluded.interface_v6,enabled=excluded.enabled,state='pending',last_error='',updated_at=excluded.updated_at,sources_json=excluded.sources_json,fail_closed_enforced=excluded.fail_closed_enforced",
        params![
            &row.binding.instance_id,
            &row.binding.profile_id,
            &row.binding.source,
            row.binding.interface.clone().unwrap_or_default(),
            row.binding.interface_v4.clone().unwrap_or_default(),
            row.binding.interface_v6.clone().unwrap_or_default(),
            row.binding.enabled as i64,
            now,
            wg_json(&row.binding.sources),
            enforcement,
        ],
    )
    .map_err(|e| ApiError::internal(format!("save egress binding error: {e}")))?;
    Ok(())
}

fn replace_desired_state_sql(
    conn: &rusqlite::Connection,
    profiles: &[EgressProfile],
    bindings: &[BindingRow],
) -> Result<(), ApiError> {
    let tx = conn
        .unchecked_transaction()
        .map_err(|e| ApiError::internal(format!("begin egress state transaction error: {e}")))?;
    tx.execute_batch(
        "CREATE TEMP TABLE IF NOT EXISTS egress_replace_profile_ids (id TEXT PRIMARY KEY NOT NULL);\
         CREATE TEMP TABLE IF NOT EXISTS egress_replace_binding_ids (id TEXT PRIMARY KEY NOT NULL);\
         DELETE FROM egress_replace_profile_ids;\
         DELETE FROM egress_replace_binding_ids;",
    )
    .map_err(|e| ApiError::internal(format!("prepare egress state replacement error: {e}")))?;
    for profile in profiles {
        upsert_profile_row(&tx, profile)?;
        tx.execute(
            "INSERT INTO egress_replace_profile_ids (id) VALUES (?1)",
            params![&profile.id],
        )
        .map_err(|e| ApiError::internal(format!("stage egress profile id error: {e}")))?;
    }
    for binding in bindings {
        upsert_binding_row(&tx, binding)?;
        tx.execute(
            "INSERT INTO egress_replace_binding_ids (id) VALUES (?1)",
            params![&binding.binding.instance_id],
        )
        .map_err(|e| ApiError::internal(format!("stage egress binding id error: {e}")))?;
    }
    tx.execute(
        "DELETE FROM egress_bindings WHERE instance_id NOT IN (SELECT id FROM egress_replace_binding_ids)",
        [],
    )
    .map_err(|e| ApiError::internal(format!("remove obsolete egress bindings error: {e}")))?;
    tx.execute(
        "DELETE FROM egress_profiles WHERE id NOT IN (SELECT id FROM egress_replace_profile_ids)",
        [],
    )
    .map_err(|e| ApiError::internal(format!("remove obsolete egress profiles error: {e}")))?;
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit egress state transaction error: {e}")))
}

