#!/usr/bin/env bash
set -Eeuo pipefail
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
source "$ROOT_DIR/scripts/install_full.sh"
fail() { echo "FAIL: $*" >&2; exit 1; }

(
    db_query_root() { return 1; }
    mysqladmin() { return 0; }
    if db_ping; then fail 'unauthenticated database is marked ready';fi
)
(
    # A MariaDB mysqld alias must not cause a MySQL package replacement.
    have_cmd() { [[ "$1" == mysqld ]]; }
    mysqld() { printf '%s\n' 'mysqld Ver 10.11.18-MariaDB'; }
    DB_TYPE=mysql
    configure_database() { [[ "$DB_TYPE" == mariadb ]]; }
    install_mysql() { fail 'replaced an existing database'; }
    install_mariadb() { fail 'replaced an existing database'; }
    install_local_database || fail 'existing daemon was not detected'
)
(
    # A stale client preference must not hide a working compatibility client.
    # This models distributions that ship both names while only one client
    # can authenticate to the local daemon.
    db_client() { printf '%s\n' mysql; }
    have_cmd() { [[ "$1" == mysql || "$1" == mariadb ]]; }
    mysql() { return 1; }
    mariadb() { printf '%s\n' '10.11.18-MariaDB'; }
    DB_PASSWORD=disposable
    [[ "$(db_query_root 'SELECT VERSION()')" == '10.11.18-MariaDB' ]] || fail 'alternate database client was not attempted'
)
(
    have_cmd() { return 1; }
    database_datadir_has_data() { return 0; }
    install_mysql() { fail 'installed over orphaned user data'; }
    if install_local_database;then fail 'orphaned data allowed engine fallback';fi
    if database_fallback_is_safe;then fail 'existing data allows fallback';fi
)
(
    have_cmd() { [[ "$1" == mysqld ]]; }
    database_datadir_has_data() { return 1; }
    if database_fallback_is_safe;then fail 'installed daemon allows cross-engine fallback';fi
)
(
    # When both daemon binaries exist, an authenticated server probe wins over
    # the requested label and over binary ordering.
    db_query_root() { printf '%s\n' '11.4.2-MariaDB'; }
    DB_TYPE=mysql
    detect_installed_database || fail 'live database probe was not accepted'
    [[ "$DB_TYPE" == mariadb ]] || fail 'live MariaDB was classified as MySQL'
)
(
    # If neither daemon can be queried while both engines are installed, never
    # guess which data directory is safe to modify.
    db_query_root() { return 1; }
    have_cmd() { [[ "$1" == mariadbd || "$1" == mysqld ]]; }
    mariadbd() { printf '%s\n' 'mariadbd Ver 11.4.2-MariaDB'; }
    mysqld() { printf '%s\n' 'mysqld Ver 8.4.0 for Linux'; }
    if detect_installed_database; then fail 'ambiguous installed daemons were guessed'; fi
)
(
    # A data marker alone cannot prove which system service launches which
    # daemon when both engines are installed.
    db_query_root() { return 1; }
    have_cmd() { [[ "$1" == mariadbd || "$1" == mysqld ]]; }
    mariadbd() { printf '%s\n' 'mariadbd Ver 11.4.2-MariaDB'; }
    mysqld() { printf '%s\n' 'mysqld Ver 8.4.0 for Linux'; }
    database_datadir_engine_hint() { printf '%s\n' mysql; }
    if detect_installed_database; then fail 'data marker bypassed ambiguous service detection'; fi
)
(
    # A safety refusal from detection must propagate through both installation
    # entry points instead of being treated as "database not installed".
    detect_installed_database() { return 2; }
    select_db_service() { fail 'selected a service after a safety refusal'; }
    initialize_database_datadir() { fail 'initialized data after a safety refusal'; }
    database_datadir_has_data() { fail 'continued after a safety refusal'; }
    if configure_database; then fail 'ambiguous configure path was accepted'; fi
    install_mysql() { fail 'ambiguous host attempted a package install'; }
    install_mariadb() { fail 'ambiguous host attempted a package install'; }
    if install_local_database; then fail 'ambiguous local install path was accepted'; fi
)
(
    db_query_root() { return 1; }
    have_cmd() { [[ "$1" == mysqld ]]; }
    mysqld() { return 1; }
    install_mysql() { fail 'unreadable daemon version allowed package replacement'; }
    if install_local_database; then fail 'unreadable daemon version was treated as absent'; fi
)
(
    # A stopped/partially configured daemon must not start against the other
    # engine's existing data directory just because only one binary is visible.
    DB_TYPE=mysql
    database_datadir_engine_hint() { printf '%s\n' mariadb; }
    if initialize_database_datadir; then fail 'cross-engine data directory was accepted'; fi
)
(
    # A native service manager failure must not leave a configured database
    # running without boot enablement.
    SERVICE_MANAGER=systemd
    DB_TYPE=mysql
    DB_SERVICE=mysql
    DB_PASSWORD=disposable
    detect_installed_database() { DB_TYPE=mysql; return 0; }
    select_db_service() { DB_SERVICE=mysql; }
    initialize_database_datadir() { return 0; }
    apply_database_config() { DB_CONFIG_CHANGED=false; return 0; }
    service_enable() { return 1; }
    if configure_database; then fail 'database service enable failure was ignored'; fi
)
(
    # Engine-specific data-directory initialization failures must be visible;
    # silently continuing would later start against an incomplete directory.
    OS_FAMILY=arch
    DB_TYPE=mariadb
    database_datadir_engine_hint() { return 1; }
    database_datadir_has_data() { return 1; }
    have_cmd() { [[ "$1" == mariadb-install-db ]]; }
    mariadb-install-db() { return 1; }
    if initialize_database_datadir; then fail 'Arch MariaDB init failure was ignored'; fi
)
(
    # Alpine's init script fallback has the same fail-closed contract.
    OS_FAMILY=alpine
    DB_TYPE=mariadb
    database_datadir_engine_hint() { return 1; }
    database_datadir_has_data() { return 1; }
    have_cmd() { [[ "$1" == mysql_install_db ]]; }
    mysql_install_db() { return 1; }
    if initialize_database_datadir; then fail 'Alpine MariaDB init failure was ignored'; fi
)
echo 'Database install safety tests passed (14 scenarios).'
