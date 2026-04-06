#!/bin/bash
# 模块 10: 实例生命周期 (Admin + User)
# 依赖: 01_init (ADMIN_TOKEN), 09_providers (PROVIDER_ID)

run_module_10() {
    report_add_section "10 - 实例生命周期"
    local group="instances"

    if [[ -z "$PROVIDER_ID" ]]; then
        chain_break "$group" "无Provider,跳过实例测试"
        return 1
    fi

    # ── Admin 实例列表 ──
    test_api "Admin实例列表" "GET" "/api/v1/admin/instances?page=1&pageSize=10" "200" "" "$group"

    # ── Admin 创建实例 ──
    local inst_data="{\"provider_id\":${PROVIDER_ID},\"instance_type\":\"container\",\"image\":\"debian:12\",\"cpu\":1,\"memory\":256,\"disk\":5,\"network_type\":\"nat_ipv4\"}"
    local ir; ir=$(test_api "Admin创建实例" "POST" "/api/v1/admin/instances" "200" "$inst_data" "$group")
    local inst_id; inst_id=$(echo "$ir" | jq -r '.data.id // .data.ID // .data.task_id // empty' 2>/dev/null)

    # 如果返回的是task_id，等待任务完成后获取实例ID
    if [[ -n "$inst_id" ]]; then
        local maybe_task; maybe_task=$(echo "$ir" | jq -r '.data.task_id // empty' 2>/dev/null)
        if [[ -n "$maybe_task" ]]; then
            log_info "创建实例任务: ${maybe_task}"
            local task_r; task_r=$(wait_task_complete "$SERVER_URL" "$maybe_task" "$ADMIN_TOKEN" 300 10)
            inst_id=$(echo "$task_r" | jq -r '.data.instance_id // .data.result.id // empty' 2>/dev/null)
        fi
    fi
    [[ -z "$inst_id" ]] && { chain_break "$group" "创建实例失败"; return 1; }
    log_info "实例 ID: ${inst_id}"

    # ── 实例详情(Admin) ──
    local detail; detail=$(test_api "Admin实例详情" "GET" "/api/v1/admin/instances/${inst_id}" "200" "" "$group")

    # ── 验证实例配置+IP ──
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    local d_cpu; d_cpu=$(echo "$detail" | jq -r '.data.cpu // empty' 2>/dev/null)
    local d_mem; d_mem=$(echo "$detail" | jq -r '.data.memory // empty' 2>/dev/null)
    local d_ip; d_ip=$(echo "$detail" | jq -r '.data.ip // .data.ipv4 // .data.internal_ip // empty' 2>/dev/null)
    if [[ -n "$d_cpu" || -n "$d_mem" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "实例配置验证: CPU=${d_cpu} MEM=${d_mem} IP=${d_ip}"
        report_add_pass "实例配置验证" "GET" "/api/v1/admin/instances/${inst_id}"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "实例配置为空"
        report_add_fail "实例配置验证" "GET" "/api/v1/admin/instances/${inst_id}" "" "有值" "空" "$detail"
    fi

    # ── 实例操作 ──
    test_api "停止实例" "POST" "/api/v1/admin/instances/${inst_id}/action" "200" \
        "{\"action\":\"stop\"}" "$group"
    sleep 5

    test_api "启动实例" "POST" "/api/v1/admin/instances/${inst_id}/action" "200" \
        "{\"action\":\"start\"}" "$group"
    sleep 5

    test_api "重启实例" "POST" "/api/v1/admin/instances/${inst_id}/action" "200" \
        "{\"action\":\"restart\"}" "$group"
    sleep 5

    # ── 重置密码 ──
    local rp; rp=$(test_api "重置实例密码" "PUT" "/api/v1/admin/instances/${inst_id}/reset-password" "200" \
        "{\"password\":\"NewInstPass123!\"}" "$group")
    local rp_task; rp_task=$(echo "$rp" | jq -r '.data.task_id // empty' 2>/dev/null)
    if [[ -n "$rp_task" ]]; then
        sleep 5
        test_api "获取新密码" "GET" "/api/v1/admin/instances/${inst_id}/password/${rp_task}" "200" "" "$group"
    fi

    # ── 编辑实例 ──
    test_api "编辑实例" "PUT" "/api/v1/admin/instances/${inst_id}" "200" \
        "{\"name\":\"ci-edited-instance\"}" "$group"

    # ── 端口映射 ──
    test_api "获取实例端口映射" "GET" "/api/v1/admin/instances/${inst_id}/port-mappings" "200" "" "$group"

    # ── 实例资源监控 ──
    test_api "实例资源监控(Admin)" "GET" "/api/v1/admin/instances/${inst_id}/monitoring/resources" "200" "" "$group"

    # ── 冻结/解冻实例 ──
    test_api "冻结实例" "POST" "/api/v1/admin/instances/freeze" "200" \
        "{\"instance_id\":${inst_id}}" "$group"
    sleep 2
    test_api "解冻实例" "POST" "/api/v1/admin/instances/unfreeze" "200" \
        "{\"instance_id\":${inst_id}}" "$group"

    # ── 实例过期 ──
    local inst_exp; inst_exp=$(date -u -d "+2 days" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v+2d '+%Y-%m-%dT%H:%M:%SZ')
    test_api "设置实例过期" "POST" "/api/v1/admin/instances/set-expiry" "200" \
        "{\"instance_id\":${inst_id},\"expires_at\":\"${inst_exp}\"}" "$group"

    # ── 创建第二个实例用于转移测试 ──
    if [[ -n "$USER_TOKEN" && "$USER_TOKEN" != "$ADMIN_TOKEN" ]]; then
        # 转移实例到用户
        local user_info; user_info=$(curl -s --max-time 30 \
            -H "Authorization: Bearer ${USER_TOKEN}" "${SERVER_URL}/api/v1/user/profile" 2>/dev/null)
        local target_uid; target_uid=$(echo "$user_info" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
        if [[ -n "$target_uid" ]]; then
            test_api "转移实例" "POST" "/api/v1/admin/instances/transfer" "200" \
                "{\"instance_id\":${inst_id},\"target_user_id\":${target_uid}}" "$group"
            sleep 2
            # 用户侧验证
            test_api "用户查看转移后实例" "GET" "/api/v1/user/instances" "200" "" "$group" "$USER_TOKEN"
        fi
    fi

    # ── 重置系统(如果支持) ──
    test_api "重置实例系统" "POST" "/api/v1/admin/instances/${inst_id}/action" "200" \
        "{\"action\":\"rebuild\",\"image\":\"debian:12\"}" "$group"
    sleep 10

    # ── 任务管理 ──
    test_api "Admin任务列表" "GET" "/api/v1/admin/tasks?page=1&pageSize=10" "200" "" "$group"
    test_api "任务统计" "GET" "/api/v1/admin/tasks/stats" "200" "" "$group"
    test_api "任务总统计" "GET" "/api/v1/admin/tasks/overall-stats" "200" "" "$group"

    # ── 删除实例 ──
    test_api "删除实例" "DELETE" "/api/v1/admin/instances/${inst_id}" "200" "" "$group"
}
