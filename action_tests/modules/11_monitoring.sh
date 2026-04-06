#!/bin/bash
# 模块 11: 监控与Agent (Admin)
# 依赖: 09_providers (PROVIDER_ID)

run_module_11() {
    report_add_section "11 - 监控与Agent"
    local group="monitoring"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "无Provider,跳过监控测试"
        return 1
    fi

    # ── 监控配置 ──
    test_api "获取监控配置" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/config" "200" "" "$group"
    test_api "更新监控配置" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/config" "200" \
        "{\"enabled\":true,\"interval_seconds\":60,\"monitoring_type\":\"agent\"}" "$group"

    # ── Agent 部署 ──
    local agent_r; agent_r=$(test_api "部署Agent" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent" "200" "" "$group")
    if [[ $? -eq 0 ]]; then
        sleep 10
        # 等待Agent就绪
        test_api_retry "Agent状态" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/status" "200" "" 5 10 "$group"
    fi

    # ── 监控数据 ──
    test_api "获取Provider监控" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/monitors" "200" "" "$group"
    test_api "获取Agent监控列表" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent-monitors" "200" "" "$group"
    test_api "获取Provider资源概要" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/resources" "200" "" "$group"

    # ── 同步 ──
    test_api "同步Provider监控" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/sync" "200" "" "$group"

    # ── 清理数据 ──
    test_api "清理Provider监控数据" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/clear" "200" "" "$group"

    # ──卸载Agent ──
    test_api "卸载Agent" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/monitoring/agent" "200" "" "$group"
}
