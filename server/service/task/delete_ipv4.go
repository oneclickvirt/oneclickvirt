package task

import (
	"fmt"
	"gorm.io/gorm"
	providerModel "oneclickvirt/model/provider"
)

func releaseDeletedInstanceIPv4(db *gorm.DB, instanceID uint) error {
	if db == nil || instanceID == 0 {
		return fmt.Errorf("释放独立IPv4缺少数据库或实例身份")
	}
	if err := db.Model(&providerModel.ProviderIPv4Pool{}).
		Where("instance_id = ? AND is_allocated = ? AND deleted_at IS NULL", instanceID, true).
		Updates(map[string]interface{}{"is_allocated": false, "instance_id": nil}).Error; err != nil {
		return fmt.Errorf("释放实例IPv4池绑定失败: %w", err)
	}
	return nil
}
