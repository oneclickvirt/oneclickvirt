#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export ONECLICKVIRT_INSTALL_ROOT="$TMP_DIR/opt/oneclickvirt"
export ONECLICKVIRT_SERVICE_FILE="$TMP_DIR/etc/oneclickvirt.service"
export ONECLICKVIRT_CLI_LINK="$TMP_DIR/bin/oneclickvirt"
export ONECLICKVIRT_SERVICE_NAME="oneclickvirt-test"

mkdir -p "$TMP_DIR/fake-bin" "$TMP_DIR/calls"
cat > "$TMP_DIR/fake-bin/systemctl" <<'SCRIPT'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$TEST_CALLS/systemctl"
SCRIPT
cat > "$TMP_DIR/fake-bin/journalctl" <<'SCRIPT'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$TEST_CALLS/journalctl"
SCRIPT
chmod +x "$TMP_DIR/fake-bin/systemctl" "$TMP_DIR/fake-bin/journalctl"
export PATH="$TMP_DIR/fake-bin:$PATH"
export TEST_CALLS="$TMP_DIR/calls"

# shellcheck disable=SC1091
# shellcheck source=../install.sh
source "$ROOT_DIR/scripts/install.sh"

prepare_installation() {
    mkdir -p \
        "$ONECLICKVIRT_INSTALL_ROOT/server/storage" \
        "$ONECLICKVIRT_INSTALL_ROOT/web" \
        "$(dirname "$ONECLICKVIRT_SERVICE_FILE")" \
        "$(dirname "$ONECLICKVIRT_CLI_LINK")"
    touch \
        "$ONECLICKVIRT_INSTALL_ROOT/server/oneclickvirt-server" \
        "$ONECLICKVIRT_INSTALL_ROOT/server/server-allinone-linux-amd64" \
        "$ONECLICKVIRT_INSTALL_ROOT/server/config.yaml" \
        "$ONECLICKVIRT_INSTALL_ROOT/server/storage/sentinel" \
        "$ONECLICKVIRT_INSTALL_ROOT/web/index.html" \
        "$ONECLICKVIRT_SERVICE_FILE" \
        "$ONECLICKVIRT_CLI_LINK"
}

prepare_installation
uninstall_server --yes

test ! -e "$ONECLICKVIRT_INSTALL_ROOT/server/oneclickvirt-server"
test ! -e "$ONECLICKVIRT_INSTALL_ROOT/server/server-allinone-linux-amd64"
test ! -e "$ONECLICKVIRT_INSTALL_ROOT/web"
test -e "$ONECLICKVIRT_INSTALL_ROOT/server/config.yaml"
test -e "$ONECLICKVIRT_INSTALL_ROOT/server/storage/sentinel"
test ! -e "$ONECLICKVIRT_SERVICE_FILE"
test ! -e "$ONECLICKVIRT_CLI_LINK"
grep -Fxq 'disable --now oneclickvirt-test' "$TEST_CALLS/systemctl"
grep -Fxq 'daemon-reload' "$TEST_CALLS/systemctl"

show_service_status
show_service_logs --lines 25
show_service_logs --lines 10 --follow
grep -Fxq 'status oneclickvirt-test --no-pager' "$TEST_CALLS/systemctl"
grep -Fxq -- '-u oneclickvirt-test -n 25 --no-pager' "$TEST_CALLS/journalctl"
grep -Fxq -- '-u oneclickvirt-test -n 10 -f' "$TEST_CALLS/journalctl"

if show_service_logs --lines invalid >/dev/null 2>&1; then
    echo "invalid log line count unexpectedly succeeded" >&2
    exit 1
fi

if noninteractive=true uninstall_server >/dev/null 2>&1; then
    echo "non-interactive uninstall without --yes unexpectedly succeeded" >&2
    exit 1
fi

if (export MANAGED_INSTALL_ROOT=/; uninstall_server --yes >/dev/null 2>&1); then
    echo "uninstall accepted an unsafe installation root" >&2
    exit 1
fi

prepare_installation
uninstall_server --yes --purge
test ! -e "$ONECLICKVIRT_INSTALL_ROOT"

mkdir -p "$ONECLICKVIRT_INSTALL_ROOT/server"
cat > "$MANAGED_ENV_FILE" <<'EOF'
CUSTOM_FLAG="keep"
DB_HOST="old.internal"
EOF
export DB_HOST="db.internal"
export DB_PASSWORD='test"pass\value'
persist_runtime_environment ""
grep -Fxq 'CUSTOM_FLAG="keep"' "$MANAGED_ENV_FILE"
grep -Fxq 'DB_HOST="db.internal"' "$MANAGED_ENV_FILE"
grep -Fxq 'DB_PASSWORD="test\"pass\\value"' "$MANAGED_ENV_FILE"
test "$(grep -c '^DB_HOST=' "$MANAGED_ENV_FILE")" -eq 1
env_mode=$(stat -c '%a' "$MANAGED_ENV_FILE" 2>/dev/null || stat -f '%Lp' "$MANAGED_ENV_FILE")
test "$env_mode" = "600"

create_systemd_service
test "$(grep -c '^WorkingDirectory=' "$ONECLICKVIRT_SERVICE_FILE")" -eq 1
grep -Fxq "WorkingDirectory=$ONECLICKVIRT_INSTALL_ROOT/server" "$ONECLICKVIRT_SERVICE_FILE"
grep -Fxq "EnvironmentFile=-$MANAGED_ENV_FILE" "$ONECLICKVIRT_SERVICE_FILE"

prepare_installation
export noninteractive=true
unset FORCE_REINSTALL CONFIRM_REINSTALL
set +e
confirm_existing_install_action >/dev/null 2>&1
confirm_status=$?
set -e
test "$confirm_status" -eq 2

export FORCE_REINSTALL=true
unset CONFIRM_REINSTALL
set +e
confirm_existing_install_action >/dev/null 2>&1
confirm_status=$?
set -e
test "$confirm_status" -eq 2

export CONFIRM_REINSTALL=REINSTALL
confirm_existing_install_action >/dev/null 2>&1

printf '' > "$TEST_CALLS/main"
check_root() { :; }
env_check() { printf '%s\n' env_check >> "$TEST_CALLS/main"; }
upgrade_server() { printf '%s\n' upgrade_server >> "$TEST_CALLS/main"; }
install_server() { printf '%s\n' install_server >> "$TEST_CALLS/main"; }
install_web() { printf '%s\n' install_web >> "$TEST_CALLS/main"; }
unset FORCE_REINSTALL CONFIRM_REINSTALL
main install
grep -Fxq 'env_check' "$TEST_CALLS/main"
grep -Fxq 'upgrade_server' "$TEST_CALLS/main"
if grep -Fxq 'install_server' "$TEST_CALLS/main"; then
    echo "existing non-interactive install did not switch to upgrade" >&2
    exit 1
fi

echo "install lifecycle command tests passed"
