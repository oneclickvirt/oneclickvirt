#!/bin/bash
# https://hetzner.cloud/?ref=CnWVr0FGneUl
# API Docs: https://docs.hetzner.cloud/

HETZNER_API_BASE="${HETZNER_API_BASE:-https://api.hetzner.cloud/v1}"
HETZNER_API_TOKEN="${HETZNER_API_TOKEN:-}"
HETZNER_LOCATION="${HETZNER_LOCATION:-fsn1}"
# cx22 (the former 2 vCPU / 4 GB baseline) is deprecated by Hetzner. cx23
# keeps the same CPU and memory profile with the current shared-vCPU catalog.
HETZNER_SERVER_TYPE="${HETZNER_SERVER_TYPE:-cx23}"

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

_hetzner_wait_action() {
    local id="$1" max_wait="${2:-600}" retryable_error="${3:-}" elapsed=0
    [[ -n "$id" ]] || return 1
    while (( elapsed < max_wait )); do
        local resp body code status error_code
        resp=$(hetzner_request "GET" "/actions/${id}") || return 1
        code=$(hetzner_parse_code "$resp")
        body=$(hetzner_parse_body "$resp")
        [[ "$code" == "200" ]] || { log_error "[hetzner] Action ${id} lookup failed (HTTP ${code})"; return 1; }
        status=$(jq -r '.action.status // empty' <<< "$body" 2>/dev/null)
        case "$status" in
            success) return 0 ;;
            error)
                error_code=$(jq -r '.action.error.code // "unknown"' <<< "$body" 2>/dev/null)
                HETZNER_LAST_ACTION_ERROR="$error_code"
                if [[ -n "$retryable_error" && "$error_code" == "$retryable_error" ]]; then
                    log_warning "[hetzner] Action ${id} is retryable (${error_code})"
                else
                    log_error "[hetzner] Action ${id} failed (${error_code})"
                fi
                return 1
                ;;
        esac
        sleep 5
        elapsed=$((elapsed + 5))
    done
    log_error "[hetzner] Action ${id} did not finish within ${max_wait}s"
    return 1
}

# Password-based root SSH after a rebuild is initially forced into an
# interactive password-change dialogue. A separate API password reset gives
# the test harness a usable non-interactive password for SSH/sshpass.
_hetzner_reset_root_password() {
    local id="$1" resp body code action_id password attempt
    for ((attempt=1; attempt<=3; attempt++)); do
        resp=$(hetzner_request "POST" "/servers/${id}/actions/reset_password" '{}') || return 1
        code=$(hetzner_parse_code "$resp")
        body=$(hetzner_parse_body "$resp")
        [[ "$code" == "200" || "$code" == "201" ]] || { log_error "[hetzner] Password reset failed (HTTP ${code})"; return 1; }
        password=$(jq -r '.root_password // empty' <<< "$body" 2>/dev/null)
        [[ -n "$password" ]] || { log_error "[hetzner] Password reset returned no root password"; return 1; }
        action_id=$(jq -r '.action.id // empty' <<< "$body" 2>/dev/null)
        HETZNER_LAST_ACTION_ERROR=""
        if [[ -z "$action_id" ]] || _hetzner_wait_action "$action_id" 600 guest_agent_unavailable; then
            printf '%s\n' "$password"
            return 0
        fi
        if [[ "$HETZNER_LAST_ACTION_ERROR" != "guest_agent_unavailable" ]]; then
            return 1
        fi
        if [[ "$attempt" -eq 3 ]]; then
            log_error "[hetzner] Password reset could not reach the guest agent after ${attempt} attempts"
            return 1
        fi
        log_warning "[hetzner] Guest agent not yet ready after rebuild; retrying password reset (${attempt}/3)"
        sleep 30
    done
    return 1
}

hetzner_platform_init() {
    if [[ -z "${HETZNER_API_TOKEN:-}" ]]; then
        log_error "[hetzner] HETZNER_API_TOKEN is required"
        return 1
    fi
    if [[ -n "${HETZNER_PRIVATE_KEY:-}" ]]; then
        local key_template="${TMPDIR:-/tmp}/platform_ssh_key.XXXXXX"
        if ! PLATFORM_SSH_KEY_FILE=$(mktemp "$key_template"); then
            log_error "[hetzner] Unable to create a temporary SSH key file"
            return 1
        fi
        chmod 600 "${PLATFORM_SSH_KEY_FILE}" || return 1
        printf '%s\n' "${HETZNER_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}" || return 1
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
    # This integration account is deliberately limited to one persistent
    # worker. Refuse even a direct adapter call when an inventory already
    # contains a server; the normal harness will rebuild that server instead.
    local existing
    existing=$(hetzner_platform_list_instances) || return 1
    if [[ $(jq -r 'length' <<< "$existing" 2>/dev/null) != "0" ]]; then
        log_error "[hetzner] Existing server present; reuse and rebuild it instead of creating another"
        return 1
    fi
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
            if [[ -z "${PLATFORM_SSH_KEY_FILE:-}" ]]; then
                password=$(_hetzner_reset_root_password "$id") || return 1
                PLATFORM_SSH_PASSWORD="$password"
            fi
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
    local action_id; action_id=$(echo "$body" | jq -r '.action.id // empty' 2>/dev/null)
    [[ -n "$action_id" ]] || { log_error "[hetzner] Rebuild returned no action ID"; return 1; }
    _hetzner_wait_action "$action_id" || return 1
    local password=""
    if [[ -z "${PLATFORM_SSH_KEY_FILE:-}" ]]; then
        password=$(_hetzner_reset_root_password "$id") || return 1
        PLATFORM_SSH_PASSWORD="$password"
    fi
    local sr; sr=$(hetzner_request "GET" "/servers/${id}")
    local sb; sb=$(hetzner_parse_body "$sr")
    local ip; ip=$(echo "$sb" | jq -r '.server.public_net.ipv4.ip // empty' 2>/dev/null)
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"hetzner\"}"
}

hetzner_platform_list_instances() {
    local resp; resp=$(hetzner_request "GET" "/servers")
    local code; code=$(hetzner_parse_code "$resp")
    [[ "$code" == "200" ]] || { log_error "[hetzner] Server inventory failed (HTTP ${code})"; return 1; }
    local body; body=$(hetzner_parse_body "${resp}")
    echo "$body" | jq -ec 'if (.servers | type) == "array" then [.servers[] | {instance_id: (.id|tostring), ipv4: .public_net.ipv4.ip, status: .status}] else error("missing servers inventory") end' 2>/dev/null
}

hetzner_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 -o BatchMode=yes "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
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
                -o ConnectTimeout=10 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o BatchMode=yes "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        fi
        $ok && { log_success "[hetzner] SSH ready on ${ip}"; return 0; }
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[hetzner] SSH timeout on ${ip} after ${max}s"; return 1
}
