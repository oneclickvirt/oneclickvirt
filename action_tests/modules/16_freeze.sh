#!/bin/bash
# Module 16: Freeze Management (Provider + Instance cascade)
# Dependencies: 09_providers (PROVIDER_ID)

run_module_16() {
    report_add_section "16 - Freeze Management"
    local group="freeze"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider"
        return 1
    fi

    # -- Set provider expiry --
    local exp; exp=$(date -u -d "+7 days" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v+7d '+%Y-%m-%dT%H:%M:%SZ')
    test_api "Set provider expiry" "POST" "/api/v1/admin/providers/set-expiry" "200" \
        "{\"providerId\":${PROVIDER_ID},\"expiresAt\":\"${exp}\"}" "$group"

    # -- Manual freeze provider --
    test_api "Manual freeze provider" "POST" "/api/v1/admin/providers/freeze-manual" "200" \
        "{\"id\":${PROVIDER_ID},\"reason\":\"CI test freeze\"}" "$group"

    # -- Verify frozen status --
    local ps; ps=$(test_api "Provider status (frozen)" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "200" "" "$group")
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    local frozen; frozen=$(echo "$ps" | jq -r '.data.isFrozen // empty' 2>/dev/null)
    if [[ "$frozen" == "true" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "Provider frozen status verified"
        report_add_pass "Frozen status check" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status"
        _add_result_json "Frozen status check" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "PASS" "true" "$frozen" "" "$group"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "Provider not frozen (status: ${frozen})"
        report_add_fail "Frozen status check" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "" "true" "$frozen" "$ps"
        _add_result_json "Frozen status check" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "FAIL" "true" "$frozen" "" "$group"
    fi

    # -- Cascade freeze (freeze all instances under provider) --
    test_api "Cascade freeze" "POST" "/api/v1/admin/providers/freeze" "200" \
        "{\"id\":${PROVIDER_ID}}" "$group"

    # -- Manual unfreeze --
    test_api "Manual unfreeze provider" "POST" "/api/v1/admin/providers/unfreeze-manual" "200" \
        "{\"id\":${PROVIDER_ID}}" "$group"

    # -- Cascade unfreeze --
    test_api "Cascade unfreeze" "POST" "/api/v1/admin/providers/unfreeze" "200" \
        "{\"id\":${PROVIDER_ID}}" "$group"

    # -- Verify unfrozen --
    test_api "Provider status (unfrozen)" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "200" "" "$group"

    # -- Freeze nonexistent provider --
    test_api "Freeze nonexistent provider" "POST" "/api/v1/admin/providers/freeze-manual" "400|404" \
        '{"id":99999,"reason":"test"}' "$group"

    # -- Instance freeze/unfreeze without instance --
    test_api "Freeze nonexistent instance" "POST" "/api/v1/admin/instances/freeze" "400|404" \
        '{"instanceId":99999}' "$group"

    # -- Negative: Unfreeze nonexistent provider --
    test_api "Unfreeze nonexistent provider" "POST" "/api/v1/admin/providers/unfreeze-manual" "400|404" \
        '{"id":99999}' "$group"

    # -- Negative: Cascade freeze nonexistent --
    test_api "Cascade freeze nonexistent" "POST" "/api/v1/admin/providers/freeze" "400|404" \
        '{"id":99999}' "$group"

    # -- Negative: Cascade unfreeze nonexistent --
    test_api "Cascade unfreeze nonexistent" "POST" "/api/v1/admin/providers/unfreeze" "400|404" \
        '{"id":99999}' "$group"

    # -- Negative: Freeze with missing body --
    test_api "Freeze missing body" "POST" "/api/v1/admin/providers/freeze-manual" "400" \
        '{}' "$group"

    # -- Negative: Instance freeze missing body --
    test_api "Instance freeze missing body" "POST" "/api/v1/admin/instances/freeze" "400" \
        '{}' "$group"

    # -- Negative: User cannot freeze --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> freeze (403)" "POST" "/api/v1/admin/providers/freeze-manual" "401|403" \
            '{"id":1}' "$group" "$USER_TOKEN"
    fi

    # ==============================
    # Frozen/Expired Instance SSH/SFTP/Exec Blocking Tests
    # ==============================
    if [[ -n "$TEST_INSTANCE_ID" && -n "$USER_TOKEN" ]]; then
        log_info "Testing SSH/SFTP/Exec blocking for frozen instances..."
        local block_group="freeze_block"

        # -- Freeze the instance --
        test_api "Freeze instance for block test" "POST" "/api/v1/admin/instances/freeze" "200" \
            "{\"instanceId\":${TEST_INSTANCE_ID}}" "$block_group"
        sleep 1

        # -- Verify SSH is blocked for frozen instance --
        local ssh_block_code; ssh_block_code=$(curl -s -o /dev/null -w "%{http_code}" \
            -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/instances/${TEST_INSTANCE_ID}/ssh" 2>/dev/null)
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ "$ssh_block_code" == "403" || "$ssh_block_code" == "400" ]]; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "SSH blocked for frozen instance (HTTP ${ssh_block_code})"
            _add_result_json "Frozen instance SSH block" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/ssh" "PASS" "403|400" "$ssh_block_code" "" "$block_group"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_warning "SSH on frozen instance returned HTTP ${ssh_block_code} (expected 403 or 400)"
            _add_result_json "Frozen instance SSH block" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/ssh" "FAIL" "403|400" "$ssh_block_code" "" "$block_group"
        fi

        # -- Verify SFTP list is blocked for frozen instance --
        local sftp_block_code; sftp_block_code=$(curl -s -o /dev/null -w "%{http_code}" \
            -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/instances/${TEST_INSTANCE_ID}/sftp/list?path=/" 2>/dev/null)
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ "$sftp_block_code" == "403" || "$sftp_block_code" == "400" ]]; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "SFTP list blocked for frozen instance (HTTP ${sftp_block_code})"
            _add_result_json "Frozen instance SFTP block" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/sftp/list" "PASS" "403|400" "$sftp_block_code" "" "$block_group"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_warning "SFTP list on frozen instance returned HTTP ${sftp_block_code} (expected 403 or 400)"
            _add_result_json "Frozen instance SFTP block" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/sftp/list" "FAIL" "403|400" "$sftp_block_code" "" "$block_group"
        fi

        # -- Verify Exec WebSocket is blocked for frozen instance --
        local exec_block_code; exec_block_code=$(curl -s -o /dev/null -w "%{http_code}" \
            -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/instances/${TEST_INSTANCE_ID}/exec" 2>/dev/null)
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ "$exec_block_code" == "403" || "$exec_block_code" == "400" ]]; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "Exec WebSocket blocked for frozen instance (HTTP ${exec_block_code})"
            _add_result_json "Frozen instance Exec block" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/exec" "PASS" "403|400" "$exec_block_code" "" "$block_group"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_warning "Exec WebSocket on frozen instance returned HTTP ${exec_block_code} (expected 403 or 400)"
            _add_result_json "Frozen instance Exec block" "GET" "/api/v1/user/instances/${TEST_INSTANCE_ID}/exec" "FAIL" "403|400" "$exec_block_code" "" "$block_group"
        fi

        # -- Verify frozen reason is returned in instance detail --
        local frozen_detail; frozen_detail=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/instances/${TEST_INSTANCE_ID}" 2>/dev/null)
        local is_frozen; is_frozen=$(echo "$frozen_detail" | jq -r '.data.isFrozen // empty' 2>/dev/null)
        local frozen_reason; frozen_reason=$(echo "$frozen_detail" | jq -r '.data.frozenReason // "__empty__"' 2>/dev/null)
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ "$is_frozen" == "true" ]]; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "Instance isFrozen=true, frozenReason=${frozen_reason}"
            _add_result_json "Frozen instance detail fields" "GET" "/api/v1/admin/instances/${TEST_INSTANCE_ID}" "PASS" "isFrozen=true" "isFrozen=${is_frozen} frozenReason=${frozen_reason}" "" "$block_group"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_error "Instance isFrozen expected true, got: ${is_frozen}"
            _add_result_json "Frozen instance detail fields" "GET" "/api/v1/admin/instances/${TEST_INSTANCE_ID}" "FAIL" "isFrozen=true" "isFrozen=${is_frozen}" "" "$block_group"
        fi

        # -- Unfreeze the instance --
        test_api "Unfreeze instance after block test" "POST" "/api/v1/admin/instances/unfreeze" "200" \
            "{\"instanceId\":${TEST_INSTANCE_ID}}" "$block_group"

        log_info "SSH/SFTP/Exec blocking tests for frozen instances completed"
    else
        log_info "Skipping frozen instance blocking tests: no instance or user token"
    fi
}
