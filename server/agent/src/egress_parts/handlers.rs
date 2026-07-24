pub async fn list_profiles(
    State(state): State<AppState>,
) -> Result<Json<ListProfilesResponse>, ApiError> {
    let mut profiles = {
        let conn = state.conn.lock().await;
        let mut stmt = conn
            .prepare(&format!(
                "SELECT {PROFILE_SELECT} FROM egress_profiles ORDER BY id"
            ))
            .map_err(|e| ApiError::internal(format!("prepare egress profile list error: {e}")))?;
        let rows = stmt
            .query_map([], read_profile)
            .map_err(|e| ApiError::internal(format!("query egress profile list error: {e}")))?;
        let mut result = Vec::new();
        for row in rows {
            result.push(
                row.map_err(|e| ApiError::internal(format!("read egress profile error: {e}")))?,
            );
        }
        result
    };
    enrich_profiles(&mut profiles, &host_inventory(&SystemExecutor));
    let total = profiles.len();
    Ok(Json(ListProfilesResponse { profiles, total }))
}

pub async fn put_profile(
    State(state): State<AppState>,
    Json(req): Json<EgressProfileRequest>,
) -> Result<Json<EgressProfile>, ApiError> {
    let (mut profile, requested_wg) = profile_from_request(req)?;
    let _guard = reconcile_lock().lock().await;
    let existing = {
        let conn = state.conn.lock().await;
        conn.query_row(
            &format!("SELECT {PROFILE_SELECT} FROM egress_profiles WHERE id = ?1"),
            params![&profile.id],
            read_profile,
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("read existing egress profile error: {e}")))?
    };
    if requested_wg.is_none() && profile.tunnel_type == "wireguard" {
        profile.wireguard = existing.as_ref().and_then(|value| value.wireguard.clone());
    }
    if let Some(config) = requested_wg.as_ref() {
        let mut status = config.status.clone();
        let existing_status = existing.as_ref().and_then(|value| value.wireguard.as_ref());
        if let Some(private_key) = config.private_key.as_deref() {
            atomic_write_secret(&secret_path(&profile.id, false)?, private_key)?;
            status.private_key_configured = true;
        } else if status.managed {
            status.private_key_configured = existing_status
                .is_some_and(|value| value.private_key_configured)
                || secret_path(&profile.id, false)?.is_file();
        }
        if let Some(preshared_key) = config.preshared_key.as_deref() {
            atomic_write_secret(&secret_path(&profile.id, true)?, preshared_key)?;
            status.preshared_key_configured = true;
        } else if status.managed {
            status.preshared_key_configured = existing_status
                .is_some_and(|value| value.preshared_key_configured)
                || secret_path(&profile.id, true)?.is_file();
        }
        if !status.managed {
            status.private_key_configured = false;
            status.preshared_key_configured = false;
        }
        profile.wireguard = Some(status);
    }
    {
        let conn = state.conn.lock().await;
        upsert_profile_row(&conn, &profile)?;
    }
    if profile.tunnel_type != "wireguard"
        || requested_wg
            .as_ref()
            .is_some_and(|config| !config.status.managed)
    {
        remove_profile_secrets(&profile.id);
    }
    profile = {
        let conn = state.conn.lock().await;
        conn.query_row(
            &format!("SELECT {PROFILE_SELECT} FROM egress_profiles WHERE id=?1"),
            params![&profile.id],
            read_profile,
        )
        .map_err(|e| ApiError::internal(format!("read saved egress profile error: {e}")))?
    };
    enrich_profiles(
        std::slice::from_mut(&mut profile),
        &host_inventory(&SystemExecutor),
    );
    Ok(Json(profile))
}

pub async fn delete_profile(
    State(state): State<AppState>,
    Json(req): Json<EgressProfileDeleteRequest>,
) -> Result<Json<Value>, ApiError> {
    let id = validate_id(&req.id, "id")?;
    let _guard = reconcile_lock().lock().await;
    let deleted = {
        let conn = state.conn.lock().await;
        let binding_count: i64 = conn
            .query_row(
                "SELECT COUNT(*) FROM egress_bindings WHERE profile_id = ?1",
                params![&id],
                |row| row.get(0),
            )
            .map_err(|e| ApiError::internal(format!("check egress profile bindings error: {e}")))?;
        if binding_count > 0 {
            return Err(ApiError::bad_request(
                "delete all profile bindings before deleting the egress profile",
            ));
        }
        conn.execute("DELETE FROM egress_profiles WHERE id = ?1", params![&id])
            .map_err(|e| ApiError::internal(format!("delete egress profile error: {e}")))?
    };
    drop(_guard);
    if deleted > 0 {
        let reconciled = reconcile_state(state.clone(), true).await?;
        if !reconciled.applied {
            return Err(ApiError::internal(format!(
                "egress profile was removed from desired state but host cleanup is incomplete: {}",
                reconciled.errors.join("; ")
            )));
        }
        remove_profile_secrets(&id);
    } else {
        // A previous deletion may have removed desired state before host cleanup
        // completed. Repeating DELETE is an idempotent cleanup retry.
        let reconciled = reconcile_state(state.clone(), true).await?;
        if reconciled.applied {
            remove_profile_secrets(&id);
        }
    }
    Ok(Json(
        serde_json::json!({ "id": id, "deleted": deleted > 0 }),
    ))
}

pub async fn list_bindings(
    State(state): State<AppState>,
) -> Result<Json<ListBindingsResponse>, ApiError> {
    let mut bindings = {
        let conn = state.conn.lock().await;
        let mut stmt = conn
            .prepare(&format!(
                "SELECT {BINDING_SELECT} FROM egress_bindings ORDER BY instance_id"
            ))
            .map_err(|e| ApiError::internal(format!("prepare egress binding list error: {e}")))?;
        let rows = stmt
            .query_map([], read_binding)
            .map_err(|e| ApiError::internal(format!("query egress binding list error: {e}")))?;
        let mut result = Vec::new();
        for row in rows {
            result.push(
                row.map_err(|e| ApiError::internal(format!("read egress binding error: {e}")))?,
            );
        }
        result
    };
    add_live_counters(&SystemExecutor, &mut bindings);
    let total = bindings.len();
    Ok(Json(ListBindingsResponse { bindings, total }))
}

pub async fn put_binding(
    State(state): State<AppState>,
    Json(req): Json<EgressBindingRequest>,
) -> Result<Json<EgressBinding>, ApiError> {
    let row = binding_from_request(req)?;
    // Serialize the staging barrier with full reconcile. The SQLite write and
    // the nft transaction are intentionally kept in one operation: a caller
    // must never observe a successful binding PUT before its source is blocked.
    let _guard = reconcile_lock().lock().await;
    let path = managed_sources_path()?;
    let binding = {
        let conn = state.conn.lock().await;
        persist_binding_with_quarantine(&conn, &SystemExecutor, row, &path)?
    };
    Ok(Json(binding))
}

pub async fn replace_state(
    State(state): State<AppState>,
    Json(req): Json<ReplaceStateRequest>,
) -> Result<Json<ReplaceStateResponse>, ApiError> {
    // Parse and cross-reference the complete request before touching nft,
    // files, secrets, or SQLite. Replacement is authoritative and therefore
    // must never silently drop a malformed item.
    let normalized = normalize_replace_state_request(req)?;
    let profile_ids = normalized
        .profiles
        .iter()
        .map(|(profile, _)| profile.id.clone())
        .collect::<HashSet<_>>();
    let bindings = normalized.bindings;
    let apply = normalized.apply;
    let _guard = reconcile_lock().lock().await;
    let (existing_profiles, old_networks) = {
        let conn = state.conn.lock().await;
        let (profiles, bindings) = load_desired(&conn)?;
        let existing_profiles = profiles
            .into_iter()
            .map(|row| (row.profile.id.clone(), row.profile))
            .collect::<HashMap<_, _>>();
        let old_networks = bindings
            .into_iter()
            .filter(|row| row.binding.enabled)
            .flat_map(|row| row.networks)
            .collect::<Vec<_>>();
        (existing_profiles, old_networks)
    };

    let mut profiles = Vec::with_capacity(normalized.profiles.len());
    let mut secret_updates = Vec::new();
    let mut remove_secret_ids: HashSet<String> = existing_profiles
        .keys()
        .filter(|id| !profile_ids.contains(*id))
        .cloned()
        .collect();
    for (mut profile, requested_wireguard) in normalized.profiles {
        let existing = existing_profiles.get(&profile.id);
        let (mut updates, remove_secrets) =
            prepare_profile_storage(&mut profile, requested_wireguard.as_ref(), existing)?;
        secret_updates.append(&mut updates);
        if remove_secrets {
            remove_secret_ids.insert(profile.id.clone());
        }
        profiles.push(profile);
    }

    let new_networks = bindings
        .iter()
        .filter(|row| row.binding.enabled)
        .flat_map(|row| row.networks.iter().cloned())
        .collect::<Vec<_>>();
    let mut quarantine = old_networks.clone();
    quarantine.extend(new_networks.iter().cloned());
    quarantine.sort_by_key(|network| {
        (
            matches!(network.family(), Family::V6),
            network.canonical_bits(),
            network.prefix,
        )
    });
    quarantine.dedup();
    install_staging_quarantine(&SystemExecutor, &quarantine).map_err(ApiError::internal)?;
    let path = managed_sources_path()?;
    write_managed_networks_at(&path, &quarantine)?;

    let backups = apply_secret_updates(&secret_updates)?;
    let replace_result = {
        let conn = state.conn.lock().await;
        replace_desired_state_sql(&conn, &profiles, &bindings)
    };
    if let Err(error) = replace_result {
        rollback_secret_updates(&backups);
        return Err(error);
    }
    for id in remove_secret_ids {
        remove_profile_secrets(&id);
    }

    let reconcile_result = reconcile_state_locked(state.clone(), apply, false).await;
    let reconciled = match reconcile_result {
        Ok(response) => response,
        Err(error) => {
            // The database now contains the authoritative replacement. Keep
            // both retired and new identities durable until a retry proves the
            // old data plane has been removed.
            write_managed_networks_at(&path, &quarantine)?;
            return Err(error);
        }
    };
    if apply && reconciled.applied && reconciled.errors.is_empty() {
        write_managed_networks_at(&path, &new_networks)?;
    } else {
        write_managed_networks_at(&path, &quarantine)?;
    }
    Ok(Json(ReplaceStateResponse {
        profile_count: profiles.len(),
        binding_count: bindings.len(),
        reconcile: reconciled,
    }))
}

fn binding_networks(binding: &EgressBinding) -> Result<Vec<IpNetwork>, ApiError> {
    binding
        .sources
        .iter()
        .map(|source| parse_network(source, "stored source", true))
        .collect()
}

/// Stage a binding behind the high-priority boot/staging guard before making
/// it visible in SQLite. Existing source identities are included when updating
/// a binding so an address transition cannot briefly fall through to the host
/// default route. Host commands and durable file I/O happen before the short
/// SQL-only transaction.
fn persist_binding_with_quarantine(
    conn: &rusqlite::Connection,
    executor: &impl CommandExecutor,
    row: BindingRow,
    managed_sources: &Path,
) -> Result<EgressBinding, ApiError> {
    let profile_exists: Option<i64> = conn
        .query_row(
            "SELECT 1 FROM egress_profiles WHERE id = ?1",
            params![&row.binding.profile_id],
            |value| value.get(0),
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("check egress profile error: {e}")))?;
    if profile_exists.is_none() {
        return Err(ApiError::bad_request("egress profile does not exist"));
    }

    let previous = conn
        .query_row(
            &format!("SELECT {BINDING_SELECT} FROM egress_bindings WHERE instance_id=?1"),
            params![&row.binding.instance_id],
            read_binding,
        )
        .optional()
        .map_err(|e| ApiError::internal(format!("read existing egress binding error: {e}")))?;
    let mut quarantine = if row.binding.enabled {
        row.networks.clone()
    } else {
        Vec::new()
    };
    if let Some(previous) = previous.as_ref().filter(|binding| binding.enabled) {
        quarantine.extend(binding_networks(previous)?);
    }
    quarantine.sort_by_key(|network| {
        (
            matches!(network.family(), Family::V6),
            network.canonical_bits(),
            network.prefix,
        )
    });
    quarantine.dedup();
    install_staging_quarantine(executor, &quarantine).map_err(ApiError::internal)?;
    let extra = if row.binding.enabled {
        row.networks.as_slice()
    } else {
        &[]
    };
    write_managed_sources_at_with_extra(conn, managed_sources, extra)?;

    let tx = conn
        .unchecked_transaction()
        .map_err(|e| ApiError::internal(format!("begin egress binding transaction error: {e}")))?;
    upsert_binding_row(&tx, &row)?;
    let binding = tx
        .query_row(
            &format!("SELECT {BINDING_SELECT} FROM egress_bindings WHERE instance_id=?1"),
            params![&row.binding.instance_id],
            read_binding,
        )
        .map_err(|e| ApiError::internal(format!("read saved egress binding error: {e}")))?;
    tx.commit()
        .map_err(|e| ApiError::internal(format!("commit egress binding transaction error: {e}")))?;
    Ok(binding)
}

pub async fn delete_binding(
    State(state): State<AppState>,
    Json(req): Json<EgressBindingDeleteRequest>,
) -> Result<Json<Value>, ApiError> {
    let instance_id = validate_id(&req.instance_id, "instance_id")?;
    let _guard = reconcile_lock().lock().await;
    let (deleted, retired_networks) = {
        let conn = state.conn.lock().await;
        let previous = conn
            .query_row(
                &format!("SELECT {BINDING_SELECT} FROM egress_bindings WHERE instance_id=?1"),
                params![&instance_id],
                read_binding,
            )
            .optional()
            .map_err(|e| {
                ApiError::internal(format!("read egress binding for deletion error: {e}"))
            })?;
        let retired_networks = previous
            .as_ref()
            .filter(|binding| binding.enabled)
            .map(binding_networks)
            .transpose()?
            .unwrap_or_default();
        install_staging_quarantine(&SystemExecutor, &retired_networks)
            .map_err(ApiError::internal)?;
        let deleted = conn
            .execute(
                "DELETE FROM egress_bindings WHERE instance_id = ?1",
                params![&instance_id],
            )
            .map_err(|e| ApiError::internal(format!("delete egress binding error: {e}")))?;
        (deleted, retired_networks)
    };
    drop(_guard);
    if deleted > 0 {
        let reconcile_result = reconcile_state(state.clone(), true).await;
        let cleanup_complete = reconcile_result
            .as_ref()
            .is_ok_and(|response| response.applied);
        if !cleanup_complete {
            let conn = state.conn.lock().await;
            write_managed_sources_with_extra(&conn, &retired_networks)?;
            return match reconcile_result {
                Ok(response) => Err(ApiError::internal(format!(
                    "egress binding was removed from desired state but host cleanup is incomplete; retired sources remain quarantined: {}",
                    response.errors.join("; ")
                ))),
                Err(error) => Err(ApiError::internal(format!(
                    "egress binding was removed from desired state but host cleanup failed; retired sources remain quarantined: {}",
                    error.message
                ))),
            };
        }
    }
    Ok(Json(
        serde_json::json!({ "instance_id": instance_id, "deleted": deleted > 0 }),
    ))
}

