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
LIGHTNODE_TASK_MAX_WAIT="${LIGHTNODE_TASK_MAX_WAIT:-3600}"
LIGHTNODE_STOP_TASK_MAX_WAIT="${LIGHTNODE_STOP_TASK_MAX_WAIT:-900}"
LIGHTNODE_CREATE_TASK_MAX_WAIT="${LIGHTNODE_CREATE_TASK_MAX_WAIT:-1800}"
LIGHTNODE_REINSTALL_TASK_MAX_WAIT="${LIGHTNODE_REINSTALL_TASK_MAX_WAIT:-3600}"
LIGHTNODE_PACKAGE_CODE="${LIGHTNODE_PACKAGE_CODE:-}"
LIGHTNODE_PACKAGE_TIER="${LIGHTNODE_PACKAGE_TIER:-3}"
LIGHTNODE_TARGET_CPU="${LIGHTNODE_TARGET_CPU:-2}"
LIGHTNODE_TARGET_MEMORY_MB="${LIGHTNODE_TARGET_MEMORY_MB:-4096}"
LIGHTNODE_STRICT_RECOMMENDED_SPEC="${LIGHTNODE_STRICT_RECOMMENDED_SPEC:-true}"
LIGHTNODE_LIST_PARALLELISM="${LIGHTNODE_LIST_PARALLELISM:-6}"
LIGHTNODE_LIST_PAGE_SIZE="${LIGHTNODE_LIST_PAGE_SIZE:-50}"

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
    local page="${3:-1}" page_size="${4:-50}"
    lightnode_request "GET" "/instance/list?page=${page}&pageSize=${page_size}&regionCode=${region}&zoneCode=${zone}"
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

_lightnode_get_default_package() {
    local resp; resp=$(lightnode_get_packages)
    local body; body=$(lightnode_parse_body "$resp")
    local code; code=$(lightnode_parse_code "$resp")
    if [[ "$code" != "200" && "$code" != "202" ]]; then
        log_error "[lightnode] Failed to get packages (HTTP ${code})"
        return 1
    fi
    local target_cpu="${LIGHTNODE_TARGET_CPU:-2}"
    local target_memory="${LIGHTNODE_TARGET_MEMORY_MB:-4096}"
    local tier="${LIGHTNODE_PACKAGE_TIER:-3}"
    local strict="${LIGHTNODE_STRICT_RECOMMENDED_SPEC:-true}"
    [[ "$target_cpu" =~ ^[0-9]+$ ]] || target_cpu=2
    [[ "$target_memory" =~ ^[0-9]+$ ]] || target_memory=4096
    [[ "$tier" =~ ^[0-9]+$ && "$tier" -gt 0 ]] || tier=3
    [[ "$strict" == "true" || "$strict" == "false" ]] || strict=true
    if declare -f worker_resource_requirements >/dev/null 2>&1; then
        local required_cpu required_memory _required_disk _require_kvm
        read -r required_cpu required_memory _required_disk _require_kvm < <(worker_resource_requirements "${ENV_TYPE:-docker}")
        if (( required_cpu > target_cpu || required_memory > target_memory )); then
            (( required_cpu > target_cpu )) && target_cpu="$required_cpu"
            (( required_memory > target_memory )) && target_memory="$required_memory"
            log_info "[lightnode] Raised package floor for ${ENV_TYPE:-docker}/${INSTANCE_TYPES:-both} to ${target_cpu}C/${target_memory}MB"
        fi
    fi

    local package_code
    package_code=$(printf '%s' "$body" | jq -r 2>/dev/null \
        --arg region "${LIGHTNODE_REGION}" \
        --arg zone "${LIGHTNODE_ZONE}" \
        --arg explicit "${LIGHTNODE_PACKAGE_CODE}" \
        --arg strict "$strict" \
        --argjson target_cpu "$target_cpu" \
        --argjson target_memory "$target_memory" \
        --argjson tier "$tier" '
def package_code: .packageCode // .code // .id // empty;
def package_list: ((.packages // .data.packages // .data.list // .data // []) | if type == "array" then . else [] end);
def number_from(v):
  if v == null then null
  elif (v | type) == "number" then v
  else (try ((v | tostring) | capture("(?<n>[0-9]+(\\.[0-9]+)?)").n | tonumber) catch null)
  end;
def cpu_cores:
  number_from(.cpu // .cpuCore // .cpuCores // .core // .cores // .vcpu // .vCPU // .cpuCount // .specCpu // .cpuNum);
def memory_mb:
  (.memoryMB // .memoryMb // .ramMB // .ramMb // .memorySizeMB // .memorySizeMb // .memoryInMb // .ramInMb) as $mb
  | if number_from($mb) != null then number_from($mb)
    else (.memoryGB // .memoryGb // .ramGB // .ramGb // .memorySizeGB // .memorySizeGb) as $gb
      | if number_from($gb) != null then (number_from($gb) * 1024)
        else (.memory // .mem // .ram // .memorySize // .specMemory) as $m
          | if ($m | type) == "number" then (if $m <= 128 then ($m * 1024) else $m end)
            else ($m | tostring | ascii_downcase) as $s
              | if ($s | test("[0-9]+(\\.[0-9]+)?\\s*(g|gb|gib)")) then (($s | capture("(?<n>[0-9]+(\\.[0-9]+)?)").n | tonumber) * 1024)
                elif ($s | test("[0-9]+(\\.[0-9]+)?\\s*(m|mb|mib)")) then ($s | capture("(?<n>[0-9]+(\\.[0-9]+)?)").n | tonumber)
                else null
                end
          end
      end
  end;
def region_match:
  ($region == "" or ((has("regionCode") or has("region")) | not) or ((.regionCode // .region // "") == $region));
def zone_match:
  ($zone == "" or ((has("zoneCode") or has("zone")) | not) or ((.zoneCode // .zone // "") == $zone));
(package_list | map(select(region_match and zone_match))) as $pkgs
| if $explicit != "" then
    ($pkgs
      | map(select(package_code == $explicit))
      | map(select((cpu_cores >= $target_cpu) and (memory_mb >= $target_memory)))
      | .[0]
      | package_code)
  else
    (($pkgs | map(select((cpu_cores == $target_cpu) and (memory_mb == $target_memory)))[0] | package_code) //
     (if $strict != "true" then ($pkgs | map(select((cpu_cores >= $target_cpu) and (memory_mb >= $target_memory))) | sort_by(cpu_cores, memory_mb) | ((.[($tier - 1)] // .[0]) | package_code)) else null end) //
     empty)
  end
') || package_code=""

    if [[ -z "$package_code" ]]; then
        if [[ -n "${LIGHTNODE_PACKAGE_CODE}" ]]; then
            log_error "[lightnode] Configured LIGHTNODE_PACKAGE_CODE is unavailable or smaller than ${target_cpu}C/${target_memory}MB in region=${LIGHTNODE_REGION} zone=${LIGHTNODE_ZONE}"
        else
            log_error "[lightnode] Required ${target_cpu}C/${target_memory}MB package is unavailable in region=${LIGHTNODE_REGION} zone=${LIGHTNODE_ZONE}; refusing to fall back to a smaller instance"
        fi
        return 75
    fi
    log_info "[lightnode] Using package=${package_code} (required=${target_cpu}C/${target_memory}MB tier=${tier}, strict=${strict})"
    echo "$package_code"
}

_lightnode_get_cheapest_package() {
    _lightnode_get_default_package "$@"
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
    local task_uuid="$1" max="${2:-$LIGHTNODE_TASK_MAX_WAIT}" interval="${3:-15}" elapsed=0
    log_info "[lightnode] Waiting for async task ${task_uuid} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local resp; resp=$(lightnode_get_async_task "${task_uuid}")
        local body; body=$(lightnode_parse_body "${resp}")
        local result; result=$(echo "$body" | jq -r '.asyncTaskInfo.processResult // empty' 2>/dev/null)
        local task_status; task_status=$(echo "$body" | jq -r '.asyncTaskInfo.taskStatus // empty' 2>/dev/null)
        log_debug "[lightnode] Task ${task_uuid}: result=${result} status=${task_status}"
        if [[ "$result" == "SUCCESS" ]]; then
            log_success "[lightnode] Task ${task_uuid} completed"
            return 0
        elif [[ "$result" == "FAIL" || "$result" == "CANCEL" ]]; then
            # Try to extract detailed error message from the task response
            local err_msg
            err_msg=$(echo "$body" | jq -r '.asyncTaskInfo.errorMessage // .asyncTaskInfo.failMessage // .asyncTaskInfo.remark // .asyncTaskInfo.message // empty' 2>/dev/null)
            if [[ -n "$err_msg" ]]; then
                log_error "[lightnode] Task ${task_uuid} failed: ${result} (reason: ${err_msg})"
            else
                log_error "[lightnode] Task ${task_uuid} failed: ${result}"
                log_debug "[lightnode] Task full response: ${body}"
            fi
            return 1
        fi
        sleep "${interval}"; elapsed=$((elapsed + interval))
    done
    log_error "[lightnode] Task ${task_uuid} timeout after ${max}s; leaving provider task to reach a final state"
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
    local env_type="$1"
    log_info "[lightnode] Creating instance: env=${env_type}"
    local package_code package_rc=0
    package_code=$(_lightnode_get_default_package) || package_rc=$?
    if (( package_rc != 0 )) || [[ -z "$package_code" ]]; then
        PLATFORM_LAST_ERROR="resource_exhausted"
        log_error "[lightnode] No package satisfies the worker resource budget"
        (( package_rc == 0 )) && package_rc=75
        return "$package_rc"
    fi
    local os_name="debian"
    [[ "${env_type}" == "lxd" ]] && os_name="ubuntu"
    local image_uuid; image_uuid=$(_lightnode_get_image_uuid "${os_name}")
    [[ -z "$image_uuid" ]] && { log_error "[lightnode] No ${os_name} image found"; return 1; }
    local ssh_key_uuid=""
    if [[ -n "${LIGHTNODE_SSH_KEY_UUID:-}" ]]; then
        ssh_key_uuid="\"sshKeyUUID\":\"${LIGHTNODE_SSH_KEY_UUID}\","
    fi
    local name_prefix="${PLATFORM_INSTANCE_NAME_PREFIX:-ci-test-${env_type}}"
    name_prefix=$(printf '%s' "$name_prefix" | tr -c 'A-Za-z0-9-' '-' | sed 's/--*/-/g; s/^-//; s/-$//')
    [[ -z "$name_prefix" ]] && name_prefix="ci-test"
    local instance_name
    instance_name="${name_prefix}-$(date +%Y%m%d%H%M%S)-$$"
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
    if ! _lightnode_wait_async_task "${task_uuid}" "$LIGHTNODE_CREATE_TASK_MAX_WAIT"; then
        # Async provisioning task failed — attempt to release the partially-created instance
        # so it does not pollute list_instances on the next run
        log_warning "[lightnode] Async provisioning failed for ${ecs_uuid}; attempting to release stale instance..."
        local rel_resp; rel_resp=$(lightnode_request "POST" "/instance/release" "{\"ecsResourceUUID\":\"${ecs_uuid}\"}" 2>/dev/null) || true
        local rel_code; rel_code=$(lightnode_parse_code "${rel_resp:-}" 2>/dev/null) || true
        log_info "[lightnode] Stale instance release returned HTTP ${rel_code:-unknown}"
        export PLATFORM_LAST_ERROR="resource_exhausted"
        return 1
    fi
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
    [[ -n "$task_uuid" ]] && _lightnode_wait_async_task "${task_uuid}" "$LIGHTNODE_STOP_TASK_MAX_WAIT"
    return 0
}

lightnode_platform_reinstall_instance() {
    local id="$1" os_name="${2:-debian}"
    log_info "[lightnode] Reinstalling instance ${id} with ${os_name}..."

    local chk_resp; chk_resp=$(lightnode_get_instance_detail "${id}")
    local chk_body; chk_body=$(lightnode_parse_body "${chk_resp}")
    local chk_code; chk_code=$(lightnode_parse_code "${chk_resp}")
    if [[ "$chk_code" != "200" && "$chk_code" != "202" ]]; then
        log_error "[lightnode] Cannot verify existing instance ${id} before reinstall (HTTP ${chk_code})"
        return 1
    fi

    if [[ "${LIGHTNODE_STRICT_RECOMMENDED_SPEC:-true}" == "true" ]]; then
        local required_package current_package
        required_package=$(_lightnode_get_default_package) || return 1
        current_package=$(echo "$chk_body" | jq -r '.instance.packageCode // .instance.packageConfig.packageCode // empty' 2>/dev/null)
        if [[ -z "$current_package" || "$current_package" != "$required_package" ]]; then
            log_warning "[lightnode] Existing instance ${id} package '${current_package:-unknown}' does not match required package '${required_package}'; refusing to reuse it"
            return 1
        fi
    fi

    # LightNode requires the instance to be in STOPPED state before reinstall.
    local cur_status; cur_status=$(echo "$chk_body" | jq -r '.instance.ecsStatus // empty' 2>/dev/null)
    if [[ "$cur_status" != "STOPPED" && "$cur_status" != "stopped" ]]; then
        log_info "[lightnode] Instance ${id} status='${cur_status}', force-stopping before reinstall..."
        local stop_resp; stop_resp=$(lightnode_request "POST" "/instance/stop" "{\"ecsResourceUUID\":\"${id}\",\"forceStop\":true}")
        local stop_body; stop_body=$(lightnode_parse_body "${stop_resp}")
        local stop_code; stop_code=$(lightnode_parse_code "${stop_resp}")
        if [[ "$stop_code" == "200" || "$stop_code" == "202" ]]; then
            local stop_task; stop_task=$(echo "$stop_body" | jq -r '.asyncTaskUUID // empty' 2>/dev/null)
            [[ -n "$stop_task" ]] && _lightnode_wait_async_task "${stop_task}" "$LIGHTNODE_STOP_TASK_MAX_WAIT" || true
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
    [[ -n "$task_uuid" ]] && _lightnode_wait_async_task "${task_uuid}" "$LIGHTNODE_REINSTALL_TASK_MAX_WAIT"
    # Get updated details
    local detail_resp; detail_resp=$(lightnode_get_instance_detail "${id}")
    local detail_body; detail_body=$(lightnode_parse_body "${detail_resp}")
    local ip; ip=$(echo "$detail_body" | jq -r '.instance.publicIpAddress // empty' 2>/dev/null)
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${LIGHTNODE_PASSWORD}\",\"ssh_user\":\"root\",\"platform\":\"lightnode\"}"
}

lightnode_platform_list_instances() {
    local regions_resp regions_body regions_code
    regions_resp=$(lightnode_get_regions)
    regions_body=$(lightnode_parse_body "$regions_resp")
    regions_code=$(lightnode_parse_code "$regions_resp")
    if [[ "$regions_code" != "200" && "$regions_code" != "202" ]]; then
        log_error "[lightnode] Failed to enumerate regions while listing account instances (HTTP ${regions_code})"
        return 1
    fi

    local tmp_dir pair_file
    tmp_dir=$(mktemp -d /tmp/lightnode_instances_XXXXXX)
    pair_file="${tmp_dir}/regions.tsv"
    printf '%s' "$regions_body" | jq -r '.regions[]? | [.regionCode, (.zones[]?.zoneCode // "")] | @tsv' 2>/dev/null > "$pair_file"

    local parallelism="${LIGHTNODE_LIST_PARALLELISM:-6}"
    [[ "$parallelism" =~ ^[0-9]+$ && "$parallelism" -gt 0 ]] || parallelism=6
    local page_size="${LIGHTNODE_LIST_PAGE_SIZE:-50}"
    [[ "$page_size" =~ ^[0-9]+$ && "$page_size" -gt 0 && "$page_size" -le 50 ]] || page_size=50
    local index=0 batch_count=0 failed=0 region zone pid
    local pids=()
    while IFS=$'\t' read -r region zone; do
        [[ -n "$region" && -n "$zone" ]] || continue
        index=$((index + 1))
        (
            zone_dir="${tmp_dir}/zone-${index}"
            mkdir -p "$zone_dir"
            local_resp=$(lightnode_list_instances_raw "$region" "$zone" 1 "$page_size")
            local_code=$(lightnode_parse_code "$local_resp")
            if [[ "$local_code" != "200" && "$local_code" != "202" ]]; then
                exit 1
            fi
            local_body=$(lightnode_parse_body "$local_resp")
            printf '%s' "$local_body" > "${zone_dir}/1.json"
            row_count=$(printf '%s' "$local_body" | jq -r '(.rowCount // (.instances | length) // 0) | tonumber? // 0' 2>/dev/null)
            [[ "$row_count" =~ ^[0-9]+$ ]] || exit 1
            page_count=$(((row_count + page_size - 1) / page_size))
            [[ "$page_count" -gt 0 ]] || page_count=1
            for ((page = 2; page <= page_count; page++)); do
                local_resp=$(lightnode_list_instances_raw "$region" "$zone" "$page" "$page_size")
                local_code=$(lightnode_parse_code "$local_resp")
                if [[ "$local_code" != "200" && "$local_code" != "202" ]]; then
                    exit 1
                fi
                lightnode_parse_body "$local_resp" > "${zone_dir}/${page}.json"
            done
            if ! jq -cs '{instances: [.[] | .instances[]?]}' "${zone_dir}"/*.json > "${tmp_dir}/page-${index}.json" 2>/dev/null; then
                exit 1
            fi
            rm -rf "$zone_dir"
        ) &
        pids+=("$!")
        batch_count=$((batch_count + 1))
        if [[ "$batch_count" -ge "$parallelism" ]]; then
            for pid in "${pids[@]}"; do
                wait "$pid" || failed=1
            done
            pids=()
            batch_count=0
        fi
    done < "$pair_file"
    for pid in "${pids[@]}"; do
        wait "$pid" || failed=1
    done

    if [[ "$failed" -ne 0 ]]; then
        rm -rf "$tmp_dir"
        log_error "[lightnode] Failed to list instances from one or more regions; refusing to use a partial account view"
        return 1
    fi

    local page_files=("${tmp_dir}"/page-*.json)
    if [[ ! -e "${page_files[0]}" ]]; then
        rm -rf "$tmp_dir"
        printf '[]\n'
        return 0
    fi

    jq -cs 2>/dev/null '[.[] | .instances[]? | {
        instance_id: .ecsResourceUUID,
        ipv4: .publicIpAddress,
        status: .ecsStatus,
        name: .instanceName,
        region: .regionCode,
        zone: .zoneCode,
        package_code: .packageCode,
        cpu: (.cpu // .cpuCore // .cpuCores),
        memory: (.memory // .memoryMB // .memoryMb)
    }]' "${page_files[@]}"
    local result=$?
    rm -rf "$tmp_dir"
    return "$result"
}

lightnode_platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    local ssh_user; ssh_user=$(get_platform_ssh_user "lightnode")
    local local_timeout=$((timeout + 120))
    if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
        local ssh_cmd=(ssh -i "${PLATFORM_SSH_KEY_FILE}" \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 \
            -o BatchMode=yes \
            "${ssh_user}@${ip}" \
            "timeout ${timeout} bash -c $(printf '%q' "${cmd}")")
        if command -v timeout >/dev/null 2>&1; then
            timeout "${local_timeout}" "${ssh_cmd[@]}"
        else
            "${ssh_cmd[@]}"
        fi
    elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
        local ssh_cmd=(sshpass -e ssh \
            -o StrictHostKeyChecking=no \
            -o UserKnownHostsFile=/dev/null \
            -o ConnectTimeout=30 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 \
            -o PreferredAuthentications=password \
            -o PubkeyAuthentication=no \
            -o NumberOfPasswordPrompts=3 \
            "${ssh_user}@${ip}" \
            "timeout ${timeout} bash -c $(printf '%q' "${cmd}")")
        if command -v timeout >/dev/null 2>&1; then
            SSHPASS="${PLATFORM_SSH_PASSWORD}" timeout "${local_timeout}" "${ssh_cmd[@]}"
        else
            SSHPASS="${PLATFORM_SSH_PASSWORD}" "${ssh_cmd[@]}"
        fi
    else
        log_error "[lightnode] No SSH credentials available"
        return 1
    fi
}

lightnode_platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}" elapsed=0
    local ssh_user; ssh_user=$(get_platform_ssh_user "lightnode")
    local start_ts; start_ts=$(date +%s)
    log_info "[lightnode] Waiting for SSH on ${ip} (max ${max}s)..."
    while [[ $elapsed -lt $max ]]; do
        local ssh_ok=false
        if [[ -n "${PLATFORM_SSH_KEY_FILE:-}" && -f "${PLATFORM_SSH_KEY_FILE}" ]]; then
            local ssh_cmd=(ssh -i "${PLATFORM_SSH_KEY_FILE}" \
                -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=8 -o ConnectionAttempts=1 \
                -o ServerAliveInterval=8 -o ServerAliveCountMax=1 -o BatchMode=yes \
                "${ssh_user}@${ip}" "echo ok")
            if command -v timeout >/dev/null 2>&1; then
                timeout 20 "${ssh_cmd[@]}" >/dev/null 2>&1 && ssh_ok=true
            else
                "${ssh_cmd[@]}" >/dev/null 2>&1 && ssh_ok=true
            fi
        elif [[ -n "${PLATFORM_SSH_PASSWORD:-}" ]]; then
            local ssh_cmd=(sshpass -e ssh \
                -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
                -o ConnectTimeout=8 -o ConnectionAttempts=1 \
                -o ServerAliveInterval=8 -o ServerAliveCountMax=1 \
                -o PreferredAuthentications=password \
                -o PubkeyAuthentication=no \
                -o NumberOfPasswordPrompts=1 \
                "${ssh_user}@${ip}" "echo ok")
            if command -v timeout >/dev/null 2>&1; then
                SSHPASS="${PLATFORM_SSH_PASSWORD}" timeout 20 "${ssh_cmd[@]}" >/dev/null 2>&1 && ssh_ok=true
            else
                SSHPASS="${PLATFORM_SSH_PASSWORD}" "${ssh_cmd[@]}" >/dev/null 2>&1 && ssh_ok=true
            fi
        fi
        if $ssh_ok; then
            log_success "[lightnode] SSH ready on ${ip}"
            return 0
        fi
        sleep "${interval}"
        elapsed=$(($(date +%s) - start_ts))
    done
    log_error "[lightnode] SSH timeout on ${ip} after ${max}s"
    return 1
}
