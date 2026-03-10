package provider

import (
	"context"
	"fmt"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/interfaces"

	"go.uber.org/zap"
)

// Service 处理用户提供商和配置相关功能
type Service struct {
	taskService interfaces.TaskServiceInterface
}

// taskServiceAdapter 任务服务适配器，避免循环导入
type taskServiceAdapter struct{}

// CreateTask 创建任务的适配器方法
func (tsa *taskServiceAdapter) CreateTask(userID uint, providerID *uint, instanceID *uint, taskType string, taskData string, timeoutDuration int) (*adminModel.Task, error) {
	// 使用延迟导入来避免循环依赖
	if globalTaskService == nil {
		return nil, fmt.Errorf("任务服务未初始化")
	}
	return globalTaskService.CreateTask(userID, providerID, instanceID, taskType, taskData, timeoutDuration)
}

// GetStateManager 获取状态管理器的适配器方法
func (tsa *taskServiceAdapter) GetStateManager() interfaces.TaskStateManagerInterface {
	if globalTaskService == nil {
		return nil
	}
	return globalTaskService.GetStateManager()
}

// 全局任务服务实例，在系统初始化时设置
var globalTaskService interfaces.TaskServiceInterface

// SetGlobalTaskService 设置全局任务服务实例
func SetGlobalTaskService(ts interfaces.TaskServiceInterface) {
	globalTaskService = ts
}

// NewService 创建提供商服务
func NewService() *Service {
	return &Service{
		taskService: &taskServiceAdapter{},
	}
}

// GetProviderCapabilities 获取Provider能力
// GetInstanceTypePermissions 获取实例类型权限
// ProcessCreateInstanceTask 处理创建实例的后台任务 - 三阶段处理
func (s *Service) ProcessCreateInstanceTask(ctx context.Context, task *adminModel.Task) error {
	global.APP_LOG.Debug("开始处理创建实例任务", zap.Uint("taskId", task.ID))

	// 初始化进度 (5%)
	s.updateTaskProgress(task.ID, 5, "正在准备实例创建...")

	// 阶段1: 数据库预处理（快速事务） (5% -> 25%)
	instance, err := s.prepareInstanceCreation(ctx, task)
	if err != nil {
		global.APP_LOG.Error("实例创建预处理失败", zap.Uint("taskId", task.ID), zap.Error(err))
		// 使用统一状态管理器
		stateManager := s.taskService.GetStateManager()
		if stateManager != nil {
			if err := stateManager.CompleteMainTask(task.ID, false, fmt.Sprintf("预处理失败: %v", err), nil); err != nil {
				global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
			}
		} else {
			global.APP_LOG.Error("状态管理器未初始化", zap.Uint("taskId", task.ID))
		}
		return err
	}

	// 更新进度到30% (开始调用Provider创建实例)
	s.updateTaskProgress(task.ID, 30, "正在调用Provider创建实例...")

	// 阶段2: Provider创建实例（无事务，根据ExecutionRule自动选择API或SSH）(30% -> 60%)
	apiError := s.executeProviderCreation(ctx, task, instance)

	// 阶段3: 结果处理（快速事务）
	global.APP_LOG.Debug("开始处理实例创建结果", zap.Uint("taskId", task.ID), zap.Bool("hasApiError", apiError != nil))
	if finalizeErr := s.finalizeInstanceCreation(context.Background(), task, instance, apiError); finalizeErr != nil {
		global.APP_LOG.Error("实例创建最终化失败", zap.Uint("taskId", task.ID), zap.Error(finalizeErr))
		return finalizeErr
	}
	global.APP_LOG.Debug("实例创建结果处理完成", zap.Uint("taskId", task.ID), zap.Bool("hasApiError", apiError != nil))

	// 不再返回apiError，因为业务逻辑已经完全处理了任务状态
	if apiError != nil {
		global.APP_LOG.Error("Provider创建实例失败", zap.Uint("taskId", task.ID), zap.Error(apiError))
	}

	global.APP_LOG.Info("实例创建任务处理完成", zap.Uint("taskId", task.ID), zap.Uint("instanceId", instance.ID))
	return nil
}
