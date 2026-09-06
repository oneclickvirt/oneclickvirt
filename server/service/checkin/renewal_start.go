package checkin

import (
	"fmt"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/taskgate"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) queueExpiryRenewalStart(instanceID, userID, providerID uint) error {
	if err := taskgate.EnsureAccepting(); err != nil {
		return err
	}

	activeTaskStatuses := []string{"pending", "processing", "running", "cancelling"}

	taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d,"desiredState":"running"}`, instanceID, providerID)
	if err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := taskgate.EnsureAcceptingInTx(tx); err != nil {
			return err
		}
		var lockedInstance providerModel.Instance
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Select("id, status, expiry_stopped").
			Where("id = ?", instanceID).
			First(&lockedInstance).Error; err != nil {
			return err
		}
		if !lockedInstance.ExpiryStopped {
			return nil
		}

		var activeStartCount int64
		if err := tx.Model(&adminModel.Task{}).
			Where("instance_id = ? AND task_type = ? AND status IN ?", instanceID, "start", activeTaskStatuses).
			Count(&activeStartCount).Error; err != nil {
			return err
		}
		if activeStartCount == 0 {
			task := &adminModel.Task{
				TaskType:         "start",
				Status:           "pending",
				Progress:         0,
				StatusMessage:    "签到续期后自动启动实例",
				TaskData:         taskData,
				UserID:           userID,
				ProviderID:       &providerID,
				InstanceID:       &instanceID,
				TimeoutDuration:  600,
				IsForceStoppable: true,
				CanForceStop:     false,
			}
			if err := tx.Create(task).Error; err != nil {
				return err
			}
		}

		updates := map[string]interface{}{
			"expiry_stopped":    false,
			"expiry_stopped_at": nil,
		}
		if lockedInstance.Status == "stopped" {
			updates["status"] = "starting"
		}
		return tx.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Updates(updates).Error
	}); err != nil {
		return err
	}
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return nil
}
