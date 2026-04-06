#!/bin/bash
# 模块 13: 端口映射管理 (Admin + User)
# 依赖: 09_providers (PROVIDER_ID)

run_module_13() {
    report_add_section "13 - 端口映射管理"
    local group="ports"

    # ── Admin 端口列表 ──
    test_api "Admin端口映射列表" "GET" "/api/v1/admin/port-mappings?page=1&pageSize=10" "200" "" "$group"

    # ── 端口可用性检查 ──
    test_api "端口可用性检查" "POST" "/api/v1/admin/ports/check" "200" \
        "{\"provider_id\":${PROVIDER_ID:-0},\"port\":10001,\"protocol\":\"tcp\"}" "$group"

    # ── 创建端口映射 ──
    if [[ -n "$PROVIDER_ID" ]]; then
        local pm_data="{\"provider_id\":${PROVIDER_ID},\"external_port\":10001,\"internal_port\":22,\"protocol\":\"tcp\"}"
        local pm_r; pm_r=$(test_api "创建端口映射" "POST" "/api/v1/admin/port-mappings" "200" "$pm_data" "$group")
        local pm_id; pm_id=$(echo "$pm_r" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

        local pm2_data="{\"provider_id\":${PROVIDER_ID},\"external_port\":10002,\"internal_port\":80,\"protocol\":\"tcp\"}"
        local pm2_r; pm2_r=$(test_api "创建端口映射2" "POST" "/api/v1/admin/port-mappings" "200" "$pm2_data" "$group")
        local pm2_id; pm2_id=$(echo "$pm2_r" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

        # ── 同步端口映射 ──
        test_api "同步端口映射" "POST" "/api/v1/admin/port-mappings/sync" "200" \
            "{\"provider_id\":${PROVIDER_ID}}" "$group"

        # ── 删除端口映射 ──
        if [[ -n "$pm_id" ]]; then
            test_api "删除端口映射" "DELETE" "/api/v1/admin/port-mappings/${pm_id}" "200" "" "$group"
        fi

        # ── 批量删除 ──
        if [[ -n "$pm2_id" ]]; then
            test_api "批量删除端口映射" "POST" "/api/v1/admin/port-mappings/batch-delete" "200" \
                "{\"ids\":[${pm2_id}]}" "$group"
        fi
    fi

    # ── User 端口映射 ──
    local u_token="${USER_TOKEN:-$ADMIN_TOKEN}"
    test_api "用户端口映射列表" "GET" "/api/v1/user/port-mappings" "200" "" "$group" "$u_token"
}
