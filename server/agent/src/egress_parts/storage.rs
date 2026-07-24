fn state_directory() -> Result<PathBuf, ApiError> {
    let path = env::var_os("ONECLICKVIRT_EGRESS_STATE_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from("/var/lib/oneclickvirt/egress"));
    if !path.is_absolute() {
        return Err(ApiError::bad_request(
            "ONECLICKVIRT_EGRESS_STATE_DIR must be absolute",
        ));
    }
    Ok(path)
}

fn managed_sources_path() -> Result<PathBuf, ApiError> {
    Ok(state_directory()?.join("managed-sources"))
}

/// Persist the source identities that the boot quarantine helper must protect
/// before the Agent has restored its SQLite state. The same guard table is also
/// used as a staging barrier between binding PUT and full reconciliation. The
/// file is deliberately data-only (one canonical CIDR per line) and committed
/// with rename(2).
fn write_managed_sources(conn: &rusqlite::Connection) -> Result<(), ApiError> {
    let path = managed_sources_path()?;
    write_managed_sources_at_with_extra(conn, &path, &[])
}

#[cfg(test)]
fn write_managed_sources_at(conn: &rusqlite::Connection, path: &Path) -> Result<(), ApiError> {
    write_managed_sources_at_with_extra(conn, path, &[])
}

fn write_managed_sources_with_extra(
    conn: &rusqlite::Connection,
    extra: &[IpNetwork],
) -> Result<(), ApiError> {
    let path = managed_sources_path()?;
    write_managed_sources_at_with_extra(conn, &path, extra)
}

fn write_managed_sources_at_with_extra(
    conn: &rusqlite::Connection,
    path: &Path,
    extra: &[IpNetwork],
) -> Result<(), ApiError> {
    let mut networks = load_enabled_binding_networks(conn)?;
    networks.extend_from_slice(extra);
    write_managed_networks_at(path, &networks)
}

fn load_enabled_binding_networks(conn: &rusqlite::Connection) -> Result<Vec<IpNetwork>, ApiError> {
    let mut networks = Vec::new();
    let mut stmt = conn
        .prepare("SELECT sources_json FROM egress_bindings WHERE enabled=1")
        .map_err(|e| ApiError::internal(format!("prepare managed egress sources error: {e}")))?;
    let rows = stmt
        .query_map([], |row| row.get::<_, String>(0))
        .map_err(|e| ApiError::internal(format!("query managed egress sources error: {e}")))?;
    for row in rows {
        let encoded =
            row.map_err(|e| ApiError::internal(format!("read managed egress sources error: {e}")))?;
        let values: Vec<String> = serde_json::from_str(&encoded)
            .map_err(|e| ApiError::internal(format!("decode managed egress sources error: {e}")))?;
        for value in values {
            let network = parse_network(&value, "managed source", true)
                .map_err(|error| ApiError::internal(error.message))?;
            networks.push(network);
        }
    }
    Ok(networks)
}

fn write_managed_networks_at(path: &Path, networks: &[IpNetwork]) -> Result<(), ApiError> {
    let sources: BTreeSet<String> = networks.iter().map(ToString::to_string).collect();
    let directory = path
        .parent()
        .ok_or_else(|| ApiError::internal("invalid managed egress source path"))?;
    fs::create_dir_all(directory).map_err(|e| {
        ApiError::internal(format!("create managed egress state directory error: {e}"))
    })?;
    fs::set_permissions(directory, fs::Permissions::from_mode(0o700)).map_err(|e| {
        ApiError::internal(format!("secure managed egress state directory error: {e}"))
    })?;
    let temporary = directory.join(format!(
        ".managed-sources-{}-{}",
        std::process::id(),
        now_ts()
    ));
    let mut file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(&temporary)
        .map_err(|e| ApiError::internal(format!("create managed egress source file error: {e}")))?;
    let content = if sources.is_empty() {
        String::new()
    } else {
        let mut value = sources.into_iter().collect::<Vec<_>>().join("\n");
        value.push('\n');
        value
    };
    file.write_all(content.as_bytes())
        .and_then(|_| file.sync_all())
        .map_err(|e| ApiError::internal(format!("write managed egress source file error: {e}")))?;
    fs::rename(&temporary, &path)
        .map_err(|e| ApiError::internal(format!("commit managed egress source file error: {e}")))?;
    fs::set_permissions(&path, fs::Permissions::from_mode(0o600))
        .map_err(|e| ApiError::internal(format!("secure managed egress source file error: {e}")))?;
    Ok(())
}

fn clear_boot_quarantine(executor: &impl CommandExecutor) -> Result<(), String> {
    let args = vec![
        "delete".to_string(),
        "table".to_string(),
        TABLE_FAMILY.to_string(),
        BOOT_TABLE_NAME.to_string(),
    ];
    let result = executor.run("nft", &args, None)?;
    if result.success
        || result.stderr.contains("No such file")
        || result.stderr.contains("does not exist")
    {
        Ok(())
    } else {
        Err(concise_error(&result, "remove boot egress quarantine"))
    }
}

fn nft_table_named_exists(
    executor: &impl CommandExecutor,
    table_name: &str,
) -> Result<bool, String> {
    let args = vec![
        "list".to_string(),
        "table".to_string(),
        TABLE_FAMILY.to_string(),
        table_name.to_string(),
    ];
    let result = executor.run("nft", &args, None)?;
    Ok(result.success)
}

/// Build an additive transaction for the early-boot table. Existing rules are
/// deliberately retained: they may protect a binding whose PUT completed but
/// whose reconcile request was interrupted. Full reconciliation deletes the
/// table only after the primary data plane has been installed successfully.
fn build_staging_quarantine_script(networks: &[IpNetwork], table_exists: bool) -> String {
    let mut script = String::new();
    if !table_exists {
        script.push_str(&format!("add table {TABLE_FAMILY} {BOOT_TABLE_NAME}\n"));
        script.push_str(&format!("add chain {TABLE_FAMILY} {BOOT_TABLE_NAME} boot_forward {{ type filter hook forward priority -200; policy accept; }}\n"));
        script.push_str(&format!("add chain {TABLE_FAMILY} {BOOT_TABLE_NAME} boot_output {{ type filter hook output priority -200; policy accept; }}\n"));
        script.push_str(&format!("add chain {TABLE_FAMILY} {BOOT_TABLE_NAME} boot_input {{ type filter hook input priority -200; policy accept; }}\n"));
    }
    let mut values: Vec<(String, String)> = networks
        .iter()
        .map(|network| {
            (
                network.family().nft_prefix().to_string(),
                network.to_string(),
            )
        })
        .collect();
    values.sort();
    values.dedup();
    for (family, source) in values {
        for chain in ["boot_forward", "boot_output", "boot_input"] {
            script.push_str(&format!(
                "add rule {TABLE_FAMILY} {BOOT_TABLE_NAME} {chain} {family} saddr {source} counter drop\n"
            ));
        }
    }
    script
}

fn install_staging_quarantine(
    executor: &impl CommandExecutor,
    networks: &[IpNetwork],
) -> Result<(), String> {
    if networks.is_empty() {
        return Ok(());
    }
    let table_exists = nft_table_named_exists(executor, BOOT_TABLE_NAME)?;
    let script = build_staging_quarantine_script(networks, table_exists);
    apply_nft_script(executor, &script)
        .map_err(|error| format!("install binding fail-closed quarantine: {error}"))
}

fn secret_path(profile_id: &str, preshared: bool) -> Result<PathBuf, ApiError> {
    let suffix = if preshared { "psk" } else { "key" };
    Ok(state_directory()?.join(format!("wg-{profile_id}.{suffix}")))
}

fn atomic_write_secret(path: &Path, value: &str) -> Result<(), ApiError> {
    let directory = path
        .parent()
        .ok_or_else(|| ApiError::internal("invalid egress state directory"))?;
    fs::create_dir_all(directory)
        .map_err(|e| ApiError::internal(format!("create egress state directory error: {e}")))?;
    fs::set_permissions(directory, fs::Permissions::from_mode(0o700))
        .map_err(|e| ApiError::internal(format!("secure egress state directory error: {e}")))?;
    let temporary = directory.join(format!(".secret-{}-{}", std::process::id(), now_ts()));
    let mut file = OpenOptions::new()
        .create_new(true)
        .write(true)
        .mode(0o600)
        .open(&temporary)
        .map_err(|e| ApiError::internal(format!("create egress secret error: {e}")))?;
    file.write_all(value.as_bytes())
        .and_then(|_| file.write_all(b"\n"))
        .and_then(|_| file.sync_all())
        .map_err(|e| ApiError::internal(format!("write egress secret error: {e}")))?;
    fs::rename(&temporary, path)
        .map_err(|e| ApiError::internal(format!("commit egress secret error: {e}")))?;
    fs::set_permissions(path, fs::Permissions::from_mode(0o600))
        .map_err(|e| ApiError::internal(format!("secure egress secret error: {e}")))?;
    Ok(())
}

fn remove_profile_secrets(profile_id: &str) {
    for preshared in [false, true] {
        if let Ok(path) = secret_path(profile_id, preshared) {
            if let Err(error) = fs::remove_file(&path) {
                if error.kind() != std::io::ErrorKind::NotFound {
                    warn!(profile_id, path = %path.display(), error = %error, "failed removing egress secret");
                }
            }
        }
    }
}

#[derive(Debug)]
struct SecretUpdate {
    path: PathBuf,
    value: String,
}

#[derive(Debug)]
struct SecretBackup {
    path: PathBuf,
    value: Option<String>,
}

fn rollback_secret_updates(backups: &[SecretBackup]) {
    for backup in backups.iter().rev() {
        let result = match backup.value.as_deref() {
            Some(value) => atomic_write_secret(&backup.path, value.trim()),
            None => match fs::remove_file(&backup.path) {
                Ok(()) => Ok(()),
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
                Err(error) => Err(ApiError::internal(format!(
                    "remove staged egress secret error: {error}"
                ))),
            },
        };
        if let Err(error) = result {
            warn!(path = %backup.path.display(), error = %error.message, "failed rolling back staged egress secret");
        }
    }
}

fn apply_secret_updates(updates: &[SecretUpdate]) -> Result<Vec<SecretBackup>, ApiError> {
    let mut backups = Vec::with_capacity(updates.len());
    for update in updates {
        let previous = match fs::read_to_string(&update.path) {
            Ok(value) => Some(value),
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => None,
            Err(error) => {
                rollback_secret_updates(&backups);
                return Err(ApiError::internal(format!(
                    "read existing egress secret error: {error}"
                )));
            }
        };
        if let Err(error) = atomic_write_secret(&update.path, &update.value) {
            rollback_secret_updates(&backups);
            return Err(error);
        }
        backups.push(SecretBackup {
            path: update.path.clone(),
            value: previous,
        });
    }
    Ok(backups)
}

fn prepare_profile_storage(
    profile: &mut EgressProfile,
    requested: Option<&WireGuardConfig>,
    existing: Option<&EgressProfile>,
) -> Result<(Vec<SecretUpdate>, bool), ApiError> {
    let mut updates = Vec::new();
    let mut remove_secrets = profile.tunnel_type != "wireguard";
    if requested.is_none() && profile.tunnel_type == "wireguard" {
        profile.wireguard = existing.and_then(|value| value.wireguard.clone());
        return Ok((updates, remove_secrets));
    }
    if let Some(config) = requested {
        let mut status = config.status.clone();
        let existing_status = existing.and_then(|value| value.wireguard.as_ref());
        if let Some(private_key) = config.private_key.as_ref() {
            updates.push(SecretUpdate {
                path: secret_path(&profile.id, false)?,
                value: private_key.clone(),
            });
            status.private_key_configured = true;
        } else if status.managed {
            status.private_key_configured = existing_status
                .is_some_and(|value| value.private_key_configured)
                || secret_path(&profile.id, false)?.is_file();
        }
        if let Some(preshared_key) = config.preshared_key.as_ref() {
            updates.push(SecretUpdate {
                path: secret_path(&profile.id, true)?,
                value: preshared_key.clone(),
            });
            status.preshared_key_configured = true;
        } else if status.managed {
            status.preshared_key_configured = existing_status
                .is_some_and(|value| value.preshared_key_configured)
                || secret_path(&profile.id, true)?.is_file();
        }
        if !status.managed {
            status.private_key_configured = false;
            status.preshared_key_configured = false;
            remove_secrets = true;
        }
        profile.wireguard = Some(status);
    }
    Ok((updates, remove_secrets))
}

fn validate_host_ip(raw: Option<String>, field: &str) -> Result<Option<String>, ApiError> {
    raw.filter(|value| !value.trim().is_empty())
        .map(|value| {
            let network = parse_network(&value, field, true)?;
            if network.prefix != network.family().max_prefix() {
                return Err(ApiError::bad_request(format!(
                    "{field} must be a single address"
                )));
            }
            Ok(network.addr.to_string())
        })
        .transpose()
}

fn wireguard_from_request(req: WireGuardConfigRequest) -> Result<WireGuardConfig, ApiError> {
    let managed = req.managed.unwrap_or(true);
    let private_key = req
        .private_key
        .map(|value| validate_key(&value, "wireguard private key"))
        .transpose()?;
    let preshared_key = req
        .preshared_key
        .map(|value| validate_key(&value, "wireguard preshared key"))
        .transpose()?;
    let peer_public_key = req
        .peer_public_key
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_key(&value, "wireguard peer public key"))
        .transpose()?;
    let endpoint = req
        .endpoint
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_endpoint(&value))
        .transpose()?;
    let addresses = if managed {
        validate_vec_networks(req.addresses, "wireguard address", &[], true, false)?
    } else {
        req.addresses.unwrap_or_default()
    };
    let allowed_ips = if managed {
        validate_vec_networks(
            req.allowed_ips,
            "wireguard allowed IP",
            &["0.0.0.0/0", "::/0"],
            false,
            true,
        )?
    } else {
        req.allowed_ips.unwrap_or_default()
    };
    let persistent_keepalive = req.persistent_keepalive.unwrap_or(25);
    let mtu = req.mtu.unwrap_or(1420);
    if managed && peer_public_key.is_none() {
        return Err(ApiError::bad_request(
            "managed WireGuard requires peer_public_key",
        ));
    }
    if !(576..=9000).contains(&mtu) {
        return Err(ApiError::bad_request(
            "wireguard mtu must be between 576 and 9000",
        ));
    }
    Ok(WireGuardConfig {
        status: WireGuardStatus {
            managed,
            peer_public_key,
            endpoint,
            addresses,
            allowed_ips,
            persistent_keepalive,
            mtu,
            private_key_configured: private_key.is_some(),
            preshared_key_configured: preshared_key.is_some(),
        },
        private_key,
        preshared_key,
    })
}

fn profile_from_request(
    req: EgressProfileRequest,
) -> Result<(EgressProfile, Option<WireGuardConfig>), ApiError> {
    let id = validate_id(&req.id, "id")?;
    let mode = normalize_mode(&req.mode)?;
    let tunnel_type = normalize_tunnel_type(req.tunnel_type)?;
    let tunnel_interface = validate_interface(&req.tunnel_interface, "tunnel_interface")?;
    let gateway = validate_host_ip(req.gateway, "gateway")?;
    let public_ipv4 = validate_host_ip(req.public_ipv4, "public_ipv4")?;
    let public_ipv6 = validate_host_ip(req.public_ipv6, "public_ipv6")?;
    if public_ipv4
        .as_deref()
        .and_then(|value| value.parse::<IpAddr>().ok())
        .is_some_and(|ip| ip.is_ipv6())
    {
        return Err(ApiError::bad_request("public_ipv4 must be IPv4"));
    }
    if public_ipv6
        .as_deref()
        .and_then(|value| value.parse::<IpAddr>().ok())
        .is_some_and(|ip| ip.is_ipv4())
    {
        return Err(ApiError::bad_request("public_ipv6 must be IPv6"));
    }
    let route_table = req.route_table.unwrap_or(0);
    let mark = req.mark.unwrap_or(0);
    let enabled = req.enabled.unwrap_or(false);
    let fail_closed = req.fail_closed.unwrap_or(true);
    if enabled
        && (route_table == 0 || route_table > MAX_ROUTE_TABLE || (253..=255).contains(&route_table))
    {
        return Err(ApiError::bad_request(format!(
            "route_table must be 1..={MAX_ROUTE_TABLE} and not 253..=255"
        )));
    }
    if enabled && (mark == 0 || mark > MAX_MARK) {
        return Err(ApiError::bad_request(format!(
            "mark must be 1..={MAX_MARK}"
        )));
    }
    if enabled && !fail_closed {
        return Err(ApiError::bad_request("fail_closed must remain enabled"));
    }
    let wireguard = req.wireguard.map(wireguard_from_request).transpose()?;
    if wireguard.is_some() && tunnel_type != "wireguard" {
        return Err(ApiError::bad_request(
            "wireguard config requires tunnel_type=wireguard",
        ));
    }
    Ok((
        EgressProfile {
            id,
            mode,
            tunnel_type,
            tunnel_interface,
            gateway,
            route_table,
            mark,
            public_ipv4,
            public_ipv6,
            enabled,
            fail_closed,
            status: "pending".to_string(),
            last_error: None,
            updated_at: now_ts(),
            wireguard: wireguard.as_ref().map(|config| config.status.clone()),
            tunnel_ready: false,
            last_handshake_at: None,
        },
        wireguard,
    ))
}

fn binding_from_request(req: EgressBindingRequest) -> Result<BindingRow, ApiError> {
    let primary = parse_network(&req.source, "source", true)?;
    let mut networks = Vec::new();
    for value in req
        .sources
        .unwrap_or_default()
        .into_iter()
        .chain(std::iter::once(req.source.clone()))
    {
        let network = parse_network(&value, "source", true)?;
        if !networks
            .iter()
            .any(|existing: &IpNetwork| existing == &network)
        {
            networks.push(network);
        }
    }
    if networks.len() > 64 {
        return Err(ApiError::bad_request(
            "at most 64 binding sources are allowed",
        ));
    }
    for left in 0..networks.len() {
        for right in (left + 1)..networks.len() {
            if networks[left].family() == networks[right].family()
                && networks[left].canonical_bits() <= networks[right].end_bits()
                && networks[right].canonical_bits() <= networks[left].end_bits()
            {
                return Err(ApiError::bad_request("binding sources must not overlap"));
            }
        }
    }
    let sources: Vec<String> = networks.iter().map(ToString::to_string).collect();
    let interface = req
        .interface
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_interface(&value, "interface"))
        .transpose()?;
    let interface_v4 = req
        .interface_v4
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_interface(&value, "interface_v4"))
        .transpose()?
        .or_else(|| interface.clone());
    let interface_v6 = req
        .interface_v6
        .filter(|value| !value.trim().is_empty())
        .map(|value| validate_interface(&value, "interface_v6"))
        .transpose()?
        .or_else(|| interface.clone());
    let enabled = req.enabled.unwrap_or(true);
    let needs_v4 = networks
        .iter()
        .any(|network| network.family() == Family::V4);
    let needs_v6 = networks
        .iter()
        .any(|network| network.family() == Family::V6);
    if enabled && ((needs_v4 && interface_v4.is_none()) || (needs_v6 && interface_v6.is_none())) {
        return Err(ApiError::bad_request(
            "enabled native binding requires a host ingress interface for every source family",
        ));
    }
    Ok(BindingRow {
        binding: EgressBinding {
            instance_id: validate_id(&req.instance_id, "instance_id")?,
            profile_id: validate_id(&req.profile_id, "profile_id")?,
            source: primary.to_string(),
            sources,
            interface,
            interface_v4,
            interface_v6,
            enabled,
            state: "pending".to_string(),
            last_error: None,
            fail_closed_enforced: None,
            updated_at: now_ts(),
            traffic_bytes_in: 0,
            traffic_bytes_out: 0,
            traffic_bytes_dropped: 0,
        },
        networks,
    })
}

fn normalize_replace_state_request(
    req: ReplaceStateRequest,
) -> Result<NormalizedReplaceState, ApiError> {
    if req.profiles.len() > MAX_STATE_PROFILES || req.bindings.len() > MAX_STATE_BINDINGS {
        return Err(ApiError::bad_request("egress state batch is too large"));
    }
    let mut profile_ids = HashSet::with_capacity(req.profiles.len());
    let mut profiles = Vec::with_capacity(req.profiles.len());
    for request in req.profiles {
        let (profile, wireguard) = profile_from_request(request)?;
        if !profile_ids.insert(profile.id.clone()) {
            return Err(ApiError::bad_request(format!(
                "duplicate egress profile id {}",
                profile.id
            )));
        }
        profiles.push((profile, wireguard));
    }
    let mut binding_ids = HashSet::with_capacity(req.bindings.len());
    let mut bindings = Vec::with_capacity(req.bindings.len());
    for request in req.bindings {
        let binding = binding_from_request(request)?;
        if !binding_ids.insert(binding.binding.instance_id.clone()) {
            return Err(ApiError::bad_request(format!(
                "duplicate egress binding instance_id {}",
                binding.binding.instance_id
            )));
        }
        if !profile_ids.contains(&binding.binding.profile_id) {
            return Err(ApiError::bad_request(format!(
                "egress binding {} references a profile outside this state batch",
                binding.binding.instance_id
            )));
        }
        bindings.push(binding);
    }
    Ok(NormalizedReplaceState {
        profiles,
        bindings,
        apply: req.apply.unwrap_or(false),
    })
}

fn optional_string(value: String) -> Option<String> {
    (!value.is_empty()).then_some(value)
}

