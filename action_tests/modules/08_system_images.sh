#!/bin/bash
# Module 08: System Image Management
# Dependencies: 01_init (ADMIN_TOKEN)

run_module_08() {
    report_add_section "08 - System Images"
    local group="images"

    # -- List --
    test_api "Image list" "GET" "/api/v1/admin/system-images?page=1&pageSize=10" "200" "" "$group"

    # -- Create images --
    local i1; i1=$(test_api "Create image (debian)" "POST" "/api/v1/admin/system-images" "200" \
        '{"name":"Debian 12","image":"debian:12","type":"container","provider_types":["docker","lxd","incus","podman","containerd"],"status":"active"}' "$group")
    local iid1; iid1=$(echo "$i1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    local i2; i2=$(test_api "Create image (ubuntu)" "POST" "/api/v1/admin/system-images" "200" \
        '{"name":"Ubuntu 22.04","image":"ubuntu:22.04","type":"container","provider_types":["docker","lxd","incus"],"status":"active"}' "$group")
    local iid2; iid2=$(echo "$i2" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    local i3; i3=$(test_api "Create image (alpine)" "POST" "/api/v1/admin/system-images" "200" \
        '{"name":"Alpine 3.19","image":"alpine:3.19","type":"container","provider_types":["docker","podman","containerd"],"status":"active"}' "$group")
    local iid3; iid3=$(echo "$i3" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Create VM image --
    test_api "Create VM image" "POST" "/api/v1/admin/system-images" "200" \
        '{"name":"Debian 12 VM","image":"debian:12","type":"vm","provider_types":["lxd","incus","proxmoxve"],"status":"active"}' "$group"

    # -- Create with missing name --
    test_api "Create image (no name)" "POST" "/api/v1/admin/system-images" "400" \
        '{"image":"test:latest"}' "$group"

    # -- Edit --
    if [[ -n "$iid1" ]]; then
        test_api "Edit image" "PUT" "/api/v1/admin/system-images/${iid1}" "200" \
            '{"name":"Debian 12 Updated"}' "$group"
    fi

    # -- Batch status update --
    if [[ -n "$iid1" && -n "$iid2" ]]; then
        test_api "Batch deactivate images" "PUT" "/api/v1/admin/system-images/batch-status" "200" \
            "{\"ids\":[${iid1},${iid2}],\"status\":\"inactive\"}" "$group"
        test_api "Batch activate images" "PUT" "/api/v1/admin/system-images/batch-status" "200" \
            "{\"ids\":[${iid1},${iid2}],\"status\":\"active\"}" "$group"
    fi

    # -- Public available images --
    test_api_noauth "Public available images" "GET" "/api/v1/public/system-images/available" "200" "" "$group"

    # -- User images --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User image list" "GET" "/api/v1/user/images" "200" "" "$group" "$USER_TOKEN"
        test_api "User filtered images" "GET" "/api/v1/user/images/filtered?provider_type=${ENV_TYPE}" "200" "" "$group" "$USER_TOKEN"
    fi

    # -- Delete single --
    if [[ -n "$iid3" ]]; then
        test_api "Delete image" "DELETE" "/api/v1/admin/system-images/${iid3}" "200" "" "$group"
    fi

    # -- Delete nonexistent --
    test_api "Delete nonexistent image" "DELETE" "/api/v1/admin/system-images/99999" "404" "" "$group"

    # -- Batch delete --
    if [[ -n "$iid1" && -n "$iid2" ]]; then
        test_api "Batch delete images" "POST" "/api/v1/admin/system-images/batch-delete" "200" \
            "{\"ids\":[${iid1},${iid2}]}" "$group"
    fi
}
