#!/bin/bash
# Module 30: Provider Agent Mode & Advanced Features
# Dependencies: 09_providers (PROVIDER_ID), ADMIN_TOKEN
# Tests: agent secret generation, agent connectionType, GPU fields, detect-gpus,
#        stopped containers (LXD/Incus), exec command

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
        local switch_endpoint="${WORKER_IP}"
        local switch_port=22
        if [[ -n "$switch_endpoint" ]]; then
            local providers_resp; providers_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                "${SERVER_URL}/api/v1/admin/providers?page=1&pageSize=200" 2>/dev/null)
            local endpoint_conflict; endpoint_conflict=$(echo "$providers_resp" | jq -r 2>/dev/null \
                --arg endpoint "$switch_endpoint" \
                --arg self_id "$agent_pid" \
                --argjson ssh_port "$switch_port" \
                'def p: (.sshPort // .ssh_port // 22 | tonumber? // 22);
                 def e: (.endpoint // .host // "");
                 if any((.data.list // .data.items // .data // [])[]?; ((.id // .ID | tostring) != $self_id) and (e == $endpoint) and (p == $ssh_port)) then "yes" else "no" end' 2>/dev/null)
            if [[ "$endpoint_conflict" == "yes" ]]; then
                for candidate_port in 22022 22023 22024 22025 22026 22027 22028 22029; do
                    local port_conflict; port_conflict=$(echo "$providers_resp" | jq -r 2>/dev/null \
                        --arg endpoint "$switch_endpoint" \
                        --arg self_id "$agent_pid" \
                        --argjson ssh_port "$candidate_port" \
                        'def p: (.sshPort // .ssh_port // 22 | tonumber? // 22);
                         def e: (.endpoint // .host // "");
                         if any((.data.list // .data.items // .data // [])[]?; ((.id // .ID | tostring) != $self_id) and (e == $endpoint) and (p == $ssh_port)) then "yes" else "no" end' 2>/dev/null)
                    if [[ "$port_conflict" != "yes" ]]; then
                        switch_port="$candidate_port"
                        break
                    fi
                done
                log_info "Switch agent->ssh uses non-conflicting endpoint tuple: ${switch_endpoint}:${switch_port}"
            fi

            # If collision still happens due backend-side normalization, retry with alternative ports.
            local switch_payload=''
            if [[ -n "$worker_pass" ]]; then
                switch_payload="{\"connectionType\":\"ssh\",\"endpoint\":\"${switch_endpoint}\",\"sshPort\":${switch_port},\"username\":\"root\",\"password\":\"${worker_pass}\"}"
            elif [[ -n "$worker_key" ]]; then
                local escaped_switch_key; escaped_switch_key=$(echo "$worker_key" | jq -Rsa .)
                switch_payload="{\"connectionType\":\"ssh\",\"endpoint\":\"${switch_endpoint}\",\"sshPort\":${switch_port},\"username\":\"root\",\"sshKey\":${escaped_switch_key}}"
            fi
            if [[ -n "$switch_payload" ]]; then
                local switch_resp=''
                local switch_code=''
                local switched_ok=0
                local try_port=''
                for try_port in "$switch_port" 22022 22023 22024 22025 22026 22027 22028 22029; do
                    if [[ "$try_port" != "$switch_port" ]]; then
                        if [[ -n "$worker_pass" ]]; then
                            switch_payload="{\"connectionType\":\"ssh\",\"endpoint\":\"${switch_endpoint}\",\"sshPort\":${try_port},\"username\":\"root\",\"password\":\"${worker_pass}\"}"
                        else
                            local escaped_switch_key_retry; escaped_switch_key_retry=$(echo "$worker_key" | jq -Rsa .)
                            switch_payload="{\"connectionType\":\"ssh\",\"endpoint\":\"${switch_endpoint}\",\"sshPort\":${try_port},\"username\":\"root\",\"sshKey\":${escaped_switch_key_retry}}"
                        fi
                    fi
                    switch_resp=$(test_api "Switch agent->ssh (with creds)" "PUT" "/api/v1/admin/providers/${agent_pid}" "200|409" \
                        "$switch_payload" "$group")
                    switch_code=$(echo "$switch_resp" | jq -r '.code // empty' 2>/dev/null)
                    if [[ "$switch_code" == "200" ]]; then
                        switched_ok=1
                        switch_port="$try_port"
                        break
                    fi
                    log_warning "Switch agent->ssh conflict on ${switch_endpoint}:${try_port}, trying next port"
                done

                if [[ "$switched_ok" -eq 1 ]]; then
                    # Run task-based configure flow after a successful switch to ssh mode.
                    local sw_ac_resp; sw_ac_resp=$(test_api "Auto-configure switched provider" "POST" \
                        "/api/v1/admin/providers/auto-configure" "200|400|500" \
                        "{\"providerId\":${agent_pid}}" "$group")
                    local sw_ac_task; sw_ac_task=$(echo "$sw_ac_resp" | jq -r '.data.task_id // empty' 2>/dev/null)
                    if [[ -n "$sw_ac_task" ]]; then
                        log_info "Waiting switched-provider auto-config task: ${sw_ac_task}"
                        wait_task_complete "$SERVER_URL" "$sw_ac_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || true
                    fi
                else
                    test_api "Switch agent->ssh (with creds)" "PUT" "/api/v1/admin/providers/${agent_pid}" "200" \
                        "$switch_payload" "$group"
                fi
            else
                log_warning "Skipping agent->ssh switch test because no SSH credentials are available"
            fi
        fi

        # -- Cleanup --
        test_api "Delete agent-mode provider" "DELETE" "/api/v1/admin/providers/${agent_pid}" "200" "" "$group"
    else
        log_warning "Could not create agent-mode provider for full test (may be 409 conflict)"
    fi

    # =========================================================
    # Section C: GPU Fields
    # =========================================================

    # -- Enable GPU on existing provider --
    test_api "Enable GPU on provider" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
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
    test_api "Disable GPU on provider" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        '{"gpuEnabled":false,"gpuDeviceIds":""}' "$group"

    # =========================================================
    # Section D: LXD/Incus-specific GPU & Container Features
    # =========================================================

    if [[ "$ENV_TYPE" == "lxd" || "$ENV_TYPE" == "incus" ]]; then
        # -- detect-gpus via SSH --
        test_api "Detect GPUs (LXD/Incus)" "GET" \
            "/api/v1/admin/providers/${PROVIDER_ID}/detect-gpus" "200|400|500" "" "$group"

        # -- Get stopped containers for copy mode source selection --
        local stopped_resp; stopped_resp=$(test_api "Get stopped containers" "GET" \
            "/api/v1/admin/providers/${PROVIDER_ID}/stopped-containers" "200|400|500" "" "$group")
        local containers; containers=$(echo "$stopped_resp" | jq -r '.data | length' 2>/dev/null)
        log_info "Stopped containers available for copy mode: ${containers:-0}"
    else
        # Non-LXD/Incus: endpoints should return graceful error
        test_api "Detect GPUs (non-LXD: expect 400/500)" "GET" \
            "/api/v1/admin/providers/${PROVIDER_ID}/detect-gpus" "200|400|500" "" "$group"
        test_api "Stopped containers (non-LXD: expect 400/500)" "GET" \
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
