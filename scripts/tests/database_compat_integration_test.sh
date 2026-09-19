#!/usr/bin/env bash
# Real databases only. Missing Docker/readiness/assertions are failures, not SKIP.
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
RUN_ID="ocv-dbcompat-$(date +%s)-$$"
ACTIVE_CONTAINER=""
cleanup() {
    if [[ -n "$ACTIVE_CONTAINER" ]] && [[ "$(docker inspect --format '{{index .Config.Labels "ocv.test.run"}}' "$ACTIVE_CONTAINER" 2>/dev/null)" == "$RUN_ID" ]]; then
        docker rm -f -v "$ACTIVE_CONTAINER" >/dev/null
    fi
}
trap cleanup EXIT
docker info >/dev/null
# Synthetic, disposable test credentials deliberately exercise DSN delimiters.
export OCV_TEST_DB_PASSWORD=' test:@/?#&%+"\value '
for image in ${OCV_TEST_DB_IMAGES:-mariadb:10.11 mysql:8.4}; do
    case "$image" in mariadb:*) engine=mariadb; client=mariadb ;; mysql:*) engine=mysql; client=mysql ;; *) echo "Unsupported test image: $image" >&2; exit 1 ;; esac
    ACTIVE_CONTAINER="$RUN_ID-${image//[:.]/-}"
    echo "Starting real database: $image"
    docker run -d --name "$ACTIVE_CONTAINER" --label "ocv.test.run=$RUN_ID" \
        --memory=1536m --cpus=2 -v /var/lib/mysql \
        -p 127.0.0.1::3306 \
        -e MYSQL_ROOT_PASSWORD=ocv-disposable-test-root \
        -e MYSQL_DATABASE=ocv_dbcompat -e MYSQL_USER=oneclickvirt \
        -e MYSQL_PASSWORD=ocv-test-bootstrap \
        -v "$ROOT_DIR/deploy/my.cnf:/etc/mysql/conf.d/custom.cnf:ro" "$image" >/dev/null
    ready=false
    for ((attempt=0;attempt<90;attempt++)); do
        if docker exec -e MYSQL_PWD=ocv-test-bootstrap "$ACTIVE_CONTAINER" "$client" --protocol=tcp -h127.0.0.1 -uoneclickvirt --connect-timeout=2 -e 'SELECT 1' >/dev/null 2>&1; then ready=true;break;fi
        if [[ "$(docker inspect --format '{{.State.Running}}' "$ACTIVE_CONTAINER")" != true ]]; then break;fi
        sleep 1
    done
    if [[ "$ready" != true ]]; then docker logs --tail=60 "$ACTIVE_CONTAINER";exit 1;fi
    # The upstream MySQL image interpolates MYSQL_PASSWORD into a SQL literal,
    # interpreting backslashes. Provision the deliberately difficult test password
    # explicitly so this tests our connector, not a misconfigured fixture.
    password_sql="${OCV_TEST_DB_PASSWORD//\'/\'\'}"
    docker exec -i -e MYSQL_PWD=ocv-disposable-test-root "$ACTIVE_CONTAINER" "$client" -uroot <<SQL
SET SESSION sql_mode='NO_BACKSLASH_ESCAPES';
ALTER USER 'oneclickvirt'@'%' IDENTIFIED BY '${password_sql}';
SQL
    # The shared deploy/my.cnf must apply the redo tuning to both engines. A
    # MariaDB server uses innodb_log_file_size, while current MySQL exposes
    # innodb_redo_log_capacity; query the server variable rather than trusting
    # the requested image label.
    if [[ "$engine" == mariadb ]]; then
        redo_value="$(docker exec -e MYSQL_PWD=ocv-disposable-test-root "$ACTIVE_CONTAINER" "$client" -uroot -N -B -e 'SELECT @@GLOBAL.innodb_log_file_size')"
    else
        redo_value="$(docker exec -e MYSQL_PWD=ocv-disposable-test-root "$ACTIVE_CONTAINER" "$client" -uroot -N -B -e 'SELECT @@GLOBAL.innodb_redo_log_capacity')"
    fi
    [[ "$redo_value" =~ ^[0-9]+$ && "$redo_value" -ge 268435456 ]] || {
        echo "shared database config did not apply redo tuning for $image: $redo_value" >&2
        docker logs --tail=60 "$ACTIVE_CONTAINER"
        exit 1
    }
    export OCV_TEST_DB_ADDR OCV_TEST_DB_ENGINE="$engine"
    OCV_TEST_DB_ADDR="$(docker port "$ACTIVE_CONTAINER" 3306/tcp)"
    (cd "$ROOT_DIR/server" && go test -count=1 -race -tags=dbintegration -v ./utils/dbconnect -run '^TestLive')
    docker exec -e "MYSQL_PWD=$OCV_TEST_DB_PASSWORD" "$ACTIVE_CONTAINER" "$client" -uoneclickvirt ocv_dbcompat \
        -e 'CREATE TABLE restart_sentinel (id INT PRIMARY KEY); INSERT INTO restart_sentinel VALUES (914);'
    # Restart the SAME data, not a fresh volume, and repeat all assertions.
    docker restart "$ACTIVE_CONTAINER" >/dev/null
    ready=false
    for ((attempt=0;attempt<60;attempt++)); do
        if docker exec -e "MYSQL_PWD=$OCV_TEST_DB_PASSWORD" "$ACTIVE_CONTAINER" "$client" --protocol=tcp -h127.0.0.1 -uoneclickvirt --connect-timeout=2 -e 'SELECT 1' >/dev/null 2>&1; then ready=true;break;fi
        sleep 1
    done
    [[ "$ready" == true ]] || { docker logs --tail=60 "$ACTIVE_CONTAINER";exit 1; }
    # Docker can reassign an ephemeral published port during restart.
    OCV_TEST_DB_ADDR="$(docker port "$ACTIVE_CONTAINER" 3306/tcp)"
    [[ "$(docker exec -e "MYSQL_PWD=$OCV_TEST_DB_PASSWORD" "$ACTIVE_CONTAINER" "$client" -uoneclickvirt ocv_dbcompat -N -B -e 'SELECT id FROM restart_sentinel')" == 914 ]] || { echo 'Data was lost across restart' >&2;exit 1; }
    (cd "$ROOT_DIR/server" && go test -count=1 -race -tags=dbintegration -v ./utils/dbconnect -run '^TestLive')
    cleanup
    ACTIVE_CONTAINER=""
done
echo 'Database compatibility integration tests passed (including restart).'
