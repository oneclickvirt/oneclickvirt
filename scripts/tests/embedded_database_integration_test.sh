#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/tests/docker_fixture.sh"
FIXTURE_OWNER="ocv-embedded-$$-${RANDOM}${RANDOM}"
ACTIVE_CONTAINER=""
cleanup() {
    local exit_code=$? cleanup_status=0
    if [[ -n "$ACTIVE_CONTAINER" ]]; then
        remove_owned_docker_fixture container "$ACTIVE_CONTAINER" "$FIXTURE_OWNER" || cleanup_status=1
    fi
    if [[ "$exit_code" == "0" && "$cleanup_status" != "0" ]]; then
        exit_code=1
    fi
    return "$exit_code"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
for image in ${OCV_TEST_DB_IMAGES:-mariadb:10.11 mysql:8.4}; do
    echo "Testing actual embedded initialization functions on $image"
    ACTIVE_CONTAINER="${FIXTURE_OWNER}-${image//[:.]/-}"
    docker run --rm --name "$ACTIVE_CONTAINER" --label "ocv.test.run=$FIXTURE_OWNER" \
        --memory=1536m --cpus=2 --tmpfs /var/lib/mysql:rw,size=1g \
        --entrypoint bash -v "$ROOT_DIR:/ocv-test:ro" "$image" \
        /ocv-test/scripts/tests/embedded_database_live_test.sh
    ACTIVE_CONTAINER=""
done
