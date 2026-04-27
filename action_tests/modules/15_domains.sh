#!/bin/bash
# Module 15: Domain Management
# Dependencies: 09_providers (PROVIDER_ID), 02_auth (USER_TOKEN)

run_module_15() {
    report_add_section "15 - Domain Management"
    local group="domains"

    # -- Admin domain list --
    test_api "Admin domain list" "GET" "/api/v1/admin/domains?page=1&pageSize=10" "200" "" "$group"

    # -- Domain config at provider level (must be enabled before user domain creation) --
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Get domain config" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" "" "$group"
        test_api "Update domain config" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" \
            '{"enabled":true,"base_domain":"test.example.com"}' "$group"
    fi

    # -- User domains --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User domain list" "GET" "/api/v1/user/domains" "200" "" "$group" "$USER_TOKEN"

        local inst_for_domain="${TEST_INSTANCE_ID:-1}"

        # -- Create domain (requires instanceId; may fail if no instances exist) --
        local d1; d1=$(test_api "Create user domain" "POST" "/api/v1/user/domains" "200|400|404" \
            "{\"instanceId\":${inst_for_domain},\"domainName\":\"ci-test.example.com\",\"protocol\":\"http\",\"internalIP\":\"127.0.0.1\",\"internalPort\":80}" "$group" "$USER_TOKEN")
        local did1; did1=$(echo "$d1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

        # -- Create duplicate --
        test_api "Create duplicate domain" "POST" "/api/v1/user/domains" "400|404|409" \
            "{\"instanceId\":${inst_for_domain},\"domainName\":\"ci-test.example.com\",\"protocol\":\"http\",\"internalIP\":\"127.0.0.1\",\"internalPort\":80}" "$group" "$USER_TOKEN"

        # -- Create with invalid domain --
        test_api "Create invalid domain" "POST" "/api/v1/user/domains" "400" \
            '{"domainName":"","internalPort":80}' "$group" "$USER_TOKEN"

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

    # -- Admin delete --
    local admin_dids; admin_dids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/domains?page=1&pageSize=50" 2>/dev/null | \
        jq -r '.data.list[0].id // .data.list[0].ID // empty' 2>/dev/null)
    if [[ -n "$admin_dids" ]]; then
        test_api "Admin delete domain" "DELETE" "/api/v1/admin/domains/${admin_dids}" "200" "" "$group"
    fi

    # -- Negative: Delete nonexistent domain --
    test_api "Delete nonexistent domain" "DELETE" "/api/v1/admin/domains/99999" "200|400|404" "" "$group"

    # -- Negative: User delete nonexistent domain --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User delete nonexistent" "DELETE" "/api/v1/user/domains/99999" "400|404" "" "$group" "$USER_TOKEN"
    fi

    # -- Negative: User2 cannot edit/delete user1 domain --
    if [[ -n "$USER_TOKEN" && -n "$USER_TOKEN2" ]]; then
        # Create a domain for user1
        local inst_for_d="${TEST_INSTANCE_ID:-1}"
        local d_iso; d_iso=$(test_api "User1 create for isolation" "POST" "/api/v1/user/domains" "200|400|404|409" \
            "{\"instanceId\":${inst_for_d},\"domainName\":\"iso-test.example.com\",\"protocol\":\"http\",\"internalIP\":\"127.0.0.1\",\"internalPort\":80}" "$group" "$USER_TOKEN")
        local d_iso_id; d_iso_id=$(echo "$d_iso" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
        if [[ -n "$d_iso_id" ]]; then
            test_api "User2 edit user1 domain" "PUT" "/api/v1/user/domains/${d_iso_id}" "400|403|404" \
                '{"target_port":9999}' "$group" "$USER_TOKEN2"
            test_api "User2 delete user1 domain" "DELETE" "/api/v1/user/domains/${d_iso_id}" "400|403|404" "" "$group" "$USER_TOKEN2"
            # Cleanup
            test_api "Cleanup isolation domain" "DELETE" "/api/v1/user/domains/${d_iso_id}" "200" "" "$group" "$USER_TOKEN"
        fi
    fi

    # -- Negative: Create domain with excessively long name --
    if [[ -n "$USER_TOKEN" ]]; then
        local long_domain; long_domain=$(printf 'a%.0s' {1..256})
        test_api "Create long domain" "POST" "/api/v1/user/domains" "400|404" \
            "{\"instanceId\":1,\"domainName\":\"${long_domain}.example.com\",\"protocol\":\"http\",\"internalIP\":\"127.0.0.1\",\"internalPort\":80}" "$group" "$USER_TOKEN"
    fi
}
