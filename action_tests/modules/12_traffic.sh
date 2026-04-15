#!/bin/bash
# Module 12: Traffic Management
# Dependencies: 09_providers (PROVIDER_ID), 02_auth (USER_TOKEN)

run_module_12() {
    report_add_section "12 - Traffic Management"
    local group="traffic"

    # -- System traffic overview --
    test_api "System traffic overview" "GET" "/api/v1/admin/traffic/overview" "200" "" "$group"

    # -- Provider traffic --
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Provider traffic stats" "GET" "/api/v1/admin/traffic/provider/${PROVIDER_ID}" "200" "" "$group"
    fi

    # -- User traffic --
    local test_uid; test_uid=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/users?page=1&pageSize=50" 2>/dev/null | \
        jq -r "[.data.list[]? | select(.username==\"${TEST_USER}\")][0].id // empty" 2>/dev/null)
    if [[ -n "$test_uid" ]]; then
        test_api "User traffic stats" "GET" "/api/v1/admin/traffic/user/${test_uid}" "200" "" "$group"
    fi

    # -- Traffic ranking --
    test_api "Traffic ranking" "GET" "/api/v1/admin/traffic/users/rank" "200" "" "$group"

    # -- Traffic limits --
    if [[ -n "$test_uid" ]]; then
        test_api "Manage traffic limits" "POST" "/api/v1/admin/traffic/manage" "200" \
            "{\"type\":\"user\",\"action\":\"limit\",\"target_id\":${test_uid},\"reason\":\"CI test\"}" "$group"

        test_api "Batch manage limits" "POST" "/api/v1/admin/traffic/batch-manage" "200" \
            "{\"action\":\"unlimit\",\"user_ids\":[${test_uid}]}" "$group"
    fi

    # -- Traffic monitor --
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Traffic monitor operation" "POST" "/api/v1/admin/providers/traffic-monitor" "200" \
            "{\"providerId\":${PROVIDER_ID},\"operation\":\"enable\"}" "$group"
        test_api "Traffic monitor tasks" "GET" "/api/v1/admin/providers/traffic-monitor/tasks?page=1&pageSize=10" "200" "" "$group"
        test_api "Latest traffic monitor" "GET" "/api/v1/admin/providers/traffic-monitor/latest?providerId=${PROVIDER_ID}" "200" "" "$group"

        # -- Traffic monitor task detail (get first task if available) --
        local tm_tasks; tm_tasks=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/traffic-monitor/tasks?page=1&pageSize=1" 2>/dev/null)
        local tm_task_id; tm_task_id=$(echo "$tm_tasks" | jq -r '.data.list[0].id // .data[0].id // empty' 2>/dev/null)
        if [[ -n "$tm_task_id" ]]; then
            test_api "Traffic monitor task detail" "GET" "/api/v1/admin/providers/traffic-monitor/tasks/${tm_task_id}" "200" "" "$group"
        fi
    fi

    # -- Traffic sync --
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Sync provider traffic" "POST" "/api/v1/admin/traffic/sync/provider/${PROVIDER_ID}" "200" '{}' "$group"
    fi
    if [[ -n "$test_uid" ]]; then
        test_api "Sync user traffic" "POST" "/api/v1/admin/traffic/sync/user/${test_uid}" "200" '{}' "$group"
    fi
    test_api "Sync all traffic" "POST" "/api/v1/admin/traffic/sync/all" "200" '{}' "$group"

    # -- Batch sync (requires user_ids) --
    if [[ -n "$test_uid" ]]; then
        test_api "Batch sync traffic" "POST" "/api/v1/admin/traffic/batch-sync" "200" \
            "{\"user_ids\":[${test_uid}]}" "$group"
    fi

    # -- Clear user traffic --
    if [[ -n "$test_uid" ]]; then
        test_api "Clear user traffic" "DELETE" "/api/v1/admin/traffic/user/${test_uid}/clear" "200" "" "$group"
    fi

    # -- User-side traffic APIs --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User traffic overview" "GET" "/api/v1/user/traffic/overview" "200" "" "$group" "$USER_TOKEN"
        test_api "User traffic instances" "GET" "/api/v1/user/traffic/instances" "200" "" "$group" "$USER_TOKEN"
        test_api "User traffic limit status" "GET" "/api/v1/user/traffic/limit-status" "200" "" "$group" "$USER_TOKEN"
        test_api "User traffic history" "GET" "/api/v1/user/traffic/history" "200" "" "$group" "$USER_TOKEN"
    fi

    # -- Negative tests --
    # Traffic stats for nonexistent provider
    test_api "Traffic (nonexistent provider)" "GET" "/api/v1/admin/traffic/provider/99999" "200|400|404" "" "$group"
    # Traffic stats for nonexistent user
    test_api "Traffic (nonexistent user)" "GET" "/api/v1/admin/traffic/user/99999" "200|400|404" "" "$group"
    # Sync traffic for nonexistent instance
    test_api "Sync traffic (nonexistent instance)" "POST" "/api/v1/admin/traffic/sync/instance/99999" "200|400|404" '{}' "$group"
    # Manage traffic with invalid action
    test_api "Manage traffic (invalid action)" "POST" "/api/v1/admin/traffic/manage" "400" \
        '{"type":"invalid","action":"invalid","target_id":99999}' "$group"

    # -- Negative: User cannot access admin traffic endpoints --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> admin traffic (403)" "GET" "/api/v1/admin/traffic/provider/1" "401|403" "" "$group" "$USER_TOKEN"
        test_api "User -> manage traffic (403)" "POST" "/api/v1/admin/traffic/manage" "401|403" \
            '{"type":"provider","action":"sync","target_id":1}' "$group" "$USER_TOKEN"
    fi

    # -- Negative: Traffic sync with invalid target --
    test_api "Manage traffic (zero target)" "POST" "/api/v1/admin/traffic/manage" "400" \
        '{"type":"provider","action":"sync","target_id":0}' "$group"

    # -- Negative: Traffic manage with missing type --
    test_api "Manage traffic (missing type)" "POST" "/api/v1/admin/traffic/manage" "400" \
        '{"action":"sync","target_id":1}' "$group"
}
