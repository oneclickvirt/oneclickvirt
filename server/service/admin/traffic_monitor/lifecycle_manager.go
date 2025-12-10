package traffic_monitor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	"oneclickvirt/service/pmacct"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// LifecycleManager 流量监控生命周期管理器
type LifecycleManager struct {
	mu sync.RWMutex
}

var (
	manager     *LifecycleManager
	managerOnce sync.Once
)

// GetManager 获取流量监控生命周期管理器单例
func GetManager() *LifecycleManager {
	managerOnce.Do(func() {
		manager = &LifecycleManager{}
	})
	return manager
}

// AttachMonitor 为单个实例附加流量监控
func (m *LifecycleManager) AttachMonitor(ctx context.Context, instanceID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查实例是否存在
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("实例不存在: %w", err)
	}

	// 检查Provider是否启用流量控制
	var provider providerModel.Provider
	if err := global.APP_DB.First(&provider, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("Provider不存在: %w", err)
	}

	if !provider.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量控制，跳过监控附加",
			zap.Uint("instanceID", instanceID),
			zap.Uint("providerID", provider.ID))
		return nil
	}

	// 检查是否已存在监控记录
	var existingMonitor monitoringModel.PmacctMonitor
	err := global.APP_DB.Where("instance_id = ?", instanceID).First(&existingMonitor).Error
	if err == nil {
		if existingMonitor.IsEnabled {
			global.APP_LOG.Debug("监控已存在且已启用",
				zap.Uint("instanceID", instanceID),
				zap.Uint("monitorID", existingMonitor.ID))
			return nil
		}
	}

	// 使用pmacct服务初始化监控
	pmacctService := pmacct.NewServiceWithContext(ctx)
	pmacctService.SetProviderID(instance.ProviderID)

	if err := pmacctService.InitializePmacctForInstance(instanceID); err != nil {
		return fmt.Errorf("初始化流量监控失败: %w", err)
	}

	global.APP_LOG.Info("成功附加流量监控",
		zap.Uint("instanceID", instanceID),
		zap.String("instanceName", instance.Name))

	return nil
}

// DetachMonitor 为单个实例删除流量监控
func (m *LifecycleManager) DetachMonitor(ctx context.Context, instanceID uint) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查实例是否存在
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		global.APP_LOG.Warn("实例不存在，继续清理监控数据",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
	}

	// 使用pmacct服务清理监控
	pmacctService := pmacct.NewServiceWithContext(ctx)
	if instance.ID > 0 {
		pmacctService.SetProviderID(instance.ProviderID)
	}

	if err := pmacctService.CleanupPmacctData(instanceID); err != nil {
		return fmt.Errorf("清理pmacct监控失败: %w", err)
	}

	global.APP_LOG.Info("成功删除流量监控",
		zap.Uint("instanceID", instanceID))

	return nil
}

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

	// 批量处理实例
	for i, inst := range instances {
		progress := (i + 1) * 100 / totalCount
		message := fmt.Sprintf("正在处理实例 %d/%d: %s", i+1, totalCount, inst.Name)
		m.updateTaskProgress(taskID, progress, message)

		outputBuilder.WriteString(fmt.Sprintf("[%d/%d] 实例: %s (ID: %d)\n", i+1, totalCount, inst.Name, inst.ID))

		// 检查是否已存在监控
		var existingMonitor monitoringModel.PmacctMonitor
		err := global.APP_DB.Where("instance_id = ?", inst.ID).First(&existingMonitor).Error

		if err == nil && existingMonitor.IsEnabled {
			outputBuilder.WriteString("  ✓ 监控已存在且已启用，跳过\n\n")
			successCount++
			continue
		}

		// 初始化监控
		if err := pmacctService.InitializePmacctForInstance(inst.ID); err != nil {
			outputBuilder.WriteString(fmt.Sprintf("  ✗ 失败: %v\n\n", err))
			failedCount++
			global.APP_LOG.Error("启用流量监控失败",
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

	// 批量处理监控记录
	for i, record := range monitorRecords {
		progress := (i + 1) * 100 / totalCount
		message := fmt.Sprintf("正在处理监控记录 %d/%d", i+1, totalCount)
		m.updateTaskProgress(taskID, progress, message)

		// 获取实例名称（可能已删除）
		var instanceName string
		global.APP_DB.Model(&providerModel.Instance{}).
			Unscoped().
			Select("name").
			Where("id = ?", record.InstanceID).
			Scan(&instanceName)
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

			global.APP_LOG.Error("删除流量监控失败",
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

	// 一次性查询所有需要的数据（避免N+1问题）
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
		global.APP_LOG.Error("批量查询监控配置失败", zap.Error(err))
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
		global.APP_LOG.Error("批量查询流量记录失败", zap.Error(err))
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
	outputBuilder.WriteString(fmt.Sprintf("=== 流量监控三层检测 ===\n"))
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
				// 添加说明
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
				// 添加诊断建议
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
			// 添加诊断建议
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

	outputBuilder.WriteString(fmt.Sprintf("\n=== 检测汇总 ===\n"))
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

// batchCheckPmacctProcesses 批量检查多个实例的pmacct服务状态
// 兼容多种初始化系统（systemd/OpenRC/SysVinit），使用临时脚本检查所有服务，执行后自动清理
func (m *LifecycleManager) batchCheckPmacctProcesses(providerInstance provider.Provider, instanceNames []string) map[string]bool {
	resultMap := make(map[string]bool)

	if len(instanceNames) == 0 {
		return resultMap
	}

	// 初始化所有实例为false
	for _, name := range instanceNames {
		resultMap[name] = false
	}

	// 生成唯一的临时脚本名
	scriptPath := fmt.Sprintf("/tmp/check_pmacct_%d.sh", time.Now().UnixNano())

	// 构建检查脚本 - 兼容多种初始化系统
	scriptContent := "#!/bin/bash\n"
	scriptContent += "# 批量检查pmacct服务状态（兼容systemd/OpenRC/SysVinit）\n\n"
	scriptContent += "# 检测初始化系统类型\n"
	scriptContent += "check_service_status() {\n"
	scriptContent += "    local service_name=$1\n"
	scriptContent += "    local instance_name=$2\n"
	scriptContent += "    \n"
	scriptContent += "    # 优先尝试systemd\n"
	scriptContent += "    if command -v systemctl >/dev/null 2>&1; then\n"
	scriptContent += "        if systemctl is-active --quiet \"${service_name}\" 2>/dev/null; then\n"
	scriptContent += "            echo \"${instance_name}:active\"\n"
	scriptContent += "            return 0\n"
	scriptContent += "        fi\n"
	scriptContent += "    fi\n"
	scriptContent += "    \n"
	scriptContent += "    # 尝试OpenRC\n"
	scriptContent += "    if command -v rc-service >/dev/null 2>&1; then\n"
	scriptContent += "        if rc-service \"${service_name}\" status 2>/dev/null | grep -q \"started\\|running\"; then\n"
	scriptContent += "            echo \"${instance_name}:active\"\n"
	scriptContent += "            return 0\n"
	scriptContent += "        fi\n"
	scriptContent += "    fi\n"
	scriptContent += "    \n"
	scriptContent += "    # 尝试传统service命令\n"
	scriptContent += "    if command -v service >/dev/null 2>&1; then\n"
	scriptContent += "        if service \"${service_name}\" status 2>/dev/null | grep -q \"running\\|active\"; then\n"
	scriptContent += "            echo \"${instance_name}:active\"\n"
	scriptContent += "            return 0\n"
	scriptContent += "        fi\n"
	scriptContent += "    fi\n"
	scriptContent += "    \n"
	scriptContent += "    # 最后降级为进程检查\n"
	scriptContent += "    if ps aux 2>/dev/null | grep \"pmacctd\" | grep \"${instance_name}\" | grep -v grep >/dev/null; then\n"
	scriptContent += "        echo \"${instance_name}:active\"\n"
	scriptContent += "        return 0\n"
	scriptContent += "    fi\n"
	scriptContent += "    \n"
	scriptContent += "    echo \"${instance_name}:inactive\"\n"
	scriptContent += "}\n\n"

	// 为每个实例调用检查函数
	for _, name := range instanceNames {
		scriptContent += fmt.Sprintf("check_service_status \"pmacctd-%s\" \"%s\"\n", name, name)
	}
	scriptContent += fmt.Sprintf("\nrm -f %s\n", scriptPath) // 脚本执行完自动删除

	// 上传脚本
	if err := m.uploadScriptViaSFTP(providerInstance, scriptContent, scriptPath); err != nil {
		global.APP_LOG.Error("上传检查脚本失败", zap.Error(err))
		return resultMap
	}

	// 执行脚本（脚本会自动删除自己）
	execCtx, execCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer execCancel()

	output, err := providerInstance.ExecuteSSHCommand(execCtx, fmt.Sprintf("bash %s", scriptPath))
	if err != nil {
		global.APP_LOG.Error("批量检查pmacct服务失败", zap.Error(err))
		// 尝试手动清理脚本（以防自动清理失败）
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		providerInstance.ExecuteSSHCommand(cleanupCtx, fmt.Sprintf("rm -f %s", scriptPath))
		return resultMap
	}

	// 解析输出，格式为: 实例名:状态
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			name := strings.TrimSpace(parts[0])
			status := strings.TrimSpace(parts[1])
			resultMap[name] = (status == "active")
		}
	}

	global.APP_LOG.Debug("批量检查pmacct服务完成",
		zap.Int("totalInstances", len(instanceNames)),
		zap.Int("runningCount", countTrue(resultMap)))

	return resultMap
}

// uploadScriptViaSFTP 通过SFTP上传脚本文件
func (m *LifecycleManager) uploadScriptViaSFTP(providerInstance provider.Provider, content, remotePath string) error {
	// 尝试使用echo写入（更简单，无需SFTP）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 转义特殊字符
	escapedContent := strings.ReplaceAll(content, "'", "'\\''")
	cmd := fmt.Sprintf("echo '%s' > %s && chmod +x %s", escapedContent, remotePath, remotePath)

	_, err := providerInstance.ExecuteSSHCommand(ctx, cmd)
	return err
}

// countTrue 计算map中true值的数量
func countTrue(m map[string]bool) int {
	count := 0
	for _, v := range m {
		if v {
			count++
		}
	}
	return count
}

// updateTaskStatus 更新任务状态
func (m *LifecycleManager) updateTaskStatus(taskID uint, status string, progress int, message string, startedAt, completedAt *time.Time) error {
	updates := map[string]interface{}{
		"status":   status,
		"progress": progress,
		"message":  message,
	}

	if startedAt != nil {
		updates["started_at"] = startedAt
	}
	if completedAt != nil {
		updates["completed_at"] = completedAt
	}

	return global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).
		Where("id = ?", taskID).
		Updates(updates).Error
}

// updateTaskProgress 更新任务进度
func (m *LifecycleManager) updateTaskProgress(taskID uint, progress int, message string) {
	global.APP_DB.Model(&adminModel.TrafficMonitorTask{}).
		Where("id = ?", taskID).
		Updates(map[string]interface{}{
			"progress": progress,
			"message":  message,
		})
}
