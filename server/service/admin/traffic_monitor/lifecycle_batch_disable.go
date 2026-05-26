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

// BatchDisableMonitoring 批量删除Provider下所有实例的流量监控
func (m *LifecycleManager) BatchDisableMonitoring(ctx context.Context, providerID uint, taskID uint) error {
	// 更新任务状态为运行中
	now := time.Now()
	if err := m.updateTaskStatus(taskID, "running", 0, "开始批量删除流量监控", &now, nil); err != nil {
		return err
	}

	// 获取Provider信息
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		completedAt := time.Now()
		m.updateTaskStatus(taskID, "failed", 0, "Provider不存在", nil, &completedAt)
		return fmt.Errorf("Provider不存在: %w", err)
	}

	// 尝试获取Provider实例（非必需，因为清理可能在Provider离线时进行）
	_, providerExists := providerService.GetProviderService().GetProviderByID(providerID)
	if !providerExists {
		global.APP_LOG.Warn("Provider未连接，将尝试直接SSH连接进行清理",
			zap.Uint("providerID", providerID),
			zap.String("providerName", provider.Name))
	}

	// 查询所有有监控记录的实例（包括已删除的实例）
	var monitorRecords []struct {
		ID         uint
		InstanceID uint
		IsEnabled  bool
	}
	// 使用LEFT JOIN查询，包括已删除的实例
	if err := global.APP_DB.Model(&monitoringModel.PmacctMonitor{}).
		Select("pmacct_monitors.id, pmacct_monitors.instance_id, pmacct_monitors.is_enabled").
		Joins("LEFT JOIN instances ON instances.id = pmacct_monitors.instance_id").
		Where("instances.provider_id = ? OR (instances.id IS NULL AND pmacct_monitors.instance_id IS NOT NULL)", providerID).
		Find(&monitorRecords).Error; err != nil {
		completedAt := time.Now()
		m.updateTaskStatus(taskID, "failed", 0, "查询监控记录失败", nil, &completedAt)
		return fmt.Errorf("查询监控记录失败: %w", err)
	}

	totalCount := len(monitorRecords)
	if totalCount == 0 {
		completedAt := time.Now()
		m.updateTaskStatus(taskID, "completed", 100, "没有需要删除的监控记录", nil, &completedAt)
		return nil
	}

	// 更新任务总数
	global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
		Update("total_count", totalCount)

	var successCount, failedCount int
	var outputBuilder strings.Builder
	outputBuilder.WriteString(fmt.Sprintf("开始删除 %d 个实例的流量监控\n", totalCount))
	outputBuilder.WriteString(fmt.Sprintf("Provider: %s (ID: %d)\n", provider.Name, provider.ID))
	outputBuilder.WriteString(fmt.Sprintf("Provider连接状态: %v\n\n", providerExists))

	pmacctService := pmacct.NewServiceWithContext(ctx)
	pmacctService.SetProviderID(providerID)

	// Pre-fetch all instance names to avoid N+1 queries inside loop
	disableInstIDs := make([]uint, len(monitorRecords))
	for i, record := range monitorRecords {
		disableInstIDs[i] = record.InstanceID
	}
	var instNameRows []struct {
		ID   uint
		Name string
	}
	global.APP_DB.Model(&providerModel.Instance{}).
		Unscoped().
		Select("id, name").
		Where("id IN ?", disableInstIDs).
		Scan(&instNameRows)
	instNameMap := make(map[uint]string, len(instNameRows))
	for _, row := range instNameRows {
		instNameMap[row.ID] = row.Name
	}

	// 批量处理监控记录
	for i, record := range monitorRecords {
		progress := (i + 1) * 100 / totalCount
		message := fmt.Sprintf("正在处理监控记录 %d/%d", i+1, totalCount)
		m.updateTaskProgress(taskID, progress, message)

		// 从预加载的map中获取实例名称
		instanceName := instNameMap[record.InstanceID]
		if instanceName == "" {
			instanceName = fmt.Sprintf("未知实例 (ID: %d)", record.InstanceID)
		}

		outputBuilder.WriteString(fmt.Sprintf("[%d/%d] 实例: %s (ID: %d)\n", i+1, totalCount, instanceName, record.InstanceID))

		// 清理监控，使用更长的超时时间
		cleanupCtx, cleanupCancel := context.WithTimeout(ctx, 2*time.Minute)
		cleanupErr := pmacctService.CleanupPmacctDataWithContext(cleanupCtx, record.InstanceID)
		cleanupCancel()

		if cleanupErr != nil {
			// 检查是否是上下文取消或超时
			if cleanupCtx.Err() == context.Canceled {
				outputBuilder.WriteString("  ✗ 任务已取消\n\n")
			} else if cleanupCtx.Err() == context.DeadlineExceeded {
				outputBuilder.WriteString("  ✗ 执行超时（已超过2分钟）\n\n")
			} else {
				outputBuilder.WriteString(fmt.Sprintf("  ✗ 失败: %v\n\n", cleanupErr))
			}
			failedCount++

			global.APP_LOG.Warn("删除流量监控失败",
				zap.Uint("instanceID", record.InstanceID),
				zap.String("instanceName", instanceName),
				zap.Error(cleanupErr))
		} else {
			outputBuilder.WriteString("  ✓ 成功删除监控\n")
			outputBuilder.WriteString("    - 已停止systemd/OpenRC/SysV服务\n")
			outputBuilder.WriteString("    - 已清理进程和配置文件\n")
			outputBuilder.WriteString("    - 已删除数据库记录\n\n")
			successCount++
		}

		// 每处理 5 个或者每 10% 的进度就更新一次输出
		if (i+1)%5 == 0 || progress%10 == 0 {
			global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
				Updates(map[string]interface{}{
					"success_count": successCount,
					"failed_count":  failedCount,
					"output":        outputBuilder.String(),
				})
		}
	}

	// 完成任务
	completedAt := time.Now()
	finalMessage := fmt.Sprintf("批量删除完成: 成功 %d, 失败 %d", successCount, failedCount)
	status := "completed"
	if failedCount > 0 && successCount == 0 {
		status = "failed"
	}

	outputBuilder.WriteString(fmt.Sprintf("\n=== 任务完成 ===\n总计: %d, 成功: %d, 失败: %d\n", totalCount, successCount, failedCount))
	if failedCount > 0 {
		outputBuilder.WriteString("\n注意: 部分实例清理失败，可能原因：\n")
		outputBuilder.WriteString("- SSH连接失败或超时\n")
		outputBuilder.WriteString("- Provider宿主机不可达\n")
		outputBuilder.WriteString("- 服务已经被手动删除\n")
		outputBuilder.WriteString("- 权限不足\n")
		outputBuilder.WriteString("\n建议: 检查Provider SSH连接后重试\n")
	}

	m.updateTaskStatus(taskID, status, 100, finalMessage, nil, &completedAt)
	global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
		Update("output", outputBuilder.String())

	return nil
}
