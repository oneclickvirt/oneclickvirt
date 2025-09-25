package config

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	"oneclickvirt/service/database"
	taskManager "oneclickvirt/service/task"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TaskService 配置任务服务
type TaskService struct {
	runningTasks map[uint]*TaskContext // providerId -> TaskContext
	mutex        sync.RWMutex
}

// TaskContext 配置任务上下文
type TaskContext struct {
	Task       *admin.ConfigurationTask
	CancelFunc chan struct{}
}

var taskService *TaskService
var taskOnce sync.Once

// GetTaskService 获取配置任务服务单例
func GetTaskService() *TaskService {
	taskOnce.Do(func() {
		taskService = &TaskService{
			runningTasks: make(map[uint]*TaskContext),
		}
		// 只有在数据库已初始化时才清理未完成的任务
		if isSystemInitialized() {
			taskService.cleanupUnfinishedTasks()
		} else {
			global.APP_LOG.Debug("系统未初始化，跳过配置任务清理")
		}
	})
	return taskService
}

// isSystemInitialized 检查系统是否已初始化（本地检查，避免循环依赖）
func isSystemInitialized() bool {
	if global.APP_DB == nil {
		return false
	}

	// 简单的数据库连接测试
	sqlDB, err := global.APP_DB.DB()
	if err != nil {
		return false
	}

	if err := sqlDB.Ping(); err != nil {
		return false
	}

	// 检查是否有用户表，这是一个基本的初始化标志
	return global.APP_DB.Migrator().HasTable("users")
}

// NewTaskService 创建配置任务服务实例
func NewTaskService() *TaskService {
	return GetTaskService()
}

// cleanupUnfinishedTasks 清理未完成的任务（服务重启后）
func (s *TaskService) cleanupUnfinishedTasks() {
	// 再次检查数据库是否可用
	if global.APP_DB == nil {
		global.APP_LOG.Warn("数据库连接不存在，无法清理未完成的配置任务")
		return
	}

	var tasks []admin.ConfigurationTask
	global.APP_DB.Where("status IN ?", []string{
		admin.TaskStatusPending,
		admin.TaskStatusRunning,
	}).Find(&tasks)

	stateManager := taskManager.GetTaskStateManager()
	if stateManager == nil {
		global.APP_LOG.Fatal("统一任务状态管理器未初始化，无法清理未完成任务")
		return
	}

	for _, task := range tasks {
		global.APP_LOG.Debug("清理未完成任务", zap.Uint("taskId", task.ID), zap.Uint("providerId", task.ProviderID), zap.String("taskType", task.TaskType))

		if err := stateManager.CancelConfigTask(task.ID, "服务重启，任务已取消"); err != nil {
			global.APP_LOG.Error("清理任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
		}
	}
}

// GetRunningTask 获取Provider的运行中任务
func (s *TaskService) GetRunningTask(providerID uint) *admin.ConfigurationTask {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if ctx, exists := s.runningTasks[providerID]; exists {
		return ctx.Task
	}
	return nil
}

// GetProviderHistory 获取Provider的历史任务
func (s *TaskService) GetProviderHistory(providerID uint, limit int) ([]admin.ConfigurationTaskResponse, error) {
	var tasks []admin.ConfigurationTask
	db := global.APP_DB.Preload("Provider").
		Where("provider_id = ?", providerID).
		Order("created_at DESC")

	if limit > 0 {
		db = db.Limit(limit)
	}

	if err := db.Find(&tasks).Error; err != nil {
		return nil, err
	}

	responses := make([]admin.ConfigurationTaskResponse, len(tasks))
	for i, task := range tasks {
		responses[i] = s.convertToResponse(task)
	}

	return responses, nil
}

// CreateAutoConfigTask 创建自动配置任务
func (s *TaskService) CreateAutoConfigTask(userID uint, providerData map[string]interface{}) (interface{}, error) {
	// 从providerData中提取providerID
	providerID, ok := providerData["provider_id"].(uint)
	if !ok {
		if pid, ok := providerData["provider_id"].(float64); ok {
			providerID = uint(pid)
		} else {
			return nil, fmt.Errorf("无效的provider_id")
		}
	}

	// 创建配置任务
	task, err := s.CreateTask(providerID, "auto_config", userID, "system")
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":          task.ID,
		"status":      task.Status,
		"provider_id": task.ProviderID,
		"task_type":   task.TaskType,
	}, nil
}

// CreateUploadTask 创建上传任务
func (s *TaskService) CreateUploadTask(userID uint, providerID uint, uploadData map[string]interface{}) (interface{}, error) {
	// 创建上传任务
	task, err := s.CreateTask(providerID, "upload", userID, "system")
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"id":          task.ID,
		"status":      task.Status,
		"provider_id": task.ProviderID,
		"task_type":   task.TaskType,
	}, nil
}

// StopTask 停止任务
func (s *TaskService) StopTask(taskID uint) error {
	return s.CancelTask(taskID)
}

// CancelTask 取消任务
func (s *TaskService) CancelTask(taskID uint) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var task admin.ConfigurationTask
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		return fmt.Errorf("任务不存在: %w", err)
	}

	// 如果任务已经被取消，直接返回成功
	if task.Status == admin.TaskStatusCancelled {
		return nil
	}

	// 只能取消等待中或运行中的任务
	if task.Status != admin.TaskStatusPending && task.Status != admin.TaskStatusRunning {
		return fmt.Errorf("只能取消等待中或运行中的任务，当前状态：%s", task.Status)
	}

	// 发送取消信号
	if ctx, exists := s.runningTasks[task.ProviderID]; exists {
		close(ctx.CancelFunc)
		delete(s.runningTasks, task.ProviderID)
	}

	// 使用统一任务状态管理器取消任务
	stateManager := taskManager.GetTaskStateManager()
	if stateManager == nil {
		return fmt.Errorf("统一任务状态管理器未初始化")
	}

	global.APP_LOG.Info("使用统一管理器取消配置任务", zap.Uint("taskId", task.ID))
	return stateManager.CancelConfigTask(task.ID, "任务已被取消")
}

// CreateTask 创建任务
func (s *TaskService) CreateTask(providerID uint, taskType string, userID uint, username string) (*admin.ConfigurationTask, error) {
	global.APP_LOG.Debug("开始创建配置任务",
		zap.Uint("providerID", providerID),
		zap.String("taskType", taskType),
		zap.Uint("executorID", userID),
		zap.String("executorName", username))

	task := &admin.ConfigurationTask{
		ProviderID:   providerID,
		TaskType:     taskType,
		Status:       admin.TaskStatusPending,
		ExecutorID:   userID,
		ExecutorName: username,
		Progress:     0,
	}

	dbService := database.GetDatabaseService()
	err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(task).Error
	})

	if err != nil {
		global.APP_LOG.Error("配置任务创建失败",
			zap.Uint("providerID", providerID),
			zap.String("taskType", taskType),
			zap.Error(err))
		return nil, fmt.Errorf("创建任务失败: %w", err)
	}

	global.APP_LOG.Info("配置任务创建成功",
		zap.Uint("taskID", task.ID),
		zap.Uint("providerID", providerID),
		zap.String("taskType", taskType))
	return task, nil
}

// StartTask 启动任务
func (s *TaskService) StartTask(taskID uint) error {
	global.APP_LOG.Debug("开始启动配置任务", zap.Uint("taskID", taskID))

	var task admin.ConfigurationTask
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		global.APP_LOG.Error("查询配置任务失败", zap.Uint("taskID", taskID), zap.Error(err))
		return fmt.Errorf("任务不存在: %w", err)
	}

	if task.Status != admin.TaskStatusPending {
		global.APP_LOG.Warn("配置任务状态不允许启动",
			zap.Uint("taskID", taskID),
			zap.String("status", task.Status))
		return fmt.Errorf("任务状态不允许启动: %s", task.Status)
	}

	// 检查是否有正在运行的任务
	s.mutex.Lock()
	if ctx, exists := s.runningTasks[task.ProviderID]; exists {
		s.mutex.Unlock()
		global.APP_LOG.Warn("Provider已有正在运行的任务",
			zap.Uint("providerID", task.ProviderID),
			zap.Uint("existingTaskID", ctx.Task.ID),
			zap.Uint("newTaskID", taskID))
		return fmt.Errorf("Provider %d 已有正在运行的任务 %d", task.ProviderID, ctx.Task.ID)
	}

	// 启动配置任务
	stateManager := taskManager.GetTaskStateManager()
	if err := stateManager.StartConfigTask(task.ID); err != nil {
		s.mutex.Unlock()
		return fmt.Errorf("启动配置任务失败: %w", err)
	}

	// 创建任务上下文
	ctx := &TaskContext{
		Task:       &task,
		CancelFunc: make(chan struct{}),
	}
	s.runningTasks[task.ProviderID] = ctx
	s.mutex.Unlock()

	return nil
}

// GetTaskList 获取任务列表
func (s *TaskService) GetTaskList(req *admin.ConfigurationTaskListRequest) ([]admin.ConfigurationTaskResponse, int64, error) {
	db := global.APP_DB.Model(&admin.ConfigurationTask{}).
		Preload("Provider")

	// 构建查询条件
	if req.ProviderID > 0 {
		db = db.Where("provider_id = ?", req.ProviderID)
	}
	if req.TaskType != "" {
		db = db.Where("task_type = ?", req.TaskType)
	}
	if req.Status != "" {
		db = db.Where("status = ?", req.Status)
	}
	if req.ExecutorID > 0 {
		db = db.Where("executor_id = ?", req.ExecutorID)
	}

	// 获取总数
	var total int64
	db.Count(&total)

	// 分页查询
	var tasks []admin.ConfigurationTask
	if err := db.Order("created_at DESC").
		Offset((req.Page - 1) * req.PageSize).
		Limit(req.PageSize).
		Find(&tasks).Error; err != nil {
		return nil, 0, err
	}

	// 转换为响应格式
	responses := make([]admin.ConfigurationTaskResponse, len(tasks))
	for i, task := range tasks {
		responses[i] = s.convertToResponse(task)
	}

	return responses, total, nil
}

// GetTaskDetail 获取任务详情
func (s *TaskService) GetTaskDetail(taskID uint) (*admin.ConfigurationTaskDetailResponse, error) {
	var task admin.ConfigurationTask
	if err := global.APP_DB.Preload("Provider").First(&task, taskID).Error; err != nil {
		return nil, fmt.Errorf("任务不存在: %w", err)
	}

	response := s.convertToDetailResponse(task)
	return &response, nil
}

// UpdateTaskLog 更新任务日志
func (s *TaskService) UpdateTaskLog(taskID uint, logMessage string) error {
	return global.APP_DB.Model(&admin.ConfigurationTask{}).
		Where("id = ?", taskID).
		Update("log_output", logMessage).Error
}

// UpdateTaskProgress 更新任务进度
func (s *TaskService) UpdateTaskProgress(taskID uint, progress int) error {
	return global.APP_DB.Model(&admin.ConfigurationTask{}).
		Where("id = ?", taskID).
		Update("progress", progress).Error
}

// FinishTask 完成任务
func (s *TaskService) FinishTask(taskID uint, success bool, errorMessage string, resultData map[string]interface{}) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	var task admin.ConfigurationTask
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		return fmt.Errorf("任务不存在: %w", err)
	}

	// 从运行中的任务中移除
	delete(s.runningTasks, task.ProviderID)

	// 使用统一任务状态管理器更新状态
	stateManager := taskManager.GetTaskStateManager()
	if stateManager == nil {
		return fmt.Errorf("统一任务状态管理器未初始化")
	}

	global.APP_LOG.Info("使用统一管理器完成配置任务", zap.Uint("taskId", task.ID))
	return stateManager.CompleteConfigTask(task.ID, success, errorMessage, resultData)
}

// convertToResponse 转换为响应格式
func (s *TaskService) convertToResponse(task admin.ConfigurationTask) admin.ConfigurationTaskResponse {
	response := admin.ConfigurationTaskResponse{
		ID:           task.ID,
		ProviderID:   task.ProviderID,
		TaskType:     task.TaskType,
		Status:       task.Status,
		Progress:     task.Progress,
		StartedAt:    task.StartedAt,
		CompletedAt:  task.CompletedAt,
		ExecutorID:   task.ExecutorID,
		ExecutorName: task.ExecutorName,
		Success:      task.Success,
		ErrorMessage: task.ErrorMessage,
		LogSummary:   task.LogSummary,
		CreatedAt:    task.CreatedAt,
		UpdatedAt:    task.UpdatedAt,
	}

	// 添加Provider信息
	if task.Provider != nil {
		response.ProviderName = task.Provider.Name
		response.ProviderType = task.Provider.Type
	}

	// 计算时长
	if task.StartedAt != nil {
		endTime := time.Now()
		if task.CompletedAt != nil {
			endTime = *task.CompletedAt
		}
		duration := endTime.Sub(*task.StartedAt)
		response.Duration = duration.Round(time.Second).String()
	}

	return response
}

// convertToDetailResponse 转换为详细响应格式（包含完整日志）
func (s *TaskService) convertToDetailResponse(task admin.ConfigurationTask) admin.ConfigurationTaskDetailResponse {
	baseResponse := s.convertToResponse(task)

	detail := admin.ConfigurationTaskDetailResponse{
		ConfigurationTaskResponse: baseResponse,
		LogOutput:                 task.LogOutput,
	}

	// 解析结果数据
	if task.ResultData != "" {
		var resultData map[string]interface{}
		if err := json.Unmarshal([]byte(task.ResultData), &resultData); err == nil {
			detail.ResultData = resultData
		}
	}

	return detail
}
