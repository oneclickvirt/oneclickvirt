#!/bin/bash
# Provider 相关 API 测试模块 - 需要节点已纳管
# 测试 Provider 管理、实例开设、监控、端口映射等完整功能链路

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ============================================================
# Provider 管理测试
# ============================================================
test_provider_management() {
    local provider_id="$1"
    local group="provider_mgmt"
    report_add_section "Provider 管理测试 (ID: ${provider_id})"

    # 获取 Provider 列表
    test_api "获取Provider列表" "GET" "/api/v1/admin/providers" "200" "" "$group" || {
        chain_break "$group" "获取Provider列表失败"
        return 1
    }

    # 获取 Provider 状态
    test_api "获取Provider状态" "GET" "/api/v1/admin/providers/${provider_id}/status" "200" "" "$group" || true

    # 健康检查
    test_api "Provider健康检查" "POST" "/api/v1/admin/providers/${provider_id}/health-check" "200" "" "$group" || true

    # 获取端口使用情况
    test_api "Provider端口使用" "GET" "/api/v1/admin/providers/${provider_id}/port-usage" "200" "" "$group" || true

    # 检查名称
    test_api "检查Provider名称" "GET" "/api/v1/admin/providers/check-name?name=test_nonexist" "200" "" "$group" || true

    # 实例发现
    test_api "发现Provider实例" "POST" "/api/v1/admin/providers/${provider_id}/discover" "200" "" "$group" || true

    # 获取孤立实例
    test_api "获取孤立实例" "GET" "/api/v1/admin/providers/${provider_id}/orphaned" "200" "" "$group" || true
}

# ============================================================
# 监控管理测试
# ============================================================
test_monitoring_management() {
    local provider_id="$1"
    local group="monitoring"
    report_add_section "监控管理测试 (Provider: ${provider_id})"

    # 获取监控配置
    test_api "获取监控配置" "GET" "/api/v1/admin/providers/${provider_id}/monitoring/config" "200" "" "$group" || true

    # Agent 部署
    test_api "部署Agent" "POST" "/api/v1/admin/providers/${provider_id}/monitoring/agent" "200" "" "$group" || true

    # 等待 Agent 启动
    sleep 30

    # Agent 状态
    test_api "获取Agent状态" "GET" "/api/v1/admin/providers/${provider_id}/monitoring/status" "200" "" "$group" || true

    # 获取监控器列表
    test_api "获取Provider监控器" "GET" "/api/v1/admin/providers/${provider_id}/monitoring/monitors" "200" "" "$group" || true

    # Agent 监控器列表
    test_api "获取Agent监控器列表" "GET" "/api/v1/admin/providers/${provider_id}/monitoring/agent-monitors" "200" "" "$group" || true

    # 同步监控器
    test_api "同步监控器" "POST" "/api/v1/admin/providers/${provider_id}/monitoring/sync" "200" "" "$group" || true

    # 资源概要
    test_api "Provider资源概要" "GET" "/api/v1/admin/providers/${provider_id}/monitoring/resources" "200" "" "$group" || true
}

# ============================================================
# 实例创建和管理测试（核心功能）
# ============================================================
test_instance_lifecycle() {
    local provider_id="$1"
    local provider_type="$2"
    local group="instance_lifecycle"
    report_add_section "实例生命周期测试 (${provider_type})"

    # 获取用户可用镜像
    local images_body
    images_body=$(test_api "获取可用镜像(filtered)" "GET" "/api/v1/user/images/filtered?provider_type=${provider_type}" "200" "" "$group") || {
        chain_break "$group" "获取镜像列表失败"
        return 1
    }

    # 获取 Provider 能力
    test_api "获取Provider能力(用户)" "GET" "/api/v1/user/providers/${provider_id}/capabilities" "200" "" "$group" || true

    # =================== 创建 Debian 实例 ===================
    log_info "创建 Debian 测试实例..."
    local debian_create_body
    debian_create_body=$(test_api "创建Debian实例" "POST" "/api/v1/user/instances" "200" \
        "{\"provider_id\":${provider_id},\"os\":\"debian\",\"cpu\":1,\"memory\":512,\"disk\":5,\"instance_type\":\"container\"}" "$group") || {
        chain_break "$group" "创建Debian实例失败"
        return 1
    }

    local debian_task_id
    debian_task_id=$(echo "$debian_create_body" | jq -r '.data.task_id // empty' 2>/dev/null)
    local debian_instance_id
    debian_instance_id=$(echo "$debian_create_body" | jq -r '.data.instance_id // .data.id // empty' 2>/dev/null)

    # 等待任务完成
    if [[ -n "$debian_task_id" ]]; then
        log_info "等待 Debian 实例创建任务完成 (task: ${debian_task_id})..."
        local task_result
        task_result=$(wait_task_complete "$SERVER_URL" "$debian_task_id" "$ADMIN_TOKEN" 600) || {
            chain_break "$group" "Debian实例创建任务超时或失败"
            # 即使失败也记录
            TOTAL_TESTS=$((TOTAL_TESTS + 1))
            FAILED_TESTS=$((FAILED_TESTS + 1))
            report_add_fail "等待Debian实例创建" "GET" "/api/v1/admin/tasks/${debian_task_id}" "" "completed" "timeout" "$task_result"
            return 1
        }
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        PASSED_TESTS=$((PASSED_TESTS + 1))
        report_add_pass "Debian实例创建完成" "任务" "/api/v1/admin/tasks/${debian_task_id}"

        # 从任务获取实例 ID
        if [[ -z "$debian_instance_id" ]]; then
            debian_instance_id=$(echo "$task_result" | jq -r '.data.instance_id // empty' 2>/dev/null)
        fi
    fi

    # 导出实例 ID 供后续测试使用
    export DEBIAN_INSTANCE_ID="$debian_instance_id"

    if [[ -n "$debian_instance_id" ]]; then
        test_instance_operations "$debian_instance_id" "$provider_id" "debian"
    fi

    # =================== 创建 Alpine 实例 ===================
    log_info "创建 Alpine 测试实例..."
    local alpine_create_body
    alpine_create_body=$(test_api "创建Alpine实例" "POST" "/api/v1/user/instances" "200" \
        "{\"provider_id\":${provider_id},\"os\":\"alpine\",\"cpu\":1,\"memory\":256,\"disk\":2,\"instance_type\":\"container\"}" "$group") || {
        log_warning "创建 Alpine 实例失败，跳过 Alpine 测试"
    }

    local alpine_task_id
    alpine_task_id=$(echo "${alpine_create_body:-}" | jq -r '.data.task_id // empty' 2>/dev/null)
    local alpine_instance_id
    alpine_instance_id=$(echo "${alpine_create_body:-}" | jq -r '.data.instance_id // .data.id // empty' 2>/dev/null)

    if [[ -n "$alpine_task_id" ]]; then
        log_info "等待 Alpine 实例创建任务完成..."
        local alpine_task_result
        alpine_task_result=$(wait_task_complete "$SERVER_URL" "$alpine_task_id" "$ADMIN_TOKEN" 600) || true
        if [[ -n "$alpine_task_result" ]]; then
            TOTAL_TESTS=$((TOTAL_TESTS + 1))
            local alpine_status
            alpine_status=$(echo "$alpine_task_result" | jq -r '.data.status // empty' 2>/dev/null)
            if [[ "$alpine_status" == "completed" ]]; then
                PASSED_TESTS=$((PASSED_TESTS + 1))
                report_add_pass "Alpine实例创建完成" "任务" "/api/v1/admin/tasks/${alpine_task_id}"
            else
                FAILED_TESTS=$((FAILED_TESTS + 1))
                report_add_fail "Alpine实例创建" "任务" "/api/v1/admin/tasks/${alpine_task_id}" "" "completed" "$alpine_status" "$alpine_task_result"
            fi
        fi

        if [[ -z "$alpine_instance_id" ]]; then
            alpine_instance_id=$(echo "${alpine_task_result:-}" | jq -r '.data.instance_id // empty' 2>/dev/null)
        fi
    fi

    export ALPINE_INSTANCE_ID="$alpine_instance_id"

    if [[ -n "$alpine_instance_id" ]]; then
        test_instance_operations "$alpine_instance_id" "$provider_id" "alpine"
    fi

    # =================== 清理实例 ===================
    report_add_section "实例清理"

    if [[ -n "$debian_instance_id" ]]; then
        test_instance_delete "$debian_instance_id" "debian"
    fi
    if [[ -n "$alpine_instance_id" ]]; then
        test_instance_delete "$alpine_instance_id" "alpine"
    fi
}

# ============================================================
# 实例操作测试
# ============================================================
test_instance_operations() {
    local instance_id="$1"
    local provider_id="$2"
    local os_name="$3"
    local group="instance_ops_${os_name}"
    report_add_section "${os_name} 实例操作测试 (ID: ${instance_id})"

    # 获取实例详情
    local detail_body
    detail_body=$(test_api "获取${os_name}实例详情" "GET" "/api/v1/user/instances/${instance_id}" "200" "" "$group") || {
        chain_break "$group" "获取实例详情失败"
        return 1
    }

    # 验证实例详情包含必要字段
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    local inst_status
    inst_status=$(echo "$detail_body" | jq -r '.data.status // empty' 2>/dev/null)
    if [[ "$inst_status" == "running" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        report_add_pass "${os_name}实例状态验证(running)" "验证" "实例状态"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "${os_name}实例状态验证" "验证" "实例状态" "" "running" "$inst_status" "$detail_body"
    fi

    # 获取实例端口信息
    test_api "获取${os_name}实例端口" "GET" "/api/v1/user/instances/${instance_id}/ports" "200" "" "$group" || true

    # 获取实例端口映射（管理员）
    test_api "获取${os_name}实例端口映射(管理)" "GET" "/api/v1/admin/instances/${instance_id}/port-mappings" "200" "" "$group" || true

    # 获取监控状态
    test_api "获取${os_name}实例监控状态" "GET" "/api/v1/user/instances/${instance_id}/monitoring/status" "200" "" "$group" || true

    # 获取监控数据
    test_api "获取${os_name}实例监控" "GET" "/api/v1/user/instances/${instance_id}/monitoring" "200" "" "$group" || true

    # 资源监控
    test_api "获取${os_name}实例资源监控" "GET" "/api/v1/user/instances/${instance_id}/monitoring/resources" "200" "" "$group" || true

    # 管理员资源监控
    test_api "获取${os_name}实例资源(管理)" "GET" "/api/v1/admin/instances/${instance_id}/monitoring/resources" "200" "" "$group" || true

    # Pmacct 数据
    test_api "获取${os_name}Pmacct摘要" "GET" "/api/v1/user/instances/${instance_id}/pmacct/summary" "200" "" "$group" || true

    # 流量历史
    test_api "获取${os_name}实例流量历史" "GET" "/api/v1/user/instances/${instance_id}/traffic/history" "200" "" "$group" || true

    # 实例流量详情
    test_api "获取${os_name}实例流量详情" "GET" "/api/v1/user/traffic/instance/${instance_id}" "200" "" "$group" || true

    # =================== SSH 连接 + 速度测试 ===================
    # 从详情获取 SSH 信息
    local ssh_host
    ssh_host=$(echo "$detail_body" | jq -r '.data.ssh_host // .data.host // empty' 2>/dev/null)
    local ssh_port
    ssh_port=$(echo "$detail_body" | jq -r '.data.ssh_port // .data.port // 22' 2>/dev/null)

    if [[ -n "$ssh_host" ]]; then
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if nc -z -w 5 "$ssh_host" "$ssh_port" 2>/dev/null; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            report_add_pass "${os_name}实例SSH端口可达" "TCP" "${ssh_host}:${ssh_port}"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            report_add_fail "${os_name}实例SSH端口不可达" "TCP" "${ssh_host}:${ssh_port}" "" "可达" "不可达" "端口检测失败"
        fi
    fi

    # =================== 速度测试（在主控VM上通过exec进行） ===================
    # 通过管理接口在节点上执行测速
    log_info "在${os_name}实例上执行基本网络测试..."

    # =================== 监控数据验证 ===================
    log_info "等待监控数据采集（120s）..."
    local mon_ok=false
    for i in $(seq 1 12); do
        sleep 10
        local mon_response
        mon_response=$(curl -s --max-time 10 \
            -H "Authorization: Bearer ${ADMIN_TOKEN}" \
            "${SERVER_URL}/api/v1/user/instances/${instance_id}/monitoring/resources" 2>/dev/null)
        local data_items
        data_items=$(echo "$mon_response" | jq '.data | if type == "array" then length else 0 end' 2>/dev/null)

        if [[ "${data_items:-0}" -gt 0 ]]; then
            mon_ok=true
            TOTAL_TESTS=$((TOTAL_TESTS + 1))
            PASSED_TESTS=$((PASSED_TESTS + 1))
            report_add_pass "${os_name}监控数据验证(${data_items}条)" "验证" "监控数据"
            break
        fi
        log_info "第 ${i}/12 次检查，暂无监控数据..."
    done

    if [[ "$mon_ok" == "false" ]]; then
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "${os_name}监控数据验证" "验证" "监控数据" "" "有数据" "无数据" "120秒内未检测到监控数据"
    fi

    # =================== 实例操作测试 ===================

    # 停止实例
    local stop_body
    stop_body=$(test_api "停止${os_name}实例" "POST" "/api/v1/user/instances/action" "200" \
        "{\"instance_id\":${instance_id},\"action\":\"stop\"}" "$group") || true
    local stop_task_id
    stop_task_id=$(echo "${stop_body:-}" | jq -r '.data.task_id // empty' 2>/dev/null)
    if [[ -n "$stop_task_id" ]]; then
        wait_task_complete "$SERVER_URL" "$stop_task_id" "$ADMIN_TOKEN" 120 || true
    else
        sleep 15
    fi

    # 启动实例
    local start_body
    start_body=$(test_api "启动${os_name}实例" "POST" "/api/v1/user/instances/action" "200" \
        "{\"instance_id\":${instance_id},\"action\":\"start\"}" "$group") || true
    local start_task_id
    start_task_id=$(echo "${start_body:-}" | jq -r '.data.task_id // empty' 2>/dev/null)
    if [[ -n "$start_task_id" ]]; then
        wait_task_complete "$SERVER_URL" "$start_task_id" "$ADMIN_TOKEN" 120 || true
    else
        sleep 15
    fi

    # 重启实例
    local restart_body
    restart_body=$(test_api "重启${os_name}实例" "POST" "/api/v1/user/instances/action" "200" \
        "{\"instance_id\":${instance_id},\"action\":\"restart\"}" "$group") || true
    local restart_task_id
    restart_task_id=$(echo "${restart_body:-}" | jq -r '.data.task_id // empty' 2>/dev/null)
    if [[ -n "$restart_task_id" ]]; then
        wait_task_complete "$SERVER_URL" "$restart_task_id" "$ADMIN_TOKEN" 120 || true
    else
        sleep 15
    fi

    # 重置密码
    local reset_pwd_body
    reset_pwd_body=$(test_api "重置${os_name}实例密码" "PUT" "/api/v1/user/instances/${instance_id}/reset-password" "200" "" "$group") || true
    local reset_task_id
    reset_task_id=$(echo "${reset_pwd_body:-}" | jq -r '.data.task_id // empty' 2>/dev/null)
    if [[ -n "$reset_task_id" ]]; then
        wait_task_complete "$SERVER_URL" "$reset_task_id" "$ADMIN_TOKEN" 120 || true
        # 获取新密码
        test_api "获取${os_name}新密码" "GET" "/api/v1/user/instances/${instance_id}/password/${reset_task_id}" "200" "" "$group" || true
    fi

    # 管理员实例操作
    test_api "管理员重置${os_name}实例密码" "PUT" "/api/v1/admin/instances/${instance_id}/reset-password" "200" "" "$group" || true
}

# ============================================================
# 实例删除测试
# ============================================================
test_instance_delete() {
    local instance_id="$1"
    local os_name="$2"
    local group="instance_delete"

    if [[ -z "$instance_id" ]]; then
        return 0
    fi

    log_info "删除 ${os_name} 实例 ${instance_id}..."
    local del_body
    del_body=$(test_api "删除${os_name}实例" "POST" "/api/v1/user/instances/action" "200" \
        "{\"instance_id\":${instance_id},\"action\":\"delete\"}" "$group") || true
    local del_task_id
    del_task_id=$(echo "${del_body:-}" | jq -r '.data.task_id // empty' 2>/dev/null)
    if [[ -n "$del_task_id" ]]; then
        wait_task_complete "$SERVER_URL" "$del_task_id" "$ADMIN_TOKEN" 300 || true
    fi
}

# ============================================================
# 端口映射测试
# ============================================================
test_port_mappings() {
    local provider_id="$1"
    local instance_id="$2"
    local group="port_mapping"
    report_add_section "端口映射测试"

    if [[ -z "$instance_id" ]]; then
        log_skip "无可用实例，跳过端口映射测试"
        return 0
    fi

    # 查看端口映射列表
    test_api "端口映射列表" "GET" "/api/v1/admin/port-mappings" "200" "" "$group" || true

    # 创建端口映射
    local port_body
    port_body=$(test_api "创建端口映射" "POST" "/api/v1/admin/port-mappings" "200" \
        "{\"instance_id\":${instance_id},\"host_port\":0,\"guest_port\":80,\"protocol\":\"tcp\"}" "$group") || true

    local port_id
    port_id=$(echo "${port_body:-}" | jq -r '.data.id // .data.task_id // empty' 2>/dev/null)

    # 检查端口可用性
    test_api "检查端口可用性" "POST" "/api/v1/admin/ports/check" "200" \
        "{\"provider_id\":${provider_id},\"port\":9999}" "$group" || true

    # 同步端口映射
    test_api "同步端口映射" "POST" "/api/v1/admin/port-mappings/sync" "200" \
        "{\"provider_id\":${provider_id}}" "$group" || true

    # 获取Provider端口配置
    test_api "获取Provider端口配置" "GET" "/api/v1/admin/providers/${provider_id}/port-usage" "200" "" "$group" || true

    # 删除端口映射
    if [[ -n "$port_id" ]] && echo "$port_id" | grep -qE '^[0-9]+$'; then
        test_api "删除端口映射" "DELETE" "/api/v1/admin/port-mappings/${port_id}" "200" "" "$group" || true
    fi
}

# ============================================================
# 冻结管理测试
# ============================================================
test_freeze_management() {
    local provider_id="$1"
    local group="freeze"
    report_add_section "冻结管理测试"

    # 设置 Provider 过期时间
    test_api "设置Provider过期时间" "POST" "/api/v1/admin/providers/set-expiry" "200" \
        "{\"provider_id\":${provider_id},\"expires_at\":\"2099-12-31T23:59:59Z\"}" "$group" || true

    # 手动冻结（测试后解冻，不影响后续测试）
    test_api "手动冻结Provider" "POST" "/api/v1/admin/providers/freeze-manual" "200" \
        "{\"provider_id\":${provider_id},\"reason\":\"测试冻结\"}" "$group" || true

    # 解冻
    test_api "解冻Provider" "POST" "/api/v1/admin/providers/unfreeze-manual" "200" \
        "{\"provider_id\":${provider_id}}" "$group" || true
}

# ============================================================
# 防火墙/屏蔽规则测试
# ============================================================
test_block_rules() {
    local provider_id="$1"
    local group="block_rules"
    report_add_section "屏蔽规则测试"

    # 获取屏蔽规则
    test_api "获取屏蔽规则列表" "GET" "/api/v1/admin/block-rules" "200" "" "$group" || true

    # 创建屏蔽规则
    local rule_body
    rule_body=$(test_api "创建屏蔽规则" "POST" "/api/v1/admin/block-rules" "200" \
        '{"name":"test-rule","pattern":"stratum+tcp","type":"mining","enabled":true,"ip_version":"both"}' "$group") || true
    local rule_id
    rule_id=$(echo "${rule_body:-}" | jq -r '.data.id // empty' 2>/dev/null)

    if [[ -n "$rule_id" ]]; then
        test_api "获取单个屏蔽规则" "GET" "/api/v1/admin/block-rules/${rule_id}" "200" "" "$group" || true
        test_api "更新屏蔽规则" "PUT" "/api/v1/admin/block-rules/${rule_id}" "200" \
            '{"name":"test-rule-updated","enabled":false}' "$group" || true
        test_api "删除屏蔽规则" "DELETE" "/api/v1/admin/block-rules/${rule_id}" "200" "" "$group" || true
    fi

    # Agent 启用的 Provider 列表
    test_api "获取Agent启用Provider" "GET" "/api/v1/admin/block-rules/agent-providers" "200" "" "$group" || true

    # Provider 屏蔽状态
    test_api "获取Provider屏蔽状态" "GET" "/api/v1/admin/providers/${provider_id}/block-status" "200" "" "$group" || true
}

# ============================================================
# 域名管理测试（通过数据库模拟）
# ============================================================
test_domain_management() {
    local provider_id="$1"
    local group="domain"
    report_add_section "域名管理测试"

    # 管理员域名列表
    test_api "管理员域名列表" "GET" "/api/v1/admin/domains" "200" "" "$group" || true

    # 用户域名列表
    test_api "用户域名列表" "GET" "/api/v1/user/domains" "200" "" "$group" || true

    # 获取域名配置
    test_api "获取域名配置" "GET" "/api/v1/admin/providers/${provider_id}/domain-config" "200" "" "$group" || true
}

# ============================================================
# KYC 测试（通过数据库模拟）
# ============================================================
test_kyc_management() {
    local group="kyc"
    report_add_section "KYC 管理测试"

    # 管理员 KYC 列表
    test_api "管理员KYC列表" "GET" "/api/v1/admin/kyc" "200" "" "$group" || true

    # 用户 KYC 状态
    test_api "用户KYC状态" "GET" "/api/v1/user/kyc" "200" "" "$group" || true
}

# ============================================================
# 签到测试
# ============================================================
test_checkin() {
    local provider_id="$1"
    local group="checkin"
    report_add_section "签到管理测试"

    # 获取签到配置
    test_api "获取签到配置" "GET" "/api/v1/admin/providers/${provider_id}/checkin-config" "200" "" "$group" || true
    test_api "获取签到记录" "GET" "/api/v1/user/checkin/records" "200" "" "$group" || true
}

# ============================================================
# 硬件报告测试
# ============================================================
test_hardware_report() {
    local provider_id="$1"
    local group="hardware"
    report_add_section "硬件报告测试"

    # 获取硬件报告
    test_api "获取硬件测试报告" "GET" "/api/v1/admin/providers/${provider_id}/hardware-report" "200" "" "$group" || true

    # 公共硬件报告
    test_api "获取公共硬件报告" "GET" "/api/v1/public/providers/${provider_id}/hardware-report" "200" "" "$group" || true
}

# ============================================================
# IPv4 池测试
# ============================================================
test_ipv4_pool() {
    local provider_id="$1"
    local group="ipv4_pool"
    report_add_section "IPv4 地址池测试"

    test_api "获取IPv4池" "GET" "/api/v1/admin/providers/${provider_id}/ipv4-pool" "200" "" "$group" || true
}

# ============================================================
# 流量同步测试
# ============================================================
test_traffic_sync() {
    local provider_id="$1"
    local group="traffic_sync"
    report_add_section "流量同步测试"

    test_api "同步Provider流量" "POST" "/api/v1/admin/traffic/sync/provider/${provider_id}" "200" "" "$group" || true
    test_api "同步全部流量" "POST" "/api/v1/admin/traffic/sync/all" "200" "" "$group" || true

    # Provider 流量历史
    test_api "Provider流量历史" "GET" "/api/v1/admin/providers/${provider_id}/traffic/history" "200" "" "$group" || true
}

# ============================================================
# 管理员分组测试
# ============================================================
test_admin_group() {
    local group="admin_group"
    report_add_section "管理员分组测试"

    test_api "获取管理员分组信息" "GET" "/api/v1/admin/group-info" "200" "" "$group" || true
}

# ============================================================
# 性能监控测试
# ============================================================
test_performance_monitoring() {
    local group="performance"
    report_add_section "性能监控测试"

    test_api "获取性能指标" "GET" "/api/v1/admin/performance/metrics" "200" "" "$group" || true
    test_api "获取性能历史" "GET" "/api/v1/admin/performance/history" "200" "" "$group" || true
    test_api "获取操作日志" "GET" "/api/v1/admin/monitoring/audit-logs" "200" "" "$group" || true
    test_api "获取日志日期列表" "GET" "/api/v1/admin/logs/dates" "200" "" "$group" || true
}

# ============================================================
# 运行所有 Provider 相关测试
# ============================================================
run_provider_tests() {
    local provider_id="$1"
    local provider_type="$2"

    test_provider_management "$provider_id"
    test_monitoring_management "$provider_id"
    test_freeze_management "$provider_id"
    test_block_rules "$provider_id"
    test_domain_management "$provider_id"
    test_kyc_management
    test_checkin "$provider_id"
    test_hardware_report "$provider_id"
    test_ipv4_pool "$provider_id"
    test_admin_group
    test_performance_monitoring
    test_instance_lifecycle "$provider_id" "$provider_type"
    test_port_mappings "$provider_id" "${DEBIAN_INSTANCE_ID:-}"
    test_traffic_sync "$provider_id"
}
