#[derive(Debug)]
struct ApplyOutcome {
    fail_closed: bool,
    nft_replaced: bool,
    profile_errors: HashMap<String, String>,
    global_errors: Vec<String>,
    runtime: Vec<RuntimeProfile>,
    counters: HashMap<String, u64>,
}

fn apply_prepared(
    executor: &impl CommandExecutor,
    prepared: &PreparedReconcile,
    previous: &[RuntimeProfile],
) -> ApplyOutcome {
    let counters = read_nft_counters(executor);
    let mut outcome = ApplyOutcome {
        fail_closed: prepared.nft_bindings.is_empty(),
        nft_replaced: false,
        profile_errors: HashMap::new(),
        global_errors: Vec::new(),
        runtime: previous.to_vec(),
        counters,
    };
    let table_exists = match nft_table_exists(executor) {
        Ok(value) => value,
        Err(error) => {
            outcome
                .global_errors
                .push(format!("inspect nft egress table: {error}"));
            return outcome;
        }
    };
    if prepared.nft_bindings.is_empty() {
        // Absence of the table is already the correct atomic state.
        outcome.nft_replaced = true;
        if table_exists {
            let args = vec![
                "delete".to_string(),
                "table".to_string(),
                TABLE_FAMILY.to_string(),
                TABLE_NAME.to_string(),
            ];
            match run_checked(executor, "nft", args, "remove empty egress table") {
                Ok(()) => {}
                Err(error) => {
                    outcome.global_errors.push(error);
                    return outcome;
                }
            }
        }
    } else {
        // Install a quarantine table before touching routes or tunnel state.
        // The only transition out of quarantine is a later atomic nft replace
        // after every profile has configured (or been explicitly quarantined).
        let quarantine = build_nft_script(&prepared.nft_bindings, table_exists, true);
        if let Err(error) = apply_nft_script(executor, &quarantine) {
            outcome.global_errors.push(error);
            let emergency = build_nft_script(&prepared.nft_bindings, table_exists, true);
            match apply_nft_script(executor, &emergency) {
                Ok(()) => {
                    outcome.fail_closed = true;
                    outcome.nft_replaced = true;
                }
                Err(emergency_error) => outcome.global_errors.push(format!(
                    "emergency fail-closed nft transaction failed: {emergency_error}"
                )),
            }
            return outcome;
        }
        outcome.fail_closed = true;
        outcome.nft_replaced = true;
    }

    let desired_runtime: HashMap<String, RuntimeProfile> = prepared
        .applications
        .iter()
        .map(|application| {
            let profile = &application.profile;
            let probe_sources = [Family::V4, Family::V6]
                .into_iter()
                .filter(|family| application.families.contains(family))
                .flat_map(|family| managed_probe_sources(profile, family).unwrap_or_default())
                .collect();
            (
                profile.id.clone(),
                RuntimeProfile {
                    profile_id: profile.id.clone(),
                    route_table: profile.route_table,
                    mark: profile.mark,
                    tunnel_interface: profile.tunnel_interface.clone(),
                    has_v4: application.families.contains(&Family::V4),
                    has_v6: application.families.contains(&Family::V6),
                    managed_interface: profile.wireguard.as_ref().is_some_and(|wg| wg.managed),
                    probe_sources,
                },
            )
        })
        .collect();
    let desired_interfaces: HashSet<&str> = desired_runtime
        .values()
        .map(|runtime| runtime.tunnel_interface.as_str())
        .collect();
    let mut retained = Vec::new();
    for old in previous {
        let unchanged = desired_runtime.get(&old.profile_id).is_some_and(|new| {
            old.route_table == new.route_table
                && old.mark == new.mark
                && old.tunnel_interface == new.tunnel_interface
                && old.has_v4 == new.has_v4
                && old.has_v6 == new.has_v6
                && old.managed_interface == new.managed_interface
                && old.probe_sources == new.probe_sources
        });
        if unchanged {
            // Keep the last successfully-applied runtime until the replacement
            // attempt below has completed. A failed re-apply must not turn an
            // existing resource into untracked state.
            retained.push(old.clone());
            continue;
        }
        let delete_interface = !desired_interfaces.contains(old.tunnel_interface.as_str());
        if let Err(error) = cleanup_runtime_profile(executor, old, delete_interface) {
            outcome
                .global_errors
                .push(format!("cleanup profile {}: {error}", old.profile_id));
            retained.push(old.clone());
        }
    }
    for application in &prepared.applications {
        let runtime = desired_runtime
            .get(&application.profile.id)
            .expect("prepared runtime exists")
            .clone();
        let activation = configure_profile_routes(executor, application)
            .and_then(|_| verify_profile_health(executor, application));
        if let Err(error) = activation {
            // nftables is already fail-closed at this point. Roll back any route,
            // rule, or managed WireGuard interface created before the failure. If
            // rollback itself fails, retain the attempted runtime so a later
            // reconciliation can retry cleanup instead of orphaning host state.
            let cleanup_error = cleanup_runtime_profile(executor, &runtime, true).err();
            let message = match cleanup_error {
                Some(cleanup_error) => {
                    retained.retain(|old| old.profile_id != runtime.profile_id);
                    retained.push(runtime);
                    format!("{error}; rollback failed: {cleanup_error}")
                }
                None => error,
            };
            outcome
                .profile_errors
                .insert(application.profile.id.clone(), message);
            continue;
        }
        retained.retain(|old| old.profile_id != runtime.profile_id);
        retained.push(runtime);
    }
    if !prepared.nft_bindings.is_empty() {
        let mut active_bindings = prepared.nft_bindings.clone();
        for binding in &mut active_bindings {
            if outcome.profile_errors.contains_key(&binding.profile_id) {
                binding.quarantine = true;
                binding.tunnel_interface = None;
                binding.effective_mark = None;
            }
        }
        let active_script = build_nft_script(&active_bindings, true, false);
        if let Err(error) = apply_nft_script(executor, &active_script) {
            outcome
                .global_errors
                .push(format!("activate egress nft policy: {error}"));
            // Retain the already-installed quarantine barrier. A best-effort
            // re-apply handles executors/filesystems that partially replaced it.
            let emergency = build_nft_script(&prepared.nft_bindings, true, true);
            if let Err(emergency_error) = apply_nft_script(executor, &emergency) {
                outcome.global_errors.push(format!(
                    "emergency fail-closed nft transaction failed: {emergency_error}"
                ));
            }
        }
    }
    outcome.runtime = retained;
    outcome
}

fn outcome_for_dry_run() -> ApplyOutcome {
    ApplyOutcome {
        fail_closed: false,
        nft_replaced: false,
        profile_errors: HashMap::new(),
        global_errors: Vec::new(),
        runtime: Vec::new(),
        counters: HashMap::new(),
    }
}

fn counter_value(counters: &HashMap<String, u64>, direction: char, instance_id: &str) -> i64 {
    (*counters
        .get(&counter_name(direction, instance_id))
        .unwrap_or(&0))
    .min(i64::MAX as u64) as i64
}

fn persist_reconcile(
    conn: &rusqlite::Connection,
    prepared: &PreparedReconcile,
    outcome: &ApplyOutcome,
    profile_warnings: &HashMap<String, String>,
    apply_requested: bool,
    apply_enabled: bool,
) -> Result<(), ApiError> {
    let now = now_ts();
    let tx = conn
        .unchecked_transaction()
        .map_err(|e| ApiError::internal(format!("begin egress state transaction error: {e}")))?;
    let mut profile_states: HashMap<String, (String, String)> = HashMap::new();
    for plan in &prepared.plans {
        let mut status = plan.status.clone();
        let mut error = plan.error.clone().unwrap_or_default();
        if apply_requested {
            if !apply_enabled {
                status = "blocked".to_string();
                error = format!("{APPLY_ENV}=true is required");
            } else if !outcome.global_errors.is_empty() {
                status = "blocked".to_string();
                error = outcome.global_errors.join("; ");
            } else if outcome.profile_errors.contains_key(&plan.profile_id) {
                status = "blocked".to_string();
                error = outcome
                    .profile_errors
                    .get(&plan.profile_id)
                    .cloned()
                    .unwrap_or_default();
            } else if let Some(warning) = profile_warnings.get(&plan.profile_id) {
                status = "degraded".to_string();
                error = warning.clone();
            } else if status == "planned" {
                status = "applied".to_string();
            }
        }
        if plan.status == "disabled" {
            status = "disabled".to_string();
            error.clear();
        }
        let enforcement = if plan.status == "disabled" || !apply_enabled {
            None
        } else {
            Some(outcome.fail_closed as i64)
        };
        tx.execute("UPDATE egress_bindings SET state=?1,last_error=?2,updated_at=?3,traffic_bytes_in=traffic_bytes_in+?4,traffic_bytes_out=traffic_bytes_out+?5,traffic_bytes_dropped=traffic_bytes_dropped+?6,fail_closed_enforced=CASE WHEN ?7 THEN ?8 ELSE fail_closed_enforced END WHERE instance_id=?9",
			params![&status, &error, now, if outcome.nft_replaced { counter_value(&outcome.counters, 'i', &plan.instance_id) } else { 0 }, if outcome.nft_replaced { counter_value(&outcome.counters, 'o', &plan.instance_id) } else { 0 }, if outcome.nft_replaced { counter_value(&outcome.counters, 'd', &plan.instance_id) } else { 0 }, apply_requested, enforcement, &plan.instance_id])
            .map_err(|e| ApiError::internal(format!("update egress binding state error: {e}")))?;
        let entry = profile_states
            .entry(plan.profile_id.clone())
            .or_insert_with(|| ("disabled".to_string(), String::new()));
        let rank = |value: &str| match value {
            "blocked" => 5,
            "degraded" => 4,
            "applied" => 3,
            "planned" | "pending" => 2,
            "disabled" => 1,
            _ => 0,
        };
        if rank(&status) >= rank(&entry.0) {
            entry.0 = status;
            entry.1 = error;
        }
    }
    for (profile_id, (status, error)) in profile_states {
        tx.execute(
            "UPDATE egress_profiles SET status=?1,last_error=?2,updated_at=?3 WHERE id=?4",
            params![status, error, now, profile_id],
        )
        .map_err(|e| ApiError::internal(format!("update egress profile state error: {e}")))?;
    }
    if apply_requested && apply_enabled && outcome.nft_replaced {
        tx.execute("DELETE FROM egress_runtime_profiles", [])
            .map_err(|e| ApiError::internal(format!("replace egress runtime state error: {e}")))?;
        for runtime in &outcome.runtime {
            tx.execute("INSERT INTO egress_runtime_profiles (profile_id,route_table,mark,tunnel_interface,has_v4,has_v6,managed_interface,probe_sources_json,updated_at) VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9)",
                params![&runtime.profile_id, runtime.route_table, runtime.mark, &runtime.tunnel_interface, runtime.has_v4 as i64, runtime.has_v6 as i64, runtime.managed_interface as i64, wg_json(&runtime.probe_sources), now])
                .map_err(|e| ApiError::internal(format!("write egress runtime state error: {e}")))?;
        }
    }
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit egress state transaction error: {e}")))
}

async fn reconcile_state(
    state: AppState,
    apply_requested: bool,
) -> Result<ReconcileResponse, ApiError> {
    let _guard = reconcile_lock().lock().await;
    reconcile_state_locked(state, apply_requested, true).await
}

async fn reconcile_state_locked(
    state: AppState,
    apply_requested: bool,
    persist_exact_sources: bool,
) -> Result<ReconcileResponse, ApiError> {
    let mut capabilities = detect_capabilities();
    if apply_requested
        && capabilities.auto_install_enabled
        && !capabilities.missing_dependencies.is_empty()
    {
        let package_set = {
            let conn = state.conn.lock().await;
            let (profiles, _) = load_desired(&conn)?;
            if profiles
                .iter()
                .any(|row| row.profile.enabled && row.profile.tunnel_type == "wireguard")
            {
                "wireguard"
            } else {
                "native"
            }
        };
        let package_set = package_set.to_string();
        match tokio::task::spawn_blocking(move || install_dependency_set(&package_set)).await {
            Ok(Ok(_)) => capabilities = detect_capabilities(),
            Ok(Err(error)) => capabilities.reasons.push(error.message),
            Err(error) => capabilities
                .reasons
                .push(format!("dependency installer task failed: {error}")),
        }
    }
    let (profiles, bindings, previous) = {
        let conn = state.conn.lock().await;
        let (profiles, bindings) = load_desired(&conn)?;
        let previous = load_runtime(&conn)?;
        (profiles, bindings, previous)
    };
    if apply_requested && capabilities.apply_enabled {
        let kernel_errors = ensure_kernel_prerequisites(&profiles, &bindings);
        capabilities = detect_capabilities();
        capabilities.reasons.extend(kernel_errors);
    }
    let inventory = host_inventory(&SystemExecutor);
    let prepared = prepare_reconcile(
        &profiles,
        &bindings,
        &capabilities,
        &inventory,
        apply_requested,
    );
    let mut outcome = if apply_requested && capabilities.apply_enabled {
        let prepared_for_apply = prepared.clone();
        tokio::task::spawn_blocking(move || {
            apply_prepared(&SystemExecutor, &prepared_for_apply, &previous)
        })
        .await
        .map_err(|e| ApiError::internal(format!("egress apply task failed: {e}")))?
    } else {
        outcome_for_dry_run()
    };
    if outcome.nft_replaced && outcome.profile_errors.is_empty() && outcome.global_errors.is_empty()
    {
        if let Err(error) = clear_boot_quarantine(&SystemExecutor) {
            outcome.global_errors.push(error);
        }
    }
    // Health failures are hard profile errors inside apply_prepared. A profile
    // is never activated merely because its interface exists.
    let profile_warnings: HashMap<String, String> = HashMap::new();
    {
        let conn = state.conn.lock().await;
        persist_reconcile(
            &conn,
            &prepared,
            &outcome,
            &profile_warnings,
            apply_requested,
            capabilities.apply_enabled,
        )?;
        if persist_exact_sources {
            write_managed_sources(&conn)?;
        }
    }
    let mut plans = prepared.plans;
    if apply_requested {
        for plan in &mut plans {
            if plan.status == "disabled" {
                continue;
            }
            if !capabilities.apply_enabled {
                plan.status = "blocked".to_string();
                plan.error = Some(format!("{APPLY_ENV}=true is required"));
            } else if let Some(error) = outcome.profile_errors.get(&plan.profile_id) {
                plan.status = "blocked".to_string();
                plan.error = Some(error.clone());
            } else if let Some(warning) = profile_warnings.get(&plan.profile_id) {
                plan.status = "degraded".to_string();
                plan.error = Some(warning.clone());
            } else if !outcome.global_errors.is_empty() {
                plan.status = "blocked".to_string();
                plan.error = Some(outcome.global_errors.join("; "));
            } else if plan.status == "planned" {
                plan.status = "applied".to_string();
            }
        }
    }
    let all_ok = plans
        .iter()
        .all(|plan| matches!(plan.status.as_str(), "applied" | "disabled"));
    let applied = apply_requested
        && capabilities.apply_enabled
        && outcome.nft_replaced
        && all_ok
        && outcome.global_errors.is_empty();
    let has_enabled = bindings.iter().any(|row| row.binding.enabled);
    let fail_closed = if apply_requested && capabilities.apply_enabled {
        outcome.fail_closed
    } else {
        !has_enabled
    };
    info!(
        apply_requested,
        applied,
        fail_closed,
        plans = plans.len(),
        errors = outcome.global_errors.len(),
        "egress state reconciled"
    );
    Ok(ReconcileResponse {
        applied,
        fail_closed,
        capabilities,
        plans,
        errors: outcome.global_errors,
    })
}

/// Reconcile persisted state after the agent has restarted.  This is called by
/// `main` only after the SQLite schema and AppState are ready.
pub async fn reconcile_startup(state: AppState) {
    let startup_networks = {
        let conn = state.conn.lock().await;
        let mut networks = Vec::new();
        match conn.prepare("SELECT sources_json FROM egress_bindings WHERE enabled=1") {
            Ok(mut stmt) => match stmt.query_map([], |row| row.get::<_, String>(0)) {
                Ok(rows) => {
                    for row in rows {
                        let Ok(encoded) = row else {
                            warn!("startup egress quarantine source row is unreadable");
                            return;
                        };
                        let Ok(values) = serde_json::from_str::<Vec<String>>(&encoded) else {
                            warn!("startup egress quarantine source list is corrupt");
                            return;
                        };
                        for value in values {
                            let Ok(network) = parse_network(&value, "startup managed source", true)
                            else {
                                warn!("startup egress quarantine contains an invalid source");
                                return;
                            };
                            networks.push(network);
                        }
                    }
                    networks
                }
                Err(error) => {
                    warn!(%error, "startup egress quarantine query failed");
                    return;
                }
            },
            Err(error) => {
                warn!(%error, "startup egress quarantine prepare failed");
                return;
            }
        }
    };
    if !startup_networks.is_empty() {
        if let Err(error) = install_staging_quarantine(&SystemExecutor, &startup_networks) {
            warn!(%error, "startup egress quarantine failed; reconciliation is not allowed to activate traffic");
            return;
        }
    }
    if !env_enabled(APPLY_ENV) {
        warn!("startup egress apply guard is disabled; managed sources remain quarantined");
        return;
    }
    if let Err(error) = reconcile_state(state, true).await {
        warn!(error = %error.message, "startup egress reconciliation failed");
    }
}

pub async fn reconcile(
    State(state): State<AppState>,
    Json(req): Json<ReconcileRequest>,
) -> Result<Json<ReconcileResponse>, ApiError> {
    Ok(Json(
        reconcile_state(state, req.apply.unwrap_or(false)).await?,
    ))
}
