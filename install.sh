#!/bin/bash
# from https://github.com/oneclickvirt/oneclickvirt
# 2025.11.07

VERSION="" 
REPO="oneclickvirt/oneclickvirt"
BASE_URL=""
cdn_urls="https://cdn0.spiritlhl.top/ http://cdn3.spiritlhl.net/ http://cdn1.spiritlhl.net/ http://cdn2.spiritlhl.net/"
cdn_success_url=""
github_api_urls=(
    "https://api.github.com"
    "https://githubapi.spiritlhl.workers.dev"
    "https://githubapi.spiritlhl.top"
)
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

log_with_level() {
    local level="$1"
    local first_line="$2"
    local second_line="$3"
    echo -e "${level} ${first_line}"
    if [ -n "$second_line" ]; then
        echo -e "${level} ${second_line}"
    fi
}

log_info() {
    log_with_level "${BLUE}[INFO]${NC}" "$1" "$2"
}

log_success() {
    log_with_level "${GREEN}[SUCCESS]${NC}" "$1" "$2"
}

log_warning() {
    log_with_level "${YELLOW}[WARNING]${NC}" "$1" "$2"
}

log_error() {
    log_with_level "${RED}[ERROR]${NC}" "$1" "$2"
}

reading() {
    if [ $# -eq 3 ]; then
        printf "\033[32m\033[01m%s\033[0m\n" "$1"
        printf "\033[32m\033[01m%s\033[0m" "$2"
        read "$3"
    else
        printf "\033[32m\033[01m%s\033[0m" "$1"
        read "$2"
    fi
}

get_latest_version() {
    # 如果用户通过环境变量指定了版本，直接使用
    if [ -n "$INSTALL_VERSION" ]; then
        log_info "Using requested version: $INSTALL_VERSION" "使用指定版本: $INSTALL_VERSION"
        VERSION="$INSTALL_VERSION"
        BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
        return 0
    fi
    
    log_info "Fetching the latest release version..." "正在获取最新版本信息..."
    
    local version=""
    for api_url in "${github_api_urls[@]}"; do
        log_info "Trying to fetch version metadata from $api_url..." "正在尝试从 $api_url 获取版本信息..."
        
        # 尝试获取最新release信息
        local response
        if response=$(curl -sL --connect-timeout 10 --max-time 30 "${api_url}/repos/${REPO}/releases/latest" 2>/dev/null); then
            version=$(echo "$response" | grep '"tag_name":' | sed -E 's/.*"tag_name": "([^"]+)".*/\1/')
            
            if [ -n "$version" ] && [ "$version" != "null" ]; then
                log_success "Latest version resolved: $version" "成功获取最新版本: $version"
                VERSION="$version"
                BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
                return 0
            fi
        fi
        
        log_warning "Failed to fetch version metadata from $api_url, trying the next endpoint..." "从 $api_url 获取版本失败，正在尝试下一个接口..."
        sleep 1
    done
    
    log_error "Unable to fetch the latest version from any API endpoint." "无法从任何 API 接口获取最新版本信息。"
    log_error "Please check network connectivity or set INSTALL_VERSION manually." "请检查网络连接，或手动设置 INSTALL_VERSION。"
    return 1
}

check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "This script must be run as root." "此脚本需要以 root 身份运行。"
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
            log_error "Unsupported architecture: $arch" "不支持的架构: $arch"
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
        log_warning "Unable to detect the operating system, trying common package managers..." "无法识别系统，正在尝试常见包管理器..."
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
            log_error "Unable to detect a supported package manager, aborting installation." "无法识别受支持的包管理器，安装终止。"
            exit 1
        fi
    fi
    
    log_success "Detected operating system: $SYSTEM" "检测到系统: $SYSTEM"
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
        log_warning "Missing required tools: ${missing[*]}" "缺少必要工具: ${missing[*]}"
        log_info "Installing missing dependencies..." "正在安装缺少的依赖工具..."
        
        # 如果是非交互模式，询问是否更新系统
        if [ "$noninteractive" != "true" ]; then
            log_warning "A package index update may take some time and could briefly affect network availability." "更新系统包索引可能耗时较长，并可能导致网络短暂波动。"
            reading "Update package indexes before installing dependencies? (y/N): " "是否先更新系统包索引再安装依赖？(y/N): " update_confirm
            case "$update_confirm" in
                [Yy]*)
                    log_info "Updating package indexes..." "正在更新系统包索引..."
                    if ! ${UPDATE_CMD} 2>/dev/null; then
                        log_warning "Package index update failed, continuing with dependency installation." "系统更新失败，继续安装依赖。"
                    fi
                    ;;
                *)
                    log_warning "Skipping package index update; some package installations may fail." "已跳过系统更新，某些软件包可能安装失败。"
                    ;;
            esac
        fi
        
        for dep in "${missing[@]}"; do
            log_info "Installing $dep..." "正在安装 $dep..."
            if ! ${INSTALL_CMD} "$dep" 2>/dev/null; then
                log_error "Failed to install $dep." "安装 $dep 失败。"
                exit 1
            fi
        done
        log_success "Dependency installation completed." "依赖工具安装完成。"
    else
        log_success "All required tools are already installed." "所有必要工具均已安装。"
    fi
}

get_memory_size() {
    # Returns total memory (RAM + swap) in MB
    if [ -f /proc/meminfo ]; then
        local mem_kb swap_kb
        mem_kb=$(grep MemTotal /proc/meminfo | awk '{print $2}')
        swap_kb=$(grep SwapTotal /proc/meminfo | awk '{print $2}')
        mem_kb=${mem_kb:-0}
        swap_kb=${swap_kb:-0}
        echo $(((mem_kb + swap_kb) / 1024)) # Convert to MB
        return 0
    fi
    if command -v free >/dev/null 2>&1; then
        local mem_mb swap_mb
        mem_mb=$(free -m | awk '/^Mem:/ {print $2}')
        swap_mb=$(free -m | awk '/^Swap:/ {print $2}')
        mem_mb=${mem_mb:-0}
        swap_mb=${swap_mb:-0}
        echo $((mem_mb + swap_mb)) # Already in MB
        return 0
    fi
    if command -v sysctl >/dev/null 2>&1; then
        local mem_bytes
        mem_bytes=$(sysctl -n hw.memsize 2>/dev/null || sysctl -n hw.physmem 2>/dev/null)
        if [ -n "$mem_bytes" ]; then
            echo $((mem_bytes / 1024 / 1024)) # Convert to MB (no swap info on macOS/BSD via sysctl)
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
        log_info "CDN mirrors are available; accelerated downloads will be used." "CDN 可用，将使用 CDN 加速下载。"
    else
        log_warning "CDN mirrors are unavailable; falling back to direct origin downloads." "CDN 不可用，将使用原始链接下载。"
    fi
}

download_file() {
    local url="$1"
    local output="$2"
    local max_retries=3
    local retry_count=0
    local total_size=0

    # Get file size from headers
    total_size=$(curl -sIkL --connect-timeout 10 "$url" 2>/dev/null | grep -i 'Content-Length' | awk '{print $2}' | tr -d '\r\n ' | grep -o '[0-9]*' | tail -1)
    total_size=${total_size:-0}
    [ -z "$total_size" ] && total_size=0

    _dl_progress() {
        local out="$1"
        local total="$2"
        local pid="$3"
        local shown=0
        while kill -0 "$pid" 2>/dev/null; do
            if [ -f "$out" ]; then
                local cur=0
                cur=$(stat -c%s "$out" 2>/dev/null || stat -f%z "$out" 2>/dev/null)
                cur=$(printf '%s' "$cur" | tr -d '\r\n ' | grep -o '[0-9]*' | head -1)
                cur=${cur:-0}
                if [ "$total" -gt 0 ] && [ "$cur" -gt 0 ]; then
                    local pct=$((cur * 100 / total))
                    [ "$pct" -gt 100 ] && pct=100
                    if [ "$pct" -gt "$shown" ]; then
                        local bar=""
                        local filled=$((pct / 2))
                        local i=0
                        while [ $i -lt $filled ]; do bar="${bar}#"; i=$((i+1)); done
                        while [ $i -lt 50 ]; do bar="${bar}."; i=$((i+1)); done
                        printf "\r [%-50s] %3d%%" "$bar" "$pct"
                        shown=$pct
                    fi
                fi
            fi
            sleep 0.5
        done
        if [ -f "$out" ] && [ "$total" -gt 0 ]; then
            printf "\r [%-50s] 100%%\n" "$(printf '#%.0s' $(seq 1 50))"
        else
            printf "\r\033[K"
        fi
    }
    
    while [ $retry_count -lt $max_retries ]; do
        echo ""
        curl -L --connect-timeout 20 --max-time 600 -o "$output" "$url" 2>/dev/null &
        local dl_pid=$!
        _dl_progress "$output" "$total_size" "$dl_pid" &
        local mon_pid=$!
        wait "$dl_pid" 2>/dev/null
        wait "$mon_pid" 2>/dev/null
        if [ -s "$output" ]; then
            return 0
        fi

        rm -f "$output"
        wget -T 20 -t 3 -O "$output" "$url" 2>/dev/null &
        dl_pid=$!
        _dl_progress "$output" "$total_size" "$dl_pid" &
        mon_pid=$!
        wait "$dl_pid" 2>/dev/null
        wait "$mon_pid" 2>/dev/null
        if [ -s "$output" ]; then
            return 0
        fi
        
        retry_count=$((retry_count + 1))
        log_warning "Download failed, retrying (${retry_count}/${max_retries}): $url" "下载失败，正在重试 (${retry_count}/${max_retries}): $url"
        sleep 2
    done
    
    log_error "Download failed: $url" "下载失败: $url"
    return 1
}

create_directories() {
    local dirs=("/opt/oneclickvirt" "/opt/oneclickvirt/server")
    
    # 如果自定义了Web路径，使用自定义路径，否则使用默认路径
    if [ -n "$custom_web_path" ]; then
        dirs+=("$custom_web_path")
    else
        dirs+=("/opt/oneclickvirt/web")
    fi
    
    for dir in "${dirs[@]}"; do
        if [ ! -d "$dir" ]; then
            mkdir -p "$dir"
            log_info "Creating directory: $dir" "正在创建目录: $dir"
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
    log_info "Downloading server binary (${arch})..." "正在下载服务器二进制文件 (${arch})..."
    log_info "Download URL: $download_url" "下载链接: $download_url"
    
    if download_file "$download_url" "$temp_file"; then
        log_success "Download completed: $filename" "下载完成: $filename"
    else
        log_error "Failed to download: $download_url" "下载失败: $download_url"
        exit 1
    fi
    
    log_info "Extracting server binary package..." "正在解压服务器二进制文件..."
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
                log_error "No executable file was found after extraction." "解压后未找到可执行文件。"
                exit 1
            fi
        fi
        chmod 777 /opt/oneclickvirt/server/oneclickvirt-server
        rm -f "$temp_file"
        log_success "Server binary installation completed." "服务器二进制文件安装完成。"
    else
        log_error "Extraction failed." "解压失败。"
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
    
    # 确定Web安装路径
    local web_path
    if [ -n "$custom_web_path" ]; then
        web_path="$custom_web_path"
        log_info "Using custom web path: $web_path" "使用自定义 Web 路径: $web_path"
    else
        web_path="/opt/oneclickvirt/web"
        log_info "Using default web path: $web_path" "使用默认 Web 路径: $web_path"
    fi
    
    log_info "Downloading web assets..." "正在下载 Web 应用文件..."
    log_info "Download URL: $download_url" "下载链接: $download_url"
    
    if download_file "$download_url" "$temp_file"; then
        log_success "Download completed: $filename" "下载完成: $filename"
    else
        log_error "Failed to download: $download_url" "下载失败: $download_url"
        exit 1
    fi
    
    log_info "Extracting web assets..." "正在解压 Web 应用文件..."
    if command -v unzip &> /dev/null; then
        if unzip -q "$temp_file" -d "$web_path/"; then
            rm -f "$temp_file"
            chmod 777 "$web_path/"
            log_success "Web assets installed successfully: $web_path" "Web 应用文件安装完成: $web_path"
        else
            log_error "Extraction failed." "解压失败。"
            exit 1
        fi
    else
        log_error "The unzip utility is missing." "未找到 unzip 工具。"
        log_info "Installing unzip..." "正在安装 unzip..."
        if ! ${INSTALL_CMD} unzip 2>/dev/null; then
            log_error "Failed to install unzip; skipping web asset installation." "unzip 安装失败，跳过 Web 文件安装。"
            return 1
        fi
        if unzip -q "$temp_file" -d "$web_path/"; then
            rm -f "$temp_file"
            chmod 777 "$web_path/"
            log_success "Web assets installed successfully: $web_path" "Web 应用文件安装完成: $web_path"
        else
            log_error "Extraction failed." "解压失败。"
            exit 1
        fi
    fi
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
    
    log_info "Downloading configuration file..." "正在下载配置文件..."
    log_info "Download URL: $download_url" "下载链接: $download_url"
    
    if download_file "$download_url" "$config_file"; then
        chmod 644 "$config_file"
        log_success "Configuration file download completed." "配置文件下载完成。"
    else
        log_error "Failed to download configuration file: $config_url" "配置文件下载失败: $config_url"
        exit 1
    fi
}

create_readme() {
    local readme_file="/opt/oneclickvirt/server/readme.md"
    
    log_info "Creating the usage guide..." "正在创建使用说明文件..."
    
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

    log_success "Usage guide created successfully." "使用说明文件创建完成。"
}

create_systemd_service() {
    local service_file="/etc/systemd/system/oneclickvirt.service"
    
    log_info "Creating the systemd service file..." "正在创建 systemd 服务文件..."
    
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
    log_success "systemd service file created successfully." "systemd 服务文件创建完成。"
}

create_symlink() {
    if [ ! -L "/usr/local/bin/oneclickvirt" ]; then
        ln -sf /opt/oneclickvirt/server/oneclickvirt-server /usr/local/bin/oneclickvirt
        log_success "CLI symlink created: /usr/local/bin/oneclickvirt" "命令行链接已创建: /usr/local/bin/oneclickvirt"
    else
        log_info "CLI symlink already exists." "命令行链接已存在。"
    fi
}

upgrade_server() {
    if [ ! -f "/opt/oneclickvirt/server/oneclickvirt-server" ]; then
        log_error "No existing installation was detected; please use the install command for a fresh setup." "未检测到已安装版本，请使用 install 选项进行全新安装。"
        exit 1
    fi
    
    log_info "Starting upgrade to version: $VERSION" "开始升级到版本: $VERSION"
    
    # 检查服务是否正在运行
    local service_was_running=false
    if systemctl is-active --quiet oneclickvirt 2>/dev/null; then
        log_info "Stopping the oneclickvirt service..." "正在停止 oneclickvirt 服务..."
        systemctl stop oneclickvirt
        service_was_running=true
    fi
    
    # 升级服务器二进制文件
    log_info "Upgrading server binary..." "正在升级服务器二进制文件..."
    install_server
    
    # 升级Web文件 - 先删除旧文件，再解压新文件
    log_info "Upgrading web assets..." "正在升级 Web 应用文件..."
    
    # 确定Web路径
    local web_path
    if [ -n "$custom_web_path" ]; then
        web_path="$custom_web_path"
    else
        web_path="/opt/oneclickvirt/web"
    fi
    
    # 删除旧的Web文件夹内容（但保留文件夹本身）
    if [ -d "$web_path" ]; then
        log_info "Cleaning old web assets at: $web_path" "正在清理旧的 Web 文件: $web_path"
        rm -rf "${web_path:?}"/*
        log_success "Old web assets have been cleaned up." "旧 Web 文件已清理。"
    fi
    
    # 安装新的Web文件
    install_web
    
    # 重新启动服务
    if [ "$service_was_running" = true ]; then
        log_info "Restarting the oneclickvirt service..." "正在重新启动 oneclickvirt 服务..."
        systemctl start oneclickvirt
        sleep 2
        if systemctl is-active --quiet oneclickvirt; then
            log_success "Service restarted successfully." "服务已成功重启。"
        else
            log_error "Service failed to start; check logs with: journalctl -u oneclickvirt -n 50" "服务启动失败，请检查日志: journalctl -u oneclickvirt -n 50"
        fi
    fi
    
    log_success "Upgrade completed successfully." "升级完成！"
    log_info "Version: $VERSION" "版本: $VERSION"
    log_info "Configuration file kept unchanged: /opt/oneclickvirt/server/config.yaml" "配置文件保持不变: /opt/oneclickvirt/server/config.yaml"
    log_info "Web path: $web_path" "Web 路径: $web_path"
    if [ "$service_was_running" = false ]; then
        log_warning "The service was not auto-started; start it manually with: systemctl start oneclickvirt" "服务未自动启动，请手动执行: systemctl start oneclickvirt"
    fi
}

check_memory_warning() {
    local mem_size
    mem_size=$(get_memory_size)
    if [ -n "$mem_size" ] && [ "$mem_size" -lt 2048 ]; then
        log_warning "Warning: system memory (RAM + swap) is below 2 GB (${mem_size} MB)." "警告：系统内存（含 swap）低于 2GB (${mem_size}MB)。"
        log_warning "This may affect runtime performance." "这可能会影响程序运行性能。"
        if [ "$noninteractive" != "true" ]; then
            reading "Continue with installation? (y/N): " "是否继续安装？(y/N): " confirm
            case "$confirm" in
                [Yy]*)
                    log_info "Continuing installation..." "继续安装..."
                    ;;
                *)
                    log_info "Installation cancelled." "安装已取消。"
                    exit 0
                    ;;
            esac
        fi
    fi
}

show_info() {
    log_success "OneClickVirt installation completed." "oneclickvirt 安装完成！"
    echo ""
    log_info "Installation summary:" "安装信息："
    log_info "  Version: $VERSION" "  版本: $VERSION"
    log_info "  System: $SYSTEM" "  系统: $SYSTEM"
    log_info "  Architecture: $(detect_arch)" "  架构: $(detect_arch)"
    log_info "  Install path: /opt/oneclickvirt" "  安装路径: /opt/oneclickvirt"
    if [ -n "$custom_web_path" ]; then
        log_info "  Web path: $custom_web_path (custom)" "  Web 路径: $custom_web_path (自定义)"
    else
        log_info "  Web path: /opt/oneclickvirt/web (default)" "  Web 路径: /opt/oneclickvirt/web (默认)"
    fi
    echo ""
    log_info "Quick usage:" "使用方法："
    log_info "  Help: oneclickvirt --help" "  查看帮助: oneclickvirt --help"
    log_info "  Start service: systemctl start oneclickvirt" "  启动服务: systemctl start oneclickvirt"
    log_info "  Service status: systemctl status oneclickvirt" "  查看状态: systemctl status oneclickvirt"
    log_info "  Full guide: /opt/oneclickvirt/server/readme.md" "  详细说明: /opt/oneclickvirt/server/readme.md"
    echo ""
    log_warning "Review and update the configuration before starting the service: /opt/oneclickvirt/server/config.yaml" "请在启动服务前检查并修改配置文件: /opt/oneclickvirt/server/config.yaml"
}

env_check() {
    log_info "Starting environment checks..." "开始环境检查..."
    
    # 获取最新版本
    if ! get_latest_version; then
        log_error "Unable to resolve the latest version, installation aborted." "无法获取最新版本，安装终止。"
        exit 1
    fi
    
    detect_system
    check_memory_warning
    check_dependencies
    check_cdn_file
        log_success "Environment checks completed." "环境检查完成。"
}

show_help() {
    cat <<"EOF"
OneClickVirt installer
OneClickVirt 安装脚本

Usage: $0 [option]
用法: $0 [选项]

Options:
选项:
    env                   Check and prepare the environment only
    env                   仅检查和准备环境
    install               Full installation (default)
    install               完整安装（默认）
    upgrade               Upgrade an existing installation
    upgrade               升级已安装版本
    help                  Show this help message
    help                  显示此帮助信息

Environment variables:
环境变量:
    CN=true                    Force China mirrors
    CN=true                    强制使用中国镜像
    noninteractive=true        Non-interactive mode
    noninteractive=true        非交互模式
    WEB_PATH=/path             Custom web install path (default: /opt/oneclickvirt/web)
    WEB_PATH=/path             自定义 Web 安装路径（默认: /opt/oneclickvirt/web）
    INSTALL_VERSION=v1.0.0     Install a specific version (default: latest release)
    INSTALL_VERSION=v1.0.0     指定安装版本（默认：自动获取最新版本）

Examples:
示例:
    $0                                    # Install the latest version
    $0                                    # 安装最新版本
    $0 env                                # Environment checks only
    $0 env                                # 仅进行环境检查
    $0 upgrade                            # Upgrade to the latest version
    $0 upgrade                            # 升级到最新版本
    CN=true $0                            # Install using China mirrors
    CN=true $0                            # 使用中国镜像安装
    noninteractive=true $0                # Install without prompts
    noninteractive=true $0                # 非交互安装
    WEB_PATH=/var/www/html $0             # Install with a custom web path
    WEB_PATH=/var/www/html $0             # 使用自定义 Web 路径安装
    INSTALL_VERSION=v1.0.0 $0             # Install a specific version
    INSTALL_VERSION=v1.0.0 $0             # 安装指定版本
    INSTALL_VERSION=v1.0.0 $0 upgrade     # Upgrade to a specific version
    INSTALL_VERSION=v1.0.0 $0 upgrade     # 升级到指定版本
    WEB_PATH=/custom/path $0 upgrade      # Upgrade with a custom web path
    WEB_PATH=/custom/path $0 upgrade      # 升级时指定自定义 Web 路径
EOF
}

main() {
    # 从环境变量读取自定义Web路径
    custom_web_path="${WEB_PATH:-}"
    
    case "${1:-install}" in
        "env")
            check_root
            env_check
            ;;
        "install")
            check_root
            env_check
            # 处理自定义Web路径（仅在 install 模式下询问）
            if [ "$noninteractive" != "true" ] && [ -z "$custom_web_path" ]; then
                reading "Use a custom web path? (y/N): " "是否使用自定义 Web 路径？(y/N): " use_custom
                case "$use_custom" in
                    [Yy]*)
                        reading "Enter the web path (for example: /var/www/html): " "请输入 Web 路径（例如 /var/www/html）: " custom_web_path
                        if [ -n "$custom_web_path" ]; then
                            log_info "Custom web path selected: $custom_web_path" "将使用自定义 Web 路径: $custom_web_path"
                        else
                            log_warning "No path was provided; the default path will be used: /opt/oneclickvirt/web" "未输入路径，将使用默认路径: /opt/oneclickvirt/web"
                        fi
                        ;;
                    *)
                        log_info "Using default web path: /opt/oneclickvirt/web" "使用默认 Web 路径: /opt/oneclickvirt/web"
                        ;;
                esac
            elif [ -n "$custom_web_path" ]; then
                log_info "Detected WEB_PATH from environment: $custom_web_path" "检测到环境变量 WEB_PATH: $custom_web_path"
            fi
            create_directories
            install_server
            install_web
            download_config
            create_readme
            create_systemd_service
            create_symlink
            show_info
            ;;
        "upgrade")
            check_root
            env_check
            upgrade_server
            ;;
        "help"|"-h"|"--help")
            show_help
            ;;
        *)
            log_error "Unknown option: $1" "未知选项: $1"
            show_help
            exit 1
            ;;
    esac
}

if [[ "${BASH_SOURCE[0]}" == "${0}" ]]; then
    main "$@"
fi