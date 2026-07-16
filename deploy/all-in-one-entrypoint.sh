#!/bin/bash

set -Eeuo pipefail

APP_DIR="${APP_DIR:-/app}"
STORAGE_DIR="${STORAGE_DIR:-${APP_DIR}/storage}"
MYSQL_DATA_DIR="${MYSQL_DATA_DIR:-/var/lib/mysql}"
MYSQL_SOCKET="${MYSQL_SOCKET:-/var/run/mysqld/mysqld.sock}"
MYSQL_PID_FILE="${MYSQL_PID_FILE:-/var/run/mysqld/mysqld.pid}"
MYSQL_LOG_FILE="${MYSQL_LOG_FILE:-/var/log/mysql/error.log}"
MYSQL_DATABASE="${MYSQL_DATABASE:-oneclickvirt}"
MYSQL_PASSWORD_FILE="${MYSQL_PASSWORD_FILE:-${STORAGE_DIR}/mysql_root_password}"
DB_INIT_FLAG="${DB_INIT_FLAG:-${MYSQL_DATA_DIR}/.mysql_initialized}"
SUPERVISOR_CONFIG="${SUPERVISOR_CONFIG:-/etc/supervisor/conf.d/supervisord.conf}"

detect_database_runtime() {
    if command -v mariadbd >/dev/null 2>&1; then
        EMBEDDED_DB_TYPE="mariadb"
        DB_DAEMON="$(command -v mariadbd)"
    elif command -v mysqld >/dev/null 2>&1; then
        EMBEDDED_DB_TYPE="mysql"
        DB_DAEMON="$(command -v mysqld)"
    else
        echo "ERROR: no MySQL-compatible database daemon was found" >&2
        return 1
    fi
    echo "Detected architecture $(uname -m), using ${EMBEDDED_DB_TYPE} (${DB_DAEMON})"
}

load_database_password() {
    local supplied_password="${MYSQL_ROOT_PASSWORD:-}"
    local persisted_password=""

    DATABASE_PASSWORD_NEEDS_SYNC=false
    if [[ -r "${MYSQL_PASSWORD_FILE}" ]]; then
        persisted_password="$(<"${MYSQL_PASSWORD_FILE}")"
    fi

    if [[ -n "${supplied_password}" ]]; then
        MYSQL_ROOT_PASSWORD="${supplied_password}"
        if [[ -z "${persisted_password}" || "${persisted_password}" != "${supplied_password}" ]]; then
            DATABASE_PASSWORD_NEEDS_SYNC=true
        fi
    elif [[ -n "${persisted_password}" ]]; then
        MYSQL_ROOT_PASSWORD="${persisted_password}"
    else
        # tr receives SIGPIPE after head has enough bytes. Disable pipefail only
        # inside this bounded generator so a successful password is not treated
        # as a fatal startup error on faster ARM and AMD64 hosts.
        MYSQL_ROOT_PASSWORD="$(set +o pipefail; LC_ALL=C tr -dc 'A-Za-z0-9' </dev/urandom | head -c 24)"
        DATABASE_PASSWORD_NEEDS_SYNC=true
        echo "Generated a random embedded database password. It will be persisted in ${MYSQL_PASSWORD_FILE}."
    fi

    if [[ -z "${MYSQL_ROOT_PASSWORD}" || "${MYSQL_ROOT_PASSWORD}" == *$'\n'* || "${MYSQL_ROOT_PASSWORD}" == *$'\r'* ]]; then
        echo "ERROR: MYSQL_ROOT_PASSWORD must be non-empty and must not contain newlines" >&2
        return 1
    fi
    export MYSQL_ROOT_PASSWORD
}

persist_database_password() {
    mkdir -p "$(dirname "${MYSQL_PASSWORD_FILE}")"
    printf '%s' "${MYSQL_ROOT_PASSWORD}" >"${MYSQL_PASSWORD_FILE}"
    chmod 600 "${MYSQL_PASSWORD_FILE}"
}

initialize_data_directory_if_needed() {
    DATA_DIRECTORY_INITIALIZED=false
    if [[ -d "${MYSQL_DATA_DIR}/mysql" ]]; then
        echo "Database system tables already exist; preserving the data directory"
        return
    fi

    echo "Initializing ${EMBEDDED_DB_TYPE} data directory"
    find "${MYSQL_DATA_DIR}" -mindepth 1 -maxdepth 1 -exec rm -rf -- {} +
    if [[ "${EMBEDDED_DB_TYPE}" == "mysql" ]]; then
        "${DB_DAEMON}" --initialize-insecure --user=mysql --datadir="${MYSQL_DATA_DIR}" --skip-name-resolve
    else
        mariadb-install-db --user=mysql --datadir="${MYSQL_DATA_DIR}" --skip-name-resolve
    fi
    DATA_DIRECTORY_INITIALIZED=true
}

sql_escape_string() {
    # MySQL and MariaDB both accept doubled single quotes. Backslashes must be
    # doubled as well because the default SQL mode treats them as escapes.
    printf '%s' "$1" | sed -e 's/\\/\\\\/g' -e "s/'/''/g"
}

wait_for_socket_database() {
    local pid="$1"
    for _ in $(seq 1 90); do
        if mysql --protocol=socket --socket="${MYSQL_SOCKET}" -e "SELECT 1" >/dev/null 2>&1; then
            return
        fi
        if ! kill -0 "${pid}" 2>/dev/null; then
            echo "ERROR: temporary ${EMBEDDED_DB_TYPE} server exited during startup" >&2
            tail -n 100 "${MYSQL_LOG_FILE}" >&2 2>/dev/null || true
            return 1
        fi
        sleep 1
    done
    echo "ERROR: temporary ${EMBEDDED_DB_TYPE} server did not become ready" >&2
    return 1
}

configure_database_credentials() {
    local escaped_password mysql_pid
    escaped_password="$(sql_escape_string "${MYSQL_ROOT_PASSWORD}")"

    echo "Configuring embedded database credentials"
    "${DB_DAEMON}" \
        --user=mysql \
        --skip-networking \
        --skip-grant-tables \
        --socket="${MYSQL_SOCKET}" \
        --pid-file="${MYSQL_PID_FILE}" \
        --log-error="${MYSQL_LOG_FILE}" &
    mysql_pid=$!
    trap 'kill "${mysql_pid}" 2>/dev/null || true' RETURN
    wait_for_socket_database "${mysql_pid}"

    if [[ "${EMBEDDED_DB_TYPE}" == "mysql" ]]; then
        mysql --protocol=socket --socket="${MYSQL_SOCKET}" <<SQLEND
FLUSH PRIVILEGES;
ALTER USER 'root'@'localhost' IDENTIFIED WITH mysql_native_password BY '${escaped_password}';
DROP USER IF EXISTS 'root'@'127.0.0.1';
DROP USER IF EXISTS 'root'@'%';
CREATE USER 'root'@'127.0.0.1' IDENTIFIED WITH mysql_native_password BY '${escaped_password}';
CREATE USER 'root'@'%' IDENTIFIED WITH mysql_native_password BY '${escaped_password}';
GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON *.* TO 'root'@'%' WITH GRANT OPTION;
CREATE DATABASE IF NOT EXISTS \`${MYSQL_DATABASE}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
FLUSH PRIVILEGES;
SQLEND
    else
        mysql --protocol=socket --socket="${MYSQL_SOCKET}" <<SQLEND
FLUSH PRIVILEGES;
SET PASSWORD FOR 'root'@'localhost' = PASSWORD('${escaped_password}');
GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION;
CREATE USER IF NOT EXISTS 'root'@'127.0.0.1' IDENTIFIED BY '${escaped_password}';
ALTER USER 'root'@'127.0.0.1' IDENTIFIED BY '${escaped_password}';
GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;
CREATE USER IF NOT EXISTS 'root'@'%' IDENTIFIED BY '${escaped_password}';
ALTER USER 'root'@'%' IDENTIFIED BY '${escaped_password}';
GRANT ALL PRIVILEGES ON *.* TO 'root'@'%' WITH GRANT OPTION;
CREATE DATABASE IF NOT EXISTS \`${MYSQL_DATABASE}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
FLUSH PRIVILEGES;
SQLEND
    fi

    kill "${mysql_pid}" 2>/dev/null || true
    wait "${mysql_pid}" 2>/dev/null || true
    trap - RETURN
    persist_database_password
    printf '%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ): initialized with ${EMBEDDED_DB_TYPE}" >"${DB_INIT_FLAG}"
}

database_password_works() {
    mysqladmin --protocol=tcp --host=127.0.0.1 --user=root \
        --password="${MYSQL_ROOT_PASSWORD}" ping --silent >/dev/null 2>&1
}

write_wait_and_start_script() {
    cat >"${APP_DIR}/wait-and-start.sh" <<'APPWAIT'
#!/bin/bash
set -Eeuo pipefail

for i in $(seq 1 90); do
    if mysqladmin --protocol=tcp --host="${DB_HOST}" --port="${DB_PORT}" \
        --user="${DB_USER}" --password="${DB_PASSWORD}" ping --silent >/dev/null 2>&1; then
        exec /app/main
    fi
    echo "Waiting for database... (${i}/90)"
    sleep 2
done
echo "Database not ready after wait; application start aborted" >&2
exit 1
APPWAIT
    chmod 755 "${APP_DIR}/wait-and-start.sh"
}

write_supervisor_config() {
    local daemon_command
    if [[ "${EMBEDDED_DB_TYPE}" == "mysql" ]]; then
        daemon_command="${DB_DAEMON} --defaults-file=/etc/mysql/conf.d/custom.cnf --lc-messages=en_US"
    else
        daemon_command="${DB_DAEMON} --defaults-file=/etc/mysql/conf.d/custom.cnf"
    fi

    cat >"${SUPERVISOR_CONFIG}" <<SUPEREND
[supervisord]
nodaemon=true
user=root
logfile=/dev/stdout
logfile_maxbytes=0

[program:mysql]
command=${daemon_command}
autostart=true
autorestart=true
user=mysql
priority=1
stdout_logfile=/dev/stdout
stderr_logfile=/dev/stderr
stdout_logfile_maxbytes=0
stderr_logfile_maxbytes=0
startsecs=10
startretries=3

[program:app]
command=/bin/bash /app/wait-and-start.sh
directory=/app
autostart=true
autorestart=true
user=root
priority=2
stdout_logfile=/dev/stdout
stderr_logfile=/dev/stderr
stdout_logfile_maxbytes=0
stderr_logfile_maxbytes=0
startsecs=1

[program:nginx]
command=/usr/sbin/nginx -g "daemon off;"
autostart=true
autorestart=true
user=root
priority=3
stdout_logfile=/dev/stdout
stderr_logfile=/dev/stderr
stdout_logfile_maxbytes=0
stderr_logfile_maxbytes=0
SUPEREND
}

configure_frontend_url() {
    if [[ -z "${FRONTEND_URL:-}" ]]; then
        return
    fi
    local escaped_url
    escaped_url="$(printf '%s' "${FRONTEND_URL}" | sed 's/[\\&|]/\\&/g')"
    sed -i "s|frontend-url:.*|frontend-url: \"${escaped_url}\"|g" "${APP_DIR}/config.yaml"
    if [[ "${FRONTEND_URL}" == https://* ]]; then
        # $scheme is an nginx variable, not shell expansion.
        # shellcheck disable=SC2016
        sed -i 's|proxy_set_header X-Forwarded-Proto \$scheme;|proxy_set_header X-Forwarded-Proto https;|g' /etc/nginx/nginx.conf
    fi
}

main() {
    echo "Starting OneClickVirt..."
    mkdir -p "${STORAGE_DIR}" /var/run/mysqld /var/log/mysql "${MYSQL_DATA_DIR}"
    chown -R mysql:mysql "${MYSQL_DATA_DIR}" /var/run/mysqld /var/log/mysql
    chmod 755 /var/run/mysqld

    detect_database_runtime
    load_database_password
    if [[ ! "${MYSQL_DATABASE}" =~ ^[A-Za-z0-9_]+$ ]]; then
        echo "ERROR: MYSQL_DATABASE may contain only letters, digits, and underscores" >&2
        return 1
    fi
    configure_frontend_url
    initialize_data_directory_if_needed

    # A valid system-table directory without our marker is an older or
    # interrupted installation. Preserve it and resume only credential setup.
    if [[ ! -f "${DB_INIT_FLAG}" || "${DATA_DIRECTORY_INITIALIZED}" == "true" || "${DATABASE_PASSWORD_NEEDS_SYNC}" == "true" ]]; then
        configure_database_credentials
    fi

    export DB_HOST="127.0.0.1"
    export DB_PORT="3306"
    export DB_NAME="${MYSQL_DATABASE}"
    export DB_USER="root"
    export DB_PASSWORD="${MYSQL_ROOT_PASSWORD}"
    export DB_TYPE="${EMBEDDED_DB_TYPE}"

    write_wait_and_start_script
    write_supervisor_config
    exec supervisord -c "${SUPERVISOR_CONFIG}"
}

if [[ "${BASH_SOURCE[0]}" == "$0" ]]; then
    main "$@"
fi
