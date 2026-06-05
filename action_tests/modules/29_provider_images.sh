#!/bin/bash
# Module 29: Provider Image Individual Testing
# Dependencies: 01_init (ADMIN_TOKEN), 09_providers (PROVIDER_ID)
# Tests each available provider image: create instance → verify running → delete → verify deleted

run_module_29() {
    report_add_section "29 - Provider Image Testing"
    local group="provider_images"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "No provider, skipping image tests"
        return 1
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
            log_warning "No images found for provider type ${ENV_TYPE} arch ${provider_arch}"
            chain_break "$group" "No images available for testing"
            return 1
        fi
    fi

    log_info "Found ${image_count} candidate images (arch=${provider_arch})"
    ensure_provider_health_ready "$PROVIDER_ID" "$ADMIN_TOKEN" || {
        chain_break "$group" "Provider health check failed before image creation tests"
        return 1
    }

    # -- Test each unique image (deduplicate by name) --
    local tested=0 passed=0 failed=0
    declare -A seen_images   # track which image names we've already tested

    for idx in $(seq 0 $((image_count - 1))); do
        local img_entry; img_entry=$(echo "$images_json" | jq -c ".[$idx]" 2>/dev/null)
        # Try different field names for image identifier
        local img_name; img_name=$(echo "$img_entry" | jq -r '.image // .name // .url // empty' 2>/dev/null)
        local img_type; img_type=$(echo "$img_entry" | jq -r '.instanceType // .instance_type // "container"' 2>/dev/null)
        local img_arch; img_arch=$(echo "$img_entry" | jq -r '.architecture // empty' 2>/dev/null)

        if [[ -z "$img_name" ]]; then
            log_warning "Skipping image at index ${idx}: no name/identifier"
            continue
        fi

        # Skip if we've already tested this image name (deduplication)
        if [[ -n "${seen_images[$img_name]:-}" ]]; then
            log_debug "Skipping duplicate image: ${img_name}"
            continue
        fi

        # Skip images whose architecture doesn't match the provider's
        if [[ -n "$img_arch" && "$img_arch" != "null" && "$img_arch" != "$provider_arch" ]]; then
            log_debug "Skipping image ${img_name} (arch=${img_arch} != provider arch=${provider_arch})"
            continue
        fi

        # Skip images not matching current instance type config
        if ! should_test_type "$img_type"; then
            log_debug "Skipping ${img_type} image: ${img_name} (not testing ${img_type})"
            continue
        fi

        # Skip images not matching TEST_IMAGES filter (default: alpine,debian)
        if ! should_test_image "$img_name"; then
            log_debug "Skipping image: ${img_name} (not in TEST_IMAGES=${TEST_IMAGES})"
            continue
        fi

        # Mark as seen before testing so we never repeat
        seen_images[$img_name]=1
        tested=$((tested + 1))
        local test_label="Image[${tested}]: ${img_name} (${img_type}, arch=${img_arch:-${provider_arch}})"
        log_info "Testing: ${test_label}"

        # -- Create instance with this image --
        local inst_data
        if [[ "$img_type" == "vm" ]]; then
            inst_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"vm\",\"image\":\"${img_name}\",\"cpu\":1,\"memory\":512,\"disk\":5,\"bandwidth\":1000,\"network_type\":\"nat_ipv4\"}"
        else
            inst_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"${img_name}\",\"cpu\":1,\"memory\":512,\"disk\":5,\"bandwidth\":1000,\"network_type\":\"nat_ipv4\"}"
        fi

        local create_resp
        local create_retries="${IMAGE_CREATE_RETRIES:-3}"
        local create_retry_interval="${IMAGE_CREATE_RETRY_INTERVAL:-20}"
        if ! create_resp=$(test_api_retry "Create ${test_label}" "POST" "/api/v1/admin/instances" "200" \
            "$inst_data" "$create_retries" "$create_retry_interval" "$group"); then
            failed=$((failed + 1))
            continue
        fi

        local inst_id; inst_id=$(echo "$create_resp" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
        local task_id; task_id=$(echo "$create_resp" | jq -r '.data.task_id // empty' 2>/dev/null)

        # Handle async task-based creation
        if [[ -n "$task_id" ]]; then
            log_info "Waiting for creation task: ${task_id}"
            local task_result; task_result=$(wait_task_complete "$SERVER_URL" "$task_id" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 10) || true
            if [[ -z "$inst_id" ]]; then
                inst_id=$(echo "$task_result" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
            fi
        fi

        # Fallback: if no instance ID from response, find the most recently created instance with this image
        if [[ -z "$inst_id" ]]; then
            log_info "Instance ID not in response, querying instance list for image: ${img_name}"
            sleep 2
            local list_resp; list_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                "${SERVER_URL}/api/v1/admin/instances?page=1&pageSize=10&status=creating" 2>/dev/null) || true
            inst_id=$(echo "$list_resp" | jq -r '[.data.list[]? | select(.image=="'"${img_name}"'")] | sort_by(.id) | last | .id // .ID // empty' 2>/dev/null)
        fi

        if [[ -z "$inst_id" ]]; then
            log_error "Failed to create instance for image: ${img_name}"
            _record_result "Create ${test_label}" "POST" "/api/v1/admin/instances" "FAIL" "200" "" "No instance ID in response" "$group"
            failed=$((failed + 1))
            continue
        fi

        log_info "Created instance ${inst_id} with image ${img_name}"

        # -- Wait for instance to reach running state --
        wait_instance_status "$inst_id" "running" "$INSTANCE_STATUS_MAX_WAIT" 10 "$ADMIN_TOKEN" "image-test instance ${inst_id}" > /dev/null || true

        # -- Verify instance exists and has expected state --
        local verify_resp; verify_resp=$(test_api "Verify ${test_label}" "GET" \
            "/api/v1/admin/instances/${inst_id}" "200" "" "$group")
        local inst_status; inst_status=$(echo "$verify_resp" | jq -r '.data.status // empty' 2>/dev/null)
        local inst_image; inst_image=$(echo "$verify_resp" | jq -r '.data.image // empty' 2>/dev/null)

        if [[ -n "$inst_status" ]]; then
            log_info "Instance ${inst_id} status: ${inst_status}, image: ${inst_image}"
        fi

        # -- Delete instance --
        log_info "Deleting instance ${inst_id}..."
        if delete_instance_safe "$inst_id" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT"; then
            log_success "Deleted instance ${inst_id} (image: ${img_name})"
            _record_result "Delete ${test_label}" "DELETE" "/api/v1/admin/instances/${inst_id}" "PASS" "200" "200" "" "$group"
        else
            log_error "Failed to delete instance ${inst_id} (image: ${img_name})"
            _record_result "Delete ${test_label}" "DELETE" "/api/v1/admin/instances/${inst_id}" "FAIL" "200" "" "Deletion failed" "$group"
            failed=$((failed + 1))
            continue
        fi

        # -- Verify deleted --
        local del_verify; del_verify=$(curl -s --max-time 10 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/admin/instances/${inst_id}" 2>/dev/null) || true
        local del_code; del_code=$(echo "$del_verify" | jq -r '.code // empty' 2>/dev/null)
        if [[ "$del_code" != "200" ]]; then
            log_success "Verified instance ${inst_id} deleted (image: ${img_name})"
            _record_result "Verify deleted ${test_label}" "GET" "/api/v1/admin/instances/${inst_id}" "PASS" "404" "$del_code" "" "$group"
            passed=$((passed + 1))
        else
            log_error "Instance ${inst_id} still exists after deletion (image: ${img_name})"
            _record_result "Verify deleted ${test_label}" "GET" "/api/v1/admin/instances/${inst_id}" "FAIL" "404" "200" "Instance still exists" "$group"
            failed=$((failed + 1))
            # Force cleanup to prevent disk full
            delete_instance_safe "$inst_id" "$ADMIN_TOKEN" "$INSTANCE_TASK_MAX_WAIT" 2>/dev/null || true
        fi
    done

    log_section "Image test summary: tested=${tested} passed=${passed} failed=${failed}"

    if [[ $tested -eq 0 ]]; then
        log_warning "No images were tested (check INSTANCE_TYPES and provider type compatibility)"
    fi

    [[ $failed -eq 0 ]]
}
