package traffic_monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/pmacct"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// BatchEnableMonitoring 批量启用Provider下所有实例的流量监控
func (m *LifecycleManager) BatchEnableMonitoring(ctx context.Context, providerID uint, taskID uint) error {
	// 更新任务状态为运行中
	now := time.Now()
	if err := m.updateTaskStatus(taskID, "running", 0, "开始批量启用流量监控", &now, nil); err != nil {
		return err
	}

	// 获取Provider信息
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		m.updateTaskStatus(taskID, "failed", 0, "Provider不存在", nil, &now)
		return fmt.Errorf("Provider不存在: %w", err)
	}

	// 获取Provider实例
	_, exists := providerService.GetProviderService().GetProviderByID(providerID)
	if !exists {
		m.updateTaskStatus(taskID, "failed", 0, "Provider未连接", nil, &now)
		return fmt.Errorf("Provider未连接")
	}

	// 查询所有活跃实例（使用精简字段查询，避免加载不必要数据）
	var instances []struct {
		ID         uint
		Name       string
		ProviderID uint
		Status     string
	}
	if err := global.APP_DB.Model(&providerModel.Instance{}).
		Select("id, name, provider_id, status").
		Where("provider_id = ? AND status NOT IN (?)", providerID, []string{"deleted", "deleting"}).
		Find(&instances).Error; err != nil {
		m.updateTaskStatus(taskID, "failed", 0, "查询实例失败", nil, &now)
		return fmt.Errorf("查询实例失败: %w", err)
	}

	totalCount := len(instances)
	if totalCount == 0 {
		completedAt := time.Now()
		m.updateTaskStatus(taskID, "completed", 100, "没有需要启用监控的实例", nil, &completedAt)
		return nil
	}

	// 更新任务总数
	global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
		Update("total_count", totalCount)

	var successCount, failedCount int
	var outputBuilder strings.Builder
	outputBuilder.WriteString(fmt.Sprintf("开始为 %d 个实例启用流量监控\n\n", totalCount))

	pmacctService := pmacct.NewServiceWithContext(ctx)
	pmacctService.SetProviderID(providerID)

	// Pre-fetch all existing monitors for this provider's instances to avoid N+1 queries
	instIDs := make([]uint, len(instances))
	for i, inst := range instances {
		instIDs[i] = inst.ID
	}
	var existingMonitors []monitoringModel.PmacctMonitor
	global.APP_DB.Where("instance_id IN ?", instIDs).Find(&existingMonitors)
	monitorMap := make(map[uint]*monitoringModel.PmacctMonitor, len(existingMonitors))
	for i := range existingMonitors {
		monitorMap[existingMonitors[i].InstanceID] = &existingMonitors[i]
	}

	// 批量处理实例
	for i, inst := range instances {
		progress := (i + 1) * 100 / totalCount
		message := fmt.Sprintf("正在处理实例 %d/%d: %s", i+1, totalCount, inst.Name)
		m.updateTaskProgress(taskID, progress, message)

		outputBuilder.WriteString(fmt.Sprintf("[%d/%d] 实例: %s (ID: %d)\n", i+1, totalCount, inst.Name, inst.ID))

		// 检查是否已存在监控记录（从预加载的map中查找）
		existingMonitor := monitorMap[inst.ID]

		if existingMonitor != nil {
			// 已存在监控记录
			if existingMonitor.IsEnabled {
				outputBuilder.WriteString("  ✓ 监控已存在且已启用，跳过\n\n")
				successCount++
				continue
			} else {
				// 监控已存在但未启用，先删除旧记录（确保完全清理）
				global.APP_LOG.Debug("发现未启用的监控记录，先删除后重新初始化",
					zap.Uint("instanceID", inst.ID),
					zap.Uint("oldMonitorID", existingMonitor.ID))

				if err := global.APP_DB.Unscoped().Delete(existingMonitor).Error; err != nil {
					outputBuilder.WriteString(fmt.Sprintf("  ✗ 删除旧监控记录失败: %v\n\n", err))
					failedCount++
					global.APP_LOG.Warn("删除旧监控记录失败",
						zap.Uint("instanceID", inst.ID),
						zap.String("instanceName", inst.Name),
						zap.Uint("monitorID", existingMonitor.ID),
						zap.Error(err))
					continue
				}
				// 删除后继续执行初始化流程
			}
		}

		// 不存在监控记录或已删除旧记录，初始化新监控
		if err := pmacctService.InitializePmacctForInstance(inst.ID); err != nil {
			outputBuilder.WriteString(fmt.Sprintf("  ✗ 失败: %v\n\n", err))
			failedCount++
			global.APP_LOG.Warn("启用流量监控失败",
				zap.Uint("instanceID", inst.ID),
				zap.String("instanceName", inst.Name),
				zap.Error(err))
		} else {
			outputBuilder.WriteString("  ✓ 成功启用监控\n\n")
			successCount++
		}

		// 更新输出和计数
		global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
			Updates(map[string]interface{}{
				"success_count": successCount,
				"failed_count":  failedCount,
				"output":        outputBuilder.String(),
			})
	}

	// 完成任务
	completedAt := time.Now()
	finalMessage := fmt.Sprintf("批量启用完成: 成功 %d, 失败 %d", successCount, failedCount)
	status := "completed"
	if failedCount > 0 && successCount == 0 {
		status = "failed"
	}

	outputBuilder.WriteString(fmt.Sprintf("\n=== 任务完成 ===\n总计: %d, 成功: %d, 失败: %d\n", totalCount, successCount, failedCount))

	m.updateTaskStatus(taskID, status, 100, finalMessage, nil, &completedAt)
	global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
		Update("output", outputBuilder.String())

	return nil
}
