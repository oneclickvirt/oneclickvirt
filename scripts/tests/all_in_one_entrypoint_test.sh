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
    [[ "${escaped}" == "a''b\\c" ]] || fail "unexpected SQL escaping: ${escaped}"
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

test_dockerfile_preserves_agent_compatibility() {
    if grep -Fq 'CompatibleAgentVersion' "${DOCKERFILE}"; then
        fail "Dockerfile must not rewrite the Agent compatibility version with a build label"
    fi
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

test_incomplete_data_is_not_deleted() (
    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT
    export MYSQL_DATA_DIR="${temp_dir}"
    source "${ENTRYPOINT}"
    EMBEDDED_DB_TYPE=mysql
    printf 'keep' >"${temp_dir}/ibdata1"
    if initialize_data_directory_if_needed; then fail "nonempty incomplete data was reinitialized"; fi
    [[ "$(<"${temp_dir}/ibdata1")" == keep ]] || fail "incomplete data was removed"
    mkdir "${temp_dir}/mysql"
    touch "${temp_dir}/aria_log_control"
    if initialize_data_directory_if_needed; then fail "MySQL accepted MariaDB data"; fi
)

test_readiness_requires_authentication() (
    source "${ENTRYPOINT}"
    MYSQL_ROOT_PASSWORD=disposable-test-password
    mysqladmin() { return 0; }
    mysql() { return 1; }
    if database_password_works; then fail "mysqladmin ping false-positive accepted"; fi
)

test_runtime_config_repairs_only_tuning() (
    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT
    export MYSQL_CONFIG_FILE="${temp_dir}/original.cnf" MYSQL_RUNTIME_CONFIG="${temp_dir}/runtime.cnf"
    source "${ENTRYPOINT}"
    printf '%s\n' '[mysqld]' 'innodb_redo_log_capacity=256M' 'query_cache_type=0' 'require_secure_transport=ON' 'bind-address=127.0.0.1' >"${MYSQL_CONFIG_FILE}"
    DB_DAEMON=runtime_daemon_stub
    runtime_daemon_stub() { return 0; }
    chown() { :; }
    prepare_database_config
    grep -Fxq 'loose-innodb_redo_log_capacity=256M' "${MYSQL_RUNTIME_CONFIG}" || fail "redo option was not made version-safe"
    grep -Fxq 'require_secure_transport=ON' "${MYSQL_RUNTIME_CONFIG}" || fail "TLS requirement was weakened"
    grep -Fxq 'bind-address=127.0.0.1' "${MYSQL_RUNTIME_CONFIG}" || fail "listener policy was changed"
    grep -Fxq 'innodb_redo_log_capacity=256M' "${MYSQL_CONFIG_FILE}" || fail "source config was modified"
)

test_runtime_config_adapts_engine_specific_options() (
    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT
    export MYSQL_CONFIG_FILE="${temp_dir}/original.cnf" MYSQL_RUNTIME_CONFIG="${temp_dir}/runtime.cnf"
    source "${ENTRYPOINT}"
    printf '%s\n' '[mysqld]' 'loose-innodb_redo_log_capacity=128M' 'loose-query_cache_type=0' 'loose-query_cache_size=0' >"${MYSQL_CONFIG_FILE}"
    DB_DAEMON=runtime_daemon_stub
    runtime_daemon_stub() { return 0; }
    chown() { :; }

    EMBEDDED_DB_TYPE=mariadb
    prepare_database_config
    grep -Fxq 'innodb_log_file_size=128M' "${MYSQL_RUNTIME_CONFIG}" || fail "MariaDB redo option was not adapted"
    grep -Fxq 'query_cache_type=0' "${MYSQL_RUNTIME_CONFIG}" || fail "MariaDB query cache option was not restored"

    EMBEDDED_DB_TYPE=mysql
    prepare_database_config
    grep -Fxq 'loose-query_cache_type=0' "${MYSQL_RUNTIME_CONFIG}" || fail "MySQL query cache option was not made harmless"

    # A MariaDB-only redo spelling must be translated in the other direction
    # when the actual daemon is MySQL. Keep the legacy spelling loose so an
    # older MySQL can still use it, while current MySQL consumes the modern
    # redo-capacity option.
    printf '%s\n' '[mysqld]' 'innodb_log_file_size=128M' >"${MYSQL_CONFIG_FILE}"
    prepare_database_config
    grep -Fxq 'loose-innodb_log_file_size=128M' "${MYSQL_RUNTIME_CONFIG}" || fail "MySQL fallback redo option was not preserved"
    grep -Fxq 'loose-innodb_redo_log_capacity=128M' "${MYSQL_RUNTIME_CONFIG}" || fail "MariaDB redo option was not translated for MySQL"
)

test_runtime_config_forces_lifecycle_paths() (
    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT
    export MYSQL_CONFIG_FILE="${temp_dir}/original.cnf" MYSQL_RUNTIME_CONFIG="${temp_dir}/runtime.cnf"
    export MYSQL_DATA_DIR="${temp_dir}/data" MYSQL_SOCKET="${temp_dir}/run/mysql.sock"
    export MYSQL_PID_FILE="${temp_dir}/run/mysql.pid" MYSQL_LOG_FILE="${temp_dir}/log/mysql.log" MYSQL_PORT=3307
    source "${ENTRYPOINT}"
    printf '%s\n' '[mysqld]' 'datadir=/wrong/data' 'socket=/wrong/socket' 'port=3306' >"${MYSQL_CONFIG_FILE}"
    DB_DAEMON=runtime_daemon_stub
    runtime_daemon_stub() { return 0; }
    chown() { :; }
    prepare_database_config
    grep -Fxq "datadir=${MYSQL_DATA_DIR}" "${MYSQL_RUNTIME_CONFIG}" || fail "runtime datadir was not enforced"
    grep -Fxq "socket=${MYSQL_SOCKET}" "${MYSQL_RUNTIME_CONFIG}" || fail "runtime socket was not enforced"
    grep -Fxq "port=${MYSQL_PORT}" "${MYSQL_RUNTIME_CONFIG}" || fail "runtime port was not enforced"
    grep -Fxq 'datadir=/wrong/data' "${MYSQL_RUNTIME_CONFIG}" || fail "source tuning was unexpectedly removed"
)

test_password_resolution_and_sync_tracking
test_sql_password_escaping
test_existing_database_directory_is_preserved
test_dockerfile_installs_runtime_entrypoint
test_dockerfile_blocks_dotfiles
test_dockerfile_defers_database_service_start
test_dockerfile_preserves_agent_compatibility
test_runtime_overlay_is_available
test_incomplete_data_is_not_deleted
test_readiness_requires_authentication
test_runtime_config_repairs_only_tuning
test_runtime_config_adapts_engine_specific_options
test_runtime_config_forces_lifecycle_paths

echo "all-in-one entrypoint tests passed"
