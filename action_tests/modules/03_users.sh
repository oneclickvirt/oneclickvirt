#!/bin/bash
# 模块 03: 用户管理 (Super Admin)
# 依赖: 01_init (ADMIN_TOKEN)

run_module_03() {
    report_add_section "03 - 用户管理"
    local group="users"

    # ── 用户列表 ──
    test_api "获取用户列表" "GET" "/api/v1/admin/users?page=1&pageSize=10" "200" "" "$group"

    # ── 创建用户 ──
    local user1_data="{\"username\":\"test_user_01\",\"password\":\"Test123!@#\",\"level\":1}"
    local r1; r1=$(test_api "创建用户01" "POST" "/api/v1/admin/users" "200" "$user1_data" "$group")
    local user1_id; user1_id=$(echo "$r1" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    [[ -z "$user1_id" ]] && chain_break "$group" "创建用户失败"

    local user2_data="{\"username\":\"test_user_02\",\"password\":\"Test123!@#\",\"level\":1}"
    local r2; r2=$(test_api "创建用户02" "POST" "/api/v1/admin/users" "200" "$user2_data" "$group")
    local user2_id; user2_id=$(echo "$r2" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    local user3_data="{\"username\":\"test_user_03\",\"password\":\"Test123!@#\",\"level\":1}"
    local r3; r3=$(test_api "创建用户03" "POST" "/api/v1/admin/users" "200" "$user3_data" "$group")
    local user3_id; user3_id=$(echo "$r3" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)

    # ── 重复创建 ──
    test_api "重复用户名创建" "POST" "/api/v1/admin/users" "400" "$user1_data" "$group"

    # ── 编辑用户 ──
    if [[ -n "$user1_id" ]]; then
        test_api "编辑用户01" "PUT" "/api/v1/admin/users/${user1_id}" "200" \
            "{\"username\":\"test_user_01_edited\",\"level\":1}" "$group"
    fi

    # ── 改等级 ──
    if [[ -n "$user1_id" ]]; then
        test_api "用户01改等级为2" "PUT" "/api/v1/admin/users/${user1_id}/level" "200" \
            "{\"level\":2}" "$group"
    fi

    # ── 重置密码 ──
    if [[ -n "$user1_id" ]]; then
        test_api "重置用户01密码" "PUT" "/api/v1/admin/users/${user1_id}/reset-password" "200" \
            "{\"password\":\"NewPass123!@#\"}" "$group"
    fi

    # ── 禁用/启用 ──
    if [[ -n "$user1_id" ]]; then
        test_api "禁用用户01" "PUT" "/api/v1/admin/users/${user1_id}/status" "200" \
            "{\"status\":\"disabled\"}" "$group"
        test_api "启用用户01" "PUT" "/api/v1/admin/users/${user1_id}/status" "200" \
            "{\"status\":\"active\"}" "$group"
    fi

    # ── 批量改等级 ──
    if [[ -n "$user2_id" && -n "$user3_id" ]]; then
        test_api "批量改等级" "PUT" "/api/v1/admin/users/batch-level" "200" \
            "{\"ids\":[${user2_id},${user3_id}],\"level\":3}" "$group"
    fi

    # ── 批量禁用 ──
    if [[ -n "$user2_id" && -n "$user3_id" ]]; then
        test_api "批量禁用" "PUT" "/api/v1/admin/users/batch-status" "200" \
            "{\"ids\":[${user2_id},${user3_id}],\"status\":\"disabled\"}" "$group"
    fi

    # ── 单个删除 ──
    if [[ -n "$user1_id" ]]; then
        test_api "删除用户01" "DELETE" "/api/v1/admin/users/${user1_id}" "200" "" "$group"
    fi

    # ── 批量删除 ──
    if [[ -n "$user2_id" && -n "$user3_id" ]]; then
        test_api "批量删除用户" "POST" "/api/v1/admin/users/batch-delete" "200" \
            "{\"ids\":[${user2_id},${user3_id}]}" "$group"
    fi

    # ── 创建普通管理员(用于后续隔离测试) ──
    local na_data="{\"username\":\"${NORMAL_ADMIN_USER}\",\"password\":\"${NORMAL_ADMIN_PASS}\",\"level\":2}"
    local na_r; na_r=$(test_api "创建普通管理员" "POST" "/api/v1/admin/users" "200" "$na_data" "$group")
    local na_id; na_id=$(echo "$na_r" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    if [[ -n "$na_id" ]]; then
        NORMAL_ADMIN_TOKEN=$(do_login "$SERVER_URL" "$NORMAL_ADMIN_USER" "$NORMAL_ADMIN_PASS")
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        if [[ -n "$NORMAL_ADMIN_TOKEN" ]]; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "普通管理员登录成功"
            report_add_pass "普通管理员登录" "POST" "/api/v1/auth/login"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_error "普通管理员登录失败"
            report_add_fail "普通管理员登录" "POST" "/api/v1/auth/login" "" "token" "empty" ""
        fi
    fi

    # ── 用户过期设置 ──
    local temp_user="{\"username\":\"test_user_expiry\",\"password\":\"Test123!@#\",\"level\":1}"
    local er; er=$(test_api "创建过期测试用户" "POST" "/api/v1/admin/users" "200" "$temp_user" "$group")
    local eid; eid=$(echo "$er" | jq -r '.data.id // .data.ID // empty' 2>/dev/null)
    if [[ -n "$eid" ]]; then
        local exp_time; exp_time=$(date -u -d "+1 day" '+%Y-%m-%dT%H:%M:%SZ' 2>/dev/null || date -u -v+1d '+%Y-%m-%dT%H:%M:%SZ')
        test_api "设置用户过期" "POST" "/api/v1/admin/users/set-expiry" "200" \
            "{\"user_id\":${eid},\"expires_at\":\"${exp_time}\"}" "$group"
        test_api "删除过期测试用户" "DELETE" "/api/v1/admin/users/${eid}" "200" "" "$group"
    fi

    # ── 以用户身份登录 ──
    if [[ -n "$na_id" ]]; then
        test_api "以用户身份登录" "POST" "/api/v1/admin/users/${na_id}/login-as" "200" "" "$group"
    fi

    # ── 配额查询 ──
    if [[ -n "$na_id" ]]; then
        test_api "查询用户配额" "GET" "/api/v1/admin/quota/users/${na_id}" "200" "" "$group"
    fi

    # ── 实例类型权限 ──
    test_api "获取实例类型权限" "GET" "/api/v1/admin/instance-type-permissions" "200" "" "$group"
    test_api "更新实例类型权限" "PUT" "/api/v1/admin/instance-type-permissions" "200" \
        "{\"container\":true,\"vm\":true}" "$group"
}
