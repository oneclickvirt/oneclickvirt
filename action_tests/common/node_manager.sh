#!/bin/bash
# Node Manager - Platform-agnostic node creation, environment installation, master/worker deployment
# Two-node architecture: master node runs OneClickVirt, worker node runs virtualization environment

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/platform_interface.sh"

declare -A ENV_INSTALL_SCRIPTS=(
    [docker]="https://raw.githubusercontent.com/oneclickvirt/docker/main/scripts/dockerinstall.sh"
    [lxd]="https://raw.githubusercontent.com/oneclickvirt/lxd/main/scripts/lxdinstall.sh"
    [incus]="https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/incus_install.sh"
    [podman]="https://raw.githubusercontent.com/oneclickvirt/podman/main/podmaninstall.sh"
    [containerd]="https://raw.githubusercontent.com/oneclickvirt/containerd/main/containerdinstall.sh"
    [proxmoxve]="https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/install_pve.sh"
    [qemu]="https://raw.githubusercontent.com/oneclickvirt/qemu/main/qemuinstall.sh"
    [kubevirt]="https://raw.githubusercontent.com/oneclickvirt/kubevirt/main/kubevirtinstall.sh"
)
PVE_BUILD_BACKEND="https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/build_backend.sh"
PVE_BUILD_NAT="https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/build_nat_network.sh"
PVE_LAST_JOB_STATE=""

# Network reloads can briefly make SSH appear healthy before dropping it again.
# Require a small consecutive-success window before a completed mutating job is
# allowed to advance to a reboot or the next configuration stage.
pve_wait_for_ssh_stable() {
    local ip="$1"
    local max_wait="${2:-${PVE_REMOTE_SSH_STABLE_WAIT:-180}}"
    local probes="${PVE_REMOTE_STABLE_PROBES:-3}"
    local interval="${PVE_REMOTE_STABLE_INTERVAL:-5}"
    local probe_timeout="${PVE_REMOTE_STABLE_PROBE_TIMEOUT:-30}"
    local elapsed=0 successes=0
    [[ "$max_wait" =~ ^[0-9]+$ && "$max_wait" -gt 0 ]] || max_wait=180
    [[ "$probes" =~ ^[0-9]+$ && "$probes" -gt 0 ]] || probes=3
    [[ "$interval" =~ ^[0-9]+$ && "$interval" -gt 0 ]] || interval=5
    [[ "$probe_timeout" =~ ^[0-9]+$ && "$probe_timeout" -gt 0 ]] || probe_timeout=30

    while [[ $elapsed -le $max_wait ]]; do
        if wait_for_ssh "$ip" "$probe_timeout" >/dev/null 2>&1 && \
           platform_exec_once "$ip" "true" "$probe_timeout" >/dev/null 2>&1; then
            successes=$((successes + 1))
            [[ $successes -ge $probes ]] && return 0
        else
            successes=0
        fi
        [[ $elapsed -eq $max_wait ]] && break
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    return 1
}

pve_mark_job_completed() {
    local ip="$1" label="$2" message="${3:-Detached PVE job '${label}' completed}"
    if ! pve_wait_for_ssh_stable "$ip"; then
        PVE_LAST_JOB_STATE="completed_unstable"
        log_error "Detached PVE job '${label}' completed, but SSH did not remain stable before the next stage"
        return 75
    fi
    PVE_LAST_JOB_STATE="completed"
    log_success "$message"
    return 0
}

mysql_root_exec() {
    local db_password="${DB_PASSWORD:-${MYSQL_ROOT_PASSWORD:-}}"
    local args=(-u root -h 127.0.0.1)
    [[ -n "$db_password" ]] && args+=("-p${db_password}")
    mysql "${args[@]}" "$@"
}

ensure_worker_dns() {
    local ip="$1" label="${2:-worker}"
    [[ -z "$ip" ]] && { log_warning "DNS check skipped: worker IP is empty"; return 1; }

    log_info "Verifying DNS on ${label}..."
    # shellcheck disable=SC2016 # Variables are expanded by the remote shell.
    local dns_script='
targets="github.com raw.githubusercontent.com images.lxd.canonical.com images.linuxcontainers.org"
check_dns() {
    for host in $targets; do
        getent ahostsv4 "$host" >/dev/null 2>&1 || return 1
    done
}
if check_dns; then
    echo "DNS_OK"
    exit 0
fi
if command -v resolvectl >/dev/null 2>&1; then
    resolvectl flush-caches >/dev/null 2>&1 || true
fi
if command -v systemctl >/dev/null 2>&1; then
    systemctl restart systemd-resolved >/dev/null 2>&1 || true
    sleep 2
fi
if check_dns; then
    echo "DNS_OK_AFTER_RESOLVED_RESTART"
    exit 0
fi
if [ -L /etc/resolv.conf ]; then
    rm -f /etc/resolv.conf
fi
cat > /etc/resolv.conf <<'"'"'RESOLVCONF'"'"'
nameserver 1.1.1.1
nameserver 8.8.8.8
nameserver 9.9.9.9
options timeout:2 attempts:3 rotate
RESOLVCONF
if check_dns; then
    echo "DNS_REPAIRED"
    exit 0
fi
echo "DNS_FAILED"
cat /etc/resolv.conf || true
exit 1
'
    if platform_exec_and_wait "${ip}" "${dns_script}" 120; then
        log_success "DNS verified on ${label}"
        return 0
    fi
    log_warning "DNS verification/repair failed on ${label}"
    return 1
}

ensure_worker_swap() {
    local ip="$1" label="${2:-worker}" swap_mb="${WORKER_SWAP_MB:-2048}"
    [[ -z "$ip" ]] && { log_warning "Swap setup skipped: worker IP is empty"; return 1; }
    [[ "$swap_mb" =~ ^[0-9]+$ ]] || swap_mb=2048
    [[ "$swap_mb" -le 0 ]] && return 0

    log_info "Ensuring ${swap_mb}MB swap on ${label}..."
    local swap_script
    swap_script=$(cat <<SWAP_SCRIPT
set -u
target_mb=${swap_mb}
current_mb=\$(awk 'NR>1 {sum += int(\$3 / 1024)} END {print sum + 0}' /proc/swaps 2>/dev/null)
if [ "\${current_mb:-0}" -ge "\$target_mb" ]; then
    echo "SWAP_OK existing=\${current_mb}MB"
    exit 0
fi
swap_file="/swapfile-oneclickvirt"
if swapon --show=NAME 2>/dev/null | grep -qx "\$swap_file"; then
    swapoff "\$swap_file" || true
fi
rm -f "\$swap_file" || exit 1
if command -v fallocate >/dev/null 2>&1; then
    fallocate -l "\${target_mb}M" "\$swap_file" || dd if=/dev/zero of="\$swap_file" bs=1M count="\$target_mb" status=none || exit 1
else
    dd if=/dev/zero of="\$swap_file" bs=1M count="\$target_mb" status=none || exit 1
fi
chmod 600 "\$swap_file" || exit 1
mkswap "\$swap_file" >/dev/null || exit 1
swapon "\$swap_file" || exit 1
grep -q ' /swapfile-oneclickvirt ' /etc/fstab 2>/dev/null || echo '/swapfile-oneclickvirt none swap sw 0 0' >> /etc/fstab
new_mb=\$(awk 'NR>1 {sum += int(\$3 / 1024)} END {print sum + 0}' /proc/swaps 2>/dev/null)
echo "SWAP_OK total=\${new_mb}MB"
SWAP_SCRIPT
)
    local swap_output
    if swap_output=$(platform_exec_and_wait "${ip}" "${swap_script}" 300 2>&1); then
        [[ -n "$swap_output" ]] && log_debug "Swap setup output on ${label}: ${swap_output}"
        log_success "Swap ready on ${label}"
        return 0
    fi
    [[ -n "$swap_output" ]] && log_warning "Swap setup output on ${label}: ${swap_output}"
    log_warning "Swap setup failed on ${label}"
    return 1
}

stabilize_worker_network_for_env() {
    local ip="$1" env="$2" label="${3:-worker}"
    ensure_worker_dns "$ip" "$label" || return 1

    case "$env" in
        lxd)
            log_info "Refreshing LXD daemon DNS view on ${label}..."
            platform_exec_and_wait "$ip" '
if command -v systemctl >/dev/null 2>&1; then
    systemctl restart snap.lxd.daemon >/dev/null 2>&1 || systemctl restart lxd >/dev/null 2>&1 || true
fi
if command -v snap >/dev/null 2>&1; then
    snap restart lxd >/dev/null 2>&1 || true
fi
sleep 3
command -v lxc >/dev/null 2>&1 && lxc info >/dev/null 2>&1
' 180 >/dev/null 2>&1 || log_warning "LXD daemon DNS refresh did not verify cleanly on ${label}"
            ;;
        incus)
            log_info "Refreshing Incus daemon DNS view on ${label}..."
            platform_exec_and_wait "$ip" '
if command -v systemctl >/dev/null 2>&1; then
    systemctl restart incus >/dev/null 2>&1 || true
fi
sleep 3
command -v incus >/dev/null 2>&1 && incus info >/dev/null 2>&1
' 180 >/dev/null 2>&1 || log_warning "Incus daemon DNS refresh did not verify cleanly on ${label}"
            ;;
    esac
}

run_kubevirt_installer_with_retry() {
    local ip="$1" install_cmd="$2"
    local attempt max_attempts=2
    for attempt in $(seq 1 "$max_attempts"); do
        log_info "KubeVirt install attempt ${attempt}/${max_attempts}..."
        if platform_exec_and_wait "$ip" "$install_cmd" 7200; then
            return 0
        fi
        log_warning "KubeVirt install attempt ${attempt} did not complete over SSH; waiting for worker recovery before retry..."
        wait_for_ssh "$ip" 600 || return 1
        ensure_worker_dns "$ip" "worker after kubevirt install disconnect" || true
        if KUBEVIRT_RUNTIME_MAX_WAIT="${KUBEVIRT_INSTALL_RETRY_READY_WAIT:-600}" verify_worker_runtime "kubevirt-retry-${attempt}" "$ip" "kubevirt"; then
            log_success "KubeVirt runtime became ready after install SSH disconnect"
            return 0
        fi
        sleep 20
    done
    return 1
}

create_test_node() {
    local env_type="$1" hours="${2:-8}"
    log_info "Creating test node: env=${env_type} hours=${hours}"
    # Use platform abstraction with auto-fallback
    local result _rc
    result=$(try_create_with_fallback "$env_type" "$hours")
    _rc=$?
    if [[ $_rc -ne 0 || -z "$result" ]]; then
        if [[ $_rc -eq 75 ]]; then
            log_warning "No platform has temporary capacity for a test node"
        else
            log_error "All platforms failed to create a test node"
        fi
        # Propagate exit code 75 (EX_TEMPFAIL) so callers can detect transient
        # resource exhaustion even though this function runs inside $().
        return $_rc
    fi
    local id ip password platform_name
    id=$(echo "${result}" | jq -r '.instance_id // empty' 2>/dev/null)
    ip=$(echo "${result}" | jq -r '.ipv4 // empty' 2>/dev/null)
    password=$(echo "${result}" | jq -r '.password // empty' 2>/dev/null)
    platform_name=$(echo "${result}" | jq -r '.platform // empty' 2>/dev/null)
    [[ -z "${ip}" ]] && { log_error "Cannot get IP from create response: ${result}"; return 1; }
    # try_create_with_fallback runs inside $() so ACTIVE_PLATFORM and PLATFORM_SSH_KEY_FILE
    # set within it are lost when that subshell exits. Re-initialize the platform here so
    # that wait_for_ssh (and any other SSH operations in this function) work correctly.
    if [[ -n "$platform_name" ]]; then
        platform_init "$platform_name" || { log_error "Failed to re-init platform '${platform_name}'"; return 1; }
    fi
    # Update global SSH password if provided
    [[ -n "$password" ]] && PLATFORM_SSH_PASSWORD="$password"
    # Wait for SSH to be available before handing off the node
    wait_for_ssh "${ip}" 600 || { log_error "SSH never became available on ${ip}"; return 1; }
    ensure_worker_swap "${ip}" "new ${platform_name} worker" || log_warning "Continuing even though swap setup did not complete on new worker"
    log_success "Node created on '${platform_name}': ID=${id} IP=[MASKED]"
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"platform\":\"${platform_name}\"}"
}

find_local_env_install_script() {
    local env="$1"
    local candidate
    local candidates=()

    case "$env" in
        docker)
            [[ -n "${DOCKER_INSTALL_SCRIPT_LOCAL_PATH:-}" ]] && candidates+=("${DOCKER_INSTALL_SCRIPT_LOCAL_PATH}")
            candidates+=(
                "/Volumes/Additional/个人数据/GitHub/docker/scripts/dockerinstall.sh"
                "${SCRIPT_DIR}/../../../docker/scripts/dockerinstall.sh"
                "${SCRIPT_DIR}/../../../../../docker/scripts/dockerinstall.sh"
            )
            ;;
        lxd)
            [[ -n "${LXD_INSTALL_SCRIPT_LOCAL_PATH:-}" ]] && candidates+=("${LXD_INSTALL_SCRIPT_LOCAL_PATH}")
            candidates+=(
                "/Volumes/Additional/个人数据/GitHub/lxd/scripts/lxdinstall.sh"
                "${SCRIPT_DIR}/../../../lxd/scripts/lxdinstall.sh"
                "${SCRIPT_DIR}/../../../../../lxd/scripts/lxdinstall.sh"
            )
            ;;
        proxmoxve)
            [[ -n "${PVE_INSTALL_SCRIPT_LOCAL_PATH:-}" ]] && candidates+=("${PVE_INSTALL_SCRIPT_LOCAL_PATH}")
            candidates+=(
                "/Volumes/Additional/个人数据/GitHub/pve/scripts/install_pve.sh"
                "${SCRIPT_DIR}/../../../pve/scripts/install_pve.sh"
                "${SCRIPT_DIR}/../../../../../pve/scripts/install_pve.sh"
            )
            ;;
        incus)
            [[ -n "${INCUS_INSTALL_SCRIPT_LOCAL_PATH:-}" ]] && candidates+=("${INCUS_INSTALL_SCRIPT_LOCAL_PATH}")
            candidates+=(
                "/Volumes/Additional/个人数据/GitHub/incus/scripts/incus_install.sh"
                "${SCRIPT_DIR}/../../../incus/scripts/incus_install.sh"
                "${SCRIPT_DIR}/../../../../../incus/scripts/incus_install.sh"
            )
            ;;
		podman)
			[[ -n "${PODMAN_INSTALL_SCRIPT_LOCAL_PATH:-}" ]] && candidates+=("${PODMAN_INSTALL_SCRIPT_LOCAL_PATH}")
			candidates+=(
				"/Volumes/Additional/个人数据/GitHub/podman/podmaninstall.sh"
				"${SCRIPT_DIR}/../../../podman/podmaninstall.sh"
				"${SCRIPT_DIR}/../../../../../podman/podmaninstall.sh"
			)
			;;
		containerd)
			[[ -n "${CONTAINERD_INSTALL_SCRIPT_LOCAL_PATH:-}" ]] && candidates+=("${CONTAINERD_INSTALL_SCRIPT_LOCAL_PATH}")
			candidates+=(
				"/Volumes/Additional/个人数据/GitHub/containerd/containerdinstall.sh"
				"${SCRIPT_DIR}/../../../containerd/containerdinstall.sh"
				"${SCRIPT_DIR}/../../../../../containerd/containerdinstall.sh"
			)
			;;
		kubevirt)
			[[ -n "${KUBEVIRT_INSTALL_SCRIPT_LOCAL_PATH:-}" ]] && candidates+=("${KUBEVIRT_INSTALL_SCRIPT_LOCAL_PATH}")
			candidates+=(
				"/Volumes/Additional/个人数据/GitHub/kubevirt/kubevirtinstall.sh"
				"${SCRIPT_DIR}/../../../kubevirt/kubevirtinstall.sh"
				"${SCRIPT_DIR}/../../../../../kubevirt/kubevirtinstall.sh"
			)
			;;
		qemu)
			[[ -n "${QEMU_INSTALL_SCRIPT_LOCAL_PATH:-}" ]] && candidates+=("${QEMU_INSTALL_SCRIPT_LOCAL_PATH}")
			candidates+=(
				"/Volumes/Additional/个人数据/GitHub/qemu/qemuinstall.sh"
				"${SCRIPT_DIR}/../../../qemu/qemuinstall.sh"
				"${SCRIPT_DIR}/../../../../../qemu/qemuinstall.sh"
			)
			;;
		*)
			return 1
			;;
	esac

    for candidate in "${candidates[@]}"; do
        [[ -n "$candidate" && -f "$candidate" ]] && {
            (cd "$(dirname "$candidate")" && printf '%s/%s\n' "$(pwd -P)" "$(basename "$candidate")")
            return 0
        }
    done
    return 1
}

build_env_install_command() {
    local env="$1" url="$2" noninteractive_prefix="$3" env_prefix="$4"
    local local_script=""
    if local_script=$(find_local_env_install_script "$env"); then
        local payload
        payload=$(gzip -c "$local_script" | base64 | tr -d '\n') || return 1
        log_info "Using local ${env} installer: ${local_script}"
        cat <<ENV_INSTALL_CMD
${noninteractive_prefix} cat > /tmp/envinstall.sh.gz.b64 <<'ENV_INSTALL_B64'
${payload}
ENV_INSTALL_B64
base64 -d /tmp/envinstall.sh.gz.b64 | gzip -dc > /tmp/envinstall.sh && rm -f /tmp/envinstall.sh.gz.b64 && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh
ENV_INSTALL_CMD
        return 0
    fi
    log_info "Using remote ${env} installer: ${url}"
    printf '%s\n' "${noninteractive_prefix} curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh"
}

build_pve_aux_command() {
    local kind="$1" url="$2" command_prefix="$3"
    local candidate="" local_script=""
    local candidates=()
    case "$kind" in
        backend)
            [[ -n "${PVE_BUILD_BACKEND_LOCAL_PATH:-}" ]] && candidates+=("${PVE_BUILD_BACKEND_LOCAL_PATH}")
            candidates+=(
                "/Volumes/Additional/个人数据/GitHub/pve/scripts/build_backend.sh"
                "${SCRIPT_DIR}/../../../pve/scripts/build_backend.sh"
                "${SCRIPT_DIR}/../../../../../pve/scripts/build_backend.sh"
            )
            ;;
        nat)
            [[ -n "${PVE_BUILD_NAT_LOCAL_PATH:-}" ]] && candidates+=("${PVE_BUILD_NAT_LOCAL_PATH}")
            candidates+=(
                "/Volumes/Additional/个人数据/GitHub/pve/scripts/build_nat_network.sh"
                "${SCRIPT_DIR}/../../../pve/scripts/build_nat_network.sh"
                "${SCRIPT_DIR}/../../../../../pve/scripts/build_nat_network.sh"
            )
            ;;
        *) return 1 ;;
    esac
    for candidate in "${candidates[@]}"; do
        if [[ -n "$candidate" && -f "$candidate" ]]; then
            local_script="$candidate"
            break
        fi
    done
    if [[ -n "$local_script" ]]; then
        local payload
        payload=$(gzip -c "$local_script" | base64 | tr -d '\n') || return 1
        log_info "Using local PVE ${kind} script: ${local_script}"
        cat <<PVE_AUX_CMD
${command_prefix} cat > /tmp/pve-${kind}.sh.gz.b64 <<'PVE_AUX_B64'
${payload}
PVE_AUX_B64
base64 -d /tmp/pve-${kind}.sh.gz.b64 | gzip -dc > /tmp/pve-${kind}.sh && rm -f /tmp/pve-${kind}.sh.gz.b64 && chmod +x /tmp/pve-${kind}.sh && ${command_prefix} bash /tmp/pve-${kind}.sh
PVE_AUX_CMD
        return 0
    fi
    printf '%s\n' "${command_prefix} curl -sSL '${url}' | bash"
}

# Run a PVE installer/bridge script as a detached remote job.  Both the PVE
# installer and build_nat_network.sh can reload networking, so the SSH session
# may disappear while the remote process is still doing useful work.  A
# status file makes completion observable after SSH recovers and prevents the
# generic SSH retry loop from launching the same mutating script twice.
pve_run_remote_job() {
    local ip="$1" command="$2" label="${3:-pve-job}" max_wait="${4:-3600}"
    local poll_interval="${PVE_REMOTE_POLL_INTERVAL:-10}"
    local recovery_wait="${PVE_REMOTE_SSH_RECOVERY_WAIT:-600}"
    local running_grace_wait="${PVE_REMOTE_RUNNING_GRACE_WAIT:-900}"
    [[ "$max_wait" =~ ^[0-9]+$ && "$max_wait" -gt 0 ]] || max_wait=3600
    [[ "$poll_interval" =~ ^[0-9]+$ && "$poll_interval" -gt 0 ]] || poll_interval=10
    [[ "$recovery_wait" =~ ^[0-9]+$ && "$recovery_wait" -gt 0 ]] || recovery_wait=600
    [[ "$running_grace_wait" =~ ^[0-9]+$ && "$running_grace_wait" -gt 0 ]] || running_grace_wait=900

    local safe_label token state_dir script_file log_file status_file started_file pid_file lock_dir payload
    safe_label=$(printf '%s' "$label" | tr -c 'A-Za-z0-9_.-' '-' | sed 's/--*/-/g; s/^-//; s/-$//')
    [[ -n "$safe_label" ]] || safe_label="pve-job"
    token="${safe_label}-$$-$(date +%s)-${RANDOM}"
    state_dir="/tmp/oneclickvirt-pve-jobs"
    script_file="${state_dir}/${token}.sh"
    log_file="${state_dir}/${token}.log"
    status_file="${state_dir}/${token}.status"
    started_file="${state_dir}/${token}.started"
    pid_file="${state_dir}/${token}.pid"
    lock_dir="${state_dir}/${token}.lock"
    PVE_LAST_JOB_STATE="unknown"
    payload=$(printf '%s' "$command" | base64 | tr -d '\n') || return 1

    local launcher
    launcher=$(printf '%s\n' \
        "set -o errexit -o nounset" \
        "mkdir -p ${state_dir}" \
        "if mkdir ${lock_dir} 2>/dev/null; then" \
        "  rm -f ${status_file} ${started_file} ${pid_file} ${log_file} ${script_file}" \
        "  printf '%s' '${payload}' | base64 -d > ${script_file}" \
        "  chmod 700 ${script_file}" \
        "  printf '%s\\n' STARTED > ${started_file}" \
        "  nohup bash -c 'bash ${script_file} >${log_file} 2>&1; rc=\$?; printf \"%s\\n\" \"\$rc\" > ${status_file}.tmp; mv -f ${status_file}.tmp ${status_file}; rm -f ${script_file} ${pid_file}; rmdir ${lock_dir} 2>/dev/null || true' </dev/null >/dev/null 2>&1 &" \
        "  printf '%s\\n' \"\$!\" > ${pid_file}" \
        "  printf '%s\\n' STARTED" \
        "else" \
        "  if [ -s ${status_file} ]; then cat ${status_file}; else echo RUNNING; fi" \
        "fi")

    log_info "Starting detached PVE job '${label}' (network may be unavailable while it runs)..."
    local launch_rc=0
    platform_exec_once "$ip" "$launcher" 60 >/dev/null 2>&1 || launch_rc=$?
    if [[ $launch_rc -ne 0 ]]; then
        log_warning "Detached PVE job '${label}' lost its initial SSH response (exit=${launch_rc}); checking for remote completion"
    fi

    local elapsed=0 query_output="" state="" query_rc=0 ssh_recovery_attempted=false launch_attempts=1
    while [[ $elapsed -lt $max_wait ]]; do
        query_output=""
        query_rc=0
        query_output=$(platform_exec_once "$ip" \
            "if [ -s ${status_file} ]; then cat ${status_file}; elif [ -f ${started_file} ] || [ -d ${lock_dir} ]; then echo RUNNING; else echo NOT_STARTED; fi" \
            30 2>/dev/null) || query_rc=$?
        if [[ $query_rc -ne 0 ]]; then
            if [[ "$ssh_recovery_attempted" == "false" ]]; then
                ssh_recovery_attempted=true
                log_info "PVE job '${label}' is not reachable over SSH; waiting up to ${recovery_wait}s for network recovery"
                if ! wait_for_ssh "$ip" "$recovery_wait" >/dev/null 2>&1; then
                    log_warning "SSH did not recover while observing PVE job '${label}'"
                    return 75
                fi
                # Count a lost-transport observation against the primary
                # budget even when the provider's wait helper returns early;
                # repeated flaps must not create an unbounded polling loop.
                elapsed=$((elapsed + poll_interval))
                continue
            fi
            sleep "$poll_interval"
            elapsed=$((elapsed + poll_interval))
            continue
        fi
        ssh_recovery_attempted=false
        if [[ $query_rc -eq 0 ]]; then
            state=$(printf '%s\n' "$query_output" | tr -d '\r' | tail -n 1)
            if [[ "$state" =~ ^[0-9]+$ ]]; then
                if [[ "$state" -eq 0 ]]; then
                    pve_mark_job_completed "$ip" "$label" "Detached PVE job '${label}' completed"
                    return $?
                fi
                local remote_log
                remote_log=$(platform_exec_once "$ip" "tail -80 ${log_file} 2>/dev/null || true" 30 2>/dev/null || true)
                [[ -n "$remote_log" ]] && log_warning "Detached PVE job '${label}' output:\n${remote_log}"
                PVE_LAST_JOB_STATE="failed"
                log_warning "Detached PVE job '${label}' completed with exit=${state}"
                return 1
            fi
            if [[ "$state" == "NOT_STARTED" && $launch_rc -ne 0 ]]; then
                # The launcher itself did not reach the host.  Give the SSH
                # endpoint one recovery cycle before classifying it as infra.
                if wait_for_ssh "$ip" "$recovery_wait" >/dev/null 2>&1; then
                    launch_attempts=$((launch_attempts + 1))
                    if [[ "$launch_attempts" -gt 2 ]]; then
                        log_error "Detached PVE job '${label}' was not started after SSH recovery"
                        PVE_LAST_JOB_STATE="not_started"
                        return 75
                    fi
                    launch_rc=0
                    platform_exec_once "$ip" "$launcher" 60 >/dev/null 2>&1 || launch_rc=$?
                    continue
                fi
                PVE_LAST_JOB_STATE="unknown"
                return 75
            fi
        fi
        sleep "$poll_interval"
        elapsed=$((elapsed + poll_interval))
    done

    log_warning "Detached PVE job '${label}' did not expose a completion status within ${max_wait}s; waiting for SSH recovery (${recovery_wait}s)"
    local final_query_rc=1
    state="UNKNOWN"
    if wait_for_ssh "$ip" "$recovery_wait" >/dev/null 2>&1; then
        final_query_rc=0
        query_output=$(platform_exec_once "$ip" \
            "if [ -s ${status_file} ]; then cat ${status_file}; elif [ -f ${started_file} ] || [ -d ${lock_dir} ]; then echo RUNNING; else echo NOT_STARTED; fi" \
            30 2>/dev/null) || final_query_rc=$?
        state=$(printf '%s\n' "$query_output" | tr -d '\r' | tail -n 1)
        if [[ $final_query_rc -eq 0 && "$state" =~ ^[0-9]+$ ]]; then
            if [[ "$state" -eq 0 ]]; then
                pve_mark_job_completed "$ip" "$label" "Detached PVE job '${label}' completed after SSH recovery"
                return $?
            fi
            PVE_LAST_JOB_STATE="failed"
            log_warning "Detached PVE job '${label}' completed after recovery with exit=${state}"
            return 1
        fi
        if [[ $final_query_rc -eq 0 && "$state" == "RUNNING" ]]; then
            # Never restart a worker while its mutating installer is still
            # running.  Give a long network reload a separate grace window,
            # then classify the result as infrastructure if it remains live.
            local grace_elapsed=0 grace_query_rc grace_state
            log_warning "Detached PVE job '${label}' is still running after the primary wait; observing for ${running_grace_wait}s before classifying it"
            while [[ $grace_elapsed -lt $running_grace_wait ]]; do
                sleep "$poll_interval"
                grace_elapsed=$((grace_elapsed + poll_interval))
                grace_query_rc=0
                query_output=$(platform_exec_once "$ip" \
                    "if [ -s ${status_file} ]; then cat ${status_file}; elif [ -f ${started_file} ] || [ -d ${lock_dir} ]; then echo RUNNING; else echo UNKNOWN; fi" \
                    30 2>/dev/null) || grace_query_rc=$?
                if [[ $grace_query_rc -ne 0 ]]; then
                    # A second network flap during the grace window should
                    # consume recovery time, not be mistaken for a completed
                    # timeout.  Never launch a replacement job here.
                    wait_for_ssh "$ip" "$recovery_wait" >/dev/null 2>&1 || true
                    continue
                fi
                grace_state=$(printf '%s\n' "$query_output" | tr -d '\r' | tail -n 1)
                if [[ "$grace_state" =~ ^[0-9]+$ ]]; then
                    if [[ "$grace_state" -eq 0 ]]; then
                        pve_mark_job_completed "$ip" "$label" "Detached PVE job '${label}' completed during the extended recovery window"
                        return $?
                    fi
                    PVE_LAST_JOB_STATE="failed"
                    log_warning "Detached PVE job '${label}' completed during recovery with exit=${grace_state}"
                    return 1
                fi
                [[ "$grace_state" == "RUNNING" ]] || break
            done
            PVE_LAST_JOB_STATE="running"
            log_error "Detached PVE job '${label}' is still running after ${running_grace_wait}s of extended observation; leaving it untouched"
            return 75
        fi
    fi

    # A provider restart is safe only when the launcher failed and the host
    # explicitly reports that no detached job was started.  An unknown state
    # is never restarted because the installer may still be changing network
    # state behind a temporarily unavailable SSH endpoint.
    if [[ $final_query_rc -eq 0 && "$state" == "NOT_STARTED" && "$launch_rc" -ne 0 && "${PVE_REMOTE_RESTART_ON_LOSS:-true}" == "true" && -n "${ACTIVE_INSTANCE_ID:-}" ]] && \
       declare -f platform_recover_worker >/dev/null 2>&1; then
        log_warning "Attempting provider restart for worker transport recovery after PVE job '${label}'"
        if platform_recover_worker "${ACTIVE_INSTANCE_ID}" "$ip" "$recovery_wait" >/dev/null 2>&1; then
            query_output=$(platform_exec_once "$ip" \
                "if [ -s ${status_file} ]; then cat ${status_file}; else echo UNKNOWN; fi" \
                30 2>/dev/null || true)
            state=$(printf '%s\n' "$query_output" | tr -d '\r' | tail -n 1)
            if [[ "$state" == "0" ]]; then
                pve_mark_job_completed "$ip" "$label" "Detached PVE job '${label}' completed after provider recovery"
                return $?
            fi
        fi
    fi
    [[ "$PVE_LAST_JOB_STATE" == "unknown" && "$state" == "NOT_STARTED" ]] && PVE_LAST_JOB_STATE="not_started"
    log_error "Detached PVE job '${label}' could not be observed after SSH recovery"
    return 75
}

pve_check_postcondition() {
    local ip="$1" kind="$2" check_cmd=""
    local state_dir="${PVE_STATE_DIR:-/usr/local/bin}"
    [[ "$state_dir" =~ ^/[A-Za-z0-9._/-]+$ ]] || state_dir="/usr/local/bin"
    case "$kind" in
        install)
            check_cmd='set -o errexit -o nounset
command -v pvesh >/dev/null 2>&1 && command -v pct >/dev/null 2>&1 && command -v qm >/dev/null 2>&1
mountpoint -q /etc/pve
node_name="$(hostname -s)"
test -d "/etc/pve/nodes/${node_name}/lxc" && test -d "/etc/pve/nodes/${node_name}/qemu-server"
systemctl is-active --quiet pve-cluster && systemctl is-active --quiet pvedaemon && systemctl is-active --quiet pveproxy
ss -lnt 2>/dev/null | grep -q ":8006 "
curl -ksS --connect-timeout 3 --max-time 10 https://127.0.0.1:8006/api2/json/version >/dev/null'
            ;;
        backend)
            check_cmd='test -s /usr/local/bin/build_backend_pve.txt && command -v pvesh >/dev/null 2>&1 && mountpoint -q /etc/pve'
            ;;
        nat)
            # The NAT script persists the selected subnet/gateway and may use
            # nftables or iptables depending on the host.  Verify all three
            # layers so a stale interfaces stanza cannot be accepted alone.
            check_cmd=$(cat <<'NAT_POSTCONDITION'
set -o errexit -o nounset
test -r /etc/network/interfaces
grep -Eq "^[[:space:]]*auto[[:space:]]+vmbr1([[:space:]]|$)" /etc/network/interfaces
test -s __STATE_DIR__/pve_nat_subnet && test -s __STATE_DIR__/pve_nat_gateway
nat_subnet="$(tr -d "[:space:]" < __STATE_DIR__/pve_nat_subnet)"
nat_gateway="$(tr -d "[:space:]" < __STATE_DIR__/pve_nat_gateway)"
printf "%s" "$nat_subnet" | grep -Eq "^([0-9]{1,3}[.]){3}0/24$"
printf "%s" "$nat_gateway" | grep -Eq "^([0-9]{1,3}[.]){3}[0-9]{1,3}$"
ip link show dev vmbr1 2>/dev/null | grep -Eq "(^|[[:space:]])state[[:space:]]+(UP|UNKNOWN)([[:space:]]|$)"
ip -o -4 addr show dev vmbr1 2>/dev/null | grep -Fq " ${nat_gateway}/24 "
nat_rule_found=false
if command -v nft >/dev/null 2>&1 && nft list ruleset >/dev/null 2>&1; then
    if nft list ruleset 2>/dev/null | grep -F "$nat_subnet" | grep -Fq masquerade; then
        nat_rule_found=true
    fi
fi
if command -v iptables >/dev/null 2>&1 && iptables -t nat -S POSTROUTING 2>/dev/null | grep -F "$nat_subnet" | grep -Fq MASQUERADE; then
    nat_rule_found=true
fi
test "$nat_rule_found" = true
NAT_POSTCONDITION
)
            check_cmd="${check_cmd//__STATE_DIR__/${state_dir}}"
            ;;
        *) return 1 ;;
    esac
    platform_exec_once "$ip" "$check_cmd" 90 >/dev/null 2>&1
}

pve_wait_postcondition() {
    local ip="$1" kind="$2" max_wait="${PVE_POSTCONDITION_MAX_WAIT:-180}"
    local interval="${PVE_POSTCONDITION_POLL_INTERVAL:-10}" elapsed=0
    [[ "$max_wait" =~ ^[0-9]+$ && "$max_wait" -gt 0 ]] || max_wait=180
    [[ "$interval" =~ ^[0-9]+$ && "$interval" -gt 0 ]] || interval=10
    while [[ $elapsed -le $max_wait ]]; do
        pve_check_postcondition "$ip" "$kind" && return 0
        [[ $elapsed -eq $max_wait ]] && break
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    return 1
}

pve_reboot_marker_present() {
    local ip="$1"
    platform_exec_once "$ip" "test -s /usr/local/bin/reboot_pve.txt" 30 >/dev/null 2>&1
}

pve_wait_reboot_marker() {
    local ip="$1" max_wait="${PVE_REBOOT_MARKER_MAX_WAIT:-180}"
    local interval="${PVE_REBOOT_MARKER_POLL_INTERVAL:-10}" elapsed=0
    [[ "$max_wait" =~ ^[0-9]+$ && "$max_wait" -gt 0 ]] || max_wait=180
    [[ "$interval" =~ ^[0-9]+$ && "$interval" -gt 0 ]] || interval=10
    while [[ $elapsed -le $max_wait ]]; do
        pve_reboot_marker_present "$ip" && return 0
        if ! wait_for_ssh "$ip" "$interval" >/dev/null 2>&1; then
            elapsed=$((elapsed + interval))
            continue
        fi
        [[ $elapsed -eq $max_wait ]] && break
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    return 1
}

pve_run_job_or_accept_postcondition() {
    local ip="$1" command="$2" label="$3" kind="$4" max_wait="$5" rc=0
    PVE_LAST_JOB_STATE="unknown"
    pve_run_remote_job "$ip" "$command" "$label" "$max_wait" || rc=$?
    [[ $rc -eq 0 ]] && return 0
    # A live detached job must never be bypassed by a partial postcondition;
    # doing so would start the next mutating phase concurrently on the host.
    # A postcondition can explain a known non-zero completion (for example
    # build_backend.sh deliberately exits 1 when its durable marker exists),
    # but it cannot prove anything while the remote job state is unknown or
    # still running.  In those states, advancing could overlap a network
    # mutation that is still active.
    if [[ "${PVE_LAST_JOB_STATE:-unknown}" == "failed" ]] && pve_wait_postcondition "$ip" "$kind"; then
        log_warning "PVE job '${label}' returned ${rc}, but its ${kind} postcondition is present; continuing"
        return 0
    fi
    [[ $rc -eq 75 ]] && return 75
    return "$rc"
}

install_env() {
    local id="$1" ip="$2" env="$3"
    log_section "Installing ${env} environment on worker node"
    local noninteractive_prefix="export noninteractive=true; export DEBIAN_FRONTEND=noninteractive;"
    local install_wait="${ENV_INSTALL_MAX_WAIT:-3600}"
    local apt_lock_wait="${APT_LOCK_MAX_WAIT:-1800}"
    local apt_install_wait="${APT_INSTALL_MAX_WAIT:-1800}"
    local reboot_wait="${ENV_REBOOT_SSH_MAX_WAIT:-600}"
    local pve_wait="${PVE_INSTALL_MAX_WAIT:-3600}"
    local url="${ENV_INSTALL_SCRIPTS[$env]:-}"
    [[ -z "$url" ]] && { log_error "Unknown environment: ${env}"; return 1; }
    local pve_use_private_ip="${PVE_USE_PRIVATE_IP:-true}"
    local pve_main_interface="${PVE_MAIN_INTERFACE:-}"
    if [[ "$env" == "proxmoxve" && "${ACTIVE_PLATFORM:-}" == "lightnode" ]]; then
        pve_use_private_ip="${PVE_USE_PRIVATE_IP:-false}"
        pve_main_interface="${PVE_MAIN_INTERFACE:-eth1}"
    fi
    local pve_hostname="${PVE_HOSTNAME:-pve}"
    case "${pve_use_private_ip,,}" in
        true|false) ;;
        *) log_error "PVE_USE_PRIVATE_IP must be true or false"; return 1 ;;
    esac
    if [[ ! "$pve_hostname" =~ ^[A-Za-z0-9]+$ ]]; then
        log_error "PVE_HOSTNAME must contain only letters and digits"
        return 1
    fi
    if [[ -n "$pve_main_interface" && ! "$pve_main_interface" =~ ^[A-Za-z0-9_.-]+$ ]]; then
        log_error "PVE_MAIN_INTERFACE must contain only letters, numbers, dots, underscores, or hyphens"
        return 1
    fi
    if [[ -n "${PVE_NAT_SUBNET:-}" && ! "${PVE_NAT_SUBNET}" =~ ^([0-9]{1,3}\.){3}0/24$ ]]; then
        log_error "PVE_NAT_SUBNET must be an IPv4 /24 network ending in .0"
        return 1
    fi
    local pve_env_prefix="DEBIAN_FRONTEND=noninteractive noninteractive=true USE_PRIVATE_IP=${pve_use_private_ip} PVE_HOSTNAME=${pve_hostname}"
    if [[ -n "$pve_main_interface" ]]; then
        pve_env_prefix+=" PVE_MAIN_INTERFACE=${pve_main_interface}"
    fi
    local pve_install_cmd=""
    local pve_backend_cmd=""
    local pve_nat_cmd=""
    if [[ "$env" == "proxmoxve" ]]; then
        pve_install_cmd=$(build_env_install_command "$env" "$url" "$noninteractive_prefix" "$pve_env_prefix") || return 1
        pve_backend_cmd=$(build_pve_aux_command backend "$PVE_BUILD_BACKEND" "$noninteractive_prefix") || return 1
        local pve_nat_prefix="$noninteractive_prefix"
        if [[ -n "${PVE_NAT_SUBNET:-}" ]]; then
            pve_nat_prefix+=" PVE_NAT_SUBNET=${PVE_NAT_SUBNET}"
        fi
        pve_nat_cmd=$(build_pve_aux_command nat "$PVE_BUILD_NAT" "$pve_nat_prefix") || return 1
    fi
    if declare -f platform_validate_worker_resources >/dev/null 2>&1; then
        platform_validate_worker_resources "$env" "$ip" "${ACTIVE_PLATFORM:-}" || return 75
    fi
    ensure_worker_swap "${ip}" "worker before ${env} install" || log_warning "Continuing even though swap setup did not complete before ${env} install"
    # Wait for cloud-init and other processes to release apt/dpkg locks
    # min_wait=120s (required wait), max_wait defaults to 1800s, interval=15s
    wait_for_apt_lock "${ip}" 120 "$apt_lock_wait" 15 || return 75
    platform_exec_and_wait "${ip}" "${noninteractive_prefix} apt-get update -y && apt-get -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y curl wget sudo jq ipcalc lsof" "$apt_install_wait" || return 75
    ensure_worker_dns "${ip}" "worker before ${env} install" || true
    # Build non-interactive env var prefix per script type
    local env_prefix
    case "$env" in
        docker)
            env_prefix="DEBIAN_FRONTEND=noninteractive NEED_DISK_LIMIT=n CN=false WITHOUTCDN=false IPV6_MAXIMUM_SUBSET=n"
            ;;
        lxd)
            env_prefix="DEBIAN_FRONTEND=noninteractive noninteractive=true NONINTERACTIVE=true CN=false WITHOUTCDN=false"
            ;;
        incus)
            env_prefix="DEBIAN_FRONTEND=noninteractive noninteractive=true INCUS_NONINTERACTIVE=true INCUS_STORAGE_BACKEND=${INCUS_STORAGE_BACKEND:-dir} WITHOUTCDN=false"
            ;;
        podman)
            env_prefix="DEBIAN_FRONTEND=noninteractive NEED_DISK_LIMIT=n WITHOUTCDN=false"
            ;;
        containerd)
            env_prefix="DEBIAN_FRONTEND=noninteractive NEED_DISK_LIMIT=n WITHOUTCDN=false"
            ;;
        qemu)
            env_prefix="DEBIAN_FRONTEND=noninteractive QEMU_IMAGES_PATH=/var/lib/libvirt/images"
            ;;
        kubevirt)
            env_prefix="DEBIAN_FRONTEND=noninteractive"
            ;;
        *)
            env_prefix="DEBIAN_FRONTEND=noninteractive"
            ;;
    esac
	local env_install_cmd=""
	env_install_cmd=$(build_env_install_command "$env" "$url" "$noninteractive_prefix" "$env_prefix") || return 1

    if [[ "$env" == "proxmoxve" ]]; then
        log_info "PVE install step 1/3: installing PVE kernel (reboot required)..."
        # The first pass intentionally exits after writing reboot_pve.txt.  It
        # can reload networking before that marker is written, so observe it as
        # a detached job and never restart the installer from an SSH retry.
        local pve_pre_job_rc=0 pve_reboot_required=true
        if pve_check_postcondition "${ip}" install; then
            # A reused worker may already have PVE installed.  The upstream
            # installer exits early in that case; do not force an unnecessary
            # reboot or wait forever for a marker that can never be created.
            pve_reboot_required=false
            log_info "Existing PVE runtime detected; skipping the pre-reboot installer pass"
        elif pve_reboot_marker_present "${ip}"; then
            log_info "PVE installer left its reboot marker from an earlier pass; continuing with the pending reboot"
        else
            pve_run_remote_job "${ip}" "${pve_install_cmd}" \
                "PVE installer pre-reboot" "$pve_wait" || pve_pre_job_rc=$?
            if pve_reboot_marker_present "${ip}"; then
                :
            elif pve_check_postcondition "${ip}" install; then
                pve_reboot_required=false
                log_info "PVE installer reported an already-ready runtime; skipping the kernel reboot"
            else
                log_error "PVE pre-reboot installer did not leave reboot marker or a ready PVE runtime (exit=${pve_pre_job_rc})"
                [[ $pve_pre_job_rc -eq 75 ]] && return 75
                return "${pve_pre_job_rc:-1}"
            fi
        fi
        if [[ "$pve_reboot_required" == "true" ]]; then
            if ! pve_wait_reboot_marker "${ip}"; then
                log_error "PVE pre-reboot installer did not expose /usr/local/bin/reboot_pve.txt"
                return 75
            fi
            if ! pve_wait_for_ssh_stable "${ip}" "$reboot_wait"; then
                log_error "Worker SSH did not stabilize before the PVE kernel reboot"
                return 75
            fi
            log_info "Rebooting worker to load PVE kernel..."
            platform_exec_once "${ip}" "reboot" 10 || true
            sleep 25
            pve_wait_for_ssh_stable "${ip}" "$reboot_wait" || return 75
        fi
        ensure_worker_swap "${ip}" "worker after PVE reboot" || log_warning "Swap setup after PVE reboot did not complete"
        stabilize_worker_network_for_env "${ip}" "${env}" "worker after PVE reboot" || true
        log_info "PVE install step 2/3: completing PVE configuration after reboot..."
        local pve_job_rc=0
        pve_run_job_or_accept_postcondition "${ip}" "${pve_install_cmd}" \
            "PVE installer post-reboot" install "$pve_wait" || pve_job_rc=$?
        (( pve_job_rc == 0 )) || return "$pve_job_rc"
        log_info "PVE install step 3a/3: configuring backend bridge..."
        pve_run_job_or_accept_postcondition "${ip}" "${pve_backend_cmd}" \
            "PVE backend bridge" backend "${install_wait}" || pve_job_rc=$?
        (( pve_job_rc == 0 )) || return "$pve_job_rc"
        log_info "PVE install step 3b/3: building NAT IPv4 network..."
        pve_run_job_or_accept_postcondition "${ip}" "${pve_nat_cmd}" \
            "PVE NAT network" nat "${install_wait}" || pve_job_rc=$?
        (( pve_job_rc == 0 )) || return "$pve_job_rc"
    elif [[ "$env" == "kubevirt" ]]; then
        # kubevirt needs K3s + KubeVirt + CDI, single-pass install (no reboot needed)
        # K3s + KubeVirt + CDI typically takes 60-120 minutes; use 7200s (2h) to be safe
        log_info "Installing KubeVirt environment (K3s + KubeVirt + CDI)..."
        run_kubevirt_installer_with_retry "${ip}" "${env_install_cmd}"
    elif [[ "$env" == "qemu" ]]; then
        # qemu needs libvirt + QEMU/KVM, single-pass install
        log_info "Installing QEMU/KVM environment..."
        platform_exec_and_wait "${ip}" "${env_install_cmd}" "$install_wait"
    else
        platform_exec_and_wait "${ip}" "${env_install_cmd}" "$install_wait" || true
        log_info "Rebooting worker to apply network/kernel settings..."
        platform_exec_and_wait "${ip}" "reboot" 10 || true
        log_info "Waiting for SSH after reboot (max ${reboot_wait}s)..."
        wait_for_ssh "${ip}" "$reboot_wait"
        ensure_worker_swap "${ip}" "worker after ${env} reboot" || log_warning "Swap setup after ${env} reboot did not complete"
        stabilize_worker_network_for_env "${ip}" "${env}" "worker after ${env} reboot" || true
        log_info "Re-running ${env} install to complete post-reboot setup..."
        platform_exec_and_wait "${ip}" "${env_install_cmd}" "$install_wait"
    fi
    stabilize_worker_network_for_env "${ip}" "${env}" "worker after ${env} install" || true
}

verify_worker_runtime() {
    local _id="$1" ip="$2" env="$3"
    local verify_cmd=""
    local verify_timeout=180
    case "$env" in
        docker)
            verify_cmd="command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1"
            ;;
        podman)
            verify_cmd="command -v podman >/dev/null 2>&1 && podman info >/dev/null 2>&1"
            ;;
        containerd)
            verify_cmd="command -v ctr >/dev/null 2>&1 && systemctl is-active --quiet containerd"
            ;;
        lxd)
            verify_cmd="command -v lxc >/dev/null 2>&1 && lxc info >/dev/null 2>&1"
            ;;
        incus)
            verify_cmd="command -v incus >/dev/null 2>&1 && incus info >/dev/null 2>&1"
            ;;
        proxmoxve)
            verify_cmd=$(cat <<'VERIFY_PROXMOXVE'
set -u
node_name="$(hostname -s)"
command -v pvesh >/dev/null 2>&1
command -v pct >/dev/null 2>&1
command -v qm >/dev/null 2>&1
mountpoint -q /etc/pve
test -d "/etc/pve/nodes/${node_name}/lxc"
test -d "/etc/pve/nodes/${node_name}/qemu-server"
systemctl is-active --quiet pve-cluster
systemctl is-active --quiet pvedaemon
systemctl is-active --quiet pveproxy
ss -lnt 2>/dev/null | grep -q ':8006 '
curl -ksS --connect-timeout 3 --max-time 10 https://127.0.0.1:8006/api2/json/version >/dev/null
VERIFY_PROXMOXVE
)
            ;;
        qemu)
            verify_cmd="command -v virsh >/dev/null 2>&1 && (systemctl is-active --quiet libvirtd || systemctl is-active --quiet virtqemud)"
            ;;
        kubevirt)
            verify_timeout="${KUBEVIRT_RUNTIME_MAX_WAIT:-2400}"
            verify_cmd=$(cat <<'VERIFY_KUBEVIRT'
set -u
export KUBECONFIG="${KUBECONFIG:-/etc/rancher/k3s/k3s.yaml}"
deadline=$((SECONDS + ${KUBEVIRT_RUNTIME_MAX_WAIT:-2400}))
last_reason=""
while [ "$SECONDS" -lt "$deadline" ]; do
    if ! command -v kubectl >/dev/null 2>&1; then
        last_reason="kubectl missing"
    elif ! kubectl get nodes >/dev/null 2>&1; then
        last_reason="kubernetes API unavailable"
    elif ! kubectl wait --for=condition=Ready nodes --all --timeout=20s >/dev/null 2>&1; then
        last_reason="node not Ready"
    elif ! kubectl get crd virtualmachines.kubevirt.io >/dev/null 2>&1; then
        last_reason="KubeVirt VirtualMachine CRD missing"
    elif ! kubectl get crd kubevirts.kubevirt.io >/dev/null 2>&1; then
        last_reason="KubeVirt CRD missing"
    elif ! kubectl -n kubevirt get kubevirt kubevirt >/dev/null 2>&1; then
        last_reason="KubeVirt CR missing"
    elif ! kubectl -n kubevirt wait kubevirt/kubevirt --for=condition=Available --timeout=30s >/dev/null 2>&1; then
        phase="$(kubectl -n kubevirt get kubevirt kubevirt -o jsonpath='{.status.phase}' 2>/dev/null || true)"
        last_reason="KubeVirt not Available (phase=${phase:-unknown})"
    elif ! kubectl get crd datavolumes.cdi.kubevirt.io >/dev/null 2>&1; then
        last_reason="CDI DataVolume CRD missing"
    elif ! kubectl api-resources --api-group=cdi.kubevirt.io 2>/dev/null | grep -q '^datavolumes[[:space:]]'; then
        last_reason="CDI DataVolume API resource not discoverable"
    else
        echo "KUBEVIRT_RUNTIME_READY"
        kubectl get nodes -o wide || true
        kubectl -n kubevirt get kubevirt kubevirt || true
        kubectl -n kubevirt get pods || true
        kubectl -n cdi get pods 2>/dev/null || true
        exit 0
    fi
    echo "WAITING_KUBEVIRT_RUNTIME: ${last_reason}"
    sleep 20
done
echo "KUBEVIRT_RUNTIME_NOT_READY: ${last_reason:-timeout}"
echo "--- nodes ---"
kubectl get nodes -o wide 2>&1 || true
echo "--- kubevirt cr ---"
kubectl -n kubevirt get kubevirt kubevirt -o yaml 2>&1 || true
echo "--- kubevirt pods ---"
kubectl -n kubevirt get pods -o wide 2>&1 || true
echo "--- kubevirt pending pod descriptions ---"
for pod in $(kubectl -n kubevirt get pods --field-selector=status.phase=Pending -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null); do
    echo "### describe pod/${pod}"
    kubectl -n kubevirt describe pod "${pod}" 2>&1 || true
done
echo "--- kubevirt recent events ---"
kubectl -n kubevirt get events --sort-by=.lastTimestamp 2>&1 || true
echo "--- kubevirt recent logs ---"
for pod in $(kubectl -n kubevirt get pods -o jsonpath='{range .items[*]}{.metadata.name}{"\n"}{end}' 2>/dev/null); do
    echo "### logs pod/${pod}"
    kubectl -n kubevirt logs "${pod}" --all-containers --tail=120 2>&1 || true
done
echo "--- cdi resources ---"
kubectl get crd | grep -E 'cdi|kubevirt' 2>&1 || true
kubectl -n cdi get all -o wide 2>&1 || true
echo "--- cdi recent events ---"
kubectl -n cdi get events --sort-by=.lastTimestamp 2>&1 || true
exit 1
VERIFY_KUBEVIRT
)
            ;;
        *)
            log_warning "Unknown runtime '${env}', skipping runtime verification"
            return 0
            ;;
    esac

    log_info "Verifying ${env} runtime on worker..."
    local verify_output=""
    if verify_output=$(platform_exec_and_wait "${ip}" "${verify_cmd}" "$verify_timeout" 2>&1); then
        if declare -f platform_validate_worker_resources >/dev/null 2>&1 && \
           ! platform_validate_worker_resources "$env" "$ip" "${ACTIVE_PLATFORM:-}"; then
            log_warning "${env} runtime is ready, but the worker no longer satisfies its peak resource budget"
            return 75
        fi
        log_success "${env} runtime verified on worker"
        [[ "${DEBUG:-0}" == "1" && -n "$verify_output" ]] && printf '%s\n' "$verify_output" >&2
        return 0
    fi
    log_warning "${env} runtime verification failed or timed out"
    [[ -n "$verify_output" ]] && printf '%s\n' "$verify_output" >&2

    # A runtime command failure and an unreachable worker are different failure
    # classes. Package installation (notably ifupdown2 on PVE) may reload the
    # network and make SSH disappear after the installer itself has completed.
    # Preserve that distinction so the orchestrator can mark transient node
    # transport loss as an infrastructure skip instead of a product regression.
    local ssh_recheck_wait="${WORKER_RUNTIME_SSH_RECHECK_WAIT:-90}"
    if ! wait_for_ssh "${ip}" "${ssh_recheck_wait}" >/dev/null 2>&1; then
        log_error "Worker SSH stayed unreachable after ${env} runtime verification"
        return 75
    fi
    return 1
}

# Pre-populate the worker with deterministic instances for discovery/import
# testing. Readiness is exported per instance type so discovery assertions never
# pass because an unrelated instance happened to have the same broad type.
prepare_dirty_node() {
    local id="$1" ip="$2" env="$3" requested_types="${4:-${INSTANCE_TYPES:-both}}"
    local expected_fixtures=0 ready_fixtures=0
    local prepare_container=false prepare_vm=false

    case "$requested_types" in
        both) prepare_container=true; prepare_vm=true ;;
        container) prepare_container=true ;;
        vm) prepare_vm=true ;;
        *) log_error "Unsupported dirty-node instance type selection: ${requested_types}"; return 75 ;;
    esac

    export DIRTY_NODE_CONTAINER_READY=false
    export DIRTY_NODE_VM_READY=false
    export DIRTY_NODE_CONTAINER_EXPECTED=false
    export DIRTY_NODE_VM_EXPECTED=false
    export DIRTY_NODE_CONTAINER_NAME=""
    export DIRTY_NODE_VM_NAME=""
    export DIRTY_NODE_VM_PROVIDER_ID=""

    log_section "Preparing non-clean worker node for discovery tests"
    case "$env" in
        docker)
            expected_fixtures=1
            DIRTY_NODE_CONTAINER_EXPECTED=true
            if platform_exec_and_wait "${ip}" "docker inspect pre_existing_1 >/dev/null 2>&1 || docker run -d --name pre_existing_1 -e API_TOKEN=dirty-node-secret -p 22022:22/tcp -p 28080:80/tcp alpine sleep 3600" 120; then
                DIRTY_NODE_CONTAINER_READY=true
                DIRTY_NODE_CONTAINER_NAME="pre_existing_1"
                ready_fixtures=$((ready_fixtures + 1))
            fi
            # A stopped second container adds state coverage but is not required
            # by the discovery contract, so its image availability is optional.
            platform_exec_and_wait "${ip}" "docker inspect pre_existing_2 >/dev/null 2>&1 || { docker run -d --name pre_existing_2 debian:12 sleep 3600 && docker stop pre_existing_2; }" 120 || true
            ;;
        podman)
            expected_fixtures=1
            DIRTY_NODE_CONTAINER_EXPECTED=true
            if platform_exec_and_wait "${ip}" "podman inspect pre_existing_1 >/dev/null 2>&1 || podman run -d --name pre_existing_1 -e API_TOKEN=dirty-node-secret -p 22022:22/tcp docker.io/library/alpine sleep 3600" 120; then
                DIRTY_NODE_CONTAINER_READY=true
                DIRTY_NODE_CONTAINER_NAME="pre_existing_1"
                ready_fixtures=$((ready_fixtures + 1))
            fi
            ;;
        containerd)
            expected_fixtures=1
            DIRTY_NODE_CONTAINER_EXPECTED=true
            if platform_exec_and_wait "${ip}" "nerdctl inspect pre_existing_1 >/dev/null 2>&1 || nerdctl run -d --name pre_existing_1 -e API_TOKEN=dirty-node-secret -p 22022:22/tcp docker.io/library/alpine:latest sleep 3600" 120; then
                DIRTY_NODE_CONTAINER_READY=true
                DIRTY_NODE_CONTAINER_NAME="pre_existing_1"
                ready_fixtures=$((ready_fixtures + 1))
            fi
            ;;
        lxd)
            # Discovery only needs instance definitions. Empty, stopped
            # definitions avoid coupling this test to a public image server.
            if [[ "$prepare_container" == "true" ]]; then
                expected_fixtures=$((expected_fixtures + 1))
                DIRTY_NODE_CONTAINER_EXPECTED=true
                if platform_exec_and_wait "${ip}" "lxc info pre-existing-1 >/dev/null 2>&1 || lxc init pre-existing-1 --empty -c limits.cpu=1 -c limits.memory=256MiB" 120; then
                    DIRTY_NODE_CONTAINER_READY=true
                    DIRTY_NODE_CONTAINER_NAME="pre-existing-1"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            if [[ "$prepare_vm" == "true" ]]; then
                expected_fixtures=$((expected_fixtures + 1))
                DIRTY_NODE_VM_EXPECTED=true
                if platform_exec_and_wait "${ip}" "lxc info pre-existing-vm >/dev/null 2>&1 || lxc init pre-existing-vm --empty --vm -c limits.cpu=1 -c limits.memory=512MiB" 180; then
                    DIRTY_NODE_VM_READY=true
                    DIRTY_NODE_VM_NAME="pre-existing-vm"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            ;;
        incus)
            if [[ "$prepare_container" == "true" ]]; then
                expected_fixtures=$((expected_fixtures + 1))
                DIRTY_NODE_CONTAINER_EXPECTED=true
                if platform_exec_and_wait "${ip}" "incus info pre-existing-1 >/dev/null 2>&1 || incus init pre-existing-1 --empty -c limits.cpu=1 -c limits.memory=256MiB" 120; then
                    DIRTY_NODE_CONTAINER_READY=true
                    DIRTY_NODE_CONTAINER_NAME="pre-existing-1"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            if [[ "$prepare_vm" == "true" ]]; then
                expected_fixtures=$((expected_fixtures + 1))
                DIRTY_NODE_VM_EXPECTED=true
                if platform_exec_and_wait "${ip}" "incus info pre-existing-vm >/dev/null 2>&1 || incus init pre-existing-vm --empty --vm -c limits.cpu=1 -c limits.memory=512MiB" 180; then
                    DIRTY_NODE_VM_READY=true
                    DIRTY_NODE_VM_NAME="pre-existing-vm"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            ;;
        proxmoxve)
            # A stopped, diskless QEMU definition is sufficient for discovery and
            # import coverage. It avoids image downloads while exercising the same
            # PVE 8/9 cluster resource API as a normal VM.
            if [[ "$prepare_vm" == "true" ]]; then
                expected_fixtures=1
                DIRTY_NODE_VM_EXPECTED=true
                if platform_exec_and_wait "${ip}" "qm status 990 >/dev/null 2>&1 || qm create 990 --name pre-existing-vm --memory 512 --cores 1 --ostype l26" 120; then
                    DIRTY_NODE_VM_READY=true
                    DIRTY_NODE_VM_NAME="pre-existing-vm"
                    DIRTY_NODE_VM_PROVIDER_ID="990"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            ;;
        qemu)
            if [[ "$prepare_vm" == "true" ]]; then
                expected_fixtures=$((expected_fixtures + 1))
                DIRTY_NODE_VM_EXPECTED=true
                if platform_exec_and_wait "${ip}" "virsh dominfo pre-existing-vm >/dev/null 2>&1 || printf '%s' '<domain type=\"kvm\"><name>pre-existing-vm</name><memory unit=\"MiB\">512</memory><vcpu>1</vcpu><os><type arch=\"x86_64\">hvm</type></os></domain>' | virsh define /dev/stdin" 120; then
                    DIRTY_NODE_VM_READY=true
                    DIRTY_NODE_VM_NAME="pre-existing-vm"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            if [[ "$prepare_container" == "true" ]]; then
                expected_fixtures=$((expected_fixtures + 1))
                DIRTY_NODE_CONTAINER_EXPECTED=true
                if platform_exec_and_wait "${ip}" "virsh -c lxc:/// dominfo pre-existing-container >/dev/null 2>&1 || { mkdir -p /tmp/oneclickvirt-ci-lxc-rootfs; emulator=\$(command -v libvirt_lxc 2>/dev/null || echo /usr/libexec/libvirt_lxc); printf '%s' \"<domain type='lxc'><name>pre-existing-container</name><memory unit='MiB'>256</memory><vcpu>1</vcpu><os><type>exe</type><init>/bin/sh</init></os><devices><emulator>\${emulator}</emulator><filesystem type='mount'><source dir='/tmp/oneclickvirt-ci-lxc-rootfs'/><target dir='/'/></filesystem></devices></domain>\" | virsh -c lxc:/// define /dev/stdin; }" 120; then
                    DIRTY_NODE_CONTAINER_READY=true
                    DIRTY_NODE_CONTAINER_NAME="pre-existing-container"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            ;;
        kubevirt)
            platform_exec_and_wait "${ip}" "kubectl create namespace kubevirt-vms >/dev/null 2>&1 || true" 60 || true
            if [[ "$prepare_vm" == "true" ]]; then
                expected_fixtures=$((expected_fixtures + 1))
                DIRTY_NODE_VM_EXPECTED=true
                if platform_exec_and_wait "${ip}" "printf '%s' '{\"apiVersion\":\"kubevirt.io/v1\",\"kind\":\"VirtualMachine\",\"metadata\":{\"name\":\"pre-existing-vm\",\"namespace\":\"kubevirt-vms\"},\"spec\":{\"running\":false,\"template\":{\"metadata\":{\"labels\":{\"kubevirt.io/domain\":\"pre-existing-vm\"}},\"spec\":{\"domain\":{\"cpu\":{\"cores\":1},\"devices\":{},\"resources\":{\"requests\":{\"memory\":\"512Mi\"}}},\"terminationGracePeriodSeconds\":0}}}}' | kubectl apply -f -" 120; then
                    DIRTY_NODE_VM_READY=true
                    DIRTY_NODE_VM_NAME="pre-existing-vm"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            if [[ "$prepare_container" == "true" ]]; then
                expected_fixtures=$((expected_fixtures + 1))
                DIRTY_NODE_CONTAINER_EXPECTED=true
                if platform_exec_and_wait "${ip}" "printf '%s' '{\"apiVersion\":\"apps/v1\",\"kind\":\"Deployment\",\"metadata\":{\"name\":\"pre-existing-container\",\"namespace\":\"kubevirt-vms\",\"labels\":{\"oneclickvirt.io/type\":\"container\"}},\"spec\":{\"replicas\":0,\"selector\":{\"matchLabels\":{\"app\":\"pre-existing-container\"}},\"template\":{\"metadata\":{\"labels\":{\"app\":\"pre-existing-container\",\"oneclickvirt.io/type\":\"container\"}},\"spec\":{\"containers\":[{\"name\":\"main\",\"image\":\"busybox:latest\",\"resources\":{\"requests\":{\"cpu\":\"1\",\"memory\":\"256Mi\"}}}]}}}}' | kubectl apply -f -" 120; then
                    DIRTY_NODE_CONTAINER_READY=true
                    DIRTY_NODE_CONTAINER_NAME="pre-existing-container"
                    ready_fixtures=$((ready_fixtures + 1))
                fi
            fi
            ;;
        *)
            log_warning "No dirty-node fixture definition for environment '${env}'"
            return 0
            ;;
    esac

    export DIRTY_NODE_CONTAINER_READY DIRTY_NODE_VM_READY
    export DIRTY_NODE_CONTAINER_EXPECTED DIRTY_NODE_VM_EXPECTED
    export DIRTY_NODE_CONTAINER_NAME DIRTY_NODE_VM_NAME DIRTY_NODE_VM_PROVIDER_ID

    if (( ready_fixtures == expected_fixtures )); then
        log_success "Prepared ${ready_fixtures}/${expected_fixtures} dirty-node fixtures for ${env}"
        return 0
    fi
    log_warning "Prepared only ${ready_fixtures}/${expected_fixtures} dirty-node fixtures for ${env}"
    (( ready_fixtures > 0 )) && return 1
    return 75
}

deploy_master() {
    local id="$1" ip="$2" port="${3:-80}"
    log_section "Deploying master on ${ip} (port ${port})"
    platform_exec_and_wait "${ip}" "export noninteractive=true; export DEBIAN_FRONTEND=noninteractive; curl -sSL https://raw.githubusercontent.com/oneclickvirt/docker/main/scripts/dockerinstall.sh | bash" 600
    platform_exec_and_wait "${ip}" "docker pull oneclickvirt/oneclickvirt:latest && docker run -d --name oneclickvirt --restart=always -p ${port}:80 oneclickvirt/oneclickvirt:latest" 300
}

# MASTER_SERVER_DIR holds the path to the server directory where the binary runs.
# Set by deploy_master_local() and referenced by log helper functions.
MASTER_SERVER_DIR=""

ensure_ci_agent_assets() {
    local server_dir="$1"
    [[ "${ACTION_TEST_GENERATE_STUB_AGENT:-true}" == "true" ]] || return 0

    local asset_dir="${server_dir}/assets/agent"
    mkdir -p "$asset_dir" || return 1

    local version="${ACTION_TEST_STUB_AGENT_VERSION:-0.2.0}"
    local arch archive tmp binary
    for arch in amd64 arm64; do
        archive="${asset_dir}/oneclickvirt-agent-linux-${arch}.tar.gz"

        tmp="$(mktemp -d)"
        binary="${tmp}/oneclickvirt-agent-linux-${arch}"
        cat > "$binary" <<'EOF'
#!/bin/sh
case "${1:-}" in
  --version|-V|version)
    echo "oneclickvirt-agent __STUB_AGENT_VERSION__"
    exit 0
    ;;
esac

if command -v python3 >/dev/null 2>&1; then
  exec python3 - <<'PY'
import json
import os
import time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

PORT = int(os.environ.get("AGENT_PORT", "23782"))
TOKEN = ""
ENV_PATH = "/opt/oneclickvirt/agent/.env"
try:
    with open(ENV_PATH, "r", encoding="utf-8") as env_file:
        for line in env_file:
            if line.startswith("API_TOKEN="):
                TOKEN = line.split("=", 1)[1].strip()
                break
except OSError:
    pass

monitors = {}
next_id = 1

def as_interfaces(value):
    if isinstance(value, list):
        return [str(item) for item in value]
    if value in (None, ""):
        return []
    return [str(value)]

class Handler(BaseHTTPRequestHandler):
    server_version = "oneclickvirt-agent-stub/__STUB_AGENT_VERSION__"

    def log_message(self, fmt, *args):
        return

    def _authorized(self):
        if not TOKEN:
            return True
        return self.headers.get("x-token", "") == TOKEN

    def _body(self):
        length = int(self.headers.get("Content-Length", "0") or "0")
        if length <= 0:
            return {}
        raw = self.rfile.read(length)
        if not raw:
            return {}
        return json.loads(raw.decode("utf-8"))

    def _send(self, status, payload):
        data = json.dumps(payload, separators=(",", ":")).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def do_GET(self):
        if self.path.startswith("/swagger-ui/") or self.path in ("/", "/health"):
            return self._send(200, {"status": "ok", "version": "__STUB_AGENT_VERSION__"})
        if not self._authorized():
            return self._send(401, {"error": "unauthorized"})
        if self.path.startswith("/api/v1/list"):
            rows = []
            for monitor_id, monitor in sorted(monitors.items()):
                rows.append({
                    "id": monitor_id,
                    "interface": monitor.get("interface", []),
                    "provider_kind": monitor.get("provider_kind"),
                    "instance_name": monitor.get("instance_name"),
                    "total_bytes": 0,
                    "total_bytes_in": 0,
                    "total_bytes_out": 0,
                    "updated_at": int(time.time()),
                })
            return self._send(200, {"monitors": rows, "total": len(rows)})
        return self._send(404, {"error": "not found"})

    def do_POST(self):
        global next_id
        if not self._authorized():
            return self._send(401, {"error": "unauthorized"})
        try:
            body = self._body()
        except Exception as exc:
            return self._send(400, {"error": str(exc)})

        if self.path.startswith("/api/v1/add"):
            monitor_id = next_id
            next_id += 1
            interfaces = as_interfaces(body.get("interface"))
            monitors[monitor_id] = {
                "interface": interfaces,
                "provider_kind": body.get("provider_kind"),
                "instance_name": body.get("instance_name"),
                "inner_ip": body.get("inner_ip"),
            }
            return self._send(200, {"id": monitor_id, "interface": interfaces})

        if self.path.startswith("/api/v1/update"):
            monitor_id = int(body.get("id") or 0)
            if monitor_id <= 0:
                return self._send(400, {"error": "invalid id"})
            interfaces = as_interfaces(body.get("new_interface", body.get("interface")))
            monitors[monitor_id] = {
                "interface": interfaces,
                "provider_kind": body.get("provider_kind"),
                "instance_name": body.get("instance_name"),
                "inner_ip": body.get("inner_ip"),
            }
            return self._send(200, {"id": monitor_id, "interface": interfaces})

        if self.path.startswith("/api/v1/delete"):
            monitor_id = int(body.get("id") or 0)
            monitors.pop(monitor_id, None)
            return self._send(200, {"id": monitor_id, "deleted": True})

        if self.path.startswith("/api/v1/info"):
            monitor_id = int(body.get("id") or 0)
            human = "0 B"
            return self._send(200, {
                "id": monitor_id,
                "interface": monitors.get(monitor_id, {}).get("interface", []),
                "used_traffic": 0,
                "used_traffic_in": 0,
                "used_traffic_out": 0,
                "used_traffic_human": human,
                "last_update_time": int(time.time()),
            })

        if self.path.startswith("/api/v1/resources"):
            return self._send(200, {"id": int(body.get("id") or 0), "data": []})

        if self.path.startswith("/api/v1/cleanup"):
            return self._send(200, {"deleted": 0, "max_update_seconds": 0})

        return self._send(404, {"error": "not found"})

httpd = ThreadingHTTPServer(("0.0.0.0", PORT), Handler)
httpd.serve_forever()
PY
fi

trap 'exit 0' INT TERM
while :; do
  sleep 3600 &
  wait "$!" || exit 0
done
EOF
        sed_inplace "s/__STUB_AGENT_VERSION__/${version}/g" "$binary"
        chmod +x "$binary"
        tar -czf "$archive" -C "$tmp" "oneclickvirt-agent-linux-${arch}" || {
            rm -rf "$tmp"
            return 1
        }
        rm -rf "$tmp"
    done
    log_success "CI stub agent assets ready"
}

deploy_master_local() {
    local _port="${1:-8888}"
    [[ "$DB_NAME" =~ ^[A-Za-z0-9_]+$ ]] || { log_error "Invalid DB_NAME '${DB_NAME}'"; return 1; }
    # Use BASH_SOURCE[0] to get the directory of THIS file (node_manager.sh) regardless of
    # how SCRIPT_DIR is set in the calling script.
    local _this_dir; _this_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    # Server sources live two levels up: common/ -> action_tests/ -> repo_root/server/
    local source_server_dir; source_server_dir="$(cd "${_this_dir}/../../server" && pwd)"
    local server_dir="$source_server_dir"
    if [[ -n "${ACTION_TEST_SERVER_WORKDIR:-}" ]]; then
        log_info "Preparing isolated server workdir: ${ACTION_TEST_SERVER_WORKDIR}"
        rm -rf "${ACTION_TEST_SERVER_WORKDIR}"
        mkdir -p "$(dirname "${ACTION_TEST_SERVER_WORKDIR}")"
        if command -v rsync >/dev/null 2>&1; then
            rsync -a --delete \
                --exclude '.git' \
                --exclude '/logs' \
                --exclude '/storage' \
                --exclude '/tmp' \
                --exclude '/agent/target' \
                --exclude '/oneclickvirt-server*' \
                "${source_server_dir}/" "${ACTION_TEST_SERVER_WORKDIR}/"
        else
            cp -R "${source_server_dir}" "${ACTION_TEST_SERVER_WORKDIR}"
        fi
        server_dir="$(cd "${ACTION_TEST_SERVER_WORKDIR}" && pwd)"
    fi
    MASTER_SERVER_DIR="$server_dir"
    export MASTER_SERVER_DIR

    log_section "Deploying master locally on runner from source (port ${_port})"
    log_info "Server directory: ${server_dir}"

    # Patch config.yaml for CI: bypass captcha + notification checks, fix quoted bool/int types
    log_info "Patching config.yaml for CI environment..."
    local cfg="${server_dir}/config.yaml"
    # Set env=development to bypass captcha, email/telegram/qq sends in development mode
    sed_inplace 's/^\( \{4\}env:\) .*/\1 development/' "$cfg"
    sed_inplace "s/^\( \{4\}addr:\) [0-9][0-9]*/\1 ${_port}/" "$cfg"
    # Fix quoted booleans → unquoted (match any quoted true/false value)
    sed_inplace 's/^\( \{4\}auto-create:\) "\(true\|false\)"/\1 \2/' "$cfg"
    sed_inplace 's/^\( \{4\}log-zap:\) "\(true\|false\)"/\1 \2/' "$cfg"
    sed_inplace 's/^\( \{4\}singular:\) "\(true\|false\)"/\1 \2/' "$cfg"
    # Fix quoted integers → unquoted (match any quoted numeric value)
    sed_inplace 's/^\( \{4\}max-idle-conns:\) "[0-9]*"/\1 10/' "$cfg"
    sed_inplace 's/^\( \{4\}max-lifetime:\) "[0-9]*"/\1 3600/' "$cfg"
    sed_inplace 's/^\( \{4\}max-open-conns:\) "[0-9]*"/\1 100/' "$cfg"
    sed_inplace 's/^\( \{4\}email-smtp-port:\) "[0-9]*"/\1 587/' "$cfg"
    # Fix quoted integer map keys (e.g. level-limits: "1": → 1:)
    sed_inplace 's/^\( *\)"\([0-9]\+\)":/\1\2:/' "$cfg"
    # Keep config.yaml aligned with the CI-created MySQL TCP credential.
    local db_password="${DB_PASSWORD:-${MYSQL_ROOT_PASSWORD:-}}"
    local db_password_escaped
    db_password_escaped=$(printf '%s' "$db_password" | sed 's/[\/&]/\\&/g')
    sed_inplace "/^mysql:/,/^[^[:space:]]/s|^\(    password:\).*|\1 \"${db_password_escaped}\"|" "$cfg"
    sed_inplace "/^mysql:/,/^[^[:space:]]/s|^\(    db-name:\).*|\1 ${DB_NAME}|" "$cfg"
    # Disable captcha (real repo default may be true; env=development bypasses checks but
    # setting it to false avoids any reload warnings in the log)
    sed_inplace 's/^\( \{4\}enabled:\) true/\1 false/' "$cfg"
    log_success "config.yaml patched"

    # Build and start the server binary in background so that:
    #   - config.yaml is found in the working directory (no binary path issues)
    #   - storage/ and logs/ are created relative to server_dir
    #   - killing the PID actually kills the server (no go run wrapper)
    rm -f "$SERVER_PID_FILE" "$SERVER_LOG_FILE"
    
    # Build server binary first, then run it (avoids orphan child process from go run)
    cd "$server_dir" || return 1
    ensure_ci_agent_assets "$server_dir" || {
        log_error "Failed to prepare CI agent assets"
        cd - >/dev/null || true
        return 1
    }
    log_info "Building server binary..."
    if ! go build -o "$SERVER_BINARY" . 2>"$SERVER_BUILD_LOG"; then
        log_error "Server build failed:"
        cat "$SERVER_BUILD_LOG" >&2 || true
        cd - >/dev/null || true
        return 1
    fi
    if [[ ! -x "$SERVER_BINARY" ]]; then
        log_error "Server binary missing or not executable after build"
        cd - >/dev/null || true
        return 1
    fi
    log_success "Server binary built"
    
    GIN_MODE=debug nohup "$SERVER_BINARY" > "$SERVER_LOG_FILE" 2>&1 &
    local pid=$!
    echo "$pid" > "$SERVER_PID_FILE"
    cd - >/dev/null || true
    
    log_info "Server process started (PID ${pid}), waiting for startup..."
    
    # Binary start is faster than go run; wait up to 60s for HTTP
    local i elapsed=0 max_wait=60
    for i in $(seq 1 12); do  # 12 * 5 = 60s
        sleep 5
        elapsed=$((i * 5))
        
        # Check if process is still alive
        if ! kill -0 "$pid" 2>/dev/null; then
            log_error "Server process died during startup (PID ${pid})"
            log_error "=== Last 50 lines of server log ==="
            tail -50 "$SERVER_LOG_FILE" >&2 || true
            return 1
        fi
        
        # Check if HTTP endpoint is responding (accept 200 or 503 - 503 means server is up but DB not ready)
        local status_code
        status_code=$(curl -s -o /dev/null -w "%{http_code}" --max-time 3 "http://localhost:${_port}/health" 2>/dev/null) || true
        if [[ "$status_code" == "200" || "$status_code" == "503" ]]; then
            log_success "Server started and responding (PID ${pid}, elapsed ${elapsed}s, HTTP ${status_code})"
            return 0
        fi
        
        [[ $((elapsed % 15)) -eq 0 ]] && log_debug "Server still starting (${elapsed}/${max_wait}s, HTTP ${status_code:-no response})..."
    done
    
    log_error "Server startup timeout after ${max_wait}s (PID ${pid})"
    log_error "=== Last 50 lines of server log ==="
    tail -50 "$SERVER_LOG_FILE" >&2 || true
    return 1
}

cleanup_all_nodes() {
    local ids="$1"
    platform_cleanup_all "$ids"
}

# reset_master_server: stop the current server, wipe the DB, restart, and re-initialise.
# Call between execution-rule iterations when EXECUTION_RULE=all.
# Depends on: MASTER_SERVER_DIR, ADMIN_USER, ADMIN_PASS, MASTER_PORT  (all exported by run_env_test.sh)
# and the helper functions init_system / admin_login / wait_init_ready / wait_db_ready
# from test_framework.sh (already sourced before this file).
reset_master_server() {
    local port="${1:-${MASTER_PORT:-8888}}"
    [[ "$DB_NAME" =~ ^[A-Za-z0-9_]+$ ]] || { log_error "Invalid DB_NAME '${DB_NAME}'"; return 1; }
    log_section "Resetting master server for execution-rule switch (port ${port})"

    # 1. Kill existing server process
    if [[ -f "$SERVER_PID_FILE" ]]; then
        local old_pid; old_pid=$(cat "$SERVER_PID_FILE" 2>/dev/null || true)
        kill "${old_pid}" 2>/dev/null || true
        rm -f "$SERVER_PID_FILE"
    fi
    sleep 2

    # 2. Reset MySQL database
    log_info "Resetting database (drop + recreate ${DB_NAME})..."
    if mysql_root_exec \
        -e "DROP DATABASE IF EXISTS \`${DB_NAME}\`; CREATE DATABASE \`${DB_NAME}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" 2>/dev/null; then
        log_success "Database reset successful"
    else
        log_error "Database reset failed"
        return 1
    fi

    # 3. Restart server binary from the already-compiled binary
    if [[ -z "${MASTER_SERVER_DIR:-}" || ! -d "${MASTER_SERVER_DIR}" ]]; then
        log_error "MASTER_SERVER_DIR ('${MASTER_SERVER_DIR:-}') not set or missing; cannot restart"
        return 1
    fi
    cd "${MASTER_SERVER_DIR}" || return 1
    GIN_MODE=debug nohup "$SERVER_BINARY" >> "$SERVER_LOG_FILE" 2>&1 &
    local pid=$!
    echo "${pid}" > "$SERVER_PID_FILE"
    cd - >/dev/null || true
    log_info "Server restarted (PID ${pid})"

    # 4. Wait for HTTP endpoint
    local i
    for i in $(seq 1 12); do
        sleep 5
        local sc
        sc=$(curl -s -o /dev/null -w "%{http_code}" --max-time 3 "http://localhost:${port}/health" 2>/dev/null) || true
        if [[ "${sc}" == "200" || "${sc}" == "503" ]]; then
            log_success "Server responding after reset (HTTP ${sc})"
            break
        fi
        [[ ${i} -eq 12 ]] && { log_error "Server restart timeout (60 s)"; return 1; }
    done

    # 5. Wait for init endpoint
    if ! wait_init_ready "http://localhost:${port}" 120 5; then
        log_error "Init endpoint not ready after reset"
        return 1
    fi

    # 6. Re-initialise system
    local init_check; init_check=$(curl -s --max-time 10 "http://localhost:${port}/api/v1/public/init/check" 2>/dev/null)
    local need_init; need_init=$(echo "${init_check}" | jq -r '.data.needInit // true' 2>/dev/null)
    if [[ "${need_init}" == "true" ]]; then
        local init_resp; init_resp=$(init_system "http://localhost:${port}" "${ADMIN_USER}" "${ADMIN_PASS}")
        local init_code; init_code=$(echo "${init_resp}" | jq -r '.code // empty' 2>/dev/null)
        if [[ "${init_code}" != "200" ]]; then
            log_error "System re-initialisation failed (code=${init_code}): ${init_resp}"
            return 1
        fi
        log_success "System re-initialised"
        wait_db_ready "http://localhost:${port}" 120 3
    fi

    # 7. Re-login and refresh ADMIN_TOKEN
    ADMIN_TOKEN=$(admin_login "http://localhost:${port}" "${ADMIN_USER}" "${ADMIN_PASS}")
    if [[ -z "${ADMIN_TOKEN}" ]]; then
        log_error "Admin re-login failed after reset"
        return 1
    fi
    export ADMIN_TOKEN
    log_success "Master server reset complete; ADMIN_TOKEN refreshed"
}
