#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
export fixture
fail() { echo "Firewall runner prerequisite test failed: $*" >&2; exit 1; }

# Execute the real kernel setup script without changing the developer's kernel.
uname() { printf '%s\n' "${MOCK_KERNEL:-Linux}"; }
modprobe() {
    printf '%s\n' "$1" >> "$fixture/modules"
    [[ "$1" != "${MISSING_MODULE:-}" ]]
}
export -f uname modprobe
bash "$ROOT_DIR/scripts/tests/prepare_firewall_kernel.sh"
for module in nf_tables iptable_filter iptable_nat ip6table_filter ip6table_nat xt_comment; do
    grep -Fxq "$module" "$fixture/modules" || fail "missing host module ${module}"
done
: > "$fixture/modules"
if MISSING_MODULE=ip6table_nat bash "$ROOT_DIR/scripts/tests/prepare_firewall_kernel.sh" >"$fixture/setup.log" 2>&1; then
    fail "missing IPv6 legacy NAT module was ignored"
fi
grep -Fq 'Unable to load ip6table_nat' "$fixture/setup.log" || fail "missing module diagnostic"
! grep -Fq xt_comment "$fixture/modules" || fail "setup continued after a missing module"
if MOCK_KERNEL=Darwin bash "$ROOT_DIR/scripts/tests/prepare_firewall_kernel.sh" >"$fixture/setup.log" 2>&1; then
    fail "kernel setup accepted a non-Linux Docker client"
fi
unset -f uname modprobe

# Execute the actual runner script with Docker/Go boundary mocks. A failed
# read-only kernel probe must stop before Go tests and still remove the fixture.
docker() {
    printf '%s\n' "$*" >> "$fixture/docker.calls"
    case "$1" in
        run)
            [[ "$*" == *'--cap-add NET_ADMIN --network bridge'* ]] || return 2
            [[ "$*" != *--privileged* && "$*" != *--mount* && "$*" != *' -v '* ]] || return 2
            printf '%s\n' fixture-firewall-id
            ;;
        exec)
            if [[ "$*" == *'for tool in iptables-nft ip6tables-nft iptables-legacy ip6tables-legacy'* ]]; then
                [[ "$*" == *'for table in nat filter'* && "$*" == *'-S >/dev/null'* ]] || return 2
                [[ "${MISSING_TABLE:-false}" != true ]] || return 1
            fi
            ;;
    esac
}
go() {
    [[ "${OCV_FIREWALL_TEST_CONTAINER:-}" == ocv-firewall-regression-* ]] || return 2
    printf '%s\n' "$*" >> "$fixture/go.calls"
}
export -f docker go
if MISSING_TABLE=true bash "$ROOT_DIR/scripts/tests/firewall_integration_test.sh" >"$fixture/runner.log" 2>&1; then
    fail "missing kernel table was ignored"
fi
[[ ! -s "$fixture/go.calls" ]] || fail "Go tests ran with missing kernel tables"
grep -Fq 'kernel-preflight' "$fixture/runner.log" || fail "no early kernel diagnosis"
grep -Fxq 'rm -f fixture-firewall-id' "$fixture/docker.calls" || fail "failed preflight leaked its container"
: > "$fixture/docker.calls"
bash "$ROOT_DIR/scripts/tests/firewall_integration_test.sh" >"$fixture/runner.log" 2>&1
[[ "$(wc -l < "$fixture/go.calls" | tr -d ' ')" == 2 ]] || fail "both real firewall backends were not exercised"
grep -Fxq 'rm -f fixture-firewall-id' "$fixture/docker.calls" || fail "successful tests leaked their container"
grep -Fq 'run: sudo bash scripts/tests/prepare_firewall_kernel.sh' "$ROOT_DIR/.github/workflows/integration-tests.yml" || fail "CI does not prepare host kernel modules"

echo "firewall runner prerequisite tests passed"
