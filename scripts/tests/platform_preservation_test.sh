#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=../../action_tests/common/platform_interface.sh
unset SKIP_INSTANCE_DELETE
source "${ROOT_DIR}/action_tests/common/platform_interface.sh"

fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
calls="$fixture/calls"
fail() { printf 'platform preservation test failed: %s\n' "$*" >&2; exit 1; }
log_info() { :; }
log_success() { :; }
log_warning() { :; }
log_error() { :; }
log_debug() { :; }
[[ "${SKIP_INSTANCE_DELETE}" == "true" ]] || fail "instance preservation is not the default"
grep -Fq 'RAW_INSTANCE_TYPES="${3:-container}"' "${ROOT_DIR}/action_tests/run_env_test.sh" ||
    fail "environment harness still defaults to VM-inclusive instance types"
grep -Fq 'ACTION_TEST_IPV4_ONLY="${ACTION_TEST_IPV4_ONLY:-false}"' "${ROOT_DIR}/action_tests/run_env_test.sh" ||
    fail "environment harness does not include IPv6 by default"
get_enabled_platforms() { printf '%s\n' "${MOCK_PLATFORM:-lightnode}"; }
platform_init() { ACTIVE_PLATFORM="$1"; }
wait_for_ssh() {
    [[ "${MOCK_SSH_READY:-true}" == true ]] || return 1
    if [[ "${MOCK_EXPECT_PASSWORD:-false}" == true ]]; then
        [[ "$PLATFORM_SSH_PASSWORD" == "nested-worker-password" ]]
    fi
}
platform_validate_worker_resources() { [[ "${MOCK_RESOURCES_READY:-true}" == true ]]; }
platform_dispatch() {
    local platform="$1" action="$2"
    shift 2
    printf '%s %s %s\n' "$platform" "$action" "$*" >> "$calls"
    case "$action" in
        list_instances) printf '%s\n' "$MOCK_INVENTORY" ;;
        reinstall_instance)
            [[ "${MOCK_REINSTALL_READY:-true}" == true ]] || return 1
            jq -cn --arg id "$1" --arg password "${MOCK_RESULT_PASSWORD:-}" \
                '{instance_id:$id,ipv4:"192.0.2.2",password:$password}'
            ;;
        create_instance)
            jq -cn --arg password "${MOCK_RESULT_PASSWORD:-}" \
                '{instance_id:"new-id",ipv4:"192.0.2.3",password:$password}'
            ;;
        delete_instance) return 0 ;;
        *) fail "unexpected dispatch action $action" ;;
    esac
}
assert_no_delete_or_create() {
    ! grep -Eq ' (delete_instance|create_instance) ' "$calls" || fail "a preserved instance was deleted or replaced"
}

SKIP_INSTANCE_DELETE=true
INSTANCE_TYPES=container
MOCK_INVENTORY='[{"instance_id":"old-1"},{"instance_id":"old-2"}]'
: > "$calls"
if try_create_with_fallback lxd 8 > "$fixture/result"; then
    fail "ambiguous account inventory was accepted"
fi
assert_no_delete_or_create
! grep -q ' reinstall_instance ' "$calls" || fail "an arbitrary existing instance was reinstalled"

PLATFORM_REUSE_INSTANCE_ID=old-2
: > "$calls"
try_create_with_fallback lxd 8 > "$fixture/result" || fail "explicit reuse failed"
grep -q ' reinstall_instance old-2 ubuntu' "$calls" || fail "the selected LXD instance was not rebuilt with Ubuntu"
assert_no_delete_or_create
unset PLATFORM_REUSE_INSTANCE_ID

MOCK_PLATFORM=hetzner
MOCK_INVENTORY='[{"instance_id":"old-1"}]'
PLATFORM_ALLOW_CONCURRENT_INSTANCES=true
: > "$calls"
try_create_with_fallback docker 8 > "$fixture/result" || fail "Hetzner worker reuse failed in concurrent mode"
grep -q ' reinstall_instance old-1 debian' "$calls" || fail "Hetzner Docker worker was not rebuilt with Debian"
assert_no_delete_or_create
unset PLATFORM_ALLOW_CONCURRENT_INSTANCES MOCK_PLATFORM

# Nested runtimes validate resources through SSH before create_test_node can
# restore credentials in the outer shell. The adapter's $() boundary must not
# discard the rebuilt or newly created Worker's password at that point.
MOCK_PLATFORM=hetzner
MOCK_INVENTORY='[{"instance_id":"old-1"}]'
MOCK_RESULT_PASSWORD=nested-worker-password
MOCK_EXPECT_PASSWORD=true
PLATFORM_SSH_PASSWORD=""
try_create_with_fallback incus 8 > "$fixture/result" || fail "nested rebuild lost SSH credentials"
[[ "$PLATFORM_SSH_PASSWORD" == "$MOCK_RESULT_PASSWORD" ]] || fail "rebuilt password was not restored"
MOCK_INVENTORY='[]'
PLATFORM_SSH_PASSWORD=""
try_create_with_fallback incus 8 > "$fixture/result" || fail "nested creation lost SSH credentials"
[[ "$PLATFORM_SSH_PASSWORD" == "$MOCK_RESULT_PASSWORD" ]] || fail "created password was not restored"
unset MOCK_PLATFORM MOCK_RESULT_PASSWORD MOCK_EXPECT_PASSWORD

MOCK_PLATFORM=hetzner
MOCK_INVENTORY='[{"instance_id":"old-1","ipv4":"192.0.2.4"}]'
PLATFORM_REUSE_INSTANCE_ID=old-1
PLATFORM_REUSE_INSTANCE_PASSWORD='fixture-password'
PLATFORM_REUSE_EXISTING_AS_IS=true
: > "$calls"
try_create_with_fallback containerd 8 > "$fixture/result" || fail "explicit as-is reuse failed"
[[ $(jq -r '.instance_id + " " + .ipv4' "$fixture/result") == 'old-1 192.0.2.4' ]] || fail "as-is reuse selected the wrong worker"
! grep -q ' reinstall_instance ' "$calls" || fail "as-is reuse rebuilt the worker"
assert_no_delete_or_create
unset PLATFORM_REUSE_INSTANCE_PASSWORD
: > "$calls"
if try_create_with_fallback containerd 8 > "$fixture/result"; then
    fail "as-is reuse accepted a missing SSH password"
fi
! grep -Eq ' (reinstall_instance|create_instance|delete_instance) ' "$calls" || fail "invalid as-is reuse changed a worker"
unset PLATFORM_REUSE_INSTANCE_ID PLATFORM_REUSE_EXISTING_AS_IS MOCK_PLATFORM

MOCK_INVENTORY='[{"instance_id":"old-1"}]'
MOCK_REINSTALL_READY=false
: > "$calls"
if try_create_with_fallback lxd 8 > "$fixture/result"; then
    fail "failed reinstall was treated as success"
fi
assert_no_delete_or_create
MOCK_REINSTALL_READY=true

MOCK_INVENTORY='[]'
MOCK_SSH_READY=false
: > "$calls"
if try_create_with_fallback lxd 8 > "$fixture/result"; then
    fail "unreachable new worker was treated as success"
fi
[[ $(grep -c ' create_instance ' "$calls") -eq 1 ]] || fail "creation was retried after preserving an unavailable worker"
! grep -q ' delete_instance ' "$calls" || fail "an unavailable new worker was deleted"
MOCK_SSH_READY=true

# The LightNode adapter also has its own partial-create cleanup path. It must
# honor the same preservation setting before touching the release endpoint.
_lightnode_get_default_package() { printf 'third-tier\n'; }
_lightnode_get_image_uuid() { printf 'image-id\n'; }
_lightnode_wait_async_task() { return 1; }
lightnode_request() {
    printf '%s\n' "$2" >> "$calls"
    case "$2" in
        /instance/create) printf '%s\n200\n' '{"asyncTaskInfo":{"asyncTaskUUID":"task-id","ecsResourceUUID":"new-id"}}' ;;
        /instance/release) printf '%s\n200\n' '{}' ;;
        *) fail "unexpected LightNode request $2" ;;
    esac
}
LIGHTNODE_REGION=test-region
LIGHTNODE_ZONE=test-zone
LIGHTNODE_PASSWORD='TestPass123!'
: > "$calls"
if lightnode_platform_create_instance docker > "$fixture/result"; then
    fail "failed async task was treated as success"
fi
! grep -q '/instance/release' "$calls" || fail "partial LightNode instance was released despite preservation"

printf 'Platform preservation tests passed\n'
