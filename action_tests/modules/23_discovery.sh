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

    # ---- Check if dirty node preparation was done ----
    # The run_env_test.sh should have called prepare_dirty_node() before this runs,
    # which creates pre-existing containers/instances on the worker node.

    # ---- Discover existing instances on provider (may fail if provider not connected) ----
    local discover_resp; discover_resp=$(test_api "Discover instances" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200|400" '' "$group" "$ADMIN_TOKEN")

    # ---- Get orphaned instances (instances on node but not in DB) ----
    local orphaned_resp; orphaned_resp=$(test_api "Get orphaned instances" "GET" \
        "/api/v1/admin/providers/${PROVIDER_ID}/orphaned" "200|400" '' "$group" "$ADMIN_TOKEN")

    # ---- Sync check (compare DB state vs actual node state) ----
    test_api "Sync check" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/sync-check" "200|400" \
        '' "$group" "$ADMIN_TOKEN"

    # ---- Import discovered instances ----
    # Parse discovered instance names from the response
    local instance_names; instance_names=$(echo "$discover_resp" | grep -o '"name":"[^"]*"' | head -3 | cut -d'"' -f4)

    if [[ -n "$instance_names" ]]; then
        local first_name; first_name=$(echo "$instance_names" | head -1)
        local import_resp; import_resp=$(test_api "Import discovered instance" "POST" \
            "/api/v1/admin/providers/${PROVIDER_ID}/import" "200" \
            '{"instanceUuids":["'"$first_name"'"]}' "$group" "$ADMIN_TOKEN")

        # ---- Verify imported instance appears in instance list ----
        test_api "List after import" "GET" "/api/v1/admin/instances?page=1&pageSize=50" "200" \
            '' "$group" "$ADMIN_TOKEN"

        # ---- Import again (should handle gracefully) ----
        test_api "Re-import same instance" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "200|400|409" \
            '{"instanceUuids":["'"$first_name"'"]}' "$group" "$ADMIN_TOKEN"
    else
        log_info "No discovered instances to import (worker may not have pre-existing instances)"
    fi

    # ---- Import with empty list ----
    test_api "Import empty names" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "400" \
        '{"instanceUuids":[]}' "$group" "$ADMIN_TOKEN"

    # ---- Import nonexistent instance UUID ----
    test_api "Import nonexistent name" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/import" "200|400" \
        '{"instanceUuids":["nonexistent_instance_xyz"]}' "$group" "$ADMIN_TOKEN"

    # ---- Discovery on nonexistent provider ----
    test_api "Discover bad provider" "POST" "/api/v1/admin/providers/99999/discover" "400|404" \
        '' "$group" "$ADMIN_TOKEN"

    # ---- Orphaned on nonexistent provider ----
    test_api "Orphaned bad provider" "GET" "/api/v1/admin/providers/99999/orphaned" "400|404" \
        '' "$group" "$ADMIN_TOKEN"

    # ---- Normal admin discovers on own provider ----
    if [[ -n "$NORMAL_ADMIN_TOKEN" ]]; then
        test_api "Normal admin discover" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200|400|403" \
            '' "$group" "$NORMAL_ADMIN_TOKEN"
    fi

    # ---- Post-import health check ----
    test_api "Provider health after import" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/health-check" "200" \
        '' "$group" "$ADMIN_TOKEN"

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
