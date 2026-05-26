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
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// BatchDetectMonitoring 批量检测Provider下所有实例的流量监控状态
// 检测三个层面：
// 1. 实例层面：pmacct_monitors表中的配置记录
// 2. 数据层面：是否存在历史流量记录
// 3. 服务层面：宿主机上pmacct守护服务是否实际运行
func (m *LifecycleManager) BatchDetectMonitoring(ctx context.Context, providerID uint, taskID uint) error {
	// 更新任务状态为运行中
	now := time.Now()
	if err := m.updateTaskStatus(taskID, "running", 0, "开始批量检测流量监控", &now, nil); err != nil {
		return err
	}

	// 获取Provider信息
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, providerID).Error; err != nil {
		m.updateTaskStatus(taskID, "failed", 0, "Provider不存在", nil, &now)
		return fmt.Errorf("Provider不存在: %w", err)
	}

	// 获取Provider实例
	providerInstance, exists := providerService.GetProviderService().GetProviderByID(providerID)
	if !exists {
		m.updateTaskStatus(taskID, "failed", 0, "Provider未连接", nil, &now)
		return fmt.Errorf("Provider未连接")
	}

	// 一次性查询所有需要的数据
	// 1. 查询所有活跃实例，包含内网IP
	var instances []providerModel.Instance
	if err := global.APP_DB.
		Where("provider_id = ? AND status NOT IN (?)", providerID, []string{"deleted", "deleting"}).
		Select("id, name, provider_id, status, private_ip").
		Find(&instances).Error; err != nil {
		m.updateTaskStatus(taskID, "failed", 0, "查询实例失败", nil, &now)
		return fmt.Errorf("查询实例失败: %w", err)
	}

	totalCount := len(instances)
	if totalCount == 0 {
		completedAt := time.Now()
		m.updateTaskStatus(taskID, "completed", 100, "没有需要检测的实例", nil, &completedAt)
		return nil
	}

	// 提取实例ID列表
	instanceIDs := make([]uint, totalCount)
	for i, inst := range instances {
		instanceIDs[i] = inst.ID
	}

	// 2. 批量查询监控配置（一次查询）
	var monitors []monitoringModel.PmacctMonitor
	monitorMap := make(map[uint]*monitoringModel.PmacctMonitor)
	if err := global.APP_DB.Where("instance_id IN ?", instanceIDs).Find(&monitors).Error; err != nil {
		global.APP_LOG.Warn("批量查询监控配置失败", zap.Error(err))
	} else {
		for i := range monitors {
			monitorMap[monitors[i].InstanceID] = &monitors[i]
		}
	}

	// 3. 批量查询流量记录存在性（一次查询，只检查是否有记录）
	var trafficCounts []struct {
		InstanceID uint
		Count      int64
	}
	trafficExistsMap := make(map[uint]bool)
	if err := global.APP_DB.Model(&monitoringModel.PmacctTrafficRecord{}).
		Select("instance_id, COUNT(*) as count").
		Where("instance_id IN ?", instanceIDs).
		Group("instance_id").
		Find(&trafficCounts).Error; err != nil {
		global.APP_LOG.Warn("批量查询流量记录失败", zap.Error(err))
	} else {
		for _, tc := range trafficCounts {
			trafficExistsMap[tc.InstanceID] = tc.Count > 0
		}
	}

	// 4. 批量检查pmacct进程（一次SSH命令）
	instanceNames := make([]string, totalCount)
	for i, inst := range instances {
		instanceNames[i] = inst.Name
	}
	processStatusMap := m.batchCheckPmacctProcesses(providerInstance, instanceNames)

	// 更新任务总数
	global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
		Update("total_count", totalCount)

	var fullyEnabledCount, partialCount, disabledCount, errorCount int
	var outputBuilder strings.Builder
	outputBuilder.WriteString("=== 流量监控三层检测 ===\n")
	outputBuilder.WriteString(fmt.Sprintf("Provider: %s (ID: %d)\n", provider.Name, provider.ID))
	outputBuilder.WriteString(fmt.Sprintf("实例总数: %d\n\n", totalCount))

	// 批量检测实例（所有数据已准备好，无需额外查询）
	for i, inst := range instances {
		progress := (i + 1) * 100 / totalCount
		message := fmt.Sprintf("正在检测实例 %d/%d: %s", i+1, totalCount, inst.Name)
		m.updateTaskProgress(taskID, progress, message)

		outputBuilder.WriteString(fmt.Sprintf("[%d/%d] 实例: %s (ID: %d)\n", i+1, totalCount, inst.Name, inst.ID))

		// 三层检测
		monitor, hasConfig := monitorMap[inst.ID]
		hasTraffic := trafficExistsMap[inst.ID]
		processRunning := processStatusMap[inst.Name]

		// 层级1：配置检测
		if !hasConfig {
			outputBuilder.WriteString("  [配置层] ✗ 未配置监控\n")
			outputBuilder.WriteString("  [数据层] - 跳过\n")
			outputBuilder.WriteString("  [进程层] - 跳过\n")
			outputBuilder.WriteString("  综合状态: 未启用\n")
			outputBuilder.WriteString("  提示: 通过管理面板启用该实例的流量监控\n\n")
			disabledCount++
			continue
		}

		if !monitor.IsEnabled {
			outputBuilder.WriteString("  [配置层] ⊘ 已禁用\n")
			if inst.PrivateIP != "" {
				outputBuilder.WriteString(fmt.Sprintf("    内网IP: %s\n", inst.PrivateIP))
			}
			outputBuilder.WriteString("  [数据层] - 跳过\n")
			outputBuilder.WriteString("  [进程层] - 跳过\n")
			outputBuilder.WriteString("  综合状态: 已禁用\n")
			outputBuilder.WriteString("  提示: 通过管理面板重新启用该实例的流量监控\n\n")
			disabledCount++
			continue
		}

		// 层级2：数据检测
		outputBuilder.WriteString("  [配置层] ✓ 已配置\n")
		if inst.PrivateIP != "" {
			outputBuilder.WriteString(fmt.Sprintf("    内网IP: %s\n", inst.PrivateIP))
		} else {
			outputBuilder.WriteString("    内网IP: 未设置\n")
		}

		if hasTraffic {
			outputBuilder.WriteString("  [数据层] ✓ 存在流量记录\n")
		} else {
			outputBuilder.WriteString("  [数据层] ⚠ 无流量记录\n")
		}

		// 层级3：服务检测
		if processRunning {
			outputBuilder.WriteString("  [服务层] ✓ pmacct服务运行中\n")
		} else {
			outputBuilder.WriteString("  [服务层] ✗ pmacct服务未运行\n")
		}

		// 综合判断
		if hasTraffic && processRunning {
			outputBuilder.WriteString("  综合状态: 完全启用 ✓\n")
			outputBuilder.WriteString("  检查命令:\n")
			outputBuilder.WriteString(fmt.Sprintf("    检查服务: systemctl status pmacctd-%s || rc-service pmacctd-%s status || service pmacctd-%s status\n", inst.Name, inst.Name, inst.Name))
			outputBuilder.WriteString(fmt.Sprintf("    查看配置: cat /var/lib/pmacct/%s/pmacctd.conf\n", inst.Name))
			outputBuilder.WriteString(fmt.Sprintf("    查看日志: journalctl -u pmacctd-%s -n 20 || tail -n 20 /var/log/messages | grep pmacctd-%s\n", inst.Name, inst.Name))
			outputBuilder.WriteString("\n")
			fullyEnabledCount++
		} else if hasTraffic || processRunning {
			if !hasTraffic {
				outputBuilder.WriteString("  综合状态: 正常（暂无流量记录） ✓\n")
				outputBuilder.WriteString("  说明:\n")
				outputBuilder.WriteString("    服务运行正常，等待流量数据采集\n")
				outputBuilder.WriteString("    新启用的监控需要等待1-5分钟后才会有流量记录\n")
				outputBuilder.WriteString("  检查命令:\n")
				outputBuilder.WriteString(fmt.Sprintf("    检查服务: systemctl status pmacctd-%s || rc-service pmacctd-%s status || service pmacctd-%s status\n", inst.Name, inst.Name, inst.Name))
				outputBuilder.WriteString(fmt.Sprintf("    查看配置: cat /var/lib/pmacct/%s/pmacctd.conf\n", inst.Name))
				outputBuilder.WriteString(fmt.Sprintf("    检查数据: sqlite3 /var/lib/pmacct/%s/traffic.db 'SELECT COUNT(*) FROM acct_v9;'\n", inst.Name))
				outputBuilder.WriteString("\n")
				fullyEnabledCount++
			} else {
				outputBuilder.WriteString("  综合状态: 部分异常 ⚠\n")
				outputBuilder.WriteString("  诊断建议:\n")
				if !processRunning {
					outputBuilder.WriteString(fmt.Sprintf("    检查服务: systemctl status pmacctd-%s || rc-service pmacctd-%s status || service pmacctd-%s status\n", inst.Name, inst.Name, inst.Name))
					outputBuilder.WriteString(fmt.Sprintf("    查看日志: journalctl -u pmacctd-%s -n 50 || tail -n 50 /var/log/messages | grep pmacctd-%s\n", inst.Name, inst.Name))
				}
				outputBuilder.WriteString(fmt.Sprintf("    验证配置: cat /var/lib/pmacct/%s/pmacctd.conf\n", inst.Name))
				outputBuilder.WriteString("    手动采集: 通过管理面板执行流量采集任务\n")
				outputBuilder.WriteString("\n")
				partialCount++
			}
		} else {
			outputBuilder.WriteString("  综合状态: 异常 ✗\n")
			outputBuilder.WriteString("  诊断建议:\n")
			outputBuilder.WriteString(fmt.Sprintf("    1. 检查服务: systemctl status pmacctd-%s || rc-service pmacctd-%s status || service pmacctd-%s status\n", inst.Name, inst.Name, inst.Name))
			outputBuilder.WriteString(fmt.Sprintf("    2. 查看日志: journalctl -u pmacctd-%s -n 50 || tail -n 50 /var/log/messages | grep pmacctd-%s\n", inst.Name, inst.Name))
			outputBuilder.WriteString(fmt.Sprintf("    3. 验证配置: cat /var/lib/pmacct/%s/pmacctd.conf\n", inst.Name))
			outputBuilder.WriteString(fmt.Sprintf("    4. 检查数据库: ls -lh /var/lib/pmacct/%s/traffic.db && sqlite3 /var/lib/pmacct/%s/traffic.db 'SELECT COUNT(*) FROM acct_v9;'\n", inst.Name, inst.Name))
			outputBuilder.WriteString("    5. 重新启用监控: 通过管理面板禁用后重新启用该实例的流量监控\n")
			outputBuilder.WriteString("\n")
			errorCount++
		}

		// 定期更新输出（每10个实例更新一次，减少数据库写入）
		if (i+1)%10 == 0 || i == totalCount-1 {
			global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
				Update("output", outputBuilder.String())
		}
	}

	// 完成任务
	completedAt := time.Now()
	finalMessage := fmt.Sprintf("检测完成: 完全启用 %d, 部分异常 %d, 未启用 %d, 异常 %d",
		fullyEnabledCount, partialCount, disabledCount, errorCount)

	outputBuilder.WriteString("\n=== 检测汇总 ===\n")
	outputBuilder.WriteString(fmt.Sprintf("总计: %d\n", totalCount))
	outputBuilder.WriteString(fmt.Sprintf("正常: %d (服务运行正常，包含暂无流量记录的新启用实例)\n", fullyEnabledCount))
	outputBuilder.WriteString(fmt.Sprintf("部分异常: %d (配置✓ 但数据或服务异常)\n", partialCount))
	outputBuilder.WriteString(fmt.Sprintf("未启用: %d\n", disabledCount))
	outputBuilder.WriteString(fmt.Sprintf("异常: %d\n", errorCount))
	outputBuilder.WriteString("\n提示: 新启用的监控需要等待1-5分钟后才会有流量记录\n")

	m.updateTaskStatus(taskID, "completed", 100, finalMessage, nil, &completedAt)
	global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).Where("id = ?", taskID).
		Updates(map[string]interface{}{
			"output":        outputBuilder.String(),
			"success_count": fullyEnabledCount,
			"failed_count":  partialCount + disabledCount + errorCount,
		})

	return nil
}
