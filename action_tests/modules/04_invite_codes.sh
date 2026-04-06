#!/bin/bash
# 模块 04: 邀请码管理 (Super Admin)
# 依赖: 01_init (ADMIN_TOKEN)

run_module_04() {
    report_add_section "04 - 邀请码管理"
    local group="invite"

    # ── 邀请码列表(初始) ──
    test_api "获取邀请码列表" "GET" "/api/v1/admin/invite-codes?page=1&pageSize=10" "200" "" "$group"

    # ── 自定义创建 ──
    local code1_data="{\"code\":\"TEST-INVITE-CUSTOM-001\",\"max_uses\":5,\"level\":1}"
    local r1; r1=$(test_api "自定义创建邀请码" "POST" "/api/v1/admin/invite-codes" "200" "$code1_data" "$group")
    local code1_id; code1_id=$(echo "$r1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    [[ -z "$code1_id" ]] && chain_break "$group" "创建邀请码失败"

    # ── 批量生成 ──
    local gen_data="{\"count\":3,\"max_uses\":1,\"prefix\":\"CI-TEST-\",\"level\":1}"
    local gen_r; gen_r=$(test_api "批量生成邀请码" "POST" "/api/v1/admin/invite-codes/generate" "200" "$gen_data" "$group")
    local gen_ids; gen_ids=$(echo "$gen_r" | jq -r '[.data[]?.id // .data.codes[]?.id] | map(select(. != null))' 2>/dev/null)

    # ── 导出 ──
    test_api "导出邀请码" "GET" "/api/v1/admin/invite-codes/export" "200" "" "$group"

    # ── 使用邀请码注册 ──
    local invite_code; invite_code=$(echo "$gen_r" | jq -r '.data[0].code // .data.codes[0].code // empty' 2>/dev/null)
    if [[ -n "$invite_code" ]]; then
        local reg_data="{\"username\":\"invite_test_user\",\"password\":\"InvTest123!@#\",\"confirm_password\":\"InvTest123!@#\",\"invite_code\":\"${invite_code}\"}"
        test_api_noauth "使用邀请码注册" "POST" "/api/v1/auth/register" "200" "$reg_data" "$group"
    fi

    # ── 再次查询确认已使用 ──
    test_api "已使用后列表" "GET" "/api/v1/admin/invite-codes?page=1&pageSize=10" "200" "" "$group"

    # ── 单个删除 ──
    if [[ -n "$code1_id" ]]; then
        test_api "删除自定义邀请码" "DELETE" "/api/v1/admin/invite-codes/${code1_id}" "200" "" "$group"
    fi

    # ── 批量删除 ──
    if [[ "$gen_ids" != "[]" && "$gen_ids" != "null" && -n "$gen_ids" ]]; then
        local batch_ids; batch_ids=$(echo "$gen_ids" | jq -c '.' 2>/dev/null)
        [[ -n "$batch_ids" && "$batch_ids" != "[]" ]] && \
            test_api "批量删除邀请码" "POST" "/api/v1/admin/invite-codes/batch-delete" "200" \
                "{\"ids\":${batch_ids}}" "$group"
    fi

    # ── 清理注册的邀请码用户 ──
    local inv_users; inv_users=$(curl -s --max-time 30 \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/users?page=1&pageSize=100" 2>/dev/null)
    local inv_uid; inv_uid=$(echo "$inv_users" | jq -r '.data.list[]? | select(.username=="invite_test_user") | .id // .ID' 2>/dev/null | head -1)
    [[ -n "$inv_uid" ]] && test_api "清理邀请码用户" "DELETE" "/api/v1/admin/users/${inv_uid}" "200" "" "$group"
}
