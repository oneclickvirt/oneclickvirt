#!/bin/bash
# Test Framework Core - logging, assertions, reporting, wait functions, state management
set -uo pipefail

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; NC='\033[0m'

_ts() { date '+%Y-%m-%dT%H:%M:%S'; }
log_info()    { echo -e "${BLUE}[INFO]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_success() { echo -e "${GREEN}[PASS]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_error()   { echo -e "${RED}[FAIL]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_section() { echo -e "\n${CYAN}========== $* ==========${NC}\n" >&2; }
log_skip()    { echo -e "${YELLOW}[SKIP]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_debug()   { [[ "${DEBUG:-0}" == "1" ]] && echo -e "[DEBUG] $(date '+%H:%M:%S') $*" >&2 || true; }

# -- Counters --
TOTAL_TESTS=0; PASSED_TESTS=0; FAILED_TESTS=0; SKIPPED_TESTS=0
declare -A CHAIN_BROKEN
REPORT_FILE=""
RESULTS_FILE=""
TEST_START_TS=""
MASTER_NODE_ID=""
MASTER_NODE_IP=""

# -- Global variables (shared across modules) --
SERVER_URL=""
ADMIN_TOKEN=""
ADMIN_USER="${ADMIN_USER:-admin}"
ADMIN_PASS="${ADMIN_PASS:-Admin123!@#}"
NORMAL_ADMIN_TOKEN=""
NORMAL_ADMIN_USER="test_admin"
NORMAL_ADMIN_PASS="TestAdmin123!@#"
USER_TOKEN=""
TEST_USER="test_user_ci"
TEST_USER_PASS="TestUser123!@#"
TEST_USER2="test_user_ci_2"
TEST_USER2_PASS="TestUser2_123!@#"
USER_TOKEN2=""
PROVIDER_ID=""
ENV_TYPE="${ENV_TYPE:-docker}"
INSTANCE_TYPES="${INSTANCE_TYPES:-both}"
NODE_IP=""
NODE_PASSWORD=""
WORKER_IP=""
WORKER_PASSWORD=""
WORKER_ID=""

# -- JSON result collector for HTML report --
declare -a TEST_RESULTS_JSON=()

# -- API test function --
# Args: test_name method url expected_code [data] [group] [token_override]
test_api() {
    local name="$1" method="$2" url="$3" expected="$4"
    local data="${5:-}" group="${6:-default}" token="${7:-$ADMIN_TOKEN}"
    local test_start; test_start=$(_ts)
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ -n "${CHAIN_BROKEN[$group]:-}" ]]; then
        SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
        log_skip "${name} (chain broken: ${CHAIN_BROKEN[$group]})"
        report_add_skip "$name" "$method" "$url" "${CHAIN_BROKEN[$group]}"
        _record_result "$name" "$method" "$url" "SKIP" "" "" "${CHAIN_BROKEN[$group]}" "$group"
        return 1
    fi
    local args=(-s -w "\n%{http_code}" --max-time 60
        -H "Content-Type: application/json" -X "${method}")
    [[ -n "$token" ]] && args+=(-H "Authorization: Bearer ${token}")
    [[ -n "$data" ]] && args+=(-d "$data")
    local resp; resp=$(curl "${args[@]}" "${SERVER_URL}${url}" 2>&1) || true
    local code; code=$(echo "$resp" | tail -1)
    local body; body=$(echo "$resp" | sed '$d')
    # Support pipe-separated expected codes (e.g. "200|201|400")
    local match=false
    IFS='|' read -ra exp_codes <<< "$expected"
    for ec in "${exp_codes[@]}"; do
        [[ "$code" == "$ec" ]] && { match=true; break; }
    done
    if [[ "$match" == "false" ]]; then
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "${name} - expected HTTP ${expected}, got HTTP ${code}"
        # Capture service logs on failure (timestamp-based)
        local error_logs=""
        error_logs=$(capture_service_logs "$test_start" 2>/dev/null) || true
        report_add_fail "$name" "$method" "$url" "$data" "$expected" "$code" "$body"
        _record_result "$name" "$method" "$url" "FAIL" "$expected" "$code" "$body" "$group" "$error_logs"
        return 1
    fi
    PASSED_TESTS=$((PASSED_TESTS + 1))
    log_success "${name}"
    report_add_pass "$name" "$method" "$url"
    _record_result "$name" "$method" "$url" "PASS" "$expected" "$code" "" "$group"
    echo "$body"
    return 0
}

# Retry wrapper
test_api_retry() {
    local name="$1" method="$2" url="$3" expected="$4" data="${5:-}" retries="${6:-3}" interval="${7:-5}" group="${8:-default}" token="${9:-$ADMIN_TOKEN}"
    local i=0
    while [[ $i -lt $retries ]]; do
        i=$((i + 1))
        [[ $i -gt 1 ]] && { log_info "Retry ${name} (${i}/${retries})..."; sleep "$interval"; }
        local st=$TOTAL_TESTS sp=$PASSED_TESTS sf=$FAILED_TESTS ss=$SKIPPED_TESTS
        local result; result=$(test_api "$name" "$method" "$url" "$expected" "$data" "$group" "$token" 2>&1) && { echo "$result"; return 0; }
        [[ $i -lt $retries ]] && { TOTAL_TESTS=$st; PASSED_TESTS=$sp; FAILED_TESTS=$sf; SKIPPED_TESTS=$ss; }
    done
    return 1
}

# Without auth token
test_api_noauth() {
    local name="$1" method="$2" url="$3" expected="$4" data="${5:-}" group="${6:-default}"
    test_api "$name" "$method" "$url" "$expected" "$data" "$group" ""
}

chain_break() { CHAIN_BROKEN[$1]="$2"; log_warning "Chain broken [${1}]: ${2}"; }

# -- Utility: should we test this instance type? --
should_test_type() {
    local itype="$1"
    case "$INSTANCE_TYPES" in
        both) return 0 ;;
        container) [[ "$itype" == "container" ]] && return 0 || return 1 ;;
        vm) [[ "$itype" == "vm" ]] && return 0 || return 1 ;;
    esac
    return 0
}

# -- Platform capabilities map (hardcoded per spec) --
# Type ID   Platform              Instance Types
# docker    Docker                container
# lxd       LXD                   container, vm
# incus     Incus                 container, vm
# podman    Podman                container
# containerd Containerd (nerdctl)  container
# proxmoxve Proxmox VE            container, vm
declare -A PLATFORM_SUPPORTS_VM=(
    [docker]=0 [lxd]=1 [incus]=1 [podman]=0 [containerd]=0 [proxmoxve]=1
)

env_supports_container() {
    # All supported platforms support containers
    case "$ENV_TYPE" in
        docker|lxd|incus|podman|containerd|proxmoxve) return 0 ;;
        *) return 1 ;;
    esac
}

env_supports_vm() {
    [[ "${PLATFORM_SUPPORTS_VM[${ENV_TYPE}]:-0}" -eq 1 ]]
}

# Validate and auto-correct instance types based on platform capabilities
validate_instance_types() {
    local platform="$1"
    local types="$2"
    local supports_vm="${PLATFORM_SUPPORTS_VM[$platform]:-0}"
    case "$types" in
        both)
            if [[ "$supports_vm" -eq 0 ]]; then
                log_warning "Platform '${platform}' does not support VM; auto-correcting to 'container'"
                echo "container"
                return 0
            fi
            echo "both"
            ;;
        vm)
            if [[ "$supports_vm" -eq 0 ]]; then
                log_error "Platform '${platform}' does not support VM instance type"
                log_warning "Auto-correcting to 'container'"
                echo "container"
                return 0
            fi
            echo "vm"
            ;;
        container)
            echo "container"
            ;;
        *)
            log_error "Unknown instance type: ${types}; defaulting to 'container'"
            echo "container"
            ;;
    esac
    return 0
}

# -- Wait functions --
wait_server_ready() {
    local url="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "Waiting for server: ${url}"
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s -o /dev/null -w "%{http_code}" --max-time 10 "${url}/health" 2>/dev/null) || true
        [[ "$r" == "200" ]] && { log_success "Server is ready"; return 0; }
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "Server readiness timeout (${max}s)"; return 1
}

wait_db_ready() {
    local url="$1" max="${2:-120}" interval="${3:-5}" elapsed=0
    log_info "Waiting for system initialization to complete..."
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 "${url}/api/v1/public/init/check" 2>/dev/null) || true
        local need_init; need_init=$(echo "$r" | jq -r '.data.needInit // true' 2>/dev/null)
        if [[ "$need_init" == "false" ]]; then
            log_success "System initialization complete"
            return 0
        fi
        log_debug "Init not complete yet (needInit=${need_init}), waiting..."
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "System init wait timeout after ${max}s"
    return 1
}

wait_task_complete() {
    local url="$1" task_id="$2" token="$3" max="${4:-600}" interval="${5:-10}" elapsed=0
    log_info "Waiting for task ${task_id} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 -H "Authorization: Bearer ${token}" \
            "${url}/api/v1/admin/tasks/${task_id}" 2>/dev/null) || true
        local st; st=$(echo "$r" | jq -r '.data.status // empty' 2>/dev/null)
        case "$st" in
            completed) log_success "Task ${task_id} completed"; echo "$r"; return 0 ;;
            failed|cancelled|timeout) log_error "Task ${task_id}: ${st}"; echo "$r"; return 1 ;;
        esac
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "Task timeout"; return 1
}

# -- Auth helpers --
# wait_init_ready: waits until /api/v1/public/init/check responds with code=0 (server+DB both up)
# NOTE: the server uses code=0 for success (not 200). code=0 means the API is reachable.
wait_init_ready() {
    local url="$1" max="${2:-180}" interval="${3:-5}" elapsed=0
    log_info "Waiting for init endpoint to respond..."
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 "${url}/api/v1/public/init/check" 2>/dev/null) || true
        local code; code=$(echo "$r" | jq -r '.code // empty' 2>/dev/null)
        if [[ "$code" == "0" ]]; then
            local need_init; need_init=$(echo "$r" | jq -r '.data.needInit // true' 2>/dev/null)
            log_success "Init endpoint ready (needInit=${need_init})"
            return 0
        fi
        log_debug "Init endpoint not ready yet (code=${code:-<no response>}), waiting..."
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "Init endpoint timeout after ${max}s"
    return 1
}

init_system() {
    # All-in-one container: MySQL on 127.0.0.1:3306, root with empty password
    local url="$1" user="$2" pass="$3"
    local data
    printf -v data \
        '{"admin":{"username":"%s","password":"%s","email":"%s@test.local"},"database":{"type":"mysql","host":"127.0.0.1","port":"3306","database":"oneclickvirt","username":"root","password":""}}' \
        "$user" "$pass" "$user"
    local resp; resp=$(curl -s --max-time 60 -H "Content-Type: application/json" -X POST -d "$data" "${url}/api/v1/public/init" 2>/dev/null)
    log_info "Init response: ${resp}"
    echo "$resp"
}

do_login() {
    local url="$1" user="$2" pass="$3"
    local r; r=$(curl -s --max-time 30 -H "Content-Type: application/json" -X POST \
        -d "{\"username\":\"${user}\",\"password\":\"${pass}\"}" "${url}/api/v1/auth/login" 2>/dev/null)
    echo "$r" | jq -r '.data.token // empty' 2>/dev/null
}

admin_login() {
    local url="$1" user="${2:-admin}" pass="${3:-Admin123!@#}"
    local raw; raw=$(curl -s --max-time 30 -H "Content-Type: application/json" -X POST \
        -d "{\"username\":\"${user}\",\"password\":\"${pass}\"}" "${url}/api/v1/auth/login" 2>/dev/null)
    log_debug "Login response for ${user}: ${raw}"
    local token; token=$(echo "$raw" | jq -r '.data.token // empty' 2>/dev/null)
    [[ -n "$token" ]] && { log_success "Login success: ${user}"; echo "$token"; return 0; }
    log_error "Login failed: ${user} - $(echo "$raw" | jq -r '.msg // .message // .data // "no response"' 2>/dev/null)"
    return 1
}

add_provider() {
    local url="$1" token="$2" name="$3" ptype="$4" ip="$5" port="${6:-22}" user="${7:-root}" pass="$8"
    curl -s --max-time 60 -H "Authorization: Bearer ${token}" -H "Content-Type: application/json" \
        -X POST -d "{\"name\":\"${name}\",\"type\":\"${ptype}\",\"ssh_host\":\"${ip}\",\"ssh_port\":${port},\"ssh_user\":\"${user}\",\"ssh_password\":\"${pass}\"}" \
        "${url}/api/v1/admin/providers" 2>/dev/null
}

# -- Results file (JSON Lines) init --
init_results_file() {
    RESULTS_FILE="$1"
    : > "$RESULTS_FILE"
    TEST_START_TS=$(_ts)
}

# -- Record test result to JSON Lines file --
_record_result() {
    local name="$1" method="$2" url="$3" status="$4" expected="$5" actual="$6" detail="$7" group="$8" error_logs="${9:-}"
    local ts; ts=$(_ts)
    local safe_detail; safe_detail=$(echo "$detail" | head -c 2000 | sed 's/"/\\"/g' | tr '\n' ' ')
    local safe_logs; safe_logs=$(echo "$error_logs" | head -c 2000 | sed 's/"/\\"/g' | tr '\n' ' ')
    local json="{\"name\":\"${name}\",\"method\":\"${method}\",\"url\":\"${url}\",\"status\":\"${status}\",\"expected\":\"${expected}\",\"actual\":\"${actual}\",\"detail\":\"${safe_detail}\",\"group\":\"${group}\",\"timestamp\":\"${ts}\",\"error_logs\":\"${safe_logs}\"}"
    [[ -n "$RESULTS_FILE" ]] && echo "$json" >> "$RESULTS_FILE"
    TEST_RESULTS_JSON+=("$json")
}

# -- JSON result helper (backward compat) --
_add_result_json() {
    local name="$1" method="$2" url="$3" status="$4" expected="$5" actual="$6" detail="$7" group="$8"
    _record_result "$name" "$method" "$url" "$status" "$expected" "$actual" "$detail" "$group" ""
}

# -- Markdown report --
report_init() {
    REPORT_FILE="$1"
    local env="$2" ts; ts=$(date -u '+%Y-%m-%d %H:%M:%S UTC')
    cat > "$REPORT_FILE" << EOF
# ${env} Integration Test Report

Test Time: ${ts}
Environment: ${env}
Instance Types: ${INSTANCE_TYPES}

## Summary

| Metric | Value |
|--------|-------|
| Total | _PENDING_ |
| Passed | _PENDING_ |
| Failed | _PENDING_ |
| Skipped | _PENDING_ |
| Pass Rate | _PENDING_ |

## Test Details

EOF
}

report_add_section() {
    [[ -z "$REPORT_FILE" ]] && return
    echo -e "\n### $1\n\n| Status | Test | Method | Route | Note |\n|--------|------|--------|-------|------|" >> "$REPORT_FILE"
}

report_add_pass() {
    [[ -z "$REPORT_FILE" ]] && return
    echo "| PASS | $1 | $2 | \`$3\` | - |" >> "$REPORT_FILE"
}

report_add_fail() {
    local name="$1" method="$2" url="$3" data="$4" expect="$5" actual="$6" body="$7"
    [[ -z "$REPORT_FILE" ]] && return
    echo "| FAIL | ${name} | ${method} | \`${url}\` | expected ${expect}, got ${actual} |" >> "$REPORT_FILE"
    {
        echo ""; echo "<details>"; echo "<summary>${name} - Details</summary>"; echo ""
        echo "**Request**: \`${method} ${url}\`"
        [[ -n "$data" ]] && { echo ""; echo '```json'; echo "$data" | jq '.' 2>/dev/null || echo "$data"; echo '```'; }
        echo ""; echo "**Expected**: ${expect} / **Actual**: ${actual}"; echo ""
        echo '```json'; echo "$body" | jq '.' 2>/dev/null || echo "$body"; echo '```'
        echo ""; echo "</details>"; echo ""
    } >> "$REPORT_FILE"
}

report_add_skip() {
    [[ -z "$REPORT_FILE" ]] && return
    echo "| SKIP | $1 | $2 | \`$3\` | $4 |" >> "$REPORT_FILE"
}

report_finalize() {
    [[ -z "$REPORT_FILE" ]] && return
    local rate=0
    [[ $TOTAL_TESTS -gt 0 ]] && rate=$(( PASSED_TESTS * 100 / TOTAL_TESTS ))
    sed -i.bak "s/| Total | _PENDING_ |/| Total | ${TOTAL_TESTS} |/" "$REPORT_FILE"
    sed -i.bak "s/| Passed | _PENDING_ |/| Passed | ${PASSED_TESTS} |/" "$REPORT_FILE"
    sed -i.bak "s/| Failed | _PENDING_ |/| Failed | ${FAILED_TESTS} |/" "$REPORT_FILE"
    sed -i.bak "s/| Skipped | _PENDING_ |/| Skipped | ${SKIPPED_TESTS} |/" "$REPORT_FILE"
    sed -i.bak "s/| Pass Rate | _PENDING_ |/| Pass Rate | ${rate}% |/" "$REPORT_FILE"
    rm -f "${REPORT_FILE}.bak"
    echo -e "\n---\n\nCompleted: Total=${TOTAL_TESTS} Passed=${PASSED_TESTS} Failed=${FAILED_TESTS} Skipped=${SKIPPED_TESTS} Rate=${rate}%" >> "$REPORT_FILE"
    log_section "Results: Total=${TOTAL_TESTS} Passed=${PASSED_TESTS} Failed=${FAILED_TESTS} Skipped=${SKIPPED_TESTS} Rate=${rate}%"
}

# -- HTML report generation (delegates to report/generate_report.sh) --
generate_html_report() {
    local output_file="$1" env_name="$2"
    local script_dir; script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local report_script="${script_dir}/../report/generate_report.sh"
    local service_log_file="${REPORT_DIR:-/tmp}/${env_name}-service-errors.log"

    # Fetch service error logs for inclusion in report
    fetch_full_service_logs "$service_log_file" || true

    if [[ -f "$report_script" && -n "$RESULTS_FILE" ]]; then
        bash "$report_script" "$RESULTS_FILE" "$output_file" "$env_name" "$service_log_file" || {
            log_warning "Report generator failed, creating fallback report"
            echo "<html><body><h1>Report generation failed</h1><p>Results file: ${RESULTS_FILE}</p><pre>$(cat "$RESULTS_FILE" 2>/dev/null | head -100)</pre></body></html>" > "$output_file"
        }
    else
        log_warning "Report script or results file not found (script=${report_script}, results=${RESULTS_FILE})"
        echo "<html><body><h1>No results available</h1></body></html>" > "$output_file"
    fi
}

# -- State management: save/restore between modules --
SAVED_CONFIG=""
SAVED_INSTANCE_IDS=""

save_base_state() {
    log_info "Saving base state before module..."
    SAVED_CONFIG=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/system-config" 2>/dev/null) || true
    local inst_resp; inst_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/instances?page=1&page_size=1000" 2>/dev/null) || true
    SAVED_INSTANCE_IDS=$(echo "$inst_resp" | jq -r '.data.items[]?.id // empty' 2>/dev/null | tr '\n' ',' | sed 's/,$//')
    log_debug "Saved instance IDs: ${SAVED_INSTANCE_IDS:-none}"
}

restore_base_state() {
    log_info "Restoring base state after module..."
    # Delete any instances created during the module
    local curr_resp; curr_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/instances?page=1&page_size=1000" 2>/dev/null) || true
    local curr_ids; curr_ids=$(echo "$curr_resp" | jq -r '.data.items[]?.id // empty' 2>/dev/null)
    for id in $curr_ids; do
        if [[ -n "$id" ]] && ! echo ",$SAVED_INSTANCE_IDS," | grep -q ",${id},"; then
            log_info "Cleaning up instance created during module: ${id}"
            curl -s --max-time 60 -X DELETE -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                "${SERVER_URL}/api/v1/admin/instances/${id}" 2>/dev/null || true
            sleep 2
        fi
    done
    # Re-login to refresh tokens
    ADMIN_TOKEN=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS" 2>/dev/null) || true
    USER_TOKEN=$(do_login "$SERVER_URL" "$TEST_USER" "$TEST_USER_PASS" 2>/dev/null) || true
    USER_TOKEN2=$(do_login "$SERVER_URL" "$TEST_USER2" "$TEST_USER2_PASS" 2>/dev/null) || true
    NORMAL_ADMIN_TOKEN=$(do_login "$SERVER_URL" "$NORMAL_ADMIN_USER" "$NORMAL_ADMIN_PASS" 2>/dev/null) || true
    log_info "Base state restored"
}

# -- Service log capture (master runs locally on runner) --
capture_service_logs() {
    local since="${1:-}" max_lines="${2:-50}"
    docker logs oneclickvirt --since="${since}" 2>&1 | grep -iE 'error|panic|fatal|warn' | tail -"${max_lines}" 2>/dev/null || true
}

fetch_full_service_logs() {
    local output_file="$1"
    docker logs oneclickvirt --tail=500 2>&1 > "${output_file}" 2>/dev/null \
        || echo "No service logs available" > "${output_file}"
}

dump_master_logs() {
    local date_dir; date_dir=$(date +%Y-%m-%d)
    log_info "=== App error log (/app/storage/logs/${date_dir}/error.log) ==="
    docker exec oneclickvirt cat "/app/storage/logs/${date_dir}/error.log" 2>/dev/null || echo "(app error log not found)"
    log_info "=== App warn log (/app/storage/logs/${date_dir}/warn.log) ==="
    docker exec oneclickvirt cat "/app/storage/logs/${date_dir}/warn.log" 2>/dev/null || echo "(app warn log not found)"
    log_info "=== MySQL error log (/var/log/mysql/error.log) ==="
    docker exec oneclickvirt tail -100 /var/log/mysql/error.log 2>/dev/null || echo "(mysql error log not found)"
    log_info "=== Supervisor MySQL error log (/var/log/supervisor/mysql_error.log) ==="
    docker exec oneclickvirt tail -50 /var/log/supervisor/mysql_error.log 2>/dev/null || echo "(supervisor mysql error log not found)"
    log_info "=== Nginx error log (/var/log/nginx/error.log) ==="
    docker exec oneclickvirt tail -50 /var/log/nginx/error.log 2>/dev/null || echo "(nginx error log not found)"
}
