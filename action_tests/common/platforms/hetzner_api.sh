#!/bin/bash
# https://hetzner.cloud/?ref=CnWVr0FGneUl
# API Docs: https://docs.hetzner.cloud/

HETZNER_API_BASE="${HETZNER_API_BASE:-https://api.hetzner.cloud/v1}"
HETZNER_API_TOKEN="${HETZNER_API_TOKEN:-}"
HETZNER_LOCATION="${HETZNER_LOCATION:-fsn1}"
HETZNER_SERVER_TYPE="${HETZNER_SERVER_TYPE:-cx22}"

hetzner_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${HETZNER_API_TOKEN}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${HETZNER_API_BASE}${endpoint}"
}

hetzner_parse_body() { echo "$1" | sed '$d'; }
hetzner_parse_code() { echo "$1" | tail -1; }

hetzner_platform_init() {
    if [[ -z "${HETZNER_API_TOKEN:-}" ]]; then
        log_error "[hetzner] HETZNER_API_TOKEN is required"
        return 1
    fi
    if [[ -n "${HETZNER_PRIVATE_KEY:-}" ]]; then
        PLATFORM_SSH_KEY_FILE=$(mktemp /tmp/platform_ssh_key_XXXXXX.pem)
        chmod 600 "${PLATFORM_SSH_KEY_FILE}"
        printf '%s\n' "${HETZNER_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}"
    fi
    # Upload SSH key if provided
    if [[ -n "${HETZNER_SSH_PUBLIC_KEY:-}" ]]; then
        local name="ci-key-$(date +%s)"
        local data="{\"name\":\"${name}\",\"public_key\":\"${HETZNER_SSH_PUBLIC_KEY}\"}"
        local resp; resp=$(hetzner_request "POST" "/ssh_keys" "$data")
        local body; body=$(hetzner_parse_body "$resp")
        HETZNER_SSH_KEY_ID=$(echo "$body" | jq -r '.ssh_key.id // empty' 2>/dev/null)
    fi
    log_info "[hetzner] Platform initialized (location: ${HETZNER_LOCATION})"
    return 0
}

hetzner_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[hetzner] Creating server: env=${env_type}"
    local image="debian-12"
    [[ "${env_type}" == "lxd" ]] && image="ubuntu-24.04"
    local name="ci-test-$(date +%s)"
    local data="{\"name\":\"${name}\",\"server_type\":\"${HETZNER_SERVER_TYPE}\",\"image\":\"${image}\",\"location\":\"${HETZNER_LOCATION}\",\"start_after_create\":true"
    [[ -n "${HETZNER_SSH_KEY_ID:-}" ]] && data="${data},\"ssh_keys\":[${HETZNER_SSH_KEY_ID}]"
    data="${data}}"
    local resp; resp=$(hetzner_request "POST" "/servers" "$data")
    local body; body=$(hetzner_parse_body "${resp}")
    local code; code=$(hetzner_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "201" ]]; then
        log_error "[hetzner] Create failed (HTTP ${code}): ${body}"
        return 1
    fi
    local id; id=$(echo "$body" | jq -r '.server.id // empty' 2>/dev/null)
    local ip; ip=$(echo "$body" | jq -r '.server.public_net.ipv4.ip // empty' 2>/dev/null)
    local password; password=$(echo "$body" | jq -r '.root_password // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "[hetzner] No server ID in response"; return 1; }
    PLATFORM_SSH_PASSWORD="${password}"
    # Wait for server to be running
    local max=300 elapsed=0
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(hetzner_request "GET" "/servers/${id}")
        local sb; sb=$(hetzner_parse_body "$sr")
        local status; status=$(echo "$sb" | jq -r '.server.status // empty' 2>/dev/null)
        ip=$(echo "$sb" | jq -r '.server.public_net.ipv4.ip // empty' 2>/dev/null)
        if [[ "$status" == "running" && -n "$ip" ]]; then
            log_success "[hetzner] Server ${id} ready: IP=${ip}"
            echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"hetzner\"}"
            return 0
        fi
        sleep 10; elapsed=$((elapsed + 10))
    done
    log_error "[hetzner] Server ${id} timeout after ${max}s"
    return 1
}

hetzner_platform_delete_instance() {
    local id="$1"
    log_info "[hetzner] Deleting server ${id}..."
    hetzner_request "DELETE" "/servers/${id}"
    return 0
}

hetzner_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[hetzner] Rebuilding server ${id}..."
    local image="debian-12"
    [[ "$os_name" == *"ubuntu"* ]] && image="ubuntu-24.04"
    local data="{\"image\":\"${image}\"}"
    local resp; resp=$(hetzner_request "POST" "/servers/${id}/actions/rebuild" "$data")
    local body; body=$(hetzner_parse_body "${resp}")
    local code; code=$(hetzner_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "201" ]]; then
        log_error "[hetzner] Rebuild failed (HTTP ${code}): ${body}"
        return 1
    fi
    local password; password=$(echo "$body" | jq -r '.root_password // empty' 2>/dev/null)
    [[ -n "$password" ]] && PLATFORM_SSH_PASSWORD="$password"
    sleep 30
    local sr; sr=$(hetzner_request "GET" "/servers/${id}")
    local sb; sb=$(hetzner_parse_body "$sr")
    local ip; ip=$(echo "$sb" | jq -r '.server.public_net.ipv4.ip // empty' 2>/dev/null)
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"hetzner\"}"
}

hetzner_platform_list_instances() {
    local resp; resp=$(hetzner_request "GET" "/servers")
    local body; body=$(hetzner_parse_body "${resp}")
    echo "$body" | jq -c '[.servers[]? | {instance_id: (.id|tostring), ipv4: .public_net.ipv4.ip, status: .status}]' 2>/dev/null
}

hetzner_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o BatchMode=yes "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[hetzner] No SSH credentials"; return 1
    fi
}

hetzner_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "[hetzner] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local ok=false
        if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
            ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o BatchMode=yes "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        fi
        $ok && { log_success "[hetzner] SSH ready on ${ip}"; return 0; }
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[hetzner] SSH timeout on ${ip} after ${max}s"; return 1
}
