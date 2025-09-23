#!/bin/bash
# from https://github.com/oneclickvirt/oneclickvirt
# 2025.09.23


VERSION="v20250923-102432"
REPO="oneclickvirt/oneclickvirt"
BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
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

check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "此脚本需要以root身份运行"
        exit 1
    fi
}

detect_arch() {
    local arch=$(uname -m)
    case $arch in
        x86_64)
            echo "amd64"
            ;;
        aarch64|arm64)
            echo "arm64"
            ;;
        *)
            log_error "不支持的架构: $arch"
            exit 1
            ;;
    esac
}

check_dependencies() {
    local deps=("curl" "tar")
    local missing=()
    for dep in "${deps[@]}"; do
        if ! command -v "$dep" &> /dev/null; then
            missing+=("$dep")
        fi
    done
    if [ ${#missing[@]} -ne 0 ]; then
        log_error "缺少必要工具: ${missing[*]}"
        log_info "请先安装缺少的工具，例如："
        log_info "Ubuntu/Debian: apt update && apt install -y ${missing[*]}"
        log_info "CentOS/RHEL: yum install -y ${missing[*]}"
        exit 1
    fi
}

create_directories() {
    local dirs=("/opt/oneclickvirt" "/opt/oneclickvirt/bin" "/opt/oneclickvirt/web")
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
    local download_url="${BASE_URL}/${filename}"
    local temp_file="/tmp/${filename}"
    log_info "下载服务器二进制文件 (${arch})..."
    if curl -L -o "$temp_file" "$download_url"; then
        log_success "下载完成: $filename"
    else
        log_error "下载失败: $download_url"
        exit 1
    fi
    log_info "解压服务器二进制文件..."
    if tar -xzf "$temp_file" -C /opt/oneclickvirt/bin/; then
        mv "/opt/oneclickvirt/bin/server-linux-${arch}" "/opt/oneclickvirt/bin/oneclickvirt-server"
        chmod +x "/opt/oneclickvirt/bin/oneclickvirt-server"
        log_success "服务器二进制文件安装完成"
    else
        log_error "解压失败"
        exit 1
    fi
    rm -f "$temp_file"
}

install_web() {
    local filename="web-dist.zip"
    local download_url="${BASE_URL}/${filename}"
    local temp_file="/tmp/${filename}"
    log_info "下载Web应用文件..."
    
    if curl -L -o "$temp_file" "$download_url"; then
        log_success "下载完成: $filename"
    else
        log_error "下载失败: $download_url"
        exit 1
    fi
    log_info "解压Web应用文件..."
    if command -v unzip &> /dev/null; then
        if unzip -q "$temp_file" -d /opt/oneclickvirt/web/; then
            log_success "Web应用文件安装完成"
        else
            log_error "解压失败"
            exit 1
        fi
    else
        log_warning "未找到unzip工具，跳过Web文件安装"
        log_info "请手动安装unzip后重新运行: apt install unzip 或 yum install unzip"
    fi
    rm -f "$temp_file"
}


create_systemd_service() {
    local service_file="/etc/systemd/system/oneclickvirt.service"
    
    log_info "创建systemd服务文件..."
    
    cat > "$service_file" << EOF
[Unit]
Description=OneClickVirt Server
After=network.target

[Service]
Type=simple
User=root
WorkingDirectory=/opt/oneclickvirt
ExecStart=/opt/oneclickvirt/bin/oneclickvirt-server
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal

[Install]
WantedBy=multi-user.target
EOF

    systemctl daemon-reload
    log_success "systemd服务文件创建完成"
}


create_symlink() {
    if [ ! -L "/usr/local/bin/oneclickvirt" ]; then
        ln -s /opt/oneclickvirt/bin/oneclickvirt-server /usr/local/bin/oneclickvirt
        log_success "创建命令行链接: /usr/local/bin/oneclickvirt"
    fi
}

show_info() {
    log_success "oneclickvirt 安装完成!"
    echo ""
    echo "版本: $VERSION"
    echo "安装目录: /opt/oneclickvirt"
    echo "配置文件: /opt/oneclickvirt/config/"
    echo "Web文件: /opt/oneclickvirt/web/"
    echo ""
    echo "使用方法:"
    echo "  启动服务: systemctl start oneclickvirt"
    echo "  停止服务: systemctl stop oneclickvirt" 
    echo "  开机自启: systemctl enable oneclickvirt"
    echo "  查看状态: systemctl status oneclickvirt"
    echo "  查看日志: journalctl -u oneclickvirt -f"
    echo ""
    echo "或直接运行: oneclickvirt 或 /opt/oneclickvirt/bin/oneclickvirt-server"
    echo ""
    log_warning "请根据需要修改配置文件后启动服务"
}

main() {
    check_root
    check_dependencies
    create_directories
    install_server
    install_web
    create_systemd_service
    create_symlink
    show_info
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi
