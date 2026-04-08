#!/bin/bash
# Alice (Ephemera) Platform API Provider
# Implements the standard platform interface for AliceInit.

ALICE_API_BASE="${ALICE_API_BASE:-https://app.alice.ws/cli/v1}"
ALICE_CLIENT_ID="${ALICE_CLIENT_ID:-}"
ALICE_CLIENT_SECRET="${ALICE_CLIENT_SECRET:-}"
ALICE_PUBLIC_KEY="${ALICE_PUBLIC_KEY:-}"
ALICE_PRIVATE_KEY="${ALICE_PRIVATE_KEY:-}"
ALICEINIT_TOKEN="${ALICE_CLIENT_ID}:${ALICE_CLIENT_SECRET}"

# ============================================================================
# Low-level API helpers
# ============================================================================
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

alice_get_profile()     { alice_request "GET" "/account/profile"; }
alice_get_permissions() { alice_request "GET" "/evo/permissions"; }
alice_get_ssh_keys()    { alice_request "GET" "/account/ssh-keys"; }
alice_get_plans()       { alice_request "GET" "/evo/plans"; }
alice_get_plan_os()     { alice_request "GET" "/evo/plans/$1/os-images"; }
alice_list_instances_raw()  { alice_request "GET" "/evo/instances"; }
alice_get_instance_state()  { alice_request "GET" "/evo/instances/$1/state"; }
alice_delete_instance_raw() { alice_request "DELETE" "/evo/instances/$1"; }

alice_create_instance_raw() {
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

# ============================================================================
# Internal helpers
# ============================================================================
_alice_get_min_package_id() {
    local r; r=$(alice_get_permissions)
    local body; body=$(alice_parse_body "$r")
    local http_code; http_code=$(alice_parse_code "$r")
    log_debug "[alice] GET /evo/permissions HTTP ${http_code}"
    local allow; allow=$(echo "$body" | jq -r '.data.allow_packages // empty' 2>/dev/null)
    if [[ -z "$allow" ]]; then
        log_error "[alice] No allow_packages in permissions response"
        return 1
    fi
    echo "${allow%%|*}"
}

_alice_get_os_id_for_plan() {
    local plan_id="$1" name="${2:-debian}"
    local r; r=$(alice_get_plan_os "${plan_id}")
    local body; body=$(alice_parse_body "$r")
    local http_code; http_code=$(alice_parse_code "$r")
    log_debug "[alice] GET /evo/plans/${plan_id}/os-images HTTP ${http_code}"
    local id; id=$(echo "$body" | jq -r "[.data[].os_list[] | select(.name | test(\"${name}\";\"i\"))][0].id // empty" 2>/dev/null)
    if [[ -z "$id" ]]; then
        log_error "[alice] Cannot find OS matching '${name}' for plan ${plan_id}"
        return 1
    fi
    echo "$id"
}

_alice_get_ssh_key_id() {
    if [[ -z "${ALICE_PUBLIC_KEY:-}" ]]; then
        log_warning "[alice] ALICE_PUBLIC_KEY not set, SSH key ID cannot be resolved"
        return 1
    fi
    local r; r=$(alice_get_ssh_keys)
    local body; body=$(alice_parse_body "$r")
    local key_type key_body
    key_type=$(echo "${ALICE_PUBLIC_KEY}" | awk '{print $1}')
    key_body=$(echo "${ALICE_PUBLIC_KEY}" | awk '{print $2}')
    local id; id=$(echo "$body" | jq -r --arg kt "$key_type" --arg kb "$key_body" \
        '.data[] | select((.publickey | rtrimstr("\n") | split(" ")) as $p | ($p[0] == $kt and $p[1] == $kb)) | .id' \
        2>/dev/null | head -1)
    if [[ -z "$id" ]]; then
        id=$(echo "$body" | jq -r '.data[0].id // empty' 2>/dev/null)
        if [[ -n "$id" ]]; then
            log_warning "[alice] Could not match key exactly; using first key ID: ${id}"
        else
            log_error "[alice] No SSH keys found"
            return 1
        fi
    fi
    echo "$id"
}

_alice_wait_instance_ready() {
    local id="$1" max="${2:-600}" interval="${3:-15}" elapsed=0
    log_info "[alice] Waiting for instance ${id} to be ready (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(alice_get_instance_state "${id}")
        local sb; sb=$(alice_parse_body "${sr}")
        local sc; sc=$(alice_parse_code "${sr}")
        if [[ "${sc}" == "200" ]]; then
            local status; status=$(echo "${sb}" | jq -r '.data.status // empty' 2>/dev/null)
            local state;  state=$(echo "${sb}"  | jq -r '.data.state.state // empty' 2>/dev/null)
            log_debug "[alice] Instance ${id}: status=${status} state=${state}"
            if [[ "${status}" == "complete" && "${state}" == "running" ]]; then
                log_success "[alice] Instance ${id} is ready"
                return 0
            fi
        fi
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[alice] Instance ${id} readiness timeout after ${max}s"
    return 1
}

# ============================================================================
# Standard Platform Interface Implementation
# ============================================================================

alice_platform_init() {
    if [[ -z "${ALICE_CLIENT_ID:-}" || -z "${ALICE_CLIENT_SECRET:-}" ]]; then
        log_error "[alice] ALICE_CLIENT_ID and ALICE_CLIENT_SECRET are required"
        return 1
    fi
    ALICEINIT_TOKEN="${ALICE_CLIENT_ID}:${ALICE_CLIENT_SECRET}"
    if [[ -z "${ALICE_PRIVATE_KEY:-}" ]]; then
        log_error "[alice] ALICE_PRIVATE_KEY is required for SSH access"
        return 1
    fi
    # Write SSH private key to temp file
    PLATFORM_SSH_KEY_FILE=$(mktemp /tmp/platform_ssh_key_XXXXXX.pem)
    chmod 600 "${PLATFORM_SSH_KEY_FILE}"
    printf '%s\n' "${ALICE_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}"
    log_info "[alice] Platform initialized (API: ${ALICE_API_BASE})"
    return 0
}

alice_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[alice] Creating instance: env=${env_type} hours=${hours}"
    local pkg; pkg=$(_alice_get_min_package_id) || return 1
    local os_name="debian"
    [[ "${env_type}" == "lxd" ]] && os_name="ubuntu"
    local os_id; os_id=$(_alice_get_os_id_for_plan "${pkg}" "${os_name}") || return 1
    local ssh_key_id; ssh_key_id=$(_alice_get_ssh_key_id) || ssh_key_id=""
    local resp; resp=$(alice_create_instance_raw "${pkg}" "${os_id}" "${hours}" "${ssh_key_id}" "")
    local body; body=$(alice_parse_body "${resp}")
    local http_code; http_code=$(alice_parse_code "${resp}")
    if [[ "${http_code}" != "200" ]]; then
        log_error "[alice] Create failed (HTTP ${http_code}): ${body}"
        return 1
    fi
    local id; id=$(echo "${body}" | jq -r '.data.id // .data.instance_id // empty' 2>/dev/null)
    [[ -z "${id}" ]] && { log_error "[alice] Cannot get instance ID: ${body}"; return 1; }
    log_success "[alice] Instance creation requested, ID: ${id}"
    _alice_wait_instance_ready "${id}" 600 || return 1
    local ip; ip=$(echo "${body}" | jq -r '.data.ipv4 // .data.ip // empty' 2>/dev/null)
    local password; password=$(echo "${body}" | jq -r '.data.password // empty' 2>/dev/null)
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"alice\"}"
}

alice_platform_delete_instance() {
    local id="$1"
    log_info "[alice] Deleting instance ${id}..."
    local resp; resp=$(alice_delete_instance_raw "${id}")
    local code; code=$(alice_parse_code "${resp}")
    log_debug "[alice] DELETE instance ${id} HTTP ${code}"
    return 0
}

alice_platform_reinstall_instance() {
    log_error "[alice] Reinstall not supported - AliceInit does not have a reinstall API"
    return 1
}

alice_platform_list_instances() {
    local resp; resp=$(alice_list_instances_raw)
    local body; body=$(alice_parse_body "${resp}")
    local code; code=$(alice_parse_code "${resp}")
    if [[ "${code}" != "200" ]]; then
        log_error "[alice] List instances failed (HTTP ${code})"
        return 1
    fi
    echo "${body}" | jq -c '[.data[]? | {instance_id: (.id // .instance_id | tostring), ipv4: (.ipv4 // .ip // ""), status: (.status // "")}]' 2>/dev/null
}

alice_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    local ssh_user; ssh_user=$(get_platform_ssh_user "alice")
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 \
            -o BatchMode=yes \
            "${ssh_user}@${ip}" \
            "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[alice] SSH key not initialized"
        return 1
    fi
}

alice_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    local ssh_user; ssh_user=$(get_platform_ssh_user "alice")
    log_info "[alice] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        if ssh -i "${PLATFORM_SSH_KEY_FILE}" \
               -o StrictHostKeyChecking=no \
               -o UserKnownHostsFile=/dev/null \
               -o ConnectTimeout=10 \
               -o BatchMode=yes \
               "${ssh_user}@${ip}" "echo ok" >/dev/null 2>&1; then
            log_success "[alice] SSH ready on ${ip}"
            return 0
        fi
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[alice] SSH timeout on ${ip} after ${max}s"
    return 1
}
