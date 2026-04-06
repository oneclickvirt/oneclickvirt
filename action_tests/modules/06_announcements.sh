#!/bin/bash
# 模块 06: 公告管理 (Super Admin)
# 依赖: 01_init (ADMIN_TOKEN)

run_module_06() {
    report_add_section "06 - 公告管理"
    local group="announcements"

    # ── 列表 ──
    test_api "获取公告列表" "GET" "/api/v1/admin/announcements?page=1&pageSize=10" "200" "" "$group"

    # ── 创建公告 ──
    local a1="{\"title\":\"CI测试公告1\",\"content\":\"这是CI测试公告内容\",\"type\":\"info\",\"status\":\"active\"}"
    local r1; r1=$(test_api "创建公告1" "POST" "/api/v1/admin/announcements" "200" "$a1" "$group")
    local a1_id; a1_id=$(echo "$r1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    [[ -z "$a1_id" ]] && chain_break "$group" "创建公告失败"

    local a2="{\"title\":\"CI测试公告2\",\"content\":\"第二条测试公告\",\"type\":\"warning\",\"status\":\"active\"}"
    local r2; r2=$(test_api "创建公告2" "POST" "/api/v1/admin/announcements" "200" "$a2" "$group")
    local a2_id; a2_id=$(echo "$r2" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    local a3="{\"title\":\"CI测试公告3\",\"content\":\"第三条测试公告\",\"type\":\"error\",\"status\":\"inactive\"}"
    local r3; r3=$(test_api "创建公告3" "POST" "/api/v1/admin/announcements" "200" "$a3" "$group")
    local a3_id; a3_id=$(echo "$r3" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # ── 编辑 ──
    if [[ -n "$a1_id" ]]; then
        test_api "编辑公告1" "PUT" "/api/v1/admin/announcements/${a1_id}" "200" \
            "{\"title\":\"CI-已编辑公告\",\"content\":\"更新后内容\",\"type\":\"info\",\"status\":\"active\"}" "$group"
    fi

    # ── 批量状态更新 ──
    if [[ -n "$a2_id" && -n "$a3_id" ]]; then
        test_api "批量禁用公告" "PUT" "/api/v1/admin/announcements/batch-status" "200" \
            "{\"ids\":[${a2_id},${a3_id}],\"status\":\"inactive\"}" "$group"
    fi

    # ── 公开接口验证 ──
    test_api_noauth "公开获取公告" "GET" "/api/v1/public/announcements" "200" "" "$group"

    # ── 单个删除 ──
    if [[ -n "$a1_id" ]]; then
        test_api "删除公告1" "DELETE" "/api/v1/admin/announcements/${a1_id}" "200" "" "$group"
    fi

    # ── 批量删除 ──
    if [[ -n "$a2_id" && -n "$a3_id" ]]; then
        test_api "批量删除公告" "POST" "/api/v1/admin/announcements/batch-delete" "200" \
            "{\"ids\":[${a2_id},${a3_id}]}" "$group"
    fi
}
