package provider

import (
	"context"
	"fmt"
	"oneclickvirt/global"
	"oneclickvirt/model/admin"
	domainModel "oneclickvirt/model/domain"
	"oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	resourceModel "oneclickvirt/model/resource"
	"oneclickvirt/provider"
	agentService "oneclickvirt/service/agent"
	"oneclickvirt/service/database"
	domainService "oneclickvirt/service/domain"
	"oneclickvirt/service/firewall"
	"oneclickvirt/service/pmacct"
	providerService "oneclickvirt/service/provider"
	"oneclickvirt/service/task"
	"os"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// DeleteProvider 删除Provider（级联硬删除所有相关数据）
// forceDelete=false: 正常删除，先删除节点上所有实例（实际删除宿主机资源），再清理数据库
// forceDelete=true: 强制删除，仅清理数据库记录，不触碰宿主机上的实际实例
func (s *Service) DeleteProvider(providerID uint, forceDelete bool) error {
	global.APP_LOG.Info("开始删除Provider及其所有关联数据",
		zap.Uint("providerID", providerID),
		zap.Bool("forceDelete", forceDelete))

	// 检查Provider是否存在
	var existingProvider providerModel.Provider
	if err := global.APP_DB.First(&existingProvider, providerID).Error; err != nil {
		return fmt.Errorf("提供商不存在")
	}

	if !forceDelete {
		// ==========================================
		// 正常删除模式：先级联删除节点上的所有实例
		// ==========================================

		// 获取所有非删除状态的实例
		var instances []providerModel.Instance
		global.APP_DB.Where("provider_id = ? AND status != ?", providerID, "deleted").
			Find(&instances)

		if len(instances) > 0 {
			global.APP_LOG.Info("正常删除模式：开始级联删除节点上的实例",
				zap.Uint("providerID", providerID),
				zap.Int("instanceCount", len(instances)))

			// 尝试获取已连接的Provider实例来执行实际的宿主机删除
			providerApiService := &providerService.ProviderApiService{}
			connected := false

			// 先尝试获取已连接的provider
			if prov, exists := providerService.GetProviderService().GetProviderByID(providerID); exists && prov.IsConnected() {
				connected = true
			}

			if !connected {
				// 尝试连接（删除操作允许访问冻结的provider）
				if _, _, err := providerApiService.GetProviderByIDForOperation(providerID, "delete"); err != nil {
					global.APP_LOG.Warn("正常删除模式：无法连接到节点，请使用强制删除",
						zap.Uint("providerID", providerID),
						zap.Error(err))
					return fmt.Errorf("无法连接到节点「%s」，无法安全删除实例。请使用强制删除（仅清理数据库记录，不触碰宿主机上的实际实例）", existingProvider.Name)
				}
			}

			// 逐个删除实例（在宿主机上实际删除）
			// 只处理需要实际删除的实例（跳过已删除/失败状态的实例）
			var deleteErrors []string
			successCount := 0
			skipCount := 0
			for _, inst := range instances {
				// 跳过已经处于终态的实例（deleted, failed 等无需在宿主机上操作）
				if inst.Status == "deleted" || inst.Status == "failed" {
					skipCount++
					global.APP_LOG.Debug("正常删除模式：跳过已处于终态的实例",
						zap.Uint("providerID", providerID),
						zap.Uint("instanceID", inst.ID),
						zap.String("instanceName", inst.Name),
						zap.String("status", inst.Status))
					continue
				}

				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
				err := providerApiService.DeleteInstanceByProviderID(ctx, providerID, inst.Name)
				cancel()
				if err != nil {
					errMsg := fmt.Sprintf("实例 %s(ID:%d, 状态:%s): %v", inst.Name, inst.ID, inst.Status, err)
					deleteErrors = append(deleteErrors, errMsg)
					global.APP_LOG.Error("正常删除模式：删除实例失败，将中止Provider删除以保护数据安全",
						zap.Uint("providerID", providerID),
						zap.Uint("instanceID", inst.ID),
						zap.String("instanceName", inst.Name),
						zap.String("status", inst.Status),
						zap.Error(err))
				} else {
					successCount++
					global.APP_LOG.Info("正常删除模式：实例删除成功",
						zap.Uint("providerID", providerID),
						zap.Uint("instanceID", inst.ID),
						zap.String("instanceName", inst.Name))
				}
			}

			if skipCount > 0 {
				global.APP_LOG.Info("正常删除模式：跳过了已处于终态的实例",
					zap.Uint("providerID", providerID),
					zap.Int("skipCount", skipCount))
			}

			// 如果有实例删除失败，中止Provider删除，防止产生孤立实例
			if len(deleteErrors) > 0 {
				global.APP_LOG.Error("正常删除模式：部分实例删除失败，中止Provider删除",
					zap.Uint("providerID", providerID),
					zap.Int("successCount", successCount),
					zap.Int("failCount", len(deleteErrors)),
					zap.Strings("errors", deleteErrors))
				return fmt.Errorf("级联删除失败：成功删除 %d 个实例，%d 个实例删除失败。已中止Provider删除以保护数据安全。失败的实例：%s。请检查后重试，或使用强制删除（仅清理数据库）",
					successCount, len(deleteErrors), deleteErrors[0])
			}

			global.APP_LOG.Info("正常删除模式：所有实例删除成功",
				zap.Uint("providerID", providerID),
				zap.Int("count", successCount))
		} else {
			global.APP_LOG.Info("正常删除模式：节点上无实例，直接清理数据库",
				zap.Uint("providerID", providerID))
		}
	} else {
		// ==========================================
		// 强制删除模式：仅清理数据库，不触碰宿主机
		// ==========================================
		var instanceCount int64
		global.APP_DB.Model(&providerModel.Instance{}).
			Where("provider_id = ?", providerID).
			Count(&instanceCount)
		if instanceCount > 0 {
			global.APP_LOG.Warn("强制删除模式：仅清理数据库记录，宿主机上的实际实例不会被删除",
				zap.Uint("providerID", providerID),
				zap.Int64("instanceCount", instanceCount))
		}
	}

	// ==========================================
	// 数据库清理（两种模式共用）
	// ==========================================

	// 获取所有关联的实例ID（包括软删除的）
	var instanceIDs []uint
	global.APP_DB.Unscoped().Model(&providerModel.Instance{}).
		Where("provider_id = ?", providerID).
		Pluck("id", &instanceIDs)

	var controllerPortIDs []uint
	if err := global.APP_DB.Model(&providerModel.Port{}).
		Where("provider_id = ? AND mapping_type = ?", providerID, "controller").
		Pluck("id", &controllerPortIDs).Error; err != nil {
		global.APP_LOG.Warn("查询Provider控制端端口转发失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
	}
	for _, portID := range controllerPortIDs {
		agentService.StopControllerPortForward(portID)
	}

	var providerDomains []domainModel.Domain
	if err := global.APP_DB.Where("provider_id = ?", providerID).Find(&providerDomains).Error; err != nil {
		global.APP_LOG.Warn("查询Provider域名绑定失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
	}
	if len(providerDomains) > 0 {
		(&domainService.Service{}).RemoveDomainProxies(providerDomains)
	}

	dbService := database.GetDatabaseService()
	err := dbService.ExecuteTransaction(context.Background(), func(tx *gorm.DB) error {
		// 1. 硬删除所有关联的端口映射（包括软删除的）
		portResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&providerModel.Port{})
		if portResult.Error != nil {
			global.APP_LOG.Error("删除Provider端口映射失败", zap.Error(portResult.Error))
			return portResult.Error
		}
		if portResult.RowsAffected > 0 {
			global.APP_LOG.Debug("成功删除Provider端口映射",
				zap.Uint("providerID", providerID),
				zap.Int64("count", portResult.RowsAffected))
		}

		// 2. 硬删除所有关联的任务（包括软删除的）
		taskResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&admin.Task{})
		if taskResult.Error != nil {
			global.APP_LOG.Error("删除Provider任务失败", zap.Error(taskResult.Error))
			return taskResult.Error
		}
		if taskResult.RowsAffected > 0 {
			global.APP_LOG.Debug("成功删除Provider任务",
				zap.Uint("providerID", providerID),
				zap.Int64("count", taskResult.RowsAffected))
		}

		// 3. 硬删除配置任务（包括软删除的）
		configTaskResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&admin.ConfigurationTask{})
		if configTaskResult.Error != nil {
			global.APP_LOG.Error("删除Provider配置任务失败", zap.Error(configTaskResult.Error))
			return configTaskResult.Error
		}
		if configTaskResult.RowsAffected > 0 {
			global.APP_LOG.Debug("成功删除Provider配置任务",
				zap.Uint("providerID", providerID),
				zap.Int64("count", configTaskResult.RowsAffected))
		}

		// 4. 硬删除所有实例记录（包括软删除的）
		instanceResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&providerModel.Instance{})
		if instanceResult.Error != nil {
			global.APP_LOG.Error("删除Provider实例记录失败", zap.Error(instanceResult.Error))
			return instanceResult.Error
		}
		if instanceResult.RowsAffected > 0 {
			global.APP_LOG.Debug("成功删除Provider实例记录",
				zap.Uint("providerID", providerID),
				zap.Int64("count", instanceResult.RowsAffected))
		}

		// 5. 硬删除监控配置（monitoring_configs）
		monConfigResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&monitoring.MonitoringConfig{})
		if monConfigResult.Error != nil {
			global.APP_LOG.Error("删除Provider监控配置失败", zap.Error(monConfigResult.Error))
			return monConfigResult.Error
		}
		if monConfigResult.RowsAffected > 0 {
			global.APP_LOG.Debug("成功删除Provider监控配置",
				zap.Uint("providerID", providerID),
				zap.Int64("count", monConfigResult.RowsAffected))
		}

		// 6. 硬删除Agent监控记录（agent_monitors）
		agentMonResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&monitoring.AgentMonitor{})
		if agentMonResult.Error != nil {
			global.APP_LOG.Error("删除Provider Agent监控记录失败", zap.Error(agentMonResult.Error))
			return agentMonResult.Error
		}
		if agentMonResult.RowsAffected > 0 {
			global.APP_LOG.Debug("成功删除Provider Agent监控记录",
				zap.Uint("providerID", providerID),
				zap.Int64("count", agentMonResult.RowsAffected))
		}

		// 7. 硬删除资源指标（resource_metrics）
		resMetricResult := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&monitoring.ResourceMetric{})
		if resMetricResult.Error != nil {
			global.APP_LOG.Error("删除Provider资源指标失败", zap.Error(resMetricResult.Error))
			return resMetricResult.Error
		}
		if resMetricResult.RowsAffected > 0 {
			global.APP_LOG.Debug("成功删除Provider资源指标",
				zap.Uint("providerID", providerID),
				zap.Int64("count", resMetricResult.RowsAffected))
		}

		// 8. 硬删除硬件测试报告
		tx.Unscoped().Where("provider_id = ?", providerID).Delete(&providerModel.HardwareTestReport{})

		// 9. 硬删除资源预留记录
		tx.Unscoped().Where("provider_id = ?", providerID).Delete(&resourceModel.ResourceReservation{})

		// 10. 硬删除域名绑定和域名配置
		if err := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&domainModel.Domain{}).Error; err != nil {
			global.APP_LOG.Error("删除Provider域名绑定失败", zap.Error(err))
			return err
		}
		if err := tx.Unscoped().Where("provider_id = ?", providerID).Delete(&domainModel.DomainConfig{}).Error; err != nil {
			global.APP_LOG.Error("删除Provider域名配置失败", zap.Error(err))
			return err
		}

		// 11. 硬删除Provider本身
		if err := tx.Unscoped().Delete(&providerModel.Provider{}, providerID).Error; err != nil {
			global.APP_LOG.Error("删除Provider记录失败", zap.Error(err))
			return err
		}

		return nil
	})

	if err != nil {
		global.APP_LOG.Error("Provider删除事务失败", zap.Uint("providerID", providerID), zap.Error(err))
		return err
	}

	// 6. 事务外清理Provider关联的封禁规则应用并重新同步Agent规则
	firewall.CleanupProviderApplications(providerID, instanceIDs)

	// 7. 事务外批量删除流量相关数据（避免长时间锁表）
	s.batchCleanupProviderTrafficData(providerID, instanceIDs)

	// 8. 立即清理所有相关资源（防止内存泄漏）
	s.cleanupAllProviderResources(providerID)

	// 9. 删除本地证书和配置备份文件
	s.cleanupProviderLocalFiles(&existingProvider)

	global.APP_LOG.Info("Provider及所有关联数据删除成功",
		zap.Uint("providerID", providerID),
		zap.Int("instanceCount", len(instanceIDs)))
	return nil
}

// cleanupProviderLocalFiles 删除Provider关联的本地证书和配置备份文件
func (s *Service) cleanupProviderLocalFiles(p *providerModel.Provider) {
	paths := []string{p.CertPath, p.KeyPath, p.CACertPath, p.ConfigBackupPath}
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			global.APP_LOG.Warn("删除Provider本地文件失败",
				zap.Uint("providerID", p.ID),
				zap.String("path", path),
				zap.Error(err))
		} else if err == nil {
			global.APP_LOG.Debug("已删除Provider本地文件",
				zap.Uint("providerID", p.ID),
				zap.String("path", path))
		}
	}
}

// cleanupAllProviderResources 清理Provider的所有相关资源（防止内存泄漏）
// 清理顺序：先断开连接 -> 清理缓存 -> 清理工作池 -> 清理状态 -> 清理Transport
func (s *Service) cleanupAllProviderResources(providerID uint) {
	global.APP_LOG.Info("开始清理Provider的所有内存资源", zap.Uint("providerID", providerID))

	// 1. 先断开Agent及其隧道资源，避免Provider删除后仍有WebSocket/端口转发写入。
	agentService.GetHub().DisconnectProvider(providerID)
	agentService.RemoveTunnelManager(providerID)
	agentService.RemoveClient(providerID)
	global.APP_LOG.Debug("Agent连接和客户端缓存已清理", zap.Uint("providerID", providerID))

	// 2. 先清理SSH连接池（断开SSH连接，避免后续操作使用过期连接）
	if global.APP_SSH_POOL != nil {
		if pool, ok := global.APP_SSH_POOL.(interface {
			Remove(uint)
		}); ok {
			pool.Remove(providerID)
			global.APP_LOG.Debug("SSH连接池已清理", zap.Uint("providerID", providerID))
		}
	}

	// 3. 从 ProviderService 中移除 Provider
	providerService.GetProviderService().RemoveProvider(providerID)
	global.APP_LOG.Debug("Provider已移除", zap.Uint("providerID", providerID))

	// 4. 清理任务工作池及其所有相关的sync.Map（同步清理pools、lastAccess、createdAt）
	if taskService := task.GetTaskService(); taskService != nil {
		taskService.DeleteProviderPool(providerID)
		global.APP_LOG.Debug("任务工作池已清理", zap.Uint("providerID", providerID))
	}

	// 5. 清理监控状态（同步清理providerStateManager和lastResetTime）
	if global.APP_MONITORING_SCHEDULER != nil {
		if scheduler, ok := global.APP_MONITORING_SCHEDULER.(interface {
			DeleteProviderState(uint)
		}); ok {
			scheduler.DeleteProviderState(providerID)
			global.APP_LOG.Debug("监控状态已清理", zap.Uint("providerID", providerID))
		}
	}

	// 6. 清理HTTP Transport（释放连接池资源，同步清理transports和providerMap）
	provider.GetTransportCleanupManager().CleanupProvider(providerID)
	global.APP_LOG.Debug("HTTP Transport已清理", zap.Uint("providerID", providerID))

	global.APP_LOG.Info("所有Provider内存资源清理完成", zap.Uint("providerID", providerID))
}

// batchCleanupProviderTrafficData 批量清理Provider的流量相关数据
func (s *Service) batchCleanupProviderTrafficData(providerID uint, instanceIDs []uint) {
	// 1. TrafficRecord表已删除，跳过流量记录清理
	global.APP_LOG.Debug("跳过流量记录清理（TrafficRecord表已删除）",
		zap.Uint("providerID", providerID))

	// 2. 不删除Provider的pmacct流量记录，保留历史数据用于统计
	// 即使Provider被删除，历史流量数据仍然有价值
	global.APP_LOG.Info("保留Provider pmacct流量历史记录",
		zap.Uint("providerID", providerID))

	// 3. 删除Provider的pmacct监控记录（停止后续采集）
	monitorResult := global.APP_DB.Unscoped().Where("provider_id = ?", providerID).
		Delete(&monitoring.PmacctMonitor{})
	if monitorResult.Error != nil {
		global.APP_LOG.Error("删除Provider pmacct监控记录失败",
			zap.Uint("providerID", providerID),
			zap.Error(monitorResult.Error))
	} else if monitorResult.RowsAffected > 0 {
		global.APP_LOG.Info("成功删除Provider pmacct监控记录",
			zap.Uint("providerID", providerID),
			zap.Int64("count", monitorResult.RowsAffected))
	}

	// 4. 删除Provider的Agent监控记录
	agentResult := global.APP_DB.Unscoped().Where("provider_id = ?", providerID).
		Delete(&monitoring.AgentMonitor{})
	if agentResult.Error != nil {
		global.APP_LOG.Error("删除Provider Agent监控记录失败",
			zap.Uint("providerID", providerID),
			zap.Error(agentResult.Error))
	} else if agentResult.RowsAffected > 0 {
		global.APP_LOG.Info("成功删除Provider Agent监控记录",
			zap.Uint("providerID", providerID),
			zap.Int64("count", agentResult.RowsAffected))
	}

	// 5. 删除Provider的资源指标记录
	metricResult := global.APP_DB.Unscoped().Where("provider_id = ?", providerID).
		Delete(&monitoring.ResourceMetric{})
	if metricResult.Error != nil {
		global.APP_LOG.Error("删除Provider资源指标记录失败",
			zap.Uint("providerID", providerID),
			zap.Error(metricResult.Error))
	} else if metricResult.RowsAffected > 0 {
		global.APP_LOG.Info("成功删除Provider资源指标记录",
			zap.Uint("providerID", providerID),
			zap.Int64("count", metricResult.RowsAffected))
	}
}

// cleanupPmacctDataOptimized 使用预加载的数据清理pmacct
func (s *Service) cleanupPmacctDataOptimized(
	pmacctService *pmacct.Service,
	instance *providerModel.Instance,
	providerInstance provider.Provider,
) error {
	// 调用原有的清理方法，它会处理宿主机清理和数据库清理
	return pmacctService.CleanupPmacctData(instance.ID)
}
