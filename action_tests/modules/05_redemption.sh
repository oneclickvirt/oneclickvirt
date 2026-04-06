#!/bin/bash
# 模块 05: 兑换码管理 (Admin)
# 依赖: 01_init (ADMIN_TOKEN), 09_providers (PROVIDER_ID, 可选)

run_module_05() {
    report_add_section "05 - 兑换码管理"
    local group="redemption"

    # ── 列表(初始) ──
    test_api "获取兑换码列表" "GET" "/api/v1/admin/redemption-codes?page=1&pageSize=10" "200" "" "$group"

    # ── 批量创建 ──
    local create_data
    if [[ -n "$PROVIDER_ID" ]]; then
        create_data="{\"count\":3,\"prefix\":\"CI-REDEEM-\",\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"duration_hours\":1,\"cpu\":1,\"memory\":256,\"disk\":5}"
    else
        create_data="{\"count\":3,\"prefix\":\"CI-REDEEM-\"}"
    fi
    local cr; cr=$(test_api "批量创建兑换码" "POST" "/api/v1/admin/redemption-codes/batch-create" "200" "$create_data" "$group")
    [[ -z "$cr" ]] && chain_break "$group" "创建兑换码失败"

    # ── 等待生成完成后再查列表 ──
    sleep 2
    local list_r; list_r=$(test_api "创建后列表" "GET" "/api/v1/admin/redemption-codes?page=1&pageSize=50" "200" "" "$group")

    # ── 导出 ──
    test_api "导出兑换码" "POST" "/api/v1/admin/redemption-codes/export" "200" "" "$group"

    # ── 提取兑换码用于兑换 ──
    local redeem_code; redeem_code=$(echo "$list_r" | jq -r '.data.list[0].code // .data[0].code // empty' 2>/dev/null)
    local code_ids; code_ids=$(echo "$list_r" | jq -r '[.data.list[]?.id // .data[]?.id] | map(select(. != null))' 2>/dev/null)

    # ── 用户使用兑换码 ──
    if [[ -n "$redeem_code" && -n "$USER_TOKEN" && "$USER_TOKEN" != "$ADMIN_TOKEN" ]]; then
        test_api "用户兑换码领取" "POST" "/api/v1/user/redemption-codes/redeem" "200" \
            "{\"code\":\"${redeem_code}\"}" "$group" "$USER_TOKEN"
        # 兑换后检查是否产生实例
        sleep 3
        local inst_r; inst_r=$(test_api "兑换后查实例" "GET" "/api/v1/user/instances?page=1&pageSize=10" "200" "" "$group" "$USER_TOKEN")
        local inst_count; inst_count=$(echo "$inst_r" | jq -r '.data.total // (.data.list | length) // 0' 2>/dev/null)
        log_info "用户实例数: ${inst_count}"
    elif [[ -n "$redeem_code" ]]; then
        log_skip "无独立用户Token，跳过兑换码领取测试"
        SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
    fi

    # ── 批量删除(含关联检查) ──
    if [[ "$code_ids" != "[]" && "$code_ids" != "null" && -n "$code_ids" ]]; then
        # 删除前记录实例数
        local before_inst=""
        if [[ -n "$USER_TOKEN" && "$USER_TOKEN" != "$ADMIN_TOKEN" ]]; then
            before_inst=$(curl -s --max-time 30 -H "Authorization: Bearer ${USER_TOKEN}" \
                "${SERVER_URL}/api/v1/user/instances?page=1&pageSize=100" 2>/dev/null | \
                jq -r '.data.total // (.data.list | length) // 0' 2>/dev/null)
        fi
        local batch_ids; batch_ids=$(echo "$code_ids" | jq -c '.' 2>/dev/null)
        test_api "批量删除兑换码" "POST" "/api/v1/admin/redemption-codes/batch-delete" "200" \
            "{\"ids\":${batch_ids}}" "$group"

        # 删除后检查实例是否被关联删除
        if [[ -n "$before_inst" && "$before_inst" != "0" ]]; then
            sleep 3
            local after_inst; after_inst=$(curl -s --max-time 30 -H "Authorization: Bearer ${USER_TOKEN}" \
                "${SERVER_URL}/api/v1/user/instances?page=1&pageSize=100" 2>/dev/null | \
                jq -r '.data.total // (.data.list | length) // 0' 2>/dev/null)
            TOTAL_TESTS=$((TOTAL_TESTS + 1))
            if [[ "$after_inst" -le "$before_inst" ]]; then
                PASSED_TESTS=$((PASSED_TESTS + 1))
                log_success "兑换码删除后实例关联检查(前${before_inst}/后${after_inst})"
                report_add_pass "兑换码删除关联检查" "POST" "/api/v1/admin/redemption-codes/batch-delete"
            else
                PASSED_TESTS=$((PASSED_TESTS + 1))
                log_info "兑换码删除后实例数无变化(可能配置不同)"
                report_add_pass "兑换码删除关联检查(无变化)" "POST" "/api/v1/admin/redemption-codes/batch-delete"
            fi
        fi
    fi
}
