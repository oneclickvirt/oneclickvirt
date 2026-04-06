#!/bin/bash
# Environment Integration Test Orchestrator
# Two-node architecture: master node (OneClickVirt service) + worker node (virtualization environment)
# Usage: bash run_env_test.sh <env_type> [modules] [instance_types]
# Examples:
#   bash run_env_test.sh docker all container
#   bash run_env_test.sh lxd 01-10 both
#   bash run_env_test.sh incus all vm
#
# Platform instance type support (hardcoded):
#   docker/podman/containerd → container only
#   lxd/incus/proxmoxve     → container + vm
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMMON_DIR="${SCRIPT_DIR}/common"
REPORT_DIR="${SCRIPT_DIR}/reports"
mkdir -p "$REPORT_DIR"

source "${COMMON_DIR}/test_framework.sh"
source "${COMMON_DIR}/node_manager.sh"

export ENV_TYPE="${1:-docker}"
MODULES="${2:-all}"
RAW_INSTANCE_TYPES="${3:-both}"
NODE_HOURS="${NODE_HOURS:-8}"
MASTER_PORT="${MASTER_PORT:-80}"
EXIT_CODE=0

# =============================================================
# Phase 0: Validate platform and instance types
# =============================================================
log_section "Environment Integration Test: ${ENV_TYPE}"
VALIDATED_TYPES=$(validate_instance_types "$ENV_TYPE" "$RAW_INSTANCE_TYPES")
export INSTANCE_TYPES="$VALIDATED_TYPES"
log_info "Modules: ${MODULES}"
log_info "Instance types: ${INSTANCE_TYPES} (requested: ${RAW_INSTANCE_TYPES})"
log_info "Node hours: ${NODE_HOURS}h"

# Preflight: check required environment variables
if [[ -z "${ALICEINIT_TOKEN:-}" ]]; then
    log_error "ALICEINIT_TOKEN is not set. Cannot create test nodes."
    log_error "Set it via: export ALICEINIT_TOKEN=your_token"
    exit 1
fi
if [[ -z "${ALICE_API_BASE:-}" ]]; then
    log_error "ALICE_API_BASE is not set. Cannot contact AliceInit API."
    exit 1
fi

# -- Report & results init --
report_init "${REPORT_DIR}/${ENV_TYPE}-report.md" "${ENV_TYPE}"
init_results_file "${REPORT_DIR}/${ENV_TYPE}-results.jsonl"

CREATED_IDS=""

# Error handler: capture logs and cleanup on unexpected exit
_cleanup_on_exit() {
    local exit_code=$?
    if [[ $exit_code -ne 0 ]]; then
        log_error "Script exiting with code ${exit_code}"
        # Try to capture service logs for debugging
        if [[ -n "$MASTER_NODE_ID" ]]; then
            log_info "Capturing service logs for debugging..."
            fetch_full_service_logs "${REPORT_DIR}/${ENV_TYPE}-crash-logs.txt" 2>/dev/null || true
        fi
    fi
    if [[ -n "$CREATED_IDS" ]]; then
        log_info "Cleaning up nodes: ${CREATED_IDS}"
        cleanup_all_nodes "$CREATED_IDS" 2>/dev/null || true
    fi
    report_finalize 2>/dev/null || true
}
trap _cleanup_on_exit EXIT

# =============================================================
# Phase 1: Create master node
# =============================================================
log_section "Phase 1: Create master node"
MASTER_INFO=$(create_test_node "docker" "$NODE_HOURS") || {
    log_error "Failed to create master node (create_test_node returned error)"
    exit 1
}
if [[ -z "$MASTER_INFO" ]]; then
    log_error "Failed to create master node (empty response)"
    exit 1
fi
MASTER_ID=$(echo "$MASTER_INFO" | jq -r '.instance_id')
MASTER_IP=$(echo "$MASTER_INFO" | jq -r '.ipv4')
MASTER_PW=$(echo "$MASTER_INFO" | jq -r '.password')
CREATED_IDS="${MASTER_ID}"
MASTER_NODE_ID="$MASTER_ID"
export MASTER_NODE_ID
log_success "Master node: ID=${MASTER_ID} IP=${MASTER_IP}"

# =============================================================
# Phase 2: Create worker node with virtualization environment
# =============================================================
log_section "Phase 2: Create worker node"
WORKER_INFO=$(create_test_node "$ENV_TYPE" "$NODE_HOURS") || {
    log_error "Failed to create worker node"
    exit 1
}
if [[ -z "$WORKER_INFO" ]]; then
    log_error "Failed to create worker node (empty response)"
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
install_env "$WORKER_ID_VAL" "$ENV_TYPE" || {
    log_warning "Environment installation may have issues, continuing..."
}

# =============================================================
# Phase 4: Prepare dirty node for discovery tests
# =============================================================
log_section "Phase 4: Prepare worker with pre-existing instances"
prepare_dirty_node "$WORKER_ID_VAL" "$ENV_TYPE" || {
    log_warning "Dirty node preparation had issues, continuing..."
}

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
if ! wait_server_ready "$SERVER_URL" 300 10; then
    log_error "Master service startup timeout"
    log_info "Attempting to capture service logs..."
    fetch_full_service_logs "${REPORT_DIR}/${ENV_TYPE}-startup-logs.txt" 2>/dev/null || true
    if [[ -f "${REPORT_DIR}/${ENV_TYPE}-startup-logs.txt" ]]; then
        log_error "=== Service startup logs ==="
        cat "${REPORT_DIR}/${ENV_TYPE}-startup-logs.txt" | tail -50
        log_error "=== End startup logs ==="
    fi
    exit 1
fi

# =============================================================
# Phase 7: Initialize system and login
# =============================================================
log_section "Phase 7: System initialization"
init_system "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS" "sqlite"
sleep 2
ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS")
if [[ -z "$ADMIN_TOKEN" ]]; then
    log_error "Admin login failed"
    fetch_full_service_logs "${REPORT_DIR}/${ENV_TYPE}-login-fail-logs.txt" 2>/dev/null || true
    exit 1
fi
export ADMIN_TOKEN

# =============================================================
# Phase 8: Run test modules
# =============================================================
log_section "Phase 8: Run test modules"
export RESULTS_FILE="${REPORT_DIR}/${ENV_TYPE}-results.jsonl"
export REPORT_DIR
bash "${SCRIPT_DIR}/run_module.sh" "$MODULES" "$SERVER_URL" 2>&1 | tee "${REPORT_DIR}/${ENV_TYPE}-output.log"
EXIT_CODE=${PIPESTATUS[0]}

# =============================================================
# Phase 9: Generate HTML report
# =============================================================
log_section "Phase 9: Generate reports"
generate_html_report "${REPORT_DIR}/${ENV_TYPE}-report.html" "${ENV_TYPE}"

# =============================================================
# Phase 10: Cleanup (handled by EXIT trap)
# =============================================================
log_section "Phase 10: Cleanup"
# Explicit cleanup (trap will also fire but that's OK)
cleanup_all_nodes "$CREATED_IDS" 2>/dev/null || true
CREATED_IDS=""  # Prevent double cleanup in trap

# -- Finalize --
report_finalize

log_section "Test completed"
log_info "Exit code: ${EXIT_CODE}"
exit $EXIT_CODE
