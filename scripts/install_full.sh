#!/bin/bash
# ==============================================================================
# OneClickVirt Full Installation Script (Bare Metal / VPS)
# Installs: MySQL/MariaDB + Reverse Proxy (Caddy/Nginx/OpenResty) + App
# Source: https://github.com/oneclickvirt/oneclickvirt
# Version: 1.0.0
# ==============================================================================
set -uo pipefail

export noninteractive="${noninteractive:-${NONINTERACTIVE:-false}}"
export NONINTERACTIVE="$noninteractive"
VERSION=""
REPO="oneclickvirt/oneclickvirt"
BASE_URL=""
INSTALL_DIR="/opt/oneclickvirt"
SERVER_DIR="${INSTALL_DIR}/server"
WEB_DIR="${INSTALL_DIR}/web"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

OS="unknown"
OS_VERSION=""
OS_LIKE=""
OS_FAMILY="unknown"
KERNEL_NAME="$(uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]')"
PKG_MANAGER=""
SERVICE_MANAGER="none"
DB_SERVICE=""
MYSQL_CNF_DIR="${MYSQL_CNF_DIR:-/etc/mysql/conf.d}"
DB_CONFIG_SOURCE="${DB_CONFIG_SOURCE:-}"
DB_CONFIG_SOURCE_PATH=""
DB_CONFIG_SOURCE_EPHEMERAL="false"
DB_CONFIG_CHANGED="false"
NGINX_CONF_DIR="/etc/nginx/conf.d"
CADDY_CONFIG_DIR="/etc/caddy"
CADDY_LOG_DIR="/var/log/caddy"
DOWNLOAD_RETRIES="${DOWNLOAD_RETRIES:-4}"
DB_WAIT_TIMEOUT="${DB_WAIT_TIMEOUT:-180}"
AUTO_DB_FALLBACK="${AUTO_DB_FALLBACK:-true}"
# Service-unit roots are overridable for isolated tests and staging installs.
# Production defaults retain the native locations on each supported OS.
SYSTEMD_UNIT_DIR="${SYSTEMD_UNIT_DIR:-/etc/systemd/system}"
OPENRC_INIT_DIR="${OPENRC_INIT_DIR:-/etc/init.d}"
FREEBSD_RC_DIR="${FREEBSD_RC_DIR:-/usr/local/etc/rc.d}"
SYSV_INIT_DIR="${SYSV_INIT_DIR:-/etc/init.d}"

# ---- Color helpers ----
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'
BLUE='\033[0;34m'; CYAN='\033[0;36m'; NC='\033[0m'

log_info()    { echo -e "${BLUE}[INFO]${NC} $1"; [[ -n "${2:-}" ]] && echo -e "${BLUE}[INFO]${NC} $2"; }
log_success() { echo -e "${GREEN}[OK]${NC} $1"; [[ -n "${2:-}" ]] && echo -e "${GREEN}[OK]${NC} $2"; }
log_warning() { echo -e "${YELLOW}[WARN]${NC} $1"; [[ -n "${2:-}" ]] && echo -e "${YELLOW}[WARN]${NC} $2"; }
log_error()   { echo -e "${RED}[ERROR]${NC} $1" >&2; [[ -n "${2:-}" ]] && echo -e "${RED}[ERROR]${NC} $2" >&2; }

sql_escape() {
    printf "%s" "$1" | sed "s/'/''/g"
}

json_escape() {
    local s="$1"
    s=${s//\\/\\\\}
    s=${s//\"/\\\"}
    s=${s//$'\n'/\\n}
    s=${s//$'\r'/\\r}
    s=${s//$'\t'/\\t}
    s=${s//$'\b'/\\b}
    s=${s//$'\f'/\\f}
    printf "%s" "$s"
}

have_cmd() {
    command -v "$1" >/dev/null 2>&1
}

run_retry() {
    local attempts="$1" delay="$2"; shift 2
    local n=1 rc=0
    while true; do
        "$@" && return 0
        rc=$?
        if [[ "$n" -ge "$attempts" ]]; then
            return "$rc"
        fi
        log_warning "Command failed (attempt ${n}/${attempts}), retrying in ${delay}s: $*" "命令执行失败（第 ${n}/${attempts} 次），${delay}s 后重试: $*"
        sleep "$delay"
        n=$((n + 1))
        [[ "$delay" -lt 30 ]] && delay=$((delay * 2))
    done
}

download_file() {
    local url="$1" dest="$2"
    run_retry "$DOWNLOAD_RETRIES" 3 curl -fL --connect-timeout 15 --max-time 180 "$url" -o "$dest"
}

version_major() {
    printf "%s" "${1:-0}" | sed -E 's/^([0-9]+).*/\1/'
}

is_linux_kernel() {
    [[ "$KERNEL_NAME" == "linux" ]]
}

# ---- Usage ----
usage() {
    cat << 'EOF'
Usage / 用法: bash install_full.sh [OPTIONS] / [选项]

Options / 选项:
  --db-type TYPE          Database type: mysql (default) or mariadb
                          数据库类型: mysql（默认）或 mariadb
  --db-password PASS      Database root password (auto-generated if not set)
                          数据库 root 密码（未设置则自动生成）
  --external-db           Use an external database (skip local DB install)
                          使用外部数据库（跳过本地数据库安装）
  --db-host HOST          External DB host (implies --external-db)
                          外部数据库主机（隐含 --external-db）
  --db-port PORT          External DB port (default: 3306)
                          外部数据库端口（默认: 3306）
  --db-name NAME          External DB name (default: oneclickvirt)
                          外部数据库名称（默认: oneclickvirt）
  --db-user USER          External DB user (default: oneclickvirt)
                          外部数据库用户（默认: oneclickvirt）
  --db-pass PASS          External DB password / 外部数据库密码
  --admin-email EMAIL     Admin email for auto-init (default: admin@oneclickvirt.local)
                          管理员邮箱（默认: admin@oneclickvirt.local）
  --proxy TYPE            Reverse proxy: caddy, nginx, openresty (default: caddy)
                          反向代理: caddy, nginx, openresty（默认: caddy）
  --domain DOMAIN         Domain name or IP (e.g. panel.example.com, 1.2.3.4)
                          域名或 IP（如 panel.example.com, 1.2.3.4）
                          Prefix with https:// to enable TLS, http:// to disable.
                          添加 https:// 前缀以启用 TLS，http:// 前缀以禁用。
                          If omitted in interactive mode, auto-detects public/private IP
                          交互模式下如省略，将自动检测公网/内网 IP
                          and prompts for choice (public IPv4 / localhost / private IPv4).
                          并提示选择（公网 IPv4 / 本地回环 / 内网 IPv4）。
  --email EMAIL           Email for TLS certificate notifications
                          TLS 证书通知邮箱
  --tls METHOD            TLS method: letsencrypt, zerossl, selfsigned, off
                          TLS 方式: letsencrypt, zerossl, selfsigned, off
                          TLS requires a real domain name (not bare IP or localhost).
                          TLS 需要真实域名（不能是裸 IP 或 localhost）。
  --non-interactive       Run without prompts / 非交互模式运行
  --force                 Skip system resource checks (disk & memory)
                          跳过系统资源检查（磁盘和内存）
  --version VERSION       Specific version to install (default: latest)
                          指定安装版本（默认: 最新）
  --db-wait-timeout SEC   Seconds to wait for local DB readiness (default: 180)
                          等待本地数据库就绪的秒数（默认: 180）
  --no-db-fallback        Do not auto-fallback from MySQL to MariaDB-compatible backend
                          禁止 MySQL 自动回退到 MariaDB 兼容后端
  --help                  Show this help / 显示此帮助

Examples / 示例:
  # Interactive mode — just press Enter at each prompt
  # 交互模式 — 每次提示按回车即可
  bash install_full.sh

  # Non-interactive with local DB + domain + TLS
  # 非交互模式：本地数据库 + 域名 + TLS
  bash install_full.sh --non-interactive --domain https://panel.example.com --email admin@example.com

  # Non-interactive with a public IP (TLS disabled, local DB)
  # 非交互模式：使用公网 IP（TLS 已禁用，本地数据库）
  bash install_full.sh --non-interactive --domain http://1.2.3.4

  # Use external database (separated deployment / 分离式部署)
  bash install_full.sh --external-db --db-host 10.0.0.5 --db-name oneclickvirt --db-user ocv --db-pass mypass

  # Non-interactive with external DB / 非交互模式：外部数据库
  bash install_full.sh --non-interactive --domain https://panel.example.com --email admin@example.com \\
      --external-db --db-host 10.0.0.5 --db-port 3306 --db-name oneclickvirt --db-user ocv --db-pass mypass
EOF
    exit 0
}

# ---- Argument parsing ----
DB_TYPE="mysql"
DB_PASSWORD=""
PROXY="caddy"
DOMAIN=""
EMAIL=""
TLS_METHOD="letsencrypt"
NONINTERACTIVE="${noninteractive:-false}"
FORCE_INSTALL="false"
INSTALL_VERSION=""
DOMAIN_PROTO_DETECTED=""
TLS_EXPLICIT="false"
# External database (separated deployment / 分离式部署)
EXTERNAL_DB="false"
DB_HOST=""
DB_PORT="3306"
DB_NAME_EXT="oneclickvirt"
DB_USER_EXT="oneclickvirt"
DB_PASS_EXT=""
# Auto-init admin credentials
ADMIN_USER="admin"
ADMIN_PASS="Admin123!@#"
ADMIN_EMAIL="admin@oneclickvirt.local"

# Normalize domain: strip https:// or http:// prefix, auto-detect TLS preference
# Returns via DOMAIN and DOMAIN_PROTO_DETECTED globals
normalize_domain() {
    local raw="$1"
    DOMAIN_PROTO_DETECTED=""
    if [[ "$raw" == https://* ]]; then
        DOMAIN="${raw#https://}"
        DOMAIN_PROTO_DETECTED="https"
    elif [[ "$raw" == http://* ]]; then
        DOMAIN="${raw#http://}"
        DOMAIN_PROTO_DETECTED="http"
    else
        DOMAIN="$raw"
    fi
    # Strip trailing slash
    DOMAIN="${DOMAIN%/}"
}

# Domain text is embedded directly in Caddyfile/Nginx configuration. Reject
# control characters and configuration delimiters before any template is
# rendered; otherwise a malformed CLI/env value could inject directives or
# silently produce an unreachable proxy. Colons remain allowed for IPv6
# literals and optional host:port forms handled by the proxy itself.
validate_domain() {
    local value="${1:-}"
    if [[ -z "$value" || "$value" == *$'\n'* || "$value" == *$'\r'* || "$value" == *$'\t'* || "$value" =~ [\{\}\;\#\"\\] || "$value" == */* ]]; then
        log_error "Domain must be a non-empty host/IP without whitespace or configuration delimiters." \
            "域名必须是非空主机名/IP，不能包含空白或配置分隔符。"
        return 1
    fi
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --db-type)       DB_TYPE="$2"; shift 2 ;;
        --db-password)   DB_PASSWORD="$2"; shift 2 ;;
        --proxy)         PROXY="$2"; shift 2 ;;
        --domain)        normalize_domain "$2"; shift 2 ;;
        --email)         EMAIL="$2"; shift 2 ;;
        --tls)           TLS_METHOD="$2"; TLS_EXPLICIT="true"; shift 2 ;;
        --non-interactive) NONINTERACTIVE="true"; shift ;;
        --force)          FORCE_INSTALL="true"; shift ;;
        --external-db)    EXTERNAL_DB="true"; shift ;;
        --db-host)        DB_HOST="$2"; EXTERNAL_DB="true"; shift 2 ;;
        --db-port)        DB_PORT="$2"; shift 2 ;;
        --db-name)        DB_NAME_EXT="$2"; shift 2 ;;
        --db-user)        DB_USER_EXT="$2"; shift 2 ;;
        --db-pass)        DB_PASS_EXT="$2"; shift 2 ;;
        --admin-email)    ADMIN_EMAIL="$2"; shift 2 ;;
        --version)        INSTALL_VERSION="$2"; shift 2 ;;
        --db-wait-timeout) DB_WAIT_TIMEOUT="$2"; shift 2 ;;
        --no-db-fallback) AUTO_DB_FALLBACK="false"; shift ;;
        --help)           usage ;;
        *) log_error "Unknown option: $1" "未知选项: $1"; usage ;;
    esac
done

# Engine labels are hints only. Normalize spelling before validation and again
# after the interactive prompt so `MYSQL`, `MariaDB`, and accidental padding do
# not bypass the live server detection path.
normalize_db_type() {
    DB_TYPE="$(printf '%s' "${DB_TYPE:-}" | tr '[:upper:]' '[:lower:]' | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')"
}
validate_db_type() {
    case "${DB_TYPE:-}" in
        mysql|mariadb) return 0 ;;
        *)
            log_error "Unsupported database type '${DB_TYPE:-}' (use mysql or mariadb)." \
                "不支持的数据库类型 '${DB_TYPE:-}'（请使用 mysql 或 mariadb）。"
            return 1
            ;;
    esac
}
normalize_db_type
validate_db_type || exit 1

# Apply protocol-detected TLS from --domain prefix (only if --tls not explicitly set)
if [[ "$TLS_EXPLICIT" != "true" && -n "$DOMAIN_PROTO_DETECTED" ]]; then
    if [[ "$DOMAIN_PROTO_DETECTED" == "https" ]]; then
        TLS_METHOD="letsencrypt"
        log_info "Detected https:// prefix — TLS enabled (${TLS_METHOD})" "检测到 https:// 前缀 — TLS 已启用 (${TLS_METHOD})"
    elif [[ "$DOMAIN_PROTO_DETECTED" == "http" ]]; then
        TLS_METHOD="off"
        log_info "Detected http:// prefix — TLS disabled" "检测到 http:// 前缀 — TLS 已禁用"
    fi
fi

# ---- Validate options ----
VALID_DB_TYPES="mysql mariadb"
VALID_PROXIES="caddy nginx openresty"
VALID_TLS="letsencrypt zerossl selfsigned off"

if ! echo "$VALID_DB_TYPES" | grep -qw "$DB_TYPE"; then
    log_error "Invalid database type: $DB_TYPE (use: $VALID_DB_TYPES)"
    exit 1
fi
if ! echo "$VALID_PROXIES" | grep -qw "$PROXY"; then
    log_error "Invalid proxy: $PROXY (use: $VALID_PROXIES)"
    exit 1
fi
if ! echo "$VALID_TLS" | grep -qw "$TLS_METHOD"; then
    log_error "Invalid TLS method: $TLS_METHOD (use: $VALID_TLS)"
    exit 1
fi

# ---- Prerequisites ----
check_root() {
    if [[ $EUID -ne 0 ]]; then
        log_error "This script must be run as root (use sudo)." "此脚本必须以 root 身份运行（请使用 sudo）。"
        exit 1
    fi
}

check_system_resources() {
    if [[ "${SKIP_RESOURCE_CHECK:-false}" == "true" || "${FORCE_INSTALL:-false}" == "true" ]]; then
        log_warning "Skipping disk/memory resource checks (SKIP_RESOURCE_CHECK or --force)" "跳过磁盘/内存资源检查（SKIP_RESOURCE_CHECK 或 --force）"
        return 0
    fi

    local has_disk_warning=false has_mem_warning=false
    local avail_gb=0 ram_gb=0 swap_gb=0 combined_gb=0

    # ── disk check ──────────────────────────────────────────────────────────
    local min_disk_kb=$((10 * 1024 * 1024))
    local available_disk_kb
    available_disk_kb=$(df -Pk / 2>/dev/null | awk 'NR==2 {print $4}')
    if [[ -n "$available_disk_kb" && "$available_disk_kb" -lt "$min_disk_kb" ]]; then
        avail_gb=$((available_disk_kb / 1024 / 1024))
        has_disk_warning=true
    fi

    # ── memory check (MemTotal + SwapTotal) ──────────────────────────────────
    local min_combined_kb=$((2 * 1024 * 1024))  # 2 GB combined (RAM + swap)
    local memtotal_kb=0 swaptotal_kb=0
    if [[ -r /proc/meminfo ]]; then
        memtotal_kb=$(awk '/^MemTotal:/ {print $2}' /proc/meminfo)
        swaptotal_kb=$(awk '/^SwapTotal:/ {print $2}' /proc/meminfo)
    elif command -v free &>/dev/null; then
        memtotal_kb=$(free -k | awk '/^Mem:/ {print $2}')
        swaptotal_kb=$(free -k | awk '/^Swap:/ {print $2}')
    fi
    memtotal_kb=${memtotal_kb:-0}
    swaptotal_kb=${swaptotal_kb:-0}
    local combined_kb=$((memtotal_kb + swaptotal_kb))
    if [[ "$combined_kb" -gt 0 ]]; then
        combined_gb=$(awk "BEGIN {printf \"%.1f\", $combined_kb / 1024 / 1024}")
    fi
    if [[ "$combined_kb" -gt 0 && "$combined_kb" -lt "$min_combined_kb" ]]; then
        ram_gb=$(awk "BEGIN {printf \"%.1f\", $memtotal_kb / 1024 / 1024}")
        swap_gb=$(awk "BEGIN {printf \"%.1f\", $swaptotal_kb / 1024 / 1024}")
        has_mem_warning=true
    fi

    # ── handle warnings ─────────────────────────────────────────────────────
    if [[ "$has_disk_warning" == "true" || "$has_mem_warning" == "true" ]]; then
        log_warning "System resources below recommended levels:" "系统资源低于推荐配置:"
        if [[ "$has_disk_warning" == "true" ]]; then
            log_warning "  - Disk: ${avail_gb} GB available, 10 GB recommended" "  - 磁盘: ${avail_gb} GB 可用, 推荐 10 GB"
        fi
        if [[ "$has_mem_warning" == "true" ]]; then
            log_warning "  - Memory: ${ram_gb} GB RAM + ${swap_gb} GB swap = ${combined_gb} GB total, 2 GB recommended" "  - 内存: ${ram_gb} GB RAM + ${swap_gb} GB swap = ${combined_gb} GB 总计, 推荐 2 GB"
        fi
        log_warning "Installation may fail or performance may be degraded." "安装可能失败或性能下降。"

        if [[ "${NONINTERACTIVE:-false}" == "true" ]]; then
            log_error "Resource checks failed. Re-run with --force to bypass, or set SKIP_RESOURCE_CHECK=true." "资源检查未通过。请使用 --force 跳过检查，或设置 SKIP_RESOURCE_CHECK=true。"
            exit 1
        fi

        # Interactive: ask for confirmation / 交互式：请求确认
        read -r -p "$(echo -e "${YELLOW}[WARN]${NC} Continue anyway? / 是否继续安装? (y/N): ")" confirm
        case "$confirm" in
            [Yy]*)
                log_warning "Continuing installation despite resource warnings..." "忽略资源警告，继续安装..."
                ;;
            *)
                log_info "Installation cancelled by user." "用户取消安装。"
                exit 0
                ;;
        esac
        return 0
    fi

    log_success "System resource checks passed." "系统资源检查通过。"
}

detect_arch() {
    case $(uname -m) in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) log_error "Unsupported architecture: $(uname -m)" "不支持的架构: $(uname -m)"; exit 1 ;;
    esac
}

# ── IP detection ─────────────────────────────────────────────────────────────
detect_public_ipv4() {
    # Try multiple public-IP echo services, return first successful result
    local svc
    for svc in \
        "https://ifconfig.me" \
        "https://ipinfo.io/ip" \
        "https://icanhazip.com" \
        "https://api.ipify.org" \
        "https://checkip.amazonaws.com"; do
        local ip
        ip=$(curl -4 -s --connect-timeout 5 --max-time 10 "$svc" 2>/dev/null | tr -d '[:space:]')
        if [[ -n "$ip" && "$ip" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            echo "$ip"
            return 0
        fi
    done
    return 1
}

detect_private_ipv4() {
    # Return the first non-loopback private IPv4 address
    local ip
    if command -v hostname &>/dev/null; then
        ip=$(hostname -I 2>/dev/null | tr ' ' '\n' | grep -E '^(10\.|172\.(1[6-9]|2[0-9]|3[0-1])\.|192\.168\.)' | head -1)
    fi
    if [[ -z "$ip" ]] && command -v ip &>/dev/null; then
        ip=$(ip -4 addr show scope global 2>/dev/null | awk '/inet / {split($2, a, "/"); if (a[1] !~ /^127[.]/) {print a[1]; exit}}')
    fi
    if [[ -z "$ip" ]] && command -v ifconfig &>/dev/null; then
        ip=$(ifconfig 2>/dev/null | grep -Eo 'inet (addr:)?[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | grep -Eo '[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | grep -v '^127\.' | head -1)
    fi
    if [[ -n "$ip" ]]; then
        echo "$ip"
        return 0
    fi
    return 1
}

detect_package_manager() {
    if have_cmd apt-get; then PKG_MANAGER="apt"
    elif have_cmd dnf; then PKG_MANAGER="dnf"
    elif have_cmd yum; then PKG_MANAGER="yum"
    elif have_cmd zypper; then PKG_MANAGER="zypper"
    elif have_cmd pacman; then PKG_MANAGER="pacman"
    elif have_cmd apk; then PKG_MANAGER="apk"
    elif have_cmd pkg; then PKG_MANAGER="pkg"
    elif have_cmd pkg_add; then PKG_MANAGER="pkg_add"
    elif have_cmd pkgin; then PKG_MANAGER="pkgin"
    elif have_cmd brew; then PKG_MANAGER="brew"
    else PKG_MANAGER=""
    fi
}

detect_service_manager() {
    if have_cmd systemctl && { [[ -d /run/systemd/system ]] || systemctl is-system-running >/dev/null 2>&1; }; then
        SERVICE_MANAGER="systemd"
    elif have_cmd rc-service; then
        SERVICE_MANAGER="openrc"
    elif have_cmd rcctl; then
        SERVICE_MANAGER="rcctl"
    elif have_cmd service; then
        case "$KERNEL_NAME" in
            freebsd|dragonfly) SERVICE_MANAGER="freebsd-service" ;;
            *) SERVICE_MANAGER="sysv-service" ;;
        esac
    else
        SERVICE_MANAGER="none"
    fi
}

detect_os() {
    if [[ -f /etc/os-release ]]; then
        # shellcheck source=/dev/null
        . /etc/os-release
        OS="${ID:-unknown}"
        OS_VERSION="${VERSION_ID:-}"
        OS_LIKE="${ID_LIKE:-}"
    elif [[ -f /etc/debian_version ]]; then
        OS="debian"
        OS_VERSION="$(cat /etc/debian_version 2>/dev/null || true)"
    elif [[ -f /etc/redhat-release ]]; then
        OS="rhel"
        OS_VERSION="$(sed -nE 's/.* ([0-9]+([.][0-9]+)?).*/\1/p' /etc/redhat-release 2>/dev/null | head -1)"
    else
        case "$KERNEL_NAME" in
            freebsd) OS="freebsd" ;;
            openbsd) OS="openbsd" ;;
            netbsd) OS="netbsd" ;;
            dragonfly) OS="dragonflybsd" ;;
            darwin) OS="darwin" ;;
            *) OS="unknown" ;;
        esac
    fi
    OS=$(printf "%s" "$OS" | tr '[:upper:]' '[:lower:]')
    OS_LIKE=$(printf "%s" "$OS_LIKE" | tr '[:upper:]' '[:lower:]')

    case "$OS:$OS_LIKE" in
        ubuntu:*|debian:*|raspbian:*|linuxmint:*|pop:*|*:debian*)
            OS_FAMILY="debian" ;;
        centos:*|rhel:*|almalinux:*|rocky:*|fedora:*|amzn:*|ol:*|opencloudos:*|*:rhel*|*:fedora*)
            OS_FAMILY="rhel" ;;
        opensuse*:|sles:*|suse:*|*:suse*)
            OS_FAMILY="suse" ;;
        arch:*|manjaro:*|endeavouros:*|*:arch*)
            OS_FAMILY="arch" ;;
        alpine:*)
            OS_FAMILY="alpine" ;;
        freebsd:*|openbsd:*|netbsd:*|dragonflybsd:*)
            OS_FAMILY="bsd" ;;
        darwin:*)
            OS_FAMILY="darwin" ;;
        *)
            OS_FAMILY="unknown" ;;
    esac

    detect_package_manager
    detect_service_manager

    case "$OS_FAMILY" in
        debian)
            MYSQL_CNF_DIR="/etc/mysql/conf.d"
            NGINX_CONF_DIR="/etc/nginx/sites-available"
            ;;
        rhel|suse|arch|alpine)
            MYSQL_CNF_DIR="/etc/my.cnf.d"
            NGINX_CONF_DIR="/etc/nginx/conf.d"
            ;;
        bsd)
            MYSQL_CNF_DIR="/usr/local/etc/mysql/conf.d"
            NGINX_CONF_DIR="/usr/local/etc/nginx/conf.d"
            CADDY_CONFIG_DIR="/usr/local/etc/caddy"
            CADDY_LOG_DIR="/var/log/caddy"
            ;;
    esac

    if [[ -z "$PKG_MANAGER" ]]; then
        log_error "Unable to detect a supported package manager." "无法识别受支持的包管理器。"
        exit 1
    fi

    log_success "Detected OS: ${OS} ${OS_VERSION:-unknown} (${OS_FAMILY}, pkg=${PKG_MANAGER}, svc=${SERVICE_MANAGER})" \
        "检测到操作系统: ${OS} ${OS_VERSION:-unknown}（${OS_FAMILY}, 包管理=${PKG_MANAGER}, 服务=${SERVICE_MANAGER}）"
}

pkg_update() {
    case "$PKG_MANAGER" in
        apt) DEBIAN_FRONTEND=noninteractive run_retry 3 3 apt-get update -qq ;;
        dnf) run_retry 3 3 dnf -y makecache ;;
        yum) run_retry 3 3 yum -y makecache ;;
        zypper) run_retry 3 3 zypper --non-interactive refresh ;;
        pacman) run_retry 3 3 pacman -Sy --noconfirm ;;
        apk) run_retry 3 3 apk update ;;
        pkg) run_retry 3 3 pkg update -f ;;
        pkgin) run_retry 3 3 pkgin -y update ;;
        pkg_add|brew) return 0 ;;
        *) return 1 ;;
    esac
}

pkg_install() {
    [[ "$#" -eq 0 ]] && return 0
    case "$PKG_MANAGER" in
        apt)
            DEBIAN_FRONTEND=noninteractive run_retry 3 3 apt-get install -y -qq \
                -o Dpkg::Options::="--force-confdef" \
                -o Dpkg::Options::="--force-confold" "$@"
            ;;
        dnf) run_retry 3 3 dnf -y install "$@" ;;
        yum) run_retry 3 3 yum -y install "$@" ;;
        zypper) run_retry 3 3 zypper --non-interactive install -y "$@" ;;
        pacman) run_retry 3 3 pacman -S --noconfirm --needed "$@" ;;
        apk) run_retry 3 3 apk add --no-cache "$@" ;;
        pkg) run_retry 3 3 pkg install -y "$@" ;;
        pkg_add) run_retry 3 3 pkg_add -I "$@" ;;
        pkgin) run_retry 3 3 pkgin -y install "$@" ;;
        brew) run_retry 3 3 brew install "$@" ;;
        *) log_error "Unsupported package manager: ${PKG_MANAGER}" "不支持的包管理器: ${PKG_MANAGER}"; return 1 ;;
    esac
}

install_dependencies() {
    log_info "Installing base dependencies..." "正在安装基础依赖..."
    pkg_update || log_warning "Package index update failed; continuing with install attempt." "包索引更新失败，将继续尝试安装。"
    local deps=()
    case "$PKG_MANAGER" in
        apk) deps=(curl wget tar gzip unzip ca-certificates iproute2 procps) ;;
        pacman) deps=(curl wget tar gzip unzip ca-certificates iproute2 procps-ng) ;;
        pkg|pkg_add|pkgin) deps=(curl wget gtar gzip unzip ca_root_nss) ;;
        brew) deps=(curl wget gnu-tar gzip unzip) ;;
        *) deps=(curl wget tar gzip unzip ca-certificates) ;;
    esac
    pkg_install "${deps[@]}" || {
        log_error "Failed to install base dependencies." "基础依赖安装失败。"
        return 1
    }
    log_success "Base dependencies installed." "基础依赖安装完成。"
}

service_exists() {
    local name="$1"
    case "$SERVICE_MANAGER" in
        systemd)
            systemctl list-unit-files "${name}.service" --no-legend 2>/dev/null | grep -q . || \
                systemctl cat "$name" >/dev/null 2>&1
            ;;
        openrc)
            rc-service -l 2>/dev/null | grep -qx "$name"
            ;;
        rcctl)
            rcctl ls all 2>/dev/null | grep -qx "$name"
            ;;
        freebsd-service|sysv-service)
            service -l 2>/dev/null | grep -qx "$name" || [[ -x "/etc/init.d/${name}" ]] || [[ -x "/usr/local/etc/rc.d/${name}" ]]
            ;;
        none)
            return 1
            ;;
    esac
}

service_reset_failed() {
    local name="$1"
    [[ "$SERVICE_MANAGER" == "systemd" ]] && systemctl reset-failed "$name" >/dev/null 2>&1 || true
}

service_enable() {
    local name="$1"
    case "$SERVICE_MANAGER" in
        systemd) systemctl enable "$name" >/dev/null 2>&1 ;;
        openrc) rc-update add "$name" default >/dev/null 2>&1 ;;
        rcctl) rcctl enable "$name" >/dev/null 2>&1 ;;
        freebsd-service)
            sysrc "${name}_enable=YES" >/dev/null 2>&1
            ;;
        sysv-service|none) return 0 ;;
    esac
}

service_start() {
    local name="$1"
    service_reset_failed "$name"
    case "$SERVICE_MANAGER" in
        systemd) systemctl start "$name" ;;
        openrc) rc-service "$name" start ;;
        rcctl) rcctl start "$name" ;;
        freebsd-service|sysv-service) service "$name" start ;;
        none) return 1 ;;
    esac
}

service_restart() {
    local name="$1"
    service_reset_failed "$name"
    case "$SERVICE_MANAGER" in
        systemd) systemctl restart "$name" ;;
        openrc) rc-service "$name" restart ;;
        rcctl) rcctl restart "$name" ;;
        freebsd-service|sysv-service) service "$name" restart ;;
        none) return 1 ;;
    esac
}

service_reload_or_restart() {
    local name="$1"
    service_reset_failed "$name"
    case "$SERVICE_MANAGER" in
        systemd) systemctl reload "$name" 2>/dev/null || systemctl restart "$name" ;;
        openrc) rc-service "$name" reload 2>/dev/null || rc-service "$name" restart ;;
        rcctl) rcctl reload "$name" 2>/dev/null || rcctl restart "$name" ;;
        freebsd-service|sysv-service) service "$name" reload 2>/dev/null || service "$name" restart ;;
        none) return 1 ;;
    esac
}

service_is_active() {
    local name="$1"
    case "$SERVICE_MANAGER" in
        systemd) systemctl is-active --quiet "$name" ;;
        openrc) rc-service "$name" status >/dev/null 2>&1 ;;
        rcctl) rcctl check "$name" >/dev/null 2>&1 ;;
        freebsd-service|sysv-service) service "$name" status >/dev/null 2>&1 ;;
        none) return 1 ;;
    esac
}

service_hint() {
    local name="$1"
    case "$SERVICE_MANAGER" in
        systemd) printf "systemctl status %s  or  journalctl -u %s -n 50" "$name" "$name" ;;
        openrc) printf "rc-service %s status  or  tail -n 80 /var/log/mysql/*.err /var/log/mysqld*.log" "$name" ;;
        rcctl) printf "rcctl check %s  or  tail -n 80 /var/log/mysql*.log" "$name" ;;
        freebsd-service) printf "service %s status  or  tail -n 80 /var/db/mysql/*.err /var/log/mysql*.log" "$name" ;;
        sysv-service) printf "service %s status  or  tail -n 80 /var/log/mysql/*.err /var/log/mysqld*.log" "$name" ;;
        none) printf "no service manager detected; check process logs under %s" "$INSTALL_DIR" ;;
    esac
}

db_service_candidates() {
    case "$DB_TYPE" in
        mariadb)
            printf "%s\n" mariadb mysql mysql-server mysqld
            ;;
        mysql)
            printf "%s\n" mysql mysqld mysql-server mariadb
            ;;
    esac
}

select_db_service() {
    local candidate
    DB_SERVICE=""
    while IFS= read -r candidate; do
        if service_exists "$candidate"; then
            DB_SERVICE="$candidate"
            break
        fi
    done < <(db_service_candidates)

    if [[ -z "$DB_SERVICE" ]]; then
        DB_SERVICE="$(db_service_candidates | head -1)"
        log_warning "Could not verify database service name, using candidate: ${DB_SERVICE}" "无法确认数据库服务名，使用候选服务: ${DB_SERVICE}"
    else
        log_info "Database service selected: ${DB_SERVICE}" "已选择数据库服务: ${DB_SERVICE}"
    fi
}

db_client() {
    if [[ "$DB_TYPE" == "mariadb" ]] && have_cmd mariadb; then
        printf "mariadb"
    elif have_cmd mysql; then
        printf "mysql"
    elif have_cmd mariadb; then
        printf "mariadb"
    else
        return 1
    fi
}

db_admin_client() {
    if [[ "$DB_TYPE" == "mariadb" ]] && have_cmd mariadb-admin; then
        printf "mariadb-admin"
    elif have_cmd mysqladmin; then
        printf "mysqladmin"
    elif have_cmd mariadb-admin; then
        printf "mariadb-admin"
    else
        return 1
    fi
}

db_ping() {
    db_query_root 'SELECT 1' >/dev/null 2>&1
}

# mysqladmin ping reports success even for Access denied. Readiness and detection
# require an authenticated query against this local daemon, not a client label.
db_query_root() {
    local sql="$1" client preferred
    preferred=$(db_client 2>/dev/null || true)
    [[ -z "$preferred" ]] && return 1

    # Package managers frequently install both mysql and mariadb client names,
    # and a stale engine hint can select a compatibility binary that is not
    # usable on this host.  Try the preferred client first, then the other
    # available client against the same authenticated socket/TCP endpoint.  A
    # client label is never evidence of the server engine.
    local -a clients=("$preferred")
    [[ "$preferred" == mysql ]] || clients+=(mysql)
    [[ "$preferred" == mariadb ]] || clients+=(mariadb)
    for client in "${clients[@]}"; do
        have_cmd "$client" || continue
        printf '%s\n' "$sql" | "$client" --protocol=socket --connect-timeout=5 -u root -N -B 2>/dev/null && return 0
        if [[ -n "${DB_PASSWORD:-}" ]]; then
            printf '%s\n' "$sql" | MYSQL_PWD="$DB_PASSWORD" "$client" --protocol=socket --connect-timeout=5 -u root -N -B 2>/dev/null && return 0
            printf '%s\n' "$sql" | MYSQL_PWD="$DB_PASSWORD" "$client" --protocol=tcp -h 127.0.0.1 --connect-timeout=5 -u root -N -B 2>/dev/null && return 0
        fi
    done
    return 1
}

db_exec_root() {
    # sql_escape uses SQL-standard doubled quotes. Scope this to the temporary
    # admin session so literal backslashes survive either engine's defaults.
    # The global mode and application runtime sessions remain unchanged.
    db_query_root "SET SESSION sql_mode=CONCAT_WS(',', NULLIF(@@SESSION.sql_mode, ''), 'NO_BACKSLASH_ESCAPES'); $1" >/dev/null
}

detect_installed_database() {
    local version daemon

    # A configured label or the presence/order of client binaries is not
    # authoritative.  Probe the local server first, because distributions can
    # leave both compatibility clients installed while only one daemon owns the
    # data directory.  db_query_root uses an authenticated SELECT and therefore
    # cannot mistake mysqladmin's unauthenticated ping for a live server.
    if version=$(db_query_root 'SELECT VERSION()' 2>/dev/null); then
        case "$version" in
            *MariaDB*|*mariadb*) DB_TYPE="mariadb" ;;
            *) DB_TYPE="mysql" ;;
        esac
        return 0
    fi

    # If the daemon is installed but stopped, a single unambiguous binary is a
    # safe hint; configure_database will start its service and probe again.  If
    # both engines are installed and the server cannot be queried, refuse to
    # guess: selecting the wrong service could apply credentials/configuration to
    # the wrong data directory.
    local -a candidates=()
    for daemon in mariadbd mysqld; do
        have_cmd "$daemon" || continue
        if ! version=$("$daemon" --no-defaults --version 2>/dev/null) || [[ -z "$version" ]]; then
            log_error "An installed database daemon could not report its version; refusing to install or start another engine." \
                "已安装的数据库服务端无法报告版本，拒绝安装或启动其他引擎。"
            return 2
        fi
        case "$version" in
            *MariaDB*|*mariadb*) candidates+=("mariadb") ;;
            *) candidates+=("mysql") ;;
        esac
    done
    # A MariaDB compatibility alias may expose both names for the same daemon.
    if ((${#candidates[@]} > 0)); then
        local first="${candidates[0]}" candidate
        for candidate in "${candidates[@]:1}"; do
            [[ "$candidate" == "$first" ]] || {
                log_error "Both MySQL and MariaDB daemons are installed but no authenticated server could be detected; refusing to guess the data directory." \
                    "同时检测到 MySQL 和 MariaDB，但无法通过认证查询确定正在使用的服务端，拒绝猜测数据目录。"
                # Distinguish a safety refusal from "nothing installed yet".
                # A data marker alone cannot prove which system service owns
                # the daemon: unlike the embedded entrypoint, this installer
                # starts distribution-provided services, not a daemon path.
                return 2
            }
        done
        DB_TYPE="$first"
        return 0
    fi
    return 1
}

database_datadir_has_data() {
    local directory
    for directory in /var/lib/mysql /var/db/mysql; do
        if [[ -d "$directory" && -n "$(find "$directory" -mindepth 1 -maxdepth 1 ! -name lost+found -print -quit)" ]]; then
            return 0
        fi
    done
    return 1
}

database_datadir_engine_hint() {
    local directory marker maria=false mysql=false
    for directory in /var/lib/mysql /var/db/mysql; do
        [[ -d "$directory" ]] || continue
        if [[ -f "$directory/aria_log_control" ]]; then
            maria=true
        else
            for marker in "$directory"/aria_log.*; do
                if [[ -e "$marker" ]]; then
                    maria=true
                    break
                fi
            done
        fi
        if [[ -f "$directory/mysql.ibd" || -d "$directory/#innodb_redo" ]]; then
            mysql=true
        fi
    done
    if [[ "$maria" == true && "$mysql" == false ]]; then
        printf '%s\n' mariadb
    elif [[ "$mysql" == true && "$maria" == false ]]; then
        printf '%s\n' mysql
    else
        return 1
    fi
}

database_fallback_is_safe() {
    # Never install another engine over an existing daemon or data directory.
    ! have_cmd mysqld && ! have_cmd mariadbd && ! database_datadir_has_data
}

database_process_running() {
    pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1 || pgrep -f '[m]ysqld_safe|[m]ariadbd-safe' >/dev/null 2>&1
}

# Locate the version-matched database configuration shipped with the project.
# A release may contain only this installer, so fall back to the raw file for
# the selected release/tag.  The caller owns the returned temporary file.
database_config_source() {
    local candidate raw_ref temp
    DB_CONFIG_SOURCE_PATH=""
    DB_CONFIG_SOURCE_EPHEMERAL="false"
    if [[ -n "${DB_CONFIG_SOURCE:-}" ]]; then
        if [[ "$DB_CONFIG_SOURCE" != /* || "$DB_CONFIG_SOURCE" == *$'\n'* || "$DB_CONFIG_SOURCE" == *$'\r'* ]]; then
            log_error "DB_CONFIG_SOURCE must be an absolute path without control characters." \
                "DB_CONFIG_SOURCE 必须是不含控制字符的绝对路径。"
            return 1
        fi
        if [[ ! -r "$DB_CONFIG_SOURCE" || ! -s "$DB_CONFIG_SOURCE" ]]; then
            log_error "Configured DB_CONFIG_SOURCE is not a readable, non-empty file: ${DB_CONFIG_SOURCE}" \
                "配置的 DB_CONFIG_SOURCE 不是可读取的非空文件: ${DB_CONFIG_SOURCE}"
            return 1
        fi
        DB_CONFIG_SOURCE_PATH="$DB_CONFIG_SOURCE"
        return 0
    fi
    for candidate in \
        "${SCRIPT_DIR}/../deploy/my.cnf" \
        "${SCRIPT_DIR}/deploy/my.cnf" \
        "${INSTALL_DIR}/deploy/my.cnf" \
        "/opt/oneclickvirt/deploy/my.cnf"; do
        if [[ -r "$candidate" && -s "$candidate" ]]; then
            DB_CONFIG_SOURCE_PATH="$candidate"
            return 0
        fi
    done
    have_cmd curl || {
        log_error "deploy/my.cnf was not found and curl is unavailable; refusing to start an unconfigured database." \
            "未找到 deploy/my.cnf 且 curl 不可用，拒绝启动未配置的数据库。"
        return 1
    }
    # Database setup runs before release assets are resolved, so prefer the
    # explicitly requested version when VERSION is not populated yet.
    raw_ref="${VERSION:-${INSTALL_VERSION:-main}}"
    temp=$(mktemp "${TMPDIR:-/tmp}/oneclickvirt-my.cnf.XXXXXX") || return 1
    if ! curl -fsSL --connect-timeout 15 --max-time 120 \
        "https://raw.githubusercontent.com/${REPO}/${raw_ref}/deploy/my.cnf" -o "$temp" || [[ ! -s "$temp" ]]; then
        rm -f "$temp"
        log_error "Unable to download deploy/my.cnf for ${raw_ref}; refusing to continue." \
            "无法下载 ${raw_ref} 对应的 deploy/my.cnf，拒绝继续。"
        return 1
    fi
    DB_CONFIG_SOURCE_PATH="$temp"
    DB_CONFIG_SOURCE_EPHEMERAL="true"
    return 0
}

database_daemon_binary() {
    case "$DB_TYPE" in
        mariadb)
            if have_cmd mariadbd; then command -v mariadbd; return 0; fi
            if have_cmd mysqld; then command -v mysqld; return 0; fi
            ;;
        mysql)
            if have_cmd mysqld; then command -v mysqld; return 0; fi
            if have_cmd mariadbd; then command -v mariadbd; return 0; fi
            ;;
    esac
    return 1
}

# Render the shared config without touching its source.  Only options known to
# differ between engines are translated; all other user/project settings are
# kept verbatim so an unsupported security option cannot be silently dropped.
render_database_config() {
    local source="$1" target="$2"
    awk -v engine="$DB_TYPE" '
        /^[[:space:]]*[#;]/ { print; next }
        {
            line=$0
            key=line; sub(/=.*/, "", key); gsub(/^[[:space:]]+|[[:space:]]+$/, "", key)
            normalized=key; sub(/^loose-/, "", normalized); gsub(/-/, "_", normalized)
            if (engine == "mariadb" && normalized == "innodb_redo_log_capacity") {
                sub(/^[[:space:]]*[^=]+/, "innodb_log_file_size", line)
            } else if (engine == "mysql" && normalized == "innodb_log_file_size") {
                value=line; sub(/^[^=]*=/, "", value)
                sub(/^[[:space:]]*[^=]+/, "loose-innodb_log_file_size", line)
                print line
                print "loose-innodb_redo_log_capacity=" value
                next
            } else if (engine == "mysql" && normalized == "innodb_redo_log_capacity") {
                if (key !~ /^loose-/) sub(/^[[:space:]]*/, "loose-", line)
            } else if (engine == "mysql" && normalized ~ /^query_cache_(type|size)$/) {
                if (key !~ /^loose-/) sub(/^[[:space:]]*/, "loose-", line)
            } else if (engine == "mariadb" && normalized ~ /^query_cache_(type|size)$/) {
                sub(/^[[:space:]]*loose-/, "", line)
            }
            print line
        }
    ' "$source" > "$target" || return 1
    # Bare-metal installs use a loopback application connection.  The shared
    # Compose file intentionally binds 0.0.0.0 for the API container, but that
    # would expose a host database to the network.  A final section makes the
    # local-only policy deterministic even when the source contains a bind
    # directive earlier in the file.
    printf '\n[mysqld]\nbind-address=127.0.0.1\n' >> "$target" || return 1
}

# Apply the project defaults as a drop-in file.  Existing distro/admin files
# are never overwritten.  The candidate is syntax-checked by the actual daemon
# before an atomic rename; a prior drop-in is copied to a timestamped backup.
apply_database_config() {
    local source temp daemon target backup stamp
    DB_CONFIG_CHANGED="false"
    if [[ "$MYSQL_CNF_DIR" != /* || "$MYSQL_CNF_DIR" == *$'\n'* || "$MYSQL_CNF_DIR" == *$'\r'* ]]; then
        log_error "MYSQL_CNF_DIR must be an absolute path without control characters." \
            "MYSQL_CNF_DIR 必须是不含控制字符的绝对路径。"
        return 1
    fi
    database_config_source || return 1
    source="$DB_CONFIG_SOURCE_PATH"
    target="${MYSQL_CNF_DIR%/}/oneclickvirt.cnf"
    if [[ -L "$target" ]]; then
        log_error "Refusing to overwrite symlinked database drop-in: ${target}" \
            "拒绝覆盖符号链接数据库配置: ${target}"
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        return 1
    fi
    mkdir -p "$MYSQL_CNF_DIR" || {
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        return 1
    }
    temp=$(mktemp "${MYSQL_CNF_DIR%/}/.oneclickvirt.cnf.tmp.XXXXXX") || {
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        return 1
    }
    if ! render_database_config "$source" "$temp"; then
        rm -f "$temp"
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        return 1
    fi
    daemon=$(database_daemon_binary 2>/dev/null || true)
    if [[ -z "$daemon" ]]; then
        log_error "No ${DB_TYPE} database daemon is available to validate configuration." \
            "没有可用于校验 ${DB_TYPE} 配置的数据库服务端。"
        rm -f "$temp"
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        return 1
    fi
    if ! "$daemon" --defaults-file="$temp" --verbose --help >/dev/null 2>&1; then
        log_error "Database configuration is invalid for detected ${DB_TYPE}; existing files were preserved." \
            "检测到的 ${DB_TYPE} 数据库配置无效，已保留现有文件。"
        rm -f "$temp"
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        return 1
    fi
    chmod 0644 "$temp" || {
        rm -f "$temp"
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        return 1
    }
    chown root:root "$temp" 2>/dev/null || true
    if [[ -e "$target" ]] && cmp -s "$temp" "$target"; then
        rm -f "$temp"
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        log_info "Database drop-in already up to date: ${target}" "数据库配置已是最新: ${target}"
        return 0
    fi
    if [[ -e "$target" ]]; then
        stamp="$(date +%s 2>/dev/null || printf '%s' 0).$$"
        backup="${target}.bak.${stamp}"
        cp -p "$target" "$backup" || {
            log_error "Unable to preserve the previous database drop-in (${target}); refusing replacement." \
                "无法保留旧数据库配置（${target}），拒绝替换。"
            rm -f "$temp"
            [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
            return 1
        }
    fi
    if ! mv -f "$temp" "$target"; then
        rm -f "$temp"
        [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
        return 1
    fi
    [[ "$DB_CONFIG_SOURCE_EPHEMERAL" == true ]] && rm -f "$source"
    DB_CONFIG_CHANGED="true"
    log_success "Database drop-in installed: ${target}" "数据库配置已安装: ${target}"
    return 0
}

wait_for_database_ready() {
    local timeout="${1:-120}" interval="${2:-5}" elapsed=0 last_start=999
    log_info "Waiting for database service to become ready..." "正在等待数据库服务就绪..."
    while [[ $elapsed -lt $timeout ]]; do
        if db_ping; then
            log_success "Database ready after ${elapsed}s" "数据库已在 ${elapsed}s 后就绪"
            return 0
        fi

        if ! database_process_running || [[ "$last_start" -ge 20 ]]; then
            log_warning "Database is not ready, attempting to start ${DB_SERVICE:-$DB_TYPE}..." "数据库尚未就绪，正在尝试启动 ${DB_SERVICE:-$DB_TYPE}..."
            if [[ -n "$DB_SERVICE" ]] && ! service_start "$DB_SERVICE" >/dev/null 2>&1; then
                log_warning "Database service start attempt failed; will retry until the readiness deadline." \
                    "数据库服务启动尝试失败，将在就绪截止时间前继续重试。"
            fi
            last_start=0
        fi

        sleep "$interval"
        elapsed=$((elapsed + interval))
        last_start=$((last_start + interval))
    done
    log_error "Database did not become ready within ${timeout}s" "数据库在 ${timeout}s 内未就绪"
    log_error "Check: $(service_hint "${DB_SERVICE:-$DB_TYPE}")" "请检查: $(service_hint "${DB_SERVICE:-$DB_TYPE}")"
    return 1
}

wait_for_http_ready() {
    local url="$1" timeout="${2:-120}" interval="${3:-5}" elapsed=0
    log_info "Waiting for OneClickVirt API health endpoint..." "正在等待 OneClickVirt API 健康端点..."
    while [[ $elapsed -lt $timeout ]]; do
        if curl -fsS --connect-timeout 3 --max-time 8 "$url" >/dev/null 2>&1; then
            log_success "API health endpoint ready after ${elapsed}s" "API 健康端点在 ${elapsed}s 后就绪"
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    log_error "API health endpoint was not ready within ${timeout}s: ${url}" "API 健康端点在 ${timeout}s 内未就绪: ${url}"
    return 1
}

# wait_for_init_ready polls GET /api/v1/public/init/check until needInit=true
wait_for_init_ready() {
    local timeout="${1:-180}" interval="${2:-5}" elapsed=0
    log_info "Waiting for system to be ready for initialization..." "正在等待系统就绪以进行初始化..."
    while [[ $elapsed -lt $timeout ]]; do
        local resp
        resp=$(curl -fsS --connect-timeout 3 --max-time 8 "http://127.0.0.1:8888/api/v1/public/init/check" 2>/dev/null || true)
        if echo "$resp" | grep -q '"needInit":true'; then
            log_success "System ready for initialization after ${elapsed}s" "系统已在 ${elapsed}s 后就绪，可以初始化"
            return 0
        fi
        # If already initialized, that's also fine
        if echo "$resp" | grep -q '"needInit":false'; then
            log_info "System appears already initialized, skipping auto-init." "系统似乎已初始化，跳过自动初始化。"
            return 2
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    log_warning "Init check timed out after ${timeout}s. You may need to initialize manually." "初始化检查在 ${timeout}s 后超时，可能需要手动初始化。"
    return 1
}

# auto_init_system sends POST /api/v1/public/init with default admin credentials
auto_init_system() {
    local _db_host _db_port _db_name _db_user _db_pass
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        _db_host="${DB_HOST:-127.0.0.1}"
        _db_port="${DB_PORT:-3306}"
        _db_name="${DB_NAME_EXT:-oneclickvirt}"
        _db_user="${DB_USER_EXT:-oneclickvirt}"
        _db_pass="${DB_PASS_EXT}"
    else
        _db_host="127.0.0.1"
        _db_port="3306"
        _db_name="oneclickvirt"
        _db_user="oneclickvirt"
        _db_pass="${DB_PASSWORD}"
    fi

    local admin_user_json admin_pass_json admin_email_json db_type_json db_host_json db_port_json db_name_json db_user_json db_pass_json
    admin_user_json=$(json_escape "$ADMIN_USER")
    admin_pass_json=$(json_escape "$ADMIN_PASS")
    admin_email_json=$(json_escape "$ADMIN_EMAIL")
    db_type_json=$(json_escape "$DB_TYPE")
    db_host_json=$(json_escape "$_db_host")
    db_port_json=$(json_escape "$_db_port")
    db_name_json=$(json_escape "$_db_name")
    db_user_json=$(json_escape "$_db_user")
    db_pass_json=$(json_escape "$_db_pass")

    local payload
    payload=$(cat <<INIT_JSON
{
  "admin": {
    "username": "${admin_user_json}",
    "password": "${admin_pass_json}",
    "email": "${admin_email_json}"
  },
  "user": {
    "enabled": false
  },
  "database": {
    "type": "${db_type_json}",
    "host": "${db_host_json}",
    "port": "${db_port_json}",
    "database": "${db_name_json}",
    "username": "${db_user_json}",
    "password": "${db_pass_json}"
  }
}
INIT_JSON
)

    log_info "Auto-initializing system with default admin account..." "正在自动初始化系统（默认管理员账户）..."
    local resp="" attempt
    for attempt in 1 2 3 4 5; do
        resp=$(curl -fsS --connect-timeout 5 --max-time 30 -X POST "http://127.0.0.1:8888/api/v1/public/init" \
            -H "Content-Type: application/json" \
            -d "$payload" 2>/dev/null || true)
        if echo "$resp" | grep -q '"code":200\|系统初始化成功\|已初始化'; then
            break
        fi
        log_warning "Auto-init attempt ${attempt}/5 did not succeed, retrying..." "自动初始化第 ${attempt}/5 次未成功，准备重试..."
        sleep $((attempt * 3))
    done
    if echo "$resp" | grep -q '"code":200'; then
        log_success "System initialized successfully." "系统初始化成功。"
        return 0
    elif echo "$resp" | grep -q '已初始化'; then
        log_info "System already initialized (no action needed)." "系统已初始化，无需操作。"
        return 0
    else
        log_warning "Auto-init may have failed. Response: ${resp}" "自动初始化可能失败。响应: ${resp}"
        log_warning "You can initialize manually via the web UI: ${_display_url:-http://127.0.0.1:8888}" "可通过 Web 界面手动初始化: ${_display_url:-http://127.0.0.1:8888}"
        return 1
    fi
}

# ---- Database installation ----
should_prefer_mariadb() {
    [[ "$AUTO_DB_FALLBACK" != "true" || "$DB_TYPE" != "mysql" ]] && return 1
    case "$OS_FAMILY" in
        arch|alpine|bsd|suse) return 0 ;;
    esac
    case "$OS" in
        debian|raspbian) return 0 ;;
        ubuntu)
            local major; major=$(version_major "$OS_VERSION")
            [[ "${major:-0}" -ge 25 ]] && return 0
            ;;
    esac
    return 1
}

prefer_mariadb_if_needed() {
    if should_prefer_mariadb; then
        log_warning "MySQL packages are often unavailable or unstable on ${OS} ${OS_VERSION:-unknown}; using MariaDB as the MySQL-compatible local backend." \
            "${OS} ${OS_VERSION:-unknown} 上 MySQL 包常不可用或不稳定；将使用 MariaDB 作为 MySQL 兼容本地后端。"
        DB_TYPE="mariadb"
    fi
}

install_mysql() {
    log_info "Installing MySQL 8.0..." "正在安装 MySQL 8.0..."
    prefer_mariadb_if_needed
    if [[ "$DB_TYPE" == "mariadb" ]]; then
        install_mariadb
        return
    fi
    case "$OS" in
        ubuntu|debian|raspbian)
            pkg_install mysql-server mysql-client || return 1
            ;;
        centos|rhel|almalinux|rocky|fedora)
            pkg_install mysql-server mysql || return 1
            ;;
        amzn|ol|opencloudos)
            if ! pkg_install mysql-server mysql; then
                [[ "$AUTO_DB_FALLBACK" != "true" ]] && return 1
                database_fallback_is_safe || return 1
                log_warning "MySQL package install failed; falling back to MariaDB." "MySQL 包安装失败，回退到 MariaDB。"
                DB_TYPE="mariadb"
                pkg_install mariadb-server mariadb || return 1
            fi
            ;;
        *)
            if [[ "$AUTO_DB_FALLBACK" == "true" ]]; then
                database_fallback_is_safe || return 1
                log_warning "MySQL auto-install is not mapped for ${OS}; falling back to MariaDB." "未针对 ${OS} 映射 MySQL 自动安装，回退到 MariaDB。"
                DB_TYPE="mariadb"
                install_mariadb
                return
            fi
            pkg_install mysql-server mysql-client || return 1
            ;;
    esac
    select_db_service
    log_success "MySQL installed." "MySQL 安装完成。"
}

install_mariadb() {
    log_info "Installing MariaDB..." "正在安装 MariaDB..."
    case "$OS" in
        ubuntu|debian|raspbian)
            pkg_install mariadb-server mariadb-client || return 1
            ;;
        centos|rhel|almalinux|rocky|fedora|amzn|ol|opencloudos)
            pkg_install mariadb-server mariadb || return 1
            ;;
        arch|manjaro)
            pkg_install mariadb || return 1
            ;;
        alpine)
            pkg_install mariadb mariadb-client || return 1
            ;;
        opensuse*|sles|suse)
            pkg_install mariadb mariadb-client || return 1
            ;;
        freebsd|dragonflybsd)
            pkg_install mariadb114-server mariadb114-client || \
                pkg_install mariadb1011-server mariadb1011-client || \
                pkg_install mariadb106-server mariadb106-client || \
                pkg_install mariadb-server mariadb-client || return 1
            ;;
        openbsd|netbsd)
            pkg_install mariadb-server mariadb-client || pkg_install mariadb || return 1
            ;;
        *)
            pkg_install mariadb-server mariadb-client || pkg_install mariadb || return 1
            ;;
    esac
    select_db_service
    log_success "MariaDB installed." "MariaDB 安装完成。"
}

initialize_database_datadir() {
    local data_hint
    data_hint="$(database_datadir_engine_hint 2>/dev/null || true)"
    if [[ -n "$data_hint" && "$data_hint" != "$DB_TYPE" ]]; then
        log_error "Database data appears to belong to ${data_hint}, but the selected daemon is ${DB_TYPE}; refusing cross-engine startup." \
            "数据库数据目录疑似属于 ${data_hint}，当前选择的服务端是 ${DB_TYPE}，拒绝跨引擎启动。"
        return 1
    fi
    if [[ ! -d /var/lib/mysql/mysql && ! -d /var/db/mysql/mysql ]] && database_datadir_has_data; then
        log_error "Database data exists without system tables; refusing automatic reinitialization." "数据库已有数据但缺少系统表，拒绝自动重新初始化。"
        return 1
    fi
    case "$OS_FAMILY:$DB_TYPE" in
        arch:mariadb)
            if [[ ! -d /var/lib/mysql/mysql ]] && have_cmd mariadb-install-db; then
                log_info "Initializing MariaDB data directory..." "正在初始化 MariaDB 数据目录..."
                if ! mariadb-install-db --user=mysql --basedir=/usr --datadir=/var/lib/mysql >/dev/null 2>&1; then
                    log_error "MariaDB data directory initialization failed." "MariaDB 数据目录初始化失败。"
                    return 1
                fi
            fi
            ;;
        alpine:mariadb)
            if [[ ! -d /var/lib/mysql/mysql ]]; then
                log_info "Initializing MariaDB data directory..." "正在初始化 MariaDB 数据目录..."
                if ! /etc/init.d/mariadb setup >/dev/null 2>&1 &&
                   ! mysql_install_db --user=mysql --datadir=/var/lib/mysql >/dev/null 2>&1; then
                    log_error "MariaDB data directory initialization failed." "MariaDB 数据目录初始化失败。"
                    return 1
                fi
            fi
            ;;
        bsd:mariadb|bsd:mysql)
            # BSD rc scripts normally initialize on first start; keep this hook for package variants.
            if [[ ! -d /var/db/mysql/mysql ]] &&
               ! service "${DB_SERVICE:-mysql-server}" initdb >/dev/null 2>&1; then
                log_error "Database data directory initialization failed." "数据库数据目录初始化失败。"
                return 1
            fi
            ;;
    esac

    if [[ "$DB_TYPE" == "mariadb" || "$DB_TYPE" == "mysql" ]] &&
       [[ ! -d /var/lib/mysql/mysql && ! -d /var/db/mysql/mysql ]]; then
        log_error "Database initialization completed without creating system tables." \
            "数据库初始化完成但未生成系统表。"
        return 1
    fi
}

configure_database() {
    log_info "Configuring database..." "正在配置数据库..."

    # The caller may have loaded a stale mysql/mariadb hint from YAML or an
    # environment variable.  Resolve the daemon/data owner before the
    # pre-start safety check, otherwise a healthy MariaDB installation can be
    # rejected merely because it was labelled mysql.  Ambiguous dual-engine
    # hosts still fail closed in detect_installed_database.
    local requested_db_type="$DB_TYPE"
    if detect_installed_database; then
        if [[ "$requested_db_type" != "$DB_TYPE" ]]; then
            log_warning "Database hint ${requested_db_type} does not match the detected ${DB_TYPE}; using the detected engine." \
                "数据库配置标签 ${requested_db_type} 与实际检测到的 ${DB_TYPE} 不一致，将使用实际服务端。"
        fi
    else
        log_error "Cannot safely determine the database daemon; configuration and service startup were not attempted." \
            "无法安全确认数据库服务端，未执行配置或启动服务。"
        return 1
    fi

    if [[ -z "$DB_PASSWORD" ]]; then
        DB_PASSWORD=$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 24)
    fi

    select_db_service
    initialize_database_datadir || return 1
    # Install the version-matched project defaults only after the real daemon
    # has been identified.  A syntax check against that daemon prevents a
    # MySQL/MariaDB config mix-up from taking the service down.
    apply_database_config || return 1
    # A native service manager is part of the local database contract: if the
    # unit cannot be enabled or a changed configuration cannot be reloaded,
    # continuing would leave a healthy-looking install running stale settings.
    # Hosts without a service manager retain the foreground/process fallback.
    if [[ "$SERVICE_MANAGER" != "none" ]] && ! service_enable "$DB_SERVICE"; then
        log_error "Failed to enable the ${DB_TYPE} database service (${DB_SERVICE})." \
            "无法启用 ${DB_TYPE} 数据库服务（${DB_SERVICE}）。"
        return 1
    fi
    if [[ "$SERVICE_MANAGER" != "none" ]]; then
        if [[ "$DB_CONFIG_CHANGED" == "true" ]] && service_is_active "$DB_SERVICE"; then
            if ! service_reload_or_restart "$DB_SERVICE" >/dev/null 2>&1 &&
               ! service_start "$DB_SERVICE" >/dev/null 2>&1; then
                log_error "Failed to reload or restart the ${DB_TYPE} database service after applying configuration." \
                    "应用配置后无法重载或重启 ${DB_TYPE} 数据库服务。"
                return 1
            fi
        elif ! service_is_active "$DB_SERVICE" && ! service_start "$DB_SERVICE" >/dev/null 2>&1; then
            log_error "Failed to start the ${DB_TYPE} database service (${DB_SERVICE})." \
                "无法启动 ${DB_TYPE} 数据库服务（${DB_SERVICE}）。"
            return 1
        fi
    fi
    sleep 3
    wait_for_database_ready "$DB_WAIT_TIMEOUT" 5 || return 1

    local actual_version
    actual_version=$(db_query_root 'SELECT VERSION()') || return 1
    case "$actual_version" in
        *MariaDB*|*mariadb*) DB_TYPE="mariadb" ;;
        *) DB_TYPE="mysql" ;;
    esac
    log_info "Detected database server: ${DB_TYPE}" "已检测实际数据库服务端: ${DB_TYPE}"

    local DB_NAME="oneclickvirt"
    local DB_USER="oneclickvirt"
    local DB_PASSWORD_SQL; DB_PASSWORD_SQL=$(sql_escape "$DB_PASSWORD")

    local app_sql="
        CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
        CREATE USER IF NOT EXISTS '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD_SQL}';
        CREATE USER IF NOT EXISTS '${DB_USER}'@'localhost' IDENTIFIED BY '${DB_PASSWORD_SQL}';
        ALTER USER '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD_SQL}';
        ALTER USER '${DB_USER}'@'localhost' IDENTIFIED BY '${DB_PASSWORD_SQL}';
        GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'127.0.0.1';
        GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'localhost';
        FLUSH PRIVILEGES;
    "
    if ! db_exec_root "$app_sql"; then
        log_error "Failed to create database/user. Check: $(service_hint "$DB_SERVICE")" "数据库/用户创建失败。请检查: $(service_hint "$DB_SERVICE")"
        return 1
    fi

    local root_sql="
        ALTER USER 'root'@'localhost' IDENTIFIED BY '${DB_PASSWORD_SQL}';
        CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD_SQL}';
        GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;
        FLUSH PRIVILEGES;
    "
    db_exec_root "$root_sql" || log_warning "Root password hardening was skipped; app database user is configured." "root 密码加固已跳过；应用数据库用户已配置。"

    local hardening_sql="
        DELETE FROM mysql.user WHERE User='';
        DROP DATABASE IF EXISTS test;
        DELETE FROM mysql.db WHERE Db='test' OR Db='test\\_%';
        FLUSH PRIVILEGES;
    "
    db_exec_root "$hardening_sql" || log_warning "Optional database cleanup was skipped due to server compatibility." "可选数据库清理因服务端兼容性差异已跳过。"

    log_success "Database configured: ${DB_NAME} / user=${DB_USER} (localhost only)" "数据库配置完成: ${DB_NAME} / 用户=${DB_USER}（仅本地）"
}

install_local_database() {
    local requested_db="$DB_TYPE" detection_rc
    if detect_installed_database; then
        log_info "Using existing database daemon: ${DB_TYPE}" "复用现有数据库服务端: ${DB_TYPE}"
        configure_database
        return $?
    else
        detection_rc=$?
        if ((detection_rc == 2)); then
            return 1
        fi
    fi
    if database_datadir_has_data; then
        log_error "Existing database data requires the matching engine; refusing package fallback." "检测到已有数据库数据，请使用对应引擎，禁止自动跨引擎接管。"
        return 1
    fi
    case "$DB_TYPE" in
        mysql) install_mysql ;;
        mariadb) install_mariadb ;;
    esac

    if configure_database; then
        return 0
    fi

    if [[ "$AUTO_DB_FALLBACK" == "true" && "$requested_db" == "mysql" && "$DB_TYPE" != "mariadb" ]] && database_fallback_is_safe; then
        log_warning "MySQL did not become usable; retrying with MariaDB-compatible backend." "MySQL 未能可用，正在使用 MariaDB 兼容后端重试。"
        DB_TYPE="mariadb"
        install_mariadb || return 1
        configure_database
        return $?
    fi

    return 1
}

# ---- Reverse proxy installation ----
install_caddy() {
    log_info "Installing Caddy..." "正在安装 Caddy..."
    if command -v caddy &>/dev/null; then
        log_success "Caddy already installed." "Caddy 已安装。"
        return 0
    fi
    local caddy_os; caddy_os=$(release_os)
    download_file "https://caddyserver.com/api/download?os=${caddy_os}&arch=$(detect_arch)" /usr/local/bin/caddy || {
        log_error "Failed to download Caddy for ${caddy_os}/$(detect_arch)." "下载 ${caddy_os}/$(detect_arch) 的 Caddy 失败。"
        return 1
    }
    if ! chmod +x /usr/local/bin/caddy || ! mkdir -p "$CADDY_CONFIG_DIR" "$CADDY_LOG_DIR" "$INSTALL_DIR"; then
        log_error "Failed to prepare the Caddy installation." "准备 Caddy 安装失败。"
        return 1
    fi

    case "$SERVICE_MANAGER" in
        systemd)
            mkdir -p "$SYSTEMD_UNIT_DIR" || return 1
            if ! cat > "${SYSTEMD_UNIT_DIR}/caddy.service" << EOF
[Unit]
Description=Caddy Web Server
After=network.target
[Service]
ExecStart=/usr/local/bin/caddy run --config ${CADDY_CONFIG_DIR}/Caddyfile
ExecReload=/usr/local/bin/caddy reload --config ${CADDY_CONFIG_DIR}/Caddyfile
Restart=on-failure
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
EOF
            then
                log_error "Unable to write the Caddy systemd unit." "无法写入 Caddy systemd 服务单元。"
                return 1
            fi
            if ! systemctl daemon-reload >/dev/null 2>&1; then
                log_error "systemd daemon-reload failed for Caddy." "Caddy 的 systemd daemon-reload 失败。"
                return 1
            fi
            ;;
        openrc)
            mkdir -p "$OPENRC_INIT_DIR" || return 1
            if ! cat > "${OPENRC_INIT_DIR}/caddy" << EOF
#!/sbin/openrc-run
name="Caddy Web Server"
command="/usr/local/bin/caddy"
command_args="run --config ${CADDY_CONFIG_DIR}/Caddyfile"
command_background="yes"
pidfile="/run/caddy.pid"
depend() { need net; }
EOF
            then
                log_error "Unable to write the Caddy OpenRC script." "无法写入 Caddy OpenRC 脚本。"
                return 1
            fi
            chmod +x "${OPENRC_INIT_DIR}/caddy" || return 1
            ;;
        freebsd-service)
            mkdir -p "$FREEBSD_RC_DIR" || return 1
            if ! cat > "${FREEBSD_RC_DIR}/caddy" << EOF
#!/bin/sh
# PROVIDE: caddy
# REQUIRE: NETWORKING
# KEYWORD: shutdown
. /etc/rc.subr
name="caddy"
rcvar="caddy_enable"
command="/usr/sbin/daemon"
pidfile="/var/run/caddy.pid"
command_args="-p \${pidfile} -f /usr/local/bin/caddy run --config ${CADDY_CONFIG_DIR}/Caddyfile"
load_rc_config \$name
: \${caddy_enable:=YES}
run_rc_command "\$1"
EOF
            then
                log_error "Unable to write the Caddy rc.d script." "无法写入 Caddy rc.d 脚本。"
                return 1
            fi
            chmod +x "${FREEBSD_RC_DIR}/caddy" || return 1
            ;;
        sysv-service)
            mkdir -p "$SYSV_INIT_DIR" || return 1
            if ! cat > "${SYSV_INIT_DIR}/caddy" << EOF
#!/bin/sh
### BEGIN INIT INFO
# Provides:          caddy
# Required-Start:    \$network
# Required-Stop:     \$network
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: Caddy Web Server
### END INIT INFO
case "\$1" in
  start)
    nohup /usr/local/bin/caddy run --config "${CADDY_CONFIG_DIR}/Caddyfile" > "${CADDY_LOG_DIR}/caddy.log" 2>&1 &
    echo \$! > /var/run/caddy.pid
    ;;
  stop)
    [ -f /var/run/caddy.pid ] && kill "\$(cat /var/run/caddy.pid)" 2>/dev/null || true
    ;;
  restart)
    "\$0" stop
    sleep 1
    "\$0" start
    ;;
  status)
    [ -f /var/run/caddy.pid ] && kill -0 "\$(cat /var/run/caddy.pid)" 2>/dev/null
    ;;
  *) echo "Usage: \$0 {start|stop|restart|status}"; exit 1 ;;
esac
EOF
            then
                log_error "Unable to write the Caddy SysV init script." "无法写入 Caddy SysV init 脚本。"
                return 1
            fi
            chmod +x "${SYSV_INIT_DIR}/caddy" || return 1
            ;;
        rcctl|none)
            if ! cat > "${INSTALL_DIR}/start-caddy.sh" << EOF
#!/bin/sh
nohup /usr/local/bin/caddy run --config "${CADDY_CONFIG_DIR}/Caddyfile" > "${CADDY_LOG_DIR}/caddy.log" 2>&1 &
echo \$! > "${INSTALL_DIR}/caddy.pid"
EOF
            then
                log_error "Unable to write the Caddy launcher script." "无法写入 Caddy 启动脚本。"
                return 1
            fi
            chmod +x "${INSTALL_DIR}/start-caddy.sh" || return 1
            ;;
        *)
            log_warning "Caddy service file not created for service manager ${SERVICE_MANAGER}; it can be started manually." \
                "暂未为服务管理器 ${SERVICE_MANAGER} 创建 Caddy 服务文件，可手动启动。"
            ;;
    esac
    log_success "Caddy installed." "Caddy 安装完成。"
}

configure_caddy() {
    local tls_config=""
    local scheme_prefix=""
    case "$TLS_METHOD" in
        letsencrypt|zerossl)
            tls_config="tls ${EMAIL}"
            [[ "$TLS_METHOD" == "zerossl" ]] && tls_config="tls ${EMAIL} { issuer zerossl }"
            ;;
        selfsigned)
            tls_config="tls internal"
            ;;
        off)
            tls_config=""
            # Caddy v2 enables automatic HTTPS by default.
            # For bare IP / localhost / HTTP-only deployments, the http:// scheme
            # must be used in the site address to disable automatic TLS & redirect.
            scheme_prefix="http://"
            ;;
    esac

    if ! mkdir -p "$CADDY_CONFIG_DIR" "$CADDY_LOG_DIR"; then
        log_error "Unable to create Caddy configuration/log directories." "无法创建 Caddy 配置或日志目录。"
        return 1
    fi
    if ! cat > "${CADDY_CONFIG_DIR}/Caddyfile" << CADDY_EOF
# OneClickVirt Caddy Configuration
# Generated by install_full.sh

${scheme_prefix}${DOMAIN} {
    ${tls_config}

    # Security headers
    header X-Frame-Options "SAMEORIGIN"
    header X-Content-Type-Options "nosniff"
    header X-XSS-Protection "1; mode=block"
    header Referrer-Policy "strict-origin-when-cross-origin"

    # API proxy
    handle /api/* {
        reverse_proxy 127.0.0.1:8888
    }

    # Swagger docs
    handle /swagger/* {
        reverse_proxy 127.0.0.1:8888
    }

    # WebSocket support (Agent / SSH Terminal)
    @websocket {
        header Connection *Upgrade*
        header Upgrade websocket
    }
    reverse_proxy @websocket 127.0.0.1:8888

    # Static frontend
    handle {
        root * ${WEB_DIR}
        encode gzip
        file_server
        try_files {path} /index.html
    }

    log {
        output file ${CADDY_LOG_DIR}/access.log
        level INFO
    }
}
CADDY_EOF
    then
        log_error "Unable to write the Caddy configuration." "无法写入 Caddy 配置。"
        return 1
    fi
    log_success "Caddy configuration written to ${CADDY_CONFIG_DIR}/Caddyfile" "Caddy 配置已写入 ${CADDY_CONFIG_DIR}/Caddyfile"
}

install_nginx() {
    log_info "Installing Nginx..." "正在安装 Nginx..."
    case "$OS" in
        ubuntu|debian|raspbian) pkg_install nginx certbot python3-certbot-nginx || return 1 ;;
        centos|rhel|almalinux|rocky|fedora|amzn|ol|opencloudos) pkg_install nginx certbot python3-certbot-nginx || return 1 ;;
        arch|manjaro) pkg_install nginx certbot certbot-nginx || return 1 ;;
        alpine) pkg_install nginx certbot || return 1 ;;
        freebsd|openbsd|netbsd|dragonflybsd) pkg_install nginx py311-certbot || pkg_install nginx certbot || return 1 ;;
        *) pkg_install nginx certbot python3-certbot-nginx || pkg_install nginx certbot || return 1 ;;
    esac
    log_success "Nginx installed." "Nginx 安装完成。"
}

configure_nginx() {
    local NGINX_CONF
    if [[ "$OS_FAMILY" == "debian" ]]; then
        mkdir -p /etc/nginx/sites-available /etc/nginx/sites-enabled || return 1
        NGINX_CONF="/etc/nginx/sites-available/oneclickvirt"
    else
        mkdir -p "$NGINX_CONF_DIR" || return 1
        NGINX_CONF="${NGINX_CONF_DIR}/oneclickvirt.conf"
    fi

    if ! cat > "$NGINX_CONF" << NGINX_EOF
server {
    listen 80;
    server_name ${DOMAIN};

    client_max_body_size 100m;

    location /api/ {
        proxy_pass http://127.0.0.1:8888;
        proxy_http_version 1.1;
        proxy_set_header Host \$http_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header REMOTE-HOST \$remote_addr;
        proxy_set_header X-Forwarded-Host \$http_host;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-Port \$server_port;
    }

    location /swagger/ {
        proxy_pass http://127.0.0.1:8888;
        proxy_http_version 1.1;
        proxy_set_header Host \$http_host;
        proxy_set_header X-Forwarded-Host \$http_host;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-Port \$server_port;
    }

    location /ws/ {
        proxy_pass http://127.0.0.1:8888;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$http_host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Host \$http_host;
        proxy_set_header X-Forwarded-Proto \$scheme;
        proxy_set_header X-Forwarded-Port \$server_port;
        proxy_read_timeout 3600s;
    }

    location / {
        root ${WEB_DIR};
        index index.html;
        try_files \$uri \$uri/ /index.html;
    }
}
NGINX_EOF
    then
        log_error "Unable to write the Nginx configuration: ${NGINX_CONF}" "无法写入 Nginx 配置: ${NGINX_CONF}"
        return 1
    fi
    if [[ ! -s "$NGINX_CONF" ]]; then
        log_error "Unable to write the Nginx configuration: ${NGINX_CONF}" "无法写入 Nginx 配置: ${NGINX_CONF}"
        return 1
    fi

    if [[ "$OS_FAMILY" == "debian" ]]; then
        if ! ln -sf "$NGINX_CONF" /etc/nginx/sites-enabled/oneclickvirt; then
            log_error "Unable to enable the OneClickVirt Nginx site." "无法启用 OneClickVirt Nginx 站点。"
            return 1
        fi
        rm -f /etc/nginx/sites-enabled/default 2>/dev/null || true
    fi

    # TLS via certbot
    if [[ "$TLS_METHOD" == "letsencrypt" || "$TLS_METHOD" == "zerossl" ]]; then
        log_info "Obtaining TLS certificate via Certbot..." "正在通过 Certbot 获取 TLS 证书..."
        if [[ -n "$EMAIL" && "$OS_FAMILY" != "bsd" ]]; then
            certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --email "$EMAIL" 2>/dev/null || {
                log_warning "Certbot failed. You may need to run: certbot --nginx -d ${DOMAIN}" "Certbot 失败，可手动执行: certbot --nginx -d ${DOMAIN}"
            }
        elif [[ "$OS_FAMILY" == "bsd" ]]; then
            log_warning "Automatic nginx certbot integration is skipped on BSD; configure TLS manually or use Caddy." \
                "BSD 上跳过 nginx/certbot 自动集成；请手动配置 TLS 或使用 Caddy。"
        fi
    fi

    log_success "Nginx configuration written." "Nginx 配置已写入。"
}

install_openresty() {
    log_info "Installing OpenResty..." "正在安装 OpenResty..."
    case "$OS" in
        ubuntu|debian|raspbian)
            pkg_install wget gnupg ca-certificates || return 1
            wget -qO - https://openresty.org/package/pubkey.gpg | apt-key add - || return 1
            echo "deb http://openresty.org/package/${OS} $(lsb_release -sc 2>/dev/null || echo 'focal') main" \
                > /etc/apt/sources.list.d/openresty.list
            apt-get update -qq || true
            pkg_install openresty || return 1
            ;;
        centos|rhel|almalinux|rocky)
            pkg_install yum-utils || return 1
            yum-config-manager --add-repo "https://openresty.org/package/${OS}/openresty.repo" || return 1
            pkg_install openresty || return 1
            ;;
        fedora)
            pkg_install openresty || { PROXY="nginx"; install_nginx || return 1; }
            ;;
        *)
            log_warning "OpenResty auto-install not supported for ${OS}. Falling back to Nginx." "OpenResty 不支持在 ${OS} 上自动安装，回退到 Nginx。"
            PROXY="nginx"
            install_nginx || return 1
            ;;
    esac
    log_success "OpenResty installed." "OpenResty 安装完成。"
}

configure_openresty() {
    if [[ "$PROXY" == "nginx" ]]; then
        configure_nginx
        return
    fi
    configure_nginx
    log_success "OpenResty configured." "OpenResty 配置完成。"
}

# ---- Firewall configuration ----
configure_firewall() {
    log_info "Configuring firewall..." "正在配置防火墙..."
    if command -v ufw &>/dev/null; then
        ufw allow 80/tcp 2>/dev/null || true
        ufw allow 443/tcp 2>/dev/null || true
        ufw allow 22/tcp 2>/dev/null || true
        ufw --force enable 2>/dev/null || true
    elif command -v firewall-cmd &>/dev/null; then
        firewall-cmd --permanent --add-service=http 2>/dev/null || true
        firewall-cmd --permanent --add-service=https 2>/dev/null || true
        firewall-cmd --reload 2>/dev/null || true
    fi
    log_success "Firewall configured (80, 443 open)." "防火墙已配置（80, 443 端口已开放）。"
}

# ---- Application installation ----
get_latest_version() {
    if [[ -n "$INSTALL_VERSION" && "$INSTALL_VERSION" != "latest" ]]; then
        VERSION="$INSTALL_VERSION"
        BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
        log_info "Using specified version: $VERSION" "使用指定版本: $VERSION"
        return 0
    fi

    local api_urls=(
        "https://api.github.com"
        "https://githubapi.spiritlhl.workers.dev"
        "https://githubapi.spiritlhl.top"
    )

    for api in "${api_urls[@]}"; do
        local resp
        resp=$(curl -fsSL --connect-timeout 10 --max-time 30 "${api}/repos/${REPO}/releases/latest" 2>/dev/null || true)
        VERSION=$(echo "$resp" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
        if [[ -n "$VERSION" && "$VERSION" != "null" ]]; then
            BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
            log_success "Latest version: $VERSION" "最新版本: $VERSION"
            return 0
        fi
    done

    log_error "Failed to fetch latest version." "获取最新版本失败。"
    return 1
}

release_os() {
    case "$KERNEL_NAME" in
        linux) printf "linux" ;;
        freebsd) printf "freebsd" ;;
        openbsd) printf "openbsd" ;;
        netbsd) printf "netbsd" ;;
        darwin) printf "darwin" ;;
        *) printf "%s" "$KERNEL_NAME" ;;
    esac
}

tar_cmd() {
    if have_cmd tar; then
        printf "tar"
    elif have_cmd gtar; then
        printf "gtar"
    else
        return 1
    fi
}

install_oneclickvirt_service() {
    local bin_path="$1" release_asset="${2:-}" update_flavor="allinone"
    if [[ "$release_asset" == server-linux-* ]]; then
        update_flavor="standalone"
    fi
    mkdir -p "$INSTALL_DIR" || return 1
    case "$SERVICE_MANAGER" in
        systemd)
            mkdir -p "$SYSTEMD_UNIT_DIR" || {
                log_error "Failed to create the systemd unit directory." "创建 systemd 服务单元目录失败。"
                return 1
            }
            if [[ "$EXTERNAL_DB" == "true" ]]; then
                if ! cat > "${SYSTEMD_UNIT_DIR}/oneclickvirt.service" << SERV_EOF
[Unit]
Description=OneClickVirt Server
After=network.target

[Service]
Type=simple
WorkingDirectory=${SERVER_DIR}
Environment=ONECLICKVIRT_UPDATE_FLAVOR=${update_flavor}
Environment=ONECLICKVIRT_UPDATE_WEB=true
Environment=ONECLICKVIRT_PROXY_SERVICES=${PROXY}
ExecStart=${bin_path}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERV_EOF
                then
                    log_error "Failed to write the systemd unit." "写入 systemd 服务单元失败。"
                    return 1
                fi
            else
                if ! cat > "${SYSTEMD_UNIT_DIR}/oneclickvirt.service" << SERV_EOF
[Unit]
Description=OneClickVirt Server
After=network.target ${DB_SERVICE}.service
Requires=${DB_SERVICE}.service

[Service]
Type=simple
WorkingDirectory=${SERVER_DIR}
Environment=ONECLICKVIRT_UPDATE_FLAVOR=${update_flavor}
Environment=ONECLICKVIRT_UPDATE_WEB=true
Environment=ONECLICKVIRT_PROXY_SERVICES=${PROXY}
ExecStart=${bin_path}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERV_EOF
                then
                    log_error "Failed to write the systemd unit." "写入 systemd 服务单元失败。"
                    return 1
                fi
            fi
            if ! systemctl daemon-reload >/dev/null 2>&1; then
                log_error "systemd daemon-reload failed." "systemd daemon-reload 失败。"
                return 1
            fi
            if ! service_enable oneclickvirt; then
                log_error "Failed to enable the OneClickVirt service." "启用 OneClickVirt 服务失败。"
                return 1
            fi
            ;;
        openrc)
            mkdir -p "$OPENRC_INIT_DIR" || {
                log_error "Failed to create the OpenRC init directory." "创建 OpenRC 启动目录失败。"
                return 1
            }
            if ! cat > "${OPENRC_INIT_DIR}/oneclickvirt" << SERV_EOF
#!/sbin/openrc-run
name="OneClickVirt Server"
command="${bin_path}"
command_background="yes"
directory="${SERVER_DIR}"
pidfile="/run/oneclickvirt.pid"
output_log="${INSTALL_DIR}/oneclickvirt.log"
error_log="${INSTALL_DIR}/oneclickvirt.err"
depend() {
    need net
    after ${DB_SERVICE:-}
}
SERV_EOF
            then
                log_error "Failed to write the OpenRC service script." "写入 OpenRC 服务脚本失败。"
                return 1
            fi
            if ! chmod +x "${OPENRC_INIT_DIR}/oneclickvirt" || ! service_enable oneclickvirt; then
                log_error "Failed to install or enable the OpenRC service." "安装或启用 OpenRC 服务失败。"
                return 1
            fi
            ;;
        freebsd-service)
            mkdir -p "$FREEBSD_RC_DIR" || {
                log_error "Failed to create the FreeBSD rc.d directory." "创建 FreeBSD rc.d 目录失败。"
                return 1
            }
            if ! cat > "${FREEBSD_RC_DIR}/oneclickvirt" << SERV_EOF
#!/bin/sh
# PROVIDE: oneclickvirt
# REQUIRE: NETWORKING ${DB_SERVICE:-}
# KEYWORD: shutdown

. /etc/rc.subr

name="oneclickvirt"
rcvar="oneclickvirt_enable"
pidfile="/var/run/oneclickvirt.pid"
command="/usr/sbin/daemon"
command_args="-p \${pidfile} -f ${bin_path}"
start_precmd="oneclickvirt_prestart"

oneclickvirt_prestart() {
    cd "${SERVER_DIR}" || return 1
}

load_rc_config \$name
: \${oneclickvirt_enable:=YES}
run_rc_command "\$1"
SERV_EOF
            then
                log_error "Failed to write the FreeBSD rc.d script." "写入 FreeBSD rc.d 脚本失败。"
                return 1
            fi
            if ! chmod +x "${FREEBSD_RC_DIR}/oneclickvirt" || ! service_enable oneclickvirt; then
                log_error "Failed to install or enable the FreeBSD service." "安装或启用 FreeBSD 服务失败。"
                return 1
            fi
            ;;
        sysv-service)
            mkdir -p "$SYSV_INIT_DIR" || {
                log_error "Failed to create the SysV init directory." "创建 SysV 启动目录失败。"
                return 1
            }
            if ! cat > "${SYSV_INIT_DIR}/oneclickvirt" << SERV_EOF
#!/bin/sh
### BEGIN INIT INFO
# Provides:          oneclickvirt
# Required-Start:    \$network ${DB_SERVICE:-}
# Required-Stop:     \$network
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: OneClickVirt Server
### END INIT INFO
case "\$1" in
  start)
    cd "${SERVER_DIR}" || exit 1
    nohup "${bin_path}" > "${INSTALL_DIR}/oneclickvirt.log" 2>&1 &
    echo \$! > /var/run/oneclickvirt.pid
    ;;
  stop)
    [ -f /var/run/oneclickvirt.pid ] && kill "\$(cat /var/run/oneclickvirt.pid)" 2>/dev/null || true
    ;;
  restart)
    "\$0" stop
    sleep 1
    "\$0" start
    ;;
  status)
    [ -f /var/run/oneclickvirt.pid ] && kill -0 "\$(cat /var/run/oneclickvirt.pid)" 2>/dev/null
    ;;
  *) echo "Usage: \$0 {start|stop|restart|status}"; exit 1 ;;
esac
SERV_EOF
            then
                log_error "Failed to write the SysV init script." "写入 SysV 启动脚本失败。"
                return 1
            fi
            if ! chmod +x "${SYSV_INIT_DIR}/oneclickvirt"; then
                log_error "Failed to make the SysV init script executable." "无法将 SysV 启动脚本设为可执行。"
                return 1
            fi
            ;;
        none|rcctl)
            if ! cat > "${INSTALL_DIR}/start-oneclickvirt.sh" << SERV_EOF
#!/bin/sh
cd "${SERVER_DIR}" || exit 1
nohup "${bin_path}" > "${INSTALL_DIR}/oneclickvirt.log" 2>&1 &
echo \$! > "${INSTALL_DIR}/oneclickvirt.pid"
SERV_EOF
            then
                log_error "Failed to write the OneClickVirt start script." "写入 OneClickVirt 启动脚本失败。"
                return 1
            fi
            if ! chmod +x "${INSTALL_DIR}/start-oneclickvirt.sh"; then
                log_error "Failed to make the OneClickVirt start script executable." "无法将 OneClickVirt 启动脚本设为可执行。"
                return 1
            fi
            log_warning "No fully supported service manager detected; created ${INSTALL_DIR}/start-oneclickvirt.sh" \
                "未检测到完整支持的服务管理器；已创建 ${INSTALL_DIR}/start-oneclickvirt.sh"
            ;;
    esac
}

start_oneclickvirt_service() {
    case "$SERVICE_MANAGER" in
        none|rcctl)
            if ! "${INSTALL_DIR}/start-oneclickvirt.sh"; then
                return 1
            fi
            local pid_file="${INSTALL_DIR}/oneclickvirt.pid" pid
            if [[ ! -s "$pid_file" ]] || ! pid=$(cat "$pid_file") || [[ ! "$pid" =~ ^[0-9]+$ ]] || ! kill -0 "$pid" 2>/dev/null; then
                log_error "OneClickVirt start script did not leave a live process." "OneClickVirt 启动脚本未留下存活进程。"
                return 1
            fi
            ;;
        *)
            if ! service_restart oneclickvirt 2>/dev/null && ! service_start oneclickvirt 2>/dev/null; then
                log_error "Service manager failed to start OneClickVirt." "服务管理器启动 OneClickVirt 失败。"
                return 1
            fi
            local attempt
            for attempt in 1 2 3 4 5; do
                service_is_active oneclickvirt && return 0
                sleep 1
            done
            log_error "OneClickVirt is not active after start." "OneClickVirt 启动后未处于 active 状态。"
            return 1
            ;;
    esac
}

start_proxy_service() {
    local pid_file pid attempt
    case "$PROXY" in
        caddy)
            if [[ "$SERVICE_MANAGER" == "none" || "$SERVICE_MANAGER" == "rcctl" ]]; then
                if [[ ! -x "${INSTALL_DIR}/start-caddy.sh" ]] || ! "${INSTALL_DIR}/start-caddy.sh"; then
                    log_error "Unable to start Caddy with the generated launcher." "无法通过生成的启动脚本启动 Caddy。"
                    return 1
                fi
                pid_file="${INSTALL_DIR}/caddy.pid"
            else
                service_enable caddy 2>/dev/null || {
                    log_error "Unable to enable the Caddy service." "无法启用 Caddy 服务。"
                    return 1
                }
                service_restart caddy 2>/dev/null || service_start caddy 2>/dev/null || {
                    log_error "Unable to start the Caddy service." "无法启动 Caddy 服务。"
                    return 1
                }
                for attempt in 1 2 3 4 5; do
                    service_is_active caddy && return 0
                    sleep 1
                done
                log_error "Caddy is not active after start." "Caddy 启动后未处于 active 状态。"
                return 1
            fi
            ;;
        nginx|openresty)
            if [[ "$SERVICE_MANAGER" == "none" || "$SERVICE_MANAGER" == "rcctl" ]]; then
                if have_cmd "$PROXY"; then
                    "$PROXY" -t >/dev/null 2>&1 || return 1
                    "$PROXY" -s reload >/dev/null 2>&1 || "$PROXY" >/dev/null 2>&1 || return 1
                elif have_cmd nginx; then
                    nginx -t >/dev/null 2>&1 || return 1
                    nginx -s reload >/dev/null 2>&1 || nginx >/dev/null 2>&1 || return 1
                else
                    log_error "Nginx/OpenResty binary is unavailable." "Nginx/OpenResty 可执行文件不可用。"
                    return 1
                fi
            else
                service_enable "$PROXY" 2>/dev/null || {
                    log_error "Unable to enable the ${PROXY} service." "无法启用 ${PROXY} 服务。"
                    return 1
                }
                service_restart "$PROXY" 2>/dev/null || service_start "$PROXY" 2>/dev/null || {
                    log_error "Unable to start the ${PROXY} service." "无法启动 ${PROXY} 服务。"
                    return 1
                }
                for attempt in 1 2 3 4 5; do
                    service_is_active "$PROXY" && return 0
                    sleep 1
                done
                log_error "${PROXY} is not active after start." "${PROXY} 启动后未处于 active 状态。"
                return 1
            fi
            ;;
        *)
            log_error "Unsupported reverse proxy: ${PROXY}" "不支持的反向代理: ${PROXY}"
            return 1
    esac
    # Launchers used without a native service manager must leave a live process.
    if [[ -n "${pid_file:-}" ]]; then
        if [[ ! -s "$pid_file" ]] || ! pid=$(cat "$pid_file") || [[ ! "$pid" =~ ^[0-9]+$ ]] || ! kill -0 "$pid" 2>/dev/null; then
            log_error "${PROXY} launcher did not leave a live process." "${PROXY} 启动脚本未留下存活进程。"
            return 1
        fi
    fi
    return 0
}

write_application_config() {
    # JSON string escapes are also valid inside YAML double-quoted scalars.
    local _yaml_db_password
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        _yaml_db_password=$(json_escape "$DB_PASS_EXT")
    else
        _yaml_db_password=$(json_escape "$DB_PASSWORD")
    fi
    local _db_host _db_port _db_name _db_user
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        _db_host="${DB_HOST:-127.0.0.1}"
        _db_port="${DB_PORT:-3306}"
        _db_name="${DB_NAME_EXT:-oneclickvirt}"
        _db_user="${DB_USER_EXT:-oneclickvirt}"
    else
        _db_host="127.0.0.1"
        _db_port="3306"
        _db_name="oneclickvirt"
        _db_user="oneclickvirt"
    fi
    (umask 077; cat > "${SERVER_DIR}/config.yaml" << CONFIG_EOF
system:
  env: public
  addr: 8888
  db-type: "$(json_escape "$DB_TYPE")"
jwt:
  signing-key: "$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
  expires-time: 7d
  buffer-time: 1d
  issuer: oneclickvirt
mysql:
  path: "$(json_escape "$_db_host")"
  port: "$(json_escape "$_db_port")"
  db-name: "$(json_escape "$_db_name")"
  username: "$(json_escape "$_db_user")"
  password: "${_yaml_db_password}"
  config: charset=utf8mb4&parseTime=True&loc=Local&time_zone=%27%2B08%3A00%27
  max-idle-conns: "10"
  max-open-conns: "100"
  log-mode: error
  log-zap: "false"
  max-lifetime: "3600"
  auto-create: "true"
CONFIG_EOF
    ) || return 1
    if ! chmod 600 "${SERVER_DIR}/config.yaml"; then
        log_error "Unable to protect the application configuration." "无法保护应用配置文件。"
        return 1
    fi
}

install_application() {
    log_info "Installing OneClickVirt application..." "正在安装 OneClickVirt 应用..."
    local ARCH; ARCH=$(detect_arch)
    local ASSET_OS; ASSET_OS=$(release_os)
    local TAR_BIN; TAR_BIN=$(tar_cmd) || {
        log_error "tar/gtar is required but was not found." "需要 tar/gtar，但未找到。"
        return 1
    }

    if ! mkdir -p "$SERVER_DIR" "$WEB_DIR"; then
        log_error "Unable to create application directories." "无法创建应用目录。"
        return 1
    fi

    local candidates=(
        "server-allinone-${ASSET_OS}-${ARCH}.tar.gz"
    )
    if [[ "$ASSET_OS" == "linux" ]]; then
        candidates+=("server-linux-${ARCH}.tar.gz")
    fi

    local SERVER_FILE="" candidate
    for candidate in "${candidates[@]}"; do
        log_info "Downloading ${candidate}..." "正在下载 ${candidate}..."
        if download_file "${BASE_URL}/${candidate}" "/tmp/${candidate}" 2>/dev/null; then
            SERVER_FILE="$candidate"
            break
        fi
        log_warning "Release asset not available or download failed: ${candidate}" "发布资产不可用或下载失败: ${candidate}"
    done

    if [[ -z "$SERVER_FILE" ]]; then
        if [[ "$ASSET_OS" != "linux" ]]; then
            log_error "No ${ASSET_OS}/${ARCH} release asset was found. Use a Linux host/container, Docker deployment, or build the server from source for this OS." \
                "未找到 ${ASSET_OS}/${ARCH} 发布包。请使用 Linux 主机/容器、Docker 部署，或为该系统自行构建服务端。"
        else
            log_error "Failed to download server binary for ${ASSET_OS}/${ARCH}." "下载 ${ASSET_OS}/${ARCH} 服务端二进制失败。"
        fi
        return 1
    fi

    local extract_dir="/tmp/oneclickvirt-server-${VERSION:-unknown}-$$"
    if ! rm -rf "$extract_dir" || ! mkdir -p "$extract_dir"; then
        log_error "Unable to prepare the temporary extraction directory." "无法准备临时解压目录。"
        return 1
    fi
    if ! "$TAR_BIN" -xzf "/tmp/${SERVER_FILE}" -C "$extract_dir"; then
        log_error "Failed to extract ${SERVER_FILE}; refusing to install a partial application." \
            "解压 ${SERVER_FILE} 失败，拒绝安装不完整的应用。"
        rm -rf "$extract_dir"
        return 1
    fi
    local server_bin
    server_bin=$(find "$extract_dir" -type f \
        \( -name 'server-allinone-*' -o -name 'server-linux-*' \) -print | head -1)
    if [[ -z "$server_bin" ]]; then
        log_error "Extracted archive did not contain a supported server binary." "解压后的归档中未找到受支持的服务端二进制。"
        return 1
    fi
    local SERVER_BIN="${SERVER_DIR}/oneclickvirt-server"
    if ! cp "$server_bin" "$SERVER_BIN" || ! chmod +x "$SERVER_BIN"; then
        log_error "Failed to install the server binary at ${SERVER_BIN}." \
            "无法将服务端二进制安装到 ${SERVER_BIN}。"
        rm -rf "$extract_dir"
        return 1
    fi

    # Download web dist
    local WEB_FILE="web-dist.zip"
    log_info "Downloading $WEB_FILE..." "正在下载 $WEB_FILE..."
    download_file "${BASE_URL}/${WEB_FILE}" "/tmp/${WEB_FILE}" || {
        log_warning "Failed to download web-dist.zip (all-in-one server embeds frontend)" "下载 web-dist.zip 失败（all-in-one 服务器已内置前端）"
    }
    if [[ -f "/tmp/${WEB_FILE}" ]]; then
        if ! unzip -o "/tmp/${WEB_FILE}" -d "$WEB_DIR" >/dev/null 2>&1; then
            log_warning "web-dist.zip could not be extracted; the all-in-one binary may still provide the embedded frontend." \
                "web-dist.zip 解压失败；all-in-one 二进制可能仍会提供内置前端。"
        fi
    fi

    write_application_config || return 1

    if ! printf "%s\n" "${VERSION:-unknown}" > "${INSTALL_DIR}/VERSION" ||
       ! printf "%s\n" "$SERVER_FILE" > "${INSTALL_DIR}/SERVER_ASSET"; then
        log_error "Failed to write installation metadata." "写入安装元数据失败。"
        rm -rf "$extract_dir"
        rm -f "/tmp/${SERVER_FILE}" "/tmp/${WEB_FILE}"
        return 1
    fi
    if ! install_oneclickvirt_service "$SERVER_BIN" "$SERVER_FILE"; then
        log_error "Failed to install the OneClickVirt service." "安装 OneClickVirt 服务失败。"
        rm -rf "$extract_dir"
        rm -f "/tmp/${SERVER_FILE}" "/tmp/${WEB_FILE}"
        return 1
    fi

    # Start reverse proxy if configured. A proxy that failed to start leaves
    # the freshly installed panel unreachable, so treat this as a fatal
    # installation error instead of reporting a misleading success.
    if ! start_proxy_service; then
        log_error "Failed to start the reverse proxy; installation aborted." "反向代理启动失败，安装中止。"
        rm -rf "$extract_dir"
        rm -f "/tmp/${SERVER_FILE}" "/tmp/${WEB_FILE}"
        return 1
    fi

    # Start the server
    log_info "Starting OneClickVirt service..." "正在启动 OneClickVirt 服务..."
    if ! start_oneclickvirt_service; then
        log_error "Failed to start the OneClickVirt service." "OneClickVirt 服务启动失败。"
        rm -rf "$extract_dir"
        rm -f "/tmp/${SERVER_FILE}" "/tmp/${WEB_FILE}"
        return 1
    fi
    sleep 3

    log_success "Service start requested, waiting for API health endpoint..." "已请求启动服务，正在等待 API 健康端点..."
    if ! wait_for_http_ready "http://127.0.0.1:8888/api/v1/health" 240 5; then
        log_warning "API health check timed out, but service may still be initializing." "API 健康检查超时，服务可能仍在初始化中。"
        log_warning "Check: $(service_hint oneclickvirt)" "请检查: $(service_hint oneclickvirt)"
    fi

    # Auto-initialize the system (only for local DB installs)
    if [[ "$EXTERNAL_DB" != "true" ]]; then
        if wait_for_init_ready 240 5; then
            auto_init_system || true
        fi
    else
        log_info "External DB mode — skipping auto-init (initialize manually via web UI)." "外部数据库模式 — 跳过自动初始化（请通过 Web 界面手动初始化）。"
    fi

    # Cleanup
    rm -rf "$extract_dir"
    rm -f /tmp/"${SERVER_FILE}" /tmp/"${WEB_FILE}"

    log_success "Application installed and started." "应用已安装并启动。"
}

# ---- Main ----
main() {
    local SCRIPT_DIR; SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

    echo ""
    echo -e "${CYAN}============================================${NC}"
    echo -e "${CYAN}  OneClickVirt Full Installation / OneClickVirt 完整安装${NC}"
    echo -e "${CYAN}============================================${NC}"
    echo ""

    check_root
    detect_os
    check_system_resources
    install_dependencies || exit 1

    # ---- Interactive prompts (if not non-interactive) ----
    if [[ "$NONINTERACTIVE" != "true" ]]; then
        echo ""
        echo -e "${CYAN}--- Configuration / 配置 ---${NC}"
        echo -e "  (Press Enter to accept defaults shown in brackets / 按回车接受括号中的默认值)"

        read -r -p "Database type / 数据库类型 [mysql/mariadb] (default/默认: ${DB_TYPE}): " _db
        [[ -n "$_db" ]] && DB_TYPE="$_db"
        normalize_db_type
        validate_db_type || exit 1

        read -r -p "Reverse proxy / 反向代理 [caddy/nginx/openresty] (default/默认: ${PROXY}): " _px
        [[ -n "$_px" ]] && PROXY="$_px"

        local domain_prompt="Domain name or IP / 域名或 IP"
        [[ -n "$DOMAIN" && "$DOMAIN" != "localhost" ]] && domain_prompt="Domain name or IP [${DOMAIN}]"
        domain_prompt="${domain_prompt} (Enter to auto-detect / 回车自动检测, e.g. panel.example.com): "
        read -r -p "$domain_prompt" _dom
        if [[ -n "$_dom" ]]; then
            normalize_domain "$_dom"
        else
            # No domain entered — detect IPs and offer choices
            echo ""
            echo -e "  ${CYAN}No domain provided. Detecting IP addresses... / 未提供域名，正在检测 IP 地址...${NC}"
            local pub_ip="" priv_ip=""
            pub_ip=$(detect_public_ipv4 2>/dev/null || true)
            priv_ip=$(detect_private_ipv4 2>/dev/null || true)

            echo ""
            echo -e "  ${CYAN}Select an address to use / 请选择要使用的地址:${NC}"
            local opt_num=1
            if [[ -n "$pub_ip" ]]; then
                echo "  [${opt_num}] Public IPv4 / 公网 IPv4:  ${pub_ip}  (recommended / 推荐用于公网访问)"
                opt_num=$((opt_num + 1))
            fi
            echo "  [${opt_num}] Localhost / 本地回环:     127.0.0.1  (local access only / 仅本地访问)"
            opt_num=$((opt_num + 1))
            if [[ -n "$priv_ip" && "$priv_ip" != "$pub_ip" ]]; then
                echo "  [${opt_num}] Private IPv4 / 内网 IPv4:  ${priv_ip}  (LAN access / 局域网访问)"
                opt_num=$((opt_num + 1))
            fi
            echo "  [${opt_num}] Enter a custom domain/IP manually / 手动输入自定义域名或 IP"

            local choice
            read -r -p "  Your choice / 请选择 [1-${opt_num}] (default/默认: 1): " choice
            choice=${choice:-1}

            # Recalculate option positions based on what was shown
            local pos=1
            if [[ -n "$pub_ip" ]]; then
                if [[ "$choice" == "$pos" ]]; then
                    DOMAIN="$pub_ip"
                    DOMAIN_PROTO_DETECTED="http"
                    log_info "Using public IPv4: ${DOMAIN}" "使用公网 IPv4: ${DOMAIN}"
                fi
                pos=$((pos + 1))
            fi
            # localhost
            if [[ -z "$DOMAIN" && "$choice" == "$pos" ]]; then
                DOMAIN="localhost"
                log_info "Using localhost (127.0.0.1)" "使用本地回环地址 (127.0.0.1)"
            fi
            pos=$((pos + 1))
            # private IP
            if [[ -n "$priv_ip" && "$priv_ip" != "$pub_ip" ]]; then
                if [[ -z "$DOMAIN" && "$choice" == "$pos" ]]; then
                    DOMAIN="$priv_ip"
                    DOMAIN_PROTO_DETECTED="http"
                    log_info "Using private IPv4: ${DOMAIN}" "使用内网 IPv4: ${DOMAIN}"
                fi
                pos=$((pos + 1))
            fi
            # custom
            if [[ -z "$DOMAIN" && "$choice" == "$pos" ]]; then
                read -r -p "  Enter custom domain or IP / 请输入自定义域名或 IP: " _custom_dom
                if [[ -n "$_custom_dom" ]]; then
                    normalize_domain "$_custom_dom"
                else
                    DOMAIN="localhost"
                    log_warning "No input — falling back to localhost" "未输入内容 — 回退到 localhost"
                fi
            fi
            DOMAIN="${DOMAIN:-localhost}"
        fi
        log_info "Domain set to: ${DOMAIN}" "域名设置为: ${DOMAIN}"

        # Determine if DOMAIN is a real domain name (not localhost, not a bare IP)
        local _is_bare_domain="true"
        if [[ "$DOMAIN" == "localhost" ]]; then
            _is_bare_domain="false"
        elif [[ "$DOMAIN" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
            _is_bare_domain="false"
        fi

        if [[ "$_is_bare_domain" == "true" ]]; then
            # Auto-detect TLS from protocol prefix
            if [[ "$DOMAIN_PROTO_DETECTED" == "https" ]]; then
                TLS_METHOD="letsencrypt"
                log_info "Detected https:// — TLS will use Let's Encrypt" "检测到 https:// — 将使用 Let's Encrypt 证书"
            elif [[ "$DOMAIN_PROTO_DETECTED" == "http" ]]; then
                TLS_METHOD="off"
                log_info "Detected http:// — TLS disabled" "检测到 http:// — 已禁用 TLS"
            else
                local tls_prompt="TLS method / TLS 方式 [letsencrypt/zerossl/selfsigned/off]"
                [[ -n "$TLS_METHOD" ]] && tls_prompt="${tls_prompt} (default/默认: ${TLS_METHOD})"
                read -r -p "${tls_prompt}: " _tls
                [[ -n "$_tls" ]] && TLS_METHOD="$_tls"
            fi

            if [[ "$TLS_METHOD" != "off" && "$TLS_METHOD" != "selfsigned" ]]; then
                local email_prompt="Email for TLS certificate / TLS 证书邮箱"
                [[ -n "$EMAIL" ]] && email_prompt="${email_prompt} [${EMAIL}]"
                read -r -p "${email_prompt}: " _em
                [[ -n "$_em" ]] && EMAIL="$_em"
            fi
        else
            TLS_METHOD="off"
            if [[ "$DOMAIN" == "localhost" ]]; then
                log_info "Using localhost — TLS disabled." "使用本地回环地址 — 已禁用 TLS。"
            else
                log_info "Using bare IP address — TLS disabled (certificates require a domain name)." "使用裸 IP 地址 — 已禁用 TLS（证书需要域名）。"
            fi
        fi
    fi

    # ---- External database prompt (interactive only) ----
    if [[ "$NONINTERACTIVE" != "true" && "$EXTERNAL_DB" != "true" ]]; then
        echo ""
        read -r -p "Install local database? / 是否安装本地数据库? [Y/n] (n = use external DB / n = 使用外部数据库): " _local_db
        if [[ "$_local_db" =~ ^[Nn] ]]; then
            EXTERNAL_DB="true"
            echo -e "  ${CYAN}External database configuration / 外部数据库配置:${NC}"
            read -r -p "  DB Host / 数据库主机 (default/默认: 127.0.0.1): " _db_host
            DB_HOST="${_db_host:-127.0.0.1}"
            read -r -p "  DB Port / 数据库端口 (default/默认: 3306): " _db_port
            DB_PORT="${_db_port:-3306}"
            read -r -p "  DB Name / 数据库名称 (default/默认: oneclickvirt): " _db_name
            DB_NAME_EXT="${_db_name:-oneclickvirt}"
            read -r -p "  DB User / 数据库用户 (default/默认: oneclickvirt): " _db_user
            DB_USER_EXT="${_db_user:-oneclickvirt}"
            read -r -p "  DB Password / 数据库密码: " _db_pass
            DB_PASS_EXT="${_db_pass}"
            DB_PASSWORD="${_db_pass}"  # for display/summary
            log_info "Using external database: ${DB_USER_EXT}@${DB_HOST}:${DB_PORT}/${DB_NAME_EXT}" "使用外部数据库: ${DB_USER_EXT}@${DB_HOST}:${DB_PORT}/${DB_NAME_EXT}"
        fi
    fi

    # Validate non-interactive mode requirements
    if [[ "$NONINTERACTIVE" == "true" ]]; then
        if [[ "$TLS_METHOD" != "off" && "$TLS_METHOD" != "selfsigned" ]]; then
            if [[ -z "$DOMAIN" || "$DOMAIN" == "localhost" || "$DOMAIN" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
                log_error "--domain must be a real domain name (not localhost or bare IP) for TLS in non-interactive mode." "非交互模式下 TLS 需要真实域名（不能是 localhost 或裸 IP）。"
                exit 1
            fi
            if [[ -z "$EMAIL" ]]; then
                log_error "--email is required for TLS in non-interactive mode." "非交互模式下 TLS 需要提供 --email。"
                exit 1
            fi
        fi
    fi
    DOMAIN="${DOMAIN:-localhost}"
    validate_domain "$DOMAIN" || exit 1

    echo ""
    echo -e "${CYAN}--- Installation Summary / 安装摘要 ---${NC}"
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        echo "  Database:     EXTERNAL (${DB_HOST}:${DB_PORT}/${DB_NAME_EXT})"
    else
        echo "  Database:     ${DB_TYPE}"
    fi
    echo "  Proxy:        ${PROXY}"
    echo "  Domain:       ${DOMAIN:-localhost}"
    echo "  TLS:          ${TLS_METHOD}"
    echo "  Install Dir:  ${INSTALL_DIR}"
    echo ""

    if [[ "$NONINTERACTIVE" != "true" ]]; then
        read -r -p "Proceed with installation? / 是否继续安装? [Y/n]: " _confirm
        [[ "$_confirm" =~ ^[Nn] ]] && { log_info "Installation cancelled." "安装已取消。"; exit 0; }
    fi

    # ---- Install ----
    log_info "Starting installation..." "开始安装..."

    # 1. Database
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        log_info "Skipping local database install (using external DB: ${DB_HOST}:${DB_PORT}/${DB_NAME_EXT})" "跳过本地数据库安装（使用外部数据库: ${DB_HOST}:${DB_PORT}/${DB_NAME_EXT}）"
    else
        if ! install_local_database; then
            log_error "Database configuration failed. Installation aborted." "数据库配置失败，安装中止。"
            log_error "Check logs: $(service_hint "${DB_SERVICE:-$DB_TYPE}")" "请检查日志: $(service_hint "${DB_SERVICE:-$DB_TYPE}")"
            exit 1
        fi
    fi

    # 2. Reverse proxy
    case "$PROXY" in
        caddy)
            install_caddy || exit 1
            configure_caddy || exit 1
            ;;
        nginx)
            install_nginx || exit 1
            configure_nginx || exit 1
            ;;
        openresty)
            install_openresty || exit 1
            configure_openresty || exit 1
            ;;
    esac

    # 3. Firewall
    configure_firewall

    # 4. Application
    get_latest_version || exit 1
    install_application || exit 1

    # ---- Done ----
    echo ""
    echo -e "${GREEN}============================================${NC}"
    echo -e "${GREEN}  Installation Complete! / 安装完成！${NC}"
    echo -e "${GREEN}============================================${NC}"
    echo ""
    # Determine URL scheme
    local _url_scheme="http"
    local _is_bare="false"
    if [[ "$DOMAIN" == "localhost" || "$DOMAIN" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
        _is_bare="true"
        _url_scheme="http"
    elif [[ "$TLS_METHOD" != "off" ]]; then
        _url_scheme="https"
    fi
    local _display_url="${_url_scheme}://${DOMAIN}"

    echo -e "  Database / 数据库:     ${DB_TYPE} (database: oneclickvirt)"
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        echo -e "  DB Host / 主机:      ${DB_HOST}:${DB_PORT}/${DB_NAME_EXT}"
        echo -e "  DB User / 用户:      ${DB_USER_EXT}"
        echo -e "  DB Password / 密码:  ${DB_PASS_EXT}"
    else
        echo -e "  DB Password / 密码:  ${DB_PASSWORD}"
        echo -e "  DB User / 用户:      oneclickvirt (localhost only / 仅本地)"
    fi
    echo -e "  Proxy / 代理:        ${PROXY}"
    echo -e "  URL:          ${_display_url}"
    echo ""
    echo -e "  Server Logs / 服务日志:  journalctl -u oneclickvirt -f"
    echo -e "  Proxy Logs / 代理日志:   journalctl -u ${PROXY} -f"
    echo -e "  Config Dir / 配置目录:   ${SERVER_DIR}"
    echo ""
    echo -e "${YELLOW}  IMPORTANT — First-Run Setup / 重要 — 首次运行设置:${NC}"
    echo -e "  - Admin account has been auto-created (if local DB was installed):"
    echo -e "    管理员账户已自动创建（如安装了本地数据库）:"
    echo -e "    Username / 用户名:  ${ADMIN_USER}"
    echo -e "    Password / 密码:    ${ADMIN_PASS}"
    echo -e "  - Login at / 登录地址: ${_display_url}"
    echo -e "  - CHANGE THE PASSWORD after first login! / 首次登录后请修改密码！"
    if [[ "$EXTERNAL_DB" != "true" ]]; then
        echo -e "  - Database is LOCALHOST-only (bind-address=127.0.0.1) — not exposed to internet."
        echo -e "    数据库仅限本地访问 (bind-address=127.0.0.1) — 未暴露到公网。"
    fi
    echo ""
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        echo -e "${YELLOW}  External Database Credentials / 外部数据库凭据:${NC}"
        echo -e "  DB Host / 主机:      ${DB_HOST}:${DB_PORT}"
        echo -e "  DB Name / 库名:      ${DB_NAME_EXT}"
        echo -e "  DB User / 用户:      ${DB_USER_EXT}"
        echo -e "  DB Password / 密码:  ${DB_PASS_EXT}"
    else
        echo -e "${YELLOW}  Database Credentials / 数据库凭据 (auto-generated / 自动生成, for config.yaml):${NC}"
        echo -e "  DB Name / 库名:      oneclickvirt"
        echo -e "  DB User / 用户:      oneclickvirt"
        echo -e "  DB Password / 密码:  ${DB_PASSWORD}"
    fi
    echo ""

    # Save credentials
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        if ! cat > "${INSTALL_DIR}/.credentials" << CRED
Database: ${DB_TYPE} (EXTERNAL)
DB Host: ${DB_HOST}:${DB_PORT}
DB Name: ${DB_NAME_EXT}
DB User: ${DB_USER_EXT}
DB Password: ${DB_PASS_EXT}
Admin Username: ${ADMIN_USER}
Admin Password: ${ADMIN_PASS}
URL: ${_display_url}
CRED
        then
            log_error "Unable to save installation credentials." "无法保存安装凭据。"
            return 1
        fi
    else
        if ! cat > "${INSTALL_DIR}/.credentials" << CRED
Database: ${DB_TYPE}
Database Name: oneclickvirt
Database User: oneclickvirt
Database Password: ${DB_PASSWORD}
Admin Username: ${ADMIN_USER}
Admin Password: ${ADMIN_PASS}
URL: ${_display_url}
CRED
        then
            log_error "Unable to save installation credentials." "无法保存安装凭据。"
            return 1
        fi
    fi
    if ! chmod 600 "${INSTALL_DIR}/.credentials"; then
        log_error "Unable to protect the installation credentials." "无法保护安装凭据文件。"
        return 1
    fi
    log_info "Credentials saved to ${INSTALL_DIR}/.credentials" "凭据已保存至 ${INSTALL_DIR}/.credentials"
}

if [[ "${BASH_SOURCE[0]:-}" == "$0" || -z "${BASH_SOURCE[0]:-}" ]]; then
    main "$@"
fi
