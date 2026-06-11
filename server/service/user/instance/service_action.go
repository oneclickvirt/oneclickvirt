package instance

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/cache"
	"oneclickvirt/service/database"

	"gorm.io/gorm"
)

// instanceActionMu 每实例操作互斥锁，防止并发操作同一实例产生重复任务（TOCTOU）
// 使用引用计数确保同一时刻只有一个goroutine持有某实例的锁，同时正确回收内存
type instanceLockEntry struct {
	mu    sync.Mutex
	count int
}

var (
	instanceLocksMu sync.Mutex
	instanceLocks   = make(map[uint]*instanceLockEntry)
)

func getInstanceActionLock(instanceID uint) *instanceLockEntry {
	instanceLocksMu.Lock()
	lk := instanceLocks[instanceID]
	if lk == nil {
		lk = &instanceLockEntry{}
		instanceLocks[instanceID] = lk
	}
	lk.count++
	instanceLocksMu.Unlock()
	return lk
}

func releaseInstanceActionLock(instanceID uint) {
	instanceLocksMu.Lock()
	lk := instanceLocks[instanceID]
	if lk != nil {
		lk.count--
		if lk.count == 0 {
			delete(instanceLocks, instanceID)
		}
	}
	instanceLocksMu.Unlock()
}

func (s *Service) ensureNoActiveInstanceTask(instanceID uint) error {
	activeTypes := []string{"start", "stop", "restart", "reset", "rebuild", "delete", "reset-password"}
	var existingTask adminModel.Task
	err := global.APP_DB.Where("instance_id = ? AND task_type IN ? AND status IN ?", instanceID, activeTypes, []string{"pending", "processing", "running", "cancelling"}).
		First(&existingTask).Error
	if err == nil {
		return fmt.Errorf("实例已有%s任务正在进行", existingTask.TaskType)
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	return err
}

// InstanceAction 执行实例操作
func (s *Service) InstanceAction(userID uint, req userModel.InstanceActionRequest) error {
	// 对同一实例加锁，防止并发请求同时通过"已有任务"检查后各自创建任务（TOCTOU）
	lk := getInstanceActionLock(req.InstanceID)
	lk.mu.Lock()
	defer func() {
		lk.mu.Unlock()
		releaseInstanceActionLock(req.InstanceID)
	}()

	var instance providerModel.Instance
	if err := global.APP_DB.Where("id = ? AND user_id = ?", req.InstanceID, userID).First(&instance).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("实例不存在或无权限")
		}
		return err
	}

	// 操作完成后使缓存失效
	defer func() {
		cacheService := cache.GetUserCacheService()
		cacheService.InvalidateUserCache(userID)
		cacheService.InvalidateInstanceCache(req.InstanceID)
	}()
	if err := s.ensureNoActiveInstanceTask(instance.ID); err != nil {
		return err
	}
	if instance.IsFrozen && req.Action != "delete" {
		return errors.New("实例已被冻结，仅允许删除操作")
	}
	if instance.ExpiresAt != nil && instance.ExpiresAt.Before(time.Now()) && req.Action != "delete" {
		return errors.New("实例已到期，仅允许删除操作")
	}

	switch req.Action {
	case "start":
		if instance.Status != "stopped" {
			return errors.New("实例状态不允许启动")
		}

		// 禁止流量超限的实例被用户启动
		if instance.TrafficLimited {
			return errors.New("实例因流量超限已被系统限制，普通用户无法启动，请联系管理员重置流量或增加流量额度")
		}

		// 检查是否已有进行中的启动任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'start' AND status IN ('pending', 'processing', 'running', 'cancelling')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有启动任务正在进行")
		}

		// 创建启动任务
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "start", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建启动任务失败: %v", err)
		}

		instance.Status = "starting"
	case "stop":
		if instance.Status != "running" {
			return errors.New("实例状态不允许停止")
		}

		// 检查是否已有进行中的停止任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'stop' AND status IN ('pending', 'processing', 'running', 'cancelling')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有停止任务正在进行")
		}

		// 创建停止任务
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "stop", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建停止任务失败: %v", err)
		}

		instance.Status = "stopping"
	case "restart":
		if instance.Status != "running" {
			return errors.New("实例状态不允许重启")
		}

		// 检查是否已有进行中的重启任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'restart' AND status IN ('pending', 'processing', 'running', 'cancelling')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有重启任务正在进行")
		}

		// 创建重启任务
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "restart", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建重启任务失败: %v", err)
		}

		instance.Status = "restarting"
	case "reset":
		if instance.Status != "running" && instance.Status != "stopped" {
			return errors.New("实例状态不允许重置")
		}

		// 检查用户重置权限
		permissionService := auth.PermissionService{}
		if !permissionService.CheckInstanceResetPermission(userID, instance.InstanceType) {
			return errors.New("您的等级不足，无法自行重置系统，请联系管理员处理")
		}

		// 检查是否已有进行中的重置任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'reset' AND status IN ('pending', 'processing', 'running', 'cancelling')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有重置任务正在进行")
		}

		// 创建重置任务，记录原始状态
		originalStatus := instance.Status
		taskService := getTaskService()
		resetImage := instance.Image
		if req.Image != "" {
			resetImage = req.Image
		}
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d,"originalStatus":"%s","resetImage":"%s"}`, instance.ID, instance.ProviderID, originalStatus, resetImage)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "reset", taskData, 1800)
		if err != nil {
			return fmt.Errorf("创建重置任务失败: %v", err)
		}

		instance.Status = "resetting"
	case "delete":
		if instance.Status == "deleting" {
			return errors.New("实例正在删除中")
		}

		// 检查用户删除权限
		permissionService := auth.PermissionService{}
		if !permissionService.CheckInstanceDeletePermission(userID, instance.InstanceType) {
			return errors.New("您的等级不足，无法自行删除实例，请联系管理员处理")
		}

		// 检查是否已有进行中的删除任务
		var existingTask adminModel.Task
		if err := global.APP_DB.Where("instance_id = ? AND task_type = 'delete' AND status IN ('pending', 'processing', 'running', 'cancelling')", instance.ID).First(&existingTask).Error; err == nil {
			return errors.New("实例已有删除任务正在进行")
		}

		// 创建删除任务
		taskService := getTaskService()
		taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instance.ID, instance.ProviderID)
		_, err := taskService.CreateTask(userID, &instance.ProviderID, &instance.ID, "delete", taskData, 0)
		if err != nil {
			return fmt.Errorf("创建删除任务失败: %v", err)
		}

		instance.Status = "deleting"
	default:
		return errors.New("不支持的操作")
	}

	// 使用数据库抽象层保存
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&instance).Error
	})
}

func (s *Service) BatchInstanceAction(userID uint, req userModel.BatchInstanceActionRequest) userModel.BatchInstanceActionResponse {
	response := userModel.BatchInstanceActionResponse{
		Action:  req.Action,
		Total:   len(req.InstanceIDs),
		Results: make([]userModel.BatchInstanceActionResult, 0, len(req.InstanceIDs)),
	}
	seen := make(map[uint]struct{}, len(req.InstanceIDs))
	for _, instanceID := range req.InstanceIDs {
		if instanceID == 0 {
			response.FailCount++
			response.Results = append(response.Results, userModel.BatchInstanceActionResult{
				InstanceID: instanceID,
				Success:    false,
				Error:      "无效的实例ID",
			})
			continue
		}
		if _, exists := seen[instanceID]; exists {
			response.FailCount++
			response.Results = append(response.Results, userModel.BatchInstanceActionResult{
				InstanceID: instanceID,
				Success:    false,
				Error:      "实例ID重复",
			})
			continue
		}
		seen[instanceID] = struct{}{}

		actionReq := userModel.InstanceActionRequest{
			InstanceID: instanceID,
			Action:     req.Action,
			Image:      req.Image,
		}
		if err := s.InstanceAction(userID, actionReq); err != nil {
			response.FailCount++
			response.Results = append(response.Results, userModel.BatchInstanceActionResult{
				InstanceID: instanceID,
				Success:    false,
				Error:      err.Error(),
			})
			continue
		}
		response.SuccessCount++
		response.Results = append(response.Results, userModel.BatchInstanceActionResult{
			InstanceID: instanceID,
			Success:    true,
			Message:    "操作已提交",
		})
	}
	return response
}

// PerformInstanceAction 执行实例操作（兼容原方法名）
func (s *Service) PerformInstanceAction(userID uint, req userModel.InstanceActionRequest) error {
	return s.InstanceAction(userID, req)
}
