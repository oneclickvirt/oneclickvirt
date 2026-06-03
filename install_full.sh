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

# ---- Usage ----
usage() {
    cat << 'EOF'
Usage: bash install_full.sh [OPTIONS]

Options:
  --db-type TYPE          Database type: mysql (default) or mariadb
  --db-password PASS      Database root password (auto-generated if not set)
  --proxy TYPE            Reverse proxy: caddy, nginx, openresty (default: caddy)
  --domain DOMAIN         Domain name or IP (e.g. panel.example.com, 1.2.3.4)
                          Prefix with https:// to enable TLS, http:// to disable.
                          If omitted in interactive mode, auto-detects public/private IP
                          and prompts for choice (public IPv4 / localhost / private IPv4).
  --email EMAIL           Email for TLS certificate notifications
  --tls METHOD            TLS method: letsencrypt, zerossl, selfsigned, off
                          TLS requires a real domain name (not bare IP or localhost).
  --non-interactive       Run without prompts (requires --domain and --email when using TLS)
  --force                 Skip system resource checks (disk & memory)
  --version VERSION       Specific version to install (default: latest)
  --help                  Show this help

Examples:
  # Interactive mode — just press Enter at the domain prompt to pick an IP
  bash install_full.sh

  # Non-interactive with a domain + TLS
  bash install_full.sh --non-interactive --domain https://panel.example.com --email admin@example.com

  # Non-interactive with a public IP (TLS disabled)
  bash install_full.sh --non-interactive --domain http://1.2.3.4
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
        --version)        INSTALL_VERSION="$2"; shift 2 ;;
        --help)           usage ;;
        *) log_error "Unknown option: $1"; usage ;;
    esac
done

# Apply protocol-detected TLS from --domain prefix (only if --tls not explicitly set)
if [[ "$TLS_EXPLICIT" != "true" && -n "$DOMAIN_PROTO_DETECTED" ]]; then
    if [[ "$DOMAIN_PROTO_DETECTED" == "https" ]]; then
        TLS_METHOD="letsencrypt"
        log_info "Detected https:// prefix — TLS enabled (${TLS_METHOD})"
    elif [[ "$DOMAIN_PROTO_DETECTED" == "http" ]]; then
        TLS_METHOD="off"
        log_info "Detected http:// prefix — TLS disabled"
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
        log_error "This script must be run as root (use sudo)."
        exit 1
    fi
}

check_system_resources() {
    if [[ "${SKIP_RESOURCE_CHECK:-false}" == "true" || "${FORCE_INSTALL:-false}" == "true" ]]; then
        log_warning "Skipping disk/memory resource checks (SKIP_RESOURCE_CHECK or --force)"
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

    log_success "System resource checks passed."
}

detect_arch() {
    case $(uname -m) in
        x86_64|amd64) echo "amd64" ;;
        aarch64|arm64) echo "arm64" ;;
        *) log_error "Unsupported architecture: $(uname -m)"; exit 1 ;;
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
        ip=$(ip -4 addr show scope global 2>/dev/null | grep -oP 'inet \K[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+' | grep -v '^127\.' | head -1)
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

detect_os() {
    if [[ -f /etc/os-release ]]; then
        . /etc/os-release
        OS="$ID"
        OS_VERSION="$VERSION_ID"
    elif [[ -f /etc/debian_version ]]; then
        OS="debian"
        OS_VERSION="$(cat /etc/debian_version)"
    elif [[ -f /etc/redhat-release ]]; then
        OS="centos"
    else
        OS="unknown"
    fi
    OS=$(echo "$OS" | tr '[:upper:]' '[:lower:]')

    case "$OS" in
        ubuntu|debian|raspbian)
            PKG_UPDATE="apt-get update -qq"
            PKG_INSTALL="apt-get install -y -qq"
            ;;
        centos|rhel|almalinux|rocky|fedora|amzn)
            if command -v dnf &>/dev/null; then
                PKG_UPDATE="dnf -y update"
                PKG_INSTALL="dnf -y install"
            else
                PKG_UPDATE="yum -y update"
                PKG_INSTALL="yum -y install"
            fi
            ;;
        arch|manjaro)
            PKG_UPDATE="pacman -Sy"
            PKG_INSTALL="pacman -S --noconfirm"
            ;;
        alpine)
            PKG_UPDATE="apk update"
            PKG_INSTALL="apk add --no-cache"
            ;;
        *)
            log_warning "Unknown OS: $OS. Attempting apt-get..."
            PKG_UPDATE="apt-get update -qq"
            PKG_INSTALL="apt-get install -y -qq"
            ;;
    esac
    log_success "Detected OS: $OS $OS_VERSION"
}

install_dependencies() {
    log_info "Installing base dependencies..."
    $PKG_UPDATE
    $PKG_INSTALL curl wget tar gzip unzip ca-certificates
    log_success "Base dependencies installed."
}

wait_for_database_ready() {
    local timeout="${1:-60}" interval="${2:-3}" elapsed=0
    log_info "Waiting for database service to become ready..."
    while [[ $elapsed -lt $timeout ]]; do
        if command -v mysql &>/dev/null && mysql -e "SELECT 1" >/dev/null 2>&1; then
            log_success "Database ready after ${elapsed}s"
            return 0
        fi
        if command -v mariadb &>/dev/null && mariadb -e "SELECT 1" >/dev/null 2>&1; then
            log_success "Database ready after ${elapsed}s"
            return 0
        fi
        if command -v mysqladmin &>/dev/null && mysqladmin ping --silent 2>/dev/null; then
            log_success "Database ready after ${elapsed}s"
            return 0
        fi
        if command -v mariadb-admin &>/dev/null && mariadb-admin ping --silent 2>/dev/null; then
            log_success "Database ready after ${elapsed}s"
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    log_error "Database did not become ready within ${timeout}s"
    return 1
}

wait_for_http_ready() {
    local url="$1" timeout="${2:-120}" interval="${3:-5}" elapsed=0
    log_info "Waiting for OneClickVirt API health endpoint..."
    while [[ $elapsed -lt $timeout ]]; do
        if curl -fsS --max-time 5 "$url" >/dev/null 2>&1; then
            log_success "API health endpoint ready after ${elapsed}s"
            return 0
        fi
        sleep "$interval"
        elapsed=$((elapsed + interval))
    done
    log_error "API health endpoint was not ready within ${timeout}s: ${url}"
    return 1
}

# ---- Database installation ----
install_mysql() {
    log_info "Installing MySQL 8.0..."
    case "$OS" in
        ubuntu|debian|raspbian)
            $PKG_INSTALL mysql-server mysql-client
            ;;
        centos|rhel|almalinux|rocky|fedora)
            $PKG_INSTALL mysql-server mysql
            ;;
        arch|manjaro)
            $PKG_INSTALL mysql
            ;;
        alpine)
            $PKG_INSTALL mysql mysql-client
            ;;
        *)
            $PKG_INSTALL mysql-server mysql-client
            ;;
    esac
    log_success "MySQL installed."
}

install_mariadb() {
    log_info "Installing MariaDB..."
    case "$OS" in
        ubuntu|debian|raspbian)
            $PKG_INSTALL mariadb-server mariadb-client
            ;;
        centos|rhel|almalinux|rocky|fedora)
            $PKG_INSTALL mariadb-server mariadb
            ;;
        arch|manjaro)
            $PKG_INSTALL mariadb
            ;;
        alpine)
            $PKG_INSTALL mariadb mariadb-client
            ;;
        *)
            $PKG_INSTALL mariadb-server mariadb-client
            ;;
    esac
    log_success "MariaDB installed."
}

configure_database() {
    log_info "Configuring database..."

    # Start database service
    case "$OS" in
        alpine)
            rc-service mariadb start 2>/dev/null || rc-service mysql start 2>/dev/null || true
            ;;
        *)
            systemctl start "$DB_TYPE" 2>/dev/null || service "$DB_TYPE" start 2>/dev/null || true
            ;;
    esac
    sleep 3
    wait_for_database_ready 60 3

    # Generate random password if not provided
    if [[ -z "$DB_PASSWORD" ]]; then
        DB_PASSWORD=$(head -c 24 /dev/urandom | base64 | tr -dc 'a-zA-Z0-9' | head -c 20)
    fi

    # Secure the installation and create database
    local DB_NAME="oneclickvirt"
    local DB_USER="oneclickvirt"
    local DB_PASSWORD_SQL; DB_PASSWORD_SQL=$(sql_escape "$DB_PASSWORD")

    case "$DB_TYPE" in
        mysql)
            # Try auth_socket first (Ubuntu default), then password
            if mysql -e "SELECT 1" 2>/dev/null; then
                mysql -e "
                    ALTER USER 'root'@'localhost' IDENTIFIED WITH mysql_native_password BY '${DB_PASSWORD_SQL}';
                    CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED WITH mysql_native_password BY '${DB_PASSWORD_SQL}';
                    GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;
                    CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
                    CREATE USER IF NOT EXISTS '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD_SQL}';
                    GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'127.0.0.1';
                    -- Security hardening
                    DELETE FROM mysql.user WHERE User='';
                    DROP DATABASE IF EXISTS test;
                    DELETE FROM mysql.db WHERE Db='test' OR Db='test\\_%';
                    DELETE FROM mysql.user WHERE Host<>'localhost' AND Host<>'127.0.0.1' AND Host<>'::1';
                    FLUSH PRIVILEGES;
                "
            else
                log_warning "MySQL not accessible via auth_socket, trying password..."
                # MariaDB-compatible syntax
                mysql -u root -e "
                    CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
                    CREATE USER IF NOT EXISTS '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD_SQL}';
                    GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'127.0.0.1';
                    -- Security hardening
                    DELETE FROM mysql.user WHERE User='';
                    DROP DATABASE IF EXISTS test;
                    DELETE FROM mysql.db WHERE Db='test' OR Db='test\\_%';
                    DELETE FROM mysql.user WHERE Host<>'localhost' AND Host<>'127.0.0.1' AND Host<>'::1';
                    FLUSH PRIVILEGES;
                " 2>/dev/null || {
                    log_error "Failed to configure MySQL. Is it running?"
                    return 1
                }
            fi
            ;;
        mariadb)
            if mariadb -e "SELECT 1" 2>/dev/null; then
                mariadb -e "
                    ALTER USER 'root'@'localhost' IDENTIFIED BY '${DB_PASSWORD_SQL}';
                    CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD_SQL}';
                    GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;
                    CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
                    CREATE USER IF NOT EXISTS '${DB_USER}'@'127.0.0.1' IDENTIFIED BY '${DB_PASSWORD_SQL}';
                    GRANT ALL PRIVILEGES ON ${DB_NAME}.* TO '${DB_USER}'@'127.0.0.1';
                    -- Security hardening
                    DELETE FROM mysql.user WHERE User='';
                    DROP DATABASE IF EXISTS test;
                    DELETE FROM mysql.db WHERE Db='test' OR Db='test\\_%';
                    DELETE FROM mysql.user WHERE Host<>'localhost' AND Host<>'127.0.0.1' AND Host<>'::1';
                    FLUSH PRIVILEGES;
                "
            else
                log_error "MariaDB not accessible. Is it running?"
                return 1
            fi
            ;;
    esac

    # Apply optimization config from deploy/my.cnf
    local MY_CNF="/etc/mysql/conf.d/oneclickvirt.cnf"
    if [[ -f "${SCRIPT_DIR}/deploy/my.cnf" ]]; then
        cp "${SCRIPT_DIR}/deploy/my.cnf" "$MY_CNF"
    else
        # Download from GitHub
        curl -fsSL "https://raw.githubusercontent.com/${REPO}/main/deploy/my.cnf" -o "$MY_CNF" 2>/dev/null || true
    fi

    # ── Enforce localhost-only binding ──────────────────────────────────────
    # Ensure bind-address is set to 127.0.0.1 in the main server config,
    # as a safety net in case the conf.d snippet was not applied.
    _enforce_bind_address() {
        local conf_file=""
        # Find the main mysqld config file
        for f in \
            /etc/mysql/mysql.conf.d/mysqld.cnf \
            /etc/mysql/my.cnf \
            /etc/my.cnf \
            /etc/mysql/mariadb.conf.d/50-server.cnf \
            /etc/my.cnf.d/server.cnf; do
            if [[ -f "$f" ]]; then conf_file="$f"; break; fi
        done
        if [[ -z "$conf_file" ]]; then
            log_warning "Could not locate MySQL/MariaDB config file to enforce bind-address."
            return 0
        fi
        # Only add if not already present
        if grep -q '^\s*bind-address\s*=' "$conf_file" 2>/dev/null; then
            sed -i 's/^\s*bind-address\s*=.*/bind-address = 127.0.0.1/' "$conf_file"
        else
            # Insert after [mysqld] section header
            sed -i '/^\[mysqld\]/a bind-address = 127.0.0.1' "$conf_file"
        fi
        log_info "Database bind-address enforced to 127.0.0.1 (local access only)."
    }
    _enforce_bind_address

    # Enable and restart
    case "$OS" in
        alpine) rc-update add "$DB_TYPE" 2>/dev/null; rc-service "$DB_TYPE" restart 2>/dev/null ;;
        *) systemctl enable "$DB_TYPE" 2>/dev/null; systemctl restart "$DB_TYPE" 2>/dev/null ;;
    esac

    log_success "Database configured: ${DB_NAME} / user=${DB_USER} (localhost only)"
}

# ---- Reverse proxy installation ----
install_caddy() {
    log_info "Installing Caddy..."
    if command -v caddy &>/dev/null; then
        log_success "Caddy already installed."
        return 0
    fi
    curl -fsSL "https://caddyserver.com/api/download?os=linux&arch=$(detect_arch)" -o /usr/local/bin/caddy
    chmod +x /usr/local/bin/caddy
    mkdir -p /etc/caddy /var/log/caddy

    # Create Caddy systemd service
    cat > /etc/systemd/system/caddy.service << 'EOF'
[Unit]
Description=Caddy Web Server
After=network.target
[Service]
ExecStart=/usr/local/bin/caddy run --config /etc/caddy/Caddyfile
ExecReload=/usr/local/bin/caddy reload --config /etc/caddy/Caddyfile
Restart=on-failure
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
EOF
    systemctl daemon-reload 2>/dev/null || true
    log_success "Caddy installed."
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

    cat > /etc/caddy/Caddyfile << CADDY_EOF
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
        output file /var/log/caddy/access.log
        level INFO
    }
}
CADDY_EOF
    log_success "Caddy configuration written to /etc/caddy/Caddyfile"
}

install_nginx() {
    log_info "Installing Nginx..."
    case "$OS" in
        ubuntu|debian|raspbian) $PKG_INSTALL nginx certbot python3-certbot-nginx ;;
        centos|rhel|almalinux|rocky) $PKG_INSTALL nginx certbot python3-certbot-nginx ;;
        fedora) $PKG_INSTALL nginx certbot python3-certbot-nginx ;;
        arch|manjaro) $PKG_INSTALL nginx certbot certbot-nginx ;;
        alpine) $PKG_INSTALL nginx certbot ;;
        *) $PKG_INSTALL nginx certbot python3-certbot-nginx ;;
    esac
    log_success "Nginx installed."
}

configure_nginx() {
    # Download default nginx config from repo or use local
    local NGINX_CONF="/etc/nginx/sites-available/oneclickvirt"
    mkdir -p /etc/nginx/sites-available /etc/nginx/sites-enabled

    if [[ -f "${SCRIPT_DIR}/deploy/default.conf" ]]; then
        cp "${SCRIPT_DIR}/deploy/default.conf" "$NGINX_CONF"
    else
        curl -fsSL "https://raw.githubusercontent.com/${REPO}/main/deploy/default.conf" -o "$NGINX_CONF" 2>/dev/null
    fi

    # Customize for the domain
    sed -i "s/server_name localhost/server_name ${DOMAIN}/g" "$NGINX_CONF"
    sed -i "s|root /usr/share/nginx/html|root ${WEB_DIR}|g" "$NGINX_CONF"

    ln -sf "$NGINX_CONF" /etc/nginx/sites-enabled/oneclickvirt 2>/dev/null || true
    rm -f /etc/nginx/sites-enabled/default 2>/dev/null || true

    # TLS via certbot
    if [[ "$TLS_METHOD" == "letsencrypt" || "$TLS_METHOD" == "zerossl" ]]; then
        log_info "Obtaining TLS certificate via Certbot..."
        if [[ -n "$EMAIL" ]]; then
            certbot --nginx -d "$DOMAIN" --non-interactive --agree-tos --email "$EMAIL" 2>/dev/null || {
                log_warning "Certbot failed. You may need to run: certbot --nginx -d ${DOMAIN}"
            }
        fi
    fi

    log_success "Nginx configuration written."
}

install_openresty() {
    log_info "Installing OpenResty..."
    case "$OS" in
        ubuntu|debian|raspbian)
            $PKG_INSTALL --no-install-recommends wget gnupg ca-certificates
            wget -qO - https://openresty.org/package/pubkey.gpg | apt-key add -
            echo "deb http://openresty.org/package/${OS} $(lsb_release -sc 2>/dev/null || echo 'focal') main" \
                > /etc/apt/sources.list.d/openresty.list
            apt-get update -qq
            $PKG_INSTALL openresty
            ;;
        centos|rhel|almalinux|rocky)
            $PKG_INSTALL yum-utils
            yum-config-manager --add-repo https://openresty.org/package/${OS}/openresty.repo
            $PKG_INSTALL openresty
            ;;
        fedora)
            $PKG_INSTALL openresty
            ;;
        *)
            log_warning "OpenResty auto-install not supported for ${OS}. Trying generic install..."
            $PKG_INSTALL nginx
            ;;
    esac
    log_success "OpenResty installed."
}

configure_openresty() {
    # OpenResty is Nginx-compatible - reuse nginx config
    configure_nginx
    # Adjust paths for OpenResty
    sed -i 's|/etc/nginx/|/usr/local/openresty/nginx/conf/|g' /usr/local/openresty/nginx/conf/nginx.conf 2>/dev/null || true
    log_success "OpenResty configured."
}

# ---- Firewall configuration ----
configure_firewall() {
    log_info "Configuring firewall..."
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
    log_success "Firewall configured (80, 443 open)."
}

# ---- Application installation ----
get_latest_version() {
    if [[ -n "$INSTALL_VERSION" ]]; then
        VERSION="$INSTALL_VERSION"
        BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
        log_info "Using specified version: $VERSION"
        return 0
    fi

    local api_urls=(
        "https://api.github.com"
        "https://githubapi.spiritlhl.workers.dev"
        "https://githubapi.spiritlhl.top"
    )

    for api in "${api_urls[@]}"; do
        local resp
        resp=$(curl -sL --connect-timeout 10 --max-time 30 "${api}/repos/${REPO}/releases/latest" 2>/dev/null)
        VERSION=$(echo "$resp" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
        if [[ -n "$VERSION" && "$VERSION" != "null" ]]; then
            BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
            log_success "Latest version: $VERSION"
            return 0
        fi
    done

    log_error "Failed to fetch latest version."
    return 1
}

install_application() {
    log_info "Installing OneClickVirt application..."
    local ARCH; ARCH=$(detect_arch)

    mkdir -p "$SERVER_DIR" "$WEB_DIR"

    # Download server (all-in-one with embedded frontend)
    local SERVER_FILE="server-allinone-linux-${ARCH}.tar.gz"
    log_info "Downloading $SERVER_FILE..."
    curl -fsSL "${BASE_URL}/${SERVER_FILE}" -o "/tmp/${SERVER_FILE}" || {
        log_error "Failed to download server binary."
        return 1
    }
    tar -xzf "/tmp/${SERVER_FILE}" -C "$SERVER_DIR"
    chmod +x "$SERVER_DIR"/server-allinone-linux-*

    # Download web dist
    local WEB_FILE="web-dist.zip"
    log_info "Downloading $WEB_FILE..."
    curl -fsSL "${BASE_URL}/${WEB_FILE}" -o "/tmp/${WEB_FILE}" || {
        log_warning "Failed to download web-dist.zip (all-in-one server embeds frontend)"
    }
    if [[ -f "/tmp/${WEB_FILE}" ]]; then
        unzip -o "/tmp/${WEB_FILE}" -d "$WEB_DIR" 2>/dev/null || true
    fi

    # Create config.yaml
    cat > "${SERVER_DIR}/config.yaml" << CONFIG_EOF
system:
  env: public
  addr: 8888
  db-type: ${DB_TYPE}
jwt:
  signing-key: "$(head -c 32 /dev/urandom | base64)"
  expires-time: 7d
  buffer-time: 1d
  issuer: oneclickvirt
${DB_TYPE}:
  path: 127.0.0.1
  port: "3306"
  db-name: oneclickvirt
  username: oneclickvirt
  password: "${DB_PASSWORD}"
CONFIG_EOF

    # Create systemd service
    cat > /etc/systemd/system/oneclickvirt.service << SERV_EOF
[Unit]
Description=OneClickVirt Server
After=network.target ${DB_TYPE}.service
Requires=${DB_TYPE}.service

[Service]
Type=simple
WorkingDirectory=${SERVER_DIR}
ExecStart=${SERVER_DIR}/server-allinone-linux-${ARCH}
Restart=on-failure
RestartSec=5
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERV_EOF

    systemctl daemon-reload
    systemctl enable oneclickvirt

    # Start reverse proxy if configured
    case "$PROXY" in
        caddy)
            systemctl enable caddy 2>/dev/null || true
            systemctl restart caddy 2>/dev/null || true
            ;;
        nginx|openresty)
            systemctl enable "$PROXY" 2>/dev/null || true
            systemctl restart "$PROXY" 2>/dev/null || true
            ;;
    esac

    # Start the server
    systemctl restart oneclickvirt
    wait_for_http_ready "http://127.0.0.1:8888/api/v1/health" 120 5

    # Cleanup
    rm -f /tmp/"${SERVER_FILE}" /tmp/"${WEB_FILE}"

    log_success "Application installed and started."
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
    install_dependencies

    # ---- Interactive prompts (if not non-interactive) ----
    if [[ "$NONINTERACTIVE" != "true" ]]; then
        echo ""
        echo -e "${CYAN}--- Configuration ---${NC}"
        echo -e "  (Press Enter to accept defaults shown in brackets)"

        read -p "Database type [mysql/mariadb] (default: ${DB_TYPE}): " _db
        [[ -n "$_db" ]] && DB_TYPE="$_db"

        read -p "Reverse proxy [caddy/nginx/openresty] (default: ${PROXY}): " _px
        [[ -n "$_px" ]] && PROXY="$_px"

        local domain_prompt="Domain name or IP"
        [[ -n "$DOMAIN" && "$DOMAIN" != "localhost" ]] && domain_prompt="Domain name or IP [${DOMAIN}]"
        domain_prompt="${domain_prompt} (Enter to auto-detect, e.g. panel.example.com): "
        read -p "$domain_prompt" _dom
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
            read -p "  Your choice [1-${opt_num}] (default: 1): " choice
            choice=${choice:-1}

            # Recalculate option positions based on what was shown
            local pos=1
            if [[ -n "$pub_ip" ]]; then
                if [[ "$choice" == "$pos" ]]; then
                    DOMAIN="$pub_ip"
                    DOMAIN_PROTO_DETECTED="http"
                    log_info "Using public IPv4: ${DOMAIN}"
                fi
                pos=$((pos + 1))
            fi
            # localhost
            if [[ -z "$DOMAIN" && "$choice" == "$pos" ]]; then
                DOMAIN="localhost"
                log_info "Using localhost (127.0.0.1)"
            fi
            pos=$((pos + 1))
            # private IP
            if [[ -n "$priv_ip" && "$priv_ip" != "$pub_ip" ]]; then
                if [[ -z "$DOMAIN" && "$choice" == "$pos" ]]; then
                    DOMAIN="$priv_ip"
                    DOMAIN_PROTO_DETECTED="http"
                    log_info "Using private IPv4: ${DOMAIN}"
                fi
                pos=$((pos + 1))
            fi
            # custom
            if [[ -z "$DOMAIN" && "$choice" == "$pos" ]]; then
                read -p "  Enter custom domain or IP: " _custom_dom
                if [[ -n "$_custom_dom" ]]; then
                    normalize_domain "$_custom_dom"
                else
                    DOMAIN="localhost"
                    log_warning "No input — falling back to localhost"
                fi
            fi
            DOMAIN="${DOMAIN:-localhost}"
        fi
        log_info "Domain set to: ${DOMAIN}"

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
                log_info "Detected https:// — TLS will use Let's Encrypt"
            elif [[ "$DOMAIN_PROTO_DETECTED" == "http" ]]; then
                TLS_METHOD="off"
                log_info "Detected http:// — TLS disabled"
            else
                local tls_prompt="TLS method [letsencrypt/zerossl/selfsigned/off]"
                [[ -n "$TLS_METHOD" ]] && tls_prompt="${tls_prompt} (default: ${TLS_METHOD})"
                read -p "${tls_prompt}: " _tls
                [[ -n "$_tls" ]] && TLS_METHOD="$_tls"
            fi

            if [[ "$TLS_METHOD" != "off" && "$TLS_METHOD" != "selfsigned" ]]; then
                local email_prompt="Email for TLS certificate"
                [[ -n "$EMAIL" ]] && email_prompt="${email_prompt} [${EMAIL}]"
                read -p "${email_prompt}: " _em
                [[ -n "$_em" ]] && EMAIL="$_em"
            fi
        else
            TLS_METHOD="off"
            if [[ "$DOMAIN" == "localhost" ]]; then
                log_info "Using localhost — TLS disabled."
            else
                log_info "Using bare IP address — TLS disabled (certificates require a domain name)."
            fi
        fi
    fi

    # Validate non-interactive mode requirements
    if [[ "$NONINTERACTIVE" == "true" ]]; then
        if [[ "$TLS_METHOD" != "off" && "$TLS_METHOD" != "selfsigned" ]]; then
            if [[ -z "$DOMAIN" || "$DOMAIN" == "localhost" || "$DOMAIN" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
                log_error "--domain must be a real domain name (not localhost or bare IP) for TLS in non-interactive mode."
                exit 1
            fi
            if [[ -z "$EMAIL" ]]; then
                log_error "--email is required for TLS in non-interactive mode."
                exit 1
            fi
        fi
    fi
    DOMAIN="${DOMAIN:-localhost}"

    echo ""
    echo -e "${CYAN}--- Installation Summary ---${NC}"
    echo "  Database:     ${DB_TYPE}"
    echo "  Proxy:        ${PROXY}"
    echo "  Domain:       ${DOMAIN:-localhost}"
    echo "  TLS:          ${TLS_METHOD}"
    echo "  Install Dir:  ${INSTALL_DIR}"
    echo ""

    if [[ "$NONINTERACTIVE" != "true" ]]; then
        read -p "Proceed with installation? [Y/n]: " _confirm
        [[ "$_confirm" =~ ^[Nn] ]] && { log_info "Installation cancelled."; exit 0; }
    fi

    # ---- Install ----
    log_info "Starting installation..."

    # 1. Database
    case "$DB_TYPE" in
        mysql) install_mysql ;;
        mariadb) install_mariadb ;;
    esac
    configure_database

    # 2. Reverse proxy
    case "$PROXY" in
        caddy)
            install_caddy
            configure_caddy
            ;;
        nginx)
            install_nginx
            configure_nginx
            ;;
        openresty)
            install_openresty
            configure_openresty
            ;;
    esac

    # 3. Firewall
    configure_firewall

    # 4. Application
    get_latest_version
    install_application

    # ---- Done ----
    echo ""
    echo -e "${GREEN}============================================${NC}"
    echo -e "${GREEN}  Installation Complete!${NC}"
    echo -e "${GREEN}============================================${NC}"
    echo ""
    echo -e "  Database:     ${DB_TYPE} (database: oneclickvirt)"
    echo -e "  DB Password:  ${DB_PASSWORD}"
    echo -e "  Proxy:        ${PROXY}"
    if [[ -n "$DOMAIN" && "$DOMAIN" != "localhost" ]]; then
        echo -e "  URL:          https://${DOMAIN}"
    else
        echo -e "  URL:          http://$(curl -s ifconfig.me 2>/dev/null || echo 'YOUR_IP')"
    fi
    echo ""
    echo -e "  Server Logs:  journalctl -u oneclickvirt -f"
    echo -e "  Proxy Logs:   journalctl -u ${PROXY} -f"
    echo -e "  Config Dir:   ${SERVER_DIR}"
    echo ""
    echo -e "${YELLOW}  NOTE: Save the database password!${NC}"
    echo -e "  ${DB_PASSWORD}"
    echo ""

    # Save credentials
    cat > "${INSTALL_DIR}/.credentials" << CRED
Database: ${DB_TYPE}
Database Name: oneclickvirt
Database User: oneclickvirt
Database Password: ${DB_PASSWORD}
CRED
    chmod 600 "${INSTALL_DIR}/.credentials"
    log_info "Credentials saved to ${INSTALL_DIR}/.credentials"
}

main "$@"
