#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
for image in ${OCV_TEST_DB_IMAGES:-mariadb:10.11 mysql:8.4}; do
    echo "Testing actual embedded initialization functions on $image"
    docker run --rm --memory=1536m --cpus=2 --tmpfs /var/lib/mysql:rw,size=1g \
        --entrypoint bash -v "$ROOT_DIR:/ocv-test:ro" "$image" \
        /ocv-test/scripts/tests/embedded_database_live_test.sh
done
