#!/usr/bin/env bash
# Execute only inside a disposable database image with a fresh /var/lib/mysql.
set -Eeuo pipefail
[[ -f /.dockerenv && -d /ocv-test ]] || {
    echo 'Requires isolated test container (environment prerequisite; rerun via embedded_database_integration_test.sh)' >&2
    exit 75
}
export MYSQL_CONFIG_FILE=/ocv-test/scripts/tests/fixtures/database-cross-engine.cnf
export APP_DIR=/tmp/ocv-app
export MYSQL_ROOT_PASSWORD=$' embedded:test@:/?\'"\\password '
source /ocv-test/deploy/all-in-one-entrypoint.sh
mkdir -p "$STORAGE_DIR" /var/run/mysqld /var/log/mysql "$MYSQL_DATA_DIR"
chown mysql:mysql /var/run/mysqld /var/log/mysql "$MYSQL_DATA_DIR"
original_hash=$(sha256sum "$MYSQL_CONFIG_FILE")
detect_database_runtime
prepare_database_config
[[ "$(sha256sum "$MYSQL_CONFIG_FILE")" == "$original_hash" ]] || exit 1
if [[ "$EMBEDDED_DB_TYPE" == mariadb ]]; then
    grep -Eq '^innodb_log_file_size=128M$' "$MYSQL_RUNTIME_CONFIG" || {
        echo 'MariaDB runtime config did not translate innodb_redo_log_capacity' >&2
        exit 1
    }
fi
load_database_password
initialize_data_directory_if_needed
[[ "$DATA_DIRECTORY_INITIALIZED" == true ]] || exit 1
configure_database_credentials
[[ -f "$DB_INIT_FLAG" ]] || exit 1

daemon_pid=''
stop_database() {
    if [[ -n "$daemon_pid" ]]; then
        kill "$daemon_pid" 2>/dev/null || true
        wait "$daemon_pid" 2>/dev/null || true
        daemon_pid=''
    fi
}
trap stop_database EXIT
start_database() {
    "$DB_DAEMON" --defaults-file="$MYSQL_RUNTIME_CONFIG" --log-error="$MYSQL_LOG_FILE" &
    daemon_pid=$!
    local attempt
    for ((attempt=0;attempt<90;attempt++)); do
        if database_password_works; then return 0;fi
        if ! kill -0 "$daemon_pid" 2>/dev/null;then break;fi
        sleep 1
    done
    tail -n 60 "$MYSQL_LOG_FILE" >&2
    return 1
}
start_database
if [[ "$EMBEDDED_DB_TYPE" == mariadb ]]; then
    redo_size=$(MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$DB_CLIENT" --protocol=tcp -h127.0.0.1 -uroot -N -B -e 'SELECT @@GLOBAL.innodb_log_file_size')
    [[ "$redo_size" =~ ^[0-9]+$ && "$redo_size" -gt 0 ]] || exit 1
fi
(
    # Exercise the bare-metal provisioning functions against this real daemon.
    # Only OS service-management calls are replaced in the disposable container.
    source /ocv-test/scripts/install_full.sh
    DB_PASSWORD="$MYSQL_ROOT_PASSWORD"
    DB_TYPE=mysql
    select_db_service() { DB_SERVICE=synthetic-test-service; }
    service_enable() { return 0; }
    service_start() { return 0; }
    sleep() { return 0; }
    before_mode=$(db_query_root 'SELECT @@GLOBAL.sql_mode')
    configure_database
    [[ "$DB_TYPE" == "$EMBEDDED_DB_TYPE" ]] || exit 1
    after_mode=$(db_query_root 'SELECT @@GLOBAL.sql_mode')
    [[ "$before_mode" == "$after_mode" ]] || exit 1
    MYSQL_PWD="$DB_PASSWORD" "$DB_CLIENT" --protocol=tcp -h127.0.0.1 -uoneclickvirt oneclickvirt -e 'SELECT 1' >/dev/null
    echo "Bare-metal $DB_TYPE provisioning preserved literal password and global SQL mode."
)
MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$DB_CLIENT" --protocol=tcp -h127.0.0.1 -uroot "$MYSQL_DATABASE" \
    -e 'CREATE TABLE embedded_sentinel (id INT PRIMARY KEY); INSERT INTO embedded_sentinel VALUES (914);'
if MYSQL_ROOT_PASSWORD=incorrect database_password_works; then echo 'Wrong password accepted' >&2;exit 1;fi
stop_database
initialize_data_directory_if_needed
[[ "$DATA_DIRECTORY_INITIALIZED" == false ]] || exit 1
load_database_password
[[ "$DATABASE_PASSWORD_NEEDS_SYNC" == false ]] || exit 1
start_database
[[ "$(MYSQL_PWD="$MYSQL_ROOT_PASSWORD" "$DB_CLIENT" --protocol=tcp -h127.0.0.1 -uroot "$MYSQL_DATABASE" -N -B -e 'SELECT id FROM embedded_sentinel')" == 914 ]] || exit 1
echo "Embedded $EMBEDDED_DB_TYPE live initialization, authentication, config preservation and restart passed."
