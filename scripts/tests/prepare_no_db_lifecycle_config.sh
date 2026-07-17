#!/bin/bash

set -Eeuo pipefail

SOURCE_CONFIG="${1:-/app/config.yaml.default}"
TARGET_CONFIG="${2:-/storage/config.yaml}"
TEST_DB_PASSWORD="${TEST_DB_PASSWORD:-}"

fail() {
    echo "ERROR: $*" >&2
    exit 1
}

yaml_double_quote() {
    local value="$1"
    [[ "$value" != *$'\n'* && "$value" != *$'\r'* ]] ||
        fail "test database password must not contain newlines"
    value="${value//\\/\\\\}"
    value="${value//\"/\\\"}"
    printf '"%s"' "$value"
}

escape_sed_replacement() {
    printf '%s' "$1" | sed 's/[\\&|]/\\&/g'
}

replace_config_value() {
    local section="$1" key="$2" value="$3" escaped_value temp_file match_count
    match_count="$(
        sed -n "/^${section}:/,/^[^[:space:]]/p" "${TARGET_CONFIG}" \
            | grep -Ec "^[[:space:]]*${key}:" || true
    )"
    [[ "${match_count}" == "1" ]] ||
        fail "expected exactly one ${section}.${key} entry in ${TARGET_CONFIG}, found ${match_count}"

    escaped_value="$(escape_sed_replacement "$value")"
    temp_file="$(mktemp "${TARGET_CONFIG}.tmp.XXXXXX")"
    if ! sed "/^${section}:/,/^[^[:space:]]/ s|^[[:space:]]*${key}:.*$|    ${key}: ${escaped_value}|" \
        "${TARGET_CONFIG}" >"${temp_file}"; then
        rm -f "${temp_file}"
        fail "could not update ${section}.${key} in ${TARGET_CONFIG}"
    fi
    mv "${temp_file}" "${TARGET_CONFIG}"
}

[[ -s "${SOURCE_CONFIG}" ]] || fail "source config is missing: ${SOURCE_CONFIG}"
[[ -n "${TEST_DB_PASSWORD}" ]] || fail "TEST_DB_PASSWORD must be non-empty"

mkdir -p "$(dirname "${TARGET_CONFIG}")"
cp "${SOURCE_CONFIG}" "${TARGET_CONFIG}"
replace_config_value system db-type mariadb
replace_config_value mysql path ocv-db
replace_config_value mysql port '"3306"'
replace_config_value mysql db-name oneclickvirt
replace_config_value mysql username root
replace_config_value mysql password "$(yaml_double_quote "${TEST_DB_PASSWORD}")"
chmod 600 "${TARGET_CONFIG}"
