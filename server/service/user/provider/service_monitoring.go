package provider

import (
	"context"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	agentLifecycle "oneclickvirt/service/agent"
	trafficMonitor "oneclickvirt/service/admin/traffic_monitor"

	"go.uber.org/zap"
)

type postCreateMonitoringStatus struct {
	TrafficEnabled   bool
	TrafficMethod    string
	PmacctExpected   bool
	PmacctReady      bool
	AgentMonitorReady bool
}

func (s *Service) ensurePostCreateMonitoring(ctx context.Context, instanceID, providerID uint, reason string) postCreateMonitoringStatus {
	status := s.ensurePostCreatePmacctMonitor(ctx, instanceID, providerID, reason)
	s.ensurePostCreateAgentMonitor(ctx, instanceID, providerID, reason, &status)
	return status
}

func (s *Service) ensurePostCreatePmacctMonitor(ctx context.Context, instanceID, providerID uint, reason string) postCreateMonitoringStatus {
	status := postCreateMonitoringStatus{}

	var dbProvider providerModel.Provider
	if err := global.APP_DB.Where("id = ?", providerID).First(&dbProvider).Error; err != nil {
		global.APP_LOG.Warn("查询Provider监控配置失败，跳过创建后监控确认",
			zap.Uint("instanceId", instanceID),
			zap.Uint("providerId", providerID),
			zap.String("reason", reason),
			zap.Error(err))
		return status
	}

	status.TrafficEnabled = dbProvider.EnableTrafficControl
	status.TrafficMethod = dbProvider.TrafficSyncMethod
	status.PmacctExpected = dbProvider.EnableTrafficControl && dbProvider.TrafficSyncMethod != "agent"

	var existingMonitor monitoringModel.PmacctMonitor
	if err := global.APP_DB.Where("instance_id = ? AND is_enabled = ?", instanceID, true).First(&existingMonitor).Error; err == nil {
		status.PmacctReady = true
		global.APP_LOG.Debug("pmacct监控已就绪",
			zap.Uint("instanceId", instanceID),
			zap.Uint("monitorId", existingMonitor.ID),
			zap.String("reason", reason))
		return status
	}

	if !status.PmacctExpected {
		if status.TrafficEnabled {
			global.APP_LOG.Debug("Provider使用agent流量采集方式，跳过pmacct附加",
				zap.Uint("instanceId", instanceID),
				zap.Uint("providerId", providerID),
				zap.String("reason", reason))
		} else {
			global.APP_LOG.Debug("Provider未启用流量统计，无需附加pmacct监控",
				zap.Uint("instanceId", instanceID),
				zap.Uint("providerId", providerID),
				zap.String("reason", reason))
		}
		return status
	}

	attachCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	if err := trafficMonitor.GetManager().AttachMonitor(attachCtx, instanceID); err != nil {
		global.APP_LOG.Warn("创建后自动附加pmacct监控失败，实例创建继续完成",
			zap.Uint("instanceId", instanceID),
			zap.Uint("providerId", providerID),
			zap.String("reason", reason),
			zap.Error(err))
		return status
	}

	if err := global.APP_DB.Where("instance_id = ? AND is_enabled = ?", instanceID, true).First(&existingMonitor).Error; err == nil {
		status.PmacctReady = true
		global.APP_LOG.Info("创建后自动附加pmacct监控成功",
			zap.Uint("instanceId", instanceID),
			zap.Uint("monitorId", existingMonitor.ID),
			zap.String("reason", reason))
		return status
	}

	global.APP_LOG.Warn("创建后pmacct附加完成但未找到启用的监控记录",
		zap.Uint("instanceId", instanceID),
		zap.Uint("providerId", providerID),
		zap.String("reason", reason))
	return status
}

func (s *Service) ensurePostCreateAgentMonitor(ctx context.Context, instanceID, providerID uint, reason string, status *postCreateMonitoringStatus) {
	var monConfig monitoringModel.MonitoringConfig
	useAgent := true
	if err := global.APP_DB.Where("provider_id = ?", providerID).First(&monConfig).Error; err == nil {
		useAgent = monConfig.MonitoringMode != "pmacct"
	}
	if !useAgent {
		global.APP_LOG.Debug("Provider使用pmacct监控模式，跳过Agent注册",
			zap.Uint("instanceId", instanceID),
			zap.Uint("providerId", providerID),
			zap.String("reason", reason))
		return
	}

	agentCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	agentLifecycle.OnInstanceCreated(agentCtx, global.APP_DB, instanceID)
	cancel()

	var agentMonitor monitoringModel.AgentMonitor
	if err := global.APP_DB.Where("instance_id = ? AND is_enabled = ?", instanceID, true).First(&agentMonitor).Error; err == nil {
		status.AgentMonitorReady = true
		global.APP_LOG.Debug("Agent监控已就绪",
			zap.Uint("instanceId", instanceID),
			zap.Uint("agentMonitorId", agentMonitor.ID),
			zap.Int64("agentSideMonitorId", agentMonitor.AgentMonitorID),
			zap.String("reason", reason))
	}

	if status.TrafficEnabled && status.TrafficMethod == "agent" {
		config, err := agentLifecycle.GetMonitoringConfig(global.APP_DB.WithContext(ctx), providerID)
		if err != nil {
			global.APP_LOG.Warn("获取Agent监控配置失败，跳过创建后即时流量同步",
				zap.Uint("instanceId", instanceID),
				zap.Uint("providerId", providerID),
				zap.String("reason", reason),
				zap.Error(err))
			return
		}
		if config.MonitoringMode != "agent" || !config.AgentInstalled {
			global.APP_LOG.Debug("Agent监控未部署，跳过创建后即时流量同步",
				zap.Uint("instanceId", instanceID),
				zap.Uint("providerId", providerID),
				zap.String("reason", reason),
				zap.Bool("agentInstalled", config.AgentInstalled),
				zap.String("monitoringMode", config.MonitoringMode))
			return
		}

		syncCtx, syncCancel := context.WithTimeout(ctx, 90*time.Second)
		err = agentLifecycle.NewSyncService(syncCtx, global.APP_DB).SyncProviderTraffic(providerID, config)
		syncCancel()
		if err != nil {
			global.APP_LOG.Warn("创建后即时Agent流量同步失败，等待调度器后续同步",
				zap.Uint("instanceId", instanceID),
				zap.Uint("providerId", providerID),
				zap.String("reason", reason),
				zap.Error(err))
			return
		}
		global.APP_LOG.Debug("创建后即时Agent流量同步完成",
			zap.Uint("instanceId", instanceID),
			zap.Uint("providerId", providerID),
			zap.String("reason", reason))
	}
}
