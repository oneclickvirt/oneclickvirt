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
CDN_URLS="https://cdn0.spiritlhl.top https://cdn3.spiritlhl.net https://cdn1.spiritlhl.net https://cdn2.spiritlhl.net https://cdn.spiritlhl.net"
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

# ── download binary with retry + fallback ─────────────────────────────────────

mkdir -p "$INSTALL_DIR"
TMP_FILE="${INSTALL_DIR}/${BINARY_NAME}.tmp"

# _dl_progress shows an animated progress bar while a download runs in background
_dl_progress() {
  local out="$1" total="$2" pid="$3" shown=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ -f "$out" ]; then
      local cur
      cur=$(stat -c%s "$out" 2>/dev/null || stat -f%z "$out" 2>/dev/null)
      cur=$(echo "$cur" | tr -d '\r\n' | grep -o '[0-9]*' | head -1)
      cur=${cur:-0}; cur=$((10#$cur))
      if ! [[ "$cur" =~ ^[0-9]+$ ]]; then cur=0; fi
      if [ "$total" -gt 0 ] && [ "$cur" -gt 0 ]; then
        local pct=$((cur * 100 / total)); pct=$((pct > 100 ? 100 : pct))
        if [ "$pct" -gt "$shown" ]; then
          local bar="" filled=$((pct / 2)) i=0
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

# download_one attempts a single URL with curl → wget fallback, showing progress
download_one() {
  local url="$1"
  local total=0
  # Get file size from headers for progress bar
  total=$(curl -sIkL --connect-timeout 10 "$url" 2>/dev/null | grep -i Content-Length | awk '{print $2}' | tr -d '\r\n' | grep -o '[0-9]*' | tail -1)
  total=${total:-0}; total=$((10#$total))
  if ! [[ "$total" =~ ^[0-9]+$ ]]; then total=0; fi

  curl -fsSL --connect-timeout 15 --max-time 300 -o "$TMP_FILE" "$url" 2>/dev/null &
  local dl_pid=$!
  _dl_progress "$TMP_FILE" "$total" "$dl_pid" &
  local mon_pid=$!
  wait "$dl_pid"
  local curl_exit=$?
  wait "$mon_pid" 2>/dev/null
  if [ $curl_exit -eq 0 ] && [ -s "$TMP_FILE" ]; then
    return 0
  fi

  rm -f "$TMP_FILE"
  if command -v wget >/dev/null 2>&1; then
    wget -T 15 -t 3 -q -O "$TMP_FILE" "$url" 2>/dev/null &
    dl_pid=$!
    _dl_progress "$TMP_FILE" "$total" "$dl_pid" &
    mon_pid=$!
    wait "$dl_pid"
    wait "$mon_pid" 2>/dev/null
    if [ -s "$TMP_FILE" ]; then
      return 0
    fi
  fi
  return 1
}

log_info "Downloading ${BINARY_NAME} ..."

# Build list of URLs to try: CDN mirrors first, then direct GitHub
DOWNLOADED=0
GITHUB_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY_NAME}"

# Try CDN mirrors
for CDN in $CDN_URLS; do
  CDN_TARGET="${CDN}/${GITHUB_URL}"
  log_info "Trying CDN: ${CDN_TARGET}"
  if download_one "$CDN_TARGET"; then
    log_success "Downloaded via CDN (${CDN})."
    DOWNLOADED=1
    break
  fi
done

# Try direct GitHub
if [ "$DOWNLOADED" -eq 0 ]; then
  log_info "Trying direct GitHub: ${GITHUB_URL}"
  if download_one "$GITHUB_URL"; then
    log_success "Downloaded from GitHub."
    DOWNLOADED=1
  fi
fi

# Try with .tar.gz extension (some releases package the binary)
if [ "$DOWNLOADED" -eq 0 ]; then
  TAR_URL="${GITHUB_URL}.tar.gz"
  log_info "Trying tar.gz: ${TAR_URL}"
  if download_one "$TAR_URL"; then
    log_info "Extracting agent binary from tar.gz..."
    TAR_TMP="/tmp/oneclickvirt-agent-extract"
    mkdir -p "$TAR_TMP"
    if tar -xzf "$TMP_FILE" -C "$TAR_TMP" 2>/dev/null; then
      AGENT_EXTRACTED=$(find "$TAR_TMP" -type f -name "oneclickvirt-agent*" | head -1)
      if [ -n "$AGENT_EXTRACTED" ] && [ -x "$AGENT_EXTRACTED" ]; then
        mv -f "$AGENT_EXTRACTED" "$TMP_FILE"
        log_success "Extracted agent binary from archive."
        DOWNLOADED=1
      elif [ -f "$TAR_TMP/oneclickvirt-agent" ]; then
        mv -f "$TAR_TMP/oneclickvirt-agent" "$TMP_FILE"
        log_success "Extracted agent binary from archive."
        DOWNLOADED=1
      fi
    fi
    rm -rf "$TAR_TMP"
  fi
fi

if [ "$DOWNLOADED" -eq 0 ]; then
  log_error "Failed to download ${BINARY_NAME}."
  log_error "Tried all CDN mirrors and direct GitHub. Check network or release assets."
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

# ── detect init system and install service ─────────────────────────────────────

# Also create a helper script: /usr/local/bin/ocv
create_ocv_helper() {
  cat > /usr/local/bin/ocv << 'OCVEOF'
#!/bin/sh
# OneClickVirt Agent CLI helper (ocv)
set -e
AGENT_BIN="/opt/oneclickvirt/agent/oneclickvirt-agent"
SVC="oneclickvirt-agent"

_usage() {
  echo "Usage: ocv {status|start|stop|restart|upgrade|uninstall|install|log}"
  echo ""
  echo "Commands:"
  echo "  status     Show agent service status"
  echo "  start      Start agent service"
  echo "  stop       Stop agent service"
  echo "  restart    Restart agent service"
  echo "  upgrade    Upgrade agent binary to latest release"
  echo "  uninstall  Remove agent and service"
  echo "  install    Install/reinstall agent service"
  echo "  log        Show recent agent logs"
  exit 0
}

_upgrade() {
  echo "[ocv] Downloading latest agent release..."
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64)        BIN="oneclickvirt-agent-linux-amd64" ;;
    aarch64|arm64) BIN="oneclickvirt-agent-linux-arm64" ;;
    *) echo "[ocv] Unsupported arch: $ARCH"; exit 1 ;;
  esac
  REPO="oneclickvirt/oneclickvirt"
  for API in https://api.github.com https://githubapi.spiritlhl.workers.dev https://githubapi.spiritlhl.top; do
    V=$(curl -sL --connect-timeout 10 --max-time 30 "${API}/repos/${REPO}/releases/latest" 2>/dev/null | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
    [ -n "$V" ] && break
  done
  [ -z "$V" ] && echo "[ocv] Failed to get latest version" && exit 1
  URL="https://cdn.spiritlhl.net/https://github.com/${REPO}/releases/download/${V}/${BIN}"
  TMP="/opt/oneclickvirt/agent/${BIN}.tmp"
  mkdir -p /opt/oneclickvirt/agent
  curl -fsSL --connect-timeout 15 --max-time 120 -o "$TMP" "$URL" || \
    curl -fsSL --connect-timeout 15 --max-time 120 -o "$TMP" "https://github.com/${REPO}/releases/download/${V}/${BIN}"
  [ ! -s "$TMP" ] && echo "[ocv] Download failed" && exit 1
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SVC" 2>/dev/null; then
    systemctl stop "$SVC" || true
  fi
  mv -f "$TMP" "$AGENT_BIN"
  chmod +x "$AGENT_BIN"
  ln -sf "$AGENT_BIN" /usr/local/bin/oneclickvirt-agent
  echo "[ocv] Upgraded to ${V}. Restarting..."
  /usr/local/bin/ocv restart
}

_uninstall() {
  echo "[ocv] Stopping and removing agent..."
  if command -v systemctl >/dev/null 2>&1; then
    systemctl stop "$SVC" 2>/dev/null || true
    systemctl disable "$SVC" 2>/dev/null || true
    rm -f "/etc/systemd/system/${SVC}.service"
  fi
  if [ -f "/etc/init.d/${SVC}" ]; then
    "/etc/init.d/${SVC}" stop 2>/dev/null || true
    if command -v update-rc.d >/dev/null 2>&1; then
      update-rc.d -f "$SVC" remove 2>/dev/null || true
    elif command -v chkconfig >/dev/null 2>&1; then
      chkconfig --del "$SVC" 2>/dev/null || true
    elif command -v rc-update >/dev/null 2>&1; then
      rc-update del "$SVC" default 2>/dev/null || true
    fi
    rm -f "/etc/init.d/${SVC}"
  fi
  rm -f "$AGENT_BIN"
  rm -f /usr/local/bin/oneclickvirt-agent
  rm -f /usr/local/bin/ocv
  rm -rf /opt/oneclickvirt/agent
  echo "[ocv] Agent uninstalled."
}

_service_status() {
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SVC" 2>/dev/null; then
    echo "[ocv] Agent is running (systemd)"
  elif [ -f "/etc/init.d/${SVC}" ] && "/etc/init.d/${SVC}" status 2>/dev/null; then
    true
  elif command -v rc-service >/dev/null 2>&1 && rc-service "$SVC" status 2>/dev/null; then
    true
  elif pgrep -f "$AGENT_BIN" >/dev/null 2>&1; then
    echo "[ocv] Agent is running (foreground, PID $(pgrep -f "$AGENT_BIN" | head -1))"
  else
    echo "[ocv] Agent is not running"
  fi
}

case "${1:-usage}" in
  status)   _service_status ;;
  start)
    command -v systemctl >/dev/null 2>&1 && { systemctl start "$SVC"; exit $?; }
    [ -f "/etc/init.d/${SVC}" ] && { "/etc/init.d/${SVC}" start; exit $?; }
    command -v rc-service >/dev/null 2>&1 && { rc-service "$SVC" start; exit $?; }
    echo "[ocv] No service manager found"
    ;;
  stop)
    command -v systemctl >/dev/null 2>&1 && { systemctl stop "$SVC"; exit $?; }
    [ -f "/etc/init.d/${SVC}" ] && { "/etc/init.d/${SVC}" stop; exit $?; }
    command -v rc-service >/dev/null 2>&1 && { rc-service "$SVC" stop; exit $?; }
    pkill -f "$AGENT_BIN" 2>/dev/null || true
    ;;
  restart)
    command -v systemctl >/dev/null 2>&1 && { systemctl restart "$SVC"; exit $?; }
    [ -f "/etc/init.d/${SVC}" ] && { "/etc/init.d/${SVC}" restart; exit $?; }
    command -v rc-service >/dev/null 2>&1 && { rc-service "$SVC" restart; exit $?; }
    /usr/local/bin/ocv stop; sleep 1; /usr/local/bin/ocv start
    ;;
  upgrade)   _upgrade ;;
  uninstall) _uninstall ;;
  install)   echo "[ocv] Run the install script to reinstall." ;;
  log)
    if command -v journalctl >/dev/null 2>&1; then
      journalctl -u "$SVC" -n 50 --no-pager 2>/dev/null || echo "[ocv] No journald logs"
    elif [ -f "/var/log/${SVC}.log" ]; then
      tail -50 "/var/log/${SVC}.log"
    else
      echo "[ocv] No logs found"
    fi
    ;;
  *) _usage ;;
esac
OCVEOF
  chmod +x /usr/local/bin/ocv
  log_success "Helper command 'ocv' installed to /usr/local/bin/ocv"
}

# Detect init system and install appropriate service
install_service() {
  create_ocv_helper

  # ── systemd ──
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    log_info "Detected systemd — installing systemd service..."
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
      log_success "Agent service started (systemd)."
      log_info  "Manage: systemctl {start|stop|restart|status} ${SERVICE_NAME}  or  ocv"
      log_info  "Logs:   journalctl -u ${SERVICE_NAME} -f"
    else
      log_error "Agent service failed to start. Check: journalctl -u ${SERVICE_NAME} -xe"
      exit 1
    fi
    return 0
  fi

  # ── SysV init (Debian/Ubuntu/RHEL legacy) ──
  if [ -d /etc/init.d ] && { command -v update-rc.d >/dev/null 2>&1 || command -v chkconfig >/dev/null 2>&1; }; then
    log_info "Detected SysV init — installing init.d script..."
    cat > "/etc/init.d/${SERVICE_NAME}" << EOF
#!/bin/sh
### BEGIN INIT INFO
# Provides:          ${SERVICE_NAME}
# Required-Start:    \$network \$remote_fs \$syslog
# Required-Stop:     \$network \$remote_fs \$syslog
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: OneClickVirt Agent
# Description:       OneClickVirt Agent WebSocket client
### END INIT INFO
PIDFILE=/var/run/${SERVICE_NAME}.pid
LOGFILE=/var/log/${SERVICE_NAME}.log
BIN=${BINARY_PATH}
ARGS="--ws-url ${WS_URL} --secret ${SECRET}"

case "\$1" in
  start)
    echo "Starting ${SERVICE_NAME}..."
    nohup \$BIN \$ARGS >>\$LOGFILE 2>&1 &
    echo \$! > \$PIDFILE
    ;;
  stop)
    echo "Stopping ${SERVICE_NAME}..."
    [ -f \$PIDFILE ] && kill \$(cat \$PIDFILE) 2>/dev/null
    rm -f \$PIDFILE
    ;;
  restart)
    \$0 stop; sleep 1; \$0 start
    ;;
  status)
    [ -f \$PIDFILE ] && kill -0 \$(cat \$PIDFILE) 2>/dev/null && echo "Running" || echo "Not running"
    ;;
  *)
    echo "Usage: \$0 {start|stop|restart|status}"
    exit 1
    ;;
esac
EOF
    chmod +x "/etc/init.d/${SERVICE_NAME}"
    if command -v update-rc.d >/dev/null 2>&1; then
      update-rc.d "$SERVICE_NAME" defaults
    elif command -v chkconfig >/dev/null 2>&1; then
      chkconfig --add "$SERVICE_NAME"
      chkconfig "$SERVICE_NAME" on
    fi
    "/etc/init.d/${SERVICE_NAME}" start
    sleep 2
    if "/etc/init.d/${SERVICE_NAME}" status | grep -q "Running"; then
      log_success "Agent service started (SysV init)."
      log_info  "Manage: /etc/init.d/${SERVICE_NAME} {start|stop|restart|status}  or  ocv"
      log_info  "Logs:   tail -f /var/log/${SERVICE_NAME}.log"
    else
      log_error "Agent failed to start. Check: /var/log/${SERVICE_NAME}.log"
      exit 1
    fi
    return 0
  fi

  # ── OpenRC (Alpine/Gentoo) ──
  if command -v rc-service >/dev/null 2>&1 && [ -d /etc/init.d ]; then
    log_info "Detected OpenRC — installing init script..."
    cat > "/etc/init.d/${SERVICE_NAME}" << EOF
#!/sbin/openrc-run
name="${SERVICE_NAME}"
description="OneClickVirt Agent"
command="${BINARY_PATH}"
command_args="--ws-url ${WS_URL} --secret ${SECRET}"
command_background=true
pidfile="/var/run/\${RC_SVCNAME}.pid"
output_log="/var/log/\${RC_SVCNAME}.log"
error_log="/var/log/\${RC_SVCNAME}.log"
EOF
    chmod +x "/etc/init.d/${SERVICE_NAME}"
    rc-update add "$SERVICE_NAME" default
    rc-service "$SERVICE_NAME" start
    sleep 2
    if rc-service "$SERVICE_NAME" status 2>/dev/null; then
      log_success "Agent service started (OpenRC)."
      log_info  "Manage: rc-service ${SERVICE_NAME} {start|stop|restart|status}  or  ocv"
      log_info  "Logs:   tail -f /var/log/${SERVICE_NAME}.log"
    else
      log_error "Agent failed to start. Check: /var/log/${SERVICE_NAME}.log"
      exit 1
    fi
    return 0
  fi

  # ── Fallback: nohup ──
  log_warning "No supported init system found (systemd/SysV/OpenRC). Starting as foreground process."
  nohup "$BINARY_PATH" --ws-url "$WS_URL" --secret "$SECRET" \
    >/var/log/oneclickvirt-agent.log 2>&1 &
  log_success "Agent started (PID $!). Log: /var/log/oneclickvirt-agent.log"
  log_warning "The agent will NOT auto-start on reboot. Install an init system for persistence."
  log_info  "Manage: kill \$(pgrep -f oneclickvirt-agent) to stop"
  return 0
}
