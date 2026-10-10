#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
# shellcheck disable=SC1091
source "$ROOT_DIR/action_tests/common/test_framework.sh"
# shellcheck disable=SC1091
source "$ROOT_DIR/action_tests/common/platform_interface.sh"
fail() { echo "Platform SSH credentials failed: $*" >&2; exit 1; }
mktemp() {
    if [[ "${1:-}" == /tmp/platform_ssh_key_XXXXXX.pem ]]; then
        command mktemp "$fixture/key.XXXXXX"
    else
        command mktemp "$@"
    fi
}
alice_get_permissions() { printf '%s\n200\n' '{"data":{"allow_packages":"38"}}'; }
_lightnode_auto_detect_region() { :; }
_lightnode_get_image_uuid() { printf '%s\n' fixture-image; }
export ALICE_CLIENT_ID=fixture ALICE_CLIENT_SECRET=fixture ALICE_PRIVATE_KEY=fixture
export LIGHTNODE_TOKEN=fixture LIGHTNODE_PASSWORD='fixture-password' LIGHTNODE_PRIVATE_KEY=''
platform_init alice > "$fixture/result" 2> "$fixture/diagnostic" || fail 'Alice fixture init failed'
[[ -f "$PLATFORM_SSH_KEY_FILE" ]] || fail 'Alice key was not initialized'
platform_init lightnode > "$fixture/result" 2> "$fixture/diagnostic" || fail 'LightNode fixture init failed'
[[ -z "$PLATFORM_SSH_KEY_FILE" && "$PLATFORM_SSH_PASSWORD" == "$LIGHTNODE_PASSWORD" ]] || fail 'Alice key leaked into password-only LightNode'
MOCK_AUTH=password MOCK_SSH_FAIL=false
ssh() {
    printf '%s\n' "$*" >> "$fixture/ssh"
    if [[ "$MOCK_SSH_FAIL" == true ]]; then
        printf 'Permission denied: %s\n' "${SSHPASS:-}" >&2
        return 255
    fi
    if [[ "$MOCK_AUTH" == password ]]; then
        [[ "$*" != *'-i '* && "${SSHPASS:-}" == "$LIGHTNODE_PASSWORD" ]] || return 255
    else
        [[ "$*" == *'IdentitiesOnly=yes'* && "$*" == *"$PLATFORM_SSH_KEY_FILE"* ]] || return 255
    fi
    printf '%s\n' ok
}
sshpass() {
    printf '%s\n' password >> "$fixture/auth"
    [[ "$1" == -e && "${SSHPASS:-}" == "$LIGHTNODE_PASSWORD" ]] || return 1
    shift
    "$@"
}
timeout() { shift; "$@"; }
lightnode_platform_wait_ssh 192.0.2.10 1 1 > "$fixture/result" 2> "$fixture/diagnostic" || fail 'password wait used another platform key'
lightnode_platform_ssh_exec 192.0.2.10 'echo fixture' 1 > "$fixture/result" 2> "$fixture/diagnostic" || fail 'password exec used another platform key'
[[ "$(wc -l < "$fixture/auth" | tr -d ' ')" == 2 ]] || fail 'wait and exec did not both use password authentication'
echo 'PASS: Alice-to-LightNode fallback isolates credentials for wait and exec'

LIGHTNODE_PRIVATE_KEY='fixture-key'
platform_init lightnode > "$fixture/result" 2> "$fixture/diagnostic" || fail 'key-mode reinit failed'
MOCK_AUTH=key
lightnode_platform_wait_ssh 192.0.2.10 1 1 > "$fixture/result" 2> "$fixture/diagnostic" || fail 'own key was not used'
lightnode_platform_ssh_exec 192.0.2.10 'echo fixture' 1 > "$fixture/result" 2> "$fixture/diagnostic" || fail 'key exec was not isolated from agent identities'
LIGHTNODE_PRIVATE_KEY=''
platform_init lightnode > "$fixture/result" 2> "$fixture/diagnostic" || fail 'password-mode reinit failed'
[[ -z "$PLATFORM_SSH_KEY_FILE" ]] || fail 'same-provider reinit retained an old key'
failed_platform_init() { PLATFORM_SSH_KEY_FILE=partial; PLATFORM_SSH_PASSWORD=partial; return 1; }
if platform_init failed > "$fixture/result" 2> "$fixture/diagnostic"; then fail 'failed init was accepted'; fi
[[ -z "$PLATFORM_SSH_KEY_FILE" && -z "$PLATFORM_SSH_PASSWORD" && -z "$ACTIVE_PLATFORM" ]] || fail 'failed initialization left usable stale state'
echo 'PASS: own-key mode, repeated init and failed init keep credential state isolated'

MOCK_TIME=0
date() { if [[ "$1" == +%s ]]; then printf '%s\n' "$MOCK_TIME"; else printf '%s\n' '00:00:00'; fi; }
sleep() { MOCK_TIME=$((MOCK_TIME + $1)); }
if lightnode_platform_wait_ssh 192.0.2.10 60 1 > "$fixture/result" 2> "$fixture/diagnostic"; then fail 'missing credentials succeeded'; fi
[[ "$MOCK_TIME" == 0 ]] || fail 'missing credentials waited for a full timeout'
MOCK_AUTH=password MOCK_SSH_FAIL=true
export DEBUG=1
PLATFORM_SSH_PASSWORD="$LIGHTNODE_PASSWORD"
if lightnode_platform_wait_ssh 192.0.2.10 3 1 > "$fixture/result" 2> "$fixture/diagnostic"; then fail 'SSH authentication failure succeeded'; fi
[[ "$MOCK_TIME" == 3 ]] || fail 'SSH wait exceeded its polling budget'
[[ "$(grep -c 'SSH not ready' "$fixture/diagnostic")" == 1 ]] || fail 'unchanged SSH errors were logged on every poll'
grep -q 'last exit=255' "$fixture/diagnostic" || fail 'timeout lost the actual SSH exit code'
grep -q 'Permission denied' "$fixture/diagnostic" || fail 'timeout lost the actual SSH diagnostic'
if grep -q -F "$LIGHTNODE_PASSWORD" "$fixture/diagnostic"; then fail 'SSH diagnostics leaked a credential'; fi
echo 'PASS: missing credentials fail promptly and SSH failures are bounded, informative and redacted'

echo 'Platform SSH credential tests passed: 3 groups'
