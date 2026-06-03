#!/bin/bash
# ==============================================================================
# OneClickVirt Full Installation Script (Bare Metal / VPS)
# Installs: MySQL/MariaDB + Reverse Proxy (Caddy/Nginx/OpenResty) + App
# Source: https://github.com/oneclickvirt/oneclickvirt
# Version: 1.0.0
# ==============================================================================
set -uo pipefail

export NONINTERACTIVE="${NONINTERACTIVE:-false}"
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
MYSQL_CNF_DIR="/etc/mysql/conf.d"
NGINX_CONF_DIR="/etc/nginx/conf.d"
CADDY_CONFIG_DIR="/etc/caddy"
CADDY_LOG_DIR="/var/log/caddy"
DOWNLOAD_RETRIES="${DOWNLOAD_RETRIES:-4}"
DB_WAIT_TIMEOUT="${DB_WAIT_TIMEOUT:-180}"
AUTO_DB_FALLBACK="${AUTO_DB_FALLBACK:-true}"

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
Usage: bash install_full.sh [OPTIONS]

Options:
  --db-type TYPE          Database type: mysql (default) or mariadb
  --db-password PASS      Database root password (auto-generated if not set)
  --external-db           Use an external database (skip local DB install)
  --db-host HOST          External DB host (implies --external-db)
  --db-port PORT          External DB port (default: 3306)
  --db-name NAME          External DB name (default: oneclickvirt)
  --db-user USER          External DB user (default: oneclickvirt)
  --db-pass PASS          External DB password
  --admin-email EMAIL     Admin email for auto-init (default: admin@oneclickvirt.local)
  --proxy TYPE            Reverse proxy: caddy, nginx, openresty (default: caddy)
  --domain DOMAIN         Domain name or IP (e.g. panel.example.com, 1.2.3.4)
                          Prefix with https:// to enable TLS, http:// to disable.
                          If omitted in interactive mode, auto-detects public/private IP
                          and prompts for choice (public IPv4 / localhost / private IPv4).
  --email EMAIL           Email for TLS certificate notifications
  --tls METHOD            TLS method: letsencrypt, zerossl, selfsigned, off
                          TLS requires a real domain name (not bare IP or localhost).
  --non-interactive       Run without prompts
  --force                 Skip system resource checks (disk & memory)
  --version VERSION       Specific version to install (default: latest)
  --db-wait-timeout SEC   Seconds to wait for local DB readiness (default: 180)
  --no-db-fallback        Do not auto-fallback from MySQL to MariaDB-compatible backend
  --help                  Show this help

Examples:
  # Interactive mode — just press Enter at each prompt
  bash install_full.sh

  # Non-interactive with local DB + domain + TLS
  bash install_full.sh --non-interactive --domain https://panel.example.com --email admin@example.com

  # Non-interactive with a public IP (TLS disabled, local DB)
  bash install_full.sh --non-interactive --domain http://1.2.3.4

  # Use external database (separated deployment / 分离式部署)
  bash install_full.sh --external-db --db-host 10.0.0.5 --db-name oneclickvirt --db-user ocv --db-pass mypass

  # Non-interactive with external DB
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
NONINTERACTIVE="false"
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

    local resource_warnings=()

    # ── disk check ──────────────────────────────────────────────────────────
    local min_disk_kb=$((10 * 1024 * 1024))
    local available_disk_kb
    available_disk_kb=$(df -Pk / 2>/dev/null | awk 'NR==2 {print $4}')
    if [[ -n "$available_disk_kb" && "$available_disk_kb" -lt "$min_disk_kb" ]]; then
        local avail_gb=$((available_disk_kb / 1024 / 1024))
        resource_warnings+=("$(printf 'Disk space: %d GB available, 10 GB recommended' "$avail_gb")")
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
    local combined_gb=0
    if [[ "$combined_kb" -gt 0 ]]; then
        combined_gb=$(awk "BEGIN {printf \"%.1f\", $combined_kb / 1024 / 1024}")
    fi
    if [[ "$combined_kb" -gt 0 && "$combined_kb" -lt "$min_combined_kb" ]]; then
        local ram_gb=0 swap_gb=0
        ram_gb=$(awk "BEGIN {printf \"%.1f\", $memtotal_kb / 1024 / 1024}")
        swap_gb=$(awk "BEGIN {printf \"%.1f\", $swaptotal_kb / 1024 / 1024}")
        resource_warnings+=("$(printf 'Memory: %.1f GB RAM + %.1f GB swap = %.1f GB total, 2 GB recommended' "$ram_gb" "$swap_gb" "$combined_gb")")
    fi

    # ── handle warnings ─────────────────────────────────────────────────────
    if [[ ${#resource_warnings[@]} -gt 0 ]]; then
        log_warning "System resources below recommended levels:" "系统资源低于推荐配置:"
        for w in "${resource_warnings[@]}"; do
            log_warning "  - $w" "  - $w"
        done
        log_warning "Installation may fail or performance may be degraded." "安装可能失败或性能下降。"

        if [[ "${NONINTERACTIVE:-false}" == "true" ]]; then
            log_error "Resource checks failed. Re-run with --force to bypass, or set SKIP_RESOURCE_CHECK=true." "资源检查未通过。请使用 --force 跳过检查，或设置 SKIP_RESOURCE_CHECK=true。"
            exit 1
        fi

        # Interactive: ask for confirmation
        read -r -p "$(echo -e "${YELLOW}[WARN]${NC} Continue anyway? (y/N): ")" confirm
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
            sysrc "${name}_enable=YES" >/dev/null 2>&1 || true
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
    local admin
    admin=$(db_admin_client 2>/dev/null || true)
    if [[ -n "$admin" ]]; then
        "$admin" ping --silent >/dev/null 2>&1 && return 0
        "$admin" -u root ping --silent >/dev/null 2>&1 && return 0
        if [[ -n "${DB_PASSWORD:-}" ]]; then
            "$admin" -u root -p"${DB_PASSWORD}" ping --silent >/dev/null 2>&1 && return 0
            "$admin" -h 127.0.0.1 -u root -p"${DB_PASSWORD}" ping --silent >/dev/null 2>&1 && return 0
        fi
    fi

    local client
    client=$(db_client 2>/dev/null || true)
    if [[ -n "$client" ]]; then
        "$client" -e "SELECT 1" >/dev/null 2>&1 && return 0
        "$client" -u root -e "SELECT 1" >/dev/null 2>&1 && return 0
        if [[ -n "${DB_PASSWORD:-}" ]]; then
            "$client" -u root -p"${DB_PASSWORD}" -e "SELECT 1" >/dev/null 2>&1 && return 0
            "$client" -h 127.0.0.1 -u root -p"${DB_PASSWORD}" -e "SELECT 1" >/dev/null 2>&1 && return 0
        fi
    fi
    return 1
}

db_exec_root() {
    local sql="$1" client
    client=$(db_client 2>/dev/null || true)
    [[ -z "$client" ]] && return 1

    "$client" -e "$sql" >/dev/null 2>&1 && return 0
    "$client" -u root -e "$sql" >/dev/null 2>&1 && return 0
    if [[ -n "${DB_PASSWORD:-}" ]]; then
        "$client" -u root -p"${DB_PASSWORD}" -e "$sql" >/dev/null 2>&1 && return 0
        "$client" -h 127.0.0.1 -u root -p"${DB_PASSWORD}" -e "$sql" >/dev/null 2>&1 && return 0
    fi
    return 1
}

database_process_running() {
    pgrep -x mysqld >/dev/null 2>&1 || pgrep -x mariadbd >/dev/null 2>&1 || pgrep -f '[m]ysqld_safe|[m]ariadbd-safe' >/dev/null 2>&1
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
            [[ -n "$DB_SERVICE" ]] && service_start "$DB_SERVICE" >/dev/null 2>&1 || true
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
        if echo "$resp" | grep -q '"code":0\|已初始化'; then
            break
        fi
        log_warning "Auto-init attempt ${attempt}/5 did not succeed, retrying..." "自动初始化第 ${attempt}/5 次未成功，准备重试..."
        sleep $((attempt * 3))
    done
    if echo "$resp" | grep -q '"code":0'; then
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
                log_warning "MySQL package install failed; falling back to MariaDB." "MySQL 包安装失败，回退到 MariaDB。"
                DB_TYPE="mariadb"
                pkg_install mariadb-server mariadb || return 1
            fi
            ;;
        *)
            if [[ "$AUTO_DB_FALLBACK" == "true" ]]; then
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
    case "$OS_FAMILY:$DB_TYPE" in
        arch:mariadb)
            if [[ ! -d /var/lib/mysql/mysql ]] && have_cmd mariadb-install-db; then
                log_info "Initializing MariaDB data directory..." "正在初始化 MariaDB 数据目录..."
                mariadb-install-db --user=mysql --basedir=/usr --datadir=/var/lib/mysql >/dev/null 2>&1 || true
            fi
            ;;
        alpine:mariadb)
            if [[ ! -d /var/lib/mysql/mysql ]]; then
                log_info "Initializing MariaDB data directory..." "正在初始化 MariaDB 数据目录..."
                /etc/init.d/mariadb setup >/dev/null 2>&1 || mysql_install_db --user=mysql --datadir=/var/lib/mysql >/dev/null 2>&1 || true
            fi
            ;;
        bsd:mariadb|bsd:mysql)
            # BSD rc scripts normally initialize on first start; keep this hook for package variants.
            [[ -d /var/db/mysql/mysql ]] || service "${DB_SERVICE:-mysql-server}" initdb >/dev/null 2>&1 || true
            ;;
    esac
}

configure_database() {
    log_info "Configuring database..." "正在配置数据库..."

    if [[ -z "$DB_PASSWORD" ]]; then
        DB_PASSWORD=$(head -c 32 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 24)
    fi

    select_db_service
    initialize_database_datadir
    service_enable "$DB_SERVICE" || true
    service_start "$DB_SERVICE" >/dev/null 2>&1 || true
    sleep 3
    wait_for_database_ready "$DB_WAIT_TIMEOUT" 5 || return 1

    local DB_NAME="oneclickvirt"
    local DB_USER="oneclickvirt"
    local DB_PASSWORD_SQL; DB_PASSWORD_SQL=$(sql_escape "$DB_PASSWORD")

    local app_sql="
        CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
        CREATE USER IF NOT EXISTS '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD_SQL}';
        CREATE USER IF NOT EXISTS '${DB_USER}'@'localhost' IDENTIFIED BY '${DB_PASSWORD_SQL}';
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

    mkdir -p "$MYSQL_CNF_DIR"
    local MY_CNF="${MYSQL_CNF_DIR}/oneclickvirt.cnf"
    if [[ -f "${SCRIPT_DIR}/deploy/my.cnf" ]]; then
        cp "${SCRIPT_DIR}/deploy/my.cnf" "$MY_CNF"
    else
        download_file "https://raw.githubusercontent.com/${REPO}/main/deploy/my.cnf" "$MY_CNF" 2>/dev/null || true
    fi

    if [[ -s "$MY_CNF" ]]; then
        service_reload_or_restart "$DB_SERVICE" >/dev/null 2>&1 || true
        if ! wait_for_database_ready 60 5; then
            log_warning "Database failed after applying optimization config." "数据库服务在应用优化配置后未能启动。"
            log_warning "Removing ${MY_CNF} and retrying with defaults..." "正在移除 ${MY_CNF} 并使用默认配置重试..."
            rm -f "$MY_CNF"
            service_restart "$DB_SERVICE" >/dev/null 2>&1 || true
            wait_for_database_ready 90 5 || {
                log_error "Database service still failing. Check: $(service_hint "$DB_SERVICE")" "数据库服务仍然失败。请检查: $(service_hint "$DB_SERVICE")"
                return 1
            }
            log_warning "Database started successfully without optimization config." "数据库已在不加载优化配置的情况下启动。"
        fi
    else
        log_warning "Database optimization config was not available; continuing with defaults." "数据库优化配置不可用，将使用默认配置继续。"
    fi

    log_success "Database configured: ${DB_NAME} / user=${DB_USER} (localhost only)" "数据库配置完成: ${DB_NAME} / 用户=${DB_USER}（仅本地）"
}

install_local_database() {
    local requested_db="$DB_TYPE"
    case "$DB_TYPE" in
        mysql) install_mysql ;;
        mariadb) install_mariadb ;;
    esac

    if configure_database; then
        return 0
    fi

    if [[ "$AUTO_DB_FALLBACK" == "true" && "$requested_db" == "mysql" && "$DB_TYPE" != "mariadb" ]]; then
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
    chmod +x /usr/local/bin/caddy
    mkdir -p "$CADDY_CONFIG_DIR" "$CADDY_LOG_DIR" "$INSTALL_DIR"

    case "$SERVICE_MANAGER" in
        systemd)
            cat > /etc/systemd/system/caddy.service << EOF
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
            systemctl daemon-reload 2>/dev/null || true
            ;;
        openrc)
            cat > /etc/init.d/caddy << EOF
#!/sbin/openrc-run
name="Caddy Web Server"
command="/usr/local/bin/caddy"
command_args="run --config ${CADDY_CONFIG_DIR}/Caddyfile"
command_background="yes"
pidfile="/run/caddy.pid"
depend() { need net; }
EOF
            chmod +x /etc/init.d/caddy
            ;;
        freebsd-service)
            cat > /usr/local/etc/rc.d/caddy << EOF
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
            chmod +x /usr/local/etc/rc.d/caddy
            ;;
        sysv-service)
            cat > /etc/init.d/caddy << EOF
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
            chmod +x /etc/init.d/caddy
            ;;
        rcctl|none)
            cat > "${INSTALL_DIR}/start-caddy.sh" << EOF
#!/bin/sh
nohup /usr/local/bin/caddy run --config "${CADDY_CONFIG_DIR}/Caddyfile" > "${CADDY_LOG_DIR}/caddy.log" 2>&1 &
echo \$! > "${INSTALL_DIR}/caddy.pid"
EOF
            chmod +x "${INSTALL_DIR}/start-caddy.sh"
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
            ;;
    esac

    mkdir -p "$CADDY_CONFIG_DIR" "$CADDY_LOG_DIR"
    cat > "${CADDY_CONFIG_DIR}/Caddyfile" << CADDY_EOF
# OneClickVirt Caddy Configuration
# Generated by install_full.sh

${DOMAIN} {
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
        mkdir -p /etc/nginx/sites-available /etc/nginx/sites-enabled
        NGINX_CONF="/etc/nginx/sites-available/oneclickvirt"
    else
        mkdir -p "$NGINX_CONF_DIR"
        NGINX_CONF="${NGINX_CONF_DIR}/oneclickvirt.conf"
    fi

    cat > "$NGINX_CONF" << NGINX_EOF
server {
    listen 80;
    server_name ${DOMAIN};

    client_max_body_size 100m;

    location /api/ {
        proxy_pass http://127.0.0.1:8888;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
        proxy_set_header X-Real-IP \$remote_addr;
        proxy_set_header X-Forwarded-For \$proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto \$scheme;
    }

    location /swagger/ {
        proxy_pass http://127.0.0.1:8888;
        proxy_http_version 1.1;
        proxy_set_header Host \$host;
    }

    location /ws/ {
        proxy_pass http://127.0.0.1:8888;
        proxy_http_version 1.1;
        proxy_set_header Upgrade \$http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host \$host;
        proxy_read_timeout 3600s;
    }

    location / {
        root ${WEB_DIR};
        index index.html;
        try_files \$uri \$uri/ /index.html;
    }
}
NGINX_EOF

    if [[ "$OS_FAMILY" == "debian" ]]; then
        ln -sf "$NGINX_CONF" /etc/nginx/sites-enabled/oneclickvirt 2>/dev/null || true
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
    local bin_path="$1"
    mkdir -p "$INSTALL_DIR"
    case "$SERVICE_MANAGER" in
        systemd)
            if [[ "$EXTERNAL_DB" == "true" ]]; then
                cat > /etc/systemd/system/oneclickvirt.service << SERV_EOF
[Unit]
Description=OneClickVirt Server
After=network.target

[Service]
Type=simple
WorkingDirectory=${SERVER_DIR}
ExecStart=${bin_path}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERV_EOF
            else
                cat > /etc/systemd/system/oneclickvirt.service << SERV_EOF
[Unit]
Description=OneClickVirt Server
After=network.target ${DB_SERVICE}.service
Requires=${DB_SERVICE}.service

[Service]
Type=simple
WorkingDirectory=${SERVER_DIR}
ExecStart=${bin_path}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERV_EOF
            fi
            systemctl daemon-reload
            service_enable oneclickvirt
            ;;
        openrc)
            cat > /etc/init.d/oneclickvirt << SERV_EOF
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
            chmod +x /etc/init.d/oneclickvirt
            service_enable oneclickvirt
            ;;
        freebsd-service)
            cat > /usr/local/etc/rc.d/oneclickvirt << SERV_EOF
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
            chmod +x /usr/local/etc/rc.d/oneclickvirt
            service_enable oneclickvirt
            ;;
        sysv-service)
            cat > /etc/init.d/oneclickvirt << SERV_EOF
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
            chmod +x /etc/init.d/oneclickvirt
            ;;
        none|rcctl)
            cat > "${INSTALL_DIR}/start-oneclickvirt.sh" << SERV_EOF
#!/bin/sh
cd "${SERVER_DIR}" || exit 1
nohup "${bin_path}" > "${INSTALL_DIR}/oneclickvirt.log" 2>&1 &
echo \$! > "${INSTALL_DIR}/oneclickvirt.pid"
SERV_EOF
            chmod +x "${INSTALL_DIR}/start-oneclickvirt.sh"
            log_warning "No fully supported service manager detected; created ${INSTALL_DIR}/start-oneclickvirt.sh" \
                "未检测到完整支持的服务管理器；已创建 ${INSTALL_DIR}/start-oneclickvirt.sh"
            ;;
    esac
}

start_oneclickvirt_service() {
    case "$SERVICE_MANAGER" in
        none|rcctl)
            "${INSTALL_DIR}/start-oneclickvirt.sh"
            ;;
        *)
            service_restart oneclickvirt || service_start oneclickvirt
            ;;
    esac
}

start_proxy_service() {
    case "$PROXY" in
        caddy)
            if [[ "$SERVICE_MANAGER" == "none" || "$SERVICE_MANAGER" == "rcctl" ]]; then
                [[ -x "${INSTALL_DIR}/start-caddy.sh" ]] && "${INSTALL_DIR}/start-caddy.sh" || true
            else
                service_enable caddy 2>/dev/null || true
                service_restart caddy 2>/dev/null || service_start caddy 2>/dev/null || true
            fi
            ;;
        nginx|openresty)
            if [[ "$SERVICE_MANAGER" == "none" || "$SERVICE_MANAGER" == "rcctl" ]]; then
                if have_cmd "$PROXY"; then
                    "$PROXY" -t >/dev/null 2>&1 && "$PROXY" -s reload >/dev/null 2>&1 || "$PROXY" >/dev/null 2>&1 || true
                elif have_cmd nginx; then
                    nginx -t >/dev/null 2>&1 && nginx -s reload >/dev/null 2>&1 || nginx >/dev/null 2>&1 || true
                fi
            else
                service_enable "$PROXY" 2>/dev/null || true
                service_restart "$PROXY" 2>/dev/null || service_start "$PROXY" 2>/dev/null || true
            fi
            ;;
    esac
}

install_application() {
    log_info "Installing OneClickVirt application..." "正在安装 OneClickVirt 应用..."
    local ARCH; ARCH=$(detect_arch)
    local ASSET_OS; ASSET_OS=$(release_os)
    local TAR_BIN; TAR_BIN=$(tar_cmd) || {
        log_error "tar/gtar is required but was not found." "需要 tar/gtar，但未找到。"
        return 1
    }

    mkdir -p "$SERVER_DIR" "$WEB_DIR"

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
    rm -rf "$extract_dir"
    mkdir -p "$extract_dir"
    "$TAR_BIN" -xzf "/tmp/${SERVER_FILE}" -C "$extract_dir"
    local server_bin
    server_bin=$(find "$extract_dir" -type f -name 'server-allinone-*' -print | head -1)
    if [[ -z "$server_bin" ]]; then
        log_error "Extracted archive did not contain server-allinone binary." "解压后的归档中未找到 server-allinone 二进制。"
        return 1
    fi
    cp "$server_bin" "$SERVER_DIR/"
    local SERVER_BIN
    SERVER_BIN="${SERVER_DIR}/$(basename "$server_bin")"
    chmod +x "$SERVER_BIN"

    # Download web dist
    local WEB_FILE="web-dist.zip"
    log_info "Downloading $WEB_FILE..." "正在下载 $WEB_FILE..."
    download_file "${BASE_URL}/${WEB_FILE}" "/tmp/${WEB_FILE}" || {
        log_warning "Failed to download web-dist.zip (all-in-one server embeds frontend)" "下载 web-dist.zip 失败（all-in-one 服务器已内置前端）"
    }
    if [[ -f "/tmp/${WEB_FILE}" ]]; then
        unzip -o "/tmp/${WEB_FILE}" -d "$WEB_DIR" 2>/dev/null || true
    fi

    # Create config.yaml (password YAML-safe: wrap in quotes, escape special chars)
    local _yaml_db_password
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        _yaml_db_password=$(printf '%s' "$DB_PASS_EXT" | sed 's/"/\\"/g')
    else
        _yaml_db_password=$(printf '%s' "$DB_PASSWORD" | sed 's/"/\\"/g')
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
    cat > "${SERVER_DIR}/config.yaml" << CONFIG_EOF
system:
  env: public
  addr: 8888
  db-type: ${DB_TYPE}
jwt:
  signing-key: "$(head -c 32 /dev/urandom | base64 | tr -d '\n')"
  expires-time: 7d
  buffer-time: 1d
  issuer: oneclickvirt
mysql:
  path: ${_db_host}
  port: "${_db_port}"
  db-name: ${_db_name}
  username: ${_db_user}
  password: "${_yaml_db_password}"
  config: charset=utf8mb4&parseTime=True&loc=Local&time_zone=%27%2B08%3A00%27
  max-idle-conns: "10"
  max-open-conns: "100"
  log-mode: error
  log-zap: "false"
  max-lifetime: "3600"
  auto-create: "true"
CONFIG_EOF

    printf "%s\n" "${VERSION:-unknown}" > "${INSTALL_DIR}/VERSION"
    printf "%s\n" "$SERVER_FILE" > "${INSTALL_DIR}/SERVER_ASSET"
    install_oneclickvirt_service "$SERVER_BIN"

    # Start reverse proxy if configured
    start_proxy_service

    # Start the server
    log_info "Starting OneClickVirt service..." "正在启动 OneClickVirt 服务..."
    start_oneclickvirt_service
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
    echo -e "${CYAN}  OneClickVirt Full Installation${NC}"
    echo -e "${CYAN}============================================${NC}"
    echo ""

    check_root
    detect_os
    check_system_resources
    install_dependencies || exit 1

    # ---- Interactive prompts (if not non-interactive) ----
    if [[ "$NONINTERACTIVE" != "true" ]]; then
        echo ""
        echo -e "${CYAN}--- Configuration ---${NC}"
        echo -e "  (Press Enter to accept defaults shown in brackets)"

        read -r -p "Database type [mysql/mariadb] (default: ${DB_TYPE}): " _db
        [[ -n "$_db" ]] && DB_TYPE="$_db"

        read -r -p "Reverse proxy [caddy/nginx/openresty] (default: ${PROXY}): " _px
        [[ -n "$_px" ]] && PROXY="$_px"

        local domain_prompt="Domain name or IP"
        [[ -n "$DOMAIN" && "$DOMAIN" != "localhost" ]] && domain_prompt="Domain name or IP [${DOMAIN}]"
        domain_prompt="${domain_prompt} (Enter to auto-detect, e.g. panel.example.com): "
        read -r -p "$domain_prompt" _dom
        if [[ -n "$_dom" ]]; then
            normalize_domain "$_dom"
        else
            # No domain entered — detect IPs and offer choices
            echo ""
            echo -e "  ${CYAN}No domain provided. Detecting IP addresses...${NC}"
            local pub_ip="" priv_ip=""
            pub_ip=$(detect_public_ipv4 2>/dev/null || true)
            priv_ip=$(detect_private_ipv4 2>/dev/null || true)

            echo ""
            echo -e "  ${CYAN}Select an address to use:${NC}"
            local opt_num=1
            if [[ -n "$pub_ip" ]]; then
                echo "  [${opt_num}] Public IPv4:  ${pub_ip}  (recommended for internet access)"
                opt_num=$((opt_num + 1))
            fi
            echo "  [${opt_num}] Localhost:     127.0.0.1  (local access only)"
            opt_num=$((opt_num + 1))
            if [[ -n "$priv_ip" && "$priv_ip" != "$pub_ip" ]]; then
                echo "  [${opt_num}] Private IPv4:  ${priv_ip}  (LAN access)"
                opt_num=$((opt_num + 1))
            fi
            echo "  [${opt_num}] Enter a custom domain/IP manually"

            local choice
            read -r -p "  Your choice [1-${opt_num}] (default: 1): " choice
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
                read -r -p "  Enter custom domain or IP: " _custom_dom
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
                local tls_prompt="TLS method [letsencrypt/zerossl/selfsigned/off]"
                [[ -n "$TLS_METHOD" ]] && tls_prompt="${tls_prompt} (default: ${TLS_METHOD})"
                read -r -p "${tls_prompt}: " _tls
                [[ -n "$_tls" ]] && TLS_METHOD="$_tls"
            fi

            if [[ "$TLS_METHOD" != "off" && "$TLS_METHOD" != "selfsigned" ]]; then
                local email_prompt="Email for TLS certificate"
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
        read -r -p "Install local database? [Y/n] (n = use external DB): " _local_db
        if [[ "$_local_db" =~ ^[Nn] ]]; then
            EXTERNAL_DB="true"
            echo -e "  ${CYAN}External database configuration:${NC}"
            read -r -p "  DB Host (default: 127.0.0.1): " _db_host
            DB_HOST="${_db_host:-127.0.0.1}"
            read -r -p "  DB Port (default: 3306): " _db_port
            DB_PORT="${_db_port:-3306}"
            read -r -p "  DB Name (default: oneclickvirt): " _db_name
            DB_NAME_EXT="${_db_name:-oneclickvirt}"
            read -r -p "  DB User (default: oneclickvirt): " _db_user
            DB_USER_EXT="${_db_user:-oneclickvirt}"
            read -r -p "  DB Password: " _db_pass
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

    echo ""
    echo -e "${CYAN}--- Installation Summary ---${NC}"
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
        read -r -p "Proceed with installation? [Y/n]: " _confirm
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
    echo -e "${GREEN}  Installation Complete!${NC}"
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

    echo -e "  Database:     ${DB_TYPE} (database: oneclickvirt)"
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        echo -e "  DB Host:      ${DB_HOST}:${DB_PORT}/${DB_NAME_EXT}"
        echo -e "  DB User:      ${DB_USER_EXT}"
        echo -e "  DB Password:  ${DB_PASS_EXT}"
    else
        echo -e "  DB Password:  ${DB_PASSWORD}"
        echo -e "  DB User:      oneclickvirt (localhost only)"
    fi
    echo -e "  Proxy:        ${PROXY}"
    echo -e "  URL:          ${_display_url}"
    echo ""
    echo -e "  Server Logs:  journalctl -u oneclickvirt -f"
    echo -e "  Proxy Logs:   journalctl -u ${PROXY} -f"
    echo -e "  Config Dir:   ${SERVER_DIR}"
    echo ""
    echo -e "${YELLOW}  IMPORTANT — First-Run Setup:${NC}"
    echo -e "  - Admin account has been auto-created (if local DB was installed):"
    echo -e "    Username:  ${ADMIN_USER}"
    echo -e "    Password:  ${ADMIN_PASS}"
    echo -e "  - Login at: ${_display_url}"
    echo -e "  - CHANGE THE PASSWORD after first login!"
    if [[ "$EXTERNAL_DB" != "true" ]]; then
        echo -e "  - Database is LOCALHOST-only (bind-address=127.0.0.1) — not exposed to internet."
    fi
    echo ""
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        echo -e "${YELLOW}  External Database Credentials:${NC}"
        echo -e "  DB Host:      ${DB_HOST}:${DB_PORT}"
        echo -e "  DB Name:      ${DB_NAME_EXT}"
        echo -e "  DB User:      ${DB_USER_EXT}"
        echo -e "  DB Password:  ${DB_PASS_EXT}"
    else
        echo -e "${YELLOW}  Database Credentials (auto-generated, for config.yaml):${NC}"
        echo -e "  DB Name:      oneclickvirt"
        echo -e "  DB User:      oneclickvirt"
        echo -e "  DB Password:  ${DB_PASSWORD}"
    fi
    echo ""

    # Save credentials
    if [[ "$EXTERNAL_DB" == "true" ]]; then
        cat > "${INSTALL_DIR}/.credentials" << CRED
Database: ${DB_TYPE} (EXTERNAL)
DB Host: ${DB_HOST}:${DB_PORT}
DB Name: ${DB_NAME_EXT}
DB User: ${DB_USER_EXT}
DB Password: ${DB_PASS_EXT}
Admin Username: ${ADMIN_USER}
Admin Password: ${ADMIN_PASS}
URL: ${_display_url}
CRED
    else
        cat > "${INSTALL_DIR}/.credentials" << CRED
Database: ${DB_TYPE}
Database Name: oneclickvirt
Database User: oneclickvirt
Database Password: ${DB_PASSWORD}
Admin Username: ${ADMIN_USER}
Admin Password: ${ADMIN_PASS}
URL: ${_display_url}
CRED
    fi
    chmod 600 "${INSTALL_DIR}/.credentials"
    log_info "Credentials saved to ${INSTALL_DIR}/.credentials" "凭据已保存至 ${INSTALL_DIR}/.credentials"
}

main "$@"
