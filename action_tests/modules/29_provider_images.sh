#!/bin/bash
# Module 29: Provider Image Individual Testing
# Dependencies: 01_init (ADMIN_TOKEN), 09_providers (PROVIDER_ID)
# Tests each available provider image and preserves created instances by default.

_m29_record_skip() {
    local name="$1" method="$2" endpoint="$3" reason="$4" expected="${5:-provider image test}" actual="${6:-skipped}"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    skipped=$((skipped + 1))
    SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
    log_skip "${name} - ${reason}"
    report_add_skip "$name" "$method" "$endpoint" "$reason"
    _record_result "$name" "$method" "$endpoint" "SKIP" "$expected" "$actual" "$reason" "$group"
}

_m29_delete_or_preserve_instance() {
    local instance_id="$1" token="$2" timeout="$3" label="$4" group="$5"
    if [[ "${ACTION_TEST_PRESERVE_INSTANCES:-true}" != "false" ]]; then
        log_info "Preserving image-test instance ${instance_id} (ACTION_TEST_PRESERVE_INSTANCES=true)"
        _m29_record_skip "Preserve ${label}" "DELETE" "/api/v1/admin/instances/${instance_id}" \
            "ACTION_TEST_PRESERVE_INSTANCES=true; leaving the created image-test instance" \
            "deletion explicitly skipped" "preserved"
        return 2
    fi
    delete_instance_safe "$instance_id" "$token" "$timeout"
}

_m29_record_failure() {
    local name="$1" method="$2" endpoint="$3" detail="$4" expected="$5" actual="$6"
    failed=$((failed + 1))
    record_fail_result "$name" "$method" "$endpoint" "$expected" "$actual" "$detail" "$group"
}

_m29_record_unavailable() {
    if is_infrastructure_failure_detail "$4"; then
        _m29_record_skip "$@"
    else
        _m29_record_failure "$@"
    fi
}

_m29_create_instance_nonfatal() {
    local name="$1" data="$2" endpoint="/api/v1/admin/instances"
    local raw http_code body api_code transport_status=0
    M29_CREATE_BODY=""
    raw=$(curl -s -w '\n%{http_code}' --max-time 120 \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        -H "Content-Type: application/json" \
        -X POST -d "$data" "${SERVER_URL}${endpoint}" 2>/dev/null) || transport_status=$?
    http_code=$(printf '%s\n' "$raw" | tail -1)
    body=$(printf '%s\n' "$raw" | sed '$d')
    api_code=$(printf '%s' "$body" | jq -r '.code // empty' 2>/dev/null) || api_code=""
    if [[ "$transport_status" == 0 && "$http_code" =~ ^20[01]$ && "$api_code" == 200 ]]; then
        record_pass_result "$name" POST "$endpoint" "HTTP/API 200 or 201" "$http_code/$api_code" "" "$group"
        M29_CREATE_BODY="$body"
        return 0
    fi
    local detail="transport=${transport_status}, HTTP=${http_code:-none}, API=${api_code:-none}; ${body}"
    if [[ "$transport_status" == 0 ]] && is_http_infrastructure_response "$http_code" "$body"; then
        _m29_record_unavailable "$name" POST "$endpoint" "$detail" "HTTP/API 200 or 201" "$http_code/$api_code"
    else
        _m29_record_failure "$name" POST "$endpoint" "$detail" "HTTP/API 200 or 201" "$http_code/$api_code"
    fi
    return 1
}

_m29_select_candidates() {
    local images_json="$1" provider_arch="$2"
    local max_per_family_type="${PROVIDER_IMAGE_MAX_PER_FAMILY_TYPE:-1}"
    [[ "$max_per_family_type" =~ ^[0-9]+$ ]] || max_per_family_type=1

    local selected
    if ! selected=$(jq 2>/dev/null -c \
        --arg test_images "${TEST_IMAGES:-alpine,debian}" \
        --arg provider_arch "$provider_arch" \
        --arg instance_types "${INSTANCE_TYPES:-both}" \
        --argjson max_per_family_type "$max_per_family_type" '
        def image_name:
            (.image // .name // .url // "" | tostring);
        def image_type:
            (.instanceType // .instance_type // "" | tostring | ascii_downcase) as $declared
            | if $declared != "" and $declared != "null" then $declared
              else ((image_name + " " + (.tag // "" | tostring)) | ascii_downcase) as $hint
              | if ($hint | test("(kvm|qcow2|\\.img|\\.raw|\\.iso|(^|[-_/ ])vm([-_/ ]|$))")) then "vm" else "container" end
              end;
        def image_arch:
            (.architecture // $provider_arch // "" | tostring | ascii_downcase);
        def patterns:
            ($test_images | split(",") | map(ascii_downcase | gsub("^\\s+|\\s+$"; "")) | map(select(length > 0)));
        def matches_patterns($name):
            ($test_images | ascii_downcase) == "all" or any(patterns[]; ($name | ascii_downcase | contains(.)));
        def image_family($name):
            (.osType // .os_type // "" | tostring | ascii_downcase) as $declared
            | if $declared != "" and $declared != "null" then $declared
              else ([patterns[] | select(($name | ascii_downcase | contains(.)))] | first)
                // (try (($name | ascii_downcase) | capture("(?<family>[a-z][a-z0-9]*)").family) catch "unknown")
              end;
        def version_key($name):
            (.osVersion // .os_version // "" | tostring) as $declared
            | ([($declared + " " + $name) | scan("[0-9]+(?:\\.[0-9]+){0,3}")] | first // "")
            | split(".") | map(tonumber) | . + [0, 0, 0, 0] | .[:4];

        [to_entries[]
            | .value as $entry
            | ($entry | image_name) as $name
            | ($entry | image_type) as $type
            | ($entry | image_arch) as $arch
            | select($name != "")
            | select($arch == "" or $arch == $provider_arch)
            | select($instance_types == "both" or $instance_types == $type)
            | select(matches_patterns($name))
            | {
                entry: $entry,
                name: $name,
                type: $type,
                arch: $arch,
                family: ($entry | image_family($name)),
                stable_rank: (if ($name | ascii_downcase | contains("edge")) then 0 else 1 end),
                version_key: ($entry | version_key($name))
              }
        ]
        | sort_by(.type, .family, .stable_rank, .version_key, .name)
        | group_by(.type, .family)
        | map(reverse | if $max_per_family_type > 0 then .[:$max_per_family_type] else . end)
        | (add // [])
        | sort_by(.type, .family, .name)
    ' <<< "$images_json"); then
        log_warning "Unable to select provider image candidates from API response"
        return 1
    fi
    printf '%s\n' "$selected"
}

run_module_29() {
    report_add_section "29 - Provider Image Testing"
    local group="provider_images"

    if [[ -z "$PROVIDER_ID" ]]; then
        record_skip_result "Provider image prerequisites" "HARNESS" "module-29" "No provider from prerequisite modules" "$group"
        chain_break "$group" "No provider, skipping image tests"
        return 0
    fi

    # -- Get provider's architecture so we only test matching images --
    local provider_arch=""
    local prov_resp; prov_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers/${PROVIDER_ID}" 2>/dev/null) || true
    provider_arch=$(echo "$prov_resp" | jq -r '.data.architecture // empty' 2>/dev/null)
    [[ -z "$provider_arch" || "$provider_arch" == "null" ]] && provider_arch="amd64"
    log_info "Provider ${PROVIDER_ID} architecture: ${provider_arch}"

    # -- Fetch available images for this provider --
    local images_resp; images_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/providers/${PROVIDER_ID}/images" 2>/dev/null) || true
    local images_json; images_json=$(echo "$images_resp" | jq -c '.data // []' 2>/dev/null)
    local image_count; image_count=$(echo "$images_json" | jq 'length' 2>/dev/null)

    if [[ -z "$image_count" || "$image_count" == "0" || "$image_count" == "null" ]]; then
        log_warning "No images available for provider ${PROVIDER_ID}, testing with default images"
        # Try system images as fallback — filter by provider type AND architecture
        # Map env type to system image providerType
        # proxmoxve environments use system images stored under providerType "proxmox"
        local img_provider_type="$ENV_TYPE"
        case "$ENV_TYPE" in
            proxmoxve) img_provider_type="proxmox" ;;
        esac
        images_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/system-images?page=1&pageSize=100&status=active" 2>/dev/null) || true
        images_json=$(echo "$images_resp" | jq -c \
            '[.data.list[]? | select(.providerType=="'"${img_provider_type}"'" and .architecture=="'"${provider_arch}"'")]' 2>/dev/null)
        image_count=$(echo "$images_json" | jq 'length' 2>/dev/null)

        if [[ -z "$image_count" || "$image_count" == "0" || "$image_count" == "null" ]]; then
            record_skip_result "Provider image prerequisites" "HARNESS" "module-29" "No images for ${ENV_TYPE}/${provider_arch}" "$group"
            log_warning "No images found for provider type ${ENV_TYPE} arch ${provider_arch}"
            chain_break "$group" "No images available for testing"
            return 0
        fi
    fi

    log_info "Found ${image_count} candidate images (arch=${provider_arch})"
    ensure_provider_health_ready "$PROVIDER_ID" "$ADMIN_TOKEN" || {
        record_fail_result "Provider image health precheck" "HARNESS" "module-29" "healthy provider" "not ready" "Provider health check failed before image creation tests" "$group"
        chain_break "$group" "Provider health check failed before image creation tests"
        return 0
    }

    local selected_images_json
    if ! selected_images_json=$(_m29_select_candidates "$images_json" "$provider_arch"); then
        record_fail_result "Provider image selection" "HARNESS" "module-29" "valid image selection" "invalid" "Failed to select representative provider images" "$group"
        chain_break "$group" "Failed to select representative provider images"
        return 0
    fi
    local selected_image_count
    selected_image_count=$(echo "$selected_images_json" | jq 'length' 2>/dev/null)
    [[ "$selected_image_count" =~ ^[0-9]+$ ]] || selected_image_count=0
    log_info "Selected ${selected_image_count}/${image_count} image(s) for execution (max per OS/type=${PROVIDER_IMAGE_MAX_PER_FAMILY_TYPE}; 0 means exhaustive)"

    if [[ "$selected_image_count" -eq 0 ]]; then
        record_skip_result "Provider image selection" "HARNESS" "module-29" "No images match ${TEST_IMAGES}/${INSTANCE_TYPES}/${provider_arch}" "$group"
        log_warning "No images matched TEST_IMAGES=${TEST_IMAGES}, INSTANCE_TYPES=${INSTANCE_TYPES}, arch=${provider_arch}"
        chain_break "$group" "No matching images available for testing"
        return 0
    fi

    # -- Test each selected unique image --
    local tested=0 passed=0 failed=0 skipped=0 consecutive_fails=0 max_consecutive_fails=3
    declare -A seen_images   # track which image names we've already tested

    for idx in $(seq 0 $((selected_image_count - 1))); do
        # Early termination: if N consecutive images fail creation, the provider
        # likely has a systemic issue and further attempts are futile.
        if [[ $consecutive_fails -ge $max_consecutive_fails ]]; then
            log_warning "Too many consecutive image creation failures (${consecutive_fails}/${max_consecutive_fails}); skipping remaining selected images"
            _m29_record_skip "Remaining image ${idx}" HARNESS module-29 \
                "Stopped after three consecutive creation failures" "image lifecycle" "not executed"
            continue
        fi

        local selected_entry; selected_entry=$(echo "$selected_images_json" | jq -c ".[$idx]" 2>/dev/null)
        local img_name; img_name=$(echo "$selected_entry" | jq -r '.name // empty' 2>/dev/null)
        local img_type; img_type=$(echo "$selected_entry" | jq -r '.type // empty' 2>/dev/null)
        local img_arch; img_arch=$(echo "$selected_entry" | jq -r '.arch // empty' 2>/dev/null)

        if [[ -z "$img_name" ]]; then
            log_warning "Skipping image at index ${idx}: no name/identifier"
            continue
        fi

        # Names can be shared by container and VM catalogs.
        local image_key
        image_key=$(jq -cn --arg name "$img_name" --arg type "$img_type" --arg arch "$img_arch" '[$name,$type,$arch]')
        if [[ -n "${seen_images[$image_key]:-}" ]]; then
            log_debug "Skipping duplicate image: ${img_name}"
            continue
        fi

        # Mark as seen before testing so we never repeat
        seen_images[$image_key]=1
        tested=$((tested + 1))
        local test_label="Image[${tested}]: ${img_name} (${img_type}, arch=${img_arch:-${provider_arch}})"
        log_info "Testing: ${test_label}"
        if [[ "$img_type" == "vm" && -n "${VM_RUNTIME_INFRA_UNAVAILABLE_REASON:-}" ]]; then
            _m29_record_skip "Create ${test_label}" "POST" "/api/v1/admin/instances" \
                "${VM_RUNTIME_INFRA_UNAVAILABLE_REASON}" "available VM runtime" "unavailable"
            continue
        fi
        if ! wait_provider_active_tasks_idle "$PROVIDER_ID" "provider ${PROVIDER_ID} before ${test_label}" "$ADMIN_TOKEN" "$PROVIDER_IMAGE_TASK_MAX_WAIT" 10; then
            _m29_record_skip "Create ${test_label}" "POST" "/api/v1/admin/instances" \
                "provider still had active tasks before image creation; avoiding overlapping provider operations" \
                "idle provider task queue" "busy"
            consecutive_fails=$((consecutive_fails + 1))
            continue
        fi

        # -- Create instance with this image --
        local inst_data guest_cpu guest_memory guest_disk
        if [[ "$img_type" == vm ]]; then
            guest_cpu="$ACTION_TEST_VM_CPU" guest_memory="$ACTION_TEST_VM_MEMORY" guest_disk="$ACTION_TEST_VM_DISK"
        else
            guest_cpu="$ACTION_TEST_CONTAINER_CPU" guest_memory="$ACTION_TEST_CONTAINER_MEMORY" guest_disk="$ACTION_TEST_CONTAINER_DISK"
        fi
        inst_data=$(jq -cn --argjson provider "$PROVIDER_ID" --arg type "$img_type" --arg image "$img_name" \
            --argjson cpu "$guest_cpu" --argjson memory "$guest_memory" --argjson disk "$guest_disk" \
            '{provider_id:$provider,instance_type:$type,image:$image,cpu:$cpu,memory:$memory,disk:$disk,bandwidth:1000,network_type:"nat_ipv4"}') || return 1

        local create_resp M29_CREATE_BODY=""
        if ! _m29_create_instance_nonfatal "Create ${test_label}" "$inst_data"; then
            consecutive_fails=$((consecutive_fails + 1))
            continue
        fi
        create_resp="$M29_CREATE_BODY"
        consecutive_fails=0

        local task_id; task_id=$(echo "$create_resp" | jq -r '.data.task_id // .data.taskId // empty' 2>/dev/null)
        local inst_id=""

        # Handle async task-based creation
        if [[ -n "$task_id" ]]; then
            log_info "Waiting for optional creation task: ${task_id}"
            local task_result task_ok=true
            task_result=$(wait_task_complete_nonfatal "$SERVER_URL" "$task_id" "$ADMIN_TOKEN" "$PROVIDER_IMAGE_TASK_MAX_WAIT" 10) || task_ok=false
            inst_id=$(echo "$task_result" | jq -r '.data.instance_id // .data.instanceId // .data.result.id // empty' 2>/dev/null)
            if [[ "$task_ok" != "true" ]]; then
                local task_status task_error
                task_status=$(echo "$task_result" | jq -r '.data.status // "failed"' 2>/dev/null)
                task_error=$(echo "$task_result" | jq -r '.data.errorMessage // .data.error_message // .message // .msg // empty' 2>/dev/null)
                if [[ "$img_type" == "vm" ]] && is_vm_runtime_infrastructure_failure_detail "$task_result"; then
                    mark_vm_runtime_infrastructure_unavailable "${task_error:-VM runtime prerequisite unavailable}"
                fi
                [[ -n "$inst_id" ]] && _m29_delete_or_preserve_instance "$inst_id" "$ADMIN_TOKEN" \
                    "$PROVIDER_IMAGE_TASK_MAX_WAIT" "$test_label" "$group" >/dev/null 2>&1 || true
                _m29_record_unavailable "Create ${test_label}" "POST" "/api/v1/admin/instances" \
                    "provider creation task ended with status=${task_status:-failed}; ${task_error:-missing task error}" \
                    "completed task with instance id" "task=${task_id}"
                consecutive_fails=$((consecutive_fails + 1))
                continue
            fi
        else
            inst_id=$(echo "$create_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
        fi

        # Creation must identify its own task/instance. An image-only lookup
        # could claim or delete an unrelated instance from another run.
        if [[ ! "$inst_id" =~ ^[1-9][0-9]*$ ]]; then
            _m29_record_failure "Create ${test_label}" POST /api/v1/admin/instances \
                "Creation response/task did not identify its instance" "positive instance id" "${inst_id:-empty}"
            consecutive_fails=$((consecutive_fails + 1))
            continue
        fi

        log_info "Created instance ${inst_id} with image ${img_name}"

        # -- Wait for instance to reach running state --
        local running_result=""
        if ! running_result=$(wait_instance_status_nonfatal "$inst_id" "running" "$PROVIDER_IMAGE_STATUS_MAX_WAIT" 10 "$ADMIN_TOKEN" "image-test instance ${inst_id}"); then
            _m29_record_unavailable "Run ${test_label}" "GET" "/api/v1/admin/instances/${inst_id}" \
                "Instance did not reach running state: ${running_result:-no status response}" \
                "running" "not-running"
            _m29_delete_or_preserve_instance "$inst_id" "$ADMIN_TOKEN" \
                "$PROVIDER_IMAGE_TASK_MAX_WAIT" "$test_label" "$group" >/dev/null 2>&1 || true
            consecutive_fails=$((consecutive_fails + 1))
            continue
        fi

        # -- Verify instance exists and has expected state --
        local verify_resp verify_transport=0
        verify_resp=$(curl -fsS --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/instances/${inst_id}" 2>/dev/null) || verify_transport=$?
        local verify_code; verify_code=$(echo "$verify_resp" | jq -r '.code // empty' 2>/dev/null)
        local inst_status; inst_status=$(echo "$verify_resp" | jq -r '.data.status // empty' 2>/dev/null)
        local inst_image; inst_image=$(echo "$verify_resp" | jq -r '.data.image // empty' 2>/dev/null)

        if [[ "$verify_transport" == 0 && "$verify_code" == "200" && "$inst_status" == running ]]; then
            TOTAL_TESTS=$((TOTAL_TESTS + 1))
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_info "Instance ${inst_id} status: ${inst_status}, image: ${inst_image}"
            report_add_pass "Verify ${test_label}" "GET" "/api/v1/admin/instances/${inst_id}"
            _record_result "Verify ${test_label}" "GET" "/api/v1/admin/instances/${inst_id}" "PASS" "200" "200" "" "$group"
        else
            _m29_record_failure "Verify ${test_label}" "GET" "/api/v1/admin/instances/${inst_id}" \
                "Created image-test instance could not be read back in running state: ${verify_resp}" "200/running" "${verify_code:-empty}/${inst_status:-empty}"
            _m29_delete_or_preserve_instance "$inst_id" "$ADMIN_TOKEN" \
                "$PROVIDER_IMAGE_TASK_MAX_WAIT" "$test_label" "$group" >/dev/null 2>&1 || true
            continue
        fi

        # -- Delete only when preservation is explicitly disabled --
        log_info "Requesting image-test instance cleanup: ${inst_id}..."
        local delete_status=0
        _m29_delete_or_preserve_instance "$inst_id" "$ADMIN_TOKEN" \
            "$PROVIDER_IMAGE_TASK_MAX_WAIT" "$test_label" "$group" || delete_status=$?
        if [[ "$delete_status" -eq 0 ]]; then
            log_success "Deleted instance ${inst_id} (image: ${img_name})"
            _record_result "Delete ${test_label}" "DELETE" "/api/v1/admin/instances/${inst_id}" "PASS" "200" "200" "" "$group"
        elif [[ "$delete_status" -eq 2 ]]; then
            continue
        else
            _m29_record_failure "Delete ${test_label}" "DELETE" "/api/v1/admin/instances/${inst_id}" \
                "delete operation did not settle; cleanup was requested but provider did not confirm deletion" \
                "deleted" "not-confirmed"
            continue
        fi

        # A lost connection or empty body does not prove deletion.
        local del_verify="" del_transport=0 del_http del_body del_code
        del_verify=$(curl -s -w '\n%{http_code}' --max-time 10 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/instances/${inst_id}" 2>/dev/null) || del_transport=$?
        del_http=$(printf '%s\n' "$del_verify" | tail -1)
        del_body=$(printf '%s\n' "$del_verify" | sed '$d')
        del_code=$(jq -r '.code // empty' <<< "$del_body" 2>/dev/null) || del_code=""
        if [[ "$del_transport" == 0 && "$del_http" == 404 && "$del_code" == 404 ]]; then
            record_pass_result "Verify deleted ${test_label}" GET "/api/v1/admin/instances/${inst_id}" "404" "404" "" "$group"
            passed=$((passed + 1))
        else
            _m29_record_failure "Verify deleted ${test_label}" GET "/api/v1/admin/instances/${inst_id}" \
                "Deletion was not confirmed: ${del_body}" "HTTP/API 404" "transport=${del_transport}, ${del_http}/${del_code}"
        fi

    done

    log_section "Image test summary: tested=${tested} passed=${passed} skipped=${skipped} failed=${failed}"

    if [[ $tested -eq 0 ]]; then
        log_warning "No images were tested (check INSTANCE_TYPES and provider type compatibility)"
    fi

    [[ $failed -eq 0 ]]
}
