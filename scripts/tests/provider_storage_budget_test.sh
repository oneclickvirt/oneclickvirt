#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
source "$ROOT_DIR/action_tests/common/test_framework.sh"
source "$ROOT_DIR/action_tests/common/node_manager.sh"
export INSTANCE_TYPES=container ACTION_TEST_ALLOW_WORKER_DOWNSIZE=false
export MOCK_MEMORY=16000 MOCK_CPU=8 MOCK_ROOT_DISK=5 MOCK_BACKING_DISK=60
export MOCK_PROFILE='{"devices":{"root":{"type":"disk","path":"/","pool":"fixture"}}}'
export MOCK_POOL='{"config":{"source":"dedicated-zpool"}}'
export MOCK_SPACE='{"space":{"total":64424509440,"used":0}}'
export MOCK_QUERY_LOG="$fixture/queries"
mkdir "$fixture/bin"
cat > "$fixture/bin/lxc" <<'CLI'
#!/usr/bin/env bash
printf '%s\n' "$*" >> "$MOCK_QUERY_LOG"
[[ "$1" == query ]] || exit 1
case "$2" in
    /1.0/profiles/default) printf '%s\n' "$MOCK_PROFILE" ;;
    /1.0/storage-pools/fixture) printf '%s\n' "$MOCK_POOL" ;;
    /1.0/storage-pools/fixture/resources) printf '%s\n' "$MOCK_SPACE" ;;
    *) exit 1 ;;
esac
CLI
cp "$fixture/bin/lxc" "$fixture/bin/incus"
chmod +x "$fixture/bin/lxc" "$fixture/bin/incus"
export PATH="$fixture/bin:$PATH"
awk() {
    if [[ "${2:-}" == /proc/meminfo ]]; then printf '%s\n' "$MOCK_MEMORY"; else command awk "$@"; fi
}
getconf() { printf '%s\n' "$MOCK_CPU"; }
df() {
    local available="$MOCK_ROOT_DISK"
    [[ "${2:-}" == / ]] || available="$MOCK_BACKING_DISK"
    printf 'Filesystem 1024-blocks Used Available Capacity Mounted\nfixture 100000000 0 %s 0%% /\n' "$((available * 1024 * 1024))"
}
test() {
    if [[ "$#" == 2 && "$1" == -e && "$2" == /dev/kvm ]]; then return 0; else builtin test "$@"; fi
}
export -f awk getconf df test
platform_ssh_exec() { bash -c "$2"; }
fail() { printf 'Storage budget test failed: %s\n' "$*" >&2; exit 1; }
check_worker() {
    : > "$MOCK_QUERY_LOG"
    local expected="$1" status=0
    platform_validate_worker_resources "$ENV_TYPE" 192.0.2.10 fixture "${ACTION_TEST_WORKER_RESOURCE_PROFILE:-standard}" runtime > "$fixture/output" 2>&1 || status=$?
    [[ "$status" == "$expected" ]] || { cat "$fixture/output" >&2; fail "expected $expected, got $status"; }
    [[ "$(wc -l < "$MOCK_QUERY_LOG")" -le 6 ]] || fail 'storage queries grew beyond a fixed number'
}

for ENV_TYPE in lxd incus; do
    configure_action_test_resources_for_env "$ENV_TYPE"
    MOCK_ROOT_DISK=5 MOCK_SPACE='{"space":{"total":64424509440,"used":0}}'
    check_worker 0
    [[ "$(wc -l < "$MOCK_QUERY_LOG")" -eq 3 ]] || fail 'the actual root pool was not checked exactly once'
    MOCK_ROOT_DISK=60 MOCK_SPACE='{"space":{"total":5368709120,"used":0}}'
    check_worker 75
    grep -Fq 'WORKER_RESOURCE_INSUFFICIENT:' "$fixture/output" || fail 'pool capacity failure was not visible'
    for MOCK_SPACE in '{' '{"space":{"total":60,"used":61}}' '{"space":{"total":"unknown","used":0}}'; do
        check_worker 75
        grep -Fq 'WORKER_RESOURCE_INVALID:' "$fixture/output" || fail 'malformed pool metrics became a capacity failure'
    done
    MOCK_SPACE='{"metadata":{"space":{"total":64424509440,"used":0}}}'
    MOCK_PROFILE='{"metadata":{"devices":{"root":{"type":"disk","path":"/","pool":"fixture"}}}}'
    MOCK_POOL='{"metadata":{"config":{"source":"dedicated-zpool"}}}'
    check_worker 0
    MOCK_PROFILE='{"devices":{"root":{"type":"disk","path":"/","pool":"fixture"}}}'
    MOCK_POOL='{"config":{"source":"dedicated-zpool"}}'
    MOCK_ROOT_DISK=0
    check_worker 75
    MOCK_ROOT_DISK=60 MOCK_BACKING_DISK=3
    touch "$fixture/pool.img"
    MOCK_POOL=$(jq -cn --arg source "$fixture/pool.img" '{config:{source:$source}}')
    check_worker 75
    # ZFS may replace source with the zpool name; retain its loop backing path.
    MOCK_POOL=$(jq -cn --arg source "$fixture/pool.img" '{config:{source:"zpool","volatile.initial_source":$source}}')
    check_worker 75
    MOCK_BACKING_DISK=60
    check_worker 0
    MOCK_PROFILE='{"devices":{}}'
    check_worker 75
    # A preinstalled CLI with an uninitialized daemon must not prevent setup.
    : > "$MOCK_QUERY_LOG"
    platform_validate_worker_resources "$ENV_TYPE" 192.0.2.10 fixture standard host > "$fixture/output" 2>&1 || fail 'fresh worker with an uninitialized CLI was rejected'
    [[ ! -s "$MOCK_QUERY_LOG" ]] || fail 'host preflight queried an uninitialized daemon'
    # Runtime verification must select the actual pool check after installation.
    platform_exec_and_wait() { return 0; }
    status=0
    verify_worker_runtime fixture 192.0.2.10 "$ENV_TYPE" > "$fixture/output" 2>&1 || status=$?
    [[ "$status" == 75 && -s "$MOCK_QUERY_LOG" ]] || fail 'runtime verification skipped the missing pool'
    MOCK_PROFILE='{"devices":{"root":{"type":"disk","path":"/","pool":"fixture"}}}'
    MOCK_POOL='{"config":{"source":"dedicated-zpool"}}'
    MOCK_SPACE='{"space":{"total":23622320128,"used":0}}'
    ACTION_TEST_ALLOW_WORKER_DOWNSIZE=true
    check_worker 0
    [[ "$ACTION_TEST_WORKER_RESOURCE_PROFILE" == compact ]] || fail 'limited pool did not lower guest disk sizes'
    ACTION_TEST_ALLOW_WORKER_DOWNSIZE=false
done
echo 'PASS: LXD/Incus actual pools, API envelopes, loop backing limits, invalid metrics and compact fallback'
