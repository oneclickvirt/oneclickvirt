package utils

import (
	"encoding/json"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	maxTaskLogFieldLen = 16000
	maxTaskCommandLen  = 4000
)

// progressLogEntry 单条进度日志条目。
// 兼容旧格式 t/p/m，同时可选携带命令、输出和错误，供任务详情展开排查。
type progressLogEntry struct {
	T       string `json:"t"`                 // 时间（HH:MM:SS）
	P       int    `json:"p"`                 // 进度百分比
	M       string `json:"m"`                 // 消息/步骤 key
	Level   string `json:"level,omitempty"`   // info|warn|error|command
	Command string `json:"command,omitempty"` // 脱敏后的命令
	Output  string `json:"output,omitempty"`  // 命令 stdout/stderr 或关键返回内容
	Error   string `json:"error,omitempty"`   // 错误详情
}

func normalizeTaskLogProgress(progress int) int {
	if progress < 0 {
		return 0
	}
	if progress > 100 {
		return 100
	}
	return progress
}

func normalizeTaskLogField(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	return TruncateString(s, maxLen)
}

func currentTaskProgress(taskID uint, fallback int) int {
	fallback = normalizeTaskLogProgress(fallback)
	var task adminModel.Task
	if err := global.APP_DB.Select("progress").Where("id = ?", taskID).First(&task).Error; err == nil {
		return normalizeTaskLogProgress(task.Progress)
	}
	return fallback
}

// appendProgressLogEntry 使用 SQL CONCAT 原子追加进度日志条目，避免并发读写问题。
func appendProgressLogEntry(taskID uint, entry progressLogEntry) {
	if entry.M == "" && entry.Command == "" && entry.Output == "" && entry.Error == "" {
		return
	}
	entry.P = normalizeTaskLogProgress(entry.P)
	if entry.T == "" {
		entry.T = time.Now().Format("15:04:05")
	}
	entry.M = normalizeTaskLogField(entry.M, maxTaskLogFieldLen)
	entry.Command = normalizeTaskLogField(entry.Command, maxTaskCommandLen)
	entry.Output = normalizeTaskLogField(entry.Output, maxTaskLogFieldLen)
	entry.Error = normalizeTaskLogField(entry.Error, maxTaskLogFieldLen)
	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return
	}

	// 使用参数化 SQL 原子追加日志（避免单引号/反斜杠注入问题）：
	// 若已有日志则追加 ",<entry>" ，否则初始化为 "[<entry>]"。
	appendExpr := gorm.Expr(`CASE
		WHEN (progress_logs IS NULL OR progress_logs = '') THEN CONCAT('[', ?, ']')
		ELSE CONCAT(LEFT(progress_logs, CHAR_LENGTH(progress_logs)-1), ',', ?, ']')
	END`, string(entryJSON), string(entryJSON))

	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("id = ?", taskID).
		Update("progress_logs", appendExpr).Error; err != nil {
		global.APP_LOG.Debug("追加进度日志失败", zap.Uint("taskId", taskID), zap.Error(err))
	}
}

// appendProgressLog 兼容旧调用：只追加步骤/消息。
func appendProgressLog(taskID uint, progress int, message string) {
	if message == "" {
		return
	}
	appendProgressLogEntry(taskID, progressLogEntry{
		P:     progress,
		M:     message,
		Level: "info",
	})
}

// AppendTaskLog 追加一条任务详情日志，不依赖进度是否递增。
func AppendTaskLog(taskID uint, progress int, level string, message string) {
	if message == "" {
		return
	}
	if level == "" {
		level = "info"
	}
	appendProgressLogEntry(taskID, progressLogEntry{
		P:     progress,
		M:     message,
		Level: level,
	})
}

// AppendTaskError 追加任务错误详情，供"查看详情"里直接看到失败原因。
func AppendTaskError(taskID uint, progress int, message string, err error) {
	if err == nil && message == "" {
		return
	}
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	appendProgressLogEntry(taskID, progressLogEntry{
		P:     progress,
		M:     message,
		Level: "error",
		Error: errText,
	})
}

// AppendTaskCommandResult 追加命令执行详情：命令脱敏，输出/错误保留截断后的内容。
func AppendTaskCommandResult(taskID uint, progress int, message string, command string, output string, err error) {
	errText := ""
	level := "command"
	if err != nil {
		errText = err.Error()
		level = "error"
	}
	appendProgressLogEntry(taskID, progressLogEntry{
		P:       progress,
		M:       message,
		Level:   level,
		Command: RedactSensitiveCommand(command, maxTaskCommandLen),
		Output:  output,
		Error:   errText,
	})
}

// UpdateTaskProgress 更新任务进度（全局统一函数）
func UpdateTaskProgress(taskID uint, progress int, message string) {
	progress = normalizeTaskLogProgress(progress)

	updates := map[string]interface{}{
		"progress": progress,
	}
	if message != "" {
		updates["status_message"] = message
	}

	result := global.APP_DB.Model(&adminModel.Task{}).Where("id = ? AND progress <= ?", taskID, progress).Updates(updates)
	if result.Error != nil {
		global.APP_LOG.Error("更新任务进度失败",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message),
			zap.Error(result.Error))
	} else if result.RowsAffected == 0 {
		global.APP_LOG.Debug("任务进度未递增，仅追加进度日志",
			zap.Uint("taskId", taskID),
			zap.Int("incomingProgress", progress),
			zap.String("message", message))
	} else {
		global.APP_LOG.Debug("任务进度更新成功",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message))
	}

	// 即使进度未递增，也要保留步骤日志；否则同一百分比下的重试/错误会丢失。
	if message != "" {
		appendProgressLog(taskID, progress, message)
	}
}

// MarkTaskCompleted 标记任务最终完成（全局统一函数）
func MarkTaskCompleted(taskID uint, message string) {
	updates := map[string]interface{}{
		"status":       "completed",
		"completed_at": time.Now(),
		"progress":     100,
	}
	if message != "" {
		updates["status_message"] = message
	}

	// 只在任务状态为running/processing时才更新为completed，避免覆盖failed状态
	result := global.APP_DB.Model(&adminModel.Task{}).Where("id = ? AND status IN ?", taskID, []string{"running", "processing"}).Updates(updates)
	if result.Error != nil {
		global.APP_LOG.Error("标记任务完成失败",
			zap.Uint("taskId", taskID),
			zap.String("message", message),
			zap.Error(result.Error))
	} else if result.RowsAffected == 0 {
		// 没有更新任何行，说明任务状态不是running/processing（可能已经是failed或其他状态）
		global.APP_LOG.Warn("任务状态不是running/processing，跳过标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))
	} else {
		global.APP_LOG.Info("任务标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))
		if message != "" {
			appendProgressLog(taskID, 100, message)
		}

		// 释放并发控制锁
		if global.APP_TASK_LOCK_RELEASER != nil {
			global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
		}
	}
}

// MarkTaskFailed 标记任务失败（全局统一函数）
func MarkTaskFailed(taskID uint, errorMessage string) {
	progress := currentTaskProgress(taskID, 0)
	if err := global.APP_DB.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"status":        "failed",
		"completed_at":  time.Now(),
		"error_message": errorMessage,
	}).Error; err != nil {
		global.APP_LOG.Error("标记任务失败时出错", zap.Uint("taskId", taskID), zap.Error(err))
	}
	AppendTaskLog(taskID, progress, "error", "step.taskFailed")
	if errorMessage != "" {
		appendProgressLogEntry(taskID, progressLogEntry{P: progress, M: "step.taskFailedDetail", Level: "error", Error: errorMessage})
	}

	// 释放并发控制锁
	if global.APP_TASK_LOCK_RELEASER != nil {
		global.APP_TASK_LOCK_RELEASER.ReleaseTaskLocks(taskID)
	}
}

// GetDefaultTaskTimeout 获取默认任务超时时间（秒）
func GetDefaultTaskTimeout(taskType string) int {
	timeouts := map[string]int{
		"create":              1800, // 30分钟
		"start":               300,  // 5分钟
		"stop":                300,  // 5分钟
		"restart":             600,  // 10分钟
		"reset":               1200, // 20分钟
		"delete":              1800, // 30分钟 - 删除操作需要更长时间处理重试和清理
		"create-port-mapping": 600,  // 10分钟
		"delete-port-mapping": 300,  // 5分钟
		"reset-password":      600,  // 10分钟
	}

	if timeout, exists := timeouts[taskType]; exists {
		return timeout
	}
	return 1800 // 默认30分钟
}
