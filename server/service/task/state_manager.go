package task

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TaskStateManager 统一的任务状态管理器
type TaskStateManager struct {
	// 使用channel池架构，无需锁管理
	taskService *TaskService // 引用主任务服务

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

	// Complete callbacks can race with CancelConfigTask. Update only an active
	// row so a late provider response cannot resurrect a cancelled task.
	status := adminModel.TaskStatusFailed
	if success {
		status = adminModel.TaskStatusCompleted
	}
	now := time.Now()
	resultJSON := ""
	if resultData != nil {
		encoded, err := json.Marshal(resultData)
		if err != nil {
			return fmt.Errorf("序列化配置任务结果失败: %w", err)
		}
		resultJSON = string(encoded)
	}
	dbService := database.GetDatabaseService()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		var current adminModel.ConfigurationTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("status", "progress").First(&current, taskID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("配置任务 %d 不存在", taskID)
			}
			return fmt.Errorf("获取配置任务状态失败: %w", err)
		}
		if current.Status == adminModel.TaskStatusCompleted || current.Status == adminModel.TaskStatusFailed || current.Status == adminModel.TaskStatusCancelled {
			return nil
		}
		if current.Status != adminModel.TaskStatusPending && current.Status != adminModel.TaskStatusRunning && current.Status != adminModel.TaskStatusCancelling {
			return fmt.Errorf("配置任务 %d 状态 %s 不允许完成", taskID, current.Status)
		}
		updates := map[string]interface{}{
			"status":       status,
			"completed_at": &now,
			"success":      success,
			"progress":     100,
		}
		if !success && errorMessage != "" {
			updates["error_message"] = errorMessage
		}
		if resultJSON != "" {
			updates["result_data"] = resultJSON
		}
		if current.Status == adminModel.TaskStatusCancelling {
			updates["status"] = adminModel.TaskStatusCancelled
			updates["success"] = false
			updates["progress"] = current.Progress
			if errorMessage != "" {
				updates["error_message"] = errorMessage
			}
		}
		result := tx.Model(&adminModel.ConfigurationTask{}).
			Where("id = ? AND status = ?", taskID, current.Status).
			Updates(updates)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}

		// A late callback for a terminal task is harmless and should remain
		// idempotent. Cancellation is handled by the CAS above and cannot be
		// overwritten by this callback.
		return fmt.Errorf("配置任务 %d 状态更新失败", taskID)
	})
}

// CancelMainTask 取消主任务 - 简化版，无锁管理
func (tsm *TaskStateManager) CancelMainTask(taskID uint, reason string) error {
	global.APP_LOG.Debug("统一任务状态管理器：取消主任务",
		zap.Uint("taskId", taskID),
		zap.String("reason", reason))

	// CompleteTask is a completion callback, not a cancellation transition:
	// using it here used to turn an active task into `failed` and did not
	// propagate cancellation to the worker context. Reuse the authoritative
	// cancellation path so state, cleanup, and context handling stay aligned.
	return tsm.taskService.CancelTaskByAdmin(taskID, reason)
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

	// Repeated cancellation is idempotent. Other terminal states cannot be
	// changed after completion.
	if task.Status == adminModel.TaskStatusCancelled || task.Status == adminModel.TaskStatusCancelling {
		return nil
	}
	if task.Status != adminModel.TaskStatusPending && task.Status != adminModel.TaskStatusRunning {
		return fmt.Errorf("任务状态 %s 不允许取消", task.Status)
	}

	// Cancel atomically claims only active rows. A completion callback that won
	// the race first remains terminal; repeated cancel requests are idempotent
	// for an already-cancelled task.
	now := time.Now()
	dbService := database.GetDatabaseService()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		var current adminModel.ConfigurationTask
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("status").First(&current, taskID).Error; err != nil {
			return fmt.Errorf("获取配置任务状态失败: %w", err)
		}
		if current.Status == adminModel.TaskStatusCancelled || current.Status == adminModel.TaskStatusCancelling {
			return nil
		}
		if current.Status != adminModel.TaskStatusPending && current.Status != adminModel.TaskStatusRunning {
			return fmt.Errorf("任务状态 %s 不允许取消", current.Status)
		}
		status := adminModel.TaskStatusCancelling
		if current.Status == adminModel.TaskStatusPending {
			status = adminModel.TaskStatusCancelled
		}
		result := tx.Model(&adminModel.ConfigurationTask{}).
			Where("id = ? AND status = ?", taskID, current.Status).
			Updates(map[string]interface{}{
				"status":        status,
				"error_message": reason,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			if status == adminModel.TaskStatusCancelled {
				return tx.Model(&adminModel.ConfigurationTask{}).Where("id = ?", taskID).Update("completed_at", &now).Error
			}
			return nil
		}
		var latest adminModel.ConfigurationTask
		if err := tx.Select("status").First(&latest, taskID).Error; err != nil {
			return fmt.Errorf("获取配置任务状态失败: %w", err)
		}
		if latest.Status == adminModel.TaskStatusCancelled || latest.Status == adminModel.TaskStatusCancelling {
			return nil
		}
		return fmt.Errorf("任务状态 %s 不允许取消", latest.Status)
	})
}

// UpdateTaskProgress 统一的任务进度更新
func (tsm *TaskStateManager) UpdateTaskProgress(taskID uint, tableType string, progress int, message string) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}

	switch tableType {
	case "tasks":
		tsm.taskService.updateTaskProgress(taskID, progress, message)
		return nil
	case "configuration_tasks":
		return global.APP_DB.Model(&adminModel.ConfigurationTask{}).
			Where("id = ? AND status IN ? AND progress <= ?", taskID,
				[]string{adminModel.TaskStatusPending, adminModel.TaskStatusRunning}, progress).
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

	now := time.Now()
	dbService := database.GetDatabaseService()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	return dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		result := tx.Model(&adminModel.ConfigurationTask{}).
			Where("id = ? AND status = ?", taskID, adminModel.TaskStatusPending).
			Updates(map[string]interface{}{
				"status":     adminModel.TaskStatusRunning,
				"started_at": &now,
			})
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected > 0 {
			return nil
		}

		var current adminModel.ConfigurationTask
		if err := tx.Select("status").First(&current, taskID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return fmt.Errorf("配置任务 %d 不存在", taskID)
			}
			return fmt.Errorf("获取配置任务状态失败: %w", err)
		}
		return fmt.Errorf("任务状态 %s 不允许启动", current.Status)
	})
}

// GetTaskStateManager 获取全局任务状态管理器
func GetTaskStateManager() *TaskStateManager {
	return globalTaskStateManager
}
