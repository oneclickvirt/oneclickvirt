package profile

import (
	"context"
	"errors"
	"fmt"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/auth"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/database"
	"time"

	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// Service 处理用户资料和管理相关功能
type Service struct{}

// NewService 创建用户资料服务
func NewService() *Service {
	return &Service{}
}

// UpdateProfile 更新用户资料
func (s *Service) UpdateProfile(userID uint, req userModel.UpdateProfileRequest) error {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return err
	}

	user.Nickname = req.Nickname
	user.Email = req.Email
	user.Phone = req.Phone
	user.Telegram = req.Telegram

	// 使用数据库抽象层保存
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&user).Error
	})
}

// ChangePassword 修改密码
func (s *Service) ChangePassword(userID uint, oldPassword, newPassword string) error {
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return err
	}

	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(oldPassword)); err != nil {
		return errors.New("原密码错误")
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}

	return global.APP_DB.Model(&user).Update("password", string(hashedPassword)).Error
}

// BatchDeleteUsers 批量删除用户
func (s *Service) BatchDeleteUsers(userIDs []uint) (map[string]interface{}, error) {
	result := map[string]interface{}{
		"success":      []uint{},
		"failed":       []map[string]interface{}{},
		"total":        len(userIDs),
		"successCount": 0,
		"failedCount":  0,
	}

	// 批量查询用户信息
	var users []userModel.User
	if err := global.APP_DB.Select("id, username, email, level, status, created_at").
		Where("id IN ?", userIDs).
		Limit(1000).
		Find(&users).Error; err != nil {
		return nil, fmt.Errorf("查询用户信息失败: %w", err)
	}

	// 将用户按ID映射
	userMap := make(map[uint]userModel.User)
	for _, user := range users {
		userMap[user.ID] = user
	}

	// 批量统计实例数量
	type InstanceCountResult struct {
		UserID        uint
		InstanceCount int64
	}
	var countResults []InstanceCountResult
	global.APP_DB.Model(&providerModel.Instance{}).
		Select("user_id, COUNT(*) as instance_count").
		Where("user_id IN ?", userIDs).
		Group("user_id").
		Scan(&countResults)

	// 将实例数量按user_id映射
	instanceCountMap := make(map[uint]int64)
	for _, result := range countResults {
		instanceCountMap[result.UserID] = result.InstanceCount
	}

	for _, userID := range userIDs {
		// 从预查询的map中检查用户是否存在
		user, userExists := userMap[userID]
		if !userExists {
			result["failed"] = append(result["failed"].([]map[string]interface{}), map[string]interface{}{
				"id":    userID,
				"error": "用户不存在",
			})
			result["failedCount"] = result["failedCount"].(int) + 1
			continue
		}

		// 从预统计的map中检查是否有关联的实例
		instanceCount := instanceCountMap[userID]
		if instanceCount > 0 {
			result["failed"] = append(result["failed"].([]map[string]interface{}), map[string]interface{}{
				"id":    userID,
				"error": "用户还有关联的实例，无法删除",
			})
			result["failedCount"] = result["failedCount"].(int) + 1
			continue
		}

		// 使用数据库抽象层删除用户及相关数据
		dbService := database.GetDatabaseService()
		err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
			// 删除用户角色关联
			if err := tx.Where("user_id = ?", userID).Delete(&userModel.UserRole{}).Error; err != nil {
				return fmt.Errorf("删除用户角色关联失败: %w", err)
			}

			// 删除用户
			if err := tx.Delete(&user).Error; err != nil {
				return fmt.Errorf("删除用户失败: %w", err)
			}

			return nil
		})

		if err != nil {
			result["failed"] = append(result["failed"].([]map[string]interface{}), map[string]interface{}{
				"id":    userID,
				"error": err.Error(),
			})
			result["failedCount"] = result["failedCount"].(int) + 1
			continue
		}
		result["success"] = append(result["success"].([]uint), userID)
		result["successCount"] = result["successCount"].(int) + 1
	}

	return result, nil
}

// SearchUsers 搜索用户
func (s *Service) SearchUsers(req auth.SearchUsersRequest) ([]userModel.User, int64, error) {
	db := global.APP_DB.Model(&userModel.User{})

	// 关键词搜索
	if req.Keyword != "" {
		db = db.Where("username LIKE ? OR nickname LIKE ? OR email LIKE ?",
			"%"+req.Keyword+"%", "%"+req.Keyword+"%", "%"+req.Keyword+"%")
	}

	// 状态过滤
	if req.Status != nil {
		db = db.Where("status = ?", *req.Status)
	}

	// 角色过滤
	if req.RoleID != nil {
		db = db.Joins("JOIN role_users ON users.id = role_users.user_id").
			Where("role_users.role_id = ?", *req.RoleID)
	}

	// 时间范围过滤
	if req.StartTime != "" {
		db = db.Where("created_at >= ?", req.StartTime)
	}
	if req.EndTime != "" {
		db = db.Where("created_at <= ?", req.EndTime)
	}

	// 排序
	orderBy := "created_at DESC"
	if req.SortBy != "" {
		direction := "ASC"
		if req.SortOrder == "desc" {
			direction = "DESC"
		}
		orderBy = fmt.Sprintf("%s %s", req.SortBy, direction)
	}
	db = db.Order(orderBy)

	// 获取总数
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, errors.New("统计用户数量失败")
	}

	// 分页查询
	var users []userModel.User
	offset := (req.Page - 1) * req.PageSize
	if err := db.Offset(offset).Limit(req.PageSize).Preload("Roles").Find(&users).Error; err != nil {
		return nil, 0, errors.New("查询用户失败")
	}

	return users, total, nil
}

// GetUserTasks 获取用户任务列表
func (s *Service) GetUserTasks(userID uint, req userModel.UserTasksRequest) ([]userModel.UserTaskResponse, int64, error) {
	var tasks []adminModel.Task
	var total int64

	// 构建查询条件
	query := global.APP_DB.Model(&adminModel.Task{}).Where("user_id = ?", userID)

	// 节点筛选
	if req.ProviderId != 0 {
		query = query.Where("provider_id = ?", req.ProviderId)
	}

	// 状态筛选
	if req.Status != "" {
		query = query.Where("status = ?", req.Status)
	}

	// 任务类型筛选
	if req.TaskType != "" {
		query = query.Where("task_type = ?", req.TaskType)
	}

	// 统计总数
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("统计任务数量失败: %v", err)
	}

	// 设置分页默认值
	page := req.Page
	pageSize := req.PageSize
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 10
	}

	// 分页查询
	offset := (page - 1) * pageSize
	if err := query.Order("created_at DESC").Offset(offset).Limit(pageSize).Find(&tasks).Error; err != nil {
		return nil, 0, fmt.Errorf("查询用户任务失败: %v", err)
	}

	// 批量查询关联的实例和Provider信息
	var instanceIDs []uint
	var providerIDs []uint
	instanceIDSet := make(map[uint]bool)
	providerIDSet := make(map[uint]bool)

	for _, task := range tasks {
		if task.InstanceID != nil && !instanceIDSet[*task.InstanceID] {
			instanceIDs = append(instanceIDs, *task.InstanceID)
			instanceIDSet[*task.InstanceID] = true
		}
		if task.ProviderID != nil && !providerIDSet[*task.ProviderID] {
			providerIDs = append(providerIDs, *task.ProviderID)
			providerIDSet[*task.ProviderID] = true
		}
	}

	// 批量查询所有涉及的 provider 的任务（用于计算排队信息）
	providerTasksMap := make(map[uint][]adminModel.Task)
	if len(providerIDs) > 0 {
		var allProviderTasks []adminModel.Task
		if err := global.APP_DB.Select("id", "provider_id", "status", "created_at", "estimated_duration", "started_at").
			Where("provider_id IN ? AND status IN (?, ?)", providerIDs, "pending", "running").
			Order("provider_id ASC, created_at ASC").
			Find(&allProviderTasks).Error; err == nil {
			// 按 provider_id 分组
			for _, pt := range allProviderTasks {
				if pt.ProviderID != nil {
					providerTasksMap[*pt.ProviderID] = append(providerTasksMap[*pt.ProviderID], pt)
				}
			}
		}
	}

	// 批量查询实例信息
	var instances []providerModel.Instance
	instanceMap := make(map[uint]providerModel.Instance)
	if len(instanceIDs) > 0 {
		global.APP_DB.Select("id, name, status, instance_type, provider_id").
			Where("id IN ?", instanceIDs).
			Limit(1000).
			Find(&instances)
		for _, instance := range instances {
			instanceMap[instance.ID] = instance
		}
	}

	// 批量查询Provider信息
	var providers []providerModel.Provider
	providerMap := make(map[uint]providerModel.Provider)
	if len(providerIDs) > 0 {
		global.APP_DB.Select("id, name, type, region").
			Where("id IN ?", providerIDs).
			Limit(1000).
			Find(&providers)
		for _, provider := range providers {
			providerMap[provider.ID] = provider
		}
	}

	// 转换为响应格式
	var taskResponses []userModel.UserTaskResponse
	for _, task := range tasks {
		taskResponse := userModel.UserTaskResponse{
			ID:                    task.ID,
			UUID:                  task.UUID,
			TaskType:              task.TaskType,
			Status:                task.Status,
			Progress:              task.Progress,
			ErrorMessage:          task.ErrorMessage,
			CancelReason:          task.CancelReason,
			StatusMessage:         task.StatusMessage,
			TimeoutDuration:       task.TimeoutDuration,
			IsForceStoppable:      task.IsForceStoppable,
			CreatedAt:             task.CreatedAt,
			UpdatedAt:             task.UpdatedAt,
			PreallocatedCPU:       task.PreallocatedCPU,
			PreallocatedMemory:    task.PreallocatedMemory,
			PreallocatedDisk:      task.PreallocatedDisk,
			PreallocatedBandwidth: task.PreallocatedBandwidth,
		}

		// 设置开始时间和完成时间
		if task.StartedAt != nil {
			taskResponse.StartedAt = task.StartedAt
		}
		if task.CompletedAt != nil {
			taskResponse.CompletedAt = task.CompletedAt
		}

		// 从预加载的map中获取实例信息
		if task.InstanceID != nil {
			if instance, ok := instanceMap[*task.InstanceID]; ok {
				taskResponse.InstanceName = instance.Name
				taskResponse.InstanceID = task.InstanceID
				taskResponse.InstanceType = instance.InstanceType
			}
		}

		// 从预加载的map中获取Provider信息
		if task.ProviderID != nil {
			if provider, ok := providerMap[*task.ProviderID]; ok {
				taskResponse.ProviderName = provider.Name
				taskResponse.ProviderId = *task.ProviderID
			}
		}

		// 计算剩余时间
		if task.Status == "running" && task.StartedAt != nil {
			elapsed := time.Since(*task.StartedAt).Seconds()
			remaining := float64(task.TimeoutDuration) - elapsed
			if remaining > 0 {
				taskResponse.RemainingTime = int(remaining)
			}
		}

		// 计算排队信息
		if task.ProviderID != nil && task.Status == "pending" {
			if providerTasks, exists := providerTasksMap[*task.ProviderID]; exists {
				queuePosition := -1
				estimatedWaitTime := 0

				// 找到当前任务在队列中的位置
				pendingIndex := 0
				for _, pt := range providerTasks {
					if pt.Status == "pending" {
						if pt.ID == task.ID {
							queuePosition = pendingIndex

							// 计算预计等待时间：所有 running 任务的剩余时间
							for _, rpt := range providerTasks {
								if rpt.Status == "running" || rpt.Status == "processing" {
									if rpt.StartedAt != nil {
										elapsed := time.Since(*rpt.StartedAt).Seconds()
										remaining := float64(rpt.EstimatedDuration) - elapsed
										if remaining > 0 {
											estimatedWaitTime += int(remaining)
										}
									} else {
										estimatedWaitTime += rpt.EstimatedDuration
									}
								}
							}

							// 前面所有 pending 任务的预计执行时长
							for _, ppt := range providerTasks {
								if ppt.Status == "pending" {
									if ppt.ID == task.ID {
										break
									}
									estimatedWaitTime += ppt.EstimatedDuration
								}
							}
							break
						}
						pendingIndex++
					}
				}

				taskResponse.QueuePosition = queuePosition
				taskResponse.EstimatedWaitTime = estimatedWaitTime
			}
		}

		// 检查是否可以取消
		taskResponse.CanCancel = task.IsForceStoppable &&
			(task.Status == "pending" || task.Status == "running")

		taskResponses = append(taskResponses, taskResponse)
	}

	return taskResponses, total, nil
}

// CancelUserTask 取消用户任务
func (s *Service) CancelUserTask(userID, taskID uint) error {
	taskService := getTaskService()
	return taskService.CancelTask(taskID, userID)
}

// 获取任务服务的辅助函数
func getTaskService() interface {
	CancelTask(taskID uint, userID uint) error
} {
	return &realTaskService{}
}

type realTaskService struct{}

func (ts *realTaskService) CancelTask(taskID uint, userID uint) error {
	// 验证任务所有权
	var task adminModel.Task
	if err := global.APP_DB.Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("任务不存在或无权限")
		}
		return fmt.Errorf("查询任务失败: %v", err)
	}

	// 检查任务是否可以取消
	if task.Status == "completed" || task.Status == "failed" || task.Status == "cancelled" {
		return fmt.Errorf("任务已结束，无法取消")
	}

	if !task.IsForceStoppable {
		return fmt.Errorf("此任务不允许强制停止")
	}

	// 更新任务状态为取消
	now := time.Now()
	err := global.APP_DB.Model(&task).Updates(map[string]interface{}{
		"status":        "cancelled",
		"completed_at":  &now,
		"error_message": "用户主动取消",
	}).Error

	if err != nil {
		return fmt.Errorf("取消任务失败: %v", err)
	}

	// 释放并发控制锁
	if global.APP_TASK_LOCK_RELEASER != nil {
		global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
	}

	global.APP_LOG.Info("用户取消任务",
		zap.Uint("taskId", taskID),
		zap.Uint("userId", userID),
		zap.String("taskType", task.TaskType))

	return nil
}
