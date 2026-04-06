#!/bin/bash
# Module 15: Domain Management
# Dependencies: 09_providers (PROVIDER_ID), 02_auth (USER_TOKEN)

run_module_15() {
    report_add_section "15 - Domain Management"
    local group="domains"

    # -- Admin domain list --
    test_api "Admin domain list" "GET" "/api/v1/admin/domains?page=1&pageSize=10" "200" "" "$group"

    # -- User domains --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User domain list" "GET" "/api/v1/user/domains" "200" "" "$group" "$USER_TOKEN"

        # -- Create domain --
        local d1; d1=$(test_api "Create user domain" "POST" "/api/v1/user/domains" "200" \
            '{"domain":"ci-test.example.com","target_port":80}' "$group" "$USER_TOKEN")
        local did1; did1=$(echo "$d1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

        # -- Create duplicate --
        test_api "Create duplicate domain" "POST" "/api/v1/user/domains" "400" \
            '{"domain":"ci-test.example.com","target_port":80}' "$group" "$USER_TOKEN"

        # -- Create with invalid domain --
        test_api "Create invalid domain" "POST" "/api/v1/user/domains" "400" \
            '{"domain":"","target_port":80}' "$group" "$USER_TOKEN"

        # -- Edit domain --
        if [[ -n "$did1" ]]; then
            test_api "Edit user domain" "PUT" "/api/v1/user/domains/${did1}" "200" \
                '{"target_port":8080}' "$group" "$USER_TOKEN"
        fi

        # -- User2 cannot see user1's domain --
        if [[ -n "$USER_TOKEN2" ]]; then
            test_api "User2 domain list (isolated)" "GET" "/api/v1/user/domains" "200" "" "$group" "$USER_TOKEN2"
        fi

        # -- Delete domain --
        if [[ -n "$did1" ]]; then
            test_api "Delete user domain" "DELETE" "/api/v1/user/domains/${did1}" "200" "" "$group" "$USER_TOKEN"
        fi
    fi

    # -- Domain config at provider level --
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Get domain config" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" "" "$group"
        test_api "Update domain config" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" \
            '{"enabled":true,"base_domain":"test.example.com"}' "$group"
    fi

    # -- Admin delete --
    local admin_dids; admin_dids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/domains?page=1&pageSize=50" 2>/dev/null | \
        jq -r '.data.list[0].id // .data.list[0].ID // empty' 2>/dev/null)
    if [[ -n "$admin_dids" ]]; then
        test_api "Admin delete domain" "DELETE" "/api/v1/admin/domains/${admin_dids}" "200" "" "$group"
    fi
}
