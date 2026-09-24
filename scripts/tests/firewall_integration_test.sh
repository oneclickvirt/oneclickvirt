#!/usr/bin/env bash
set -euo pipefail

# Real kernel firewall tests in a disposable, unmounted network namespace.
# Only NET_ADMIN is needed; host kernel modules are prepared separately by CI.
# Missing Docker/kernel firewall support is a failure, never a skipped test.
# This tests rule management, not routed IPv6 connectivity or address allocation.
command -v docker >/dev/null
command -v go >/dev/null
test_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_container="ocv-firewall-regression-$(date +%Y%m%d%H%M%S)-$$"
test_container_id=""
test_log=$(mktemp "${TMPDIR:-/tmp}/ocv-firewall.XXXXXX")
cleanup() {
    if [[ -n "$test_container_id" ]]; then
        docker rm -f "$test_container_id" >/dev/null
    fi
    rm -f "$test_log"
}
trap cleanup EXIT
diagnose_firewall_runner() {
    local backend="${1:-unknown}"
    echo "=== Firewall integration diagnostics (backend=${backend}) ===" >&2
    docker version >&2 || true
    docker info --format 'server={{.ServerVersion}} os={{.OperatingSystem}} arch={{.Architecture}} cgroup={{.CgroupVersion}} security={{json .SecurityOptions}}' >&2 || true
    docker inspect "$test_container_id" --format 'container={{.Id}} image={{.Config.Image}} status={{.State.Status}} privileged={{.HostConfig.Privileged}} cap_add={{json .HostConfig.CapAdd}} network={{.HostConfig.NetworkMode}}' >&2 || true
    docker exec "$test_container_id" sh -c '
        set +e
        uname -a
        printf "container IPv6 sysctl: "; cat /proc/sys/net/ipv6/conf/all/disable_ipv6 2>/dev/null || true
        printf "container IPv6 routes:\n"; ip -6 route 2>&1 || true
        printf "nft: "; nft --version 2>&1 || true
        printf "iptables: "; iptables --version 2>&1 || true
        printf "ip6tables: "; ip6tables --version 2>&1 || true
        printf "capabilities:\n"; grep Cap /proc/self/status 2>/dev/null || true
    ' >&2 || true
    if [[ -s "$test_log" ]]; then
        echo "=== Failing Go test output ===" >&2
        cat "$test_log" >&2
    fi
}

if ! test_container_id=$(docker run --detach --name "$test_container" --cap-add NET_ADMIN --network bridge \
    --entrypoint /bin/sh debian:12 -c 'exec sleep 3600'); then
    echo "Unable to start the disposable firewall test container" >&2
    exit 1
fi
if ! docker exec "$test_container_id" sh -ec '
    export DEBIAN_FRONTEND=noninteractive
    for attempt in 1 2 3; do
        apt-get update -qq && apt-get install -y -qq --no-install-recommends nftables iptables iproute2 && exit 0
        sleep $((attempt * 2))
    done
    exit 1
'; then
    echo "Unable to install firewall tools in the disposable test container" >&2
    diagnose_firewall_runner "package-install"
    exit 1
fi
# A Debian userspace container cannot load modules from the host kernel's
# /lib/modules. Probe both backends before Go resets a fixture, so a missing
# ip6table_nat is reported once as a runner prerequisite, not dozens of failures.
if ! docker exec "$test_container_id" sh -ec '
    nft list ruleset >/dev/null
    for tool in iptables-nft ip6tables-nft iptables-legacy ip6tables-legacy; do
        for table in nat filter; do
            "$tool" -w 5 -t "$table" -S >/dev/null || exit 1
        done
    done
'; then
    echo "Missing kernel firewall support. On the Docker daemon host, run: sudo bash scripts/tests/prepare_firewall_kernel.sh" >&2
    diagnose_firewall_runner "kernel-preflight"
    exit 1
fi
cd "$test_root/server"
for xtables_mode in nft legacy; do
    printf 'Testing real firewall with iptables-%s / ip6tables-%s\n' "$xtables_mode" "$xtables_mode"
    if ! docker exec "$test_container_id" update-alternatives --set iptables "/usr/sbin/iptables-$xtables_mode" \
        || ! docker exec "$test_container_id" update-alternatives --set ip6tables "/usr/sbin/ip6tables-$xtables_mode"; then
        diagnose_firewall_runner "$xtables_mode-alternatives"
        exit 1
    fi
    : > "$test_log"
    set +e
    OCV_FIREWALL_TEST_CONTAINER="$test_container" go test -count=1 -p 1 \
        -tags firewall_integration -run '^TestFirewallIntegration' -v \
        ./provider/firewall ./provider/incus ./provider/lxd >"$test_log" 2>&1
    test_rc=$?
    set -e
    cat "$test_log"
    if [[ "$test_rc" -ne 0 ]]; then
        diagnose_firewall_runner "$xtables_mode"
        exit "$test_rc"
    fi
done
