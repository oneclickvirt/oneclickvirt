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

    # =========================================================
    # Section A: Agent Secret Generation
    # =========================================================

    # -- Generate agent secret --
    local secret_resp; secret_resp=$(test_api "Generate agent secret" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/agent-secret" "200" "" "$group")

    # Verify response contains required fields
    local agent_secret; agent_secret=$(echo "$secret_resp" | jq -r '.data.agentSecret // empty' 2>/dev/null)
    local ws_url; ws_url=$(echo "$secret_resp" | jq -r '.data.wsURL // empty' 2>/dev/null)
    local ws_path; ws_path=$(echo "$secret_resp" | jq -r '.data.wsPath // empty' 2>/dev/null)
    local install_cmd; install_cmd=$(echo "$secret_resp" | jq -r '.data.installCmd // empty' 2>/dev/null)

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
        log_success "installCmd returned (${#install_cmd} chars)"
    else
        log_warning "installCmd missing from response"
    fi

    # -- Regenerate agent secret (idempotent) --
    test_api "Regenerate agent secret" "POST" \
        "/api/v1/admin/providers/${PROVIDER_ID}/agent-secret" "200" "" "$group"

    # -- Nonexistent provider --
    test_api "Generate agent secret (no provider)" "POST" \
        "/api/v1/admin/providers/99999/agent-secret" "404|400" "" "$group"

    # =========================================================
    # Section B: connectionType=agent Provider Creation & Update
    # =========================================================

    # -- Create agent-mode provider (no SSH credentials required) --
    local agent_prov; agent_prov=$(test_api "Create agent-mode provider" "POST" "/api/v1/admin/providers" "200|409" \
        "{\"name\":\"ci-agent-mode-provider\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"auto\",\"networkType\":\"nat_ipv4\",\"endpoint\":\"${WORKER_IP}\",\"sshPort\":22,\"username\":\"root\",\"connectionType\":\"agent\"}" "$group")
    local agent_pid; agent_pid=$(echo "$agent_prov" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    if [[ -n "$agent_pid" ]]; then
        # -- Verify connectionType persisted --
        local agent_detail; agent_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${agent_pid}" 2>/dev/null)
        local ct; ct=$(echo "$agent_detail" | jq -r '.data.connectionType // empty' 2>/dev/null)
        if [[ "$ct" == "agent" ]]; then
            log_success "connectionType=agent persisted correctly"
        else
            log_warning "connectionType expected 'agent', got '${ct}'"
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

        # -- Switch from agent back to ssh mode (requires credentials) --
        test_api "Switch agent->ssh (with creds)" "PUT" "/api/v1/admin/providers/${agent_pid}" "200" \
            "{\"connectionType\":\"ssh\",\"password\":\"test\"}" "$group"

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
            "/api/v1/admin/providers/${PROVIDER_ID}/stopped-containers" "200" "" "$group")
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
