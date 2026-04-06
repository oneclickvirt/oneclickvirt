#!/bin/bash
# 节点管理工具 - 通过 AliceInit API 管理测试节点
# 处理节点创建、环境安装、主控部署、清理等操作

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/aliceinit_api.sh"
source "${SCRIPT_DIR}/test_utils.sh"

# ============================================================
# 环境安装脚本 URL（使用组织仓库）
# ============================================================
declare -A ENV_INSTALL_SCRIPTS
ENV_INSTALL_SCRIPTS=(
    [docker]="https://raw.githubusercontent.com/oneclickvirt/docker/main/scripts/dockerinstall.sh"
    [lxd]="https://raw.githubusercontent.com/oneclickvirt/lxd/main/scripts/lxdinstall.sh"
    [incus]="https://raw.githubusercontent.com/oneclickvirt/incus/main/scripts/incus_install.sh"
    [podman]="https://raw.githubusercontent.com/oneclickvirt/podman/main/podmaninstall.sh"
    [containerd]="https://raw.githubusercontent.com/oneclickvirt/containerd/main/containerdinstall.sh"
    [proxmoxve]="https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/install_pve.sh"
)

# Proxmox 额外脚本
PVE_BUILD_BACKEND="https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/build_backend.sh"
PVE_BUILD_NAT="https://raw.githubusercontent.com/oneclickvirt/pve/main/scripts/build_nat_network.sh"

# ============================================================
# 节点创建
# ============================================================

# 获取可用的最低配置套餐 ID
get_min_package_id() {
    log_info "获取可用套餐列表..."
    local response
    response=$(alice_get_packages)
    local body
    body=$(alice_parse_response "$response")
    # 获取第一个可用的套餐 ID
    local package_id
    package_id=$(echo "$body" | jq -r '.data[0].id // empty' 2>/dev/null)
    if [[ -z "$package_id" ]]; then
        # 尝试其他格式
        package_id=$(echo "$body" | jq -r '.data.packages[0].id // empty' 2>/dev/null)
    fi
    echo "$package_id"
}

# 获取 Debian OS ID
get_debian_os_id() {
    log_info "获取 Debian 系统 ID..."
    local response
    response=$(alice_get_os_list)
    local body
    body=$(alice_parse_response "$response")
    local os_id
    os_id=$(echo "$body" | jq -r '[.data[] | select(.name | test("debian"; "i"))][0].id // empty' 2>/dev/null)
    if [[ -z "$os_id" ]]; then
        os_id=$(echo "$body" | jq -r '[.data.os[] | select(.name | test("debian"; "i"))][0].id // empty' 2>/dev/null)
    fi
    echo "$os_id"
}

# 获取 Ubuntu OS ID
get_ubuntu_os_id() {
    log_info "获取 Ubuntu 系统 ID..."
    local response
    response=$(alice_get_os_list)
    local body
    body=$(alice_parse_response "$response")
    local os_id
    os_id=$(echo "$body" | jq -r '[.data[] | select(.name | test("ubuntu"; "i"))][0].id // empty' 2>/dev/null)
    if [[ -z "$os_id" ]]; then
        os_id=$(echo "$body" | jq -r '[.data.os[] | select(.name | test("ubuntu"; "i"))][0].id // empty' 2>/dev/null)
    fi
    echo "$os_id"
}

# 创建测试节点
# 参数: env_type, time_hours
# 返回: instance_id
create_test_node() {
    local env_type="$1"
    local time_hours="${2:-6}"

    local package_id
    package_id=$(get_min_package_id)
    if [[ -z "$package_id" ]]; then
        log_error "无法获取套餐 ID"
        return 1
    fi
    log_info "使用套餐 ID: ${package_id}"

    # LXD 需要 Ubuntu，其他使用 Debian
    local os_id
    if [[ "$env_type" == "lxd" ]]; then
        os_id=$(get_ubuntu_os_id)
        log_info "LXD 环境使用 Ubuntu 系统"
    else
        os_id=$(get_debian_os_id)
        log_info "${env_type} 环境使用 Debian 系统"
    fi

    if [[ -z "$os_id" ]]; then
        log_error "无法获取操作系统 ID"
        return 1
    fi
    log_info "使用操作系统 ID: ${os_id}"

    local response
    response=$(alice_create_and_wait "$package_id" "$os_id" "$time_hours")
    if [[ $? -ne 0 ]]; then
        log_error "创建节点失败"
        echo "$response"
        return 1
    fi

    local instance_id
    instance_id=$(echo "$response" | jq -r '.data.id // empty' 2>/dev/null)
    local ipv4
    ipv4=$(echo "$response" | jq -r '.data.ipv4 // .data.ip // empty' 2>/dev/null)
    local password
    password=$(echo "$response" | jq -r '.data.password // empty' 2>/dev/null)

    log_success "节点创建完成: ID=${instance_id}, IP=${ipv4}"
    echo "{\"instance_id\": \"${instance_id}\", \"ipv4\": \"${ipv4}\", \"password\": \"${password}\"}"
}

# ============================================================
# 虚拟化环境安装
# ============================================================

# 安装虚拟化环境
install_env() {
    local instance_id="$1"
    local env_type="$2"

    log_section "安装 ${env_type} 虚拟化环境"

    # 基础系统更新
    log_info "更新系统软件包..."
    alice_exec_and_wait "$instance_id" "export DEBIAN_FRONTEND=noninteractive && apt-get update -y && apt-get upgrade -y -o Dpkg::Options::='--force-confdef' -o Dpkg::Options::='--force-confold' && apt-get install -y curl wget sudo jq" 600

    case "$env_type" in
        docker)
            install_docker "$instance_id"
            ;;
        lxd)
            install_lxd "$instance_id"
            ;;
        incus)
            install_incus "$instance_id"
            ;;
        podman)
            install_podman "$instance_id"
            ;;
        containerd)
            install_containerd "$instance_id"
            ;;
        proxmoxve)
            install_proxmoxve "$instance_id"
            ;;
        *)
            log_error "未知的虚拟化环境类型: ${env_type}"
            return 1
            ;;
    esac
}

install_docker() {
    local instance_id="$1"
    log_info "安装 Docker (使用 oneclickvirt/docker 脚本)..."
    alice_exec_and_wait "$instance_id" \
        "curl -sSL ${ENV_INSTALL_SCRIPTS[docker]} | bash" 600
}

install_lxd() {
    local instance_id="$1"
    log_info "安装 LXD (使用 oneclickvirt/lxd 脚本)..."
    alice_exec_and_wait "$instance_id" \
        "curl -sSL ${ENV_INSTALL_SCRIPTS[lxd]} | bash" 600
}

install_incus() {
    local instance_id="$1"
    log_info "安装 Incus (使用 oneclickvirt/incus 脚本)..."
    alice_exec_and_wait "$instance_id" \
        "curl -sSL ${ENV_INSTALL_SCRIPTS[incus]} | bash" 600
}

install_podman() {
    local instance_id="$1"
    log_info "安装 Podman (使用 oneclickvirt/podman 脚本)..."
    alice_exec_and_wait "$instance_id" \
        "curl -sSL ${ENV_INSTALL_SCRIPTS[podman]} | bash" 600
}

install_containerd() {
    local instance_id="$1"
    log_info "安装 Containerd (使用 oneclickvirt/containerd 脚本)..."
    alice_exec_and_wait "$instance_id" \
        "curl -sSL ${ENV_INSTALL_SCRIPTS[containerd]} | bash" 600
}

install_proxmoxve() {
    local instance_id="$1"
    log_info "安装 Proxmox VE (使用 oneclickvirt/pve 脚本)..."
    # 分步安装
    alice_exec_and_wait "$instance_id" \
        "curl -sSL ${ENV_INSTALL_SCRIPTS[proxmoxve]} | bash" 1200

    log_info "构建后端..."
    alice_exec_and_wait "$instance_id" \
        "curl -sSL ${PVE_BUILD_BACKEND} | bash" 600

    log_info "构建 NAT 网络..."
    alice_exec_and_wait "$instance_id" \
        "curl -sSL ${PVE_BUILD_NAT} | bash" 600
}

# ============================================================
# 主控部署
# ============================================================

# 在节点上通过 Docker 部署主控
deploy_master() {
    local instance_id="$1"
    local master_port="${2:-8888}"

    log_section "部署主控面板"

    # 确保 Docker 可用（主控通过 Docker 部署）
    log_info "检查 Docker 是否可用于部署主控..."
    alice_exec_and_wait "$instance_id" \
        "command -v docker || (curl -fsSL https://get.docker.com | sh)" 300

    # 拉取并运行主控 Docker 容器
    log_info "拉取并启动主控 Docker 容器..."
    alice_exec_and_wait "$instance_id" \
        "docker pull spiritlhl/oneclickvirt:latest && docker rm -f oneclickvirt-master 2>/dev/null; docker run -d --name oneclickvirt-master --restart=always -p ${master_port}:80 spiritlhl/oneclickvirt:latest" 600

    # 等待主控启动
    log_info "等待主控容器启动..."
    sleep 30

    log_success "主控部署完成"
}

# ============================================================
# 清理
# ============================================================

# 清理所有测试节点
cleanup_all_nodes() {
    local node_ids="$1"  # 逗号分隔的实例 ID 列表

    log_section "清理测试节点"
    IFS=',' read -ra IDS <<< "$node_ids"
    for id in "${IDS[@]}"; do
        id=$(echo "$id" | tr -d ' ')
        if [[ -n "$id" ]]; then
            alice_delete_and_confirm "$id" 120
        fi
    done
    log_success "所有测试节点清理完成"
}
