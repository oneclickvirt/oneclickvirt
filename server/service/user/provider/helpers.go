package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/interfaces"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// updateTaskProgress 更新任务进度（使用全局工具函数）
func (s *Service) updateTaskProgress(taskID uint, progress int, message string) {
	utils.UpdateTaskProgress(taskID, progress, message)
}

// markTaskCompleted 标记任务最终完成（使用全局工具函数）
func (s *Service) markTaskCompleted(taskID uint, message string) {
	utils.MarkTaskCompleted(taskID, message)
}

// markTaskFailed 标记任务失败（使用全局工具函数）
func (s *Service) markTaskFailed(taskID uint, errorMessage string) {
	utils.MarkTaskFailed(taskID, errorMessage)
}

// generateInstanceName 生成实例名称（使用全局工具函数）
func (s *Service) generateInstanceName(providerName string) string {
	return utils.GenerateInstanceName(providerName)
}

// generatePassword 生成随机密码（使用全局工具函数）
func (s *Service) generatePassword() string {
	return utils.GenerateInstancePassword()
}

// extractHost 从endpoint中提取主机地址（使用全局工具函数）
func (s *Service) extractHost(endpoint string) string {
	return utils.ExtractHost(endpoint)
}

// getInstanceDetailsAfterCreation 创建后获取实例详情（使用全局Provider封装）
func (s *Service) getInstanceDetailsAfterCreation(ctx context.Context, instance *providerModel.Instance) (*providerModel.ProviderInstance, error) {
	// 获取Provider实例（使用全局封装函数）
	providerInstance, err := providerService.GetProviderInstanceByID(instance.ProviderID)
	if err != nil {
		return nil, fmt.Errorf("获取Provider实例失败: %w", err)
	}

	// 获取实例详细信息
	actualInstance, err := providerInstance.GetInstance(ctx, instance.Name)
	if err != nil {
		return nil, fmt.Errorf("从Provider获取实例详情失败: %w", err)
	}

	return actualInstance, nil
}

// delayedDeleteFailedInstance 延迟删除失败的实例
func (s *Service) delayedDeleteFailedInstance(instanceID uint) {
	global.APP_LOG.Info("启动延迟删除任务",
		zap.Uint("instanceId", instanceID),
		zap.String("reason", "创建失败自动清理"))

	time.Sleep(10 * time.Second)

	// 使用反射动态导入避免循环依赖问题
	// 导入路径: oneclickvirt/service/admin/instance
	adminInstanceSvc := struct {
		taskService interfaces.TaskServiceInterface
	}{
		taskService: s.taskService,
	}

	// 模拟管理员删除实例的逻辑
	if err := s.executeAdminDeleteInstance(instanceID, adminInstanceSvc.taskService); err != nil {
		global.APP_LOG.Error("延迟删除失败实例失败",
			zap.Uint("instanceId", instanceID),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("延迟删除失败实例成功",
			zap.Uint("instanceId", instanceID))
	}
}

// executeAdminDeleteInstance 执行管理员删除实例操作
func (s *Service) executeAdminDeleteInstance(instanceID uint, taskService interfaces.TaskServiceInterface) error {
	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 检查实例状态，避免重复删除
	if instance.Status == "deleting" {
		return fmt.Errorf("实例正在删除中")
	}

	// 检查是否已有进行中的删除任务
	var existingTask adminModel.Task
	if err := global.APP_DB.Where("instance_id = ? AND task_type = 'delete' AND status IN ('pending', 'running')", instance.ID).First(&existingTask).Error; err == nil {
		return fmt.Errorf("实例已有删除任务正在进行")
	}

	// 创建管理员删除任务数据
	taskData := map[string]interface{}{
		"instanceId":     instanceID,
		"providerId":     instance.ProviderID,
		"adminOperation": true, // 标记为管理员操作
	}

	taskDataJSON, err := json.Marshal(taskData)
	if err != nil {
		return fmt.Errorf("序列化任务数据失败: %v", err)
	}

	// 创建删除任务，设置为不可被用户取消
	task, err := taskService.CreateTask(instance.UserID, &instance.ProviderID, &instanceID, "delete", string(taskDataJSON), 0)
	if err != nil {
		return fmt.Errorf("创建删除任务失败: %v", err)
	}

	// 标记任务为管理员操作，不允许用户取消
	if err := global.APP_DB.Model(task).Update("is_force_stoppable", false).Error; err != nil {
		global.APP_LOG.Warn("更新任务可取消状态失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	// 更新实例状态为删除中
	if err := global.APP_DB.Model(&instance).Update("status", "deleting").Error; err != nil {
		global.APP_LOG.Warn("更新实例状态失败", zap.Uint("instanceId", instanceID), zap.Error(err))
	}

	global.APP_LOG.Info("管理员创建删除任务成功",
		zap.Uint("instanceId", instanceID),
		zap.String("instanceName", instance.Name),
		zap.Uint("taskId", task.ID))

	return nil
}
