#!/bin/bash
# 模块 01: 系统初始化与健康检查
# 依赖: 无

run_module_01() {
    report_add_section "01 - 系统初始化与健康检查"

    # ── 健康检查 ──
    test_api_noauth "健康检查-root" "GET" "/health" "200" "" "init"
    test_api_noauth "健康检查-api" "GET" "/api/health" "200" "" "init"
    test_api_noauth "Ping" "GET" "/api/ping" "200" "" "init"

    # ── 初始化状态 ──
    test_api_noauth "检查初始化状态" "GET" "/api/v1/public/init/check" "200" "" "init"

    # ── 推荐数据库类型 ──
    test_api_noauth "获取推荐DB类型" "GET" "/api/v1/public/recommended-db-type" "200" "" "init"

    # ── 系统初始化 ──
    local init_result
    init_result=$(init_system "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS" "sqlite")
    local init_code; init_code=$(echo "$init_result" | jq -r '.code // empty' 2>/dev/null)
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ "$init_code" == "200" || "$init_code" == "0" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        log_success "系统初始化(sqlite)"
        report_add_pass "系统初始化" "POST" "/api/v1/public/init"
    else
        # 可能已初始化
        local msg; msg=$(echo "$init_result" | jq -r '.message // empty' 2>/dev/null)
        if echo "$msg" | grep -qi "already\|已"; then
            PASSED_TESTS=$((PASSED_TESTS + 1))
            log_success "系统已初始化(跳过)"
            report_add_pass "系统已初始化" "POST" "/api/v1/public/init"
        else
            FAILED_TESTS=$((FAILED_TESTS + 1))
            log_error "系统初始化失败: ${msg}"
            report_add_fail "系统初始化" "POST" "/api/v1/public/init" "" "200" "$init_code" "$init_result"
            chain_break "init" "初始化失败"
            return 1
        fi
    fi

    # ── 管理员登录 ──
    ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS")
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ -n "$ADMIN_TOKEN" ]]; then
        PASSED_TESTS=$((PASSED_TESTS + 1))
        report_add_pass "管理员登录" "POST" "/api/v1/auth/login"
    else
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "管理员登录" "POST" "/api/v1/auth/login" "" "token" "empty" ""
        chain_break "init" "管理员登录失败"
        return 1
    fi

    # ── 初始化后公开接口 ──
    test_api_noauth "获取版本" "GET" "/api/v1/public/version" "200" "" "init"
    test_api_noauth "获取构建信息" "GET" "/api/v1/public/build-info" "200" "" "init"
    test_api_noauth "获取公告(公开)" "GET" "/api/v1/public/announcements" "200" "" "init"
    test_api_noauth "获取公开统计" "GET" "/api/v1/public/stats" "200" "" "init"
    test_api_noauth "获取注册配置" "GET" "/api/v1/public/register-config" "200" "" "init"
    test_api_noauth "获取公开系统配置" "GET" "/api/v1/public/system-config" "200" "" "init"
    test_api_noauth "获取可用系统镜像" "GET" "/api/v1/public/system-images/available" "200" "" "init"
    test_api_noauth "获取OAuth2提供者" "GET" "/api/v1/public/oauth2/providers" "200" "" "init"

    # ── Swagger ──
    test_api_noauth "Swagger文档" "GET" "/swagger/index.html" "200" "" "init"
}
