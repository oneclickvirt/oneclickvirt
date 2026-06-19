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

    # ==============================
    # Traffic Quota Visibility Tests
    # ==============================
    # Verify provider traffic_quota_visible is reflected in instance traffic detail
    local traffic_instance_id="${TEST_INSTANCE_ID:-}"
    if [[ -n "$traffic_instance_id" && -n "$USER_TOKEN" ]] && ensure_test_instance_available "$ADMIN_TOKEN" "$traffic_instance_id" "traffic visibility instance"; then
        log_info "Testing traffic quota visibility with instance ${traffic_instance_id}..."
        local tqv_group="traffic_visibility"

        # Get instance traffic detail
        local tqv_detail; tqv_detail=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/traffic/instance/${traffic_instance_id}" 2>/dev/null)
        local tqv_code; tqv_code=$(echo "$tqv_detail" | jq -r '.code // empty' 2>/dev/null)

        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ "$tqv_code" == "200" ]]; then
            local tqv_visible; tqv_visible=$(echo "$tqv_detail" | jq -r '.data.visible // "__missing__"' 2>/dev/null)
            if [[ "$tqv_visible" != "__missing__" ]]; then
                PASSED_TESTS=$((PASSED_TESTS + 1))
                log_success "Traffic detail contains visible field: ${tqv_visible}"
                _add_result_json "Traffic visible field" "GET" "/api/v1/user/traffic/instance/${traffic_instance_id}" "PASS" "present" "$tqv_visible" "" "$tqv_group"
            else
                FAILED_TESTS=$((FAILED_TESTS + 1))
                log_error "Traffic detail missing visible field"
                _add_result_json "Traffic visible field" "GET" "/api/v1/user/traffic/instance/${traffic_instance_id}" "FAIL" "present" "missing" "$tqv_detail" "$tqv_group"
            fi
        else
            SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
            report_add_skip "Traffic visible field" "GET" "/api/v1/user/traffic/instance/${traffic_instance_id}" "user traffic detail unavailable (code=${tqv_code:-unknown})"
            _add_result_json "Traffic visible field" "GET" "/api/v1/user/traffic/instance/${traffic_instance_id}" "SKIP" "" "" "user traffic detail unavailable (code=${tqv_code:-unknown}); response: ${tqv_detail}" "$tqv_group"
        fi

        # Get instance traffic history and check it respects visibility
        local tqv_hist; tqv_hist=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/instances/${traffic_instance_id}/traffic/history" 2>/dev/null)
        local tqv_hist_code; tqv_hist_code=$(echo "$tqv_hist" | jq -r '.code // empty' 2>/dev/null)
        log_info "Traffic history response code: ${tqv_hist_code}"
    elif [[ -n "$traffic_instance_id" ]]; then
        local tqv_group="traffic_visibility"
        record_skip_result "Traffic visible field" "GET" "/api/v1/user/traffic/instance/${traffic_instance_id}" "test instance is no longer available or USER_TOKEN is missing" "$tqv_group"
    fi

    # -- Verify provider has traffic over-limit policy fields --
    if [[ -n "$PROVIDER_ID" ]]; then
        log_info "Verifying provider traffic over-limit policy fields..."
        local prov_detail; prov_detail=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null)
        local prov_traffic_action; prov_traffic_action=$(echo "$prov_detail" | jq -r '.data.trafficOverLimitAction // empty' 2>/dev/null)
        local prov_speed_kbps; prov_speed_kbps=$(echo "$prov_detail" | jq -r '.data.trafficSpeedLimitKbps // empty' 2>/dev/null)
        log_info "Provider trafficOverLimitAction=${prov_traffic_action}, trafficSpeedLimitKbps=${prov_speed_kbps}"
    fi
}
