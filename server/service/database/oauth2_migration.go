package database

import (
	"fmt"

	userModel "oneclickvirt/model/user"

	"gorm.io/gorm"
)

// EnsureUserOAuth2Columns defensively migrates OAuth2 columns on existing users tables.
// GORM AutoMigrate normally adds these fields, but some long-running deployments may
// have skipped an earlier migration; keeping this explicit avoids OAuth2 callbacks
// failing with "Unknown column 'oauth2_avatar'" after upgrades.
func EnsureUserOAuth2Columns(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	migrator := db.Migrator()
	if !migrator.HasTable(&userModel.User{}) {
		return nil
	}

	fields := []string{
		"OAuth2ProviderID",
		"OAuth2UID",
		"OAuth2Username",
		"OAuth2Email",
		"OAuth2Avatar",
		"OAuth2Extra",
	}
	for _, field := range fields {
		if migrator.HasColumn(&userModel.User{}, field) {
			continue
		}
		if err := migrator.AddColumn(&userModel.User{}, field); err != nil {
			return fmt.Errorf("add users.%s column: %w", field, err)
		}
	}
	return nil
}
