#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

GUARD="$TMP_DIR/guard.sh"
# Keep this test tied to the installer payload rather than maintaining a
# second copy of the guard implementation.
awk '
  /cat > \/usr\/local\/bin\/oneclickvirt-egress-boot-guard << '\''EGUARDEOF'\''/ { capture=1; next }
  capture && /^EGUARDEOF$/ { exit }
  capture { print }
' "$ROOT_DIR/scripts/install_agent.sh" > "$GUARD"
chmod +x "$GUARD"
bash -n "$GUARD"

# Service launchers must consume the restricted environment file and must not
# put the WebSocket credential back into process arguments.
if grep -Eq '^(ExecStart|ARGS)=.*(--secret|AGENT_SECRET)' "$ROOT_DIR/scripts/install_agent.sh"; then
  echo "agent secret is exposed in a generated service argv" >&2
  exit 1
fi
grep -Fq "ExecStart=\${BINARY_PATH}" "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq "set -a; . \"\$1\"; set +a; exec \"\$2\"" "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq "EnvironmentFile=-\${ENV_FILE}" "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq "required_files=\"\\\$ENV_FILE\"" "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq "command=\"\${BINARY_PATH}\"" "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq 'RequiredBy=network-pre.target' "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq 'Before=network-pre.target network.target network-online.target' "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq '# X-Start-Before:    docker containerd crio libvirtd lxc lxd incus pve-guests kubelet' "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq 'before docker containerd crio podman libvirtd lxc lxd incus kubelet' "$ROOT_DIR/scripts/install_agent.sh"
grep -Fq '.route("/api/v1/egress/state", put(egress::replace_state))' "$ROOT_DIR/server/agent/src/main.rs"
grep -Fq '| ("PUT", "/api/v1/egress/state")' "$ROOT_DIR/server/agent/src/ws_client/handler.rs"

STATE_DIR="$TMP_DIR/state"
FAKE_BIN="$TMP_DIR/bin"
CALLS="$TMP_DIR/calls"
mkdir -p "$STATE_DIR" "$FAKE_BIN"
: > "$CALLS"

cat > "$FAKE_BIN/nft" <<'SCRIPT'
#!/usr/bin/env bash
set -euo pipefail
printf '%s\n' "$*" >> "$TEST_CALLS"
case "${1:-}" in
  list) exit "${NFT_TABLE_EXISTS:-1}" ;;
  -c)
    cat "${3:?}" > "$TEST_CHECK_SCRIPT"
    ;;
  -f)
    cat "${2:?}" > "$TEST_APPLY_SCRIPT"
    ;;
  *) exit 2 ;;
esac
SCRIPT
chmod +x "$FAKE_BIN/nft"

export ONECLICKVIRT_EGRESS_STATE_DIR="$STATE_DIR"
export ONECLICKVIRT_EGRESS_NFT_BIN="$FAKE_BIN/nft"
export TEST_CALLS="$CALLS"
export TEST_CHECK_SCRIPT="$TMP_DIR/check.nft"
export TEST_APPLY_SCRIPT="$TMP_DIR/apply.nft"

# A new/unconfigured node must not receive a host-wide drop table.
"$GUARD"
test ! -s "$CALLS"

: > "$STATE_DIR/managed-sources"
"$GUARD"
test ! -s "$CALLS"

# Valid dual-stack sources create an atomic check/apply transaction.
cat > "$STATE_DIR/managed-sources" <<'SOURCES'
192.0.2.17/32
2001:db8::17/128
SOURCES
"$GUARD"
grep -Fxq -- 'list table inet oneclickvirt_egress_boot' "$CALLS"
grep -Eq -- '^-c -f /' "$CALLS"
grep -Eq -- '^-f /' "$CALLS"
cmp -s "$TEST_CHECK_SCRIPT" "$TEST_APPLY_SCRIPT"
grep -Fq 'add table inet oneclickvirt_egress_boot' "$TEST_APPLY_SCRIPT"
grep -Fq 'ip saddr 192.0.2.17/32' "$TEST_APPLY_SCRIPT"
grep -Fq 'ip6 saddr 2001:db8::17/128' "$TEST_APPLY_SCRIPT"

# Existing guard state is flushed and replaced, not accumulated.
export NFT_TABLE_EXISTS=0
"$GUARD"
grep -Fq 'flush table inet oneclickvirt_egress_boot' "$TEST_APPLY_SCRIPT"

# A malformed Agent-owned file fails before nft apply; arbitrary text is never
# interpolated into a ruleset.
unset NFT_TABLE_EXISTS
: > "$CALLS"
rm -f "$TEST_APPLY_SCRIPT"
printf '%s\n' '192.0.2.17/32; drop' > "$STATE_DIR/managed-sources"
if "$GUARD" >/dev/null 2>&1; then
  echo "malformed source unexpectedly succeeded" >&2
  exit 1
fi
test ! -e "$TEST_APPLY_SCRIPT"

# A configured node without nft cannot be reported as guarded.
printf '%s\n' '192.0.2.17/32' > "$STATE_DIR/managed-sources"
export ONECLICKVIRT_EGRESS_NFT_BIN="$TMP_DIR/missing-nft"
if "$GUARD" >/dev/null 2>&1; then
  echo "configured source without nft unexpectedly succeeded" >&2
  exit 1
fi

echo "agent egress boot guard tests passed"
