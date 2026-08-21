#!/bin/bash
# Platform Interface - Dispatch layer that routes calls to the active platform provider
# Sources all enabled platform providers and provides a unified API.

PLATFORM_INTERFACE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Source platform config first
source "${PLATFORM_INTERFACE_DIR}/platform_config.sh"

# ============================================================================
# Source all platform provider files
# Each provider only activates when called by name; safe to source all.
# ============================================================================
_PLATFORMS_DIR="${PLATFORM_INTERFACE_DIR}/platforms"
for _pf in "${_PLATFORMS_DIR}"/*_api.sh; do
    [[ -f "$_pf" ]] && source "$_pf"
done
unset _pf _PLATFORMS_DIR

# ============================================================================
# Active platform tracking
# ============================================================================
ACTIVE_PLATFORM=""
ACTIVE_INSTANCE_ID=""
ACTIVE_INSTANCE_IP=""

PLATFORM_REMOTE_PATH="${PLATFORM_REMOTE_PATH:-/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin:/snap/bin:/var/lib/snapd/snap/bin:/opt/bin}"

platform_wrap_remote_command() {
    local cmd="$1"
    # shellcheck disable=SC2016 # ${PATH} is expanded by the remote shell.
    printf 'export LC_ALL=C.UTF-8 LANG=C.UTF-8 LANGUAGE=C.UTF-8 2>/dev/null || true; export PATH=%s${PATH:+:$PATH}; %s' \
        "$PLATFORM_REMOTE_PATH" "$cmd"
}

# ============================================================================
# Platform dispatch: call <platform>_platform_<action> dynamically
# ============================================================================
platform_dispatch() {
    local platform="$1" action="$2"
    shift 2
    local func="${platform}_platform_${action}"
    if ! declare -f "$func" >/dev/null 2>&1; then
        log_error "Platform '${platform}' does not implement '${action}' (function '${func}' not found)"
        return 1
    fi
    "$func" "$@"
}

# ============================================================================
# Initialize a platform provider
# Returns 0 if the platform was initialized successfully.
# ============================================================================
platform_init() {
    local platform="$1"
    log_info "Initializing platform: ${platform}"
    if ! platform_dispatch "$platform" "init"; then
        log_error "Platform '${platform}' initialization failed"
        return 1
    fi
    ACTIVE_PLATFORM="$platform"
    log_success "Platform '${platform}' initialized"
    return 0
}

# ============================================================================
# Create instance with auto-fallback across enabled platforms
# Tries each enabled platform in priority order until one succeeds.
# By default, enforces a hard max of 1 running instance per platform:
#   - Extra instances (beyond the first) are always deleted immediately.
#   - If the single kept instance can be reinstalled, reinstall it.
#   - If reinstall fails (or platform doesn't support it), delete it and
#     create a brand-new instance so we always start clean.
# Set PLATFORM_ALLOW_CONCURRENT_INSTANCES=true (or ACTION_TEST_PARALLEL_LOCAL=true)
# for local matrix runs where each process owns and cleans up its own instance ID.
#
# On exit, PLATFORM_FAILURE_REASON is set to:
#   "resource_exhausted" if every platform failed due to resource/capacity limits
#   "error"              for any other failure
# ============================================================================
PLATFORM_FAILURE_REASON=""
PLATFORM_LAST_ERROR=""

env_needs_worker_resource_check() {
    local env_type="$1"
    case "$env_type" in
        lxd|incus|proxmoxve|qemu|kubevirt) return 0 ;;
        *) return 1 ;;
    esac
}

worker_resource_requirements() {
    local env_type="$1" instance_types="${2:-${INSTANCE_TYPES:-both}}"
    local min_cpu=1 min_mem_mb=2048 min_disk_gb=20 require_kvm=false
    local container_cpu="${ACTION_TEST_CONTAINER_CPU:-2}"
    local container_memory="${ACTION_TEST_CONTAINER_MEMORY:-2048}"
    local container_disk="${ACTION_TEST_CONTAINER_DISK:-20}"
    local vm_cpu="${ACTION_TEST_VM_CPU:-2}"
    local vm_memory="${ACTION_TEST_VM_MEMORY:-4096}"
    local vm_disk="${ACTION_TEST_VM_DISK:-20}"

    [[ "$container_cpu" =~ ^[0-9]+$ ]] || container_cpu=2
    [[ "$container_memory" =~ ^[0-9]+$ ]] || container_memory=2048
    [[ "$container_disk" =~ ^[0-9]+$ ]] || container_disk=20
    [[ "$vm_cpu" =~ ^[0-9]+$ ]] || vm_cpu=2
    [[ "$vm_memory" =~ ^[0-9]+$ ]] || vm_memory=4096
    [[ "$vm_disk" =~ ^[0-9]+$ ]] || vm_disk=20

    if env_needs_worker_resource_check "$env_type"; then
        case "$instance_types" in
            container)
                min_cpu=$((1 + container_cpu))
                min_mem_mb=$((256 + container_memory + 512))
                min_disk_gb=$((container_disk + 4))
                ;;
            vm)
                min_cpu=$((1 + vm_cpu))
                min_mem_mb=$((512 + vm_memory + 512))
                min_disk_gb=$((vm_disk + 4))
                require_kvm=true
                ;;
            *)
                min_cpu=$((2 + container_cpu + vm_cpu))
                min_mem_mb=$((256 + 512 + container_memory + vm_memory + 512))
                min_disk_gb=$((container_disk + vm_disk + 4))
                require_kvm=true
                ;;
        esac
        (( min_cpu < 2 )) && min_cpu=2
        (( min_mem_mb < 4096 )) && min_mem_mb=4096
        (( min_disk_gb < 20 )) && min_disk_gb=20
        if [[ "$instance_types" == "both" && "$min_mem_mb" -lt 8192 ]]; then
            min_mem_mb=8192
        fi
    fi

    local env_prefix="${env_type^^}"
    local cpu_override_name="${env_prefix}_MIN_WORKER_CPU_CORES"
    local memory_override_name="${env_prefix}_MIN_WORKER_MEMORY_MB"
    local disk_override_name="${env_prefix}_MIN_WORKER_DISK_GB"
    local cpu_override="${!cpu_override_name:-0}"
    local memory_override="${!memory_override_name:-0}"
    local disk_override="${!disk_override_name:-0}"
    local generic_cpu_override="${ACTION_TEST_MIN_WORKER_CPU_CORES:-0}"
    local generic_memory_override="${ACTION_TEST_MIN_WORKER_MEMORY_MB:-0}"
    local generic_disk_override="${ACTION_TEST_MIN_WORKER_DISK_GB:-0}"
    [[ "$cpu_override" =~ ^[0-9]+$ ]] || cpu_override=0
    [[ "$memory_override" =~ ^[0-9]+$ ]] || memory_override=0
    [[ "$disk_override" =~ ^[0-9]+$ ]] || disk_override=0
    [[ "$generic_cpu_override" =~ ^[0-9]+$ ]] || generic_cpu_override=0
    [[ "$generic_memory_override" =~ ^[0-9]+$ ]] || generic_memory_override=0
    [[ "$generic_disk_override" =~ ^[0-9]+$ ]] || generic_disk_override=0
    (( cpu_override > min_cpu )) && min_cpu="$cpu_override"
    (( memory_override > min_mem_mb )) && min_mem_mb="$memory_override"
    (( disk_override > min_disk_gb )) && min_disk_gb="$disk_override"
    (( generic_cpu_override > min_cpu )) && min_cpu="$generic_cpu_override"
    (( generic_memory_override > min_mem_mb )) && min_mem_mb="$generic_memory_override"
    (( generic_disk_override > min_disk_gb )) && min_disk_gb="$generic_disk_override"

    printf '%s %s %s %s\n' "$min_cpu" "$min_mem_mb" "$min_disk_gb" "$require_kvm"
}

platform_validate_worker_resources() {
    local env_type="$1" ip="$2" platform="${3:-${ACTIVE_PLATFORM}}"
    if ! env_needs_worker_resource_check "$env_type"; then
        return 0
    fi

    local min_cpu nominal_mem_mb min_mem_mb min_disk_gb require_kvm
    read -r min_cpu nominal_mem_mb min_disk_gb require_kvm < <(worker_resource_requirements "$env_type")
    if [[ "$platform" == "lightnode" ]]; then
        local lightnode_cpu="${LIGHTNODE_TARGET_CPU:-0}"
        local lightnode_memory="${LIGHTNODE_TARGET_MEMORY_MB:-0}"
        [[ "$lightnode_cpu" =~ ^[0-9]+$ ]] || lightnode_cpu=0
        [[ "$lightnode_memory" =~ ^[0-9]+$ ]] || lightnode_memory=0
        (( lightnode_cpu > min_cpu )) && min_cpu="$lightnode_cpu"
        (( lightnode_memory > nominal_mem_mb )) && nominal_mem_mb="$lightnode_memory"
    fi
    min_mem_mb=$((nominal_mem_mb - nominal_mem_mb / 16))
    local check_cmd
    check_cmd=$(cat <<RESOURCE_CHECK
set -u
mem_mb=\$(awk '/MemTotal:/ {print int(\$2/1024)}' /proc/meminfo 2>/dev/null || echo 0)
mem_mb=\${mem_mb:-0}
cpu_count=\$(getconf _NPROCESSORS_ONLN 2>/dev/null || nproc 2>/dev/null || echo 0)
cpu_count=\${cpu_count:-0}
disk_gb=\$(df -Pk / 2>/dev/null | awk 'NR==2 {print int(\$4/1024/1024)}')
disk_gb=\${disk_gb:-0}
kvm_state="missing"
[ -e /dev/kvm ] && kvm_state="present"
echo "WORKER_RESOURCE_CHECK env=${env_type} platform=${platform} memory_mb=\${mem_mb} cpu=\${cpu_count} disk_gb=\${disk_gb} kvm=\${kvm_state}"
if [ "\${mem_mb}" -lt "${min_mem_mb}" ]; then
    echo "WORKER_RESOURCE_INSUFFICIENT: memory_mb=\${mem_mb} required>=${min_mem_mb}"
    exit 42
fi
if [ "\${cpu_count}" -lt "${min_cpu}" ]; then
    echo "WORKER_RESOURCE_INSUFFICIENT: cpu=\${cpu_count} required>=${min_cpu}"
    exit 42
fi
if [ "\${disk_gb}" -lt "${min_disk_gb}" ]; then
    echo "WORKER_RESOURCE_INSUFFICIENT: disk_gb=\${disk_gb} required>=${min_disk_gb}"
    exit 42
fi
if [ "${require_kvm}" = "true" ] && [ ! -e /dev/kvm ]; then
    echo "WORKER_RESOURCE_INSUFFICIENT: /dev/kvm missing"
    exit 42
fi
exit 0
RESOURCE_CHECK
)

    local kvm_requirement="not required"
    [[ "$require_kvm" == "true" ]] && kvm_requirement="required"
    log_info "Checking worker resources for ${env_type}/${INSTANCE_TYPES:-both} on ${platform} (memory>=${min_mem_mb}MB from ${nominal_mem_mb}MB nominal, cpu>=${min_cpu}, disk>=${min_disk_gb}GB, /dev/kvm ${kvm_requirement})..."
    local output
    if output=$(platform_ssh_exec "$ip" "$check_cmd" 90 2>&1); then
        [[ -n "$output" ]] && printf '%s\n' "$output" >&2
        log_success "Worker resources satisfy ${env_type} requirements"
        return 0
    fi
    [[ -n "$output" ]] && printf '%s\n' "$output" >&2
    log_warning "Worker resources do not satisfy ${env_type} requirements on ${platform}"
    return 75
}

try_create_with_fallback() {
    local env_type="$1" hours="${2:-8}"
    local enabled_platforms
    enabled_platforms=$(get_enabled_platforms)
    if [[ -z "$enabled_platforms" ]]; then
        log_error "No platforms are enabled! Set PLATFORM_<NAME>_ENABLED=true for at least one platform."
        PLATFORM_FAILURE_REASON="error"
        return 1
    fi
    log_info "Enabled platforms (priority order): ${enabled_platforms}"
    # Outer retry loop: on full resource exhaustion wait 90 s then retry once more.
    local all_resource_exhausted _attempt _max_attempts=2
    for (( _attempt=1; _attempt<=_max_attempts; _attempt++ )); do
    if [[ $_attempt -gt 1 ]]; then
        log_warning "All platforms resource-exhausted on attempt $((_attempt-1))/${_max_attempts}. Waiting 90s before retry..."
        sleep 90
    fi
    all_resource_exhausted=true
    for platform in ${enabled_platforms}; do
        log_info "=== Trying platform: ${platform} (attempt ${_attempt}/${_max_attempts}) ==="
        if ! platform_init "$platform"; then
            log_warning "Platform '${platform}' init failed, trying next..."
            all_resource_exhausted=false  # init failure is a config issue, not resource exhaustion
            continue
        fi
        local result="" exit_code

        local keep_id=""
        if [[ "${PLATFORM_ALLOW_CONCURRENT_INSTANCES:-${ACTION_TEST_PARALLEL_LOCAL:-false}}" == "true" ]]; then
            log_info "[${platform}] Concurrent instance mode enabled; creating an isolated worker instance"
        else
            # --- Enforce max-1 invariant ---
            # List all existing instances; delete every one beyond the first.
            local existing="[]"
            if ! existing=$(platform_dispatch "$platform" "list_instances" 2>/dev/null); then
                log_error "[${platform}] Unable to obtain a complete instance inventory; refusing to create another instance"
                all_resource_exhausted=false
                continue
            fi
            log_debug "[${platform}] list_instances raw: ${existing}"
            local all_ids=()
            mapfile -t all_ids < <(echo "$existing" | jq -r '.[].instance_id // empty' 2>/dev/null)
            local inst_count=${#all_ids[@]}
            if [[ $inst_count -gt 1 ]]; then
                log_warning "[${platform}] Found ${inst_count} instances — enforcing max-1, deleting $((inst_count - 1)) extra(s)..."
                local cleanup_failed=false
                for (( _i=1; _i<inst_count; _i++ )); do
                    log_info "[${platform}] Deleting extra instance ${all_ids[$_i]}..."
                    if ! platform_dispatch "$platform" "delete_instance" "${all_ids[$_i]}" 2>/dev/null; then
                        log_error "[${platform}] Failed to delete extra instance ${all_ids[$_i]}"
                        cleanup_failed=true
                    fi
                done
                if [[ "$cleanup_failed" == "true" ]]; then
                    all_resource_exhausted=false
                    continue
                fi
            fi
            [[ $inst_count -ge 1 ]] && keep_id="${all_ids[0]}"
        fi

        # --- Reuse or discard the kept instance ---
        if [[ -n "$keep_id" ]]; then
            if should_reinstall "$platform"; then
                log_info "[${platform}] Reinstalling existing instance ${keep_id}..."
                result=$(platform_dispatch "$platform" "reinstall_instance" "$keep_id" "debian")
                exit_code=$?
                if [[ $exit_code -eq 0 && -n "$result" ]]; then
                    local rip
                    rip=$(echo "$result" | jq -r '.ipv4 // empty' 2>/dev/null)
                    if [[ -n "$rip" ]]; then
                        log_success "Reinstalled existing instance on '${platform}': ID=${keep_id} IP=${rip}"
                        ACTIVE_PLATFORM="$platform"
                        ACTIVE_INSTANCE_ID="$keep_id"
                        ACTIVE_INSTANCE_IP="$rip"
                        if env_needs_worker_resource_check "$env_type"; then
                            if ! wait_for_ssh "$rip" 600; then
                                log_error "[${platform}] SSH never became available for resource validation"
                                all_resource_exhausted=false
                                platform_dispatch "$platform" "delete_instance" "$keep_id" 2>/dev/null || true
                                continue
                            fi
                            if ! platform_validate_worker_resources "$env_type" "$rip" "$platform"; then
                                log_warning "[${platform}] Reinstalled instance ${keep_id} does not meet ${env_type} worker requirements; releasing it and trying next platform"
                                platform_dispatch "$platform" "delete_instance" "$keep_id" 2>/dev/null || true
                                keep_id=""
                                PLATFORM_LAST_ERROR="resource_exhausted"
                                continue
                            fi
                        fi
                        PLATFORM_FAILURE_REASON=""
                        echo "$result"
                        return 0
                    else
                        log_error "[${platform}] Reinstall returned no IP. Raw result: ${result}"
                    fi
                else
                    log_error "[${platform}] Reinstall failed (exit=${exit_code}). Raw output: ${result:-<empty>}"
                fi
                log_warning "[${platform}] Reinstall failed — deleting ${keep_id} and creating fresh instance..."
            else
                log_info "[${platform}] Platform does not support reinstall — deleting instance ${keep_id}..."
            fi
            # Delete the kept instance before creating a fresh one
            platform_dispatch "$platform" "delete_instance" "$keep_id" 2>/dev/null || true
            keep_id=""
        fi

        # --- Create a brand-new instance (no existing instances remain) ---
        # Reset per-attempt error tracker before the create call
        PLATFORM_LAST_ERROR=""
        # Cap hours to platform-specific maximums to avoid API rejection
        local capped_hours="$hours"
        case "$platform" in
            alice) [[ $capped_hours -gt 24 ]] && { log_info "[${platform}] Capping hours from ${hours} to 24 (platform maximum)"; capped_hours=24; } ;;
        esac
        log_info "[${platform}] Creating new instance (env=${env_type} hours=${capped_hours})..."
        result=$(platform_dispatch "$platform" "create_instance" "$env_type" "$capped_hours")
        exit_code=$?
        if [[ $exit_code -eq 0 && -n "$result" ]]; then
            local cip cid
            cip=$(echo "$result" | jq -r '.ipv4 // empty' 2>/dev/null)
            cid=$(echo "$result" | jq -r '.instance_id // empty' 2>/dev/null)
            if [[ -n "$cip" ]]; then
                log_success "Instance created on '${platform}': ID=${cid} IP=${cip}"
                ACTIVE_PLATFORM="$platform"
                ACTIVE_INSTANCE_ID="$cid"
                ACTIVE_INSTANCE_IP="$cip"
                if env_needs_worker_resource_check "$env_type"; then
                    if ! wait_for_ssh "$cip" 600; then
                        log_error "[${platform}] SSH never became available for resource validation"
                        all_resource_exhausted=false
                        platform_dispatch "$platform" "delete_instance" "$cid" 2>/dev/null || true
                        continue
                    fi
                    if ! platform_validate_worker_resources "$env_type" "$cip" "$platform"; then
                        log_warning "[${platform}] Instance ${cid} does not meet ${env_type} worker requirements; releasing it and trying next platform"
                        platform_dispatch "$platform" "delete_instance" "$cid" 2>/dev/null || true
                        PLATFORM_LAST_ERROR="resource_exhausted"
                        continue
                    fi
                fi
                PLATFORM_FAILURE_REASON=""
                echo "$result"
                return 0
            else
                log_error "[${platform}] create_instance returned no IP. Raw result: ${result}"
            fi
        else
            if [[ "$exit_code" -eq 75 || "${PLATFORM_LAST_ERROR:-}" == "resource_exhausted" ]]; then
                log_warning "[${platform}] create_instance has no temporary capacity (exit=${exit_code})"
            else
                log_error "[${platform}] create_instance failed (exit=${exit_code}). Raw output: ${result:-<empty>}"
            fi
        fi
        # Track whether this failure was resource exhaustion or something else
        if [[ "$exit_code" -ne 75 && "${PLATFORM_LAST_ERROR:-}" != "resource_exhausted" ]]; then
            all_resource_exhausted=false
        fi
        log_warning "Platform '${platform}' exhausted, trying next..."
    done
    if [[ "$all_resource_exhausted" == "true" ]]; then
        log_warning "All enabled platforms are temporarily out of capacity (attempt ${_attempt}/${_max_attempts})"
    else
        log_error "All enabled platforms failed to create an instance (attempt ${_attempt}/${_max_attempts})"
    fi
    # Only retry on full resource exhaustion — non-transient errors don't benefit from retry
    [[ "$all_resource_exhausted" != "true" ]] && break
    done  # end outer retry loop
    if [[ "$all_resource_exhausted" == "true" ]]; then
        PLATFORM_FAILURE_REASON="resource_exhausted"
        log_warning "All enabled platforms are temporarily out of capacity; treating this as a transient skip"
        # Return 75 (EX_TEMPFAIL) so this exit code survives subshell boundaries;
        # the caller cannot rely on PLATFORM_FAILURE_REASON across $() invocations.
        return 75
    else
        PLATFORM_FAILURE_REASON="error"
        log_error "All enabled platforms failed to create an instance"
    fi
    return 1
}

# ============================================================================
# Delete/cleanup instance on the active platform
# Respects SKIP_INSTANCE_DELETE and monthly/prepaid billing settings.
# ============================================================================
platform_delete_instance() {
    local instance_id="$1"
    local platform="${ACTIVE_PLATFORM}"
    [[ -z "$platform" ]] && { log_error "No active platform set"; return 1; }
    # Check if deletion should be skipped
    if should_skip_delete "$platform"; then
        log_info "Skipping instance deletion for '${platform}' (billing: ${PLATFORM_BILLING_TYPE[$platform]:-unknown}, SKIP_INSTANCE_DELETE=${SKIP_INSTANCE_DELETE})"
        return 0
    fi
    log_info "Deleting instance ${instance_id} on platform '${platform}'..."
    platform_dispatch "$platform" "delete_instance" "$instance_id"
}

# ============================================================================
# SSH execution on the active platform
# ============================================================================
platform_ssh_exec() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    local platform="${ACTIVE_PLATFORM}"
    [[ -z "$platform" ]] && { log_error "No active platform set"; return 1; }
    platform_dispatch "$platform" "ssh_exec" "$ip" "$(platform_wrap_remote_command "$cmd")" "$timeout"
}

# ============================================================================
# Wait for SSH on the active platform
# ============================================================================
platform_wait_ssh() {
    local ip="$1" max="${2:-300}" interval="${3:-10}"
    local platform="${ACTIVE_PLATFORM}"
    [[ -z "$platform" ]] && { log_error "No active platform set"; return 1; }
    platform_dispatch "$platform" "wait_ssh" "$ip" "$max" "$interval"
}

# ============================================================================
# Cleanup all instances (comma-separated IDs)
# ============================================================================
platform_cleanup_all() {
    local ids="$1"
    IFS=',' read -ra arr <<< "$ids"
    for id in "${arr[@]}"; do
        [[ -n "$id" ]] && platform_delete_instance "$id" || true
    done
}

# ============================================================================
# Compatibility shims: these functions are called by node_manager.sh and
# other scripts that previously used alice_* functions directly.
# ============================================================================

# wait_for_ssh: wait for SSH connectivity using the active platform's method
wait_for_ssh() {
    local ip="$1" max="${2:-300}"
    platform_wait_ssh "$ip" "$max" 10
}

# Execute a command on a remote node via SSH (replaces alice_exec_and_wait)
platform_exec_and_wait() {
    local ip="$1" cmd="$2" timeout="${3:-300}"
    local attempts="${PLATFORM_EXEC_RETRIES:-3}"
    local delay="${PLATFORM_EXEC_RETRY_DELAY:-10}"
    [[ "$attempts" =~ ^[0-9]+$ && "$attempts" -gt 0 ]] || attempts=3
    [[ "$delay" =~ ^[0-9]+$ ]] || delay=10

    local attempt rc
    for ((attempt = 1; attempt <= attempts; attempt++)); do
        platform_ssh_exec "$ip" "$cmd" "$timeout"
        rc=$?
        if [[ $rc -eq 0 ]]; then
            return 0
        fi
        if [[ $attempt -lt $attempts ]]; then
            log_warning "Remote command failed on ${ip} (attempt ${attempt}/${attempts}, exit=${rc}); retrying in ${delay}s"
            sleep "$delay"
        fi
    done
    return "$rc"
}

# Wait for apt/dpkg locks to be released on remote node.
# Uses the same approach as the original aliceinit_api.sh:
# - Always wait min_wait seconds first (cloud-init needs time).
# - Then actively test with `apt-get check` (more reliable than fuser).
# - On timeout: return failure so callers can wait/retry without corrupting dpkg state.
wait_for_apt_lock() {
    local ip="$1" min_wait="${2:-120}" max_wait="${3:-300}" interval="${4:-10}"
    log_info "Waiting for apt/dpkg locks on ${ip} (min ${min_wait}s, max ${max_wait}s)..."
    local elapsed=0
    while [[ $elapsed -lt $min_wait ]]; do
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    while [[ $elapsed -lt $max_wait ]]; do
        if platform_ssh_exec "$ip" \
            'DEBIAN_FRONTEND=noninteractive apt-get check >/dev/null 2>&1' \
            30 >/dev/null 2>&1; then
            log_success "apt/dpkg locks free on ${ip} (after ${elapsed}s)"
            return 0
        fi
        log_debug "apt/dpkg not ready on ${ip} (${elapsed}/${max_wait}s), retrying..."
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    log_warning "apt/dpkg locks still held after ${max_wait}s on ${ip}; leaving package manager state untouched"
    return 1
}
