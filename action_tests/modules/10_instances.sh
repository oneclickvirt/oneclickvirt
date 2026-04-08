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

    # -- Admin instance list --
    test_api "Admin instance list" "GET" "/api/v1/admin/instances?page=1&pageSize=10" "200" "" "$group"

    # ==============================
    # Container tests
    # ==============================
    local container_id=""
    if should_test_type "container" && env_supports_container; then
        log_info "Testing container instances..."

        local inst_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":256,\"disk\":5,\"network_type\":\"nat_ipv4\"}"
        local ir; ir=$(test_api "Create container instance" "POST" "/api/v1/admin/instances" "200" "$inst_data" "$group")
        container_id=$(echo "$ir" | jq -r '.data.id // .data.ID // .data.task_id // empty' 2>/dev/null)

        # Handle task-based creation
        local maybe_task; maybe_task=$(echo "$ir" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$maybe_task" ]]; then
            log_info "Instance creation task: ${maybe_task}"
            local task_r; task_r=$(wait_task_complete "$SERVER_URL" "$maybe_task" "$ADMIN_TOKEN" 300 10)
            container_id=$(echo "$task_r" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
        fi

        if [[ -n "$container_id" ]]; then
            log_info "Container instance ID: ${container_id}"

            # -- Wait for instance SSH to be ready --
            log_info "Waiting 30s for SSH daemon startup..."
            sleep 30
            local ssh_ready=false
            local ssh_waited=30
            while [[ $ssh_waited -lt 60 ]]; do
                local inst_status; inst_status=$(curl -s --max-time 10 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                    "${SERVER_URL}/api/v1/admin/instances/${container_id}" 2>/dev/null)
                local running; running=$(echo "$inst_status" | jq -r '.data.status // empty' 2>/dev/null)
                if [[ "$running" == "running" ]]; then
                    ssh_ready=true
                    log_success "Instance is running (waited ${ssh_waited}s)"
                    break
                fi
                log_info "Instance status: ${running:-unknown}, waiting... (${ssh_waited}s/60s)"
                sleep 10
                ssh_waited=$((ssh_waited + 10))
            done
            if [[ "$ssh_ready" != "true" ]]; then
                log_warning "Instance may not be fully ready after 60s, continuing tests"
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

            # -- Operations --
            test_api "Stop container" "POST" "/api/v1/admin/instances/${container_id}/action" "200" \
                '{"action":"stop"}' "$group"
            sleep 5
            test_api "Start container" "POST" "/api/v1/admin/instances/${container_id}/action" "200" \
                '{"action":"start"}' "$group"
            sleep 5
            test_api "Restart container" "POST" "/api/v1/admin/instances/${container_id}/action" "200" \
                '{"action":"restart"}' "$group"
            sleep 5

            # -- Invalid action --
            test_api "Invalid action" "POST" "/api/v1/admin/instances/${container_id}/action" "400" \
                '{"action":"invalid_action"}' "$group"

            # -- Reset password --
            local rp; rp=$(test_api "Reset container password" "PUT" "/api/v1/admin/instances/${container_id}/reset-password" "200" \
                '{"password":"NewContPass123!"}' "$group")
            local rp_task; rp_task=$(echo "$rp" | jq -r '.data.task_id // empty' 2>/dev/null)
            if [[ -n "$rp_task" ]]; then
                sleep 5
                test_api "Get new password" "GET" "/api/v1/admin/instances/${container_id}/password/${rp_task}" "200" "" "$group"
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
                local target_uid; target_uid=$(echo "$user_info" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
                if [[ -n "$target_uid" ]]; then
                    test_api "Transfer container" "POST" "/api/v1/admin/instances/transfer" "200" \
                        "{\"instanceId\":${container_id},\"targetUserId\":${target_uid}}" "$group"
                    sleep 2
                    test_api "User sees transferred instance" "GET" "/api/v1/user/instances" "200" "" "$group" "$USER_TOKEN"
                fi
            fi

            # -- Rebuild --
            test_api "Rebuild container" "POST" "/api/v1/admin/instances/${container_id}/action" "200" \
                '{"action":"rebuild","image":"debian:12"}' "$group"
            sleep 10
        fi
    fi

    # ==============================
    # VM tests
    # ==============================
    local vm_id=""
    if should_test_type "vm" && env_supports_vm; then
        log_info "Testing VM instances..."

        local vm_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"vm\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":512,\"disk\":10,\"network_type\":\"nat_ipv4\"}"
        local vr; vr=$(test_api "Create VM instance" "POST" "/api/v1/admin/instances" "200" "$vm_data" "$group")
        vm_id=$(echo "$vr" | jq -r '.data.id // .data.ID // .data.task_id // empty' 2>/dev/null)

        local vm_task; vm_task=$(echo "$vr" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$vm_task" ]]; then
            local vm_tr; vm_tr=$(wait_task_complete "$SERVER_URL" "$vm_task" "$ADMIN_TOKEN" 600 15)
            vm_id=$(echo "$vm_tr" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
        fi

        if [[ -n "$vm_id" ]]; then
            log_info "VM instance ID: ${vm_id}"

            # -- Wait for VM SSH to be ready --
            log_info "Waiting 30s for VM SSH daemon startup..."
            sleep 30
            local vm_ssh_ready=false
            local vm_ssh_waited=30
            while [[ $vm_ssh_waited -lt 60 ]]; do
                local vm_status_r; vm_status_r=$(curl -s --max-time 10 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                    "${SERVER_URL}/api/v1/admin/instances/${vm_id}" 2>/dev/null)
                local vm_running; vm_running=$(echo "$vm_status_r" | jq -r '.data.status // empty' 2>/dev/null)
                if [[ "$vm_running" == "running" ]]; then
                    vm_ssh_ready=true
                    log_success "VM is running (waited ${vm_ssh_waited}s)"
                    break
                fi
                log_info "VM status: ${vm_running:-unknown}, waiting... (${vm_ssh_waited}s/60s)"
                sleep 10
                vm_ssh_waited=$((vm_ssh_waited + 10))
            done
            if [[ "$vm_ssh_ready" != "true" ]]; then
                log_warning "VM may not be fully ready after 60s, continuing tests"
            fi

            test_api "VM detail" "GET" "/api/v1/admin/instances/${vm_id}" "200" "" "$group"
            test_api "Stop VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"stop"}' "$group"
            sleep 10
            test_api "Start VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"start"}' "$group"
            sleep 10
            test_api "Restart VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"restart"}' "$group"
            sleep 10
            test_api "VM port mappings" "GET" "/api/v1/admin/instances/${vm_id}/port-mappings" "200" "" "$group"
            test_api "VM resources" "GET" "/api/v1/admin/instances/${vm_id}/monitoring/resources" "200" "" "$group"

            # -- Delete VM --
            test_api "Delete VM" "DELETE" "/api/v1/admin/instances/${vm_id}" "200" "" "$group"
        fi
    fi

    # -- Create with invalid provider --
    test_api "Create instance (invalid provider)" "POST" "/api/v1/admin/instances" "400|404" \
        '{"provider_id":99999,"instance_type":"container","image":"debian:12","cpu":1,"memory":256,"disk":5}' "$group"

    # -- Create with missing fields --
    test_api "Create instance (missing image)" "POST" "/api/v1/admin/instances" "400" \
        "{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"cpu\":1,\"memory\":256}" "$group"

    # -- Task management --
    test_api "Admin task list" "GET" "/api/v1/admin/tasks?page=1&pageSize=10" "200" "" "$group"
    test_api "Task stats" "GET" "/api/v1/admin/tasks/stats" "200" "" "$group"
    test_api "Overall task stats" "GET" "/api/v1/admin/tasks/overall-stats" "200" "" "$group"

    # -- Get nonexistent instance (route does not exist so Gin returns 404) --
    test_api "Get nonexistent instance" "GET" "/api/v1/admin/instances/99999" "404" "" "$group"

    # -- Delete container (cleanup) --
    if [[ -n "$container_id" ]]; then
        test_api "Delete container" "DELETE" "/api/v1/admin/instances/${container_id}" "200" "" "$group"
    fi

    # -- Delete nonexistent instance --
    test_api "Delete nonexistent instance" "DELETE" "/api/v1/admin/instances/99999" "404|500" "" "$group"
}
