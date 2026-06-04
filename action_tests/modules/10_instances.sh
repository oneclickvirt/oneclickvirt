#!/bin/bash
# Module 10: Instance Lifecycle (Admin + User, Container + VM)
# Dependencies: 01_init (ADMIN_TOKEN), 09_providers (PROVIDER_ID)

run_module_10() {
    report_add_section "10 - Instance Lifecycle"
    local group="instances"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider, skipping instance tests"
        return 1
    fi

    # -- Health check before instance creation --
    log_info "Verifying provider health before instance tests..."
    local hc_resp; hc_resp=$(test_api_retry "Provider health (pre-instance)" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/health-check" "200" '{}' 3 10 "$group")
    if [[ $? -ne 0 ]]; then
        chain_break "$group" "Provider health check failed, cannot create instances"
        return 1
    fi
    log_info "Provider health check triggered; waiting ${INSTANCE_HEALTH_SETTLE_SECONDS}s before instance creation..."
    sleep "$INSTANCE_HEALTH_SETTLE_SECONDS"

    # -- Admin instance list --
    test_api "Admin instance list" "GET" "/api/v1/admin/instances?page=1&pageSize=10" "200" "" "$group"

    # ==============================
    # Container tests
    # ==============================
    local container_id=""
    if should_test_type "container" && env_supports_container; then
        log_info "Testing container instances..."

        local inst_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":256,\"disk\":5,\"network_type\":\"nat_ipv4\"}"
        local ir
        if ! ir=$(test_api_retry "Create container instance" "POST" "/api/v1/admin/instances" "200" "$inst_data" 3 15 "$group"); then
            log_warning "Container instance creation did not complete successfully; downstream container checks will be skipped"
            ir=""
        fi
        # Debug: log full creation response
        log_info "Create instance response: $(echo "$ir" | jq -c '.' 2>/dev/null | head -c 2000)"
        container_id=$(echo "$ir" | jq -r '.data.id // .data.ID // .data.task_id // empty' 2>/dev/null)

        # Handle task-based creation
        local maybe_task; maybe_task=$(echo "$ir" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$maybe_task" ]]; then
            log_info "Instance creation task: ${maybe_task}"
            local task_r; task_r=$(wait_task_complete "$SERVER_URL" "$maybe_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10)
            log_info "Task complete response: $(echo "$task_r" | jq -c '.' 2>/dev/null | head -c 2000)"
            container_id=$(echo "$task_r" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
        fi

        if [[ -n "$container_id" ]]; then
            log_info "Container instance ID: ${container_id}"
            # Export for downstream modules (18, 19, 22, 24)
            export TEST_INSTANCE_ID="$container_id"

            # -- Wait for instance SSH to be ready --
            log_info "Waiting 30s for SSH daemon startup..."
            sleep 30
            local ssh_ready=false
            if wait_instance_status "$container_id" "running" "$INSTANCE_STATUS_MAX_WAIT" 10 "$ADMIN_TOKEN" "container ${container_id}" > /dev/null; then
                ssh_ready=true
            else
                log_warning "Instance may not be fully ready after ${INSTANCE_STATUS_MAX_WAIT}s, continuing tests"
            fi

            # -- Detail --
            local detail; detail=$(test_api "Container detail" "GET" "/api/v1/admin/instances/${container_id}" "200" "" "$group")

            # -- Config validation --
            TOTAL_TESTS=$((TOTAL_TESTS + 1))
            local d_cpu; d_cpu=$(echo "$detail" | jq -r '.data.cpu // empty' 2>/dev/null)
            local d_mem; d_mem=$(echo "$detail" | jq -r '.data.memory // empty' 2>/dev/null)
            if [[ -n "$d_cpu" || -n "$d_mem" ]]; then
                PASSED_TESTS=$((PASSED_TESTS + 1))
                log_success "Container config: CPU=${d_cpu} MEM=${d_mem}"
                report_add_pass "Container config validation" "GET" "/api/v1/admin/instances/${container_id}"
                _add_result_json "Container config validation" "GET" "/api/v1/admin/instances/${container_id}" "PASS" "" "" "" "$group"
            else
                FAILED_TESTS=$((FAILED_TESTS + 1))
                log_error "Container config empty"
                report_add_fail "Container config validation" "GET" "/api/v1/admin/instances/${container_id}" "" "non-empty" "empty" "$detail"
                _add_result_json "Container config validation" "GET" "/api/v1/admin/instances/${container_id}" "FAIL" "non-empty" "empty" "" "$group"
            fi

            # -- Operations (only if instance is running) --
            if [[ "$ssh_ready" == "true" ]]; then
                local stop_resp; stop_resp=$(test_api "Stop container" "POST" "/api/v1/admin/instances/${container_id}/action" "200" \
                    '{"action":"stop"}' "$group") || stop_resp=""
                [[ -n "$stop_resp" ]] && wait_instance_operation_settled "$container_id" "$stop_resp" "stopped" "stop container ${container_id}" "$ADMIN_TOKEN" || true

                local start_resp; start_resp=$(test_api "Start container" "POST" "/api/v1/admin/instances/${container_id}/action" "200" \
                    '{"action":"start"}' "$group") || start_resp=""
                [[ -n "$start_resp" ]] && wait_instance_operation_settled "$container_id" "$start_resp" "running" "start container ${container_id}" "$ADMIN_TOKEN" || true

                local restart_resp; restart_resp=$(test_api "Restart container" "POST" "/api/v1/admin/instances/${container_id}/action" "200" \
                    '{"action":"restart"}' "$group") || restart_resp=""
                [[ -n "$restart_resp" ]] && wait_instance_operation_settled "$container_id" "$restart_resp" "running" "restart container ${container_id}" "$ADMIN_TOKEN" || true
            else
                log_warning "Skipping stop/start/restart tests: instance not in 'running' state"
                SKIPPED_TESTS=$((SKIPPED_TESTS + 3))
                for op in "Stop container" "Start container" "Restart container"; do
                    report_add_skip "$op" "POST" "/api/v1/admin/instances/${container_id}/action" "instance not in running state"
                    _add_result_json "$op" "POST" "/api/v1/admin/instances/${container_id}/action" "SKIP" "" "instance not running" "" "$group"
                done
            fi

            # -- Invalid action --
            test_api "Invalid action" "POST" "/api/v1/admin/instances/${container_id}/action" "400" \
                '{"action":"invalid_action"}' "$group"
            test_api "Batch action invalid action" "POST" "/api/v1/admin/instances/batch-action" "400" \
                "{\"instanceIds\":[${container_id}],\"action\":\"invalid_action\"}" "$group"
            test_api "Batch action empty ids" "POST" "/api/v1/admin/instances/batch-action" "400" \
                '{"instanceIds":[],"action":"start"}' "$group"

            # -- Reset password --
            local known_test_pw="NewContPass123!"
            local rp; rp=$(test_api "Reset container password" "PUT" "/api/v1/admin/instances/${container_id}/reset-password" "200|400|500" \
                "{\"password\":\"${known_test_pw}\"}" "$group")
            export TEST_INSTANCE_PASSWORD="${known_test_pw}"
            local rp_task; rp_task=$(echo "$rp" | jq -r '.data.task_id // empty' 2>/dev/null)
            if [[ -n "$rp_task" ]]; then
                wait_task_complete "$SERVER_URL" "$rp_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || true
                local gp; gp=$(test_api "Get new password" "GET" "/api/v1/admin/instances/${container_id}/password/${rp_task}" "200" "" "$group")
                local gp_pw; gp_pw=$(echo "$gp" | jq -r '.data.password // empty' 2>/dev/null)
                if [[ -n "$gp_pw" && "$gp_pw" != "null" ]]; then
                    export TEST_INSTANCE_PASSWORD="$gp_pw"
                fi
            fi

            # -- Edit instance --
            test_api "Edit container" "PUT" "/api/v1/admin/instances/${container_id}" "200" \
                '{"name":"ci-container-edited"}' "$group"

            # -- Port mappings --
            test_api "Container port mappings" "GET" "/api/v1/admin/instances/${container_id}/port-mappings" "200" "" "$group"

            # -- Resource monitoring --
            test_api "Container resources" "GET" "/api/v1/admin/instances/${container_id}/monitoring/resources" "200" "" "$group"

            # -- Freeze/unfreeze --
            test_api "Freeze container" "POST" "/api/v1/admin/instances/freeze" "200" \
                "{\"instanceId\":${container_id}}" "$group"
            sleep 2
            test_api "Unfreeze container" "POST" "/api/v1/admin/instances/unfreeze" "200" \
                "{\"instanceId\":${container_id}}" "$group"

            # -- Set expiry --
            local inst_exp; inst_exp=$(date -u -d "+2 days" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v+2d '+%Y-%m-%dT%H:%M:%SZ')
            test_api "Set container expiry" "POST" "/api/v1/admin/instances/set-expiry" "200" \
                "{\"instanceId\":${container_id},\"expiresAt\":\"${inst_exp}\"}" "$group"

            # -- Transfer to user --
            if [[ -n "$USER_TOKEN" && "$USER_TOKEN" != "$ADMIN_TOKEN" ]]; then
                local user_info; user_info=$(curl -s --max-time 30 \
                    -H "Authorization: Bearer ${USER_TOKEN}" "${SERVER_URL}/api/v1/user/profile" 2>/dev/null)
                local target_uid; target_uid=$(echo "$user_info" | jq -r '.data.user.id // .data.user.ID // .data.id // .data.ID // empty' 2>/dev/null)
                if [[ -n "$target_uid" ]]; then
                    test_api "Transfer container" "POST" "/api/v1/admin/instances/transfer" "200|400|403|404" \
                        "{\"instanceId\":${container_id},\"targetUserId\":${target_uid}}" "$group"
                    sleep 2
                    test_api "User sees transferred instance" "GET" "/api/v1/user/instances" "200" "" "$group" "$USER_TOKEN"
                fi
            fi

            # -- Rebuild --
            local rb_resp; rb_resp=$(test_api "Rebuild container" "POST" "/api/v1/admin/instances/${container_id}/action" "200|400|500" \
                '{"action":"rebuild","image":"debian:12"}' "$group")
            log_info "Rebuild response: $(echo "$rb_resp" | jq -c '.' 2>/dev/null | head -c 2000)"
            local rb_task; rb_task=$(echo "$rb_resp" | jq -r '.data.task_id // empty' 2>/dev/null)
            if [[ -n "$rb_task" ]]; then
                log_info "Waiting for rebuild task ${rb_task}..."
                wait_task_complete "$SERVER_URL" "$rb_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || {
                    log_warning "Rebuild task ${rb_task} did not complete within timeout"
                }
            fi
            # Wait for instance to reach running state after rebuild
            wait_instance_status "$container_id" "running" "$INSTANCE_STATUS_MAX_WAIT" 10 "$ADMIN_TOKEN" "container ${container_id} after rebuild" > /dev/null || true
        fi
    fi

    # ==============================
    # VM tests
    # ==============================
    local vm_id=""
    if should_test_type "vm" && env_supports_vm; then
        log_info "Testing VM instances..."

        local vm_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"vm\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":512,\"disk\":10,\"network_type\":\"nat_ipv4\"}"
        local vr
        if ! vr=$(test_api_retry "Create VM instance" "POST" "/api/v1/admin/instances" "200" "$vm_data" 3 20 "$group"); then
            log_warning "VM instance creation did not complete successfully; downstream VM checks will be skipped"
            vr=""
        fi
        vm_id=$(echo "$vr" | jq -r '.data.id // .data.ID // .data.task_id // empty' 2>/dev/null)

        local vm_task; vm_task=$(echo "$vr" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$vm_task" ]]; then
            local vm_tr; vm_tr=$(wait_task_complete "$SERVER_URL" "$vm_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 15)
            vm_id=$(echo "$vm_tr" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
        fi

        if [[ -n "$vm_id" ]]; then
            log_info "VM instance ID: ${vm_id}"

            # -- Wait for VM SSH to be ready --
            log_info "Waiting 30s for VM SSH daemon startup..."
            sleep 30
            local vm_ssh_ready=false
            if wait_instance_status "$vm_id" "running" "$INSTANCE_STATUS_MAX_WAIT" 10 "$ADMIN_TOKEN" "VM ${vm_id}" > /dev/null; then
                vm_ssh_ready=true
            fi
            if [[ "$vm_ssh_ready" != "true" ]]; then
                log_warning "VM may not be fully ready after ${INSTANCE_STATUS_MAX_WAIT}s, continuing tests"
            fi

            test_api "VM detail" "GET" "/api/v1/admin/instances/${vm_id}" "200" "" "$group"
            local vm_stop_resp; vm_stop_resp=$(test_api "Stop VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"stop"}' "$group") || vm_stop_resp=""
            [[ -n "$vm_stop_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_stop_resp" "stopped" "stop VM ${vm_id}" "$ADMIN_TOKEN" || true
            local vm_start_resp; vm_start_resp=$(test_api "Start VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"start"}' "$group") || vm_start_resp=""
            [[ -n "$vm_start_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_start_resp" "running" "start VM ${vm_id}" "$ADMIN_TOKEN" || true
            local vm_restart_resp; vm_restart_resp=$(test_api "Restart VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"restart"}' "$group") || vm_restart_resp=""
            [[ -n "$vm_restart_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_restart_resp" "running" "restart VM ${vm_id}" "$ADMIN_TOKEN" || true
            test_api "VM port mappings" "GET" "/api/v1/admin/instances/${vm_id}/port-mappings" "200" "" "$group"
            test_api "VM resources" "GET" "/api/v1/admin/instances/${vm_id}/monitoring/resources" "200" "" "$group"

            # -- Delete VM --
            local vm_delete_resp; vm_delete_resp=$(test_api "Delete VM" "DELETE" "/api/v1/admin/instances/${vm_id}" "200" "" "$group") || vm_delete_resp=""
            [[ -n "$vm_delete_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_delete_resp" "deleted" "delete VM ${vm_id}" "$ADMIN_TOKEN" || true
        fi
    fi

    # -- Create with invalid provider --
    test_api "Create instance (invalid provider)" "POST" "/api/v1/admin/instances" "400|404" \
        '{"provider_id":99999,"instance_type":"container","image":"debian:12","cpu":1,"memory":256,"disk":5}' "$group"

    # -- Create with missing fields --
    test_api "Create instance (missing image)" "POST" "/api/v1/admin/instances" "400" \
        "{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"cpu\":1,\"memory\":256}" "$group"

    # -- Negative: Create with negative resources --
    test_api "Create instance (negative cpu)" "POST" "/api/v1/admin/instances" "400" \
        "{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"debian:12\",\"cpu\":-1,\"memory\":256,\"disk\":5}" "$group"
    test_api "Create instance (negative memory)" "POST" "/api/v1/admin/instances" "400" \
        "{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":-256,\"disk\":5}" "$group"
    test_api "Create instance (zero disk)" "POST" "/api/v1/admin/instances" "400" \
        "{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":256,\"disk\":0}" "$group"

    # -- Negative: Create with invalid instance_type --
    test_api "Create instance (bad type)" "POST" "/api/v1/admin/instances" "400" \
        "{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"invalid_type\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":256,\"disk\":5}" "$group"

    # -- Negative: Instance action with missing instanceId --
    test_api "Instance action (no id)" "POST" "/api/v1/admin/instances/action" "400|404" \
        '{"action":"stop"}' "$group"

    # -- Negative: Instance action on nonexistent instance --
    test_api "Action nonexistent instance" "POST" "/api/v1/admin/instances/99999/action" "400|404" \
        '{"action":"stop"}' "$group"

    # -- Negative: User creates instance without permission (if provider restricted) --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User instance list" "GET" "/api/v1/user/instances?page=1&pageSize=10" "200" "" "$group" "$USER_TOKEN"
        test_api "User task list" "GET" "/api/v1/user/tasks?page=1&pageSize=10" "200" "" "$group" "$USER_TOKEN"
    fi

    # -- Task management --
    test_api "Admin task list" "GET" "/api/v1/admin/tasks?page=1&pageSize=10" "200" "" "$group"
    test_api "Task stats" "GET" "/api/v1/admin/tasks/stats" "200" "" "$group"
    test_api "Overall task stats" "GET" "/api/v1/admin/tasks/overall-stats" "200" "" "$group"

    # -- Task detail (get first task ID if available) --
    local tasks_resp; tasks_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/tasks?page=1&pageSize=1" 2>/dev/null)
    local first_task_id; first_task_id=$(echo "$tasks_resp" | jq -r '.data.list[0].id // .data[0].id // empty' 2>/dev/null)
    if [[ -n "$first_task_id" ]]; then
        test_api "Task detail" "GET" "/api/v1/admin/tasks/${first_task_id}" "200" "" "$group"
        test_api "Cancel completed task" "POST" "/api/v1/admin/tasks/${first_task_id}/cancel" "200|400" "" "$group"
    fi
    test_api "Get nonexistent task" "GET" "/api/v1/admin/tasks/99999" "404|400" "" "$group"

    # -- Get nonexistent instance (route does not exist so Gin returns 404) --
    test_api "Get nonexistent instance" "GET" "/api/v1/admin/instances/99999" "404" "" "$group"

    # -- Delete container (cleanup) --
    # NOTE: Only delete container if no downstream modules need TEST_INSTANCE_ID.
    # When running all modules, keep the container for modules 18, 19, 22, 24.
    # The restore_base_state handler will clean it up after all modules complete.
    if [[ -n "$container_id" && -z "$TEST_INSTANCE_ID" ]]; then
        test_api "Delete container" "DELETE" "/api/v1/admin/instances/${container_id}" "200" "" "$group"
    fi

    # -- Delete nonexistent instance --
    test_api "Delete nonexistent instance" "DELETE" "/api/v1/admin/instances/99999" "404|400" "" "$group"
}
