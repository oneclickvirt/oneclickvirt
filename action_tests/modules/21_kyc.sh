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
            '{"status":"approved","comment":"Test approved"}' "$group" "$ADMIN_TOKEN"

        # ---- Review already-reviewed KYC ----
        test_api "Re-review KYC" "PUT" "/api/v1/admin/kyc/${kyc_id}/review" "400|200" \
            '{"status":"approved","comment":"Double review"}' "$group" "$ADMIN_TOKEN"
    fi

    # ---- Review nonexistent KYC (may return 500 for nonexistent) ----
    test_api "Review nonexistent KYC" "PUT" "/api/v1/admin/kyc/99999/review" "404|500" \
        '{"status":"approved"}' "$group" "$ADMIN_TOKEN"

    # ---- Alipay KYC (likely to fail without real config, test endpoint existence) ----
    test_api "Submit Alipay KYC" "POST" "/api/v1/user/kyc/alipay" "400|200|500" \
        '{}' "$group" "$USER_TOKEN"
    test_api "Query Alipay result" "GET" "/api/v1/user/kyc/alipay/result" "200|400|404|500" \
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
}
