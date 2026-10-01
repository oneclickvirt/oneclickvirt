package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
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
	Context    context.Context
	CancelFunc context.CancelFunc
	// Done closes only after the terminal state is persisted and the Provider
	// reservation is released. Forced reruns wait on this signal instead of
	// polling the database while a remote call unwinds.
	Done       chan struct{}
	finishMu   sync.Mutex
	completion *configCompletion
	retrying   bool
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

	// This method runs when the process-local service is first created. No
	// worker from a previous process can still call FinishTask, so leaving these
	// rows in "cancelling" would permanently block the Provider queue.
	now := time.Now()
	result := global.APP_DB.Model(&admin.ConfigurationTask{}).
		Where("status IN ?", []string{admin.TaskStatusPending, admin.TaskStatusRunning, admin.TaskStatusCancelling}).
		Updates(map[string]interface{}{
			"status":        admin.TaskStatusCancelled,
			"success":       false,
			"error_message": "服务重启，任务已取消",
			"completed_at":  &now,
		})
	if result.Error != nil {
		global.APP_LOG.Warn("清理未完成配置任务失败", zap.Error(result.Error))
		return
	}
	if result.RowsAffected > 0 {
		global.APP_LOG.Info("已清理服务重启前未完成的配置任务", zap.Int64("count", result.RowsAffected))
	}
}

// GetRunningTask 获取Provider的运行中任务
func (s *TaskService) GetRunningTask(providerID uint) *admin.ConfigurationTask {
	s.mutex.RLock()
	defer s.mutex.RUnlock()

	if ctx, exists := s.runningTasks[providerID]; exists {
		// Do not expose the mutable ownership record to API callers; cancellation
		// updates its status while holding the service mutex.
		taskCopy := *ctx.Task
		return &taskCopy
	}
	return nil
}

// GetTaskContext 返回可由取消操作终止的任务Context。
func (s *TaskService) GetTaskContext(taskID uint) context.Context {
	s.mutex.RLock()
	for _, taskCtx := range s.runningTasks {
		if taskCtx.Task.ID == taskID {
			s.mutex.RUnlock()
			return taskCtx.Context
		}
	}
	s.mutex.RUnlock()

	// If a cancelled task has no in-memory entry (for example after a restart),
	// return an already-cancelled context instead of silently falling back to the
	// process context and continuing the work.
	if global.APP_DB != nil {
		var task admin.ConfigurationTask
		if err := global.APP_DB.Select("status").First(&task, taskID).Error; err == nil && (task.Status == admin.TaskStatusCancelled || task.Status == admin.TaskStatusCancelling) {
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			return cancelled
		}
	}
	return nil
}

// WaitForTaskRelease waits until a cancelled task has finished unwinding and
// released its Provider slot. Forced reruns use this barrier so a second
// configuration operation cannot overlap a still-running provider call.
func (s *TaskService) WaitForTaskRelease(providerID, taskID uint, timeout time.Duration) error {
	s.retryFinishedTask(providerID)
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	waitCtx, waitCancel := context.WithTimeout(context.Background(), timeout)
	defer waitCancel()

	// The normal path is process-local: FinishTask closes Done after the
	// terminal database transition. This avoids a tight database polling loop
	// while an SSH/API request is unwinding.
	s.mutex.RLock()
	taskCtx, owned := s.runningTasks[providerID]
	owned = owned && taskCtx.Task.ID == taskID
	var released <-chan struct{}
	if owned {
		released = taskCtx.Done
	}
	s.mutex.RUnlock()
	if owned && released != nil {
		select {
		case <-released:
			return nil
		case <-waitCtx.Done():
			return fmt.Errorf("上一个配置任务仍在取消，请稍后重试")
		}
	}

	// A missing in-memory owner can only happen after a process restart or when
	// a legacy caller created a service without a release channel. Check the
	// durable state once, then use a low-frequency fallback for that recovery
	// case instead of turning every forced rerun into dozens of queries.
	if global.APP_DB == nil {
		return fmt.Errorf("数据库连接不可用，无法确认配置任务是否已停止")
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var active admin.ConfigurationTask
		activeQuery := global.APP_DB.WithContext(waitCtx).Model(&admin.ConfigurationTask{}).
			Select("id").
			Where("id = ? AND provider_id = ? AND status IN ?", taskID, providerID, []string{admin.TaskStatusRunning, admin.TaskStatusCancelling}).
			Limit(1).Find(&active)
		if activeQuery.Error != nil {
			return fmt.Errorf("查询配置任务状态失败: %w", activeQuery.Error)
		}
		if activeQuery.RowsAffected == 0 {
			return nil
		}
		select {
		case <-waitCtx.Done():
			return fmt.Errorf("上一个配置任务仍在取消，请稍后重试")
		case <-ticker.C:
		}
	}
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
	return s.CancelTaskScoped(taskID, 0)
}

// CancelTaskScoped 取消配置任务，ownerAdminID非零时限定Provider归属。
func (s *TaskService) CancelTaskScoped(taskID, ownerAdminID uint) error {

	var task admin.ConfigurationTask
	query := global.APP_DB.Where("configuration_tasks.id = ?", taskID)
	if ownerAdminID > 0 {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).
			Select("id").
			Where("owner_admin_id = ?", ownerAdminID)
		query = query.Where("configuration_tasks.provider_id IN (?)", providerIDs)
	}
	if err := query.First(&task).Error; err != nil {
		return fmt.Errorf("任务不存在或无权限: %w", err)
	}

	// 重复取消保持幂等，同时再次通知仍在退出的执行协程。
	if task.Status == admin.TaskStatusCancelled || task.Status == admin.TaskStatusCancelling {
		s.cancelRunningTaskContext(task.ProviderID, task.ID)
		return nil
	}

	// 只能取消等待中或运行中的任务
	if task.Status != admin.TaskStatusPending && task.Status != admin.TaskStatusRunning {
		return fmt.Errorf("只能取消等待中或运行中的任务，当前状态：%s", task.Status)
	}

	// 使用统一任务状态管理器取消任务
	stateManager := taskManager.GetTaskStateManager()
	if stateManager == nil {
		return fmt.Errorf("统一任务状态管理器未初始化")
	}

	global.APP_LOG.Info("使用统一管理器取消配置任务", zap.Uint("taskId", task.ID))
	if err := stateManager.CancelConfigTask(task.ID, "任务已被取消"); err != nil {
		return err
	}

	// 保留 runningTasks 项，直到 FinishTask 真正收到完成回调。立即删除会让
	// 强制重跑绕过 Provider 级互斥，在旧远端操作尚未返回时启动第二个操作。
	s.cancelRunningTaskContext(task.ProviderID, task.ID)
	return nil
}

// cancelRunningTaskContext signals a matching execution context without
// removing its ownership entry. FinishTask removes the entry after the worker
// has returned, keeping forced reruns serialized per Provider.
func (s *TaskService) cancelRunningTaskContext(providerID, taskID uint) {
	s.mutex.Lock()
	taskCtx, exists := s.runningTasks[providerID]
	if exists && taskCtx.Task.ID == taskID && taskCtx.CancelFunc != nil {
		taskCtx.Task.Status = admin.TaskStatusCancelling
		taskCtx.CancelFunc()
	}
	s.mutex.Unlock()
}

// CreateTask 创建任务
func (s *TaskService) CreateTask(providerID uint, taskType string, userID uint, username string) (*admin.ConfigurationTask, error) {
	if err := taskManager.GetTaskService().EnsureTaskPoolAccepting(); err != nil {
		return nil, err
	}

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
		global.APP_LOG.Error("配置任务状态不允许启动",
			zap.Uint("taskID", taskID),
			zap.String("status", task.Status))
		return fmt.Errorf("任务状态不允许启动: %s", task.Status)
	}

	s.retryFinishedTask(task.ProviderID)

	// The ownership map is process-local. Check it without holding the mutex
	// across any database operation; a slow state transition must not block
	// cancellation or status reads for every other Provider.
	var existing admin.ConfigurationTask
	if err := global.APP_DB.Where(
		"provider_id = ? AND id <> ? AND (status IN ? OR (status = ? AND id < ?))",
		task.ProviderID,
		task.ID,
		[]string{admin.TaskStatusRunning, admin.TaskStatusCancelling},
		admin.TaskStatusPending,
		task.ID,
	).Order("id ASC").First(&existing).Error; err == nil {
		global.APP_LOG.Error("Provider已有正在运行的任务",
			zap.Uint("providerID", task.ProviderID),
			zap.Uint("existingTaskID", existing.ID),
			zap.Uint("newTaskID", taskID))
		return fmt.Errorf("Provider %d 已有配置任务 %d", task.ProviderID, existing.ID)
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("检查Provider配置任务失败: %w", err)
	}

	stateManager := taskManager.GetTaskStateManager()
	if stateManager == nil {
		return fmt.Errorf("统一任务状态管理器未初始化")
	}

	baseContext := global.APP_SHUTDOWN_CONTEXT
	if baseContext == nil {
		baseContext = context.Background()
	}
	runContext, cancel := context.WithCancel(baseContext)
	taskCtx := &TaskContext{
		Task:       &task,
		Context:    runContext,
		CancelFunc: cancel,
		Done:       make(chan struct{}),
	}

	// Reserve the Provider slot before the database transition. Cancellation
	// may race this transition; keeping this ownership record in place prevents
	// a forced rerun from overlapping the remote operation while the CAS settles.
	s.mutex.Lock()
	if ctx, exists := s.runningTasks[task.ProviderID]; exists {
		s.mutex.Unlock()
		cancel()
		global.APP_LOG.Error("Provider已有正在运行的任务",
			zap.Uint("providerID", task.ProviderID),
			zap.Uint("existingTaskID", ctx.Task.ID),
			zap.Uint("newTaskID", taskID))
		return fmt.Errorf("Provider %d 已有正在运行的任务 %d", task.ProviderID, ctx.Task.ID)
	}
	s.runningTasks[task.ProviderID] = taskCtx
	s.mutex.Unlock()

	// Perform the database transition outside the process-wide mutex. If it
	// loses a cancellation or completion race, remove only this reservation.
	if err := stateManager.StartConfigTask(task.ID); err != nil {
		s.removeRunningTask(task.ProviderID, task.ID, taskCtx)
		cancel()
		return fmt.Errorf("启动配置任务失败: %w", err)
	}

	s.mutex.Lock()
	current, exists := s.runningTasks[task.ProviderID]
	if !exists || current != taskCtx {
		s.mutex.Unlock()
		cancel()
		return fmt.Errorf("配置任务启动状态已被其他流程接管")
	}
	// Keep the in-memory ownership record in sync with the atomic DB transition.
	if taskCtx.Context.Err() == nil {
		taskCtx.Task.Status = admin.TaskStatusRunning
	}
	now := time.Now()
	taskCtx.Task.StartedAt = &now
	s.mutex.Unlock()

	return nil
}

// removeRunningTask releases a reservation only when it still belongs to the
// task that created it. A later task for the same Provider must never be
// removed by an earlier failed start or completion callback.
func (s *TaskService) removeRunningTask(providerID, taskID uint, expected *TaskContext) context.CancelFunc {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	current, exists := s.runningTasks[providerID]
	if !exists || current.Task.ID != taskID || (expected != nil && current != expected) {
		return nil
	}
	delete(s.runningTasks, providerID)
	if current.Done != nil {
		close(current.Done)
	}
	return current.CancelFunc
}

// GetTaskList 获取任务列表
func (s *TaskService) GetTaskList(req *admin.ConfigurationTaskListRequest, ownerAdminID uint) ([]admin.ConfigurationTaskResponse, int64, error) {
	db := global.APP_DB.Model(&admin.ConfigurationTask{}).
		Preload("Provider")
	if ownerAdminID > 0 {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).
			Select("id").
			Where("owner_admin_id = ?", ownerAdminID)
		db = db.Where("configuration_tasks.provider_id IN (?)", providerIDs)
	}

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
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

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
func (s *TaskService) GetTaskDetail(taskID, ownerAdminID uint) (*admin.ConfigurationTaskDetailResponse, error) {
	var task admin.ConfigurationTask
	query := global.APP_DB.Preload("Provider").Where("configuration_tasks.id = ?", taskID)
	if ownerAdminID > 0 {
		providerIDs := global.APP_DB.Model(&providerModel.Provider{}).
			Select("id").
			Where("owner_admin_id = ?", ownerAdminID)
		query = query.Where("configuration_tasks.provider_id IN (?)", providerIDs)
	}
	if err := query.First(&task).Error; err != nil {
		return nil, fmt.Errorf("任务不存在或无权限: %w", err)
	}

	response := s.convertToDetailResponse(task)
	return &response, nil
}

// UpdateTaskLog 更新任务日志
func (s *TaskService) UpdateTaskLog(taskID uint, logMessage string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return global.APP_DB.WithContext(ctx).Model(&admin.ConfigurationTask{}).
		Where("id = ? AND status IN ?", taskID, []string{admin.TaskStatusPending, admin.TaskStatusRunning, admin.TaskStatusCancelling}).
		Update("log_output", logMessage).Error
}

// UpdateTaskProgress 更新任务进度
func (s *TaskService) UpdateTaskProgress(taskID uint, progress int) error {
	if progress < 0 {
		progress = 0
	}
	if progress > 100 {
		progress = 100
	}
	return global.APP_DB.Model(&admin.ConfigurationTask{}).
		Where("id = ? AND status IN ? AND progress <= ?", taskID,
			[]string{admin.TaskStatusPending, admin.TaskStatusRunning}, progress).
		Update("progress", progress).Error
}

// FinishTask 完成任务
type configCompletion struct {
	success      bool
	errorMessage string
	resultData   map[string]interface{}
}

func (s *TaskService) FinishTask(taskID uint, success bool, errorMessage string, resultData map[string]interface{}) error {
	completion := &configCompletion{success, errorMessage, resultData}
	var owner *TaskContext
	s.mutex.Lock()
	for _, candidate := range s.runningTasks {
		if candidate.Task.ID == taskID {
			owner = candidate
			// Keep the original result if an HTTP retry races the first callback.
			if owner.completion == nil {
				owner.completion = completion
			}
			completion = owner.completion
			break
		}
	}
	s.mutex.Unlock()
	if owner == nil {
		return taskManager.GetTaskStateManager().CompleteConfigTask(taskID, success, errorMessage, resultData)
	}
	if err := s.persistCompletion(owner, completion); err != nil {
		s.retryFinishedTask(owner.Task.ProviderID)
		return err
	}
	return nil
}

func (s *TaskService) persistCompletion(owner *TaskContext, result *configCompletion) error {
	owner.finishMu.Lock()
	defer owner.finishMu.Unlock()
	s.mutex.RLock()
	current := s.runningTasks[owner.Task.ProviderID]
	s.mutex.RUnlock()
	if current != owner {
		return nil
	}
	stateManager := taskManager.GetTaskStateManager()
	if stateManager == nil {
		return fmt.Errorf("统一任务状态管理器未初始化")
	}
	if err := stateManager.CompleteConfigTask(owner.Task.ID, result.success, result.errorMessage, result.resultData); err != nil {
		return err
	}
	if cancel := s.removeRunningTask(owner.Task.ProviderID, owner.Task.ID, owner); cancel != nil {
		cancel()
	}
	return nil
}

// Only retry persistence, never rerun the provider operation. Retries are
// bounded; a later start/forced rerun can try again after the DB recovers.
func (s *TaskService) retryFinishedTask(providerID uint) {
	s.mutex.Lock()
	owner := s.runningTasks[providerID]
	if owner == nil || owner.completion == nil || owner.retrying {
		s.mutex.Unlock()
		return
	}
	owner.retrying = true
	result := owner.completion
	s.mutex.Unlock()
	go func() {
		defer func() { s.mutex.Lock(); owner.retrying = false; s.mutex.Unlock() }()
		for attempt := 0; attempt < 3; attempt++ {
			if attempt > 0 {
				time.Sleep(time.Duration(attempt) * time.Second)
			}
			if err := s.persistCompletion(owner, result); err == nil {
				return
			}
		}
		global.APP_LOG.Error("配置任务结果暂未保存，保留完成结果供重试", zap.Uint("taskId", owner.Task.ID))
	}()
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

	// Provider信息
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
