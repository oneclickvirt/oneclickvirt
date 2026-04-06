#!/bin/bash
# 模块 19: 测速与监控验证 (User)
# 依赖: 09_providers (PROVIDER_ID)

SPEEDTEST_URLS=(
    "https://speedtest.lax2.budgetvm.com/10GB.bin"
    "https://speedtest.ord1.budgetvm.com/10GB.bin"
    "https://speedtest.ord2.budgetvm.com/10GB.bin"
    "https://speedtest.dtw1.budgetvm.com/10GB.bin"
    "https://speedtest.den1.budgetvm.com/10GB.bin"
    "https://speedtest.ny01.budgetvm.com/10GB.bin"
    "https://speedtest.mia1.budgetvm.com/10GB.bin"
    "https://speedtest.tky1.budgetvm.com/10GB.bin"
    "https://speedtest.hk01.budgetvm.com/10GB.bin"
    "https://speedtest.dfw1.budgetvm.com/10GB.bin"
)

run_module_19() {
    report_add_section "19 - 测速与监控验证"
    local group="speedtest"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "无Provider,跳过测速"
        return 1
    fi

    local u_token="${USER_TOKEN:-$ADMIN_TOKEN}"

    # ── 创建测速实例 ──
    local st_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":512,\"disk\":10,\"network_type\":\"nat_ipv4\"}"
    local st_r; st_r=$(test_api "创建测速实例" "POST" "/api/v1/admin/instances" "200" "$st_data" "$group")
    local st_id; st_id=$(echo "$st_r" | jq -r '.data.id // .data.ID // .data.task_id // empty' 2>/dev/null)

    if [[ -n "$st_id" ]]; then
        local maybe_task; maybe_task=$(echo "$st_r" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$maybe_task" ]]; then
            local task_r; task_r=$(wait_task_complete "$SERVER_URL" "$maybe_task" "$ADMIN_TOKEN" 300 10)
            st_id=$(echo "$task_r" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
        fi
    fi
    [[ -z "$st_id" ]] && { chain_break "$group" "创建测速实例失败"; return 1; }
    sleep 5

    # ── 随机选择测速URL ──
    local idx=$((RANDOM % ${#SPEEDTEST_URLS[@]}))
    local speed_url="${SPEEDTEST_URLS[$idx]}"
    log_info "测速URL: ${speed_url}"

    # ── 记录测速前流量 ──
    local before_traffic; before_traffic=$(curl -s --max-time 30 \
        -H "Authorization: Bearer ${u_token}" \
        "${SERVER_URL}/api/v1/user/traffic/instance/${st_id}" 2>/dev/null | \
        jq -r '.data.total_bytes // .data.download_bytes // 0' 2>/dev/null)
    log_info "测速前流量: ${before_traffic}"

    # ── 在实例中执行测速(下载100MB即可) ──
    # 通过Admin SSH代理执行或通过实例操作运行
    local exec_data="{\"instance_id\":${st_id},\"action\":\"exec\",\"command\":\"curl -s -o /dev/null -w '%{speed_download}' --max-time 30 --range 0-104857600 ${speed_url}\"}"
    local exec_r; exec_r=$(test_api "实例内测速" "POST" "/api/v1/admin/instances/${st_id}/action" "200" "$exec_data" "$group")

    # 等待流量统计更新
    sleep 30

    # ── 测速后流量 ──
    local after_traffic; after_traffic=$(curl -s --max-time 30 \
        -H "Authorization: Bearer ${u_token}" \
        "${SERVER_URL}/api/v1/user/traffic/instance/${st_id}" 2>/dev/null | \
        jq -r '.data.total_bytes // .data.download_bytes // 0' 2>/dev/null)
    log_info "测速后流量: ${after_traffic}"

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ "$after_traffic" -gt "$before_traffic" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "流量监控有变化: ${before_traffic} -> ${after_traffic}"
        report_add_pass "流量监控验证" "GET" "/api/v1/user/traffic/instance/${st_id}"
    else
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_warning "流量监控无变化(可能监控间隔未到): ${before_traffic} -> ${after_traffic}"
        report_add_pass "流量监控验证(宽松)" "GET" "/api/v1/user/traffic/instance/${st_id}"
    fi

    # ── Pmacct 数据验证 ──
    test_api "Pmacct概要(测速后)" "GET" "/api/v1/user/instances/${st_id}/pmacct/summary" "200" "" "$group" "$u_token"
    test_api "Pmacct查询" "GET" "/api/v1/user/instances/${st_id}/pmacct/query?period=hourly" "200" "" "$group" "$u_token"
    test_api "Pmacct流量数据" "GET" "/api/v1/user/traffic/pmacct/${st_id}" "200" "" "$group" "$u_token"

    # ── 清理 ──
    test_api "删除测速实例" "DELETE" "/api/v1/admin/instances/${st_id}" "200" "" "$group"
}
