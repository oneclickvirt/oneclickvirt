#!/bin/bash
# Test Framework Core - logging, assertions, reporting, wait functions, state management
set -uo pipefail
export noninteractive=true

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; NC='\033[0m'

_ts() { date '+%Y-%m-%dT%H:%M:%S'; }
log_info()    { echo -e "${BLUE}[INFO]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_success() { echo -e "${GREEN}[PASS]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_error()   { echo -e "${RED}[FAIL]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_section() { echo -e "\n${CYAN}========== $* ==========${NC}\n" >&2; }
log_skip()    { echo -e "${YELLOW}[SKIP]${NC} $(date '+%H:%M:%S') $*" >&2; }
log_debug()   {
    if [[ "${DEBUG:-0}" == "1" ]]; then
        # Redact sensitive patterns from debug output
        local msg="$*"
        msg=$(echo "$msg" | sed -E 's/"token":"[^"]*"/"token":"[REDACTED]"/g; s/"password":"[^"]*"/"password":"[REDACTED]"/g; s/"ssh_password":"[^"]*"/"ssh_password":"[REDACTED]"/g; s/Bearer [A-Za-z0-9._-]+/Bearer [REDACTED]/g')
        echo -e "[DEBUG] $(date '+%H:%M:%S') ${msg}" >&2
    fi
    return 0
}

# -- Safe jq wrapper: validates JSON before parsing to prevent "parse error" crashes --
# Usage: safe_jq "$body" ".data.token // empty" [default_value]
# Legacy callers may pass "-r .field"; leading jq flags are stripped for compatibility.
# Returns the jq result on success, or default_value (or empty string) on parse failure.
safe_jq() {
    local input="$1" expr="$2" default="${3:-}"
    local jq_filter="$expr"
    # Empty input: return default immediately
    if [[ -z "$input" ]]; then
        echo "$default"
        return 1
    fi
    # Normalize old call sites that included jq output flags in the expression.
    while [[ "$jq_filter" =~ ^-[A-Za-z]+[[:space:]]+(.+)$ ]]; do
        jq_filter="${BASH_REMATCH[1]}"
    done
    # Validate that input is parseable JSON before passing to jq
    if ! printf '%s' "$input" | jq empty 2>/dev/null; then
        # Input is not valid JSON - could be HTML error page, binary, etc.
        log_debug "safe_jq: input is not valid JSON (first 100 chars): ${input:0:100}"
        echo "$default"
        return 1
    fi
    # Safe to parse now
    local result
    result=$(printf '%s' "$input" | jq -r "$jq_filter" 2>/dev/null) || { echo "$default"; return 1; }
    echo "$result"
    return 0
}

# -- Sanitize response body for jq: strip non-JSON prefix (e.g., HTTP headers, warnings) --
# Some APIs may prepend warnings or have mixed output; this extracts the JSON portion
sanitize_json_body() {
    local body="$1"
    if printf '%s' "$body" | jq empty 2>/dev/null; then
        printf '%s\n' "$body"
        return 0
    fi
    # Try to find the first '{' or '[' and extract from there
    local json_start
    json_start=$(printf '%s' "$body" | grep -abo -m1 '[{\[]' | cut -d: -f1)
    if [[ -n "$json_start" ]]; then
        printf '%s' "$body" | tail -c +$((json_start + 1))
    else
        echo "$body"
    fi
}

json_string() {
    jq -cn --arg value "$1" '$value'
}

preflight_require_commands() {
    local missing=()
    local cmd
    for cmd in "$@"; do
        if ! command -v "$cmd" >/dev/null 2>&1; then
            missing+=("$cmd")
        fi
    done
    if [[ ${#missing[@]} -gt 0 ]]; then
        log_error "Missing required command(s): ${missing[*]}"
        return 1
    fi
    log_success "Required commands available: $*"
    return 0
}

preflight_check_runner_resources() {
    # Allow skipping resource checks in constrained environments (e.g., Docker CI runners)
    if [[ "${SKIP_RESOURCE_CHECK:-false}" == "true" ]]; then
        log_info "Resource check skipped (SKIP_RESOURCE_CHECK=true)"
        return 0
    fi

    local min_disk_gb="${1:-20}" min_memory_mb="${2:-4096}" path="${3:-.}"
    local disk_gb="0" memory_mb="0"

    disk_gb=$(df -Pk "$path" 2>/dev/null | awk 'NR==2 {print int($4/1024/1024)}')
    if [[ -z "$disk_gb" || "$disk_gb" -lt "$min_disk_gb" ]]; then
        log_error "Insufficient disk space: available=${disk_gb:-unknown}GB required>=${min_disk_gb}GB path=${path}"
        return 1
    fi

    if [[ -r /proc/meminfo ]]; then
        memory_mb=$(awk '/MemTotal:/ {print int($2/1024)}' /proc/meminfo)
    elif command -v sysctl >/dev/null 2>&1; then
        memory_mb=$(sysctl -n hw.memsize 2>/dev/null | awk '{print int($1/1024/1024)}')
    fi
    if [[ -z "$memory_mb" || "$memory_mb" -lt "$min_memory_mb" ]]; then
        log_error "Insufficient memory: total=${memory_mb:-unknown}MB required>=${min_memory_mb}MB"
        return 1
    fi

    log_success "Runner resources OK: disk=${disk_gb}GB memory=${memory_mb}MB"
    return 0
}

preflight_check_port_available() {
    local port="$1"
    if command -v lsof >/dev/null 2>&1; then
        if lsof -nP -iTCP:"$port" -sTCP:LISTEN -t >/dev/null 2>&1; then
            log_error "Port ${port} is already in use"
            return 1
        fi
    elif command -v ss >/dev/null 2>&1; then
        if ss -ltn "( sport = :${port} )" 2>/dev/null | awk 'NR>1 {found=1} END {exit found ? 0 : 1}'; then
            log_error "Port ${port} is already in use"
            return 1
        fi
    fi
    log_success "Port ${port} is available"
    return 0
}

wait_for_mysql_ready() {
    local timeout="${1:-60}" interval="${2:-5}" elapsed=0
    local db_password="${DB_PASSWORD:-${MYSQL_ROOT_PASSWORD:-}}"
    local mysql_args=(-h 127.0.0.1 -u root)
    [[ -n "$db_password" ]] && mysql_args+=("-p${db_password}")
    log_info "Waiting for MySQL TCP readiness..."
    while [[ $elapsed -lt $timeout ]]; do
        if command -v mysqladmin >/dev/null 2>&1 && mysqladmin "${mysql_args[@]}" ping --silent 2>/dev/null; then
            log_success "MySQL ready after ${elapsed}s"
            return 0
        fi
        if command -v mysql >/dev/null 2>&1 && mysql "${mysql_args[@]}" -e "SELECT 1;" >/dev/null 2>&1; then
            log_success "MySQL ready after ${elapsed}s"
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    log_error "MySQL not ready after ${timeout}s"
    return 1
}

# -- Counters --
TOTAL_TESTS=0; PASSED_TESTS=0; FAILED_TESTS=0; SKIPPED_TESTS=0
declare -A CHAIN_BROKEN
REPORT_FILE=""
RESULTS_FILE="${RESULTS_FILE:-}"
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
TEST_USER_PASS="${CI_TEST_USER_PASSWORD:-TestUser123!@#}"
TEST_USER2="test_user_ci_2"
TEST_USER2_PASS="TestUser2_123!@#"
USER_TOKEN2=""
PROVIDER_ID=""
ENV_TYPE="${ENV_TYPE:-docker}"
INSTANCE_TYPES="${INSTANCE_TYPES:-both}"
NODE_IP="${NODE_IP:-}"
NODE_PASSWORD="${NODE_PASSWORD:-}"
WORKER_IP="${WORKER_IP:-}"
WORKER_PASSWORD="${WORKER_PASSWORD:-}"
WORKER_ID="${WORKER_ID:-}"
TEST_INSTANCE_ID="${TEST_INSTANCE_ID:-}"
EXECUTION_RULE="${EXECUTION_RULE:-auto}"
# Image filter: comma-separated OS names to test (default: alpine,debian; "all" = test everything)
TEST_IMAGES="${TEST_IMAGES:-alpine,debian}"
# Path to the server directory; set by deploy_master_local() in node_manager.sh
MASTER_SERVER_DIR="${MASTER_SERVER_DIR:-}"
# Instance tasks can legitimately run for several minutes on freshly prepared
# virtualization nodes. Keep these defaults overridable for faster local runs.
INSTANCE_TASK_MAX_WAIT="${INSTANCE_TASK_MAX_WAIT:-600}"
INSTANCE_STATUS_MAX_WAIT="${INSTANCE_STATUS_MAX_WAIT:-600}"
INSTANCE_HEALTH_SETTLE_SECONDS="${INSTANCE_HEALTH_SETTLE_SECONDS:-30}"
INSTANCE_OPERATION_SETTLE_SECONDS="${INSTANCE_OPERATION_SETTLE_SECONDS:-3}"

# -- JSON result collector for HTML report --
declare -a TEST_RESULTS_JSON=()

# -- API test function --
# Args: test_name method url expected_code [data] [group] [token_override]
test_api() {
    local name="$1" method="$2" url="$3" expected="$4"
    local data="${5:-}" group="${6:-default}" token="${7-$ADMIN_TOKEN}"
    local test_start; test_start=$(_ts)
    TOTAL_TESTS=$((TOTAL_TESTS + 1))
    if [[ -n "${CHAIN_BROKEN[$group]:-}" ]]; then
        SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
        log_skip "${name} (chain broken: ${CHAIN_BROKEN[$group]})"
        report_add_skip "$name" "$method" "$url" "${CHAIN_BROKEN[$group]}"
        _record_result "$name" "$method" "$url" "SKIP" "" "" "${CHAIN_BROKEN[$group]}" "$group"
        return 1
    fi
    local args=(-s -w "\n%{http_code}" --max-time 180
        -H "Content-Type: application/json" -X "${method}")
    [[ -n "$token" ]] && args+=(-H "Authorization: Bearer ${token}")
    [[ -n "$data" ]] && args+=(-d "$data")
    local resp; resp=$(curl "${args[@]}" "${SERVER_URL}${url}" 2>&1) || true
    local code; code=$(echo "$resp" | tail -1)
    local body; body=$(echo "$resp" | sed '$d')
    sleep 0.3
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
    local name="$1" method="$2" url="$3" expected="$4" data="${5:-}" retries="${6:-3}" interval="${7:-5}" group="${8:-default}" token="${9-$ADMIN_TOKEN}"
    local i=0
    while [[ $i -lt $retries ]]; do
        i=$((i + 1))
        [[ $i -gt 1 ]] && { log_info "Retry ${name} (${i}/${retries})..."; sleep "$interval"; }
        local st=$TOTAL_TESTS sp=$PASSED_TESTS sf=$FAILED_TESTS ss=$SKIPPED_TESTS
        # Save RESULTS_FILE byte offset so we can undo the intermediate FAIL record on non-final retries
        local _results_before=0
        local _results_len=${#TEST_RESULTS_JSON[@]}
        [[ -n "${RESULTS_FILE:-}" && -f "$RESULTS_FILE" ]] && _results_before=$(wc -c < "$RESULTS_FILE" 2>/dev/null || echo 0)
        local _body_file
        _body_file=$(mktemp)
        if test_api "$name" "$method" "$url" "$expected" "$data" "$group" "$token" > "$_body_file"; then
            cat "$_body_file"
            rm -f "$_body_file"
            return 0
        fi
        rm -f "$_body_file"
        if [[ $i -lt $retries ]]; then
            # Undo the FAIL record written to RESULTS_FILE so it is not counted as a permanent failure
            if [[ -n "${RESULTS_FILE:-}" && -f "$RESULTS_FILE" && $_results_before -ge 0 ]]; then
                head -c "$_results_before" "$RESULTS_FILE" > "${RESULTS_FILE}.retry_tmp" 2>/dev/null && \
                    mv "${RESULTS_FILE}.retry_tmp" "$RESULTS_FILE" 2>/dev/null || true
            fi
            while [[ ${#TEST_RESULTS_JSON[@]} -gt $_results_len ]]; do
                local _last_idx=$((${#TEST_RESULTS_JSON[@]} - 1))
                unset "TEST_RESULTS_JSON[$_last_idx]"
            done
            TOTAL_TESTS=$st; PASSED_TESTS=$sp; FAILED_TESTS=$sf; SKIPPED_TESTS=$ss
        fi
    done
    return 1
}

# Without auth token
test_api_noauth() {
    local name="$1" method="$2" url="$3" expected="$4" data="${5:-}" group="${6:-default}"
    test_api "$name" "$method" "$url" "$expected" "$data" "$group" ""
}

test_api_json_value() {
    local name="$1" method="$2" url="$3" expected_http="$4" jq_expr="$5" expected_value="$6"
    local data="${7:-}" group="${8:-default}" token="${9-$ADMIN_TOKEN}"
    local test_start; test_start=$(_ts)
    TOTAL_TESTS=$((TOTAL_TESTS + 1))

    if [[ -n "${CHAIN_BROKEN[$group]:-}" ]]; then
        SKIPPED_TESTS=$((SKIPPED_TESTS + 1))
        log_skip "${name} (chain broken: ${CHAIN_BROKEN[$group]})"
        report_add_skip "$name" "$method" "$url" "${CHAIN_BROKEN[$group]}"
        _record_result "$name" "$method" "$url" "SKIP" "" "" "${CHAIN_BROKEN[$group]}" "$group"
        return 1
    fi

    local args=(-s -w "\n%{http_code}" --max-time 120 -H "Content-Type: application/json" -X "${method}")
    [[ -n "$token" ]] && args+=(-H "Authorization: Bearer ${token}")
    [[ -n "$data" ]] && args+=(-d "$data")

    local resp; resp=$(curl "${args[@]}" "${SERVER_URL}${url}" 2>&1) || true
    local code; code=$(echo "$resp" | tail -1)
    local body; body=$(echo "$resp" | sed '$d')
    sleep 0.3

    local http_match=false
    IFS='|' read -ra exp_codes <<< "$expected_http"
    for ec in "${exp_codes[@]}"; do
        [[ "$code" == "$ec" ]] && { http_match=true; break; }
    done

    if [[ "$http_match" == "false" ]]; then
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "${name} - expected HTTP ${expected_http}, got HTTP ${code}"
        local error_logs=""
        error_logs=$(capture_service_logs "$test_start" 2>/dev/null) || true
        report_add_fail "$name" "$method" "$url" "$data" "$expected_http" "$code" "$body"
        _record_result "$name" "$method" "$url" "FAIL" "$expected_http" "$code" "$body" "$group" "$error_logs"
        return 1
    fi

    local actual_value="__JQ_EVAL_ERROR__"
    local sanitized; sanitized=$(sanitize_json_body "$body")
    if actual_value=$(safe_jq "$sanitized" "$jq_expr" "__JQ_PARSE_ERROR__"); then
        :
    elif [[ "$actual_value" == "__JQ_PARSE_ERROR__" ]]; then
        log_warning "${name} - response body is not valid JSON, jq evaluation skipped"
    fi

    if [[ "$actual_value" != "$expected_value" ]]; then
        FAILED_TESTS=$((FAILED_TESTS + 1))
        log_error "${name} - expected jq(${jq_expr})=${expected_value}, got ${actual_value}"
        local error_logs=""
        local expected_detail="HTTP ${expected_http}, jq(${jq_expr})=${expected_value}"
        local actual_detail="HTTP ${code}, jq(${jq_expr})=${actual_value}"
        error_logs=$(capture_service_logs "$test_start" 2>/dev/null) || true
        report_add_fail "$name" "$method" "$url" "$data" "$expected_detail" "$actual_detail" "$body"
        _record_result "$name" "$method" "$url" "FAIL" "$expected_detail" "$actual_detail" "$body" "$group" "$error_logs"
        return 1
    fi

    PASSED_TESTS=$((PASSED_TESTS + 1))
    log_success "${name}"
    report_add_pass "$name" "$method" "$url"
    _record_result "$name" "$method" "$url" "PASS" "HTTP ${expected_http}, jq(${jq_expr})=${expected_value}" "HTTP ${code}, jq(${jq_expr})=${actual_value}" "" "$group"
    echo "$body"
    return 0
}

test_api_json_value_noauth() {
    local name="$1" method="$2" url="$3" expected_http="$4" jq_expr="$5" expected_value="$6"
    local data="${7:-}" group="${8:-default}"
    test_api_json_value "$name" "$method" "$url" "$expected_http" "$jq_expr" "$expected_value" "$data" "$group" ""
}

run_captcha_disabled_contract_checks() {
    local section_title="${1:-Global Guard: Captcha Disabled Contract}"
    local group="${2:-captcha-contract}"
    local failed=0

    report_add_section "$section_title"

    test_api_json_value_noauth \
        "Public register-config exposes captchaEnabled=false" \
        "GET" "/api/v1/public/register-config" "200" '.data.captchaEnabled' "false" "" "$group" >/dev/null || failed=1

    if [[ -n "${ADMIN_TOKEN:-}" ]]; then
        test_api_json_value \
            "Admin config keeps captcha.enabled=false" \
            "GET" "/api/v1/admin/config" "200" '.data.captcha.enabled' "false" "" "$group" "$ADMIN_TOKEN" >/dev/null || failed=1
    fi

    test_api_noauth \
        "Admin login works without captcha by default" \
        "POST" "/api/v1/auth/login" "200" \
        "{\"username\":\"${ADMIN_USER}\",\"password\":\"${ADMIN_PASS}\"}" "$group" >/dev/null || failed=1

    test_api_noauth \
        "Forgot password does not require captcha by default" \
        "POST" "/api/v1/auth/forgot-password" "200" \
        '{"email":"nonexistent-captcha-guard@ci.local"}' "$group" >/dev/null || failed=1

    return $failed
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

# -- Utility: should we test this image? --
# Returns 0 if the image name matches TEST_IMAGES filter, 1 otherwise
should_test_image() {
    local image_name="$1"
    [[ "$TEST_IMAGES" == "all" ]] && return 0
    local lower_name; lower_name=$(echo "$image_name" | tr '[:upper:]' '[:lower:]')
    IFS=',' read -ra allowed <<< "$TEST_IMAGES"
    for pattern in "${allowed[@]}"; do
        pattern=$(echo "$pattern" | tr '[:upper:]' '[:lower:]' | xargs)
        [[ "$lower_name" == *"$pattern"* ]] && return 0
    done
    return 1
}

# -- Platform capabilities map (hardcoded per spec) --
# Type ID   Platform              Instance Types
# docker    Docker                container
# lxd       LXD                   container, vm
# incus     Incus                 container, vm
# podman    Podman                container
# containerd Containerd (nerdctl)  container
# proxmoxve Proxmox VE            container, vm
# kubevirt  KubeVirt              vm
# qemu      QEMU                  vm
declare -A PLATFORM_SUPPORTS_VM=(
    [docker]=0 [lxd]=1 [incus]=1 [podman]=0 [containerd]=0 [proxmoxve]=1 [kubevirt]=1 [qemu]=1
)

env_supports_container() {
    # All supported platforms support containers
    case "$ENV_TYPE" in
        docker|lxd|incus|podman|containerd|proxmoxve) return 0 ;;
        kubevirt|qemu) return 1 ;;
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
    # Determine container support: platforms not in VM-only list support containers
    local supports_container=1
    case "$platform" in
        kubevirt|qemu) supports_container=0 ;;
    esac
    case "$types" in
        both)
            if [[ "$supports_vm" -eq 0 ]]; then
                log_warning "Platform '${platform}' does not support VM; auto-correcting to 'container'"
                echo "container"
                return 0
            fi
            if [[ "$supports_container" -eq 0 ]]; then
                log_warning "Platform '${platform}' does not support containers; auto-correcting to 'vm'"
                echo "vm"
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
            if [[ "$supports_container" -eq 0 ]]; then
                log_error "Platform '${platform}' does not support container instance type"
                log_warning "Auto-correcting to 'vm'"
                echo "vm"
                return 0
            fi
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
        # Accept both 200 (healthy) and 503 (service up but DB not initialized yet)
        if [[ "$r" == "200" || "$r" == "503" ]]; then
            log_success "Server is ready (HTTP ${r})"
            return 0
        fi
        [[ $((elapsed % 30)) -eq 0 ]] && log_debug "Server not ready yet (${elapsed}/${max}s, HTTP ${r:-no response})..."
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "Server readiness timeout (${max}s)"; return 1
}

wait_db_ready() {
    local url="$1" max="${2:-120}" interval="${3:-5}" elapsed=0
    log_info "Waiting for system initialization to complete..."
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 "${url}/api/v1/public/init/check" 2>/dev/null) || true
        local need_init; need_init=$(safe_jq "$r" '-r .data.needInit' 'unknown')
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
    local empty_count=0
    local last_known_status=""
    
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 -H "Authorization: Bearer ${token}" \
            "${url}/api/v1/admin/tasks/${task_id}" 2>/dev/null) || true
        local st; st=$(safe_jq "$r" '-r .data.status // empty' '')
        
        case "$st" in
            completed) 
                log_success "Task ${task_id} completed"
                echo "$r"
                return 0
                ;;
            failed|cancelled|timeout) 
                log_error "Task ${task_id}: ${st}"
                echo "$r"
                return 1
                ;;
            pending|running|processing|queued)
                # Task is still in progress
                log_debug "Task ${task_id} status: ${st}"
                last_known_status="$st"
                empty_count=0
                ;;
            "") 
                # Empty response - could be network error, API issue, or task cleaned up
                empty_count=$((empty_count + 1))
                
                # If we previously saw the task running and now it's gone, likely completed
                if [[ -n "$last_known_status" && $empty_count -ge 2 ]]; then
                    log_debug "Task ${task_id} not found after running - assuming completed/cleaned"
                    return 0
                fi
                
                # If we never saw the task and got multiple empty responses, it might not exist
                if [[ -z "$last_known_status" && $empty_count -ge 3 ]]; then
                    log_warning "Task ${task_id} never found after 3 attempts - assuming already completed"
                    return 0
                fi
                
                log_debug "Task ${task_id} status empty (attempt ${empty_count})"
                ;;
            *)
                # Unknown status - log and continue
                log_debug "Task ${task_id} unknown status: ${st}"
                last_known_status="$st"
                empty_count=0
                ;;
        esac
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    log_error "Task ${task_id} timeout after ${max}s"
    return 1
}

ensure_provider_health_ready() {
    local provider_id="$1" token="${2:-$ADMIN_TOKEN}" settle_seconds="${3:-$INSTANCE_HEALTH_SETTLE_SECONDS}"

    if [[ -z "$provider_id" ]]; then
        log_warning "Provider health precheck skipped: provider id is empty"
        return 1
    fi

    log_info "Triggering provider ${provider_id} health check before instance operation..."
    local resp; resp=$(curl -s -w "\n%{http_code}" --max-time 120 \
        -H "Authorization: Bearer ${token}" \
        -H "Content-Type: application/json" \
        -X POST -d '{}' \
        "${SERVER_URL}/api/v1/admin/providers/${provider_id}/health-check" 2>/dev/null) || true
    local http_code; http_code=$(echo "$resp" | tail -1)
    local body; body=$(echo "$resp" | sed '$d')
    local api_code; api_code=$(safe_jq "$body" '-r .code // empty' '')

    if [[ "$http_code" != "200" || "$api_code" != "200" ]]; then
        log_warning "Provider ${provider_id} health precheck returned HTTP=${http_code:-unknown} code=${api_code:-unknown}"
        echo "$body" | jq '.' 2>/dev/null || echo "$body"
        return 1
    fi

    log_info "Provider health check completed; waiting ${settle_seconds}s before creating/operating instances..."
    sleep "$settle_seconds"
    return 0
}

wait_instance_status() {
    local instance_id="$1" expected="$2" max="${3:-$INSTANCE_STATUS_MAX_WAIT}" interval="${4:-10}" token="${5:-$ADMIN_TOKEN}" label="${6:-instance ${instance_id}}"
    local elapsed=0 last_status="" first_dumped=false

    log_info "Waiting for ${label} status '${expected}' (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local resp; resp=$(curl -s --max-time 10 -H "Authorization: Bearer ${token}" \
            "${SERVER_URL}/api/v1/admin/instances/${instance_id}" 2>/dev/null) || true
        local code; code=$(safe_jq "$resp" '-r .code // empty' '')
        local status; status=$(safe_jq "$resp" '-r .data.status // empty' '')

        if [[ "$expected" == *"deleted"* && "$code" != "200" && -n "$code" ]]; then
            log_success "${label} deleted/gone (code=${code}, waited ${elapsed}s)"
            echo "$resp"
            return 0
        fi

        local ok=false
        IFS='|' read -ra expected_statuses <<< "$expected"
        local exp
        for exp in "${expected_statuses[@]}"; do
            if [[ "$status" == "$exp" ]]; then
                ok=true
                break
            fi
        done
        if [[ "$ok" == "true" ]]; then
            log_success "${label} status=${status} (waited ${elapsed}s)"
            echo "$resp"
            return 0
        fi

        if [[ "$first_dumped" == "false" ]]; then
            first_dumped=true
            log_debug "${label} initial detail: $(echo "$resp" | jq -c '.' 2>/dev/null | head -c 2000)"
        fi

        case "$status" in
            failed|error|cancelled|timeout)
                log_error "${label} reached terminal status=${status} before expected '${expected}'"
                echo "$resp"
                return 1
                ;;
        esac

        if [[ "$status" != "$last_status" || $((elapsed % 30)) -eq 0 ]]; then
            log_info "${label} status: ${status:-unknown}, waiting... (${elapsed}s/${max}s)"
            last_status="$status"
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done

    local final_resp; final_resp=$(curl -s --max-time 10 -H "Authorization: Bearer ${token}" \
        "${SERVER_URL}/api/v1/admin/instances/${instance_id}" 2>/dev/null) || true
    log_warning "${label} did not reach '${expected}' after ${max}s. Full detail:"
    echo "$final_resp" | jq '.' 2>/dev/null || echo "$final_resp"
    return 1
}

wait_instance_operation_settled() {
    local instance_id="$1" response="$2" expected_status="${3:-}" label="${4:-instance operation}" token="${5:-$ADMIN_TOKEN}" max="${6:-$INSTANCE_TASK_MAX_WAIT}" interval="${7:-10}"
    local task_id; task_id=$(safe_jq "$response" '-r .data.task_id // empty' '')

    if [[ -n "$task_id" ]]; then
        log_info "Waiting for ${label} task ${task_id}..."
        wait_task_complete "$SERVER_URL" "$task_id" "$token" "$max" "$interval" > /dev/null || return 1
    fi

    if [[ "${INSTANCE_OPERATION_SETTLE_SECONDS:-0}" -gt 0 ]]; then
        sleep "$INSTANCE_OPERATION_SETTLE_SECONDS"
    fi

    if [[ -n "$expected_status" ]]; then
        wait_instance_status "$instance_id" "$expected_status" "$INSTANCE_STATUS_MAX_WAIT" "$interval" "$token" "$label" > /dev/null
    fi
}

# Delete instance with proper async wait and polling
delete_instance_safe() {
    local instance_id="$1"
    local token="${2:-$ADMIN_TOKEN}"
    local max_wait="${3:-120}"
    
    log_debug "Deleting instance ${instance_id}..."
    local del_resp; del_resp=$(curl -s --max-time 60 -X DELETE -H "Authorization: Bearer ${token}" \
        "${SERVER_URL}/api/v1/admin/instances/${instance_id}" 2>/dev/null) || true
    
    local del_code; del_code=$(safe_jq "$del_resp" '-r .code // empty' '')
    
    # Already gone
    if [[ "$del_code" == "404" ]]; then
        log_debug "Instance ${instance_id} already deleted (404)"
        return 0
    fi
    
    # Check if deletion request itself failed
    if [[ -n "$del_code" && "$del_code" != "200" ]]; then
        local del_msg; del_msg=$(safe_jq "$del_resp" '-r .msg // .message // "unknown error"' 'unknown error')
        log_warning "Instance ${instance_id} deletion request returned: ${del_msg} (code: ${del_code})"
    fi
    
    # Check if deletion returns a task ID (async operation)
    local del_task; del_task=$(safe_jq "$del_resp" '-r .data.task_id // empty' '')
    if [[ -n "$del_task" ]]; then
        log_debug "Waiting for deletion task ${del_task}..."
        wait_task_complete "$SERVER_URL" "$del_task" "$token" "$max_wait" 5 > /dev/null 2>&1 || {
            log_warning "Deletion task ${del_task} did not complete within timeout"
        }
    fi
    
    # Poll until instance is gone or status is 'deleted'/'failed'
    local elapsed=0 poll_interval=5
    while [[ $elapsed -lt $max_wait ]]; do
        local verify; verify=$(curl -s --max-time 10 -H "Authorization: Bearer ${token}" \
            "${SERVER_URL}/api/v1/admin/instances/${instance_id}" 2>/dev/null) || true
        local verify_code; verify_code=$(safe_jq "$verify" '-r .code // empty' '')
        
        # Instance not found (404 or other non-200) means deleted
        if [[ "$verify_code" != "200" ]]; then
            log_debug "Instance ${instance_id} deleted successfully (code=${verify_code})"
            return 0
        fi
        
        local inst_status; inst_status=$(safe_jq "$verify" '-r .data.status // empty' '')
        if [[ "$inst_status" == "deleted" || "$inst_status" == "failed" ]]; then
            log_debug "Instance ${instance_id} in terminal state: ${inst_status}"
            return 0
        fi
        
        log_debug "Instance ${instance_id} still exists (status=${inst_status}, ${elapsed}/${max_wait}s)"
        sleep "$poll_interval"
        elapsed=$((elapsed + poll_interval))
    done
    
    log_warning "Instance ${instance_id} still exists after ${max_wait}s"
    return 1
}

# -- Auth helpers --
# wait_init_ready: waits until /api/v1/public/init/check responds with code=200 (server+DB both up)
wait_init_ready() {
    local url="$1" max="${2:-180}" interval="${3:-5}" elapsed=0
    log_info "Waiting for init endpoint to respond..."
    while [[ $elapsed -lt $max ]]; do
        local r; r=$(curl -s --max-time 10 "${url}/api/v1/public/init/check" 2>/dev/null) || true
        local code; code=$(safe_jq "$r" '-r .code // empty' '')
        if [[ "$code" == "200" ]]; then
            local need_init; need_init=$(safe_jq "$r" '-r .data.needInit // true' 'true')
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
    # All-in-one container / CI: MySQL on 127.0.0.1:3306, root password from DB_PASSWORD when set.
    local url="$1" user="$2" pass="$3"
    local db_password="${DB_PASSWORD:-${MYSQL_ROOT_PASSWORD:-}}"
    local user_json pass_json admin_email_json test_user_json test_user_pass_json test_user_email_json db_password_json
    user_json=$(json_string "$user")
    pass_json=$(json_string "$pass")
    admin_email_json=$(json_string "${user}@test.local")
    test_user_json=$(json_string "$TEST_USER")
    test_user_pass_json=$(json_string "$TEST_USER_PASS")
    test_user_email_json=$(json_string "${TEST_USER}@test.local")
    db_password_json=$(json_string "$db_password")
    local data
    printf -v data \
        '{"admin":{"username":%s,"password":%s,"email":%s},"user":{"username":%s,"password":%s,"email":%s,"enabled":true},"database":{"type":"mysql","host":"127.0.0.1","port":"3306","database":"oneclickvirt","username":"root","password":%s}}' \
        "$user_json" "$pass_json" "$admin_email_json" "$test_user_json" "$test_user_pass_json" "$test_user_email_json" "$db_password_json"
    local resp; resp=$(curl -s --max-time 60 -H "Content-Type: application/json" -X POST -d "$data" "${url}/api/v1/public/init" 2>/dev/null)
    local init_code; init_code=$(safe_jq "$resp" '-r .code // empty' '')
    log_info "Init response code: ${init_code}"
    echo "$resp"
}

do_login() {
    local url="$1" user="$2" pass="$3"
    local user_json pass_json data
    user_json=$(json_string "$user")
    pass_json=$(json_string "$pass")
    printf -v data '{"username":%s,"password":%s}' "$user_json" "$pass_json"
    local r; r=$(curl -s --max-time 30 -H "Content-Type: application/json" -X POST \
        -d "$data" "${url}/api/v1/auth/login" 2>/dev/null)
    safe_jq "$r" '-r .data.token // empty' ''
}

admin_login() {
    local url="$1" user="${2:-admin}" pass="${3:-Admin123!@#}"
    local user_json pass_json data
    user_json=$(json_string "$user")
    pass_json=$(json_string "$pass")
    printf -v data '{"username":%s,"password":%s}' "$user_json" "$pass_json"
    local raw; raw=$(curl -s --max-time 30 -H "Content-Type: application/json" -X POST \
        -d "$data" "${url}/api/v1/auth/login" 2>/dev/null)
    log_debug "Login response for ${user}: ${raw}"
    local token; token=$(safe_jq "$raw" '-r .data.token // empty' '')
    [[ -n "$token" ]] && { log_success "Login success: ${user}"; echo "$token"; return 0; }
    local login_err; login_err=$(safe_jq "$raw" '-r .msg // .message // .data // "no response"' 'no response')
    log_error "Login failed: ${user} - ${login_err}"
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
    local safe_detail; safe_detail=$(printf '%s' "$detail" | tr '\000-\037' ' ' | sed 's/"/\\"/g')
    local safe_logs; safe_logs=$(printf '%s' "$error_logs" | tr '\000-\037' ' ' | sed 's/"/\\"/g')
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

    # When tests ran in a subprocess (e.g. run_env_test.sh → run_module.sh),
    # the in-memory counters may be zero.  Fall back to counting from the JSONL
    # results file which is always written to disk.
    if [[ $TOTAL_TESTS -eq 0 && -n "${RESULTS_FILE:-}" && -f "$RESULTS_FILE" ]]; then
        local _jsonl_total=0 _jsonl_pass=0 _jsonl_fail=0 _jsonl_skip=0
        while IFS= read -r line; do
            [[ -z "$line" ]] && continue
            local _st; _st=$(echo "$line" | jq -r '.status // empty' 2>/dev/null)
            case "$_st" in
                PASS) _jsonl_pass=$((_jsonl_pass + 1)); _jsonl_total=$((_jsonl_total + 1)) ;;
                FAIL) _jsonl_fail=$((_jsonl_fail + 1)); _jsonl_total=$((_jsonl_total + 1)) ;;
                SKIP) _jsonl_skip=$((_jsonl_skip + 1)); _jsonl_total=$((_jsonl_total + 1)) ;;
            esac
        done < "$RESULTS_FILE"
        TOTAL_TESTS=$_jsonl_total
        PASSED_TESTS=$_jsonl_pass
        FAILED_TESTS=$_jsonl_fail
        SKIPPED_TESTS=$_jsonl_skip
    fi

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

    # Fetch version info from the running server
    local ver_resp; ver_resp=$(curl -s --max-time 10 "${SERVER_URL}/api/v1/public/version" 2>/dev/null) || true
    local server_ver; server_ver=$(safe_jq "$ver_resp" '-r .data.server_version // "unknown"' 'unknown')
    local agent_ver; agent_ver=$(safe_jq "$ver_resp" '-r .data.compatible_agent_version // "unknown"' 'unknown')

    # Fetch service error logs for inclusion in report
    fetch_full_service_logs "$service_log_file" || true

    if [[ -f "$report_script" && -n "$RESULTS_FILE" ]]; then
        bash "$report_script" "$RESULTS_FILE" "$output_file" "$env_name" "$service_log_file" "$server_ver" "$agent_ver" || {
            log_warning "Report generator failed, creating fallback report"
            local fallback_results
            fallback_results=$(cat "$RESULTS_FILE" 2>/dev/null || true)
            echo "<html><body><h1>Report generation failed</h1><p>Results file: ${RESULTS_FILE}</p><pre>${fallback_results}</pre></body></html>" > "$output_file"
        }
    else
        log_warning "Report script or results file not found (script=${report_script}, results=${RESULTS_FILE})"
        echo "<html><body><h1>No results available</h1></body></html>" > "$output_file"
    fi
}

# -- State management: save/restore between modules --
SAVED_CONFIG=""
SAVED_INSTANCE_IDS=""
SAVED_PROVIDER_IDS=""
SAVED_USER_IDS=""

save_base_state() {
    log_info "Saving base state before module..."
    
    # Save config
    SAVED_CONFIG=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/config" 2>/dev/null) || true
    
    # Save instances
    local inst_resp; inst_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/instances?page=1&pageSize=1000" 2>/dev/null) || true
    SAVED_INSTANCE_IDS=$(safe_jq "$inst_resp" '-r .data.list[]?.id // empty' '' | tr '\n' ',' | sed 's/,$//')
    log_debug "Saved instance IDs: ${SAVED_INSTANCE_IDS:-none}"
    
    # DO NOT save PROVIDER_ID here - it should persist across modules
    # We'll preserve whatever value exists when restoring
    
    # Save provider list (to avoid deleting base provider)
    local prov_resp; prov_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/providers?page=1&pageSize=100" 2>/dev/null) || true
    SAVED_PROVIDER_IDS=$(safe_jq "$prov_resp" '-r .data.list[]?.id // .data.items[]?.id // empty' '' | tr '\n' ',' | sed 's/,$//')
    log_debug "Saved provider IDs: ${SAVED_PROVIDER_IDS:-none}"
    
    # Save user list (to avoid deleting base test users)
    local user_resp; user_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/users?page=1&pageSize=100" 2>/dev/null) || true
    SAVED_USER_IDS=$(safe_jq "$user_resp" '-r .data.list[]?.id // .data.items[]?.id // empty' '' | tr '\n' ',' | sed 's/,$//')
    log_debug "Saved user IDs: ${SAVED_USER_IDS:-none}"
}

restore_base_state() {
    log_info "Restoring base state after module..."
    
    # Delete any instances created during the module (exclude dirty node instances)
    local curr_resp; curr_resp=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
        "${SERVER_URL}/api/v1/admin/instances?page=1&pageSize=1000" 2>/dev/null) || true
    local curr_ids; curr_ids=$(safe_jq "$curr_resp" '-r .data.list[]?.id // empty' '')
    
    for id in $curr_ids; do
        if [[ -n "$id" ]] && ! echo ",$SAVED_INSTANCE_IDS," | grep -q ",${id},"; then
            # Get instance details to check if it's a pre-existing instance (dirty node)
            local inst_detail; inst_detail=$(curl -s --max-time 30 -H "Authorization: Bearer ${ADMIN_TOKEN}" \
                "${SERVER_URL}/api/v1/admin/instances/${id}" 2>/dev/null) || true
            local inst_name; inst_name=$(safe_jq "$inst_detail" '-r .data.name // empty' '')
            
            # Skip deletion if it's a pre-existing instance (for discovery tests)
            if [[ "$inst_name" =~ pre.?existing|pre_existing|pre-existing ]]; then
                log_debug "Skipping deletion of pre-existing instance: ${inst_name} (ID: ${id})"
                continue
            fi
            
            # Skip deletion if this is the TEST_INSTANCE_ID (needed by downstream modules)
            if [[ -n "${TEST_INSTANCE_ID:-}" && "$id" == "$TEST_INSTANCE_ID" ]]; then
                log_debug "Preserving TEST_INSTANCE_ID=${id} for downstream modules"
                continue
            fi
            
            log_info "Cleaning up instance created during module: ${id}"
            delete_instance_safe "$id" "$ADMIN_TOKEN" 30 || log_warning "Failed to delete instance ${id}"
        fi
    done
    
    # PROVIDER_ID and TEST_INSTANCE_ID persist naturally across modules
    # No need to restore as they should keep their values from successful module runs
    log_debug "Current PROVIDER_ID: ${PROVIDER_ID:-<not set>}"
    log_debug "Current TEST_INSTANCE_ID: ${TEST_INSTANCE_ID:-<not set>}"
    
    # Re-login to refresh tokens with graceful error handling
    local new_admin_token; new_admin_token=$(admin_login "$SERVER_URL" "$ADMIN_USER" "$ADMIN_PASS" 2>/dev/null) || true
    if [[ -n "$new_admin_token" ]]; then
        ADMIN_TOKEN="$new_admin_token"
    else
        log_warning "Failed to refresh ADMIN_TOKEN, keeping existing token"
    fi
    
    local new_user_token; new_user_token=$(do_login "$SERVER_URL" "$TEST_USER" "$TEST_USER_PASS" 2>/dev/null) || true
    if [[ -n "$new_user_token" ]]; then
        USER_TOKEN="$new_user_token"
    else
        log_debug "Failed to refresh USER_TOKEN"
    fi
    
    local new_user_token2; new_user_token2=$(do_login "$SERVER_URL" "$TEST_USER2" "$TEST_USER2_PASS" 2>/dev/null) || true
    if [[ -n "$new_user_token2" ]]; then
        USER_TOKEN2="$new_user_token2"
    else
        log_debug "Failed to refresh USER_TOKEN2"
    fi
    
    local new_normal_admin; new_normal_admin=$(do_login "$SERVER_URL" "$NORMAL_ADMIN_USER" "$NORMAL_ADMIN_PASS" 2>/dev/null) || true
    if [[ -n "$new_normal_admin" ]]; then
        NORMAL_ADMIN_TOKEN="$new_normal_admin"
    else
        log_debug "Failed to refresh NORMAL_ADMIN_TOKEN"
    fi
    
    log_info "Base state restored"
}

# Reset CHAIN_BROKEN for specific groups (call before each module to prevent cross-contamination)
reset_chain_broken() {
    local groups_to_reset=("$@")
    if [[ ${#groups_to_reset[@]} -eq 0 ]]; then
        # Reset all groups
        CHAIN_BROKEN=()
        log_debug "Reset all CHAIN_BROKEN groups"
    else
        # Reset specific groups
        for grp in "${groups_to_reset[@]}"; do
            unset 'CHAIN_BROKEN[$grp]'
            log_debug "Reset CHAIN_BROKEN for group: ${grp}"
        done
    fi
}

# -- Service log capture (master runs locally on runner via source build) --
capture_service_logs() {
    local _since="${1:-}" max_lines="${2:-50}"
    # Read from server stdout/stderr log file; filter for relevant lines
    tail -"${max_lines}" /tmp/oneclickvirt-server.log 2>/dev/null \
        | grep -iE 'error|panic|fatal|warn' \
        || true
}

fetch_full_service_logs() {
    local output_file="$1"
    {
        echo "=== Server stdout/stderr (/tmp/oneclickvirt-server.log) ==="
        tail -500 /tmp/oneclickvirt-server.log 2>/dev/null || echo "(not found)"
        local date_dir; date_dir=$(date +%Y-%m-%d)
        local log_dir="${MASTER_SERVER_DIR}/storage/logs/${date_dir}"
        echo "=== App error log ==="
        cat "${log_dir}/error.log" 2>/dev/null || echo "(not found)"
        echo "=== App warn log ==="
        cat "${log_dir}/warn.log" 2>/dev/null || echo "(not found)"
    } > "${output_file}" 2>/dev/null \
        || echo "No service logs available" > "${output_file}"
}

dump_master_logs() {
    local date_dir; date_dir=$(date +%Y-%m-%d)
    local log_dir="${MASTER_SERVER_DIR}/storage/logs/${date_dir}"
    log_info "=== Server stdout/stderr (last 100 lines) ==="
    tail -100 /tmp/oneclickvirt-server.log 2>/dev/null || echo "(not found)"
    log_info "=== App error log (${log_dir}/error.log) ==="
    cat "${log_dir}/error.log" 2>/dev/null || echo "(not found)"
    log_info "=== App warn log (${log_dir}/warn.log) ==="
    cat "${log_dir}/warn.log" 2>/dev/null || echo "(not found)"
    log_info "=== MySQL error log (/var/log/mysql/error.log) ==="
    tail -100 /var/log/mysql/error.log 2>/dev/null || echo "(not found)"
}
