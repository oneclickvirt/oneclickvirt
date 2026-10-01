package task

import (
	"fmt"
	"strings"

	"gorm.io/gorm"
	providerModel "oneclickvirt/model/provider"
)

func transferResetIPv4BindingInTx(tx *gorm.DB, providerID, oldInstanceID, newInstanceID, bindingID uint, address string) error {
	if tx == nil || providerID == 0 || oldInstanceID == 0 || newInstanceID == 0 || bindingID == 0 || strings.TrimSpace(address) == "" {
		return fmt.Errorf("重置IPv4绑定迁移参数无效")
	}
	result := tx.Model(&providerModel.ProviderIPv4Pool{}).
		Where("id = ? AND provider_id = ? AND instance_id = ? AND address = ? AND is_allocated = ? AND deleted_at IS NULL",
			bindingID, providerID, oldInstanceID, strings.TrimSpace(address), true).
		Update("instance_id", newInstanceID)
	if result.Error != nil {
		return fmt.Errorf("迁移重置实例IPv4地址绑定失败: %w", result.Error)
	}
	if result.RowsAffected != 1 {
		return fmt.Errorf("实例IPv4地址绑定在重建期间发生并发变化")
	}
	return nil
}
