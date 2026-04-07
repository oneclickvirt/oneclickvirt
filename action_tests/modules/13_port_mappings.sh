#!/bin/bash
# Module 13: Port Mapping Management
# Dependencies: 09_providers (PROVIDER_ID)

run_module_13() {
    report_add_section "13 - Port Mappings"
    local group="port_mappings"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider"
        return 1
    fi

    # -- Admin port mapping list --
    test_api "Port mapping list" "GET" "/api/v1/admin/port-mappings?page=1&pageSize=10" "200" "" "$group"

    # -- Check port availability --
    test_api "Check port (available)" "POST" "/api/v1/admin/ports/check" "200" \
        "{\"providerId\":${PROVIDER_ID},\"hostPort\":25000,\"protocol\":\"tcp\"}" "$group"

    # -- Create port mapping (requires instance; accept 400 if no instances exist) --
    local pm; pm=$(test_api "Create port mapping" "POST" "/api/v1/admin/port-mappings" "200|400" \
        "{\"instanceId\":1,\"guestPort\":22,\"protocol\":\"tcp\",\"hostPort\":25001}" "$group")
    local pm_id; pm_id=$(echo "$pm" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Create duplicate port --
    test_api "Create duplicate port" "POST" "/api/v1/admin/port-mappings" "400" \
        "{\"instanceId\":1,\"guestPort\":22,\"protocol\":\"tcp\",\"hostPort\":25001}" "$group"

    # -- Create with invalid port --
    test_api "Create invalid port (0)" "POST" "/api/v1/admin/port-mappings" "400" \
        "{\"instanceId\":1,\"guestPort\":0,\"protocol\":\"tcp\"}" "$group"

    # -- Sync port mappings --
    test_api "Sync port mappings" "POST" "/api/v1/admin/port-mappings/sync" "200" \
        "{\"providerIds\":[${PROVIDER_ID}]}" "$group"

    # -- User port mappings --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User port mappings" "GET" "/api/v1/user/port-mappings" "200" "" "$group" "$USER_TOKEN"
    fi

    # -- Delete single --
    if [[ -n "$pm_id" ]]; then
        test_api "Delete port mapping" "DELETE" "/api/v1/admin/port-mappings/${pm_id}" "200" "" "$group"
    fi

    # -- Delete nonexistent --
    test_api "Delete nonexistent mapping" "DELETE" "/api/v1/admin/port-mappings/99999" "404|500" "" "$group"

    # -- Batch delete --
    local batch_ids; batch_ids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/port-mappings?page=1&pageSize=50" 2>/dev/null | \
        jq -c '[.data.list[]?.id // .data.list[]?.ID] | map(select(. != null))' 2>/dev/null)
    if [[ -n "$batch_ids" && "$batch_ids" != "[]" && "$batch_ids" != "null" ]]; then
        test_api "Batch delete mappings" "POST" "/api/v1/admin/port-mappings/batch-delete" "200" \
            "{\"ids\":${batch_ids}}" "$group"
    fi
}
