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

// ProviderHealthSchedulerService Provider健康检查调度服务
type ProviderHealthSchedulerService struct {
	providerService *adminProviderService.Service
	stopChan        chan struct{}
	isRunning       bool
	maxConcurrency  int           // 最大并发数
	semaphore       chan struct{} // 信号量，用于限制并发
}

// NewProviderHealthSchedulerService 创建Provider健康检查调度服务
func NewProviderHealthSchedulerService() *ProviderHealthSchedulerService {
	maxConcurrency := 3 // 最多同时检查3个provider
	return &ProviderHealthSchedulerService{
		providerService: adminProviderService.NewService(),
		stopChan:        make(chan struct{}),
		isRunning:       false,
		maxConcurrency:  maxConcurrency,
		semaphore:       make(chan struct{}, maxConcurrency),
	}
}

// Start 启动健康检查调度器
func (s *ProviderHealthSchedulerService) Start(ctx context.Context) {
	if s.isRunning {
		global.APP_LOG.Warn("Provider健康检查调度器已在运行中")
		return
	}

	s.isRunning = true
	global.APP_LOG.Info("启动Provider健康检查调度器")

	// 启动定期健康检查任务
	go s.startHealthCheckTask(ctx)
}

// Stop 停止健康检查调度器
func (s *ProviderHealthSchedulerService) Stop() {
	if !s.isRunning {
		return
	}

	global.APP_LOG.Info("停止Provider健康检查调度器")
	close(s.stopChan)
	s.isRunning = false
}

// IsRunning 检查调度器是否正在运行
func (s *ProviderHealthSchedulerService) IsRunning() bool {
	return s.isRunning
}

// startHealthCheckTask 启动自适应健康检查任务
func (s *ProviderHealthSchedulerService) startHealthCheckTask(ctx context.Context) {
	// 启动后立即执行一次
	s.checkAllProvidersHealth()

	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	ticker := time.NewTicker(3 * time.Minute)
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil {
			global.APP_LOG.Error("Provider健康检查goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("Provider健康检查任务已停止")
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopChan:
			return
		case <-ticker.C:
			// 动态调整检查间隔
			if global.APP_DB == nil {
				continue
			}

			var providerCount int64
			global.APP_DB.Model(&providerModel.Provider{}).
				Where("is_frozen = ? AND (expires_at IS NULL OR expires_at > ?)", false, time.Now()).
				Count(&providerCount)

			// 有Provider时3分钟检查，无Provider时10分钟检查（节省资源）
			newInterval := 10 * time.Minute
			if providerCount > 0 {
				newInterval = 3 * time.Minute
			}
			ticker.Reset(newInterval)

			s.checkAllProvidersHealth()
		}
	}
}

// checkAllProvidersHealth 检查所有Provider的健康状态
func (s *ProviderHealthSchedulerService) checkAllProvidersHealth() {
	// 获取所有需要检查的Provider（非冻结、未过期）
	var providers []providerModel.Provider
	err := global.APP_DB.Where("is_frozen = ? AND (expires_at IS NULL OR expires_at > ?)", false, time.Now()).
		Find(&providers).Error

	if err != nil {
		global.APP_LOG.Error("获取Provider列表失败", zap.Error(err))
		return
	}

	if len(providers) == 0 {
		global.APP_LOG.Debug("没有需要检查的Provider")
		return
	}

	global.APP_LOG.Debug("开始检查Provider健康状态", zap.Int("count", len(providers)))

	// 使用worker池模式避免创建过多goroutine
	// 对于provider数量较少的情况，直接并发处理
	// 对于provider数量较多的情况，分批处理
	providerChan := make(chan providerModel.Provider, len(providers))
	for _, provider := range providers {
		providerChan <- provider
	}
	close(providerChan)

	// 启动固定数量的worker
	var wg sync.WaitGroup
	for i := 0; i < s.maxConcurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for provider := range providerChan {
				s.checkSingleProviderHealth(provider)
			}
		}(i)
	}

	// 等待所有检查完成
	wg.Wait()
	global.APP_LOG.Debug("所有Provider健康检查完成")
}

// checkSingleProviderHealth 检查单个Provider的健康状态
func (s *ProviderHealthSchedulerService) checkSingleProviderHealth(provider providerModel.Provider) {
	// 复制副本避免共享状态，立即创建所有参数的本地副本
	// 这些变量在整个函数执行期间保持不变
	providerID := provider.ID
	providerName := provider.Name
	providerType := provider.Type
	providerEndpoint := provider.Endpoint
	oldSSHStatus := provider.SSHStatus
	oldAPIStatus := provider.APIStatus
	oldStatus := provider.Status

	global.APP_LOG.Debug("开始单个Provider健康检查",
		zap.Uint("providerId", providerID),
		zap.String("providerName", providerName),
		zap.String("providerType", providerType),
		zap.String("endpoint", providerEndpoint))

	// 添加整体超时控制（2分钟）
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	// 直接执行健康检查，带超时控制
	errChan := make(chan error, 1)
	go func() {
		errChan <- s.providerService.CheckProviderHealth(providerID)
	}()

	// 等待结果或超时
	var err error
	select {
	case err = <-errChan:
		if err != nil {
			global.APP_LOG.Warn("Provider健康检查执行出错（可能是超时或网络问题）",
				zap.Uint("provider_id", providerID),
				zap.String("provider_name", providerName),
				zap.Error(err))
		} else {
			global.APP_LOG.Debug("Provider健康检查执行完成",
				zap.Uint("provider_id", providerID),
				zap.String("provider_name", providerName))
		}
	case <-ctx.Done():
		global.APP_LOG.Warn("Provider健康检查超时，强制继续",
			zap.Uint("provider_id", providerID),
			zap.String("provider_name", providerName),
			zap.Duration("timeout", 2*time.Minute))
		// 超时也继续处理，因为状态可能部分更新
	}

	// 重新获取Provider以获得最新状态
	var updatedProvider providerModel.Provider
	if err := global.APP_DB.First(&updatedProvider, providerID).Error; err != nil {
		global.APP_LOG.Error("获取更新后的Provider失败", zap.Uint("provider_id", providerID), zap.Error(err))
		return
	}

	// 检测同类型Provider的hostname冲突（仅记录警告，不做任何处理）
	if updatedProvider.HostName != "" {
		s.detectHostnameConflicts(providerID, providerName, providerType, updatedProvider.HostName, updatedProvider.Endpoint)
	}

	// 检查Provider状态是否发生变化
	statusChanged := oldSSHStatus != updatedProvider.SSHStatus ||
		oldAPIStatus != updatedProvider.APIStatus ||
		oldStatus != updatedProvider.Status

	if statusChanged {
		global.APP_LOG.Info("Provider状态发生变化",
			zap.Uint("provider_id", providerID),
			zap.String("provider_name", providerName),
			zap.String("old_status", oldStatus),
			zap.String("new_status", updatedProvider.Status),
			zap.String("old_ssh", oldSSHStatus),
			zap.String("new_ssh", updatedProvider.SSHStatus),
			zap.String("old_api", oldAPIStatus),
			zap.String("new_api", updatedProvider.APIStatus))

		// 根据Provider健康状态更新allow_claim字段，控制是否允许申领新实例
		// 重要原则：
		// 1. 健康检查仅影响新实例的申领（allow_claim字段）
		// 2. 不影响已在进行中的任务和已创建的实例
		// 3. 只有完全offline (inactive)时才禁止申领，partial状态仍允许申领
		// 4. 健康检查超时或网络问题不应该直接禁止申领
		if updatedProvider.Status == "inactive" && oldStatus != "inactive" {
			// Provider变为完全离线，禁止申领新实例
			// 但不取消已在进行中的任务
			s.updateProviderAllowClaim(providerID, false)
			global.APP_LOG.Warn("Provider完全离线，禁止申领新实例（不影响进行中的任务）",
				zap.Uint("provider_id", providerID),
				zap.String("provider_name", providerName),
				zap.String("ssh_status", updatedProvider.SSHStatus),
				zap.String("api_status", updatedProvider.APIStatus))
		} else if updatedProvider.Status == "active" && oldStatus != "active" {
			// Provider恢复在线，允许申领新实例
			s.updateProviderAllowClaim(providerID, true)
			global.APP_LOG.Info("Provider恢复在线，允许申领新实例",
				zap.Uint("provider_id", providerID),
				zap.String("provider_name", providerName))
		} else if updatedProvider.Status == "partial" && oldStatus == "inactive" {
			// Provider从完全离线恢复到部分在线，也应该允许申领
			s.updateProviderAllowClaim(providerID, true)
			global.APP_LOG.Info("Provider部分恢复在线，允许申领新实例",
				zap.Uint("provider_id", providerID),
				zap.String("provider_name", providerName),
				zap.String("ssh_status", updatedProvider.SSHStatus),
				zap.String("api_status", updatedProvider.APIStatus))
		}
	}
}

// updateProviderAllowClaim 更新Provider的allow_claim字段
// 此方法仅控制是否允许在该Provider上申领新实例
// 不影响现有实例的状态，保持实例的实际运行状态和用户操作意图
func (s *ProviderHealthSchedulerService) updateProviderAllowClaim(providerID uint, allowClaim bool) {
	err := global.APP_DB.Model(&providerModel.Provider{}).
		Where("id = ?", providerID).
		Update("allow_claim", allowClaim).Error

	if err != nil {
		global.APP_LOG.Error("更新Provider的allow_claim状态失败",
			zap.Uint("provider_id", providerID),
			zap.Bool("allow_claim", allowClaim),
			zap.Error(err))
		return
	}

	statusMsg := "禁止申领新实例"
	if allowClaim {
		statusMsg = "允许申领新实例"
	}
	global.APP_LOG.Info("Provider申领状态已更新",
		zap.Uint("provider_id", providerID),
		zap.Bool("allow_claim", allowClaim),
		zap.String("message", statusMsg))
}

// detectHostnameConflicts 检测同类型Provider的hostname冲突
// 仅记录警告日志，不做任何实际处理，由管理员决定是否需要调整配置
func (s *ProviderHealthSchedulerService) detectHostnameConflicts(currentProviderID uint, currentProviderName, currentProviderType, currentHostName, currentEndpoint string) {
	// 查询同类型且hostname相同的其他Provider
	var conflictingProviders []providerModel.Provider
	err := global.APP_DB.Where("type = ? AND host_name = ? AND id != ? AND host_name != ''",
		currentProviderType, currentHostName, currentProviderID).
		Find(&conflictingProviders).Error

	if err != nil {
		global.APP_LOG.Error("检测hostname冲突时查询失败",
			zap.Uint("provider_id", currentProviderID),
			zap.String("provider_name", currentProviderName),
			zap.String("hostname", currentHostName),
			zap.Error(err))
		return
	}

	// 如果发现冲突，记录警告
	if len(conflictingProviders) > 0 {
		conflictNames := make([]string, 0, len(conflictingProviders))
		conflictIDs := make([]uint, 0, len(conflictingProviders))
		conflictEndpoints := make([]string, 0, len(conflictingProviders))

		for _, cp := range conflictingProviders {
			conflictNames = append(conflictNames, cp.Name)
			conflictIDs = append(conflictIDs, cp.ID)
			conflictEndpoints = append(conflictEndpoints, cp.Endpoint)
		}

		global.APP_LOG.Warn("检测到同类型Provider的hostname冲突",
			zap.Uint("current_provider_id", currentProviderID),
			zap.String("current_provider_name", currentProviderName),
			zap.String("current_provider_type", currentProviderType),
			zap.String("current_endpoint", currentEndpoint),
			zap.String("conflicting_hostname", currentHostName),
			zap.Int("conflict_count", len(conflictingProviders)),
			zap.Uints("conflicting_provider_ids", conflictIDs),
			zap.Strings("conflicting_provider_names", conflictNames),
			zap.Strings("conflicting_endpoints", conflictEndpoints),
			zap.String("suggestion", "请检查这些Provider是否指向同一物理节点，或考虑为节点配置不同的hostname以避免混淆"))
	}
}
