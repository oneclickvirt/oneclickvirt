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

log_with_level() {
  level="$1"
  first_line="$2"
  second_line="$3"
  printf "%b %s\n" "$level" "$first_line"
  if [ -n "$second_line" ]; then
    printf "%b %s\n" "$level" "$second_line"
  fi
}

log_info()    { log_with_level "${BLUE}[INFO]${NC}" "$1" "$2"; }
log_success() { log_with_level "${GREEN}[SUCCESS]${NC}" "$1" "$2"; }
log_warning() { log_with_level "${YELLOW}[WARNING]${NC}" "$1" "$2"; }
log_error()   { log_with_level "${RED}[ERROR]${NC}" "$1" "$2" >&2; }

# ── argument parsing ──────────────────────────────────────────────────────────
WS_URL=""
SECRET=""

while [ "$#" -gt 0 ]; do
  case "$1" in
    --ws-url)  WS_URL="$2";  shift 2 ;;
    --secret)  SECRET="$2";  shift 2 ;;
    *) log_error "Unknown argument: $1" "未知参数: $1"; exit 1 ;;
  esac
done

if [ -z "$WS_URL" ] || [ -z "$SECRET" ]; then
  log_error "Usage: install_agent.sh --ws-url <WS_URL> --secret <SECRET>" "用法: install_agent.sh --ws-url <WS_URL> --secret <SECRET>"
  exit 1
fi

# ── sanity checks ─────────────────────────────────────────────────────────────
if [ "$(id -u)" -ne 0 ]; then
  log_error "This script must be run as root." "此脚本必须以 root 身份运行。"
  exit 1
fi

# ── detect architecture ───────────────────────────────────────────────────────
ARCH=$(uname -m)
case "$ARCH" in
  x86_64)          BINARY_SUFFIX="linux-amd64"  ;;
  aarch64|arm64)   BINARY_SUFFIX="linux-arm64"  ;;
  *)
    log_error "Unsupported architecture: $ARCH" "不支持的架构: $ARCH"
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
log_info "Fetching the latest release version..." "正在获取最新版本信息..."
VERSION=""
for API_URL in $GITHUB_API_URLS; do
  RESPONSE=$(curl -sL --connect-timeout 10 --max-time 30 \
    "${API_URL}/repos/${REPO}/releases/latest" 2>/dev/null) || continue
  VERSION=$(echo "$RESPONSE" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
  [ -n "$VERSION" ] && break
done

if [ -z "$VERSION" ]; then
  log_error "Failed to fetch the latest release version. Check your network." "获取最新版本失败，请检查网络连接。"
  exit 1
fi
log_info "Latest version: ${VERSION}" "最新版本: ${VERSION}"

# ── check GitHub reachability ─────────────────────────────────────────────────
_github_reachable() {
  curl -sIkL --connect-timeout 6 --max-time 10 "https://github.com" 2>/dev/null | grep -q "HTTP" && return 0
  return 1
}

# Pre-check which CDN mirrors are actually available (only called when GitHub is down)
_check_cdn_available() {
  local test_url="https://raw.githubusercontent.com/oneclickvirt/oneclickvirt/main/.back/test"
  printf '%s' "" > /tmp/ocv_working_cdns
  for cdn in $CDN_URLS; do
    if curl -sL -k --max-time 6 "${cdn}/${test_url}" 2>/dev/null | grep -q "success"; then
      printf '%s ' "$cdn" >> /tmp/ocv_working_cdns
    fi
  done
  [ -s /tmp/ocv_working_cdns ] && return 0 || return 1
}

# ── download binary ───────────────────────────────────────────────────────────

mkdir -p "$INSTALL_DIR"
TMP_FILE="${INSTALL_DIR}/${BINARY_NAME}.tmp"

# _dl_progress shows an animated progress bar while a download runs in background
_dl_progress() {
  local out="$1" total="$2" pid="$3" shown=0
  # Strip leading zeros to avoid octal interpretation (e.g. Content-Length "06293879")
  total=$(echo "$total" | sed 's/^0*//')
  [ -z "$total" ] && total=0
  while kill -0 "$pid" 2>/dev/null; do
    if [ -f "$out" ]; then
      local cur=0
      cur=$(stat -c%s "$out" 2>/dev/null || stat -f%z "$out" 2>/dev/null)
      cur=$(printf '%s' "$cur" | tr -d '\r\n ' | grep -o '[0-9]*' | head -1)
      cur=${cur:-0}
      # Strip leading zeros from cur as well (paranoia)
      cur=$(echo "$cur" | sed 's/^0*//')
      [ -z "$cur" ] && cur=0
      if [ "$total" -gt 0 ] && [ "$cur" -gt 0 ]; then
        local pct=$((cur * 100 / total))
        [ "$pct" -gt 100 ] && pct=100
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
  total=$(curl -sIkL --connect-timeout 10 "$url" 2>/dev/null | grep -i 'Content-Length' | awk '{print $2}' | tr -d '\r\n ' | grep -o '[0-9]*' | tail -1)
  total=${total:-0}
  [ -z "$total" ] && total=0
  # Strip leading zeros to avoid octal interpretation in dash/sh (e.g. "06293879" → "6293879")
  total=$(echo "$total" | sed 's/^0*//')
  [ -z "$total" ] && total=0

  # Try curl — check file existence rather than exit code (some servers return non-zero on valid downloads)
  curl -fsSL --connect-timeout 20 --max-time 300 -o "$TMP_FILE" "$url" 2>/dev/null &
  local dl_pid=$!
  _dl_progress "$TMP_FILE" "$total" "$dl_pid" &
  local mon_pid=$!
  wait "$dl_pid" 2>/dev/null
  wait "$mon_pid" 2>/dev/null
  # Validate: file must exist, be non-empty, and not start with '<' (HTML error page)
  if [ -s "$TMP_FILE" ]; then
    local first_byte
    first_byte=$(dd if="$TMP_FILE" bs=1 count=1 2>/dev/null)
    if [ "$first_byte" != "<" ]; then
      return 0
    fi
    # HTML error page — clean up and return failure
    rm -f "$TMP_FILE"
  fi

  # Try wget
  rm -f "$TMP_FILE"
  if command -v wget >/dev/null 2>&1; then
    wget -T 20 -t 3 -q -O "$TMP_FILE" "$url" 2>/dev/null &
    dl_pid=$!
    _dl_progress "$TMP_FILE" "$total" "$dl_pid" &
    mon_pid=$!
    wait "$dl_pid" 2>/dev/null
    wait "$mon_pid" 2>/dev/null
    if [ -s "$TMP_FILE" ]; then
      return 0
    fi
  fi
  return 1
}

log_info "Downloading ${BINARY_NAME} ..." "正在下载 ${BINARY_NAME} ..."

# GitHub direct first, CDN as fallback
DOWNLOADED=0
GITHUB_URL="https://github.com/${REPO}/releases/download/${VERSION}/${BINARY_NAME}"

# 1. Try GitHub direct first
if download_one "$GITHUB_URL" && [ -s "$TMP_FILE" ]; then
  log_success "Downloaded from GitHub." "已从 GitHub 下载完成。"
  DOWNLOADED=1
fi

# 2. GitHub failed — check if GitHub is reachable, try CDN only if it's not
if [ "$DOWNLOADED" -eq 0 ] && ! _github_reachable && _check_cdn_available; then
  log_warning "GitHub is unreachable, switching to CDN mirrors..." "GitHub 不可达，正在切换到 CDN 镜像..."
  for CDN in $(cat /tmp/ocv_working_cdns); do
    CDN_TARGET="${CDN}/${GITHUB_URL}"
    rm -f "$TMP_FILE"
    if download_one "$CDN_TARGET" && [ -s "$TMP_FILE" ]; then
      log_success "Downloaded via CDN." "已通过 CDN 下载完成。"
      DOWNLOADED=1
      break
    fi
  done
  rm -f /tmp/ocv_working_cdns
fi

# Try with .tar.gz extension (some releases package the binary)
if [ "$DOWNLOADED" -eq 0 ]; then
  TAR_URL="${GITHUB_URL}.tar.gz"
  if download_one "$TAR_URL" && [ -s "$TMP_FILE" ]; then
    log_info "Extracting the agent binary from the tar.gz archive..." "正在从 tar.gz 压缩包中提取 Agent 二进制文件..."
    TAR_TMP="/tmp/oneclickvirt-agent-extract"
    mkdir -p "$TAR_TMP"
    if tar -xzf "$TMP_FILE" -C "$TAR_TMP" 2>/dev/null; then
      AGENT_EXTRACTED=$(find "$TAR_TMP" -type f -name "oneclickvirt-agent*" | head -1)
      if [ -n "$AGENT_EXTRACTED" ] && [ -x "$AGENT_EXTRACTED" ]; then
        mv -f "$AGENT_EXTRACTED" "$TMP_FILE"
        log_success "Agent binary extracted from archive." "已从压缩包提取 Agent 二进制文件。"
        DOWNLOADED=1
      elif [ -f "$TAR_TMP/oneclickvirt-agent" ]; then
        mv -f "$TAR_TMP/oneclickvirt-agent" "$TMP_FILE"
        log_success "Agent binary extracted from archive." "已从压缩包提取 Agent 二进制文件。"
        DOWNLOADED=1
      fi
    fi
    rm -rf "$TAR_TMP"
  fi
fi

if [ "$DOWNLOADED" -eq 0 ]; then
  log_error "Failed to download ${BINARY_NAME}." "下载 ${BINARY_NAME} 失败。"
  log_error "All CDN mirrors and direct GitHub downloads were attempted. Check network connectivity or release assets." "已尝试所有 CDN 镜像和 GitHub 直连，请检查网络或发布资源。"
  rm -f "$TMP_FILE"
  exit 1
fi

# ── install binary ────────────────────────────────────────────────────────────
# Stop existing service before replacing binary
if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
  log_info "Stopping the existing ${SERVICE_NAME} service..." "正在停止现有的 ${SERVICE_NAME} 服务..."
  systemctl stop "$SERVICE_NAME" || true
fi

mv -f "$TMP_FILE" "$BINARY_PATH"
chmod +x "$BINARY_PATH"
ln -sf "$BINARY_PATH" /usr/local/bin/oneclickvirt-agent
log_success "Binary installed to ${BINARY_PATH}" "二进制文件已安装到 ${BINARY_PATH}"

# ── detect init system and install service ─────────────────────────────────────

# Create a helper script: /usr/local/bin/ocv (called both by install_service and as fallback)
create_ocv_helper() {
  cat > /usr/local/bin/ocv << 'OCVEOF'
#!/bin/sh
# OneClickVirt Agent CLI helper (ocv)
set -e
AGENT_BIN="/opt/oneclickvirt/agent/oneclickvirt-agent"
SVC="oneclickvirt-agent"

_usage() {
  echo "Usage: ocv {status|start|stop|restart|upgrade|uninstall|install|log}"
  echo "用法: ocv {status|start|stop|restart|upgrade|uninstall|install|log}"
  echo ""
  echo "Commands:"
  echo "命令说明:"
  echo "  status     Show agent service status"
  echo "  status     查看 agent 服务状态"
  echo "  start      Start agent service"
  echo "  start      启动 agent 服务"
  echo "  stop       Stop agent service"
  echo "  stop       停止 agent 服务"
  echo "  restart    Restart agent service"
  echo "  restart    重启 agent 服务"
  echo "  upgrade    Upgrade agent binary to latest release"
  echo "  upgrade    升级 agent 二进制到最新版本"
  echo "  uninstall  Remove agent and service"
  echo "  uninstall  卸载 agent 和服务"
  echo "  install    Install/reinstall agent service"
  echo "  install    安装或重装 agent 服务"
  echo "  log        Show recent agent logs"
  echo "  log        查看最近的 agent 日志"
  exit 0
}

_upgrade() {
  echo "[ocv] Downloading latest agent release..."
  echo "[ocv] 正在下载最新的 agent 发布版本..."
  ARCH=$(uname -m)
  case "$ARCH" in
    x86_64)        BIN="oneclickvirt-agent-linux-amd64" ;;
    aarch64|arm64) BIN="oneclickvirt-agent-linux-arm64" ;;
    *) echo "[ocv] Unsupported arch: $ARCH"; echo "[ocv] 不支持的架构: $ARCH"; exit 1 ;;
  esac
  REPO="oneclickvirt/oneclickvirt"
  for API in https://api.github.com https://githubapi.spiritlhl.workers.dev https://githubapi.spiritlhl.top; do
    local response
    response=$(curl -sL --connect-timeout 10 --max-time 30 "${API}/repos/${REPO}/releases/latest" 2>/dev/null)
    V=$(printf '%s' "$response" | grep -o '"tag_name": *"[^"]*"' | head -1 | grep -o '"[^"]*"$' | tr -d '"')
    [ -n "$V" ] && break
  done
  [ -z "$V" ] && echo "[ocv] Failed to get latest version" && echo "[ocv] 获取最新版本失败" && exit 1
  TMP="/opt/oneclickvirt/agent/${BIN}.tmp"
  mkdir -p /opt/oneclickvirt/agent
  DOWNLOADED=0
  for CDN in https://cdn0.spiritlhl.top https://cdn3.spiritlhl.net https://cdn1.spiritlhl.net https://cdn2.spiritlhl.net https://cdn.spiritlhl.net; do
    URL="${CDN}/https://github.com/${REPO}/releases/download/${V}/${BIN}"
    if curl -fsSL --connect-timeout 20 --max-time 180 -o "$TMP" "$URL" 2>/dev/null && [ -s "$TMP" ]; then
      DOWNLOADED=1; break
    fi
  done
  if [ "$DOWNLOADED" -eq 0 ]; then
    curl -fsSL --connect-timeout 20 --max-time 180 -o "$TMP" "https://github.com/${REPO}/releases/download/${V}/${BIN}" 2>/dev/null || true
  fi
  [ ! -s "$TMP" ] && echo "[ocv] Download failed" && echo "[ocv] 下载失败" && exit 1
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SVC" 2>/dev/null; then
    systemctl stop "$SVC" || true
  fi
  mv -f "$TMP" "$AGENT_BIN"
  chmod +x "$AGENT_BIN"
  ln -sf "$AGENT_BIN" /usr/local/bin/oneclickvirt-agent
  echo "[ocv] Upgraded to ${V}. Restarting..."
  echo "[ocv] 已升级到 ${V}，正在重启..."
  /usr/local/bin/ocv restart
}

_uninstall() {
  echo "[ocv] Stopping and removing agent..."
  echo "[ocv] 正在停止并移除 agent..."
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
  echo "[ocv] Agent 已卸载。"
}

_service_status() {
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet "$SVC" 2>/dev/null; then
    echo "[ocv] Agent is running (systemd)"
    echo "[ocv] Agent 正在运行（systemd）"
  elif [ -f "/etc/init.d/${SVC}" ] && "/etc/init.d/${SVC}" status 2>/dev/null | grep -q "Running"; then
    echo "[ocv] Agent is running (init.d)"
    echo "[ocv] Agent 正在运行（init.d）"
  elif command -v rc-service >/dev/null 2>&1 && rc-service "$SVC" status 2>/dev/null; then
    echo "[ocv] Agent is running (OpenRC)"
    echo "[ocv] Agent 正在运行（OpenRC）"
  elif command -v pgrep >/dev/null 2>&1 && pgrep -f "$AGENT_BIN" >/dev/null 2>&1; then
    echo "[ocv] Agent is running (foreground, PID $(pgrep -f "$AGENT_BIN" | head -1))"
    echo "[ocv] Agent 正在运行（前台模式，PID $(pgrep -f "$AGENT_BIN" | head -1)）"
  elif ps aux 2>/dev/null | grep -v grep | grep -q "$AGENT_BIN"; then
    echo "[ocv] Agent is running (foreground)"
    echo "[ocv] Agent 正在运行（前台模式）"
  else
    echo "[ocv] Agent is not running"
    echo "[ocv] Agent 未运行"
  fi
}

_do_stop() {
  # Try service managers first
  command -v systemctl >/dev/null 2>&1 && systemctl stop "$SVC" 2>/dev/null
  [ -f "/etc/init.d/${SVC}" ] && "/etc/init.d/${SVC}" stop 2>/dev/null
  command -v rc-service >/dev/null 2>&1 && rc-service "$SVC" stop 2>/dev/null
  # Fallback: kill by binary name
  if command -v pkill >/dev/null 2>&1; then
    pkill -f "$AGENT_BIN" 2>/dev/null || true
  elif command -v pgrep >/dev/null 2>&1; then
    pgrep -f "$AGENT_BIN" 2>/dev/null | xargs -r kill 2>/dev/null || true
  elif command -v killall >/dev/null 2>&1; then
    killall oneclickvirt-agent 2>/dev/null || true
  fi
}

case "${1:-usage}" in
  status)   _service_status ;;
  start)
    command -v systemctl >/dev/null 2>&1 && { systemctl start "$SVC"; exit $?; }
    [ -f "/etc/init.d/${SVC}" ] && { "/etc/init.d/${SVC}" start; exit $?; }
    command -v rc-service >/dev/null 2>&1 && { rc-service "$SVC" start; exit $?; }
    # Fallback: start directly in background
    echo "[ocv] No service manager found, starting in foreground..."
    echo "[ocv] 未找到服务管理器，正在以前台方式启动..."
    nohup "$AGENT_BIN" >/var/log/oneclickvirt-agent.log 2>&1 &
    echo "[ocv] Agent started (PID $!)"
    echo "[ocv] Agent 已启动（PID $!）"
    ;;
  stop)
    _do_stop
    ;;
  restart)
    command -v systemctl >/dev/null 2>&1 && { systemctl restart "$SVC"; exit $?; }
    [ -f "/etc/init.d/${SVC}" ] && { "/etc/init.d/${SVC}" restart; exit $?; }
    command -v rc-service >/dev/null 2>&1 && { rc-service "$SVC" restart; exit $?; }
    _do_stop; sleep 1; /usr/local/bin/ocv start
    ;;
  upgrade)   _upgrade ;;
  uninstall) _uninstall ;;
  install)   echo "[ocv] Run the install script to reinstall."; echo "[ocv] 如需重装，请重新运行安装脚本。" ;;
  log)
    if command -v journalctl >/dev/null 2>&1; then
      journalctl -u "$SVC" -n 50 --no-pager 2>/dev/null || { echo "[ocv] No journald logs"; echo "[ocv] 没有找到 journald 日志"; }
    elif [ -f "/var/log/${SVC}.log" ]; then
      tail -50 "/var/log/${SVC}.log"
    else
      echo "[ocv] No logs found"
      echo "[ocv] 未找到日志"
    fi
    ;;
  *) _usage ;;
esac
OCVEOF
  chmod +x /usr/local/bin/ocv
  log_success "Helper command 'ocv' installed to /usr/local/bin/ocv" "辅助命令 'ocv' 已安装到 /usr/local/bin/ocv"
}

# Detect init system and install appropriate service
install_service() {
  create_ocv_helper

  # ── systemd ──
  if command -v systemctl >/dev/null 2>&1 && [ -d /run/systemd/system ]; then
    log_info "Detected systemd; installing the systemd service..." "检测到 systemd，正在安装 systemd 服务..."

    # Write secret to a separate env file with restricted permissions (0600)
    # so it does NOT appear in `systemctl cat` or `ps aux` output.
    ENV_FILE="${INSTALL_DIR}/env"
    cat > "$ENV_FILE" << EOF
# OneClickVirt Agent environment (permissions: 0600)
WS_URL=${WS_URL}
AGENT_SECRET=${SECRET}
EOF
    chmod 600 "$ENV_FILE"
    chown root:root "$ENV_FILE" 2>/dev/null || true
    log_info "Agent environment file created at ${ENV_FILE} with 0600 permissions." "Agent 环境文件已创建: ${ENV_FILE}（权限 0600）。"

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
EnvironmentFile=-${ENV_FILE}
ExecStart=${BINARY_PATH} --ws-url \${WS_URL} --secret \${AGENT_SECRET}
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
      log_success "Agent service started successfully (systemd)." "Agent 服务已启动（systemd）。"
      log_info  "Manage with: systemctl {start|stop|restart|status} ${SERVICE_NAME}  or  ocv" "管理命令: systemctl {start|stop|restart|status} ${SERVICE_NAME}  或  ocv"
      log_info  "Logs: journalctl -u ${SERVICE_NAME} -f" "日志查看: journalctl -u ${SERVICE_NAME} -f"
    else
      log_error "Agent service failed to start. Check: journalctl -u ${SERVICE_NAME} -xe" "Agent 服务启动失败，请检查: journalctl -u ${SERVICE_NAME} -xe"
      exit 1
    fi
    return 0
  fi

  # ── SysV init (Debian/Ubuntu/RHEL legacy) ──
  if [ -d /etc/init.d ] && { command -v update-rc.d >/dev/null 2>&1 || command -v chkconfig >/dev/null 2>&1; }; then
    log_info "Detected SysV init; installing the init.d script..." "检测到 SysV init，正在安装 init.d 脚本..."

    # Write secret to a separate env file with restricted permissions (0600)
    ENV_FILE="${INSTALL_DIR}/env"
    cat > "$ENV_FILE" << EOF
# OneClickVirt Agent environment (permissions: 0600)
WS_URL=${WS_URL}
AGENT_SECRET=${SECRET}
EOF
    chmod 600 "$ENV_FILE"
    chown root:root "$ENV_FILE" 2>/dev/null || true

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
ENV_FILE=${ENV_FILE}

# Source env file to get WS_URL and AGENT_SECRET
if [ -f "\$ENV_FILE" ]; then
  . "\$ENV_FILE"
fi
ARGS="--ws-url \${WS_URL} --secret \${AGENT_SECRET}"

case "\$1" in
  start)
    echo "Starting ${SERVICE_NAME}..."
    echo "正在启动 ${SERVICE_NAME}..."
    nohup \$BIN \$ARGS >>\$LOGFILE 2>&1 &
    echo \$! > \$PIDFILE
    ;;
  stop)
    echo "Stopping ${SERVICE_NAME}..."
    echo "正在停止 ${SERVICE_NAME}..."
    [ -f \$PIDFILE ] && kill \$(cat \$PIDFILE) 2>/dev/null
    rm -f \$PIDFILE
    ;;
  restart)
    \$0 stop; sleep 1; \$0 start
    ;;
  status)
    [ -f \$PIDFILE ] && kill -0 \$(cat \$PIDFILE) 2>/dev/null && { echo "Running"; echo "正在运行"; } || { echo "Not running"; echo "未运行"; }
    ;;
  *)
    echo "Usage: \$0 {start|stop|restart|status}"
    echo "用法: \$0 {start|stop|restart|status}"
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
      log_success "Agent service started successfully (SysV init)." "Agent 服务已启动（SysV init）。"
      log_info  "Manage with: /etc/init.d/${SERVICE_NAME} {start|stop|restart|status}  or  ocv" "管理命令: /etc/init.d/${SERVICE_NAME} {start|stop|restart|status}  或  ocv"
      log_info  "Logs: tail -f /var/log/${SERVICE_NAME}.log" "日志查看: tail -f /var/log/${SERVICE_NAME}.log"
    else
      log_error "Agent failed to start. Check: /var/log/${SERVICE_NAME}.log" "Agent 启动失败，请检查: /var/log/${SERVICE_NAME}.log"
      exit 1
    fi
    return 0
  fi

  # ── OpenRC (Alpine/Gentoo) ──
  if command -v rc-service >/dev/null 2>&1 && [ -d /etc/init.d ]; then
    log_info "Detected OpenRC; installing the init script..." "检测到 OpenRC，正在安装 init 脚本..."

    # Write secret to a separate env file with restricted permissions (0600)
    ENV_FILE="${INSTALL_DIR}/env"
    cat > "$ENV_FILE" << EOF
# OneClickVirt Agent environment (permissions: 0600)
WS_URL=${WS_URL}
AGENT_SECRET=${SECRET}
EOF
    chmod 600 "$ENV_FILE"
    chown root:root "$ENV_FILE" 2>/dev/null || true

    cat > "/etc/init.d/${SERVICE_NAME}" << EOF
#!/sbin/openrc-run
name="${SERVICE_NAME}"
description="OneClickVirt Agent"
command="/bin/sh"
command_args="-c '. ${ENV_FILE} && exec ${BINARY_PATH}'"
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
      log_success "Agent service started successfully (OpenRC)." "Agent 服务已启动（OpenRC）。"
      log_info  "Manage with: rc-service ${SERVICE_NAME} {start|stop|restart|status}  or  ocv" "管理命令: rc-service ${SERVICE_NAME} {start|stop|restart|status}  或  ocv"
      log_info  "Logs: tail -f /var/log/${SERVICE_NAME}.log" "日志查看: tail -f /var/log/${SERVICE_NAME}.log"
    else
      log_error "Agent failed to start. Check: /var/log/${SERVICE_NAME}.log" "Agent 启动失败，请检查: /var/log/${SERVICE_NAME}.log"
      exit 1
    fi
    return 0
  fi

  # ── Fallback: nohup ──
  log_warning "No supported init system was found (systemd/SysV/OpenRC); starting as a foreground process." "未找到受支持的 init 系统（systemd/SysV/OpenRC），将以前台进程方式启动。"

  # Create env file with restricted permissions
  ENV_FILE="${INSTALL_DIR}/env"
  cat > "$ENV_FILE" << EOF
# OneClickVirt Agent environment (permissions: 0600)
WS_URL=${WS_URL}
AGENT_SECRET=${SECRET}
EOF
  chmod 600 "$ENV_FILE"
  chown root:root "$ENV_FILE" 2>/dev/null || true

  # Source env file to set WS_URL/AGENT_SECRET in environment,
  # then exec the agent binary WITHOUT CLI args so secret does NOT
  # appear in `ps aux` output (env vars are in /proc/PID/environ,
  # readable only by root).
  nohup sh -c ". ${ENV_FILE} && exec ${BINARY_PATH}" \
    >/var/log/oneclickvirt-agent.log 2>&1 &
  log_success "Agent started (PID $!). Log: /var/log/oneclickvirt-agent.log" "Agent 已启动（PID $!）。日志文件: /var/log/oneclickvirt-agent.log"
  log_warning "The agent will not auto-start after reboot. Install an init system for persistence." "Agent 重启后不会自动启动，如需持久化请安装 init 系统。"
  log_info  "To stop it, run: kill \$(pgrep -f oneclickvirt-agent)" "停止命令: kill \$(pgrep -f oneclickvirt-agent)"
  return 0
}

# ── Execute ────────────────────────────────────────────────────────────────────
install_service

# Always ensure ocv helper is available (even if service setup failed or was skipped)
if [ ! -x /usr/local/bin/ocv ]; then
  create_ocv_helper
fi
