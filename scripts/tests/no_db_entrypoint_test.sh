#!/bin/bash

set -Eeuo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
ENTRYPOINT="${REPO_ROOT}/deploy/no-db-entrypoint.sh"
DEFAULT_CONFIG_SOURCE="${REPO_ROOT}/server/config.yaml"

assert_contains() {
    local file="$1"
    local expected="$2"
    if ! grep -Fq "${expected}" "${file}"; then
        echo "Expected ${file} to contain: ${expected}" >&2
        exit 1
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

test_persistent_config_survives_restart
test_explicit_config_mount_remains_authoritative
test_frontend_url_updates_persisted_config_and_proxy_scheme

echo "no-db entrypoint tests passed"
