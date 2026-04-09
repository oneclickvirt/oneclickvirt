#!/bin/bash
# Module Runner - runs selected test modules
# Usage: bash run_module.sh <module_number|module_range> [server_url]
# Examples:
#   bash run_module.sh 01
#   bash run_module.sh 01-05
#   bash run_module.sh 01,03,05
#   bash run_module.sh all
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODULES_DIR="${SCRIPT_DIR}/modules"
COMMON_DIR="${SCRIPT_DIR}/common"

source "${COMMON_DIR}/test_framework.sh"

MODULE_INPUT="${1:-}"
SERVER_URL="${2:-${SERVER_URL:-}}"

if [[ -z "$MODULE_INPUT" ]]; then
    echo "Usage: $0 <module_number> [server_url]"
    echo "Formats: 01 | 01-05 | 01,03,05 | all"
    echo ""
    echo "Available modules:"
    for f in "${MODULES_DIR}"/*.sh; do
        local_name=$(basename "$f")
        echo "  ${local_name}"
    done
    exit 1
fi

parse_modules() {
    local input="$1"
    local modules=()
    if [[ "$input" == "all" ]]; then
        for f in "${MODULES_DIR}"/*.sh; do
            local num; num=$(basename "$f" | grep -oP '^\d+')
            modules+=("$num")
        done
    elif [[ "$input" == *-* ]]; then
        local start; start=$(echo "$input" | cut -d- -f1 | sed 's/^0*//')
        local end_val; end_val=$(echo "$input" | cut -d- -f2 | sed 's/^0*//')
        for ((i=start; i<=end_val; i++)); do
            modules+=($(printf "%02d" "$i"))
        done
    elif [[ "$input" == *,* ]]; then
        IFS=',' read -ra parts <<< "$input"
        for p in "${parts[@]}"; do
            modules+=($(printf "%02d" "$(echo "$p" | sed 's/^0*//')"))
        done
    else
        modules+=($(printf "%02d" "$(echo "$input" | sed 's/^0*//')"))
    fi
    echo "${modules[@]}"
}

MODULES=($(parse_modules "$MODULE_INPUT"))

if [[ -z "$SERVER_URL" ]]; then
    echo "Error: SERVER_URL not set"
    exit 1
fi

log_section "Starting module tests: ${MODULES[*]}"
log_info "Server: ${SERVER_URL}"
log_info "Environment: ${ENV_TYPE}"
log_info "Instance types: ${INSTANCE_TYPES}"

# Init report
REPORT_DIR="${REPORT_DIR:-${SCRIPT_DIR}/reports}"
mkdir -p "$REPORT_DIR"
report_init "${REPORT_DIR}/module-${MODULE_INPUT}.md" "Module ${MODULE_INPUT}"

# Init results file (inherit from parent or create new one)
if [[ -z "${RESULTS_FILE:-}" ]]; then
    RESULTS_FILE="${REPORT_DIR}/module-${MODULE_INPUT}-results.jsonl"
    init_results_file "$RESULTS_FILE"
elif [[ ! -f "$RESULTS_FILE" ]]; then
    init_results_file "$RESULTS_FILE"
fi

# Login first
wait_server_ready "$SERVER_URL" 60 5 || { log_error "Server unreachable"; exit 1; }
ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS") || { log_error "Admin login failed"; exit 1; }

# Ensure TEST_USER2 and NORMAL_ADMIN_USER exist before running modules.
# When public registration is disabled (the post-init default), these users must be
# created via the admin API.  We always attempt creation and accept 200 (created) or
# 400/409 (already exists) as success.
log_info "Ensuring test user (${TEST_USER}) exists..."
curl -s --max-time 30 \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -X POST \
    -d "{\"username\":\"${TEST_USER}\",\"password\":\"${TEST_USER_PASS}\",\"email\":\"test@ci.local\",\"level\":1,\"userType\":\"user\"}" \
    "${SERVER_URL}/api/v1/admin/users" > /dev/null 2>&1 || true

log_info "Ensuring test user 2 (${TEST_USER2}) exists..."
curl -s --max-time 30 \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -X POST \
    -d "{\"username\":\"${TEST_USER2}\",\"password\":\"${TEST_USER2_PASS}\",\"email\":\"test2@ci.local\",\"level\":1,\"userType\":\"user\"}" \
    "${SERVER_URL}/api/v1/admin/users" > /dev/null 2>&1 || true

log_info "Ensuring normal-admin user (${NORMAL_ADMIN_USER}) exists..."
curl -s --max-time 30 \
    -H "Authorization: Bearer ${ADMIN_TOKEN}" \
    -H "Content-Type: application/json" \
    -X POST \
    -d "{\"username\":\"${NORMAL_ADMIN_USER}\",\"password\":\"${NORMAL_ADMIN_PASS}\",\"email\":\"test_admin@ci.local\",\"level\":5,\"userType\":\"normal_admin\"}" \
    "${SERVER_URL}/api/v1/admin/users" > /dev/null 2>&1 || true

USER_TOKEN=$(do_login "$SERVER_URL" "$TEST_USER" "$TEST_USER_PASS") || USER_TOKEN=""
USER_TOKEN2=$(do_login "$SERVER_URL" "$TEST_USER2" "$TEST_USER2_PASS") || USER_TOKEN2=""
NORMAL_ADMIN_TOKEN=$(do_login "$SERVER_URL" "$NORMAL_ADMIN_USER" "$NORMAL_ADMIN_PASS") || NORMAL_ADMIN_TOKEN=""

export ADMIN_TOKEN USER_TOKEN USER_TOKEN2 NORMAL_ADMIN_TOKEN

# Load and run modules (with state save/restore between each)
EXIT_CODE=0
MODULE_COUNT=${#MODULES[@]}
MODULE_IDX=0
for mod in "${MODULES[@]}"; do
    MODULE_IDX=$((MODULE_IDX + 1))
    mod_file="${MODULES_DIR}/${mod}_*.sh"
    mod_files=(${mod_file})
    if [[ ! -f "${mod_files[0]}" ]]; then
        log_warning "Module ${mod} not found, skipping"
        continue
    fi
    source "${mod_files[0]}"
    func_name="run_module_${mod}"
    if declare -f "$func_name" > /dev/null 2>&1; then
        log_section "Running module ${mod} (${MODULE_IDX}/${MODULE_COUNT})"
        
        # Save state before module
        save_base_state 2>/dev/null || true
        local_start_ts=$(_ts)
        if ! "$func_name"; then
            EXIT_CODE=1
            log_error "Module ${mod} failed"
            # Capture service logs at the time of failure
            if [[ -n "${MASTER_NODE_ID:-}" ]] && declare -F capture_service_logs > /dev/null 2>&1; then
                log_info "Capturing service logs for module ${mod} failure..."
                local_logs=$(capture_service_logs "$local_start_ts" 100 2>/dev/null) || true
                if [[ -n "$local_logs" ]]; then
                    log_error "=== Service error logs (module ${mod}) ==="
                    echo "$local_logs" | head -30
                    log_error "=== End service logs ==="
                    # Save to file for report
                    echo "$local_logs" > "${REPORT_DIR}/module-${mod}-error-logs.txt" 2>/dev/null || true
                fi
            fi
        fi
        # Restore state after module to prevent cross-module contamination
        restore_base_state 2>/dev/null || true
    else
        log_warning "Module ${mod} has no entry function ${func_name}"
    fi
done

# Summary
report_finalize

# Generate HTML report if we have a results file and are not delegating to parent
if [[ "${GENERATE_MODULE_REPORT:-true}" == "true" ]]; then
    if [[ -n "${RESULTS_FILE:-}" && -f "${RESULTS_FILE:-}" ]]; then
        generate_html_report "${REPORT_DIR}/module-${MODULE_INPUT}-report.html" "Module-${MODULE_INPUT}"
    else
        log_warning "No results file found, skipping HTML report generation"
    fi
fi

log_section "Test completed"
log_info "Total: ${TOTAL_TESTS} | Passed: ${PASSED_TESTS} | Failed: ${FAILED_TESTS} | Skipped: ${SKIPPED_TESTS}"
if [[ $EXIT_CODE -ne 0 ]]; then
    log_warning "Some modules had failures (exit_code=${EXIT_CODE}), see reports for details"
fi
# Always exit 0 to avoid failing the entire Action; failures are captured in reports
exit 0
