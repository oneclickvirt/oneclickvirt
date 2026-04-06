#!/bin/bash
# Module 22: Checkin System
# Dependencies: 02_auth (USER_TOKEN), 09_providers (PROVIDER_ID), 10_instances (TEST_INSTANCE_ID)

run_module_22() {
    report_add_section "22 - Checkin"
    local group="checkin"

    if [[ -z "$USER_TOKEN" || -z "$ADMIN_TOKEN" ]]; then
        chain_break "$group" "No user/admin token"
        return 1
    fi

    # ---- Admin: Configure checkin for provider ----
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Get checkin config" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/checkin-config" "200" \
            "" "$group" "$ADMIN_TOKEN"
        test_api "Update checkin config" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/checkin-config" "200" \
            '{"enabled":true,"interval_hours":24,"extend_hours":48,"max_consecutive":30}' \
            "$group" "$ADMIN_TOKEN"
    fi

    # ---- User: Generate checkin code ----
    if [[ -n "$TEST_INSTANCE_ID" ]]; then
        local code_resp; code_resp=$(test_api "Generate checkin code" "POST" \
            "/api/v1/user/checkin/code/${TEST_INSTANCE_ID}" "200" '' "$group" "$USER_TOKEN")
        local checkin_code; checkin_code=$(echo "$code_resp" | grep -o '"code":"[^"]*"' | head -1 | cut -d'"' -f4)

        # ---- Perform checkin ----
        if [[ -n "$checkin_code" ]]; then
            test_api "Perform checkin" "POST" "/api/v1/user/checkin" "200" \
                '{"code":"'"$checkin_code"'","instance_id":'"$TEST_INSTANCE_ID"'}' "$group" "$USER_TOKEN"
        else
            test_api "Perform checkin (no code)" "POST" "/api/v1/user/checkin" "200|400" \
                '{"instance_id":'"$TEST_INSTANCE_ID"'}' "$group" "$USER_TOKEN"
        fi

        # ---- Checkin with invalid code ----
        test_api "Checkin invalid code" "POST" "/api/v1/user/checkin" "400" \
            '{"code":"INVALID_CODE_XYZ","instance_id":'"$TEST_INSTANCE_ID"'}' "$group" "$USER_TOKEN"
    fi

    # ---- Checkin for nonexistent instance ----
    test_api "Generate code nonexistent" "POST" "/api/v1/user/checkin/code/99999" "404|400" \
        '' "$group" "$USER_TOKEN"

    # ---- Checkin empty body ----
    test_api "Checkin empty body" "POST" "/api/v1/user/checkin" "400" \
        '{}' "$group" "$USER_TOKEN"

    # ---- Get checkin records ----
    test_api "Checkin records" "GET" "/api/v1/user/checkin/records" "200" "" "$group" "$USER_TOKEN"

    # ---- User2 checkin records (isolated) ----
    if [[ -n "$USER_TOKEN2" ]]; then
        test_api "User2 checkin records" "GET" "/api/v1/user/checkin/records" "200" "" "$group" "$USER_TOKEN2"
    fi

    # ---- Unauthenticated ----
    test_api "No auth checkin (401)" "POST" "/api/v1/user/checkin" "401" '{}' "$group" ""
}
