#!/bin/bash
# Module 05: Redemption Code Management
# Dependencies: 01_init (ADMIN_TOKEN), 02_auth (USER_TOKEN)

run_module_05() {
    report_add_section "05 - Redemption Codes"
    local group="redemption"

    # -- List --
    test_api "Redemption code list" "GET" "/api/v1/admin/redemption-codes?page=1&pageSize=10" "200" "" "$group"

    # -- Batch create (requires provider + images; accept 400 if preconditions not met) --
    local provider_for_redeem="${PROVIDER_ID:-1}"
    local rc; rc=$(test_api "Batch create codes" "POST" "/api/v1/admin/redemption-codes/batch-create" "200|400|404|500" \
        "{\"count\":3,\"providerId\":${provider_for_redeem},\"instanceType\":\"container\",\"imageId\":1,\"cpuId\":\"1\",\"memoryId\":\"1\",\"diskId\":\"1\",\"bandwidthId\":\"1\",\"remark\":\"CI test\"}" "$group")

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

    # -- User redeems code --
    if [[ -n "$code_val" && -n "$USER_TOKEN" ]]; then
        test_api "User redeem code" "POST" "/api/v1/user/redemption-codes/redeem" "200" \
            "{\"code\":\"${code_val}\"}" "$group" "$USER_TOKEN"
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
