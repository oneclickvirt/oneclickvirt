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

get_os_id() {
    local name="$1"
    local r; r=$(alice_get_os_list)
    local body; body=$(alice_parse_body "$r")
    local http_code; http_code=$(alice_parse_code "$r")
    log_debug "GET /evo/os HTTP ${http_code}: $(echo "$body" | head -c 500)"
    local id; id=$(echo "$body" | jq -r "[.data[]? | select(.name | test(\"${name}\";\"i\"))][0].id // empty" 2>/dev/null)
    if [[ -z "$id" ]]; then
        log_error "Cannot find OS matching '${name}' in /evo/os"
        log_error "Available OS list: $(echo "$body" | jq -r '[.data[]?.name] | join(", ")' 2>/dev/null || echo "$body")"
        return 1
    fi
    echo "$id"
}

create_test_node() {
    local env_type="$1" hours="${2:-8}"
    # Validate prerequisites
    if [[ -z "${ALICE_CLIENT_ID:-}" ]]; then
        log_error "ALICE_CLIENT_ID is not set - cannot create test nodes"
        return 1
    fi
    if [[ -z "${ALICE_CLIENT_SECRET:-}" ]]; then
        log_error "ALICE_CLIENT_SECRET is not set - cannot create test nodes"
        return 1
    fi
    if [[ -z "${ALICE_API_BASE:-}" ]]; then
        log_error "ALICE_API_BASE is not set - cannot create test nodes"
        return 1
    fi
    log_info "Creating test node: env=${env_type} hours=${hours}"
    log_debug "API base: ${ALICE_API_BASE}"
    local pkg; pkg=$(get_min_package_id)
    if [[ -z "$pkg" ]]; then
        log_error "Cannot get package ID from AliceInit API"
        log_error "Check ALICE_CLIENT_ID, ALICE_CLIENT_SECRET and ALICE_API_BASE settings"
        local profile_resp; profile_resp=$(alice_get_profile 2>/dev/null) || true
        log_error "Profile check response: $(alice_parse_body "$profile_resp" 2>/dev/null)"
        return 1
    fi
    log_debug "Package ID: ${pkg}"
    local os_name="debian"
    [[ "$env_type" == "lxd" ]] && os_name="ubuntu"
    local os_id; os_id=$(get_os_id "$os_name")
    [[ -z "$os_id" ]] && { log_error "Cannot get ${os_name} OS ID"; return 1; }
    local r; r=$(alice_create_and_wait "$pkg" "$os_id" "$hours")
    [[ $? -ne 0 ]] && return 1
    local id; id=$(echo "$r" | jq -r '.data.id // empty')
    local ip; ip=$(echo "$r" | jq -r '.data.ipv4 // .data.ip // empty')
    local pw; pw=$(echo "$r" | jq -r '.data.password // empty')
    log_success "Node created: ID=${id} IP=${ip}"
    echo "{\"instance_id\":\"${id}\",\"ipv4\":\"${ip}\",\"password\":\"${pw}\"}"
}

install_env() {
    local id="$1" env="$2"
    log_section "Installing ${env} environment"
    alice_exec_and_wait "$id" "export DEBIAN_FRONTEND=noninteractive && apt-get update -y && apt-get install -y curl wget sudo jq" 600
    local url="${ENV_INSTALL_SCRIPTS[$env]:-}"
    [[ -z "$url" ]] && { log_error "Unknown environment: ${env}"; return 1; }
    alice_exec_and_wait "$id" "curl -sSL ${url} | bash" 1200
    if [[ "$env" == "proxmoxve" ]]; then
        alice_exec_and_wait "$id" "curl -sSL ${PVE_BUILD_BACKEND} | bash" 600
        alice_exec_and_wait "$id" "curl -sSL ${PVE_BUILD_NAT} | bash" 600
    fi
}

# Pre-populate worker with dummy containers for discovery/import testing
prepare_dirty_node() {
    local id="$1" env="$2"
    log_section "Preparing non-clean worker node for discovery tests"
    case "$env" in
        docker)
            alice_exec_and_wait "$id" "docker run -d --name pre_existing_1 alpine sleep 3600" 120
            alice_exec_and_wait "$id" "docker run -d --name pre_existing_2 debian:12 sleep 3600" 120
            ;;
        podman)
            alice_exec_and_wait "$id" "podman run -d --name pre_existing_1 docker.io/library/alpine sleep 3600" 120
            ;;
        containerd)
            alice_exec_and_wait "$id" "ctr images pull docker.io/library/alpine:latest && ctr run -d docker.io/library/alpine:latest pre_existing_1 sleep 3600" 120
            ;;
        lxd)
            alice_exec_and_wait "$id" "lxc launch images:debian/12 pre-existing-1" 120
            ;;
        incus)
            alice_exec_and_wait "$id" "incus launch images:debian/12 pre-existing-1" 120
            ;;
        proxmoxve)
            log_info "Proxmox pre-population skipped (requires manual template)"
            ;;
    esac
}

deploy_master() {
    local id="$1" port="${2:-80}"
    log_section "Deploying master (port ${port})"
    alice_exec_and_wait "$id" "curl -sSL https://raw.githubusercontent.com/oneclickvirt/docker/main/scripts/dockerinstall.sh | bash" 600
    alice_exec_and_wait "$id" "docker pull spiritlhl/oneclickvirt:latest && docker run -d --name oneclickvirt --restart=always -p ${port}:80 spiritlhl/oneclickvirt:latest" 300
}

cleanup_all_nodes() {
    local ids="$1"
    IFS=',' read -ra arr <<< "$ids"
    for id in "${arr[@]}"; do
        [[ -n "$id" ]] && alice_delete_and_confirm "$id" || true
    done
}
