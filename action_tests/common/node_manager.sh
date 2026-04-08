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
)
PVE_BUILD_BACKEND="https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/build_backend.sh"
PVE_BUILD_NAT="https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/build_nat_network.sh"

create_test_node() {
    local env_type="$1" hours="${2:-8}"
    log_info "Creating test node: env=${env_type} hours=${hours}"
    # Use platform abstraction with auto-fallback
    local result
    result=$(try_create_with_fallback "$env_type" "$hours")
    if [[ $? -ne 0 || -z "$result" ]]; then
        log_error "All platforms failed to create a test node"
        return 1
    fi
    local id ip password platform_name
    id=$(echo "${result}" | jq -r '.instance_id // empty' 2>/dev/null)
    ip=$(echo "${result}" | jq -r '.ipv4 // empty' 2>/dev/null)
    password=$(echo "${result}" | jq -r '.password // empty' 2>/dev/null)
    platform_name=$(echo "${result}" | jq -r '.platform // empty' 2>/dev/null)
    [[ -z "${ip}" ]] && { log_error "Cannot get IP from create response: ${result}"; return 1; }
    # Update global SSH password if provided
    [[ -n "$password" ]] && PLATFORM_SSH_PASSWORD="$password"
    # Wait for SSH to be available before handing off the node
    wait_for_ssh "${ip}" 300 || { log_error "SSH never became available on ${ip}"; return 1; }
    log_success "Node created on '${platform_name}': ID=${id} IP=${ip}"
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\",\"platform\":\"${platform_name}\"}"
}

install_env() {
    local id="$1" ip="$2" env="$3"
    log_section "Installing ${env} environment on ${ip}"
    # Wait for cloud-init and other processes to release apt/dpkg locks
    # min_wait=120s (required wait), max_wait=300s (timeout), interval=10s
    wait_for_apt_lock "${ip}" 120 300 10
    platform_exec_and_wait "${ip}" "export DEBIAN_FRONTEND=noninteractive && apt-get update -y && apt-get install -y curl wget sudo jq ipcalc lsof" 600
    local url="${ENV_INSTALL_SCRIPTS[$env]:-}"
    [[ -z "$url" ]] && { log_error "Unknown environment: ${env}"; return 1; }
    # Build non-interactive env var prefix per script type
    local env_prefix
    case "$env" in
        docker)
            env_prefix="NEED_DISK_LIMIT=n CN=false WITHOUTCDN=false IPV6_MAXIMUM_SUBSET=n"
            ;;
        lxd)
            env_prefix="NONINTERACTIVE=true CN=false WITHOUTCDN=false"
            ;;
        incus)
            env_prefix="INCUS_NONINTERACTIVE=true WITHOUTCDN=false"
            ;;
        podman)
            env_prefix="NEED_DISK_LIMIT=n WITHOUTCDN=false"
            ;;
        containerd)
            env_prefix="NEED_DISK_LIMIT=n WITHOUTCDN=false"
            ;;
        *)
            env_prefix="DEBIAN_FRONTEND=noninteractive"
            ;;
    esac
    if [[ "$env" == "proxmoxve" ]]; then
        log_info "PVE install step 1/3: installing PVE kernel (reboot required)..."
        platform_exec_and_wait "${ip}" "curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && bash /tmp/envinstall.sh" 1200 || true
        log_info "Rebooting worker to load PVE kernel..."
        platform_exec_and_wait "${ip}" "reboot" 10 || true
        sleep 25
        wait_for_ssh "${ip}" 300
        log_info "PVE install step 2/3: completing PVE configuration after reboot..."
        platform_exec_and_wait "${ip}" "curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && bash /tmp/envinstall.sh" 1200
        log_info "PVE install step 3a/3: configuring backend bridge..."
        platform_exec_and_wait "${ip}" "curl -sSL '${PVE_BUILD_BACKEND}' | bash" 600
        log_info "PVE install step 3b/3: building NAT IPv4 network..."
        platform_exec_and_wait "${ip}" "curl -sSL '${PVE_BUILD_NAT}' | bash" 600
    else
        platform_exec_and_wait "${ip}" "curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh" 1200 || true
        log_info "Rebooting worker to apply network/kernel settings..."
        platform_exec_and_wait "${ip}" "reboot" 10 || true
        log_info "Waiting for SSH after reboot (max 180s)..."
        wait_for_ssh "${ip}" 180
        log_info "Re-running ${env} install to complete post-reboot setup..."
        platform_exec_and_wait "${ip}" "curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh" 1200
    fi
}

# Pre-populate worker with dummy containers for discovery/import testing
prepare_dirty_node() {
    local id="$1" ip="$2" env="$3"
    log_section "Preparing non-clean worker node for discovery tests (${ip})"
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
    esac
}

deploy_master() {
    local id="$1" ip="$2" port="${3:-80}"
    log_section "Deploying master on ${ip} (port ${port})"
    platform_exec_and_wait "${ip}" "curl -sSL https://raw.githubusercontent.com/oneclickvirt/docker/main/scripts/dockerinstall.sh | bash" 600
    platform_exec_and_wait "${ip}" "docker pull spiritlhl/oneclickvirt:latest && docker run -d --name oneclickvirt --restart=always -p ${port}:80 spiritlhl/oneclickvirt:latest" 300
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
    # Disable captcha (real repo default may be true; env=development bypasses checks but
    # setting it to false avoids any reload warnings in the log)
    sed -i 's/^\( \{4\}enabled:\) true/\1 false/' "$cfg"
    log_success "config.yaml patched"

    # Start the server in background via `go run .` from server_dir so that:
    #   - config.yaml is found in the working directory (no binary path issues)
    #   - storage/ and logs/ are created relative to server_dir
    rm -f /tmp/oneclickvirt-server.pid /tmp/oneclickvirt-server.log
    
    # Start server and capture PID correctly (avoid subshell issues)
    cd "$server_dir" || return 1
    GIN_MODE=debug nohup go run . > /tmp/oneclickvirt-server.log 2>&1 &
    local pid=$!
    echo "$pid" > /tmp/oneclickvirt-server.pid
    cd - >/dev/null || true
    
    log_info "Server process started (PID ${pid}), waiting for compilation and startup..."
    
    # go run takes longer to start (compilation happens at runtime); wait up to 120s for process + HTTP
    local i elapsed=0 max_wait=120
    for i in $(seq 1 24); do  # 24 * 5 = 120s
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
