#!/bin/bash
# Module 11: Monitoring & Agent
# Dependencies: 09_providers (PROVIDER_ID)

run_module_11() {
    report_add_section "11 - Monitoring & Agent"
    local group="monitoring"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider"
        return 1
    fi

    # -- Monitoring config --
    test_api "Get monitoring config" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/config" "200" "" "$group"
    test_api "Update monitoring config" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/config" "200" \
        '{"monitoring_mode":"agent","collect_interval":60,"resource_collect_interval":30}' "$group"

    # -- Deploy agent --
    local da; da=$(test_api_retry "Deploy agent" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent" "200" \
        '{}' 3 15 "$group")
    sleep 15

    # -- Agent status --
    test_api_retry "Agent status" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/status" "200" \
        '' 3 10 "$group"

    # -- Provider monitors --
    test_api "Provider monitors" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/monitors" "200" "" "$group"

    # -- Agent monitors list --
    test_api "Agent monitors list" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent-monitors" "200" "" "$group"

    # -- Resource summary --
    test_api "Resource summary" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/resources" "200" "" "$group"

    # -- Sync monitors --
    test_api "Sync monitors" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/sync" "200" '{}' "$group"

    # -- Clear monitor data --
    test_api "Clear monitors" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/clear" "200" "" "$group"

    # -- Uninstall agent --
    test_api "Uninstall agent" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent" "200" "" "$group"

    # -- Status after uninstall --
    test_api "Status after uninstall" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/status" "200" "" "$group"
}
