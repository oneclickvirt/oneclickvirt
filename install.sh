#!/bin/bash
# from https://github.com/oneclickvirt/oneclickvirt
# 2025.09.27

VERSION="v20251031-035054"
REPO="oneclickvirt/oneclickvirt"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
cdn_urls="https://cdn0.spiritlhl.top/ http://cdn3.spiritlhl.net/ http://cdn1.spiritlhl.net/ http://cdn2.spiritlhl.net/"
cdn_success_url=""
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

log_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

log_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

log_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

reading() { 
    printf "\033[32m\033[01m%s\033[0m" "$1"
    read "$2"
}

check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "此脚本需要以root身份运行"
        exit 1
    fi
}

detect_arch() {
    local arch=$(uname -m)
    case $arch in
        x86_64|amd64|x64)
            echo "amd64"
            ;;
        aarch64|arm64|armv8|armv8l)
            echo "arm64"
            ;;
        *)
            log_error "不支持的架构: $arch"
            exit 1
            ;;
    esac
}

detect_system() {
    if [ -f /etc/opencloudos-release ]; then
        SYS="opencloudos"
    elif [ -s /etc/os-release ]; then
        SYS="$(grep -i pretty_name /etc/os-release | cut -d \" -f2)"
    elif command -v hostnamectl >/dev/null 2>&1; then
        SYS="$(hostnamectl | grep -i system | cut -d : -f2 | sed 's/^ *//')"
    elif command -v lsb_release >/dev/null 2>&1; then
        SYS="$(lsb_release -sd)"
    elif [ -s /etc/lsb-release ]; then
        SYS="$(grep -i description /etc/lsb-release | cut -d \" -f2)"
    elif [ -s /etc/redhat-release ]; then
        SYS="$(cat /etc/redhat-release)"
    elif [ -s /etc/issue ]; then
        SYS="$(head -n1 /etc/issue | cut -d '\' -f1 | sed '/^[ ]*$/d')"
    else
        SYS="$(uname -s)"
    fi
    
    SYSTEM=""
    sys_lower=$(echo "$SYS" | tr '[:upper:]' '[:lower:]')
    if echo "$sys_lower" | grep -E "debian|astra" >/dev/null 2>&1; then
        SYSTEM="Debian"
        UPDATE_CMD="apt-get update"
        INSTALL_CMD="apt-get -y install"
    elif echo "$sys_lower" | grep -E "ubuntu" >/dev/null 2>&1; then
        SYSTEM="Ubuntu"
        UPDATE_CMD="apt-get update"
        INSTALL_CMD="apt-get -y install"
    elif echo "$sys_lower" | grep -E "centos|red hat|kernel|oracle linux|alma|rocky" >/dev/null 2>&1; then
        SYSTEM="CentOS"
        UPDATE_CMD="yum -y update"
        INSTALL_CMD="yum -y install"
    elif echo "$sys_lower" | grep -E "amazon linux" >/dev/null 2>&1; then
        SYSTEM="AmazonLinux"
        UPDATE_CMD="yum -y update"
        INSTALL_CMD="yum -y install"
    elif echo "$sys_lower" | grep -E "fedora" >/dev/null 2>&1; then
        SYSTEM="Fedora"
        UPDATE_CMD="dnf -y update"
        INSTALL_CMD="dnf -y install"
    elif echo "$sys_lower" | grep -E "arch" >/dev/null 2>&1; then
        SYSTEM="Arch"
        UPDATE_CMD="pacman -Sy"
        INSTALL_CMD="pacman -S --noconfirm"
    elif echo "$sys_lower" | grep -E "freebsd" >/dev/null 2>&1; then
        SYSTEM="FreeBSD"
        UPDATE_CMD="pkg update"
        INSTALL_CMD="pkg install -y"
    elif echo "$sys_lower" | grep -E "alpine" >/dev/null 2>&1; then
        SYSTEM="Alpine"
        UPDATE_CMD="apk update"
        INSTALL_CMD="apk add --no-cache"
    elif echo "$sys_lower" | grep -E "opencloudos" >/dev/null 2>&1; then
        SYSTEM="OpenCloudOS"
        UPDATE_CMD="yum -y update"
        INSTALL_CMD="yum -y install"
    fi
    
    if [ -z "$SYSTEM" ]; then
        log_warning "无法识别系统，尝试常用包管理器..."
        if command -v apt-get >/dev/null 2>&1; then
            SYSTEM="Unknown-Debian"
            UPDATE_CMD="apt-get update"
            INSTALL_CMD="apt-get -y install"
        elif command -v yum >/dev/null 2>&1; then
            SYSTEM="Unknown-RHEL"
            UPDATE_CMD="yum -y update"
            INSTALL_CMD="yum -y install"
        elif command -v dnf >/dev/null 2>&1; then
            SYSTEM="Unknown-Fedora"
            UPDATE_CMD="dnf -y update"
            INSTALL_CMD="dnf -y install"
        elif command -v pacman >/dev/null 2>&1; then
            SYSTEM="Unknown-Arch"
            UPDATE_CMD="pacman -Sy"
            INSTALL_CMD="pacman -S --noconfirm"
        elif command -v apk >/dev/null 2>&1; then
            SYSTEM="Unknown-Alpine"
            UPDATE_CMD="apk update"
            INSTALL_CMD="apk add"
        else
            log_error "无法识别包管理器，退出安装"
            exit 1
        fi
    fi
    
    log_success "检测到系统: $SYSTEM"
}

check_dependencies() {
    local deps=("curl" "tar" "unzip")
    local missing=()
    
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            missing+=("$dep")
        fi
    done
    
    if [ ${#missing[@]} -ne 0 ]; then
        log_warning "缺少必要工具: ${missing[*]}"
        log_info "正在安装缺少的工具..."
        
        # 如果是非交互模式，询问是否更新系统
        if [ "$noninteractive" != "true" ]; then
            log_warning "系统更新可能需要较长时间，并可能导致网络短暂中断"
            reading "是否更新系统包管理器? (y/N): " update_confirm
            case "$update_confirm" in
                [Yy]*)
                    log_info "更新系统包管理器..."
                    if ! ${UPDATE_CMD} 2>/dev/null; then
                        log_warning "系统更新失败，继续安装依赖"
                    fi
                    ;;
                *)
                    log_warning "跳过系统更新，某些包可能安装失败"
                    ;;
            esac
        fi
        
        for dep in "${missing[@]}"; do
            log_info "安装 $dep..."
            if ! ${INSTALL_CMD} "$dep" 2>/dev/null; then
                log_error "安装 $dep 失败"
                exit 1
            fi
        done
        log_success "依赖工具安装完成"
    else
        log_success "所有必要工具已安装"
    fi
}

get_memory_size() {
    if [ -f /proc/meminfo ]; then
        local mem_kb
        mem_kb=$(grep MemTotal /proc/meminfo | awk '{print $2}')
        echo $((mem_kb / 1024)) # Convert to MB
        return 0
    fi
    if command -v free >/dev/null 2>&1; then
        local mem_kb
        mem_kb=$(free -m | awk '/^Mem:/ {print $2}')
        echo "$mem_kb" # Already in MB
        return 0
    fi
    if command -v sysctl >/dev/null 2>&1; then
        local mem_bytes
        mem_bytes=$(sysctl -n hw.memsize 2>/dev/null || sysctl -n hw.physmem 2>/dev/null)
        if [ -n "$mem_bytes" ]; then
            echo $((mem_bytes / 1024 / 1024)) # Convert to MB
            return 0
        fi
    fi
    echo 0
    return 1
}

check_cdn() {
    local o_url="$1"
    local cdn_url
    for cdn_url in $cdn_urls; do
        if curl -4 -sL -k "$cdn_url$o_url" --max-time 6 | grep -q "success" >/dev/null 2>&1; then
            cdn_success_url="$cdn_url"
            return 0
        fi
        sleep 0.5
    done
    cdn_success_url=""
    return 1
}

check_cdn_file() {
    check_cdn "https://raw.githubusercontent.com/spiritLHLS/ecs/main/back/test"
    if [ -n "$cdn_success_url" ]; then
        log_info "CDN 可用，使用 CDN 加速下载"
    else
        log_warning "CDN 不可用，使用原始链接下载"
    fi
}

download_file() {
    local url="$1"
    local output="$2"
    local max_retries=3
    local retry_count=0
    
    while [ $retry_count -lt $max_retries ]; do
        if curl -L --connect-timeout 10 --max-time 60 -o "$output" "$url" 2>/dev/null; then
            return 0
        elif wget -T 10 -t 3 -O "$output" "$url" 2>/dev/null; then
            return 0
        fi
        
        retry_count=$((retry_count + 1))
        log_warning "下载失败，重试 (${retry_count}/${max_retries}): $url"
        sleep 2
    done
    
    log_error "下载失败: $url"
    return 1
}

create_directories() {
    local dirs=("/opt/oneclickvirt" "/opt/oneclickvirt/server" "/opt/oneclickvirt/web")
    for dir in "${dirs[@]}"; do
        if [ ! -d "$dir" ]; then
            mkdir -p "$dir"
            log_info "创建目录: $dir"
        fi
    done
}

install_server() {
    local arch=$(detect_arch)
    local filename="server-linux-${arch}.tar.gz"
    local download_url
    
    if [ -n "$cdn_success_url" ]; then
        download_url="${cdn_success_url}${BASE_URL}/${filename}"
    else
        download_url="${BASE_URL}/${filename}"
    fi
    
    local temp_file="/opt/oneclickvirt/${filename}"
    log_info "下载服务器二进制文件 (${arch})..."
    log_info "下载链接: $download_url"
    
    if download_file "$download_url" "$temp_file"; then
        log_success "下载完成: $filename"
    else
        log_error "下载失败: $download_url"
        exit 1
    fi
    
    log_info "解压服务器二进制文件..."
    if tar -xzf "$temp_file" -C /opt/oneclickvirt/server/; then
        # 检查解压后的文件名并重命名
        if [ -f "/opt/oneclickvirt/server/server-linux-${arch}" ]; then
            mv "/opt/oneclickvirt/server/server-linux-${arch}" "/opt/oneclickvirt/server/oneclickvirt-server"
        elif [ -f "/opt/oneclickvirt/server/oneclickvirt-server" ]; then
            # 文件已经是正确的名称
            :
        else
            # 寻找可执行文件
            local executable=$(find /opt/oneclickvirt/server/ -type f -executable | head -n1)
            if [ -n "$executable" ]; then
                mv "$executable" "/opt/oneclickvirt/server/oneclickvirt-server"
            else
                log_error "未找到可执行文件"
                exit 1
            fi
        fi
        chmod 777 /opt/oneclickvirt/server/oneclickvirt-server
        rm -f "$temp_file"
        log_success "服务器二进制文件安装完成"
    else
        log_error "解压失败"
        exit 1
    fi
}

install_web() {
    local filename="web-dist.zip"
    local download_url
    if [ -n "$cdn_success_url" ]; then
        download_url="${cdn_success_url}${BASE_URL}/${filename}"
    else
        download_url="${BASE_URL}/${filename}"
    fi
    local temp_file="/opt/oneclickvirt/${filename}"
    log_info "下载Web应用文件..."
    log_info "下载链接: $download_url"
    
    if download_file "$download_url" "$temp_file"; then
        log_success "下载完成: $filename"
    else
        log_error "下载失败: $download_url"
        exit 1
    fi
    log_info "解压Web应用文件..."
    if command -v unzip &> /dev/null; then
        if unzip -q "$temp_file" -d /opt/oneclickvirt/web/; then
            rm -f "$temp_file"
            log_success "Web应用文件安装完成"
        else
            log_error "解压失败"
            exit 1
        fi
    else
        log_error "未找到unzip工具"
        log_info "正在安装unzip..."
        if ! ${INSTALL_CMD} unzip 2>/dev/null; then
            log_error "unzip安装失败，跳过Web文件安装"
            return 1
        fi
        if unzip -q "$temp_file" -d /opt/oneclickvirt/web/; then
            rm -f "$temp_file"
            log_success "Web应用文件安装完成"
        else
            log_error "解压失败"
            exit 1
        fi
    fi
    chmod 777 /opt/oneclickvirt/web/
}

download_config() {
    local config_url="https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/refs/heads/main/server/config.yaml"
    local config_file="/opt/oneclickvirt/server/config.yaml"
    local download_url
    
    if [ -n "$cdn_success_url" ]; then
        download_url="${cdn_success_url}${config_url}"
    else
        download_url="$config_url"
    fi
    
    log_info "下载配置文件..."
    log_info "下载链接: $download_url"
    
    if download_file "$download_url" "$config_file"; then
        chmod 644 "$config_file"
        log_success "配置文件下载完成"
    else
        log_error "配置文件下载失败: $config_url"
        exit 1
    fi
}

create_readme() {
    local readme_file="/opt/oneclickvirt/server/readme.md"
    
    log_info "创建使用说明文件..."
    
    cat > "$readme_file" << EOF
# OneClickVirt 使用方法

## 版本信息
版本: $VERSION
系统: $SYSTEM
架构: $(detect_arch)

## 目录结构
- 安装目录: /opt/oneclickvirt
- 服务器文件: /opt/oneclickvirt/server/
- Web文件: /opt/oneclickvirt/web/
- 配置文件: /opt/oneclickvirt/server/config.yaml

## 服务管理命令
- 启动服务: systemctl start oneclickvirt
- 停止服务: systemctl stop oneclickvirt  
- 重启服务: systemctl restart oneclickvirt
- 开机自启: systemctl enable oneclickvirt
- 禁用自启: systemctl disable oneclickvirt
- 查看状态: systemctl status oneclickvirt
- 查看日志: journalctl -u oneclickvirt -f
- 查看最近日志: journalctl -u oneclickvirt --since "1 hour ago"

## 直接运行
- oneclickvirt
- /opt/oneclickvirt/server/oneclickvirt-server

## 配置文件
请根据需要修改 /opt/oneclickvirt/server/config.yaml 配置文件后启动服务

## 端口说明
请确保防火墙允许服务所需端口通过

## 注意事项
- 首次启动前请检查配置文件
- 建议先测试直接运行，确认无误后再使用systemd服务
- 如遇问题，请查看日志文件排查

## 卸载方法
- 停止服务: systemctl stop oneclickvirt
- 删除服务: systemctl disable oneclickvirt && rm -f /etc/systemd/system/oneclickvirt.service
- 删除文件: rm -rf /opt/oneclickvirt /usr/local/bin/oneclickvirt
- 重载systemd: systemctl daemon-reload
EOF

    log_success "使用说明文件创建完成"
}

create_systemd_service() {
    local service_file="/etc/systemd/system/oneclickvirt.service"
    
    log_info "创建systemd服务文件..."
    
    cat > "$service_file" << EOF
[Unit]
Description=OneClickVirt Server
Documentation=https://github.com/oneclickvirt/oneclickvirt
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=/opt/oneclickvirt/server
ExecStart=/opt/oneclickvirt/server/oneclickvirt-server
ExecReload=/bin/kill -HUP \$MAINPID
Restart=always
RestartSec=5
StartLimitInterval=60
StartLimitBurst=3
StandardOutput=journal
StandardError=journal
SyslogIdentifier=oneclickvirt

# Security settings
NoNewPrivileges=yes
PrivateTmp=yes
ProtectSystem=strict
ReadWritePaths=/opt/oneclickvirt

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    log_success "systemd服务文件创建完成"
}

create_symlink() {
    if [ ! -L "/usr/local/bin/oneclickvirt" ]; then
        ln -sf /opt/oneclickvirt/server/oneclickvirt-server /usr/local/bin/oneclickvirt
        log_success "创建命令行链接: /usr/local/bin/oneclickvirt"
    else
        log_info "命令行链接已存在"
    fi
}

check_memory_warning() {
    local mem_size
    mem_size=$(get_memory_size)
    if [ -n "$mem_size" ] && [ "$mem_size" -lt 1024 ]; then
        log_warning "警告: 您的系统内存少于1GB (${mem_size}MB)"
        log_warning "这可能会影响程序运行性能"
        if [ "$noninteractive" != "true" ]; then
            reading "是否继续安装? (y/N): " confirm
            case "$confirm" in
                [Yy]*)
                    log_info "继续安装..."
                    ;;
                *)
                    log_info "取消安装"
                    exit 0
                    ;;
            esac
        fi
    fi
}

show_info() {
    log_success "oneclickvirt 安装完成!"
    echo ""
    log_info "安装信息:"
    log_info "  版本: $VERSION"
    log_info "  系统: $SYSTEM"
    log_info "  架构: $(detect_arch)"
    log_info "  安装路径: /opt/oneclickvirt"
    echo ""
    log_info "使用方法:"
    log_info "  查看帮助: oneclickvirt --help"
    log_info "  启动服务: systemctl start oneclickvirt"
    log_info "  查看状态: systemctl status oneclickvirt"
    log_info "  详细说明: /opt/oneclickvirt/server/readme.md"
    echo ""
    log_warning "请在启动服务前检查并修改配置文件: /opt/oneclickvirt/server/config.yaml"
}

env_check() {
    log_info "开始环境检查..."
    detect_system
    check_memory_warning
    check_dependencies
    check_cdn_file
    log_success "环境检查完成"
}

show_help() {
    cat <<"EOF"
OneClickVirt 安装脚本

用法: $0 [选项]

选项:
  env                   仅检查和准备环境
  install              完整安装 (默认)
  help                 显示此帮助信息
  
环境变量:
  CN=true              强制使用中国镜像
  noninteractive=true  非交互模式

示例:
  $0                   # 完整安装
  $0 env              # 仅环境检查
  CN=true $0          # 使用中国镜像安装
  noninteractive=true $0  # 非交互安装
EOF
}

main() {
    case "${1:-install}" in
        "env")
            check_root
            env_check
            ;;
        "install")
            check_root
            env_check
            create_directories
            install_server
            install_web
            download_config
            create_readme
            create_systemd_service
            create_symlink
            show_info
            ;;
        "help"|"-h"|"--help")
            show_help
            ;;
        *)
            log_error "未知选项: $1"
            show_help
            exit 1
            ;;
    esac
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
