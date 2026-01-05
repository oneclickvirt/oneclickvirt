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
	ProviderID  uint
	TaskQueue   chan TaskRequest   // 任务队列
	WorkerCount int                // 工作者数量（并发数）
	Ctx         context.Context    // 上下文
	Cancel      context.CancelFunc // 取消函数
	TaskService *TaskService       // 任务服务引用
}

// TaskService 任务管理服务
type TaskService struct {
	dbService      *database.DatabaseService
	contextManager *TaskContextManager  // 任务上下文管理器
	poolManager    *ProviderPoolManager // Provider工作池管理器
	shutdown       chan struct{}        // 系统关闭信号
	wg             sync.WaitGroup       // 用于等待所有goroutine完成
	ctx            context.Context      // 服务级别的context
	cancel         context.CancelFunc   // 服务级别的cancel函数
}

const (
	maxRunningContexts     = 1000             // 最大运行中的任务context数量
	maxTaskQueueSize       = 1000             // 每个Provider工作池的最大队列容量
	contextCleanupInterval = 30 * time.Second // 定期清理
	maxContextAge          = 15 * time.Minute // 超时强制清理
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
		global.APP_LOG.Info("任务context清理goroutine已停止")
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
				global.APP_LOG.Info("Context清理完成",
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
		global.APP_LOG.Info("Provider工作池清理goroutine已停止")
	}()

	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			// 清理空闲的工作池
			cleaned := s.poolManager.CleanupIdle(maxPoolIdleTime)

			// 从数据库查询有效的provider ID并清理已删除的
			if global.APP_DB != nil {
				var validProviderIDs []uint
				if err := global.APP_DB.Model(&providerModel.Provider{}).
					Pluck("id", &validProviderIDs).Error; err == nil {
					s.poolManager.CleanupDeleted(validProviderIDs)
				}
			}

			if cleaned > 0 {
				global.APP_LOG.Info("Provider工作池清理完成",
					zap.Int("cleaned", cleaned),
					zap.Int64("remaining", s.poolManager.Count()))
			}
		}
	}
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

	// 取消所有任务上下文
	s.contextManager.CancelAll()

	// 取消所有工作池
	s.poolManager.CancelAll()

	// 等待所有goroutine完成
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	// 等待最多30秒
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case <-done:
		global.APP_LOG.Info("所有后台任务已完成")
	case <-timer.C:
		global.APP_LOG.Warn("等待后台任务超时，强制退出")
	}

	global.APP_LOG.Info("TaskService关闭完成")
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

	global.APP_LOG.Info("端口映射分组完成",
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
