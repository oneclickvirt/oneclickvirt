#!/bin/bash
# Module 23: Instance Discovery & Import (non-clean node testing)
# Dependencies: 09_providers (PROVIDER_ID), node_manager (WORKER_IP)
# This tests the critical requirement: discovering and importing existing instances
# on nodes that are NOT clean (already have containers/VMs running).

run_module_23() {
    report_add_section "23 - Discovery & Import"
    local group="discovery"

    if [[ -z "$PROVIDER_ID" || -z "$ADMIN_TOKEN" ]]; then
        chain_break "$group" "No provider or admin token"
        return 1
    fi

    # Provider录入后的健康检查和首次非纯净节点同步现在都是持久化后台任务。
    # 等待同一Provider队列稳定后再断言导入结果，避免依赖裸goroutine时序。
    if ! wait_provider_active_tasks_idle "$PROVIDER_ID" "provider ${PROVIDER_ID} initial discovery" "$ADMIN_TOKEN" 600 5; then
        chain_break "$group" "Initial provider background tasks did not settle"
        return 1
    fi

    # ---- Check if dirty node preparation was done ----
    # The run_env_test.sh should have called prepare_dirty_node() before this runs,
    # which creates pre-existing containers/instances on the worker node.

    # ---- Discover existing instances on provider ----
    local discover_resp; discover_resp=$(test_api "Discover instances" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200" '' "$group" "$ADMIN_TOKEN")
    test_api_json_value "Discovered identities are normalized" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200" \
        '[.data.discoveredInstances[]? | select((.uuid // "") == "" or (.providerInstanceId // "") == "" or ((.instanceType != "vm") and (.instanceType != "container")))] | length' "0" \
        '' "$group" "$ADMIN_TOKEN"
    if [[ "${DIRTY_NODE_CONTAINER_EXPECTED:-false}" == "true" ]]; then
        if [[ "${DIRTY_NODE_CONTAINER_READY:-false}" == "true" && -n "${DIRTY_NODE_CONTAINER_NAME:-}" ]]; then
            test_api_json_value "Discover exact pre-existing container" "POST" \
                "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200" \
                '.data.discoveredInstances | map(select(.name == "'"${DIRTY_NODE_CONTAINER_NAME}"'" and .instanceType == "container")) | length' "1" \
                '' "$group" "$ADMIN_TOKEN"
        else
            record_skip_result "Discover exact pre-existing container" "POST" \
                "/api/v1/admin/providers/${PROVIDER_ID}/discover" \
                "The deterministic container fixture was not available" "$group"
        fi
    fi
    if [[ "${DIRTY_NODE_VM_EXPECTED:-false}" == "true" ]]; then
        if [[ "${DIRTY_NODE_VM_READY:-false}" == "true" ]]; then
            if [[ "$ENV_TYPE" == "proxmoxve" ]]; then
                test_api_json_value "Discover exact pre-existing PVE VM" "POST" \
                    "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200" \
                    '.data.discoveredInstances | map(select(.providerInstanceId == "'"${DIRTY_NODE_VM_PROVIDER_ID:-990}"'" and .instanceType == "vm")) | length' "1" \
                    '' "$group" "$ADMIN_TOKEN"
                test_api_json_value "Auto-imported PVE VM keeps VMID" "GET" \
                    "/api/v1/admin/instances?page=1&pageSize=50" "200" \
                    '[.. | objects | select(.providerVmId? == "'"${DIRTY_NODE_VM_PROVIDER_ID:-990}"'" and .isImported? == true)] | length' "1" \
                    '' "$group" "$ADMIN_TOKEN"
            elif [[ -n "${DIRTY_NODE_VM_NAME:-}" ]]; then
                test_api_json_value "Discover exact pre-existing VM" "POST" \
                    "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200" \
                    '.data.discoveredInstances | map(select(.name == "'"${DIRTY_NODE_VM_NAME}"'" and .instanceType == "vm")) | length' "1" \
                    '' "$group" "$ADMIN_TOKEN"
            fi
        else
            record_skip_result "Discover exact pre-existing VM" "POST" \
                "/api/v1/admin/providers/${PROVIDER_ID}/discover" \
                "The deterministic VM fixture was not available" "$group"
        fi
    fi

    # ---- Get orphaned instances (instances on node but not in DB) ----
    local orphaned_resp; orphaned_resp=$(test_api "Get orphaned instances" "GET" \
        "/api/v1/admin/providers/${PROVIDER_ID}/orphaned" "200" '' "$group" "$ADMIN_TOKEN")

    # ---- Sync check (compare DB state vs actual node state) ----
    test_api "Sync check" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/sync-check" "200" \
        '' "$group" "$ADMIN_TOKEN"

    # ---- Import discovered instances ----
    # Import by the provider discovery UUID. Names are display values and may be
    # duplicated or synthesized for unnamed PVE guests.
    local instance_uuids
    if [[ "$ENV_TYPE" == "proxmoxve" && "${DIRTY_NODE_VM_READY:-false}" == "true" ]]; then
        instance_uuids=$(echo "$orphaned_resp" | jq -r \
            --arg provider_id "${DIRTY_NODE_VM_PROVIDER_ID:-990}" \
            '.data.orphanedInstances[]? | select(.providerInstanceId == $provider_id and .instanceType == "vm") | .uuid' \
            2>/dev/null | head -1 || true)
    elif [[ "${DIRTY_NODE_CONTAINER_READY:-false}" == "true" || "${DIRTY_NODE_VM_READY:-false}" == "true" ]]; then
        instance_uuids=$(echo "$orphaned_resp" | jq -r \
            --arg container_name "${DIRTY_NODE_CONTAINER_NAME:-}" \
            --arg vm_name "${DIRTY_NODE_VM_NAME:-}" \
            '.data.orphanedInstances[]? | select((($container_name != "") and .name == $container_name and .instanceType == "container") or (($vm_name != "") and .name == $vm_name and .instanceType == "vm")) | .uuid' \
            2>/dev/null | head -3 || true)
    else
        instance_uuids=""
    fi

    if [[ -n "$instance_uuids" ]]; then
        local first_uuid; first_uuid=$(echo "$instance_uuids" | head -1)
        test_api_json_value "Import discovered instance" "POST" \
            "/api/v1/admin/providers/${PROVIDER_ID}/import" "200" \
            '.data.successCount' "1" \
            '{"instanceUuids":["'"$first_uuid"'"]}' "$group" "$ADMIN_TOKEN"

        # ---- Verify imported instance appears in instance list ----
        test_api "List after import" "GET" "/api/v1/admin/instances?page=1&pageSize=50" "200" \
            '' "$group" "$ADMIN_TOKEN"
        # ---- Import again (should handle gracefully) ----
        test_api_json_value "Re-import same instance" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "200" \
            '.data.skippedCount' "1" '{"instanceUuids":["'"$first_uuid"'"]}' "$group" "$ADMIN_TOKEN"
    else
        # Auto-import may already have claimed every dirty-node fixture. Verify
        # that explicitly importing an already-managed discovery is idempotent.
        local managed_uuid
        if [[ "$ENV_TYPE" == "proxmoxve" && "${DIRTY_NODE_VM_READY:-false}" == "true" ]]; then
            managed_uuid=$(echo "$discover_resp" | jq -r \
                --arg provider_id "${DIRTY_NODE_VM_PROVIDER_ID:-990}" \
                '.data.discoveredInstances[]? | select(.providerInstanceId == $provider_id and .instanceType == "vm") | .uuid' \
                2>/dev/null | head -1 || true)
        else
            managed_uuid=$(echo "$discover_resp" | jq -r \
                --arg container_name "${DIRTY_NODE_CONTAINER_NAME:-}" \
                --arg vm_name "${DIRTY_NODE_VM_NAME:-}" \
                '.data.discoveredInstances[]? | select((($container_name != "") and .name == $container_name and .instanceType == "container") or (($vm_name != "") and .name == $vm_name and .instanceType == "vm")) | .uuid' \
                2>/dev/null | head -1 || true)
        fi
        if [[ -n "$managed_uuid" ]]; then
            test_api_json_value "Import already-managed instance" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "200" \
                '.data.skippedCount' "1" '{"instanceUuids":["'"$managed_uuid"'"]}' "$group" "$ADMIN_TOKEN"
        else
            log_info "No discovered instances to import (worker may not have pre-existing instances)"
        fi
    fi

    if [[ "$ENV_TYPE" == "docker" || "$ENV_TYPE" == "podman" || "$ENV_TYPE" == "containerd" ]]; then
        test_api_json_value "Imported discovery data is redacted" "GET" \
            "/api/v1/admin/instances?page=1&pageSize=50" "200" \
            '[.. | strings | select(contains("dirty-node-secret"))] | length' "0" \
            '' "$group" "$ADMIN_TOKEN"
    fi

    # ---- Manual sync button backend: must queue a visible admin task ----
    local sync_task_resp sync_task_id sync_task_result
    sync_task_resp=$(test_api "Queue instance sync task" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/sync-instances" "200" '' "$group" "$ADMIN_TOKEN")
    sync_task_id=$(echo "$sync_task_resp" | jq -r '.data.id // empty' 2>/dev/null)
    if [[ -n "$sync_task_id" ]]; then
        sync_task_result=$(wait_task_complete "$SERVER_URL" "$sync_task_id" "$ADMIN_TOKEN" 600 5) || true
        record_task_terminal_result "Instance sync background" "GET" \
            "/api/v1/admin/tasks/${sync_task_id}" "$sync_task_result" "$group" || true
    else
        record_fail_result "Queue instance sync task" "POST" \
            "/api/v1/admin/providers/${PROVIDER_ID}/sync-instances" "task id" "missing" "$sync_task_resp" "$group"
    fi

    # ---- Import with empty list ----
    test_api "Import empty names" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "400" \
        '{"instanceUuids":[]}' "$group" "$ADMIN_TOKEN"

    # ---- Import nonexistent instance UUID ----
    test_api "Import nonexistent UUID" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "400" \
        '{"instanceUuids":["nonexistent_instance_xyz"]}' "$group" "$ADMIN_TOKEN"

    # ---- Discovery on nonexistent provider ----
    test_api "Discover bad provider" "POST" "/api/v1/admin/providers/99999/discover" "400|404" \
        '' "$group" "$ADMIN_TOKEN"

    # ---- Orphaned on nonexistent provider ----
    test_api "Orphaned bad provider" "GET" "/api/v1/admin/providers/99999/orphaned" "400|404" \
        '' "$group" "$ADMIN_TOKEN"

    # ---- Normal admin discovers on own provider ----
    if [[ -n "$NORMAL_ADMIN_TOKEN" ]]; then
        test_api "Normal admin discover" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200|400|403|500" \
            '' "$group" "$NORMAL_ADMIN_TOKEN"
    fi

    # ---- Post-import health check ----
    if ! ensure_provider_health_ready "$PROVIDER_ID" "$ADMIN_TOKEN" 0; then
        record_fail_result "Provider health after import" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/health-check-task" \
            "completed task" "failed" "Provider health task failed after import" "$group"
    fi

    # ---- Negative: Import with missing body ----
    test_api "Import missing body" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "400" \
        '{}' "$group" "$ADMIN_TOKEN"

    # ---- Negative: Sync check on nonexistent provider ----
    test_api "Sync check bad provider" "POST" "/api/v1/admin/providers/99999/sync-check" "400|404" \
        '' "$group" "$ADMIN_TOKEN"

    # ---- Negative: User cannot use discovery ----
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> discover (403)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/discover" "401|403" \
            '' "$group" "$USER_TOKEN"
        test_api "User -> orphaned (403)" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/orphaned" "401|403" \
            '' "$group" "$USER_TOKEN"
        test_api "User -> import (403)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "401|403" \
            '{"instanceUuids":["test"]}' "$group" "$USER_TOKEN"
    fi
}
