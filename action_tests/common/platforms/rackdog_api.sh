#!/bin/bash
# RackDog Platform API Provider
# https://cloud.rackdog.com/referral/bx8fms
# API Docs: https://api.rackdog.com/

RACKDOG_API_BASE="${RACKDOG_API_BASE:-https://api.rackdog.com/v1}"
RACKDOG_API_KEY="${RACKDOG_API_KEY:-}"
RACKDOG_LOCATION="${RACKDOG_LOCATION:-}"
RACKDOG_PASSWORD="${RACKDOG_PASSWORD:-CiTest1234!}"

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
    local resp; resp=$(rackdog_request "POST" "/user-resource/vm" "$data")
    local body; body=$(rackdog_parse_body "${resp}")
    local http_code; http_code=$(rackdog_parse_code "${resp}")
    if [[ "${http_code}" != "200" && "${http_code}" != "201" ]]; then
        log_error "[rackdog] Create failed (HTTP ${http_code}): ${body}"
        return 1
    fi
    local uuid; uuid=$(echo "$body" | jq -r '.uuid // empty' 2>/dev/null)
    local ip; ip=$(echo "$body" | jq -r '.private_ipv4 // empty' 2>/dev/null)
    local pub_ip; pub_ip=$(echo "$body" | jq -r '.public_ipv4 // empty' 2>/dev/null)
    [[ -n "$pub_ip" ]] && ip="$pub_ip"
    local ssh_user; ssh_user=$(echo "$body" | jq -r '.username // "root"' 2>/dev/null)
    [[ -z "$uuid" ]] && { log_error "[rackdog] No UUID in response"; return 1; }
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
    log_info "[rackdog] Reinstalling VM ${id}..."
    local os_version="12"
    [[ "$os_name" == "ubuntu" ]] && os_version="22.04"
    local resp; resp=$(rackdog_request "POST" "/user-resource/vm/reinstall" "uuid=${id}&os_name=${os_name}&os_version=${os_version}")
    local body; body=$(rackdog_parse_body "${resp}")
    local code; code=$(rackdog_parse_code "${resp}")
    if [[ "$code" != "200" ]]; then
        log_error "[rackdog] Reinstall failed (HTTP ${code}): ${body}"
        return 1
    fi
    local ip; ip=$(echo "$body" | jq -r '.private_ipv4 // empty' 2>/dev/null)
    local pub_ip; pub_ip=$(echo "$body" | jq -r '.public_ipv4 // empty' 2>/dev/null)
    [[ -n "$pub_ip" ]] && ip="$pub_ip"
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
