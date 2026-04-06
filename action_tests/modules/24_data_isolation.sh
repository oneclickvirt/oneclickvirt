#!/bin/bash
# Module 24: Data Isolation (multi-user isolation verification)
# Dependencies: 02_auth (USER_TOKEN, USER_TOKEN2), 10_instances (TEST_INSTANCE_ID)

run_module_24() {
    report_add_section "24 - Data Isolation"
    local group="data_isolation"

    if [[ -z "$USER_TOKEN" || -z "$USER_TOKEN2" ]]; then
        chain_break "$group" "Need both user tokens"
        return 1
    fi

    # ---- Instance isolation ----
    # User1 instances not visible to User2
    local u1_instances; u1_instances=$(test_api "User1 instances" "GET" "/api/v1/user/instances" "200" "" "$group" "$USER_TOKEN")
    local u2_instances; u2_instances=$(test_api "User2 instances" "GET" "/api/v1/user/instances" "200" "" "$group" "$USER_TOKEN2")

    if [[ -n "$TEST_INSTANCE_ID" ]]; then
        test_api "User2 -> user1 instance (403/404)" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "403|404" \
            "" "$group" "$USER_TOKEN2"
        test_api "User2 -> user1 instance action (403/404)" "POST" "/api/v1/user/instances/action" "403|404|400" \
            '{"instance_id":"'"$TEST_INSTANCE_ID"'","action":"stop"}' "$group" "$USER_TOKEN2"
        test_api "User2 -> user1 instance password (403/404)" "PUT" "/api/v1/user/instances/${TEST_INSTANCE_ID}/reset-password" "403|404" \
            '{"password":"Hack123!@#"}' "$group" "$USER_TOKEN2"
        test_api "User2 -> user1 monitoring (403/404)" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/monitoring" "403|404" \
            "" "$group" "$USER_TOKEN2"
        test_api "User2 -> user1 ports (403/404)" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/ports" "403|404" \
            "" "$group" "$USER_TOKEN2"
        test_api "User2 -> user1 traffic (403/404)" "GET" "/api/v1/user/traffic/instance/${TEST_INSTANCE_ID}" "403|404" \
            "" "$group" "$USER_TOKEN2"
    fi

    # ---- Domain isolation ----
    test_api "User1 domains" "GET" "/api/v1/user/domains" "200" "" "$group" "$USER_TOKEN"
    test_api "User2 domains" "GET" "/api/v1/user/domains" "200" "" "$group" "$USER_TOKEN2"

    # ---- Task isolation ----
    test_api "User1 tasks" "GET" "/api/v1/user/tasks" "200" "" "$group" "$USER_TOKEN"
    test_api "User2 tasks" "GET" "/api/v1/user/tasks" "200" "" "$group" "$USER_TOKEN2"

    # ---- Traffic isolation ----
    test_api "User1 traffic" "GET" "/api/v1/user/traffic/overview" "200" "" "$group" "$USER_TOKEN"
    test_api "User2 traffic" "GET" "/api/v1/user/traffic/overview" "200" "" "$group" "$USER_TOKEN2"

    # ---- Port mapping isolation ----
    test_api "User1 port mappings" "GET" "/api/v1/user/port-mappings" "200" "" "$group" "$USER_TOKEN"
    test_api "User2 port mappings" "GET" "/api/v1/user/port-mappings" "200" "" "$group" "$USER_TOKEN2"

    # ---- KYC isolation ----
    test_api "User1 KYC" "GET" "/api/v1/user/kyc" "200" "" "$group" "$USER_TOKEN"
    test_api "User2 KYC" "GET" "/api/v1/user/kyc" "200" "" "$group" "$USER_TOKEN2"

    # ---- Checkin isolation ----
    test_api "User1 checkin records" "GET" "/api/v1/user/checkin/records" "200" "" "$group" "$USER_TOKEN"
    test_api "User2 checkin records" "GET" "/api/v1/user/checkin/records" "200" "" "$group" "$USER_TOKEN2"

    # ---- Dashboard isolation ----
    test_api "User1 dashboard" "GET" "/api/v1/user/dashboard" "200" "" "$group" "$USER_TOKEN"
    test_api "User2 dashboard" "GET" "/api/v1/user/dashboard" "200" "" "$group" "$USER_TOKEN2"

    # ---- Profile isolation ----
    local u1_profile; u1_profile=$(test_api "User1 profile" "GET" "/api/v1/user/profile" "200" "" "$group" "$USER_TOKEN")
    local u2_profile; u2_profile=$(test_api "User2 profile" "GET" "/api/v1/user/profile" "200" "" "$group" "$USER_TOKEN2")

    # ---- User cannot access admin routes ----
    test_api "User -> admin dashboard (403)" "GET" "/api/v1/admin/dashboard" "401|403" "" "$group" "$USER_TOKEN"
    test_api "User -> admin instances (403)" "GET" "/api/v1/admin/instances" "401|403" "" "$group" "$USER_TOKEN"
    test_api "User -> admin providers (403)" "GET" "/api/v1/admin/providers" "401|403" "" "$group" "$USER_TOKEN"
    test_api "User -> admin users (403)" "GET" "/api/v1/admin/users" "401|403" "" "$group" "$USER_TOKEN"
    test_api "User -> admin config (403)" "GET" "/api/v1/admin/config" "401|403" "" "$group" "$USER_TOKEN"

    # ---- Token cross-contamination check ----
    test_api "Empty token -> user profile (401)" "GET" "/api/v1/user/profile" "401" "" "$group" ""
    test_api "Garbage token -> user profile (401)" "GET" "/api/v1/user/profile" "401" "" "$group" "garbage_token_value"
}
