package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	dashboardModel "oneclickvirt/model/dashboard"
	"oneclickvirt/model/provider"
	"oneclickvirt/service/traffic"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SchedulerService 全局任务调度器
type SchedulerService struct {
	taskService TaskServiceInterface
	ctx         context.Context
	cancel      context.CancelFunc
	wg          sync.WaitGroup
	running     bool
	mu          sync.RWMutex
	triggerChan chan struct{} // 用于立即触发任务处理
}

// TaskServiceInterface 任务服务接口
type TaskServiceInterface interface {
	StartTask(taskID uint) error
	CancelTaskByAdmin(taskID uint, reason string) error
	CleanupTimeoutTasksWithLockRelease(timeoutThreshold time.Time) (int64, int64)
}

// NewSchedulerService 创建新的调度器服务
func NewSchedulerService(taskService TaskServiceInterface) *SchedulerService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SchedulerService{
		taskService: taskService,
		ctx:         ctx,
		cancel:      cancel,
		running:     false,
		triggerChan: make(chan struct{}, 1), // 缓冲通道，避免阻塞
	}
}

// Start 启动调度器
func (s *SchedulerService) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("scheduler is already running")
	}

	s.running = true
	s.wg.Add(1)
	go s.runTaskScheduler()

	global.APP_LOG.Info("Task scheduler started")
	return nil
}

// Stop 停止调度器
func (s *SchedulerService) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return fmt.Errorf("scheduler is not running")
	}

	s.cancel()
	s.wg.Wait()
	s.running = false

	global.APP_LOG.Info("Task scheduler stopped")
	return nil
}

// IsRunning 检查调度器是否运行中
func (s *SchedulerService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// TriggerTaskProcessing 立即触发任务处理（非阻塞）
func (s *SchedulerService) TriggerTaskProcessing() {
	select {
	case s.triggerChan <- struct{}{}:
		// 成功发送触发信号
	default:
		// 通道已满，说明已有待处理的触发信号，忽略
	}
}

// StartScheduler 启动调度器（实现global.Scheduler接口）
func (s *SchedulerService) StartScheduler() {
	s.Start()
}

// StopScheduler 停止调度器（实现global.Scheduler接口）
func (s *SchedulerService) StopScheduler() {
	s.Stop()
}

// runTaskScheduler 主调度循环
func (s *SchedulerService) runTaskScheduler() {
	defer s.wg.Done()

	// 创建定时器
	taskTicker := time.NewTicker(5 * time.Second)         // 任务处理保持5秒
	cleanupTicker := time.NewTicker(1 * time.Minute)      // 超时清理保持1分钟
	maintenanceTicker := time.NewTicker(10 * time.Minute) // 系统维护保持10分钟
	trafficAggTicker := time.NewTicker(5 * time.Minute)   // 流量聚合保持5分钟

	defer func() {
		taskTicker.Stop()
		cleanupTicker.Stop()
		maintenanceTicker.Stop()
		trafficAggTicker.Stop()
	}()

	global.APP_LOG.Info("Task scheduler main loop started with traffic aggregation")

	for {
		select {
		case <-s.ctx.Done():
			global.APP_LOG.Info("Task scheduler context cancelled, exiting")
			return

		case <-taskTicker.C:
			s.processPendingTasks()

		case <-s.triggerChan:
			// 立即处理pending任务
			global.APP_LOG.Debug("Scheduler triggered immediately")
			s.processPendingTasks()

		case <-cleanupTicker.C:
			s.cleanupTimeoutTasks()

		case <-maintenanceTicker.C:
			s.performMaintenance()

		case <-trafficAggTicker.C:
			// 定期聚合流量数据，更新缓存
			s.aggregateTrafficData()
		}
	}
}

// processPendingTasks 处理待处理任务
func (s *SchedulerService) processPendingTasks() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过任务处理")
		return
	}

	// 获取所有待处理任务，按创建时间排序
	var pendingTasks []adminModel.Task
	err := global.APP_DB.Where("status = ?", "pending").
		Order("created_at ASC").
		Find(&pendingTasks).Error

	if err != nil {
		global.APP_LOG.Error("Failed to fetch pending tasks", zap.Error(err))
		return
	}

	if len(pendingTasks) == 0 {
		return
	}

	// 只在有任务需要处理时记录一次日志
	global.APP_LOG.Debug("处理待处理任务", zap.Int("count", len(pendingTasks)))

	// 按顺序处理每个任务
	for _, task := range pendingTasks {
		select {
		case <-s.ctx.Done():
			return
		default:
			s.tryStartTask(task)
		}
	}
}

// tryStartTask 尝试启动任务
func (s *SchedulerService) tryStartTask(task adminModel.Task) {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过任务启动")
		return
	}

	// 检查ProviderID是否为空
	if task.ProviderID == nil {
		global.APP_LOG.Error("Task has no provider ID", zap.Uint("task_id", task.ID))
		s.taskService.CancelTaskByAdmin(task.ID, "No provider assigned")
		return
	}

	// 检查Provider是否可用（基础检查）
	var provider provider.Provider
	err := global.APP_DB.Where("id = ?", *task.ProviderID).
		First(&provider).Error

	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// Provider不存在，取消任务
			s.taskService.CancelTaskByAdmin(task.ID, "Provider not found")
		} else {
			global.APP_LOG.Error("Failed to fetch provider",
				zap.Uint("provider_id", *task.ProviderID),
				zap.Error(err))
		}
		return
	}

	// 检查Provider的实际状态，而不仅仅是allow_claim标志
	// allow_claim可能因临时健康检查失败而被误设为false
	// 但如果Provider实际上是active状态且未冻结，应该允许任务继续执行
	// 对于删除任务，即使Provider被冻结，也允许执行（特别是管理员发起的删除任务）
	if provider.IsFrozen && task.TaskType != "delete" {
		global.APP_LOG.Warn("Provider is frozen, cancelling task",
			zap.Uint("provider_id", *task.ProviderID),
			zap.String("provider_name", provider.Name),
			zap.String("task_type", task.TaskType),
			zap.Uint("task_id", task.ID))
		s.taskService.CancelTaskByAdmin(task.ID, "Provider is frozen")
		return
	}

	// 如果是删除任务且Provider被冻结，记录日志但允许继续执行
	if provider.IsFrozen && task.TaskType == "delete" {
		global.APP_LOG.Info("Provider is frozen but allowing delete task to proceed",
			zap.Uint("provider_id", *task.ProviderID),
			zap.String("provider_name", provider.Name),
			zap.String("task_type", task.TaskType),
			zap.Uint("task_id", task.ID))
	}

	// 检查Provider是否过期
	if provider.ExpiresAt != nil && provider.ExpiresAt.Before(time.Now()) {
		global.APP_LOG.Warn("Provider has expired, cancelling task",
			zap.Uint("provider_id", *task.ProviderID),
			zap.String("provider_name", provider.Name),
			zap.Uint("task_id", task.ID))
		s.taskService.CancelTaskByAdmin(task.ID, "Provider has expired")
		return
	}

	// 对于删除任务，允许inactive状态的Provider，因为GetProviderByID会自动尝试重新连接
	// 其他任务类型仍需检查Provider状态
	if provider.Status == "inactive" && task.TaskType != "delete" {
		global.APP_LOG.Warn("Provider is inactive, cancelling task",
			zap.Uint("provider_id", *task.ProviderID),
			zap.String("provider_name", provider.Name),
			zap.String("ssh_status", provider.SSHStatus),
			zap.String("api_status", provider.APIStatus),
			zap.String("task_type", task.TaskType),
			zap.Uint("task_id", task.ID))
		s.taskService.CancelTaskByAdmin(task.ID, "Provider is inactive")
		return
	}

	// 对于删除任务，即使Provider状态为inactive，也允许继续执行
	// GetProviderByID会尝试重新连接，确保删除操作能够完成
	if provider.Status == "inactive" && task.TaskType == "delete" {
		global.APP_LOG.Info("Provider is inactive but allowing delete task to proceed, will attempt reconnection",
			zap.Uint("provider_id", *task.ProviderID),
			zap.String("provider_name", provider.Name),
			zap.String("task_type", task.TaskType),
			zap.Uint("task_id", task.ID))
	}

	// 记录当前allow_claim状态，但不阻止任务执行
	if !provider.AllowClaim {
		global.APP_LOG.Info("Provider allow_claim is false, but provider is active, allowing task to proceed",
			zap.Uint("provider_id", *task.ProviderID),
			zap.String("provider_name", provider.Name),
			zap.String("status", provider.Status),
			zap.Uint("task_id", task.ID))
	}

	// 尝试启动任务 - 让TaskService处理所有并发控制逻辑
	err = s.taskService.StartTask(task.ID)
	if err != nil {
		// 如果启动失败，记录日志但不做其他处理
		// TaskService会处理所有的错误情况
		global.APP_LOG.Debug("Task start attempt failed (this is normal for concurrency control)",
			zap.Uint("task_id", task.ID),
			zap.Uint("provider_id", *task.ProviderID),
			zap.String("reason", err.Error()))
	} else {
		global.APP_LOG.Info("Task started successfully",
			zap.Uint("task_id", task.ID),
			zap.Uint("provider_id", *task.ProviderID))
	}
}

// GetSchedulerStats 获取调度器统计信息
func (s *SchedulerService) GetSchedulerStats() map[string]interface{} {
	var stats map[string]interface{} = make(map[string]interface{})

	// 统计各状态任务数量
	var statusCounts []dashboardModel.TaskStatusCount

	global.APP_DB.Model(&adminModel.Task{}).
		Select("status, count(*) as count").
		Group("status").
		Find(&statusCounts)

	taskStats := make(map[string]int64)
	for _, sc := range statusCounts {
		taskStats[sc.Status] = sc.Count
	}

	stats["task_counts"] = taskStats
	stats["scheduler_running"] = s.IsRunning()
	stats["last_update"] = time.Now()

	return stats
}

// aggregateTrafficData 聚合流量数据到缓存表
// 定期将 pmacct_traffic_records 原始数据聚合到 instance_traffic_histories 缓存表
// 用于加速查询，避免每次都执行复杂的分段计算
func (s *SchedulerService) aggregateTrafficData() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		return
	}

	// 创建流量聚合服务
	aggService := traffic.NewAggregationService()

	// 聚合当月流量数据
	err := aggService.AggregateCurrentMonth()
	if err != nil {
		global.APP_LOG.Error("流量聚合失败", zap.Error(err))
		return
	}

	global.APP_LOG.Debug("流量聚合任务完成")
}
