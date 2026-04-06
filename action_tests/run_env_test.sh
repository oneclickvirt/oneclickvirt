#!/bin/bash
# 环境集成测试编排器 - 创建节点、安装环境、部署主控、运行全模块测试
# 用法: bash run_env_test.sh <env_type> [modules]
# 示例: bash run_env_test.sh docker all
#        bash run_env_test.sh lxd 01-10
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMMON_DIR="${SCRIPT_DIR}/common"
REPORT_DIR="${SCRIPT_DIR}/reports"
mkdir -p "$REPORT_DIR"

source "${COMMON_DIR}/test_framework.sh"
source "${COMMON_DIR}/node_manager.sh"

export ENV_TYPE="${1:-docker}"
MODULES="${2:-all}"
NODE_HOURS="${NODE_HOURS:-8}"
MASTER_PORT="${MASTER_PORT:-8888}"
EXIT_CODE=0

log_section "环境集成测试: ${ENV_TYPE}"
log_info "模块: ${MODULES}"
log_info "节点时长: ${NODE_HOURS}h"

# ── 报告初始化 ──
report_init "${REPORT_DIR}/${ENV_TYPE}-report.md" "${ENV_TYPE}"

# ── 阶段1: 创建节点 ──
log_section "阶段1: 创建节点"

CREATED_IDS=""

# 创建主控节点
log_info "创建主控节点..."
MASTER_INFO=$(create_test_node "$ENV_TYPE" "$NODE_HOURS")
if [[ $? -ne 0 || -z "$MASTER_INFO" ]]; then
    log_error "创建主控节点失败"
    report_finalize
    exit 1
fi
MASTER_ID=$(echo "$MASTER_INFO" | jq -r '.instance_id')
MASTER_IP=$(echo "$MASTER_INFO" | jq -r '.ipv4')
MASTER_PW=$(echo "$MASTER_INFO" | jq -r '.password')
CREATED_IDS="${MASTER_ID}"
export NODE_IP="$MASTER_IP"
export NODE_PASSWORD="$MASTER_PW"
log_success "主控节点: ID=${MASTER_ID} IP=${MASTER_IP}"

# ── 阶段2: 安装虚拟化环境 ──
log_section "阶段2: 安装 ${ENV_TYPE} 环境"
install_env "$MASTER_ID" "$ENV_TYPE"

# ── 阶段3: 部署主控 ──
log_section "阶段3: 部署主控"
deploy_master "$MASTER_ID" "$MASTER_PORT"

export SERVER_URL="http://${MASTER_IP}:${MASTER_PORT}"
log_info "主控地址: ${SERVER_URL}"

# ── 阶段4: 等待服务就绪 ──
log_section "阶段4: 等待服务就绪"
wait_server_ready "$SERVER_URL" 300 10 || {
    log_error "主控服务启动超时"
    cleanup_all_nodes "$CREATED_IDS"
    report_finalize
    exit 1
}

# ── 阶段5: 初始化并登录 ──
log_section "阶段5: 系统初始化"
init_system "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS" "sqlite"
sleep 2
ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS")
if [[ -z "$ADMIN_TOKEN" ]]; then
    log_error "管理员登录失败"
    cleanup_all_nodes "$CREATED_IDS"
    report_finalize
    exit 1
fi
export ADMIN_TOKEN

# ── 阶段6: 运行测试模块 ──
log_section "阶段6: 运行测试模块"
bash "${SCRIPT_DIR}/run_module.sh" "$MODULES" "$SERVER_URL" 2>&1 | tee "${REPORT_DIR}/${ENV_TYPE}-output.log"
EXIT_CODE=${PIPESTATUS[0]}

# ── 阶段7: 清理 ──
log_section "阶段7: 清理"
cleanup_all_nodes "$CREATED_IDS"

# ── 最终报告 ──
report_finalize

log_section "测试结束"
log_info "退出码: ${EXIT_CODE}"
exit $EXIT_CODE
