#!/usr/bin/env bash
set -euo pipefail

# Exercise the generated Containerd readiness probe without a remote worker.
# The fixture deliberately places the CNI binaries in the nerdctl-full 2.4
# location and provides no nerdctl network object: runtime readiness is based
# on the containerd socket and the installer-owned conflist instead.
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
fail() { echo "Containerd runtime verifier test failed: $*" >&2; exit 1; }

source "$ROOT_DIR/action_tests/common/test_framework.sh"
source "$ROOT_DIR/action_tests/common/node_manager.sh"

fake_bin="$fixture/bin"
runtime_root="$fixture/runtime root"
cni_dir="$runtime_root/usr/local/libexec/cni"
config_dir="$runtime_root/etc/cni/net.d"
mkdir -p "$fake_bin" "$cni_dir" "$config_dir"
ln -s "$(command -v jq)" "$fake_bin/jq"
cat > "$fake_bin/ctr" <<'SH'
#!/bin/sh
[ "${FIXTURE_CTR_FAILURE:-false}" != true ] || { echo 'containerd socket unavailable' >&2; exit 1; }
[ "$1" = version ] || exit 1
SH
cat > "$fake_bin/nerdctl" <<'SH'
#!/bin/sh
# No lazily-created nerdctl network object is needed by readiness.
[ "$1" = version ] || { echo 'nerdctl only supports version in this fixture' >&2; exit 1; }
SH
cat > "$fake_bin/systemctl" <<'SH'
#!/bin/sh
[ "${FIXTURE_SYSTEMD:-active}" != unavailable ] || exit 1
case "$*" in
    *LoadState*) echo loaded ;;
    *ActiveState*)
        [ "${FIXTURE_SYSTEMD:-active}" != bus_lost ] || exit 1
        echo "${FIXTURE_SYSTEMD:-active}"
        ;;
    *) exit 1 ;;
esac
SH
chmod +x "$fake_bin/ctr" "$fake_bin/nerdctl" "$fake_bin/systemctl"
for plugin in bridge host-local loopback portmap firewall tuning; do
    printf '#!/bin/sh\nexit 0\n' > "$cni_dir/$plugin"
    chmod +x "$cni_dir/$plugin"
done
cat > "$config_dir/10-containerd-net.conflist" <<'JSON'
{
  "cniVersion": "1.0.0",
  "name": "containerd-net",
  "plugins": [
    {"type": "bridge", "ipam": {"type": "host-local"}},
    {"type": "portmap"},
    {"type": "firewall"},
    {"type": "tuning"}
  ]
}
JSON

# Keep the real dispatch, PATH wrapper and retry functions. Only replace SSH
# with the same double-shell quoting used by the actual platform transports.
ACTIVE_PLATFORM=fixture
PLATFORM_EXEC_RETRIES=1
PLATFORM_REMOTE_PATH="$fake_bin:"
fixture_platform_ssh_exec() {
    local command="$2"
    CONTAINERD_VERIFY_ROOT="$runtime_root" bash -c "bash -c $(printf '%q' "$command")"
}
platform_validate_worker_resources() { return 0; }
wait_for_ssh() { return 0; }

verify_worker_runtime fixture-worker 127.0.0.1 containerd || fail "valid nerdctl-full fixture was rejected"
for state in unavailable bus_lost; do
    FIXTURE_SYSTEMD="$state" verify_worker_runtime fixture-worker 127.0.0.1 containerd || fail "healthy socket rejected when systemd=$state"
done

expect_failure() {
    local output
    if output=$(verify_worker_runtime fixture-worker 127.0.0.1 containerd 2>&1); then
        fail "$1 was accepted"
    fi
    grep -Fq "$2" <<< "$output" || fail "$1 diagnostic missing: $output"
}
FIXTURE_SYSTEMD=inactive expect_failure "inactive service" 'containerd systemd service is not active'
FIXTURE_CTR_FAILURE=true expect_failure "unavailable socket" 'containerd socket unavailable'

cp "$config_dir/10-containerd-net.conflist" "$fixture/good.json"
for filter in '.plugins |= map(select(.type != "portmap"))' '.plugins[0].ipam.type = "invalid"' '.name = "other-network"'; do
    jq "$filter" "$fixture/good.json" > "$config_dir/10-containerd-net.conflist"
    expect_failure "invalid CNI chain" 'invalid containerd-net plugin chain'
done
printf '{invalid json\n' > "$config_dir/10-containerd-net.conflist"
expect_failure "malformed JSON" 'invalid containerd-net plugin chain'
: > "$config_dir/10-containerd-net.conflist"
expect_failure "empty CNI configuration" 'Containerd CNI configuration is missing'
cp "$fixture/good.json" "$config_dir/10-containerd-net.conflist"

rm -f "$cni_dir/firewall"
expect_failure "missing CNI plugin" "$cni_dir/firewall"

echo "Containerd runtime verifier tests passed"
