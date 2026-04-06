#!/bin/bash
# 模块 07: 系统配置与等级限制 (Super Admin / Admin)
# 依赖: 01_init (ADMIN_TOKEN)

run_module_07() {
    report_add_section "07 - 系统配置与等级限制"
    local group="config"

    # ── 获取统一配置 (Super Admin) ──
    local cfg; cfg=$(test_api "获取统一配置(admin)" "GET" "/api/v1/admin/config" "200" "" "$group")

    # ── 获取统一配置 (config路由) ──
    test_api "获取统一配置(config)" "GET" "/api/v1/config" "200" "" "$group"

    # ── 更新配置(部分字段) ──
    local update_cfg="{\"site_name\":\"CI-Test-Site\",\"registration_enabled\":true,\"registration_require_invite_code\":false}"
    test_api "更新系统配置" "PUT" "/api/v1/admin/config" "200" "$update_cfg" "$group"

    # ── 验证配置生效 ──
    local after; after=$(test_api "验证配置更新" "GET" "/api/v1/admin/config" "200" "" "$group")
    local site_name; site_name=$(echo "$after" | jq -r '.data.site_name // empty' 2>/dev/null)
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ "$site_name" == "CI-Test-Site" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "配置值验证: site_name=${site_name}"
        report_add_pass "配置值正确性验证" "GET" "/api/v1/admin/config"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "配置值不匹配: 期望CI-Test-Site, 实际${site_name}"
        report_add_fail "配置值正确性验证" "GET" "/api/v1/admin/config" "" "CI-Test-Site" "$site_name" "$after"
    fi

    # ── 等级限制配置 ──
    local level_cfg="{\"level_limits\":{\"1\":{\"max_instances\":2,\"max_cpu\":2,\"max_memory\":1024,\"max_disk\":20},\"2\":{\"max_instances\":5,\"max_cpu\":4,\"max_memory\":2048,\"max_disk\":50},\"3\":{\"max_instances\":10,\"max_cpu\":8,\"max_memory\":4096,\"max_disk\":100}}}"
    test_api "设置等级限制" "PUT" "/api/v1/admin/config" "200" "$level_cfg" "$group"

    # ── 公开系统配置验证 ──
    test_api_noauth "公开系统配置" "GET" "/api/v1/public/system-config" "200" "" "$group"

    # ── 注册配置(含邀请码设置验证) ──
    test_api_noauth "注册配置" "GET" "/api/v1/public/register-config" "200" "" "$group"

    # ── config路由更新 ──
    test_api "config路由更新" "PUT" "/api/v1/config" "200" \
        "{\"site_name\":\"CI-Test-Final\"}" "$group"

    # ── 管理员面板 ──
    test_api "管理员面板" "GET" "/api/v1/admin/dashboard" "200" "" "$group"

    # ── 系统监控(super admin) ──
    test_api "系统监控" "GET" "/api/v1/admin/monitoring/system" "200" "" "$group"

    # ── 审计日志 ──
    test_api "审计日志" "GET" "/api/v1/admin/monitoring/audit-logs?page=1&pageSize=10" "200" "" "$group"

    # ── 性能指标 ──
    test_api "性能指标" "GET" "/api/v1/admin/performance/metrics" "200" "" "$group"
    test_api "性能历史" "GET" "/api/v1/admin/performance/history" "200" "" "$group"

    # ── 日志 ──
    test_api "日志日期列表" "GET" "/api/v1/admin/logs/dates" "200" "" "$group"

    # ── Admin分组信息 ──
    test_api "获取分组信息" "GET" "/api/v1/admin/group-info" "200" "" "$group"
    test_api "更新分组信息" "PUT" "/api/v1/admin/group-info" "200" \
        "{\"name\":\"CI Test Group\"}" "$group"
}
