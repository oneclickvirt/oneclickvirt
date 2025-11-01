package vnstat

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/provider"
	providerService "oneclickvirt/service/provider"

	"go.uber.org/zap"
)

// Service VnStat服务
type Service struct {
	ctx        context.Context // 服务级别的context，用于传递到所有操作
	providerID uint            // 当前操作的ProviderID，用于获取超时配置
}

// NewService 创建VnStat服务实例
func NewService() *Service {
	return &Service{
		ctx:        context.Background(), // 默认使用Background，但会在需要时传递可取消的context
		providerID: 0,
	}
}

// NewServiceWithContext 使用指定context创建VnStat服务实例
func NewServiceWithContext(ctx context.Context) *Service {
	return &Service{
		ctx:        ctx,
		providerID: 0,
	}
}

// SetProviderID 设置当前操作的ProviderID
func (s *Service) SetProviderID(providerID uint) {
	s.providerID = providerID
}

// InitializeVnStatForInstance 为实例初始化vnStat监控
func (s *Service) InitializeVnStatForInstance(instanceID uint) error {
	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return fmt.Errorf("failed to get instance: %w", err)
	}

	// 获取Provider信息
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, instance.ProviderID).Error; err != nil {
		return fmt.Errorf("failed to get provider: %w", err)
	}

	// 设置当前操作的ProviderID，用于获取超时配置
	s.SetProviderID(providerInfo.ID)

	// 获取Provider实例
	providerInstance, err := provider.GetProvider(providerInfo.Type)
	if err != nil {
		return fmt.Errorf("failed to get provider instance: %w", err)
	}

	// 检查Provider连接
	if !providerInstance.IsConnected() {
		nodeConfig := provider.NodeConfig{
			Name:              providerInfo.Name,
			Host:              providerService.ExtractHostFromEndpoint(providerInfo.Endpoint),
			Port:              providerInfo.SSHPort,
			Username:          providerInfo.Username,
			Password:          providerInfo.Password,
			PrivateKey:        providerInfo.SSHKey,
			Type:              providerInfo.Type,
			NetworkType:       providerInfo.NetworkType,
			SSHConnectTimeout: providerInfo.SSHConnectTimeout,
			SSHExecuteTimeout: providerInfo.SSHExecuteTimeout,
		}

		ctx, cancel := s.getContextWithTimeout(providerInfo.ID, false)
		defer cancel()
		if err := providerInstance.Connect(ctx, nodeConfig); err != nil {
			return fmt.Errorf("failed to connect to provider: %w", err)
		}
	}

	// 获取实例的网络接口列表
	interfaces, err := s.getInstanceNetworkInterfaces(providerInstance, instance.Name)
	if err != nil {
		return fmt.Errorf("failed to get network interfaces: %w", err)
	}

	// 记录第一个成功初始化的接口，用于更新实例表
	var primaryInterface string

	// 为每个接口初始化vnStat和数据库记录
	for _, iface := range interfaces {
		if err := s.initVnStatForInterface(providerInstance, instance.Name, iface); err != nil {
			global.APP_LOG.Error("初始化vnStat接口失败",
				zap.Uint("instance_id", instanceID),
				zap.String("interface", iface),
				zap.Error(err))
			continue
		}

		// 创建接口记录
		vnstatInterface := &monitoringModel.VnStatInterface{
			InstanceID: instanceID,
			ProviderID: instance.ProviderID,
			Interface:  iface,
			IsEnabled:  true,
			LastSync:   time.Now(),
		}

		if err := global.APP_DB.Create(vnstatInterface).Error; err != nil {
			global.APP_LOG.Error("创建vnStat接口记录失败",
				zap.Uint("instance_id", instanceID),
				zap.String("interface", iface),
				zap.Error(err))
		} else {
			// 记录第一个成功创建的接口作为主接口
			if primaryInterface == "" {
				primaryInterface = iface
			}
		}
	}

	// 更新实例表中的vnstat_interface字段
	if primaryInterface != "" {
		if err := global.APP_DB.Model(&instance).Update("vnstat_interface", primaryInterface).Error; err != nil {
			global.APP_LOG.Error("更新实例vnstat接口字段失败",
				zap.Uint("instance_id", instanceID),
				zap.String("interface", primaryInterface),
				zap.Error(err))
		} else {
			global.APP_LOG.Info("更新实例vnstat接口字段成功",
				zap.Uint("instance_id", instanceID),
				zap.String("interface", primaryInterface))
		}
	}

	global.APP_LOG.Info("vnStat初始化完成",
		zap.Uint("instance_id", instanceID),
		zap.String("instance_name", instance.Name),
		zap.Int("interfaces_count", len(interfaces)),
		zap.String("primary_interface", primaryInterface))

	return nil
}

// CollectVnStatData 收集实例的vnStat数据
func (s *Service) CollectVnStatData(ctx context.Context) error {
	// 获取所有启用的vnStat接口
	var interfaces []monitoringModel.VnStatInterface
	err := global.APP_DB.Where("is_enabled = ?", true).Find(&interfaces).Error
	if err != nil {
		return fmt.Errorf("failed to get vnstat interfaces: %w", err)
	}

	if len(interfaces) == 0 {
		global.APP_LOG.Debug("没有启用的vnStat接口")
		return nil
	}

	global.APP_LOG.Info("开始收集vnStat数据", zap.Int("interfaces_count", len(interfaces)))

	// 批量处理接口，避免同时创建太多数据库连接
	batchSize := 5 // 每批处理5个接口
	for i := 0; i < len(interfaces); i += batchSize {
		end := i + batchSize
		if end > len(interfaces) {
			end = len(interfaces)
		}

		// 检查上下文是否已取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 处理当前批次
		for j := i; j < end; j++ {
			iface := interfaces[j]

			// 为每个接口设置超时
			collectCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)

			if err := s.collectInterfaceData(collectCtx, &iface); err != nil {
				global.APP_LOG.Error("收集接口vnStat数据失败",
					zap.Uint("instance_id", iface.InstanceID),
					zap.String("interface", iface.Interface),
					zap.Error(err))
			}

			cancel() // 立即释放资源
		}

		// 批次间增加短暂延迟，让数据库连接有时间释放
		if end < len(interfaces) {
			time.Sleep(3 * time.Second)
		}
	}

	global.APP_LOG.Info("vnStat数据收集完成")
	return nil
}

// GetVnStatSummary 获取实例的vnStat流量汇总（聚合所有接口）
func (s *Service) GetVnStatSummary(instanceID uint, interfaceName string) (*monitoringModel.VnStatSummary, error) {
	// 如果指定了接口，返回单个接口的数据
	if interfaceName != "" {
		return s.getVnStatSummaryForInterface(instanceID, interfaceName)
	}

	// 否则返回所有接口的聚合数据
	return s.getAggregatedVnStatSummary(instanceID)
}

// getVnStatSummaryForInterface 获取单个接口的vnStat流量汇总
func (s *Service) getVnStatSummaryForInterface(instanceID uint, interfaceName string) (*monitoringModel.VnStatSummary, error) {
	summary := &monitoringModel.VnStatSummary{
		InstanceID: instanceID,
		Interface:  interfaceName,
	}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// 获取今日流量
	var todayRecord monitoringModel.VnStatTrafficRecord
	err := global.APP_DB.Where("instance_id = ? AND interface = ? AND year = ? AND month = ? AND day = ? AND hour = 0",
		instanceID, interfaceName, today.Year(), int(today.Month()), today.Day()).First(&todayRecord).Error
	if err == nil {
		summary.Today = &todayRecord
	}

	// 获取本月流量
	var monthRecord monitoringModel.VnStatTrafficRecord
	err = global.APP_DB.Where("instance_id = ? AND interface = ? AND year = ? AND month = ? AND day = 0 AND hour = 0",
		instanceID, interfaceName, today.Year(), int(today.Month())).First(&monthRecord).Error
	if err == nil {
		summary.ThisMonth = &monthRecord
	}

	// 获取总流量
	var totalRecord monitoringModel.VnStatTrafficRecord
	err = global.APP_DB.Where("instance_id = ? AND interface = ? AND year = 0 AND month = 0 AND day = 0 AND hour = 0",
		instanceID, interfaceName).First(&totalRecord).Error
	if err == nil {
		summary.AllTime = &totalRecord
	}

	// 获取最近30天的历史记录
	var history []*monitoringModel.VnStatTrafficRecord
	err = global.APP_DB.Where("instance_id = ? AND interface = ? AND day > 0",
		instanceID, interfaceName).
		Order("year desc, month desc, day desc").
		Limit(30).
		Find(&history).Error
	if err == nil {
		summary.History = history
	}

	return summary, nil
}

// getAggregatedVnStatSummary 获取实例所有接口的聚合vnStat流量汇总
func (s *Service) getAggregatedVnStatSummary(instanceID uint) (*monitoringModel.VnStatSummary, error) {
	summary := &monitoringModel.VnStatSummary{
		InstanceID: instanceID,
		Interface:  "all", // 表示所有接口的聚合
	}

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())

	// 获取今日聚合流量
	todayRecord := s.aggregateTrafficRecords(instanceID, today.Year(), int(today.Month()), today.Day(), 0)
	if todayRecord != nil {
		summary.Today = todayRecord
	}

	// 获取本月聚合流量
	monthRecord := s.aggregateTrafficRecords(instanceID, today.Year(), int(today.Month()), 0, 0)
	if monthRecord != nil {
		summary.ThisMonth = monthRecord
	}

	// 获取总聚合流量
	totalRecord := s.aggregateTrafficRecords(instanceID, 0, 0, 0, 0)
	if totalRecord != nil {
		summary.AllTime = totalRecord
	}

	// 获取最近30天的聚合历史记录
	history := s.getAggregatedHistory(instanceID, 30)
	summary.History = history

	return summary, nil
}

// aggregateTrafficRecords 聚合指定条件的流量记录
func (s *Service) aggregateTrafficRecords(instanceID uint, year, month, day, hour int) *monitoringModel.VnStatTrafficRecord {
	type AggregateResult struct {
		TotalRxBytes int64
		TotalTxBytes int64
	}

	var result AggregateResult
	query := global.APP_DB.Model(&monitoringModel.VnStatTrafficRecord{}).
		Where("instance_id = ?", instanceID)

	// 根据参数添加时间条件
	if year > 0 {
		query = query.Where("year = ?", year)
	}
	if month > 0 {
		query = query.Where("month = ?", month)
	}
	if day > 0 {
		query = query.Where("day = ?", day)
	} else if year > 0 && month > 0 {
		query = query.Where("day = ?", 0) // 月度统计
	}
	if hour > 0 {
		query = query.Where("hour = ?", hour)
	} else if day > 0 {
		query = query.Where("hour = ?", 0) // 日度统计
	}

	err := query.Select("COALESCE(SUM(rx_bytes), 0) as total_rx_bytes, COALESCE(SUM(tx_bytes), 0) as total_tx_bytes").
		Scan(&result).Error

	if err != nil {
		global.APP_LOG.Error("聚合流量记录失败",
			zap.Uint("instanceID", instanceID),
			zap.Int("year", year),
			zap.Int("month", month),
			zap.Int("day", day),
			zap.Int("hour", hour),
			zap.Error(err))
		return nil
	}

	// 如果没有数据，返回nil
	if result.TotalRxBytes == 0 && result.TotalTxBytes == 0 {
		return nil
	}

	// 创建聚合记录
	return &monitoringModel.VnStatTrafficRecord{
		InstanceID: instanceID,
		Interface:  "all", // 聚合接口标识
		RxBytes:    result.TotalRxBytes,
		TxBytes:    result.TotalTxBytes,
		TotalBytes: result.TotalRxBytes + result.TotalTxBytes,
		Year:       year,
		Month:      month,
		Day:        day,
		Hour:       hour,
		RecordTime: time.Now(),
	}
}

// getAggregatedHistory 获取聚合的历史记录
func (s *Service) getAggregatedHistory(instanceID uint, limit int) []*monitoringModel.VnStatTrafficRecord {
	// 获取最近N天的日度聚合数据
	rows, err := global.APP_DB.Raw(`
		SELECT 
			instance_id,
			'all' as interface,
			year,
			month,
			day,
			0 as hour,
			COALESCE(SUM(rx_bytes), 0) as rx_bytes,
			COALESCE(SUM(tx_bytes), 0) as tx_bytes,
			COALESCE(SUM(rx_bytes + tx_bytes), 0) as total_bytes,
			MAX(record_time) as record_time
		FROM vnstat_traffic_records 
		WHERE instance_id = ? AND day > 0 
		GROUP BY instance_id, year, month, day 
		ORDER BY year DESC, month DESC, day DESC 
		LIMIT ?
	`, instanceID, limit).Rows()

	if err != nil {
		global.APP_LOG.Error("获取聚合历史记录失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return []*monitoringModel.VnStatTrafficRecord{}
	}
	defer rows.Close()

	var history []*monitoringModel.VnStatTrafficRecord
	for rows.Next() {
		var record monitoringModel.VnStatTrafficRecord
		err := global.APP_DB.ScanRows(rows, &record)
		if err != nil {
			global.APP_LOG.Error("扫描聚合历史记录失败", zap.Error(err))
			continue
		}
		history = append(history, &record)
	}

	return history
}

// CleanupVnStatData 清理实例的vnStat数据
func (s *Service) CleanupVnStatData(instanceID uint) error {
	// 获取实例信息
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		global.APP_LOG.Warn("获取实例信息失败，跳过vnstat接口删除",
			zap.Uint("instance_id", instanceID),
			zap.Error(err))
	} else {
		// 获取Provider信息
		var providerInfo providerModel.Provider
		if err := global.APP_DB.First(&providerInfo, instance.ProviderID).Error; err != nil {
			global.APP_LOG.Warn("获取Provider信息失败，跳过vnstat接口删除",
				zap.Uint("instance_id", instanceID),
				zap.Uint("provider_id", instance.ProviderID),
				zap.Error(err))
		} else {
			// 获取要删除的接口列表
			var interfaces []monitoringModel.VnStatInterface
			if err := global.APP_DB.Where("instance_id = ?", instanceID).Find(&interfaces).Error; err != nil {
				global.APP_LOG.Warn("获取vnstat接口列表失败",
					zap.Uint("instance_id", instanceID),
					zap.Error(err))
			} else if len(interfaces) > 0 {
				// 获取Provider实例
				providerInstance, err := provider.GetProvider(providerInfo.Type)
				if err != nil {
					global.APP_LOG.Warn("获取Provider实例失败，跳过vnstat接口删除",
						zap.Uint("instance_id", instanceID),
						zap.String("provider_type", providerInfo.Type),
						zap.Error(err))
				} else {
					// 检查Provider连接
					if !providerInstance.IsConnected() {
						nodeConfig := provider.NodeConfig{
							Name:        providerInfo.Name,
							Host:        providerService.ExtractHostFromEndpoint(providerInfo.Endpoint),
							Port:        providerInfo.SSHPort,
							Username:    providerInfo.Username,
							Password:    providerInfo.Password,
							PrivateKey:  providerInfo.SSHKey,
							Type:        providerInfo.Type,
							NetworkType: providerInfo.NetworkType,
						}

						if err := providerInstance.Connect(context.Background(), nodeConfig); err != nil {
							global.APP_LOG.Warn("连接Provider失败，跳过vnstat接口删除",
								zap.Uint("instance_id", instanceID),
								zap.String("provider_name", providerInfo.Name),
								zap.Error(err))
						}
					}

					// 如果连接成功，删除每个接口
					if providerInstance.IsConnected() {
						for _, iface := range interfaces {
							if err := s.removeVnStatInterface(providerInstance, iface.Interface); err != nil {
								global.APP_LOG.Warn("删除vnstat接口失败",
									zap.Uint("instance_id", instanceID),
									zap.String("interface", iface.Interface),
									zap.Error(err))
								// 继续删除其他接口，不因单个接口失败而中断
							}
						}
					}
				}
			}
		}
	}

	// 删除接口记录
	if err := global.APP_DB.Where("instance_id = ?", instanceID).Delete(&monitoringModel.VnStatInterface{}).Error; err != nil {
		return fmt.Errorf("failed to delete vnstat interfaces: %w", err)
	}

	// 删除流量记录
	result := global.APP_DB.Where("instance_id = ?", instanceID).Delete(&monitoringModel.VnStatTrafficRecord{})
	if result.Error != nil {
		return fmt.Errorf("failed to delete vnstat traffic records: %w", result.Error)
	}

	// 清空实例表中的vnstat_interface字段
	if err := global.APP_DB.Model(&providerModel.Instance{}).Where("id = ?", instanceID).Update("vnstat_interface", "").Error; err != nil {
		global.APP_LOG.Warn("清空实例vnstat接口字段失败",
			zap.Uint("instance_id", instanceID),
			zap.Error(err))
		// 不返回错误，继续执行
	}

	global.APP_LOG.Info("vnStat数据清理完成",
		zap.Uint("instance_id", instanceID),
		zap.Int64("deleted_records", result.RowsAffected))

	return nil
}

// CleanupOldVnStatData 清理过期的vnStat数据
func (s *Service) CleanupOldVnStatData(retentionDays int) error {
	if retentionDays <= 0 {
		retentionDays = 90 // 默认保留90天
	}

	cutoffTime := time.Now().AddDate(0, 0, -retentionDays)

	// 只清理日度记录（day > 0），保留月度（day = 0, month > 0）和总计（year = 0）记录
	result := global.APP_DB.Where("record_time < ? AND day > 0", cutoffTime).Delete(&monitoringModel.VnStatTrafficRecord{})
	if result.Error != nil {
		return fmt.Errorf("failed to cleanup old vnstat data: %w", result.Error)
	}

	global.APP_LOG.Info("清理过期vnStat数据",
		zap.Int("retention_days", retentionDays),
		zap.Time("cutoff_time", cutoffTime),
		zap.Int64("deleted_records", result.RowsAffected))

	// 额外：清理过期的月度记录（保留最近12个月的月度数据）
	cutoffMonth := time.Now().AddDate(0, -12, 0)
	monthResult := global.APP_DB.Where("record_time < ? AND day = 0 AND month > 0 AND year > 0", cutoffMonth).
		Delete(&monitoringModel.VnStatTrafficRecord{})
	if monthResult.Error != nil {
		global.APP_LOG.Error("清理过期月度数据失败", zap.Error(monthResult.Error))
	} else if monthResult.RowsAffected > 0 {
		global.APP_LOG.Info("清理过期月度数据",
			zap.Int64("deleted_records", monthResult.RowsAffected))
	}

	return nil
}

// GetVnStatSummaryByInstanceID 获取实例vnStat摘要
func (s *Service) GetVnStatSummaryByInstanceID(instanceID uint, interfaceName string) (interface{}, error) {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// 如果没有指定接口，获取默认接口
	if interfaceName == "" {
		var vnstatInterface monitoringModel.VnStatInterface
		if err := global.APP_DB.Where("instance_id = ? AND is_enabled = true", instanceID).First(&vnstatInterface).Error; err != nil {
			return nil, fmt.Errorf("no vnstat interface found for instance: %d", instanceID)
		}
		interfaceName = vnstatInterface.Interface
	}

	// 获取Provider信息和连接
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}

	providerInstance, err := provider.GetProvider(providerInfo.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider instance: %w", err)
	}

	if !providerInstance.IsConnected() {
		nodeConfig := provider.NodeConfig{
			Name:        providerInfo.Name,
			Host:        providerService.ExtractHostFromEndpoint(providerInfo.Endpoint),
			Port:        providerInfo.SSHPort,
			Username:    providerInfo.Username,
			Password:    providerInfo.Password,
			PrivateKey:  providerInfo.SSHKey,
			Type:        providerInfo.Type,
			NetworkType: providerInfo.NetworkType,
		}

		if err := providerInstance.Connect(context.Background(), nodeConfig); err != nil {
			return nil, fmt.Errorf("failed to connect to provider: %w", err)
		}
	}

	// 执行vnstat命令获取摘要信息
	// 限制查询范围：最近30天的数据，减少传输量
	cmd := fmt.Sprintf("vnstat -i %s -d 30 --json", interfaceName)
	output, err := providerInstance.ExecuteSSHCommand(context.Background(), cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to get vnstat summary: %w", err)
	}

	return map[string]interface{}{
		"instanceID":    instanceID,
		"interfaceName": interfaceName,
		"data":          output,
	}, nil
}

// QueryVnStatData 查询vnStat数据
func (s *Service) QueryVnStatData(instanceID uint, interfaceName, dateRange string) (interface{}, error) {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// 如果没有指定接口，获取默认接口
	if interfaceName == "" {
		var vnstatInterface monitoringModel.VnStatInterface
		if err := global.APP_DB.Where("instance_id = ? AND is_enabled = true", instanceID).First(&vnstatInterface).Error; err != nil {
			return nil, fmt.Errorf("no vnstat interface found for instance: %d", instanceID)
		}
		interfaceName = vnstatInterface.Interface
	}

	// 获取Provider信息和连接
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}

	providerInstance, err := provider.GetProvider(providerInfo.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider instance: %w", err)
	}

	if !providerInstance.IsConnected() {
		nodeConfig := provider.NodeConfig{
			Name:        providerInfo.Name,
			Host:        providerService.ExtractHostFromEndpoint(providerInfo.Endpoint),
			Port:        providerInfo.SSHPort,
			Username:    providerInfo.Username,
			Password:    providerInfo.Password,
			PrivateKey:  providerInfo.SSHKey,
			Type:        providerInfo.Type,
			NetworkType: providerInfo.NetworkType,
		}

		if err := providerInstance.Connect(context.Background(), nodeConfig); err != nil {
			return nil, fmt.Errorf("failed to connect to provider: %w", err)
		}
	}

	// 根据日期范围构建vnstat命令，限制返回数据量
	var cmd string
	switch dateRange {
	case "hourly":
		// 只返回最近24小时的数据
		cmd = fmt.Sprintf("vnstat -i %s -h 24 --json", interfaceName)
	case "daily":
		// 只返回最近30天的数据
		cmd = fmt.Sprintf("vnstat -i %s -d 30 --json", interfaceName)
	case "monthly":
		// 只返回最近12个月的数据
		cmd = fmt.Sprintf("vnstat -i %s -m 12 --json", interfaceName)
	default:
		// 默认返回最近30天的数据（包含月度统计）
		cmd = fmt.Sprintf("vnstat -i %s -d 30 --json", interfaceName)
	}

	output, err := providerInstance.ExecuteSSHCommand(context.Background(), cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to query vnstat data: %w", err)
	}

	return map[string]interface{}{
		"instanceID":    instanceID,
		"interfaceName": interfaceName,
		"dateRange":     dateRange,
		"data":          output,
	}, nil
}

// GetVnStatInterfaces 获取vnStat监控的接口列表
func (s *Service) GetVnStatInterfaces(instanceID uint) ([]string, error) {
	var interfaces []monitoringModel.VnStatInterface
	if err := global.APP_DB.Where("instance_id = ? AND is_enabled = true", instanceID).Find(&interfaces).Error; err != nil {
		return nil, fmt.Errorf("failed to get vnstat interfaces: %w", err)
	}

	var result []string
	for _, iface := range interfaces {
		result = append(result, iface.Interface)
	}

	return result, nil
}

// GetVnStatDashboardData 获取vnStat仪表板数据
func (s *Service) GetVnStatDashboardData(instanceID uint) (interface{}, error) {
	var instance providerModel.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return nil, fmt.Errorf("failed to get instance: %w", err)
	}

	// 获取所有启用的接口
	interfaces, err := s.GetVnStatInterfaces(instanceID)
	if err != nil {
		return nil, err
	}

	if len(interfaces) == 0 {
		return map[string]interface{}{
			"instanceID": instanceID,
			"interfaces": []string{},
			"message":    "No vnstat interfaces found",
		}, nil
	}

	// 获取Provider信息和连接
	var providerInfo providerModel.Provider
	if err := global.APP_DB.First(&providerInfo, instance.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("failed to get provider: %w", err)
	}

	providerInstance, err := provider.GetProvider(providerInfo.Type)
	if err != nil {
		return nil, fmt.Errorf("failed to get provider instance: %w", err)
	}

	if !providerInstance.IsConnected() {
		nodeConfig := provider.NodeConfig{
			Name:        providerInfo.Name,
			Host:        providerService.ExtractHostFromEndpoint(providerInfo.Endpoint),
			Port:        providerInfo.SSHPort,
			Username:    providerInfo.Username,
			Password:    providerInfo.Password,
			PrivateKey:  providerInfo.SSHKey,
			Type:        providerInfo.Type,
			NetworkType: providerInfo.NetworkType,
		}

		if err := providerInstance.Connect(context.Background(), nodeConfig); err != nil {
			return nil, fmt.Errorf("failed to connect to provider: %w", err)
		}
	}

	// 获取所有接口的总体统计
	dashboardData := make(map[string]interface{})
	dashboardData["instanceID"] = instanceID
	dashboardData["interfaces"] = interfaces

	for _, iface := range interfaces {
		// 限制查询范围：最近30天的数据，减少传输量
		cmd := fmt.Sprintf("vnstat -i %s -d 30 --json", iface)
		output, err := providerInstance.ExecuteSSHCommand(context.Background(), cmd)
		if err != nil {
			global.APP_LOG.Warn("获取接口vnstat数据失败",
				zap.Uint("instance_id", instanceID),
				zap.String("interface", iface),
				zap.Error(err))
			continue
		}
		dashboardData[iface] = output
	}

	return dashboardData, nil
}
