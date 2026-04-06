#!/bin/bash
# 模块 14: 防火墙/封禁规则 (Admin) - 三级应用
# 依赖: 09_providers (PROVIDER_ID)

run_module_14() {
    report_add_section "14 - 防火墙封禁规则"
    local group="firewall"

    # ── 规则列表 ──
    test_api "获取封禁规则列表" "GET" "/api/v1/admin/block-rules?page=1&pageSize=10" "200" "" "$group"

    # ── 创建规则 ──
    local rule1="{\"name\":\"ci-test-rule-1\",\"type\":\"ip\",\"value\":\"10.0.0.0/8\",\"action\":\"drop\",\"description\":\"CI测试规则1\"}"
    local r1; r1=$(test_api "创建封禁规则1" "POST" "/api/v1/admin/block-rules" "200" "$rule1" "$group")
    local rule1_id; rule1_id=$(echo "$r1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    [[ -z "$rule1_id" ]] && chain_break "$group" "创建封禁规则失败"

    local rule2="{\"name\":\"ci-test-rule-2\",\"type\":\"port\",\"value\":\"445\",\"action\":\"drop\",\"description\":\"CI测试规则2\"}"
    local r2; r2=$(test_api "创建封禁规则2" "POST" "/api/v1/admin/block-rules" "200" "$rule2" "$group")
    local rule2_id; rule2_id=$(echo "$r2" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # ── 查看详情 ──
    if [[ -n "$rule1_id" ]]; then
        test_api "封禁规则详情" "GET" "/api/v1/admin/block-rules/${rule1_id}" "200" "" "$group"
    fi

    # ── 编辑规则 ──
    if [[ -n "$rule1_id" ]]; then
        test_api "编辑封禁规则" "PUT" "/api/v1/admin/block-rules/${rule1_id}" "200" \
            "{\"name\":\"ci-test-rule-1-edited\",\"type\":\"ip\",\"value\":\"172.16.0.0/12\",\"action\":\"drop\"}" "$group"
    fi

    # ── 三级应用: 全局→Provider→实例 ──
    if [[ -n "$rule1_id" && -n "$PROVIDER_ID" ]]; then
        # 应用到Provider
        test_api "应用规则到Provider" "POST" "/api/v1/admin/block-rules/apply" "200" \
            "{\"rule_id\":${rule1_id},\"target_type\":\"provider\",\"target_ids\":[${PROVIDER_ID}]}" "$group"

        # 查看应用状态
        test_api "查看规则应用" "GET" "/api/v1/admin/block-rules/applications?rule_id=${rule1_id}" "200" "" "$group"

        # Provider封禁状态
        test_api "Provider封禁状态" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/block-status" "200" "" "$group"

        # Agent启用Provider列表
        test_api "Agent启用Provider列表" "GET" "/api/v1/admin/block-rules/agent-providers" "200" "" "$group"

        # 移除应用
        test_api "移除规则应用" "POST" "/api/v1/admin/block-rules/remove" "200" \
            "{\"rule_id\":${rule1_id},\"target_type\":\"provider\",\"target_ids\":[${PROVIDER_ID}]}" "$group"
    fi

    # ── 删除规则 ──
    if [[ -n "$rule1_id" ]]; then
        test_api "删除封禁规则1" "DELETE" "/api/v1/admin/block-rules/${rule1_id}" "200" "" "$group"
    fi
    if [[ -n "$rule2_id" ]]; then
        test_api "删除封禁规则2" "DELETE" "/api/v1/admin/block-rules/${rule2_id}" "200" "" "$group"
    fi
}
