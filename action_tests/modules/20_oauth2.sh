#!/bin/bash
# Module 20: OAuth2 Provider Management
# Dependencies: 01_init (ADMIN_TOKEN)

run_module_20() {
    report_add_section "20 - OAuth2"
    local group="oauth2"

    if [[ -z "$ADMIN_TOKEN" ]]; then
        chain_break "$group" "No admin token"
        return 1
    fi

    # ---- Presets ----
    test_api "List OAuth2 presets" "GET" "/api/v1/oauth2/presets" "200" "" "$group" "$ADMIN_TOKEN"
    test_api "Get preset (github)" "GET" "/api/v1/oauth2/presets/github" "200|404" "" "$group" "$ADMIN_TOKEN"
    test_api "Get preset (nonexistent)" "GET" "/api/v1/oauth2/presets/nonexistent_provider" "404" "" "$group" "$ADMIN_TOKEN"

    # ---- Create OAuth2 provider (with all required fields) ----
    local create_resp; create_resp=$(test_api "Create OAuth2 provider" "POST" "/api/v1/oauth2/providers" "200|201" \
        '{"name":"test_github","displayName":"Test GitHub","providerType":"preset","clientId":"test_client_id","clientSecret":"test_client_secret","redirectUrl":"http://localhost:8888/oauth2/callback","authUrl":"https://github.com/login/oauth/authorize","tokenUrl":"https://github.com/login/oauth/access_token","userInfoUrl":"https://api.github.com/user","enabled":true}' \
        "$group" "$ADMIN_TOKEN")
    local oauth2_id; oauth2_id=$(echo "$create_resp" | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)

    # ---- Create duplicate ----
    test_api "Create duplicate OAuth2" "POST" "/api/v1/oauth2/providers" "400|409" \
        '{"name":"test_github","displayName":"Test GitHub Dup","providerType":"preset","clientId":"dup","clientSecret":"dup","redirectUrl":"http://localhost:8888/oauth2/callback","authUrl":"https://github.com/login/oauth/authorize","tokenUrl":"https://github.com/login/oauth/access_token","userInfoUrl":"https://api.github.com/user","enabled":true}' \
        "$group" "$ADMIN_TOKEN"

    # ---- Create with missing fields ----
    test_api "Create OAuth2 missing fields" "POST" "/api/v1/oauth2/providers" "400" \
        '{"name":""}' "$group" "$ADMIN_TOKEN"

    # ---- List providers ----
    test_api "List OAuth2 providers" "GET" "/api/v1/oauth2/providers" "200" "" "$group" "$ADMIN_TOKEN"

    # ---- Public providers list ----
    test_api "Public OAuth2 providers" "GET" "/api/v1/public/oauth2/providers" "200" "" "$group" ""

    if [[ -n "$oauth2_id" ]]; then
        # ---- Get single provider ----
        test_api "Get OAuth2 provider" "GET" "/api/v1/oauth2/providers/${oauth2_id}" "200" "" "$group" "$ADMIN_TOKEN"

        # ---- Update provider ----
        test_api "Update OAuth2 provider" "PUT" "/api/v1/oauth2/providers/${oauth2_id}" "200" \
            '{"name":"test_github_updated","displayName":"Updated GitHub","providerType":"preset","clientId":"updated_id","clientSecret":"updated_secret","enabled":false}' \
            "$group" "$ADMIN_TOKEN"

        # ---- Reset registration count ----
        test_api "Reset OAuth2 count" "POST" "/api/v1/oauth2/providers/${oauth2_id}/reset-count" "200" \
            '' "$group" "$ADMIN_TOKEN"

        # ---- Delete provider ----
        test_api "Delete OAuth2 provider" "DELETE" "/api/v1/oauth2/providers/${oauth2_id}" "200" "" "$group" "$ADMIN_TOKEN"

        # ---- Get deleted (404) ----
        test_api "Get deleted OAuth2 (404)" "GET" "/api/v1/oauth2/providers/${oauth2_id}" "404" "" "$group" "$ADMIN_TOKEN"
    fi

    # ---- Delete nonexistent ----
    test_api "Delete nonexistent OAuth2" "DELETE" "/api/v1/oauth2/providers/99999" "404" "" "$group" "$ADMIN_TOKEN"

    # ---- User cannot manage OAuth2 ----
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> OAuth2 create (401/403)" "POST" "/api/v1/oauth2/providers" "401|403" \
            '{"name":"user_test","providerType":"preset","clientId":"x","clientSecret":"x"}' "$group" "$USER_TOKEN"
        test_api "User -> OAuth2 list (401/403)" "GET" "/api/v1/oauth2/providers" "401|403" "" "$group" "$USER_TOKEN"
        test_api "User -> OAuth2 delete (401/403)" "DELETE" "/api/v1/oauth2/providers/1" "401|403" "" "$group" "$USER_TOKEN"
    fi

    # ---- Negative: Update nonexistent provider ----
    test_api "Update nonexistent OAuth2" "PUT" "/api/v1/oauth2/providers/99999" "400|404" \
        '{"name":"ghost","displayName":"Ghost"}' "$group" "$ADMIN_TOKEN"

    # ---- Negative: Reset count nonexistent ----
    test_api "Reset count nonexistent" "POST" "/api/v1/oauth2/providers/99999/reset-count" "400|404" \
        '' "$group" "$ADMIN_TOKEN"

    # ---- Negative: Create with invalid redirect URL ----
    test_api "Create OAuth2 (invalid URL)" "POST" "/api/v1/oauth2/providers" "400|200" \
        '{"name":"bad_url","displayName":"Bad URL","providerType":"custom","clientId":"x","clientSecret":"x","redirectUrl":"not_a_url","authUrl":"not_a_url","tokenUrl":"not_a_url","userInfoUrl":"not_a_url","enabled":true}' \
        "$group" "$ADMIN_TOKEN"

    # ---- Negative: Create with XSS in name ----
    test_api "Create OAuth2 (XSS)" "POST" "/api/v1/oauth2/providers" "400|200" \
        '{"name":"<script>","displayName":"<img onerror=alert(1)>","providerType":"custom","clientId":"x","clientSecret":"x","redirectUrl":"http://localhost/cb","authUrl":"http://a.com","tokenUrl":"http://t.com","userInfoUrl":"http://u.com","enabled":true}' \
        "$group" "$ADMIN_TOKEN"

    # -- Cleanup: delete test OAuth2 providers created by XSS/bad_url tests --
    local cleanup_ids; cleanup_ids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/oauth2/providers" 2>/dev/null | \
        jq -r '.data[]? | select(.name | test("bad_url|<script>|test_github")) | .id' 2>/dev/null)
    for cid in $cleanup_ids; do
        curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            -X DELETE "${SERVER_URL}/api/v1/oauth2/providers/${cid}" 2>/dev/null || true
    done
}
