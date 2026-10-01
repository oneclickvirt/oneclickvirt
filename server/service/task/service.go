package task

import (
	"context"
	"fmt"
	"oneclickvirt/provider/portmapping"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	userprovider "oneclickvirt/service/user/provider"
	"sort"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
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
	submitMu       sync.Mutex // serializes enqueue and shutdown
	closed         bool
	retireWhenIdle bool  // Provider was removed; do not reopen this pool
	submitting     int   // queue reservations while a database claim is in flight
	ownedContexts  int64 // includes asynchronous post-processing
	outstanding    int64 // queued + executing (including the dequeue boundary)
	ProviderID     uint
	TaskQueue      chan TaskRequest   // 任务队列
	WorkerCount    int                // 工作者数量（并发数）
	activeCount    int64              // 当前正在执行的任务数
	liveWorkers    int64              // worker goroutines that have not exited
	Ctx            context.Context    // 上下文
	Cancel         context.CancelFunc // 取消函数
	TaskService    *TaskService       // 任务服务引用
}

// TaskService 任务管理服务
type TaskService struct {
	queuedTasks         sync.Map // locally owned processing tasks; used for failed-write recovery
	cancellationCleanup sync.Map // task IDs with an active cancellation cleanup attempt
	dbService           *database.DatabaseService
	contextManager      *TaskContextManager  // 任务上下文管理器
	poolManager         *ProviderPoolManager // Provider工作池管理器
	repairSubmitMu      sync.Mutex           // 端口映射修复提交互斥，避免同一Provider重复入队
	shutdown            chan struct{}        // 系统关闭信号
	shutdownOnce        sync.Once            // 保证并发Shutdown只关闭一次
	wg                  sync.WaitGroup       // 用于等待所有goroutine完成
	ctx                 context.Context      // 服务级别的context
	cancel              context.CancelFunc   // 服务级别的cancel函数
}

const (
	maxRunningContexts     = 1000             // 最大运行中的任务context数量
	maxTaskQueueSize       = 1000             // 每个Provider工作池的最大队列容量
	contextCleanupInterval = 30 * time.Second // 定期清理
	maxContextAge          = 3 * time.Hour    // 超时强制清理（需大于最长任务超时时间2小时）
	poolCleanupInterval    = 5 * time.Minute  // Provider工作池清理间隔
	maxPoolIdleTime        = 30 * time.Minute // 工作池最大空闲时间
)

var (
	taskService     *TaskService
	taskServiceOnce sync.Once
)

// GetTaskService 获取任务服务单例
func GetTaskService() *TaskService {
	taskServiceOnce.Do(func() {
		// 使用应用级shutdown context
		ctx, cancel := context.WithCancel(global.APP_SHUTDOWN_CONTEXT)
		taskService = &TaskService{
			dbService:      database.GetDatabaseService(),
			contextManager: NewTaskContextManager(maxRunningContexts, maxContextAge),
			poolManager:    NewProviderPoolManager(),
			shutdown:       make(chan struct{}),
			ctx:            ctx,
			cancel:         cancel,
		}
		// 设置全局任务锁释放器
		global.APP_TASK_LOCK_RELEASER = taskService

		// 初始化统一任务状态管理器
		InitTaskStateManager(taskService)

		// 只有在数据库已初始化时才清理running状态的任务
		if isSystemInitialized() {
			taskService.cleanupRunningTasksOnStartup()
		} else {
			global.APP_LOG.Debug("系统未初始化，跳过任务清理")
		}

		// 启动context自动清理goroutine
		go taskService.cleanupStaleContexts()

		// 启动provider工作池自动清理goroutine
		go taskService.cleanupIdleProviderPools()
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

// cleanupRunningTasksOnStartup 服务启动时清理上一个进程遗留的活跃任务。
// 兼容旧命名：历史上只清理 running，现在会同时清理 processing/cancelling。
func (s *TaskService) cleanupRunningTasksOnStartup() {
	s.cleanupInterruptedTasks("服务重启，任务被中断")
}

// cleanupStaleContexts 定期清理陈旧的任务context，防止内存泄漏
func (s *TaskService) cleanupStaleContexts() {
	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	ticker := time.NewTicker(contextCleanupInterval)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil {
			global.APP_LOG.Error("任务context清理goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Debug("任务context清理goroutine已停止")
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// 清理陈旧的context
			cleaned := s.contextManager.CleanupStale()

			// 如果超过容量80%，强制清理
			s.contextManager.ForceLimitSize()

			if cleaned > 0 || s.contextManager.Count() > int64(maxRunningContexts/2) {
				global.APP_LOG.Debug("Context清理完成",
					zap.Int("cleaned", cleaned),
					zap.Int64("total", s.contextManager.Count()))
			}
		}
	}
}

// cleanupIdleProviderPools 定期清理空闲的Provider工作池
func (s *TaskService) cleanupIdleProviderPools() {
	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	ticker := time.NewTicker(poolCleanupInterval)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil {
			global.APP_LOG.Error("Provider工作池清理goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Debug("Provider工作池清理goroutine已停止")
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// Read active task ownership once for the whole cleanup pass. The
			// previous implementation queried the task table again for every
			// Provider pool, which became a timer-driven N+1 query pattern.
			var busyProviders map[uint]struct{}
			if global.APP_DB != nil {
				parentCtx := s.ctx
				if parentCtx == nil {
					parentCtx = context.Background()
				}
				queryCtx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
				var busyProviderIDs []uint
				if err := global.APP_DB.WithContext(queryCtx).Model(&adminModel.Task{}).
					Where("status IN ? AND provider_id IS NOT NULL", []string{mainTaskStatusProcessing, mainTaskStatusRunning, mainTaskStatusCancelling}).
					Distinct("provider_id").Pluck("provider_id", &busyProviderIDs).Error; err != nil {
					cancel()
					global.APP_LOG.Warn("批量读取Provider活跃任务失败，跳过本轮空闲池清理", zap.Error(err))
				} else {
					cancel()
					busyProviders = make(map[uint]struct{}, len(busyProviderIDs))
					for _, providerID := range busyProviderIDs {
						busyProviders[providerID] = struct{}{}
					}
				}
			}
			// 清理空闲的工作池。busyProviders 为 nil 表示批量查询失败；
			// 这时保守地保留所有池，避免误停正在执行的任务。
			cleaned := 0
			if busyProviders != nil {
				cleaned = s.poolManager.CleanupIdle(maxPoolIdleTime, busyProviders)
			}

			// 从数据库查询有效的provider ID并清理已删除的
			if global.APP_DB != nil {
				var validProviderIDs []uint
				parentCtx := s.ctx
				if parentCtx == nil {
					parentCtx = context.Background()
				}
				queryCtx, cancel := context.WithTimeout(parentCtx, 10*time.Second)
				if err := global.APP_DB.WithContext(queryCtx).Model(&providerModel.Provider{}).
					Pluck("id", &validProviderIDs).Error; err == nil {
					cancel()
					s.poolManager.CleanupDeleted(validProviderIDs)
				} else {
					cancel()
				}
			}

			if cleaned > 0 {
				global.APP_LOG.Debug("Provider工作池清理完成",
					zap.Int("cleaned", cleaned),
					zap.Int64("remaining", s.poolManager.Count()))
			}
		}
	}
}

// Shutdown 优雅关闭任务服务，等待所有goroutine完成
func (s *TaskService) Shutdown() {
	s.shutdownOnce.Do(func() {
		global.APP_LOG.Info("开始关闭任务服务，等待所有后台任务完成...")
		if s.shutdown != nil {
			close(s.shutdown)
		}
		if s.cancel != nil {
			s.cancel()
		}
		if s.contextManager != nil {
			s.contextManager.CancelAll()
		}

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		workStopped := true
		if s.poolManager != nil {
			if err := s.poolManager.CancelAllAndWait(shutdownCtx); err != nil {
				workStopped = false
				global.APP_LOG.Warn("等待任务工作池退出超时", zap.Error(err))
			}
		}
		if workStopped && !waitTaskServiceWork(shutdownCtx, &s.wg) {
			workStopped = false
			global.APP_LOG.Warn("等待任务后处理退出超时")
		}

		if workStopped {
			global.APP_LOG.Info("任务工作池和后处理已退出")
		} else {
			// Startup recovery owns any unfinished rows after the old process has
			// exited. Do not terminalize them while a Provider call may still run.
			global.APP_LOG.Warn("仍有任务执行未确认退出，保留状态供下次启动恢复")
		}
		global.APP_LOG.Info("TaskService关闭完成")
	})
}

func waitTaskServiceWork(ctx context.Context, wg *sync.WaitGroup) bool {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-ctx.Done():
		return false
	}
}

// DeleteProviderPool 删除Provider工作池
func (s *TaskService) DeleteProviderPool(providerID uint) {
	s.poolManager.Delete(providerID)
}

// StartTask 启动任务 - 委托给新的实现
func (s *TaskService) StartTask(taskID uint) error {
	return s.StartTaskWithPool(taskID)
}

// executeCreateInstanceTask 执行创建实例任务
func (s *TaskService) executeCreateInstanceTask(ctx context.Context, task *adminModel.Task) error {
	// 使用用户provider服务处理创建实例任务，避免循环依赖
	userProviderService := userprovider.NewService()
	return userProviderService.ProcessCreateInstanceTask(ctx, task)
}

// executeCreateRedemptionInstanceTask 执行兑换码实例创建任务
func (s *TaskService) executeCreateRedemptionInstanceTask(ctx context.Context, task *adminModel.Task) error {
	userProviderService := userprovider.NewService()
	return userProviderService.ProcessCreateRedemptionInstanceTask(ctx, task)
}

// executeResetInstanceTask 执行重置实例任务
func (s *TaskService) executeResetInstanceTask(ctx context.Context, task *adminModel.Task) error {
	return s.executeResetTask(ctx, task)
}

// restorePortMappingsOptimized 端口映射恢复逻辑
// 检测连续端口范围，避免重复创建端口代理设备
func (s *TaskService) restorePortMappingsOptimized(
	ctx context.Context,
	ports []providerModel.Port,
	instanceID uint,
	instanceName string,
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

	global.APP_LOG.Debug("端口映射分组完成",
		zap.Int("totalPorts", len(ports)),
		zap.Int("groups", len(consecutiveGroups)))

	// 处理每个分组
	for _, group := range consecutiveGroups {
		// 对于重置任务，所有端口映射都需要重新创建到远程服务器
		// 因为旧实例已被删除，新实例上没有任何端口映射配置
		for _, oldPort := range group {
			isSSH := oldPort.IsSSH
			portReq := &portmapping.PortMappingRequest{
				InstanceID:    fmt.Sprintf("%d", instanceID),
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
					InstanceID:    instanceID,
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

	return successCount, failCount
}

// GetStateManager 获取任务状态管理器
func (s *TaskService) GetStateManager() interfaces.TaskStateManagerInterface {
	return GetTaskStateManager()
}

// GetStats 获取任务系统统计信息（用于性能监控）
func (s *TaskService) GetStats() (runningContexts int, providerPools int, totalQueueSize int) {
	runningContexts = int(s.contextManager.Count())
	providerPools = int(s.poolManager.Count())
	totalQueueSize = 0
	return
}
