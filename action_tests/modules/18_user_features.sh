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
    test_api "User reset password" "PUT" "/api/v1/user/reset-password" "200|400|404|500" \
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
    test_api "Cancel nonexistent task" "POST" "/api/v1/user/tasks/99999/cancel" "400|401|404" "" "$group" "$USER_TOKEN"

    # ---- User domains ----
    test_api "User domains" "GET" "/api/v1/user/domains" "200" "" "$group" "$USER_TOKEN"

    # ---- Instance-specific user tests (only if we have an instance) ----
    if [[ -n "$TEST_INSTANCE_ID" ]]; then
        test_api "User instance detail" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "200|403|404" "" "$group" "$USER_TOKEN"
        test_api "User instance monitoring" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/monitoring" "200|403|404" "" "$group" "$USER_TOKEN"
        test_api "User instance resources" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/monitoring/resources" "200|403|404" "" "$group" "$USER_TOKEN"
        test_api "User instance status" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/monitoring/status" "200|403|404" "" "$group" "$USER_TOKEN"
        test_api "User instance ports" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/ports" "200|403|404" "" "$group" "$USER_TOKEN"

        # If controller-forwarded mappings are present, user-facing publicIP must be available for UI display.
        local user_ports_detail
        user_ports_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/instances/${TEST_INSTANCE_ID}/ports" 2>/dev/null)
        local user_ports_code
        user_ports_code=$(echo "$user_ports_detail" | jq -r '.code // empty' 2>/dev/null)
        if [[ "$user_ports_code" == "200" ]]; then
            local controller_count
            controller_count=$(echo "$user_ports_detail" | jq -r '[.data.list[]? | select(.mappingType == "controller")] | length' 2>/dev/null)
            if [[ "$controller_count" =~ ^[0-9]+$ && "$controller_count" -gt 0 ]]; then
                local user_public_ip
                user_public_ip=$(echo "$user_ports_detail" | jq -r '.data.publicIP // empty' 2>/dev/null)
                if [[ -n "$user_public_ip" ]]; then
                    log_success "Controller mapping display publicIP is present: ${user_public_ip}"
                else
                    log_warning "Controller mapping exists but user publicIP is empty"
                fi
            fi
        fi

        test_api "User instance traffic" "GET" "/api/v1/user/traffic/instance/${TEST_INSTANCE_ID}" "200|403|404" "" "$group" "$USER_TOKEN"
        test_api "User instance traffic hist" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/traffic/history" "200|403|404" "" "$group" "$USER_TOKEN"

        # Instance password reset via user API
        local urp; urp=$(test_api "User reset instance password" "PUT" "/api/v1/user/instances/${TEST_INSTANCE_ID}/reset-password" "200|400|403|404" \
            '{"password":"UserNewPass123!"}' "$group" "$USER_TOKEN")
        local urp_task; urp_task=$(echo "$urp" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$urp_task" ]]; then
            wait_task_complete "$SERVER_URL" "$urp_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || true
            test_api "User get new password" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/password/${urp_task}" "200|403|404" "" "$group" "$USER_TOKEN"
        fi

        # Instance action via user API
        test_api "User instance action (invalid)" "POST" "/api/v1/user/instances/action" "400|403|404" \
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

    # ---- Unauthenticated access must be blocked ----
    test_api "No token -> profile (401)" "GET" "/api/v1/user/profile" "401" "" "$group" ""
    test_api "No token -> instances (401)" "GET" "/api/v1/user/instances" "401" "" "$group" ""

    # ---- Claim resource (invalid) ----
    test_api "Claim resource invalid" "POST" "/api/v1/user/resources/claim" "400" \
        '{}' "$group" "$USER_TOKEN"

    # ---- Negative: User profile update with XSS ----
    test_api "Profile XSS nickname" "PUT" "/api/v1/user/profile" "400" \
        '{"nickname":"<script>alert(1)</script>"}' "$group" "$USER_TOKEN"

    # ---- Negative: User profile update with long nickname ----
    local long_nick; long_nick=$(printf 'N%.0s' {1..300})
    test_api "Profile long nickname" "PUT" "/api/v1/user/profile" "400" \
        "{\"nickname\":\"${long_nick}\"}" "$group" "$USER_TOKEN"

    # ---- Negative: User get nonexistent instance ----
    test_api "User instance 99999" "GET" "/api/v1/user/instances/99999" "400|403|404" "" "$group" "$USER_TOKEN"

    # ---- Negative: User action on nonexistent instance ----
    test_api "Action nonexistent instance" "POST" "/api/v1/user/instances/action" "400|404" \
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

    # ---- Verify new response fields: trafficQuotaVisible, isFrozen, frozenReason ----
    if [[ -n "$TEST_INSTANCE_ID" ]]; then
        log_info "Verifying new response fields in user instance detail..."
        local uf_group="user_fields"

        # User instance detail
        local uf_detail; uf_detail=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/instances/${TEST_INSTANCE_ID}" 2>/dev/null)

        # Check trafficQuotaVisible field
        local uf_tqv; uf_tqv=$(echo "$uf_detail" | jq -r '.data.trafficQuotaVisible' 2>/dev/null)
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ "$uf_tqv" != "null" ]]; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "User instance detail contains trafficQuotaVisible: ${uf_tqv}"
            _add_result_json "User trafficQuotaVisible field" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "PASS" "present" "$uf_tqv" "" "$uf_group"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_error "User instance detail missing trafficQuotaVisible field"
            _add_result_json "User trafficQuotaVisible field" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "FAIL" "present" "missing" "" "$uf_group"
        fi

        # Check isFrozen field
        local uf_frozen; uf_frozen=$(echo "$uf_detail" | jq -r '.data.isFrozen' 2>/dev/null)
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ "$uf_frozen" != "null" ]]; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "User instance detail contains isFrozen: ${uf_frozen}"
            _add_result_json "User isFrozen field" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "PASS" "present" "$uf_frozen" "" "$uf_group"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_error "User instance detail missing isFrozen field"
            _add_result_json "User isFrozen field" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "FAIL" "present" "missing" "" "$uf_group"
        fi

        # Check frozenReason field (may be empty string for unfrozen instances)
        local uf_frozen_reason; uf_frozen_reason=$(echo "$uf_detail" | jq -r '.data.frozenReason // "__missing__"' 2>/dev/null)
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ "$uf_frozen_reason" != "__missing__" ]]; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "User instance detail contains frozenReason: '${uf_frozen_reason}'"
            _add_result_json "User frozenReason field" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "PASS" "present" "$uf_frozen_reason" "" "$uf_group"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_error "User instance detail missing frozenReason field"
            _add_result_json "User frozenReason field" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}" "FAIL" "present" "missing" "" "$uf_group"
        fi

        # Check expiresAt field in user detail
        local uf_expires; uf_expires=$(echo "$uf_detail" | jq -r '.data.expiresAt // "__missing__"' 2>/dev/null)
        log_info "User instance expiresAt: ${uf_expires}"

        # -- User creates share link --
        local user_share_resp; user_share_resp=$(test_api "User create share link (features)" "POST" \
            "/api/v1/user/instances/${TEST_INSTANCE_ID}/share-links" "200|403|404" \
            '{"expiresInMinutes":15}' "$uf_group" "$USER_TOKEN")
        local user_share_token; user_share_token=$(echo "$user_share_resp" | jq -r '.data.token // empty' 2>/dev/null)
        if [[ -n "$user_share_token" ]]; then
            log_success "User share token created: prefix=${user_share_token:0:8}..."
            # Verify share link accessible via public API (no auth)
            test_api_noauth "User share link accessible (public)" "GET" \
                "/api/v1/public/instance-shares/${user_share_token}" "200" "" "$uf_group"
        fi
    fi
}
