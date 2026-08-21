package instance

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/constant"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/cache"
	consoleService "oneclickvirt/service/console"
	"oneclickvirt/service/database"
	"oneclickvirt/service/taskgate"
	"oneclickvirt/utils"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (s *Service) BatchInstanceAction(req adminModel.BatchInstanceActionRequest, ownerAdminID uint) adminModel.BatchInstanceActionResponse {
	failedResponse := func(message string) adminModel.BatchInstanceActionResponse {
		response := adminModel.BatchInstanceActionResponse{
			Action:  req.Action,
			Total:   len(req.InstanceIDs),
			Results: make([]adminModel.BatchInstanceActionResult, 0, len(req.InstanceIDs)),
		}
		for _, instanceID := range req.InstanceIDs {
			response.FailCount++
			response.Results = append(response.Results, adminModel.BatchInstanceActionResult{
				InstanceID: instanceID,
				Error:      message,
			})
		}
		return response
	}

	if len(req.InstanceIDs) == 0 || len(req.InstanceIDs) > 100 {
		return failedResponse("实例数量必须在1到100之间")
	}
	if err := taskgate.EnsureAccepting(); err != nil {
		return failedResponse(err.Error())
	}

	response := adminModel.BatchInstanceActionResponse{
		Action:  req.Action,
		Total:   len(req.InstanceIDs),
		Results: make([]adminModel.BatchInstanceActionResult, 0, len(req.InstanceIDs)),
	}
	acceptedInstances := make([]providerModel.Instance, 0, len(req.InstanceIDs))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := database.GetDatabaseService().ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		response = adminModel.BatchInstanceActionResponse{
			Action:  req.Action,
			Total:   len(req.InstanceIDs),
			Results: make([]adminModel.BatchInstanceActionResult, 0, len(req.InstanceIDs)),
		}
		acceptedInstances = acceptedInstances[:0]

		if err := taskgate.EnsureAcceptingInTx(tx); err != nil {
			return err
		}

		uniqueIDs := make([]uint, 0, len(req.InstanceIDs))
		seen := make(map[uint]struct{}, len(req.InstanceIDs))
		for _, instanceID := range req.InstanceIDs {
			if instanceID == 0 {
				continue
			}
			if _, exists := seen[instanceID]; exists {
				continue
			}
			seen[instanceID] = struct{}{}
			uniqueIDs = append(uniqueIDs, instanceID)
		}

		var instances []providerModel.Instance
		if len(uniqueIDs) > 0 {
			instanceQuery := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("instances.id IN ?", uniqueIDs)
			if ownerAdminID > 0 {
				providerIDs := tx.Model(&providerModel.Provider{}).Select("id").Where("owner_admin_id = ?", ownerAdminID)
				instanceQuery = instanceQuery.Where("instances.provider_id IN (?)", providerIDs)
			}
			if err := instanceQuery.Find(&instances).Error; err != nil {
				return err
			}
		}
		instanceMap := make(map[uint]providerModel.Instance, len(instances))
		for _, instance := range instances {
			instanceMap[instance.ID] = instance
		}

		activeTypes := []string{"create", "create_instance", "create_redemption_instance", "start", "stop", "restart", "reset", "rebuild", "delete", "reset-password"}
		var activeTasks []adminModel.Task
		if len(uniqueIDs) > 0 {
			if err := tx.Select("instance_id, task_type").
				Where("instance_id IN ? AND task_type IN ? AND status IN ?", uniqueIDs, activeTypes, []string{"pending", "processing", "running", "cancelling"}).
				Order("id ASC").
				Find(&activeTasks).Error; err != nil {
				return err
			}
		}
		activeTaskMap := make(map[uint]string, len(activeTasks))
		for _, activeTask := range activeTasks {
			if activeTask.InstanceID == nil {
				continue
			}
			if _, exists := activeTaskMap[*activeTask.InstanceID]; !exists {
				activeTaskMap[*activeTask.InstanceID] = activeTask.TaskType
			}
		}

		tasks := make([]*adminModel.Task, 0, len(uniqueIDs))
		acceptedIDs := make([]uint, 0, len(uniqueIDs))
		seen = make(map[uint]struct{}, len(req.InstanceIDs))
		for _, instanceID := range req.InstanceIDs {
			if instanceID == 0 {
				response.FailCount++
				response.Results = append(response.Results, adminModel.BatchInstanceActionResult{InstanceID: instanceID, Error: "无效的实例ID"})
				continue
			}
			if _, exists := seen[instanceID]; exists {
				response.FailCount++
				response.Results = append(response.Results, adminModel.BatchInstanceActionResult{InstanceID: instanceID, Error: "实例ID重复"})
				continue
			}
			seen[instanceID] = struct{}{}

			instance, exists := instanceMap[instanceID]
			if !exists {
				response.FailCount++
				response.Results = append(response.Results, adminModel.BatchInstanceActionResult{InstanceID: instanceID, Error: "实例不存在或无权操作"})
				continue
			}
			if constant.IsBusyStatus(instance.Status) {
				response.FailCount++
				response.Results = append(response.Results, adminModel.BatchInstanceActionResult{InstanceID: instanceID, Error: fmt.Sprintf("实例正在操作进行中（当前状态：%s）", instance.Status)})
				continue
			}
			if err := validateAdminInstanceAction(instance.Status, req.Action); err != nil {
				response.FailCount++
				response.Results = append(response.Results, adminModel.BatchInstanceActionResult{InstanceID: instanceID, Error: err.Error()})
				continue
			}
			if activeType, exists := activeTaskMap[instanceID]; exists {
				response.FailCount++
				response.Results = append(response.Results, adminModel.BatchInstanceActionResult{InstanceID: instanceID, Error: fmt.Sprintf("实例已有%s任务正在进行", activeType)})
				continue
			}

			taskData := map[string]interface{}{"instanceId": instance.ID, "providerId": instance.ProviderID}
			if req.Action == "reset" || req.Action == "rebuild" {
				taskData["originalStatus"] = instance.Status
			}
			if req.Action == "delete" {
				taskData["adminOperation"] = true
			}
			taskDataJSON, err := json.Marshal(taskData)
			if err != nil {
				return err
			}
			providerID := instance.ProviderID
			id := instance.ID
			tasks = append(tasks, &adminModel.Task{
				UserID:            instance.UserID,
				ProviderID:        &providerID,
				InstanceID:        &id,
				TaskType:          req.Action,
				Status:            "pending",
				TaskData:          string(taskDataJSON),
				TimeoutDuration:   utils.GetDefaultTaskTimeout(req.Action),
				IsForceStoppable:  req.Action != "delete",
				EstimatedDuration: utils.GetEstimatedTaskDuration(req.Action, instance.InstanceType),
			})
			acceptedIDs = append(acceptedIDs, instance.ID)
			acceptedInstances = append(acceptedInstances, instance)
			response.SuccessCount++
			response.Results = append(response.Results, adminModel.BatchInstanceActionResult{InstanceID: instanceID, Success: true, Message: "操作已提交"})
		}

		if len(tasks) == 0 {
			return nil
		}
		if err := tx.CreateInBatches(tasks, 100).Error; err != nil {
			return err
		}
		result := tx.Model(&providerModel.Instance{}).Where("id IN ?", acceptedIDs).Update("status", nextAdminInstanceStatus(req.Action))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != int64(len(acceptedIDs)) {
			return fmt.Errorf("部分实例状态已变更")
		}
		return nil
	})
	if err != nil {
		return failedResponse(err.Error())
	}

	cacheService := cache.GetUserCacheService()
	for _, instance := range acceptedInstances {
		cacheService.InvalidateUserCache(instance.UserID)
		cacheService.InvalidateInstanceCache(instance.ID)
		consoleService.InvalidateInstanceConsoleCaches(instance.ID)
	}
	return response
}
