#!/bin/bash
# Module 10: Instance Lifecycle (Admin + User, Container + VM)
# Dependencies: 01_init (ADMIN_TOKEN), 09_providers (PROVIDER_ID)

_m10_host_ipv6_probe_script() {
    local expected_address="$1"
    local expected_interface="${2:-}"
    printf 'address=%q\ninterface=%q\n' "$expected_address" "$expected_interface"
    cat <<'HOST_IPV6_PROBE'
set -uo pipefail
addr_present=0
route_present=0
if LC_ALL=C NO_COLOR=1 ip -j -6 addr show 2>/dev/null | python3 -c '
import ipaddress, json, re, sys
try:
    rows = json.loads(re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", sys.stdin.read()))
    address = ipaddress.IPv6Address(sys.argv[1])
    interface = sys.argv[2]
    found = isinstance(rows, list) and any(
        ipaddress.IPv6Address(info["local"]) == address
        for row in rows if not interface or row.get("ifname") == interface
        for info in row.get("addr_info", [])
        if info.get("family") == "inet6")
except (KeyError, TypeError, ValueError, json.JSONDecodeError):
    found = False
sys.exit(0 if found else 1)
' "$address" "$interface"; then addr_present=1; fi
if LC_ALL=C NO_COLOR=1 ip -j -6 route show default 2>/dev/null | python3 -c '
import json, re, sys
try:
    rows = json.loads(re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", sys.stdin.read()))
    found = isinstance(rows, list) and any(row.get("dst") == "default" for row in rows)
except (TypeError, ValueError, json.JSONDecodeError):
    found = False
sys.exit(0 if found else 1)
'; then route_present=1; fi
probe_ipv6() {
    local interface_arg="${1:-}" endpoint value
    for endpoint in https://ipv6.icanhazip.com https://api64.ipify.org https://ipv6.ip.sb; do
        if [ -n "$interface_arg" ]; then
            value=$(curl --noproxy '*' -6 --interface "$interface_arg" -fsS --connect-timeout 6 --max-time 12 "$endpoint" 2>/dev/null | tr -d '\r' | tail -1)
        else
            value=$(curl --noproxy '*' -6 -fsS --connect-timeout 6 --max-time 12 "$endpoint" 2>/dev/null | tr -d '\r' | tail -1)
        fi
        if [ -n "$value" ] && printf '%s' "$value" | grep -Fq ':'; then
            printf '%s' "$value"
            return 0
        fi
    done
    return 1
}
host_bound=$(probe_ipv6 "$address" 2>/dev/null || true)
host_default=$(probe_ipv6 2>/dev/null || true)
printf '%s|%s|%s|%s\n' "$addr_present" "$route_present" "$host_bound" "$host_default"
HOST_IPV6_PROBE
}

_m10_host_ipv6_source_probe_script() {
    cat <<'HOST_IPV6_SOURCE_PROBE'
set -euo pipefail
address_json=$(LC_ALL=C NO_COLOR=1 ip -j -6 addr show scope global)
route_json=$(LC_ALL=C NO_COLOR=1 ip -j -6 route get 2606:4700:4700::1111)
python3 - "$address_json" "$route_json" <<'PY'
import ipaddress
import json
import re
import sys

def parse(raw):
    return json.loads(re.sub(r"\x1b\[[0-?]*[ -/]*[@-~]", "", raw))

try:
    interfaces = parse(sys.argv[1])
    routes = parse(sys.argv[2])
    if not isinstance(interfaces, list) or not isinstance(routes, list) or not routes:
        raise ValueError("invalid IPv6 interface or route data")
    route = routes[0]
    interface = route.get("dev", "")
    source = route.get("prefsrc") or route.get("src") or ""
    if not interface:
        raise ValueError("IPv6 route has no device")
    candidates = []
    for row in interfaces:
        if row.get("ifname") != interface:
            continue
        for info in row.get("addr_info", []):
            if info.get("family") != "inet6" or info.get("scope") != "global":
                continue
            if info.get("tentative") or info.get("dadfailed") or any(
                    flag in ("tentative", "dadfailed") for flag in info.get("flags", [])):
                continue
            address = ipaddress.IPv6Address(info["local"])
            if address.is_global and (not source or str(address) == str(ipaddress.IPv6Address(source))):
                candidates.append(address)
    if not candidates:
        raise ValueError("route-selected source is not a usable local global IPv6 address")
    print(str(candidates[0]) + "|" + interface)
except (KeyError, TypeError, ValueError, json.JSONDecodeError):
    raise SystemExit(1)
PY
HOST_IPV6_SOURCE_PROBE
}

_m10_host_ipv6_source_probe_command() {
    local probe_script encoded
    probe_script=$(_m10_host_ipv6_source_probe_script) || return 1
    encoded=$(printf '%s' "$probe_script" | base64 | tr -d '\r\n') || return 1
    [[ -n "$encoded" ]] || return 1
    printf "printf '%%s' '%s' | base64 -d | bash" "$encoded"
}

_m10_host_ipv6_probe_command() {
    local expected_address="$1" expected_interface="${2:-}" probe_script encoded
    probe_script=$(_m10_host_ipv6_probe_script "$expected_address" "$expected_interface") || return 1
    encoded=$(printf '%s' "$probe_script" | base64 | tr -d '\r\n') || return 1
    [[ -n "$encoded" ]] || return 1
    printf "printf '%%s' '%s' | base64 -d | bash" "$encoded"
}

run_module_10() {
    report_add_section "10 - Instance Lifecycle"
    local group="instances"
    # Live HZ runs keep successfully created guests so later checks and a
    # follow-up run can inspect them.  Set this to false only for a deliberately
    # disposable test environment.
    local preserve_test_instances="${ACTION_TEST_PRESERVE_INSTANCES:-true}"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider, skipping instance tests"
        return 1
    fi

    local _ssh_rc=0
    ensure_worker_ssh_reachable 120 "instance lifecycle" || _ssh_rc=$?
    if [[ $_ssh_rc -ne 0 ]]; then
        if [[ $_ssh_rc -eq 75 ]]; then
            exit 75
        fi
        chain_break "$group" "Worker SSH is not reachable before instance tests"
        return 1
    fi
    if declare -F stabilize_worker_network_for_env >/dev/null 2>&1 && [[ -n "${WORKER_IP:-}" ]]; then
        stabilize_worker_network_for_env "$WORKER_IP" "${ENV_TYPE:-}" "worker before instance lifecycle" || {
            record_skip_result "Worker DNS/runtime precheck" "SSH" "${WORKER_IP}" \
                "worker DNS/runtime stabilization failed before instance lifecycle" "$group"
            exit 75
        }
    fi

    # -- Health check before instance creation --
    log_info "Verifying provider health before instance tests..."
    if ! ensure_provider_health_ready "$PROVIDER_ID" "$ADMIN_TOKEN" "$INSTANCE_HEALTH_SETTLE_SECONDS"; then
        record_fail_result "Provider health (pre-instance)" "POST" \
            "/api/v1/admin/providers/${PROVIDER_ID}/health-check-task" "completed task" "failed" \
            "Provider health task failed before instance lifecycle" "$group"
        chain_break "$group" "Provider health check failed, cannot create instances"
        return 1
    fi

    # -- Admin instance list --
    test_api "Admin instance list" "GET" "/api/v1/admin/instances?page=1&pageSize=10" "200" "" "$group"

    # ==============================
    # Container tests
    # ==============================
    local container_id="" container_name="" container_created=false
    if should_test_type "container" && env_supports_container; then
        log_info "Testing container instances..."

        local img_provider_type="${ENV_TYPE:-docker}"
        case "${img_provider_type}" in
            proxmoxve) img_provider_type="proxmox" ;;
        esac
        local provider_arch; provider_arch=$(current_test_arch "amd64")

        # Resolve container image: prefer a matching active system image for the current
        # provider type. Falls back to "debian:12" (which the server-side
        # populateImageURLFromSystemImage will try to map to a system image).
        local container_image="debian:12"
        local sys_images; sys_images=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/system-images?page=1&pageSize=1000&providerType=${img_provider_type}&instanceType=container&architecture=${provider_arch}&status=active" 2>/dev/null)
        if [[ -n "$sys_images" ]]; then
            local resolved_name; resolved_name=$(echo "$sys_images" | jq -r --arg pt "$img_provider_type" --arg arch "$provider_arch" \
                '.data.list[]? | select(.osType=="debian" and .providerType==$pt and .instanceType=="container" and .status=="active" and (.architecture==$arch or $arch=="")) | .name' 2>/dev/null | head -1)
            if [[ -n "$resolved_name" && "$resolved_name" != "null" ]]; then
                container_image="$resolved_name"
                log_info "Resolved container image from system images: ${container_image}"
            else
                log_info "No active debian ${img_provider_type} container image found; using fallback '${container_image}'"
            fi
        fi

        local container_network_type="${ACTION_TEST_CONTAINER_NETWORK_TYPE:-nat_ipv4}"
        case "$container_network_type" in
            nat_ipv4|nat_ipv4_ipv6|ipv6_only) ;;
            *) chain_break "$group" "Invalid container network type: ${container_network_type}"; return 1 ;;
        esac
        local host_ipv6_before="" host_ipv6_source="" host_ipv6_interface=""
        if [[ "$container_network_type" != "nat_ipv4" ]]; then
            host_ipv6_before=$(platform_ssh_exec "$WORKER_IP" \
                "curl --noproxy '*' -6 -fsS --connect-timeout 6 --max-time 12 https://ipv6.ip.sb" 30 2>/dev/null | tr -d '\r' | tail -1)
            local host_ipv6_source_state host_ipv6_source_probe
            host_ipv6_source_probe=$(_m10_host_ipv6_source_probe_command)
            host_ipv6_source_state=$(platform_ssh_exec "$WORKER_IP" "$host_ipv6_source_probe" 30 2>/dev/null || true)
            IFS='|' read -r host_ipv6_source host_ipv6_interface <<< "$host_ipv6_source_state"
            if ! python3 -c 'import ipaddress,sys; a=ipaddress.IPv6Address(sys.argv[1]); sys.exit(0 if a.is_global else 1)' "$host_ipv6_before" 2>/dev/null ||
               ! python3 -c 'import ipaddress,sys; a=ipaddress.IPv6Address(sys.argv[1]); sys.exit(0 if a.is_global else 1)' "$host_ipv6_source" 2>/dev/null ||
               [[ -z "$host_ipv6_interface" ]]; then
                record_fail_result "Host IPv6 before container creation" "SSH" "$WORKER_IP" \
                    "working public IPv6 egress and route-selected local source" "unavailable" "IPv6 container test requires a working host IPv6 baseline" "$group"
                return 1
            fi
            record_pass_result "Host IPv6 before container creation" "SSH" "$WORKER_IP" \
                "$host_ipv6_before" "$host_ipv6_source on $host_ipv6_interface" "Host public IPv6 egress and route-selected local source are available" "$group"
        fi
        local inst_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"${container_image}\",\"cpu\":${ACTION_TEST_CONTAINER_CPU},\"memory\":${ACTION_TEST_CONTAINER_MEMORY},\"disk\":${ACTION_TEST_CONTAINER_DISK},\"bandwidth\":1000,\"network_type\":\"${container_network_type}\"}"
        local ir
        # Instance creation is deliberately single-shot.  Retrying a POST after
        # a lost response can create a second remote instance.
        if ! ir=$(test_api "Create container instance" "POST" "/api/v1/admin/instances" "200|infra" "$inst_data" "$group"); then
            log_warning "Container instance creation returned non-200; downstream container checks will be skipped"
            ir=""
        elif is_infrastructure_failure_detail "$ir"; then
            # `test_api` records an explicit provider-capacity response as a
            # SKIP and returns its body.  Do not pass that body into the
            # success-ID parser, which would create a second missing-ID FAIL.
            log_warning "Container instance creation skipped due to provider capacity"
            ir=""
        fi
        # Debug: log full creation response
        log_info "Create instance response: $(echo "$ir" | jq -c '.' 2>/dev/null || printf '%s' "$ir")"
        # Handle task-based creation
        local maybe_task; maybe_task=$(echo "$ir" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$maybe_task" ]]; then
            log_info "Instance creation task: ${maybe_task}"
            local task_r=""
            if task_r=$(wait_task_complete_nonfatal "$SERVER_URL" "$maybe_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10); then
                log_info "Task complete response: $(echo "$task_r" | jq -c '.' 2>/dev/null || printf '%s' "$task_r")"
                container_id=$(echo "$task_r" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
                if [[ -z "$container_id" ]]; then
                    record_fail_result "Create container instance task result" "GET" "/api/v1/admin/tasks/${maybe_task}" "instance id" "missing" "$task_r" "$group"
                fi
            else
                log_info "Task failed response: $(echo "$task_r" | jq -c '.' 2>/dev/null || printf '%s' "$task_r")"
                if is_infrastructure_failure_detail "$task_r"; then
                    local infra_detail; infra_detail=$(echo "$task_r" | jq -c '.data.errorMessage // .message // .msg // .' 2>/dev/null || printf '%s' "$task_r")
                    log_warning "Container creation skipped due to transient worker or PVE infrastructure: ${infra_detail}"
                    record_skip_result "Create container instance task (infrastructure)" "GET" "/api/v1/admin/tasks/${maybe_task}" "${infra_detail}" "$group"
                else
                    local task_actual; task_actual=$(safe_jq "$task_r" '.data.status // .message // .msg // "failed"' 'failed')
                    record_fail_result "Create container instance task" "GET" "/api/v1/admin/tasks/${maybe_task}" "completed" "$task_actual" "$task_r" "$group"
                fi
            fi
        else
            container_id=$(echo "$ir" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
            if [[ -n "$ir" && -z "$container_id" ]]; then
                record_fail_result "Create container instance result" "POST" "/api/v1/admin/instances" "instance id" "missing" "$ir" "$group" "$inst_data"
            fi
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
            local detail=""
            if ! detail=$(test_api "Container detail" "GET" "/api/v1/admin/instances/${container_id}" "200" "" "$group"); then
                log_warning "Container ${container_id} disappeared after creation; clearing TEST_INSTANCE_ID to avoid cascading failures"
                TEST_INSTANCE_ID=""
                export TEST_INSTANCE_ID
                container_id=""
            else
                container_created=true
                container_name=$(echo "$detail" | jq -r '.data.name // empty' 2>/dev/null)

                if [[ "$container_network_type" != "nat_ipv4" ]]; then
                    local guest_ipv6 host_ipv6_bound host_ipv6_default host_ipv6_state
                    guest_ipv6=$(echo "$detail" | jq -r '(.data.publicIPv6 | select(. != null and . != "")) // .data.ipv6Address // empty' 2>/dev/null)
                    if [[ "$container_network_type" == "ipv6_only" ]] && ! python3 -c \
                        'import ipaddress,sys; a=ipaddress.ip_interface(sys.argv[1]).ip; sys.exit(0 if a.is_global else 1)' \
                        "$guest_ipv6" 2>/dev/null; then
                        record_fail_result "Container IPv6 assignment" "GET" "/api/v1/admin/instances/${container_id}" \
                            "public IPv6 address" "${guest_ipv6:-missing}" "IPv6-only containers require a public guest address" "$group"
                    elif [[ -n "$guest_ipv6" && "$guest_ipv6" != "null" ]]; then
                        record_pass_result "Container IPv6 assignment" "GET" "/api/v1/admin/instances/${container_id}" \
                            "IPv6 address" "$guest_ipv6" "The container received an IPv6 address" "$group"
                    else
                        record_fail_result "Container IPv6 assignment" "GET" "/api/v1/admin/instances/${container_id}" \
                            "IPv6 address" "missing" "The container has no IPv6 address after creation" "$group"
                    fi
                    # Use machine-readable host state and test the original
                    # address both bound to the interface and via default egress.
                    local host_ipv6_probe
                    host_ipv6_probe=$(_m10_host_ipv6_probe_command "$host_ipv6_source" "$host_ipv6_interface")
                    host_ipv6_state=$(platform_ssh_exec "$WORKER_IP" "$host_ipv6_probe" 90 2>/dev/null || true)
                    local host_addr_present host_route_present
                    IFS='|' read -r host_addr_present host_route_present host_ipv6_bound host_ipv6_default <<< "$host_ipv6_state"
                    if [[ "$host_addr_present" == "1" && "$host_route_present" == "1" &&
                          -n "$host_ipv6_bound" ]] &&
                       python3 -c 'import ipaddress,sys; a=ipaddress.IPv6Address(sys.argv[1]); sys.exit(0 if a.is_global else 1)' "$host_ipv6_bound" 2>/dev/null &&
                       python3 -c 'import ipaddress,sys; a=ipaddress.IPv6Address(sys.argv[1]); sys.exit(0 if a.is_global else 1)' "$host_ipv6_default" 2>/dev/null; then
                        record_pass_result "Host IPv6 after container assignment" "SSH" "$WORKER_IP" \
                            "$host_ipv6_source on $host_ipv6_interface" "$host_ipv6_bound; default=$host_ipv6_default" "Original host source remains bound and default IPv6 egress works" "$group"
                    else
                        record_fail_result "Host IPv6 after container assignment" "SSH" "$WORKER_IP" \
                            "$host_ipv6_source on $host_ipv6_interface" "${host_ipv6_state:-unavailable}" "Container assignment interrupted the original host source, default route, or IPv6 egress" "$group"
                    fi
                fi

                # -- Transparent egress API coverage (non-destructive paths only) --
                test_api "Container egress status" "GET" "/api/v1/admin/instances/${container_id}/egress" "200" "" "$group"
                test_api "Reject unsafe egress fallback" "PUT" "/api/v1/admin/instances/${container_id}/egress" "403" \
                    '{"profile":{"id":"action-test","mode":"native","tunnel_type":"wireguard","tunnel_interface":"wg-action-test","route_table":100,"mark":100,"fail_closed":false},"source":"192.0.2.10","interface":"action-test0","apply":false}' "$group"
                test_api "Reject invalid egress dependency set" "POST" "/api/v1/admin/instances/${container_id}/egress/dependencies" "400" \
                    '{"package_set":"invalid"}' "$group"
                test_api "Reject invalid egress reconcile body" "POST" "/api/v1/admin/instances/${container_id}/egress/reconcile" "400" \
                    '{"apply":"invalid"}' "$group"
                test_api "Reject invalid egress unbind apply flag" "DELETE" "/api/v1/admin/instances/${container_id}/egress?apply=invalid" "400" "" "$group"

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
                    _add_result_json "Container config validation" "GET" "/api/v1/admin/instances/${container_id}" "FAIL" "non-empty" "empty" "$detail" "$group"
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
            local rp; rp=$(test_api "Reset container password" "PUT" "/api/v1/admin/instances/${container_id}/reset-password" "200|infra" \
                "{\"password\":\"${known_test_pw}\"}" "$group")
            export TEST_INSTANCE_PASSWORD="${known_test_pw}"
            local rp_task; rp_task=$(echo "$rp" | jq -r '.data.task_id // .data.taskId // .data.id // empty' 2>/dev/null)
            if [[ -n "$rp_task" ]]; then
                wait_task_complete "$SERVER_URL" "$rp_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || true
                local gp; gp=$(test_api "Get new password" "GET" "/api/v1/admin/instances/${container_id}/password/${rp_task}" "200" "" "$group")
                local gp_pw; gp_pw=$(echo "$gp" | jq -r '.data.password // empty' 2>/dev/null)
                if [[ -n "$gp_pw" && "$gp_pw" != "null" ]]; then
                    export TEST_INSTANCE_PASSWORD="$gp_pw"
                fi
            fi
            wait_instance_active_tasks_idle "$container_id" "container ${container_id} before rebuild" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 || true

            # -- Edit instance --
            local container_provider_name_before_rename="$container_name"
            if test_api "Edit container" "PUT" "/api/v1/admin/instances/${container_id}" "200" \
                '{"name":"ci-container-edited"}' "$group" > /dev/null; then
                container_name="ci-container-edited"
            fi

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
            if [[ "${ACTION_TEST_SKIP_REBUILD:-false}" == "true" ]]; then
                record_skip_result "Rebuild container" "POST" "/api/v1/admin/instances/${container_id}/action" \
                    "Earlier live rebuild attempts exhausted their image-download retry budget; preserving the IPv6 test container" "$group"
            else
            local rb_resp; rb_resp=$(test_api "Rebuild container" "POST" "/api/v1/admin/instances/${container_id}/action" "200|infra" \
                "{\"action\":\"rebuild\",\"image\":\"${container_image}\"}" "$group")
            log_info "Rebuild response: $(echo "$rb_resp" | jq -c '.' 2>/dev/null || printf '%s' "$rb_resp")"
            # Only proceed with rebuild wait if the server returned 200 (success).
            # Infrastructure failures are already recorded as SKIP by test_api;
            # validation/product failures remain recorded as FAIL.
            local rb_code; rb_code=$(echo "$rb_resp" | jq -r '.code // empty' 2>/dev/null)
            if [[ "$rb_code" == "200" ]]; then
                local rb_task; rb_task=$(echo "$rb_resp" | jq -r '.data.task_id // .data.taskId // .data.id // empty' 2>/dev/null)
                local rb_task_resp="" rebuild_task_ok=true
                # Older controllers omit the task ID. Resolve it before waiting:
                # rebuild can transfer the task away from the old instance ID.
                if [[ -z "$rb_task" ]]; then
                    if rb_task_resp=$(get_latest_instance_task_response "$container_id" "rebuild" "$ADMIN_TOKEN"); then
                        rb_task=$(safe_jq "$rb_task_resp" '-r .data.id // .data.ID // empty' '')
                    fi
                fi
                if [[ "$rb_task" =~ ^[0-9]+$ && "$rb_task" != "0" ]]; then
                    log_info "Waiting for rebuild task ${rb_task}..."
                    if ! rb_task_resp=$(wait_task_complete "$SERVER_URL" "$rb_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10); then
                        log_warning "Rebuild task ${rb_task} did not complete within timeout"
                    fi
                    if ! record_task_terminal_result "Rebuild container task" "GET" "/api/v1/admin/tasks/${rb_task}" "$rb_task_resp" "$group"; then
                        rebuild_task_ok=false
                    fi
                else
                    record_fail_result "Rebuild container task lookup" "GET" "/api/v1/admin/tasks?page=1&pageSize=100" "latest rebuild task" "missing" "$rb_resp" "$group"
                    rebuild_task_ok=false
                fi
                if [[ "$rebuild_task_ok" == "true" ]]; then
                    local rebuilt_container_id=""
                    if [[ -n "$container_name" ]]; then
                        rebuilt_container_id=$(resolve_instance_id_by_name "$container_name" "$PROVIDER_ID" "$ADMIN_TOKEN" || true)
                    fi
                    if [[ -n "$rebuilt_container_id" && "$rebuilt_container_id" != "$container_id" ]]; then
                        log_info "Container ${container_id} was replaced by ${rebuilt_container_id} after rebuild"
                        container_id="$rebuilt_container_id"
                        TEST_INSTANCE_ID="$container_id"
                        export TEST_INSTANCE_ID
                    fi
                    if [[ -n "$container_provider_name_before_rename" && "$container_provider_name_before_rename" != "$container_name" ]]; then
                        local orphan_check_resp orphan_check_code orphan_check_body orphan_match
                        orphan_check_resp=$(curl -s --max-time 60 -w "\n%{http_code}" \
                            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}/orphaned" 2>/dev/null || true)
                        orphan_check_code=$(printf '%s\n' "$orphan_check_resp" | tail -1)
                        orphan_check_body=$(printf '%s\n' "$orphan_check_resp" | sed '$d')
                        if [[ "$orphan_check_code" == "200" ]]; then
                            orphan_match=$(printf '%s' "$orphan_check_body" | jq -r --arg n "$container_provider_name_before_rename" \
                                '[.data.orphanedInstances[]? | select((.name // "") == $n or (.uuid // "") == $n)] | length' 2>/dev/null || echo "0")
                            if [[ "$orphan_match" == "0" ]]; then
                                report_add_pass "No stale remote after renamed rebuild" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/orphaned"
                                _add_result_json "No stale remote after renamed rebuild" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/orphaned" "PASS" "" "" "" "$group"
                            else
                                record_fail_result "No stale remote after renamed rebuild" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/orphaned" \
                                    "old remote ${container_provider_name_before_rename} absent" "present" "$orphan_check_body" "$group"
                            fi
                        else
                            record_skip_result "No stale remote after renamed rebuild" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/orphaned" \
                                "orphan endpoint unavailable (HTTP ${orphan_check_code})" "$group"
                        fi
                    fi
                    # Wait for instance to reach running state after rebuild
                    local rebuild_status_wait="${INSTANCE_REBUILD_STATUS_MAX_WAIT:-180}"
                    wait_instance_status "$container_id" "running" "$rebuild_status_wait" 10 "$ADMIN_TOKEN" "container ${container_id} after rebuild" > /dev/null || {
                        log_warning "Container ${container_id} did not report running within ${rebuild_status_wait}s after rebuild"
                    }
                    local rebuilt_detail rebuilt_pw
                    rebuilt_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                        "${SERVER_URL}/api/v1/admin/instances/${container_id}" 2>/dev/null) || true
                    rebuilt_pw=$(echo "$rebuilt_detail" | jq -r '.data.password // empty' 2>/dev/null)
                    if [[ -n "$rebuilt_pw" && "$rebuilt_pw" != "null" ]]; then
                        export TEST_INSTANCE_PASSWORD="$rebuilt_pw"
                    fi
                else
                    log_warning "Skipping post-rebuild running-state wait because rebuild task did not complete"
                fi
            else
                log_info "Rebuild returned code=${rb_code}, skipping post-rebuild status wait"
            fi
            fi
            fi
        fi
    fi

    # ==============================
    # VM tests
    # ==============================
    local vm_id="" vm_created=false
    if should_test_type "vm" && env_supports_vm; then
        log_info "Testing VM instances..."

        local vm_img_provider_type="${ENV_TYPE:-docker}"
        case "${vm_img_provider_type}" in
            proxmoxve) vm_img_provider_type="proxmox" ;;
        esac
        local vm_provider_arch; vm_provider_arch=$(current_test_arch "amd64")

        # Resolve VM image from system images using the same provider-specific
        # contract as populateImageURLFromSystemImage.
        local vm_image="debian:12"
        local vm_sys_images; vm_sys_images=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/system-images?page=1&pageSize=1000&providerType=${vm_img_provider_type}&instanceType=vm&architecture=${vm_provider_arch}&status=active" 2>/dev/null)
        if [[ -n "$vm_sys_images" ]]; then
            local vm_resolved
            if vm_resolved=$(resolve_vm_system_image "$vm_sys_images" "$vm_img_provider_type" "$vm_provider_arch"); then
                vm_image="$vm_resolved"
                log_info "Resolved VM image from system images: ${vm_image}"
            else
                log_info "No active debian ${vm_img_provider_type} VM image found; using fallback '${vm_image}'"
            fi
        fi

        local vm_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"vm\",\"image\":\"${vm_image}\",\"cpu\":${ACTION_TEST_VM_CPU},\"memory\":${ACTION_TEST_VM_MEMORY},\"disk\":${ACTION_TEST_VM_DISK},\"bandwidth\":1000,\"network_type\":\"nat_ipv4\"}"
        local vr
        # Keep VM creation single-shot for the same reason as container creation:
        # a timeout after acceptance must never trigger a duplicate POST.
        if ! vr=$(test_api "Create VM instance" "POST" "/api/v1/admin/instances" "200|infra" "$vm_data" "$group"); then
            log_warning "VM instance creation returned non-200; downstream VM checks will be skipped"
            vr=""
        elif is_infrastructure_failure_detail "$vr"; then
            log_warning "VM instance creation skipped due to provider capacity"
            vr=""
        fi
        local vm_task; vm_task=$(echo "$vr" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$vm_task" ]]; then
            local vm_tr=""
            if vm_tr=$(wait_task_complete_nonfatal "$SERVER_URL" "$vm_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 15); then
                log_info "VM task complete response: $(echo "$vm_tr" | jq -c '.' 2>/dev/null || printf '%s' "$vm_tr")"
                vm_id=$(echo "$vm_tr" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
                if [[ -z "$vm_id" ]]; then
                    record_fail_result "Create VM instance task result" "GET" "/api/v1/admin/tasks/${vm_task}" "instance id" "missing" "$vm_tr" "$group"
                fi
            else
                log_info "VM task failed response: $(echo "$vm_tr" | jq -c '.' 2>/dev/null || printf '%s' "$vm_tr")"
                local vm_infra_detail; vm_infra_detail=$(echo "$vm_tr" | jq -c '.data.errorMessage // .message // .msg // .' 2>/dev/null || printf '%s' "$vm_tr")
                if is_infrastructure_failure_detail "$vm_tr"; then
                    if is_vm_runtime_infrastructure_failure_detail "$vm_tr"; then
                        mark_vm_runtime_infrastructure_unavailable "$vm_infra_detail"
                    fi
                    log_warning "VM creation skipped due to worker/runtime infrastructure: ${vm_infra_detail}"
                    record_skip_result "Create VM instance task (infrastructure)" "GET" "/api/v1/admin/tasks/${vm_task}" "${vm_infra_detail}" "$group"
                else
                    local vm_task_actual; vm_task_actual=$(safe_jq "$vm_tr" '.data.status // .message // .msg // "failed"' 'failed')
                    record_fail_result "Create VM instance task" "GET" "/api/v1/admin/tasks/${vm_task}" "completed" "$vm_task_actual" "$vm_tr" "$group"
                fi
            fi
        else
            vm_id=$(echo "$vr" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
            if [[ -n "$vr" && -z "$vm_id" ]]; then
                record_fail_result "Create VM instance result" "POST" "/api/v1/admin/instances" "instance id" "missing" "$vr" "$group" "$vm_data"
            fi
        fi

        if [[ -n "$vm_id" ]]; then
            log_info "VM instance ID: ${vm_id}"
            vm_created=true

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
            # Only test stop/start/restart if VM is in a runnable state
            if [[ "$vm_ssh_ready" == "true" ]]; then
                local vm_stop_resp; vm_stop_resp=$(test_api "Stop VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"stop"}' "$group") || vm_stop_resp=""
                [[ -n "$vm_stop_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_stop_resp" "stopped" "stop VM ${vm_id}" "$ADMIN_TOKEN" || true
                local vm_start_resp; vm_start_resp=$(test_api "Start VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"start"}' "$group") || vm_start_resp=""
                [[ -n "$vm_start_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_start_resp" "running" "start VM ${vm_id}" "$ADMIN_TOKEN" || true
                local vm_restart_resp; vm_restart_resp=$(test_api "Restart VM" "POST" "/api/v1/admin/instances/${vm_id}/action" "200" '{"action":"restart"}' "$group") || vm_restart_resp=""
                [[ -n "$vm_restart_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_restart_resp" "running" "restart VM ${vm_id}" "$ADMIN_TOKEN" || true
            else
                log_warning "Skipping VM stop/start/restart tests: VM not in 'running' state"
                SKIPPED_TESTS=$((SKIPPED_TESTS + 3))
                for op in "Stop VM" "Start VM" "Restart VM"; do
                    report_add_skip "$op" "POST" "/api/v1/admin/instances/${vm_id}/action" "VM not in running state"
                    _add_result_json "$op" "POST" "/api/v1/admin/instances/${vm_id}/action" "SKIP" "" "VM not running" "" "$group"
                done
            fi
            test_api "VM port mappings" "GET" "/api/v1/admin/instances/${vm_id}/port-mappings" "200" "" "$group"
            test_api "VM resources" "GET" "/api/v1/admin/instances/${vm_id}/monitoring/resources" "200" "" "$group"

            # -- Delete VM --
            if [[ "$preserve_test_instances" == "true" ]]; then
                record_skip_result "Preserve VM" "DELETE" "/api/v1/admin/instances/${vm_id}" \
                    "ACTION_TEST_PRESERVE_INSTANCES=true; leaving the created VM for inspection" "$group"
            else
                local vm_delete_resp; vm_delete_resp=$(test_api "Delete VM" "DELETE" "/api/v1/admin/instances/${vm_id}" "200" "" "$group") || vm_delete_resp=""
                [[ -n "$vm_delete_resp" ]] && wait_instance_operation_settled "$vm_id" "$vm_delete_resp" "deleted" "delete VM ${vm_id}" "$ADMIN_TOKEN" || true
            fi
        fi
    fi

    # If the environment was supposed to create instances but both types failed (not skipped),
    # chain-break so downstream modules (17, 18, 19, 22, 24, 28) skip gracefully instead of
    # producing cascading failures.
    local wanted_container=false; should_test_type "container" && env_supports_container && wanted_container=true
    local wanted_vm=false; should_test_type "vm" && env_supports_vm && wanted_vm=true
    if [[ "$wanted_container" == "true" && "$container_created" != "true" ]] && \
       [[ "$wanted_vm" == "true" && "$vm_created" != "true" ]]; then
        chain_break "$group" "Provider instance creation unavailable (both container and VM creation failed)"
    elif [[ "$wanted_container" == "true" && "$container_created" != "true" ]] && [[ "$wanted_vm" != "true" ]]; then
        chain_break "$group" "Provider container creation unavailable"
    elif [[ "$wanted_vm" == "true" && "$vm_created" != "true" ]] && [[ "$wanted_container" != "true" ]]; then
        chain_break "$group" "Provider VM creation unavailable"
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
        if [[ "$preserve_test_instances" == "true" ]]; then
            export TEST_INSTANCE_ID="$container_id"
            record_skip_result "Preserve container" "DELETE" "/api/v1/admin/instances/${container_id}" \
                "ACTION_TEST_PRESERVE_INSTANCES=true; leaving the created container for inspection" "$group"
        else
            test_api "Delete container" "DELETE" "/api/v1/admin/instances/${container_id}" "200" "" "$group"
        fi
    fi

    # -- Delete nonexistent instance --
    test_api "Delete nonexistent instance" "DELETE" "/api/v1/admin/instances/99999" "404|400" "" "$group"

    # ==============================
    # User API instance creation (matching frontend flow)
    # ==============================
    # The frontend uses /api/v1/user/instances with resource IDs (imageId, cpuId, etc.),
    # not the admin API with direct values. Test both paths.
    if [[ -n "$USER_TOKEN" && -n "$PROVIDER_ID" ]]; then
        log_info "Testing user API instance creation (frontend-equivalent flow)..."

        # Get available system images to find a valid imageId.
        # Prefer alpine (smallest, fastest download) for instance creation tests.
        # Filter by provider type matching the current ENV_TYPE.
        local user_img_provider_type="${ENV_TYPE:-docker}"
        case "${user_img_provider_type}" in
            proxmoxve) user_img_provider_type="proxmox" ;;
        esac
        local user_provider_arch; user_provider_arch=$(current_test_arch "amd64")
        local user_image_instance_type="container"
        if ! env_supports_container && env_supports_vm; then
            user_image_instance_type="vm"
        fi
        local filtered_user_images; filtered_user_images=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${USER_TOKEN}" \
            "${SERVER_URL}/api/v1/user/images/filtered?provider_id=${PROVIDER_ID}&instance_type=${user_image_instance_type}" 2>/dev/null)
        local user_image_filter
        # shellcheck disable=SC2016 # jq expands $arch; Bash must preserve it verbatim.
        user_image_filter='[.data[]? | select((.isActive == true or .status == "active") and (.architecture == $arch or $arch == ""))] | (map(select((.osType // "") == "alpine")) + map(select((.osType // "") == "debian")) + .) | .[0].id // empty'
        local user_image_id
        user_image_id=$(printf '%s' "$filtered_user_images" | jq -r --arg arch "$user_provider_arch" "$user_image_filter" 2>/dev/null || true)
        local user_inst_resp="" user_inst_task=""
        if [[ -n "$user_image_id" && "$user_image_id" != "null" ]]; then
            log_info "Using filtered user image ID=${user_image_id} (${user_img_provider_type}/${user_image_instance_type}/${user_provider_arch}) for user instance creation test"
            user_inst_resp=$(test_api "User creates instance (frontend-equivalent)" "POST" \
                "/api/v1/user/instances" "200|400|404|409" \
                "{\"providerId\":${PROVIDER_ID},\"imageId\":${user_image_id},\"cpuId\":\"1\",\"memoryId\":\"1\",\"diskId\":\"1\",\"bandwidthId\":\"1\"}" \
                "$group" "$USER_TOKEN")
            user_inst_task=$(echo "$user_inst_resp" | jq -r '.data.taskId // .data.task_id // empty' 2>/dev/null)
        else
            record_skip_result "User creates instance (frontend-equivalent)" "POST" \
                "/api/v1/user/instances" "no active image available from user filtered-image endpoint" "$group"
            log_warning "No active user image is available for ${user_img_provider_type}/${user_image_instance_type}/${user_provider_arch}; skipping creation request"
        fi
        local user_inst_id=""

        # Wait for task if async
        if [[ -n "$user_inst_task" ]]; then
            log_info "Waiting for user instance creation task ${user_inst_task}..."
            wait_task_complete "$SERVER_URL" "$user_inst_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || true
            # Try to get instance ID from completed task
            local task_detail; task_detail=$(curl -s --max-time 10 -H "Authorization: Bearer ${USER_TOKEN}" \
                "${SERVER_URL}/api/v1/user/tasks?page=1&pageSize=1" 2>/dev/null)
            user_inst_id=$(echo "$task_detail" | jq -r '.data.list[0].instanceId // empty' 2>/dev/null)
        else
            user_inst_id=$(echo "$user_inst_resp" | jq -r '.data.id // .data.instanceId // empty' 2>/dev/null)
        fi

        # Verify the instance appears in user's instance list
        if [[ -n "$user_inst_id" ]]; then
            log_info "User instance ID from user API creation: ${user_inst_id}"
            test_api "User sees created instance" "GET" "/api/v1/user/instances?page=1&pageSize=10" "200" \
                "" "$group" "$USER_TOKEN"
            test_api "User instance detail" "GET" "/api/v1/user/instances/${user_inst_id}" "200|403" \
                "" "$group" "$USER_TOKEN"
            # Cleanup — admin deletes the instance
            if [[ "$preserve_test_instances" == "true" ]]; then
                record_skip_result "Preserve user-created instance" "DELETE" "/api/v1/admin/instances/${user_inst_id}" \
                    "ACTION_TEST_PRESERVE_INSTANCES=true; leaving the created instance for inspection" "$group"
            else
                local u_del_resp; u_del_resp=$(test_api "Admin delete user-created instance" "DELETE" \
                    "/api/v1/admin/instances/${user_inst_id}" "200" "" "$group" "$ADMIN_TOKEN") || u_del_resp=""
                [[ -n "$u_del_resp" ]] && wait_instance_operation_settled "$user_inst_id" "$u_del_resp" "deleted" "delete user-created instance ${user_inst_id}" "$ADMIN_TOKEN" || true
            fi
        else
            log_warning "User API instance creation did not yield an instance ID (may be 400 if preconditions not met)"
        fi

        # Negative: user creates without required resource IDs
        test_api "User create instance (missing imageId)" "POST" "/api/v1/user/instances" "400" \
            "{\"providerId\":${PROVIDER_ID},\"cpuId\":\"1\",\"memoryId\":\"1\"}" "$group" "$USER_TOKEN"
        test_api "User create instance (missing providerId)" "POST" "/api/v1/user/instances" "400" \
            "{\"imageId\":1,\"cpuId\":\"1\",\"memoryId\":\"1\"}" "$group" "$USER_TOKEN"
    fi

    # ==============================
    # Instance Share Link Tests
    # ==============================
    # The VM lifecycle above deletes its test VM before reaching this section.
    # Do not reuse that stale ID (it would turn the share precondition into a
    # false 404). Prefer the retained container, and only use the VM when the
    # admin detail endpoint confirms it is still present.
    local share_instance_id=""
    if [[ -n "$container_id" ]] && ensure_test_instance_available "$ADMIN_TOKEN" "$container_id" "container share test"; then
        share_instance_id="$container_id"
    elif [[ -n "$vm_id" ]] && ensure_test_instance_available "$ADMIN_TOKEN" "$vm_id" "VM share test"; then
        share_instance_id="$vm_id"
    fi
    if [[ -n "$share_instance_id" ]]; then
        log_info "Testing instance share links with instance ID=${share_instance_id}..."
        local share_group="instances_share"

        # Ensure instance is unfrozen and running for share tests
        test_api "Unfreeze for share test" "POST" "/api/v1/admin/instances/unfreeze" "200" \
            "{\"instanceId\":${share_instance_id}}" "$share_group"

        # -- User creates share link --
        if [[ -n "$USER_TOKEN" ]]; then
            local share_resp; share_resp=$(test_api "User create instance share link" "POST" \
                "/api/v1/user/instances/${share_instance_id}/share-links" "200|403|404" \
                '{"expiresInMinutes":30}' "$share_group" "$USER_TOKEN")
            local share_token; share_token=$(echo "$share_resp" | jq -r '.data.token // empty' 2>/dev/null)
            local share_url; share_url=$(echo "$share_resp" | jq -r '.data.url // empty' 2>/dev/null)
            local share_expires; share_expires=$(echo "$share_resp" | jq -r '.data.expiresAt // empty' 2>/dev/null)

            if [[ -n "$share_token" ]]; then
                log_info "Share token created: prefix=${share_token:0:8}..."

                TOTAL_TESTS=$((TOTAL_TESTS + 1))
                if [[ -n "$share_url" ]]; then
                    PASSED_TESTS=$((PASSED_TESTS + 1))
                    log_success "Share link URL returned: ${share_url}"
                    _add_result_json "Share link URL check" "POST" "/api/v1/user/instances/${share_instance_id}/share-links" "PASS" "non-empty" "present" "" "$share_group"
                else
                    FAILED_TESTS=$((FAILED_TESTS + 1))
                    log_error "Share link URL missing in response"
                    _add_result_json "Share link URL check" "POST" "/api/v1/user/instances/${share_instance_id}/share-links" "FAIL" "non-empty" "missing" "$share_resp" "$share_group"
                fi

                TOTAL_TESTS=$((TOTAL_TESTS + 1))
                if [[ -n "$share_expires" ]]; then
                    PASSED_TESTS=$((PASSED_TESTS + 1))
                    log_success "Share link expiresAt returned: ${share_expires}"
                    _add_result_json "Share link expiry check" "POST" "/api/v1/user/instances/${share_instance_id}/share-links" "PASS" "non-empty" "present" "" "$share_group"
                else
                    FAILED_TESTS=$((FAILED_TESTS + 1))
                    log_error "Share link expiresAt missing"
                    _add_result_json "Share link expiry check" "POST" "/api/v1/user/instances/${share_instance_id}/share-links" "FAIL" "non-empty" "missing" "$share_resp" "$share_group"
                fi

                # -- Access shared instance detail (public, no auth) --
                local shared_detail; shared_detail=$(test_api_noauth "Shared instance detail (public)" "GET" \
                    "/api/v1/public/instance-shares/${share_token}" "200" "" "$share_group")

                # Verify essential fields in shared detail
                local shared_id; shared_id=$(echo "$shared_detail" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
                TOTAL_TESTS=$((TOTAL_TESTS + 1))
                if [[ -n "$shared_id" ]]; then
                    PASSED_TESTS=$((PASSED_TESTS + 1))
                    log_success "Shared detail contains instance ID: ${shared_id}"
                    _add_result_json "Shared detail ID check" "GET" "/api/v1/public/instance-shares/${share_token}" "PASS" "non-empty" "$shared_id" "" "$share_group"
                else
                    FAILED_TESTS=$((FAILED_TESTS + 1))
                    log_error "Shared detail missing instance ID"
                    _add_result_json "Shared detail ID check" "GET" "/api/v1/public/instance-shares/${share_token}" "FAIL" "non-empty" "missing" "$shared_detail" "$share_group"
                fi

                # Verify isFrozen and frozenReason fields in shared detail
                local shared_frozen; shared_frozen=$(echo "$shared_detail" | jq -r '.data.isFrozen' 2>/dev/null)
                TOTAL_TESTS=$((TOTAL_TESTS + 1))
                if [[ "$shared_frozen" != "null" ]]; then
                    PASSED_TESTS=$((PASSED_TESTS + 1))
                    log_success "Shared detail contains isFrozen field: ${shared_frozen}"
                    _add_result_json "Shared detail isFrozen field" "GET" "/api/v1/public/instance-shares/${share_token}" "PASS" "present" "$shared_frozen" "" "$share_group"
                else
                    FAILED_TESTS=$((FAILED_TESTS + 1))
                    log_error "Shared detail missing isFrozen field"
                    _add_result_json "Shared detail isFrozen field" "GET" "/api/v1/public/instance-shares/${share_token}" "FAIL" "present" "missing" "$shared_detail" "$share_group"
                fi

                # Verify trafficQuotaVisible field in shared detail
                local shared_tqv; shared_tqv=$(echo "$shared_detail" | jq -r '.data.trafficQuotaVisible' 2>/dev/null)
                TOTAL_TESTS=$((TOTAL_TESTS + 1))
                if [[ "$shared_tqv" != "null" && -n "$shared_tqv" ]]; then
                    PASSED_TESTS=$((PASSED_TESTS + 1))
                    log_success "Shared detail contains trafficQuotaVisible field: ${shared_tqv}"
                    _add_result_json "Shared detail trafficQuotaVisible field" "GET" "/api/v1/public/instance-shares/${share_token}" "PASS" "present" "$shared_tqv" "" "$share_group"
                else
                    FAILED_TESTS=$((FAILED_TESTS + 1))
                    log_error "Shared detail missing trafficQuotaVisible field"
                    _add_result_json "Shared detail trafficQuotaVisible field" "GET" "/api/v1/public/instance-shares/${share_token}" "FAIL" "present" "missing" "$shared_detail" "$share_group"
                fi

                # -- Shared instance ports (public) --
                test_api_noauth "Shared instance ports (public)" "GET" \
                    "/api/v1/public/instance-shares/${share_token}/ports" "200|403|404" "" "$share_group"

                # -- Shared instance monitoring (public) --
                test_api_noauth "Shared instance monitoring (public)" "GET" \
                    "/api/v1/public/instance-shares/${share_token}/monitoring" "200|403|404" "" "$share_group"

                # -- Shared instance resource monitoring (public) --
                test_api_noauth "Shared instance resources (public)" "GET" \
                    "/api/v1/public/instance-shares/${share_token}/monitoring/resources" "200|403|404" "" "$share_group"

                # -- Shared instance traffic detail (public) --
                test_api_noauth "Shared instance traffic detail (public)" "GET" \
                    "/api/v1/public/instance-shares/${share_token}/traffic/detail" "200|403|404" "" "$share_group"

                # -- Shared instance filtered images (public) --
                test_api_noauth "Shared instance images (public)" "GET" \
                    "/api/v1/public/instance-shares/${share_token}/images/filtered" "200|403|404" "" "$share_group"

                # -- Shared instance action (public) --
                test_api_noauth "Shared instance action (invalid action)" "POST" \
                    "/api/v1/public/instance-shares/${share_token}/action" "400" \
                    '{"action":"invalid_action"}' "$share_group"

                # -- Shared instance password reset (public) --
                local shrp; shrp=$(test_api_noauth "Shared instance reset password" "PUT" \
                    "/api/v1/public/instance-shares/${share_token}/reset-password" "200|400|403" \
                    '{}' "$share_group")
                local shrp_task; shrp_task=$(echo "$shrp" | jq -r '.data.taskId // .data.task_id // empty' 2>/dev/null)
                if [[ -n "$shrp_task" ]]; then
                    wait_task_complete "$SERVER_URL" "$shrp_task" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10 > /dev/null 2>&1 || true
                    test_api_noauth "Shared instance get new password" "GET" \
                        "/api/v1/public/instance-shares/${share_token}/password/${shrp_task}" "200|403|404" "" "$share_group"
                fi
            else
                log_warning "User share link creation did not return a token (may be 403 if user doesn't own instance)"
            fi
        fi

        # -- Admin creates share link --
        local admin_share_resp; admin_share_resp=$(test_api "Admin create instance share link" "POST" \
            "/api/v1/admin/instances/${share_instance_id}/share-links" "200|403|404" \
            '{"expiresInMinutes":60}' "$share_group")
        local admin_share_token; admin_share_token=$(echo "$admin_share_resp" | jq -r '.data.token // empty' 2>/dev/null)

        if [[ -n "$admin_share_token" ]]; then
            log_info "Admin share token created: prefix=${admin_share_token:0:8}..."

            # -- Access admin-created share link (public) --
            test_api_noauth "Admin share detail (public)" "GET" \
                "/api/v1/public/instance-shares/${admin_share_token}" "200" "" "$share_group"

            # Verify fields in admin share detail
            local admin_shared_detail; admin_shared_detail=$(curl -s --max-time 30 \
                "${SERVER_URL}/api/v1/public/instance-shares/${admin_share_token}" 2>/dev/null)
            
            # Check isFrozen field
            local adm_is_frozen; adm_is_frozen=$(echo "$admin_shared_detail" | jq -r 'if .data | has("isFrozen") then .data.isFrozen else "__missing__" end' 2>/dev/null)
            if [[ "$adm_is_frozen" != "__missing__" ]]; then
                log_success "Admin share detail isFrozen field present: ${adm_is_frozen}"
            else
                log_warning "Admin share detail missing isFrozen field"
            fi

            # Check frozenReason field
            local adm_frozen_reason; adm_frozen_reason=$(echo "$admin_shared_detail" | jq -r '.data.frozenReason // "__empty__"' 2>/dev/null)
            log_info "Admin share detail frozenReason: ${adm_frozen_reason}"

            # Check expiresAt field
            local adm_expires; adm_expires=$(echo "$admin_shared_detail" | jq -r '.data.expiresAt // "__missing__"' 2>/dev/null)
            if [[ "$adm_expires" != "__missing__" ]]; then
                log_success "Admin share detail expiresAt field present: ${adm_expires}"
            else
                log_warning "Admin share detail missing expiresAt field"
            fi
        fi

        # -- Negative: share link with invalid token --
        test_api_noauth "Shared instance (invalid token)" "GET" \
            "/api/v1/public/instance-shares/invalid_token_xxxxx" "401|404" "" "$share_group"

        # -- Negative: share link with empty token --
        test_api_noauth "Shared instance (empty token)" "GET" \
            "/api/v1/public/instance-shares/" "404" "" "$share_group"

        # -- Negative: action on shared instance without freeze check --
        if [[ -n "$admin_share_token" ]]; then
            test_api_noauth "Shared instance action (empty body)" "POST" \
                "/api/v1/public/instance-shares/${admin_share_token}/action" "400" \
                '{}' "$share_group"
        fi
    else
        log_info "Skipping share link tests: no instance available"
    fi
}
