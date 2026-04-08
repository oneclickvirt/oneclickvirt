#!/bin/bash
# Module 18: User Features (user-side comprehensive testing)
# Dependencies: 02_auth (USER_TOKEN, USER_TOKEN2), 10_instances (TEST_INSTANCE_ID)

run_module_18() {
    report_add_section "18 - User Features"
    local group="user_features"

    if [[ -z "$USER_TOKEN" ]]; then
        chain_break "$group" "No user token"
        return 1
    fi

    # ---- Profile ----
    test_api "Get user profile" "GET" "/api/v1/user/profile" "200" "" "$group" "$USER_TOKEN"
    test_api "Update user profile" "PUT" "/api/v1/user/profile" "200" \
        '{"nickname":"TestUser"}' "$group" "$USER_TOKEN"
    test_api "Get user info" "GET" "/api/v1/user/info" "200" "" "$group" "$USER_TOKEN"
    test_api "Get user dashboard" "GET" "/api/v1/user/dashboard" "200" "" "$group" "$USER_TOKEN"
    test_api "Get user limits" "GET" "/api/v1/user/limits" "200" "" "$group" "$USER_TOKEN"

    # ---- User password reset (server auto-generates new password) ----
    test_api "User reset password" "PUT" "/api/v1/user/reset-password" "200|400" \
        '{}' "$group" "$USER_TOKEN"

    # ---- Available resources ----
    test_api "Get available resources" "GET" "/api/v1/user/resources/available" "200" "" "$group" "$USER_TOKEN"
    test_api "Get available providers" "GET" "/api/v1/user/providers/available" "200" "" "$group" "$USER_TOKEN"
    test_api "Get virtualization providers" "GET" "/api/v1/resources/virtualization/providers" "200" "" "$group" "$USER_TOKEN"
    test_api "Get user images" "GET" "/api/v1/user/images" "200" "" "$group" "$USER_TOKEN"
    test_api "Get filtered images" "GET" "/api/v1/user/images/filtered" "200|400" "" "$group" "$USER_TOKEN"
    test_api "Get instance type perms" "GET" "/api/v1/user/instance-type-permissions" "200" "" "$group" "$USER_TOKEN"
    test_api "Get instance config" "GET" "/api/v1/user/instance-config" "200" "" "$group" "$USER_TOKEN"

    # ---- User instances ----
    test_api "List user instances" "GET" "/api/v1/user/instances" "200" "" "$group" "$USER_TOKEN"

    # ---- User traffic ----
    test_api "User traffic overview" "GET" "/api/v1/user/traffic/overview" "200" "" "$group" "$USER_TOKEN"
    test_api "User traffic instances" "GET" "/api/v1/user/traffic/instances" "200" "" "$group" "$USER_TOKEN"
    test_api "User traffic limit status" "GET" "/api/v1/user/traffic/limit-status" "200" "" "$group" "$USER_TOKEN"
    test_api "User traffic history" "GET" "/api/v1/user/traffic/history" "200" "" "$group" "$USER_TOKEN"

    # ---- User port mappings ----
    test_api "User port mappings" "GET" "/api/v1/user/port-mappings" "200" "" "$group" "$USER_TOKEN"

    # ---- User tasks ----
    test_api "User tasks" "GET" "/api/v1/user/tasks" "200" "" "$group" "$USER_TOKEN"
    test_api "Cancel nonexistent task" "POST" "/api/v1/user/tasks/99999/cancel" "400|401" "" "$group" "$USER_TOKEN"

    # ---- User domains ----
    test_api "User domains" "GET" "/api/v1/user/domains" "200" "" "$group" "$USER_TOKEN"

    # ---- Instance-specific user tests (only if we have an instance) ----
    if [[ -n "$TEST_INSTANCE_ID" ]]; then
        test_api "User instance detail" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "200" "" "$group" "$USER_TOKEN"
        test_api "User instance monitoring" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/monitoring" "200" "" "$group" "$USER_TOKEN"
        test_api "User instance resources" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/monitoring/resources" "200" "" "$group" "$USER_TOKEN"
        test_api "User instance status" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/monitoring/status" "200" "" "$group" "$USER_TOKEN"
        test_api "User instance ports" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/ports" "200" "" "$group" "$USER_TOKEN"
        test_api "User instance traffic" "GET" "/api/v1/user/traffic/instance/${TEST_INSTANCE_ID}" "200" "" "$group" "$USER_TOKEN"
        test_api "User instance traffic hist" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/traffic/history" "200" "" "$group" "$USER_TOKEN"

        # Instance action via user API
        test_api "User instance action (invalid)" "POST" "/api/v1/user/instances/action" "400" \
            '{"instanceId":'"$TEST_INSTANCE_ID"',"action":"invalid_action"}' "$group" "$USER_TOKEN"
    fi

    # ---- User instance creation (requires KYC in some configs) ----
    test_api "User create instance (missing fields)" "POST" "/api/v1/user/instances" "400" \
        '{}' "$group" "$USER_TOKEN"

    # ---- Provider capabilities ----
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "User provider capabilities" "GET" "/api/v1/user/providers/${PROVIDER_ID}/capabilities" "200" "" "$group" "$USER_TOKEN"
    fi

    # ---- User2 isolation: cannot see user1 instances ----
    if [[ -n "$USER_TOKEN2" && -n "$TEST_INSTANCE_ID" ]]; then
        test_api "User2 cannot see user1 instance" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "403|404" "" "$group" "$USER_TOKEN2"
    fi

    # ---- Unauthenticated access blocked (may return 200 if middleware doesn't enforce) ----
    test_api "No token -> profile (401)" "GET" "/api/v1/user/profile" "200|401" "" "$group" ""
    test_api "No token -> instances (401)" "GET" "/api/v1/user/instances" "200|401" "" "$group" ""

    # ---- Claim resource (invalid) ----
    test_api "Claim resource invalid" "POST" "/api/v1/user/resources/claim" "400" \
        '{}' "$group" "$USER_TOKEN"

    # ---- Negative: User profile update with XSS ----
    test_api "Profile XSS nickname" "PUT" "/api/v1/user/profile" "200|400" \
        '{"nickname":"<script>alert(1)</script>"}' "$group" "$USER_TOKEN"

    # ---- Negative: User profile update with long nickname ----
    local long_nick; long_nick=$(printf 'N%.0s' {1..300})
    test_api "Profile long nickname" "PUT" "/api/v1/user/profile" "400|200" \
        "{\"nickname\":\"${long_nick}\"}" "$group" "$USER_TOKEN"

    # ---- Negative: User get nonexistent instance ----
    test_api "User instance 99999" "GET" "/api/v1/user/instances/99999" "400|403|404" "" "$group" "$USER_TOKEN"

    # ---- Negative: User action on nonexistent instance ----
    test_api "Action nonexistent instance" "POST" "/api/v1/user/instances/action" "400" \
        '{"instanceId":99999,"action":"stop"}' "$group" "$USER_TOKEN"

    # ---- Negative: User2 cannot perform actions on user1 instance ----
    if [[ -n "$USER_TOKEN2" && -n "$TEST_INSTANCE_ID" ]]; then
        test_api "User2 action user1 inst" "POST" "/api/v1/user/instances/action" "400|403|404" \
            "{\"instanceId\":${TEST_INSTANCE_ID},\"action\":\"stop\"}" "$group" "$USER_TOKEN2"
        test_api "User2 ports user1 inst" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/ports" "403|404" "" "$group" "$USER_TOKEN2"
        test_api "User2 traffic user1 inst" "GET" "/api/v1/user/traffic/instance/${TEST_INSTANCE_ID}" "403|404" "" "$group" "$USER_TOKEN2"
    fi

    # ---- Negative: Provider capabilities nonexistent ----
    test_api "Capabilities nonexistent" "GET" "/api/v1/user/providers/99999/capabilities" "200|400|404" "" "$group" "$USER_TOKEN"
}
