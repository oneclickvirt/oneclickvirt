// Package dbcompat provides runtime MySQL/MariaDB dialect detection for ON DUPLICATE KEY UPDATE compatibility.
//
// MySQL 9.0 removed the VALUES() function in ON DUPLICATE KEY UPDATE clauses.
// MariaDB (all versions) does NOT support the MySQL 8.0.19+ row-alias syntax.
//
// Compatibility matrix:
//   - MariaDB any version:    VALUES(col) ✓  row-alias ✗
//   - MySQL < 9.0:            VALUES(col) ✓  row-alias ✗ (or partial 8.0.19+)
//   - MySQL 9.0+:             VALUES(col) ✗  row-alias ✓
//
// Usage: call Init(db) once at startup, then use Exec() or UseRowAlias() per statement.
package dbcompat

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
)

// useRowAlias is true only on MySQL 9.0+ where VALUES() in ON DUPLICATE KEY UPDATE is removed.
var useRowAlias bool

// Init detects the database dialect once at startup via SELECT VERSION().
// Must be called after the GORM db handle is connected.
func Init(db *gorm.DB) {
	var version string
	db.Raw("SELECT VERSION()").Scan(&version)
	lower := strings.ToLower(version)
	// MariaDB reports e.g. "10.11.4-MariaDB" — never needs row-alias.
	if strings.Contains(lower, "mariadb") {
		useRowAlias = false
		return
	}
	// MySQL version string is e.g. "9.0.1" or "8.0.36".
	var major int
	parts := strings.SplitN(version, ".", 2)
	if len(parts) > 0 {
		fmt.Sscanf(parts[0], "%d", &major)
	}
	useRowAlias = major >= 9
}

// UseRowAlias reports whether MySQL 9.0+ row-alias syntax is required.
// When true, use  "VALUES ... AS _new_row  ON DUPLICATE KEY UPDATE col = _new_row.col".
// When false, use "VALUES ...              ON DUPLICATE KEY UPDATE col = VALUES(col)".
func UseRowAlias() bool { return useRowAlias }

// Exec runs one of two SQL variants depending on the detected dialect.
//
//   - valuesSQL   — SQL using legacy   VALUES(col)  syntax (MariaDB / MySQL < 9)
//   - rowAliasSQL — SQL using new      _src.col     syntax (MySQL 9.0+)
//
// The same args slice is forwarded unchanged to whichever SQL is chosen.
func Exec(db *gorm.DB, valuesSQL, rowAliasSQL string, args ...interface{}) *gorm.DB {
	if useRowAlias {
		return db.Exec(rowAliasSQL, args...)
	}
	return db.Exec(valuesSQL, args...)
}
