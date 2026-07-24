#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    fn capabilities() -> HostCapabilities {
        HostCapabilities {
            supported: true,
            mode: "native".to_string(),
            running_as_root: true,
            ip_available: true,
            nft_available: true,
            wireguard_available: true,
            curl_available: true,
            ipv4_forwarding: true,
            ipv6_forwarding: true,
            apply_enabled: true,
            auto_install_enabled: false,
            package_manager: Some("apt-get".to_string()),
            missing_dependencies: Vec::new(),
            checked_at: 0,
            reasons: Vec::new(),
        }
    }

    fn gateway_profile() -> ProfileRow {
        ProfileRow {
            profile: EgressProfile {
                id: "profile-1".to_string(),
                mode: "native".to_string(),
                tunnel_type: "gateway".to_string(),
                tunnel_interface: "tun0".to_string(),
                gateway: None,
                route_table: 100,
                mark: 7,
                public_ipv4: Some("192.0.2.10".to_string()),
                public_ipv6: Some("2001:db8::10".to_string()),
                enabled: true,
                fail_closed: true,
                status: "pending".to_string(),
                last_error: None,
                updated_at: 0,
                wireguard: None,
                tunnel_ready: true,
                last_handshake_at: None,
            },
        }
    }

    fn gateway_profile_request(id: &str) -> EgressProfileRequest {
        EgressProfileRequest {
            id: id.to_string(),
            mode: "native".to_string(),
            tunnel_type: Some("gateway".to_string()),
            tunnel_interface: "tun0".to_string(),
            gateway: None,
            route_table: Some(100),
            mark: Some(7),
            public_ipv4: Some("192.0.2.10".to_string()),
            public_ipv6: None,
            enabled: Some(true),
            fail_closed: Some(true),
            wireguard: None,
        }
    }

    fn managed_wireguard_profile() -> EgressProfile {
        let mut profile = gateway_profile().profile;
        profile.tunnel_type = "wireguard".to_string();
        profile.tunnel_interface = "wg-ocv".to_string();
        let mut status = default_wg_status();
        status.managed = true;
        status.addresses = vec![
            "10.250.0.2/24".to_string(),
            "2001:db8:250::2/64".to_string(),
        ];
        profile.wireguard = Some(status);
        profile
    }

    fn binding_request(instance_id: &str, profile_id: &str) -> EgressBindingRequest {
        EgressBindingRequest {
            instance_id: instance_id.to_string(),
            profile_id: profile_id.to_string(),
            source: "10.0.0.2".to_string(),
            sources: None,
            interface: Some("veth100".to_string()),
            interface_v4: None,
            interface_v6: None,
            enabled: Some(true),
        }
    }

    fn binding(instance_id: &str, source: &str, additional: &[&str]) -> BindingRow {
        binding_from_request(EgressBindingRequest {
            instance_id: instance_id.to_string(),
            profile_id: "profile-1".to_string(),
            source: source.to_string(),
            sources: Some(
                additional
                    .iter()
                    .map(|value| (*value).to_string())
                    .collect(),
            ),
            interface: Some("veth100".to_string()),
            interface_v4: Some("veth100".to_string()),
            interface_v6: Some("veth100".to_string()),
            enabled: Some(true),
        })
        .unwrap()
    }

    fn inventory() -> HostInventory {
        HostInventory {
            interfaces: HashSet::from(["tun0".to_string(), "veth100".to_string()]),
            ..HostInventory::default()
        }
    }

    #[test]
    fn rejects_control_characters_and_command_syntax() {
        assert!(parse_network("2001:db8::1/80\n64", "source", true).is_err());
        assert!(parse_network("2001:db8::1/80'", "source", true).is_err());
        assert!(validate_interface("wg0;reboot", "interface").is_err());
        assert!(validate_endpoint("vpn.example.com:51820\nPostUp=x").is_err());
        assert!(validate_key("not-a-wireguard-key", "key").is_err());
    }

    #[test]
    fn accepts_discrete_and_small_ipv6_networks() {
        assert_eq!(
            parse_network("2001:db8::42", "source", true)
                .unwrap()
                .to_string(),
            "2001:db8::42/128"
        );
        assert_eq!(
            parse_network("2001:db8:1:2:3::42/80", "source", true)
                .unwrap()
                .to_string(),
            "2001:db8:1:2:3::/80"
        );
        assert_eq!(
            parse_network("2001:db8::/127", "source", true)
                .unwrap()
                .to_string(),
            "2001:db8::/127"
        );
    }

    #[test]
    fn replacement_rejects_duplicates_and_missing_profile_references() {
        let duplicate_profiles = normalize_replace_state_request(ReplaceStateRequest {
            profiles: vec![
                gateway_profile_request("profile-1"),
                gateway_profile_request("profile-1"),
            ],
            bindings: Vec::new(),
            apply: Some(true),
        })
        .unwrap_err();
        assert!(
            duplicate_profiles
                .message
                .contains("duplicate egress profile")
        );

        let duplicate_bindings = normalize_replace_state_request(ReplaceStateRequest {
            profiles: vec![gateway_profile_request("profile-1")],
            bindings: vec![
                binding_request("instance-1", "profile-1"),
                binding_request("instance-1", "profile-1"),
            ],
            apply: Some(true),
        })
        .unwrap_err();
        assert!(
            duplicate_bindings
                .message
                .contains("duplicate egress binding")
        );

        let missing_profile = normalize_replace_state_request(ReplaceStateRequest {
            profiles: vec![gateway_profile_request("profile-1")],
            bindings: vec![binding_request("instance-1", "profile-2")],
            apply: Some(true),
        })
        .unwrap_err();
        assert!(missing_profile.message.contains("outside this state batch"));
    }

    #[test]
    fn empty_replacement_removes_all_desired_state_in_one_transaction() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        upsert_profile_row(&conn, &gateway_profile().profile).unwrap();
        upsert_binding_row(&conn, &binding("instance-1", "10.0.0.2", &[])).unwrap();

        replace_desired_state_sql(&conn, &[], &[]).unwrap();

        let profile_count: i64 = conn
            .query_row("SELECT COUNT(*) FROM egress_profiles", [], |row| row.get(0))
            .unwrap();
        let binding_count: i64 = conn
            .query_row("SELECT COUNT(*) FROM egress_bindings", [], |row| row.get(0))
            .unwrap();
        assert_eq!((profile_count, binding_count), (0, 0));
    }

    #[test]
    fn failed_replacement_transaction_preserves_previous_state() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        let previous = gateway_profile().profile;
        upsert_profile_row(&conn, &previous).unwrap();
        let mut first = previous.clone();
        first.tunnel_interface = "tun1".to_string();
        let mut duplicate = first.clone();
        duplicate.tunnel_interface = "tun2".to_string();

        assert!(replace_desired_state_sql(&conn, &[first, duplicate], &[]).is_err());
        let stored: String = conn
            .query_row(
                "SELECT tunnel_interface FROM egress_profiles WHERE id='profile-1'",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(stored, "tun0");
    }

    #[test]
    fn keeps_wireguard_interface_host_bits() {
        let values = validate_vec_networks(
            Some(vec![
                "10.8.0.2/24".to_string(),
                "2001:db8::2/64".to_string(),
            ]),
            "address",
            &[],
            true,
            false,
        )
        .unwrap();
        assert_eq!(values, vec!["10.8.0.2/24", "2001:db8::2/64"]);
    }

    #[test]
    fn dual_stack_binding_uses_one_profile_without_leak_family() {
        let row = binding("instance-1", "10.0.0.2", &["2001:db8::2"]);
        assert_eq!(row.networks.len(), 2);
        assert!(
            row.networks
                .iter()
                .any(|network| network.family() == Family::V4)
        );
        assert!(
            row.networks
                .iter()
                .any(|network| network.family() == Family::V6)
        );
        assert_eq!(row.binding.sources, vec!["2001:db8::2/128", "10.0.0.2/32"]);
    }

    #[test]
    fn nft_plan_classifies_forwarded_dual_stack_and_blocks_local_dns() {
        let profiles = vec![gateway_profile()];
        let mut row = binding("instance-1", "10.0.0.2", &["2001:db8::2"]);
        row.binding.interface_v6 = Some("veth200".to_string());
        let bindings = vec![row];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        assert_eq!(prepared.plans[0].status, "planned");
        let script = build_nft_script(&prepared.nft_bindings, false, false);
        assert!(script.contains("hook prerouting priority -150"));
        assert!(script.contains("hook forward priority 0"));
        assert!(script.contains("ip saddr 10.0.0.2/32"));
        assert!(script.contains("ip6 saddr 2001:db8::2/128"));
        assert!(script.contains("udp sport 68 udp dport 67 accept"));
        assert!(script.contains("udp sport 546 udp dport 547 accept"));
        assert!(script.contains("icmpv6 type { 133, 135, 136 } accept"));
        assert!(script.contains("enforce_input ip saddr 10.0.0.2/32"));
        assert!(!script.contains("classify_output"));
        assert!(script.contains("oifname != \"tun0\""));
        assert!(script.contains("enforce_forward iifname \"veth100\" meta nfproto ipv4 drop"));
        assert!(script.contains("classify_prerouting iifname \"veth200\" ip6 saddr"));
        assert!(script.contains("enforce_forward iifname \"veth200\" meta nfproto ipv6 drop"));
        assert!(script.contains("enforce_input iifname \"veth100\" meta nfproto ipv4 drop"));
        assert!(script.contains("enforce_input iifname \"veth200\" meta nfproto ipv6 drop"));
        assert!(script.contains("drop"));
    }

    #[test]
    fn overlapping_sources_fail_closed_for_both_bindings() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![
            binding("instance-1", "10.0.0.0/24", &[]),
            binding("instance-2", "10.0.0.4", &[]),
        ];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        assert!(prepared.plans.iter().all(|plan| plan.status == "blocked"));
        assert!(
            prepared
                .nft_bindings
                .iter()
                .all(|binding| binding.quarantine)
        );
    }

    #[derive(Default)]
    struct RecordingExecutor {
        calls: Mutex<Vec<(String, Vec<String>, Option<String>)>>,
    }

    impl CommandExecutor for RecordingExecutor {
        fn run(
            &self,
            program: &str,
            args: &[String],
            input: Option<&str>,
        ) -> Result<CommandResult, String> {
            self.calls.lock().unwrap().push((
                program.to_string(),
                args.to_vec(),
                input.map(str::to_string),
            ));
            let is_list = program == "nft" && args.iter().any(|arg| arg == "list");
            let stdout = if program == "curl" && args.iter().any(|arg| arg == "--ipv4") {
                "192.0.2.10\n".to_string()
            } else if program == "curl" && args.iter().any(|arg| arg == "--ipv6") {
                "2001:db8::10\n".to_string()
            } else {
                String::new()
            };
            Ok(CommandResult {
                success: !is_list,
                stdout,
                stderr: if is_list {
                    "No such file or directory".to_string()
                } else {
                    String::new()
                },
            })
        }
    }

    #[derive(Default)]
    struct PublicIpMismatchExecutor {
        calls: Mutex<Vec<(String, Vec<String>, Option<String>)>>,
    }

    impl CommandExecutor for PublicIpMismatchExecutor {
        fn run(
            &self,
            program: &str,
            args: &[String],
            input: Option<&str>,
        ) -> Result<CommandResult, String> {
            self.calls.lock().unwrap().push((
                program.to_string(),
                args.to_vec(),
                input.map(str::to_string),
            ));
            let is_list = program == "nft" && args.iter().any(|arg| arg == "list");
            let stdout = if program == "curl" {
                "198.51.100.99\n".to_string()
            } else {
                String::new()
            };
            Ok(CommandResult {
                success: !is_list,
                stdout,
                stderr: if is_list {
                    "No such file or directory".to_string()
                } else {
                    String::new()
                },
            })
        }
    }

    struct FailingStagingExecutor;

    impl CommandExecutor for FailingStagingExecutor {
        fn run(
            &self,
            program: &str,
            args: &[String],
            _input: Option<&str>,
        ) -> Result<CommandResult, String> {
            let is_list = program == "nft" && args.iter().any(|arg| arg == "list");
            Ok(CommandResult {
                success: is_list,
                stdout: String::new(),
                stderr: if is_list {
                    String::new()
                } else {
                    "injected staging quarantine failure".to_string()
                },
            })
        }
    }

    fn test_managed_sources_path(label: &str) -> PathBuf {
        env::temp_dir()
            .join(format!(
                "oneclickvirt-egress-binding-{label}-{}-{}",
                std::process::id(),
                now_ts()
            ))
            .join("managed-sources")
    }

    #[test]
    fn binding_put_stages_quarantine_before_commit_and_acknowledges_enforcement() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute(
            "INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)",
            [],
        )
        .unwrap();
        let path = test_managed_sources_path("success");
        let row = binding("instance-1", "10.0.0.2", &[]);
        let executor = RecordingExecutor::default();
        let saved = persist_binding_with_quarantine(&conn, &executor, row, &path).unwrap();

        assert_eq!(saved.state, "pending");
        assert_eq!(saved.fail_closed_enforced, Some(true));
        assert_eq!(fs::read_to_string(&path).unwrap(), "10.0.0.2/32\n");
        let count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM egress_bindings WHERE instance_id='instance-1'",
                [],
                |value| value.get(0),
            )
            .unwrap();
        assert_eq!(count, 1);
        let scripts: Vec<String> = executor
            .calls
            .lock()
            .unwrap()
            .iter()
            .filter(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .filter_map(|(_, _, input)| input.clone())
            .collect();
        assert_eq!(
            scripts.len(),
            1,
            "nft must apply the validated staging transaction"
        );
        assert!(scripts[0].contains("oneclickvirt_egress_boot"));
        assert!(scripts[0].contains("boot_forward ip saddr 10.0.0.2/32 counter drop"));
        assert!(scripts[0].contains("boot_output ip saddr 10.0.0.2/32 counter drop"));
        fs::remove_dir_all(path.parent().unwrap()).unwrap();
    }

    #[test]
    fn binding_put_does_not_commit_when_staging_quarantine_fails() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute(
            "INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)",
            [],
        )
        .unwrap();
        let path = test_managed_sources_path("failure");
        let row = binding("instance-1", "10.0.0.2", &[]);
        let error = persist_binding_with_quarantine(&conn, &FailingStagingExecutor, row, &path)
            .expect_err("failed quarantine must reject the binding PUT");
        assert!(error.message.contains("staging quarantine"));
        let count: i64 = conn
            .query_row("SELECT COUNT(*) FROM egress_bindings", [], |value| {
                value.get(0)
            })
            .unwrap();
        assert_eq!(count, 0);
        assert!(!path.exists());
    }

    #[test]
    fn binding_update_stages_old_and_new_sources_until_reconcile() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute(
            "INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)",
            [],
        )
        .unwrap();
        conn.execute(
            "INSERT INTO egress_bindings (instance_id,profile_id,source,interface,enabled,state,last_error,created_at,updated_at,sources_json,fail_closed_enforced) VALUES ('instance-1','profile-1','10.0.0.2/32','veth100',1,'applied','',1,1,'[\"10.0.0.2/32\"]',1)",
            [],
        )
        .unwrap();
        let path = test_managed_sources_path("update");
        let row = binding("instance-1", "10.0.0.3", &[]);
        let executor = RecordingExecutor::default();
        let saved = persist_binding_with_quarantine(&conn, &executor, row, &path).unwrap();
        assert_eq!(saved.fail_closed_enforced, Some(true));
        let scripts: Vec<String> = executor
            .calls
            .lock()
            .unwrap()
            .iter()
            .filter(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .filter_map(|(_, _, input)| input.clone())
            .collect();
        assert!(scripts[0].contains("saddr 10.0.0.2/32 counter drop"));
        assert!(scripts[0].contains("saddr 10.0.0.3/32 counter drop"));
        fs::remove_dir_all(path.parent().unwrap()).unwrap();
    }

    struct MidProfileFailureExecutor {
        calls: Mutex<Vec<(String, Vec<String>, Option<String>)>>,
        failed: Mutex<bool>,
        fail_cleanup: bool,
    }

    impl MidProfileFailureExecutor {
        fn new(fail_cleanup: bool) -> Self {
            Self {
                calls: Mutex::new(Vec::new()),
                failed: Mutex::new(false),
                fail_cleanup,
            }
        }
    }

    impl CommandExecutor for MidProfileFailureExecutor {
        fn run(
            &self,
            program: &str,
            args: &[String],
            input: Option<&str>,
        ) -> Result<CommandResult, String> {
            self.calls.lock().unwrap().push((
                program.to_string(),
                args.to_vec(),
                input.map(str::to_string),
            ));
            let is_nft_list = program == "nft" && args.iter().any(|arg| arg == "list");
            let is_rule_add =
                program == "ip" && args.windows(2).any(|window| window == ["rule", "add"]);
            let failed_before = *self.failed.lock().unwrap();
            let is_failed_cleanup = self.fail_cleanup
                && failed_before
                && program == "ip"
                && args.windows(2).any(|window| window == ["route", "del"]);
            if is_rule_add {
                *self.failed.lock().unwrap() = true;
            }
            let success = !(is_nft_list || is_rule_add || is_failed_cleanup);
            Ok(CommandResult {
                success,
                stdout: String::new(),
                stderr: if is_nft_list {
                    "No such file or directory".to_string()
                } else if is_rule_add {
                    "injected policy rule failure".to_string()
                } else if is_failed_cleanup {
                    "injected rollback failure".to_string()
                } else {
                    String::new()
                },
            })
        }
    }

    #[test]
    fn apply_installs_atomic_kill_switch_before_routes_and_uses_rule_del_add() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![binding("instance-1", "10.0.0.2", &["2001:db8::2"])];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        let executor = RecordingExecutor::default();
        let outcome = apply_prepared(&executor, &prepared, &[]);
        assert!(outcome.fail_closed);
        assert!(outcome.profile_errors.is_empty());
        let calls = executor.calls.lock().unwrap();
        let nft_apply = calls
            .iter()
            .position(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .unwrap();
        let first_ip = calls
            .iter()
            .position(|(program, _, _)| program == "ip")
            .unwrap();
        assert!(nft_apply < first_ip);
        let nft_scripts: Vec<&str> = calls
            .iter()
            .filter(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .filter_map(|(_, _, input)| input.as_deref())
            .collect();
        assert_eq!(nft_scripts.len(), 2);
        assert!(!nft_scripts[0].contains("meta mark set"));
        assert!(nft_scripts[0].contains("enforce_forward ip saddr 10.0.0.2/32"));
        assert!(nft_scripts[1].contains("meta mark set"));
        assert!(
            calls
                .iter()
                .all(|(program, _, _)| !matches!(program.as_str(), "sh" | "bash" | "zsh"))
        );
        assert!(calls.iter().any(|(program, args, _)| program == "ip"
            && args.windows(2).any(|window| window == ["rule", "del"])));
        assert!(calls.iter().any(|(program, args, _)| program == "ip"
            && args.windows(2).any(|window| window == ["rule", "add"])));
        assert!(!calls.iter().any(|(program, args, _)| program == "ip"
            && args.windows(2).any(|window| window == ["rule", "replace"])));
    }

    #[test]
    fn managed_wireguard_probe_rules_bind_and_cleanup_by_source() {
        let profile = managed_wireguard_profile();
        let executor = RecordingExecutor::default();
        configure_health_probe_rules(&executor, &profile, Family::V4).unwrap();
        configure_health_probe_rules(&executor, &profile, Family::V6).unwrap();
        assert_eq!(
            probe_profile_public_ip(&executor, &profile, Family::V4).unwrap(),
            "192.0.2.10".parse::<IpAddr>().unwrap()
        );
        assert_eq!(
            probe_profile_public_ip(&executor, &profile, Family::V6).unwrap(),
            "2001:db8::10".parse::<IpAddr>().unwrap()
        );

        let runtime = RuntimeProfile {
            profile_id: profile.id.clone(),
            route_table: profile.route_table,
            mark: profile.mark,
            tunnel_interface: profile.tunnel_interface.clone(),
            has_v4: true,
            has_v6: true,
            managed_interface: false,
            probe_sources: vec!["10.250.0.2".to_string(), "2001:db8:250::2".to_string()],
        };
        let cleanup_start = executor.calls.lock().unwrap().len();
        cleanup_runtime_profile(&executor, &runtime, false).unwrap();

        let calls = executor.calls.lock().unwrap();
        let priority = (PROBE_RULE_PRIORITY_BASE + profile.route_table).to_string();
        for (family, source) in [("-4", "10.250.0.2"), ("-6", "2001:db8:250::2")] {
            assert!(calls.iter().any(|(program, args, _)| {
                program == "ip"
                    && args
                        == &[
                            family,
                            "rule",
                            "add",
                            "priority",
                            priority.as_str(),
                            "from",
                            source,
                            "table",
                            "100",
                        ]
            }));
            assert!(calls[cleanup_start..].iter().any(|(program, args, _)| {
                program == "ip"
                    && args
                        == &[
                            family,
                            "rule",
                            "del",
                            "priority",
                            priority.as_str(),
                            "from",
                            source,
                            "table",
                            "100",
                        ]
            }));
        }
        assert!(calls.iter().any(|(program, args, _)| {
            program == "curl"
                && args
                    .windows(2)
                    .any(|window| window == ["--interface", "10.250.0.2"])
        }));
        assert!(calls.iter().any(|(program, args, _)| {
            program == "curl"
                && args
                    .windows(2)
                    .any(|window| window == ["--interface", "2001:db8:250::2"])
        }));
    }

    #[test]
    fn public_identity_mismatch_keeps_profile_quarantined() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![binding("instance-1", "10.0.0.2", &[])];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        let executor = PublicIpMismatchExecutor::default();
        let outcome = apply_prepared(&executor, &prepared, &[]);
        assert!(outcome.fail_closed);
        assert!(
            outcome
                .profile_errors
                .get("profile-1")
                .is_some_and(|error| error.contains("identity mismatch"))
        );
        let calls = executor.calls.lock().unwrap();
        let final_nft = calls
            .iter()
            .rev()
            .find(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .and_then(|(_, _, input)| input.as_deref())
            .unwrap();
        assert!(!final_nft.contains("meta mark set"));
        assert!(final_nft.contains("ip saddr 10.0.0.2/32 counter name"));
        assert!(final_nft.contains("drop"));
    }

    #[test]
    fn failed_profile_apply_rolls_back_before_omitting_runtime_state() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![binding("instance-1", "10.0.0.2", &[])];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        let executor = MidProfileFailureExecutor::new(false);
        let outcome = apply_prepared(&executor, &prepared, &[]);
        assert!(outcome.fail_closed);
        assert!(outcome.profile_errors.contains_key("profile-1"));
        assert!(outcome.runtime.is_empty());

        let calls = executor.calls.lock().unwrap();
        let failed_add = calls
            .iter()
            .position(|(program, args, _)| {
                program == "ip" && args.windows(2).any(|window| window == ["rule", "add"])
            })
            .unwrap();
        let cleanup_rule = calls
            .iter()
            .rposition(|(program, args, _)| {
                program == "ip" && args.windows(2).any(|window| window == ["rule", "del"])
            })
            .unwrap();
        let cleanup_route = calls
            .iter()
            .position(|(program, args, _)| {
                program == "ip" && args.windows(2).any(|window| window == ["route", "del"])
            })
            .unwrap();
        assert!(failed_add < cleanup_rule);
        assert!(cleanup_rule < cleanup_route);
        let final_nft = calls
            .iter()
            .rev()
            .find(|(program, args, _)| program == "nft" && args == &["-f", "-"])
            .and_then(|(_, _, input)| input.as_deref())
            .unwrap();
        assert!(!final_nft.contains("meta mark set"));
        assert!(final_nft.contains("enforce_forward ip saddr 10.0.0.2/32"));
    }

    #[test]
    fn failed_profile_rollback_retains_runtime_for_later_cleanup() {
        let profiles = vec![gateway_profile()];
        let bindings = vec![binding("instance-1", "10.0.0.2", &[])];
        let prepared = prepare_reconcile(&profiles, &bindings, &capabilities(), &inventory(), true);
        let executor = MidProfileFailureExecutor::new(true);
        let outcome = apply_prepared(&executor, &prepared, &[]);
        assert_eq!(outcome.runtime.len(), 1);
        assert_eq!(outcome.runtime[0].profile_id, "profile-1");
        assert!(
            outcome
                .profile_errors
                .get("profile-1")
                .is_some_and(|error| error.contains("rollback failed"))
        );
    }

    #[test]
    fn native_binding_without_ingress_interface_stays_quarantined() {
        let profiles = vec![gateway_profile()];
        let mut row = binding("instance-1", "10.0.0.2", &[]);
        row.binding.interface = None;
        row.binding.interface_v4 = None;
        let prepared = prepare_reconcile(&profiles, &[row], &capabilities(), &inventory(), true);
        assert_eq!(prepared.plans[0].status, "blocked");
        assert!(
            prepared.plans[0]
                .error
                .as_deref()
                .is_some_and(|error| error.contains("ingress interface"))
        );
        assert!(
            prepared
                .nft_bindings
                .iter()
                .all(|binding| binding.quarantine)
        );
    }

    #[test]
    fn dependency_plans_cover_supported_package_managers() {
        for manager in ["apt-get", "dnf", "yum", "apk", "pacman", "zypper"] {
            let plan = dependency_command_plan(manager, true).unwrap();
            let flattened = plan.concat();
            assert!(flattened.iter().any(|value| value == "nftables"));
            assert!(flattened.iter().any(|value| value == "wireguard-tools"));
            assert!(!flattened.iter().any(|value| value.contains(';')));
        }
        let apt = dependency_command_plan("apt-get", false).unwrap();
        assert_eq!(apt[0], ["update", "-qq"]);
        assert!(apt[1].starts_with(&[
            "install".to_string(),
            "-y".to_string(),
            "--no-install-recommends".to_string()
        ]));
        assert_eq!(
            dependency_command_plan("pacman", false).unwrap()[0][..2],
            ["-Sy", "--noconfirm"]
        );
        assert_eq!(
            dependency_command_plan("zypper", false).unwrap()[0][..2],
            ["--non-interactive", "install"]
        );
    }

    #[test]
    fn counter_snapshot_is_parsed_once_by_name() {
        let value: Value = serde_json::json!({"nftables":[{"counter":{"name":"ocv_o_deadbeef","packets":2,"bytes":1234}}]});
        let mut counters = HashMap::new();
        collect_nft_counters(&value, &mut counters);
        assert_eq!(counters.get("ocv_o_deadbeef"), Some(&1234));
    }

    #[test]
    fn database_migration_is_idempotent() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        init_db(&conn).unwrap();
        let sources_column: i64 = conn.query_row("SELECT COUNT(*) FROM pragma_table_info('egress_bindings') WHERE name='sources_json'", [], |row| row.get(0)).unwrap();
        let enforcement_column: i64 = conn.query_row("SELECT COUNT(*) FROM pragma_table_info('egress_bindings') WHERE name='fail_closed_enforced'", [], |row| row.get(0)).unwrap();
        let runtime_table: i64 = conn.query_row("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='egress_runtime_profiles'", [], |row| row.get(0)).unwrap();
        let probe_sources_column: i64 = conn.query_row("SELECT COUNT(*) FROM pragma_table_info('egress_runtime_profiles') WHERE name='probe_sources_json'", [], |row| row.get(0)).unwrap();
        assert_eq!(sources_column, 1);
        assert_eq!(enforcement_column, 1);
        assert_eq!(runtime_table, 1);
        assert_eq!(probe_sources_column, 1);
    }

    #[test]
    fn managed_sources_file_is_canonical_sorted_and_private() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute("INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)", []).unwrap();
        conn.execute("INSERT INTO egress_bindings (instance_id,profile_id,source,interface,enabled,created_at,updated_at,sources_json) VALUES ('instance-1','profile-1','10.0.0.2/32','veth100',1,1,1,'[\"2001:db8::2/128\",\"10.0.0.2/32\"]')", []).unwrap();
        let directory = env::temp_dir().join(format!(
            "oneclickvirt-egress-test-{}-{}",
            std::process::id(),
            now_ts()
        ));
        let path = directory.join("managed-sources");
        write_managed_sources_at(&conn, &path).unwrap();
        assert_eq!(
            fs::read_to_string(&path).unwrap(),
            "10.0.0.2/32\n2001:db8::2/128\n"
        );
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        conn.execute("UPDATE egress_bindings SET enabled=0", [])
            .unwrap();
        write_managed_sources_at(&conn, &path).unwrap();
        assert_eq!(fs::read_to_string(&path).unwrap(), "");
        fs::remove_dir_all(directory).unwrap();
    }

    #[test]
    fn reconcile_persists_observed_fail_closed_enforcement() {
        let conn = rusqlite::Connection::open_in_memory().unwrap();
        init_db(&conn).unwrap();
        conn.execute("INSERT INTO egress_profiles (id,mode,tunnel_interface,created_at,updated_at) VALUES ('profile-1','native','tun0',1,1)", []).unwrap();
        conn.execute("INSERT INTO egress_bindings (instance_id,profile_id,source,interface,enabled,created_at,updated_at,sources_json) VALUES ('instance-1','profile-1','10.0.0.2/32','veth100',1,1,1,'[\"10.0.0.2/32\"]')", []).unwrap();
        let prepared = PreparedReconcile {
            plans: vec![RoutePlan {
                instance_id: "instance-1".to_string(),
                profile_id: "profile-1".to_string(),
                status: "planned".to_string(),
                commands: Vec::new(),
                error: None,
            }],
            nft_bindings: Vec::new(),
            applications: Vec::new(),
        };
        let outcome = ApplyOutcome {
            fail_closed: true,
            nft_replaced: true,
            profile_errors: HashMap::new(),
            global_errors: Vec::new(),
            runtime: Vec::new(),
            counters: HashMap::new(),
        };
        persist_reconcile(&conn, &prepared, &outcome, &HashMap::new(), true, true).unwrap();
        let enforced: Option<i64> = conn
            .query_row(
                "SELECT fail_closed_enforced FROM egress_bindings WHERE instance_id='instance-1'",
                [],
                |row| row.get(0),
            )
            .unwrap();
        assert_eq!(enforced, Some(1));
    }

    #[test]
    fn profile_serialization_never_contains_private_material() {
        let profile = gateway_profile().profile;
        let json = serde_json::to_string(&profile).unwrap();
        assert!(!json.contains("private_key"));
        assert!(!json.contains("preshared_key"));
    }
}
