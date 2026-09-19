#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap '[[ -n "${TEST_PID:-}" ]] && kill "$TEST_PID" 2>/dev/null || true; rm -rf "$TMP_DIR"' EXIT

# Source functions without executing the installer entrypoint.
# shellcheck disable=SC1090
source <(sed '/^main "\$@"$/d' "$ROOT_DIR/scripts/install_full.sh")

DOMAIN='panel.example.com'
validate_domain "$DOMAIN"
DOMAIN='2001:db8::1'
validate_domain "$DOMAIN"
if validate_domain $'panel.example.com\nheader X-Evil true'; then
    echo 'domain configuration injection unexpectedly accepted' >&2
    exit 1
fi
DB_TYPE='postgres'
if validate_db_type; then
    echo 'unsupported database type unexpectedly accepted' >&2
    exit 1
fi
DB_TYPE='mysql'

# Critical installer artifacts must propagate write/permission failures.  Keep
# these checks close to the lifecycle tests so a future refactor cannot turn a
# partial installation into a reported success.
grep -q 'if ! cat > "\$NGINX_CONF"' "$ROOT_DIR/scripts/install_full.sh"
grep -q 'if ! chmod 600 "\${SERVER_DIR}/config.yaml"' "$ROOT_DIR/scripts/install_full.sh"
grep -q 'if ! chmod 600 "\${INSTALL_DIR}/.credentials"' "$ROOT_DIR/scripts/install_full.sh"
grep -q 'if ! mkdir -p "\$SERVER_DIR" "\$WEB_DIR"' "$ROOT_DIR/scripts/install_full.sh"
grep -q 'Unable to write the Caddy OpenRC script' "$ROOT_DIR/scripts/install_full.sh"
grep -q 'Unable to write the Caddy rc.d script' "$ROOT_DIR/scripts/install_full.sh"
grep -q 'Unable to write the Caddy SysV init script' "$ROOT_DIR/scripts/install_full.sh"
grep -q 'Unable to write the Caddy launcher script' "$ROOT_DIR/scripts/install_full.sh"

INSTALL_DIR="$TMP_DIR/opt/oneclickvirt"
SERVER_DIR="$INSTALL_DIR/server"
mkdir -p "$INSTALL_DIR" "$SERVER_DIR" "$TMP_DIR/systemd" "$TMP_DIR/openrc" "$TMP_DIR/freebsd" "$TMP_DIR/sysv"
EXTERNAL_DB=true
DB_SERVICE=mysql
PROXY=caddy
SYSTEMD_UNIT_DIR="$TMP_DIR/systemd"
OPENRC_INIT_DIR="$TMP_DIR/openrc"
FREEBSD_RC_DIR="$TMP_DIR/freebsd"
SYSV_INIT_DIR="$TMP_DIR/sysv"

# A service-manager failure must abort installation instead of being silently
# ignored after the unit has been written.
SERVICE_MANAGER=systemd
systemctl() { [[ "$*" == 'daemon-reload' ]] && return 0; return 1; }
service_enable() { return 1; }
if install_oneclickvirt_service "$SERVER_DIR/oneclickvirt-server"; then
    echo 'systemd enable failure unexpectedly succeeded' >&2
    exit 1
fi
test -s "$SYSTEMD_UNIT_DIR/oneclickvirt.service"

# Successful unit installation and active-state verification are both required.
service_enable() { return 0; }
service_restart() { return 0; }
service_start() { return 0; }
service_is_active() { return 0; }
install_oneclickvirt_service "$SERVER_DIR/oneclickvirt-server"
start_oneclickvirt_service

# A successful start command with an inactive unit is still a failed install.
service_is_active() { return 1; }
if start_oneclickvirt_service; then
    echo 'inactive systemd service unexpectedly succeeded' >&2
    exit 1
fi

# The no-manager fallback must detect a launcher that exits without leaving a
# live process; a real long-lived process is accepted.
SERVICE_MANAGER=none
cat > "$TMP_DIR/server.sh" <<'SCRIPT'
#!/usr/bin/env bash
sleep 30
SCRIPT
chmod +x "$TMP_DIR/server.sh"
install_oneclickvirt_service "$TMP_DIR/server.sh"
start_oneclickvirt_service
TEST_PID="$(cat "$INSTALL_DIR/oneclickvirt.pid")"
kill -0 "$TEST_PID"

# Reverse-proxy startup is part of the reachable-panel contract. Missing or
# short-lived launchers must fail the install instead of being silently ignored.
PROXY=caddy
rm -f "$INSTALL_DIR/start-caddy.sh"
if start_proxy_service; then
    echo 'missing Caddy launcher unexpectedly succeeded' >&2
    exit 1
fi
cat > "$INSTALL_DIR/start-caddy.sh" <<'SCRIPT'
#!/usr/bin/env bash
exit 0
SCRIPT
chmod +x "$INSTALL_DIR/start-caddy.sh"
if start_proxy_service; then
    echo 'short-lived Caddy launcher unexpectedly succeeded' >&2
    exit 1
fi

echo 'install_full service lifecycle tests passed'
