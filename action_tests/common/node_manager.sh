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
    alice_exec_and_wait "${ip}" "export DEBIAN_FRONTEND=noninteractive && apt-get update -y && apt-get install -y curl wget sudo jq" 600
    local url="${ENV_INSTALL_SCRIPTS[$env]:-}"
    [[ -z "$url" ]] && { log_error "Unknown environment: ${env}"; return 1; }
    alice_exec_and_wait "${ip}" "curl -sSL ${url} | bash" 1200
    if [[ "$env" == "proxmoxve" ]]; then
        alice_exec_and_wait "${ip}" "curl -sSL ${PVE_BUILD_BACKEND} | bash" 600
        alice_exec_and_wait "${ip}" "curl -sSL ${PVE_BUILD_NAT} | bash" 600
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

deploy_master_local() {
    local port="${1:-80}"
    log_section "Deploying master locally on runner (port ${port})"
    docker pull spiritlhl/oneclickvirt:latest
    docker run -d --name oneclickvirt --restart=always -p "${port}:80" spiritlhl/oneclickvirt:latest
}

cleanup_all_nodes() {
    local ids="$1"
    IFS=',' read -ra arr <<< "$ids"
    for id in "${arr[@]}"; do
        [[ -n "$id" ]] && alice_delete_and_confirm "$id" || true
    done
}
