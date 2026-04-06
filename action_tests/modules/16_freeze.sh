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
        "{\"provider_id\":${PROVIDER_ID},\"expires_at\":\"${exp}\"}" "$group"

    # -- Manual freeze provider --
    test_api "Manual freeze provider" "POST" "/api/v1/admin/providers/freeze-manual" "200" \
        "{\"provider_id\":${PROVIDER_ID},\"reason\":\"CI test freeze\"}" "$group"

    # -- Verify frozen status --
    local ps; ps=$(test_api "Provider status (frozen)" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "200" "" "$group")
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    local frozen; frozen=$(echo "$ps" | jq -r '.data.frozen // .data.is_frozen // empty' 2>/dev/null)
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
        "{\"provider_id\":${PROVIDER_ID}}" "$group"

    # -- Manual unfreeze --
    test_api "Manual unfreeze provider" "POST" "/api/v1/admin/providers/unfreeze-manual" "200" \
        "{\"provider_id\":${PROVIDER_ID}}" "$group"

    # -- Cascade unfreeze --
    test_api "Cascade unfreeze" "POST" "/api/v1/admin/providers/unfreeze" "200" \
        "{\"provider_id\":${PROVIDER_ID}}" "$group"

    # -- Verify unfrozen --
    test_api "Provider status (unfrozen)" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "200" "" "$group"

    # -- Freeze nonexistent provider --
    test_api "Freeze nonexistent provider" "POST" "/api/v1/admin/providers/freeze-manual" "404" \
        '{"provider_id":99999,"reason":"test"}' "$group"

    # -- Instance freeze/unfreeze without instance --
    test_api "Freeze nonexistent instance" "POST" "/api/v1/admin/instances/freeze" "404" \
        '{"instance_id":99999}' "$group"
}
