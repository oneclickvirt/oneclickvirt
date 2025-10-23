package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"oneclickvirt/service/database"
	"oneclickvirt/service/interfaces"
	provider2 "oneclickvirt/service/provider"
	"oneclickvirt/service/traffic"
	userprovider "oneclickvirt/service/user/provider"
	"oneclickvirt/service/vnstat"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"

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

// GetStateManager 获取任务状态管理器
func (s *TaskService) GetStateManager() interfaces.TaskStateManagerInterface {
	return GetTaskStateManager()
}
