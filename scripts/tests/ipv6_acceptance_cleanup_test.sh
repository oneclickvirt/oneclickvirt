#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
TEST_DIR="$(mktemp -d)"
trap 'rm -rf "$TEST_DIR"' EXIT
export TEST_EVENTS="$TEST_DIR/events"

# Execute the real cleanup shell with local SSH doubles. Acceptance containers
# stay available for inspection; only temporary probe host-key files are removed.
# shellcheck disable=SC1090
source <(sed -n '/^probe_exec() {/,/^trap on_exit EXIT/ { /^trap on_exit EXIT/d; p; }' \
    "$ROOT_DIR/scripts/tests/ipv6_external_acceptance_test.sh")
node_ssh() {
    printf '%s\n' node >> "$TEST_EVENTS"
    return 99
}
probe_ssh() {
    printf '%s\n' probe >> "$TEST_EVENTS"
    [[ "${FAIL_PROBE:-false}" != true ]] || return 17
    bash -c "$1"
}

CONTAINER_NAME=ocv-ipv6-test
CONTAINER_CREATED=false
PROBE_KNOWN_HOSTS=""
: > "$TEST_EVENTS"
output="$(cleanup 2>&1)"
[[ ! -s "$TEST_EVENTS" && -z "$output" ]]
echo 'PASS: no node access before confirmed creation'

# Read by cleanup, which is extracted from the acceptance script above.
# shellcheck disable=SC2034
CONTAINER_CREATED=true
output="$(cleanup 2>&1)"
[[ ! -s "$TEST_EVENTS" && "$output" == "Preserving IPv6 acceptance container: $CONTAINER_NAME" ]]
echo 'PASS: confirmed container is preserved without node access'

CONTAINER_CREATED=false
PROBE_KNOWN_HOSTS="$TEST_DIR/probe hosts-'quoted'"
printf '%s\n' key > "$PROBE_KNOWN_HOSTS"
: > "$TEST_EVENTS"
output="$(cleanup 2>&1)"
[[ ! -e "$PROBE_KNOWN_HOSTS" && "$(<"$TEST_EVENTS")" == probe && -z "$output" ]]
echo 'PASS: temporary probe file with spaces and quotes is removed'

# Read by the sourced cleanup function.
# shellcheck disable=SC2034
CONTAINER_CREATED=true
: > "$TEST_EVENTS"
output="$(cleanup 2>&1)"
[[ ! -e "$PROBE_KNOWN_HOSTS" && "$(<"$TEST_EVENTS")" == probe ]]
[[ "$output" == "Preserving IPv6 acceptance container: $CONTAINER_NAME" ]]
echo 'PASS: missing probe file is idempotent and container stays preserved'

FAIL_PROBE=true
printf '%s\n' key > "$PROBE_KNOWN_HOSTS"
: > "$TEST_EVENTS"
output="$(cleanup 2>&1)"
[[ -e "$PROBE_KNOWN_HOSTS" && "$(<"$TEST_EVENTS")" == probe ]]
[[ "$output" == "Preserving IPv6 acceptance container: $CONTAINER_NAME" ]]
echo 'PASS: unavailable probe never causes node access or container deletion'

: > "$TEST_EVENTS"
if (trap on_exit EXIT; exit 42) > "$TEST_DIR/exit-output" 2>&1; then
    echo 'FAIL: failed acceptance became successful during cleanup' >&2
    exit 1
else
    status=$?
fi
[[ "$status" == 42 && -e "$PROBE_KNOWN_HOSTS" && "$(<"$TEST_EVENTS")" == probe ]]
[[ "$(<"$TEST_DIR/exit-output")" == "Preserving IPv6 acceptance container: $CONTAINER_NAME" ]]
echo 'PASS: acceptance failure status survives unavailable probe cleanup'

echo 'IPv6 acceptance cleanup tests passed: 6'
