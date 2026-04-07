#!/bin/bash
# AliceInit (Ephemera) API wrapper library

ALICE_API_BASE="${ALICE_API_BASE:-https://app.alice.ws/cli/v1}"
ALICE_CLIENT_ID="${ALICE_CLIENT_ID:-}"
ALICE_CLIENT_SECRET="${ALICE_CLIENT_SECRET:-}"
ALICE_PUBLIC_KEY="${ALICE_PUBLIC_KEY:-}"
ALICE_PRIVATE_KEY="${ALICE_PRIVATE_KEY:-}"
# Bearer token is CLIENT_ID:CLIENT_SECRET
ALICEINIT_TOKEN="${ALICE_CLIENT_ID}:${ALICE_CLIENT_SECRET}"
# Temp SSH private key file (set by alice_setup_ssh_key)
_ALICE_SSH_KEY_FILE=""

alice_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${ALICEINIT_TOKEN}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${ALICE_API_BASE}${endpoint}"
}

alice_parse_body() { echo "$1" | sed '$d'; }
alice_parse_code() { echo "$1" | tail -1; }

# ---------- Account / permissions / SSH keys ----------
alice_get_profile()     { alice_request "GET" "/account/profile"; }
alice_get_permissions() { alice_request "GET" "/evo/permissions"; }
alice_get_ssh_keys()    { alice_request "GET" "/account/ssh-keys"; }

# ---------- Plans & OS images (reference: AliceEphemera/src/alice/api.ts) ----------
alice_get_plans()   { alice_request "GET" "/evo/plans"; }
alice_get_plan_os() { alice_request "GET" "/evo/plans/$1/os-images"; }

# ---------- Instance CRUD ----------
alice_list_instances()     { alice_request "GET" "/evo/instances"; }
alice_get_instance_state() { alice_request "GET" "/evo/instances/$1/state"; }
alice_delete_instance()    { alice_request "DELETE" "/evo/instances/$1"; }

alice_create_instance() {
    # product_id os_id hours [ssh_key_id] [boot_script]
    local product_id="$1" os_id="$2" hours="${3:-1}" ssh_key_id="${4:-}" boot_script="${5:-}"
    local data="{\"product_id\":${product_id},\"os_id\":${os_id},\"time\":${hours}"
    [[ -n "$ssh_key_id" ]] && data="${data},\"ssh_key_id\":${ssh_key_id}"
    [[ -n "$boot_script" ]] && data="${data},\"boot_script\":\"${boot_script}\""
    data="${data}}"
    alice_request "POST" "/evo/instances/deploy" "$data"
}

alice_instance_power() {
    alice_request "POST" "/evo/instances/$1/power" "{\"action\":\"$2\"}"
}

# ---------- SSH private-key management ----------
alice_setup_ssh_key() {
    if [[ -z "${ALICE_PRIVATE_KEY:-}" ]]; then
        log_error "ALICE_PRIVATE_KEY is not set - cannot set up SSH authentication"
        return 1
    fi
    _ALICE_SSH_KEY_FILE=$(mktemp /tmp/alice_evo_key_XXXXXX.pem)
    chmod 600 "${_ALICE_SSH_KEY_FILE}"
    printf '%s\n' "${ALICE_PRIVATE_KEY}" > "${_ALICE_SSH_KEY_FILE}"
    log_debug "SSH private key written to ${_ALICE_SSH_KEY_FILE}"
    trap '_alice_cleanup_ssh_key' EXIT
}

_alice_cleanup_ssh_key() {
    [[ -n "${_ALICE_SSH_KEY_FILE:-}" && -f "${_ALICE_SSH_KEY_FILE}" ]] && rm -f "${_ALICE_SSH_KEY_FILE}"
}

# ---------- SSH command execution ----------
# alice_ssh_exec <ip> <command> [timeout_seconds]
# Runs <command> on root@<ip> using the private key set up by alice_setup_ssh_key.
alice_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    if [[ -z "${_ALICE_SSH_KEY_FILE:-}" || ! -f "${_ALICE_SSH_KEY_FILE}" ]]; then
        log_error "SSH key not initialised - call alice_setup_ssh_key() first"
        return 1
    fi
    ssh -i "${_ALICE_SSH_KEY_FILE}" \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=30 \
        -o BatchMode=yes \
        "root@${ip}" \
        "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
}

# Backward-compat alias: alice_exec_and_wait <ip> <cmd> [timeout] [_unused_interval]
alice_exec_and_wait() { alice_ssh_exec "$1" "$2" "${3:-300}"; }

# Wait for SSH to become available on a newly created instance
wait_for_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        if ssh -i "${_ALICE_SSH_KEY_FILE}" \
               -o StrictHostKeyChecking=no \
               -o UserKnownHostsFile=/dev/null \
               -o ConnectTimeout=10 \
               -o BatchMode=yes \
               "root@${ip}" "echo ok" >/dev/null 2>&1; then
            log_success "SSH ready on ${ip}"
            return 0
        fi
        log_debug "SSH not ready on ${ip} (${elapsed}/${max}s)..."
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "SSH on ${ip} never became available after ${max}s"
    return 1
}

# Wait for apt/dpkg locks to be released (e.g., by cloud-init)
wait_for_apt_lock() {
    local ip="$1" max="${2:-300}" interval="${3:-5}" elapsed=0
    log_info "Waiting for apt/dpkg locks to be released on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        # Check if dpkg/apt locks are held by any process
        local lock_check
        lock_check=$(alice_ssh_exec "${ip}" \
            "fuser /var/lib/dpkg/lock-frontend /var/lib/dpkg/lock /var/lib/apt/lists/lock 2>/dev/null || echo 'free'" \
            30 2>/dev/null) || lock_check=""
        
        if [[ "$lock_check" == *"free"* ]] || [[ -z "$lock_check" ]]; then
            # Double-check by trying to acquire the lock briefly
            if alice_ssh_exec "${ip}" \
                "apt-get check 2>&1 | grep -qv 'Could not get lock' && exit 0 || exit 1" \
                30 >/dev/null 2>&1; then
                log_success "apt/dpkg locks released on ${ip}"
                return 0
            fi
        fi
        
        log_debug "apt/dpkg still locked on ${ip} (${elapsed}/${max}s)..."
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_warning "apt/dpkg locks may still be held after ${max}s, proceeding anyway..."
    return 1
}

# ---------- Instance lifecycle helpers ----------
alice_wait_instance_ready() {
    local id="$1" max="${2:-600}" interval="${3:-15}"
    local elapsed=0
    log_info "Waiting for instance ${id} to be ready (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(alice_get_instance_state "${id}")
        local sb; sb=$(alice_parse_body "${sr}")
        local sc; sc=$(alice_parse_code "${sr}")
        log_debug "Instance ${id} state HTTP ${sc}: ${sb}"
        if [[ "${sc}" == "200" ]]; then
            local status; status=$(echo "${sb}" | jq -r '.data.status // empty' 2>/dev/null)
            local state;  state=$(echo "${sb}"  | jq -r '.data.state.state // empty' 2>/dev/null)
            log_debug "Instance ${id}: status=${status} state=${state}"
            if [[ "${status}" == "complete" && "${state}" == "running" ]]; then
                log_success "Instance ${id} is ready"
                return 0
            fi
        fi
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "Instance ${id} readiness timeout after ${max}s"
    return 1
}

alice_create_and_wait() {
    # product_id os_id hours [ssh_key_id] [boot_script] [max_wait]
    local product_id="$1" os_id="$2" hours="${3:-1}"
    local ssh_key_id="${4:-}" boot_script="${5:-}" max="${6:-600}"
    local resp; resp=$(alice_create_instance "${product_id}" "${os_id}" "${hours}" "${ssh_key_id}" "${boot_script}")
    local body; body=$(alice_parse_body "${resp}")
    local http_code; http_code=$(alice_parse_code "${resp}")
    log_debug "POST /evo/instances/deploy HTTP ${http_code}: ${body}"
    if [[ "${http_code}" != "200" ]]; then
        log_error "Create instance failed (HTTP ${http_code}): $(echo "${body}" | jq -r '.message // .msg // empty' 2>/dev/null)"
        log_error "Full response: ${body}"
        return 1
    fi
    local id; id=$(echo "${body}" | jq -r '.data.id // .data.instance_id // empty' 2>/dev/null)
    [[ -z "${id}" ]] && { log_error "Cannot get instance ID from create response: ${body}"; return 1; }
    log_success "Instance creation requested, ID: ${id}"
    alice_wait_instance_ready "${id}" "${max}" || return 1
    # Return deploy response .data: ipv4 is a plain string, password is included
    echo "${body}" | jq -c '.data'
}

alice_delete_and_confirm() {
    local id="$1"
    log_info "Deleting instance ${id}..."
    local resp; resp=$(alice_delete_instance "${id}")
    local code; code=$(alice_parse_code "${resp}")
    log_debug "DELETE /evo/instances/${id} HTTP ${code}"
}
