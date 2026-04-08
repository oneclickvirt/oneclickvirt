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
}
