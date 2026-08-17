#!/bin/bash
# from https://github.com/oneclickvirt/oneclickvirt
# 2026.07.29

VERSION="" 
REPO="oneclickvirt/oneclickvirt"
BASE_URL=""
MANAGED_INSTALL_ROOT="${ONECLICKVIRT_INSTALL_ROOT:-/opt/oneclickvirt}"
MANAGED_SERVER_DIR="${ONECLICKVIRT_SERVER_DIR:-${MANAGED_INSTALL_ROOT}/server}"
MANAGED_SERVER_BIN="${ONECLICKVIRT_SERVER_BIN:-${MANAGED_SERVER_DIR}/oneclickvirt-server}"
MANAGED_ENV_FILE="${ONECLICKVIRT_ENV_FILE:-${MANAGED_SERVER_DIR}/oneclickvirt.env}"
MANAGED_SERVICE_FILE="${ONECLICKVIRT_SERVICE_FILE:-/etc/systemd/system/oneclickvirt.service}"
MANAGED_CLI_LINK="${ONECLICKVIRT_CLI_LINK:-/usr/local/bin/oneclickvirt}"
MANAGED_SERVICE_NAME="${ONECLICKVIRT_SERVICE_NAME:-oneclickvirt}"
EXPECTED_API_CONTRACT="2026-08-16.1"
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

existing_install_detected() {
    [ -x "$MANAGED_SERVER_BIN" ] || [ -f "$MANAGED_SERVER_DIR/config.yaml" ] || [ -f "$MANAGED_SERVICE_FILE" ]
}

managed_web_path() {
    if [ -n "${custom_web_path:-}" ]; then
        printf '%s' "$custom_web_path"
    else
        printf '%s' "${ONECLICKVIRT_WEB_DIR:-${MANAGED_INSTALL_ROOT}/web}"
    fi
}

confirm_existing_install_action() {
    log_warning "An existing OneClickVirt installation was detected. Running install again can overwrite config.yaml and web assets." \
        "检测到已有 OneClickVirt 安装。再次执行 install 可能覆盖 config.yaml 和 Web 文件。"

    if [ "${noninteractive:-false}" = "true" ]; then
        if [ "${FORCE_REINSTALL:-false}" = "true" ] && [ "${CONFIRM_REINSTALL:-}" = "REINSTALL" ]; then
            log_warning "Forced reinstall explicitly confirmed in non-interactive mode." "非交互模式已显式确认强制重装。"
            return 0
        fi
        log_warning "Non-interactive install is being converted to a safe upgrade. Set FORCE_REINSTALL=true and CONFIRM_REINSTALL=REINSTALL only for an intentional reinstall." \
            "非交互 install 已自动转换为安全升级。仅在确需重装时同时设置 FORCE_REINSTALL=true 和 CONFIRM_REINSTALL=REINSTALL。"
        return 2
    fi

    local switch_to_upgrade
    reading "Switch to the safe upgrade flow instead? (Y/n): " "是否改用安全升级流程？(Y/n): " switch_to_upgrade
    case "$switch_to_upgrade" in
        [Nn]*) ;;
        *) return 2 ;;
    esac

    local reinstall_confirmation
    reading "Fresh install may overwrite existing configuration. Type REINSTALL to continue: " \
        "全新安装可能覆盖现有配置。请输入 REINSTALL 继续：" reinstall_confirmation
    if [ "$reinstall_confirmation" != "REINSTALL" ]; then
        log_warning "Reinstall confirmation did not match; installation aborted." "重装确认不匹配，已中止安装。"
        return 1
    fi

    return 0
}

reading() {
    if [ $# -eq 3 ]; then
        printf "\033[32m\033[01m%s\033[0m\n" "$1"
        printf "\033[32m\033[01m%s\033[0m" "$2"
        read -r "$3"
    else
        printf "\033[32m\033[01m%s\033[0m" "$1"
        read -r "$2"
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
    local arch
    arch=$(uname -m)
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
        SYS="$(head -n1 /etc/issue | sed 's/\\.*//' | sed '/^[ ]*$/d')"
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
            local update_confirm=""
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
    local dirs=("$MANAGED_INSTALL_ROOT" "$MANAGED_SERVER_DIR" "$(managed_web_path)")
    
    for dir in "${dirs[@]}"; do
        if [ ! -d "$dir" ]; then
            mkdir -p "$dir"
            log_info "Creating directory: $dir" "正在创建目录: $dir"
        fi
    done
}

install_server() {
    local target_dir="${1:-$MANAGED_SERVER_DIR}"
    local arch
    arch=$(detect_arch)
    local filename="server-linux-${arch}.tar.gz"
    local download_url
    local work_dir
    work_dir=$(mktemp -d "${MANAGED_INSTALL_ROOT}/.server-download.XXXXXX") || return 1
    local temp_file="${work_dir}/${filename}"
    local extract_dir="${work_dir}/extract"
    mkdir -p "$extract_dir" "$target_dir"
    
    if [ -n "$cdn_success_url" ]; then
        download_url="${cdn_success_url}${BASE_URL}/${filename}"
    else
        download_url="${BASE_URL}/${filename}"
    fi
    
    log_info "Downloading server binary (${arch})..." "正在下载服务器二进制文件 (${arch})..."
    log_info "Download URL: $download_url" "下载链接: $download_url"
    
    if download_file "$download_url" "$temp_file"; then
        log_success "Download completed: $filename" "下载完成: $filename"
    else
        log_error "Failed to download: $download_url" "下载失败: $download_url"
        rm -rf "$work_dir"
        return 1
    fi
    
    log_info "Extracting server binary package..." "正在解压服务器二进制文件..."
    if tar -xzf "$temp_file" -C "$extract_dir"; then
        # 检查解压后的文件名并重命名
        local executable=""
        if [ -f "${extract_dir}/server-linux-${arch}" ]; then
            executable="${extract_dir}/server-linux-${arch}"
        elif [ -f "${extract_dir}/oneclickvirt-server" ]; then
            executable="${extract_dir}/oneclickvirt-server"
        else
            executable=$(find "$extract_dir" -type f -executable | head -n1)
        fi
        if [ -z "$executable" ]; then
            log_error "No executable file was found after extraction." "解压后未找到可执行文件。"
            rm -rf "$work_dir"
            return 1
        fi
        local target_binary="${target_dir}/oneclickvirt-server"
        local next_binary
        next_binary=$(mktemp "${target_dir}/.oneclickvirt-server.XXXXXX") || {
            rm -rf "$work_dir"
            return 1
        }
        if ! cp "$executable" "$next_binary" || ! chmod 0755 "$next_binary" || ! mv -f "$next_binary" "$target_binary"; then
            rm -f "$next_binary"
            rm -rf "$work_dir"
            return 1
        fi
        rm -rf "$work_dir"
        log_success "Server binary installation completed." "服务器二进制文件安装完成。"
    else
        log_error "Extraction failed." "解压失败。"
        rm -rf "$work_dir"
        return 1
    fi
}

install_web() {
    local web_path="${1:-$(managed_web_path)}"
    local filename="web-dist.zip"
    local download_url
    local work_dir
    work_dir=$(mktemp -d "${MANAGED_INSTALL_ROOT}/.web-download.XXXXXX") || return 1
    local temp_file="${work_dir}/${filename}"
    if [ -n "$cdn_success_url" ]; then
        download_url="${cdn_success_url}${BASE_URL}/${filename}"
    else
        download_url="${BASE_URL}/${filename}"
    fi
    log_info "Using web path: $web_path" "使用 Web 路径: $web_path"
    mkdir -p "$web_path"
    
    log_info "Downloading web assets..." "正在下载 Web 应用文件..."
    log_info "Download URL: $download_url" "下载链接: $download_url"
    
    if download_file "$download_url" "$temp_file"; then
        log_success "Download completed: $filename" "下载完成: $filename"
    else
        log_error "Failed to download: $download_url" "下载失败: $download_url"
        rm -rf "$work_dir"
        return 1
    fi
    
    log_info "Extracting web assets..." "正在解压 Web 应用文件..."
    if command -v unzip &> /dev/null; then
        if unzip -q -o "$temp_file" -d "$web_path/"; then
            rm -rf "$work_dir"
            chmod 0755 "$web_path/"
            log_success "Web assets installed successfully: $web_path" "Web 应用文件安装完成: $web_path"
        else
            log_error "Extraction failed." "解压失败。"
            rm -rf "$work_dir"
            return 1
        fi
    else
        log_error "The unzip utility is missing." "未找到 unzip 工具。"
        log_info "Installing unzip..." "正在安装 unzip..."
        if ! ${INSTALL_CMD} unzip 2>/dev/null; then
            log_error "Failed to install unzip; skipping web asset installation." "unzip 安装失败，跳过 Web 文件安装。"
            return 1
        fi
        if unzip -q -o "$temp_file" -d "$web_path/"; then
            rm -rf "$work_dir"
            chmod 0755 "$web_path/"
            log_success "Web assets installed successfully: $web_path" "Web 应用文件安装完成: $web_path"
        else
            log_error "Extraction failed." "解压失败。"
            rm -rf "$work_dir"
            return 1
        fi
    fi
}

download_config() {
    local config_url="https://raw.githubusercontent.com/${REPO}/${VERSION}/server/config.yaml"
    local config_file="${MANAGED_SERVER_DIR}/config.yaml"
    local download_url
    
    if [ -n "$cdn_success_url" ]; then
        download_url="${cdn_success_url}${config_url}"
    else
        download_url="$config_url"
    fi
    
    log_info "Downloading configuration file..." "正在下载配置文件..."
    log_info "Download URL: $download_url" "下载链接: $download_url"
    
    mkdir -p "$MANAGED_SERVER_DIR"
    local next_config
    next_config=$(mktemp "${MANAGED_SERVER_DIR}/.config.yaml.XXXXXX") || return 1
    if download_file "$download_url" "$next_config"; then
        chmod 0644 "$next_config"
        if ! mv -f "$next_config" "$config_file"; then
            rm -f "$next_config"
            return 1
        fi
        log_success "Configuration file download completed." "配置文件下载完成。"
    else
        rm -f "$next_config"
        log_error "Failed to download configuration file: $config_url" "配置文件下载失败: $config_url"
        return 1
    fi
}

create_readme() {
    local readme_file="${MANAGED_SERVER_DIR}/readme.md"
    
    log_info "Creating the usage guide..." "正在创建使用说明文件..."
    
    cat > "$readme_file" << EOF
# OneClickVirt 使用方法

## 版本信息
版本: $VERSION
系统: $SYSTEM
架构: $(detect_arch)

## 目录结构
- 安装目录: ${MANAGED_INSTALL_ROOT}
- 服务器文件: ${MANAGED_SERVER_DIR}/
- Web文件: $(managed_web_path)/
- 配置文件: ${MANAGED_SERVER_DIR}/config.yaml

## 服务管理命令
- 启动服务: systemctl start ${MANAGED_SERVICE_NAME}
- 停止服务: systemctl stop ${MANAGED_SERVICE_NAME}
- 重启服务: systemctl restart ${MANAGED_SERVICE_NAME}
- 开机自启: systemctl enable ${MANAGED_SERVICE_NAME}
- 禁用自启: systemctl disable ${MANAGED_SERVICE_NAME}
- 查看状态: bash install.sh status
- 查看日志: bash install.sh logs
- 持续查看日志: bash install.sh logs --follow
- 查看最近日志: journalctl -u ${MANAGED_SERVICE_NAME} --since "1 hour ago"
- 卸载应用并保留配置和存储: bash install.sh uninstall
- 完全删除应用目录: bash install.sh uninstall --purge

## 直接运行
- ${MANAGED_CLI_LINK}
- ${MANAGED_SERVER_BIN}

## 配置文件
请根据需要修改 ${MANAGED_SERVER_DIR}/config.yaml 配置文件后启动服务

## 端口说明
请确保防火墙允许服务所需端口通过

## 注意事项
- 首次启动前请检查配置文件
- 建议先测试直接运行，确认无误后再使用systemd服务
- 如遇问题，请查看日志文件排查

## 卸载方法
- 停止服务: systemctl stop ${MANAGED_SERVICE_NAME}
- 删除服务: systemctl disable ${MANAGED_SERVICE_NAME} && rm -f ${MANAGED_SERVICE_FILE}
- 删除文件: rm -rf ${MANAGED_INSTALL_ROOT} ${MANAGED_CLI_LINK}
- 重载systemd: systemctl daemon-reload
EOF

    log_success "Usage guide created successfully." "使用说明文件创建完成。"
}

create_systemd_service() {
    local service_file="$MANAGED_SERVICE_FILE"
    
    log_info "Creating the systemd service file..." "正在创建 systemd 服务文件..."
    
    mkdir -p "$(dirname "$service_file")" "$MANAGED_SERVER_DIR"
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
EnvironmentFile=-${MANAGED_ENV_FILE}
WorkingDirectory=${MANAGED_SERVER_DIR}
ExecStart=${MANAGED_SERVER_BIN}
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
ReadWritePaths=${MANAGED_INSTALL_ROOT}

[Install]
WantedBy=multi-user.target
EOF

    if ! systemctl daemon-reload; then
        log_error "Failed to reload systemd after writing the service file." "写入服务文件后重载 systemd 失败。"
        return 1
    fi
    log_success "systemd service file created successfully." "systemd 服务文件创建完成。"
}

create_symlink() {
    if [ ! -L "$MANAGED_CLI_LINK" ]; then
        ln -sf "$MANAGED_SERVER_BIN" "$MANAGED_CLI_LINK"
        log_success "CLI symlink created: $MANAGED_CLI_LINK" "命令行链接已创建: $MANAGED_CLI_LINK"
    else
        log_info "CLI symlink already exists." "命令行链接已存在。"
    fi
}

find_running_server_pids() {
    local proc pid exe cmdline
    for proc in /proc/[0-9]*; do
        [ -r "$proc/cmdline" ] || continue
        pid=${proc##*/}
        exe=$(readlink "$proc/exe" 2>/dev/null || true)
        exe=${exe% (deleted)}
        cmdline=$(tr '\0' ' ' < "$proc/cmdline" 2>/dev/null || true)
        if [ "$exe" = "$MANAGED_SERVER_BIN" ] || [[ "$exe" == "$MANAGED_SERVER_DIR/"* ]] || [[ "$cmdline" == "$MANAGED_SERVER_DIR"/* ]]; then
            printf '%s\n' "$pid"
        fi
    done
}

persist_runtime_environment() {
    local pids="$1"
    local temp_file
    mkdir -p "$MANAGED_SERVER_DIR"
    temp_file=$(mktemp "${MANAGED_SERVER_DIR}/.oneclickvirt-env.XXXXXX") || return 1
    local captured=0 name value pid escaped filtered_file

    if [ -f "$MANAGED_ENV_FILE" ]; then
        cp "$MANAGED_ENV_FILE" "$temp_file" || {
            rm -f "$temp_file"
            return 1
        }
    fi

    for name in DB_HOST DB_PORT DB_NAME DB_USER DB_PASSWORD DB_TYPE SERVER_PORT; do
        value=""
        for pid in $pids; do
            [ -r "/proc/$pid/environ" ] || continue
            value=$(tr '\0' '\n' < "/proc/$pid/environ" | sed -n "s/^${name}=//p" | head -n1)
            [ -n "$value" ] && break
        done
        if [ -z "$value" ]; then
            value="${!name:-}"
        fi
        [ -n "$value" ] || continue
        value=${value//$'\n'/}
        escaped=${value//\\/\\\\}
        escaped=${escaped//\"/\\\"}
        filtered_file=$(mktemp "${MANAGED_SERVER_DIR}/.oneclickvirt-env-filtered.XXXXXX") || {
            rm -f "$temp_file"
            return 1
        }
        awk -v key="$name" 'index($0, key "=") != 1 { print }' "$temp_file" > "$filtered_file"
        mv "$filtered_file" "$temp_file"
        printf '%s="%s"\n' "$name" "$escaped" >> "$temp_file"
        captured=$((captured + 1))
    done

    if [ "$captured" -gt 0 ]; then
        chmod 0600 "$temp_file"
        mv "$temp_file" "$MANAGED_ENV_FILE"
        log_info "Persisted runtime deployment variables to the protected environment file." "已将运行时部署变量保存到受保护的环境文件。"
    else
        rm -f "$temp_file"
    fi
}

ensure_systemd_environment_dropin() {
    local dropin_dir="${MANAGED_SERVICE_FILE}.d"
    mkdir -p "$dropin_dir"
    cat > "${dropin_dir}/10-oneclickvirt-env.conf" << EOF
[Service]
EnvironmentFile=-${MANAGED_ENV_FILE}
WorkingDirectory=${MANAGED_SERVER_DIR}
ExecStart=
ExecStart=${MANAGED_SERVER_BIN}
EOF
    systemctl daemon-reload
}

verify_managed_server_process() {
    local pid exe
    pid=$(systemctl show "$MANAGED_SERVICE_NAME" --property MainPID --value 2>/dev/null || true)
    case "$pid" in
        ''|0|*[!0-9]*) return 1 ;;
    esac
    exe=$(readlink "/proc/${pid}/exe" 2>/dev/null || true)
    exe=${exe% (deleted)}
    [ "$exe" = "$MANAGED_SERVER_BIN" ]
}

stop_running_server_pids() {
    local pids="$1" pid wait_round
    local -a pid_list=()
    [ -n "$pids" ] || return 0
    read -r -a pid_list <<< "$pids"
    kill "${pid_list[@]}" 2>/dev/null || true
    for ((wait_round = 0; wait_round < 20; wait_round++)); do
        local alive=""
        for pid in "${pid_list[@]}"; do
            kill -0 "$pid" 2>/dev/null && alive="$alive $pid"
        done
        [ -z "$alive" ] && return 0
        sleep 1
    done
    for pid in "${pid_list[@]}"; do
        kill -9 "$pid" 2>/dev/null || true
    done
}

controller_port() {
    local port
    port=$(sed -n '/^system:/,/^[^[:space:]]/ s/^[[:space:]]*addr:[[:space:]]*//p' "$MANAGED_SERVER_DIR/config.yaml" 2>/dev/null | head -n1 | tr -d '"' | tr -d "'")
    case "$port" in
        ''|*[!0-9]*) printf '8888' ;;
        *) printf '%s' "$port" ;;
    esac
}

wait_for_controller_endpoint() {
    local endpoint="$1"
    local attempts="${2:-60}"
    local port
    port=$(controller_port)
    local attempt
    for ((attempt = 0; attempt < attempts; attempt++)); do
        if curl -fsS --max-time 3 "http://127.0.0.1:${port}${endpoint}" >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    return 1
}

verify_controller_api_contract_marker() {
    local port response
    port=$(controller_port)
    response=$(curl -fsS --max-time 5 "http://127.0.0.1:${port}/api/v1/public/build-info" 2>/dev/null || true)
    printf '%s' "$response" | tr -d '[:space:]' | grep -Fq "\"apiContract\":\"${EXPECTED_API_CONTRACT}\""
}

verify_admin_route_contract() {
    local port status method route
    port=$(controller_port)
    while IFS='|' read -r method route; do
        status=$(curl -sS --max-time 5 -o /dev/null -w '%{http_code}' -X "$method" "http://127.0.0.1:${port}${route}" 2>/dev/null || true)
        case "$status" in
            000|404|502|'')
                log_error "Route contract check failed: ${method} ${route} returned ${status:-no-response}." \
                    "路由契约检查失败：${method} ${route} 返回 ${status:-无响应}。"
                return 1
                ;;
        esac
    done <<'EOF'
POST|/api/v1/admin/providers/1/health-check-task
GET|/api/v1/admin/providers/1/ipv6-pool?page=1&pageSize=1
GET|/api/v1/admin/providers/1/ipv6-tunnels
POST|/api/v1/admin/port-mappings/repair
EOF
}

upgrade_server() {
    local legacy_binary=""
    if [ ! -f "$MANAGED_SERVER_BIN" ]; then
        legacy_binary=$(find "$MANAGED_SERVER_DIR" -maxdepth 1 -type f \
            \( -name 'server-allinone-*' -o -name 'server-linux-*' \) 2>/dev/null | head -n1)
    fi

    if [ ! -f "$MANAGED_SERVER_BIN" ] && [ -z "$legacy_binary" ]; then
        log_error "No existing installation was detected; please use the install command for a fresh setup." "未检测到已安装版本，请使用 install 选项进行全新安装。"
        return 1
    fi

    if [ -n "$legacy_binary" ]; then
        log_info "Detected a legacy full-installer binary; it will be migrated during upgrade." "检测到旧版一键安装二进制，升级时将自动迁移。"
    fi
    
    log_info "Starting upgrade to version: $VERSION" "开始升级到版本: $VERSION"

    local web_path
    web_path=$(managed_web_path)
    mkdir -p "$MANAGED_INSTALL_ROOT" "$MANAGED_SERVER_DIR" "$(dirname "$web_path")"
    local upgrade_dir
    upgrade_dir=$(mktemp -d "${MANAGED_INSTALL_ROOT}/.upgrade.XXXXXX") || return 1
    local staged_server_dir="${upgrade_dir}/server"
    local staged_web_dir="${upgrade_dir}/web"
    mkdir -p "$staged_server_dir" "$staged_web_dir"

    log_info "Staging the new controller binary and web assets before downtime..." "正在停机前暂存新主控和 Web 文件..."
    if ! install_server "$staged_server_dir" || ! install_web "$staged_web_dir"; then
        rm -rf "$upgrade_dir"
        log_error "Upgrade staging failed; the running installation was not changed." "升级暂存失败，现有运行环境未被修改。"
        return 1
    fi
    if ! "$staged_server_dir/oneclickvirt-server" --version >/dev/null 2>&1; then
        rm -rf "$upgrade_dir"
        log_error "The staged controller binary failed the version preflight." "暂存的主控二进制未通过版本预检。"
        return 1
    fi

    local binary_next
    binary_next=$(mktemp "${MANAGED_SERVER_BIN}.next.XXXXXX") || {
        rm -rf "$upgrade_dir"
        return 1
    }
    if ! cp "$staged_server_dir/oneclickvirt-server" "$binary_next" || ! chmod 0755 "$binary_next"; then
        rm -f "$binary_next"
        rm -rf "$upgrade_dir"
        return 1
    fi

    local web_next
    web_next=$(mktemp -d "${web_path}.next.XXXXXX") || {
        rm -f "$binary_next"
        rm -rf "$upgrade_dir"
        return 1
    }
    if ! cp -a "$staged_web_dir/." "$web_next/"; then
        rm -f "$binary_next"
        rm -rf "$web_next" "$upgrade_dir"
        return 1
    fi

    local running_pids
    running_pids=$(find_running_server_pids | sort -u | tr '\n' ' ')
    persist_runtime_environment "$running_pids" || {
        rm -f "$binary_next"
        rm -rf "$web_next" "$upgrade_dir"
        return 1
    }

    local pre_upgrade_healthy=false
    if wait_for_controller_endpoint "/api/v1/health" 1; then
        pre_upgrade_healthy=true
    fi

    local existing_binary="$MANAGED_SERVER_BIN"
    [ -f "$existing_binary" ] || existing_binary="$legacy_binary"
    local binary_backup="${MANAGED_SERVER_BIN}.pre-upgrade"
    local web_backup="${web_path}.pre-upgrade"
    rm -f "$binary_backup"
    if [ -n "$existing_binary" ] && [ -f "$existing_binary" ]; then
        cp -a "$existing_binary" "$binary_backup" || {
            rm -f "$binary_next"
            rm -rf "$web_next" "$upgrade_dir"
            return 1
        }
    fi
    rm -rf "$web_backup"
    local service_backup="${MANAGED_SERVICE_FILE}.pre-upgrade"
    local service_existed=false
    local managed_dropin="${MANAGED_SERVICE_FILE}.d/10-oneclickvirt-env.conf"
    local managed_dropin_backup="${managed_dropin}.pre-upgrade"
    local managed_dropin_existed=false
    rm -f "$service_backup"
    rm -f "$managed_dropin_backup"
    if [ -f "$MANAGED_SERVICE_FILE" ]; then
        cp -a "$MANAGED_SERVICE_FILE" "$service_backup" || {
            rm -f "$binary_next" "$binary_backup"
            rm -rf "$web_next" "$upgrade_dir"
            return 1
        }
        service_existed=true
    fi
    if [ -f "$managed_dropin" ]; then
        cp -a "$managed_dropin" "$managed_dropin_backup" || {
            rm -f "$binary_next" "$binary_backup" "$service_backup"
            rm -rf "$web_next" "$upgrade_dir"
            return 1
        }
        managed_dropin_existed=true
    fi

    if systemctl is-active --quiet "$MANAGED_SERVICE_NAME" 2>/dev/null; then
        log_info "Stopping the managed oneclickvirt service..." "正在停止受管的 oneclickvirt 服务..."
        systemctl stop "$MANAGED_SERVICE_NAME" || true
    fi
    stop_running_server_pids "$running_pids"

    local switch_failed=false
    local web_original_moved=false
    local web_replaced=false
    if ! mv -f "$binary_next" "$MANAGED_SERVER_BIN"; then
        switch_failed=true
    fi
    if [ "$switch_failed" = false ]; then
        if [ -d "$web_path" ]; then
            if mv "$web_path" "$web_backup"; then
                web_original_moved=true
            else
                switch_failed=true
            fi
        fi
        if [ "$switch_failed" = false ] && mv "$web_next" "$web_path"; then
            web_replaced=true
        else
            switch_failed=true
        fi
    fi

    if [ "$switch_failed" = false ]; then
        if [ "$service_existed" = true ]; then
            ensure_systemd_environment_dropin || switch_failed=true
        else
            create_systemd_service || switch_failed=true
        fi
    fi

    if [ "$switch_failed" = false ]; then
        systemctl enable "$MANAGED_SERVICE_NAME" >/dev/null 2>&1 || switch_failed=true
    fi
    if [ "$switch_failed" = false ]; then
        systemctl start "$MANAGED_SERVICE_NAME" || switch_failed=true
    fi

    if [ "$switch_failed" = false ] && ! wait_for_controller_endpoint "/api/v1/public/build-info" 60; then
        switch_failed=true
    fi
    if [ "$switch_failed" = false ] && ! verify_controller_api_contract_marker; then
        log_error "The staged controller does not expose the expected API contract: ${EXPECTED_API_CONTRACT}." "暂存主控未提供预期 API 契约：${EXPECTED_API_CONTRACT}。"
        switch_failed=true
    fi
    if [ "$switch_failed" = false ] && ! verify_managed_server_process; then
        log_error "The service started, but systemd is not running the staged controller binary." "服务虽已启动，但 systemd 实际运行的不是本次暂存的主控二进制。"
        switch_failed=true
    fi
    if [ "$switch_failed" = false ] && [ "$pre_upgrade_healthy" = true ] && ! wait_for_controller_endpoint "/api/v1/health" 60; then
        switch_failed=true
    fi
    if [ "$switch_failed" = false ] && ! verify_admin_route_contract; then
        switch_failed=true
    fi

    if [ "$switch_failed" = true ]; then
        log_error "Upgrade verification failed; rolling back controller and web assets." "升级验证失败，正在回滚主控和 Web 文件。"
        systemctl stop "$MANAGED_SERVICE_NAME" >/dev/null 2>&1 || true
        if [ -f "$binary_backup" ]; then
            local rollback_binary
            rollback_binary=$(mktemp "${MANAGED_SERVER_BIN}.rollback.XXXXXX") || true
            if [ -n "$rollback_binary" ]; then
                cp -a "$binary_backup" "$rollback_binary" && chmod 0755 "$rollback_binary" && mv -f "$rollback_binary" "$MANAGED_SERVER_BIN"
                rm -f "$rollback_binary"
            fi
        fi
        if [ "$web_replaced" = true ]; then
            rm -rf "$web_path"
        fi
        if [ "$web_original_moved" = true ] && [ -d "$web_backup" ]; then
            mv "$web_backup" "$web_path"
        fi
        if [ "$service_existed" = true ] && [ -f "$service_backup" ]; then
            cp -a "$service_backup" "$MANAGED_SERVICE_FILE"
            if [ "$managed_dropin_existed" = true ] && [ -f "$managed_dropin_backup" ]; then
                mkdir -p "$(dirname "$managed_dropin")"
                cp -a "$managed_dropin_backup" "$managed_dropin"
            else
                rm -f "$managed_dropin"
            fi
            systemctl daemon-reload >/dev/null 2>&1 || true
        fi
        systemctl start "$MANAGED_SERVICE_NAME" >/dev/null 2>&1 || true
        wait_for_controller_endpoint "/api/v1/public/build-info" 30 || true
        rm -f "$binary_next" "$binary_backup" "$service_backup" "$managed_dropin_backup"
        rm -rf "$web_next" "$web_backup" "$upgrade_dir"
        return 1
    fi

    if [ -n "$legacy_binary" ] && [ "$legacy_binary" != "$MANAGED_SERVER_BIN" ]; then
        rm -f "$legacy_binary"
    fi
    rm -f "$binary_backup" "$service_backup" "$managed_dropin_backup"
    rm -rf "$web_backup" "$upgrade_dir"

    log_success "Upgrade completed successfully." "升级完成！"
    log_info "Version: $VERSION" "版本: $VERSION"
    log_info "Configuration file kept unchanged: ${MANAGED_SERVER_DIR}/config.yaml" "配置文件保持不变: ${MANAGED_SERVER_DIR}/config.yaml"
    log_info "Web path: $web_path" "Web 路径: $web_path"
}

show_service_status() {
    if ! command -v systemctl >/dev/null 2>&1; then
        log_error "systemctl is required to inspect this installation." "查看此安装的状态需要 systemctl。"
        return 1
    fi

    systemctl status "$MANAGED_SERVICE_NAME" --no-pager
}

show_service_logs() {
    local lines=100
    local follow=false

    while [ $# -gt 0 ]; do
        case "$1" in
            -f|--follow)
                follow=true
                shift
                ;;
            -n|--lines)
                if [ $# -lt 2 ] || ! [[ "$2" =~ ^[0-9]+$ ]]; then
                    log_error "--lines requires a non-negative integer." "--lines 需要一个非负整数。"
                    return 1
                fi
                lines="$2"
                shift 2
                ;;
            *)
                log_error "Unknown logs option: $1" "未知的日志选项: $1"
                return 1
                ;;
        esac
    done

    if ! command -v journalctl >/dev/null 2>&1; then
        log_error "journalctl is required to inspect this installation's logs." "查看此安装的日志需要 journalctl。"
        return 1
    fi

    if [ "$follow" = true ]; then
        journalctl -u "$MANAGED_SERVICE_NAME" -n "$lines" -f
    else
        journalctl -u "$MANAGED_SERVICE_NAME" -n "$lines" --no-pager
    fi
}

uninstall_server() {
    local assume_yes=false
    local purge=false

    while [ $# -gt 0 ]; do
        case "$1" in
            -y|--yes)
                assume_yes=true
                shift
                ;;
            --purge)
                purge=true
                shift
                ;;
            *)
                log_error "Unknown uninstall option: $1" "未知的卸载选项: $1"
                return 1
                ;;
        esac
    done

    case "$MANAGED_INSTALL_ROOT" in
        ""|/)
            log_error "Refusing to uninstall from an unsafe installation root: '$MANAGED_INSTALL_ROOT'." "拒绝从不安全的安装根目录卸载: '$MANAGED_INSTALL_ROOT'。"
            return 1
            ;;
    esac

    if [ "$assume_yes" != true ]; then
        if [ "${noninteractive:-false}" = "true" ]; then
            log_error "Non-interactive uninstall requires --yes." "无交互卸载必须指定 --yes。"
            return 1
        fi

        local prompt="Remove the OneClickVirt application"
        [ "$purge" = true ] && prompt="$prompt and all files under $MANAGED_INSTALL_ROOT"
        printf "%s? [y/N]: " "$prompt"
        local confirmation
        read -r confirmation
        case "$confirmation" in
            [Yy]|[Yy][Ee][Ss]) ;;
            *)
                log_info "Uninstall cancelled." "已取消卸载。"
                return 0
                ;;
        esac
    fi

    if command -v systemctl >/dev/null 2>&1; then
        systemctl disable --now "$MANAGED_SERVICE_NAME" >/dev/null 2>&1 || true
    fi
    rm -f "$MANAGED_SERVICE_FILE" "$MANAGED_CLI_LINK"
    if command -v systemctl >/dev/null 2>&1; then
        systemctl daemon-reload >/dev/null 2>&1 || true
        systemctl reset-failed "$MANAGED_SERVICE_NAME" >/dev/null 2>&1 || true
    fi

    if [ "$purge" = true ]; then
        rm -rf "$MANAGED_INSTALL_ROOT"
        log_success "OneClickVirt and its application directory were removed." "OneClickVirt 及其应用目录已删除。"
    else
        rm -f \
            "$MANAGED_INSTALL_ROOT/server/oneclickvirt-server" \
            "$MANAGED_INSTALL_ROOT/server/readme.md"
        find "$MANAGED_INSTALL_ROOT/server" -maxdepth 1 -type f \
            \( -name 'server-allinone-*' -o -name 'server-linux-*' \) -delete 2>/dev/null || true
        rm -rf "$MANAGED_INSTALL_ROOT/web"
        log_success "OneClickVirt was uninstalled; configuration and storage were preserved under $MANAGED_INSTALL_ROOT/server/." \
            "OneClickVirt 已卸载；配置和存储仍保留在 $MANAGED_INSTALL_ROOT/server/ 下。"
    fi

    if [ -n "${WEB_PATH:-}" ] && [ "${WEB_PATH}" != "$MANAGED_INSTALL_ROOT/web" ]; then
        log_warning "The custom web path was not removed: ${WEB_PATH}" "自定义 Web 路径未删除: ${WEB_PATH}"
    fi
    log_warning "Database, reverse-proxy, and TLS resources were not removed because they may be shared." \
        "数据库、反向代理和 TLS 资源可能被共用，因此未被删除。"
}

check_system_resources() {
    if [ "${FORCE_INSTALL:-false}" = "true" ]; then
        log_warning "Skipping resource checks (FORCE_INSTALL=true)" "跳过资源检查（FORCE_INSTALL=true）"
        return 0
    fi

    local has_warning=false

    # ── disk check ──────────────────────────────────────────────────────────
    local min_disk_kb=$((10 * 1024 * 1024))
    local available_disk_kb
    available_disk_kb=$(df -Pk / 2>/dev/null | awk 'NR==2 {print $4}')
    local avail_gb=0
    if [ -n "$available_disk_kb" ] && [ "$available_disk_kb" -lt "$min_disk_kb" ]; then
        avail_gb=$((available_disk_kb / 1024 / 1024))
        has_warning=true
    fi

    # ── memory check (RAM + swap) ──────────────────────────────────────────
    local mem_size
    mem_size=$(get_memory_size)
    if [ -n "$mem_size" ] && [ "$mem_size" -lt 2048 ]; then
        has_warning=true
    fi

    # ── handle warnings ─────────────────────────────────────────────────────
    if [ "$has_warning" = true ]; then
        log_warning "System resources below recommended levels:" "系统资源低于推荐配置:"
        if [ -n "$available_disk_kb" ] && [ "$available_disk_kb" -lt "$min_disk_kb" ]; then
            log_warning "  - Disk: ${avail_gb} GB available, 10 GB recommended" "  - 磁盘: ${avail_gb} GB 可用, 推荐 10 GB"
        fi
        if [ -n "$mem_size" ] && [ "$mem_size" -lt 2048 ]; then
            log_warning "  - Memory (RAM+swap): ${mem_size} MB, 2048 MB recommended" "  - 内存 (RAM+swap): ${mem_size} MB, 推荐 2048 MB"
        fi
        log_warning "Installation may fail or performance may be degraded." "安装可能失败或性能下降。"

        if [ "$noninteractive" = "true" ]; then
            log_error "Resource checks failed. Re-run with FORCE_INSTALL=true to bypass." "资源检查未通过。请设置 FORCE_INSTALL=true 跳过检查。"
            exit 1
        fi

        local confirm=""
        reading "Continue anyway? (y/N): " "是否继续安装？(y/N): " confirm
        case "$confirm" in
            [Yy]*)
                log_warning "Continuing despite resource warnings..." "忽略资源警告，继续安装..."
                ;;
            *)
                log_info "Installation cancelled by user." "用户取消安装。"
                exit 0
                ;;
        esac
    else
        log_success "System resource checks passed." "系统资源检查通过。"
    fi
}

show_info() {
    log_success "OneClickVirt installation completed." "OneClickVirt 安装完成！"
    echo ""
    log_info "Installation summary:" "安装信息："
    log_info "  Version:       $VERSION" "  版本:         $VERSION"
    log_info "  System:        $SYSTEM" "  系统:         $SYSTEM"
    log_info "  Architecture:  $(detect_arch)" "  架构:         $(detect_arch)"
    log_info "  Install path:  $MANAGED_INSTALL_ROOT" "  安装路径:     $MANAGED_INSTALL_ROOT"
    if [ -n "$custom_web_path" ]; then
        log_info "  Web path:      $custom_web_path (custom)" "  Web 路径:     $custom_web_path (自定义)"
    else
        log_info "  Web path:      $(managed_web_path) (default)" "  Web 路径:     $(managed_web_path) (默认)"
    fi
    echo ""
    log_info "Quick usage:" "使用方法："
    log_info "  Start:         systemctl start $MANAGED_SERVICE_NAME" "  启动:         systemctl start $MANAGED_SERVICE_NAME"
    log_info "  Status:        systemctl status $MANAGED_SERVICE_NAME" "  状态:         systemctl status $MANAGED_SERVICE_NAME"
    log_info "  Logs:          journalctl -u $MANAGED_SERVICE_NAME -f" "  日志:         journalctl -u $MANAGED_SERVICE_NAME -f"
    echo ""
    echo -e "${YELLOW}  IMPORTANT — First-Run Setup / 重要 — 首次运行设置:${NC}"
    echo -e "  - Default admin account (if auto-initialized by install_full.sh):"
    echo -e "    默认管理员账户（如果由 install_full.sh 自动初始化）:"
    echo -e "    Username / 用户名:  admin"
    echo -e "    Password / 密码:    Admin123!@#"
    echo -e "  - CHANGE THE PASSWORD after first login! / 首次登录后请修改密码！"
    echo -e "  - Start the service, then visit the web UI to begin. / 启动服务后访问 Web 界面开始使用。"
    echo -e "  - Guide / 使用说明: ${MANAGED_SERVER_DIR}/readme.md"
    echo ""
    log_warning "Review config before starting: ${MANAGED_SERVER_DIR}/config.yaml" "启动前请检查配置文件: ${MANAGED_SERVER_DIR}/config.yaml"
}

env_check() {
    log_info "Starting environment checks..." "开始环境检查..."
    
    # 获取最新版本
    if ! get_latest_version; then
        log_error "Unable to resolve the latest version, installation aborted." "无法获取最新版本，安装终止。"
        exit 1
    fi
    
    detect_system
    check_system_resources
    check_dependencies
    check_cdn_file
    log_success "Environment checks completed." "环境检查完成。"
}

show_help() {
    cat <<EOF
OneClickVirt installer / OneClickVirt 安装脚本

Usage: bash install.sh [COMMAND]
用法: bash install.sh [命令]

Commands:
命令:
    install     Full installation (default)
    install     完整安装（默认）
    env         Check and prepare the environment only
    env         仅检查和准备环境
    upgrade     Upgrade an existing installation
    upgrade     升级已安装版本
    status      Show service status
    status      查看服务状态
    logs        Show service logs (supports --lines N and --follow)
    logs        查看服务日志（支持 --lines N 和 --follow）
    uninstall   Remove the application (supports --yes and --purge)
    uninstall   卸载应用（支持 --yes 和 --purge）
    help        Show this help message
    help        显示此帮助信息

Environment variables:
环境变量:
    CN=true                     Force China mirrors / 强制使用中国镜像
    noninteractive=true         Non-interactive mode / 非交互模式
    FORCE_INSTALL=true          Skip resource checks (disk & memory) / 跳过资源检查
    WEB_PATH=/path              Custom web install path / 自定义 Web 安装路径
    INSTALL_VERSION=v1.0.0      Install a specific version / 指定安装版本
    FORCE_REINSTALL=true        Allow reinstall over an existing install / 允许覆盖已有安装
    CONFIRM_REINSTALL=REINSTALL Required with FORCE_REINSTALL in non-interactive mode / 非交互强制重装确认

Examples / 示例:
    bash install.sh                                      # Install latest / 安装最新版
    bash install.sh env                                  # Environment check / 环境检查
    bash install.sh upgrade                              # Upgrade / 升级
    bash install.sh status                               # Service status / 服务状态
    bash install.sh logs --lines 200                     # Recent logs / 最近日志
    bash install.sh logs --follow                        # Follow logs / 持续查看日志
    bash install.sh uninstall                            # Keep config and storage / 保留配置和存储
    bash install.sh uninstall --purge                    # Remove application data / 删除应用数据
    CN=true bash install.sh                              # Use CN mirrors / 使用中国镜像
    noninteractive=true bash install.sh                  # Non-interactive / 非交互
    FORCE_INSTALL=true bash install.sh                   # Skip resource check / 跳过资源检查
    WEB_PATH=/var/www/html bash install.sh               # Custom web path / 自定义 Web 路径
    INSTALL_VERSION=v1.0.0 bash install.sh               # Specific version / 指定版本
    INSTALL_VERSION=v1.0.0 bash install.sh upgrade       # Upgrade to version / 升级到指定版本
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
            reinstalling=false
            if existing_install_detected; then
                if confirm_existing_install_action; then
                    install_action=0
                else
                    install_action=$?
                fi
                case "$install_action" in
                    2)
                        env_check
                        upgrade_server
                        return $?
                        ;;
                    1)
                        return 1
                        ;;
                    0)
                        reinstalling=true
                        ;;
                esac
            fi
            env_check
            # 处理自定义Web路径（仅在 install 模式下询问）
            if [ "$noninteractive" != "true" ] && [ -z "$custom_web_path" ]; then
                use_custom=""
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
            create_directories || return 1
            running_pids=$(find_running_server_pids | sort -u | tr '\n' ' ')
            persist_runtime_environment "$running_pids" || return 1
            if [ "$reinstalling" = true ]; then
                systemctl stop "$MANAGED_SERVICE_NAME" >/dev/null 2>&1 || true
                stop_running_server_pids "$running_pids"
            fi
            install_server || return 1
            install_web || return 1
            download_config || return 1
            create_readme || return 1
            create_systemd_service || return 1
            create_symlink || return 1
            show_info
            ;;
        "upgrade")
            check_root
            env_check
            # Handle custom web path (interactive prompt, skipped if non-interactive or WEB_PATH env is set)
            # 处理自定义Web路径（交互式询问，非交互模式或已设置 WEB_PATH 环境变量时跳过）
            if [ "$noninteractive" != "true" ] && [ -z "$custom_web_path" ]; then
                use_custom=""
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
            upgrade_server
            ;;
        "status")
            show_service_status
            ;;
        "logs")
            shift
            show_service_logs "$@"
            ;;
        "uninstall"|"remove")
            check_root
            shift
            uninstall_server "$@"
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
