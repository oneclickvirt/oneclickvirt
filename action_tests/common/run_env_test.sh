#!/bin/bash
# 环境测试模板 - 每种虚拟化环境的测试入口
# 用法: ENV_TYPE=docker ./run_env_test.sh
#
# 流程:
# 1. 创建 AliceInit 节点
# 2. 安装虚拟化环境
# 3. 部署主控（Docker）
# 4. 初始化主控
# 5. 添加节点为 Provider
# 6. 运行所有 API 测试
# 7. 清理节点
# 8. 生成报告

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMMON_DIR="${SCRIPT_DIR}/../common"

# 加载公共库
source "${COMMON_DIR}/test_utils.sh"
source "${COMMON_DIR}/aliceinit_api.sh"
source "${COMMON_DIR}/node_manager.sh"
source "${COMMON_DIR}/test_common_apis.sh"
source "${COMMON_DIR}/test_provider_apis.sh"

# ============================================================
# 配置
# ============================================================
ENV_TYPE="${ENV_TYPE:-docker}"
MASTER_PORT="${MASTER_PORT:-8888}"
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Admin123!@#}"
ADMIN_TOKEN=""
SERVER_URL=""
NODE_INSTANCE_ID=""
NODE_IP=""
NODE_PASSWORD=""
PROVIDER_ID=""
REPORT_DIR="${SCRIPT_DIR}"

# ============================================================
# 主流程
# ============================================================
main() {
    local env_upper
    env_upper=$(echo "$ENV_TYPE" | tr '[:lower:]' '[:upper:]')

    log_section "${env_upper} 虚拟化环境完整测试"
    report_init "${REPORT_DIR}/README.md" "${env_upper} 虚拟化环境"

    local exit_code=0
    local cleanup_ids=""

    # =================== 阶段 1: 创建主控节点 ===================
    log_section "阶段 1: 创建主控节点"

    # 主控节点始终使用 Debian
    log_info "创建主控节点 (Debian)..."
    local master_node_info
    master_node_info=$(create_test_node "docker" 8) || {
        log_error "创建主控节点失败"
        report_add_section "环境创建"
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "创建主控节点" "API" "AliceInit创建实例" "" "成功" "失败" "无法创建测试节点"
        report_finalize
        return 1
    }

    local master_instance_id
    master_instance_id=$(echo "$master_node_info" | jq -r '.instance_id // empty' 2>/dev/null)
    local master_ip
    master_ip=$(echo "$master_node_info" | jq -r '.ipv4 // empty' 2>/dev/null)
    local master_password
    master_password=$(echo "$master_node_info" | jq -r '.password // empty' 2>/dev/null)
    cleanup_ids="${master_instance_id}"

    log_success "主控节点: ID=${master_instance_id}, IP=${master_ip}"

    # =================== 阶段 2: 创建虚拟化节点 ===================
    log_section "阶段 2: 创建 ${env_upper} 虚拟化节点"

    local node_info
    node_info=$(create_test_node "$ENV_TYPE" 8) || {
        log_error "创建虚拟化节点失败"
        report_add_section "环境创建"
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "创建虚拟化节点" "API" "AliceInit创建实例" "" "成功" "失败" "无法创建测试节点"
        cleanup_all_nodes "$cleanup_ids"
        report_finalize
        return 1
    }

    NODE_INSTANCE_ID=$(echo "$node_info" | jq -r '.instance_id // empty' 2>/dev/null)
    NODE_IP=$(echo "$node_info" | jq -r '.ipv4 // empty' 2>/dev/null)
    NODE_PASSWORD=$(echo "$node_info" | jq -r '.password // empty' 2>/dev/null)
    cleanup_ids="${cleanup_ids},${NODE_INSTANCE_ID}"

    log_success "虚拟化节点: ID=${NODE_INSTANCE_ID}, IP=${NODE_IP}"

    # =================== 阶段 3: 安装虚拟化环境 ===================
    log_section "阶段 3: 安装 ${env_upper} 虚拟化环境"
    install_env "$NODE_INSTANCE_ID" "$ENV_TYPE" || {
        log_error "安装虚拟化环境失败"
        report_add_section "环境安装"
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "安装${env_upper}环境" "SSH" "环境安装" "" "成功" "失败" "安装脚本执行失败"
        cleanup_all_nodes "$cleanup_ids"
        report_finalize
        return 1
    }

    report_add_section "环境准备"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    PASSED_TESTS=$((PASSED_TESTS + 1))
    report_add_pass "安装${env_upper}环境" "SSH" "环境安装"

    # =================== 阶段 4: 部署主控 ===================
    log_section "阶段 4: 部署主控面板"
    deploy_master "$master_instance_id" "$MASTER_PORT" || {
        log_error "部署主控失败"
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "部署主控面板" "Docker" "主控部署" "" "成功" "失败" "Docker部署失败"
        cleanup_all_nodes "$cleanup_ids"
        report_finalize
        return 1
    }

    SERVER_URL="http://${master_ip}:${MASTER_PORT}/api"

    # 等待主控启动
    wait_server_ready "http://${master_ip}:${MASTER_PORT}" 300 || {
        log_error "主控启动超时"
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "等待主控启动" "HTTP" "健康检查" "" "200" "timeout" "主控在300秒内未就绪"
        cleanup_all_nodes "$cleanup_ids"
        report_finalize
        return 1
    }

    SERVER_URL="http://${master_ip}:${MASTER_PORT}"
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    PASSED_TESTS=$((PASSED_TESTS + 1))
    report_add_pass "主控面板启动" "HTTP" "/health"

    # =================== 阶段 5: 系统初始化和认证 ===================
    log_section "阶段 5: 系统初始化"

    # 等待数据库就绪
    wait_db_ready "$SERVER_URL" 120 || true

    # 系统初始化
    init_system "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS" "mysql" || true
    sleep 5

    # 管理员登录
    ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS") || {
        log_error "管理员登录失败，无法继续测试"
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_fail "管理员登录" "POST" "/api/v1/auth/login" "" "200" "失败" "管理员登录失败"
        cleanup_all_nodes "$cleanup_ids"
        report_finalize
        return 1
    }

    # =================== 阶段 6: 运行公共 API 测试 ===================
    log_section "阶段 6: 公共 API 测试"
    run_all_common_tests

    # =================== 阶段 7: 添加 Provider 节点 ===================
    log_section "阶段 7: 添加 ${env_upper} Provider 节点"

    local provider_response
    provider_response=$(add_provider "$SERVER_URL" "$ADMIN_TOKEN" \
        "test-${ENV_TYPE}" "$ENV_TYPE" "$NODE_IP" 22 "root" "$NODE_PASSWORD")

    PROVIDER_ID=$(echo "$provider_response" | jq -r '.data.id // empty' 2>/dev/null)
    if [[ -z "$PROVIDER_ID" ]]; then
        log_error "添加 Provider 失败: $(echo "$provider_response" | jq -r '.message // empty' 2>/dev/null)"
        TOTAL_TESTS=$((TOTAL_TESTS + 1))
        FAILED_TESTS=$((FAILED_TESTS + 1))
        report_add_section "Provider 管理"
        report_add_fail "添加Provider节点" "POST" "/api/v1/admin/providers" \
            "" "200" "失败" "$provider_response"
        cleanup_all_nodes "$cleanup_ids"
        report_finalize
        return 1
    fi

    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    PASSED_TESTS=$((PASSED_TESTS + 1))
    report_add_section "Provider 管理"
    report_add_pass "添加Provider节点" "POST" "/api/v1/admin/providers"

    log_success "Provider 添加成功: ID=${PROVIDER_ID}"

    # 等待 Provider 健康检查通过
    sleep 30

    # =================== 阶段 8: 运行 Provider 相关测试 ===================
    log_section "阶段 8: ${env_upper} Provider 功能测试"
    run_provider_tests "$PROVIDER_ID" "$ENV_TYPE"

    # =================== 阶段 9: 清理 ===================
    log_section "阶段 9: 清理"

    # 删除 Provider
    if [[ -n "$PROVIDER_ID" ]]; then
        delete_provider "$SERVER_URL" "$ADMIN_TOKEN" "$PROVIDER_ID" || true
    fi

    # 删除所有 AliceInit 节点
    cleanup_all_nodes "$cleanup_ids"

    # =================== 阶段 10: 生成报告 ===================
    log_section "阶段 10: 生成报告"
    report_finalize

    log_section "测试完成"
    if [[ $FAILED_TESTS -gt 0 ]]; then
        exit_code=1
    fi

    return $exit_code
}

# 执行
main "$@"
exit $?
