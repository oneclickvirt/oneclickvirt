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
}
