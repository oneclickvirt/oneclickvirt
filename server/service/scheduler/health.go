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
	err := s.providerService.CheckProviderHealth(provider.ID)
	if err != nil {
		global.APP_LOG.Warn("Provider健康检查失败",
			zap.Uint("provider_id", provider.ID),
			zap.String("provider_name", provider.Name),
			zap.Error(err))
		return
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

		// 如果Provider变为不可用状态，更新该Provider下所有实例的状态
		if (updatedProvider.Status == "inactive" || updatedProvider.Status == "partial") &&
			(oldStatus == "active" || oldStatus == "") {
			s.syncInstanceStatusOnProviderDown(provider.ID)
		} else if updatedProvider.Status == "active" && oldStatus != "active" {
			// Provider恢复在线，记录日志但不自动更新实例状态
			// 因为实例可能在离线期间被用户停止，需要保持用户操作的意图
			global.APP_LOG.Info("Provider恢复在线，实例状态保持不变",
				zap.Uint("provider_id", provider.ID),
				zap.String("provider_name", provider.Name))
		}
	}
}

// syncInstanceStatusOnProviderDown 当Provider下线时同步实例状态
func (s *ProviderHealthSchedulerService) syncInstanceStatusOnProviderDown(providerID uint) {
	// 查找该Provider下所有状态为running的实例
	var instances []providerModel.Instance
	err := global.APP_DB.Where("provider_id = ? AND status = ?", providerID, "running").
		Find(&instances).Error

	if err != nil {
		global.APP_LOG.Error("获取Provider实例列表失败",
			zap.Uint("provider_id", providerID),
			zap.Error(err))
		return
	}

	if len(instances) == 0 {
		return
	}

	global.APP_LOG.Info("Provider下线，更新实例状态",
		zap.Uint("provider_id", providerID),
		zap.Int("instance_count", len(instances)))

	// 将所有running实例标记为unavailable
	for _, instance := range instances {
		err := global.APP_DB.Model(&providerModel.Instance{}).
			Where("id = ?", instance.ID).
			Update("status", "unavailable").Error

		if err != nil {
			global.APP_LOG.Error("更新实例状态失败",
				zap.Uint("instance_id", instance.ID),
				zap.String("instance_name", instance.Name),
				zap.Error(err))
		} else {
			global.APP_LOG.Debug("实例状态已更新为unavailable",
				zap.Uint("instance_id", instance.ID),
				zap.String("instance_name", instance.Name))
		}
	}
}
