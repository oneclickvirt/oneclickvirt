package scheduler

import (
	"context"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	adminProviderService "oneclickvirt/service/admin/provider"

	"go.uber.org/zap"
)

// InstanceSyncSchedulerService Provider实例同步调度服务
type InstanceSyncSchedulerService struct {
	providerService *adminProviderService.Service
	stopChan        chan struct{}
	isRunning       bool
	maxConcurrency  int           // 最大并发数
	semaphore       chan struct{} // 信号量，用于限制并发
}

// NewInstanceSyncSchedulerService 创建实例同步调度服务
func NewInstanceSyncSchedulerService() *InstanceSyncSchedulerService {
	maxConcurrency := 2 // 最多同时同步2个provider
	return &InstanceSyncSchedulerService{
		providerService: adminProviderService.NewService(),
		stopChan:        make(chan struct{}),
		isRunning:       false,
		maxConcurrency:  maxConcurrency,
		semaphore:       make(chan struct{}, maxConcurrency),
	}
}

// Start 启动实例同步调度器
func (s *InstanceSyncSchedulerService) Start(ctx context.Context) {
	// 检查是否启用实例同步
	if !global.APP_CONFIG.System.EnableInstanceSync {
		global.APP_LOG.Info("实例同步功能未启用，跳过调度器启动")
		return
	}

	if s.isRunning {
		global.APP_LOG.Warn("Provider实例同步调度器已在运行中")
		return
	}

	s.isRunning = true
	global.APP_LOG.Info("启动Provider实例同步调度器",
		zap.Int("syncInterval", global.APP_CONFIG.System.InstanceSyncInterval))

	// 启动定期同步任务
	go s.startSyncTask(ctx)
}

// Stop 停止实例同步调度器
func (s *InstanceSyncSchedulerService) Stop() {
	if !s.isRunning {
		return
	}

	global.APP_LOG.Info("停止Provider实例同步调度器")
	close(s.stopChan)
	s.isRunning = false
}

// IsRunning 检查调度器是否正在运行
func (s *InstanceSyncSchedulerService) IsRunning() bool {
	return s.isRunning
}

// startSyncTask 启动实例同步任务
func (s *InstanceSyncSchedulerService) startSyncTask(ctx context.Context) {
	// 延迟启动，等待系统初始化完成
	time.Sleep(2 * time.Minute)

	// 首次执行
	s.syncAllProvidersInstances()

	// 获取同步间隔（分钟），默认30分钟
	syncInterval := global.APP_CONFIG.System.InstanceSyncInterval
	if syncInterval <= 0 {
		syncInterval = 30
	}

	ticker := time.NewTicker(time.Duration(syncInterval) * time.Minute)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil {
			global.APP_LOG.Error("Provider实例同步goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("Provider实例同步任务已停止")
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			if global.APP_DB == nil {
				continue
			}

			// 执行同步检查
			s.syncAllProvidersInstances()
		}
	}
}

// syncAllProvidersInstances 同步所有Provider的实例
func (s *InstanceSyncSchedulerService) syncAllProvidersInstances() {
	startTime := time.Now()
	global.APP_LOG.Info("开始Provider实例同步检查")

	// 获取所有活跃的Provider
	var providers []providerModel.Provider
	if err := global.APP_DB.Where("status = ? AND is_frozen = ? AND (expires_at IS NULL OR expires_at > ?)",
		"active", false, time.Now()).
		Select("id", "name", "type").
		Find(&providers).Error; err != nil {
		global.APP_LOG.Error("查询Provider列表失败", zap.Error(err))
		return
	}

	if len(providers) == 0 {
		global.APP_LOG.Info("没有活跃的Provider需要同步")
		return
	}

	global.APP_LOG.Info("准备同步Provider实例",
		zap.Int("providerCount", len(providers)))

	// 使用WaitGroup等待所有同步任务完成
	var wg sync.WaitGroup
	successCount := 0
	failedCount := 0
	changedCount := 0
	var mu sync.Mutex

	for _, prov := range providers {
		wg.Add(1)

		// 获取信号量（限制并发）
		s.semaphore <- struct{}{}

		go func(provider providerModel.Provider) {
			defer func() {
				<-s.semaphore // 释放信号量
				wg.Done()
				if r := recover(); r != nil {
					global.APP_LOG.Error("Provider实例同步panic",
						zap.Uint("providerId", provider.ID),
						zap.String("providerName", provider.Name),
						zap.Any("panic", r))
				}
			}()

			// 执行实例比对
			report, err := s.providerService.CompareInstancesWithRemote(context.Background(), provider.ID)
			if err != nil {
				global.APP_LOG.Error("Provider实例同步失败",
					zap.Uint("providerId", provider.ID),
					zap.String("providerName", provider.Name),
					zap.Error(err))
				mu.Lock()
				failedCount++
				mu.Unlock()
				return
			}

			mu.Lock()
			successCount++
			totalChanges := len(report.NewInstances) + len(report.DeletedInstances) + len(report.ChangedInstances)
			changedCount += totalChanges
			mu.Unlock()

			// 如果检测到变化，记录日志和可能的告警
			if totalChanges > 0 {
				global.APP_LOG.Warn("检测到Provider实例变化",
					zap.Uint("providerId", provider.ID),
					zap.String("providerName", provider.Name),
					zap.Int("newInstances", len(report.NewInstances)),
					zap.Int("deletedInstances", len(report.DeletedInstances)),
					zap.Int("changedInstances", len(report.ChangedInstances)))

				// 记录详细变化
				if len(report.NewInstances) > 0 {
					global.APP_LOG.Info("发现新增实例",
						zap.Uint("providerId", provider.ID),
						zap.String("providerName", provider.Name),
						zap.Int("count", len(report.NewInstances)))
				}

				if len(report.DeletedInstances) > 0 {
					global.APP_LOG.Warn("发现已删除实例",
						zap.Uint("providerId", provider.ID),
						zap.String("providerName", provider.Name),
						zap.Int("count", len(report.DeletedInstances)))
				}

				if len(report.ChangedInstances) > 0 {
					for _, change := range report.ChangedInstances {
						global.APP_LOG.Info("实例状态变化",
							zap.Uint("providerId", provider.ID),
							zap.String("instanceName", change.Name),
							zap.String("oldStatus", change.OldStatus),
							zap.String("newStatus", change.NewStatus))
					}
				}

				// TODO: 可以在这里添加告警通知（邮件、Webhook等）
				// 例如：s.sendAlert(provider.ID, report)
			}
		}(prov)
	}

	// 等待所有同步任务完成
	wg.Wait()

	duration := time.Since(startTime)
	global.APP_LOG.Info("Provider实例同步检查完成",
		zap.Int("totalProviders", len(providers)),
		zap.Int("successCount", successCount),
		zap.Int("failedCount", failedCount),
		zap.Int("totalChanges", changedCount),
		zap.Duration("duration", duration))
}

// sendAlert 发送告警通知（预留接口）
func (s *InstanceSyncSchedulerService) sendAlert(providerID uint, report *adminProviderService.InstanceSyncReport) {
	// TODO: 实现告警逻辑
	// 可以通过邮件、Webhook、Slack等方式发送告警
	// 示例：
	// - 发送邮件给管理员
	// - 调用Webhook通知监控系统
	// - 记录到告警数据库表
	global.APP_LOG.Info("告警通知",
		zap.Uint("providerId", providerID),
		zap.String("providerName", report.ProviderName),
		zap.Int("newInstances", len(report.NewInstances)),
		zap.Int("deletedInstances", len(report.DeletedInstances)),
		zap.Int("changedInstances", len(report.ChangedInstances)))
}
