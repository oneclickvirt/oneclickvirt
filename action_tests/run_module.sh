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
REPORT_DIR="${SCRIPT_DIR}/reports"
mkdir -p "$REPORT_DIR"
report_init "${REPORT_DIR}/module-${MODULE_INPUT}.md" "Module ${MODULE_INPUT}"

# Login first
wait_server_ready "$SERVER_URL" 60 5 || { log_error "Server unreachable"; exit 1; }
ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS") || { log_error "Admin login failed"; exit 1; }
USER_TOKEN=$(do_login "$SERVER_URL" "$TEST_USER" "$TEST_USER_PASS") || USER_TOKEN=""
USER_TOKEN2=$(do_login "$SERVER_URL" "$TEST_USER2" "$TEST_USER2_PASS") || USER_TOKEN2=""
NORMAL_ADMIN_TOKEN=$(do_login "$SERVER_URL" "$NORMAL_ADMIN_USER" "$NORMAL_ADMIN_PASS") || NORMAL_ADMIN_TOKEN=""

export ADMIN_TOKEN USER_TOKEN USER_TOKEN2 NORMAL_ADMIN_TOKEN

# Load and run modules
EXIT_CODE=0
for mod in "${MODULES[@]}"; do
    mod_file="${MODULES_DIR}/${mod}_*.sh"
    mod_files=(${mod_file})
    if [[ ! -f "${mod_files[0]}" ]]; then
        log_warning "Module ${mod} not found, skipping"
        continue
    fi
    source "${mod_files[0]}"
    func_name="run_module_${mod}"
    if declare -f "$func_name" > /dev/null 2>&1; then
        log_section "Running module ${mod}"
        "$func_name" || EXIT_CODE=1
    else
        log_warning "Module ${mod} has no entry function ${func_name}"
    fi
done

# Summary
report_finalize
generate_html_report "${REPORT_DIR}/module-${MODULE_INPUT}-report.html" "Module-${MODULE_INPUT}"
log_section "Test completed"
log_info "Total: ${TOTAL_TESTS} | Passed: ${PASSED_TESTS} | Failed: ${FAILED_TESTS} | Skipped: ${SKIPPED_TESTS}"
exit $EXIT_CODE
