package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/provider/incus"
	"oneclickvirt/provider/lxd"
	"oneclickvirt/provider/portmapping"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	"oneclickvirt/service/provider"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/resources"
	"oneclickvirt/service/traffic"
	userprovider "oneclickvirt/service/user/provider"
	"oneclickvirt/service/vnstat"
	"sort"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	dashboardModel "oneclickvirt/model/dashboard"
	providerModel "oneclickvirt/model/provider"
	systemModel "oneclickvirt/model/system"
	userModel "oneclickvirt/model/user"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// TaskRequest 任务请求
type TaskRequest struct {
	Task       adminModel.Task
	ResponseCh chan TaskResult // 用于接收任务结果
}

// TaskResult 任务结果
type TaskResult struct {
	Success bool
	Error   error
	Data    map[string]interface{}
}

// ProviderWorkerPool Provider工作池
type ProviderWorkerPool struct {
	ProviderID  uint
	TaskQueue   chan TaskRequest   // 任务队列
	WorkerCount int                // 工作者数量（并发数）
	Ctx         context.Context    // 上下文
	Cancel      context.CancelFunc // 取消函数
	TaskService *TaskService       // 任务服务引用
}

// TaskContext 任务执行上下文
type TaskContext struct {
	TaskID     uint
	Context    context.Context
	CancelFunc context.CancelFunc
	StartTime  time.Time
}

// TaskService 任务管理服务 - 简化版本使用Channel池
type TaskService struct {
	dbService       *database.DatabaseService
	runningContexts map[uint]*TaskContext        // 正在执行的任务上下文，用于取消操作
	contextMutex    sync.RWMutex                 // 保护runningContexts的锁
	providerPools   map[uint]*ProviderWorkerPool // Provider工作池
	poolMutex       sync.RWMutex                 // 保护providerPools的锁
	shutdown        chan struct{}                // 系统关闭信号
	wg              sync.WaitGroup               // 用于等待所有goroutine完成
	ctx             context.Context              // 服务级别的context
	cancel          context.CancelFunc           // 服务级别的cancel函数
}

var (
	taskService     *TaskService
	taskServiceOnce sync.Once
)

// GetTaskService 获取任务服务单例
func GetTaskService() *TaskService {
	taskServiceOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		taskService = &TaskService{
			dbService:       database.GetDatabaseService(),
			runningContexts: make(map[uint]*TaskContext),
			providerPools:   make(map[uint]*ProviderWorkerPool),
			shutdown:        make(chan struct{}),
			ctx:             ctx,
			cancel:          cancel,
		}
		// 设置全局任务锁释放器
		// 使用channel池实现并发控制，无需额外的锁释放
		global.APP_TASK_LOCK_RELEASER = taskService

		// 初始化统一任务状态管理器
		InitTaskStateManager(taskService)

		// 只有在数据库已初始化时才清理running状态的任务
		if isSystemInitialized() {
			taskService.cleanupRunningTasksOnStartup()
		} else {
			global.APP_LOG.Debug("系统未初始化，跳过任务清理")
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

// cleanupRunningTasksOnStartup 服务启动时清理running状态的任务
func (s *TaskService) cleanupRunningTasksOnStartup() {
	// 再次检查数据库是否可用，防止在初始化过程中数据库状态发生变化
	if global.APP_DB == nil {
		global.APP_LOG.Warn("数据库连接不存在，无法清理运行中的任务")
		return
	}

	// 将所有running状态的任务标记为failed
	result := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ?", "running").
		Updates(map[string]interface{}{
			"status":        "failed",
			"error_message": "服务重启，任务被中断",
			"completed_at":  time.Now(),
		})

	if result.Error != nil {
		global.APP_LOG.Error("清理运行中任务失败", zap.Error(result.Error))
	} else if result.RowsAffected > 0 {
		global.APP_LOG.Info("服务启动时清理了运行中的任务", zap.Int64("count", result.RowsAffected))
	}

	// 内存计数器从空开始，不需要额外初始化
}

// Shutdown 优雅关闭任务服务，等待所有goroutine完成
func (s *TaskService) Shutdown() {
	global.APP_LOG.Info("开始关闭任务服务，等待所有后台任务完成...")

	// 发送关闭信号
	if s.cancel != nil {
		s.cancel()
	}
	select {
	case <-s.shutdown:
		// 已关闭
	default:
		close(s.shutdown)
	}

	// 关闭所有工作池
	s.poolMutex.Lock()
	for providerID, pool := range s.providerPools {
		global.APP_LOG.Info("关闭Provider工作池", zap.Uint("providerId", providerID))
		pool.Cancel()
	}
	s.poolMutex.Unlock()

	// 等待所有goroutine完成
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	// 等待最多30秒
	select {
	case <-done:
		global.APP_LOG.Info("所有后台任务已完成")
	case <-time.After(30 * time.Second):
		global.APP_LOG.Warn("等待后台任务超时，强制退出")
	}

	global.APP_LOG.Info("TaskService关闭完成")
}

// CreateTask 创建任务
func (s *TaskService) CreateTask(userID uint, providerID *uint, instanceID *uint, taskType string, taskData string, timeoutDuration int) (*adminModel.Task, error) {
	if timeoutDuration <= 0 {
		timeoutDuration = s.getDefaultTimeout(taskType)
	}

	task := &adminModel.Task{
		UserID:           userID,
		ProviderID:       providerID,
		InstanceID:       instanceID,
		TaskType:         taskType,
		Status:           "pending",
		TaskData:         taskData,
		TimeoutDuration:  timeoutDuration,
		IsForceStoppable: true,
	}

	err := s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		return tx.Create(task).Error
	})

	if err != nil {
		return nil, fmt.Errorf("创建任务失败: %v", err)
	}

	global.APP_LOG.Info("任务创建成功",
		zap.Uint("taskId", task.ID),
		zap.String("taskType", taskType),
		zap.Uint("userId", userID))

	return task, nil
}

// StartTask 启动任务 - 委托给新的实现
func (s *TaskService) StartTask(taskID uint) error {
	return s.StartTaskWithPool(taskID)
}

// executeTaskWithContext 删除 - 已由channel池架构替代
// 此方法已被删除，所有任务执行都通过StartTaskWithPool和worker pool进行

// startCancelListener 监听取消信号
func (s *TaskService) startCancelListener(taskID uint, ctx context.Context) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// 检查数据库中的任务状态
			var status string
			s.dbService.ExecuteQuery(context.Background(), func() error {
				return global.APP_DB.Model(&adminModel.Task{}).
					Where("id = ?", taskID).
					Select("status").Scan(&status).Error
			})

			if status == "cancelling" {
				s.contextMutex.RLock()
				if taskCtx, exists := s.runningContexts[taskID]; exists {
					taskCtx.CancelFunc()
				}
				s.contextMutex.RUnlock()
				return
			}
		}
	}
}

// executeTaskLogic 执行具体的任务逻辑
func (s *TaskService) executeTaskLogic(ctx context.Context, task *adminModel.Task) error {
	switch task.TaskType {
	case "create":
		return s.executeCreateInstanceTask(ctx, task)
	case "start":
		return s.executeStartInstanceTask(ctx, task)
	case "stop":
		return s.executeStopInstanceTask(ctx, task)
	case "restart":
		return s.executeRestartInstanceTask(ctx, task)
	case "delete":
		return s.executeDeleteInstanceTask(ctx, task)
	case "reset":
		return s.executeResetInstanceTask(ctx, task)
	case "reset-password":
		return s.executeResetPasswordTask(ctx, task)
	case "create-port-mapping":
		return s.executeCreatePortMappingTask(ctx, task)
	case "delete-port-mapping":
		return s.executeDeletePortMappingTask(ctx, task)
	default:
		return fmt.Errorf("未知的任务类型: %s", task.TaskType)
	}
}

// CompleteTask 完成任务
func (s *TaskService) CompleteTask(taskID uint, success bool, errorMessage string, resultData map[string]interface{}) error {
	// 首先获取任务信息以便释放provider计数器
	var task adminModel.Task
	err := global.APP_DB.First(&task, taskID).Error
	if err != nil {
		global.APP_LOG.Error("获取任务信息失败",
			zap.Uint("taskId", taskID),
			zap.Error(err))
		return err
	}

	// 幂等性检查：如果任务已经是完成状态，避免重复处理
	if task.Status == "completed" || task.Status == "failed" || task.Status == "cancelled" {
		global.APP_LOG.Info("任务已经是完成状态，跳过重复处理",
			zap.Uint("taskId", taskID),
			zap.String("currentStatus", task.Status),
			zap.Bool("requestedSuccess", success))
		return nil
	}

	now := time.Now()
	status := "completed"
	if !success {
		status = "failed"
	}

	err = s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		updates := map[string]interface{}{
			"status":       status,
			"completed_at": &now,
		}

		// 只在失败时设置 error_message，成功时不设置
		if !success && errorMessage != "" {
			updates["error_message"] = errorMessage
		}

		return tx.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(updates).Error
	})

	if err != nil {
		global.APP_LOG.Error("完成任务失败",
			zap.Uint("taskId", taskID),
			zap.Error(err))
		return err
	}

	// 如果任务失败且没有创建实例，释放预留资源
	if !success && task.InstanceID == nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.releaseTaskResources(taskID)
		}()
	}

	global.APP_LOG.Info("任务完成",
		zap.Uint("taskId", taskID),
		zap.Bool("success", success),
		zap.String("errorMessage", errorMessage))

	// 任务完成后，立即触发调度器检查pending任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
		global.APP_LOG.Debug("任务完成后触发调度器检查pending任务", zap.Uint("taskId", taskID))
	}

	return nil
}

// ReleaseTaskLocks 空实现 - channel池架构无需显式释放锁
func (s *TaskService) ReleaseTaskLocks(taskID uint) {
	// channel池架构自动处理并发控制，无需显式释放
}

// CancelTask 用户取消任务
func (s *TaskService) CancelTask(taskID uint, userID uint) error {
	err := s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		var task adminModel.Task
		err := tx.Where("id = ? AND user_id = ?", taskID, userID).First(&task).Error
		if err != nil {
			return fmt.Errorf("任务不存在或无权限")
		}

		// 检查任务是否允许被用户取消
		if !task.IsForceStoppable {
			return fmt.Errorf("此任务不允许取消（管理员操作）")
		}

		switch task.Status {
		case "pending":
			return s.cancelPendingTask(tx, taskID, "用户取消")
		case "running":
			return s.cancelRunningTask(tx, taskID, "用户取消")
		default:
			return fmt.Errorf("任务状态[%s]不允许取消", task.Status)
		}
	})

	return err
}

// CancelTaskByAdmin 管理员取消/强制停止任务
func (s *TaskService) CancelTaskByAdmin(taskID uint, reason string) error {
	err := s.dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		var task adminModel.Task
		err := tx.First(&task, taskID).Error
		if err != nil {
			return fmt.Errorf("任务不存在")
		}

		switch task.Status {
		case "pending":
			return s.cancelPendingTask(tx, taskID, fmt.Sprintf("管理员取消: %s", reason))
		case "processing", "running":
			// processing和running状态都使用强制停止
			return s.forceStopRunningTask(tx, taskID, fmt.Sprintf("管理员强制停止: %s", reason))
		case "cancelling":
			return s.forceKillTask(tx, taskID, fmt.Sprintf("管理员强制终止: %s", reason))
		default:
			return fmt.Errorf("任务状态[%s]不允许操作", task.Status)
		}
	})

	// 任务取消成功后，如果是删除任务，触发资源释放
	if err == nil {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handleCancelledTaskCleanup(taskID)
		}()
	}

	return err
}

// cancelPendingTask 取消pending状态的任务
func (s *TaskService) cancelPendingTask(tx *gorm.DB, taskID uint, reason string) error {
	now := time.Now()
	result := tx.Model(&adminModel.Task{}).
		Where("id = ? AND status = ?", taskID, "pending").
		Updates(map[string]interface{}{
			"status":        "cancelled",
			"cancel_reason": reason,
			"completed_at":  &now,
		})

	if result.RowsAffected == 0 {
		return fmt.Errorf("任务状态已变更，无法取消")
	}

	// 释放预留资源
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.releaseTaskResources(taskID)
	}()

	return nil
}

// cancelRunningTask 取消running状态的任务
func (s *TaskService) cancelRunningTask(tx *gorm.DB, taskID uint, reason string) error {
	// 1. 更新状态为cancelling
	result := tx.Model(&adminModel.Task{}).
		Where("id = ? AND status = ?", taskID, "running").
		Updates(map[string]interface{}{
			"status":        "cancelling",
			"cancel_reason": reason,
		})

	if result.RowsAffected == 0 {
		return fmt.Errorf("任务状态已变更，无法取消")
	}

	// 2. 发送取消信号（异步处理，避免阻塞事务）
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.contextMutex.RLock()
		if taskCtx, exists := s.runningContexts[taskID]; exists {
			taskCtx.CancelFunc()
		}
		s.contextMutex.RUnlock()
	}()

	return nil
}

// forceStopRunningTask 强制停止running状态的任务
func (s *TaskService) forceStopRunningTask(tx *gorm.DB, taskID uint, reason string) error {
	return s.forceKillTask(tx, taskID, reason)
}

// forceKillTask 强制终止任务
func (s *TaskService) forceKillTask(tx *gorm.DB, taskID uint, reason string) error {
	now := time.Now()
	err := tx.Model(&adminModel.Task{}).Where("id = ?", taskID).Updates(map[string]interface{}{
		"status":        "cancelled",
		"cancel_reason": reason,
		"completed_at":  &now,
	}).Error

	if err != nil {
		return err
	}

	// 强制清理上下文（异步处理）
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		// 获取任务信息以便记录日志
		var task adminModel.Task
		if err := global.APP_DB.First(&task, taskID).Error; err == nil {
			if task.ProviderID != nil {
				global.APP_LOG.Debug("强制取消任务",
					zap.Uint("task_id", taskID),
					zap.Uint("provider_id", *task.ProviderID))
			}
		}

		s.contextMutex.Lock()
		if taskCtx, exists := s.runningContexts[taskID]; exists {
			taskCtx.CancelFunc()
			delete(s.runningContexts, taskID)
		}
		s.contextMutex.Unlock()

		// 释放资源
		s.releaseTaskResources(taskID)
	}()

	return nil
}

// handleCancelledTaskCleanup 处理被取消任务的清理工作（特别是删除任务）
func (s *TaskService) handleCancelledTaskCleanup(taskID uint) {
	var task adminModel.Task
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		global.APP_LOG.Error("获取被取消任务信息失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	// 如果是删除任务，需要进行资源清理
	if task.TaskType == "delete" && task.InstanceID != nil {
		global.APP_LOG.Info("开始清理被取消的删除任务的资源",
			zap.Uint("taskId", taskID),
			zap.Uint("instanceId", *task.InstanceID))

		// 解析任务数据
		var taskReq adminModel.DeleteInstanceTaskRequest
		if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
			global.APP_LOG.Error("解析删除任务数据失败", zap.Uint("taskId", taskID), zap.Error(err))
			return
		}

		// 获取实例信息
		var instance providerModel.Instance
		if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				global.APP_LOG.Error("获取实例信息失败", zap.Uint("instanceId", *task.InstanceID), zap.Error(err))
			}
			return
		}

		// 恢复实例状态（如果是deleting状态）
		if instance.Status == "deleting" {
			// 尝试恢复到之前的状态，如果无法确定则设为stopped
			newStatus := "stopped"
			if err := global.APP_DB.Model(&instance).Update("status", newStatus).Error; err != nil {
				global.APP_LOG.Error("恢复实例状态失败",
					zap.Uint("instanceId", instance.ID),
					zap.String("newStatus", newStatus),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("已恢复被取消删除任务的实例状态",
					zap.Uint("instanceId", instance.ID),
					zap.String("status", newStatus))
			}
		}
	}
}

// releaseTaskResources 释放任务资源
func (s *TaskService) releaseTaskResources(taskID uint) {
	// 获取任务信息以提取sessionId
	var task adminModel.Task
	if err := global.APP_DB.First(&task, taskID).Error; err != nil {
		global.APP_LOG.Error("获取任务信息失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	// 解析任务数据以获取sessionId
	var taskData map[string]interface{}
	if err := json.Unmarshal([]byte(task.TaskData), &taskData); err != nil {
		global.APP_LOG.Error("解析任务数据失败", zap.Uint("taskId", taskID), zap.Error(err))
		return
	}

	sessionID, ok := taskData["sessionId"].(string)
	if !ok || sessionID == "" {
		global.APP_LOG.Warn("任务数据中没有sessionId", zap.Uint("taskId", taskID))
		return
	}

	// 释放预留资源
	reservationService := resources.GetResourceReservationService()
	if err := reservationService.ReleaseReservationBySession(sessionID); err != nil {
		global.APP_LOG.Warn("释放预留资源失败",
			zap.Uint("taskId", taskID),
			zap.String("sessionId", sessionID),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("任务预留资源已释放",
			zap.Uint("taskId", taskID),
			zap.String("sessionId", sessionID))
	}
}

// GetUserTasks 获取用户任务列表
func (s *TaskService) GetUserTasks(userID uint, req userModel.UserTasksRequest) ([]userModel.TaskResponse, int64, error) {
	var tasks []adminModel.Task
	var total int64

	err := s.dbService.ExecuteQuery(context.Background(), func() error {
		query := global.APP_DB.Model(&adminModel.Task{}).Where("user_id = ?", userID)

		// 应用筛选条件
		if req.ProviderId != 0 {
			query = query.Where("provider_id = ?", req.ProviderId)
		}
		if req.TaskType != "" {
			query = query.Where("task_type = ?", req.TaskType)
		}
		if req.Status != "" {
			query = query.Where("status = ?", req.Status)
		}

		// 获取总数
		if err := query.Count(&total).Error; err != nil {
			return err
		}

		// 获取任务列表
		offset := (req.Page - 1) * req.PageSize
		return query.Preload("Provider").
			Order("created_at DESC").
			Offset(offset).Limit(req.PageSize).
			Find(&tasks).Error
	})

	if err != nil {
		return nil, 0, err
	}

	// 转换为响应格式
	var taskResponses []userModel.TaskResponse
	for _, task := range tasks {
		taskResponse := userModel.TaskResponse{
			ID:              task.ID,
			UUID:            task.UUID,
			TaskType:        task.TaskType,
			Status:          task.Status,
			Progress:        task.Progress,
			ErrorMessage:    task.ErrorMessage,
			CancelReason:    task.CancelReason,
			CreatedAt:       task.CreatedAt,
			StartedAt:       task.StartedAt,
			CompletedAt:     task.CompletedAt,
			TimeoutDuration: task.TimeoutDuration,
			StatusMessage:   task.StatusMessage,
		}

		// 设置ProviderId和ProviderName
		if task.ProviderID != nil {
			taskResponse.ProviderId = *task.ProviderID
		}
		if task.Provider != nil {
			taskResponse.ProviderName = task.Provider.Name
		}

		// 设置InstanceID和InstanceName
		if task.InstanceID != nil {
			taskResponse.InstanceID = task.InstanceID
			// 获取实例名称
			var instance providerModel.Instance
			if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err == nil {
				taskResponse.InstanceName = instance.Name
			}
		}

		// 计算剩余时间
		if task.Status == "running" && task.StartedAt != nil {
			elapsed := time.Since(*task.StartedAt).Seconds()
			remaining := float64(task.TimeoutDuration) - elapsed
			if remaining > 0 {
				taskResponse.RemainingTime = int(remaining)
			}
		}

		// 设置是否可取消（考虑任务状态和是否允许被用户取消）
		taskResponse.CanCancel = (task.Status == "pending" || task.Status == "running") && task.IsForceStoppable

		taskResponses = append(taskResponses, taskResponse)
	}

	return taskResponses, total, nil
}

// GetAdminTasks 获取管理员任务列表
func (s *TaskService) GetAdminTasks(req adminModel.AdminTaskListRequest) ([]adminModel.AdminTaskResponse, int64, error) {
	var tasks []adminModel.Task
	var total int64

	err := s.dbService.ExecuteQuery(context.Background(), func() error {
		query := global.APP_DB.Model(&adminModel.Task{})

		// 应用筛选条件
		if req.ProviderID != 0 {
			query = query.Where("provider_id = ?", req.ProviderID)
		}
		if req.Username != "" {
			// 通过用户名搜索，需要连接 users 表
			query = query.Joins("LEFT JOIN users ON users.id = tasks.user_id").
				Where("users.username LIKE ?", "%"+req.Username+"%")
		}
		if req.TaskType != "" {
			query = query.Where("task_type = ?", req.TaskType)
		}
		if req.Status != "" {
			query = query.Where("status = ?", req.Status)
		}
		if req.InstanceType != "" {
			query = query.Joins("LEFT JOIN instances ON instances.id = tasks.instance_id").
				Where("instances.instance_type = ?", req.InstanceType)
		}

		// 获取总数
		if err := query.Count(&total).Error; err != nil {
			return err
		}

		// 获取任务列表
		offset := (req.Page - 1) * req.PageSize
		return query.Order("created_at DESC").
			Offset(offset).Limit(req.PageSize).
			Find(&tasks).Error
	})

	if err != nil {
		return nil, 0, err
	}

	// 转换为响应格式
	var taskResponses []adminModel.AdminTaskResponse
	for _, task := range tasks {
		var providerID uint
		if task.ProviderID != nil {
			providerID = *task.ProviderID
		}

		// 计算剩余时间
		remainingTime := 0
		if task.Status == "running" && task.StartedAt != nil {
			elapsed := time.Since(*task.StartedAt).Seconds()
			remaining := float64(task.TimeoutDuration) - elapsed
			if remaining > 0 {
				remainingTime = int(remaining)
			}
		}

		taskResponse := adminModel.AdminTaskResponse{
			ID:              task.ID,
			UUID:            task.UUID,
			TaskType:        task.TaskType,
			Status:          task.Status,
			Progress:        task.Progress,
			ErrorMessage:    task.ErrorMessage,
			CancelReason:    task.CancelReason,
			CreatedAt:       task.CreatedAt,
			StartedAt:       task.StartedAt,
			CompletedAt:     task.CompletedAt,
			TimeoutDuration: task.TimeoutDuration,
			StatusMessage:   task.StatusMessage,
			UserID:          task.UserID,
			ProviderID:      &providerID,
			// 管理员可以强制停止processing, running, cancelling状态的任务
			CanForceStop:     (task.Status == "processing" || task.Status == "running" || task.Status == "cancelling"),
			IsForceStoppable: task.IsForceStoppable,
			RemainingTime:    remainingTime,
		}

		if task.UserID != 0 {
			var user userModel.User
			if err := global.APP_DB.First(&user, task.UserID).Error; err == nil {
				taskResponse.UserName = user.Username
			}
		}

		if task.ProviderID != nil {
			var provider providerModel.Provider
			if err := global.APP_DB.First(&provider, *task.ProviderID).Error; err == nil {
				taskResponse.ProviderName = provider.Name
			}
		}

		if task.InstanceID != nil {
			var instance providerModel.Instance
			if err := global.APP_DB.First(&instance, *task.InstanceID).Error; err == nil {
				taskResponse.InstanceID = &instance.ID
				taskResponse.InstanceName = instance.Name
				taskResponse.InstanceType = instance.InstanceType
			}
		}

		taskResponses = append(taskResponses, taskResponse)
	}

	return taskResponses, total, nil
}

// getDefaultTimeout 获取默认超时时间
func (s *TaskService) getDefaultTimeout(taskType string) int {
	timeouts := map[string]int{
		"create":              1800, // 30分钟
		"start":               300,  // 5分钟
		"stop":                300,  // 5分钟
		"restart":             600,  // 10分钟
		"reset":               1200, // 20分钟
		"delete":              600,  // 10分钟
		"create-port-mapping": 600,  // 10分钟
		"delete-port-mapping": 300,  // 5分钟
		"reset-password":      600,  // 10分钟
	}

	if timeout, exists := timeouts[taskType]; exists {
		return timeout
	}
	return 1800 // 默认30分钟
}

// executeCreateInstanceTask 执行创建实例任务
func (s *TaskService) executeCreateInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 使用用户provider服务处理创建实例任务，避免循环依赖
	userProviderService := userprovider.NewService()
	return userProviderService.ProcessCreateInstanceTask(ctx, task)
}

// executeStartInstanceTask 执行启动实例任务
func (s *TaskService) executeStartInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 10, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.InstanceOperationTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 验证实例所有权
	if instance.UserID != task.UserID {
		return fmt.Errorf("无权限操作此实例")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在获取Provider配置...")

	// 获取Provider配置
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("获取Provider配置失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 50, "正在启动实例...")

	// 调用Provider启动实例，使用 Provider ID 确保使用正确的 Provider
	providerApiService := &provider2.ProviderApiService{}
	if err := providerApiService.StartInstanceByProviderID(ctx, provider.ID, instance.Name); err != nil {
		global.APP_LOG.Error("Provider启动实例失败",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Error(err))

		// 更新实例状态为启动失败
		global.APP_DB.Model(&instance).Update("status", "stopped")
		return fmt.Errorf("启动实例失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 70, "正在更新实例状态...")

	// 更新实例状态为运行中
	if err := global.APP_DB.Model(&instance).Update("status", "running").Error; err != nil {
		global.APP_LOG.Error("更新实例状态失败", zap.Error(err))
		return fmt.Errorf("更新实例状态失败: %v", err)
	}

	// 更新进度，但不立即标记完成
	s.updateTaskProgress(task.ID, 80, "正在初始化监控服务...")

	// 实例启动成功后，异步初始化vnStat监控和流量同步，完成后标记任务完成
	s.wg.Add(1)
	go func(instanceID uint, taskID uint) {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("启动实例后处理任务发生panic",
					zap.Uint("instanceId", instanceID),
					zap.Any("panic", r))
				// 即使后处理失败，也要标记任务完成，因为实例已经启动成功
				stateManager := GetTaskStateManager()
				if err := stateManager.CompleteMainTask(taskID, true, "实例启动成功，但部分监控服务初始化失败", nil); err != nil {
					global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", taskID), zap.Error(err))
				}
			}
		}()

		// 使用可取消的等待，而不是硬编码的Sleep
		select {
		case <-time.After(30 * time.Second):
			// 等待完成，继续执行
		case <-ctx.Done():
			// 任务被取消，停止后续处理
			global.APP_LOG.Info("启动实例后处理被取消",
				zap.Uint("instanceId", instanceID),
				zap.Uint("taskId", taskID))
			return
		}

		// 更新进度
		s.updateTaskProgress(taskID, 90, "正在初始化vnStat监控...")

		vnstatService := vnstat.NewService()
		vnstatSuccess := true
		if vnstatErr := vnstatService.InitializeVnStatForInstance(instanceID); vnstatErr != nil {
			global.APP_LOG.Warn("启动实例后初始化vnStat监控失败",
				zap.Uint("instanceId", instanceID),
				zap.Error(vnstatErr))
			vnstatSuccess = false
		} else {
			global.APP_LOG.Info("启动实例后vnStat监控初始化成功",
				zap.Uint("instanceId", instanceID))
		}

		// 更新进度
		s.updateTaskProgress(taskID, 95, "正在同步流量数据...")

		// 实例启动后同步流量数据，更新流量基准
		syncTrigger := traffic.NewSyncTriggerService()
		syncTrigger.TriggerInstanceTrafficSync(instanceID, "实例启动后同步")

		// 标记任务最终完成
		completionMessage := "实例启动成功"
		if !vnstatSuccess {
			completionMessage = "实例启动成功，但vnStat监控初始化失败"
		}
		stateManager := GetTaskStateManager()
		if err := stateManager.CompleteMainTask(taskID, true, completionMessage, nil); err != nil {
			global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", taskID), zap.Error(err))
		}

		global.APP_LOG.Info("启动实例后处理任务完成",
			zap.Uint("instanceId", instanceID),
			zap.Bool("vnstatSuccess", vnstatSuccess))
	}(instance.ID, task.ID)

	global.APP_LOG.Info("用户实例启动API调用成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID))

	return nil
}

// executeStopInstanceTask 执行停止实例任务
func (s *TaskService) executeStopInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 10, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.InstanceOperationTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 验证实例所有权
	if instance.UserID != task.UserID {
		return fmt.Errorf("无权限操作此实例")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在获取Provider配置...")

	// 获取Provider配置
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("获取Provider配置失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 50, "正在同步流量数据...")

	// 停止前同步最新流量数据，避免关机期间数据丢失
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instance.ID, "实例停止前同步")

	// 使用可取消的等待，而不是硬编码的Sleep
	select {
	case <-time.After(3 * time.Second):
		// 等待流量同步完成
	case <-ctx.Done():
		return fmt.Errorf("任务已取消")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 70, "正在停止实例...")

	// 调用Provider停止实例，使用 Provider ID 确保使用正确的 Provider
	providerApiService := &provider2.ProviderApiService{}
	if err := providerApiService.StopInstanceByProviderID(ctx, provider.ID, instance.Name); err != nil {
		global.APP_LOG.Error("Provider停止实例失败",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Error(err))

		// 更新实例状态为停止失败，恢复为运行状态
		global.APP_DB.Model(&instance).Update("status", "running")
		return fmt.Errorf("停止实例失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 90, "正在更新实例状态...")

	// 更新实例状态为已停止
	if err := global.APP_DB.Model(&instance).Update("status", "stopped").Error; err != nil {
		global.APP_LOG.Error("更新实例状态失败", zap.Error(err))
		return fmt.Errorf("更新实例状态失败: %v", err)
	}

	// 标记任务完成
	stateManager := GetTaskStateManager()
	if err := stateManager.CompleteMainTask(task.ID, true, "实例停止成功", nil); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("用户实例停止成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID))

	return nil
}

// executeRestartInstanceTask 执行重启实例任务
func (s *TaskService) executeRestartInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 10, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.InstanceOperationTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 验证实例所有权
	if instance.UserID != task.UserID {
		return fmt.Errorf("无权限操作此实例")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在获取Provider配置...")

	// 获取Provider配置
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("获取Provider配置失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 40, "正在同步流量数据...")

	// 重启前同步最新流量数据
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instance.ID, "实例重启前同步")

	// 使用可取消的等待
	select {
	case <-time.After(3 * time.Second):
		// 等待流量同步完成
	case <-ctx.Done():
		return fmt.Errorf("任务已取消")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 60, "正在重启实例...")

	// 调用Provider重启实例，使用 Provider ID 确保使用正确的 Provider
	providerApiService := &provider2.ProviderApiService{}

	if err := providerApiService.RestartInstanceByProviderID(ctx, provider.ID, instance.Name); err != nil {
		global.APP_LOG.Error("Provider重启实例失败",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Error(err))

		// 更新实例状态为重启失败，尝试获取当前状态
		instance.Status = "running" // 假设实例仍在运行
		global.APP_DB.Model(&instance).Update("status", "running")
		return fmt.Errorf("重启实例失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 80, "正在更新实例状态...")

	// 更新实例状态为运行中
	if err := global.APP_DB.Model(&instance).Update("status", "running").Error; err != nil {
		global.APP_LOG.Error("更新实例状态失败", zap.Error(err))
		return fmt.Errorf("更新实例状态失败: %v", err)
	}

	// 更新进度，但不立即标记完成
	s.updateTaskProgress(task.ID, 85, "正在重新初始化监控服务...")

	// 实例重启成功后，异步重新初始化vnStat监控，完成后标记任务完成
	s.wg.Add(1)
	go func(instanceID uint, taskID uint) {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("重启实例后处理任务发生panic",
					zap.Uint("instanceId", instanceID),
					zap.Any("panic", r))
				// 即使后处理失败，也要标记任务完成，因为实例已经重启成功
				stateManager := GetTaskStateManager()
				if err := stateManager.CompleteMainTask(taskID, true, "实例重启成功，但部分监控服务初始化失败", nil); err != nil {
					global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", taskID), zap.Error(err))
				}
			}
		}()

		// 使用可取消的等待
		select {
		case <-time.After(30 * time.Second):
			// 等待实例完全启动
		case <-ctx.Done():
			// 任务被取消，停止后续处理
			global.APP_LOG.Info("重启实例后处理被取消",
				zap.Uint("instanceId", instanceID),
				zap.Uint("taskId", taskID))
			return
		}

		// 更新进度
		s.updateTaskProgress(taskID, 90, "正在重新初始化vnStat监控...")

		vnstatService := vnstat.NewService()
		vnstatSuccess := true
		if vnstatErr := vnstatService.InitializeVnStatForInstance(instanceID); vnstatErr != nil {
			global.APP_LOG.Warn("重启实例后重新初始化vnStat监控失败",
				zap.Uint("instanceId", instanceID),
				zap.Error(vnstatErr))
			vnstatSuccess = false
		} else {
			global.APP_LOG.Info("重启实例后vnStat监控重新初始化成功",
				zap.Uint("instanceId", instanceID))
		}

		// 更新进度
		s.updateTaskProgress(taskID, 95, "正在同步流量数据...")

		// 重启后同步流量数据，更新流量基准
		syncTrigger := traffic.NewSyncTriggerService()
		syncTrigger.TriggerInstanceTrafficSync(instanceID, "实例重启后同步")

		// 标记任务最终完成
		completionMessage := "实例重启成功"
		if !vnstatSuccess {
			completionMessage = "实例重启成功，但vnStat监控重新初始化失败"
		}
		stateManager := GetTaskStateManager()
		if err := stateManager.CompleteMainTask(taskID, true, completionMessage, nil); err != nil {
			global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", taskID), zap.Error(err))
		}

		global.APP_LOG.Info("重启实例后处理任务完成",
			zap.Uint("instanceId", instanceID),
			zap.Bool("vnstatSuccess", vnstatSuccess))
	}(instance.ID, task.ID)

	global.APP_LOG.Info("用户实例重启API调用成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID))

	return nil
}

// executeDeleteInstanceTask 执行删除实例任务
func (s *TaskService) executeDeleteInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 10, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.DeleteInstanceTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 实例已不存在，标记任务完成
			stateManager := GetTaskStateManager()
			if err := stateManager.CompleteMainTask(task.ID, true, "实例已不存在，删除任务完成", nil); err != nil {
				global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
			}
			return nil
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 验证实例所有权 - 管理员操作跳过权限验证
	if !taskReq.AdminOperation && instance.UserID != task.UserID {
		return fmt.Errorf("无权限删除此实例")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在获取Provider配置...")

	// 获取Provider配置
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("获取Provider配置失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 40, "正在同步流量数据...")

	// 删除前进行最后一次流量同步，确保所有流量数据都被保存
	syncTrigger := traffic.NewSyncTriggerService()
	syncTrigger.TriggerInstanceTrafficSync(instance.ID, "实例删除前最终同步")

	// 使用可取消的等待
	select {
	case <-time.After(5 * time.Second):
		// 等待流量同步完成
	case <-ctx.Done():
		return fmt.Errorf("任务已取消")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 60, "正在删除实例...")

	// 调用Provider删除实例，重试机制
	providerApiService := &provider2.ProviderApiService{}
	maxRetries := global.APP_CONFIG.Task.DeleteRetryCount
	if maxRetries <= 0 {
		maxRetries = 3 // 默认重试3次
	}
	retryDelay := time.Duration(global.APP_CONFIG.Task.DeleteRetryDelay) * time.Second
	if retryDelay <= 0 {
		retryDelay = 2 * time.Second // 默认延迟2秒
	}
	var lastErr error

	providerDeleteSuccess := false
	for attempt := 1; attempt <= maxRetries; attempt++ {
		// 更新重试进度
		if attempt > 1 {
			s.updateTaskProgress(task.ID, 60+attempt*5, fmt.Sprintf("正在删除实例（第%d次尝试）...", attempt))
		}

		// 使用 Provider ID 确保使用正确的 Provider
		if err := providerApiService.DeleteInstanceByProviderID(ctx, provider.ID, instance.Name); err != nil {
			lastErr = err
			global.APP_LOG.Warn("Provider删除实例失败，准备重试",
				zap.Uint("taskId", task.ID),
				zap.String("instanceName", instance.Name),
				zap.String("provider", provider.Name),
				zap.Int("attempt", attempt),
				zap.Int("maxRetries", maxRetries),
				zap.Error(err))

			if attempt < maxRetries {
				// 等待一段时间后重试
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(retryDelay):
					// 继续重试
				}
				retryDelay *= 2 // 指数退避
			}
		} else {
			providerDeleteSuccess = true
			global.APP_LOG.Info("Provider删除实例成功",
				zap.Uint("taskId", task.ID),
				zap.String("instanceName", instance.Name),
				zap.String("provider", provider.Name),
				zap.Int("attempt", attempt))
			break
		}
	}

	if !providerDeleteSuccess {
		global.APP_LOG.Error("Provider删除实例最终失败，已重试最大次数",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Int("maxRetries", maxRetries),
			zap.Error(lastErr))
		// 即使Provider删除失败，我们也继续清理数据库记录和配额
		// 因为实例可能已经在Provider端不存在了
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 80, "正在清理数据库记录...")

	// 在事务中删除实例记录并释放资源配额
	dbService := database.GetDatabaseService()
	quotaService := resources.NewQuotaService()

	err := dbService.ExecuteTransaction(ctx, func(tx *gorm.DB) error {
		// 删除实例的端口映射
		portMappingService := resources.PortMappingService{}
		if err := portMappingService.DeleteInstancePortMappings(instance.ID); err != nil {
			// 端口映射删除失败不应该阻止实例删除，只记录警告
			global.APP_LOG.Warn("删除实例端口映射失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		}

		// 释放Provider资源
		resourceService := &resources.ResourceService{}
		if err := resourceService.ReleaseResourcesInTx(tx, instance.ProviderID, instance.InstanceType,
			instance.CPU, instance.Memory, instance.Disk); err != nil {
			global.APP_LOG.Error("释放Provider资源失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
			// 继续执行，不中断事务
		}

		// 清理实例vnStat数据
		vnstatService := vnstat.NewService()
		if err := vnstatService.CleanupVnStatData(instance.ID); err != nil {
			global.APP_LOG.Warn("清理实例vnStat数据失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
			// 继续执行，不中断事务
		}

		// 删除实例记录
		if err := tx.Delete(&instance).Error; err != nil {
			return fmt.Errorf("删除实例记录失败: %v", err)
		}

		// 释放用户配额
		resources := resources.ResourceUsage{
			CPU:       instance.CPU,
			Memory:    instance.Memory,
			Disk:      instance.Disk,
			Bandwidth: instance.Bandwidth,
		}

		if err := quotaService.UpdateUserQuotaAfterDeletionWithTx(tx, instance.UserID, resources); err != nil {
			return fmt.Errorf("释放用户配额失败: %v", err)
		}

		return nil
	})

	if err != nil {
		return err
	}

	// 标记任务完成
	operationType := "用户"
	if taskReq.AdminOperation {
		operationType = "管理员"
	}
	completionMessage := fmt.Sprintf("实例删除成功（%s操作）", operationType)
	if !providerDeleteSuccess {
		completionMessage = fmt.Sprintf("实例删除完成（%s操作），Provider删除可能失败但数据已清理", operationType)
	}
	stateManager := GetTaskStateManager()
	if err := stateManager.CompleteMainTask(task.ID, true, completionMessage, nil); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("实例删除成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID),
		zap.String("operationType", operationType),
		zap.Bool("adminOperation", taskReq.AdminOperation),
		zap.Bool("providerDeleteSuccess", providerDeleteSuccess))

	return nil
}

// executeResetInstanceTask 执行重置实例任务
func (s *TaskService) executeResetInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 5, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.InstanceOperationTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 10, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 验证实例所有权
	if instance.UserID != task.UserID {
		return fmt.Errorf("无权限操作此实例")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 15, "正在获取Provider配置...")

	// 更新进度
	s.updateTaskProgress(task.ID, 15, "正在获取Provider配置...")

	// 获取Provider配置
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("获取Provider配置失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取系统镜像信息...")

	// 获取原始系统镜像，使用Provider的架构信息进行匹配
	var systemImage systemModel.SystemImage
	if err := global.APP_DB.Where("name = ? AND provider_type = ? AND instance_type = ? AND architecture = ?",
		instance.Image, provider.Type, instance.InstanceType, provider.Architecture).First(&systemImage).Error; err != nil {
		global.APP_LOG.Error("获取系统镜像信息失败",
			zap.String("image", instance.Image),
			zap.String("providerType", provider.Type),
			zap.String("instanceType", instance.InstanceType),
			zap.String("architecture", provider.Architecture),
			zap.Error(err))
		return fmt.Errorf("获取系统镜像信息失败: %v", err)
	}

	providerApiService := &provider2.ProviderApiService{}

	// 更新进度
	s.updateTaskProgress(task.ID, 25, "正在保存端口映射配置...")

	// 更新进度
	s.updateTaskProgress(task.ID, 25, "正在保存端口映射配置...")

	// 保存旧的端口映射配置（在删除实例之前）
	global.APP_LOG.Info("保存原有端口映射配置",
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name))

	var oldPortMappings []providerModel.Port
	if err := global.APP_DB.Where("instance_id = ?", instance.ID).Find(&oldPortMappings).Error; err != nil {
		global.APP_LOG.Warn("获取旧端口映射失败",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
	} else {
		global.APP_LOG.Info("已保存端口映射配置",
			zap.Uint("instanceId", instance.ID),
			zap.Int("portCount", len(oldPortMappings)))
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在删除旧实例...")

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在删除旧实例...")

	// 第一步：删除现有实例，使用 Provider ID 确保使用正确的 Provider
	global.APP_LOG.Info("开始删除旧实例",
		zap.Uint("taskId", task.ID),
		zap.String("instanceName", instance.Name),
		zap.String("provider", provider.Name))

	deleteErr := providerApiService.DeleteInstanceByProviderID(ctx, provider.ID, instance.Name)
	if deleteErr != nil {
		global.APP_LOG.Error("Provider删除实例失败（重置过程中）",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Error(deleteErr))

		// 检查是否是容器不存在的错误
		errorStr := strings.ToLower(deleteErr.Error())
		isNotFoundError := strings.Contains(errorStr, "no such container") ||
			strings.Contains(errorStr, "not found") ||
			strings.Contains(errorStr, "already removed") ||
			strings.Contains(errorStr, "container not found")

		if !isNotFoundError {
			// 如果不是"不存在"的错误，说明删除确实失败了
			global.APP_DB.Model(&instance).Update("status", "error")
			return fmt.Errorf("删除旧实例失败: %v", deleteErr)
		}

		global.APP_LOG.Info("实例可能已不存在，继续重置流程",
			zap.String("instanceName", instance.Name))
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 40, "等待旧实例完全删除...")

	// 等待删除完成，并验证容器是否真的被删除
	// 对于Docker容器，需要更长的等待时间确保完全删除
	maxWaitTime := 100 * time.Second
	checkInterval := 10 * time.Second
	waitStart := time.Now()

	global.APP_LOG.Info("等待旧实例完全删除",
		zap.String("instanceName", instance.Name),
		zap.Duration("maxWaitTime", maxWaitTime))

	// 简单等待，确保删除操作完成
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("任务已取消")
		case <-time.After(checkInterval):
			// 如果超过最大等待时间，跳出循环
			if time.Since(waitStart) >= maxWaitTime {
				global.APP_LOG.Info("等待删除完成",
					zap.String("instanceName", instance.Name),
					zap.Duration("waitTime", time.Since(waitStart)))
				goto CreateNewInstance
			}
		}
	}

CreateNewInstance:

	// 更新进度
	s.updateTaskProgress(task.ID, 50, "正在准备重新创建实例...")

	// 第二步：重新创建实例，使用 CreateInstanceByProviderID 方法以指定准确的Provider
	createReq := provider2.CreateInstanceRequest{
		InstanceConfig: providerModel.ProviderInstanceConfig{
			Name:         instance.Name,
			Image:        instance.Image,
			InstanceType: instance.InstanceType,
			CPU:          fmt.Sprintf("%d", instance.CPU),
			Memory:       fmt.Sprintf("%dMB", instance.Memory),
			Disk:         fmt.Sprintf("%dMB", instance.Disk),
			Env:          make(map[string]string),
			Metadata:     make(map[string]string),
		},
		SystemImageID: systemImage.ID,
	}

	createReq.InstanceConfig.Env["RESET_OPERATION"] = "true"
	createReq.InstanceConfig.Metadata["original_instance_id"] = fmt.Sprintf("%d", instance.ID)

	// Docker特殊处理：需要在创建时传递端口映射配置
	if provider.Type == "docker" && len(oldPortMappings) > 0 {
		global.APP_LOG.Info("Docker类型实例，在创建时配置端口映射",
			zap.Int("portCount", len(oldPortMappings)))

		// 将端口映射信息添加到实例配置中，Docker容器需要在创建时指定端口映射
		var ports []string
		for _, oldPort := range oldPortMappings {
			// 格式: "0.0.0.0:公网端口:容器端口/协议"
			portMapping := fmt.Sprintf("0.0.0.0:%d:%d/%s", oldPort.HostPort, oldPort.GuestPort, oldPort.Protocol)
			ports = append(ports, portMapping)
		}
		createReq.InstanceConfig.Ports = ports

		global.APP_LOG.Info("Docker容器端口映射配置已添加",
			zap.Int("portCount", len(ports)),
			zap.Strings("ports", ports))
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 55, "正在重新创建实例...")

	if err := providerApiService.CreateInstanceByProviderID(ctx, provider.ID, createReq); err != nil {
		global.APP_LOG.Error("Provider重建实例失败（重置过程中）",
			zap.Uint("taskId", task.ID),
			zap.String("instanceName", instance.Name),
			zap.String("provider", provider.Name),
			zap.Error(err))

		// 更新实例状态为重置失败
		global.APP_DB.Model(&instance).Update("status", "error")
		return fmt.Errorf("重置实例失败（重建阶段）: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 65, "正在等待实例启动...")

	// 等待实例启动完成（特别是Docker和VM类型需要更长的启动时间）
	global.APP_LOG.Info("等待实例启动完成",
		zap.String("instanceName", instance.Name))

	// 简单等待，确保实例完全启动
	time.Sleep(15 * time.Second)

	// 更新进度
	s.updateTaskProgress(task.ID, 68, "正在生成并设置新密码...")

	// 第三步：生成新密码并设置到实例
	newPassword := utils.GenerateStrongPassword(12)

	global.APP_LOG.Info("开始设置实例新密码",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name))

	// 通过Provider设置实例密码，使用重试机制
	providerService := provider2.GetProviderService()
	maxPasswordRetries := 3
	var lastPasswordErr error
	passwordSetSuccess := false

	for attempt := 1; attempt <= maxPasswordRetries; attempt++ {
		if attempt > 1 {
			s.updateTaskProgress(task.ID, 68+attempt, fmt.Sprintf("正在设置密码（第%d次尝试）...", attempt))
			// 重试前等待一段时间
			time.Sleep(time.Duration(attempt*3) * time.Second)
		}

		err := providerService.SetInstancePassword(ctx, instance.ProviderID, instance.Name, newPassword)
		if err != nil {
			lastPasswordErr = err
			global.APP_LOG.Warn("设置实例密码失败，准备重试",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.String("instanceName", instance.Name),
				zap.Int("attempt", attempt),
				zap.Error(err))
			continue
		}

		passwordSetSuccess = true
		global.APP_LOG.Info("实例密码设置成功",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name),
			zap.Int("attempt", attempt))
		break
	}

	if !passwordSetSuccess {
		global.APP_LOG.Error("设置实例密码失败，已达最大重试次数",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name),
			zap.Int("maxRetries", maxPasswordRetries),
			zap.Error(lastPasswordErr))
		// 注意：这里不返回错误，因为可能是实例类型不支持密码设置
		// 但我们会记录警告，并继续完成重置流程
		global.APP_LOG.Warn("将继续完成重置流程，但密码可能未正确设置",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID))
		// 如果密码设置失败，使用默认密码
		newPassword = "root" // 或者保持原密码
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 72, "正在更新实例信息到数据库...")

	// 第四步：更新实例信息到数据库
	// 注意：重建后的IP地址应该从Provider获取，这里保持原IP
	instanceUpdates := map[string]interface{}{
		"status":    "running",
		"public_ip": instance.PublicIP, // 保持原IP，实际情况下应该从Provider重新获取
		"username":  "root",
		"password":  newPassword, // 使用设置成功的新密码
	}

	if err := global.APP_DB.Model(&instance).Updates(instanceUpdates).Error; err != nil {
		global.APP_LOG.Error("更新重置后的实例信息失败", zap.Error(err))
		return fmt.Errorf("更新实例信息失败: %v", err)
	}

	global.APP_LOG.Info("实例密码已更新到数据库",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name))

	// 更新进度
	s.updateTaskProgress(task.ID, 75, "正在恢复端口映射...")

	// 第四步：恢复原有的端口映射
	global.APP_LOG.Info("开始恢复原有端口映射",
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.String("providerType", provider.Type),
		zap.Int("portCount", len(oldPortMappings)))

	if len(oldPortMappings) > 0 {
		// 先删除数据库中的旧记录（ID会改变，但配置保持不变）
		if err := global.APP_DB.Where("instance_id = ?", instance.ID).Delete(&providerModel.Port{}).Error; err != nil {
			global.APP_LOG.Warn("清理旧端口映射记录失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		}

		// 使用保存的端口映射配置重新创建
		successCount := 0
		failCount := 0

		// Docker类型：端口映射在创建时已处理，只需恢复数据库记录
		if provider.Type == "docker" {
			global.APP_LOG.Info("Docker实例，恢复端口映射数据库记录",
				zap.Uint("instanceId", instance.ID))

			for _, oldPort := range oldPortMappings {
				newPort := providerModel.Port{
					InstanceID:    instance.ID,
					ProviderID:    provider.ID,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Protocol:      oldPort.Protocol,
					Description:   oldPort.Description,
					Status:        "active",
					IsSSH:         oldPort.IsSSH,
					IsAutomatic:   oldPort.IsAutomatic,
					PortType:      oldPort.PortType,
					MappingMethod: oldPort.MappingMethod,
					IPv6Enabled:   oldPort.IPv6Enabled,
				}

				if err := global.APP_DB.Create(&newPort).Error; err != nil {
					global.APP_LOG.Warn("恢复Docker端口映射记录失败",
						zap.Int("hostPort", oldPort.HostPort),
						zap.Error(err))
					failCount++
				} else {
					successCount++
				}
			}
		} else {
			// LXD/Incus/Proxmox类型：需要应用到远程服务器
			global.APP_LOG.Info("非Docker实例，恢复端口映射并应用到远程服务器",
				zap.Uint("instanceId", instance.ID),
				zap.String("providerType", provider.Type))

			// 初始化portmapping manager
			manager := portmapping.NewManager(&portmapping.ManagerConfig{
				DefaultMappingMethod: provider.IPv4PortMappingMethod,
			})

			// 确定portmapping类型
			portMappingType := provider.Type
			if portMappingType == "proxmox" {
				portMappingType = "iptables"
			}

			// 按协议分组端口映射
			tcpPorts := make([]providerModel.Port, 0)
			udpPorts := make([]providerModel.Port, 0)
			bothPorts := make([]providerModel.Port, 0)
			for _, oldPort := range oldPortMappings {
				if oldPort.Protocol == "tcp" {
					tcpPorts = append(tcpPorts, oldPort)
				} else if oldPort.Protocol == "udp" {
					udpPorts = append(udpPorts, oldPort)
				} else if oldPort.Protocol == "both" {
					bothPorts = append(bothPorts, oldPort)
				}
			}

			// 处理TCP端口
			if len(tcpPorts) > 0 {
				processedCount, failedCount := s.restorePortMappingsOptimized(ctx, tcpPorts, instance, provider, manager, portMappingType)
				successCount += processedCount
				failCount += failedCount
			}

			// 处理UDP端口
			if len(udpPorts) > 0 {
				processedCount, failedCount := s.restorePortMappingsOptimized(ctx, udpPorts, instance, provider, manager, portMappingType)
				successCount += processedCount
				failCount += failedCount
			}

			// 处理Both端口（保持协议为both，在实际映射时会分别创建TCP和UDP）
			if len(bothPorts) > 0 {
				processedCount, failedCount := s.restorePortMappingsOptimized(ctx, bothPorts, instance, provider, manager, portMappingType)
				successCount += processedCount
				failCount += failedCount
			}
		}

		global.APP_LOG.Info("端口映射恢复完成",
			zap.Uint("instanceId", instance.ID),
			zap.String("providerType", provider.Type),
			zap.Int("成功", successCount),
			zap.Int("失败", failCount))
	} else {
		global.APP_LOG.Warn("未找到旧的端口映射配置，将创建默认端口映射",
			zap.Uint("instanceId", instance.ID))

		// 如果没有旧的端口映射，则创建默认端口映射
		portMappingService := &resources.PortMappingService{}
		if err := portMappingService.CreateDefaultPortMappings(instance.ID, provider.ID); err != nil {
			global.APP_LOG.Warn("创建默认端口映射失败",
				zap.Uint("instanceId", instance.ID),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("默认端口映射创建成功",
				zap.Uint("instanceId", instance.ID))
		}
	}

	// 第4.5步：从数据库查询SSH端口映射并更新实例的ssh_port字段
	var sshPortMapping providerModel.Port
	if err := global.APP_DB.Where("instance_id = ? AND is_ssh = true AND status = 'active'", instance.ID).First(&sshPortMapping).Error; err == nil {
		// 找到SSH端口映射，更新实例的ssh_port字段为映射的公网端口（HostPort）
		if err := global.APP_DB.Model(&instance).Update("ssh_port", sshPortMapping.HostPort).Error; err != nil {
			global.APP_LOG.Warn("更新实例SSH端口失败",
				zap.Uint("instanceId", instance.ID),
				zap.Int("sshPort", sshPortMapping.HostPort),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("实例SSH端口已更新",
				zap.Uint("instanceId", instance.ID),
				zap.Int("sshPort", sshPortMapping.HostPort))
		}
	} else {
		global.APP_LOG.Warn("未找到SSH端口映射，使用默认值22",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		// 如果没有找到SSH端口映射，设置为默认值22
		global.APP_DB.Model(&instance).Update("ssh_port", 22)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 85, "正在重新初始化监控服务...")

	// 第五步：清理旧的vnstat接口记录并重新初始化
	global.APP_LOG.Info("开始重新初始化vnstat监控",
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name))

	// 清理旧的vnstat数据
	vnstatService := vnstat.NewService()
	if err := vnstatService.CleanupVnStatData(instance.ID); err != nil {
		global.APP_LOG.Warn("清理旧的vnstat数据失败",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
	}

	// 重新初始化vnstat监控
	if err := vnstatService.InitializeVnStatForInstance(instance.ID); err != nil {
		global.APP_LOG.Warn("重新初始化vnstat监控失败",
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		// 不影响重置流程，继续执行
	} else {
		global.APP_LOG.Info("vnstat监控重新初始化成功",
			zap.Uint("instanceId", instance.ID))
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 95, "重置完成，正在收尾...")

	global.APP_LOG.Info("用户实例重置成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID))

	return nil
}

// restorePortMappingsOptimized 优化的端口映射恢复逻辑
// 检测连续端口范围，避免重复创建端口代理设备
func (s *TaskService) restorePortMappingsOptimized(
	ctx context.Context,
	ports []providerModel.Port,
	instance providerModel.Instance,
	provider providerModel.Provider,
	manager *portmapping.Manager,
	portMappingType string,
) (successCount int, failCount int) {
	if len(ports) == 0 {
		return 0, 0
	}

	// 按端口号排序
	sort.Slice(ports, func(i, j int) bool {
		return ports[i].HostPort < ports[j].HostPort
	})

	// 检测连续端口范围
	consecutiveGroups := make([][]providerModel.Port, 0)
	currentGroup := []providerModel.Port{ports[0]}

	for i := 1; i < len(ports); i++ {
		prevPort := currentGroup[len(currentGroup)-1]
		currPort := ports[i]

		// 检查是否连续且是1:1映射
		if currPort.HostPort == prevPort.HostPort+1 &&
			currPort.GuestPort == prevPort.GuestPort+1 &&
			currPort.HostPort == currPort.GuestPort {
			// 连续端口，加入当前组
			currentGroup = append(currentGroup, currPort)
		} else {
			// 不连续，保存当前组并开始新组
			consecutiveGroups = append(consecutiveGroups, currentGroup)
			currentGroup = []providerModel.Port{currPort}
		}
	}
	// 保存最后一组
	consecutiveGroups = append(consecutiveGroups, currentGroup)

	global.APP_LOG.Info("端口映射分组完成",
		zap.Int("totalPorts", len(ports)),
		zap.Int("groups", len(consecutiveGroups)))

	// 处理每个分组
	for _, group := range consecutiveGroups {
		// 判断是否应该使用范围映射
		// LXD/Incus 支持范围映射，Proxmox 使用 iptables 需要逐个创建
		useRangeMapping := (provider.Type == "lxd" || provider.Type == "incus") && len(group) >= 3

		if useRangeMapping {
			// 3个或以上连续端口，使用范围映射（避免创建重复设备）
			// 注意：直接操作provider层，跳过portmapping manager以避免创建独立代理
			global.APP_LOG.Info("检测到连续端口范围，跳过portmapping创建（已由实例创建时处理）",
				zap.String("providerType", provider.Type),
				zap.String("protocol", group[0].Protocol),
				zap.Int("startPort", group[0].HostPort),
				zap.Int("endPort", group[len(group)-1].HostPort),
				zap.Int("count", len(group)))

			// 只恢复数据库记录，不创建实际代理（因为范围代理已存在）
			for _, oldPort := range group {
				newPort := providerModel.Port{
					InstanceID:    instance.ID,
					ProviderID:    provider.ID,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Protocol:      oldPort.Protocol,
					Description:   oldPort.Description,
					Status:        "active",
					IsSSH:         oldPort.IsSSH,
					IsAutomatic:   oldPort.IsAutomatic,
					PortType:      oldPort.PortType,
					MappingMethod: oldPort.MappingMethod,
					IPv6Enabled:   oldPort.IPv6Enabled,
				}
				if err := global.APP_DB.Create(&newPort).Error; err != nil {
					global.APP_LOG.Warn("恢复端口映射数据库记录失败",
						zap.Int("hostPort", oldPort.HostPort),
						zap.Error(err))
					failCount++
				} else {
					successCount++
				}
			}
		} else {
			// Proxmox 或少于3个端口，逐个处理
			for _, oldPort := range group {
				isSSH := oldPort.IsSSH
				portReq := &portmapping.PortMappingRequest{
					InstanceID:    fmt.Sprintf("%d", instance.ID),
					ProviderID:    provider.ID,
					Protocol:      oldPort.Protocol,
					HostPort:      oldPort.HostPort,
					GuestPort:     oldPort.GuestPort,
					Description:   oldPort.Description,
					MappingMethod: provider.IPv4PortMappingMethod,
					IsSSH:         &isSSH,
				}

				result, err := manager.CreatePortMapping(ctx, portMappingType, portReq)
				if err != nil {
					global.APP_LOG.Warn("应用端口映射到远程服务器失败",
						zap.Int("hostPort", oldPort.HostPort),
						zap.Error(err))

					// 即使失败也创建数据库记录（状态为failed）
					newPort := providerModel.Port{
						InstanceID:    instance.ID,
						ProviderID:    provider.ID,
						HostPort:      oldPort.HostPort,
						GuestPort:     oldPort.GuestPort,
						Protocol:      oldPort.Protocol,
						Description:   oldPort.Description,
						Status:        "failed",
						IsSSH:         oldPort.IsSSH,
						IsAutomatic:   oldPort.IsAutomatic,
						PortType:      oldPort.PortType,
						MappingMethod: oldPort.MappingMethod,
						IPv6Enabled:   oldPort.IPv6Enabled,
					}
					global.APP_DB.Create(&newPort)
					failCount++
				} else {
					successCount++
					global.APP_LOG.Debug("端口映射已应用到远程服务器",
						zap.Uint("portId", result.ID),
						zap.Int("hostPort", result.HostPort),
						zap.Int("guestPort", result.GuestPort))
				}
			}
		}
	}

	return successCount, failCount
}

// ForceStopTask 强制停止任务（管理员专用）
func (s *TaskService) ForceStopTask(taskID uint, reason string) error {
	if reason == "" {
		reason = "管理员强制停止"
	}
	return s.CancelTaskByAdmin(taskID, reason)
}

// GetTaskStats 获取任务统计信息
func (s *TaskService) GetTaskStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// 统计各状态任务数量
	var statusCounts []dashboardModel.TaskStatusCount

	err := global.APP_DB.Model(&adminModel.Task{}).
		Select("status, count(*) as count").
		Group("status").
		Find(&statusCounts).Error

	if err != nil {
		return nil, fmt.Errorf("统计任务状态失败: %w", err)
	}

	taskStats := make(map[string]int64)
	for _, sc := range statusCounts {
		taskStats[sc.Status] = sc.Count
	}

	stats["task_counts"] = taskStats
	stats["last_update"] = time.Now()

	return stats, nil
}

// GetTaskOverallStats 获取任务总体统计信息
func (s *TaskService) GetTaskOverallStats() (*adminModel.TaskStatsResponse, error) {
	var stats adminModel.TaskStatsResponse

	// 统计总任务数
	if err := global.APP_DB.Model(&adminModel.Task{}).Count(&stats.TotalTasks).Error; err != nil {
		return nil, fmt.Errorf("统计总任务数失败: %w", err)
	}

	// 统计各状态的任务数
	statusQueries := map[string]*int64{
		"pending":   &stats.PendingTasks,
		"running":   &stats.RunningTasks,
		"completed": &stats.CompletedTasks,
		"failed":    &stats.FailedTasks,
		"timeout":   &stats.TimeoutTasks,
	}

	for status, count := range statusQueries {
		if err := global.APP_DB.Model(&adminModel.Task{}).
			Where("status = ?", status).
			Count(count).Error; err != nil {
			return nil, fmt.Errorf("统计%s状态任务数失败: %w", status, err)
		}
	}

	// 同时统计processing状态的任务到运行中
	var processingTasks int64
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ?", "processing").
		Count(&processingTasks).Error; err != nil {
		return nil, fmt.Errorf("统计processing状态任务数失败: %w", err)
	}
	stats.RunningTasks += processingTasks

	// 统计cancelled和cancelling状态的任务到失败中
	var cancelledTasks int64
	if err := global.APP_DB.Model(&adminModel.Task{}).
		Where("status IN (?)", []string{"cancelled", "cancelling"}).
		Count(&cancelledTasks).Error; err != nil {
		return nil, fmt.Errorf("统计cancelled状态任务数失败: %w", err)
	}
	stats.FailedTasks += cancelledTasks

	return &stats, nil
}

// executeResetPasswordTask 执行重置实例密码任务
func (s *TaskService) executeResetPasswordTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 10, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.ResetPasswordTaskRequest

	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceId).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return fmt.Errorf("实例不存在")
		}
		return fmt.Errorf("获取实例信息失败: %v", err)
	}

	// 验证实例所有权
	if instance.UserID != task.UserID {
		return fmt.Errorf("无权限操作此实例")
	}

	// 检查实例状态
	if instance.Status != "running" {
		return fmt.Errorf("只有运行中的实例才能重置密码")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在生成新密码...")

	// 生成新密码
	newPassword := utils.GenerateStrongPassword(12)

	global.APP_LOG.Info("开始重置实例密码",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID))

	// 更新进度
	s.updateTaskProgress(task.ID, 50, "正在设置新密码...")

	// 通过Provider重置实例密码，重试机制
	providerService := provider.GetProviderService()
	maxRetries := 3
	var lastErr error
	passwordSetSuccess := false

	for attempt := 1; attempt <= maxRetries; attempt++ {
		// 更新重试进度
		if attempt > 1 {
			s.updateTaskProgress(task.ID, 50+attempt*10, fmt.Sprintf("正在设置新密码（第%d次尝试）...", attempt))
		}

		err := providerService.SetInstancePassword(ctx, instance.ProviderID, instance.Name, newPassword)
		if err != nil {
			lastErr = err
			global.APP_LOG.Warn("重置实例密码失败，准备重试",
				zap.Uint("taskId", task.ID),
				zap.Uint("instanceId", instance.ID),
				zap.String("instanceName", instance.Name),
				zap.Int("attempt", attempt),
				zap.Int("maxRetries", maxRetries),
				zap.Error(err))
			if attempt < maxRetries {
				// 使用可取消的等待
				select {
				case <-time.After(5 * time.Second):
					// 等待5秒后重试
				case <-ctx.Done():
					return fmt.Errorf("任务已取消")
				}
			}
		} else {
			passwordSetSuccess = true
			break
		}
	}

	if !passwordSetSuccess {
		global.APP_LOG.Error("重置实例密码最终失败",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID),
			zap.String("instanceName", instance.Name),
			zap.Error(lastErr))
		return fmt.Errorf("重置密码失败: %v", lastErr)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 90, "正在更新数据库记录...")

	// 更新数据库中的密码
	err := global.APP_DB.Model(&instance).Update("password", newPassword).Error
	if err != nil {
		global.APP_LOG.Error("更新实例密码到数据库失败",
			zap.Uint("taskId", task.ID),
			zap.Uint("instanceId", instance.ID),
			zap.Error(err))
		// 即使数据库更新失败，也不返回错误，因为实际密码已经重置成功
	}

	// 将新密码存储到任务结果中
	taskResult := map[string]interface{}{
		"instanceId":  instance.ID,
		"providerId":  instance.ProviderID,
		"newPassword": newPassword,
		"resetTime":   time.Now().Unix(),
	}
	taskResultJSON, _ := json.Marshal(taskResult)
	global.APP_DB.Model(task).Update("task_data", string(taskResultJSON))

	// 标记任务完成
	stateManager := GetTaskStateManager()
	if err := stateManager.CompleteMainTask(task.ID, true, "密码重置成功", taskResult); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("实例密码重置成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("instanceId", instance.ID),
		zap.String("instanceName", instance.Name),
		zap.Uint("userId", instance.UserID))

	return nil
}

// executeCreatePortMappingTask 执行创建端口映射任务
func (s *TaskService) executeCreatePortMappingTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 10, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.CreatePortMappingTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取端口映射信息...")

	// 获取端口映射记录
	var port providerModel.Port
	if err := global.APP_DB.First(&port, taskReq.PortID).Error; err != nil {
		return fmt.Errorf("端口映射记录不存在")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在获取实例信息...")

	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, taskReq.InstanceID).Error; err != nil {
		// 更新端口状态为失败
		global.APP_DB.Model(&port).Update("status", "failed")
		return fmt.Errorf("实例不存在")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 40, "正在获取Provider配置...")

	// 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, taskReq.ProviderID).Error; err != nil {
		// 更新端口状态为失败
		global.APP_DB.Model(&port).Update("status", "failed")
		return fmt.Errorf("Provider不存在")
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 50, "正在配置端口映射...")

	// 使用 portmapping manager 添加端口映射
	manager := portmapping.NewManager(&portmapping.ManagerConfig{
		DefaultMappingMethod: providerInfo.IPv4PortMappingMethod,
	})

	// 确定使用的 portmapping provider 类型
	portMappingType := providerInfo.Type
	if portMappingType == "proxmox" {
		portMappingType = "iptables"
	}

	portReq := &portmapping.PortMappingRequest{
		InstanceID:    fmt.Sprintf("%d", instance.ID),
		ProviderID:    providerInfo.ID,
		Protocol:      port.Protocol,
		HostPort:      port.HostPort,
		GuestPort:     port.GuestPort,
		Description:   port.Description,
		MappingMethod: providerInfo.IPv4PortMappingMethod,
	}

	// 执行端口映射添加
	s.updateTaskProgress(task.ID, 70, "正在远程服务器上配置端口映射...")

	result, err := manager.CreatePortMapping(ctx, portMappingType, portReq)
	if err != nil {
		global.APP_LOG.Error("添加端口映射失败",
			zap.Uint("taskId", task.ID),
			zap.Uint("portId", port.ID),
			zap.Int("hostPort", port.HostPort),
			zap.Int("guestPort", port.GuestPort),
			zap.Error(err))

		// 更新端口状态为失败
		global.APP_DB.Model(&port).Update("status", "failed")

		return fmt.Errorf("添加端口映射失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 90, "正在更新端口状态...")

	// Provider 会创建一条新的数据库记录，我们需要删除它并更新我们原有的记录
	if result.ID != 0 && result.ID != port.ID {
		// 删除 provider 创建的重复记录
		global.APP_DB.Delete(&providerModel.Port{}, result.ID)
		global.APP_LOG.Info("删除 provider 创建的重复端口记录",
			zap.Uint("duplicatePortId", result.ID),
			zap.Uint("originalPortId", port.ID))
	}

	// 对于 LXD/Incus，还需要在远程服务器上实际创建 proxy device
	if providerInfo.Type == "lxd" || providerInfo.Type == "incus" {
		s.updateTaskProgress(task.ID, 85, "正在应用端口映射到远程服务器...")

		// 获取 Provider 实例
		providerApiService := &provider2.ProviderApiService{}
		prov, _, err := providerApiService.GetProviderByID(providerInfo.ID)
		if err != nil {
			global.APP_LOG.Error("获取Provider实例失败",
				zap.Uint("providerId", providerInfo.ID),
				zap.Error(err))
			// 更新端口状态为失败
			global.APP_DB.Model(&port).Update("status", "failed")
			return fmt.Errorf("获取Provider实例失败: %v", err)
		}

		// 调用 provider 层的方法在远程服务器上创建实际映射
		switch providerInfo.Type {
		case "lxd":
			lxdProv, ok := prov.(*lxd.LXDProvider)
			if !ok {
				return fmt.Errorf("Provider类型断言失败")
			}
			// 调用内部方法创建端口映射
			err = lxdProv.SetupPortMappingWithIP(instance.Name, port.HostPort, port.GuestPort, port.Protocol, providerInfo.IPv4PortMappingMethod, instance.PrivateIP)

		case "incus":
			incusProv, ok := prov.(*incus.IncusProvider)
			if !ok {
				return fmt.Errorf("Provider类型断言失败")
			}
			// 调用内部方法创建端口映射
			err = incusProv.SetupPortMappingWithIP(instance.Name, port.HostPort, port.GuestPort, port.Protocol, providerInfo.IPv4PortMappingMethod, instance.PrivateIP)
		}

		if err != nil {
			global.APP_LOG.Error("在远程服务器上创建端口映射失败",
				zap.Uint("taskId", task.ID),
				zap.Uint("portId", port.ID),
				zap.Error(err))
			// 更新端口状态为失败
			global.APP_DB.Model(&port).Update("status", "failed")
			return fmt.Errorf("在远程服务器上创建端口映射失败: %v", err)
		}

		global.APP_LOG.Info("已在远程服务器上应用端口映射",
			zap.Uint("portId", port.ID),
			zap.String("providerType", providerInfo.Type))
	}

	// 更新端口状态为active
	if err := global.APP_DB.Model(&port).Updates(map[string]interface{}{
		"status":         "active",
		"mapping_method": result.MappingMethod,
	}).Error; err != nil {
		global.APP_LOG.Error("更新端口状态失败", zap.Error(err))
		return fmt.Errorf("更新端口状态失败: %v", err)
	}

	// 标记任务完成
	stateManager := GetTaskStateManager()
	taskResult := map[string]interface{}{
		"portId":    port.ID,
		"hostPort":  port.HostPort,
		"guestPort": port.GuestPort,
		"protocol":  port.Protocol,
	}
	if err := stateManager.CompleteMainTask(task.ID, true, "端口映射创建成功", taskResult); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("端口映射创建成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("portId", port.ID),
		zap.Int("hostPort", port.HostPort),
		zap.Int("guestPort", port.GuestPort))

	return nil
}

// executeDeletePortMappingTask 执行删除端口映射任务
func (s *TaskService) executeDeletePortMappingTask(ctx context.Context, task *adminModel.Task) error {
	// 初始化进度
	s.updateTaskProgress(task.ID, 10, "正在解析任务数据...")

	// 解析任务数据
	var taskReq adminModel.DeletePortMappingTaskRequest
	if err := json.Unmarshal([]byte(task.TaskData), &taskReq); err != nil {
		return fmt.Errorf("解析任务数据失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 20, "正在获取端口映射信息...")

	// 获取端口映射记录
	var port providerModel.Port
	if err := global.APP_DB.First(&port, taskReq.PortID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 端口已不存在，标记任务完成
			stateManager := GetTaskStateManager()
			if err := stateManager.CompleteMainTask(task.ID, true, "端口映射已不存在，删除任务完成", nil); err != nil {
				global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
			}
			return nil
		}
		return fmt.Errorf("获取端口映射记录失败: %v", err)
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 30, "正在获取实例信息...")

	// 获取实例信息（可能实例已被删除）
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, port.InstanceID).Error; err != nil {
		global.APP_LOG.Warn("实例不存在，继续删除端口映射记录",
			zap.Uint("instanceId", port.InstanceID),
			zap.Error(err))
		instance.Name = "" // 清空实例名称
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 40, "正在获取Provider配置...")

	// 获取Provider信息
	var providerInfo providerModel.Provider
	providerDeleteSuccess := true
	if err := global.APP_DB.First(&providerInfo, port.ProviderID).Error; err != nil {
		global.APP_LOG.Warn("Provider不存在，仅删除端口映射数据库记录",
			zap.Uint("providerId", port.ProviderID),
			zap.Error(err))
		providerDeleteSuccess = false
	} else {
		// 只有Provider存在时才尝试从远程删除
		s.updateTaskProgress(task.ID, 50, "正在从远程服务器删除端口映射...")

		// 使用 portmapping manager 删除端口映射
		manager := portmapping.NewManager(&portmapping.ManagerConfig{
			DefaultMappingMethod: providerInfo.IPv4PortMappingMethod,
		})

		portMappingType := providerInfo.Type
		if portMappingType == "proxmox" {
			portMappingType = "iptables"
		}

		deleteReq := &portmapping.DeletePortMappingRequest{
			ID:         port.ID,
			InstanceID: fmt.Sprintf("%d", instance.ID),
		}

		if err := manager.DeletePortMapping(ctx, portMappingType, deleteReq); err != nil {
			global.APP_LOG.Warn("从portmapping manager删除端口映射失败",
				zap.Uint("portId", port.ID),
				zap.Int("hostPort", port.HostPort),
				zap.Error(err))
			providerDeleteSuccess = false
			// 继续执行，不阻止数据库记录删除
		}

		// 对于 LXD/Incus，还需要在远程服务器上实际删除 proxy device
		if (providerInfo.Type == "lxd" || providerInfo.Type == "incus") && instance.Name != "" {
			s.updateTaskProgress(task.ID, 70, "正在从远程服务器删除端口映射...")

			// 获取 Provider 实例
			providerApiService := &provider2.ProviderApiService{}
			prov, _, err := providerApiService.GetProviderByID(providerInfo.ID)
			if err != nil {
				global.APP_LOG.Warn("获取Provider实例失败，跳过远程删除",
					zap.Uint("providerId", providerInfo.ID),
					zap.Error(err))
				providerDeleteSuccess = false
			} else {
				// 调用 provider 层的方法在远程服务器上删除实际映射
				var deleteErr error
				switch providerInfo.Type {
				case "lxd":
					if lxdProv, ok := prov.(*lxd.LXDProvider); ok {
						deleteErr = lxdProv.RemovePortMapping(instance.Name, port.HostPort, port.Protocol, providerInfo.IPv4PortMappingMethod)
					} else {
						deleteErr = fmt.Errorf("Provider类型断言失败")
					}

				case "incus":
					if incusProv, ok := prov.(*incus.IncusProvider); ok {
						deleteErr = incusProv.RemovePortMapping(instance.Name, port.HostPort, port.Protocol, providerInfo.IPv4PortMappingMethod)
					} else {
						deleteErr = fmt.Errorf("Provider类型断言失败")
					}
				}

				if deleteErr != nil {
					global.APP_LOG.Warn("从远程服务器删除端口映射失败",
						zap.Uint("portId", port.ID),
						zap.String("providerType", providerInfo.Type),
						zap.Error(deleteErr))
					providerDeleteSuccess = false
				} else {
					global.APP_LOG.Info("已从远程服务器删除端口映射",
						zap.Uint("portId", port.ID),
						zap.String("providerType", providerInfo.Type))
				}
			}
		}
	}

	// 更新进度
	s.updateTaskProgress(task.ID, 80, "正在删除数据库记录...")

	// 删除数据库记录
	if err := global.APP_DB.Delete(&port).Error; err != nil {
		return fmt.Errorf("删除端口映射记录失败: %v", err)
	}

	// 标记任务完成
	completionMessage := "端口映射删除成功"
	if !providerDeleteSuccess {
		completionMessage = "端口映射删除完成，远程删除可能失败但数据已清理"
	}
	stateManager := GetTaskStateManager()
	if err := stateManager.CompleteMainTask(task.ID, true, completionMessage, nil); err != nil {
		global.APP_LOG.Error("完成任务失败", zap.Uint("taskId", task.ID), zap.Error(err))
	}

	global.APP_LOG.Info("端口映射删除成功",
		zap.Uint("taskId", task.ID),
		zap.Uint("portId", port.ID),
		zap.Int("hostPort", port.HostPort),
		zap.Bool("providerDeleteSuccess", providerDeleteSuccess))

	return nil
}

// updateTaskProgress 更新任务进度
func (s *TaskService) updateTaskProgress(taskID uint, progress int, message string) {
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
		global.APP_LOG.Debug("任务进度更新成功",
			zap.Uint("taskId", taskID),
			zap.Int("progress", progress),
			zap.String("message", message))
	}
}

// markTaskCompleted 标记任务最终完成并释放锁
func (s *TaskService) markTaskCompleted(taskID uint, message string) {
	// 首先获取任务信息以便释放provider计数器
	var task adminModel.Task
	err := global.APP_DB.First(&task, taskID).Error
	if err != nil {
		global.APP_LOG.Error("获取任务信息失败",
			zap.Uint("taskId", taskID),
			zap.Error(err))
		return
	}

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
		// channel池架构自动处理并发控制
	} else if result.RowsAffected == 0 {
		// 没有更新任何行，说明任务状态不是running（可能已经是failed或其他状态）
		global.APP_LOG.Warn("任务状态不是running，跳过标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))
		// channel池架构自动处理并发控制
	} else {
		global.APP_LOG.Info("任务标记为完成",
			zap.Uint("taskId", taskID),
			zap.String("message", message))

		// channel池架构自动处理并发控制

		// 任务完成，记录日志
		if task.ProviderID != nil {
			global.APP_LOG.Debug("markTaskCompleted任务完成",
				zap.Uint("task_id", taskID),
				zap.Uint("provider_id", *task.ProviderID))
		}

		// 触发调度器处理pending任务
		if global.APP_SCHEDULER != nil {
			global.APP_SCHEDULER.TriggerTaskProcessing()
		}
	}
}

// CleanupTimeoutTasksWithLockRelease 清理超时任务并释放锁
func (s *TaskService) CleanupTimeoutTasksWithLockRelease(timeoutThreshold time.Time) (int64, int64) {
	var timeoutRunningTasks []adminModel.Task
	var timeoutCancellingTasks []adminModel.Task

	// 获取超时的running任务
	global.APP_DB.Where("status = ? AND updated_at < ?", "running", timeoutThreshold).Find(&timeoutRunningTasks)

	// 获取超时的cancelling任务
	global.APP_DB.Where("status = ? AND updated_at < ?", "cancelling", timeoutThreshold).Find(&timeoutCancellingTasks)

	// 更新超时的running任务
	result1 := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ? AND updated_at < ?", "running", timeoutThreshold).
		Updates(map[string]interface{}{
			"status":        "timeout",
			"cancel_reason": "Task timeout - exceeded 30 minutes",
			"updated_at":    time.Now(),
		})

	// 更新超时的cancelling任务
	result2 := global.APP_DB.Model(&adminModel.Task{}).
		Where("status = ? AND updated_at < ?", "cancelling", timeoutThreshold).
		Updates(map[string]interface{}{
			"status":        "cancelled",
			"cancel_reason": "Force cancelled - cancelling timeout",
			"updated_at":    time.Now(),
		})

	// channel池架构自动处理并发控制，无需手动释放锁

	var count1, count2 int64
	if result1.Error == nil {
		count1 = result1.RowsAffected
	}
	if result2.Error == nil {
		count2 = result2.RowsAffected
	}

	return count1, count2
}

// GetStateManager 获取任务状态管理器
func (s *TaskService) GetStateManager() interfaces.TaskStateManagerInterface {
	return GetTaskStateManager()
}

// ========== 新的基于Channel的工作池实现 ==========

// getOrCreateProviderPool 获取或创建Provider工作池
func (s *TaskService) getOrCreateProviderPool(providerID uint, concurrency int) *ProviderWorkerPool {
	s.poolMutex.Lock()
	defer s.poolMutex.Unlock()

	// 如果池已存在，检查并发数是否需要调整
	if pool, exists := s.providerPools[providerID]; exists {
		if pool.WorkerCount != concurrency {
			// 需要调整并发数，关闭旧池并创建新池
			pool.Cancel()
			delete(s.providerPools, providerID)
		} else {
			return pool
		}
	}

	// 创建新的工作池
	ctx, cancel := context.WithCancel(context.Background())
	pool := &ProviderWorkerPool{
		ProviderID:  providerID,
		TaskQueue:   make(chan TaskRequest, concurrency*2), // 队列大小为并发数的2倍，提供缓冲
		WorkerCount: concurrency,
		Ctx:         ctx,
		Cancel:      cancel,
		TaskService: s,
	}

	// 启动工作者
	for i := 0; i < concurrency; i++ {
		go pool.worker(i)
	}

	s.providerPools[providerID] = pool
	global.APP_LOG.Info("创建Provider工作池",
		zap.Uint("providerId", providerID),
		zap.Int("concurrency", concurrency))

	return pool
}

// worker 工作者goroutine
func (pool *ProviderWorkerPool) worker(workerID int) {
	global.APP_LOG.Info("启动Provider工作者",
		zap.Uint("providerId", pool.ProviderID),
		zap.Int("workerId", workerID))

	defer global.APP_LOG.Info("Provider工作者退出",
		zap.Uint("providerId", pool.ProviderID),
		zap.Int("workerId", workerID))

	for {
		select {
		case <-pool.Ctx.Done():
			return
		case taskReq := <-pool.TaskQueue:
			pool.executeTask(taskReq)
		}
	}
}

// executeTask 执行单个任务
func (pool *ProviderWorkerPool) executeTask(taskReq TaskRequest) {
	task := taskReq.Task
	result := TaskResult{
		Success: false,
		Error:   nil,
		Data:    make(map[string]interface{}),
	}

	// 创建任务上下文
	taskCtx, taskCancel := context.WithTimeout(pool.Ctx, time.Duration(task.TimeoutDuration)*time.Second)
	defer taskCancel()

	// 注册任务上下文
	pool.TaskService.contextMutex.Lock()
	pool.TaskService.runningContexts[task.ID] = &TaskContext{
		TaskID:     task.ID,
		Context:    taskCtx,
		CancelFunc: taskCancel,
		StartTime:  time.Now(),
	}
	pool.TaskService.contextMutex.Unlock()

	// 任务完成时清理上下文
	defer func() {
		pool.TaskService.contextMutex.Lock()
		delete(pool.TaskService.runningContexts, task.ID)
		pool.TaskService.contextMutex.Unlock()
	}()

	// 更新任务状态为运行中 - 带幂等性检查
	err := pool.TaskService.dbService.ExecuteTransaction(taskCtx, func(tx *gorm.DB) error {
		// 先检查任务当前状态，避免重复处理
		var currentTask adminModel.Task
		if err := tx.First(&currentTask, task.ID).Error; err != nil {
			return fmt.Errorf("查询任务状态失败: %v", err)
		}

		// 如果任务已经不是pending状态，说明被其他worker处理了
		if currentTask.Status != "pending" {
			return fmt.Errorf("任务状态已变更，当前状态: %s", currentTask.Status)
		}

		// 只有在pending状态时才更新为running
		return tx.Model(&adminModel.Task{}).Where("id = ? AND status = ?", task.ID, "pending").
			Updates(map[string]interface{}{
				"status":     "running",
				"started_at": time.Now(),
			}).Error
	})

	if err != nil {
		result.Error = fmt.Errorf("更新任务状态失败: %v", err)
		global.APP_LOG.Warn("任务状态更新失败，可能被其他worker处理",
			zap.Uint("taskId", task.ID),
			zap.Error(err))
		// 如果状态更新失败，不发送结果，让调度器自然忽略
		return
	}

	// 执行具体任务逻辑
	taskError := pool.TaskService.executeTaskLogic(taskCtx, &task)
	if taskError != nil {
		result.Error = taskError
	} else {
		result.Success = true
	}

	// 更新任务完成状态
	errorMsg := ""
	if result.Error != nil {
		errorMsg = result.Error.Error()
	}
	pool.TaskService.CompleteTask(task.ID, result.Success, errorMsg, result.Data)

	// 发送结果
	select {
	case taskReq.ResponseCh <- result:
	case <-taskCtx.Done():
	}
}

// StartTaskWithPool 使用工作池启动任务（新的简化版本）
func (s *TaskService) StartTaskWithPool(taskID uint) error {
	// 查询任务信息
	var task adminModel.Task
	err := s.dbService.ExecuteQuery(context.Background(), func() error {
		return global.APP_DB.First(&task, taskID).Error
	})

	if err != nil {
		return fmt.Errorf("查询任务失败: %v", err)
	}

	if task.ProviderID == nil {
		return fmt.Errorf("任务没有关联Provider")
	}

	// 获取Provider配置
	var provider providerModel.Provider
	err = s.dbService.ExecuteQuery(context.Background(), func() error {
		return global.APP_DB.First(&provider, *task.ProviderID).Error
	})

	if err != nil {
		return fmt.Errorf("查询Provider失败: %v", err)
	}

	// 确定并发数
	concurrency := 1 // 默认串行
	if provider.AllowConcurrentTasks && provider.MaxConcurrentTasks > 0 {
		concurrency = provider.MaxConcurrentTasks
	}

	// 获取或创建工作池
	pool := s.getOrCreateProviderPool(*task.ProviderID, concurrency)

	// 创建任务请求
	taskReq := TaskRequest{
		Task:       task,
		ResponseCh: make(chan TaskResult, 1),
	}

	// 发送任务到工作池（阻塞直到有空闲worker或队列有空间）
	select {
	case pool.TaskQueue <- taskReq:
		global.APP_LOG.Info("任务已发送到工作池",
			zap.Uint("taskId", taskID),
			zap.Uint("providerId", *task.ProviderID),
			zap.Int("queueLength", len(pool.TaskQueue)))
	case <-time.After(30 * time.Second):
		return fmt.Errorf("任务队列已满，发送超时")
	}

	return nil
}
