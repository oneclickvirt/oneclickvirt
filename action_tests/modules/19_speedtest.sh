#!/bin/bash
# Module 19: Speedtest (instance speed test with traffic monitoring)
# Dependencies: 10_instances (TEST_INSTANCE_ID), 09_providers (PROVIDER_ID)

run_module_19() {
    report_add_section "19 - Speedtest"
    local group="speedtest"

    if [[ -z "$TEST_INSTANCE_ID" || -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No instance or provider"
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

    # -- Run speedtest via instance action (iperf/dd) --
    local action_resp; action_resp=$(test_api "Speedtest instance action" "POST" \
        "/api/v1/admin/instances/${TEST_INSTANCE_ID}/action" "200|400|404|409|500" \
        '{"action":"restart"}' "$group" "$ADMIN_TOKEN")

    # -- Wait for traffic data to settle --
    sleep 5

    # -- Sync traffic --
    test_api "Sync instance traffic" "POST" "/api/v1/admin/traffic/sync/instance/${TEST_INSTANCE_ID}" "200" \
        '' "$group" "$ADMIN_TOKEN"

    # -- Get traffic after --
    test_api "Traffic after speedtest" "GET" "/api/v1/admin/traffic/overview" "200" "" "$group" "$ADMIN_TOKEN"

    # -- User-side traffic verification --
    test_api "User traffic after test" "GET" "/api/v1/user/traffic/overview" "200" "" "$group" "$USER_TOKEN"
    test_api "User instance traffic" "GET" "/api/v1/user/traffic/instance/${TEST_INSTANCE_ID}" "200|403|404" "" "$group" "$USER_TOKEN"

    # -- Monitor tasks --
    test_api "Monitor tasks list" "GET" "/api/v1/admin/providers/traffic-monitor/tasks" "200" "" "$group" "$ADMIN_TOKEN"
    test_api "Latest monitor task" "GET" "/api/v1/admin/providers/traffic-monitor/latest?providerId=${PROVIDER_ID}" "200" "" "$group" "$ADMIN_TOKEN"

    # -- Stop traffic monitoring --
    test_api "Stop traffic monitor" "POST" "/api/v1/admin/providers/traffic-monitor" "200" \
        '{"providerId":'"$PROVIDER_ID"',"operation":"disable"}' "$group" "$ADMIN_TOKEN"

    # -- Pmacct data (if available) --
    test_api "Pmacct summary" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/pmacct/summary" "200|403|404" "" "$group" "$USER_TOKEN"
    test_api "Pmacct query" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/pmacct/query" "200|403|404" "" "$group" "$USER_TOKEN"
    test_api "User pmacct traffic" "GET" "/api/v1/user/traffic/pmacct/${TEST_INSTANCE_ID}" "200|403|404" "" "$group" "$USER_TOKEN"
}
