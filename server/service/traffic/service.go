package traffic

import (
	"errors"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/monitoring"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/system"
	"oneclickvirt/model/user"
	userModel "oneclickvirt/model/user"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// Service 流量管理服务
type Service struct{}

// TrafficLimitType 流量限制类型
type TrafficLimitType string

const (
	UserTrafficLimit     TrafficLimitType = "user"
	ProviderTrafficLimit TrafficLimitType = "provider"
)

// NewService 创建流量服务实例
func NewService() *Service {
	return &Service{}
}

// GetUserTrafficLimitByLevel 根据用户等级获取流量限制
func (s *Service) GetUserTrafficLimitByLevel(level int) int64 {
	// 从配置中获取对应等级的流量限制
	configManager := global.APP_CONFIG.Quota.LevelLimits

	if levelConfig, exists := configManager[level]; exists {
		return levelConfig.MaxTraffic
	}

	// 默认返回100GB
	return 102400
}

// InitUserTrafficQuota 初始化用户流量配额
func (s *Service) InitUserTrafficQuota(userID uint) error {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return err
	}

	// 根据用户等级设置流量配额
	trafficLimit := s.GetUserTrafficLimitByLevel(u.Level)

	// 更新用户流量配额
	now := time.Now()
	resetTime := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())

	return global.APP_DB.Model(&u).Updates(map[string]interface{}{
		"total_traffic":    trafficLimit,
		"used_traffic":     0,
		"traffic_reset_at": resetTime,
		"traffic_limited":  false,
	}).Error
}

// SyncInstanceTraffic 同步实例流量数据
func (s *Service) SyncInstanceTraffic(instanceID uint) error {
	var instance provider.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return err
	}

	// 获取vnstat数据
	trafficData, err := s.getVnstatData(instance)
	if err != nil {
		global.APP_LOG.Warn("获取vnstat数据失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return err
	}

	// 更新实例流量数据
	updates := map[string]interface{}{
		"used_traffic_in":  trafficData.RxMB,
		"used_traffic_out": trafficData.TxMB,
	}

	if err := global.APP_DB.Model(&instance).Updates(updates).Error; err != nil {
		return err
	}

	// 更新流量记录
	return s.updateTrafficRecord(instance.UserID, instance.ProviderID, instanceID, trafficData)
}

// SyncProviderTraffic 同步Provider流量统计
// 从TrafficRecord汇总该Provider下所有实例的当月流量（包含已删除实例）
func (s *Service) SyncProviderTraffic(providerID uint) error {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 统计该Provider当月所有实例的流量使用量
	var totalUsed int64
	// 使用 Unscoped() 包含已软删除的记录，确保累计值准确
	err := global.APP_DB.Model(&userModel.TrafficRecord{}).
		Unscoped(). // ← 关键：包含已删除的记录
		Where("provider_id = ? AND year = ? AND month = ?", providerID, year, month).
		Select("COALESCE(SUM(total_used), 0)").
		Scan(&totalUsed).Error

	if err != nil {
		return err
	}

	global.APP_LOG.Debug("从TrafficRecord同步Provider流量（含已删除实例）",
		zap.Uint("providerID", providerID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Int64("totalUsed", totalUsed))

	// 更新Provider的UsedTraffic字段
	return global.APP_DB.Model(&provider.Provider{}).
		Where("id = ?", providerID).
		Update("used_traffic", totalUsed).Error
}

// CheckProviderTrafficLimit 检查Provider流量限制
func (s *Service) CheckProviderTrafficLimit(providerID uint) (bool, error) {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, err
	}

	// 检查是否需要重置流量
	if err := s.checkAndResetProviderMonthlyTraffic(providerID); err != nil {
		global.APP_LOG.Error("检查Provider月度流量重置失败",
			zap.Uint("providerID", providerID),
			zap.Error(err))
	}

	// 重新加载Provider数据
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, err
	}

	// 检查是否超限（仅在有有效的流量限制时进行检查）
	if p.MaxTraffic > 0 && p.UsedTraffic >= p.MaxTraffic {
		// 超限，标记Provider为受限状态
		if err := global.APP_DB.Model(&p).Update("traffic_limited", true).Error; err != nil {
			return false, err
		}
		return true, nil
	}

	// 未超限，确保Provider不处于受限状态
	if p.TrafficLimited {
		if err := global.APP_DB.Model(&p).Update("traffic_limited", false).Error; err != nil {
			return false, err
		}
	}

	return false, nil
}

// checkAndResetProviderMonthlyTraffic 检查并重置Provider月度流量
func (s *Service) checkAndResetProviderMonthlyTraffic(providerID uint) error {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return err
	}

	now := time.Now()

	// 检查是否到了重置时间
	if p.TrafficResetAt != nil && now.After(*p.TrafficResetAt) {
		// 重置流量
		nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())

		updates := map[string]interface{}{
			"used_traffic":     0,
			"traffic_reset_at": nextReset,
			"traffic_limited":  false,
		}

		if err := global.APP_DB.Model(&p).Updates(updates).Error; err != nil {
			return err
		}

		// 重启Provider上所有因流量受限的实例
		return s.resumeProviderInstances(providerID)
	}

	return nil
}

// resumeProviderInstances 恢复Provider上的受限实例
func (s *Service) resumeProviderInstances(providerID uint) error {
	var instances []provider.Instance
	err := global.APP_DB.Where("provider_id = ? AND traffic_limited = ?", providerID, true).Find(&instances).Error
	if err != nil {
		return err
	}

	for _, instance := range instances {
		// 恢复实例状态
		if err := global.APP_DB.Model(&instance).Updates(map[string]interface{}{
			"traffic_limited": false,
			"status":          "running",
		}).Error; err != nil {
			global.APP_LOG.Error("恢复Provider实例状态失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}

		// 启动实例
		go s.startInstanceAfterTrafficReset(instance.ID)
	}

	return nil
}

// getVnstatData 从Provider获取vnstat数据（聚合所有接口）
func (s *Service) getVnstatData(instance provider.Instance) (*system.VnstatData, error) {
	// 获取当月流量数据（聚合所有接口）
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	monthlyTrafficMB, err := s.getInstanceMonthlyTrafficFromVnStat(instance.ID, year, month)
	if err != nil {
		global.APP_LOG.Warn("获取vnstat月度数据失败，返回零值",
			zap.Uint("instanceID", instance.ID),
			zap.Error(err))
		// 返回零值而不是模拟数据
		return &system.VnstatData{
			Interface: "all", // 聚合所有接口
			RxMB:      0,
			TxMB:      0,
			TotalMB:   0,
		}, nil
	}

	// 获取详细的接收/发送数据（聚合所有接口）
	var records []monitoring.VnStatTrafficRecord
	err = global.APP_DB.Where("instance_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0",
		instance.ID, year, month).Find(&records).Error
	if err != nil {
		global.APP_LOG.Warn("获取vnstat详细记录失败，使用总流量",
			zap.Uint("instanceID", instance.ID),
			zap.Error(err))
		// 如果无法获取详细记录，假设入站和出站各占一半
		rxMB := monthlyTrafficMB / 2
		txMB := monthlyTrafficMB - rxMB
		return &system.VnstatData{
			Interface: "all", // 聚合所有接口
			RxMB:      rxMB,
			TxMB:      txMB,
			TotalMB:   monthlyTrafficMB,
		}, nil
	}

	// 累计所有接口的接收和发送字节数
	var totalRxBytes, totalTxBytes int64
	interfaceCount := make(map[string]bool)
	for _, record := range records {
		totalRxBytes += record.RxBytes
		totalTxBytes += record.TxBytes
		interfaceCount[record.Interface] = true
	}

	// 转换为MB
	rxMB := totalRxBytes / (1024 * 1024)
	txMB := totalTxBytes / (1024 * 1024)

	global.APP_LOG.Info("聚合vnstat流量数据",
		zap.Uint("instanceID", instance.ID),
		zap.Int("interfaces_count", len(interfaceCount)),
		zap.Int64("total_rx_mb", rxMB),
		zap.Int64("total_tx_mb", txMB))

	return &system.VnstatData{
		Interface: "all", // 聚合所有接口
		RxMB:      rxMB,
		TxMB:      txMB,
		TotalMB:   rxMB + txMB,
	}, nil
}

// getInstanceMonthlyTrafficFromVnStat 获取实例月度流量数据
func (s *Service) getInstanceMonthlyTrafficFromVnStat(instanceID uint, year, month int) (int64, error) {
	var totalBytes int64
	err := global.APP_DB.Model(&monitoring.VnStatTrafficRecord{}).
		Where("instance_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0", instanceID, year, month).
		Select("COALESCE(SUM(rx_bytes + tx_bytes), 0)").
		Scan(&totalBytes).Error

	if err != nil {
		return 0, err
	}

	// 转换为MB
	return totalBytes / (1024 * 1024), nil
}

// updateTrafficRecord 更新流量记录 - 按实例维度记录
func (s *Service) updateTrafficRecord(userID, providerID, instanceID uint, data *system.VnstatData) error {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 按实例维度查询流量记录
	var record userModel.TrafficRecord
	err := global.APP_DB.Where(
		"instance_id = ? AND year = ? AND month = ?",
		instanceID, year, month,
	).First(&record).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// 创建新记录，初始化为0，然后累加当前vnStat值作为增量
			record = userModel.TrafficRecord{
				UserID:         userID,
				ProviderID:     providerID,
				InstanceID:     instanceID,
				Year:           year,
				Month:          month,
				TrafficIn:      data.RxMB, // 首次记录，直接使用vnStat当前值
				TrafficOut:     data.TxMB,
				TotalUsed:      data.TotalMB,
				InterfaceName:  data.Interface,
				VnstatVersion:  0,
				LastSyncAt:     &now,
				LastVnstatRxMB: data.RxMB, // 记录基准值
				LastVnstatTxMB: data.TxMB,
			}
			return global.APP_DB.Create(&record).Error
		}
		return err
	}

	// 检测vnStat是否重置：当前值小于上次记录的基准值
	vnstatReset := data.RxMB < record.LastVnstatRxMB || data.TxMB < record.LastVnstatTxMB

	var deltaIn, deltaOut, deltaTotal int64

	if vnstatReset {
		// vnstat已重新初始化，递增版本号，当前值作为新的增量
		deltaIn = data.RxMB
		deltaOut = data.TxMB
		deltaTotal = data.TotalMB

		global.APP_LOG.Info("检测到vnstat重新初始化",
			zap.Uint("instanceID", instanceID),
			zap.String("interface", data.Interface),
			zap.Int("oldVersion", record.VnstatVersion),
			zap.Int("newVersion", record.VnstatVersion+1),
			zap.Int64("lastRxMB", record.LastVnstatRxMB),
			zap.Int64("lastTxMB", record.LastVnstatTxMB),
			zap.Int64("currentRxMB", data.RxMB),
			zap.Int64("currentTxMB", data.TxMB))
	} else {
		// 正常增量计算：当前值 - 上次基准值
		deltaIn = data.RxMB - record.LastVnstatRxMB
		deltaOut = data.TxMB - record.LastVnstatTxMB
		deltaTotal = data.TotalMB - (record.LastVnstatRxMB + record.LastVnstatTxMB)
	}

	// 只有正增量才更新（防止异常数据）
	if deltaIn > 0 || deltaOut > 0 || deltaTotal > 0 {
		updates := map[string]interface{}{
			"traffic_in":        record.TrafficIn + deltaIn,
			"traffic_out":       record.TrafficOut + deltaOut,
			"total_used":        record.TotalUsed + deltaTotal,
			"interface_name":    data.Interface,
			"last_sync_at":      now,
			"last_vnstat_rx_mb": data.RxMB, // 更新基准值
			"last_vnstat_tx_mb": data.TxMB,
		}

		// 如果检测到重置，递增版本号
		if vnstatReset {
			updates["vnstat_version"] = record.VnstatVersion + 1
		}

		if err := global.APP_DB.Model(&record).Updates(updates).Error; err != nil {
			return err
		}

		global.APP_LOG.Debug("实例流量累积更新",
			zap.Uint("userID", userID),
			zap.Uint("instanceID", instanceID),
			zap.Int64("deltaIn", deltaIn),
			zap.Int64("deltaOut", deltaOut),
			zap.Int64("deltaTotal", deltaTotal),
			zap.Bool("vnstatReset", vnstatReset),
			zap.Int64("累计TrafficIn", record.TrafficIn+deltaIn),
			zap.Int64("累计TrafficOut", record.TrafficOut+deltaOut))
	} else {
		global.APP_LOG.Debug("流量增量为零或负数，跳过更新",
			zap.Uint("instanceID", instanceID),
			zap.Int64("deltaIn", deltaIn),
			zap.Int64("deltaOut", deltaOut),
			zap.Int64("deltaTotal", deltaTotal))
	}

	// 同时更新实例表的流量基准值（用于兼容性和快速查询）
	instanceUpdates := map[string]interface{}{
		"used_traffic_in":  data.RxMB,
		"used_traffic_out": data.TxMB,
	}
	if err := global.APP_DB.Model(&provider.Instance{}).Where("id = ?", instanceID).Updates(instanceUpdates).Error; err != nil {
		global.APP_LOG.Warn("更新实例流量基准失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
	}

	return nil
}

// CheckUserTrafficLimit 检查用户流量限制
func (s *Service) CheckUserTrafficLimit(userID uint) (bool, error) {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return false, err
	}

	// 检查是否需要重置流量
	if err := s.checkAndResetMonthlyTraffic(userID); err != nil {
		global.APP_LOG.Error("检查月度流量重置失败",
			zap.Uint("userID", userID),
			zap.Error(err))
	}

	// 重新加载用户数据
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return false, err
	}

	// 计算当月总使用流量
	totalUsed, err := s.getUserMonthlyTrafficUsage(userID)
	if err != nil {
		return false, err
	}

	// 更新用户已使用流量
	if err := global.APP_DB.Model(&u).Update("used_traffic", totalUsed).Error; err != nil {
		return false, err
	}

	// 检查是否超限（仅在有有效的流量限制时进行检查）
	if u.TotalTraffic > 0 && totalUsed >= u.TotalTraffic {
		// 超限，标记用户为受限状态
		if err := global.APP_DB.Model(&u).Update("traffic_limited", true).Error; err != nil {
			return false, err
		}
		return true, nil
	}

	// 未超限，确保用户不处于受限状态
	if u.TrafficLimited {
		if err := global.APP_DB.Model(&u).Update("traffic_limited", false).Error; err != nil {
			return false, err
		}
	}

	return false, nil
}

// getUserMonthlyTrafficUsage 获取用户当月流量使用量
// 从TrafficRecord按实例汇总（包含已删除实例，保证累计值准确）
func (s *Service) getUserMonthlyTrafficUsage(userID uint) (int64, error) {
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	var totalUsed int64
	// 使用 Unscoped() 包含已软删除的记录，确保累计值准确
	err := global.APP_DB.Model(&userModel.TrafficRecord{}).
		Unscoped(). // ← 关键：包含已删除的记录
		Where("user_id = ? AND year = ? AND month = ?", userID, year, month).
		Select("COALESCE(SUM(total_used), 0)").
		Scan(&totalUsed).Error

	if err != nil {
		return 0, err
	}

	global.APP_LOG.Debug("从TrafficRecord获取用户月度流量（含已删除实例）",
		zap.Uint("userID", userID),
		zap.Int("year", year),
		zap.Int("month", month),
		zap.Int64("totalUsed", totalUsed))

	return totalUsed, nil
}

// checkAndResetMonthlyTraffic 检查并重置月度流量
func (s *Service) checkAndResetMonthlyTraffic(userID uint) error {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return err
	}

	now := time.Now()

	// 检查是否到了重置时间
	if u.TrafficResetAt != nil && now.After(*u.TrafficResetAt) {
		// 重置流量
		nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())

		updates := map[string]interface{}{
			"used_traffic":     0,
			"traffic_reset_at": nextReset,
			"traffic_limited":  false,
		}

		if err := global.APP_DB.Model(&u).Updates(updates).Error; err != nil {
			return err
		}

		// 重启用户的所有受限实例
		return s.resumeUserInstances(userID)
	}

	return nil
}

// resumeUserInstances 恢复用户的受限实例
func (s *Service) resumeUserInstances(userID uint) error {
	var instances []provider.Instance
	err := global.APP_DB.Where("user_id = ? AND traffic_limited = ?", userID, true).Find(&instances).Error
	if err != nil {
		return err
	}

	for _, instance := range instances {
		// 恢复实例状态
		if err := global.APP_DB.Model(&instance).Updates(map[string]interface{}{
			"traffic_limited": false,
			"status":          "running",
		}).Error; err != nil {
			global.APP_LOG.Error("恢复实例状态失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}

		// 启动实例
		go s.startInstanceAfterTrafficReset(instance.ID)
	}

	return nil
}

// StopProviderInstancesForTrafficLimit 因Provider流量超限停止所有实例
func (s *Service) StopProviderInstancesForTrafficLimit(providerID uint) error {
	var instances []provider.Instance
	err := global.APP_DB.Where("provider_id = ? AND status = ? AND traffic_limited = ?",
		providerID, "running", false).Find(&instances).Error
	if err != nil {
		return err
	}

	for _, instance := range instances {
		// 标记实例为流量受限
		if err := global.APP_DB.Model(&instance).Updates(map[string]interface{}{
			"traffic_limited": true,
			"status":          "stopped",
		}).Error; err != nil {
			global.APP_LOG.Error("标记Provider实例为流量受限失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}

		// 执行停止操作 - 这里需要调用用户服务
		if err := s.performInstanceAction(instance.UserID, userModel.InstanceActionRequest{
			InstanceID: instance.ID,
			Action:     "stop",
		}); err != nil {
			global.APP_LOG.Error("停止Provider实例失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
		}
	}

	return nil
}

// StopUserInstancesForTrafficLimit 因用户流量超限停止用户的所有实例
func (s *Service) StopUserInstancesForTrafficLimit(userID uint) error {
	var instances []provider.Instance
	err := global.APP_DB.Where("user_id = ? AND status = ? AND traffic_limited = ?",
		userID, "running", false).Find(&instances).Error
	if err != nil {
		return err
	}

	for _, instance := range instances {
		// 标记实例为流量受限
		if err := global.APP_DB.Model(&instance).Updates(map[string]interface{}{
			"traffic_limited": true,
			"status":          "stopped",
		}).Error; err != nil {
			global.APP_LOG.Error("标记用户实例为流量受限失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}

		// 执行停止操作
		if err := s.performInstanceAction(userID, userModel.InstanceActionRequest{
			InstanceID: instance.ID,
			Action:     "stop",
		}); err != nil {
			global.APP_LOG.Error("停止用户实例失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
		}
	}

	return nil
}

// performInstanceAction 执行实例操作（内部方法，避免循环依赖）
func (s *Service) performInstanceAction(userID uint, req userModel.InstanceActionRequest) error {
	// 这里应该调用用户服务的实例操作方法
	// 由于避免循环依赖，这里使用简化实现
	// 在实际使用中，应该通过接口或事件机制来处理
	global.APP_LOG.Info("执行实例操作",
		zap.Uint("userID", userID),
		zap.Uint("instanceID", req.InstanceID),
		zap.String("action", req.Action))
	return nil
}

// startInstanceAfterTrafficReset 流量重置后启动实例
func (s *Service) startInstanceAfterTrafficReset(instanceID uint) {
	err := s.performInstanceAction(0, userModel.InstanceActionRequest{
		InstanceID: instanceID,
		Action:     "start",
	})
	if err != nil {
		global.APP_LOG.Error("流量重置后启动实例失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
	}
}

// GetInstanceTrafficHistory 获取实例流量历史
// 按实例ID查询，因为TrafficRecord现在是按实例维度存储的
func (s *Service) GetInstanceTrafficHistory(instanceID uint) ([]userModel.TrafficRecord, error) {
	var records []userModel.TrafficRecord
	err := global.APP_DB.Where("instance_id = ?", instanceID).
		Order("year DESC, month DESC").
		Find(&records).Error

	return records, err
}

// SyncAllTrafficData 同步所有流量数据（用户级和Provider级）
func (s *Service) SyncAllTrafficData() error {
	global.APP_LOG.Debug("开始同步所有流量数据")

	// 1. 首先同步所有实例的流量数据
	var instances []provider.Instance
	err := global.APP_DB.Where("status IN ?", []string{"running", "stopped"}).Find(&instances).Error
	if err != nil {
		return err
	}

	// 收集需要处理的用户和Provider
	userMap := make(map[uint]bool)
	providerMap := make(map[uint]bool)

	for _, instance := range instances {
		if err := s.SyncInstanceTraffic(instance.ID); err != nil {
			global.APP_LOG.Error("同步实例流量失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}
		userMap[instance.UserID] = true
		providerMap[instance.ProviderID] = true
	}

	// 2. 同步Provider流量统计
	for providerID := range providerMap {
		if err := s.SyncProviderTraffic(providerID); err != nil {
			global.APP_LOG.Error("同步Provider流量失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
		}
	}

	// 3. 检查Provider流量限制并处理超限
	for providerID := range providerMap {
		limited, err := s.CheckProviderTrafficLimit(providerID)
		if err != nil {
			global.APP_LOG.Error("检查Provider流量限制失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
			continue
		}

		if limited {
			if err := s.StopProviderInstancesForTrafficLimit(providerID); err != nil {
				global.APP_LOG.Error("因Provider流量超限停止实例失败",
					zap.Uint("providerID", providerID),
					zap.Error(err))
			}
		}
	}

	// 4. 检查用户流量限制
	for userID := range userMap {
		limited, err := s.CheckUserTrafficLimit(userID)
		if err != nil {
			global.APP_LOG.Error("检查用户流量限制失败",
				zap.Uint("userID", userID),
				zap.Error(err))
			continue
		}

		if limited {
			if err := s.StopUserInstancesForTrafficLimit(userID); err != nil {
				global.APP_LOG.Error("因用户流量超限停止实例失败",
					zap.Uint("userID", userID),
					zap.Error(err))
			}
		}
	}

	global.APP_LOG.Debug("流量数据同步完成")
	return nil
}

// MarkInstanceTrafficDeleted 软删除实例的流量记录（用于实例删除时）
// 使用软删除保留历史数据，确保用户和Provider的累计流量不受影响
// 汇总查询时会自动排除已软删除的记录（如果使用 Unscoped 则包含）
func (s *Service) MarkInstanceTrafficDeleted(instanceID uint) error {
	// 使用软删除，保留流量数据用于历史统计
	// GORM会自动设置 deleted_at 字段，不会真正删除记录
	result := global.APP_DB.Where("instance_id = ?", instanceID).
		Delete(&userModel.TrafficRecord{})

	if result.Error != nil {
		return result.Error
	}

	global.APP_LOG.Info("实例流量记录已软删除（保留累计值）",
		zap.Uint("instanceID", instanceID),
		zap.Int64("affectedRows", result.RowsAffected))

	return nil
}

// ClearInstanceTrafficInterface 清理实例流量接口映射（用于实例重置时）
func (s *Service) ClearInstanceTrafficInterface(instanceID uint) error {
	// 清理实例的vnstat接口映射，重置后会重新检测
	updates := map[string]interface{}{
		"vnstat_interface": "", // 清空接口名，后续会重新检测
	}

	return global.APP_DB.Model(&provider.Instance{}).
		Where("id = ?", instanceID).
		Updates(updates).Error
}

// AutoDetectVnstatInterface 自动检测实例的vnstat接口
func (s *Service) AutoDetectVnstatInterface(instanceID uint) error {
	var instance provider.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return err
	}

	// 获取Provider信息
	var providerInfo provider.Provider
	if err := global.APP_DB.First(&providerInfo, instance.ProviderID).Error; err != nil {
		global.APP_LOG.Error("获取Provider信息失败",
			zap.Uint("instanceId", instanceID),
			zap.Error(err))
		return err
	}

	// 首先检查是否已经有vnStat接口记录
	var vnstatInterface monitoring.VnStatInterface
	err := global.APP_DB.Where("instance_id = ? AND is_enabled = true", instanceID).First(&vnstatInterface).Error
	if err == nil {
		// 已经有接口记录，使用该接口
		detectedInterface := vnstatInterface.Interface
		global.APP_LOG.Info("使用已存在的vnStat接口",
			zap.Uint("instanceId", instanceID),
			zap.String("interface", detectedInterface))

		return global.APP_DB.Model(&instance).Update("vnstat_interface", detectedInterface).Error
	}

	// 没有vnStat记录，根据Provider类型设置默认接口
	var defaultInterface string
	switch providerInfo.Type {
	case "docker":
		// Docker容器在宿主机监控，接口名会在vnstat初始化时确定
		defaultInterface = "veth_auto" // 标记为自动检测的veth接口
		global.APP_LOG.Info("Docker实例使用自动检测的veth接口",
			zap.Uint("instanceId", instanceID),
			zap.String("interface", defaultInterface))
	case "lxd", "incus":
		// LXD/Incus也在宿主机监控veth接口
		defaultInterface = "veth_auto"
		global.APP_LOG.Info("LXD/Incus实例使用自动检测的veth接口",
			zap.Uint("instanceId", instanceID),
			zap.String("interface", defaultInterface))
	case "proxmox":
		// Proxmox虚拟机通常使用ens18或类似接口
		defaultInterface = "ens18"
		global.APP_LOG.Info("Proxmox实例使用默认接口",
			zap.Uint("instanceId", instanceID),
			zap.String("interface", defaultInterface))
	default:
		// 其他类型使用eth0
		defaultInterface = "eth0"
		global.APP_LOG.Info("使用默认网络接口",
			zap.Uint("instanceId", instanceID),
			zap.String("interface", defaultInterface))
	}

	return global.APP_DB.Model(&instance).Update("vnstat_interface", defaultInterface).Error
}

// CleanupOldTrafficRecords 清理旧的流量记录
// 保留当月和上个月的数据，删除2个月前的数据（包括软删除记录）
func (s *Service) CleanupOldTrafficRecords() error {
	now := time.Now()
	cutoffYear := now.Year()
	cutoffMonth := int(now.Month()) - 2

	// 处理跨年情况
	if cutoffMonth <= 0 {
		cutoffMonth += 12
		cutoffYear--
	}

	global.APP_LOG.Info("开始清理旧流量记录",
		zap.Int("截止年份", cutoffYear),
		zap.Int("截止月份", cutoffMonth))

	// 物理删除旧记录（包括软删除的记录）
	// 删除条件：年份小于截止年份，或者年份相等但月份小于截止月份
	result := global.APP_DB.Unscoped().
		Where("year < ? OR (year = ? AND month < ?)",
			cutoffYear, cutoffYear, cutoffMonth).
		Delete(&userModel.TrafficRecord{})

	if result.Error != nil {
		global.APP_LOG.Error("清理旧流量记录失败", zap.Error(result.Error))
		return result.Error
	}

	global.APP_LOG.Info("清理旧流量记录完成",
		zap.Int64("删除记录数", result.RowsAffected),
		zap.Int("保留月份", 2))

	return nil
}
