package system

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"gorm.io/gorm"
)

const SystemInitializedFlagPath = "./storage/.system_initialized"

var ErrDatabaseUnavailable = errors.New("database unavailable")

// IsDatabaseInitialized treats the database as the source of truth. The marker
// file is intentionally excluded because container-local files can outlive a
// broken connection or disappear during an image replacement.
func IsDatabaseInitialized(db *gorm.DB) (bool, error) {
	if db == nil {
		return false, ErrDatabaseUnavailable
	}

	sqlDB, err := db.DB()
	if err != nil {
		return false, fmt.Errorf("%w: get connection: %v", ErrDatabaseUnavailable, err)
	}
	if err := sqlDB.Ping(); err != nil {
		return false, fmt.Errorf("%w: ping: %v", ErrDatabaseUnavailable, err)
	}

	if !db.Migrator().HasTable("users") {
		return false, nil
	}

	var userID uint
	result := db.Table("users").Select("id").Limit(1).Scan(&userID)
	if result.Error != nil {
		return false, fmt.Errorf("query users: %w", result.Error)
	}
	return result.RowsAffected > 0, nil
}

func HasSystemInitializedMarker() bool {
	_, err := os.Stat(SystemInitializedFlagPath)
	return err == nil
}

func EnsureSystemInitializedMarker() error {
	dir := filepath.Dir(SystemInitializedFlagPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create marker directory: %w", err)
	}

	tempFile, err := os.CreateTemp(dir, ".system_initialized-*")
	if err != nil {
		return fmt.Errorf("create temporary marker: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	content := "System initialized at: " + time.Now().Format(time.RFC3339) + "\n"
	if _, err := tempFile.WriteString(content); err != nil {
		tempFile.Close()
		return fmt.Errorf("write marker: %w", err)
	}
	if err := tempFile.Chmod(0644); err != nil {
		tempFile.Close()
		return fmt.Errorf("set marker permissions: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("close marker: %w", err)
	}
	if err := os.Rename(tempPath, SystemInitializedFlagPath); err != nil {
		return fmt.Errorf("publish marker: %w", err)
	}
	return nil
}

func RemoveSystemInitializedMarker() error {
	err := os.Remove(SystemInitializedFlagPath)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("remove marker: %w", err)
}
