#!/usr/bin/env bash
set -euo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
source "$ROOT_DIR/scripts/tests/docker_fixture.sh"
fail() { printf 'Docker fixture cleanup failed: %s\n' "$*" >&2; exit 1; }
docker() {
    printf '%s\n' "$*" >> "$fixture/calls"
    if [[ "$2" == inspect ]]; then
        [[ "$MOCK_INSPECT" == true ]] || return 1
        if [[ "$4" == *'{{.Name}}'* || "$4" == *'{{.Id}}'* ]]; then
            printf '%s %s\n' "$MOCK_OWNER" "$MOCK_ID"
        else
            printf '%s\n' "$MOCK_OWNER"
        fi
    else
        [[ "$MOCK_DELETE" == true ]]
    fi
}
MOCK_INSPECT=true MOCK_DELETE=true MOCK_OWNER=other-run MOCK_ID=immutable-id
for kind in container network volume; do
    : > "$fixture/calls"
    remove_owned_docker_fixture "$kind" reused-name this-run >/dev/null 2>&1
    ! grep -q ' rm ' "$fixture/calls" || fail 'a creation collision removed an older fixture'
    MOCK_INSPECT=false
    remove_owned_docker_fixture "$kind" reused-name this-run
    ! grep -q ' rm ' "$fixture/calls" || fail 'inspection failure allowed deletion'
    MOCK_INSPECT=true MOCK_OWNER=this-run
    remove_owned_docker_fixture "$kind" reused-name this-run
    case "$kind" in
        container) expected='container rm -f -v immutable-id' ;;
        network) expected='network rm immutable-id' ;;
        volume) expected='volume rm immutable-id' ;;
    esac
    grep -Fxq "$expected" "$fixture/calls" || fail 'cleanup used a mutable container/network name'
    MOCK_DELETE=false
    if remove_owned_docker_fixture "$kind" reused-name this-run; then fail 'cleanup failure was swallowed'; fi
    MOCK_DELETE=true MOCK_OWNER=other-run
done
if create_owned_docker_volume reused-volume this-run >/dev/null 2>&1; then fail 'volume collision allowed another run to overwrite data'; fi
MOCK_OWNER=this-run
create_owned_docker_volume fresh-volume this-run
MOCK_INSPECT=false
if create_owned_docker_volume unknown-volume this-run >/dev/null 2>&1; then fail 'an unresolved volume was used'; fi
echo 'PASS: fixture collisions, failed inspection, immutable identities and failed deletion'
