#!/bin/bash
# Module 17: Admin Isolation (Normal Admin vs Super Admin)
# Dependencies: 03_users (NORMAL_ADMIN_TOKEN, ADMIN_TOKEN)

run_module_17() {
    report_add_section "17 - Admin Isolation"
    local group="admin_isolation"

    if [[ -z "$NORMAL_ADMIN_TOKEN" ]]; then
        chain_break "$group" "No normal admin token"
        return 1
    fi

    # -- Normal admin can access dashboard --
    test_api "Normal admin dashboard" "GET" "/api/v1/admin/dashboard" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"

    # -- Normal admin provider isolation (no providers yet for this admin) --
    local na_prov; na_prov=$(test_api "Normal admin providers" "GET" "/api/v1/admin/providers?page=1&pageSize=10" "200" "" "$group" "$NORMAL_ADMIN_TOKEN")

    # -- Normal admin instance isolation --
    test_api "Normal admin instances" "GET" "/api/v1/admin/instances?page=1&pageSize=10" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"
    local isolation_instance_id="${TEST_INSTANCE_ID:-}"
    if [[ -n "$isolation_instance_id" ]] && ensure_test_instance_available "$ADMIN_TOKEN" "$isolation_instance_id" "admin isolation instance"; then
        test_api "Normal admin -> foreign instance detail (403)" "GET" "/api/v1/admin/instances/${isolation_instance_id}" "403|404" \
            "" "$group" "$NORMAL_ADMIN_TOKEN"
        # Instance may have been deleted/transferred by the time this module runs, so accept 403 (forbidden) or 404 (not found)
        test_api "Normal admin -> foreign instance action blocked before validation (403)" "POST" "/api/v1/admin/instances/${isolation_instance_id}/action" "403|404" \
            '{"action":"invalid_action_for_isolation"}' "$group" "$NORMAL_ADMIN_TOKEN"
        # Use a valid action (e.g. "start") so the request passes input validation and reaches the isolation/permission check.
        # Using "invalid_action_for_isolation" would be rejected at the validation layer with 400 before isolation is tested.
        test_api_json_value "Normal admin -> foreign batch action blocked before validation" "POST" "/api/v1/admin/instances/batch-action" "200" \
            '.data.results[0].error | contains("无权")' "true" \
            "{\"instanceIds\":[${isolation_instance_id}],\"action\":\"start\"}" "$group" "$NORMAL_ADMIN_TOKEN" >/dev/null
        test_api "Normal admin -> foreign password result (403)" "GET" "/api/v1/admin/instances/${isolation_instance_id}/password/999999" "403|404" \
            "" "$group" "$NORMAL_ADMIN_TOKEN"
    elif [[ -n "$isolation_instance_id" ]]; then
        record_skip_result "Normal admin -> foreign instance detail (403)" "GET" "/api/v1/admin/instances/${isolation_instance_id}" "test instance is no longer available" "$group"
        record_skip_result "Normal admin -> foreign instance action blocked before validation (403)" "POST" "/api/v1/admin/instances/${isolation_instance_id}/action" "test instance is no longer available" "$group"
        record_skip_result "Normal admin -> foreign batch action blocked before validation" "POST" "/api/v1/admin/instances/batch-action" "test instance is no longer available" "$group"
        record_skip_result "Normal admin -> foreign password result (403)" "GET" "/api/v1/admin/instances/${isolation_instance_id}/password/999999" "test instance is no longer available" "$group"
    fi

    # -- Super admin sees all providers --
    test_api "Super admin all providers" "GET" "/api/v1/admin/providers?page=1&pageSize=10" "200" "" "$group" "$ADMIN_TOKEN"

    # -- Normal admin cannot access super-admin routes --
    test_api "Normal admin -> users (403)" "GET" "/api/v1/admin/users" "403" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> config (403)" "GET" "/api/v1/admin/config" "403" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> system images (403)" "GET" "/api/v1/admin/system-images" "403" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> invite codes (403)" "GET" "/api/v1/admin/invite-codes" "403" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> announcements (403)" "GET" "/api/v1/admin/announcements" "403" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> audit logs (403)" "GET" "/api/v1/admin/monitoring/audit-logs" "403" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> performance (403)" "GET" "/api/v1/admin/performance/metrics" "403" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> logs (403)" "GET" "/api/v1/admin/logs/dates" "403" "" "$group" "$NORMAL_ADMIN_TOKEN"

    # -- Normal admin can access own routes --
    test_api "Normal admin -> redemption codes" "GET" "/api/v1/admin/redemption-codes?page=1&pageSize=10" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> tasks" "GET" "/api/v1/admin/tasks?page=1&pageSize=10" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> group info" "GET" "/api/v1/admin/group-info" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> block rules" "GET" "/api/v1/admin/block-rules?page=1&pageSize=10" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"
    test_api "Normal admin -> port mappings" "GET" "/api/v1/admin/port-mappings?page=1&pageSize=10" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"

    # -- Normal admin traffic access --
    test_api "Normal admin -> traffic overview" "GET" "/api/v1/admin/traffic/overview" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"

    # -- Normal admin KYC access --
    test_api "Normal admin -> KYC list" "GET" "/api/v1/admin/kyc?page=1&pageSize=10" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"

    # -- Normal admin cannot transfer instances --
    test_api "Normal admin -> transfer (403)" "POST" "/api/v1/admin/instances/transfer" "403" \
        '{"instance_id":1,"target_user_id":1}' "$group" "$NORMAL_ADMIN_TOKEN"

    # -- Normal admin cannot login as user --
    test_api "Normal admin -> login-as (403)" "POST" "/api/v1/admin/users/1/login-as" "403" \
        '' "$group" "$NORMAL_ADMIN_TOKEN"

    # -- Normal admin cannot create users --
    test_api "Normal admin -> create user (403)" "POST" "/api/v1/admin/users" "403" \
        '{"username":"na_test","password":"Test123!@#"}' "$group" "$NORMAL_ADMIN_TOKEN"

    # -- Super admin can login as user --
    if [[ -n "${USER_TOKEN:-}" ]]; then
        local _la_uid; _la_uid=$(curl -s --max-time 10 -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/profile" 2>/dev/null | jq -r '.data.user.id // .data.id // empty' 2>/dev/null)
        if [[ -n "$_la_uid" ]]; then
            test_api "Super admin login-as user" "POST" "/api/v1/admin/users/${_la_uid}/login-as" "200" \
                '' "$group"
        fi
    fi
    test_api "Login-as nonexistent user" "POST" "/api/v1/admin/users/99999/login-as" "400|404" '' "$group"
}
