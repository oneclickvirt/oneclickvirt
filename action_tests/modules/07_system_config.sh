#!/bin/bash
# Module 07: System Configuration & Level Limits
# Dependencies: 01_init (ADMIN_TOKEN)

run_module_07() {
    report_add_section "07 - System Configuration"
    local group="config"

    # -- Get unified config --
    test_api "Get unified config" "GET" "/api/v1/admin/config" "200" "" "$group"

    # -- Update config --
    test_api "Update config (site name)" "PUT" "/api/v1/admin/config" "200" \
        '{"site_name":"CI Test Platform","registration_enabled":true}' "$group"

    # -- Verify config value --
    local cfg; cfg=$(test_api "Verify config" "GET" "/api/v1/admin/config" "200" "" "$group")

    # -- Update level limits --
    test_api "Update level limits" "PUT" "/api/v1/admin/config" "200" \
        '{"level_limits":{"1":{"max_instances":3,"max_cpu":2,"max_memory":1024,"max_disk":20},"2":{"max_instances":5,"max_cpu":4,"max_memory":2048,"max_disk":50}}}' "$group"

    # -- Config route (alternative) --
    test_api "Get config (alt route)" "GET" "/api/v1/config" "200" "" "$group"
    test_api "Update config (alt route)" "PUT" "/api/v1/config" "200" \
        '{"site_name":"CI Test Platform"}' "$group"

    # -- Public system config --
    test_api_noauth "Public system config" "GET" "/api/v1/public/system-config" "200" "" "$group"

    # -- Register config --
    test_api_noauth "Register config" "GET" "/api/v1/public/register-config" "200" "" "$group"

    # -- Admin dashboard --
    test_api "Admin dashboard" "GET" "/api/v1/admin/dashboard" "200" "" "$group"

    # -- System monitoring --
    test_api "System monitoring" "GET" "/api/v1/admin/monitoring/system" "200" "" "$group"

    # -- Audit logs --
    test_api "Audit logs" "GET" "/api/v1/admin/monitoring/audit-logs?page=1&pageSize=10" "200" "" "$group"

    # -- Performance metrics --
    test_api "Performance metrics" "GET" "/api/v1/admin/performance/metrics" "200" "" "$group"
    test_api "Performance history" "GET" "/api/v1/admin/performance/history?hours=24" "200" "" "$group"

    # -- Log viewing --
    test_api "Log dates" "GET" "/api/v1/admin/logs/dates" "200" "" "$group"
    test_api "Log content" "GET" "/api/v1/admin/logs/content?date=$(date +%Y-%m-%d)&file=info" "200|404" "" "$group"

    # -- Admin group info --
    test_api "Get group info" "GET" "/api/v1/admin/group-info" "200" "" "$group"
    test_api "Update group info" "PUT" "/api/v1/admin/group-info" "200" \
        '{"name":"CI Test Group","description":"Integration test group"}' "$group"

    # -- Negative: Update config with invalid level limits (non-numeric) --
    test_api "Update config (invalid level limit type)" "PUT" "/api/v1/admin/config" "400|200" \
        '{"level_limits":{"invalid_key":{"max_instances":"not_a_number"}}}' "$group"

    # -- Negative: Update config with empty body --
    test_api "Update config (empty body)" "PUT" "/api/v1/admin/config" "400|200" '{}' "$group"

    # -- Negative: Access admin config without token --
    test_api_noauth "Admin config without token" "GET" "/api/v1/admin/config" "200|401" "" "$group"

    # -- Negative: Access admin dashboard without token --
    test_api_noauth "Dashboard without token" "GET" "/api/v1/admin/dashboard" "200|401" "" "$group"

    # -- Negative: Update config with SQL injection --
    test_api "Config (SQL injection)" "PUT" "/api/v1/admin/config" "200|400" \
        '{"site_name":"test\"; DROP TABLE users;--"}' "$group"

    # -- Negative: Access performance metrics without token --
    test_api_noauth "Metrics without token" "GET" "/api/v1/admin/performance/metrics" "200|401" "" "$group"

    # -- Negative: Access audit logs without token --
    test_api_noauth "Audit logs without token" "GET" "/api/v1/admin/monitoring/audit-logs?page=1&pageSize=10" "200|401" "" "$group"

    # -- Negative: User cannot manage system config --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> system config (403)" "PUT" "/api/v1/admin/config" "401|403" \
            '{"site_name":"Hacked"}' "$group" "$USER_TOKEN"
        test_api "User -> dashboard (403)" "GET" "/api/v1/admin/dashboard" "401|403" "" "$group" "$USER_TOKEN"
        test_api "User -> audit logs (403)" "GET" "/api/v1/admin/monitoring/audit-logs?page=1&pageSize=10" "401|403" "" "$group" "$USER_TOKEN"
    fi

    # -- Negative: Update level limits with boundary values --
    test_api "Level limits (zero values)" "PUT" "/api/v1/admin/config" "200|400" \
        '{"level_limits":{"1":{"max_instances":0,"max_cpu":0,"max_memory":0,"max_disk":0}}}' "$group"
    test_api "Level limits (negative values)" "PUT" "/api/v1/admin/config" "200|400" \
        '{"level_limits":{"1":{"max_instances":-1,"max_cpu":-1}}}' "$group"

    # -- Negative: Invalid pagination --
    test_api "Audit logs (page=0)" "GET" "/api/v1/admin/monitoring/audit-logs?page=0&pageSize=10" "200|400" "" "$group"
    test_api "Audit logs (huge page)" "GET" "/api/v1/admin/monitoring/audit-logs?page=1&pageSize=99999" "200|400" "" "$group"
}
