#!/bin/bash
# Linode (Akamai) Platform API Provider
# https://www.linode.com/lp/refer/?r=9296554d01ecacaa0be56892fd969b557722becd
# API Docs: https://techdocs.akamai.com/linode-api/reference/api

LINODE_API_BASE="${LINODE_API_BASE:-https://api.linode.com/v4}"
LINODE_TOKEN="${LINODE_TOKEN:-}"
LINODE_REGION="${LINODE_REGION:-us-east}"
LINODE_TYPE="${LINODE_TYPE:-g6-nanode-1}"

linode_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${LINODE_TOKEN}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${LINODE_API_BASE}${endpoint}"
}

linode_parse_body() { echo "$1" | sed '$d'; }
linode_parse_code() { echo "$1" | tail -1; }

linode_platform_init() {
    if [[ -z "${LINODE_TOKEN:-}" ]]; then
        log_error "[linode] LINODE_TOKEN is required"
        return 1
    fi
    if [[ -n "${LINODE_PRIVATE_KEY:-}" ]]; then
        PLATFORM_SSH_KEY_FILE=$(mktemp /tmp/platform_ssh_key_XXXXXX.pem)
        chmod 600 "${PLATFORM_SSH_KEY_FILE}"
        printf '%s\n' "${LINODE_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}"
    fi
    if [[ -n "${LINODE_SSH_PUBLIC_KEY:-}" ]]; then
        local data="{\"label\":\"ci-key-$(date +%s)\",\"ssh_key\":\"${LINODE_SSH_PUBLIC_KEY}\"}"
        local resp; resp=$(linode_request "POST" "/profile/sshkeys" "$data")
        local body; body=$(linode_parse_body "$resp")
        LINODE_SSH_KEY_ID=$(echo "$body" | jq -r '.id // empty' 2>/dev/null)
    fi
    log_info "[linode] Platform initialized (region: ${LINODE_REGION})"
    return 0
}

linode_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[linode] Creating Linode: env=${env_type}"
    local image="linode/debian12"
    [[ "${env_type}" == "lxd" ]] && image="linode/ubuntu24.04"
    local label="ci-test-$(date +%s)"
    local root_pass="${LINODE_ROOT_PASSWORD:-$(openssl rand -base64 24)}"
    local data="{\"type\":\"${LINODE_TYPE}\",\"region\":\"${LINODE_REGION}\",\"image\":\"${image}\",\"label\":\"${label}\",\"root_pass\":\"${root_pass}\",\"booted\":true"
    if [[ -n "${LINODE_SSH_PUBLIC_KEY:-}" ]]; then
        data="${data},\"authorized_keys\":[\"${LINODE_SSH_PUBLIC_KEY}\"]"
    fi
    data="${data}}"
    local resp; resp=$(linode_request "POST" "/linode/instances" "$data")
    local body; body=$(linode_parse_body "${resp}")
    local code; code=$(linode_parse_code "${resp}")
    if [[ "$code" != "200" ]]; then
        log_error "[linode] Create failed (HTTP ${code}): ${body}"
        return 1
    fi
    local id; id=$(echo "$body" | jq -r '.id // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "[linode] No Linode ID in response"; return 1; }
    PLATFORM_SSH_PASSWORD="${root_pass}"
    # Wait for running status
    local max=300 elapsed=0
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(linode_request "GET" "/linode/instances/${id}")
        local sb; sb=$(linode_parse_body "$sr")
        local status; status=$(echo "$sb" | jq -r '.status // empty' 2>/dev/null)
        local ip; ip=$(echo "$sb" | jq -r '.ipv4[0] // empty' 2>/dev/null)
        if [[ "$status" == "running" && -n "$ip" ]]; then
            log_success "[linode] Linode ${id} ready: IP=${ip}"
            echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${root_pass}\",\"ssh_user\":\"root\",\"platform\":\"linode\"}"
            return 0
        fi
        sleep 10; elapsed=$((elapsed + 10))
    done
    log_error "[linode] Linode ${id} timeout after ${max}s"
    return 1
}

linode_platform_delete_instance() {
    local id="$1"
    log_info "[linode] Deleting Linode ${id}..."
    linode_request "DELETE" "/linode/instances/${id}"
    return 0
}

linode_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[linode] Rebuilding Linode ${id}..."
    local image="linode/debian12"
    [[ "$os_name" == *"ubuntu"* ]] && image="linode/ubuntu24.04"
    local root_pass="${LINODE_ROOT_PASSWORD:-$(openssl rand -base64 24)}"
    local data="{\"image\":\"${image}\",\"root_pass\":\"${root_pass}\"}"
    local resp; resp=$(linode_request "POST" "/linode/instances/${id}/rebuild" "$data")
    local body; body=$(linode_parse_body "${resp}")
    local code; code=$(linode_parse_code "${resp}")
    if [[ "$code" != "200" ]]; then
        log_error "[linode] Rebuild failed (HTTP ${code}): ${body}"
        return 1
    fi
    PLATFORM_SSH_PASSWORD="${root_pass}"
    sleep 30
    local sr; sr=$(linode_request "GET" "/linode/instances/${id}")
    local sb; sb=$(linode_parse_body "$sr")
    local ip; ip=$(echo "$sb" | jq -r '.ipv4[0] // empty' 2>/dev/null)
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${root_pass}\",\"ssh_user\":\"root\",\"platform\":\"linode\"}"
}

linode_platform_list_instances() {
    local resp; resp=$(linode_request "GET" "/linode/instances")
    local body; body=$(linode_parse_body "${resp}")
    echo "$body" | jq -c '[.data[]? | {instance_id: (.id|tostring), ipv4: .ipv4[0], status: .status}]' 2>/dev/null
}

linode_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o BatchMode=yes "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[linode] No SSH credentials"; return 1
    fi
}

linode_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "[linode] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local ok=false
        if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
            ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o BatchMode=yes "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        fi
        $ok && { log_success "[linode] SSH ready on ${ip}"; return 0; }
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[linode] SSH timeout on ${ip} after ${max}s"; return 1
}
