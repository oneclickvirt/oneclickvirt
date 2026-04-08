#!/bin/bash
# Prepaid-Host Platform API Provider
# https://prepaid-host.com/a/server
# Auth: API key, supports password-only instances

PREPAIDHOST_API_BASE="${PREPAIDHOST_API_BASE:-https://api.prepaidhost.com/v1}"
PREPAIDHOST_API_KEY="${PREPAIDHOST_API_KEY:-}"
PREPAIDHOST_LOCATION="${PREPAIDHOST_LOCATION:-de}"
PREPAIDHOST_PLAN="${PREPAIDHOST_PLAN:-vps-basic}"

prepaidhost_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${PREPAIDHOST_API_KEY}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${PREPAIDHOST_API_BASE}${endpoint}"
}

prepaidhost_parse_body() { echo "$1" | sed '$d'; }
prepaidhost_parse_code() { echo "$1" | tail -1; }

prepaidhost_platform_init() {
    if [[ -z "${PREPAIDHOST_API_KEY:-}" ]]; then
        log_error "[prepaidhost] PREPAIDHOST_API_KEY is required"
        return 1
    fi
    log_info "[prepaidhost] Platform initialized (location: ${PREPAIDHOST_LOCATION})"
    return 0
}

prepaidhost_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[prepaidhost] Creating VPS: env=${env_type}"
    # Monthly billing - try reusing existing instance
    local list_resp; list_resp=$(prepaidhost_request "GET" "/servers")
    local list_body; list_body=$(prepaidhost_parse_body "$list_resp")
    local existing_id; existing_id=$(echo "$list_body" | jq -r '.data[0].id // .servers[0].id // empty' 2>/dev/null)
    local existing_ip; existing_ip=$(echo "$list_body" | jq -r '.data[0].ip // .data[0].ipv4 // .servers[0].ip // empty' 2>/dev/null)
    if [[ -n "$existing_id" && -n "$existing_ip" ]]; then
        log_info "[prepaidhost] Reusing existing VPS ${existing_id} (IP: ${existing_ip}), will reinstall"
        if prepaidhost_platform_reinstall_instance "$existing_id" "debian"; then
            echo "{\"instance_id\":\"${existing_id}\",\"ipv4\":\"${existing_ip}\",\"password\":\"${PLATFORM_SSH_PASSWORD}\",\"ssh_user\":\"root\",\"platform\":\"prepaidhost\"}"
            return 0
        fi
    fi
    local os="debian-12"
    [[ "${env_type}" == "lxd" ]] && os="ubuntu-24.04"
    local password="${PREPAIDHOST_ROOT_PASSWORD:-$(openssl rand -base64 24)}"
    local data="{\"plan\":\"${PREPAIDHOST_PLAN}\",\"location\":\"${PREPAIDHOST_LOCATION}\",\"os\":\"${os}\",\"password\":\"${password}\"}"
    local resp; resp=$(prepaidhost_request "POST" "/servers" "$data")
    local body; body=$(prepaidhost_parse_body "${resp}")
    local code; code=$(prepaidhost_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "201" ]]; then
        log_error "[prepaidhost] Create failed (HTTP ${code}): ${body}"
        return 1
    fi
    local id; id=$(echo "$body" | jq -r '.data.id // .id // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "[prepaidhost] No VPS ID in response"; return 1; }
    PLATFORM_SSH_PASSWORD="${password}"
    local max=600 elapsed=0
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(prepaidhost_request "GET" "/servers/${id}")
        local sb; sb=$(prepaidhost_parse_body "$sr")
        local status; status=$(echo "$sb" | jq -r '.data.status // .status // empty' 2>/dev/null)
        local ip; ip=$(echo "$sb" | jq -r '.data.ip // .data.ipv4 // .ip // empty' 2>/dev/null)
        if [[ "$status" == "active" || "$status" == "running" ]] && [[ -n "$ip" ]]; then
            log_success "[prepaidhost] VPS ${id} ready: IP=${ip}"
            echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"prepaidhost\"}"
            return 0
        fi
        sleep 15; elapsed=$((elapsed + 15))
    done
    log_error "[prepaidhost] VPS ${id} timeout after ${max}s"
    return 1
}

prepaidhost_platform_delete_instance() {
    local id="$1"
    log_info "[prepaidhost] Deleting VPS ${id}..."
    prepaidhost_request "DELETE" "/servers/${id}"
    return 0
}

prepaidhost_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[prepaidhost] Reinstalling VPS ${id}..."
    local os="debian-12"
    [[ "$os_name" == *"ubuntu"* ]] && os="ubuntu-24.04"
    local password="${PREPAIDHOST_ROOT_PASSWORD:-$(openssl rand -base64 24)}"
    local data="{\"os\":\"${os}\",\"password\":\"${password}\"}"
    local resp; resp=$(prepaidhost_request "POST" "/servers/${id}/reinstall" "$data")
    local code; code=$(prepaidhost_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "204" ]]; then
        local body; body=$(prepaidhost_parse_body "${resp}")
        log_error "[prepaidhost] Reinstall failed (HTTP ${code}): ${body}"
        return 1
    fi
    PLATFORM_SSH_PASSWORD="${password}"
    log_info "[prepaidhost] Reinstall initiated, waiting for completion..."
    sleep 60
    return 0
}

prepaidhost_platform_list_instances() {
    local resp; resp=$(prepaidhost_request "GET" "/servers")
    local body; body=$(prepaidhost_parse_body "${resp}")
    echo "$body" | jq -c '[.data[]? // .servers[]? | {instance_id: (.id|tostring), ipv4: (.ip // .ipv4), status: .status}]' 2>/dev/null
}

prepaidhost_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    if [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[prepaidhost] No SSH credentials (password auth only)"; return 1
    fi
}

prepaidhost_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "[prepaidhost] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        if [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 "root@${ip}" "echo ok" >/dev/null 2>&1 && {
                log_success "[prepaidhost] SSH ready on ${ip}"; return 0
            }
        fi
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[prepaidhost] SSH timeout on ${ip} after ${max}s"; return 1
}
