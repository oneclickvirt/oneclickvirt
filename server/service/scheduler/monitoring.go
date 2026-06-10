package scheduler

import (
	"context"
	"sync"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// PmacctServiceInterface pmacct服务接口
type PmacctServiceInterface interface {
	// CollectTrafficFromSQLite 从Provider的SQLite数据库采集流量数据并同步到MySQL
	// 架构：Memory(1min) -> SQLite(local) -> MySQL(remote)
	// 参数：预加载的instance和monitor数据
	CollectTrafficFromSQLite(instance *providerModel.Instance, monitor *monitoringModel.PmacctMonitor) error

	// CleanupOldPmacctData 清理过期的流量数据
	CleanupOldPmacctData(days int) error

	// ResetPmacctDaemon 完全重置pmacct守护进程和数据库
	ResetPmacctDaemon(instanceID uint) error
}

// MonitoringSchedulerService 监控调度服务
type MonitoringSchedulerService struct {
	pmacctService        PmacctServiceInterface
	stopChan             chan struct{}
	isRunning            bool
	wg                   sync.WaitGroup        // 追踪所有后台goroutine
	providerStateManager *ProviderStateManager // Provider状态管理器
	lastResetTime        sync.Map              // map[uint]time.Time - pmacct重置时间记录
	lastResetCleanup     time.Time             // 最后清理时间
	agentTrafficRunning  sync.Map              // map[uint]time.Time - 防止同一Provider的agent流量采集重叠
	agentResourceRunning sync.Map              // map[uint]time.Time - 防止同一Provider的agent资源采集重叠
	mu                   sync.RWMutex          // 保护 isRunning 和 lastResetCleanup
}

// NewMonitoringSchedulerService 创建监控调度服务
func NewMonitoringSchedulerService(pmacctService PmacctServiceInterface) *MonitoringSchedulerService {
	return &MonitoringSchedulerService{
		pmacctService:        pmacctService,
		stopChan:             make(chan struct{}),
		isRunning:            false,
		providerStateManager: NewProviderStateManager(),
		lastResetCleanup:     time.Now(),
	}
}

// Start 启动监控调度器
func (s *MonitoringSchedulerService) Start(ctx context.Context) {
	s.mu.Lock()
	if s.isRunning {
		s.mu.Unlock()
		global.APP_LOG.Warn("监控调度器已在运行中")
		return
	}
	s.stopChan = make(chan struct{}) // 每次启动时重建，防止复用已关闭的channel
	s.isRunning = true
	s.mu.Unlock()

	global.APP_LOG.Info("启动监控调度器")

	// 启动pmacct流量数据收集任务
	s.wg.Add(5)
	go s.startPmacctCollection(ctx)

	// 启动agent流量数据收集任务
	go s.startAgentCollection(ctx)

	// 启动agent资源监控收集任务
	go s.startAgentResourceCollection(ctx)

	// 启动清理任务
	go s.startCleanupTask(ctx)

	// 启动pmacct守护进程重置任务
	go s.startPmacctResetTask(ctx)
}

// Stop 停止监控调度器
func (s *MonitoringSchedulerService) Stop() {
	s.mu.Lock()
	if !s.isRunning {
		s.mu.Unlock()
		return
	}
	s.isRunning = false
	s.mu.Unlock()

	global.APP_LOG.Info("停止监控调度器")
	close(s.stopChan)

	// 等待所有goroutine完成（最多30秒）
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case <-done:
		global.APP_LOG.Info("监控调度器所有后台任务已完成")
	case <-timer.C:
		global.APP_LOG.Warn("监控调度器关闭超时，可能有goroutine未完成")
	}
}

// IsRunning 检查调度器是否正在运行
func (s *MonitoringSchedulerService) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isRunning
}

// DeleteProviderState 删除Provider状态（完全原子性同步清理所有相关sync.Map）
func (s *MonitoringSchedulerService) DeleteProviderState(providerID uint) {
	// 原子性操作：从所有sync.Map中删除（防止孤立条目）
	s.providerStateManager.Delete(providerID)
	s.lastResetTime.Delete(providerID)

	global.APP_LOG.Debug("原子性删除Provider状态及重置时间记录",
		zap.Uint("providerID", providerID))
}

func (s *MonitoringSchedulerService) tryStartAgentTrafficSync(providerID uint) bool {
	_, loaded := s.agentTrafficRunning.LoadOrStore(providerID, time.Now())
	return !loaded
}

func (s *MonitoringSchedulerService) finishAgentTrafficSync(providerID uint) {
	s.agentTrafficRunning.Delete(providerID)
}

func (s *MonitoringSchedulerService) tryStartAgentResourceSync(providerID uint) bool {
	_, loaded := s.agentResourceRunning.LoadOrStore(providerID, time.Now())
	return !loaded
}

func (s *MonitoringSchedulerService) finishAgentResourceSync(providerID uint) {
	s.agentResourceRunning.Delete(providerID)
}
