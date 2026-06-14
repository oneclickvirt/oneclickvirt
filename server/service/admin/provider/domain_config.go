package provider

import (
	domainModel "oneclickvirt/model/domain"

	"gorm.io/gorm"
)

func syncProviderDomainConfig(tx *gorm.DB, providerID uint, enabled bool) error {
	var count int64
	if err := tx.Model(&domainModel.DomainConfig{}).
		Where("provider_id = ?", providerID).
		Count(&count).Error; err != nil {
		return err
	}
	if count == 0 {
		return tx.Create(&domainModel.DomainConfig{
			ProviderID:        providerID,
			Enabled:           enabled,
			MaxDomainsPerUser: 3,
			DNSType:           "hosts",
			NginxConfigPath:   "/etc/nginx/conf.d",
			NginxReloadCmd:    "systemctl reload nginx",
		}).Error
	}
	return tx.Model(&domainModel.DomainConfig{}).
		Where("provider_id = ?", providerID).
		Update("enabled", enabled).Error
}
