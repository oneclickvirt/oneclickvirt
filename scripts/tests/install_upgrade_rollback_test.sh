#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

export ONECLICKVIRT_INSTALL_ROOT="$TMP_DIR/opt/oneclickvirt"
export ONECLICKVIRT_SERVICE_FILE="$TMP_DIR/etc/oneclickvirt.service"
export ONECLICKVIRT_CLI_LINK="$TMP_DIR/bin/oneclickvirt"
export ONECLICKVIRT_SERVICE_NAME="oneclickvirt-test"
export PATH="$TMP_DIR/fake-bin:$PATH"

mkdir -p "$TMP_DIR/fake-bin" "$ONECLICKVIRT_INSTALL_ROOT/server" "$ONECLICKVIRT_INSTALL_ROOT/web" "$(dirname "$ONECLICKVIRT_SERVICE_FILE")"
cat > "$TMP_DIR/fake-bin/systemctl" <<'SCRIPT'
#!/usr/bin/env bash
if [ "${1:-}" = "is-active" ]; then
    exit 1
fi
exit 0
SCRIPT
chmod +x "$TMP_DIR/fake-bin/systemctl"

# shellcheck disable=SC1091
# shellcheck source=../install.sh
source "$ROOT_DIR/scripts/install.sh"

cat > "$MANAGED_SERVER_BIN" <<'SCRIPT'
#!/usr/bin/env bash
echo old-controller
SCRIPT
chmod +x "$MANAGED_SERVER_BIN"
printf '%s\n' old-web > "$ONECLICKVIRT_INSTALL_ROOT/web/marker"
printf '%s\n' old-service > "$ONECLICKVIRT_SERVICE_FILE"

export VERSION="test-version"
install_server() {
    local target_dir="$1"
    mkdir -p "$target_dir"
    cat > "$target_dir/oneclickvirt-server" <<'SCRIPT'
#!/usr/bin/env bash
echo new-controller
SCRIPT
    chmod +x "$target_dir/oneclickvirt-server"
}
install_web() {
    local target_dir="$1"
    mkdir -p "$target_dir"
    printf '%s\n' new-web > "$target_dir/marker"
}
find_running_server_pids() { :; }
persist_runtime_environment() { :; }
wait_for_controller_endpoint() { return 0; }
verify_controller_api_contract_marker() { return 0; }
verify_managed_server_process() { return 0; }
verify_admin_route_contract() { return 1; }

set +e
upgrade_server >/dev/null 2>&1
upgrade_status=$?
set -e
test "$upgrade_status" -eq 1
grep -Fxq 'echo old-controller' "$MANAGED_SERVER_BIN"
grep -Fxq 'old-web' "$ONECLICKVIRT_INSTALL_ROOT/web/marker"
grep -Fxq 'old-service' "$ONECLICKVIRT_SERVICE_FILE"
test ! -e "$ONECLICKVIRT_SERVICE_FILE.d/10-oneclickvirt-env.conf"
test ! -e "$MANAGED_SERVER_BIN.pre-upgrade"
test ! -e "$ONECLICKVIRT_INSTALL_ROOT/web.pre-upgrade"

echo "install upgrade rollback tests passed"
