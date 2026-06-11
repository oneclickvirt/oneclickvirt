package taskgate

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/service/database"

	"gorm.io/gorm"
)

const (
	configCategory = "system"
	enabledKey     = "task_pool.enabled"
	messageKey     = "task_pool.maintenance_message"
	defaultMessage = "系统正在维护，任务池已关闭，暂不接受新的任务。请等待管理员完成升级或重启后再重试。"
)

func readConfig() (enabled bool, message string, updatedAt *time.Time, err error) {
	enabled = true
	message = defaultMessage
	if global.APP_DB == nil {
		return enabled, message, nil, nil
	}

	var enabledConfig adminModel.SystemConfig
	if err = global.APP_DB.Where("category = ? AND `key` = ?", configCategory, enabledKey).First(&enabledConfig).Error; err != nil {
		if err == gorm.ErrRecordNotFound || isMissingConfigStorage(err) {
			err = nil
		} else {
			return enabled, message, nil, err
		}
	} else {
		enabledValue := strings.ToLower(strings.TrimSpace(enabledConfig.Value))
		enabled = enabledValue == "" || enabledValue == "true" || enabledValue == "1" || enabledValue == "yes" || enabledValue == "on"
		updatedAt = &enabledConfig.UpdatedAt
	}

	var messageConfig adminModel.SystemConfig
	if err = global.APP_DB.Where("category = ? AND `key` = ?", configCategory, messageKey).First(&messageConfig).Error; err != nil {
		if err == gorm.ErrRecordNotFound || isMissingConfigStorage(err) {
			err = nil
		} else {
			return enabled, message, updatedAt, err
		}
	} else if strings.TrimSpace(messageConfig.Value) != "" {
		message = strings.TrimSpace(messageConfig.Value)
		if updatedAt == nil || messageConfig.UpdatedAt.After(*updatedAt) {
			updatedAt = &messageConfig.UpdatedAt
		}
	}

	return enabled, message, updatedAt, nil
}

func isMissingConfigStorage(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "no such table") || strings.Contains(msg, "doesn't exist") || strings.Contains(msg, "unknown table")
}

// IsEnabled returns whether new task admission is currently open.
// Missing config intentionally defaults to open to preserve current behavior.
func IsEnabled() bool {
	enabled, _, _, err := readConfig()
	if err != nil {
		return true
	}
	return enabled
}

// EnsureAccepting prevents new tasks from being inserted while the controller is
// in maintenance mode. Existing pending/running tasks are not affected.
func EnsureAccepting() error {
	enabled, message, _, err := readConfig()
	if err != nil {
		return err
	}
	if enabled {
		return nil
	}
	if strings.TrimSpace(message) == "" {
		message = defaultMessage
	}
	return fmt.Errorf("任务池已关闭：%s", message)
}

// GetStatus returns the persisted task-pool switch plus queue drain state.
func GetStatus() (*adminModel.TaskPoolStatusResponse, error) {
	enabled, message, updatedAt, err := readConfig()
	if err != nil {
		return nil, err
	}

	var pendingTasks, runningTasks int64
	var configPendingTasks, configRunningTasks int64
	if global.APP_DB != nil {
		if err := global.APP_DB.Model(&adminModel.Task{}).Where("status = ?", "pending").Count(&pendingTasks).Error; err != nil {
			return nil, err
		}
		if err := global.APP_DB.Model(&adminModel.Task{}).Where("status IN ?", []string{"running", "processing", "cancelling"}).Count(&runningTasks).Error; err != nil {
			return nil, err
		}
		if err := global.APP_DB.Model(&adminModel.ConfigurationTask{}).Where("status = ?", adminModel.TaskStatusPending).Count(&configPendingTasks).Error; err != nil {
			return nil, err
		}
		if err := global.APP_DB.Model(&adminModel.ConfigurationTask{}).Where("status = ?", adminModel.TaskStatusRunning).Count(&configRunningTasks).Error; err != nil {
			return nil, err
		}
	}

	activeTasks := pendingTasks + runningTasks + configPendingTasks + configRunningTasks
	state := "enabled"
	if !enabled && activeTasks > 0 {
		state = "draining"
	} else if !enabled {
		state = "maintenance_ready"
	}

	return &adminModel.TaskPoolStatusResponse{
		Enabled:                   enabled,
		AcceptingNewTasks:         enabled,
		State:                     state,
		Message:                   message,
		PendingTasks:              pendingTasks,
		RunningTasks:              runningTasks,
		ConfigurationPendingTasks: configPendingTasks,
		ConfigurationRunningTasks: configRunningTasks,
		ActiveTasks:               activeTasks,
		DrainComplete:             activeTasks == 0,
		CanRestartController:      !enabled && activeTasks == 0,
		UpdatedAt:                 updatedAt,
	}, nil
}

// SetEnabled changes only new-task admission. It does not cancel or pause
// existing pending/running tasks so maintenance can be performed after drain.
func SetEnabled(enabled bool, message string) (*adminModel.TaskPoolStatusResponse, error) {
	if global.APP_DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = defaultMessage
	}

	dbService := database.GetDatabaseService()
	err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		if err := upsertConfig(tx, enabledKey, fmt.Sprintf("%t", enabled), "任务池全局开关，false 时不再接受新的 pending 任务"); err != nil {
			return err
		}
		return upsertConfig(tx, messageKey, message, "任务池关闭时返回给用户的维护提示")
	})
	if err != nil {
		return nil, err
	}
	return GetStatus()
}

func upsertConfig(tx *gorm.DB, key string, value string, description string) error {
	var cfg adminModel.SystemConfig
	return tx.Where("category = ? AND `key` = ?", configCategory, key).
		Assign(adminModel.SystemConfig{
			Value:       value,
			Description: description,
			Type:        "string",
			IsPublic:    false,
		}).
		FirstOrCreate(&cfg, adminModel.SystemConfig{
			Category: configCategory,
			Key:      key,
		}).Error
}
