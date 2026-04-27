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

    # -- Deploy agent (may fail if provider not fully connected) --
    local da; da=$(test_api_retry "Deploy agent" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent" "200|400|500" \
        '{}' 3 15 "$group")
    sleep 15

    # -- Agent status --
    test_api_retry "Agent status" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/status" "200|400" \
        '' 3 10 "$group"

    # -- Provider monitors --
    test_api "Provider monitors" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/monitors" "200" "" "$group"

    # -- Agent monitors list --
    test_api "Agent monitors list" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent-monitors" "200|400|404" "" "$group"

    # -- Resource summary --
    test_api "Resource summary" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/resources" "200|400" "" "$group"

    # -- Sync monitors (may fail if agent not installed) --
    test_api "Sync monitors" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/sync" "200|400" '{}' "$group"

    # -- Clear monitor data --
    test_api "Clear monitors" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/clear" "200|400" "" "$group"

    # -- Uninstall agent (may fail if not installed) --
    test_api "Uninstall agent" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent" "200|400" "" "$group"

    # -- Status after uninstall --
    test_api "Status after uninstall" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/status" "200|400" "" "$group"

    # -- Negative: monitoring on nonexistent provider --
    test_api "Monitoring config (nonexistent)" "GET" "/api/v1/admin/providers/99999/monitoring/config" "200|400|404" "" "$group"
    test_api "Deploy agent (nonexistent)" "POST" "/api/v1/admin/providers/99999/monitoring/agent" "400|404" '{}' "$group"

    # -- Negative: invalid monitoring config --
    test_api "Update monitoring config (invalid interval)" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/config" "400" \
        '{"monitoring_mode":"invalid_mode","collect_interval":-1}' "$group"

    # -- Negative: User cannot manage monitoring --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> monitoring config (403)" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/config" "401|403" "" "$group" "$USER_TOKEN"
        test_api "User -> deploy agent (403)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent" "401|403" '{}' "$group" "$USER_TOKEN"
    fi

    # -- Negative: Monitoring sync on nonexistent provider --
    test_api "Sync (nonexistent provider)" "POST" "/api/v1/admin/providers/99999/monitoring/sync" "400|404" '{}' "$group"

    # -- Negative: Clear on nonexistent provider --
    test_api "Clear (nonexistent provider)" "DELETE" "/api/v1/admin/providers/99999/monitoring/clear" "400|404" "" "$group"

    # -- Negative: Update config with extreme values --
    test_api "Config (extreme interval)" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/config" "400" \
        '{"collect_interval":0,"resource_collect_interval":0}' "$group"

    # -- System monitoring & audit --
    test_api "System monitoring dashboard" "GET" "/api/v1/admin/monitoring/system" "200" "" "$group"
    test_api "Audit logs" "GET" "/api/v1/admin/monitoring/audit-logs?page=1&pageSize=10" "200" "" "$group"

    # -- Performance metrics --
    test_api "Performance metrics" "GET" "/api/v1/admin/performance/metrics" "200" "" "$group"
    test_api "Performance history" "GET" "/api/v1/admin/performance/history" "200" "" "$group"

    # -- Log management --
    test_api "Log dates" "GET" "/api/v1/admin/logs/dates" "200" "" "$group"
    test_api "Log content" "GET" "/api/v1/admin/logs/content" "200|400" "" "$group"

    # -- User quota --
    if [[ -n "${USER_TOKEN:-}" ]]; then
        local _uid; _uid=$(curl -s --max-time 10 -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/profile" 2>/dev/null | jq -r '.data.user.id // .data.id // 1' 2>/dev/null)
        test_api "User quota info" "GET" "/api/v1/admin/quota/users/${_uid}" "200" "" "$group"
    fi
}
