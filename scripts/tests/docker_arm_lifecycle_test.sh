#!/usr/bin/env bash

set -Eeuo pipefail

ALLINONE_IMAGE="${ALLINONE_IMAGE:-oneclickvirt-ci:arm64-allinone}"
NO_DB_IMAGE="${NO_DB_IMAGE:-oneclickvirt-ci:arm64-no-db}"
ALLINONE_BASE_IMAGE="${ALLINONE_BASE_IMAGE:-${ALLINONE_IMAGE}}"
NO_DB_BASE_IMAGE="${NO_DB_BASE_IMAGE:-${NO_DB_IMAGE}}"
EXPECTED_ARCH="${EXPECTED_ARCH:-arm64}"
WAIT_TIMEOUT="${WAIT_TIMEOUT:-240}"

RUN_ID="${GITHUB_RUN_ID:-local}-$$"
RUN_ID="${RUN_ID//[^a-zA-Z0-9_.-]/-}"
NETWORK="ocv-arm-lifecycle-${RUN_ID}"
DB_CONTAINER="ocv-arm-db-${RUN_ID}"
NO_DB_CONTAINER="ocv-arm-nodb-${RUN_ID}"
ALLINONE_CONTAINER="ocv-arm-allinone-${RUN_ID}"
NO_DB_VOLUME="ocv-arm-nodb-storage-${RUN_ID}"
ALLINONE_DB_VOLUME="ocv-arm-allinone-db-${RUN_ID}"
ALLINONE_STORAGE_VOLUME="ocv-arm-allinone-storage-${RUN_ID}"
# Include Supervisor-sensitive punctuation so the all-in-one image proves that
# database credentials are inherited safely instead of interpolated into INI.
DB_PASSWORD='ArmLifecycle!Db12,@%Q'

cleanup() {
    docker rm -f \
        "${NO_DB_CONTAINER}" \
        "${ALLINONE_CONTAINER}" \
        "${DB_CONTAINER}" >/dev/null 2>&1 || true
    docker network rm "${NETWORK}" >/dev/null 2>&1 || true
    docker volume rm \
        "${NO_DB_VOLUME}" \
        "${ALLINONE_DB_VOLUME}" \
        "${ALLINONE_STORAGE_VOLUME}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

fail() {
    echo "ERROR: $*" >&2
    docker ps -a --filter "name=${RUN_ID}" >&2 || true
    for container in "${NO_DB_CONTAINER}" "${ALLINONE_CONTAINER}" "${DB_CONTAINER}"; do
        if docker inspect "${container}" >/dev/null 2>&1; then
            echo "=== ${container} logs ===" >&2
            docker logs --tail 150 "${container}" >&2 || true
        fi
    done
    exit 1
}

assert_image_arch() {
    local image="$1" arch
    arch="$(docker image inspect --format '{{.Architecture}}' "${image}")"
    [[ "${arch}" == "${EXPECTED_ARCH}" ]] || fail "${image} architecture is ${arch}, expected ${EXPECTED_ARCH}"
}

published_port() {
    docker port "$1" 80/tcp | awk -F: 'NR == 1 {print $NF}'
}

wait_http() {
    local container="$1" path="$2" deadline port code
    deadline=$((SECONDS + WAIT_TIMEOUT))
    while (( SECONDS < deadline )); do
        if ! docker inspect "${container}" >/dev/null 2>&1; then
            fail "container ${container} disappeared while waiting for ${path}"
        fi
        if [[ "$(docker inspect --format '{{.State.Running}}' "${container}")" != "true" ]]; then
            fail "container ${container} stopped while waiting for ${path}"
        fi
        port="$(published_port "${container}")"
        code="$(curl -sS -o /dev/null -w '%{http_code}' --max-time 5 "http://127.0.0.1:${port}${path}" 2>/dev/null || true)"
        if [[ "${code}" == "200" ]]; then
            return 0
        fi
        sleep 2
    done
    fail "${container} did not return HTTP 200 for ${path} within ${WAIT_TIMEOUT}s"
}

wait_database() {
    local deadline
    deadline=$((SECONDS + WAIT_TIMEOUT))
    while (( SECONDS < deadline )); do
        if docker exec "${DB_CONTAINER}" mariadb-admin ping -uroot -p"${DB_PASSWORD}" --silent >/dev/null 2>&1; then
            return 0
        fi
        sleep 2
    done
    fail "external MariaDB did not become ready within ${WAIT_TIMEOUT}s"
}

assert_persisted_no_db_connection() {
    local expected
    for expected in \
        'db-type: mariadb' \
        'path: ocv-db' \
        'port: "3306"' \
        'db-name: oneclickvirt' \
        'username: root'; do
        docker exec "${NO_DB_CONTAINER}" grep -Fq "${expected}" /app/storage/config.yaml \
            || fail "persisted no-db config lost expected value: ${expected}"
    done
    [[ "$(docker exec "${NO_DB_CONTAINER}" readlink -f /app/config.yaml)" == "/app/storage/config.yaml" ]] \
        || fail "no-db active config is not linked to persistent storage"
}

start_no_db_container() {
    local image="$1" env_mode="${2:-blank}"
    local env_args=(
        -e DB_HOST=
        -e DB_PORT=
        -e DB_NAME=
        -e DB_USER=
        -e DB_PASSWORD=
        -e DB_TYPE=
    )
    if [[ "${env_mode}" == "literal-quotes" ]]; then
        # Some panels persist the quote characters themselves. The runtime must
        # normalize these values instead of producing a tcp/"3306" DSN.
        env_args=(
            -e "DB_HOST='ocv-db'"
            -e 'DB_PORT="3306"'
            -e 'DB_NAME="oneclickvirt"'
            -e "DB_USER='root'"
            -e "DB_PASSWORD=${DB_PASSWORD}"
            -e 'DB_TYPE="mariadb"'
        )
    fi
    docker run -d \
        --name "${NO_DB_CONTAINER}" \
        --network "${NETWORK}" \
        -p 127.0.0.1::80 \
        -v "${NO_DB_VOLUME}:/app/storage" \
        "${env_args[@]}" \
        "${image}" >/dev/null
}

test_no_db_restart_and_upgrade() {
    echo "Testing ARM64 no-db restart and image replacement with blank DB_* environment values..."
    docker volume create "${NO_DB_VOLUME}" >/dev/null

    docker run --rm -v "${NO_DB_VOLUME}:/storage" "${NO_DB_BASE_IMAGE}" bash -Eeuo pipefail -c '
        cp /app/config.yaml.default /storage/config.yaml
        sed -i "/^system:/,/^[^[:space:]]/ s/^[[:space:]]*db-type:.*/    db-type: mariadb/" /storage/config.yaml
        sed -i "/^mysql:/,/^[^[:space:]]/ s/^[[:space:]]*path:.*/    path: ocv-db/" /storage/config.yaml
        sed -i "/^mysql:/,/^[^[:space:]]/ s/^[[:space:]]*port:.*/    port: \"3306\"/" /storage/config.yaml
        sed -i "/^mysql:/,/^[^[:space:]]/ s/^[[:space:]]*db-name:.*/    db-name: oneclickvirt/" /storage/config.yaml
        sed -i "/^mysql:/,/^[^[:space:]]/ s/^[[:space:]]*username:.*/    username: root/" /storage/config.yaml
        sed -i "/^mysql:/,/^[^[:space:]]/ s/^[[:space:]]*password:.*/    password: ArmLifecycleDb12345/" /storage/config.yaml
        chmod 600 /storage/config.yaml
    '

    start_no_db_container "${NO_DB_BASE_IMAGE}"
    wait_http "${NO_DB_CONTAINER}" /api/v1/health
    assert_persisted_no_db_connection

    docker restart "${NO_DB_CONTAINER}" >/dev/null
    wait_http "${NO_DB_CONTAINER}" /api/v1/health
    assert_persisted_no_db_connection

    docker rm -f "${NO_DB_CONTAINER}" >/dev/null
    start_no_db_container "${NO_DB_IMAGE}" literal-quotes
    wait_http "${NO_DB_CONTAINER}" /api/v1/health
    assert_persisted_no_db_connection
}

start_allinone_container() {
    local image="$1"
    docker run -d \
        --name "${ALLINONE_CONTAINER}" \
        -p 127.0.0.1::80 \
        -e MYSQL_ROOT_PASSWORD="${DB_PASSWORD}" \
        -e MYSQL_DATABASE=oneclickvirt \
        -v "${ALLINONE_DB_VOLUME}:/var/lib/mysql" \
        -v "${ALLINONE_STORAGE_VOLUME}:/app/storage" \
        "${image}" >/dev/null
}

assert_allinone_marker() {
    local value
    value="$(docker exec "${ALLINONE_CONTAINER}" mariadb -h127.0.0.1 -uroot -p"${DB_PASSWORD}" -Nse \
        'SELECT marker FROM oneclickvirt.arm_lifecycle_guard WHERE id = 1' 2>/dev/null || true)"
    [[ "${value}" == "persisted" ]] || fail "all-in-one database marker did not survive lifecycle operation"
    [[ "$(docker exec "${ALLINONE_CONTAINER}" cat /app/storage/arm-lifecycle.marker 2>/dev/null || true)" == "persisted" ]] \
        || fail "all-in-one storage marker did not survive lifecycle operation"
}

test_allinone_restart_and_upgrade() {
    echo "Testing ARM64 all-in-one restart and image replacement with persistent database/storage volumes..."
    docker volume create "${ALLINONE_DB_VOLUME}" >/dev/null
    docker volume create "${ALLINONE_STORAGE_VOLUME}" >/dev/null
    start_allinone_container "${ALLINONE_BASE_IMAGE}"
    wait_http "${ALLINONE_CONTAINER}" /api/v1/health

    docker exec "${ALLINONE_CONTAINER}" mariadb -h127.0.0.1 -uroot -p"${DB_PASSWORD}" -e \
        "CREATE TABLE IF NOT EXISTS oneclickvirt.arm_lifecycle_guard (id INT PRIMARY KEY, marker VARCHAR(32)); INSERT INTO oneclickvirt.arm_lifecycle_guard VALUES (1, 'persisted') ON DUPLICATE KEY UPDATE marker='persisted';"
    docker exec "${ALLINONE_CONTAINER}" sh -c 'printf persisted > /app/storage/arm-lifecycle.marker'
    assert_allinone_marker

    docker restart "${ALLINONE_CONTAINER}" >/dev/null
    wait_http "${ALLINONE_CONTAINER}" /api/v1/health
    assert_allinone_marker

    docker rm -f "${ALLINONE_CONTAINER}" >/dev/null
    start_allinone_container "${ALLINONE_IMAGE}"
    wait_http "${ALLINONE_CONTAINER}" /api/v1/health
    assert_allinone_marker
}

command -v docker >/dev/null 2>&1 || fail "docker is required"
command -v curl >/dev/null 2>&1 || fail "curl is required"
assert_image_arch "${ALLINONE_IMAGE}"
assert_image_arch "${NO_DB_IMAGE}"
assert_image_arch "${ALLINONE_BASE_IMAGE}"
assert_image_arch "${NO_DB_BASE_IMAGE}"

docker network create "${NETWORK}" >/dev/null
docker run -d \
    --name "${DB_CONTAINER}" \
    --network "${NETWORK}" \
    --network-alias ocv-db \
    -e MARIADB_ROOT_PASSWORD="${DB_PASSWORD}" \
    -e MARIADB_DATABASE=oneclickvirt \
    mariadb:10.11 >/dev/null
wait_database

test_no_db_restart_and_upgrade
test_allinone_restart_and_upgrade

echo "ARM64 Docker lifecycle tests passed"
