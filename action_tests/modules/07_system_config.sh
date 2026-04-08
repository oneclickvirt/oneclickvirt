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
}
