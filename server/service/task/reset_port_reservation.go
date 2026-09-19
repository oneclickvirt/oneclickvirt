package task

import (
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	providerModel "oneclickvirt/model/provider"
)

// transferResetPortMappingsInTx runs inside the instance replacement
// transaction, with the provider already locked. Keeping the same rows/IDs
// preserves every mapping field and prevents another allocator taking a port
// while the replacement guest is being created. No network I/O belongs here.
func transferResetPortMappingsInTx(tx *gorm.DB, resetCtx *ResetTaskContext, newInstanceID uint) error {
	if tx == nil || resetCtx == nil || resetCtx.Provider.ID == 0 || resetCtx.OldInstanceID == 0 || newInstanceID == 0 || resetCtx.OldInstanceID == newInstanceID {
		return fmt.Errorf("重建端口迁移参数无效")
	}
	var ports []providerModel.Port
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instance_id = ?", resetCtx.OldInstanceID).Find(&ports).Error; err != nil {
		return fmt.Errorf("锁定旧实例端口失败: %w", err)
	}
	expected := make(map[uint]providerModel.Port, len(resetCtx.OldPortMappings))
	for _, port := range resetCtx.OldPortMappings {
		expected[port.ID] = resetPortDefinition(port)
	}
	for _, port := range ports {
		if port.ProviderID != resetCtx.Provider.ID {
			return fmt.Errorf("端口 %d 的Provider归属不一致", port.ID)
		}
		if port.Status == "active" {
			if want, ok := expected[port.ID]; !ok || want != resetPortDefinition(port) {
				return fmt.Errorf("端口 %d 在重建期间发生并发变化", port.ID)
			}
			delete(expected, port.ID)
		}
	}
	if len(expected) != 0 {
		return fmt.Errorf("旧实例端口在重建期间被删除或停用")
	}
	if len(ports) == 0 {
		return nil
	}
	result := tx.Model(&providerModel.Port{}).Where("instance_id = ? AND provider_id = ?", resetCtx.OldInstanceID, resetCtx.Provider.ID).
		Updates(map[string]interface{}{
			"instance_id": newInstanceID,
			// Do not let Agent recovery bind the old guest's address while the
			// replacement is starting. Inactive/failed mappings retain intent.
			"status": gorm.Expr("CASE WHEN status = 'active' THEN 'restoring' ELSE status END"),
		})
	if result.Error != nil {
		return fmt.Errorf("迁移重建端口失败: %w", result.Error)
	}
	if result.RowsAffected != int64(len(ports)) {
		return fmt.Errorf("迁移重建端口数量不一致")
	}
	return nil
}

func resetPortDefinition(port providerModel.Port) providerModel.Port {
	port.CreatedAt, port.UpdatedAt = time.Time{}, time.Time{}
	return port
}
