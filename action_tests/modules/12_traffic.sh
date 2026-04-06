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
            "{\"user_id\":${test_uid},\"monthly_limit_gb\":100}" "$group"

        test_api "Batch manage limits" "POST" "/api/v1/admin/traffic/batch-manage" "200" \
            "{\"user_ids\":[${test_uid}],\"monthly_limit_gb\":200}" "$group"
    fi

    # -- Traffic monitor --
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Traffic monitor operation" "POST" "/api/v1/admin/providers/traffic-monitor" "200" \
            "{\"provider_id\":${PROVIDER_ID},\"action\":\"start\"}" "$group"
        test_api "Traffic monitor tasks" "GET" "/api/v1/admin/providers/traffic-monitor/tasks?page=1&pageSize=10" "200" "" "$group"
        test_api "Latest traffic monitor" "GET" "/api/v1/admin/providers/traffic-monitor/latest" "200" "" "$group"
    fi

    # -- Traffic sync --
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Sync provider traffic" "POST" "/api/v1/admin/traffic/sync/provider/${PROVIDER_ID}" "200" '{}' "$group"
    fi
    if [[ -n "$test_uid" ]]; then
        test_api "Sync user traffic" "POST" "/api/v1/admin/traffic/sync/user/${test_uid}" "200" '{}' "$group"
    fi
    test_api "Sync all traffic" "POST" "/api/v1/admin/traffic/sync/all" "200" '{}' "$group"

    # -- Batch sync --
    test_api "Batch sync traffic" "POST" "/api/v1/admin/traffic/batch-sync" "200" '{}' "$group"

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
}
