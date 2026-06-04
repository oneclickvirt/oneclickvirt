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
    # Build auth payload at function scope (always set to avoid set -u issues)
    local auth_payload=""
    if [[ -n "$worker_pass" ]]; then
        auth_payload="\"password\":\"${worker_pass}\""
    elif [[ -n "$worker_key" ]]; then
        local escaped_key_create; escaped_key_create=$(echo "$worker_key" | jq -Rsa .)
        auth_payload="\"sshKey\":${escaped_key_create}"
    fi

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

    # -- nodeInstallType / bridge fields (proxmox only) --
    if [[ "$ENV_TYPE" == "proxmox" || "$ENV_TYPE" == "proxmoxve" ]]; then
        # Verify default nodeInstallType is "script" in the created provider
        local provider_detail; provider_detail=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null) || true
        local node_install_type; node_install_type=$(echo "$provider_detail" | jq -r '.data.nodeInstallType // empty' 2>/dev/null)
        if [[ "$node_install_type" == "script" || "$node_install_type" == "" ]]; then
            log_info "nodeInstallType default=script verified"
        else
            log_warning "nodeInstallType default expected 'script', got '${node_install_type}'"
        fi

        # Update to script install type (no bridge fields required)
        test_api "Set nodeInstallType=script" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
            '{"nodeInstallType":"script"}' "$group"

        # Update to third_party with all required bridge fields
        test_api "Set nodeInstallType=third_party" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
            '{"nodeInstallType":"third_party","bridgeNAT":"vmbr1","bridgeDedicatedV4":"vmbr0","bridgeDedicatedV6":"","natSubnet":"172.16.1.0/24"}' "$group"

        # Verify third_party fields were saved
        local tp_detail; tp_detail=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null) || true
        local saved_nat; saved_nat=$(echo "$tp_detail" | jq -r '.data.bridgeNAT // empty' 2>/dev/null)
        local saved_v4; saved_v4=$(echo "$tp_detail" | jq -r '.data.bridgeDedicatedV4 // empty' 2>/dev/null)
        local saved_subnet; saved_subnet=$(echo "$tp_detail" | jq -r '.data.natSubnet // empty' 2>/dev/null)
        if [[ "$saved_nat" == "vmbr1" && "$saved_v4" == "vmbr0" && "$saved_subnet" == "172.16.1.0/24" ]]; then
            log_info "third_party bridge fields saved correctly"
        else
            log_warning "third_party fields mismatch: bridgeNAT=${saved_nat} bridgeDedicatedV4=${saved_v4} natSubnet=${saved_subnet}"
        fi

        # Test with custom subnet (different from default 172.16.1.0/24)
        test_api "Set third_party custom subnet" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
            '{"nodeInstallType":"third_party","bridgeNAT":"br0","bridgeDedicatedV4":"br1","bridgeDedicatedV6":"br2","natSubnet":"10.10.0.0/24"}' "$group"

        # Revert back to script install for subsequent tests
        test_api "Revert nodeInstallType=script" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
            '{"nodeInstallType":"script"}' "$group"

        # Test creating a proxmox provider with third_party type (validation should pass with required fields)
        local tp_create_resp; tp_create_resp=$(test_api "Create proxmox third_party provider" "POST" "/api/v1/admin/providers" "200|409" \
            "{\"name\":\"ci-proxmox-thirdparty\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"${EXECUTION_RULE}\",\"networkType\":\"nat_ipv4\",\"endpoint\":\"${WORKER_IP}\",\"sshPort\":22,\"username\":\"root\",${auth_payload},\"nodeInstallType\":\"third_party\",\"bridgeNAT\":\"vmbr1\",\"bridgeDedicatedV4\":\"vmbr0\",\"bridgeDedicatedV6\":\"\",\"natSubnet\":\"172.16.1.0/24\"}" "$group")
        local tp_pid; tp_pid=$(echo "$tp_create_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
        if [[ -n "$tp_pid" ]]; then
            test_api "Delete third_party test provider" "DELETE" "/api/v1/admin/providers/${tp_pid}" "200" "" "$group"
        fi
    fi

    # -- Auto configure (required for api_only and auto execution rules, skip for ssh_only) --
    if [[ "$EXECUTION_RULE" != "ssh_only" ]]; then
        # -- Auto configure (streaming) --
        test_api_retry "Auto configure (stream)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/auto-configure-stream" "200" \
            '{}' 3 10 "$group"
        sleep 5

        # -- Auto configure (task) --
        local ac; ac=$(test_api "Auto configure (task)" "POST" "/api/v1/admin/providers/auto-configure" "200|400|500" \
            "{\"providerId\":${PROVIDER_ID}}" "$group")
        local ac_task; ac_task=$(echo "$ac" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$ac_task" ]]; then
            wait_task_complete "$SERVER_URL" "$ac_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 || true
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
        "{\"provider_ids\":[${PROVIDER_ID}]}" "$group"

    # -- CSV import/export --
    test_api "Export providers CSV (selected)" "GET" "/api/v1/admin/providers/export-csv?ids=${PROVIDER_ID}" "200" "" "$group"
    test_api "Export providers CSV (empty template)" "GET" "/api/v1/admin/providers/export-csv?ids=999999999" "200" "" "$group"

    local csv_template_tmp; csv_template_tmp=$(mktemp)
    local csv_template_code
    csv_template_code=$(curl -s --max-time 60 -o "$csv_template_tmp" -w "%{http_code}" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/export-csv?ids=999999999" 2>/dev/null || echo "000")
    if [[ "$csv_template_code" == "200" ]]; then
        local csv_template_header; csv_template_header=$(head -n 1 "$csv_template_tmp" | tr -d '\r')
        if [[ "$csv_template_header" == id,name,type,* ]]; then
            log_success "Exported empty CSV template contains header row"
        else
            log_error "CSV template header mismatch: ${csv_template_header}"
            rm -f "$csv_template_tmp"
            chain_break "$group" "CSV template header mismatch"
            return 1
        fi
    else
        log_error "Export empty CSV template failed, HTTP ${csv_template_code}"
        rm -f "$csv_template_tmp"
        chain_break "$group" "CSV template export failed"
        return 1
    fi
    rm -f "$csv_template_tmp"

    local csv_import_name="ci-csv-import-${ENV_TYPE}-${RANDOM}"
    local csv_import_file; csv_import_file=$(mktemp)
    cat > "$csv_import_file" <<EOF
id,name,type,endpoint,portIP,sshPort,username,password,sshKey,connectionType,status,architecture,container_enabled,vm_enabled,allowClaim,redeemCodeOnly,region,country,countryCode,city,executionRule,networkType,defaultPortCount,portRangeStart,portRangeEnd,maxTraffic,trafficCountMode,trafficMultiplier,enableTrafficControl,enableResourceMonitoring
,${csv_import_name},${ENV_TYPE},,,,,,,agent,active,amd64,true,false,true,false,csv-region,,,,auto,no_port_mapping,10,10000,65535,1048576,both,1,false,false
EOF

    local csv_import_resp; csv_import_resp=$(curl -s --max-time 120 -w "\n%{http_code}" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -F "file=@${csv_import_file};type=text/csv" \
        "${SERVER_URL}/api/v1/admin/providers/import-csv" 2>/dev/null || true)
    local csv_import_code; csv_import_code=$(echo "$csv_import_resp" | tail -1)
    local csv_import_body; csv_import_body=$(echo "$csv_import_resp" | sed '$d')
    if [[ "$csv_import_code" != "200" ]]; then
        log_error "Import providers CSV failed, expected 200 got ${csv_import_code}"
        log_error "Import response: ${csv_import_body}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import create failed"
        return 1
    fi
    local csv_created; csv_created=$(echo "$csv_import_body" | jq -r '.data.created // 0' 2>/dev/null)
    if [[ "$csv_created" == "0" ]]; then
        log_error "CSV import create returned created=0: ${csv_import_body}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import create did not create row"
        return 1
    fi

    local csv_created_id
    local csv_list_resp; csv_list_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers?page=1&pageSize=200&name=${csv_import_name}" 2>/dev/null || true)
    csv_created_id=$(echo "$csv_list_resp" | jq -r '.data.list[]? | select(.name=="'"${csv_import_name}"'") | .id // .ID' 2>/dev/null | head -1)
    if [[ -z "$csv_created_id" ]]; then
        log_error "CSV-created provider not found by name: ${csv_import_name}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import create verification failed"
        return 1
    fi

    cat > "$csv_import_file" <<EOF
id,name,type,endpoint,portIP,sshPort,username,password,sshKey,connectionType,status,architecture,container_enabled,vm_enabled,allowClaim,redeemCodeOnly,region,country,countryCode,city,executionRule,networkType,defaultPortCount,portRangeStart,portRangeEnd,maxTraffic,trafficCountMode,trafficMultiplier,enableTrafficControl,enableResourceMonitoring
${csv_created_id},${csv_import_name},${ENV_TYPE},,,,,,,agent,active,amd64,true,false,true,false,csv-region-updated,,,,auto,no_port_mapping,10,10000,65535,1048576,both,1,false,false
EOF

    local csv_update_resp; csv_update_resp=$(curl -s --max-time 120 -w "\n%{http_code}" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -F "file=@${csv_import_file};type=text/csv" \
        "${SERVER_URL}/api/v1/admin/providers/import-csv" 2>/dev/null || true)
    local csv_update_code; csv_update_code=$(echo "$csv_update_resp" | tail -1)
    local csv_update_body; csv_update_body=$(echo "$csv_update_resp" | sed '$d')
    if [[ "$csv_update_code" != "200" ]]; then
        log_error "Import providers CSV update failed, expected 200 got ${csv_update_code}"
        log_error "Import update response: ${csv_update_body}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import update failed"
        return 1
    fi

    local csv_updated; csv_updated=$(echo "$csv_update_body" | jq -r '.data.updated // 0' 2>/dev/null)
    if [[ "$csv_updated" == "0" ]]; then
        log_error "CSV import update returned updated=0: ${csv_update_body}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import update did not update row"
        return 1
    fi

    local csv_detail_resp; csv_detail_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${csv_created_id}" 2>/dev/null || true)
    local csv_region; csv_region=$(echo "$csv_detail_resp" | jq -r '.data.region // empty' 2>/dev/null)
    if [[ "$csv_region" != "csv-region-updated" ]]; then
        log_error "CSV import update verification failed: expected region=csv-region-updated, got ${csv_region}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import update verification failed"
        return 1
    fi

    # Fallback matching by name: when id is empty, existing provider should be updated by name
    cat > "$csv_import_file" <<EOF
id,name,type,endpoint,portIP,sshPort,username,password,sshKey,connectionType,status,architecture,container_enabled,vm_enabled,allowClaim,redeemCodeOnly,region,country,countryCode,city,executionRule,networkType,defaultPortCount,portRangeStart,portRangeEnd,maxTraffic,trafficCountMode,trafficMultiplier,enableTrafficControl,enableResourceMonitoring
,${csv_import_name},${ENV_TYPE},,,,,,,agent,active,amd64,true,false,true,false,csv-region-by-name,,,,auto,no_port_mapping,10,10000,65535,1048576,both,1,false,false
EOF

    local csv_name_update_resp; csv_name_update_resp=$(curl -s --max-time 120 -w "\n%{http_code}" \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -F "file=@${csv_import_file};type=text/csv" \
        "${SERVER_URL}/api/v1/admin/providers/import-csv" 2>/dev/null || true)
    local csv_name_update_code; csv_name_update_code=$(echo "$csv_name_update_resp" | tail -1)
    local csv_name_update_body; csv_name_update_body=$(echo "$csv_name_update_resp" | sed '$d')
    if [[ "$csv_name_update_code" != "200" ]]; then
        log_error "Import providers CSV by-name update failed, expected 200 got ${csv_name_update_code}"
        log_error "Import by-name response: ${csv_name_update_body}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import by-name update failed"
        return 1
    fi

    local csv_name_updated; csv_name_updated=$(echo "$csv_name_update_body" | jq -r '.data.updated // 0' 2>/dev/null)
    if [[ "$csv_name_updated" == "0" ]]; then
        log_error "CSV import by-name update returned updated=0: ${csv_name_update_body}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import by-name update did not update row"
        return 1
    fi

    local csv_name_detail_resp; csv_name_detail_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${csv_created_id}" 2>/dev/null || true)
    local csv_name_region; csv_name_region=$(echo "$csv_name_detail_resp" | jq -r '.data.region // empty' 2>/dev/null)
    if [[ "$csv_name_region" != "csv-region-by-name" ]]; then
        log_error "CSV import by-name verification failed: expected region=csv-region-by-name, got ${csv_name_region}"
        rm -f "$csv_import_file"
        chain_break "$group" "CSV import by-name verification failed"
        return 1
    fi

    rm -f "$csv_import_file"
    test_api "Delete CSV imported provider" "DELETE" "/api/v1/admin/providers/${csv_created_id}" "200" "" "$group"

    # -- Provider API routes --
    test_api "Provider API list" "GET" "/api/v1/providers" "200" "" "$group"
    test_api "Provider API status" "GET" "/api/v1/providers/${PROVIDER_ID}/status" "200" "" "$group"
    test_api "Provider API capabilities" "GET" "/api/v1/providers/${PROVIDER_ID}/capabilities" "200" "" "$group"
    test_api "Provider API images" "GET" "/api/v1/providers/${PROVIDER_ID}/images" "200|400|500" "" "$group"

    # -- Traffic history --
    test_api "Provider traffic history" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/traffic/history" "200" "" "$group"

    # -- isPureNode field: create provider with isPureNode=true and verify --
    local pure_node_resp; pure_node_resp=$(test_api "Create isPureNode provider" "POST" "/api/v1/admin/providers" "200|409" \
        "{\"name\":\"ci-pure-node\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"${EXECUTION_RULE}\",\"networkType\":\"nat_ipv4\",\"endpoint\":\"${WORKER_IP}\",\"sshPort\":22,\"username\":\"root\",${auth_payload},\"isPureNode\":true}" "$group")
    local pure_pid; pure_pid=$(echo "$pure_node_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    if [[ -n "$pure_pid" ]]; then
        local pure_detail; pure_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/providers/${pure_pid}" 2>/dev/null)
        local is_pure; is_pure=$(echo "$pure_detail" | jq -r '.data.isPureNode // false' 2>/dev/null)
        if [[ "$is_pure" == "true" ]]; then
            log_success "isPureNode=true saved and returned correctly"
        else
            log_warning "isPureNode mismatch: expected true, got '${is_pure}'"
        fi
        test_api "Delete isPureNode provider" "DELETE" "/api/v1/admin/providers/${pure_pid}" "200" "" "$group"
    fi

    # -- gpuEnabled field: update provider with gpuEnabled=true --
    test_api "Update provider gpuEnabled" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        '{"gpuEnabled":true,"gpuDeviceIds":"0"}' "$group"
    test_api "Update provider gpuEnabled off" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        '{"gpuEnabled":false,"gpuDeviceIds":""}' "$group"

    # -- detect-gpus: SSH-based GPU detection (may fail on non-LXD, accept 400/500) --
    test_api "Detect provider GPUs" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/detect-gpus" "200|400|500" "" "$group"

    # -- stopped-containers: fetch stopped containers for copy mode (LXD/Incus only, accept 400/500 for other types) --
    test_api "Get stopped containers" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/stopped-containers" "200|400|500" "" "$group"

    # -- exec: run a command on provider via SSH --
    test_api "Exec command on provider" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/exec" "200|400|500" \
        '{"command":"echo hello","timeout":10}' "$group"

    # -- exec: empty command must fail --
    test_api "Exec empty command (400)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/exec" "400" \
        '{"command":"","timeout":10}' "$group"

    # -- exec: nonexistent provider --
    test_api "Exec nonexistent provider (404)" "POST" "/api/v1/admin/providers/99999/exec" "404|400" \
        '{"command":"echo hello","timeout":10}' "$group"

    # -- detect-gpus on nonexistent provider --
    test_api "Detect GPUs nonexistent provider" "GET" "/api/v1/admin/providers/99999/detect-gpus" "404|400" "" "$group"

    # -- stopped-containers on nonexistent provider --
    test_api "Stopped containers nonexistent provider" "GET" "/api/v1/admin/providers/99999/stopped-containers" "404|400" "" "$group"

    # -- connectionType: update to agent mode and verify field persists --
    test_api "Update connectionType=agent" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        '{"connectionType":"agent"}' "$group"
    local ct_detail; ct_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null)
    local saved_ct; saved_ct=$(echo "$ct_detail" | jq -r '.data.connectionType // empty' 2>/dev/null)
    if [[ "$saved_ct" == "agent" ]]; then
        log_success "connectionType=agent saved correctly"
    else
        log_warning "connectionType expected 'agent', got '${saved_ct}'"
    fi
    # Revert to ssh — must restore endpoint/sshPort/networkType/containerEnabled/virtualMachineEnabled
    # because "Update connectionType=agent" forced endpoint="" sshPort=0 networkType="no_port_mapping"
    # and direct bool assignment zeroed containerEnabled/virtualMachineEnabled.
    # Without this, SSH health checks fail for ~60 min and the provider is auto-frozen,
    # causing HTTP 400 in module 29 VM image creates (images 15-22) and module 30 failures.
    test_api "Revert connectionType=ssh" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        "{\"connectionType\":\"ssh\",\"endpoint\":\"${WORKER_IP}\",\"sshPort\":22,\"username\":\"root\",\"networkType\":\"nat_ipv4\",\"containerEnabled\":true,\"virtualMachineEnabled\":true,${auth_payload}}" "$group"
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

    # Create provider with out-of-range SSH port (backend accepts any int, no port-range validation on create)
    local inv_port_resp; inv_port_resp=$(test_api "Create provider (invalid port)" "POST" "/api/v1/admin/providers" "200|409" \
        '{"name":"invalid-port-provider","type":"docker","endpoint":"192.0.2.1","sshPort":99999,"username":"root","password":"test"}' "$group")
    local inv_port_id; inv_port_id=$(echo "$inv_port_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    if [[ -n "$inv_port_id" ]]; then
        test_api "Delete invalid-port provider" "DELETE" "/api/v1/admin/providers/${inv_port_id}" "200" "" "$group"
    fi

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
