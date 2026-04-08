#!/bin/bash
# Skrime Platform API Provider
# https://skrime.eu/a/server
# Auth: API key based, supports password auth for instances

SKRIME_API_BASE="${SKRIME_API_BASE:-https://api.skrime.eu/v2}"
SKRIME_API_KEY="${SKRIME_API_KEY:-}"
SKRIME_LOCATION="${SKRIME_LOCATION:-de}"
SKRIME_PLAN="${SKRIME_PLAN:-vps-s}"

skrime_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${SKRIME_API_KEY}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${SKRIME_API_BASE}${endpoint}"
}

skrime_parse_body() { echo "$1" | sed '$d'; }
skrime_parse_code() { echo "$1" | tail -1; }

skrime_platform_init() {
    if [[ -z "${SKRIME_API_KEY:-}" ]]; then
        log_error "[skrime] SKRIME_API_KEY is required"
        return 1
    fi
    log_info "[skrime] Platform initialized (location: ${SKRIME_LOCATION})"
    return 0
}

skrime_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[skrime] Creating VPS: env=${env_type}"
    # Check existing instances first (monthly billing - may want to reuse)
    local list_resp; list_resp=$(skrime_request "GET" "/vps")
    local list_body; list_body=$(skrime_parse_body "$list_resp")
    local existing; existing=$(echo "$list_body" | jq -r '.data[0] // empty' 2>/dev/null)
    if [[ -n "$existing" && "$existing" != "null" ]]; then
        local eid; eid=$(echo "$list_body" | jq -r '.data[0].id // empty' 2>/dev/null)
        local eip; eip=$(echo "$list_body" | jq -r '.data[0].ip // .data[0].ipv4 // empty' 2>/dev/null)
        local estatus; estatus=$(echo "$list_body" | jq -r '.data[0].status // empty' 2>/dev/null)
        if [[ -n "$eid" && -n "$eip" ]]; then
            log_info "[skrime] Reusing existing VPS ${eid} (IP: ${eip}), will reinstall"
            if skrime_platform_reinstall_instance "$eid" "debian"; then
                local ri; ri=$(skrime_request "GET" "/vps/${eid}")
                local rb; rb=$(skrime_parse_body "$ri")
                eip=$(echo "$rb" | jq -r '.ip // .ipv4 // empty' 2>/dev/null)
                echo "{\"instance_id\":\"${eid}\",\"ipv4\":\"${eip}\",\"password\":\"${PLATFORM_SSH_PASSWORD}\",\"ssh_user\":\"root\",\"platform\":\"skrime\"}"
                return 0
            fi
        fi
    fi
    # Create new VPS
    local os="debian-12"
    [[ "${env_type}" == "lxd" ]] && os="ubuntu-24.04"
    local password="${SKRIME_ROOT_PASSWORD:-$(openssl rand -base64 24)}"
    local data="{\"plan\":\"${SKRIME_PLAN}\",\"location\":\"${SKRIME_LOCATION}\",\"os\":\"${os}\",\"password\":\"${password}\"}"
    local resp; resp=$(skrime_request "POST" "/vps" "$data")
    local body; body=$(skrime_parse_body "${resp}")
    local code; code=$(skrime_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "201" ]]; then
        log_error "[skrime] Create failed (HTTP ${code}): ${body}"
        return 1
    fi
    local id; id=$(echo "$body" | jq -r '.data.id // .id // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "[skrime] No VPS ID in response"; return 1; }
    PLATFORM_SSH_PASSWORD="${password}"
    local max=600 elapsed=0
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(skrime_request "GET" "/vps/${id}")
        local sb; sb=$(skrime_parse_body "$sr")
        local status; status=$(echo "$sb" | jq -r '.data.status // .status // empty' 2>/dev/null)
        local ip; ip=$(echo "$sb" | jq -r '.data.ip // .data.ipv4 // .ip // empty' 2>/dev/null)
        if [[ "$status" == "active" || "$status" == "running" ]] && [[ -n "$ip" ]]; then
            log_success "[skrime] VPS ${id} ready: IP=${ip}"
            echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"skrime\"}"
            return 0
        fi
        sleep 15; elapsed=$((elapsed + 15))
    done
    log_error "[skrime] VPS ${id} timeout after ${max}s"
    return 1
}

skrime_platform_delete_instance() {
    local id="$1"
    log_info "[skrime] Deleting VPS ${id}..."
    skrime_request "DELETE" "/vps/${id}"
    return 0
}

skrime_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[skrime] Reinstalling VPS ${id}..."
    local os="debian-12"
    [[ "$os_name" == *"ubuntu"* ]] && os="ubuntu-24.04"
    local password="${SKRIME_ROOT_PASSWORD:-$(openssl rand -base64 24)}"
    local data="{\"os\":\"${os}\",\"password\":\"${password}\"}"
    local resp; resp=$(skrime_request "POST" "/vps/${id}/reinstall" "$data")
    local code; code=$(skrime_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "204" ]]; then
        local body; body=$(skrime_parse_body "${resp}")
        log_error "[skrime] Reinstall failed (HTTP ${code}): ${body}"
        return 1
    fi
    PLATFORM_SSH_PASSWORD="${password}"
    log_info "[skrime] Reinstall initiated, waiting for completion..."
    sleep 60
    return 0
}

skrime_platform_list_instances() {
    local resp; resp=$(skrime_request "GET" "/vps")
    local body; body=$(skrime_parse_body "${resp}")
    echo "$body" | jq -c '[.data[]? | {instance_id: (.id|tostring), ipv4: (.ip // .ipv4), status: .status}]' 2>/dev/null
}

skrime_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    if [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[skrime] No SSH credentials (password auth only)"; return 1
    fi
}

skrime_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "[skrime] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        if [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 "root@${ip}" "echo ok" >/dev/null 2>&1 && {
                log_success "[skrime] SSH ready on ${ip}"; return 0
            }
        fi
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[skrime] SSH timeout on ${ip} after ${max}s"; return 1
}
