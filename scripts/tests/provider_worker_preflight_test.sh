#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
# shellcheck disable=SC1091
source "$ROOT_DIR/action_tests/common/test_framework.sh"
# shellcheck disable=SC1091
source "$ROOT_DIR/action_tests/common/node_manager.sh"

fail() { echo "Worker preflight failed: $*" >&2; exit 1; }
expect_failure() {
    local expected="$1" actual=0
    shift
    "$@" > "$fixture/unexpected" 2> "$fixture/diagnostic" || actual=$?
    [[ "$actual" == "$expected" ]] || fail "expected exit $expected, got $actual"
}
reset_profile() {
    ACTION_TEST_WORKER_RESOURCE_PROFILE=standard
    export ENV_TYPE=proxmoxve INSTANCE_TYPES=both
    configure_action_test_resources_for_env "$ENV_TYPE"
}
alice_get_permissions() { printf '%s\n%s\n' "$permissions" "$permissions_code"; }
alice_get_plans() { printf '%s\n%s\n' "$plans" "$plans_code"; }
alice_create_instance_raw() { fail 'an unusable plan reached instance creation'; }
permissions='{"data":{"allow_packages":"38|44|52"}}'
permissions_code=200 plans_code=200
reset_profile
plans='{"data":[{"id":52,"cpu":8,"memory":16384,"disk":100},{"id":38,"cpu":3,"memory":4096,"disk":60},{"id":404,"cpu":4,"memory":8192,"disk":60},{"id":44,"cpu":4,"memory":8192,"disk":60}]}'
[[ "$(_alice_get_min_package_id proxmoxve 2> "$fixture/diagnostic")" == 44 ]] || fail 'did not select the smallest allowed preferred plan'
plans='{"data":[{"id":38,"cpu":3,"memory":4096,"disk":60},{"id":404,"cpu":4,"memory":8192,"disk":60}]}'
[[ "$(_alice_get_min_package_id proxmoxve 2> "$fixture/diagnostic")" == 38 ]] || fail 'did not downgrade to an allowed usable plan'
export ACTION_TEST_ALLOW_WORKER_DOWNSIZE=false
expect_failure 75 _alice_get_min_package_id proxmoxve
ACTION_TEST_ALLOW_WORKER_DOWNSIZE=true
export PROXMOXVE_MIN_WORKER_MEMORY_MB=8192
expect_failure 75 _alice_get_min_package_id proxmoxve
unset PROXMOXVE_MIN_WORKER_MEMORY_MB
plans='{"data":[{"id":38,"cpu":1,"memory":2048,"disk":60}]}'
expect_failure 75 alice_platform_create_instance proxmoxve 8
plans='{"data":[{"id":38,"cpu":2,"memory":4096,"disk":16}]}'
expect_failure 75 alice_platform_create_instance proxmoxve 8
echo 'PASS: Alice prefers a suitable allowed plan, downgrades when usable, and keeps hard resource limits'

permissions='{"data":{"allow_packages":[38,"44"]}}'
plans='[{"id":"44","cpu":"4","memory":"8192","disk":"60"},{"id":38,"cpu":"unknown","memory":8192,"disk":60}]'
[[ "$(_alice_get_min_package_id proxmoxve 2> "$fixture/diagnostic")" == 44 ]] || fail 'numeric strings or array permissions were rejected'
permissions_code=403
expect_failure 1 _alice_get_min_package_id proxmoxve
permissions_code=200 permissions='not-json'
expect_failure 1 _alice_get_min_package_id proxmoxve
permissions='{"data":{"allow_packages":"38|44"}}' plans_code=503
expect_failure 1 _alice_get_min_package_id proxmoxve
plans_code=200 plans='{"data":{}}'
expect_failure 1 _alice_get_min_package_id proxmoxve
alice_list_instances_raw() { printf '%s\n%s\n' "$rebuild_inventory" "$rebuild_http"; }
alice_rebuild_instance_raw() { fail 'an unresolved existing plan reached rebuild'; }
rebuild_http=503 rebuild_inventory='[]'
expect_failure 1 alice_platform_reinstall_instance fixture debian
rebuild_http=200
for rebuild_inventory in '{' '{"data":[{"id":"fixture"}]}' '{"data":[{"id":"fixture","plan_id":38},{"id":"fixture","plan_id":44}]}'; do
    expect_failure 1 alice_platform_reinstall_instance fixture debian
done
echo 'PASS: invalid Alice API responses remain errors and cannot deploy a plan'

lightnode_get_packages() { printf '%s\n200\n' "$packages"; }
export LIGHTNODE_REGION='test-region' LIGHTNODE_ZONE='test-zone' LIGHTNODE_PACKAGE_CODE=''
export LIGHTNODE_TARGET_CPU=2 LIGHTNODE_TARGET_MEMORY_MB=4096 LIGHTNODE_STRICT_RECOMMENDED_SPEC=true
packages='{"packages":[{"packageCode":"small","cpu":3,"memory":4},{"packageCode":"peak","cpu":4,"memory":8}]}'
[[ "$(_lightnode_get_default_package 2> "$fixture/diagnostic")" == peak ]] || fail 'LightNode ignored the preferred peak budget'
packages='{"packages":[{"packageCode":"small","cpu":3,"memory":4}]}'
[[ "$(_lightnode_get_default_package 2> "$fixture/diagnostic")" == small ]] || fail 'LightNode did not select the usable fallback'
LIGHTNODE_PACKAGE_CODE=unavailable
[[ "$(_lightnode_get_default_package 2> "$fixture/diagnostic")" == small ]] || fail 'LightNode did not replace an unavailable requested package with a usable fallback'
ACTION_TEST_ALLOW_WORKER_DOWNSIZE=false
expect_failure 75 _lightnode_get_default_package
ACTION_TEST_ALLOW_WORKER_DOWNSIZE=true
LIGHTNODE_PACKAGE_CODE=''
packages='{"packages":[{"packageCode":"large","cpu":6,"memory":12}]}'
[[ "$(_lightnode_get_default_package 2> "$fixture/diagnostic")" == large ]] || fail 'LightNode rejected a usable larger plan without an exact spec match'
packages='{"packages":[{"packageCode":"tiny","cpu":1,"memory":2},{"packageCode":"elsewhere","cpu":4,"memory":8,"zoneCode":"another-zone"}]}'
expect_failure 75 _lightnode_get_default_package
echo 'PASS: LightNode falls back to a usable package and retains region and resource constraints'

# Execute the real resource check locally with synthetic host metrics.
MOCK_WORKER_MEMORY=3878 MOCK_WORKER_CPU=3 MOCK_WORKER_DISK=57 MOCK_WORKER_KVM=true
export MOCK_WORKER_MEMORY MOCK_WORKER_CPU MOCK_WORKER_DISK MOCK_WORKER_KVM
awk() {
    if [[ "${2:-}" == /proc/meminfo ]]; then
        printf '%s\n' "$MOCK_WORKER_MEMORY"
    else
        command awk "$@"
    fi
}
getconf() { printf '%s\n' "$MOCK_WORKER_CPU"; }
df() { printf 'Filesystem 1024-blocks Used Available Capacity Mounted\nfixture 100000000 0 %s 0%% /\n' "$((MOCK_WORKER_DISK * 1024 * 1024))"; }
test() {
    if [[ "$#" == 2 && "$1" == -e && "$2" == /dev/kvm ]]; then
        [[ "$MOCK_WORKER_KVM" == true ]]
    else
        builtin test "$@"
    fi
}
export -f awk getconf df test
platform_ssh_exec() { bash -c "$2"; }
platform_validate_worker_resources proxmoxve 192.0.2.10 alice > "$fixture/result" 2> "$fixture/diagnostic" || fail 'the real 3C/4GB host check did not accept the compact profile'
[[ "$ACTION_TEST_WORKER_RESOURCE_PROFILE" == compact && "$ACTION_TEST_CONTAINER_MEMORY" == 512 && "$ACTION_TEST_VM_MEMORY" == 512 ]] || fail 'accepted fallback did not shrink guest resources'
reset_profile
MOCK_WORKER_MEMORY=2000
expect_failure 75 platform_validate_worker_resources proxmoxve 192.0.2.10 alice
[[ "$ACTION_TEST_WORKER_RESOURCE_PROFILE" == standard && "$ACTION_TEST_CONTAINER_MEMORY" == 1024 ]] || fail 'failed check changed the guest profile'
MOCK_WORKER_MEMORY=3878 MOCK_WORKER_KVM=false
expect_failure 75 platform_validate_worker_resources incus 192.0.2.10 alice
platform_validate_worker_resources proxmoxve 192.0.2.10 alice > "$fixture/result" 2> "$fixture/diagnostic" || fail 'PVE TCG worker was rejected without KVM'
apply_worker_resource_profile standard || fail 'TCG profile could not be restored'
MOCK_WORKER_KVM=true MOCK_WORKER_DISK=10
expect_failure 75 platform_validate_worker_resources proxmoxve 192.0.2.10 alice
MOCK_WORKER_DISK=57
expect_failure 75 platform_validate_worker_resources kubevirt 192.0.2.10 alice
MOCK_WORKER_CPU=2
expect_failure 75 platform_validate_worker_resources proxmoxve 192.0.2.10 alice
INSTANCE_TYPES=container
platform_validate_worker_resources proxmoxve 192.0.2.10 alice compact > "$fixture/result" 2> "$fixture/diagnostic" || fail '2C/4GB container fallback was rejected'
[[ "$ACTION_TEST_CONTAINER_DISK" == 5 && "$ACTION_TEST_VM_DISK" == 8 ]] || fail 'compact profile left oversized disks'
apply_worker_resource_profile compact || fail 'compact reapplication failed'
apply_worker_resource_profile standard || fail 'standard restoration failed'
[[ "$ACTION_TEST_CONTAINER_MEMORY" == 1024 && "$ACTION_TEST_CONTAINER_DISK" == 8 ]] || fail 'standard retained compact resources'
reset_profile
MOCK_WORKER_CPU=3
MOCK_WORKER_MEMORY=unknown
expect_failure 75 platform_validate_worker_resources proxmoxve 192.0.2.10 alice
[[ "$ACTION_TEST_WORKER_RESOURCE_PROFILE" == standard ]] || fail 'invalid metric changed the profile'
MOCK_WORKER_MEMORY=3878 MOCK_WORKER_CPU='3 CPUs'
expect_failure 75 platform_validate_worker_resources proxmoxve 192.0.2.10 alice
MOCK_WORKER_CPU=3
for native_env in docker podman containerd; do
    ENV_TYPE="$native_env" INSTANCE_TYPES=container
    configure_action_test_resources_for_env "$native_env"
    [[ "$ACTION_TEST_CONTAINER_MEMORY" == 2048 && "$ACTION_TEST_CONTAINER_DISK" == 5 ]] || fail 'native runtime inherited nested sizes'
    MOCK_WORKER_CPU=2 MOCK_WORKER_KVM=false
    platform_validate_worker_resources "$native_env" 192.0.2.10 alice > "$fixture/result" 2> "$fixture/diagnostic" || fail "${native_env} container downgrade failed"
    [[ "$ACTION_TEST_WORKER_RESOURCE_PROFILE" == compact && "$ACTION_TEST_CONTAINER_MEMORY" == 512 ]] || fail "${native_env} kept oversized guests"
    apply_worker_resource_profile standard || fail 'native profile restoration failed'
done
reset_profile
MOCK_WORKER_CPU=3 MOCK_WORKER_KVM=true
echo 'PASS: compact guests still require host memory, disk, KVM and runtime headroom'

# Exercise the actual nested provisioning calls with local API/SSH substitutes.
permissions='{"data":{"allow_packages":"38|44"}}'
export ALICE_CLIENT_ID=fixture ALICE_CLIENT_SECRET=fixture ALICE_PRIVATE_KEY=fixture
mktemp() {
    if [[ "${1:-}" == /tmp/platform_ssh_key_XXXXXX.pem ]]; then
        command mktemp "$fixture/key.XXXXXX"
    else
        command mktemp "$@"
    fi
}
get_enabled_platforms() { printf '%s\n' alice; }
platform_dispatch() {
    case "$1:$2" in
        alice:init) alice_platform_init ;;
        alice:list_instances) printf '%s\n' '[]' ;;
        alice:create_instance) printf '%s\n' '{"instance_id":"fixture","ipv4":"192.0.2.10","password":"fixture","platform":"alice"}' ;;
        *) fail "unexpected provider operation: $1:$2" ;;
    esac
}
wait_for_ssh() { :; }
ensure_worker_swap() { :; }
export SKIP_INSTANCE_DELETE=true PLATFORM_ALLOW_CONCURRENT_INSTANCES=true
reset_profile
worker_info=$(create_test_node proxmoxve 8 2> "$fixture/diagnostic") || fail 'local provisioning fixture failed'
[[ "$(jq -r '.resource_profile' <<< "$worker_info")" == compact ]] || fail 'nested worker result lost its compact profile'
[[ "$ACTION_TEST_WORKER_RESOURCE_PROFILE" == standard ]] || fail 'command substitution unexpectedly modified the caller'
apply_worker_resource_profile "$(jq -r '.resource_profile' <<< "$worker_info")" || fail 'caller could not restore compact profile'
[[ "$ACTION_TEST_CONTAINER_MEMORY" == 512 && "$ACTION_TEST_VM_MEMORY" == 512 ]] || fail 'caller restored host thresholds without restoring guest sizes'
echo 'PASS: downgraded resource profile survives the actual worker provisioning pipeline'

echo 'Provider worker preflight tests passed: 5 groups (including peak quota and reversible profiles)'
