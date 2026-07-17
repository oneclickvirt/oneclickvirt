#!/bin/bash

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENTRYPOINT="${REPO_ROOT}/deploy/no-db-entrypoint.sh"
DEFAULT_CONFIG_SOURCE="${REPO_ROOT}/server/config.yaml"
NO_DB_DOCKERFILE="${REPO_ROOT}/Dockerfile.no-db"
LIFECYCLE_CONFIG_PREPARER="${REPO_ROOT}/scripts/tests/prepare_no_db_lifecycle_config.sh"

assert_contains() {
    local file="$1"
    local expected="$2"
    if ! grep -Fq "${expected}" "${file}"; then
        echo "Expected ${file} to contain: ${expected}" >&2
        exit 1
    fi
}

assert_file_mode() {
    local file="$1" expected="$2" actual
    if actual="$(stat -c '%a' "${file}" 2>/dev/null)"; then
        :
    elif actual="$(stat -f '%Lp' "${file}" 2>/dev/null)"; then
        :
    else
        echo "Could not determine permissions for ${file}" >&2
        return 1
    fi
    if [[ "${actual}" != "${expected}" ]]; then
        echo "Expected ${file} permissions to be ${expected}, got ${actual}" >&2
        return 1
    fi
}

test_persistent_config_survives_restart() (
    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT

    export APP_DIR="${temp_dir}/app"
    export STORAGE_DIR="${APP_DIR}/storage"
    export DEFAULT_CONFIG="${APP_DIR}/config.yaml.default"
    export ACTIVE_CONFIG="${APP_DIR}/config.yaml"
    export PERSISTED_CONFIG="${STORAGE_DIR}/config.yaml"
    mkdir -p "${APP_DIR}"
    cp "${DEFAULT_CONFIG_SOURCE}" "${DEFAULT_CONFIG}"

    # shellcheck source=../../deploy/no-db-entrypoint.sh
    source "${ENTRYPOINT}"
    prepare_runtime_config
    [[ -L "${ACTIVE_CONFIG}" ]]
    assert_contains "${PERSISTED_CONFIG}" "db-name: oneclickvirt"

    printf '\n# retained-across-image-update\n' >> "${PERSISTED_CONFIG}"
    prepare_runtime_config
    assert_contains "${ACTIVE_CONFIG}" "# retained-across-image-update"
)

test_explicit_config_mount_remains_authoritative() (
    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT

    export APP_DIR="${temp_dir}/app"
    export STORAGE_DIR="${APP_DIR}/storage"
    export DEFAULT_CONFIG="${APP_DIR}/config.yaml.default"
    export ACTIVE_CONFIG="${APP_DIR}/config.yaml"
    export PERSISTED_CONFIG="${STORAGE_DIR}/config.yaml"
    mkdir -p "${APP_DIR}"
    cp "${DEFAULT_CONFIG_SOURCE}" "${DEFAULT_CONFIG}"
    printf 'system:\n    db-type: mariadb\n' > "${ACTIVE_CONFIG}"

    # shellcheck source=../../deploy/no-db-entrypoint.sh
    source "${ENTRYPOINT}"
    prepare_runtime_config
    [[ ! -L "${ACTIVE_CONFIG}" ]]
    assert_contains "${ACTIVE_CONFIG}" "db-type: mariadb"
)

test_frontend_url_updates_persisted_config_and_proxy_scheme() (
    local temp_dir
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT

    export APP_DIR="${temp_dir}/app"
    export STORAGE_DIR="${APP_DIR}/storage"
    export DEFAULT_CONFIG="${APP_DIR}/config.yaml.default"
    export ACTIVE_CONFIG="${APP_DIR}/config.yaml"
    export PERSISTED_CONFIG="${STORAGE_DIR}/config.yaml"
    export NGINX_CONFIG="${temp_dir}/nginx.conf"
    export FRONTEND_URL='https://virt.example.com/path?a=1&b=2'
    mkdir -p "${APP_DIR}"
    cp "${DEFAULT_CONFIG_SOURCE}" "${DEFAULT_CONFIG}"
    printf 'proxy_set_header X-Forwarded-Proto $scheme;\n' > "${NGINX_CONFIG}"

    # shellcheck source=../../deploy/no-db-entrypoint.sh
    source "${ENTRYPOINT}"
    prepare_runtime_config
    configure_frontend_url
    assert_contains "${PERSISTED_CONFIG}" 'frontend-url: "https://virt.example.com/path?a=1&b=2"'
    assert_contains "${NGINX_CONFIG}" 'proxy_set_header X-Forwarded-Proto https;'
)

test_healthcheck_runtime_dependencies_are_installed() {
    assert_contains "${NO_DB_DOCKERFILE}" "ca-certificates procps nginx supervisor wget"
    assert_contains "${NO_DB_DOCKERFILE}" "CMD wget --quiet"
}

test_lifecycle_config_uses_single_password_source() (
    local temp_dir source_config target_config missing_key_config test_password
    temp_dir="$(mktemp -d)"
    trap 'rm -rf "${temp_dir}"' EXIT
    source_config="${temp_dir}/source.yaml"
    target_config="${temp_dir}/target.yaml"
    test_password='Lifecycle!Db12,@%Q&|"quoted"\path'
    cp "${DEFAULT_CONFIG_SOURCE}" "${source_config}"

    TEST_DB_PASSWORD="${test_password}" bash "${LIFECYCLE_CONFIG_PREPARER}" \
        "${source_config}" "${target_config}"

    assert_contains "${target_config}" 'db-type: mariadb'
    assert_contains "${target_config}" 'path: ocv-db'
    assert_contains "${target_config}" 'port: "3306"'
    assert_contains "${target_config}" 'password: "Lifecycle!Db12,@%Q&|\"quoted\"\\path"'
    assert_file_mode "${target_config}" 600

    missing_key_config="${temp_dir}/missing-password.yaml"
    sed '/^[[:space:]]*password:/d' "${source_config}" >"${missing_key_config}"
    if TEST_DB_PASSWORD="${test_password}" bash "${LIFECYCLE_CONFIG_PREPARER}" \
        "${missing_key_config}" "${temp_dir}/should-not-succeed.yaml" 2>/dev/null; then
        echo "Lifecycle config preparation unexpectedly accepted a missing mysql.password key" >&2
        exit 1
    fi
)

test_persistent_config_survives_restart
test_explicit_config_mount_remains_authoritative
test_frontend_url_updates_persisted_config_and_proxy_scheme
test_healthcheck_runtime_dependencies_are_installed
test_lifecycle_config_uses_single_password_source

echo "no-db entrypoint tests passed"
