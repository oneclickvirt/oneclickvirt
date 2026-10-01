#!/bin/bash
# Module 13: Port Mapping Management
# Dependencies: 09_providers (PROVIDER_ID)

wait_for_port_mapping_delete() {
    local label="$1" port_id="$2" task_id="$3" group="${4:-port_mappings}"
    local task_resp=""
    if [[ ! "$task_id" =~ ^[1-9][0-9]*$ ]]; then
        record_fail_result "${label} task id" "DELETE" "/api/v1/admin/port-mappings/${port_id}" \
            "task id" "missing" "Delete response did not include a task id" "$group"
        return 1
    fi

    if task_resp=$(wait_task_complete "$SERVER_URL" "$task_id" "$ADMIN_TOKEN" \
        "${PORT_MAPPING_TASK_MAX_WAIT:-600}" 5); then
        # wait_task_complete establishes the terminal state; retain a normal
        # API assertion for the task endpoint and then verify the mapping is
        # actually gone from the database.
        test_api "${label} task completed" "GET" "/api/v1/admin/tasks/${task_id}" \
            "200" "" "$group" >/dev/null
        test_api "${label} absent after task" "GET" \
            "/api/v1/admin/port-mappings/${port_id}" "400|404" "" "$group" >/dev/null
        return 0
    fi

    record_task_terminal_result "${label} task" "GET" "/api/v1/admin/tasks/${task_id}" \
        "$task_resp" "$group" || true
    return 1
}

run_module_13() {
    report_add_section "13 - Port Mappings"
    local group="port_mappings"
    local instance_group="port_mappings_instance"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider"
        return 1
    fi

    # -- Admin port mapping list --
    test_api "Port mapping list" "GET" "/api/v1/admin/port-mappings?page=1&pageSize=10" "200" "" "$group"

    # -- Check port availability --
    test_api "Check port (available)" "POST" "/api/v1/admin/ports/check" "200" \
        "{\"providerId\":${PROVIDER_ID},\"hostPort\":25000,\"portCount\":1,\"protocol\":\"tcp\"}" "$group"
    test_api "Check controller TCP port" "POST" "/api/v1/admin/ports/check" "200" \
        "{\"providerId\":${PROVIDER_ID},\"hostPort\":25222,\"portCount\":1,\"protocol\":\"tcp\",\"mappingType\":\"controller\"}" "$group"

    # -- Verify mappingType field in list response --
    local pm_list; pm_list=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/port-mappings?page=1&pageSize=20" 2>/dev/null)
    local has_mapping_type; has_mapping_type=$(echo "$pm_list" | jq -r '.data.list[0].mappingType // empty' 2>/dev/null)
    if [[ -n "$has_mapping_type" ]]; then
        log_success "mappingType field present in port mapping list: ${has_mapping_type}"
    else
        log_warning "mappingType field missing from port mapping list (may be empty list)"
    fi

    local inst_for_pm="" pm_id="" ctrl_pm_id=""
    local manual_mapping_type="node" node_mapping_expected="200"
    case "$ENV_TYPE" in
        docker|podman|containerd|orbstack)
            manual_mapping_type="controller"
            node_mapping_expected="400"
            ;;
    esac
    if require_test_instance "$instance_group" "Port mapping instance-dependent tests" "$ADMIN_TOKEN"; then
        inst_for_pm="$TEST_INSTANCE_ID"

        # Controller mappings need a reverse WebSocket. The SSH monitoring
        # daemon deployed by module 11 does not provide that connection, even
        # when systemd reports it active. Probe the live hub before positives.
        local controller_agent_ready=false controller_agent_reason=""
        if ensure_action_test_agent_ready "$PROVIDER_ID" "$instance_group" "$ADMIN_TOKEN"; then
            controller_agent_ready=true
        else
            controller_agent_reason="${ACTION_TEST_AGENT_READY_REASON:-Agent is offline or unavailable}"
            log_skip "Controller port mapping checks: ${controller_agent_reason}"
        fi

        # -- Create port mapping --
        test_api "Check port for manual mapping" "POST" "/api/v1/admin/ports/check" "200" \
            "{\"providerId\":${PROVIDER_ID},\"hostPort\":25001,\"portCount\":1,\"protocol\":\"tcp\",\"mappingType\":\"${manual_mapping_type}\"}" "$instance_group" >/dev/null
        local pm="" pm_request_ok=false pm_created=false pm_code=""
        if [[ "$manual_mapping_type" == "controller" && "$controller_agent_ready" != "true" ]]; then
            record_skip_result "Create port mapping" "POST" "/api/v1/admin/port-mappings" \
                "${controller_agent_reason}" "$instance_group"
        else
            if pm=$(test_api "Create port mapping" "POST" "/api/v1/admin/port-mappings" "200|infra" \
                "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"hostPort\":25001,\"mappingType\":\"${manual_mapping_type}\"}" "$instance_group"); then
                pm_request_ok=true
            fi
            pm_code=$(echo "$pm" | jq -r '.code // empty' 2>/dev/null)
            pm_id=$(echo "$pm" | jq -r '.data.portId // .data.id // .data.ID // empty' 2>/dev/null)
            if [[ "$pm_request_ok" == "true" && -n "$pm_id" ]]; then
                pm_created=true
            elif [[ "$pm_request_ok" == "true" && "$pm_code" == "200" ]]; then
                record_fail_result "Create port mapping result" "POST" \
                    "/api/v1/admin/port-mappings" "mapping id" "missing" "$pm" "$instance_group"
            fi
        fi

        # -- Create port mapping with mappingType=node (explicit) --
        # Do not use a fixed port here. A preserved instance from an earlier
        # run may legitimately own 25080, which would turn a setup collision
        # into a false negative for the mapping lifecycle itself. Probe a small
        # bounded range through the same availability API and reserve the
        # first free candidate for this run.
        local node_host_port=25080 node_port_available=false node_probe=""
        local node_port_attempt
        for ((node_port_attempt=0; node_port_attempt<24; node_port_attempt++)); do
            local node_candidate=$((node_host_port + node_port_attempt))
            node_probe=$(curl -s --max-time 15 \
                -H "Authorization: Bearer ${ADMIN_TOKEN}" -H "Content-Type: application/json" \
                -X POST -d "{\"providerId\":${PROVIDER_ID},\"hostPort\":${node_candidate},\"portCount\":1,\"protocol\":\"tcp\",\"mappingType\":\"node\"}" \
                "${SERVER_URL}/api/v1/admin/ports/check" 2>/dev/null) || node_probe=""
            if [[ "$(echo "$node_probe" | jq -r '.code // empty' 2>/dev/null)" == "200" && \
                  "$(echo "$node_probe" | jq -r '.data.available // false' 2>/dev/null)" == "true" ]]; then
                node_host_port=$node_candidate
                node_port_available=true
                break
            fi
        done
        if [[ "$node_port_available" != "true" ]]; then
            record_fail_result "Check port for explicit node mapping" "POST" "/api/v1/admin/ports/check" \
                "available port" "none" "No free node mapping port found in bounded probe range" "$instance_group"
        else
            test_api "Check port for explicit node mapping" "POST" "/api/v1/admin/ports/check" "200" \
                "{\"providerId\":${PROVIDER_ID},\"hostPort\":${node_host_port},\"portCount\":1,\"protocol\":\"tcp\"}" "$instance_group" >/dev/null
        fi
        local node_pm="" node_pm_request_ok=true node_pm_id=""
        if [[ "$node_port_available" == "true" ]] && node_pm=$(test_api "Create port mapping (node type)" "POST" "/api/v1/admin/port-mappings" "$node_mapping_expected" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":8080,\"protocol\":\"tcp\",\"hostPort\":${node_host_port},\"mappingType\":\"node\"}" "$instance_group"); then
            node_pm_request_ok=true
        else
            node_pm_request_ok=false
        fi
        node_pm_id=$(echo "$node_pm" | jq -r '.data.portId // .data.id // .data.ID // empty' 2>/dev/null)
        if [[ "$node_mapping_expected" == "200" && "$node_pm_request_ok" == "true" && -z "$node_pm_id" ]]; then
            record_fail_result "Create port mapping (node type) result" "POST" \
                "/api/v1/admin/port-mappings" "mapping id" "missing" "$node_pm" "$instance_group"
        fi

        # -- Create port mapping with mappingType=controller --
        local ctrl_pm=""
        if [[ "$controller_agent_ready" == "true" ]]; then
            ctrl_pm=$(test_api "Create port mapping (controller type)" "POST" "/api/v1/admin/port-mappings" "200|infra" \
                "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"mappingType\":\"controller\",\"internalHost\":\"10.0.0.1\"}" "$instance_group") || true
            ctrl_pm_id=$(echo "$ctrl_pm" | jq -r '.data.portId // .data.id // .data.ID // empty' 2>/dev/null)
        else
            record_skip_result "Create port mapping (controller type)" "POST" "/api/v1/admin/port-mappings" \
                "${controller_agent_reason}" "$instance_group"
        fi

        test_api "Controller mapping rejects UDP" "POST" "/api/v1/admin/port-mappings" "400" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":53,\"protocol\":\"udp\",\"mappingType\":\"controller\",\"internalHost\":\"10.0.0.1\"}" "$instance_group"
        test_api "Controller mapping rejects port range" "POST" "/api/v1/admin/port-mappings" "400" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":8000,\"portCount\":2,\"protocol\":\"tcp\",\"mappingType\":\"controller\",\"internalHost\":\"10.0.0.1\"}" "$instance_group"

        # -- no_port_mapping networkType: node-side mapping must be rejected --
        local expected_controller_host
        expected_controller_host=$(echo "$SERVER_URL" | sed -E 's#^[a-zA-Z]+://##; s#/.*$##; s#:[0-9]+$##')

        # Temporarily set networkType=no_port_mapping
        curl -s --max-time 30 -X PUT \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" -H "Content-Type: application/json" \
            -d '{"networkType":"no_port_mapping"}' \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}/port-config" >/dev/null 2>&1 || true

        test_api "no_port_mapping blocks node mapping" "POST" "/api/v1/admin/port-mappings" "400" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"hostPort\":25100,\"mappingType\":\"node\"}" "$instance_group"

        # controller mode should still be accepted when the Agent is online.
        if [[ "$controller_agent_ready" == "true" ]]; then
            test_api "no_port_mapping allows controller mapping" "POST" "/api/v1/admin/port-mappings" "200|infra" \
                "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"mappingType\":\"controller\",\"internalHost\":\"10.0.0.1\"}" "$instance_group"
        else
            record_skip_result "no_port_mapping allows controller mapping" "POST" "/api/v1/admin/port-mappings" \
                "${controller_agent_reason}" "$instance_group"
        fi

        # user-side display should expose controller host in tunnel mode (when user can access the instance)
        if [[ -n "$USER_TOKEN" ]]; then
            local user_ports_resp
            user_ports_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${USER_TOKEN}" \
                "${SERVER_URL}/api/v1/user/instances/${inst_for_pm}/ports" 2>/dev/null)
            local user_ports_code
            user_ports_code=$(echo "$user_ports_resp" | jq -r '.code // empty' 2>/dev/null)
            if [[ "$user_ports_code" == "200" ]]; then
                local user_public_ip
                user_public_ip=$(echo "$user_ports_resp" | jq -r '.data.publicIP // empty' 2>/dev/null)
                local has_controller
                has_controller=$(echo "$user_ports_resp" | jq -r '[.data.list[]? | select(.mappingType == "controller")] | length' 2>/dev/null)
                if [[ "$has_controller" =~ ^[0-9]+$ && "$has_controller" -gt 0 ]]; then
                    if [[ -n "$user_public_ip" ]]; then
                        if [[ -n "$expected_controller_host" && "$user_public_ip" == "$expected_controller_host" ]]; then
                            log_success "Tunnel mode user publicIP matches controller host: ${user_public_ip}"
                        elif [[ -n "$expected_controller_host" ]]; then
                            log_warning "Tunnel mode user publicIP mismatch: got '${user_public_ip}', expected '${expected_controller_host}'"
                        else
                            log_success "Tunnel mode user publicIP is present: ${user_public_ip}"
                        fi
                    else
                        log_warning "Tunnel mode user publicIP is empty but controller mapping exists"
                    fi
                else
                    log_info "No controller mapping in user ports response; skip tunnel publicIP assertion"
                fi
            else
                log_info "User instance ports not accessible in this env (code=${user_ports_code:-unknown}); skip tunnel publicIP assertion"
            fi
        fi

        # Restore networkType=nat_ipv4
        curl -s --max-time 30 -X PUT \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" -H "Content-Type: application/json" \
            -d '{"portRangeStart":20000,"portRangeEnd":30000,"defaultPortCount":10,"networkType":"nat_ipv4"}' \
            "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}/port-config" >/dev/null 2>&1 || true

        # -- Create duplicate port --
        # A duplicate assertion is meaningful only after the first mapping was
        # created.  Do not turn an Agent/worker failure into a second 500.
        if [[ "$pm_created" == "true" ]]; then
            # The reverse Agent can disconnect between the first creation and
            # this intentional duplicate request.  Classify that known
            # prerequisite loss as SKIP; validation/other server errors still
            # remain failures because `infra` is matched narrowly by test_api.
            local duplicate_expected="400|409"
            [[ "$manual_mapping_type" == "controller" ]] && duplicate_expected="400|409|infra"
            test_api "Create duplicate port" "POST" "/api/v1/admin/port-mappings" "$duplicate_expected" \
                "{\"instanceId\":${inst_for_pm},\"guestPort\":22,\"protocol\":\"tcp\",\"hostPort\":25001,\"mappingType\":\"${manual_mapping_type}\"}" "$instance_group"
        else
            local duplicate_reason="initial port mapping was not created"
            [[ "$manual_mapping_type" == "controller" && "$controller_agent_ready" != "true" ]] && duplicate_reason="$controller_agent_reason"
            record_skip_result "Create duplicate port" "POST" "/api/v1/admin/port-mappings" \
                "$duplicate_reason" "$instance_group"
        fi

        # -- Create with invalid port --
        test_api "Create invalid port (0)" "POST" "/api/v1/admin/port-mappings" "400" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":0,\"protocol\":\"tcp\"}" "$instance_group"
        test_api "Create port (out of range)" "POST" "/api/v1/admin/port-mappings" "400" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":70000,\"protocol\":\"tcp\",\"hostPort\":70001}" "$instance_group"
        test_api "Create port (negative)" "POST" "/api/v1/admin/port-mappings" "400" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":-1,\"protocol\":\"tcp\",\"hostPort\":-1}" "$instance_group"
    fi

    # -- Sync port mappings --
    local sync_resp="" sync_payload='{"dryRun":true}'
    if [[ -n "$pm_id" && "$pm_id" =~ ^[0-9]+$ ]]; then
        sync_payload="{\"providerIds\":[${PROVIDER_ID}],\"includedPortIds\":[${pm_id}]}"
    fi
    sync_resp=$(test_api "Sync port mappings" "POST" "/api/v1/admin/port-mappings/sync" "200|infra" \
        "$sync_payload" "$group")
    if [[ "$(echo "$sync_resp" | jq -r '.code // empty' 2>/dev/null)" == "200" ]]; then
        local sync_task_id sync_task_resp
        while IFS= read -r sync_task_id; do
            [[ -n "$sync_task_id" ]] || continue
            sync_task_resp=""
            wait_task_complete "$SERVER_URL" "$sync_task_id" "$ADMIN_TOKEN" "${PORT_MAPPING_TASK_MAX_WAIT:-600}" 5 > /dev/null 2>&1 || true
            sync_task_resp=$(curl -s --max-time 20 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                "${SERVER_URL}/api/v1/admin/tasks/${sync_task_id}" 2>/dev/null) || true
            record_task_terminal_result "Sync port mappings task" "GET" \
                "/api/v1/admin/tasks/${sync_task_id}" "$sync_task_resp" "$group" || true
        done < <(echo "$sync_resp" | jq -r '.data.tasks[]?.id // .data.tasks[]?.ID // empty' 2>/dev/null)
    fi

    # -- Forward repair: preview is read-only; execution requires server-side second confirmation --
    test_api "Repair port mappings preview" "POST" "/api/v1/admin/port-mappings/repair" "200" \
        "{\"providerIds\":[${PROVIDER_ID}],\"dryRun\":true}" "$group"
    test_api "Repair mappings (missing confirmation)" "POST" "/api/v1/admin/port-mappings/repair" "400" \
        '{"portIds":[]}' "$group"
    test_api "Repair mappings (empty selection)" "POST" "/api/v1/admin/port-mappings/repair" "400" \
        '{"portIds":[],"confirmation":"REBUILD"}' "$group"

    # -- User port mappings --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User port mappings" "GET" "/api/v1/user/port-mappings" "200" "" "$group" "$USER_TOKEN"
    fi

    # -- Delete controller port mapping if created --
    if [[ -n "$ctrl_pm_id" ]]; then
        local ctrl_delete_resp="" ctrl_delete_task_id=""
        ctrl_delete_resp=$(test_api "Delete controller port mapping" "DELETE" \
            "/api/v1/admin/port-mappings/${ctrl_pm_id}" "200" "" "$instance_group") || true
        ctrl_delete_task_id=$(echo "$ctrl_delete_resp" | jq -r '.data.taskId // .data.task_id // empty' 2>/dev/null)
        wait_for_port_mapping_delete "Delete controller port mapping" "$ctrl_pm_id" \
            "$ctrl_delete_task_id" "$instance_group" || true
    fi

    # -- Delete single --
    if [[ -n "$pm_id" ]]; then
        local delete_resp="" delete_task_id=""
        delete_resp=$(test_api "Delete port mapping" "DELETE" \
            "/api/v1/admin/port-mappings/${pm_id}" "200" "" "$instance_group") || true
        delete_task_id=$(echo "$delete_resp" | jq -r '.data.taskId // .data.task_id // empty' 2>/dev/null)
        wait_for_port_mapping_delete "Delete port mapping" "$pm_id" "$delete_task_id" \
            "$instance_group" || true
    fi
    if [[ -n "${node_pm_id:-}" ]]; then
        local node_delete_resp="" node_delete_task_id=""
        node_delete_resp=$(test_api "Delete node port mapping" "DELETE" \
            "/api/v1/admin/port-mappings/${node_pm_id}" "200" "" "$instance_group") || true
        node_delete_task_id=$(echo "$node_delete_resp" | jq -r '.data.taskId // .data.task_id // empty' 2>/dev/null)
        wait_for_port_mapping_delete "Delete node port mapping" "$node_pm_id" \
            "$node_delete_task_id" "$instance_group" || true
    fi

    # -- Delete nonexistent --
    test_api "Delete nonexistent mapping" "DELETE" "/api/v1/admin/port-mappings/99999" "404|400" "" "$group"

    # -- Batch delete --
    # Only exercise batch deletion with a temporary mapping on the running
    # fixture. Older preserved runs can contain manual mappings on failed or
    # deleted instances; sending those IDs together would make the service
    # correctly reject the whole request and obscure this test.
    local batch_ids="" batch_created_id="" batch_host_port=""
    if [[ -n "$inst_for_pm" && "${node_mapping_expected:-400}" == "200" && \
          "${node_port_available:-false}" == "true" ]]; then
        batch_host_port=$((node_host_port + 1))
        local batch_pm=""
        if batch_pm=$(test_api "Create batch-delete mapping" "POST" "/api/v1/admin/port-mappings" "200" \
            "{\"instanceId\":${inst_for_pm},\"guestPort\":8081,\"protocol\":\"tcp\",\"hostPort\":${batch_host_port},\"mappingType\":\"node\"}" "$group"); then
            batch_created_id=$(echo "$batch_pm" | jq -r '.data.portId // .data.id // .data.ID // empty' 2>/dev/null)
        fi
        if [[ "$batch_created_id" =~ ^[0-9]+$ ]]; then
            batch_ids=$(printf '%s\n' "$batch_created_id")
        fi
    fi
    if [[ -n "$batch_ids" ]]; then
        local batch_delete_resp="" batch_task_id=""
        batch_delete_resp=$(test_api "Batch delete mappings" "POST" \
            "/api/v1/admin/port-mappings/batch-delete" "200" \
            "{\"ids\":[${batch_ids}]}" "$group") || true
        while IFS= read -r batch_task_id; do
            [[ -n "$batch_task_id" ]] || continue
            local batch_task_resp=""
            if batch_task_resp=$(wait_task_complete "$SERVER_URL" "$batch_task_id" "$ADMIN_TOKEN" \
                "${PORT_MAPPING_TASK_MAX_WAIT:-600}" 5); then
                test_api "Batch delete task ${batch_task_id} completed" "GET" \
                    "/api/v1/admin/tasks/${batch_task_id}" "200" "" "$group" >/dev/null
            else
                record_task_terminal_result "Batch delete task ${batch_task_id}" "GET" \
                    "/api/v1/admin/tasks/${batch_task_id}" "$batch_task_resp" "$group" || true
            fi
        done < <(echo "$batch_delete_resp" | jq -r '.data.taskIds[]? // .data.tasks[]?.id // .data.tasks[]?.ID // empty' 2>/dev/null)
        if [[ "$batch_created_id" =~ ^[1-9][0-9]*$ ]]; then
            test_api "Batch deleted mapping absent after task" "GET" \
                "/api/v1/admin/port-mappings/${batch_created_id}" "400|404" "" "$group" >/dev/null
        fi
    else
        record_skip_result "Batch delete mappings" "POST" "/api/v1/admin/port-mappings/batch-delete" \
            "temporary mapping was not created on the running fixture" "$group"
    fi

    # -- Negative: Check port with invalid protocol --
    test_api "Check port (invalid proto)" "POST" "/api/v1/admin/ports/check" "400" \
        "{\"providerId\":${PROVIDER_ID},\"hostPort\":25000,\"portCount\":1,\"protocol\":\"invalid\"}" "$group"

    # -- Negative: Sync with nonexistent provider --
    test_api "Sync (nonexistent provider)" "POST" "/api/v1/admin/port-mappings/sync" "200|400|404" \
        '{"providerIds":[99999]}' "$group"

    # -- Negative: Batch delete empty --
    test_api "Batch delete (empty)" "POST" "/api/v1/admin/port-mappings/batch-delete" "400" \
        '{"ids":[]}' "$group"

    # -- Negative: Create for nonexistent instance --
    test_api "Create port (no instance)" "POST" "/api/v1/admin/port-mappings" "400|404" \
        '{"instanceId":99999,"guestPort":22,"protocol":"tcp","hostPort":25555}' "$group"

    # -- Negative: User cannot manage port mappings --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> create mapping (403)" "POST" "/api/v1/admin/port-mappings" "401|403" \
            '{"instanceId":99999,"guestPort":22,"protocol":"tcp","hostPort":25001}' "$group" "$USER_TOKEN"
        test_api "User -> repair mappings (403)" "POST" "/api/v1/admin/port-mappings/repair" "401|403" \
            '{"dryRun":true}' "$group" "$USER_TOKEN"
    fi
}
