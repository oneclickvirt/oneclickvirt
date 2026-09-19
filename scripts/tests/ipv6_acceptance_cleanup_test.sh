#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT
export TEST_EVENTS="$TEST_DIR/events"

# Execute the real cleanup shell against an isolated runtime double. This
# checks deletion ownership and remote failure propagation without a live VPS.
# shellcheck disable=SC1090
source <(sed -n '/^shell_quote() {/,/^on_exit() {/ { /^on_exit() {/d; p; }' \
    "$ROOT_DIR/scripts/tests/ipv6_external_acceptance_test.sh")
node_ssh() { bash -s; }
incus() {
    case "$1 $2" in
        'list --format')
            [[ "${FAIL_LIST:-false}" != true ]] || return 17
            printf '%s\n' "${LIST_NAMES:-}"
            ;;
        'config get') printf '%s\n' "$OWNER" ;;
        'delete -f')
            printf '%s\n' "$3" >> "$TEST_EVENTS"
            [[ "${FAIL_DELETE:-false}" != true ]] || return 23
            ;;
        *) return 99 ;;
    esac
}
export -f incus
export OWNER="run-'quoted'" LIST_NAMES='ocv-ipv6-test'
TEST_RUN_ID="$OWNER"
CONTAINER_NAME=ocv-ipv6-test
CONTAINER_CREATED=false
cleanup
[[ ! -e "$TEST_EVENTS" ]]
echo 'PASS: no deletion before confirmed creation'

# Read by cleanup, which is extracted from the acceptance script above.
# shellcheck disable=SC2034
CONTAINER_CREATED=true
cleanup
[[ "$(<"$TEST_EVENTS")" == "$CONTAINER_NAME" ]]
rm "$TEST_EVENTS"
echo 'PASS: matching owner, including shell punctuation, is removed'

export OWNER=another-run
if cleanup >/dev/null 2>&1; then
    echo 'FAIL: replaced container accepted for cleanup' >&2
    exit 1
fi
[[ ! -e "$TEST_EVENTS" ]]
echo 'PASS: replacement owner is preserved and reported'

export LIST_NAMES='ocv-ipv6-test-other'
cleanup
[[ ! -e "$TEST_EVENTS" ]]
echo 'PASS: missing instance is idempotent; similar name is preserved'

export FAIL_LIST=true
if cleanup >/dev/null 2>&1; then
    echo 'FAIL: unavailable runtime treated as successful cleanup' >&2
    exit 1
fi
[[ ! -e "$TEST_EVENTS" ]]
echo 'PASS: runtime query failure is reported without deletion'

export FAIL_LIST=false FAIL_DELETE=true LIST_NAMES="$CONTAINER_NAME" OWNER="$TEST_RUN_ID"
if cleanup >/dev/null 2>&1; then
    echo 'FAIL: deletion failure ignored' >&2
    exit 1
fi
[[ "$(<"$TEST_EVENTS")" == "$CONTAINER_NAME" ]]
echo 'PASS: deletion failure is reported'

echo 'IPv6 acceptance cleanup tests passed: 6'
