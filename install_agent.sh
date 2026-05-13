#!/bin/sh
# OneClickVirt Agent Installer
# Usage: curl -fsSL <url>/install_agent.sh | sh -s -- --ws-url <WS_URL> --secret <SECRET>
# Source: https://github.com/oneclickvirt/oneclickvirt

set -e

# ── colour helpers ────────────────────────────────────────────────────────────
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()    { printf "${BLUE}[INFO]${NC} %s\n"    "$1"; }
log_success() { printf "${GREEN}[SUCCESS]${NC} %s\n" "$1"; }
log_warning() { printf "${YELLOW}[WARNING]${NC} %s\n" "$1"; }
log_error()   { printf "${RED}[ERROR]${NC} %s\n"   "$1" >&2; }

# ── argument parsing ──────────────────────────────────────────────────────────
WS_URL=""
SECRET=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --ws-url)  WS_URL="$2";  shift 2 ;;
    --secret)  SECRET="$2";  shift 2 ;;
    *) log_error "Unknown argument: $1"; exit 1 ;;
  esac
done

if [ -z "$WS_URL" ] || [ -z "$SECRET" ]; then
  log_error "Usage: install_agent.sh --ws-url <WS_URL> --secret <SECRET>"
  exit 1
fi

# ── sanity checks ─────────────────────────────────────────────────────────────
if [ "$(id -u)" -ne 0 ]; then
  log_error "This script must be run as root."
  exit 1
fi

# ── detect architecture ───────────────────────────────────────────────────────
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)          BINARY_SUFFIX="linux-amd64"  ;;
  aarch64|arm64)   BINARY_SUFFIX="linux-arm64"  ;;
  *)
    log_error "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

BINARY_NAME="oneclickvirt-agent-${BINARY_SUFFIX}"
INSTALL_DIR="/opt/oneclickvirt/agent"
BINARY_PATH="${INSTALL_DIR}/oneclickvirt-agent"
SERVICE_NAME="oneclickvirt-agent"
SERVICE_FILE="/etc/systemd/system/${SERVICE_NAME}.service"

REPO="oneclickvirt/oneclickvirt"
CDN_BASE="https://cdn.spiritlhl.net"
GITHUB_API_URLS="https://api.github.com https://githubapi.spiritlhl.workers.dev https://githubapi.spiritlhl.top"

# ── resolve latest release tag ────────────────────────────────────────────────
log_info "Fetching latest release version..."
VERSION=""
for API_URL in $GITHUB_API_URLS; do
  RESPONSE=$(curl -sL --connect-timeout 10 --max-time 30 \
    "${API_URL}/repos/${REPO}/releases/latest" 2>/dev/null) || continue
  VERSION=$(echo "$RESPONSE" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
  [ -n "$VERSION" ] && break
done

if [ -z "$VERSION" ]; then
  log_error "Failed to fetch latest release version. Check your network."
  exit 1
fi
log_info "Latest version: ${VERSION}"

# ── download binary ───────────────────────────────────────────────────────────
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY_NAME}"
CDN_URL="${CDN_BASE}/${DOWNLOAD_URL}"

mkdir -p "$INSTALL_DIR"
TMP_FILE="${INSTALL_DIR}/${BINARY_NAME}.tmp"

log_info "Downloading ${BINARY_NAME} ..."

# Try CDN first, then direct GitHub
if curl -fsSL --connect-timeout 15 --max-time 120 -o "$TMP_FILE" "$CDN_URL" 2>/dev/null; then
  log_success "Downloaded via CDN."
elif curl -fsSL --connect-timeout 15 --max-time 120 -o "$TMP_FILE" "$DOWNLOAD_URL" 2>/dev/null; then
  log_success "Downloaded from GitHub."
else
  log_error "Failed to download ${BINARY_NAME}. Check network or release assets."
  rm -f "$TMP_FILE"
  exit 1
fi

# ── install binary ────────────────────────────────────────────────────────────
# Stop existing service before replacing binary
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  log_info "Stopping existing ${SERVICE_NAME} service..."
  systemctl stop "$SERVICE_NAME" || true
fi

mv -f "$TMP_FILE" "$BINARY_PATH"
chmod +x "$BINARY_PATH"
ln -sf "$BINARY_PATH" /usr/local/bin/oneclickvirt-agent
log_success "Binary installed to ${BINARY_PATH}"

# ── create systemd service ────────────────────────────────────────────────────
if ! command -v systemctl >/dev/null 2>&1; then
  log_warning "systemd not found. Starting agent in background instead."
  nohup "$BINARY_PATH" --ws-url "$WS_URL" --secret "$SECRET" \
    >/var/log/oneclickvirt-agent.log 2>&1 &
  log_success "Agent started (PID $!). Log: /var/log/oneclickvirt-agent.log"
  exit 0
fi

log_info "Creating systemd service..."
cat > "$SERVICE_FILE" << EOF
[Unit]
Description=OneClickVirt Agent
Documentation=https://github.com/oneclickvirt/oneclickvirt
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=${INSTALL_DIR}
ExecStart=${BINARY_PATH} --ws-url ${WS_URL} --secret ${SECRET}
Restart=always
RestartSec=10
StartLimitInterval=120
StartLimitBurst=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=${SERVICE_NAME}

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable "$SERVICE_NAME"
systemctl start "$SERVICE_NAME"

sleep 2
if systemctl is-active --quiet "$SERVICE_NAME"; then
  log_success "Agent service started successfully."
  log_info  "Manage: systemctl {start|stop|restart|status} ${SERVICE_NAME}"
  log_info  "Logs:   journalctl -u ${SERVICE_NAME} -f"
else
  log_error "Agent service failed to start. Check logs: journalctl -u ${SERVICE_NAME} -xe"
  exit 1
fi
