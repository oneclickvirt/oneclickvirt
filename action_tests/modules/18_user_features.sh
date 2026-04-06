#!/bin/bash
# 模块 18: 用户侧功能 (User)
# 依赖: 01_init (USER_TOKEN), 09_providers (PROVIDER_ID)

run_module_18() {
    report_add_section "18 - 用户侧功能"
    local group="user_feat"
    local u_token="${USER_TOKEN:-$ADMIN_TOKEN}"

    # ── 用户信息 ──
    test_api "用户信息" "GET" "/api/v1/user/info" "200" "" "$group" "$u_token"
    test_api "用户资料" "GET" "/api/v1/user/profile" "200" "" "$group" "$u_token"
    test_api "用户面板" "GET" "/api/v1/user/dashboard" "200" "" "$group" "$u_token"
    test_api "用户限额" "GET" "/api/v1/user/limits" "200" "" "$group" "$u_token"

    # ── 编辑资料 ──
    test_api "更新用户资料" "PUT" "/api/v1/user/profile" "200" \
        "{\"nickname\":\"CI-Test-User\"}" "$group" "$u_token"

    # ── 用户重置密码 ──
    test_api "用户重置密码" "PUT" "/api/v1/user/reset-password" "200" \
        "{\"old_password\":\"${TEST_USER_PASS}\",\"new_password\":\"${TEST_USER_PASS}\"}" "$group" "$u_token"

    # ── 可用资源 ──
    test_api "可用资源" "GET" "/api/v1/user/resources/available" "200" "" "$group" "$u_token"
    test_api "可用Provider" "GET" "/api/v1/user/providers/available" "200" "" "$group" "$u_token"
    test_api "用户镜像" "GET" "/api/v1/user/images" "200" "" "$group" "$u_token"
    test_api "实例配置" "GET" "/api/v1/user/instance-config" "200" "" "$group" "$u_token"
    test_api "实例类型权限" "GET" "/api/v1/user/instance-type-permissions" "200" "" "$group" "$u_token"

    # ── Provider能力 ──
    if [[ -n "$PROVIDER_ID" ]]; then
        test_api "Provider能力(用户)" "GET" "/api/v1/user/providers/${PROVIDER_ID}/capabilities" "200" "" "$group" "$u_token"
    fi

    # ── 用户实例列表 ──
    test_api "用户实例列表" "GET" "/api/v1/user/instances" "200" "" "$group" "$u_token"

    # ── 用户创建实例(等级限制验证) ──
    if [[ -n "$PROVIDER_ID" ]]; then
        local u_inst="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":256,\"disk\":5,\"network_type\":\"nat_ipv4\"}"
        local uir; uir=$(test_api "用户创建实例" "POST" "/api/v1/user/instances" "200" "$u_inst" "$group" "$u_token")
        local u_inst_id; u_inst_id=$(echo "$uir" | jq -r '.data.id // .data.task_id // empty' 2>/dev/null)
        # 如果是task_id等待
        if [[ -n "$u_inst_id" ]]; then
            local maybe_task; maybe_task=$(echo "$uir" | jq -r '.data.task_id // empty' 2>/dev/null)
            if [[ -n "$maybe_task" ]]; then
                local task_r; task_r=$(wait_task_complete "$SERVER_URL" "$maybe_task" "$u_token" 300 10)
                u_inst_id=$(echo "$task_r" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
            fi
        fi

        if [[ -n "$u_inst_id" ]]; then
            # ── 实例详情 ──
            local ud; ud=$(test_api "用户实例详情" "GET" "/api/v1/user/instances/${u_inst_id}" "200" "" "$group" "$u_token")
            # 验证IP和配置
            local u_ip; u_ip=$(echo "$ud" | jq -r '.data.ip // .data.ipv4 // .data.internal_ip // empty' 2>/dev/null)
            local u_cpu; u_cpu=$(echo "$ud" | jq -r '.data.cpu // empty' 2>/dev/null)
            TOTAL_TESTS=$((TOTAL_TESTS + 1))
            if [[ -n "$u_ip" || -n "$u_cpu" ]]; then
                PASSED_TESTS=$((PASSED_TESTS + 1))
                log_success "用户实例配置: IP=${u_ip} CPU=${u_cpu}"
                report_add_pass "用户实例配置验证" "GET" "/api/v1/user/instances/${u_inst_id}"
            else
                FAILED_TESTS=$((FAILED_TESTS + 1))
                report_add_fail "用户实例配置验证" "GET" "/api/v1/user/instances/${u_inst_id}" "" "有值" "空" "$ud"
            fi

            # ── 监控 ──
            test_api "用户实例监控" "GET" "/api/v1/user/instances/${u_inst_id}/monitoring" "200" "" "$group" "$u_token"
            test_api "用户实例资源监控" "GET" "/api/v1/user/instances/${u_inst_id}/monitoring/resources" "200" "" "$group" "$u_token"
            test_api "用户实例监控状态" "GET" "/api/v1/user/instances/${u_inst_id}/monitoring/status" "200" "" "$group" "$u_token"

            # ── Pmacct ──
            test_api "用户Pmacct概要" "GET" "/api/v1/user/instances/${u_inst_id}/pmacct/summary" "200" "" "$group" "$u_token"

            # ── 端口 ──
            test_api "用户实例端口" "GET" "/api/v1/user/instances/${u_inst_id}/ports" "200" "" "$group" "$u_token"

            # ── 实例操作 ──
            test_api "用户停止实例" "POST" "/api/v1/user/instances/action" "200" \
                "{\"instance_id\":${u_inst_id},\"action\":\"stop\"}" "$group" "$u_token"
            sleep 3
            test_api "用户启动实例" "POST" "/api/v1/user/instances/action" "200" \
                "{\"instance_id\":${u_inst_id},\"action\":\"start\"}" "$group" "$u_token"
            sleep 3
            test_api "用户重启实例" "POST" "/api/v1/user/instances/action" "200" \
                "{\"instance_id\":${u_inst_id},\"action\":\"restart\"}" "$group" "$u_token"
            sleep 3

            # ── 重置密码 ──
            local urp; urp=$(test_api "用户重置实例密码" "PUT" "/api/v1/user/instances/${u_inst_id}/reset-password" "200" \
                "{\"password\":\"UserNewPass123!\"}" "$group" "$u_token")
            local urp_task; urp_task=$(echo "$urp" | jq -r '.data.task_id // empty' 2>/dev/null)
            if [[ -n "$urp_task" ]]; then
                sleep 5
                test_api "用户获取新密码" "GET" "/api/v1/user/instances/${u_inst_id}/password/${urp_task}" "200" "" "$group" "$u_token"
            fi

            # ── 流量 ──
            test_api "用户实例流量详情" "GET" "/api/v1/user/traffic/instance/${u_inst_id}" "200" "" "$group" "$u_token"
            test_api "用户实例流量历史" "GET" "/api/v1/user/instances/${u_inst_id}/traffic/history" "200" "" "$group" "$u_token"
            test_api "用户Pmacct数据" "GET" "/api/v1/user/traffic/pmacct/${u_inst_id}" "200" "" "$group" "$u_token"

            # ── 签到 ──
            test_api "生成签到码" "POST" "/api/v1/user/checkin/code/${u_inst_id}" "200" "" "$group" "$u_token"
            test_api "签到记录" "GET" "/api/v1/user/checkin/records" "200" "" "$group" "$u_token"

            # ── 用户任务 ──
            test_api "用户任务列表" "GET" "/api/v1/user/tasks" "200" "" "$group" "$u_token"

            # ── 清理: Admin删除用户实例 ──
            test_api "Admin删除用户实例" "DELETE" "/api/v1/admin/instances/${u_inst_id}" "200" "" "$group" "$ADMIN_TOKEN"
        fi
    fi

    # ── KYC ──
    test_api "获取用户KYC" "GET" "/api/v1/user/kyc" "200" "" "$group" "$u_token"

    # ── 虚拟化Provider(resources路由) ──
    test_api "虚拟化Provider列表" "GET" "/api/v1/resources/virtualization/providers" "200" "" "$group" "$u_token"

    # ── Dashboard统计 ──
    test_api "Dashboard统计(用户)" "GET" "/api/v1/dashboard/stats" "200" "" "$group" "$u_token"
}
