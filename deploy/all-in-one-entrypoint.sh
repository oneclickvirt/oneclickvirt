#!/bin/bash

set -Eeuo pipefail

APP_DIR="${APP_DIR:-/app}"
STORAGE_DIR="${STORAGE_DIR:-${APP_DIR}/storage}"
MYSQL_DATA_DIR="${MYSQL_DATA_DIR:-/var/lib/mysql}"
MYSQL_SOCKET="${MYSQL_SOCKET:-/var/run/mysqld/mysqld.sock}"
MYSQL_PID_FILE="${MYSQL_PID_FILE:-/var/run/mysqld/mysqld.pid}"
MYSQL_LOG_FILE="${MYSQL_LOG_FILE:-/var/log/mysql/error.log}"
MYSQL_PORT="${MYSQL_PORT:-3306}"
MYSQL_DATABASE="${MYSQL_DATABASE:-oneclickvirt}"
MYSQL_PASSWORD_FILE="${MYSQL_PASSWORD_FILE:-${STORAGE_DIR}/mysql_root_password}"
DB_INIT_FLAG="${DB_INIT_FLAG:-${MYSQL_DATA_DIR}/.mysql_initialized}"
SUPERVISOR_CONFIG="${SUPERVISOR_CONFIG:-/etc/supervisor/conf.d/supervisord.conf}"
MYSQL_CONFIG_FILE="${MYSQL_CONFIG_FILE:-/etc/mysql/conf.d/custom.cnf}"
MYSQL_RUNTIME_CONFIG="${MYSQL_RUNTIME_CONFIG:-/var/run/mysqld/oneclickvirt.cnf}"

database_data_engine_hint() {
    local maria=false mysql=false marker
    if [[ -f "${MYSQL_DATA_DIR}/aria_log_control" ]]; then
        maria=true
    else
        for marker in "${MYSQL_DATA_DIR}"/aria_log.*; do
            if [[ -e "$marker" ]]; then
                maria=true
                break
            fi
        done
    fi
    [[ -f "${MYSQL_DATA_DIR}/mysql.ibd" || -d "${MYSQL_DATA_DIR}/#innodb_redo" ]] && mysql=true
    if [[ "$maria" == true && "$mysql" == false ]]; then
        printf '%s\n' mariadb
    elif [[ "$mysql" == true && "$maria" == false ]]; then
        printf '%s\n' mysql
    else
        return 1
    fi
}

detect_database_runtime() {
    local requested="${MYSQL_DAEMON:-}" daemon version engine
    local -a daemon_paths=() daemon_types=()

    if [[ -n "$requested" ]]; then
        if [[ -x "$requested" ]]; then
            DB_DAEMON="$requested"
        elif DB_DAEMON="$(command -v "$requested" 2>/dev/null)" && [[ -x "$DB_DAEMON" ]]; then
            :
        else
            echo "ERROR: MYSQL_DAEMON does not point to an executable database daemon" >&2
            return 1
        fi
    else
        # A package may install both mysqld and the MariaDB compatibility
        # alias. Inspect every available daemon's own version first; binary
        # name/order is never treated as the engine. If two real engines are
        # installed, only an unambiguous data-directory marker may select one.
        for daemon in mariadbd mysqld; do
            local path
            path="$(command -v "$daemon" 2>/dev/null || true)"
            [[ -x "$path" ]] || continue
            version="$($path --no-defaults --version 2>/dev/null || true)"
            [[ -n "$version" ]] || continue
            case "$version" in
                *MariaDB*|*mariadb*) engine="mariadb" ;;
                *) engine="mysql" ;;
            esac
            daemon_paths+=("$path")
            daemon_types+=("$engine")
        done
        if ((${#daemon_paths[@]} == 0)); then
            echo "ERROR: no MySQL-compatible database daemon was found" >&2
            return 1
        fi
        EMBEDDED_DB_TYPE="${daemon_types[0]}"
        for engine in "${daemon_types[@]}"; do
            if [[ "$engine" != "$EMBEDDED_DB_TYPE" ]]; then
                local data_hint
                data_hint="$(database_data_engine_hint 2>/dev/null || true)"
                if [[ -z "$data_hint" ]]; then
                    echo "ERROR: both MySQL and MariaDB daemons are installed but the data directory engine is ambiguous; refusing to guess" >&2
                    return 1
                fi
                EMBEDDED_DB_TYPE="$data_hint"
                break
            fi
        done
        # Prefer the daemon whose reported version matches the selected engine.
        for ((i = 0; i < ${#daemon_paths[@]}; i++)); do
            if [[ "${daemon_types[i]}" == "$EMBEDDED_DB_TYPE" ]]; then
                DB_DAEMON="${daemon_paths[i]}"
                break
            fi
        done
    fi

    # The selected daemon's version remains authoritative, including when an
    # explicit MYSQL_DAEMON path was supplied.
    version="$("${DB_DAEMON}" --no-defaults --version)" || return 1
    export DB_VERSION="${version}"
    case "${version}" in
        *MariaDB*|*mariadb*) EMBEDDED_DB_TYPE="mariadb" ;;
        *) EMBEDDED_DB_TYPE="mysql" ;;
    esac
    if [[ "$EMBEDDED_DB_TYPE" == mariadb ]] && command -v mariadb >/dev/null 2>&1; then
        DB_CLIENT="$(command -v mariadb)"
    elif command -v mysql >/dev/null 2>&1; then
        DB_CLIENT="$(command -v mysql)"
    else
        echo "ERROR: matching MySQL/MariaDB client was not found" >&2
        return 1
    fi
    export DB_CLIENT
    echo "Detected architecture $(uname -m), using ${EMBEDDED_DB_TYPE} (${DB_DAEMON})"
}

prepare_database_config() {
    # Keep the supplied file intact (it may be a read-only bind mount). Only
    # version-specific tuning may fall back to daemon defaults. Never loosen
    # authentication, TLS, bind-address, sql_mode or data-integrity settings.
    [[ -r "${MYSQL_CONFIG_FILE}" ]] || { echo "ERROR: database configuration is not readable" >&2; return 1; }
    if [[ "${MYSQL_CONFIG_FILE}" == "${MYSQL_RUNTIME_CONFIG}" ]]; then
        echo "ERROR: MYSQL_RUNTIME_CONFIG must be separate from the source configuration" >&2
        return 1
    fi
    case "${MYSQL_PORT}" in
        ''|*[!0-9]*) echo "ERROR: MYSQL_PORT must be numeric" >&2; return 1 ;;
    esac
    local port_number=$((10#${MYSQL_PORT}))
    if ((port_number < 1 || port_number > 65535)); then
        echo "ERROR: MYSQL_PORT must be between 1 and 65535" >&2
        return 1
    fi
    local required_path
    for required_path in "${MYSQL_DATA_DIR}" "${MYSQL_SOCKET}" "${MYSQL_PID_FILE}" "${MYSQL_LOG_FILE}" "${MYSQL_RUNTIME_CONFIG}"; do
        if [[ "${required_path}" != /* || "${required_path}" == *$'\n'* || "${required_path}" == *$'\r'* ]]; then
            echo "ERROR: database runtime paths must be absolute and must not contain newlines" >&2
            return 1
        fi
    done
    mkdir -p "$(dirname "${MYSQL_RUNTIME_CONFIG}")" || {
        echo "ERROR: cannot create the database runtime configuration directory" >&2
        return 1
    }
    umask 077
    # The shipped file is intentionally shared by both engines.  Normalize the
    # small set of options whose names or availability differs before starting
    # the daemon.  The source file is never edited, so a read-only bind mount
    # and a later engine switch remain recoverable.
    awk -v engine="${EMBEDDED_DB_TYPE:-mysql}" '
        /^[[:space:]]*[#;]/ { print; next }
        {
            line=$0
            key=line; sub(/=.*/, "", key); gsub(/^[[:space:]]+|[[:space:]]+$/, "", key)
            normalized=key; sub(/^loose-/, "", normalized); gsub(/-/, "_", normalized)
            if (engine == "mariadb" && normalized == "innodb_redo_log_capacity") {
                # MariaDB exposes this setting as innodb_log_file_size.  A
                # literal loose-innodb_redo_log_capacity is only ignored and
                # therefore does not apply the requested redo tuning.
                sub(/^[[:space:]]*[^=]+/, "innodb_log_file_size", line)
            } else if (engine == "mysql" && normalized == "innodb_log_file_size") {
                # A MariaDB configuration commonly uses innodb_log_file_size.
                # Keep that option as a loose fallback for MySQL versions that
                # predate 8.0.30, and add the modern redo-capacity spelling so
                # current MySQL releases actually apply the requested value.
                value=line; sub(/^[^=]*=/, "", value)
                sub(/^[[:space:]]*[^=]+/, "loose-innodb_log_file_size", line)
                print line
                print "loose-innodb_redo_log_capacity=" value
                next
            } else if (engine == "mysql" && normalized == "innodb_redo_log_capacity") {
                # MySQL versions before 8.0.30 do not know this option.  The
                # loose prefix is accepted and still applies it on versions
                # that do support it.
                if (key !~ /^loose-/) sub(/^[[:space:]]*/, "loose-", line)
            } else if (engine == "mysql" && normalized ~ /^query_cache_(type|size)$/) {
                # Query cache was removed from MySQL 8.  Keep the setting
                # harmless when an old MariaDB config is supplied.
                if (key !~ /^loose-/) sub(/^[[:space:]]*/, "loose-", line)
            } else if (engine == "mariadb" && normalized ~ /^query_cache_(type|size)$/) {
                # MariaDB still implements query cache; remove a compatibility
                # prefix that may have been added for a MySQL source config.
                sub(/^[[:space:]]*loose-/, "", line)
            }
            print line
        }
    ' "${MYSQL_CONFIG_FILE}" >"${MYSQL_RUNTIME_CONFIG}"
    # The container lifecycle owns these paths. A mounted or stale config file
    # may contain a different datadir/socket/port, which would otherwise make
    # initialization inspect one directory while the daemon serves another.
    # Append a final [mysqld] section so the source file remains untouched and
    # all engine-specific tuning above still follows the detected engine.
    cat >>"${MYSQL_RUNTIME_CONFIG}" <<RUNTIME_OVERRIDES

[mysqld]
datadir=${MYSQL_DATA_DIR}
socket=${MYSQL_SOCKET}
pid-file=${MYSQL_PID_FILE}
log-error=${MYSQL_LOG_FILE}
port=${MYSQL_PORT}
user=mysql
RUNTIME_OVERRIDES
    chown mysql:mysql "${MYSQL_RUNTIME_CONFIG}"
    # --help validates options without starting a server or touching user data.
    if ! "${DB_DAEMON}" --defaults-file="${MYSQL_RUNTIME_CONFIG}" --verbose --help >/dev/null; then
        echo "ERROR: database configuration is invalid for ${EMBEDDED_DB_TYPE}; source file and data were preserved" >&2
        return 1
    fi
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
    if [[ "${EMBEDDED_DB_TYPE:-}" == "mysql" && -f "${MYSQL_DATA_DIR}/aria_log_control" ]] ||
       [[ "${EMBEDDED_DB_TYPE:-}" == "mariadb" && -f "${MYSQL_DATA_DIR}/mysql.ibd" ]]; then
        echo "ERROR: existing data belongs to the other database engine; use an explicit logical migration, not automatic initialization" >&2
        return 1
    fi
    if [[ -d "${MYSQL_DATA_DIR}/mysql" ]]; then
        echo "Database system tables already exist; preserving the data directory"
        return
    fi

    if [[ -n "$(find "${MYSQL_DATA_DIR}" -mindepth 1 -maxdepth 1 ! -name lost+found -print -quit)" ]]; then
        echo "ERROR: non-empty database directory has no system tables; preserving it for recovery (no automatic deletion or cross-engine initialization)" >&2
        return 1
    fi
    echo "Initializing ${EMBEDDED_DB_TYPE} data directory"
    if [[ "${EMBEDDED_DB_TYPE}" == "mysql" ]]; then
        "${DB_DAEMON}" --defaults-file="${MYSQL_RUNTIME_CONFIG}" --initialize-insecure --user=mysql --datadir="${MYSQL_DATA_DIR}" --skip-name-resolve
    else
        mariadb-install-db --defaults-file="${MYSQL_RUNTIME_CONFIG}" --user=mysql --datadir="${MYSQL_DATA_DIR}" --skip-name-resolve
    fi
    DATA_DIRECTORY_INITIALIZED=true
}

sql_escape_string() {
    # Credential setup explicitly uses NO_BACKSLASH_ESCAPES on its own session.
    # Do not assume a global SQL mode, which may come from a supplied my.cnf.
    printf '%s' "$1" | sed -e "s/'/''/g"
}

wait_for_socket_database() {
    local pid="$1"
    for _ in $(seq 1 90); do
        if "${DB_CLIENT:-mysql}" --protocol=socket --socket="${MYSQL_SOCKET}" -e "SELECT 1" >/dev/null 2>&1; then
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
        --defaults-file="${MYSQL_RUNTIME_CONFIG}" \
        --user=mysql \
        --datadir="${MYSQL_DATA_DIR}" \
        --skip-networking \
        --skip-grant-tables \
        --socket="${MYSQL_SOCKET}" \
        --pid-file="${MYSQL_PID_FILE}" \
        --log-error="${MYSQL_LOG_FILE}" &
    mysql_pid=$!
    trap 'kill "${mysql_pid}" 2>/dev/null || true' RETURN
    wait_for_socket_database "${mysql_pid}"

    if [[ "${EMBEDDED_DB_TYPE}" == "mysql" ]]; then
        "${DB_CLIENT:-mysql}" --protocol=socket --socket="${MYSQL_SOCKET}" <<SQLEND
FLUSH PRIVILEGES;
SET SESSION sql_mode=CONCAT_WS(',', NULLIF(@@SESSION.sql_mode, ''), 'NO_BACKSLASH_ESCAPES');
# Debian's MySQL package may initialize root with auth_socket during the image
# build even though that optional plugin is not loaded by our standalone
# runtime daemon. Select MySQL's built-in password plugin explicitly instead
# of inheriting an unusable plugin from the packaged data directory.
ALTER USER 'root'@'localhost' IDENTIFIED WITH caching_sha2_password BY '${escaped_password}';
DROP USER IF EXISTS 'root'@'127.0.0.1';
DROP USER IF EXISTS 'root'@'%';
CREATE USER 'root'@'127.0.0.1' IDENTIFIED WITH caching_sha2_password BY '${escaped_password}';
CREATE USER 'root'@'%' IDENTIFIED WITH caching_sha2_password BY '${escaped_password}';
GRANT ALL PRIVILEGES ON *.* TO 'root'@'localhost' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON *.* TO 'root'@'127.0.0.1' WITH GRANT OPTION;
GRANT ALL PRIVILEGES ON *.* TO 'root'@'%' WITH GRANT OPTION;
CREATE DATABASE IF NOT EXISTS \`${MYSQL_DATABASE}\` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
FLUSH PRIVILEGES;
SQLEND
    else
        "${DB_CLIENT:-mysql}" --protocol=socket --socket="${MYSQL_SOCKET}" <<SQLEND
FLUSH PRIVILEGES;
SET SESSION sql_mode=CONCAT_WS(',', NULLIF(@@SESSION.sql_mode, ''), 'NO_BACKSLASH_ESCAPES');
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
    MYSQL_PWD="${MYSQL_ROOT_PASSWORD}" "${DB_CLIENT:-mysql}" --protocol=tcp --host=127.0.0.1 --port="${MYSQL_PORT}" --user=root \
        --connect-timeout=5 -e 'SELECT 1' >/dev/null 2>&1
}

write_wait_and_start_script() {
    cat >"${APP_DIR}/wait-and-start.sh" <<'APPWAIT'
#!/bin/bash
set -Eeuo pipefail

for i in $(seq 1 90); do
    if MYSQL_PWD="${DB_PASSWORD}" "${DB_CLIENT:-mysql}" --protocol=tcp --host="${DB_HOST}" --port="${DB_PORT}" \
        --user="${DB_USER}" --connect-timeout=5 -e 'SELECT 1' >/dev/null 2>&1; then
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
        daemon_command="${DB_DAEMON} --defaults-file=${MYSQL_RUNTIME_CONFIG} --lc-messages=en_US"
    else
        daemon_command="${DB_DAEMON} --defaults-file=${MYSQL_RUNTIME_CONFIG}"
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
    prepare_database_config
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
    export DB_PORT="${MYSQL_PORT}"
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
