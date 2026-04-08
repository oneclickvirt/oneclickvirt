#!/bin/bash
# CloudSigma Platform API Provider
# https://tyo.cloudsigma.com/ui/?affid=hhualong24502
# API Docs: https://cloudsigma-docs.readthedocs.io/

CLOUDSIGMA_API_BASE="${CLOUDSIGMA_API_BASE:-https://zrh.cloudsigma.com/api/2.0}"
CLOUDSIGMA_EMAIL="${CLOUDSIGMA_EMAIL:-}"
CLOUDSIGMA_PASSWORD="${CLOUDSIGMA_PASSWORD:-}"

cloudsigma_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -u "${CLOUDSIGMA_EMAIL}:${CLOUDSIGMA_PASSWORD}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${CLOUDSIGMA_API_BASE}${endpoint}"
}

cloudsigma_parse_body() { echo "$1" | sed '$d'; }
cloudsigma_parse_code() { echo "$1" | tail -1; }

cloudsigma_platform_init() {
    if [[ -z "${CLOUDSIGMA_EMAIL:-}" || -z "${CLOUDSIGMA_PASSWORD:-}" ]]; then
        log_error "[cloudsigma] CLOUDSIGMA_EMAIL and CLOUDSIGMA_PASSWORD are required"
        return 1
    fi
    if [[ -n "${CLOUDSIGMA_PRIVATE_KEY:-}" ]]; then
        PLATFORM_SSH_KEY_FILE=$(mktemp /tmp/platform_ssh_key_XXXXXX.pem)
        chmod 600 "${PLATFORM_SSH_KEY_FILE}"
        printf '%s\n' "${CLOUDSIGMA_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}"
    fi
    log_info "[cloudsigma] Platform initialized"
    return 0
}

cloudsigma_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[cloudsigma] Creating server: env=${env_type}"
    local vnc_pass="${CLOUDSIGMA_VNC_PASSWORD:-$(openssl rand -base64 12)}"
    # Create a drive from library image
    local lib_image_name="Debian 12"
    [[ "${env_type}" == "lxd" ]] && lib_image_name="Ubuntu 24.04"
    # List library and find suitable image
    local lib_resp; lib_resp=$(cloudsigma_request "GET" "/libdrives/?limit=100")
    local lib_body; lib_body=$(cloudsigma_parse_body "$lib_resp")
    local image_uuid; image_uuid=$(echo "$lib_body" | jq -r "[.objects[]? | select(.name | test(\"${lib_image_name}\";\"i\"))][0].uuid // empty" 2>/dev/null)
    if [[ -z "$image_uuid" ]]; then
        log_error "[cloudsigma] No library image found for ${lib_image_name}"
        return 1
    fi
    # Clone library drive
    local clone_resp; clone_resp=$(cloudsigma_request "POST" "/libdrives/${image_uuid}/action/?do=clone" "{\"name\":\"ci-drive-$(date +%s)\"}")
    local clone_body; clone_body=$(cloudsigma_parse_body "$clone_resp")
    local drive_uuid; drive_uuid=$(echo "$clone_body" | jq -r '.objects[0].uuid // empty' 2>/dev/null)
    [[ -z "$drive_uuid" ]] && { log_error "[cloudsigma] Failed to clone drive"; return 1; }
    sleep 10
    # Create server
    local server_name="ci-test-$(date +%s)"
    local data="{\"name\":\"${server_name}\",\"cpu\":2000,\"mem\":2147483648,\"vnc_password\":\"${vnc_pass}\",\"drives\":[{\"boot_order\":1,\"dev_channel\":\"0:0\",\"device\":\"virtio\",\"drive\":\"${drive_uuid}\"}],\"nics\":[{\"ip_v4_conf\":{\"conf\":\"dhcp\"}}]}"
    local resp; resp=$(cloudsigma_request "POST" "/servers/" "$data")
    local body; body=$(cloudsigma_parse_body "${resp}")
    local code; code=$(cloudsigma_parse_code "${resp}")
    if [[ "$code" != "201" && "$code" != "200" ]]; then
        log_error "[cloudsigma] Create failed (HTTP ${code}): ${body}"
        return 1
    fi
    local id; id=$(echo "$body" | jq -r '.uuid // .objects[0].uuid // empty' 2>/dev/null)
    [[ -z "$id" ]] && { log_error "[cloudsigma] No server UUID in response"; return 1; }
    # Start server
    cloudsigma_request "POST" "/servers/${id}/action/?do=start" "" >/dev/null 2>&1
    # Wait for running + IP
    local max=300 elapsed=0 ip=""
    while [[ $elapsed -lt $max ]]; do
        local sr; sr=$(cloudsigma_request "GET" "/servers/${id}/")
        local sb; sb=$(cloudsigma_parse_body "$sr")
        local status; status=$(echo "$sb" | jq -r '.status // empty' 2>/dev/null)
        ip=$(echo "$sb" | jq -r '.runtime.nics[0].ip_v4.uuid // .nics[0].runtime.ip_v4.uuid // empty' 2>/dev/null)
        [[ -z "$ip" ]] && ip=$(echo "$sb" | jq -r '.runtime.nics[0].ip_v4 // empty' 2>/dev/null)
        if [[ "$status" == "running" && -n "$ip" && "$ip" != "null" ]]; then
            log_success "[cloudsigma] Server ${id} ready: IP=${ip}"
            echo "{\"instance_id\":\"${id}\",\"drive_uuid\":\"${drive_uuid}\",\"ipv4\":\"${ip}\",\"password\":\"${vnc_pass}\",\"ssh_user\":\"cloudsigma\",\"platform\":\"cloudsigma\"}"
            return 0
        fi
        sleep 15; elapsed=$((elapsed + 15))
    done
    log_error "[cloudsigma] Server ${id} timeout after ${max}s"
    return 1
}

cloudsigma_platform_delete_instance() {
    local id="$1"
    log_info "[cloudsigma] Stopping and deleting server ${id}..."
    cloudsigma_request "POST" "/servers/${id}/action/?do=stop" "" >/dev/null 2>&1
    sleep 10
    cloudsigma_request "DELETE" "/servers/${id}/" "" >/dev/null 2>&1
    return 0
}

cloudsigma_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_error "[cloudsigma] Reinstall not directly supported - use delete+create"
    return 1
}

cloudsigma_platform_list_instances() {
    local resp; resp=$(cloudsigma_request "GET" "/servers/detail/")
    local body; body=$(cloudsigma_parse_body "${resp}")
    echo "$body" | jq -c '[.objects[]? | {instance_id: .uuid, ipv4: (.runtime.nics[0].ip_v4 // ""), status: .status}]' 2>/dev/null
}

cloudsigma_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    local ssh_user="${CLOUDSIGMA_SSH_USER:-cloudsigma}"
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o BatchMode=yes "${ssh_user}@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 "${ssh_user}@${ip}" "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[cloudsigma] No SSH credentials"; return 1
    fi
}

cloudsigma_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    local ssh_user="${CLOUDSIGMA_SSH_USER:-cloudsigma}"
    log_info "[cloudsigma] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local ok=false
        if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
            ssh -i "${PLATFORM_SSH_KEY_FILE}" -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o BatchMode=yes "${ssh_user}@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 "${ssh_user}@${ip}" "echo ok" >/dev/null 2>&1 && ok=true
        fi
        $ok && { log_success "[cloudsigma] SSH ready on ${ip}"; return 0; }
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[cloudsigma] SSH timeout on ${ip} after ${max}s"; return 1
}
