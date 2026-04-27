#!/bin/bash
# RackDog Platform API Provider
# https://cloud.rackdog.com/referral/bx8fms
# API Docs: https://api.rackdog.com/

RACKDOG_API_BASE="${RACKDOG_API_BASE:-https://api.rackdog.com/v1}"
RACKDOG_API_KEY="${RACKDOG_API_KEY:-}"
RACKDOG_LOCATION="${RACKDOG_LOCATION:-}"
RACKDOG_PASSWORD="${RACKDOG_PASSWORD:-CiTest1234!}"
RACKDOG_BILLING_ACCOUNT_ID="${RACKDOG_BILLING_ACCOUNT_ID:-}"

rackdog_request() {
    local method="$1" endpoint="$2" data="${3:-}" content_type="${4:-form}"
    local url="${RACKDOG_API_BASE}${endpoint}"
    [[ -n "${RACKDOG_LOCATION}" ]] && url="${RACKDOG_API_BASE}/${RACKDOG_LOCATION}${endpoint#/v1}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "apikey: ${RACKDOG_API_KEY}"
        -X "${method}")
    if [[ -n "$data" ]]; then
        if [[ "$content_type" == "json" ]]; then
            args+=(-H "Content-Type: application/json" -d "$data")
        else
            args+=(-d "$data")
        fi
    fi
    curl "${args[@]}" "${url}"
}

rackdog_parse_body() { echo "$1" | sed '$d'; }
rackdog_parse_code() { echo "$1" | tail -1; }

# Wait for a VM to reach a usable (non-transitional) status with a valid IP.
# Prints the IP to stdout on success.
_rackdog_wait_vm_ready() {
    local uuid="$1" max="${2:-600}" interval="${3:-15}" elapsed=0
    log_info "[rackdog] Waiting for VM ${uuid} to be ready (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local resp; resp=$(rackdog_request "GET" "/user-resource/vm/list")
        local body; body=$(rackdog_parse_body "$resp")
        local vm; vm=$(echo "$body" | jq -c --arg u "$uuid" '.[]? | select(.uuid == $u)' 2>/dev/null)
        local status; status=$(echo "$vm" | jq -r '.status // empty' 2>/dev/null)
        local ip; ip=$(echo "$vm" | jq -r '.public_ipv4 // .private_ipv4 // empty' 2>/dev/null)
        log_debug "[rackdog] VM ${uuid}: status=${status:-?} ip=${ip:-none} (${elapsed}/${max}s)"
        # Accept any status that is not a transitional/pending state
        if [[ -n "$status" && "$status" != "installing" && "$status" != "pending"
              && "$status" != "reinstalling" && -n "$ip" && "$ip" != "null" ]]; then
            log_success "[rackdog] VM ${uuid} ready: status=${status} IP=${ip}"
            echo "$ip"
            return 0
        fi
        sleep "$interval"; elapsed=$((elapsed + interval))
    done
    log_error "[rackdog] VM ${uuid} readiness timeout after ${max}s"
    return 1
}

rackdog_platform_init() {
    if [[ -z "${RACKDOG_API_KEY:-}" ]]; then
        log_error "[rackdog] RACKDOG_API_KEY is required"
        return 1
    fi
    # Auto-detect location if not set
    if [[ -z "${RACKDOG_LOCATION}" ]]; then
        local resp; resp=$(rackdog_request "GET" "/config/locations")
        local body; body=$(rackdog_parse_body "$resp")
        RACKDOG_LOCATION=$(echo "$body" | jq -r '[.[] | select(.is_default == true)][0].slug // .[0].slug // empty' 2>/dev/null)
        [[ -n "$RACKDOG_LOCATION" ]] && log_info "[rackdog] Using location: ${RACKDOG_LOCATION}"
    fi
    PLATFORM_SSH_PASSWORD="${RACKDOG_PASSWORD}"
    if [[ -n "${RACKDOG_PRIVATE_KEY:-}" ]]; then
        PLATFORM_SSH_KEY_FILE=$(mktemp /tmp/platform_ssh_key_XXXXXX.pem)
        chmod 600 "${PLATFORM_SSH_KEY_FILE}"
        printf '%s\n' "${RACKDOG_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}"
    fi
    # Auto-detect billing_account_id if not provided (required for global API tokens)
    if [[ -z "${RACKDOG_BILLING_ACCOUNT_ID:-}" ]]; then
        local ba_resp; ba_resp=$(rackdog_request "GET" "/billing_accounts")
        local ba_body; ba_body=$(rackdog_parse_body "$ba_resp")
        RACKDOG_BILLING_ACCOUNT_ID=$(echo "$ba_body" | jq -r '(.[0].id // .[0].uuid // empty)' 2>/dev/null)
        [[ -n "$RACKDOG_BILLING_ACCOUNT_ID" ]] && log_info "[rackdog] Auto-detected billing_account_id: ${RACKDOG_BILLING_ACCOUNT_ID}"
    fi
    log_info "[rackdog] Platform initialized"
    return 0
}

rackdog_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[rackdog] Creating VM: env=${env_type}"
    local os_name="debian" os_version="12"
    [[ "${env_type}" == "lxd" ]] && os_name="ubuntu" && os_version="22.04"
    local pool_resp; pool_resp=$(rackdog_request "GET" "/user-resource/host_pool/list")
    local pool_body; pool_body=$(rackdog_parse_body "$pool_resp")
    local pool_uuid; pool_uuid=$(echo "$pool_body" | jq -r '.[0].uuid // empty' 2>/dev/null)
    local public_key_param=""
    if [[ -n "${RACKDOG_PUBLIC_KEY:-}" ]]; then
        public_key_param="&public_key=$(printf '%s' "${RACKDOG_PUBLIC_KEY}" | jq -sRr @uri)"
    fi
    local data="name=ci-test-$(date +%s)&os_name=${os_name}&os_version=${os_version}&disks=40&vcpu=2&ram=2048&username=root&password=${RACKDOG_PASSWORD}${public_key_param}"
    [[ -n "$pool_uuid" ]] && data="${data}&designated_pool_uuid=${pool_uuid}"
    [[ -n "${RACKDOG_BILLING_ACCOUNT_ID:-}" ]] && data="${data}&billing_account_id=${RACKDOG_BILLING_ACCOUNT_ID}"
    local resp; resp=$(rackdog_request "POST" "/user-resource/vm" "$data")
    local body; body=$(rackdog_parse_body "${resp}")
    local http_code; http_code=$(rackdog_parse_code "${resp}")
    if [[ "${http_code}" != "200" && "${http_code}" != "201" ]]; then
        log_error "[rackdog] Create failed (HTTP ${http_code}): ${body}"
        return 1
    fi
    local uuid; uuid=$(echo "$body" | jq -r '.uuid // empty' 2>/dev/null)
    local ssh_user; ssh_user=$(echo "$body" | jq -r '.username // "root"' 2>/dev/null)
    if [[ -z "$uuid" ]]; then
        log_error "[rackdog] No UUID in create response: ${body}"
        return 1
    fi
    log_info "[rackdog] VM ${uuid} creation accepted, waiting for IP assignment..."
    local ip; ip=$(_rackdog_wait_vm_ready "$uuid" 600 15) || return 1
    log_success "[rackdog] VM created: ${uuid} IP: ${ip}"
    echo "{\"instance_id\":\"${uuid}\",\"ipv4\":\"${ip}\",\"password\":\"${RACKDOG_PASSWORD}\",\"ssh_user\":\"${ssh_user}\",\"platform\":\"rackdog\"}"
}

rackdog_platform_delete_instance() {
    local id="$1"
    log_info "[rackdog] Deleting VM ${id}..."
    rackdog_request "DELETE" "/user-resource/vm" "uuid=${id}"
    return 0
}

rackdog_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[rackdog] Reinstalling VM ${id} (os=${os_name})..."
    local os_version="12"
    [[ "$os_name" == "ubuntu" ]] && os_version="22.04"
    # Capture current IP before reinstall; it will not change after OS reinstall
    local list_r; list_r=$(rackdog_request "GET" "/user-resource/vm/list")
    local list_b; list_b=$(rackdog_parse_body "$list_r")
    local current_ip; current_ip=$(echo "$list_b" | jq -r --arg u "$id" '.[]? | select(.uuid == $u) | (.public_ipv4 // .private_ipv4 // "")' 2>/dev/null)
    log_info "[rackdog] VM ${id} current IP before reinstall: ${current_ip:-unknown}"
    local resp; resp=$(rackdog_request "POST" "/user-resource/vm/reinstall" "uuid=${id}&os_name=${os_name}&os_version=${os_version}")
    local body; body=$(rackdog_parse_body "${resp}")
    local code; code=$(rackdog_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "202" ]]; then
        log_error "[rackdog] Reinstall API failed (HTTP ${code}): ${body}"
        return 1
    fi
    log_info "[rackdog] Reinstall accepted (HTTP ${code}), waiting for VM to become ready..."
    local ip; ip=$(_rackdog_wait_vm_ready "$id" 900 15)
    if [[ $? -ne 0 || -z "$ip" ]]; then
        # Reinstall takes long; fall back to the known pre-reinstall IP if poll timed out
        if [[ -n "$current_ip" ]]; then
            log_warning "[rackdog] Readiness poll timed out; using pre-reinstall IP ${current_ip}"
            ip="$current_ip"
        else
            log_error "[rackdog] Cannot determine VM IP after reinstall"
            return 1
        fi
    fi
    PLATFORM_SSH_PASSWORD="${RACKDOG_PASSWORD}"
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${RACKDOG_PASSWORD}\",\"ssh_user\":\"root\",\"platform\":\"rackdog\"}"
}

rackdog_platform_list_instances() {
    local resp; resp=$(rackdog_request "GET" "/user-resource/vm/list")
    local body; body=$(rackdog_parse_body "${resp}")
    echo "$body" | jq -c '[.[]? | {instance_id: .uuid, ipv4: (.public_ipv4 // .private_ipv4 // ""), status: .status}]' 2>/dev/null
}

rackdog_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    local ssh_user; ssh_user=$(get_platform_ssh_user "rackdog")
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" \
            -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o BatchMode=yes \
            "${ssh_user}@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh \
            -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 \
            "${ssh_user}@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[rackdog] No SSH credentials available"
        return 1
    fi
}

rackdog_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    local ssh_user; ssh_user=$(get_platform_ssh_user "rackdog")
    log_info "[rackdog] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local ok=false
        if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
            ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o BatchMode=yes "${ssh_user}@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 "${ssh_user}@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        fi
        $ok && { log_success "[rackdog] SSH ready on ${ip}"; return 0; }
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[rackdog] SSH timeout on ${ip} after ${max}s"
    return 1
}
