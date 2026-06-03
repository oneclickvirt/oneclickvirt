#!/bin/bash
# Cubepath Platform API Provider
# https://my.cubepath.com/register?ref=T8UXFTX9JURXZ
# Auth: API key, supports SSH key + password

CUBEPATH_API_BASE="${CUBEPATH_API_BASE:-https://api.cubepath.io/v1}"
CUBEPATH_API_KEY="${CUBEPATH_API_KEY:-}"
CUBEPATH_LOCATION="${CUBEPATH_LOCATION:-de}"
CUBEPATH_PLAN="${CUBEPATH_PLAN:-vps-basic}"

cubepath_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${CUBEPATH_API_KEY}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${CUBEPATH_API_BASE}${endpoint}"
}

cubepath_parse_body() { echo "$1" | sed '$d'; }
cubepath_parse_code() { echo "$1" | tail -1; }

cubepath_platform_init() {
    if [[ -z "${CUBEPATH_API_KEY:-}" ]]; then
        log_error "[cubepath] CUBEPATH_API_KEY is required"
        return 1
    fi
    if [[ -n "${CUBEPATH_PRIVATE_KEY:-}" ]]; then
        PLATFORM_SSH_KEY_FILE=$(mktemp /tmp/platform_ssh_key_XXXXXX.pem)
        chmod 600 "${PLATFORM_SSH_KEY_FILE}"
        printf '%s\n' "${CUBEPATH_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}"
    fi
    log_info "[cubepath] Platform initialized (location: ${CUBEPATH_LOCATION})"
    return 0
}

cubepath_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[cubepath] Creating VPS: env=${env_type}"
    # Monthly billing - try reusing existing instance
    local list_resp; list_resp=$(cubepath_request "GET" "/servers")
    local list_body; list_body=$(cubepath_parse_body "$list_resp")
    local existing_id; existing_id=$(echo "$list_body" | jq -r '.data[0].id // .servers[0].id // empty' 2>/dev/null)
    local existing_ip; existing_ip=$(echo "$list_body" | jq -r '.data[0].ip // .data[0].ipv4 // .servers[0].ip // empty' 2>/dev/null)
    if [[ -n "$existing_id" && -n "$existing_ip" ]]; then
        log_info "[cubepath] Reusing existing VPS ${existing_id} (IP: ${existing_ip}), will reinstall"
        if cubepath_platform_reinstall_instance "$existing_id" "debian"; then
            echo "{\"instance_id\":\"${existing_id}\",\"ipv4\":\"${existing_ip}\",\"password\":\"${PLATFORM_SSH_PASSWORD}\",\"ssh_user\":\"root\",\"platform\":\"cubepath\"}"
            return 0
        fi
    fi
    local os="debian-12"
    [[ "${env_type}" == "lxd" ]] && os="ubuntu-24.04"
    local password="${CUBEPATH_ROOT_PASSWORD:-$(openssl rand -base64 24)}"
    local data="{\"plan\":\"${CUBEPATH_PLAN}\",\"location\":\"${CUBEPATH_LOCATION}\",\"os\":\"${os}\",\"password\":\"${password}\"}"
    if [[ -n "${CUBEPATH_SSH_PUBLIC_KEY:-}" ]]; then
        data=$(echo "$data" | jq --arg key "${CUBEPATH_SSH_PUBLIC_KEY}" '. + {ssh_key: $key}' 2>/dev/null) || {
            log_error "[cubepath] Failed to build create payload with SSH key"
            return 1
        }
    fi
    local resp; resp=$(cubepath_request "POST" "/servers" "$data")
    local body; body=$(cubepath_parse_body "${resp}")
    local code; code=$(cubepath_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "201" ]]; then
        log_error "[cubepath] Create failed (HTTP ${code}): ${body}"
        return 1
    fi
    local id; id=$(echo "$body" | jq -r '.data.id // .id // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "[cubepath] No VPS ID in response"; return 1; }
    PLATFORM_SSH_PASSWORD="${password}"
    local max=600 elapsed=0
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(cubepath_request "GET" "/servers/${id}")
        local sb; sb=$(cubepath_parse_body "$sr")
        local status; status=$(echo "$sb" | jq -r '.data.status // .status // empty' 2>/dev/null)
        local ip; ip=$(echo "$sb" | jq -r '.data.ip // .data.ipv4 // .ip // empty' 2>/dev/null)
        if [[ "$status" == "active" || "$status" == "running" ]] && [[ -n "$ip" ]]; then
            log_success "[cubepath] VPS ${id} ready: IP=${ip}"
            echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"cubepath\"}"
            return 0
        fi
        sleep 15; elapsed=$((elapsed + 15))
    done
    log_error "[cubepath] VPS ${id} timeout after ${max}s"
    return 1
}

cubepath_platform_delete_instance() {
    local id="$1"
    log_info "[cubepath] Deleting VPS ${id}..."
    cubepath_request "DELETE" "/servers/${id}"
    return 0
}

cubepath_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[cubepath] Reinstalling VPS ${id}..."
    local os="debian-12"
    [[ "$os_name" == *"ubuntu"* ]] && os="ubuntu-24.04"
    local password="${CUBEPATH_ROOT_PASSWORD:-$(openssl rand -base64 24)}"
    local data="{\"os\":\"${os}\",\"password\":\"${password}\"}"
    local resp; resp=$(cubepath_request "POST" "/servers/${id}/reinstall" "$data")
    local code; code=$(cubepath_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "204" ]]; then
        local body; body=$(cubepath_parse_body "${resp}")
        log_error "[cubepath] Reinstall failed (HTTP ${code}): ${body}"
        return 1
    fi
    PLATFORM_SSH_PASSWORD="${password}"
    log_info "[cubepath] Reinstall initiated, waiting for completion..."
    sleep 60
    return 0
}

cubepath_platform_list_instances() {
    local resp; resp=$(cubepath_request "GET" "/servers")
    local body; body=$(cubepath_parse_body "${resp}")
    echo "$body" | jq -c '[.data[]? // .servers[]? | {instance_id: (.id|tostring), ipv4: (.ip // .ipv4), status: .status}]' 2>/dev/null
}

cubepath_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o BatchMode=yes "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[cubepath] No SSH credentials"; return 1
    fi
}

cubepath_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "[cubepath] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local ok=false
        if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
            ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o BatchMode=yes "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        fi
        $ok && { log_success "[cubepath] SSH ready on ${ip}"; return 0; }
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[cubepath] SSH timeout on ${ip} after ${max}s"; return 1
}
