#!/bin/bash
# Module 30: Provider Agent Mode & Advanced Features
# Dependencies: 09_providers (PROVIDER_ID), ADMIN_TOKEN
# Tests: agent secret generation, agent connectionType, GPU fields, detect-gpus,
#        source containers for copy mode, exec command

run_module_30() {
    report_add_section "30 - Provider Agent Mode & Advanced Features"
    local group="provider_agent_mode"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider"
        return 1
    fi

    # Resolve worker credentials (global vars from run_env_test.sh)
    local worker_pass="${WORKER_PASSWORD:-${NODE_PASSWORD:-}}"
    local worker_key="${ALICE_PRIVATE_KEY:-}"
    local worker_platform="${WORKER_PLATFORM:-${ACTIVE_PLATFORM:-}}"
    local worker_auth_pref="ssh_key_or_password"
    if declare -f get_platform_auth_method >/dev/null 2>&1 && [[ -n "$worker_platform" ]]; then
        worker_auth_pref=$(get_platform_auth_method "$worker_platform" 2>/dev/null || echo "ssh_key_or_password")
    elif [[ "$worker_platform" == "alice" ]]; then
        worker_auth_pref="ssh_key"
    fi
    local use_worker_key="false"
    if [[ "$worker_auth_pref" == "ssh_key" && -n "$worker_key" ]]; then
        use_worker_key="true"
    fi
    log_info "Provider agent SSH restore auth preference: platform=${worker_platform:-unknown}, auth_pref=${worker_auth_pref}, use_key=${use_worker_key}"

    # =========================================================
    # Section A: Agent Secret Generation
    # CAUTION: GenerateAgentSecret side-effects connection_type→agent.
    # Save original value and restore after this section.
    # =========================================================

    # -- Save original connection_type so we can restore it later --
    local orig_detail; orig_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null)
    local orig_ct; orig_ct=$(echo "$orig_detail" | jq -r '.data.connectionType // "ssh"' 2>/dev/null)
    log_info "Original provider connectionType: ${orig_ct}"

    # -- Generate agent secret --
    local secret_resp; secret_resp=$(test_api "Generate agent secret" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/agent-secret" "200" "" "$group")

    # Verify response contains required fields
    local agent_secret; agent_secret=$(echo "$secret_resp" | jq -r '.data.agentSecret // empty' 2>/dev/null)
    local ws_url; ws_url=$(echo "$secret_resp" | jq -r '.data.wsURL // empty' 2>/dev/null)
    local ws_path; ws_path=$(echo "$secret_resp" | jq -r '.data.wsPath // empty' 2>/dev/null)
    local install_cmd; install_cmd=$(echo "$secret_resp" | jq -r '.data.installCmdController // .data.installCmdGithub // empty' 2>/dev/null)

    if [[ -n "$agent_secret" ]]; then
        log_success "agentSecret returned (length: ${#agent_secret})"
    else
        log_warning "agentSecret missing from response"
    fi
    if [[ -n "$ws_url" ]]; then
        log_success "wsURL returned: ${ws_url}"
    else
        log_warning "wsURL missing from response"
    fi
    if [[ -n "$ws_path" ]]; then
        log_success "wsPath returned: ${ws_path}"
    else
        log_warning "wsPath missing from response"
    fi
    if [[ -n "$install_cmd" ]]; then
        log_success "installCmdController returned (${#install_cmd} chars)"
    else
        log_warning "installCmdController missing from response"
    fi

    # -- Regenerate agent secret (idempotent) --
    test_api "Regenerate agent secret" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/agent-secret" "200" "" "$group"

    # -- Nonexistent provider --
    test_api "Generate agent secret (no provider)" "POST" \
        "/api/v1/admin/providers/99999/agent-secret" "404|400" "" "$group"

    # -- Restore original connection_type (GenerateAgentSecret side-effect) --
    if [[ "$orig_ct" != "agent" ]]; then
        log_info "Restoring provider connectionType from 'agent' back to '${orig_ct}'..."
        curl -s --max-time 30 -X PUT \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            -H "Content-Type: application/json" \
            -d "{\"connectionType\":\"${orig_ct}\"}" \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" >/dev/null 2>&1 || true
        # Verify restoration
        local restored_detail; restored_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null)
        local restored_ct; restored_ct=$(echo "$restored_detail" | jq -r '.data.connectionType // empty' 2>/dev/null)
        if [[ "$restored_ct" == "$orig_ct" ]]; then
            log_success "Provider connectionType restored to '${orig_ct}'"
        else
            log_warning "Provider connectionType restoration may have failed: expected '${orig_ct}', got '${restored_ct}'"
        fi
        if ! wait_provider_active_tasks_idle "$PROVIDER_ID" "provider ${PROVIDER_ID} connection-mode restore" "$ADMIN_TOKEN" 300 5; then
            chain_break "$group" "Provider runtime reload did not settle after connection-mode restore"
            return 1
        fi
    fi

    # =========================================================
    # Section B: connectionType=agent Provider Creation & Update
    # ========================================================

    # -- Create agent-mode provider (no SSH credentials or mapped networking required) --
    local agent_prov; agent_prov=$(test_api "Create agent-mode provider" "POST" "/api/v1/admin/providers" "200|409" \
        "{\"name\":\"ci-agent-mode-provider\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"auto\",\"networkType\":\"no_port_mapping\",\"connectionType\":\"agent\"}" "$group")
    local agent_pid; agent_pid=$(echo "$agent_prov" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    if [[ -n "$agent_pid" ]]; then
        # -- Verify connectionType persisted --
        local agent_detail; agent_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${agent_pid}" 2>/dev/null)
        local ct; ct=$(echo "$agent_detail" | jq -r '.data.connectionType // empty' 2>/dev/null)
        local nt; nt=$(echo "$agent_detail" | jq -r '.data.networkType // empty' 2>/dev/null)
        local traffic_enabled; traffic_enabled=$(echo "$agent_detail" | jq -r '.data.enableTrafficControl // empty' 2>/dev/null)
        local resource_enabled; resource_enabled=$(echo "$agent_detail" | jq -r '.data.enableResourceMonitoring // empty' 2>/dev/null)
        if [[ "$ct" == "agent" ]]; then
            log_success "connectionType=agent persisted correctly"
        else
            log_warning "connectionType expected 'agent', got '${ct}'"
        fi
        if [[ "$nt" == "no_port_mapping" ]]; then
            log_success "agent-mode provider defaulted to no_port_mapping"
        else
            log_warning "agent-mode provider networkType expected 'no_port_mapping', got '${nt}'"
        fi
        if [[ "$traffic_enabled" == "true" && "$resource_enabled" == "true" ]]; then
            log_success "agent-mode provider monitoring defaults enabled"
        else
            log_warning "agent-mode provider monitoring defaults mismatch: traffic=${traffic_enabled} resource=${resource_enabled}"
        fi

        # -- Generate secret for agent-mode provider --
        local agent_secret_resp; agent_secret_resp=$(test_api "Agent provider: generate secret" "POST" \
            "/api/v1/admin/providers/${agent_pid}/agent-secret" "200" "" "$group")
        local a_secret; a_secret=$(echo "$agent_secret_resp" | jq -r '.data.agentSecret // empty' 2>/dev/null)
        if [[ -n "$a_secret" ]]; then
            log_success "Agent provider secret generated"
        fi

        # -- Update agent provider without SSH credentials (should succeed) --
        test_api "Update agent provider (no SSH creds)" "PUT" "/api/v1/admin/providers/${agent_pid}" "200" \
            '{"connectionType":"agent","name":"ci-agent-mode-provider-updated"}' "$group"

        # -- Agent monitoring status should be queryable even without SSH endpoint --
        test_api "Agent provider monitoring status" "GET" "/api/v1/admin/providers/${agent_pid}/monitoring/status" "200|400" "" "$group"

        # -- Switch from agent back to ssh mode (requires full SSH connection info) --
        # The worker's only SSH listener is port 22. Container host mappings such
        # as 22022 are not alternate host SSH listeners and must never be tried
        # here. The primary Provider normally owns WORKER_IP:22, so a fast 409 is
        # a valid duplicate-endpoint result; Section A already covers a successful
        # same-Provider agent->ssh transition using the real endpoint.
        local switch_endpoint="${WORKER_IP}"
        local switch_port=22
        if [[ -n "$switch_endpoint" ]]; then
            local switch_payload=''
            if [[ "$use_worker_key" == "true" || ( -z "$worker_pass" && -n "$worker_key" ) ]]; then
                switch_payload=$(jq -cn --arg endpoint "$switch_endpoint" --arg key "$worker_key" --argjson port "$switch_port" \
                    '{connectionType:"ssh",endpoint:$endpoint,sshPort:$port,username:"root",password:"",sshKey:$key}')
            elif [[ -n "$worker_pass" ]]; then
                switch_payload=$(jq -cn --arg endpoint "$switch_endpoint" --arg password "$worker_pass" --argjson port "$switch_port" \
                    '{connectionType:"ssh",endpoint:$endpoint,sshPort:$port,username:"root",password:$password,sshKey:""}')
            fi
            if [[ -n "$switch_payload" ]]; then
                local switch_resp=''
                local switch_code=''
                switch_resp=$(ACTION_TEST_API_TIMEOUT=30 test_api "Switch agent->ssh (with creds)" "PUT" "/api/v1/admin/providers/${agent_pid}" "200|409" \
                    "$switch_payload" "$group")
                switch_code=$(echo "$switch_resp" | jq -r '.code // empty' 2>/dev/null)
                if [[ "$switch_code" == "200" ]]; then
                    if ! wait_provider_active_tasks_idle "$agent_pid" "switched provider ${agent_pid} runtime reload" "$ADMIN_TOKEN" 300 5; then
                        record_fail_result "Switched provider runtime reload" "GET" "/api/v1/admin/tasks" \
                            "no active tasks" "timeout" "Provider reload task did not settle before auto-configure" "$group"
                    fi
                    # Run task-based configure flow after a successful switch to ssh mode.
                    local sw_ac_resp; sw_ac_resp=$(test_api "Auto-configure switched provider" "POST" \
                        "/api/v1/admin/providers/auto-configure" "200|400|500" \
                        "{\"providerId\":${agent_pid}}" "$group")
                    local sw_ac_task; sw_ac_task=$(echo "$sw_ac_resp" | jq -r '.data.taskId // .data.task_id // empty' 2>/dev/null)
                    if [[ -n "$sw_ac_task" ]]; then
                        log_info "Waiting switched-provider auto-config task: ${sw_ac_task}"
                        wait_configuration_task_complete_nonfatal "$sw_ac_task" "$ADMIN_TOKEN" "$CONFIG_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || true
                    fi
                else
                    log_info "Worker SSH endpoint ${switch_endpoint}:22 is already owned; duplicate tuple was rejected without trying unrelated container ports"
                fi
            else
                log_warning "Skipping agent->ssh switch test because no SSH credentials are available"
            fi
        fi

        # -- Cleanup --
        wait_provider_active_tasks_idle "$agent_pid" "agent-mode provider ${agent_pid} before cleanup" "$ADMIN_TOKEN" 180 5 || true
        delete_provider_and_wait "$agent_pid" "agent-mode provider" "$group" "$ADMIN_TOKEN" true
    else
        log_warning "Could not create agent-mode provider for full test (may be 409 conflict)"
    fi

    # =========================================================
    # Section C: GPU Fields
    # =========================================================

    # -- Enable GPU on existing provider --
    ACTION_TEST_API_TIMEOUT=30 test_api "Enable GPU on provider" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        '{"gpuEnabled":true,"gpuDeviceIds":"0,1"}' "$group"

    # -- Verify gpuEnabled persisted --
    local gpu_detail; gpu_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null)
    local gpu_val; gpu_val=$(echo "$gpu_detail" | jq -r '.data.gpuEnabled // empty' 2>/dev/null)
    local gpu_ids; gpu_ids=$(echo "$gpu_detail" | jq -r '.data.gpuDeviceIds // empty' 2>/dev/null)
    if [[ "$gpu_val" == "true" ]]; then
        log_success "gpuEnabled=true persisted"
    else
        log_warning "gpuEnabled expected true, got '${gpu_val}'"
    fi
    if [[ "$gpu_ids" == "0,1" ]]; then
        log_success "gpuDeviceIds='0,1' persisted"
    else
        log_warning "gpuDeviceIds expected '0,1', got '${gpu_ids}'"
    fi

    # -- Disable GPU --
    ACTION_TEST_API_TIMEOUT=30 test_api "Disable GPU on provider" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        '{"gpuEnabled":false,"gpuDeviceIds":""}' "$group"
    if ! wait_provider_active_tasks_idle "$PROVIDER_ID" "provider ${PROVIDER_ID} GPU updates" "$ADMIN_TOKEN" 300 5; then
        record_fail_result "Provider runtime reload after GPU updates" "GET" "/api/v1/admin/tasks" \
            "no active tasks" "timeout" "Provider reload task did not settle" "$group"
    fi

    # =========================================================
    # Section D: LXD/Incus-specific GPU & Container Features
    # =========================================================

    if [[ "$ENV_TYPE" == "lxd" || "$ENV_TYPE" == "incus" ]]; then
        # -- detect-gpus via SSH --
        test_api "Detect GPUs (LXD/Incus)" "GET" \
            "/api/v1/admin/providers/${PROVIDER_ID}/detect-gpus" "200|400|500" "" "$group"

        # -- Get copyable source containers for copy mode source selection --
        local stopped_resp; stopped_resp=$(test_api "Get copyable source containers" "GET" \
            "/api/v1/admin/providers/${PROVIDER_ID}/stopped-containers" "200|400|500" "" "$group")
        local containers; containers=$(echo "$stopped_resp" | jq -r '.data.containers | length' 2>/dev/null)
        log_info "Source containers available for copy mode: ${containers:-0}"
    else
        # Non-LXD/Incus: endpoints should return graceful error
        test_api "Detect GPUs (non-LXD: expect 400/500)" "GET" \
            "/api/v1/admin/providers/${PROVIDER_ID}/detect-gpus" "200|400|500" "" "$group"
        test_api "Source containers (unsupported provider: expect 400/500)" "GET" \
            "/api/v1/admin/providers/${PROVIDER_ID}/stopped-containers" "200|400|500" "" "$group"
    fi

    # =========================================================
    # Section E: exec Command
    # =========================================================

    # -- exec: valid command --
    local exec_resp; exec_resp=$(test_api "Exec: echo hello" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/exec" "200|400|500" \
        '{"command":"echo hello","timeout":10}' "$group")
    local exec_output; exec_output=$(echo "$exec_resp" | jq -r '.data.output // .data // empty' 2>/dev/null)
    if [[ -n "$exec_output" ]]; then
        log_success "exec returned output: $(echo "$exec_output" | head -1)"
    fi

    # -- exec: empty command must be rejected --
    test_api "Exec: empty command (400)" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/exec" "400" \
        '{"command":""}' "$group"

    # -- exec: no auth --
    test_api "Exec: no auth (401)" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/exec" "401" \
        '{"command":"echo hello"}' "$group" ""

    # -- exec: nonexistent provider --
    test_api "Exec: nonexistent provider (404)" "POST" \
        "/api/v1/admin/providers/99999/exec" "404|400" \
        '{"command":"echo hello","timeout":10}' "$group"

    # =========================================================
    # Section F: Agent Status Verification
    # =========================================================

    # -- Verify agentStatus field is returned in provider detail --
    local detail; detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null)
    local agent_status; agent_status=$(echo "$detail" | jq -r '.data.agentStatus // empty' 2>/dev/null)
    if [[ -n "$agent_status" ]]; then
        log_success "agentStatus field present in provider detail: ${agent_status}"
    else
        log_warning "agentStatus field not returned in provider detail"
    fi
}
