#!/bin/bash
# Vultr Platform API Provider
# Main website：https://www.vultr.com/?ref=9124520-8H
# API Docs: https://www.vultr.com/api/

VULTR_API_BASE="${VULTR_API_BASE:-https://api.vultr.com/v2}"
VULTR_API_KEY="${VULTR_API_KEY:-}"
VULTR_REGION="${VULTR_REGION:-ewr}"
VULTR_PLAN="${VULTR_PLAN:-vc2-1c-1gb}"
VULTR_PASSWORD="${VULTR_PASSWORD:-}"

vultr_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "Authorization: Bearer ${VULTR_API_KEY}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${VULTR_API_BASE}${endpoint}"
}

vultr_parse_body() { echo "$1" | sed '$d'; }
vultr_parse_code() { echo "$1" | tail -1; }

vultr_platform_init() {
    if [[ -z "${VULTR_API_KEY:-}" ]]; then
        log_error "[vultr] VULTR_API_KEY is required"
        return 1
    fi
    if [[ -n "${VULTR_PRIVATE_KEY:-}" ]]; then
        PLATFORM_SSH_KEY_FILE=$(mktemp /tmp/platform_ssh_key_XXXXXX.pem)
        chmod 600 "${PLATFORM_SSH_KEY_FILE}"
        printf '%s\n' "${VULTR_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}"
    fi
    log_info "[vultr] Platform initialized (region: ${VULTR_REGION})"
    return 0
}

vultr_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[vultr] Creating instance: env=${env_type}"
    # Get Debian or Ubuntu OS ID
    local os_name="Debian" os_id=""
    [[ "${env_type}" == "lxd" ]] && os_name="Ubuntu"
    local os_resp; os_resp=$(vultr_request "GET" "/os")
    local os_body; os_body=$(vultr_parse_body "$os_resp")
    os_id=$(echo "$os_body" | jq -r "[.os[] | select(.name | test(\"${os_name}\";\"i\")) | select(.arch == \"x64\")][0].id // empty" 2>/dev/null)
    [[ -z "$os_id" ]] && { log_error "[vultr] No ${os_name} OS found"; return 1; }
    # Create SSH key on Vultr if we have a public key
    local sshkey_ids="[]"
    if [[ -n "${VULTR_SSH_KEY_ID:-}" ]]; then
        sshkey_ids="[\"${VULTR_SSH_KEY_ID}\"]"
    fi
    local label="ci-test-$(date +%s)"
    local data="{\"region\":\"${VULTR_REGION}\",\"plan\":\"${VULTR_PLAN}\",\"os_id\":${os_id},\"label\":\"${label}\",\"sshkey_id\":${sshkey_ids},\"backups\":\"disabled\"}"
    local resp; resp=$(vultr_request "POST" "/instances" "$data")
    local body; body=$(vultr_parse_body "${resp}")
    local http_code; http_code=$(vultr_parse_code "${resp}")
    if [[ "${http_code}" != "200" && "${http_code}" != "202" ]]; then
        log_error "[vultr] Create failed (HTTP ${http_code}): ${body}"
        return 1
    fi
    local id; id=$(echo "$body" | jq -r '.instance.id // empty' 2>/dev/null)
    local password; password=$(echo "$body" | jq -r '.instance.default_password // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "[vultr] No instance ID in response"; return 1; }
    PLATFORM_SSH_PASSWORD="${password}"
    # Wait for instance to become active
    local max=600 elapsed=0
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(vultr_request "GET" "/instances/${id}")
        local sb; sb=$(vultr_parse_body "$sr")
        local status; status=$(echo "$sb" | jq -r '.instance.status // empty' 2>/dev/null)
        local power; power=$(echo "$sb" | jq -r '.instance.power_status // empty' 2>/dev/null)
        local ip; ip=$(echo "$sb" | jq -r '.instance.main_ip // empty' 2>/dev/null)
        if [[ "$status" == "active" && "$power" == "running" && "$ip" != "0.0.0.0" && -n "$ip" ]]; then
            log_success "[vultr] Instance ${id} ready: IP=${ip}"
            echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"vultr\"}"
            return 0
        fi
        sleep 15; elapsed=$((elapsed + 15))
    done
    log_error "[vultr] Instance ${id} timeout after ${max}s"
    return 1
}

vultr_platform_delete_instance() {
    local id="$1"
    log_info "[vultr] Deleting instance ${id}..."
    vultr_request "DELETE" "/instances/${id}"
    return 0
}

vultr_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[vultr] Reinstalling instance ${id}..."
    local resp; resp=$(vultr_request "POST" "/instances/${id}/reinstall")
    local body; body=$(vultr_parse_body "${resp}")
    local code; code=$(vultr_parse_code "${resp}")
    if [[ "$code" != "202" && "$code" != "204" ]]; then
        log_error "[vultr] Reinstall failed (HTTP ${code}): ${body}"
        return 1
    fi
    local password; password=$(echo "$body" | jq -r '.instance.default_password // empty' 2>/dev/null)
    [[ -n "$password" ]] && PLATFORM_SSH_PASSWORD="$password"
    sleep 30
    local sr; sr=$(vultr_request "GET" "/instances/${id}")
    local sb; sb=$(vultr_parse_body "$sr")
    local ip; ip=$(echo "$sb" | jq -r '.instance.main_ip // empty' 2>/dev/null)
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"ssh_user\":\"root\",\"platform\":\"vultr\"}"
}

vultr_platform_list_instances() {
    local resp; resp=$(vultr_request "GET" "/instances")
    local body; body=$(vultr_parse_body "${resp}")
    echo "$body" | jq -c '[.instances[]? | {instance_id: .id, ipv4: .main_ip, status: .status}]' 2>/dev/null
}

vultr_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 -o BatchMode=yes "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 "root@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[vultr] No SSH credentials"; return 1
    fi
}

vultr_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    log_info "[vultr] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local ok=false
        if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
            ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 -o BatchMode=yes "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 "root@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        fi
        $ok && { log_success "[vultr] SSH ready on ${ip}"; return 0; }
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[vultr] SSH timeout on ${ip} after ${max}s"; return 1
}
