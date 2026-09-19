#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/install_full.sh"

fail() { echo "FAIL: $*" >&2; exit 1; }
TEST_ROOT="$(mktemp -d)"
trap 'rm -rf "$TEST_ROOT"' EXIT

SOURCE="$TEST_ROOT/source.cnf"
printf '%s\n' '[mysqld]' 'bind-address=0.0.0.0' \
    'loose-innodb_redo_log_capacity=256M' 'query_cache_type=0' >"$SOURCE"
export DB_CONFIG_SOURCE="$SOURCE"
export MYSQL_CNF_DIR="$TEST_ROOT/etc/mysql/conf.d"

MYSQL_CNF_DIR='relative/path'
if apply_database_config; then fail 'relative MYSQL_CNF_DIR was accepted'; fi
MYSQL_CNF_DIR="$TEST_ROOT/etc/mysql/conf.d"
printf -v BAD_SOURCE '%s\n' "$TEST_ROOT/source.cnf"
DB_CONFIG_SOURCE="$BAD_SOURCE"
if database_config_source; then fail 'control-character DB_CONFIG_SOURCE was accepted'; fi
DB_CONFIG_SOURCE="$SOURCE"

have_cmd() { [[ "$1" == mariadbd || "$1" == mysqld ]]; }
mariadbd() { return 0; }
mysqld() { return 0; }

DB_TYPE=mariadb
apply_database_config || fail 'MariaDB config application failed'
TARGET="$MYSQL_CNF_DIR/oneclickvirt.cnf"
grep -Fxq 'innodb_log_file_size=256M' "$TARGET" || fail 'MariaDB redo option was not translated'
grep -Fxq 'query_cache_type=0' "$TARGET" || fail 'MariaDB query cache option was not restored'
grep -Fxq 'bind-address=127.0.0.1' "$TARGET" || fail 'bare-metal bind policy was not enforced'
grep -Fxq 'bind-address=0.0.0.0' "$SOURCE" || fail 'source config was modified'

printf '%s\n' '[mysqld]' 'sentinel=old' >"$TARGET"
mariadbd() { return 1; }
if apply_database_config; then fail 'invalid daemon configuration was accepted'; fi
grep -Fxq 'sentinel=old' "$TARGET" || fail 'invalid config replaced the active drop-in'

mariadbd() { return 0; }
DB_TYPE=mysql
apply_database_config || fail 'MySQL config application failed'
grep -Fxq 'loose-innodb_redo_log_capacity=256M' "$TARGET" || fail 'MySQL redo option was not made version-safe'
compgen -G "$TARGET.bak.*" >/dev/null || fail 'previous drop-in was not preserved'

echo 'Database config application tests passed.'
