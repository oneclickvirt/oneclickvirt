#!/bin/bash
# Node Manager - AliceInit node creation, environment installation, master/worker deployment
# Two-node architecture: master node runs OneClickVirt, worker node runs virtualization environment

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/aliceinit_api.sh"

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

get_min_package_id() {
    local r; r=$(alice_get_permissions)
    local body; body=$(alice_parse_body "$r")
    local http_code; http_code=$(alice_parse_code "$r")
    log_debug "GET /evo/permissions HTTP ${http_code}: ${body}"
    # allow_packages is a pipe-separated string like "38|39|40|41|42"
    local allow; allow=$(echo "$body" | jq -r '.data.allow_packages // empty' 2>/dev/null)
    if [[ -z "$allow" ]]; then
        log_error "GET /evo/permissions response has no allow_packages field"
        log_error "Full response: ${body}"
        return 1
    fi
    # Take the first package ID from the pipe-separated list
    echo "${allow%%|*}"
}

get_os_id_for_plan() {
    local plan_id="$1" name="${2:-debian}"
    local r; r=$(alice_get_plan_os "${plan_id}")
    local body; body=$(alice_parse_body "$r")
    local http_code; http_code=$(alice_parse_code "$r")
    log_debug "GET /evo/plans/${plan_id}/os-images HTTP ${http_code}: ${body}"
    # Flatten all os_list arrays and find the first entry whose name matches
    local id; id=$(echo "$body" | jq -r "[.data[].os_list[] | select(.name | test(\"${name}\";\"i\"))][0].id // empty" 2>/dev/null)
    if [[ -z "$id" ]]; then
        log_error "Cannot find OS matching '${name}' in /evo/plans/${plan_id}/os-images"
        log_error "Available: $(echo "$body" | jq -r '[.data[].os_list[].name] | join(", ")' 2>/dev/null || echo "$body")"
        return 1
    fi
    echo "$id"
}

get_ssh_key_id() {
    if [[ -z "${ALICE_PUBLIC_KEY:-}" ]]; then
        log_error "ALICE_PUBLIC_KEY is not set - cannot find SSH key ID"
        return 1
    fi
    local r; r=$(alice_get_ssh_keys)
    local body; body=$(alice_parse_body "$r")
    local http_code; http_code=$(alice_parse_code "$r")
    log_debug "GET /account/ssh-keys HTTP ${http_code}: ${body}"
    # Match key by type and base64 body; the stored publickey may have trailing \n
    local key_type key_body
    key_type=$(echo "${ALICE_PUBLIC_KEY}" | awk '{print $1}')
    key_body=$(echo "${ALICE_PUBLIC_KEY}" | awk '{print $2}')
    local id; id=$(echo "$body" | jq -r --arg kt "$key_type" --arg kb "$key_body" \
        '.data[] | select((.publickey | rtrimstr("\n") | split(" ")) as $p | ($p[0] == $kt and $p[1] == $kb)) | .id' \
        2>/dev/null | head -1)
    if [[ -z "$id" ]]; then
        # Fallback: use the first available key
        id=$(echo "$body" | jq -r '.data[0].id // empty' 2>/dev/null)
        if [[ -n "$id" ]]; then
            log_warning "Could not match ALICE_PUBLIC_KEY exactly; using first key ID: ${id}"
        else
            log_error "No SSH keys found in /account/ssh-keys"
            return 1
        fi
    fi
    echo "$id"
}

create_test_node() {
    local env_type="$1" hours="${2:-8}"
    if [[ -z "${ALICE_CLIENT_ID:-}" ]]; then
        log_error "ALICE_CLIENT_ID is not set - cannot create test nodes"
        return 1
    fi
    if [[ -z "${ALICE_CLIENT_SECRET:-}" ]]; then
        log_error "ALICE_CLIENT_SECRET is not set - cannot create test nodes"
        return 1
    fi
    log_info "Creating test node: env=${env_type} hours=${hours}"
    log_debug "API base: ${ALICE_API_BASE}"
    # Resolve minimum allowed plan ID
    local pkg; pkg=$(get_min_package_id)
    if [[ -z "${pkg}" ]]; then
        log_error "Cannot get package ID from AliceInit API"
        local profile_resp; profile_resp=$(alice_get_profile 2>/dev/null) || true
        log_error "Profile check response: $(alice_parse_body "${profile_resp}" 2>/dev/null)"
        return 1
    fi
    log_debug "Package ID: ${pkg}"
    # Choose OS by environment type
    local os_name="debian"
    [[ "${env_type}" == "lxd" ]] && os_name="ubuntu"
    local os_id; os_id=$(get_os_id_for_plan "${pkg}" "${os_name}")
    [[ -z "${os_id}" ]] && { log_error "Cannot get ${os_name} OS ID for plan ${pkg}"; return 1; }
    log_debug "OS ID: ${os_id}"
    # Resolve SSH key ID from ALICE_PUBLIC_KEY
    local ssh_key_id
    ssh_key_id=$(get_ssh_key_id) || ssh_key_id=""
    if [[ -z "${ssh_key_id}" ]]; then
        log_warning "No SSH key ID resolved; instance will use password auth only"
    else
        log_debug "SSH key ID: ${ssh_key_id}"
    fi
    local inst; inst=$(alice_create_and_wait "${pkg}" "${os_id}" "${hours}" "${ssh_key_id}" "" 600)
    [[ $? -ne 0 ]] && return 1
    local id; id=$(echo "${inst}" | jq -r '.id // empty' 2>/dev/null)
    local ip; ip=$(echo "${inst}" | jq -r '.ipv4 // .ip // empty' 2>/dev/null)
    local password; password=$(echo "${inst}" | jq -r '.password // empty' 2>/dev/null)
    [[ -z "${ip}" ]] && { log_error "Cannot get IP from create response: ${inst}"; return 1; }
    # Wait for SSH to be available before handing off the node
    wait_for_ssh "${ip}" 300 || { log_error "SSH never became available on ${ip}"; return 1; }
    log_success "Node created: ID=${id} IP=${ip}"
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${password}\"}"
}

install_env() {
    local id="$1" ip="$2" env="$3"
    log_section "Installing ${env} environment on ${ip}"
    # Wait for cloud-init and other processes to release apt/dpkg locks
    # min_wait=120s (required wait), max_wait=300s (timeout), interval=10s
    wait_for_apt_lock "${ip}" 120 300 10
    alice_exec_and_wait "${ip}" "export DEBIAN_FRONTEND=noninteractive && apt-get update -y && apt-get install -y curl wget sudo jq ipcalc lsof" 600
    local url="${ENV_INSTALL_SCRIPTS[$env]:-}"
    [[ -z "$url" ]] && { log_error "Unknown environment: ${env}"; return 1; }
    # Build non-interactive env var prefix per script type
    local env_prefix
    case "$env" in
        docker)
            # NEED_DISK_LIMIT=n: skip btrfs prompt; CN=false: skip China mirror; IPV6_MAXIMUM_SUBSET=n: skip IPv6 max subnet prompt
            env_prefix="NEED_DISK_LIMIT=n CN=false WITHOUTCDN=false IPV6_MAXIMUM_SUBSET=n"
            ;;
        lxd)
            # NONINTERACTIVE=true: skip all prompts; CN=false: skip China mirror
            env_prefix="NONINTERACTIVE=true CN=false WITHOUTCDN=false"
            ;;
        incus)
            # INCUS_NONINTERACTIVE=true: skip all prompts
            env_prefix="INCUS_NONINTERACTIVE=true WITHOUTCDN=false"
            ;;
        podman)
            # NEED_DISK_LIMIT=n: skip btrfs prompt
            env_prefix="NEED_DISK_LIMIT=n WITHOUTCDN=false"
            ;;
        containerd)
            # NEED_DISK_LIMIT=n: skip btrfs prompt
            env_prefix="NEED_DISK_LIMIT=n WITHOUTCDN=false"
            ;;
        *)
            env_prefix="DEBIAN_FRONTEND=noninteractive"
            ;;
    esac
    if [[ "$env" == "proxmoxve" ]]; then
        # PVE step 1: installs PVE kernel packages, writes /usr/local/bin/reboot_pve.txt, then exits
        log_info "PVE install step 1/3: installing PVE kernel (reboot required)..."
        alice_exec_and_wait "${ip}" "curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && bash /tmp/envinstall.sh" 1200 || true
        # PVE script instructs: wait at least 20s after reboot before re-executing
        log_info "Rebooting worker to load PVE kernel..."
        alice_exec_and_wait "${ip}" "reboot" 10 || true
        sleep 25
        wait_for_ssh "${ip}" 300
        # PVE step 2: re-run detects /usr/local/bin/reboot_pve.txt and completes PVE configuration
        log_info "PVE install step 2/3: completing PVE configuration after reboot..."
        alice_exec_and_wait "${ip}" "curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && bash /tmp/envinstall.sh" 1200
        # PVE step 3: configure backend bridge, then build NAT IPv4 network
        log_info "PVE install step 3a/3: configuring backend bridge..."
        alice_exec_and_wait "${ip}" "curl -sSL '${PVE_BUILD_BACKEND}' | bash" 600
        log_info "PVE install step 3b/3: building NAT IPv4 network..."
        alice_exec_and_wait "${ip}" "curl -sSL '${PVE_BUILD_NAT}' | bash" 600
    else
        # Step 1: initial install (may request reboot for kernel modules / network changes)
        alice_exec_and_wait "${ip}" "curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh" 1200 || true
        # Step 2: reboot to apply kernel modules / network config changes
        log_info "Rebooting worker to apply network/kernel settings..."
        alice_exec_and_wait "${ip}" "reboot" 10 || true
        log_info "Waiting for SSH after reboot (max 180s)..."
        wait_for_ssh "${ip}" 180
        # Step 3: re-run after reboot for all env types:
        #   docker/podman/containerd: completes network bridge/config setup
        #   lxd/incus: script detects /usr/local/bin/lxd_reboot (written on first run) and finishes
        log_info "Re-running ${env} install to complete post-reboot setup..."
        alice_exec_and_wait "${ip}" "curl -sSL '${url}' -o /tmp/envinstall.sh && chmod +x /tmp/envinstall.sh && ${env_prefix} bash /tmp/envinstall.sh" 1200
    fi
}

# Pre-populate worker with dummy containers for discovery/import testing
prepare_dirty_node() {
    local id="$1" ip="$2" env="$3"
    log_section "Preparing non-clean worker node for discovery tests (${ip})"
    case "$env" in
        docker)
            alice_exec_and_wait "${ip}" "docker run -d --name pre_existing_1 alpine sleep 3600" 120
            alice_exec_and_wait "${ip}" "docker run -d --name pre_existing_2 debian:12 sleep 3600" 120
            ;;
        podman)
            alice_exec_and_wait "${ip}" "podman run -d --name pre_existing_1 docker.io/library/alpine sleep 3600" 120
            ;;
        containerd)
            alice_exec_and_wait "${ip}" "ctr images pull docker.io/library/alpine:latest && ctr run -d docker.io/library/alpine:latest pre_existing_1 sleep 3600" 120
            ;;
        lxd)
            alice_exec_and_wait "${ip}" "lxc launch images:debian/12 pre-existing-1" 120
            ;;
        incus)
            alice_exec_and_wait "${ip}" "incus launch images:debian/12 pre-existing-1" 120
            ;;
        proxmoxve)
            log_info "Proxmox pre-population skipped (requires manual template)"
            ;;
    esac
}

deploy_master() {
    local id="$1" ip="$2" port="${3:-80}"
    log_section "Deploying master on ${ip} (port ${port})"
    alice_exec_and_wait "${ip}" "curl -sSL https://raw.githubusercontent.com/oneclickvirt/docker/main/scripts/dockerinstall.sh | bash" 600
    alice_exec_and_wait "${ip}" "docker pull spiritlhl/oneclickvirt:latest && docker run -d --name oneclickvirt --restart=always -p ${port}:80 spiritlhl/oneclickvirt:latest" 300
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
    (cd "$server_dir" && GIN_MODE=debug nohup go run . > /tmp/oneclickvirt-server.log 2>&1 &
     echo $! > /tmp/oneclickvirt-server.pid)
    # go run takes longer to start (compilation happens at runtime); wait up to 10s
    local pid i
    for i in $(seq 1 10); do
        sleep 10
        pid=$(cat /tmp/oneclickvirt-server.pid 2>/dev/null)
        [[ -n "$pid" ]] && kill -0 "$pid" 2>/dev/null && break
    done
    if [[ -z "$pid" ]] || ! kill -0 "$pid" 2>/dev/null; then
        log_error "Server process failed to start"
        tail -30 /tmp/oneclickvirt-server.log >&2 || true
        return 1
    fi
    log_success "Server started via go run (PID ${pid})"
}

cleanup_all_nodes() {
    local ids="$1"
    IFS=',' read -ra arr <<< "$ids"
    for id in "${arr[@]}"; do
        [[ -n "$id" ]] && alice_delete_and_confirm "$id" || true
    done
}
