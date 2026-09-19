// Package dbcompat selects SQL using the dialect of the connection executing it,
// including transactions. Initialization and reconnects may have different engines
// open concurrently; a process-global engine flag would race and cross those pools.
package dbcompat

import (
	"fmt"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

// Init is retained for startup callers. GORM already detects ServerVersion when
// opening the pool; there is no mutable global dialect state to initialize.
func Init(db *gorm.DB) {}

// UseRowAlias avoids legacy VALUES() syntax on newer MySQL. MariaDB does not
// support MySQL's row-alias syntax. Reading immutable metadata adds no SQL.
func UseRowAlias(db *gorm.DB) bool {
	if db == nil {
		return false
	}
	dialect, ok := db.Dialector.(*mysql.Dialector)
	if !ok || strings.Contains(strings.ToLower(dialect.ServerVersion), "mariadb") {
		return false
	}
	var major int
	fmt.Sscanf(dialect.ServerVersion, "%d.", &major)
	return major >= 9
}

func Exec(db *gorm.DB, valuesSQL, rowAliasSQL string, args ...interface{}) *gorm.DB {
	if UseRowAlias(db) {
		return db.Exec(rowAliasSQL, args...)
	}
	return db.Exec(valuesSQL, args...)
}
