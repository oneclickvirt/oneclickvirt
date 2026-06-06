#!/bin/bash
# Module 08: System Image Management
# Dependencies: 01_init (ADMIN_TOKEN)

run_module_08() {
    report_add_section "08 - System Images"
    local group="images"
    local test_arch="amd64"
    case "${TARGET_ARCH:-$(uname -m 2>/dev/null || echo x86_64)}" in
        aarch64|arm64) test_arch="arm64" ;;
        x86_64|amd64) test_arch="amd64" ;;
    esac
    # incus_images repo uses x86_64 instead of amd64 in filenames
    local lxd_arch="x86_64"
    [[ "$test_arch" == "arm64" ]] && lxd_arch="arm64"

    # Map ENV_TYPE to the correct GitHub releases repo for real image URLs
    local img_repo="docker"
    case "${ENV_TYPE:-docker}" in
        docker)     img_repo="docker" ;;
        podman)     img_repo="podman" ;;
        containerd) img_repo="containerd" ;;
        lxd|incus)  img_repo="docker" ;;  # LXD/Incus containers also use docker-format tar.gz
        *)          img_repo="docker" ;;
    esac
    local base_url="https://github.com/oneclickvirt/${img_repo}/releases/download"
    local img_provider_type="${ENV_TYPE:-docker}"
    # Normalize: lxd/incus container images are docker-format; keep providerType as the actual env
    log_info "System image tests: arch=${test_arch} env=${ENV_TYPE:-docker} repo=${img_repo}"

    # -- List --
    test_api "Image list" "GET" "/api/v1/admin/system-images?page=1&pageSize=10" "200" "" "$group"

    # -- Create images with REAL GitHub release URLs --
    local i1; i1=$(test_api "Create image (debian)" "POST" "/api/v1/admin/system-images" "200" \
        "{\"name\":\"ci-debian-12\",\"providerType\":\"${img_provider_type}\",\"instanceType\":\"container\",\"architecture\":\"${test_arch}\",\"url\":\"${base_url}/debian/spiritlhl_debian_${test_arch}.tar.gz\",\"description\":\"CI test debian image\",\"osType\":\"debian\",\"osVersion\":\"12\",\"minMemoryMB\":128,\"minDiskMB\":512}" "$group")
    local iid1; iid1=$(echo "$i1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    local i2; i2=$(test_api "Create image (ubuntu)" "POST" "/api/v1/admin/system-images" "200" \
        "{\"name\":\"ci-ubuntu-22.04\",\"providerType\":\"${img_provider_type}\",\"instanceType\":\"container\",\"architecture\":\"${test_arch}\",\"url\":\"${base_url}/ubuntu/spiritlhl_ubuntu_${test_arch}.tar.gz\",\"description\":\"CI test ubuntu image\",\"osType\":\"ubuntu\",\"osVersion\":\"22.04\",\"minMemoryMB\":128,\"minDiskMB\":512}" "$group")
    local iid2; iid2=$(echo "$i2" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # Alpine image: smallest (~5MB), preferred for instance creation tests
    local i3; i3=$(test_api "Create image (alpine)" "POST" "/api/v1/admin/system-images" "200" \
        "{\"name\":\"ci-alpine-3.19\",\"providerType\":\"${img_provider_type}\",\"instanceType\":\"container\",\"architecture\":\"${test_arch}\",\"url\":\"${base_url}/alpine/spiritlhl_alpine_${test_arch}.tar.gz\",\"description\":\"CI test alpine image (small, for creation tests)\",\"osType\":\"alpine\",\"osVersion\":\"3.19\",\"minMemoryMB\":64,\"minDiskMB\":256}" "$group")
    local iid3; iid3=$(echo "$i3" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # -- Create VM image (uses incus_images repo, different URL format) --
    local vm_img_url="https://github.com/oneclickvirt/incus_images/releases/download/debian/debian_12_bookworm_${lxd_arch}_cloud.zip"
    test_api "Create VM image" "POST" "/api/v1/admin/system-images" "200" \
        "{\"name\":\"ci-debian-12-vm\",\"providerType\":\"lxd\",\"instanceType\":\"vm\",\"architecture\":\"${test_arch}\",\"url\":\"${vm_img_url}\",\"description\":\"CI test VM image\",\"osType\":\"debian\",\"osVersion\":\"12\",\"minMemoryMB\":256,\"minDiskMB\":2048}" "$group"

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
        test_api "User filtered images" "GET" "/api/v1/user/images/filtered?provider_type=${ENV_TYPE}" "200|400" "" "$group" "$USER_TOKEN"
    fi

    # -- Delete single image test (use a temporary image, keep alpine for downstream modules) --
    # Create a temp image just for the single-delete test
    local tmp_img; tmp_img=$(test_api "Create temp image for delete test" "POST" "/api/v1/admin/system-images" "200" \
        "{\"name\":\"ci-temp-for-delete\",\"providerType\":\"${img_provider_type}\",\"instanceType\":\"container\",\"architecture\":\"${test_arch}\",\"url\":\"${base_url}/alpine/spiritlhl_alpine_${test_arch}.tar.gz\",\"description\":\"temp for delete test\",\"osType\":\"temp\",\"osVersion\":\"1\",\"minMemoryMB\":64,\"minDiskMB\":64}" "$group")
    local tmp_iid; tmp_iid=$(echo "$tmp_img" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    if [[ -n "$tmp_iid" ]]; then
        test_api "Delete image" "DELETE" "/api/v1/admin/system-images/${tmp_iid}" "200" "" "$group"
    fi

    # -- Delete nonexistent --
    test_api "Delete nonexistent image" "DELETE" "/api/v1/admin/system-images/99999" "404" "" "$group"

    # -- Batch delete --
    if [[ -n "$iid1" && -n "$iid2" ]]; then
        test_api "Batch delete images" "POST" "/api/v1/admin/system-images/batch-delete" "200" \
            "{\"ids\":[${iid1},${iid2}]}" "$group"
    fi

    # -- Negative: Edit nonexistent image --
    test_api "Edit nonexistent image" "PUT" "/api/v1/admin/system-images/99999" "400|404" \
        '{"name":"Ghost Image"}' "$group"

    # -- Negative: Create with negative resource values --
    test_api "Create image (negative memory)" "POST" "/api/v1/admin/system-images" "400" \
        "{\"name\":\"neg-test\",\"providerType\":\"${img_provider_type}\",\"instanceType\":\"container\",\"architecture\":\"${test_arch}\",\"url\":\"${base_url}/alpine/spiritlhl_alpine_${test_arch}.tar.gz\",\"minMemoryMB\":-1,\"minDiskMB\":-1}" "$group"

    # -- Negative: Batch status with empty ids --
    test_api "Batch status (empty ids)" "PUT" "/api/v1/admin/system-images/batch-status" "400" \
        '{"ids":[],"status":"active"}' "$group"

    # -- Negative: Batch delete empty --
    test_api "Batch delete (empty)" "POST" "/api/v1/admin/system-images/batch-delete" "400" \
        '{"ids":[]}' "$group"

    # -- Negative: User cannot manage images --
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "User -> create image (403)" "POST" "/api/v1/admin/system-images" "401|403" \
            '{"name":"hack"}' "$group" "$USER_TOKEN"
        test_api "User -> delete image (403)" "DELETE" "/api/v1/admin/system-images/1" "401|403" "" "$group" "$USER_TOKEN"
    fi
}
