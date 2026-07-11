#!/bin/bash

set -Eeuo pipefail

APP_DIR="${APP_DIR:-/app}"
STORAGE_DIR="${STORAGE_DIR:-${APP_DIR}/storage}"
DEFAULT_CONFIG="${DEFAULT_CONFIG:-${APP_DIR}/config.yaml.default}"
ACTIVE_CONFIG="${ACTIVE_CONFIG:-${APP_DIR}/config.yaml}"
PERSISTED_CONFIG="${PERSISTED_CONFIG:-${STORAGE_DIR}/config.yaml}"
NGINX_CONFIG="${NGINX_CONFIG:-/etc/nginx/nginx.conf}"
SUPERVISOR_CONFIG="${SUPERVISOR_CONFIG:-/etc/supervisor/conf.d/supervisord.conf}"

prepare_runtime_config() {
    mkdir -p "${STORAGE_DIR}"/{cache,certs,configs,exports,logs,temp,uploads}

    # Keep an explicitly bind-mounted config file authoritative.
    if [[ -e "${ACTIVE_CONFIG}" && ! -L "${ACTIVE_CONFIG}" ]]; then
        if [[ ! -s "${ACTIVE_CONFIG}" ]]; then
            echo "ERROR: ${ACTIVE_CONFIG} is empty" >&2
            return 1
        fi
        echo "Using externally mounted configuration: ${ACTIVE_CONFIG}"
        return
    fi

    if [[ ! -s "${PERSISTED_CONFIG}" ]]; then
        if [[ ! -s "${DEFAULT_CONFIG}" ]]; then
            echo "ERROR: default configuration is missing: ${DEFAULT_CONFIG}" >&2
            return 1
        fi
        cp "${DEFAULT_CONFIG}" "${PERSISTED_CONFIG}"
        chmod 600 "${PERSISTED_CONFIG}"
        echo "Created persistent runtime configuration: ${PERSISTED_CONFIG}"
    fi

    ln -sfn "${PERSISTED_CONFIG}" "${ACTIVE_CONFIG}"
}

escape_sed_replacement() {
    printf '%s' "$1" | sed 's/[\\&|]/\\&/g'
}

sed_in_place() {
    if sed --version >/dev/null 2>&1; then
        sed -i "$@"
    else
        sed -i '' "$@"
    fi
}

configure_frontend_url() {
    if [[ -z "${FRONTEND_URL:-}" ]]; then
        return
    fi

    local escaped_url config_path
    config_path="${ACTIVE_CONFIG}"
    if [[ -L "${ACTIVE_CONFIG}" ]]; then
        config_path="${PERSISTED_CONFIG}"
    fi
    escaped_url="$(escape_sed_replacement "${FRONTEND_URL}")"
    echo "Configuring frontend-url"
    sed_in_place "s|^\\([[:space:]]*frontend-url:[[:space:]]*\\).*$|\\1\"${escaped_url}\"|" "${config_path}"

    # A TLS-terminating reverse proxy reaches this container over HTTP. Use the
    # public URL to preserve the original scheme without adding duplicate headers.
    if [[ "${FRONTEND_URL}" == https://* ]]; then
        sed_in_place 's|proxy_set_header X-Forwarded-Proto \$scheme;|proxy_set_header X-Forwarded-Proto https;|g' "${NGINX_CONFIG}"
    fi
}

write_supervisor_config() {
    mkdir -p "$(dirname "${SUPERVISOR_CONFIG}")"
    cat > "${SUPERVISOR_CONFIG}" <<'SUPEREND'
[supervisord]
nodaemon=true
user=root
logfile=/dev/stdout
logfile_maxbytes=0

[program:app]
command=/app/main
directory=/app
autostart=true
autorestart=true
user=root
priority=1
stdout_logfile=/dev/stdout
stderr_logfile=/dev/stderr
stdout_logfile_maxbytes=0
stderr_logfile_maxbytes=0
startsecs=5

[program:nginx]
command=/usr/sbin/nginx -g "daemon off;"
autostart=true
autorestart=true
user=root
priority=2
stdout_logfile=/dev/stdout
stderr_logfile=/dev/stderr
stdout_logfile_maxbytes=0
stderr_logfile_maxbytes=0
SUPEREND
}

main() {
    echo "Starting OneClickVirt (No Database)..."
    prepare_runtime_config
    configure_frontend_url
    write_supervisor_config
    exec supervisord -c "${SUPERVISOR_CONFIG}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
