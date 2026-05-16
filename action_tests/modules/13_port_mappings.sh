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

    local inst_for_pm="${TEST_INSTANCE_ID:-1}"

    # -- Check port availability --
    test_api "Check port (available)" "POST" "/api/v1/admin/ports/check" "200" \
        "{\"providerId\":${PROVIDER_ID},\"hostPort\":25000,\"portCount\":1,\"protocol\":\"tcp\"}" "$group"

    # -- Create port mapping (requires instance; accept 400 if no instances exist) --
    local pm; pm=$(test_api "Create port mapping" "POST" "/api/v1/admin/port-mappings" "200|400" \
        "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"hostPort\":25001}" "$group")
    local pm_id; pm_id=$(echo "$pm" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Create port mapping with mappingType=node (explicit) --
    test_api "Create port mapping (node type)" "POST" "/api/v1/admin/port-mappings" "200|400" \
        "{\"instanceId\":${inst_for_pm},\"guestPort\":8080,\"protocol\":\"tcp\",\"hostPort\":25080,\"mappingType\":\"node\"}" "$group"

    # -- Create port mapping with mappingType=controller --
    local ctrl_pm; ctrl_pm=$(test_api "Create port mapping (controller type)" "POST" "/api/v1/admin/port-mappings" "200|400|500" \
        "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"mappingType\":\"controller\",\"internalHost\":\"10.0.0.1\"}" "$group")
    local ctrl_pm_id; ctrl_pm_id=$(echo "$ctrl_pm" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Verify mappingType field in list response --
    local pm_list; pm_list=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/port-mappings?page=1&pageSize=20" 2>/dev/null)
    local has_mapping_type; has_mapping_type=$(echo "$pm_list" | jq -r '.data.list[0].mappingType // empty' 2>/dev/null)
    if [[ -n "$has_mapping_type" ]]; then
        log_success "mappingType field present in port mapping list: ${has_mapping_type}"
    else
        log_warning "mappingType field missing from port mapping list (may be empty list)"
    fi

    # -- no_port_mapping networkType: node-side mapping must be rejected --
    if [[ -n "$PROVIDER_ID" ]]; then
        # Temporarily set networkType=no_port_mapping
        curl -s --max-time 30 -X PUT \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" -H "Content-Type: application/json" \
            -d '{"networkType":"no_port_mapping"}' \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}/port-config" >/dev/null 2>&1 || true

        test_api "no_port_mapping blocks node mapping" "POST" "/api/v1/admin/port-mappings" "400" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"hostPort\":25100,\"mappingType\":\"node\"}" "$group"

        # controller mode should still be accepted (or 400 if controller func not initialized)
        test_api "no_port_mapping allows controller mapping" "POST" "/api/v1/admin/port-mappings" "200|400|500" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"mappingType\":\"controller\",\"internalHost\":\"10.0.0.1\"}" "$group"

        # Restore networkType=nat_ipv4
        curl -s --max-time 30 -X PUT \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" -H "Content-Type: application/json" \
            -d '{"portRangeStart":20000,"portRangeEnd":30000,"defaultPortCount":10,"networkType":"nat_ipv4"}' \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}/port-config" >/dev/null 2>&1 || true
    fi

    # -- Delete controller port mapping if created --
    if [[ -n "$ctrl_pm_id" ]]; then
        test_api "Delete controller port mapping" "DELETE" "/api/v1/admin/port-mappings/${ctrl_pm_id}" "200" "" "$group"
    fi

    # -- Create duplicate port --
    test_api "Create duplicate port" "POST" "/api/v1/admin/port-mappings" "400|409" \
        "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"hostPort\":25001}" "$group"

    # -- Create with invalid port --
    test_api "Create invalid port (0)" "POST" "/api/v1/admin/port-mappings" "400" \
        "{\"instanceId\":${inst_for_pm},\"guestPort\":0,\"protocol\":\"tcp\"}" "$group"

    # -- Sync port mappings --
    test_api "Sync port mappings" "POST" "/api/v1/admin/port-mappings/sync" "200|400|404" \
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
    test_api "Delete nonexistent mapping" "DELETE" "/api/v1/admin/port-mappings/99999" "404|400" "" "$group"

    # -- Batch delete --
    local batch_ids; batch_ids=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/port-mappings?page=1&pageSize=50" 2>/dev/null | \
        jq -c '[.data.list[]?.id // .data.list[]?.ID] | map(select(. != null))' 2>/dev/null)
    if [[ -n "$batch_ids" && "$batch_ids" != "[]" && "$batch_ids" != "null" ]]; then
        test_api "Batch delete mappings" "POST" "/api/v1/admin/port-mappings/batch-delete" "200" \
            "{\"ids\":${batch_ids}}" "$group"
    fi

    # -- Negative: Create with port out of range --
    test_api "Create port (out of range)" "POST" "/api/v1/admin/port-mappings" "400" \
        "{\"instanceId\":${inst_for_pm},\"guestPort\":70000,\"protocol\":\"tcp\",\"hostPort\":70001}" "$group"

    # -- Negative: Create with negative port --
    test_api "Create port (negative)" "POST" "/api/v1/admin/port-mappings" "400" \
        "{\"instanceId\":${inst_for_pm},\"guestPort\":-1,\"protocol\":\"tcp\",\"hostPort\":-1}" "$group"

    # -- Negative: Check port with invalid protocol --
    test_api "Check port (invalid proto)" "POST" "/api/v1/admin/ports/check" "400" \
        "{\"providerId\":${PROVIDER_ID},\"hostPort\":25000,\"portCount\":1,\"protocol\":\"invalid\"}" "$group"

    # -- Negative: Sync with nonexistent provider --
    test_api "Sync (nonexistent provider)" "POST" "/api/v1/admin/port-mappings/sync" "200|400|404" \
        '{"providerIds":[99999]}' "$group"

    # -- Negative: Batch delete empty --
    test_api "Batch delete (empty)" "POST" "/api/v1/admin/port-mappings/batch-delete" "400" \
        '{"ids":[]}' "$group"

    # -- Negative: Create for nonexistent instance --
    test_api "Create port (no instance)" "POST" "/api/v1/admin/port-mappings" "400|404" \
        '{"instanceId":99999,"guestPort":22,"protocol":"tcp","hostPort":25555}' "$group"

    # -- Negative: User cannot manage port mappings --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> create mapping (403)" "POST" "/api/v1/admin/port-mappings" "401|403" \
            '{"instanceId":1,"guestPort":22,"protocol":"tcp","hostPort":25001}' "$group" "$USER_TOKEN"
    fi
}
