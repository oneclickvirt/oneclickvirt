#!/bin/bash
# Module 09: Provider Management
# Dependencies: 01_init (ADMIN_TOKEN), worker node (WORKER_IP + WORKER_PASSWORD or ALICE_PRIVATE_KEY)

run_module_09() {
    report_add_section "09 - Provider Management"
    local group="providers"

    if [[ -z "$WORKER_IP" && -n "$NODE_IP" ]]; then
        WORKER_IP="$NODE_IP"
    fi
    local worker_pass="${WORKER_PASSWORD:-${NODE_PASSWORD:-}}"
    local worker_key="${ALICE_PRIVATE_KEY:-}"
    if [[ -z "$WORKER_IP" || ( -z "$worker_pass" && -z "$worker_key" ) ]]; then
        chain_break "$group" "No worker node information (need IP + password or SSH key)"
        return 1
    fi

    # -- Provider list --
    test_api "Provider list" "GET" "/api/v1/admin/providers?page=1&pageSize=10" "200" "" "$group"

    # -- SSH connection test (use available auth method) --
    if [[ -n "$worker_pass" ]]; then
        test_api "Test SSH connection (password)" "POST" "/api/v1/admin/providers/test-ssh-connection" "200|400|500" \
            "{\"host\":\"${WORKER_IP}\",\"port\":22,\"username\":\"root\",\"password\":\"${worker_pass}\"}" "$group"
    fi
    if [[ -n "$worker_key" ]]; then
        local escaped_key; escaped_key=$(echo "$worker_key" | jq -Rsa .)
        test_api "Test SSH connection (key)" "POST" "/api/v1/admin/providers/test-ssh-connection" "200|400" \
            "{\"host\":\"${WORKER_IP}\",\"port\":22,\"username\":\"root\",\"sshKey\":${escaped_key}}" "$group"
    fi

    # -- SSH test with invalid credentials --
    test_api "Test SSH (invalid)" "POST" "/api/v1/admin/providers/test-ssh-connection" "400|500" \
        '{"host":"192.0.2.1","port":22,"username":"root","password":"wrong"}' "$group"

    # -- Check provider name --
    test_api "Check provider name" "GET" "/api/v1/admin/providers/check-name?name=ci-test-provider" "200" "" "$group"

    # -- Check endpoint --
    test_api "Check endpoint" "GET" "/api/v1/admin/providers/check-endpoint?endpoint=${WORKER_IP}&sshPort=22" "200" "" "$group"

    # -- Create provider (or reuse existing one from state restoration) --
    if [[ -n "$PROVIDER_ID" ]]; then
        # Provider ID exists (restored from previous module),verify it's still valid
        log_info "Using existing provider ID: ${PROVIDER_ID}"
        local verify_resp; verify_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null) || true
        local verify_code; verify_code=$(echo "$verify_resp" | jq -r '.code // empty' 2>/dev/null)
        if [[ "$verify_code" != "200" ]]; then
            log_warning "Existing PROVIDER_ID ${PROVIDER_ID} is invalid, creating new one"
            PROVIDER_ID=""
        fi
    fi
    
    if [[ -z "$PROVIDER_ID" ]]; then
        # Build auth payload: prefer password, fall back to SSH key
        local auth_payload
        if [[ -n "$worker_pass" ]]; then
            auth_payload="\"password\":\"${worker_pass}\""
        elif [[ -n "$worker_key" ]]; then
            local escaped_key_create; escaped_key_create=$(echo "$worker_key" | jq -Rsa .)
            auth_payload="\"sshKey\":${escaped_key_create}"
        fi
        log_info "Creating provider with executionRule=${EXECUTION_RULE}"
        local pr; pr=$(test_api "Create provider" "POST" "/api/v1/admin/providers" "200" \
            "{\"name\":\"ci-${ENV_TYPE}-provider\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"${EXECUTION_RULE}\",\"networkType\":\"nat_ipv4\",\"endpoint\":\"${WORKER_IP}\",\"sshPort\":22,\"username\":\"root\",${auth_payload}}" "$group")
        
        # Debug: log the response
        log_debug "Provider creation response: ${pr}"
        
        # Try multiple possible field names for the provider ID
        PROVIDER_ID=$(echo "$pr" | jq -r '.data.id // .data.ID // .data.provider_id // .data.providerId // .data.providerID // empty' 2>/dev/null)
        
        # If still empty, try to get from list (newly created should be the only one or last one)
        if [[ -z "$PROVIDER_ID" ]]; then
            log_warning "Provider ID not found in response, fetching from provider list..."
            local list_resp; list_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                "${SERVER_URL}/api/v1/admin/providers?page=1&pageSize=10" 2>/dev/null) || true
            PROVIDER_ID=$(echo "$list_resp" | jq -r '.data.list[]? | select(.name=="ci-'"${ENV_TYPE}"'-provider") | .id // .ID' 2>/dev/null | head -1)
        fi
        
        if [[ -z "$PROVIDER_ID" ]]; then
            log_error "Failed to extract provider ID from response or list"
            log_error "Response was: ${pr}"
            chain_break "$group" "Provider creation failed - no ID in response"
            return 1
        fi
        
        log_info "Created new provider ID: ${PROVIDER_ID}"
    fi

    # -- Create duplicate name --
    test_api "Create duplicate provider" "POST" "/api/v1/admin/providers" "409" \
        "{\"name\":\"ci-${ENV_TYPE}-provider\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"${EXECUTION_RULE}\",\"networkType\":\"nat_ipv4\",\"endpoint\":\"${WORKER_IP}\",\"sshPort\":22,\"username\":\"root\",${auth_payload}}" "$group"

    # -- Edit provider --
    test_api "Edit provider" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        '{"name":"ci-provider-updated"}' "$group"
    test_api "Edit provider back" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        "{\"name\":\"ci-${ENV_TYPE}-provider\"}" "$group"

    # -- Auto configure (required for api_only and auto execution rules, skip for ssh_only) --
    if [[ "$EXECUTION_RULE" != "ssh_only" ]]; then
        # -- Auto configure (streaming) --
        test_api_retry "Auto configure (stream)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/auto-configure-stream" "200" \
            '{}' 3 10 "$group"
        sleep 5

        # -- Auto configure (task) --
        local ac; ac=$(test_api "Auto configure (task)" "POST" "/api/v1/admin/providers/auto-configure" "200|400" \
            "{\"providerId\":${PROVIDER_ID}}" "$group")
        local ac_task; ac_task=$(echo "$ac" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$ac_task" ]]; then
            wait_task_complete "$SERVER_URL" "$ac_task" "$ADMIN_TOKEN" 300 10 || true
        fi
    else
        log_info "Skipping auto-configure for ssh_only execution rule"
    fi

    # -- Health check --
    test_api_retry "Provider health check" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/health-check" "200" \
        '{}' 3 10 "$group"

    # -- Provider status --
    test_api "Provider status" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "200" "" "$group"

    # -- Certificate generation (may fail for certain provider types or unconfigured providers) --
    test_api "Generate certificate" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/generate-cert" "200|400|500" \
        '{}' "$group"

    # -- Port configuration --
    test_api "Update port config" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/port-config" "200" \
        '{"portRangeStart":20000,"portRangeEnd":30000,"defaultPortCount":10,"networkType":"nat_ipv4"}' "$group"
    test_api "Get port usage" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/port-usage" "200" "" "$group"

    # -- IPv4 pool --
    test_api "Set IPv4 pool" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" \
        '{"addresses":"10.0.0.100/24"}' "$group"
    test_api "Get IPv4 pool" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" "" "$group"

    # -- Delete specific IPv4 pool entry --
    local pool_resp; pool_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" 2>/dev/null)
    local pool_entry_id; pool_entry_id=$(echo "$pool_resp" | jq -r '.data[0].id // .data.list[0].id // empty' 2>/dev/null)
    if [[ -n "$pool_entry_id" ]]; then
        test_api "Delete IPv4 pool entry" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool/${pool_entry_id}" "200" "" "$group"
    fi

    test_api "Clear IPv4 pool" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" "" "$group"

    # -- Configuration tasks --
    test_api "Configuration tasks" "GET" "/api/v1/admin/configuration-tasks?page=1&pageSize=10" "200" "" "$group"

    # -- Hardware report --
    test_api "Save hardware report" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200|400|500" \
        '{"pasteUrl":"https://paste.spiritlhl.net/#/show/ENn4E.txt"}' "$group"
    test_api "Save hardware report (invalid URL)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "400" \
        '{"pasteUrl":"https://example.com/some-report.txt"}' "$group"
    test_api "Get hardware report" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200|404" "" "$group"
    test_api_noauth "Public hardware report" "GET" "/api/v1/public/providers/${PROVIDER_ID}/hardware-report" "200|404" "" "$group"
    test_api "Delete hardware report" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200" "" "$group"

    # -- Checkin config --
    test_api "Get checkin config" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/checkin-config" "200" "" "$group"
    test_api "Update checkin config" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/checkin-config" "200" \
        '{"enabled":true,"extension_hours":24}' "$group"

    # -- Domain config --
    test_api "Get domain config" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" "" "$group"
    test_api "Update domain config" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" \
        '{"enabled":true,"base_domain":"test.example.com"}' "$group"

    # -- Export configs --
    test_api "Export provider configs" "POST" "/api/v1/admin/providers/export-configs" "200" \
        '{"format":"json"}' "$group"

    # -- Provider API routes --
    test_api "Provider API list" "GET" "/api/v1/providers" "200" "" "$group"
    test_api "Provider API status" "GET" "/api/v1/providers/${PROVIDER_ID}/status" "200" "" "$group"
    test_api "Provider API capabilities" "GET" "/api/v1/providers/${PROVIDER_ID}/capabilities" "200" "" "$group"
    test_api "Provider API images" "GET" "/api/v1/providers/${PROVIDER_ID}/images" "200|400|500" "" "$group"

    # -- Traffic history --
    test_api "Provider traffic history" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/traffic/history" "200" "" "$group"

    # -- Provider creation with SSH key auth (verify both auth methods) --
    if [[ -n "${ALICE_PRIVATE_KEY:-}" ]]; then
        local escaped_key; escaped_key=$(echo "$ALICE_PRIVATE_KEY" | jq -Rsa .)
        local key_provider; key_provider=$(test_api "Create provider (key auth)" "POST" "/api/v1/admin/providers" "200|409" \
            "{\"name\":\"ci-${ENV_TYPE}-key-provider\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"auto\",\"networkType\":\"nat_ipv4\",\"endpoint\":\"${WORKER_IP}\",\"sshPort\":22,\"username\":\"root\",\"sshKey\":${escaped_key}}" "$group")
        local key_pid; key_pid=$(echo "$key_provider" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
        if [[ -n "$key_pid" ]]; then
            test_api "Key provider status" "GET" "/api/v1/admin/providers/${key_pid}/status" "200" "" "$group"
            test_api "Delete key provider" "DELETE" "/api/v1/admin/providers/${key_pid}" "200" "" "$group"
        fi
    fi

    # -- Negative tests --
    # Create provider with missing required fields
    test_api "Create provider (no name)" "POST" "/api/v1/admin/providers" "400" \
        "{\"type\":\"docker\",\"endpoint\":\"${WORKER_IP}\",\"sshPort\":22,\"username\":\"root\",\"password\":\"test\"}" "$group"

    # Create provider with invalid SSH port
    test_api "Create provider (invalid port)" "POST" "/api/v1/admin/providers" "400" \
        '{"name":"invalid-port-provider","type":"docker","endpoint":"192.0.2.1","sshPort":99999,"username":"root","password":"test"}' "$group"

    # Get nonexistent provider
    test_api "Get nonexistent provider" "GET" "/api/v1/admin/providers/99999" "404" "" "$group"

    # Delete nonexistent provider
    test_api "Delete nonexistent provider" "DELETE" "/api/v1/admin/providers/99999" "404|400" "" "$group"

    # Edit nonexistent provider
    test_api "Edit nonexistent provider" "PUT" "/api/v1/admin/providers/99999" "404|400" \
        '{"name":"ghost-provider"}' "$group"

    # Health check on nonexistent provider
    test_api "Health check nonexistent" "POST" "/api/v1/admin/providers/99999/health-check" "404|400" '{}' "$group"
}
