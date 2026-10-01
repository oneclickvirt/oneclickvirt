package provider

import (
	"fmt"

	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/resources"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Lock the task before touching its instance or accounting. Cancellation uses
// this same row, so it cannot be overwritten by a stale create callback.
func lockRunningCreate(tx *gorm.DB, taskID uint) error {
	var current adminModel.Task
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").First(&current, taskID).Error; err != nil {
		return err
	}
	if current.Status != "running" {
		return fmt.Errorf("创建任务已停止，当前状态: %s", current.Status)
	}
	return nil
}

// A provider error can leave a live guest behind. Keep IPs, ports and capacity
// reserved until the normal delete task confirms removal from the provider.
func quarantineFailedCreate(tx *gorm.DB, instance *providerModel.Instance) error {
	if err := tx.Model(&providerModel.Instance{}).Where("id = ?", instance.ID).
		Updates(map[string]interface{}{"status": "error", "desired_state": providerModel.InstanceDesiredStateStopped}).Error; err != nil {
		return err
	}
	if err := tx.Model(&providerModel.Port{}).Where("instance_id = ?", instance.ID).Update("status", "deleting").Error; err != nil {
		return err
	}
	if instance.UserID > 0 {
		return resources.NewQuotaService().RecalculateUserQuotaInTx(tx, instance.UserID)
	}
	return nil
}
