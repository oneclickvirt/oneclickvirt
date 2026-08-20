#!/bin/bash

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENTRYPOINT="${REPO_ROOT}/deploy/all-in-one-entrypoint.sh"
DOCKERFILE="${REPO_ROOT}/Dockerfile"
RUNTIME_OVERLAY_DOCKERFILE="${REPO_ROOT}/deploy/runtime-overlay.dockerfile"

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

test_password_resolution_and_sync_tracking() (
    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT

    export MYSQL_PASSWORD_FILE="${temp_dir}/mysql_root_password"
    # shellcheck source=/dev/null
    source "${ENTRYPOINT}"

    unset MYSQL_ROOT_PASSWORD
    load_database_password
    [[ ${#MYSQL_ROOT_PASSWORD} -eq 24 ]] || fail "generated password length is not 24"
    [[ "${DATABASE_PASSWORD_NEEDS_SYNC}" == "true" ]] || fail "new password must require database sync"
    persist_database_password

    local generated="${MYSQL_ROOT_PASSWORD}"
    unset MYSQL_ROOT_PASSWORD
    load_database_password
    [[ "${MYSQL_ROOT_PASSWORD}" == "${generated}" ]] || fail "persisted password was not reused"
    [[ "${DATABASE_PASSWORD_NEEDS_SYNC}" == "false" ]] || fail "unchanged persisted password should not require sync"

    MYSQL_ROOT_PASSWORD='Changed!Password,With%Quotes"And\Slash'
    load_database_password
    [[ "${DATABASE_PASSWORD_NEEDS_SYNC}" == "true" ]] || fail "changed explicit password must require sync"
)

test_sql_password_escaping() (
    # shellcheck source=/dev/null
    source "${ENTRYPOINT}"
    local escaped
    escaped="$(sql_escape_string "a'b\\c")"
    [[ "${escaped}" == "a''b\\\\c" ]] || fail "unexpected SQL escaping: ${escaped}"
)

test_existing_database_directory_is_preserved() (
    local temp_dir sentinel
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT
    export MYSQL_DATA_DIR="${temp_dir}/mysql-data"
    mkdir -p "${MYSQL_DATA_DIR}/mysql"
    sentinel="${MYSQL_DATA_DIR}/important-user-data"
    printf 'keep' >"${sentinel}"

    # shellcheck source=/dev/null
    source "${ENTRYPOINT}"
    initialize_data_directory_if_needed
    [[ -f "${sentinel}" ]] || fail "existing database data was removed"
    [[ "${DATA_DIRECTORY_INITIALIZED}" == "false" ]] || fail "existing database was marked as newly initialized"
)

test_dockerfile_installs_runtime_entrypoint() {
    grep -Fq 'COPY deploy/all-in-one-entrypoint.sh /start.sh' "${DOCKERFILE}" \
        || fail "Dockerfile does not install the all-in-one entrypoint"
    grep -Fq '!deploy/all-in-one-entrypoint.sh' "${REPO_ROOT}/.dockerignore" \
        || fail ".dockerignore excludes the all-in-one entrypoint"
}

test_dockerfile_blocks_dotfiles() {
    grep -Fq 'location ~ /\.(?!well-known(?:/|$)) {' "${DOCKERFILE}" \
        || fail "Dockerfile does not block dotfile requests before SPA fallback"
    grep -Fq "deny all;" "${DOCKERFILE}" \
        || fail "Dockerfile does not deny dotfile requests"
}

test_dockerfile_defers_database_service_start() {
    grep -Fq '/usr/sbin/policy-rc.d' "${DOCKERFILE}" \
        || fail "Dockerfile does not defer database service startup during image build"
}

test_runtime_overlay_is_available() {
    grep -Fq 'FROM ${BASE_IMAGE} AS runtime' "${RUNTIME_OVERLAY_DOCKERFILE}" \
        || fail "runtime overlay does not preserve the existing all-in-one base image"
    grep -Fq 'COPY deploy/all-in-one-nginx.conf /etc/nginx/nginx.conf' "${RUNTIME_OVERLAY_DOCKERFILE}" \
        || fail "runtime overlay does not install the hardened nginx configuration"
    grep -Fq 'ARG BUILD_COMMIT=runtime-overlay' "${RUNTIME_OVERLAY_DOCKERFILE}" \
        || fail "runtime overlay does not expose a build commit marker"
    if grep -Fq 'CompatibleAgentVersion = "${SERVER_VERSION}"' "${RUNTIME_OVERLAY_DOCKERFILE}"; then
        fail "runtime overlay must not replace the Agent compatibility version with a build label"
    fi
    grep -Fq '!deploy/all-in-one-nginx.conf' "${REPO_ROOT}/.dockerignore" \
        || fail ".dockerignore excludes the runtime overlay nginx configuration"
}

test_password_resolution_and_sync_tracking
test_sql_password_escaping
test_existing_database_directory_is_preserved
test_dockerfile_installs_runtime_entrypoint
test_dockerfile_blocks_dotfiles
test_dockerfile_defers_database_service_start
test_runtime_overlay_is_available

echo "all-in-one entrypoint tests passed"
