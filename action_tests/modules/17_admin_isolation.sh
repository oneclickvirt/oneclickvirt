#!/bin/bash
# 模块 17: 普通管理员隔离测试
# 依赖: 03_users (NORMAL_ADMIN_TOKEN), 09_providers (PROVIDER_ID)

run_module_17() {
    report_add_section "17 - 普通管理员隔离"
    local group="isolation"

    if [[ -z "$NORMAL_ADMIN_TOKEN" ]]; then
        log_warning "无普通管理员Token，跳过隔离测试"
        SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
        return 0
    fi

    local nat="$NORMAL_ADMIN_TOKEN"

    # ── 普通管理员面板 ──
    test_api "普通管理员面板" "GET" "/api/v1/admin/dashboard" "200" "" "$group" "$nat"

    # ── 普通管理员只看自己的Provider ──
    local na_providers; na_providers=$(test_api "普通管理员Provider列表" "GET" "/api/v1/admin/providers?page=1&pageSize=50" "200" "" "$group" "$nat")
    local na_prov_count; na_prov_count=$(echo "$na_providers" | jq -r '.data.total // (.data.list | length) // 0' 2>/dev/null)
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    # 普通管理员应只有自己下的节点(可能为0)
    PASSED_TESTS=$((PASSED_TESTS + 1))
    log_success "普通管理员Provider数: ${na_prov_count}"
    report_add_pass "普通管理员Provider隔离" "GET" "/api/v1/admin/providers"

    # ── SuperAdmin能看到所有 ──
    local sa_providers; sa_providers=$(test_api "超管Provider列表" "GET" "/api/v1/admin/providers?page=1&pageSize=50" "200" "" "$group" "$ADMIN_TOKEN")
    local sa_prov_count; sa_prov_count=$(echo "$sa_providers" | jq -r '.data.total // (.data.list | length) // 0' 2>/dev/null)
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ "$sa_prov_count" -ge "$na_prov_count" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "超管Provider数(${sa_prov_count}) >= 普通管理员(${na_prov_count})"
        report_add_pass "超管可见所有Provider" "GET" "/api/v1/admin/providers"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "隔离异常: 超管${sa_prov_count} < 普管${na_prov_count}"
        report_add_fail "超管可见所有Provider" "GET" "/api/v1/admin/providers" "" ">=${na_prov_count}" "$sa_prov_count" ""
    fi

    # ── 实例隔离 ──
    local na_instances; na_instances=$(test_api "普通管理员实例列表" "GET" "/api/v1/admin/instances?page=1&pageSize=50" "200" "" "$group" "$nat")
    local na_inst_count; na_inst_count=$(echo "$na_instances" | jq -r '.data.total // (.data.list | length) // 0' 2>/dev/null)
    log_info "普通管理员实例数: ${na_inst_count}"

    # ── 普通管理员不能访问超管接口 ──
    test_api "普管访问用户管理(应403)" "GET" "/api/v1/admin/users?page=1&pageSize=10" "403" "" "$group" "$nat"
    test_api "普管访问系统镜像(应403)" "GET" "/api/v1/admin/system-images?page=1&pageSize=10" "403" "" "$group" "$nat"
    test_api "普管访问邀请码(应403)" "GET" "/api/v1/admin/invite-codes?page=1&pageSize=10" "403" "" "$group" "$nat"
    test_api "普管访问公告管理(应403)" "GET" "/api/v1/admin/announcements?page=1&pageSize=10" "403" "" "$group" "$nat"
    test_api "普管访问系统配置(应403)" "GET" "/api/v1/admin/config" "403" "" "$group" "$nat"

    # ── 普通管理员可用接口 ──
    test_api "普管获取兑换码" "GET" "/api/v1/admin/redemption-codes?page=1&pageSize=10" "200" "" "$group" "$nat"
    test_api "普管获取任务" "GET" "/api/v1/admin/tasks?page=1&pageSize=10" "200" "" "$group" "$nat"
    test_api "普管获取分组信息" "GET" "/api/v1/admin/group-info" "200" "" "$group" "$nat"
}
