#!/bin/bash

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENTRYPOINT="${REPO_ROOT}/deploy/all-in-one-entrypoint.sh"
DOCKERFILE="${REPO_ROOT}/Dockerfile"

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

test_password_resolution_and_sync_tracking
test_sql_password_escaping
test_existing_database_directory_is_preserved
test_dockerfile_installs_runtime_entrypoint

echo "all-in-one entrypoint tests passed"
