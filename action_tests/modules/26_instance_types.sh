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

    ensure_provider_health_ready "$PROVIDER_ID" "$ADMIN_TOKEN" || {
        chain_break "$group" "Provider health check failed before instance type creation tests"
        return 1
    }

    local type_task_wait="${INSTANCE_TYPE_TASK_MAX_WAIT:-7200}"
    if [[ "$INSTANCE_TASK_MAX_WAIT" =~ ^[0-9]+$ && "$type_task_wait" =~ ^[0-9]+$ && "$type_task_wait" -lt "$INSTANCE_TASK_MAX_WAIT" ]]; then
        type_task_wait="$INSTANCE_TASK_MAX_WAIT"
    fi

    # ---- Container-specific tests ----
    if should_test_type "container" && env_supports_container; then
        log_info "Testing container-specific operations"
        if ! wait_provider_active_tasks_idle "$PROVIDER_ID" "provider ${PROVIDER_ID} before container type test" "$ADMIN_TOKEN" "$type_task_wait" 10; then
            record_skip_result "Create type-test container precheck" "GET" "/api/v1/admin/tasks?page=1&pageSize=100" "provider still has active tasks after ${type_task_wait}s; leaving them to finish" "$group"
            return 0
        fi

        # Create container instance
        local ct_resp; ct_resp=$(test_api "Create container instance" "POST" "/api/v1/admin/instances" "200|201|400" \
            '{"provider_id":'"$PROVIDER_ID"',"name":"type-test-ct","instance_type":"container","image":"debian:12","cpu":1,"memory":512,"disk":5,"bandwidth":1000}' \
            "$group" "$ADMIN_TOKEN")
        local ct_task; ct_task=$(echo "$ct_resp" | jq -r '.data.task_id // .data.taskId // empty' 2>/dev/null)
        local ct_id=""
        local ct_created=false

        if [[ -n "$ct_task" ]]; then
            local ct_task_resp=""
            if ct_task_resp=$(wait_task_complete_nonfatal "$SERVER_URL" "$ct_task" "$ADMIN_TOKEN" "$type_task_wait" 10); then
                ct_created=true
                ct_id=$(echo "$ct_task_resp" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
                if [[ -z "$ct_id" ]]; then
                    record_fail_result "Create type-test container task result" "GET" "/api/v1/admin/tasks/${ct_task}" "instance id" "missing" "$ct_task_resp" "$group"
                    ct_created=false
                fi
            else
                ct_id=$(echo "$ct_task_resp" | jq -r '.data.instance_id // .data.instanceId // .data.result.id // empty' 2>/dev/null)
                if is_infrastructure_failure_detail "$ct_task_resp"; then
                    local ct_infra_detail; ct_infra_detail=$(echo "$ct_task_resp" | jq -c '.data.errorMessage // .message // .msg // .' 2>/dev/null || printf '%s' "$ct_task_resp")
                    record_skip_result "Create type-test container task (infrastructure)" "GET" "/api/v1/admin/tasks/${ct_task}" "${ct_infra_detail}" "$group"
                else
                    local ct_task_actual; ct_task_actual=$(safe_jq "$ct_task_resp" '.data.status // .message // .msg // "failed"' 'failed')
                    if is_active_task_status "$ct_task_actual"; then
                        ct_id=""
                        record_skip_result "Create type-test container task (still running)" "GET" "/api/v1/admin/tasks/${ct_task}" "task remained ${ct_task_actual} after ${type_task_wait}s; leaving it to finish" "$group"
                    else
                        record_fail_result "Create type-test container task" "GET" "/api/v1/admin/tasks/${ct_task}" "completed" "$ct_task_actual" "$ct_task_resp" "$group"
                    fi
                fi
            fi
        else
            ct_id=$(echo "$ct_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
            [[ -n "$ct_id" ]] && ct_created=true
        fi

        if [[ "$ct_created" == "true" && -n "$ct_id" ]]; then
            local ct_status_resp=""
            if ct_status_resp=$(wait_instance_status "$ct_id" "running" "$INSTANCE_STATUS_MAX_WAIT" 10 "$ADMIN_TOKEN" "type-test container ${ct_id}"); then
            # Container-specific operations
                test_api "Container monitoring" "GET" "/api/v1/admin/instances/${ct_id}/monitoring/resources" "200" \
                    "" "$group" "$ADMIN_TOKEN"
                test_api "Container port mappings" "GET" "/api/v1/admin/instances/${ct_id}/port-mappings" "200" \
                    "" "$group" "$ADMIN_TOKEN"
            else
                local ct_status_actual; ct_status_actual=$(safe_jq "$ct_status_resp" '.data.status // .message // .msg // "not-running"' 'not-running')
                record_fail_result "Type-test container running" "GET" "/api/v1/admin/instances/${ct_id}" "running" "$ct_status_actual" "$ct_status_resp" "$group"
            fi

            # Cleanup
            local ct_delete_resp; ct_delete_resp=$(test_api "Delete test container" "DELETE" "/api/v1/admin/instances/${ct_id}" "200" "" "$group" "$ADMIN_TOKEN") || ct_delete_resp=""
            [[ -n "$ct_delete_resp" ]] && wait_instance_operation_settled "$ct_id" "$ct_delete_resp" "deleted" "delete type-test container ${ct_id}" "$ADMIN_TOKEN" || true
        elif [[ -n "$ct_id" ]]; then
            delete_instance_safe "$ct_id" "$ADMIN_TOKEN" 180 || true
        fi

        # Disable container permission and verify rejection
        test_api "Disable container perm" "PUT" "/api/v1/admin/instance-type-permissions" "200|400" \
            '{"minLevelForContainer":99,"minLevelForVM":1,"minLevelForDeleteContainer":99,"minLevelForDeleteVM":1,"minLevelForResetContainer":99,"minLevelForResetVM":1}' "$group" "$ADMIN_TOKEN"

        if [[ -n "$USER_TOKEN" ]]; then
            test_api "User create container (disabled)" "POST" "/api/v1/user/instances" "400|403" \
                '{"provider_id":'"$PROVIDER_ID"',"type":"container","image":"debian:11","cpu":1,"memory":512,"disk":5,"bandwidth":1000}' \
                "$group" "$USER_TOKEN"
        fi

        # Re-enable
        test_api "Re-enable container perm" "PUT" "/api/v1/admin/instance-type-permissions" "200|400" \
            '{"minLevelForContainer":1,"minLevelForVM":1,"minLevelForDeleteContainer":1,"minLevelForDeleteVM":1,"minLevelForResetContainer":1,"minLevelForResetVM":1}' "$group" "$ADMIN_TOKEN"
    fi

    # ---- VM-specific tests ----
    if should_test_type "vm" && env_supports_vm; then
        log_info "Testing VM-specific operations"
        if ! wait_provider_active_tasks_idle "$PROVIDER_ID" "provider ${PROVIDER_ID} before VM type test" "$ADMIN_TOKEN" "$type_task_wait" 10; then
            record_skip_result "Create type-test VM precheck" "GET" "/api/v1/admin/tasks?page=1&pageSize=100" "provider still has active tasks after ${type_task_wait}s; leaving them to finish" "$group"
            return 0
        fi

        # Create VM instance
        local vm_resp; vm_resp=$(test_api "Create VM instance" "POST" "/api/v1/admin/instances" "200|201|400" \
            '{"provider_id":'"$PROVIDER_ID"',"name":"type-test-vm","instance_type":"vm","image":"debian-11","cpu":1,"memory":512,"disk":5,"bandwidth":1000}' \
            "$group" "$ADMIN_TOKEN")
        local vm_task; vm_task=$(echo "$vm_resp" | jq -r '.data.task_id // .data.taskId // empty' 2>/dev/null)
        local vm_id=""
        local vm_created=false

        if [[ -n "$vm_task" ]]; then
            local vm_task_resp=""
            if vm_task_resp=$(wait_task_complete_nonfatal "$SERVER_URL" "$vm_task" "$ADMIN_TOKEN" "$type_task_wait" 10); then
                vm_created=true
                vm_id=$(echo "$vm_task_resp" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
                if [[ -z "$vm_id" ]]; then
                    record_fail_result "Create type-test VM task result" "GET" "/api/v1/admin/tasks/${vm_task}" "instance id" "missing" "$vm_task_resp" "$group"
                    vm_created=false
                fi
            else
                vm_id=$(echo "$vm_task_resp" | jq -r '.data.instance_id // .data.instanceId // .data.result.id // empty' 2>/dev/null)
                if is_infrastructure_failure_detail "$vm_task_resp"; then
                    local vm_infra_detail; vm_infra_detail=$(echo "$vm_task_resp" | jq -c '.data.errorMessage // .message // .msg // .' 2>/dev/null || printf '%s' "$vm_task_resp")
                    record_skip_result "Create type-test VM task (infrastructure)" "GET" "/api/v1/admin/tasks/${vm_task}" "${vm_infra_detail}" "$group"
                else
                    local vm_task_actual; vm_task_actual=$(safe_jq "$vm_task_resp" '.data.status // .message // .msg // "failed"' 'failed')
                    if is_active_task_status "$vm_task_actual"; then
                        vm_id=""
                        record_skip_result "Create type-test VM task (still running)" "GET" "/api/v1/admin/tasks/${vm_task}" "task remained ${vm_task_actual} after ${type_task_wait}s; leaving it to finish" "$group"
                    else
                        record_fail_result "Create type-test VM task" "GET" "/api/v1/admin/tasks/${vm_task}" "completed" "$vm_task_actual" "$vm_task_resp" "$group"
                    fi
                fi
            fi
        else
            vm_id=$(echo "$vm_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
            [[ -n "$vm_id" ]] && vm_created=true
        fi

        if [[ "$vm_created" == "true" && -n "$vm_id" ]]; then
            local vm_status_resp=""
            if vm_status_resp=$(wait_instance_status "$vm_id" "running" "$INSTANCE_STATUS_MAX_WAIT" 10 "$ADMIN_TOKEN" "type-test VM ${vm_id}"); then
                test_api "VM monitoring" "GET" "/api/v1/admin/instances/${vm_id}/monitoring/resources" "200" \
                    "" "$group" "$ADMIN_TOKEN"
            else
                local vm_status_actual; vm_status_actual=$(safe_jq "$vm_status_resp" '.data.status // .message // .msg // "not-running"' 'not-running')
                record_fail_result "Type-test VM running" "GET" "/api/v1/admin/instances/${vm_id}" "running" "$vm_status_actual" "$vm_status_resp" "$group"
            fi

            # Cleanup
            local vm_delete_resp; vm_delete_resp=$(test_api "Delete test VM" "DELETE" "/api/v1/admin/instances/${vm_id}" "200" "" "$group" "$ADMIN_TOKEN") || vm_delete_resp=""
            [[ -n "$vm_delete_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_delete_resp" "deleted" "delete type-test VM ${vm_id}" "$ADMIN_TOKEN" || true
        elif [[ -n "$vm_id" ]]; then
            delete_instance_safe "$vm_id" "$ADMIN_TOKEN" 180 || true
        fi

        # Disable VM permission
        test_api "Disable VM perm" "PUT" "/api/v1/admin/instance-type-permissions" "200|400" \
            '{"minLevelForContainer":1,"minLevelForVM":99,"minLevelForDeleteContainer":1,"minLevelForDeleteVM":99,"minLevelForResetContainer":1,"minLevelForResetVM":99}' "$group" "$ADMIN_TOKEN"

        if [[ -n "$USER_TOKEN" ]]; then
            test_api "User create VM (disabled)" "POST" "/api/v1/user/instances" "400|403" \
                '{"provider_id":'"$PROVIDER_ID"',"type":"vm","image":"debian-11","cpu":1,"memory":512,"disk":5,"bandwidth":1000}' \
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
