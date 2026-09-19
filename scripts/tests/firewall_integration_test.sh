#!/usr/bin/env bash
set -euo pipefail

# Real kernel firewall tests in a disposable, unmounted network namespace.
# Missing Docker/NET_ADMIN/IPv6 support is a failure, never a skipped test.
command -v docker >/dev/null
command -v go >/dev/null
test_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
test_container="ocv-firewall-regression-$(date +%Y%m%d%H%M%S)-$$"
test_container_id=""
cleanup() {
    if [[ -n "$test_container_id" ]]; then
        docker rm -f "$test_container_id" >/dev/null
    fi
}
trap cleanup EXIT
test_container_id=$(docker run --detach --name "$test_container" --cap-add NET_ADMIN \
    --entrypoint /bin/sh debian:12 -c 'exec sleep 3600')
docker exec "$test_container_id" sh -c 'apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq nftables iptables iproute2'
cd "$test_root/server"
for xtables_mode in nft legacy; do
    printf 'Testing real firewall with iptables-%s / ip6tables-%s\n' "$xtables_mode" "$xtables_mode"
    docker exec "$test_container_id" update-alternatives --set iptables "/usr/sbin/iptables-$xtables_mode"
    docker exec "$test_container_id" update-alternatives --set ip6tables "/usr/sbin/ip6tables-$xtables_mode"
    OCV_FIREWALL_TEST_CONTAINER="$test_container" go test -count=1 -p 1 \
        -tags firewall_integration -run '^TestFirewallIntegration' -v \
        ./provider/firewall ./provider/incus ./provider/lxd
done
