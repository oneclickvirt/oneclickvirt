#!/bin/bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
MODULE="${ROOT_DIR}/action_tests/modules/09_providers.sh"
WORKFLOW="${ROOT_DIR}/.github/workflows/integration-tests.yml"

fail() {
    echo "IPv6 tunnel CI isolation test failed: $*" >&2
    exit 1
}

# shellcheck source=../../action_tests/modules/09_providers.sh
# shellcheck disable=SC1091 # The fixture path is resolved from this repository root.
source "$MODULE"

CALLS_FILE=$(mktemp)
SKIPS_FILE=$(mktemp)
trap 'rm -f "$CALLS_FILE" "$SKIPS_FILE"' EXIT

test_api() {
    printf '%s|%s|%s\n' "$1" "$2" "$3" >> "$CALLS_FILE"
    printf '%s' '{"code":200,"data":{"id":42}}'
}

record_skip_result() {
    printf '%s|%s|%s|%s|%s\n' "$1" "$2" "$3" "$4" "$5" >> "$SKIPS_FILE"
}

record_fail_result() {
    fail "unexpected failure result: $*"
}

export PROVIDER_ID=1
export ACTION_TEST_LIVE_IPV6_TUNNEL=false
run_ipv6_tunnel_host_lifecycle_tests providers >/dev/null
[[ ! -s "$CALLS_FILE" ]] || fail "default Action mode invoked a tunnel endpoint: $(<"$CALLS_FILE")"
grep -Fq '默认CI不调用隧道接口' "$SKIPS_FILE" || fail "default Action mode did not record the tunnel isolation skip"

: > "$CALLS_FILE"
export ACTION_TEST_LIVE_IPV6_TUNNEL=true
run_ipv6_tunnel_host_lifecycle_tests providers >/dev/null
grep -Fq '/api/v1/admin/providers/1/ipv6-tunnels' "$CALLS_FILE" || fail "explicit host lifecycle opt-in did not invoke tunnel endpoints"
grep -Fq 'Delete disabled IPv6 tunnel' "$CALLS_FILE" || fail "explicit host lifecycle opt-in omitted cleanup coverage"

action_test_requires_routed_ipv6 kubevirt || fail "KubeVirt must use the routed IPv6 pool contract"
action_test_requires_routed_ipv6 qemu || fail "QEMU must use the routed IPv6 pool contract"
if action_test_requires_routed_ipv6 docker; then
    fail "Docker must retain the manual IPv6 pool contract"
fi

grep -Fq 'live_ipv6_tunnel:' "$WORKFLOW" || fail "workflow has no explicit tunnel lifecycle input"
grep -Fq 'ACTION_TEST_LIVE_IPV6_TUNNEL:' "$WORKFLOW" || fail "workflow does not pass the tunnel lifecycle input to the harness"

echo "IPv6 tunnel CI isolation tests passed"
