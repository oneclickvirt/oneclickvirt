package scheduler

import (
	"context"
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
}

// NewProviderHealthSchedulerService 创建Provider健康检查调度服务
func NewProviderHealthSchedulerService() *ProviderHealthSchedulerService {
	return &ProviderHealthSchedulerService{
		providerService: adminProviderService.NewService(),
		stopChan:        make(chan struct{}),
		isRunning:       false,
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

// startHealthCheckTask 启动健康检查任务
func (s *ProviderHealthSchedulerService) startHealthCheckTask(ctx context.Context) {
	// 健康检查间隔（3分钟）
	ticker := time.NewTicker(3 * time.Minute)
	defer ticker.Stop()

	// 启动后立即执行一次
	s.checkAllProvidersHealth()

	for {
		select {
		case <-s.stopChan:
			global.APP_LOG.Info("Provider健康检查任务已停止")
			return
		case <-ticker.C:
			// 检查数据库是否已初始化
			if global.APP_DB == nil {
				global.APP_LOG.Debug("数据库未初始化，跳过健康检查")
				continue
			}

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

	// 并发检查所有Provider
	for _, provider := range providers {
		go s.checkSingleProviderHealth(provider)
	}
}

// checkSingleProviderHealth 检查单个Provider的健康状态
func (s *ProviderHealthSchedulerService) checkSingleProviderHealth(provider providerModel.Provider) {
	oldSSHStatus := provider.SSHStatus
	oldAPIStatus := provider.APIStatus
	oldStatus := provider.Status

	// 执行健康检查
	// 注意：即使检查失败（超时、网络错误等），状态也已经被更新到数据库
	// 因此我们需要继续处理，而不是直接返回
	err := s.providerService.CheckProviderHealth(provider.ID)
	if err != nil {
		global.APP_LOG.Warn("Provider健康检查执行出错（可能是超时或网络问题）",
			zap.Uint("provider_id", provider.ID),
			zap.String("provider_name", provider.Name),
			zap.Error(err))
		// 不要return，继续获取更新后的状态
	}

	// 重新获取Provider以获得最新状态
	var updatedProvider providerModel.Provider
	if err := global.APP_DB.First(&updatedProvider, provider.ID).Error; err != nil {
		global.APP_LOG.Error("获取更新后的Provider失败", zap.Uint("provider_id", provider.ID), zap.Error(err))
		return
	}

	// 检查Provider状态是否发生变化
	statusChanged := oldSSHStatus != updatedProvider.SSHStatus ||
		oldAPIStatus != updatedProvider.APIStatus ||
		oldStatus != updatedProvider.Status

	if statusChanged {
		global.APP_LOG.Info("Provider状态发生变化",
			zap.Uint("provider_id", provider.ID),
			zap.String("provider_name", provider.Name),
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
			s.updateProviderAllowClaim(provider.ID, false)
			global.APP_LOG.Warn("Provider完全离线，禁止申领新实例（不影响进行中的任务）",
				zap.Uint("provider_id", provider.ID),
				zap.String("provider_name", provider.Name),
				zap.String("ssh_status", updatedProvider.SSHStatus),
				zap.String("api_status", updatedProvider.APIStatus))
		} else if updatedProvider.Status == "active" && oldStatus != "active" {
			// Provider恢复在线，允许申领新实例
			s.updateProviderAllowClaim(provider.ID, true)
			global.APP_LOG.Info("Provider恢复在线，允许申领新实例",
				zap.Uint("provider_id", provider.ID),
				zap.String("provider_name", provider.Name))
		} else if updatedProvider.Status == "partial" && oldStatus == "inactive" {
			// Provider从完全离线恢复到部分在线，也应该允许申领
			s.updateProviderAllowClaim(provider.ID, true)
			global.APP_LOG.Info("Provider部分恢复在线，允许申领新实例",
				zap.Uint("provider_id", provider.ID),
				zap.String("provider_name", provider.Name),
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
