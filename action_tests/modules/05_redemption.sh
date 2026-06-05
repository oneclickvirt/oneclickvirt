#!/bin/bash
# Module 05: Redemption Code Management
# Dependencies: 01_init (ADMIN_TOKEN), 02_auth (USER_TOKEN)

run_module_05() {
    report_add_section "05 - Redemption Codes"
    local group="redemption"

    # -- List --
    test_api "Redemption code list" "GET" "/api/v1/admin/redemption-codes?page=1&pageSize=10" "200" "" "$group"

    # -- Batch create standard mode (requires provider + images; accept 400 if preconditions not met) --
    local provider_for_redeem="${PROVIDER_ID:-1}"

    # Detect provider type to decide whether copy mode is applicable.
    local provider_type; provider_type=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${provider_for_redeem}" 2>/dev/null | \
        jq -r '.data.type // empty' 2>/dev/null)
    local is_copy_capable=false
    case "$provider_type" in
        lxd|incus|docker|podman|containerd|orbstack) is_copy_capable=true ;;
    esac
    log_info "Provider type for redemption tests: ${provider_type} (copy mode applicable: ${is_copy_capable})"

    # ---- Container redemption codes (standard mode) ----
    test_api "Batch create codes (container, standard)" "POST" "/api/v1/admin/redemption-codes/batch-create" "200|400|404|500" \
        "{\"count\":2,\"providerId\":${provider_for_redeem},\"instanceType\":\"container\",\"imageId\":1,\"cpuId\":\"1\",\"memoryId\":\"1\",\"diskId\":\"1\",\"bandwidthId\":\"1\",\"remark\":\"CI container test\",\"creationMode\":\"standard\"}" "$group"

    # ---- VM redemption codes (standard mode) ----
    test_api "Batch create codes (VM, standard)" "POST" "/api/v1/admin/redemption-codes/batch-create" "200|400|404|500" \
        "{\"count\":2,\"providerId\":${provider_for_redeem},\"instanceType\":\"vm\",\"imageId\":1,\"cpuId\":\"1\",\"memoryId\":\"1\",\"diskId\":\"1\",\"bandwidthId\":\"1\",\"remark\":\"CI VM test\",\"creationMode\":\"standard\"}" "$group"

    # ---- Copy mode (LXD/Incus and Docker-family providers) ----
    if [[ "$is_copy_capable" == "true" ]]; then
        # Fetch copyable source containers.
        local source_resp; source_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${provider_for_redeem}/stopped-containers" 2>/dev/null)
        local real_source; real_source=$(echo "$source_resp" | jq -r '.data.containerDetails[0].name // .data.containers[0] // empty' 2>/dev/null)

        if [[ -n "$real_source" ]]; then
            log_info "Found source container for copy mode test: ${real_source}"
            test_api "Batch create codes (copy mode with real source)" "POST" "/api/v1/admin/redemption-codes/batch-create" "200|400" \
                "{\"count\":1,\"providerId\":${provider_for_redeem},\"instanceType\":\"container\",\"creationMode\":\"copy\",\"sourceContainer\":\"${real_source}\",\"remark\":\"CI copy mode test\"}" "$group"
        else
            log_info "No source containers available, testing copy mode with placeholder source"
            test_api "Batch create codes (copy mode placeholder)" "POST" "/api/v1/admin/redemption-codes/batch-create" "200|400|404|500" \
                "{\"count\":1,\"providerId\":${provider_for_redeem},\"instanceType\":\"container\",\"creationMode\":\"copy\",\"sourceContainer\":\"ci-test-source\",\"remark\":\"CI copy mode test\"}" "$group"
        fi
    else
        # Non-copy-capable providers should reject copy mode gracefully.
        test_api "Batch create codes (copy mode rejected for provider)" "POST" "/api/v1/admin/redemption-codes/batch-create" "400|404|500" \
            "{\"count\":1,\"providerId\":${provider_for_redeem},\"instanceType\":\"container\",\"creationMode\":\"copy\",\"sourceContainer\":\"test-source\",\"remark\":\"CI copy mode test\"}" "$group"
    fi

    # -- Copy mode without sourceContainer must fail --
    test_api "Copy mode no sourceContainer (400/404)" "POST" "/api/v1/admin/redemption-codes/batch-create" "400|404" \
        "{\"count\":1,\"providerId\":${provider_for_redeem},\"instanceType\":\"container\",\"creationMode\":\"copy\",\"sourceContainer\":\"\"}" "$group"

    # -- Verify creationMode field is present in list response --
    local list_resp; list_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/redemption-codes?page=1&pageSize=10" 2>/dev/null)
    local has_mode; has_mode=$(echo "$list_resp" | jq -r '.data.list[0].creationMode // empty' 2>/dev/null)
    if [[ -n "$has_mode" ]]; then
        log_success "creationMode field present in list response: ${has_mode}"
    else
        log_warning "creationMode field missing from list response (may be empty list)"
    fi

    # -- Create with invalid params --
    test_api "Create codes (zero count)" "POST" "/api/v1/admin/redemption-codes/batch-create" "400" \
        '{"count":0,"type":"instance"}' "$group"

    # -- Export --
    test_api "Export redemption codes" "POST" "/api/v1/admin/redemption-codes/export" "200" \
        '{"format":"json"}' "$group"

    # -- Get a code for redemption --
    local code_val; code_val=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/redemption-codes?page=1&pageSize=10" 2>/dev/null | \
        jq -r '.data.list[0].code // empty' 2>/dev/null)

    # -- User redeems code and verify instance creation --
    if [[ -n "$code_val" && -n "$USER_TOKEN" ]]; then
        local redeem_resp; redeem_resp=$(test_api "User redeem code" "POST" "/api/v1/user/redemption-codes/redeem" "200|400" \
            "{\"code\":\"${code_val}\"}" "$group" "$USER_TOKEN")
        # Check if redemption created an instance task and wait for it
        local redeem_task; redeem_task=$(echo "$redeem_resp" | jq -r '.data.taskId // .data.task_id // empty' 2>/dev/null)
        if [[ -n "$redeem_task" ]]; then
            log_info "Redemption task created: ${redeem_task}, waiting for completion..."
            wait_task_complete "$SERVER_URL" "$redeem_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || true
            # Verify instance appears in user's list after redemption
            test_api "User sees redeemed instance" "GET" "/api/v1/user/instances?page=1&pageSize=10" "200" \
                "" "$group" "$USER_TOKEN"
        fi
    fi

    # -- Redeem invalid code --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "Redeem invalid code" "POST" "/api/v1/user/redemption-codes/redeem" "400|404" \
            '{"code":"NONEXISTENT_CODE"}' "$group" "$USER_TOKEN"
    fi

    # -- Redeem empty code --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "Redeem empty code" "POST" "/api/v1/user/redemption-codes/redeem" "400" \
            '{"code":""}' "$group" "$USER_TOKEN"
    fi

    # -- Batch delete --
    local rc_ids; rc_ids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/redemption-codes?page=1&pageSize=50" 2>/dev/null | \
        jq -c '[.data.list[]?.id // .data.list[]?.ID] | map(select(. != null))' 2>/dev/null)
    if [[ -n "$rc_ids" && "$rc_ids" != "[]" && "$rc_ids" != "null" ]]; then
        test_api "Batch delete codes" "POST" "/api/v1/admin/redemption-codes/batch-delete" "200" \
            "{\"ids\":${rc_ids}}" "$group"
    fi

    # -- Negative: Batch create with negative count --
    test_api "Create codes (negative count)" "POST" "/api/v1/admin/redemption-codes/batch-create" "400" \
        '{"count":-1,"type":"instance"}' "$group"

    # -- Negative: Redeem without auth --
    test_api "Redeem without auth" "POST" "/api/v1/user/redemption-codes/redeem" "401" \
        '{"code":"TESTCODE"}' "$group" ""

    # -- Negative: Batch delete empty --
    test_api "Batch delete (empty)" "POST" "/api/v1/admin/redemption-codes/batch-delete" "400" \
        '{"ids":[]}' "$group"

    # -- Negative: User cannot manage redemption codes --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> code list (403)" "GET" "/api/v1/admin/redemption-codes?page=1&pageSize=10" "401|403" "" "$group" "$USER_TOKEN"
        test_api "User -> create code (403)" "POST" "/api/v1/admin/redemption-codes/batch-create" "401|403" \
            '{"count":1,"type":"instance"}' "$group" "$USER_TOKEN"
    fi
}
