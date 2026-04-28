#!/bin/bash
# LightNode Platform API Provider
# https://www.lightnode.com/?inviteCode=QOIU9D&promoteWay=LINK
# API Docs: https://apidoc.lightnode.com/cn/327190862e0

LIGHTNODE_API_BASE="${LIGHTNODE_API_BASE:-https://openapi.lightnode.com}"
LIGHTNODE_TOKEN="${LIGHTNODE_TOKEN:-}"
LIGHTNODE_REGION="${LIGHTNODE_REGION:-}"
LIGHTNODE_ZONE="${LIGHTNODE_ZONE:-}"
# Password rules: 8-30 chars, upper+lower+digit + one of: ()`~!@#$*-+={}[]:;,.?/
LIGHTNODE_PASSWORD="${LIGHTNODE_PASSWORD:-CiTest1234!}"

# ============================================================================
# Low-level API helpers
# ============================================================================
lightnode_request() {
    local method="$1" endpoint="$2" data="${3:-}"
    local args=(-s -w "\n%{http_code}" --max-time 120
        -H "x-open-token: ${LIGHTNODE_TOKEN}"
        -H "Content-Type: application/json"
        -X "${method}")
    [[ -n "$data" ]] && args+=(-d "$data")
    curl "${args[@]}" "${LIGHTNODE_API_BASE}${endpoint}"
}

lightnode_parse_body() { echo "$1" | sed '$d'; }
lightnode_parse_code() { echo "$1" | tail -1; }

lightnode_get_regions() { lightnode_request "GET" "/region/list"; }
lightnode_get_packages() {
    local region="${1:-${LIGHTNODE_REGION}}" zone="${2:-${LIGHTNODE_ZONE}}"
    local qs=""
    [[ -n "$region" ]] && qs="regionCode=${region}"
    [[ -n "$zone" ]] && qs="${qs:+${qs}&}zoneCode=${zone}"
    [[ -n "$qs" ]] && qs="?${qs}"
    lightnode_request "GET" "/package/list${qs}"
}
lightnode_get_images() {
    local region="${1:-${LIGHTNODE_REGION}}"
    local qs="pageSize=50"
    [[ -n "$region" ]] && qs="${qs}&regionCode=${region}&imageType=System"
    lightnode_request "GET" "/image/list?${qs}"
}
lightnode_get_ssh_keys() { lightnode_request "GET" "/sshKey/list"; }

lightnode_get_instance_detail() {
    lightnode_request "GET" "/instance/detail?ecsResourceUUID=$1"
}

lightnode_get_async_task() {
    lightnode_request "GET" "/asynctask/getResult?asyncTaskUUID=$1"
}

lightnode_list_instances_raw() {
    local region="${1:-${LIGHTNODE_REGION}}" zone="${2:-${LIGHTNODE_ZONE}}"
    lightnode_request "GET" "/instance/list?regionCode=${region}&zoneCode=${zone}"
}

# ============================================================================
# Internal helpers
# ============================================================================
_lightnode_auto_detect_region() {
    if [[ -n "${LIGHTNODE_REGION}" && -n "${LIGHTNODE_ZONE}" ]]; then
        return 0
    fi
    log_info "[lightnode] Auto-detecting available region..."
    local resp; resp=$(lightnode_get_regions)
    local body; body=$(lightnode_parse_body "$resp")
    local code; code=$(lightnode_parse_code "$resp")
    if [[ "$code" != "200" && "$code" != "202" ]]; then
        log_error "[lightnode] Failed to get regions (HTTP ${code})"
        return 1
    fi
    LIGHTNODE_REGION=$(echo "$body" | jq -r '.regions[0].regionCode // empty' 2>/dev/null)
    LIGHTNODE_ZONE=$(echo "$body" | jq -r '.regions[0].zones[0].zoneCode // empty' 2>/dev/null)
    if [[ -z "$LIGHTNODE_REGION" || -z "$LIGHTNODE_ZONE" ]]; then
        log_error "[lightnode] No regions available"
        return 1
    fi
    log_info "[lightnode] Using region=${LIGHTNODE_REGION} zone=${LIGHTNODE_ZONE}"
}

_lightnode_get_cheapest_package() {
    local resp; resp=$(lightnode_get_packages)
    local body; body=$(lightnode_parse_body "$resp")
    # Filter to current region/zone to avoid picking a package unavailable here
    local code; code=$(lightnode_parse_code "$resp")
    if [[ -n "${LIGHTNODE_REGION}" ]]; then
        echo "$body" | jq -r "[.packages[]? | select(.regionCode == \"${LIGHTNODE_REGION}\")][0].packageCode // .packages[0].packageCode // empty" 2>/dev/null
    else
        echo "$body" | jq -r '.packages[0].packageCode // empty' 2>/dev/null
    fi
}

_lightnode_get_image_uuid() {
    local name="${1:-debian}"
    local resp; resp=$(lightnode_get_images)
    local body; body=$(lightnode_parse_body "$resp")
    # Match against osDistroVersion (the canonical OS field, e.g. "Debian") OR imageName.
    # The API docs show imageName is a user-defined display name; osDistroVersion is the
    # actual OS distribution string reliably set by the platform.
    echo "$body" | jq -r "[.images[]? | select((.osDistroVersion // \"\" | test(\"${name}\";\"i\")) or (.imageName // \"\" | test(\"${name}\";\"i\")))][0].imageResourceUUID // empty" 2>/dev/null
}

_lightnode_wait_async_task() {
    local task_uuid="$1" max="${2:-600}" interval="${3:-15}" elapsed=0
    log_info "[lightnode] Waiting for async task ${task_uuid} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local resp; resp=$(lightnode_get_async_task "${task_uuid}")
        local body; body=$(lightnode_parse_body "${resp}")
        local result; result=$(echo "$body" | jq -r '.asyncTaskInfo.processResult // empty' 2>/dev/null)
        local status; status=$(echo "$body" | jq -r '.asyncTaskInfo.taskStatus // empty' 2>/dev/null)
        log_debug "[lightnode] Task ${task_uuid}: result=${result} status=${status}"
        if [[ "$result" == "SUCCESS" ]]; then
            log_success "[lightnode] Task ${task_uuid} completed"
            return 0
        elif [[ "$result" == "FAIL" || "$result" == "CANCEL" ]]; then
            log_error "[lightnode] Task ${task_uuid} failed: ${result}"
            return 1
        fi
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[lightnode] Task ${task_uuid} timeout after ${max}s"
    return 1
}

# ============================================================================
# Standard Platform Interface Implementation
# ============================================================================

lightnode_platform_init() {
    if [[ -z "${LIGHTNODE_TOKEN:-}" ]]; then
        log_error "[lightnode] LIGHTNODE_TOKEN is required"
        return 1
    fi
    _lightnode_auto_detect_region || return 1
    # Verify at least one usable OS image exists in the selected region
    local image_uuid; image_uuid=$(_lightnode_get_image_uuid "debian")
    if [[ -z "$image_uuid" ]]; then
        image_uuid=$(_lightnode_get_image_uuid "ubuntu")
        if [[ -z "$image_uuid" ]]; then
            log_error "[lightnode] No debian or ubuntu images available in region ${LIGHTNODE_REGION}"
            return 1
        fi
        log_info "[lightnode] No debian image found; ubuntu image available as fallback"
    fi
    # LightNode supports password auth - write password for SSH
    PLATFORM_SSH_PASSWORD="${LIGHTNODE_PASSWORD}"
    # Also support SSH key if LIGHTNODE_PRIVATE_KEY is set
    if [[ -n "${LIGHTNODE_PRIVATE_KEY:-}" ]]; then
        PLATFORM_SSH_KEY_FILE=$(mktemp /tmp/platform_ssh_key_XXXXXX.pem)
        chmod 600 "${PLATFORM_SSH_KEY_FILE}"
        printf '%s\n' "${LIGHTNODE_PRIVATE_KEY}" > "${PLATFORM_SSH_KEY_FILE}"
    fi
    log_info "[lightnode] Platform initialized"
    return 0
}

lightnode_platform_create_instance() {
    local env_type="$1" hours="${2:-8}"
    log_info "[lightnode] Creating instance: env=${env_type}"
    local package_code; package_code=$(_lightnode_get_cheapest_package)
    [[ -z "$package_code" ]] && { log_error "[lightnode] No packages available"; return 1; }
    local os_name="debian"
    [[ "${env_type}" == "lxd" ]] && os_name="ubuntu"
    local image_uuid; image_uuid=$(_lightnode_get_image_uuid "${os_name}")
    [[ -z "$image_uuid" ]] && { log_error "[lightnode] No ${os_name} image found"; return 1; }
    local ssh_key_uuid=""
    if [[ -n "${LIGHTNODE_SSH_KEY_UUID:-}" ]]; then
        ssh_key_uuid="\"sshKeyUUID\":\"${LIGHTNODE_SSH_KEY_UUID}\","
    fi
    local instance_name="ci-test-$(date +%Y%m%d%H%M%S)"
    local data="{\"packageConfig\":{\"packageCode\":\"${package_code}\",\"regionCode\":\"${LIGHTNODE_REGION}\",\"zoneCode\":\"${LIGHTNODE_ZONE}\",\"instanceName\":\"${instance_name}\",\"imageResourceUUID\":\"${image_uuid}\",${ssh_key_uuid}\"password\":\"${LIGHTNODE_PASSWORD}\"}}"
    local resp; resp=$(lightnode_request "POST" "/instance/create" "$data")
    local body; body=$(lightnode_parse_body "${resp}")
    local http_code; http_code=$(lightnode_parse_code "${resp}")
    if [[ "${http_code}" != "200" && "${http_code}" != "202" ]]; then
        log_error "[lightnode] Create failed (HTTP ${http_code}): ${body}"
        return 1
    fi
    local task_uuid; task_uuid=$(echo "$body" | jq -r '.asyncTaskInfo.asyncTaskUUID // empty' 2>/dev/null)
    local ecs_uuid; ecs_uuid=$(echo "$body" | jq -r '.asyncTaskInfo.ecsResourceUUID // empty' 2>/dev/null)
    [[ -z "$ecs_uuid" ]] && { log_error "[lightnode] No ecsResourceUUID in response"; return 1; }
    log_success "[lightnode] Instance creation requested: ${ecs_uuid}"
    _lightnode_wait_async_task "${task_uuid}" 600 || return 1
    # Get instance details
    local detail_resp; detail_resp=$(lightnode_get_instance_detail "${ecs_uuid}")
    local detail_body; detail_body=$(lightnode_parse_body "${detail_resp}")
    local ip; ip=$(echo "$detail_body" | jq -r '.instance.publicIpAddress // empty' 2>/dev/null)
    local ssh_user; ssh_user=$(echo "$detail_body" | jq -r '.instance.sysAccount // "root"' 2>/dev/null)
    [[ -z "$ip" ]] && { log_error "[lightnode] Cannot get IP for ${ecs_uuid}"; return 1; }
    echo "{\"instance_id\":\"${ecs_uuid}\",\"ipv4\":\"${ip}\",\"password\":\"${LIGHTNODE_PASSWORD}\",\"ssh_user\":\"${ssh_user}\",\"platform\":\"lightnode\"}"
}

lightnode_platform_delete_instance() {
    local id="$1"
    log_info "[lightnode] Releasing instance ${id}..."
    local data="{\"ecsResourceUUID\":\"${id}\"}"
    local resp; resp=$(lightnode_request "POST" "/instance/release" "$data")
    local body; body=$(lightnode_parse_body "${resp}")
    local code; code=$(lightnode_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "202" ]]; then
        log_error "[lightnode] Release failed (HTTP ${code}): ${body}"
        return 1
    fi
    local task_uuid; task_uuid=$(echo "$body" | jq -r '.asyncTaskInfo.asyncTaskUUID // empty' 2>/dev/null)
    [[ -n "$task_uuid" ]] && _lightnode_wait_async_task "${task_uuid}" 300
    return 0
}

lightnode_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[lightnode] Reinstalling instance ${id} with ${os_name}..."

    # LightNode requires the instance to be in STOPPED state before reinstall.
    # Check current status and force-stop if needed.
    local chk_resp; chk_resp=$(lightnode_get_instance_detail "${id}")
    local chk_body; chk_body=$(lightnode_parse_body "${chk_resp}")
    local cur_status; cur_status=$(echo "$chk_body" | jq -r '.instance.ecsStatus // empty' 2>/dev/null)
    if [[ "$cur_status" != "STOPPED" && "$cur_status" != "stopped" ]]; then
        log_info "[lightnode] Instance ${id} status='${cur_status}', force-stopping before reinstall..."
        local stop_resp; stop_resp=$(lightnode_request "POST" "/instance/stop" "{\"ecsResourceUUID\":\"${id}\",\"forceStop\":true}")
        local stop_body; stop_body=$(lightnode_parse_body "${stop_resp}")
        local stop_code; stop_code=$(lightnode_parse_code "${stop_resp}")
        if [[ "$stop_code" == "200" || "$stop_code" == "202" ]]; then
            local stop_task; stop_task=$(echo "$stop_body" | jq -r '.asyncTaskUUID // empty' 2>/dev/null)
            [[ -n "$stop_task" ]] && _lightnode_wait_async_task "${stop_task}" 180 || true
        else
            log_warning "[lightnode] Stop returned HTTP ${stop_code}, proceeding anyway..."
        fi
    fi

    local image_uuid; image_uuid=$(_lightnode_get_image_uuid "${os_name}")
    [[ -z "$image_uuid" ]] && { log_error "[lightnode] No ${os_name} image found"; return 1; }
    local ssh_key_field=""
    if [[ -n "${LIGHTNODE_SSH_KEY_UUID:-}" ]]; then
        ssh_key_field="\"sshKeyResourceUUID\":\"${LIGHTNODE_SSH_KEY_UUID}\","
    fi
    local data="{\"ecsResourceUUID\":\"${id}\",\"password\":\"${LIGHTNODE_PASSWORD}\",\"imageResourceUUID\":\"${image_uuid}\",${ssh_key_field}\"regionCode\":\"${LIGHTNODE_REGION}\"}"
    local resp; resp=$(lightnode_request "POST" "/instance/reinstallSystem" "$data")
    local body; body=$(lightnode_parse_body "${resp}")
    local code; code=$(lightnode_parse_code "${resp}")
    if [[ "$code" != "200" && "$code" != "202" ]]; then
        log_error "[lightnode] Reinstall failed (HTTP ${code}): ${body}"
        return 1
    fi
    local task_uuid; task_uuid=$(echo "$body" | jq -r '.asyncTaskUUID // empty' 2>/dev/null)
    [[ -n "$task_uuid" ]] && _lightnode_wait_async_task "${task_uuid}" 600
    # Get updated details
    local detail_resp; detail_resp=$(lightnode_get_instance_detail "${id}")
    local detail_body; detail_body=$(lightnode_parse_body "${detail_resp}")
    local ip; ip=$(echo "$detail_body" | jq -r '.instance.publicIpAddress // empty' 2>/dev/null)
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${LIGHTNODE_PASSWORD}\",\"ssh_user\":\"root\",\"platform\":\"lightnode\"}"
}

lightnode_platform_list_instances() {
    local resp; resp=$(lightnode_list_instances_raw)
    local body; body=$(lightnode_parse_body "${resp}")
    echo "$body" | jq -c '[.instances[]? | {instance_id: .ecsResourceUUID, ipv4: .publicIpAddress, status: .ecsStatus}]' 2>/dev/null
}

lightnode_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    local ssh_user; ssh_user=$(get_platform_ssh_user "lightnode")
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        ssh -i "${PLATFORM_SSH_KEY_FILE}" \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 \
            -o BatchMode=yes \
            "${ssh_user}@${ip}" \
            "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 \
            "${ssh_user}@${ip}" \
            "timeout ${timeout} bash -c $(printf '%q' "${cmd}")"
    else
        log_error "[lightnode] No SSH credentials available"
        return 1
    fi
}

lightnode_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    local ssh_user; ssh_user=$(get_platform_ssh_user "lightnode")
    log_info "[lightnode] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local ssh_ok=false
        if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
            ssh -i "${PLATFORM_SSH_KEY_FILE}" \
                -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 -o BatchMode=yes \
                "${ssh_user}@${ip}" "echo ok" >/dev/null 2>&1 && ssh_ok=true
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            sshpass -p "${PLATFORM_SSH_PASSWORD}" ssh \
                -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=10 \
                "${ssh_user}@${ip}" "echo ok" >/dev/null 2>&1 && ssh_ok=true
        fi
        if $ssh_ok; then
            log_success "[lightnode] SSH ready on ${ip}"
            return 0
        fi
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[lightnode] SSH timeout on ${ip} after ${max}s"
    return 1
}
