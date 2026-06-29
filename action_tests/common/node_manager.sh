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
set -e
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
rm -f "\$swap_file"
if command -v fallocate >/dev/null 2>&1; then
    fallocate -l "\${target_mb}M" "\$swap_file" || dd if=/dev/zero of="\$swap_file" bs=1M count="\$target_mb" status=none
else
    dd if=/dev/zero of="\$swap_file" bs=1M count="\$target_mb" status=none
fi
chmod 600 "\$swap_file"
mkswap "\$swap_file" >/dev/null
swapon "\$swap_file"
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
        log_error "All platforms failed to create a test node"
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

install_env() {
    local id="$1" ip="$2" env="$3"
    log_section "Installing ${env} environment on worker node"
    local noninteractive_prefix="export noninteractive=true; export DEBIAN_FRONTEND=noninteractive;"
    local install_wait="${ENV_INSTALL_MAX_WAIT:-3600}"
    local apt_lock_wait="${APT_LOCK_MAX_WAIT:-1800}"
    local apt_install_wait="${APT_INSTALL_MAX_WAIT:-1800}"
    local reboot_wait="${ENV_REBOOT_SSH_MAX_WAIT:-600}"
    local pve_wait="${PVE_INSTALL_MAX_WAIT:-3600}"
    if declare -f platform_validate_worker_resources >/dev/null 2>&1; then
        platform_validate_worker_resources "$env" "$ip" "${ACTIVE_PLATFORM:-}" || return 75
    fi
    ensure_worker_swap "${ip}" "worker before ${env} install" || log_warning "Continuing even though swap setup did not complete before ${env} install"
    # Wait for cloud-init and other processes to release apt/dpkg locks
    # min_wait=120s (required wait), max_wait defaults to 1800s, interval=15s
    wait_for_apt_lock "${ip}" 120 "$apt_lock_wait" 15 || return 75
    platform_exec_and_wait "${ip}" "${noninteractive_prefix} apt-get update -y && apt-get -o Dpkg::Options::=--force-confdef -o Dpkg::Options::=--force-confold install -y curl wget sudo jq ipcalc lsof" "$apt_install_wait" || return 75
    ensure_worker_dns "${ip}" "worker before ${env} install" || true
    local url="${ENV_INSTALL_SCRIPTS[$env]:-}"
    [[ -z "$url" ]] && { log_error "Unknown environment: ${env}"; return 1; }
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
            env_prefix="DEBIAN_FRONTEND=noninteractive noninteractive=true INCUS_NONINTERACTIVE=true WITHOUTCDN=false"
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
    if [[ "$env" == "proxmoxve" ]]; then
        log_info "PVE install step 1/3: installing PVE kernel (reboot required)..."
        platform_exec_and_wait "${ip}" "${noninteractive_prefix} curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && DEBIAN_FRONTEND=noninteractive bash /tmp/envinstall.sh" "$pve_wait" || true
        log_info "Rebooting worker to load PVE kernel..."
        platform_exec_and_wait "${ip}" "reboot" 10 || true
        sleep 25
        wait_for_ssh "${ip}" "$reboot_wait"
        ensure_worker_swap "${ip}" "worker after PVE reboot" || log_warning "Swap setup after PVE reboot did not complete"
        stabilize_worker_network_for_env "${ip}" "${env}" "worker after PVE reboot" || true
        log_info "PVE install step 2/3: completing PVE configuration after reboot..."
        platform_exec_and_wait "${ip}" "${noninteractive_prefix} curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && DEBIAN_FRONTEND=noninteractive bash /tmp/envinstall.sh" "$pve_wait"
        log_info "PVE install step 3a/3: configuring backend bridge..."
        platform_exec_and_wait "${ip}" "${noninteractive_prefix} curl -sSL '${PVE_BUILD_BACKEND}' | bash" "$install_wait"
        log_info "PVE install step 3b/3: building NAT IPv4 network..."
        platform_exec_and_wait "${ip}" "${noninteractive_prefix} curl -sSL '${PVE_BUILD_NAT}' | bash" "$install_wait"
    elif [[ "$env" == "kubevirt" ]]; then
        # kubevirt needs K3s + KubeVirt + CDI, single-pass install (no reboot needed)
        # K3s + KubeVirt + CDI typically takes 60-120 minutes; use 7200s (2h) to be safe
        log_info "Installing KubeVirt environment (K3s + KubeVirt + CDI)..."
        run_kubevirt_installer_with_retry "${ip}" "${noninteractive_prefix} curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh"
    elif [[ "$env" == "qemu" ]]; then
        # qemu needs libvirt + QEMU/KVM, single-pass install
        log_info "Installing QEMU/KVM environment..."
        platform_exec_and_wait "${ip}" "${noninteractive_prefix} curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh" "$install_wait"
    else
        platform_exec_and_wait "${ip}" "${noninteractive_prefix} curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh" "$install_wait" || true
        log_info "Rebooting worker to apply network/kernel settings..."
        platform_exec_and_wait "${ip}" "reboot" 10 || true
        log_info "Waiting for SSH after reboot (max ${reboot_wait}s)..."
        wait_for_ssh "${ip}" "$reboot_wait"
        ensure_worker_swap "${ip}" "worker after ${env} reboot" || log_warning "Swap setup after ${env} reboot did not complete"
        stabilize_worker_network_for_env "${ip}" "${env}" "worker after ${env} reboot" || true
        log_info "Re-running ${env} install to complete post-reboot setup..."
        platform_exec_and_wait "${ip}" "${noninteractive_prefix} curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh" "$install_wait"
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
            verify_cmd="command -v pvesh >/dev/null 2>&1 && command -v pct >/dev/null 2>&1 && command -v qm >/dev/null 2>&1"
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
        log_success "${env} runtime verified on worker"
        [[ "${DEBUG:-0}" == "1" && -n "$verify_output" ]] && printf '%s\n' "$verify_output" >&2
        return 0
    fi
    log_warning "${env} runtime verification failed or timed out"
    [[ -n "$verify_output" ]] && printf '%s\n' "$verify_output" >&2
    return 1
}

# Pre-populate worker with dummy containers for discovery/import testing
prepare_dirty_node() {
    local id="$1" ip="$2" env="$3"
    log_section "Preparing non-clean worker node for discovery tests"
    case "$env" in
        docker)
            platform_exec_and_wait "${ip}" "docker run -d --name pre_existing_1 alpine sleep 3600" 120
            platform_exec_and_wait "${ip}" "docker run -d --name pre_existing_2 debian:12 sleep 3600" 120
            ;;
        podman)
            platform_exec_and_wait "${ip}" "podman run -d --name pre_existing_1 docker.io/library/alpine sleep 3600" 120
            ;;
        containerd)
            platform_exec_and_wait "${ip}" "ctr images pull docker.io/library/alpine:latest && ctr run -d docker.io/library/alpine:latest pre_existing_1 sleep 3600" 120
            ;;
        lxd)
            platform_exec_and_wait "${ip}" "lxc launch images:debian/12 pre-existing-1" 120
            ;;
        incus)
            platform_exec_and_wait "${ip}" "incus launch images:debian/12 pre-existing-1" 120
            ;;
        proxmoxve)
            log_info "Proxmox pre-population skipped (requires manual template)"
            ;;
        qemu)
            log_info "QEMU pre-population skipped (requires VM images)"
            ;;
        kubevirt)
            log_info "KubeVirt pre-population skipped (requires VM manifests)"
            ;;
    esac
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

deploy_master_local() {
    # Port argument kept for API compatibility but the Go server port is fixed at 8888 via config.yaml
    local _port="${1:-8888}"
    # Use BASH_SOURCE[0] to get the directory of THIS file (node_manager.sh) regardless of
    # how SCRIPT_DIR is set in the calling script.
    local _this_dir; _this_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    # Server sources live two levels up: common/ -> action_tests/ -> repo_root/server/
    local server_dir; server_dir="$(cd "${_this_dir}/../../server" && pwd)"
    MASTER_SERVER_DIR="$server_dir"
    export MASTER_SERVER_DIR

    log_section "Deploying master locally on runner from source (port ${_port})"
    log_info "Server directory: ${server_dir}"

    # Patch config.yaml for CI: bypass captcha + notification checks, fix quoted bool/int types
    log_info "Patching config.yaml for CI environment..."
    local cfg="${server_dir}/config.yaml"
    # Set env=development to bypass captcha, email/telegram/qq sends in development mode
    sed -i 's/^\( \{4\}env:\) .*/\1 development/' "$cfg"
    # Fix quoted booleans → unquoted (match any quoted true/false value)
    sed -i 's/^\( \{4\}auto-create:\) "\(true\|false\)"/\1 \2/' "$cfg"
    sed -i 's/^\( \{4\}log-zap:\) "\(true\|false\)"/\1 \2/' "$cfg"
    sed -i 's/^\( \{4\}singular:\) "\(true\|false\)"/\1 \2/' "$cfg"
    # Fix quoted integers → unquoted (match any quoted numeric value)
    sed -i 's/^\( \{4\}max-idle-conns:\) "[0-9]*"/\1 10/' "$cfg"
    sed -i 's/^\( \{4\}max-lifetime:\) "[0-9]*"/\1 3600/' "$cfg"
    sed -i 's/^\( \{4\}max-open-conns:\) "[0-9]*"/\1 100/' "$cfg"
    sed -i 's/^\( \{4\}email-smtp-port:\) "[0-9]*"/\1 587/' "$cfg"
    # Fix quoted integer map keys (e.g. level-limits: "1": → 1:)
    sed -i 's/^\( *\)"\([0-9]\+\)":/\1\2:/' "$cfg"
    # Keep config.yaml aligned with the CI-created MySQL TCP credential.
    local db_password="${DB_PASSWORD:-${MYSQL_ROOT_PASSWORD:-}}"
    local db_password_escaped
    db_password_escaped=$(printf '%s' "$db_password" | sed 's/[\/&]/\\&/g')
    sed -i "/^mysql:/,/^[^[:space:]]/s|^\(    password:\).*|\1 \"${db_password_escaped}\"|" "$cfg"
    # Disable captcha (real repo default may be true; env=development bypasses checks but
    # setting it to false avoids any reload warnings in the log)
    sed -i 's/^\( \{4\}enabled:\) true/\1 false/' "$cfg"
    log_success "config.yaml patched"

    # Build and start the server binary in background so that:
    #   - config.yaml is found in the working directory (no binary path issues)
    #   - storage/ and logs/ are created relative to server_dir
    #   - killing the PID actually kills the server (no go run wrapper)
    rm -f /tmp/oneclickvirt-server.pid /tmp/oneclickvirt-server.log
    
    # Build server binary first, then run it (avoids orphan child process from go run)
    cd "$server_dir" || return 1
    log_info "Building server binary..."
    if ! go build -o /tmp/oneclickvirt-server . 2>/tmp/oneclickvirt-build.log; then
        log_error "Server build failed:"
        cat /tmp/oneclickvirt-build.log >&2 || true
        cd - >/dev/null || true
        return 1
    fi
    if [[ ! -x /tmp/oneclickvirt-server ]]; then
        log_error "Server binary missing or not executable after build"
        cd - >/dev/null || true
        return 1
    fi
    log_success "Server binary built"
    
    GIN_MODE=debug nohup /tmp/oneclickvirt-server > /tmp/oneclickvirt-server.log 2>&1 &
    local pid=$!
    echo "$pid" > /tmp/oneclickvirt-server.pid
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
            tail -50 /tmp/oneclickvirt-server.log >&2 || true
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
    tail -50 /tmp/oneclickvirt-server.log >&2 || true
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
    log_section "Resetting master server for execution-rule switch (port ${port})"

    # 1. Kill existing server process
    if [[ -f /tmp/oneclickvirt-server.pid ]]; then
        local old_pid; old_pid=$(cat /tmp/oneclickvirt-server.pid 2>/dev/null || true)
        kill "${old_pid}" 2>/dev/null || true
        rm -f /tmp/oneclickvirt-server.pid
    fi
    pkill -f '/tmp/oneclickvirt-server' 2>/dev/null || true
    sleep 2

    # 2. Reset MySQL database
    log_info "Resetting database (drop + recreate oneclickvirt)..."
    if mysql_root_exec \
        -e "DROP DATABASE IF EXISTS oneclickvirt; CREATE DATABASE oneclickvirt CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;" 2>/dev/null; then
        log_success "Database reset successful"
    else
        log_error "Database reset failed"
        return 1
    fi

    # 3. Restart server binary from the already-compiled binary in /tmp
    if [[ -z "${MASTER_SERVER_DIR:-}" || ! -d "${MASTER_SERVER_DIR}" ]]; then
        log_error "MASTER_SERVER_DIR ('${MASTER_SERVER_DIR:-}') not set or missing; cannot restart"
        return 1
    fi
    cd "${MASTER_SERVER_DIR}" || return 1
    GIN_MODE=debug nohup /tmp/oneclickvirt-server >> /tmp/oneclickvirt-server.log 2>&1 &
    local pid=$!
    echo "${pid}" > /tmp/oneclickvirt-server.pid
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
