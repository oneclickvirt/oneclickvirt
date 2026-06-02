package instance

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/service/task"
	trafficService "oneclickvirt/service/traffic"

	"go.uber.org/zap"
)

// GetUserTrafficUsageWithPmacct 获取用户流量使用情况（使用pmacct数据）
func (s *Service) GetUserTrafficUsageWithPmacct(userID uint) (map[string]interface{}, error) {
	// 获取用户流量使用情况
	var user userModel.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return nil, err
	}

	// 获取用户流量限制
	tService := trafficService.NewService()
	trafficLimit := tService.GetUserTrafficLimitByLevel(user.Level)

	// 简化的流量使用查询（包含已删除实例，保证累计值准确）
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用统一的流量查询服务（从pmacct_traffic_records实时聚合）
	queryService := trafficService.NewQueryService()
	monthlyStats, err := queryService.GetUserMonthlyTraffic(userID, year, month)
	if err != nil {
		return nil, err
	}

	totalUsed := int64(monthlyStats.ActualUsageMB)

	return map[string]interface{}{
		"used":      totalUsed,
		"limit":     trafficLimit,
		"remaining": trafficLimit - totalUsed,
	}, nil
}

// HasInstanceAccess 检查用户是否有权限访问实例
func (s *Service) HasInstanceAccess(userID, instanceID uint) bool {
	// 通过查询实例是否属于该用户来验证权限
	count := int64(0)
	err := global.APP_DB.Model(&providerModel.Instance{}).Where("id = ? AND user_id = ?", instanceID, userID).Count(&count).Error
	return err == nil && count > 0
}

// ResetInstancePassword 重置实例密码
func (s *Service) ResetInstancePassword(userID uint, instanceID uint) (uint, error) {
	// 验证实例所有权
	if !s.HasInstanceAccess(userID, instanceID) {
		return 0, errors.New("无权限访问此实例")
	}

	lk := getInstanceActionLock(instanceID)
	lk.mu.Lock()
	defer func() {
		lk.mu.Unlock()
		releaseInstanceActionLock(instanceID)
	}()

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return 0, fmt.Errorf("实例不存在: %w", err)
	}

	// 检查实例状态
	if instance.Status != "running" {
		return 0, errors.New("只有运行中的实例才能重置密码")
	}
	if err := s.ensureNoActiveInstanceTask(instance.ID); err != nil {
		return 0, err
	}

	// 创建重置密码任务
	taskService := task.GetTaskService()
	taskData := fmt.Sprintf(`{"instanceId":%d,"providerId":%d}`, instanceID, instance.ProviderID)
	taskModel, err := taskService.CreateTask(userID, &instance.ProviderID, &instanceID, "reset-password", taskData, 1800)
	if err != nil {
		return 0, fmt.Errorf("创建重置密码任务失败: %w", err)
	}

	global.APP_LOG.Info("用户创建实例密码重置任务",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", instanceID),
		zap.Uint("taskID", taskModel.ID))

	return taskModel.ID, nil
}

// GetInstanceNewPassword 获取实例新密码
func (s *Service) GetInstanceNewPassword(userID uint, instanceID uint, taskID uint) (string, int64, error) {
	// 验证实例所有权
	if !s.HasInstanceAccess(userID, instanceID) {
		return "", 0, errors.New("无权限访问此实例")
	}

	// 获取任务信息
	var taskModel adminModel.Task
	if err := global.APP_DB.Where("id = ? AND user_id = ? AND instance_id = ?", taskID, userID, instanceID).First(&taskModel).Error; err != nil {
		return "", 0, fmt.Errorf("任务不存在或无权限: %w", err)
	}

	// 检查任务类型
	if taskModel.TaskType != "reset-password" {
		return "", 0, errors.New("任务类型不正确")
	}

	// 检查任务状态
	if taskModel.Status != "completed" {
		return "", 0, errors.New("密码重置任务尚未完成")
	}

	// 解析任务结果获取新密码
	var taskResult map[string]interface{}
	if err := json.Unmarshal([]byte(taskModel.TaskData), &taskResult); err != nil {
		return "", 0, fmt.Errorf("解析任务结果失败: %w", err)
	}

	newPassword, exists := taskResult["newPassword"].(string)
	if !exists || newPassword == "" {
		return "", 0, errors.New("任务结果中未找到新密码")
	}

	// 获取重置时间
	var resetTime int64
	if resetTimeFloat, ok := taskResult["resetTime"].(float64); ok {
		resetTime = int64(resetTimeFloat)
	} else {
		// 如果没有重置时间，使用任务完成时间
		if taskModel.CompletedAt != nil {
			resetTime = taskModel.CompletedAt.Unix()
		}
	}

	global.APP_LOG.Info("用户获取实例新密码",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", instanceID),
		zap.Uint("taskID", taskID))

	return newPassword, resetTime, nil
}
