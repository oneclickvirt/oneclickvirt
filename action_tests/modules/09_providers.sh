#!/bin/bash
# 模块 09: Provider 管理 (Admin)
# 依赖: 01_init (ADMIN_TOKEN), NODE_IP, NODE_PASSWORD 由环境变量传入

run_module_09() {
    report_add_section "09 - Provider 管理"
    local group="provider"

    # ── 列表(初始) ──
    test_api "获取Provider列表" "GET" "/api/v1/admin/providers?page=1&pageSize=10" "200" "" "$group"

    # ── SSH连接测试 ──
    if [[ -n "$NODE_IP" && -n "$NODE_PASSWORD" ]]; then
        test_api "测试SSH连接" "POST" "/api/v1/admin/providers/test-ssh-connection" "200" \
            "{\"ssh_host\":\"${NODE_IP}\",\"ssh_port\":22,\"ssh_user\":\"root\",\"ssh_password\":\"${NODE_PASSWORD}\"}" "$group"
    fi

    # ── 名称查重 ──
    test_api "检查Provider名称" "GET" "/api/v1/admin/providers/check-name?name=ci-test-${ENV_TYPE}" "200" "" "$group"

    # ── 创建 Provider ──
    local prov_data
    if [[ -n "$NODE_IP" && -n "$NODE_PASSWORD" ]]; then
        prov_data="{\"name\":\"ci-test-${ENV_TYPE}\",\"type\":\"${ENV_TYPE}\",\"ssh_host\":\"${NODE_IP}\",\"ssh_port\":22,\"ssh_user\":\"root\",\"ssh_password\":\"${NODE_PASSWORD}\"}"
    else
        prov_data="{\"name\":\"ci-test-${ENV_TYPE}\",\"type\":\"${ENV_TYPE}\",\"ssh_host\":\"127.0.0.1\",\"ssh_port\":22,\"ssh_user\":\"root\",\"ssh_password\":\"test\"}"
    fi
    local pr; pr=$(test_api "创建Provider" "POST" "/api/v1/admin/providers" "200" "$prov_data" "$group")
    PROVIDER_ID=$(echo "$pr" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    [[ -z "$PROVIDER_ID" ]] && { chain_break "$group" "创建Provider失败"; return 1; }

    # ── 检查端点 ──
    test_api "检查Provider端点" "GET" "/api/v1/admin/providers/check-endpoint?host=${NODE_IP:-127.0.0.1}&port=22" "200" "" "$group"

    # ── 编辑 Provider ──
    test_api "编辑Provider" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}" "200" \
        "{\"name\":\"ci-test-${ENV_TYPE}-edited\",\"type\":\"${ENV_TYPE}\"}" "$group"

    # ── 自动配置(流式) ──
    test_api "自动配置Provider(流式)" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/auto-configure-stream" "200" "" "$group"

    # ── 自动配置(任务式) ──
    local ac_r; ac_r=$(test_api "自动配置Provider(任务)" "POST" "/api/v1/admin/providers/auto-configure" "200" \
        "{\"provider_id\":${PROVIDER_ID}}" "$group")
    local task_id; task_id=$(echo "$ac_r" | jq -r '.data.task_id // .data.id // empty' 2>/dev/null)
    if [[ -n "$task_id" ]]; then
        # 等待配置任务完成
        wait_task_complete "$SERVER_URL" "$task_id" "$ADMIN_TOKEN" 600 15
    fi

    # ── 配置任务列表 ──
    test_api "配置任务列表" "GET" "/api/v1/admin/configuration-tasks?page=1&pageSize=10" "200" "" "$group"
    if [[ -n "$task_id" ]]; then
        test_api "配置任务详情" "GET" "/api/v1/admin/configuration-tasks/${task_id}" "200" "" "$group"
    fi

    # ── 健康检查 ──
    test_api "Provider健康检查" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/health-check" "200" "" "$group"
    test_api "Provider状态" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/status" "200" "" "$group"

    # ── 证书 ──
    test_api "生成证书" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/generate-cert" "200" "" "$group"

    # ── 端口配置 ──
    test_api "更新端口配置" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/port-config" "200" \
        "{\"port_range_start\":10000,\"port_range_end\":20000,\"max_ports_per_instance\":5}" "$group"
    test_api "端口使用情况" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/port-usage" "200" "" "$group"

    # ── IPv4池 ──
    test_api "设置IPv4池" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" \
        "{\"entries\":[{\"ip\":\"192.168.1.100\",\"gateway\":\"192.168.1.1\",\"subnet\":\"255.255.255.0\"}]}" "$group"
    test_api "获取IPv4池" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" "" "$group"
    test_api "清空IPv4池" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/ipv4-pool" "200" "" "$group"

    # ── 实例发现/同步 ──
    test_api "实例发现" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/discover" "200" "" "$group"
    test_api "获取孤立实例" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/orphaned" "200" "" "$group"
    test_api "实例同步检查" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/sync-check" "200" "" "$group"

    # ── 硬件报告 ──
    test_api "保存硬件报告" "POST" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200" \
        "{\"report_data\":\"CI test hardware report\"}" "$group"
    test_api "获取硬件报告" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200" "" "$group"
    test_api_noauth "公开硬件报告" "GET" "/api/v1/public/providers/${PROVIDER_ID}/hardware-report" "200" "" "$group"
    test_api "删除硬件报告" "DELETE" "/api/v1/admin/providers/${PROVIDER_ID}/hardware-report" "200" "" "$group"

    # ── 签到配置 ──
    test_api "获取签到配置" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/checkin-config" "200" "" "$group"
    test_api "更新签到配置" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/checkin-config" "200" \
        "{\"enabled\":true,\"interval_hours\":24}" "$group"

    # ── 域名配置 ──
    test_api "获取域名配置" "GET" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" "" "$group"
    test_api "更新域名配置" "PUT" "/api/v1/admin/providers/${PROVIDER_ID}/domain-config" "200" \
        "{\"enabled\":false}" "$group"

    # ── 导出配置 ──
    test_api "导出Provider配置" "POST" "/api/v1/admin/providers/export-configs" "200" "" "$group"

    # ── Provider API 路由 ──
    test_api "Provider API列表" "GET" "/api/v1/providers/" "200" "" "$group"
    test_api "Provider状态(API)" "GET" "/api/v1/providers/${PROVIDER_ID}/status" "200" "" "$group"
    test_api "Provider能力(API)" "GET" "/api/v1/providers/${PROVIDER_ID}/capabilities" "200" "" "$group"
    test_api "Provider镜像列表(API)" "GET" "/api/v1/providers/${PROVIDER_ID}/images" "200" "" "$group"
    test_api "Provider实例列表(API)" "GET" "/api/v1/providers/${PROVIDER_ID}/instances" "200" "" "$group"

    log_info "Provider ID: ${PROVIDER_ID} (用于后续模块)"
}
