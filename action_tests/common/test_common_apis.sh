#!/bin/bash
# 公共 API 测试模块 - 不依赖特定虚拟化环境
# 包含: 公共接口、认证、系统初始化等测试

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ============================================================
# 公共接口测试
# ============================================================
test_public_apis() {
    local group="public"
    report_add_section "公共接口测试"

    # 健康检查
    test_api "健康检查" "GET" "/health" "200" "" "$group" || chain_break "$group" "健康检查失败"
    test_api "API健康检查" "GET" "/api/health" "200" "" "$group" || true

    # 初始化状态检查
    test_api "检查初始化状态" "GET" "/api/v1/public/init/check" "200" "" "$group" || true

    # 版本信息
    test_api "获取版本信息" "GET" "/api/v1/public/version" "200" "" "$group" || true

    # 构建信息
    test_api "获取构建信息" "GET" "/api/v1/public/build-info" "200" "" "$group" || true

    # 推荐数据库类型
    test_api "获取推荐数据库类型" "GET" "/api/v1/public/recommended-db-type" "200" "" "$group" || true

    # 注册配置
    test_api "获取注册配置" "GET" "/api/v1/public/register-config" "200" "" "$group" || true

    # 系统配置（公共）
    test_api "获取系统配置" "GET" "/api/v1/public/system-config" "200" "" "$group" || true
}

# ============================================================
# 系统初始化测试
# ============================================================
test_system_init() {
    local group="init"
    report_add_section "系统初始化测试"

    # 初始化前检查
    local check_response
    check_response=$(curl -s --max-time 10 "${SERVER_URL}/api/v1/public/init/check" 2>/dev/null)
    local initialized
    initialized=$(echo "$check_response" | jq -r '.data.initialized // false' 2>/dev/null)

    if [[ "$initialized" == "true" ]]; then
        log_info "系统已初始化，跳过初始化测试"
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        PASSED_TESTS=$((PASSED_TESTS + 1))
        report_add_pass "系统已初始化" "GET" "/api/v1/public/init/check"
        return 0
    fi

    # 执行系统初始化
    local init_body
    init_body=$(init_system "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS" "mysql")
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    local init_code
    init_code=$(echo "$init_body" | jq -r '.code // empty' 2>/dev/null)
    if [[ "$init_code" == "200" ]] || [[ "$init_code" == "0" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        report_add_pass "系统初始化" "POST" "/api/v1/public/init"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "系统初始化" "POST" "/api/v1/public/init" "" "200" "$init_code" "$init_body"
        chain_break "$group" "系统初始化失败"
        return 1
    fi

    # 初始化后验证
    sleep 5
    test_api "初始化后检查" "GET" "/api/v1/public/init/check" "200" "" "$group" || true
}

# ============================================================
# 认证 API 测试
# ============================================================
test_auth_apis() {
    local group="auth"
    report_add_section "认证 API 测试"

    # 获取验证码
    test_api "获取验证码" "GET" "/api/v1/auth/captcha" "200" "" "$group" || true

    # 管理员登录
    local login_body
    login_body=$(test_api "管理员登录" "POST" "/api/v1/auth/login" "200" \
        "{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\"}" "$group")
    if [[ $? -ne 0 ]]; then
        chain_break "$group" "管理员登录失败"
        return 1
    fi
    ADMIN_TOKEN=$(echo "$login_body" | jq -r '.data.token // empty' 2>/dev/null)
    if [[ -z "$ADMIN_TOKEN" ]]; then
        chain_break "$group" "无法获取管理员 Token"
        return 1
    fi

    # 无效登录测试
    test_api "空用户名登录" "POST" "/api/v1/auth/login" "400" \
        '{"username":"","password":"test"}' "$group" || true

    test_api "错误密码登录" "POST" "/api/v1/auth/login" "401" \
        '{"username":"admin","password":"wrongpassword"}' "$group" || true

    # 注册测试用户
    local reg_body
    reg_body=$(test_api "注册测试用户" "POST" "/api/v1/auth/register" "200" \
        "{\"username\":\"testuser_$(date +%s)\",\"password\":\"TestPass123!@#\"}" "$group") || true

    # 密码强度验证
    test_api "弱密码注册" "POST" "/api/v1/auth/register" "400" \
        '{"username":"weakpwduser","password":"123"}' "$group" || true

    # 重复用户名注册
    test_api "重复用户名注册" "POST" "/api/v1/auth/register" "409" \
        "{\"username\":\"${ADMIN_USER}\",\"password\":\"TestPass123!@#\"}" "$group" || true

    # 忘记密码（不发送邮件只验证接口）
    test_api "忘记密码接口" "POST" "/api/v1/auth/forgot-password" "400" \
        '{"email":"nonexistent@test.com"}' "$group" || true
}

# ============================================================
# 管理员仪表盘和监控测试
# ============================================================
test_admin_dashboard() {
    local group="admin_dashboard"
    report_add_section "管理员仪表盘测试"

    test_api "管理员仪表盘" "GET" "/api/v1/admin/dashboard" "200" "" "$group" || true
    test_api "任务统计" "GET" "/api/v1/admin/tasks/stats" "200" "" "$group" || true
    test_api "任务总计统计" "GET" "/api/v1/admin/tasks/overall-stats" "200" "" "$group" || true
    test_api "管理员任务列表" "GET" "/api/v1/admin/tasks" "200" "" "$group" || true
}

# ============================================================
# 系统配置测试
# ============================================================
test_admin_config() {
    local group="admin_config"
    report_add_section "系统配置测试"

    # 获取统一配置
    local config_body
    config_body=$(test_api "获取统一配置" "GET" "/api/v1/admin/config" "200" "" "$group") || {
        chain_break "$group" "获取配置失败"
        return 1
    }

    # 获取认证配置
    test_api "获取配置(v1/config)" "GET" "/api/v1/config" "200" "" "$group" || true
}

# ============================================================
# 用户管理测试
# ============================================================
test_admin_users() {
    local group="admin_users"
    report_add_section "用户管理测试"

    # 用户列表
    test_api "用户列表" "GET" "/api/v1/admin/users" "200" "" "$group" || {
        chain_break "$group" "获取用户列表失败"
        return 1
    }

    # 创建用户
    local ts
    ts=$(date +%s)
    local create_body
    create_body=$(test_api "创建用户" "POST" "/api/v1/admin/users" "200" \
        "{\"username\":\"test_user_${ts}\",\"password\":\"Test123!@#\",\"email\":\"test_${ts}@test.com\"}" "$group") || {
        chain_break "$group" "创建用户失败"
        return 1
    }
    local test_user_id
    test_user_id=$(echo "$create_body" | jq -r '.data.id // empty' 2>/dev/null)

    if [[ -n "$test_user_id" ]]; then
        # 更新用户
        test_api "更新用户" "PUT" "/api/v1/admin/users/${test_user_id}" "200" \
            "{\"nickname\":\"测试用户\"}" "$group" || true

        # 更新用户状态
        test_api "禁用用户" "PUT" "/api/v1/admin/users/${test_user_id}/status" "200" \
            '{"status":0}' "$group" || true

        test_api "启用用户" "PUT" "/api/v1/admin/users/${test_user_id}/status" "200" \
            '{"status":1}' "$group" || true

        # 更新用户等级
        test_api "更新用户等级" "PUT" "/api/v1/admin/users/${test_user_id}/level" "200" \
            '{"level":2}' "$group" || true

        # 重置用户密码
        test_api "重置用户密码" "PUT" "/api/v1/admin/users/${test_user_id}/reset-password" "200" "" "$group" || true

        # 获取用户配额
        test_api "获取用户配额" "GET" "/api/v1/admin/quota/users/${test_user_id}" "200" "" "$group" || true

        # 删除用户
        test_api "删除用户" "DELETE" "/api/v1/admin/users/${test_user_id}" "200" "" "$group" || true
    fi

    # 批量操作测试
    test_api "批量更新用户等级(无用户)" "PUT" "/api/v1/admin/users/batch-level" "400" \
        '{"user_ids":[],"level":1}' "$group" || true
}

# ============================================================
# 公告管理测试
# ============================================================
test_admin_announcements() {
    local group="admin_announcements"
    report_add_section "公告管理测试"

    # 公共公告接口
    test_api "获取公共公告" "GET" "/api/v1/public/announcements" "200" "" "$group" || true

    # 创建公告
    local ann_body
    ann_body=$(test_api "创建公告" "POST" "/api/v1/admin/announcements" "200" \
        '{"title":"测试公告","content":"这是一条测试公告","type":"info","status":1}' "$group") || {
        chain_break "$group" "创建公告失败"
        return 1
    }
    local ann_id
    ann_id=$(echo "$ann_body" | jq -r '.data.id // empty' 2>/dev/null)

    if [[ -n "$ann_id" ]]; then
        # 获取公告列表
        test_api "获取公告列表" "GET" "/api/v1/admin/announcements" "200" "" "$group" || true

        # 更新公告
        test_api "更新公告" "PUT" "/api/v1/admin/announcements/${ann_id}" "200" \
            '{"title":"更新的公告","content":"更新内容"}' "$group" || true

        # 删除公告
        test_api "删除公告" "DELETE" "/api/v1/admin/announcements/${ann_id}" "200" "" "$group" || true
    fi
}

# ============================================================
# 邀请码测试
# ============================================================
test_admin_invite_codes() {
    local group="admin_invite"
    report_add_section "邀请码测试"

    # 获取邀请码列表
    test_api "邀请码列表" "GET" "/api/v1/admin/invite-codes" "200" "" "$group" || true

    # 生成邀请码
    local invite_body
    invite_body=$(test_api "生成邀请码" "POST" "/api/v1/admin/invite-codes/generate" "200" \
        '{"count":2,"max_uses":1,"expires_hours":24}' "$group") || true

    # 导出邀请码
    test_api "导出邀请码" "GET" "/api/v1/admin/invite-codes/export" "200" "" "$group" || true
}

# ============================================================
# 系统镜像测试
# ============================================================
test_admin_system_images() {
    local group="admin_images"
    report_add_section "系统镜像管理测试"

    # 获取镜像列表
    test_api "获取系统镜像列表" "GET" "/api/v1/admin/system-images" "200" "" "$group" || true

    # 公共可用镜像
    test_api "获取公共可用镜像" "GET" "/api/v1/public/system-images/available" "200" "" "$group" || true

    # 创建镜像
    local img_body
    img_body=$(test_api "创建系统镜像" "POST" "/api/v1/admin/system-images" "200" \
        '{"name":"test-image","os_type":"linux","arch":"amd64","provider_type":"docker","download_url":"","status":1}' "$group") || true
    local img_id
    img_id=$(echo "${img_body:-}" | jq -r '.data.id // empty' 2>/dev/null)

    if [[ -n "$img_id" ]]; then
        test_api "更新系统镜像" "PUT" "/api/v1/admin/system-images/${img_id}" "200" \
            '{"name":"test-image-updated"}' "$group" || true
        test_api "删除系统镜像" "DELETE" "/api/v1/admin/system-images/${img_id}" "200" "" "$group" || true
    fi
}

# ============================================================
# 兑换码测试
# ============================================================
test_admin_redemption_codes() {
    local group="admin_redeem"
    report_add_section "兑换码管理测试"

    test_api "兑换码列表" "GET" "/api/v1/admin/redemption-codes" "200" "" "$group" || true

    local redeem_body
    redeem_body=$(test_api "批量创建兑换码" "POST" "/api/v1/admin/redemption-codes/batch-create" "200" \
        '{"count":2,"type":"traffic","value":100,"max_uses":1}' "$group") || true
}

# ============================================================
# 用户接口测试（需要用户 Token）
# ============================================================
test_user_apis() {
    local group="user"
    report_add_section "用户接口测试"

    # 保存管理员 Token，切换到用户视角
    local saved_token="$ADMIN_TOKEN"

    # 用管理员 Token 测试用户端点
    test_api "用户个人信息" "GET" "/api/v1/user/profile" "200" "" "$group" || true
    test_api "用户仪表盘" "GET" "/api/v1/user/dashboard" "200" "" "$group" || true
    test_api "用户限额" "GET" "/api/v1/user/limits" "200" "" "$group" || true
    test_api "用户实例列表" "GET" "/api/v1/user/instances" "200" "" "$group" || true
    test_api "用户可用资源" "GET" "/api/v1/user/resources/available" "200" "" "$group" || true
    test_api "用户可用Provider" "GET" "/api/v1/user/providers/available" "200" "" "$group" || true
    test_api "用户系统镜像" "GET" "/api/v1/user/images" "200" "" "$group" || true
    test_api "用户过滤镜像" "GET" "/api/v1/user/images/filtered" "200" "" "$group" || true
    test_api "用户实例类型权限" "GET" "/api/v1/user/instance-type-permissions" "200" "" "$group" || true
    test_api "用户实例配置" "GET" "/api/v1/user/instance-config" "200" "" "$group" || true
    test_api "用户任务列表" "GET" "/api/v1/user/tasks" "200" "" "$group" || true
    test_api "用户端口映射" "GET" "/api/v1/user/port-mappings" "200" "" "$group" || true

    # 流量接口
    test_api "用户流量概览" "GET" "/api/v1/user/traffic/overview" "200" "" "$group" || true
    test_api "用户流量限制状态" "GET" "/api/v1/user/traffic/limit-status" "200" "" "$group" || true
    test_api "用户流量历史" "GET" "/api/v1/user/traffic/history" "200" "" "$group" || true
    test_api "用户所有实例流量" "GET" "/api/v1/user/traffic/instances" "200" "" "$group" || true

    # 仪表盘统计
    test_api "仪表盘统计" "GET" "/api/v1/public/stats" "200" "" "$group" || true
    test_api "仪表盘统计(user)" "GET" "/api/v1/dashboard/stats" "200" "" "$group" || true

    # KYC (无法实际测试，但可以测试接口可达性)
    test_api "获取KYC状态" "GET" "/api/v1/user/kyc" "200" "" "$group" || true

    # 签到记录
    test_api "获取签到记录" "GET" "/api/v1/user/checkin/records" "200" "" "$group" || true

    # 域名列表
    test_api "用户域名列表" "GET" "/api/v1/user/domains" "200" "" "$group" || true

    # 修改用户密码
    test_api "用户修改密码(无效)" "PUT" "/api/v1/user/reset-password" "400" \
        '{"old_password":"","new_password":""}' "$group" || true

    ADMIN_TOKEN="$saved_token"
}

# ============================================================
# 权限验证测试
# ============================================================
test_permission_validation() {
    local group="permission"
    report_add_section "权限验证测试"

    # 未认证访问管理接口
    local saved_token="$ADMIN_TOKEN"
    ADMIN_TOKEN=""
    test_api "未认证访问管理员仪表盘" "GET" "/api/v1/admin/dashboard" "401" "" "$group" || true
    test_api "未认证访问用户信息" "GET" "/api/v1/user/profile" "401" "" "$group" || true
    ADMIN_TOKEN="$saved_token"

    # 无效 Token
    local saved_token2="$ADMIN_TOKEN"
    ADMIN_TOKEN="invalid_token_12345"
    test_api "无效Token访问" "GET" "/api/v1/admin/dashboard" "401" "" "$group" || true
    ADMIN_TOKEN="$saved_token2"
}

# ============================================================
# 流量管理测试
# ============================================================
test_admin_traffic() {
    local group="admin_traffic"
    report_add_section "流量管理测试"

    test_api "系统流量概览" "GET" "/api/v1/admin/traffic/overview" "200" "" "$group" || true
    test_api "用户流量排行" "GET" "/api/v1/admin/traffic/users/rank" "200" "" "$group" || true
}

# ============================================================
# OAuth2 测试
# ============================================================
test_oauth2_apis() {
    local group="oauth2"
    report_add_section "OAuth2 测试"

    test_api "获取启用的OAuth2提供商" "GET" "/api/v1/public/oauth2/providers" "200" "" "$group" || true
    test_api "获取OAuth2提供商列表" "GET" "/api/v1/oauth2/providers" "200" "" "$group" || true
    test_api "获取OAuth2预设列表" "GET" "/api/v1/oauth2/presets" "200" "" "$group" || true
}

# ============================================================
# 实例类型权限测试
# ============================================================
test_instance_type_permissions() {
    local group="instance_type_perm"
    report_add_section "实例类型权限测试"

    test_api "获取实例类型权限" "GET" "/api/v1/admin/instance-type-permissions" "200" "" "$group" || true
}

# ============================================================
# 输入验证和边界测试
# ============================================================
test_input_validation() {
    local group="validation"
    report_add_section "输入验证测试"

    # 空请求体
    test_api "空体登录" "POST" "/api/v1/auth/login" "400" '{}' "$group" || true

    # 超长输入
    local long_str
    long_str=$(python3 -c "print('A' * 10000)" 2>/dev/null || printf '%0.sA' {1..10000})
    test_api "超长用户名" "POST" "/api/v1/auth/login" "400" \
        "{\"username\":\"${long_str}\",\"password\":\"test\"}" "$group" || true

    # 特殊字符
    test_api "特殊字符用户名" "POST" "/api/v1/auth/login" "400" \
        '{"username":"<script>alert(1)</script>","password":"test"}' "$group" || true

    # SQL 注入尝试
    test_api "SQL注入尝试" "POST" "/api/v1/auth/login" "400" \
        '{"username":"admin'\'' OR 1=1--","password":"test"}' "$group" || true
}

# ============================================================
# 运行所有公共测试
# ============================================================
run_all_common_tests() {
    test_public_apis
    test_system_init
    test_auth_apis
    test_permission_validation
    test_input_validation
    test_admin_dashboard
    test_admin_config
    test_admin_users
    test_admin_announcements
    test_admin_invite_codes
    test_admin_system_images
    test_admin_redemption_codes
    test_admin_traffic
    test_oauth2_apis
    test_instance_type_permissions
    test_user_apis
}
