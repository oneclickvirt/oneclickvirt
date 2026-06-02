package provider

import (
	"time"

	checkinModel "oneclickvirt/model/checkin"
	providerModel "oneclickvirt/model/provider"

	"gorm.io/gorm"
)

func determineInitialInstanceExpiryInTx(tx *gorm.DB, dbProvider *providerModel.Provider) *time.Time {
	if dbProvider == nil {
		return nil
	}
	if dbProvider.ExpiresAt != nil {
		return dbProvider.ExpiresAt
	}

	var cfg checkinModel.CheckinConfig
	if err := tx.Select("enabled", "default_expire_days").
		Where("provider_id = ? AND enabled = ?", dbProvider.ID, true).
		First(&cfg).Error; err != nil {
		return nil
	}

	days := cfg.DefaultExpireDays
	if days <= 0 {
		days = 7
	}
	expiry := time.Now().Add(time.Duration(days) * 24 * time.Hour)
	return &expiry
}
