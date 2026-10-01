package task

import (
	"encoding/json"
	"fmt"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"oneclickvirt/constant"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
)

// reserveInstanceOperationInTx is shared by all CreateTask callers. The same
// instance row lock is used by the admin batch API. The task and its busy state
// become visible together, including for password operations with no busy state.
func reserveInstanceOperationInTx(tx *gorm.DB, task *adminModel.Task) error {
	if task.InstanceID == nil {
		return nil
	}
	statuses := map[string]string{"start": "starting", "stop": "stopping", "restart": "restarting", "reset": "resetting", "rebuild": "rebuilding", "delete": "deleting", "reset-password": ""}
	next, applies := statuses[task.TaskType]
	if !applies {
		return nil
	}
	var instance providerModel.Instance
	if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&instance, *task.InstanceID).Error; err != nil {
		return err
	}
	if task.ProviderID == nil || *task.ProviderID != instance.ProviderID || task.UserID != instance.UserID {
		return fmt.Errorf("实例归属已变化，请刷新后重试")
	}
	if constant.IsBusyStatus(instance.Status) || instance.Status == constant.InstanceStatusDeleted {
		return fmt.Errorf("实例正在操作或已删除，请刷新后重试")
	}
	var count int64
	if err := tx.Model(&adminModel.Task{}).Where("instance_id = ? AND status IN ?", instance.ID, []string{mainTaskStatusPending, mainTaskStatusProcessing, mainTaskStatusRunning, mainTaskStatusCancelling}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return fmt.Errorf("实例已有任务正在进行")
	}
	switch task.TaskType {
	case "start":
		if instance.Status != "stopped" {
			return fmt.Errorf("实例状态不允许启动")
		}
	case "stop", "restart", "reset-password":
		if instance.Status != "running" {
			return fmt.Errorf("实例状态不允许此操作")
		}
	case "reset", "rebuild":
		if instance.Status != "running" && instance.Status != "stopped" {
			return fmt.Errorf("实例状态不允许重置")
		}
	}
	data := map[string]interface{}{}
	if err := json.Unmarshal([]byte(task.TaskData), &data); err != nil {
		return err
	}
	data["originalStatus"] = instance.Status
	data["originalDesiredState"] = instance.DesiredState
	encoded, err := json.Marshal(data)
	if err != nil {
		return err
	}
	task.TaskData = string(encoded)
	if task.TaskType == "delete" && data["adminOperation"] == true {
		task.IsForceStoppable = false
	}
	if next == "" {
		return nil
	}
	updates := map[string]interface{}{"status": next}
	switch task.TaskType {
	case "start", "restart":
		updates["desired_state"] = providerModel.InstanceDesiredStateRunning
	case "stop":
		updates["desired_state"] = providerModel.InstanceDesiredStateStopped
	}
	return tx.Model(&instance).Updates(updates).Error
}
