#!/bin/bash
# 模块 08: 系统镜像管理 (Super Admin)
# 依赖: 01_init (ADMIN_TOKEN)

run_module_08() {
    report_add_section "08 - 系统镜像管理"
    local group="images"

    # ── 列表 ──
    test_api "获取系统镜像列表" "GET" "/api/v1/admin/system-images?page=1&pageSize=50" "200" "" "$group"

    # ── 创建镜像 ──
    local img1="{\"name\":\"ci-test-debian\",\"display_name\":\"CI Test Debian\",\"os_type\":\"linux\",\"provider_type\":\"docker\",\"image_source\":\"debian:12\",\"status\":\"active\"}"
    local r1; r1=$(test_api "创建测试镜像1" "POST" "/api/v1/admin/system-images" "200" "$img1" "$group")
    local img1_id; img1_id=$(echo "$r1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    [[ -z "$img1_id" ]] && chain_break "$group" "创建镜像失败"

    local img2="{\"name\":\"ci-test-ubuntu\",\"display_name\":\"CI Test Ubuntu\",\"os_type\":\"linux\",\"provider_type\":\"docker\",\"image_source\":\"ubuntu:22.04\",\"status\":\"active\"}"
    local r2; r2=$(test_api "创建测试镜像2" "POST" "/api/v1/admin/system-images" "200" "$img2" "$group")
    local img2_id; img2_id=$(echo "$r2" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    local img3="{\"name\":\"ci-test-alpine\",\"display_name\":\"CI Test Alpine\",\"os_type\":\"linux\",\"provider_type\":\"docker\",\"image_source\":\"alpine:3.19\",\"status\":\"inactive\"}"
    local r3; r3=$(test_api "创建测试镜像3" "POST" "/api/v1/admin/system-images" "200" "$img3" "$group")
    local img3_id; img3_id=$(echo "$r3" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # ── 编辑 ──
    if [[ -n "$img1_id" ]]; then
        test_api "编辑镜像1" "PUT" "/api/v1/admin/system-images/${img1_id}" "200" \
            "{\"name\":\"ci-test-debian-edited\",\"display_name\":\"CI Debian Edited\",\"os_type\":\"linux\",\"provider_type\":\"docker\",\"image_source\":\"debian:12\",\"status\":\"active\"}" "$group"
    fi

    # ── 批量状态更新 ──
    if [[ -n "$img2_id" && -n "$img3_id" ]]; then
        test_api "批量启用镜像" "PUT" "/api/v1/admin/system-images/batch-status" "200" \
            "{\"ids\":[${img2_id},${img3_id}],\"status\":\"active\"}" "$group"
    fi

    # ── 公开可用镜像 ──
    test_api_noauth "公开可用镜像" "GET" "/api/v1/public/system-images/available" "200" "" "$group"

    # ── 用户侧镜像 ──
    if [[ -n "$USER_TOKEN" ]]; then
        test_api "用户获取镜像" "GET" "/api/v1/user/images" "200" "" "$group" "$USER_TOKEN"
        test_api "用户过滤镜像" "GET" "/api/v1/user/images/filtered?provider_type=docker" "200" "" "$group" "$USER_TOKEN"
    fi

    # ── 单个删除 ──
    if [[ -n "$img1_id" ]]; then
        test_api "删除镜像1" "DELETE" "/api/v1/admin/system-images/${img1_id}" "200" "" "$group"
    fi

    # ── 批量删除 ──
    if [[ -n "$img2_id" && -n "$img3_id" ]]; then
        test_api "批量删除镜像" "POST" "/api/v1/admin/system-images/batch-delete" "200" \
            "{\"ids\":[${img2_id},${img3_id}]}" "$group"
    fi
}
