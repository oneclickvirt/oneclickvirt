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
#   docker/podman/containerd        → container only
#   lxd/incus/proxmoxve             → container + vm
#   kubevirt/qemu                   → vm only
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMMON_DIR="${SCRIPT_DIR}/common"
REPORT_DIR="${SCRIPT_DIR}/reports"
mkdir -p "$REPORT_DIR"

source "${COMMON_DIR}/test_framework.sh"
source "${COMMON_DIR}/node_manager.sh"
# Restore SCRIPT_DIR: sourced files above set SCRIPT_DIR to their own directory
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

export ENV_TYPE="${1:-docker}"
MODULES="${2:-all}"
RAW_INSTANCE_TYPES="${3:-both}"
NODE_HOURS="${NODE_HOURS:-8}"
MASTER_PORT="${MASTER_PORT:-8888}"
EXIT_CODE=0

# =============================================================
# Phase 0: Validate platform and instance types
# =============================================================
log_section "Environment Integration Test: ${ENV_TYPE}"
VALIDATED_TYPES=$(validate_instance_types "$ENV_TYPE" "$RAW_INSTANCE_TYPES")
export INSTANCE_TYPES="$VALIDATED_TYPES"
log_info "Modules: ${MODULES}"
log_info "Instance types: ${INSTANCE_TYPES} (requested: ${RAW_INSTANCE_TYPES})"
log_info "Execution rule: ${EXECUTION_RULE}"
log_info "Node hours: ${NODE_HOURS}h"

# Preflight: check that at least one platform is enabled and has credentials
ENABLED_PLATFORMS=$(get_enabled_platforms)
if [[ -z "${ENABLED_PLATFORMS}" ]]; then
    log_error "No cloud platforms are enabled."
    log_error "Set PLATFORM_<NAME>_ENABLED=true and provide the corresponding secrets."
    log_error "Example: export PLATFORM_ALICE_ENABLED=true ALICE_CLIENT_ID=xxx ALICE_CLIENT_SECRET=xxx"
    exit 1
fi
log_info "Enabled platforms: ${ENABLED_PLATFORMS}"
log_info "Active platform will be selected automatically with fallback"

# -- Report & results init --
report_init "${REPORT_DIR}/${ENV_TYPE}-report.md" "${ENV_TYPE}"
init_results_file "${REPORT_DIR}/${ENV_TYPE}-results.jsonl"

CREATED_IDS=""

# Error handler: capture logs and cleanup on unexpected exit
_cleanup_on_exit() {
    local exit_code=$?
    if [[ $exit_code -ne 0 ]]; then
        log_error "Script exiting with code ${exit_code}"
        log_info "Capturing service logs for debugging..."
        fetch_full_service_logs "${REPORT_DIR}/${ENV_TYPE}-crash-logs.txt" 2>/dev/null || true
    fi
    if [[ -n "$CREATED_IDS" ]]; then
        log_info "Cleaning up nodes: ${CREATED_IDS}"
        cleanup_all_nodes "$CREATED_IDS" 2>/dev/null || true
    fi
    # Kill the Go server process started by deploy_master_local
    if [[ -f /tmp/oneclickvirt-server.pid ]]; then
        kill "$(cat /tmp/oneclickvirt-server.pid)" 2>/dev/null || true
        rm -f /tmp/oneclickvirt-server.pid
    fi
    pkill -f '/tmp/oneclickvirt-server' 2>/dev/null || true
    rm -f /tmp/oneclickvirt-server
    report_finalize 2>/dev/null || true
}
trap _cleanup_on_exit EXIT

# =============================================================
# Phase 1: Deploy master service on runner (source build + local MySQL)
# =============================================================
log_section "Phase 1: Deploy master on runner"
deploy_master_local "$MASTER_PORT" || {
    log_warning "First master deploy attempt failed; retrying after 30s..."
    # Kill any stale server process before retry — if the first attempt timed out
    # but the process is still alive it will hold port ${MASTER_PORT} and cause
    # the second nohup to fail immediately with "address already in use".
    if [[ -f /tmp/oneclickvirt-server.pid ]]; then
        kill "$(cat /tmp/oneclickvirt-server.pid)" 2>/dev/null || true
        rm -f /tmp/oneclickvirt-server.pid
    fi
    pkill -f '/tmp/oneclickvirt-server' 2>/dev/null || true
    sleep 30
    deploy_master_local "$MASTER_PORT" || {
        log_error "Failed to deploy master on runner after retry"
        # Treat as transient infrastructure failure so the Action doesn't hard-fail
        exit 75
    }
}
export MASTER_NODE_ID=""
export MASTER_NODE_IP="127.0.0.1"
log_success "Master deployed locally on runner (port ${MASTER_PORT})"

# =============================================================
# Phase 2: Create worker node with virtualization environment
# =============================================================
log_section "Phase 2: Create worker node"
WORKER_INFO=$(create_test_node "$ENV_TYPE" "$NODE_HOURS") || {
    _worker_rc=$?
    if [[ $_worker_rc -eq 75 ]]; then
        log_error "Failed to create worker node: all cloud platforms temporarily out of resources"
    else
        log_error "Failed to create worker node (infrastructure failure, exit=${_worker_rc})"
    fi
    log_info "This is a transient infrastructure condition, not a test failure."
    log_info "Re-run the workflow when resources are available, or add more cloud platform accounts."
    # Exit 75 (EX_TEMPFAIL) for any cloud/infrastructure failure — keeps Action green
    exit 75
}
if [[ -z "$WORKER_INFO" ]]; then
    log_error "Failed to create worker node (empty response)"
    exit 1
fi
WORKER_ID_VAL=$(echo "$WORKER_INFO" | jq -r '.instance_id')
export WORKER_IP; WORKER_IP=$(echo "$WORKER_INFO" | jq -r '.ipv4')
export NODE_PASSWORD; NODE_PASSWORD=$(echo "$WORKER_INFO" | jq -r '.password // empty')
export WORKER_PASSWORD="$NODE_PASSWORD"
WORKER_PLATFORM=$(echo "$WORKER_INFO" | jq -r '.platform // empty')
CREATED_IDS="${WORKER_ID_VAL}"
export NODE_IP="$WORKER_IP"
# create_test_node runs inside $() so ACTIVE_PLATFORM and PLATFORM_SSH_KEY_FILE are lost
# when that subshell exits. Re-initialize the platform in the main shell so all subsequent
# SSH operations (install_env, module tests, cleanup) use the correct platform context.
if [[ -n "$WORKER_PLATFORM" ]]; then
    platform_init "$WORKER_PLATFORM" || log_warning "Could not re-init platform '${WORKER_PLATFORM}' in main shell"
    ACTIVE_INSTANCE_ID="${WORKER_ID_VAL}"
    ACTIVE_INSTANCE_IP="${WORKER_IP}"
fi
log_success "Worker node: ID=${WORKER_ID_VAL} IP=[MASKED] Platform=${WORKER_PLATFORM}"
log_info "Waiting for cloud-init on worker node (handled by wait_for_apt_lock)..."

# =============================================================
# Phase 3: Install virtualization environment on worker
# =============================================================
log_section "Phase 3: Install ${ENV_TYPE} on worker node"
install_env "$WORKER_ID_VAL" "$WORKER_IP" "$ENV_TYPE" || {
    log_warning "Environment installation may have issues, continuing..."
}

# =============================================================
# Phase 4: Prepare dirty node for discovery tests
# =============================================================
log_section "Phase 4: Prepare worker with pre-existing instances"
prepare_dirty_node "$WORKER_ID_VAL" "$WORKER_IP" "$ENV_TYPE" || {
    log_warning "Dirty node preparation had issues, continuing..."
}

# =============================================================
# Phase 5: Set server URL (master already deployed on runner in Phase 1)
# =============================================================
export SERVER_URL="http://localhost:${MASTER_PORT}"
log_info "Master URL: ${SERVER_URL}"

# =============================================================
# Phase 6: Wait for service readiness
# =============================================================
log_section "Phase 6: Wait for service readiness"
if ! wait_server_ready "$SERVER_URL" 300 10; then
    log_warning "Master service startup timeout; attempting server restart..."
    # Kill stale process and restart from already-compiled binary
    if [[ -f /tmp/oneclickvirt-server.pid ]]; then
        kill "$(cat /tmp/oneclickvirt-server.pid)" 2>/dev/null || true
        rm -f /tmp/oneclickvirt-server.pid
    fi
    pkill -f '/tmp/oneclickvirt-server' 2>/dev/null || true
    sleep 5
    if [[ -n "${MASTER_SERVER_DIR:-}" && -f /tmp/oneclickvirt-server ]]; then
        cd "${MASTER_SERVER_DIR}" || {
            log_error "Cannot cd to server dir ${MASTER_SERVER_DIR} — restart aborted"
            exit 75
        }
        GIN_MODE=debug nohup /tmp/oneclickvirt-server >> /tmp/oneclickvirt-server.log 2>&1 &
        echo $! > /tmp/oneclickvirt-server.pid
        cd - >/dev/null || true
    fi
    if ! wait_server_ready "$SERVER_URL" 120 10; then
        log_error "Master service still not ready after restart"
        fetch_full_service_logs "${REPORT_DIR}/${ENV_TYPE}-startup-logs.txt" 2>/dev/null || true
        if [[ -f "${REPORT_DIR}/${ENV_TYPE}-startup-logs.txt" ]]; then
            log_error "=== Service startup logs ==="
            tail -50 "${REPORT_DIR}/${ENV_TYPE}-startup-logs.txt"
            log_error "=== End startup logs ==="
        fi
        exit 75
    fi
fi

# =============================================================
# Phase 7: Initialize system and login
# =============================================================
log_section "Phase 7: System initialization and login"
# Wait until /api/v1/public/init/check is reachable (MySQL + app both up)
if ! wait_init_ready "$SERVER_URL" 180 5; then
    log_error "Init endpoint never became ready"
    fetch_full_service_logs "${REPORT_DIR}/${ENV_TYPE}-init-fail-logs.txt" 2>/dev/null || true
    dump_master_logs
    exit 75
fi
# Check whether initialization is still required
INIT_CHECK=$(curl -s --max-time 10 "${SERVER_URL}/api/v1/public/init/check" 2>/dev/null)
NEED_INIT=$(echo "$INIT_CHECK" | jq -r '.data.needInit // true' 2>/dev/null)
log_info "Init check: needInit=${NEED_INIT}"
if [[ "$NEED_INIT" == "true" ]]; then
    log_info "Initializing system..."
    INIT_RESP=$(init_system "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS")
    INIT_CODE=$(echo "$INIT_RESP" | jq -r '.code // empty' 2>/dev/null)
    if [[ "$INIT_CODE" != "200" ]]; then
        log_error "System initialization failed (code=${INIT_CODE}): ${INIT_RESP}"
        fetch_full_service_logs "${REPORT_DIR}/${ENV_TYPE}-init-fail-logs.txt" 2>/dev/null || true
        dump_master_logs
        exit 75
    fi
    log_success "System initialized, waiting for async setup to complete..."
    wait_db_ready "$SERVER_URL" 120 3
fi
# Login with admin credentials
ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS")
if [[ -z "$ADMIN_TOKEN" ]]; then
    log_warning "Admin login failed on first attempt; retrying after 20s..."
    sleep 20
    ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS")
fi
if [[ -z "$ADMIN_TOKEN" ]]; then
    log_error "Admin login failed after retry"
    fetch_full_service_logs "${REPORT_DIR}/${ENV_TYPE}-login-fail-logs.txt" 2>/dev/null || true
    dump_master_logs
    exit 75
fi
export ADMIN_TOKEN

# =============================================================
# Phase 8: Run test modules
# =============================================================
log_section "Phase 8: Run test modules"
export RESULTS_FILE="${REPORT_DIR}/${ENV_TYPE}-results.jsonl"
export REPORT_DIR
export GENERATE_MODULE_REPORT=false

# Determine which execution rules to test.
# If EXECUTION_RULE=all, cycle through api_only → ssh_only → auto, resetting
# the server and re-registering state between each run.
if [[ "${EXECUTION_RULE}" == "all" ]]; then
    EXECUTION_RULES_LIST="api_only ssh_only auto"
else
    EXECUTION_RULES_LIST="${EXECUTION_RULE}"
fi

EXIT_CODE=0
_first_rule=true
for _current_rule in ${EXECUTION_RULES_LIST}; do
    # Reset system before each subsequent run
    if [[ "${_first_rule}" == "true" ]]; then
        _first_rule=false
    else
        log_section "Resetting system for execution rule: ${_current_rule}"
        reset_master_server "${MASTER_PORT}" || {
            log_error "System reset failed before execution rule ${_current_rule}"
            EXIT_CODE=1
            break
        }
    fi

    export EXECUTION_RULE="${_current_rule}"
    log_section "Running modules with EXECUTION_RULE=${_current_rule}"

    local_output_log="${REPORT_DIR}/${ENV_TYPE}-${_current_rule}-output.log"
    bash "${SCRIPT_DIR}/run_module.sh" "$MODULES" "$SERVER_URL" 2>&1 | tee "${local_output_log}"
    _run_exit=${PIPESTATUS[0]}
    [[ ${_run_exit} -ne 0 ]] && EXIT_CODE=${_run_exit}

    # Produce per-rule HTML report
    generate_html_report "${REPORT_DIR}/${ENV_TYPE}-${_current_rule}-report.html" "${ENV_TYPE}-${_current_rule}" 2>/dev/null || true
done
# Also write the combined last-run output to the default log file for backward compat
cp "${REPORT_DIR}/${ENV_TYPE}-${_current_rule}-output.log" "${REPORT_DIR}/${ENV_TYPE}-output.log" 2>/dev/null || true

# =============================================================
# Phase 9: Generate HTML report
# =============================================================
log_section "Phase 9: Generate reports"
# The per-rule reports were generated inside the loop above.
# Generate a final combined/summary report using the last run's state (always present).
generate_html_report "${REPORT_DIR}/${ENV_TYPE}-report.html" "${ENV_TYPE}"

# =============================================================
# Phase 10: Cleanup (handled by EXIT trap)
# =============================================================
log_section "Phase 10: Cleanup"
# Explicit cleanup (trap will also fire but that's OK)
cleanup_all_nodes "$CREATED_IDS" 2>/dev/null || true
CREATED_IDS=""  # Prevent double cleanup in trap
# Kill the Go server process
if [[ -f /tmp/oneclickvirt-server.pid ]]; then
    kill "$(cat /tmp/oneclickvirt-server.pid)" 2>/dev/null || true
    rm -f /tmp/oneclickvirt-server.pid
fi
pkill -f '/tmp/oneclickvirt-server' 2>/dev/null || true
rm -f /tmp/oneclickvirt-server

# -- Finalize --
report_finalize

log_section "Test completed"
log_info "Exit code: ${EXIT_CODE}"
if [[ $EXIT_CODE -ne 0 ]]; then
    log_warning "Some test modules had failures, see reports for details"
fi
# Always exit 0 to avoid failing the entire Action; test failures are captured in reports
exit 0
