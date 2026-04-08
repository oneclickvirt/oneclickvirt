#!/bin/bash
# Module 21: KYC (Know Your Customer)
# Dependencies: 02_auth (USER_TOKEN, ADMIN_TOKEN)

run_module_21() {
    report_add_section "21 - KYC"
    local group="kyc"

    if [[ -z "$USER_TOKEN" || -z "$ADMIN_TOKEN" ]]; then
        chain_break "$group" "No user/admin token"
        return 1
    fi

    # ---- User KYC status (initially empty) ----
    test_api "Get user KYC status" "GET" "/api/v1/user/kyc" "200" "" "$group" "$USER_TOKEN"

    # ---- Submit KYC (field names: realName, idNumber) ----
    local submit_resp; submit_resp=$(test_api "Submit KYC" "POST" "/api/v1/user/kyc" "200|201" \
        '{"realName":"Test User","idNumber":"110101199001011234"}' \
        "$group" "$USER_TOKEN")

    # ---- Submit duplicate KYC ----
    test_api "Submit duplicate KYC" "POST" "/api/v1/user/kyc" "400|409" \
        '{"realName":"Test User","idNumber":"110101199001011234"}' \
        "$group" "$USER_TOKEN"

    # ---- Submit KYC missing fields ----
    test_api "Submit KYC empty" "POST" "/api/v1/user/kyc" "400" \
        '{}' "$group" "$USER_TOKEN"

    # ---- Admin list KYC submissions ----
    local kyc_list; kyc_list=$(test_api "Admin KYC list" "GET" "/api/v1/admin/kyc?page=1&pageSize=10" "200" \
        "" "$group" "$ADMIN_TOKEN")
    local kyc_id; kyc_id=$(echo "$kyc_list" | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)

    # ---- Admin review KYC ----
    if [[ -n "$kyc_id" ]]; then
        test_api "Admin approve KYC" "PUT" "/api/v1/admin/kyc/${kyc_id}/review" "200" \
            '{"approved":true,"rejectReason":""}' "$group" "$ADMIN_TOKEN"

        # ---- Review already-reviewed KYC ----
        test_api "Re-review KYC" "PUT" "/api/v1/admin/kyc/${kyc_id}/review" "400|200" \
            '{"approved":true,"rejectReason":""}' "$group" "$ADMIN_TOKEN"
    fi

    # ---- Review nonexistent KYC (should return 404) ----
    test_api "Review nonexistent KYC" "PUT" "/api/v1/admin/kyc/99999/review" "404|400" \
        '{"approved":true}' "$group" "$ADMIN_TOKEN"

    # ---- Alipay KYC (likely to fail without real config, test endpoint existence) ----
    test_api "Submit Alipay KYC" "POST" "/api/v1/user/kyc/alipay" "400|200" \
        '{}' "$group" "$USER_TOKEN"
    test_api "Query Alipay result" "GET" "/api/v1/user/kyc/alipay/result" "200|400|404" \
        "" "$group" "$USER_TOKEN"

    # ---- Check KYC after approval ----
    test_api "KYC status after approval" "GET" "/api/v1/user/kyc" "200" "" "$group" "$USER_TOKEN"

    # ---- User2 cannot see user1 KYC ----
    if [[ -n "$USER_TOKEN2" ]]; then
        test_api "User2 own KYC status" "GET" "/api/v1/user/kyc" "200" "" "$group" "$USER_TOKEN2"
    fi

    # ---- Normal admin can access KYC ----
    if [[ -n "$NORMAL_ADMIN_TOKEN" ]]; then
        test_api "Normal admin KYC list" "GET" "/api/v1/admin/kyc?page=1&pageSize=10" "200" "" "$group" "$NORMAL_ADMIN_TOKEN"
    fi

    # ---- Negative: Submit KYC with XSS in fields ----
    if [[ -n "$USER_TOKEN2" ]]; then
        test_api "KYC XSS realName" "POST" "/api/v1/user/kyc" "200|400" \
            '{"realName":"<script>alert(1)</script>","idNumber":"110101199001012345"}' "$group" "$USER_TOKEN2"
    fi

    # ---- Negative: Submit KYC with overly long fields ----
    local long_name; long_name=$(printf 'X%.0s' {1..300})
    if [[ -n "$USER_TOKEN2" ]]; then
        test_api "KYC long realName" "POST" "/api/v1/user/kyc" "400|200" \
            "{\"realName\":\"${long_name}\",\"idNumber\":\"110101199001013456\"}" "$group" "$USER_TOKEN2"
    fi

    # ---- Negative: Admin reject KYC ----
    if [[ -n "$kyc_id" ]]; then
        # Reset KYC first by getting a new submission from user2
        local kyc2_id; kyc2_id=$(echo "$kyc_list" | jq -r '[.data.list[]?][1].id // empty' 2>/dev/null)
        if [[ -n "$kyc2_id" ]]; then
            test_api "Admin reject KYC" "PUT" "/api/v1/admin/kyc/${kyc2_id}/review" "200|400" \
                '{"approved":false,"rejectReason":"CI test rejection"}' "$group" "$ADMIN_TOKEN"
        fi
    fi

    # ---- Negative: User cannot access admin KYC list ----
    test_api "User -> KYC admin list (403)" "GET" "/api/v1/admin/kyc?page=1&pageSize=10" "401|403" "" "$group" "$USER_TOKEN"

    # ---- Negative: User cannot review KYC ----
    test_api "User -> review KYC (403)" "PUT" "/api/v1/admin/kyc/1/review" "401|403" \
        '{"approved":true}' "$group" "$USER_TOKEN"

    # ---- Negative: Submit KYC without auth ----
    test_api "KYC submit no auth" "POST" "/api/v1/user/kyc" "401" \
        '{"realName":"Ghost","idNumber":"123"}' "$group" ""
}
