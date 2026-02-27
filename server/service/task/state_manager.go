package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TaskStateManager 统一的任务状态管理器
type TaskStateManager struct {
	// 使用channel池架构，无需锁管理
	taskService *TaskService // 引用主任务服务

	// 状态管理
	mutex sync.RWMutex
}

// 编译时接口检查
var _ interfaces.TaskStateManagerInterface = (*TaskStateManager)(nil)

// NewTaskStateManager 创建新的任务状态管理器
func NewTaskStateManager(taskService *TaskService) *TaskStateManager {
	return &TaskStateManager{
		taskService: taskService,
	}
}

// TaskInfo 任务信息结构
type TaskInfo struct {
	ID         uint
	TableType  string // "tasks" 或 "configuration_tasks"
	Status     string
	ProviderID *uint
}

// CompleteMainTask 完成主任务（admin.Task表）- 简化版，无锁管理
func (tsm *TaskStateManager) CompleteMainTask(taskID uint, success bool, errorMessage string, resultData map[string]interface{}) error {
	global.APP_LOG.Debug("统一任务状态管理器：完成主任务",
		zap.Uint("taskId", taskID),
		zap.Bool("success", success))

	// channel池架构自动处理并发控制，直接更新状态
	return tsm.taskService.CompleteTask(taskID, success, errorMessage, resultData)
}

// CompleteConfigTask 完成配置任务（admin.ConfigurationTask表）
func (tsm *TaskStateManager) CompleteConfigTask(taskID uint, success bool, errorMessage string, resultData map[string]interface{}) error {
	global.APP_LOG.Debug("统一任务状态管理器：完成配置任务",
		zap.Uint("taskId", taskID),
		zap.Bool("success", success))

	// 获取配置任务信息
	var task adminModel.ConfigurationTask
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		return fmt.Errorf("获取配置任务信息失败: %w", err)
	}

	// 更新配置任务状态
	now := time.Now()
	task.CompletedAt = &now
	task.Success = success
	task.Progress = 100

	if success {
		task.Status = adminModel.TaskStatusCompleted
	} else {
		task.Status = adminModel.TaskStatusFailed
		task.ErrorMessage = errorMessage
	}

	// 使用事务保存
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&task).Error
	})
}

// CancelMainTask 取消主任务 - 简化版，无锁管理
func (tsm *TaskStateManager) CancelMainTask(taskID uint, reason string) error {
	global.APP_LOG.Debug("统一任务状态管理器：取消主任务",
		zap.Uint("taskId", taskID),
		zap.String("reason", reason))

	var task adminModel.Task
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		return fmt.Errorf("获取任务信息失败: %w", err)
	}

	// 只有pending和running状态的任务可以取消
	if task.Status != "pending" && task.Status != "running" {
		return fmt.Errorf("任务状态 %s 不允许取消", task.Status)
	}

	// channel池架构自动处理并发控制，直接更新状态
	return tsm.taskService.CompleteTask(taskID, false, reason, nil)
}

// CancelConfigTask 取消配置任务
func (tsm *TaskStateManager) CancelConfigTask(taskID uint, reason string) error {
	global.APP_LOG.Debug("统一任务状态管理器：取消配置任务",
		zap.Uint("taskId", taskID),
		zap.String("reason", reason))

	var task adminModel.ConfigurationTask
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		return fmt.Errorf("获取配置任务信息失败: %w", err)
	}

	// 只有pending和running状态的任务可以取消
	if task.Status != adminModel.TaskStatusPending && task.Status != adminModel.TaskStatusRunning {
		return fmt.Errorf("任务状态 %s 不允许取消", task.Status)
	}

	// 更新任务状态
	now := time.Now()
	task.Status = adminModel.TaskStatusCancelled
	task.CompletedAt = &now
	task.ErrorMessage = reason

	// 使用事务保存
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&task).Error
	})
}

// UpdateTaskProgress 统一的任务进度更新
func (tsm *TaskStateManager) UpdateTaskProgress(taskID uint, tableType string, progress int, message string) error {
	tsm.mutex.Lock()
	defer tsm.mutex.Unlock()

	switch tableType {
	case "tasks":
		tsm.taskService.updateTaskProgress(taskID, progress, message)
		return nil
	case "configuration_tasks":
		return global.APP_DB.Model(&adminModel.ConfigurationTask{}).
			Where("id = ?", taskID).
			Updates(map[string]interface{}{
				"progress": progress,
			}).Error
	default:
		return fmt.Errorf("未知的任务表类型: %s", tableType)
	}
}

// GetTaskInfo 获取任务信息（自动识别表类型）
func (tsm *TaskStateManager) GetTaskInfo(taskID uint) (*TaskInfo, error) {
	// 先尝试主任务表
	var mainTask adminModel.Task
	if err := global.APP_DB.First(&mainTask, taskID).Error; err == nil {
		return &TaskInfo{
			ID:         mainTask.ID,
			TableType:  "tasks",
			Status:     mainTask.Status,
			ProviderID: mainTask.ProviderID,
		}, nil
	}

	// 再尝试配置任务表
	var configTask adminModel.ConfigurationTask
	if err := global.APP_DB.First(&configTask, taskID).Error; err == nil {
		return &TaskInfo{
			ID:         configTask.ID,
			TableType:  "configuration_tasks",
			Status:     configTask.Status,
			ProviderID: &configTask.ProviderID,
		}, nil
	}

	return nil, fmt.Errorf("未找到任务 ID: %d", taskID)
}

// 全局任务状态管理器实例
var globalTaskStateManager *TaskStateManager

// InitTaskStateManager 初始化全局任务状态管理器
func InitTaskStateManager(taskService *TaskService) {
	globalTaskStateManager = NewTaskStateManager(taskService)
}

// StartConfigTask 启动配置任务
func (tsm *TaskStateManager) StartConfigTask(taskID uint) error {
	global.APP_LOG.Debug("统一任务状态管理器：启动配置任务",
		zap.Uint("taskId", taskID))

	// 获取配置任务信息
	var task adminModel.ConfigurationTask
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		return fmt.Errorf("获取配置任务信息失败: %w", err)
	}

	// 只有pending状态的任务可以启动
	if task.Status != adminModel.TaskStatusPending {
		return fmt.Errorf("任务状态 %s 不允许启动", task.Status)
	}

	// 更新任务状态
	now := time.Now()
	task.Status = adminModel.TaskStatusRunning
	task.StartedAt = &now

	// 使用事务保存
	dbService := database.GetDatabaseService()
	return dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Save(&task).Error
	})
}

// GetTaskStateManager 获取全局任务状态管理器
func GetTaskStateManager() *TaskStateManager {
	return globalTaskStateManager
}
