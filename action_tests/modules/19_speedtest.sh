#!/bin/bash
# Module 19: Speedtest (instance speed test with traffic monitoring)
# Dependencies: 10_instances (TEST_INSTANCE_ID), 09_providers (PROVIDER_ID)

run_module_19() {
    report_add_section "19 - Speedtest"
    local group="speedtest"

    local speedtest_instance_id="${TEST_INSTANCE_ID:-}"
    if [[ -z "$speedtest_instance_id" || -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No instance or provider"
        return 0
    fi
    if ! ensure_test_instance_available "$ADMIN_TOKEN" "$speedtest_instance_id" "speedtest instance"; then
        chain_break "$group" "Test instance is no longer available"
        return 0
    fi

    # -- Get traffic before speedtest --
    local traffic_before; traffic_before=$(test_api "Traffic before speedtest" "GET" \
        "/api/v1/admin/traffic/overview" "200" "" "$group" "$ADMIN_TOKEN")

    # -- Deploy monitoring agent if not done --
    test_api "Ensure monitoring agent" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent" "200|400|409|500" \
        '{"action":"deploy"}' "$group" "$ADMIN_TOKEN"

    # -- Start traffic monitoring --
    test_api "Start traffic monitor" "POST" "/api/v1/admin/providers/traffic-monitor" "200" \
        '{"providerId":'"$PROVIDER_ID"',"operation":"enable"}' "$group" "$ADMIN_TOKEN"

    # -- Run speedtest via instance action (only if instance is in a runnable state) --
    local inst_status; inst_status=$(curl -s --max-time 10 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/instances/${speedtest_instance_id}" 2>/dev/null | jq -r '.data.status // empty' 2>/dev/null)
    if [[ "$inst_status" == "running" || "$inst_status" == "stopped" ]]; then
        local action_resp; action_resp=$(test_api "Speedtest instance action" "POST" \
            "/api/v1/admin/instances/${speedtest_instance_id}/action" "200|400|404|409|500" \
            '{"action":"restart"}' "$group" "$ADMIN_TOKEN")
        wait_instance_operation_settled "$speedtest_instance_id" "$action_resp" "running" "speedtest restart ${speedtest_instance_id}" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 "$group" || true
    else
        record_skip_result "Speedtest instance action" "POST" "/api/v1/admin/instances/${speedtest_instance_id}/action" "instance status=${inst_status:-gone}, not runnable" "$group"
    fi

    # -- Wait for traffic data to settle --
    sleep 5

    # -- Sync traffic --
    test_api "Sync instance traffic" "POST" "/api/v1/admin/traffic/sync/instance/${speedtest_instance_id}" "200" \
        '' "$group" "$ADMIN_TOKEN"

    # -- Get traffic after --
    test_api "Traffic after speedtest" "GET" "/api/v1/admin/traffic/overview" "200" "" "$group" "$ADMIN_TOKEN"

    # -- User-side traffic verification --
    test_api "User traffic after test" "GET" "/api/v1/user/traffic/overview" "200" "" "$group" "$USER_TOKEN"
    test_api "User instance traffic" "GET" "/api/v1/user/traffic/instance/${speedtest_instance_id}" "200|403|404" "" "$group" "$USER_TOKEN"

    # -- Monitor tasks --
    test_api "Monitor tasks list" "GET" "/api/v1/admin/providers/traffic-monitor/tasks" "200" "" "$group" "$ADMIN_TOKEN"
    test_api "Latest monitor task" "GET" "/api/v1/admin/providers/traffic-monitor/latest?providerId=${PROVIDER_ID}" "200" "" "$group" "$ADMIN_TOKEN"

    # -- Stop traffic monitoring --
    test_api "Stop traffic monitor" "POST" "/api/v1/admin/providers/traffic-monitor" "200" \
        '{"providerId":'"$PROVIDER_ID"',"operation":"disable"}' "$group" "$ADMIN_TOKEN"

    # -- Pmacct data (if available) --
    test_api "Pmacct summary" "GET" "/api/v1/user/instances/${speedtest_instance_id}/pmacct/summary" "200|403|404" "" "$group" "$USER_TOKEN"
    test_api "Pmacct query" "GET" "/api/v1/user/instances/${speedtest_instance_id}/pmacct/query" "200|403|404" "" "$group" "$USER_TOKEN"
    test_api "User pmacct traffic" "GET" "/api/v1/user/traffic/pmacct/${speedtest_instance_id}" "200|403|404" "" "$group" "$USER_TOKEN"
}
