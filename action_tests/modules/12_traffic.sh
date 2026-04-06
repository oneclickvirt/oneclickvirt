#!/bin/bash
# 模块 12: 流量管理 (Admin + User)
# 依赖: 09_providers (PROVIDER_ID)

run_module_12() {
    report_add_section "12 - 流量管理"
    local group="traffic"

    # ── Admin 流量概览 ──
    test_api "系统流量概览" "GET" "/api/v1/admin/traffic/overview" "200" "" "$group"

    # ── Provider 流量 ──
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Provider流量统计" "GET" "/api/v1/admin/traffic/provider/${PROVIDER_ID}" "200" "" "$group"
        test_api "Provider流量历史" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/traffic/history" "200" "" "$group"
    fi

    # ── 用户流量排名 ──
    test_api "用户流量排名" "GET" "/api/v1/admin/traffic/users/rank" "200" "" "$group"

    # ── 流量管理操作 ──
    test_api "流量限制管理" "POST" "/api/v1/admin/traffic/manage" "200" \
        "{\"action\":\"set\",\"target_type\":\"provider\",\"target_id\":${PROVIDER_ID:-0},\"monthly_limit_gb\":100}" "$group"

    # ── 流量监控(Provider级) ──
    test_api "流量监控操作" "POST" "/api/v1/admin/providers/traffic-monitor" "200" \
        "{\"action\":\"start\",\"provider_id\":${PROVIDER_ID:-0}}" "$group"
    test_api "流量监控任务列表" "GET" "/api/v1/admin/providers/traffic-monitor/tasks" "200" "" "$group"
    test_api "最新流量监控任务" "GET" "/api/v1/admin/providers/traffic-monitor/latest" "200" "" "$group"

    # ── 流量同步(Super Admin) ──
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "同步Provider流量" "POST" "/api/v1/admin/traffic/sync/provider/${PROVIDER_ID}" "200" "" "$group"
    fi
    test_api "全量同步流量" "POST" "/api/v1/admin/traffic/sync/all" "200" "" "$group"

    # ── User 流量接口 ──
    local u_token="${USER_TOKEN:-$ADMIN_TOKEN}"
    test_api "用户流量概览" "GET" "/api/v1/user/traffic/overview" "200" "" "$group" "$u_token"
    test_api "用户实例流量汇总" "GET" "/api/v1/user/traffic/instances" "200" "" "$group" "$u_token"
    test_api "用户流量限制状态" "GET" "/api/v1/user/traffic/limit-status" "200" "" "$group" "$u_token"
    test_api "用户流量历史" "GET" "/api/v1/user/traffic/history" "200" "" "$group" "$u_token"
}
