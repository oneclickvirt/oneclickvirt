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
# Enforces a hard max of 1 running instance per platform at all times:
#   - Extra instances (beyond the first) are always deleted immediately.
#   - If the single kept instance can be reinstalled, reinstall it.
#   - If reinstall fails (or platform doesn't support it), delete it and
#     create a brand-new instance so we always start clean.
#
# On exit, PLATFORM_FAILURE_REASON is set to:
#   "resource_exhausted" if every platform failed due to resource/capacity limits
#   "error"              for any other failure
# ============================================================================
PLATFORM_FAILURE_REASON=""
PLATFORM_LAST_ERROR=""

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

        # --- Enforce max-1 invariant ---
        # List all existing instances; delete every one beyond the first.
        local existing="[]"
        existing=$(platform_dispatch "$platform" "list_instances" 2>/dev/null) || existing="[]"
        log_debug "[${platform}] list_instances raw: ${existing}"
        local all_ids=() keep_id=""
        mapfile -t all_ids < <(echo "$existing" | jq -r '.[].instance_id // empty' 2>/dev/null)
        local inst_count=${#all_ids[@]}
        if [[ $inst_count -gt 1 ]]; then
            log_warning "[${platform}] Found ${inst_count} instances — enforcing max-1, deleting $((inst_count - 1)) extra(s)..."
            for (( _i=1; _i<inst_count; _i++ )); do
                log_info "[${platform}] Deleting extra instance ${all_ids[$_i]}..."
                platform_dispatch "$platform" "delete_instance" "${all_ids[$_i]}" 2>/dev/null || true
            done
        fi
        [[ $inst_count -ge 1 ]] && keep_id="${all_ids[0]}"

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
        log_info "[${platform}] Creating new instance (env=${env_type} hours=${hours})..."
        result=$(platform_dispatch "$platform" "create_instance" "$env_type" "$hours")
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
                PLATFORM_FAILURE_REASON=""
                echo "$result"
                return 0
            else
                log_error "[${platform}] create_instance returned no IP. Raw result: ${result}"
            fi
        else
            log_error "[${platform}] create_instance failed (exit=${exit_code}). Raw output: ${result:-<empty>}"
        fi
        # Track whether this failure was resource exhaustion or something else
        if [[ "${PLATFORM_LAST_ERROR:-}" != "resource_exhausted" ]]; then
            all_resource_exhausted=false
        fi
        log_warning "Platform '${platform}' exhausted, trying next..."
    done
    log_error "All enabled platforms failed to create an instance (attempt ${_attempt}/${_max_attempts})"
    # Only retry on full resource exhaustion — non-transient errors don't benefit from retry
    [[ "$all_resource_exhausted" != "true" ]] && break
    done  # end outer retry loop
    log_error "All enabled platforms failed to create an instance"
    if [[ "$all_resource_exhausted" == "true" ]]; then
        PLATFORM_FAILURE_REASON="resource_exhausted"
        log_warning "All platform failures were due to resource/capacity exhaustion (transient condition)"
        # Return 75 (EX_TEMPFAIL) so this exit code survives subshell boundaries;
        # the caller cannot rely on PLATFORM_FAILURE_REASON across $() invocations.
        return 75
    else
        PLATFORM_FAILURE_REASON="error"
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
    platform_dispatch "$platform" "ssh_exec" "$ip" "$cmd" "$timeout"
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
    platform_ssh_exec "$ip" "$cmd" "$timeout"
}

# Wait for apt/dpkg locks to be released on remote node.
# Uses the same approach as the original aliceinit_api.sh:
# - Always wait min_wait seconds first (cloud-init needs time).
# - Then actively test with `apt-get check` (more reliable than fuser).
# - On timeout: force-kill and remove lock files, then proceed.
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
    log_warning "apt/dpkg lock wait timeout (${max_wait}s), attempting forced cleanup..."
    platform_ssh_exec "$ip" '
        pkill -9 apt 2>/dev/null || true
        pkill -9 apt-get 2>/dev/null || true
        pkill -9 dpkg 2>/dev/null || true
        for f in /var/lib/dpkg/lock /var/lib/dpkg/lock-frontend \
                 /var/cache/apt/archives/lock /var/lib/apt/lists/lock; do
            [ -f "$f" ] && rm -f "$f" 2>/dev/null || true
        done
        dpkg --configure -a 2>/dev/null || true
    ' 60 >/dev/null 2>&1 || true
    log_warning "Forced apt cleanup complete, proceeding"
    return 0
}
