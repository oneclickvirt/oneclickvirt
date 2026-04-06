#!/bin/bash
# 模块 15: 域名管理 (Admin + User)
# 依赖: 09_providers (PROVIDER_ID)

run_module_15() {
    report_add_section "15 - 域名管理"
    local group="domains"

    # ── Admin 域名列表 ──
    test_api "Admin域名列表" "GET" "/api/v1/admin/domains?page=1&pageSize=10" "200" "" "$group"

    # ── User 域名操作 ──
    local u_token="${USER_TOKEN:-$ADMIN_TOKEN}"

    test_api "用户域名列表" "GET" "/api/v1/user/domains" "200" "" "$group" "$u_token"

    # ── 创建用户域名 ──
    local dom_data="{\"domain_name\":\"ci-test.example.com\",\"protocol\":\"http\",\"internal_port\":80}"
    local dr; dr=$(test_api "创建用户域名" "POST" "/api/v1/user/domains" "200" "$dom_data" "$group" "$u_token")
    local dom_id; dom_id=$(echo "$dr" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # ── 编辑域名 ──
    if [[ -n "$dom_id" ]]; then
        test_api "编辑用户域名" "PUT" "/api/v1/user/domains/${dom_id}" "200" \
            "{\"domain_name\":\"ci-test-edited.example.com\",\"protocol\":\"http\",\"internal_port\":8080}" "$group" "$u_token"
    fi

    # ── 删除用户域名 ──
    if [[ -n "$dom_id" ]]; then
        test_api "删除用户域名(User)" "DELETE" "/api/v1/user/domains/${dom_id}" "200" "" "$group" "$u_token"
    fi

    # ── Admin 域名配置 ──
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "获取域名配置" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" "" "$group"
        test_api "更新域名配置" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" \
            "{\"enabled\":true,\"max_domains_per_user\":3}" "$group"
    fi

    # ── Admin 删除域名(如果有残留) ──
    local admin_doms; admin_doms=$(curl -s --max-time 30 \
        -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/domains?page=1&pageSize=100" 2>/dev/null)
    local ci_dom_id; ci_dom_id=$(echo "$admin_doms" | jq -r '.data.list[]? | select(.domain_name | test("ci-test")) | .id' 2>/dev/null | head -1)
    if [[ -n "$ci_dom_id" ]]; then
        test_api "Admin删除域名" "DELETE" "/api/v1/admin/domains/${ci_dom_id}" "200" "" "$group"
    fi
}
