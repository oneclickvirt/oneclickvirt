package provider

import (
	"errors"
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

// The caller holds the provider row lock. A locking read sees the most
// recently committed reservation even after an earlier MySQL snapshot read.
func requireUnusedInstanceName(tx *gorm.DB, providerID uint, name string) error {
	var sibling providerModel.Instance
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").
		Where("provider_id = ? AND name = ?", providerID, name).Take(&sibling).Error
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return nil
	case err != nil:
		return err
	default:
		return fmt.Errorf("节点上已存在同名实例 %q", name)
	}
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

// A failed finalization transaction may have rolled back the status change
// while the remote guest already exists. Make the row operable for recovery,
// but retain its allocations and never overwrite a cancelled task or a later
// lifecycle transition.
func quarantineCreateFinalizationFailure(db *gorm.DB, taskID uint, instance *providerModel.Instance) error {
	return db.Transaction(func(tx *gorm.DB) error {
		if err := lockRunningCreate(tx, taskID); err != nil {
			return err
		}
		var current providerModel.Instance
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id", "status").First(&current, instance.ID).Error; err != nil {
			return err
		}
		if current.Status != "creating" {
			return nil
		}
		return quarantineFailedCreate(tx, instance)
	})
}
