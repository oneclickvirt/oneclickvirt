fn concise_error(result: &CommandResult, context: &str) -> String {
    let detail = result
        .stderr
        .lines()
        .chain(result.stdout.lines())
        .map(str::trim)
        .find(|line| !line.is_empty())
        .unwrap_or("command failed")
        .chars()
        .take(300)
        .collect::<String>();
    format!("{context}: {detail}")
}

fn run_checked(
    executor: &impl CommandExecutor,
    program: &str,
    args: Vec<String>,
    context: &str,
) -> Result<(), String> {
    let result = executor.run(program, &args, None)?;
    if result.success {
        Ok(())
    } else {
        Err(concise_error(&result, context))
    }
}

fn apply_nft_script(executor: &impl CommandExecutor, script: &str) -> Result<(), String> {
    let check_args = vec!["-c".to_string(), "-f".to_string(), "-".to_string()];
    let checked = executor.run("nft", &check_args, Some(script))?;
    if !checked.success {
        return Err(concise_error(&checked, "nft validation failed"));
    }
    let apply_args = vec!["-f".to_string(), "-".to_string()];
    let applied = executor.run("nft", &apply_args, Some(script))?;
    if applied.success {
        Ok(())
    } else {
        Err(concise_error(&applied, "nft transaction failed"))
    }
}

fn write_wireguard_runtime_config(
    profile: &EgressProfile,
    status: &WireGuardStatus,
) -> Result<PathBuf, String> {
    let private_path = secret_path(&profile.id, false).map_err(|e| e.message)?;
    let private_key = fs::read_to_string(&private_path)
        .map_err(|e| format!("read WireGuard private key: {e}"))?;
    validate_key(private_key.trim(), "stored WireGuard private key").map_err(|e| e.message)?;
    let mut config = format!(
        "[Interface]\nPrivateKey = {}\n\n[Peer]\nPublicKey = {}\nAllowedIPs = {}\n",
        private_key.trim(),
        status
            .peer_public_key
            .as_deref()
            .ok_or("WireGuard peer public key is missing")?,
        status.allowed_ips.join(", ")
    );
    if status.preshared_key_configured {
        let path = secret_path(&profile.id, true).map_err(|e| e.message)?;
        let psk =
            fs::read_to_string(&path).map_err(|e| format!("read WireGuard preshared key: {e}"))?;
        validate_key(psk.trim(), "stored WireGuard preshared key").map_err(|e| e.message)?;
        config.push_str(&format!("PresharedKey = {}\n", psk.trim()));
    }
    if let Some(endpoint) = status.endpoint.as_deref() {
        config.push_str(&format!("Endpoint = {endpoint}\n"));
    }
    if status.persistent_keepalive > 0 {
        config.push_str(&format!(
            "PersistentKeepalive = {}\n",
            status.persistent_keepalive
        ));
    }
    let directory = state_directory().map_err(|e| e.message)?;
    fs::create_dir_all(&directory).map_err(|e| format!("create egress state directory: {e}"))?;
    let path = directory.join(format!(
        ".wg-runtime-{}-{}.conf",
        profile.id,
        std::process::id()
    ));
    atomic_write_secret(&path, config.trim_end()).map_err(|e| e.message)?;
    Ok(path)
}

fn configure_wireguard(
    executor: &impl CommandExecutor,
    profile: &EgressProfile,
) -> Result<(), String> {
    let Some(status) = profile.wireguard.as_ref().filter(|status| status.managed) else {
        return run_checked(
            executor,
            "wg",
            vec!["show".to_string(), profile.tunnel_interface.clone()],
            "WireGuard interface is unavailable",
        );
    };
    if !Path::new("/sys/class/net")
        .join(&profile.tunnel_interface)
        .exists()
    {
        run_checked(
            executor,
            "ip",
            vec![
                "link".to_string(),
                "add".to_string(),
                "dev".to_string(),
                profile.tunnel_interface.clone(),
                "type".to_string(),
                "wireguard".to_string(),
            ],
            "create WireGuard interface",
        )?;
    }
    let runtime_config = write_wireguard_runtime_config(profile, status)?;
    let set_result = run_checked(
        executor,
        "wg",
        vec![
            "setconf".to_string(),
            profile.tunnel_interface.clone(),
            runtime_config.display().to_string(),
        ],
        "configure WireGuard interface",
    );
    let _ = fs::remove_file(&runtime_config);
    set_result?;
    for family in [Family::V4, Family::V6] {
        // This interface is managed exclusively by one profile; replacing its
        // global addresses avoids retaining stale source addresses on updates.
        let _ = executor.run(
            "ip",
            &[
                family.ip_flag().to_string(),
                "address".to_string(),
                "flush".to_string(),
                "dev".to_string(),
                profile.tunnel_interface.clone(),
                "scope".to_string(),
                "global".to_string(),
            ],
            None,
        );
    }
    for address in &status.addresses {
        let network =
            parse_network(address, "stored WireGuard address", false).map_err(|e| e.message)?;
        run_checked(
            executor,
            "ip",
            vec![
                network.family().ip_flag().to_string(),
                "address".to_string(),
                "replace".to_string(),
                network.host_string(),
                "dev".to_string(),
                profile.tunnel_interface.clone(),
            ],
            "assign WireGuard address",
        )?;
    }
    run_checked(
        executor,
        "ip",
        vec![
            "link".to_string(),
            "set".to_string(),
            "dev".to_string(),
            profile.tunnel_interface.clone(),
            "mtu".to_string(),
            status.mtu.to_string(),
            "up".to_string(),
        ],
        "activate WireGuard interface",
    )?;
    run_checked(
        executor,
        "wg",
        vec!["show".to_string(), profile.tunnel_interface.clone()],
        "verify WireGuard interface",
    )
}

fn managed_probe_sources(profile: &EgressProfile, family: Family) -> Result<Vec<String>, String> {
    let Some(status) = profile.wireguard.as_ref().filter(|status| status.managed) else {
        return Ok(Vec::new());
    };
    status
        .addresses
        .iter()
        .map(|address| {
            parse_network(address, "stored WireGuard address", false).map_err(|e| e.message)
        })
        .filter_map(|result| match result {
            Ok(network) if network.family() == family => Some(Ok(network.addr.to_string())),
            Ok(_) => None,
            Err(error) => Some(Err(error)),
        })
        .collect()
}

fn configure_health_probe_rules(
    executor: &impl CommandExecutor,
    profile: &EgressProfile,
    family: Family,
) -> Result<(), String> {
    let probe_priority = PROBE_RULE_PRIORITY_BASE + profile.route_table;
    for source in managed_probe_sources(profile, family)? {
        let mut probe_rule = vec![
            family.ip_flag().to_string(),
            "rule".to_string(),
            "del".to_string(),
            "priority".to_string(),
            probe_priority.to_string(),
            "from".to_string(),
            source,
            "table".to_string(),
            profile.route_table.to_string(),
        ];
        let deleted = executor.run("ip", &probe_rule, None)?;
        if !deleted.success
            && !deleted.stderr.contains("No such")
            && !deleted.stderr.contains("Cannot find")
        {
            return Err(concise_error(
                &deleted,
                "remove previous egress health probe rule",
            ));
        }
        probe_rule[2] = "add".to_string();
        run_checked(
            executor,
            "ip",
            probe_rule,
            "configure egress health probe rule",
        )?;
    }
    Ok(())
}

fn configure_profile_routes(
    executor: &impl CommandExecutor,
    application: &ProfileApplication,
) -> Result<(), String> {
    let profile = &application.profile;
    if profile.tunnel_type == "wireguard" {
        configure_wireguard(executor, profile)?;
    }
    for family in &application.families {
        let mut route = vec![
            family.ip_flag().to_string(),
            "route".to_string(),
            "replace".to_string(),
            "table".to_string(),
            profile.route_table.to_string(),
            "default".to_string(),
        ];
        if let Some(gateway) = profile
            .gateway
            .as_deref()
            .and_then(|value| value.parse::<IpAddr>().ok())
            .filter(|ip| ip.is_ipv4() == matches!(family, Family::V4))
        {
            route.extend(["via".to_string(), gateway.to_string()]);
        }
        route.extend(["dev".to_string(), profile.tunnel_interface.clone()]);
        run_checked(executor, "ip", route, "configure egress default route")?;
        let mark = effective_mark(profile.mark);
        let priority = RULE_PRIORITY_BASE + profile.route_table;
        let mut rule = vec![
            family.ip_flag().to_string(),
            "rule".to_string(),
            "del".to_string(),
            "priority".to_string(),
            priority.to_string(),
            "fwmark".to_string(),
            format!("0x{mark:08x}/0xffffffff"),
            "table".to_string(),
            profile.route_table.to_string(),
        ];
        let deleted = executor.run("ip", &rule, None)?;
        if !deleted.success
            && !deleted.stderr.contains("No such")
            && !deleted.stderr.contains("Cannot find")
        {
            return Err(concise_error(
                &deleted,
                "remove previous egress policy rule",
            ));
        }
        rule[2] = "add".to_string();
        run_checked(executor, "ip", rule, "configure egress policy rule")?;
        configure_health_probe_rules(executor, profile, *family)?;
    }
    Ok(())
}

fn probe_bind_target(profile: &EgressProfile, family: Family) -> Result<String, String> {
    if profile
        .wireguard
        .as_ref()
        .is_some_and(|status| status.managed)
    {
        return managed_probe_sources(profile, family)?
            .into_iter()
            .next()
            .ok_or_else(|| {
                format!(
                    "managed WireGuard has no local {} address for health probing",
                    match family {
                        Family::V4 => "IPv4",
                        Family::V6 => "IPv6",
                    }
                )
            });
    }
    Ok(profile.tunnel_interface.clone())
}

fn probe_profile_public_ip(
    executor: &impl CommandExecutor,
    profile: &EgressProfile,
    family: Family,
) -> Result<IpAddr, String> {
    let (family_arg, url) = match family {
        Family::V4 => ("--ipv4", PUBLIC_IPV4_PROBE_URL),
        Family::V6 => ("--ipv6", PUBLIC_IPV6_PROBE_URL),
    };
    let bind_target = probe_bind_target(profile, family)?;
    let args = vec![
        "--silent".to_string(),
        "--show-error".to_string(),
        "--fail".to_string(),
        "--connect-timeout".to_string(),
        "5".to_string(),
        "--max-time".to_string(),
        "15".to_string(),
        family_arg.to_string(),
        "--interface".to_string(),
        bind_target,
        url.to_string(),
    ];
    let result = executor.run("curl", &args, None)?;
    if !result.success {
        return Err(concise_error(
            &result,
            &format!(
                "probe public {} through {}",
                match family {
                    Family::V4 => "IPv4",
                    Family::V6 => "IPv6",
                },
                profile.tunnel_interface
            ),
        ));
    }
    let output = result.stdout.trim();
    if output.is_empty() || output.chars().any(char::is_whitespace) {
        return Err("public egress probe returned a non-scalar address".to_string());
    }
    let observed = output
        .parse::<IpAddr>()
        .map_err(|_| "public egress probe returned an invalid address".to_string())?;
    if observed.is_ipv4() != matches!(family, Family::V4) {
        return Err("public egress probe returned the wrong address family".to_string());
    }
    Ok(observed)
}

fn verify_wireguard_handshake(
    executor: &impl CommandExecutor,
    profile: &EgressProfile,
) -> Result<(), String> {
    let result = executor.run(
        "wg",
        &[
            "show".to_string(),
            profile.tunnel_interface.clone(),
            "latest-handshakes".to_string(),
        ],
        None,
    )?;
    if !result.success {
        return Err(concise_error(&result, "read WireGuard latest handshake"));
    }
    let latest = result
        .stdout
        .lines()
        .filter_map(|line| line.split_whitespace().nth(1))
        .filter_map(|value| value.parse::<i64>().ok())
        .max();
    if !latest.is_some_and(recent_wireguard_handshake) {
        return Err(format!(
            "WireGuard interface {} has no recent handshake",
            profile.tunnel_interface
        ));
    }
    Ok(())
}

fn verify_profile_health(
    executor: &impl CommandExecutor,
    application: &ProfileApplication,
) -> Result<(), String> {
    let profile = &application.profile;
    // The public probe runs first so a newly configured WireGuard peer has
    // traffic that can establish its initial handshake.
    for family in &application.families {
        let expected = match family {
            Family::V4 => profile.public_ipv4.as_deref(),
            Family::V6 => profile.public_ipv6.as_deref(),
        }
        .ok_or_else(|| {
            format!(
                "expected public {} is required for strict egress verification",
                match family {
                    Family::V4 => "IPv4",
                    Family::V6 => "IPv6",
                }
            )
        })?
        .parse::<IpAddr>()
        .map_err(|_| "stored expected public address is invalid".to_string())?;
        let observed = probe_profile_public_ip(executor, profile, *family)?;
        if observed != expected {
            return Err(format!(
                "public egress identity mismatch for {}: expected {}, observed {}",
                match family {
                    Family::V4 => "IPv4",
                    Family::V6 => "IPv6",
                },
                expected,
                observed
            ));
        }
    }
    if profile.tunnel_type == "wireguard" {
        verify_wireguard_handshake(executor, profile)?;
    }
    Ok(())
}

fn cleanup_runtime_profile(
    executor: &impl CommandExecutor,
    runtime: &RuntimeProfile,
    delete_interface: bool,
) -> Result<(), String> {
    let mut errors = Vec::new();
    for (family, enabled) in [(Family::V4, runtime.has_v4), (Family::V6, runtime.has_v6)] {
        if !enabled {
            continue;
        }
        let probe_priority = PROBE_RULE_PRIORITY_BASE + runtime.route_table;
        for source in &runtime.probe_sources {
            let Ok(address) = source.parse::<IpAddr>() else {
                errors.push("stored egress health probe source is invalid".to_string());
                continue;
            };
            if address.is_ipv4() != matches!(family, Family::V4) {
                continue;
            }
            let probe_rule_args = vec![
                family.ip_flag().to_string(),
                "rule".to_string(),
                "del".to_string(),
                "priority".to_string(),
                probe_priority.to_string(),
                "from".to_string(),
                address.to_string(),
                "table".to_string(),
                runtime.route_table.to_string(),
            ];
            if let Ok(result) = executor.run("ip", &probe_rule_args, None)
                && !result.success
                && !result.stderr.contains("No such")
                && !result.stderr.contains("Cannot find")
            {
                errors.push(concise_error(
                    &result,
                    "remove stale egress health probe rule",
                ));
            }
        }
        let mark = effective_mark(runtime.mark);
        let priority = RULE_PRIORITY_BASE + runtime.route_table;
        let rule_args = vec![
            family.ip_flag().to_string(),
            "rule".to_string(),
            "del".to_string(),
            "priority".to_string(),
            priority.to_string(),
            "fwmark".to_string(),
            format!("0x{mark:08x}/0xffffffff"),
            "table".to_string(),
            runtime.route_table.to_string(),
        ];
        if let Ok(result) = executor.run("ip", &rule_args, None) {
            if !result.success
                && !result.stderr.contains("No such")
                && !result.stderr.contains("Cannot find")
            {
                errors.push(concise_error(&result, "remove stale egress rule"));
            }
        }
        let route_args = vec![
            family.ip_flag().to_string(),
            "route".to_string(),
            "del".to_string(),
            "table".to_string(),
            runtime.route_table.to_string(),
            "default".to_string(),
        ];
        if let Ok(result) = executor.run("ip", &route_args, None) {
            if !result.success
                && !result.stderr.contains("No such")
                && !result.stderr.contains("Cannot find")
            {
                errors.push(concise_error(&result, "remove stale egress route"));
            }
        }
    }
    if delete_interface && runtime.managed_interface {
        let args = vec![
            "link".to_string(),
            "delete".to_string(),
            "dev".to_string(),
            runtime.tunnel_interface.clone(),
        ];
        if let Ok(result) = executor.run("ip", &args, None) {
            if !result.success
                && !result.stderr.contains("does not exist")
                && !result.stderr.contains("Cannot find")
            {
                errors.push(concise_error(&result, "remove stale WireGuard interface"));
            }
        }
    }
    if errors.is_empty() {
        Ok(())
    } else {
        Err(errors.join("; "))
    }
}
