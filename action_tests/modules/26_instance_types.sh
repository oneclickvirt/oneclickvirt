#!/bin/bash
# Module 26: Instance Type Tests (VM vs Container specific)
# Dependencies: 09_providers (PROVIDER_ID), 01_init (ADMIN_TOKEN)
# Tests container and VM creation based on INSTANCE_TYPES and env capabilities.

run_module_26() {
    report_add_section "26 - Instance Types"
    local group="instance_types"

    if [[ -z "$PROVIDER_ID" || -z "$ADMIN_TOKEN" ]]; then
        chain_break "$group" "No provider or admin token"
        return 1
    fi

    # ---- Get provider capabilities ----
    local caps; caps=$(test_api "Provider capabilities" "GET" \
        "/api/v1/admin/providers/${PROVIDER_ID}/status" "200" "" "$group" "$ADMIN_TOKEN")

    # ---- Instance type permissions ----
    test_api "Get instance type perms" "GET" "/api/v1/admin/instance-type-permissions" "200" \
        "" "$group" "$ADMIN_TOKEN"

    # ---- Update instance type permissions ----
    test_api "Update type perms (both)" "PUT" "/api/v1/admin/instance-type-permissions" "200|400" \
        '{"minLevelForContainer":1,"minLevelForVM":1,"minLevelForDeleteContainer":1,"minLevelForDeleteVM":1,"minLevelForResetContainer":1,"minLevelForResetVM":1}' "$group" "$ADMIN_TOKEN"

    # ---- Container-specific tests ----
    if should_test_type "container" && env_supports_container; then
        log_info "Testing container-specific operations"

        # Create container instance
        local ct_resp; ct_resp=$(test_api "Create container instance" "POST" "/api/v1/admin/instances" "200|201|400|500" \
            '{"provider_id":'"$PROVIDER_ID"',"name":"type_test_ct","instance_type":"container","image":"debian:12","cpu":1,"memory":512,"disk":5}' \
            "$group" "$ADMIN_TOKEN")
        local ct_task; ct_task=$(echo "$ct_resp" | jq -r '.data.task_id // empty' 2>/dev/null)

        if [[ -n "$ct_task" ]]; then
            # Wait for container creation
            local waited=0
            while [[ $waited -lt 120 ]]; do
                local status; status=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
                    "${SERVER_URL}/api/v1/admin/tasks/${ct_task}" | jq -r '.data.status // empty' 2>/dev/null)
                [[ "$status" == "completed" || "$status" == "failed" ]] && break
                sleep 5; waited=$((waited + 5))
            done

            local ct_id; ct_id=$(echo "$ct_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
            if [[ -n "$ct_id" ]]; then
                # Container-specific operations
                test_api "Container monitoring" "GET" "/api/v1/admin/instances/${ct_id}/monitoring/resources" "200" \
                    "" "$group" "$ADMIN_TOKEN"
                test_api "Container port mappings" "GET" "/api/v1/admin/instances/${ct_id}/port-mappings" "200" \
                    "" "$group" "$ADMIN_TOKEN"

                # Cleanup
                test_api "Delete test container" "DELETE" "/api/v1/admin/instances/${ct_id}" "200" "" "$group" "$ADMIN_TOKEN"
            fi
        fi

        # Disable container permission and verify rejection
        test_api "Disable container perm" "PUT" "/api/v1/admin/instance-type-permissions" "200|400" \
            '{"minLevelForContainer":99,"minLevelForVM":1,"minLevelForDeleteContainer":99,"minLevelForDeleteVM":1,"minLevelForResetContainer":99,"minLevelForResetVM":1}' "$group" "$ADMIN_TOKEN"

        if [[ -n "$USER_TOKEN" ]]; then
            test_api "User create container (disabled)" "POST" "/api/v1/user/instances" "400|403" \
                '{"provider_id":'"$PROVIDER_ID"',"type":"container","image":"debian:11","cpu":1,"memory":512,"disk":5}' \
                "$group" "$USER_TOKEN"
        fi

        # Re-enable
        test_api "Re-enable container perm" "PUT" "/api/v1/admin/instance-type-permissions" "200|400" \
            '{"minLevelForContainer":1,"minLevelForVM":1,"minLevelForDeleteContainer":1,"minLevelForDeleteVM":1,"minLevelForResetContainer":1,"minLevelForResetVM":1}' "$group" "$ADMIN_TOKEN"
    fi

    # ---- VM-specific tests ----
    if should_test_type "vm" && env_supports_vm; then
        log_info "Testing VM-specific operations"

        # Create VM instance
        local vm_resp; vm_resp=$(test_api "Create VM instance" "POST" "/api/v1/admin/instances" "200|201|400|500" \
            '{"provider_id":'"$PROVIDER_ID"',"name":"type_test_vm","instance_type":"vm","image":"debian-11","cpu":1,"memory":1024,"disk":10}' \
            "$group" "$ADMIN_TOKEN")
        local vm_task; vm_task=$(echo "$vm_resp" | jq -r '.data.task_id // empty' 2>/dev/null)

        if [[ -n "$vm_task" ]]; then
            local waited=0
            while [[ $waited -lt 180 ]]; do
                local status; status=$(curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
                    "${SERVER_URL}/api/v1/admin/tasks/${vm_task}" | jq -r '.data.status // empty' 2>/dev/null)
                [[ "$status" == "completed" || "$status" == "failed" ]] && break
                sleep 5; waited=$((waited + 5))
            done

            local vm_id; vm_id=$(echo "$vm_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
            if [[ -n "$vm_id" ]]; then
                test_api "VM monitoring" "GET" "/api/v1/admin/instances/${vm_id}/monitoring/resources" "200" \
                    "" "$group" "$ADMIN_TOKEN"

                # Cleanup
                test_api "Delete test VM" "DELETE" "/api/v1/admin/instances/${vm_id}" "200" "" "$group" "$ADMIN_TOKEN"
            fi
        fi

        # Disable VM permission
        test_api "Disable VM perm" "PUT" "/api/v1/admin/instance-type-permissions" "200|400" \
            '{"minLevelForContainer":1,"minLevelForVM":99,"minLevelForDeleteContainer":1,"minLevelForDeleteVM":99,"minLevelForResetContainer":1,"minLevelForResetVM":99}' "$group" "$ADMIN_TOKEN"

        if [[ -n "$USER_TOKEN" ]]; then
            test_api "User create VM (disabled)" "POST" "/api/v1/user/instances" "400|403" \
                '{"provider_id":'"$PROVIDER_ID"',"type":"vm","image":"debian-11","cpu":1,"memory":1024,"disk":10}' \
                "$group" "$USER_TOKEN"
        fi

        # Re-enable
        test_api "Re-enable all perms" "PUT" "/api/v1/admin/instance-type-permissions" "200|400" \
            '{"minLevelForContainer":1,"minLevelForVM":1,"minLevelForDeleteContainer":1,"minLevelForDeleteVM":1,"minLevelForResetContainer":1,"minLevelForResetVM":1}' "$group" "$ADMIN_TOKEN"
    fi

    # ---- User-side type permission check ----
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User type permissions" "GET" "/api/v1/user/instance-type-permissions" "200" \
            "" "$group" "$USER_TOKEN"
    fi
}
