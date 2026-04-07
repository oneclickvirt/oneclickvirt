#!/bin/bash
# Module 09: Provider Management
# Dependencies: 01_init (ADMIN_TOKEN), worker node (WORKER_IP, WORKER_PASSWORD)

run_module_09() {
    report_add_section "09 - Provider Management"
    local group="providers"

    if [[ -z "$WORKER_IP" && -n "$NODE_IP" ]]; then
        WORKER_IP="$NODE_IP"
    fi
    local worker_pass="${WORKER_PASSWORD:-${NODE_PASSWORD:-}}"
    if [[ -z "$WORKER_IP" || -z "$worker_pass" ]]; then
        chain_break "$group" "No worker node information"
        return 1
    fi

    # -- Provider list --
    test_api "Provider list" "GET" "/api/v1/admin/providers?page=1&pageSize=10" "200" "" "$group"

    # -- SSH connection test --
    test_api "Test SSH connection" "POST" "/api/v1/admin/providers/test-ssh-connection" "200" \
        "{\"ssh_host\":\"${WORKER_IP}\",\"ssh_port\":22,\"ssh_user\":\"root\",\"ssh_password\":\"${worker_pass}\"}" "$group"

    # -- SSH test with invalid credentials --
    test_api "Test SSH (invalid)" "POST" "/api/v1/admin/providers/test-ssh-connection" "400" \
        '{"ssh_host":"192.0.2.1","ssh_port":22,"ssh_user":"root","ssh_password":"wrong"}' "$group"

    # -- Check provider name --
    test_api "Check provider name" "GET" "/api/v1/admin/providers/check-name?name=ci-test-provider" "200" "" "$group"

    # -- Check endpoint --
    test_api "Check endpoint" "GET" "/api/v1/admin/providers/check-endpoint?host=${WORKER_IP}&port=22" "200" "" "$group"

    # -- Create provider --
    local pr; pr=$(test_api "Create provider" "POST" "/api/v1/admin/providers" "200" \
        "{\"name\":\"ci-${ENV_TYPE}-provider\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"auto\",\"networkType\":\"nat_ipv4\",\"ssh_host\":\"${WORKER_IP}\",\"ssh_port\":22,\"ssh_user\":\"root\",\"ssh_password\":\"${worker_pass}\"}" "$group")
    PROVIDER_ID=$(echo "$pr" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    [[ -z "$PROVIDER_ID" ]] && { chain_break "$group" "Provider creation failed"; return 1; }
    log_info "Provider ID: ${PROVIDER_ID}"

    # -- Create duplicate name --
    test_api "Create duplicate provider" "POST" "/api/v1/admin/providers" "400" \
        "{\"name\":\"ci-${ENV_TYPE}-provider\",\"type\":\"${ENV_TYPE}\",\"executionRule\":\"auto\",\"networkType\":\"nat_ipv4\",\"ssh_host\":\"${WORKER_IP}\",\"ssh_port\":22,\"ssh_user\":\"root\",\"ssh_password\":\"${worker_pass}\"}" "$group"

    # -- Edit provider --
    test_api "Edit provider" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        '{"name":"ci-provider-updated"}' "$group"
    test_api "Edit provider back" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        "{\"name\":\"ci-${ENV_TYPE}-provider\"}" "$group"

    # -- Auto configure (streaming) --
    test_api_retry "Auto configure (stream)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/auto-configure-stream" "200" \
        '{}' 3 10 "$group"
    sleep 5

    # -- Auto configure (task) --
    local ac; ac=$(test_api "Auto configure (task)" "POST" "/api/v1/admin/providers/auto-configure" "200" \
        "{\"provider_id\":${PROVIDER_ID}}" "$group")
    local ac_task; ac_task=$(echo "$ac" | jq -r '.data.task_id // empty' 2>/dev/null)
    if [[ -n "$ac_task" ]]; then
        wait_task_complete "$SERVER_URL" "$ac_task" "$ADMIN_TOKEN" 300 10 || true
    fi

    # -- Health check --
    test_api_retry "Provider health check" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/health-check" "200" \
        '{}' 3 10 "$group"

    # -- Provider status --
    test_api "Provider status" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "200" "" "$group"

    # -- Certificate generation --
    test_api "Generate certificate" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/generate-cert" "200" \
        '{}' "$group"

    # -- Port configuration --
    test_api "Update port config" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/port-config" "200" \
        '{"start_port":20000,"end_port":30000}' "$group"
    test_api "Get port usage" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/port-usage" "200" "" "$group"

    # -- IPv4 pool --
    test_api "Set IPv4 pool" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" \
        '{"entries":[{"ip":"10.0.0.100","prefix":24,"gateway":"10.0.0.1"}]}' "$group"
    test_api "Get IPv4 pool" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" "" "$group"
    test_api "Clear IPv4 pool" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" "" "$group"

    # -- Configuration tasks --
    test_api "Configuration tasks" "GET" "/api/v1/admin/configuration-tasks?page=1&pageSize=10" "200" "" "$group"

    # -- Hardware report --
    test_api "Save hardware report" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200" \
        '{"cpu_model":"Test CPU","cpu_cores":4,"memory_total":8192,"disk_total":100000}' "$group"
    test_api "Get hardware report" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200" "" "$group"
    test_api_noauth "Public hardware report" "GET" "/api/v1/public/providers/${PROVIDER_ID}/hardware-report" "200" "" "$group"
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
    test_api "Provider API list" "GET" "/api/v1/providers/" "200" "" "$group"
    test_api "Provider API status" "GET" "/api/v1/providers/${PROVIDER_ID}/status" "200" "" "$group"
    test_api "Provider API capabilities" "GET" "/api/v1/providers/${PROVIDER_ID}/capabilities" "200" "" "$group"
    test_api "Provider API images" "GET" "/api/v1/providers/${PROVIDER_ID}/images" "200" "" "$group"

    # -- Traffic history --
    test_api "Provider traffic history" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/traffic/history" "200" "" "$group"
}
