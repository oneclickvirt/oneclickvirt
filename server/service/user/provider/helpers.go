package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
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

// updateTaskProgress 更新任务进度
func (s *Service) updateTaskProgress(taskID uint, progress int, message string) {
	updates := map[string]interface{}{
		"progress": progress,
	}
	if message != "" {
		updates["status_message"] = message
	}

	if err := global.APP_DB.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(updates).Error; err != nil {
		global.APP_LOG.Error("更新任务进度失败",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("任务进度更新成功",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message))
	}
}

// markTaskCompleted 标记任务最终完成
func (s *Service) markTaskCompleted(taskID uint, message string) {
	updates := map[string]interface{}{
		"status":       "completed",
		"completed_at": time.Now(),
		"progress":     100,
	}
	if message != "" {
		updates["status_message"] = message
	}

	// 只在任务状态为running时才更新为completed，避免覆盖failed状态
	result := global.APP_DB.Model(&adminModel.Task{}).Where("id = ? AND status = ?", taskID, "running").Updates(updates)
	if result.Error != nil {
		global.APP_LOG.Error("标记任务完成失败",
			zap.Uint("taskId", taskID),
			zap.String("message", message),
			zap.Error(result.Error))
	} else if result.RowsAffected == 0 {
		// 没有更新任何行，说明任务状态不是running（可能已经是failed或其他状态）
		global.APP_LOG.Warn("任务状态不是running，跳过标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))
	} else {
		global.APP_LOG.Info("任务标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))

		// 释放并发控制锁
		if global.APP_TASK_LOCK_RELEASER != nil {
			global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
		}
	}
}

// markTaskFailed 标记任务失败
func (s *Service) markTaskFailed(taskID uint, errorMessage string) {
	if err := global.APP_DB.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"status":        "failed",
		"completed_at":  time.Now(),
		"error_message": errorMessage,
	}).Error; err != nil {
		global.APP_LOG.Error("标记任务失败时出错", zap.Uint("taskId", taskID), zap.Error(err))
	}

	// 注释：新机制中资源预留已在创建时被原子化消费，无需额外释放

	// 释放并发控制锁
	if global.APP_TASK_LOCK_RELEASER != nil {
		global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
	}
}

// generateInstanceName 生成实例名称
func (s *Service) generateInstanceName(providerName string) string {
	// 生成格式: provider-name-4位随机字符 (如: docker-d73a)
	randomStr := fmt.Sprintf("%04x", rand.Intn(65536)) // 生成4位16进制随机字符

	// 清理provider名称，移除特殊字符
	cleanName := strings.ReplaceAll(strings.ToLower(providerName), " ", "-")
	cleanName = strings.ReplaceAll(cleanName, "_", "-")

	return fmt.Sprintf("%s-%s", cleanName, randomStr)
}

// generatePassword 生成随机密码
func (s *Service) generatePassword() string {
	return utils.GenerateStrongPassword(12)
}

// extractHost 从endpoint中提取主机地址
func (s *Service) extractHost(endpoint string) string {
	if strings.Contains(endpoint, "://") {
		parts := strings.Split(endpoint, "://")
		if len(parts) > 1 {
			hostPort := parts[1]
			if strings.Contains(hostPort, ":") {
				hostParts := strings.Split(hostPort, ":")
				return hostParts[0]
			}
			return hostPort
		}
	}

	// 如果没有协议前缀，直接返回主机部分
	if strings.Contains(endpoint, ":") {
		parts := strings.Split(endpoint, ":")
		return parts[0]
	}

	return endpoint
}

// getInstanceDetailsAfterCreation 创建后获取实例详情
func (s *Service) getInstanceDetailsAfterCreation(ctx context.Context, instance *providerModel.Instance) (*providerModel.ProviderInstance, error) {
	// 获取Provider信息
	var dbProvider providerModel.Provider
	if err := global.APP_DB.First(&dbProvider, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 获取Provider实例（使用ID）
	providerSvc := providerService.GetProviderService()
	providerInstance, exists := providerSvc.GetProviderByID(instance.ProviderID)

	if !exists {
		// 如果Provider未连接，尝试动态加载
		if err := providerSvc.LoadProvider(dbProvider); err != nil {
			return nil, fmt.Errorf("连接Provider失败: %w", err)
		}

		// 重新获取Provider实例
		providerInstance, exists = providerSvc.GetProviderByID(instance.ProviderID)
		if !exists {
			return nil, fmt.Errorf("Provider ID %d 连接后仍然不可用", instance.ProviderID)
		}
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
	task, err := taskService.CreateTask(instance.UserID, &instance.ProviderID, &instanceID, "delete", string(taskDataJSON), 1800)
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
