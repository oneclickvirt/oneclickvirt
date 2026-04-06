#!/bin/bash
# Environment Integration Test Orchestrator
# Two-node architecture: master node (OneClickVirt service) + worker node (virtualization environment)
# Usage: bash run_env_test.sh <env_type> [modules] [instance_types]
# Examples:
#   bash run_env_test.sh docker all both
#   bash run_env_test.sh lxd 01-10 container
#   bash run_env_test.sh incus all vm
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMMON_DIR="${SCRIPT_DIR}/common"
REPORT_DIR="${SCRIPT_DIR}/reports"
mkdir -p "$REPORT_DIR"

source "${COMMON_DIR}/test_framework.sh"
source "${COMMON_DIR}/node_manager.sh"

export ENV_TYPE="${1:-docker}"
MODULES="${2:-all}"
export INSTANCE_TYPES="${3:-both}"
NODE_HOURS="${NODE_HOURS:-8}"
MASTER_PORT="${MASTER_PORT:-80}"
EXIT_CODE=0

log_section "Environment Integration Test: ${ENV_TYPE}"
log_info "Modules: ${MODULES}"
log_info "Instance types: ${INSTANCE_TYPES}"
log_info "Node hours: ${NODE_HOURS}h"

# -- Report init --
report_init "${REPORT_DIR}/${ENV_TYPE}-report.md" "${ENV_TYPE}"

CREATED_IDS=""

# =============================================================
# Phase 1: Create master node
# =============================================================
log_section "Phase 1: Create master node"
MASTER_INFO=$(create_test_node "docker" "$NODE_HOURS")
if [[ $? -ne 0 || -z "$MASTER_INFO" ]]; then
    log_error "Failed to create master node"
    report_finalize
    exit 1
fi
MASTER_ID=$(echo "$MASTER_INFO" | jq -r '.instance_id')
MASTER_IP=$(echo "$MASTER_INFO" | jq -r '.ipv4')
MASTER_PW=$(echo "$MASTER_INFO" | jq -r '.password')
CREATED_IDS="${MASTER_ID}"
log_success "Master node: ID=${MASTER_ID} IP=${MASTER_IP}"

# =============================================================
# Phase 2: Create worker node with virtualization environment
# =============================================================
log_section "Phase 2: Create worker node"
WORKER_INFO=$(create_test_node "$ENV_TYPE" "$NODE_HOURS")
if [[ $? -ne 0 || -z "$WORKER_INFO" ]]; then
    log_error "Failed to create worker node"
    cleanup_all_nodes "$CREATED_IDS"
    report_finalize
    exit 1
fi
WORKER_ID_VAL=$(echo "$WORKER_INFO" | jq -r '.instance_id')
export WORKER_IP; WORKER_IP=$(echo "$WORKER_INFO" | jq -r '.ipv4')
export WORKER_PASSWORD; WORKER_PASSWORD=$(echo "$WORKER_INFO" | jq -r '.password')
CREATED_IDS="${CREATED_IDS},${WORKER_ID_VAL}"
export NODE_IP="$WORKER_IP"
export NODE_PASSWORD="$WORKER_PASSWORD"
log_success "Worker node: ID=${WORKER_ID_VAL} IP=${WORKER_IP}"

# =============================================================
# Phase 3: Install virtualization environment on worker
# =============================================================
log_section "Phase 3: Install ${ENV_TYPE} on worker node"
install_env "$WORKER_ID_VAL" "$ENV_TYPE"

# =============================================================
# Phase 4: Prepare dirty node for discovery tests
# =============================================================
log_section "Phase 4: Prepare worker with pre-existing instances"
prepare_dirty_node "$WORKER_ID_VAL" "$ENV_TYPE"

# =============================================================
# Phase 5: Deploy master service
# =============================================================
log_section "Phase 5: Deploy OneClickVirt on master node"
deploy_master "$MASTER_ID" "$MASTER_PORT"

export SERVER_URL="http://${MASTER_IP}:${MASTER_PORT}"
log_info "Master URL: ${SERVER_URL}"

# =============================================================
# Phase 6: Wait for service readiness
# =============================================================
log_section "Phase 6: Wait for service readiness"
wait_server_ready "$SERVER_URL" 300 10 || {
    log_error "Master service startup timeout"
    cleanup_all_nodes "$CREATED_IDS"
    report_finalize
    exit 1
}

# =============================================================
# Phase 7: Initialize system and login
# =============================================================
log_section "Phase 7: System initialization"
init_system "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS" "sqlite"
sleep 2
ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS")
if [[ -z "$ADMIN_TOKEN" ]]; then
    log_error "Admin login failed"
    cleanup_all_nodes "$CREATED_IDS"
    report_finalize
    exit 1
fi
export ADMIN_TOKEN

# =============================================================
# Phase 8: Run test modules
# =============================================================
log_section "Phase 8: Run test modules"
bash "${SCRIPT_DIR}/run_module.sh" "$MODULES" "$SERVER_URL" 2>&1 | tee "${REPORT_DIR}/${ENV_TYPE}-output.log"
EXIT_CODE=${PIPESTATUS[0]}

# =============================================================
# Phase 9: Generate HTML report
# =============================================================
log_section "Phase 9: Generate reports"
generate_html_report "${REPORT_DIR}/${ENV_TYPE}-report.html" "${ENV_TYPE}"

# =============================================================
# Phase 10: Cleanup
# =============================================================
log_section "Phase 10: Cleanup"
cleanup_all_nodes "$CREATED_IDS"

# -- Finalize --
report_finalize

log_section "Test completed"
log_info "Exit code: ${EXIT_CODE}"
exit $EXIT_CODE
