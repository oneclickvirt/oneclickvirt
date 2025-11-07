package traffic

import (
	"errors"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
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
	// 检查Provider是否启用了流量控制
	var p provider.Provider
	if err := global.APP_DB.Select("enable_traffic_control").First(&p, providerID).Error; err != nil {
		return fmt.Errorf("查询Provider失败: %w", err)
	}

	// 如果未启用流量控制，跳过同步
	if !p.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量控制，跳过流量同步",
			zap.Uint("providerID", providerID))
		return nil
	}

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

	// 如果未启用流量控制，跳过检查
	if !p.EnableTrafficControl {
		global.APP_LOG.Debug("Provider未启用流量控制，跳过流量限制检查",
			zap.Uint("providerID", providerID))
		return false, nil
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

	// 初始化TrafficResetAt（新Provider或数据迁移）
	if p.TrafficResetAt == nil {
		nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
		p.TrafficResetAt = &nextReset
		if err := global.APP_DB.Model(&p).Update("traffic_reset_at", nextReset).Error; err != nil {
			global.APP_LOG.Error("初始化Provider流量重置时间失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
		}
		return nil // 本月不重置，等下个月
	}

	// 检查是否到了重置时间（使用 >= 判断，确保整点00:00:00立即触发）
	if !now.Before(*p.TrafficResetAt) {
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
// 使用乐观锁防止并发场景下重复处理同一实例
func (s *Service) resumeProviderInstances(providerID uint) error {
	var instances []provider.Instance
	err := global.APP_DB.Where("provider_id = ? AND traffic_limited = ?", providerID, true).Find(&instances).Error
	if err != nil {
		return err
	}

	global.APP_LOG.Info("开始恢复Provider受限实例",
		zap.Uint("providerID", providerID),
		zap.Int("实例数量", len(instances)))

	successCount := 0
	for _, instance := range instances {
		// 使用乐观锁：只更新traffic_limited=true的实例
		// 如果并发任务已处理，RowsAffected会是0
		result := global.APP_DB.Model(&provider.Instance{}).
			Where("id = ? AND traffic_limited = ?", instance.ID, true).
			Updates(map[string]interface{}{
				"traffic_limited": false,
				"status":          "running",
			})

		if result.Error != nil {
			global.APP_LOG.Error("恢复Provider实例状态失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(result.Error))
			continue
		}

		if result.RowsAffected == 0 {
			// 已被其他任务处理，跳过
			global.APP_LOG.Debug("实例已被其他任务恢复，跳过",
				zap.Uint("instanceID", instance.ID))
			continue
		}

		// 成功更新状态，创建启动任务
		if err := s.createStartTaskForInstance(instance.ID, instance.UserID, instance.ProviderID); err != nil {
			global.APP_LOG.Error("创建实例启动任务失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			// 回滚状态更新
			global.APP_DB.Model(&provider.Instance{}).
				Where("id = ?", instance.ID).
				Updates(map[string]interface{}{
					"traffic_limited": true,
					"status":          "stopped",
				})
			continue
		}

		successCount++
		global.APP_LOG.Info("已创建实例启动任务",
			zap.Uint("instanceID", instance.ID),
			zap.String("instanceName", instance.Name))
	}

	global.APP_LOG.Info("Provider实例恢复完成",
		zap.Uint("providerID", providerID),
		zap.Int("成功数量", successCount),
		zap.Int("总数量", len(instances)))

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
		// 严重错误：无法获取vnstat数据
		// 使用上次记录的基准值，保持流量不变（不增不减）
		global.APP_LOG.Error("获取vnstat月度数据失败，使用上次基准值避免流量统计中断",
			zap.Uint("instanceID", instance.ID),
			zap.Int64("lastRxMB", instance.UsedTrafficIn),
			zap.Int64("lastTxMB", instance.UsedTrafficOut),
			zap.Error(err),
			zap.String("影响", "本次不更新流量，等待下次同步"))

		// 返回上次的基准值（从Instance表获取）
		// 这样updateTrafficRecord会计算出0增量，不会更新
		return &system.VnstatData{
			Interface: instance.VnstatInterface,
			RxMB:      instance.UsedTrafficIn,
			TxMB:      instance.UsedTrafficOut,
			TotalMB:   instance.UsedTrafficIn + instance.UsedTrafficOut,
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

// getInstanceMonthlyTrafficFromVnStat 获取实例月度流量数据（MB）
// 注意：此函数直接统计双向流量，不考虑 Provider 的流量模式
// 流量模式的处理应在上层调用时通过 SQL 完成
// 此函数会聚合实例的所有接口流量，避免重复统计
func (s *Service) getInstanceMonthlyTrafficFromVnStat(instanceID uint, year, month int) (int64, error) {
	var totalBytes int64

	// 使用子查询聚合所有接口的流量，避免重复统计
	err := global.APP_DB.Raw(`
		SELECT COALESCE(SUM(rx_bytes + tx_bytes), 0)
		FROM (
			SELECT SUM(rx_bytes) as rx_bytes, SUM(tx_bytes) as tx_bytes
			FROM vnstat_traffic_records
			WHERE instance_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0
			GROUP BY instance_id
		) agg
	`, instanceID, year, month).Scan(&totalBytes).Error

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
			// 查询上个月的记录，获取基准值
			var lastMonthRecord userModel.TrafficRecord
			lastMonth := now.AddDate(0, -1, 0)
			lastYear := lastMonth.Year()
			lastMonthNum := int(lastMonth.Month())

			lastMonthErr := global.APP_DB.Where(
				"instance_id = ? AND year = ? AND month = ?",
				instanceID, lastYear, lastMonthNum,
			).First(&lastMonthRecord).Error

			var baseRxMB, baseTxMB int64 = 0, 0
			var initialTrafficIn, initialTrafficOut, initialTotalUsed int64 = 0, 0, 0

			if lastMonthErr == nil {
				// 找到上个月的记录，使用上个月的LastVnstat值作为本月基准
				baseRxMB = lastMonthRecord.LastVnstatRxMB
				baseTxMB = lastMonthRecord.LastVnstatTxMB

				// 计算本月已产生的增量
				if data.RxMB >= baseRxMB && data.TxMB >= baseTxMB {
					initialTrafficIn = data.RxMB - baseRxMB
					initialTrafficOut = data.TxMB - baseTxMB
					initialTotalUsed = initialTrafficIn + initialTrafficOut // 使用 RxMB + TxMB 统一计算
				} else {
					// vnStat重置了，当前值就是本月增量
					initialTrafficIn = data.RxMB
					initialTrafficOut = data.TxMB
					initialTotalUsed = data.RxMB + data.TxMB // 使用 RxMB + TxMB 统一计算
					baseRxMB = 0
					baseTxMB = 0
				}

				global.APP_LOG.Info("月度切换，从上月基准计算增量",
					zap.Uint("instanceID", instanceID),
					zap.Int("上月年份", lastYear),
					zap.Int("上月月份", lastMonthNum),
					zap.Int64("上月基准RxMB", baseRxMB),
					zap.Int64("上月基准TxMB", baseTxMB),
					zap.Int64("当前vnStat RxMB", data.RxMB),
					zap.Int64("当前vnStat TxMB", data.TxMB),
					zap.Int64("本月增量In", initialTrafficIn),
					zap.Int64("本月增量Out", initialTrafficOut),
					zap.Int64("本月TotalUsed", initialTotalUsed))
			} else {
				// 没有上个月的记录（新实例），直接使用当前vnStat值
				initialTrafficIn = data.RxMB
				initialTrafficOut = data.TxMB
				initialTotalUsed = data.RxMB + data.TxMB // 使用 RxMB + TxMB 统一计算

				global.APP_LOG.Info("新实例或首次记录，使用vnStat当前值",
					zap.Uint("instanceID", instanceID),
					zap.Int64("初始RxMB", initialTrafficIn),
					zap.Int64("初始TxMB", initialTrafficOut),
					zap.Int64("初始TotalUsed", initialTotalUsed))
			}

			// 创建新记录
			record = userModel.TrafficRecord{
				UserID:         userID,
				ProviderID:     providerID,
				InstanceID:     instanceID,
				Year:           year,
				Month:          month,
				TrafficIn:      initialTrafficIn,  // 使用计算后的增量
				TrafficOut:     initialTrafficOut, // 使用计算后的增量
				TotalUsed:      initialTotalUsed,  // 使用计算后的增量
				InterfaceName:  data.Interface,
				VnstatVersion:  0,
				LastSyncAt:     &now,
				LastVnstatRxMB: data.RxMB, // 记录当前vnStat值作为基准
				LastVnstatTxMB: data.TxMB,
			}
			return global.APP_DB.Create(&record).Error
		}
		return err
	}

	// 检测vnStat是否重置：更严格的重置检测
	// 两者都下降，且下降幅度超过10%才判定为重置（即当前值小于上次的90%）
	// 同时要求基准值足够大（大于10MB），避免误判小流量波动
	const resetThreshold = 0.9     // 保留90%，下降超过10%才算重置
	const minBaselineForReset = 10 // 基准值至少10MB才检测重置

	rxDecreased := record.LastVnstatRxMB > minBaselineForReset && float64(data.RxMB) < float64(record.LastVnstatRxMB)*resetThreshold
	txDecreased := record.LastVnstatTxMB > minBaselineForReset && float64(data.TxMB) < float64(record.LastVnstatTxMB)*resetThreshold
	vnstatReset := rxDecreased && txDecreased // 两者都下降才判定为重置

	var deltaIn, deltaOut, deltaTotal int64

	if vnstatReset {
		// vnstat已重新初始化，递增版本号，当前值作为新的增量
		deltaIn = data.RxMB
		deltaOut = data.TxMB
		deltaTotal = data.RxMB + data.TxMB // 使用 RxMB + TxMB 统一计算

		global.APP_LOG.Warn("检测到vnstat重新初始化（严格模式：两者都下降>10%）",
			zap.Uint("instanceID", instanceID),
			zap.String("interface", data.Interface),
			zap.Int("oldVersion", record.VnstatVersion),
			zap.Int("newVersion", record.VnstatVersion+1),
			zap.Int64("lastRxMB", record.LastVnstatRxMB),
			zap.Int64("lastTxMB", record.LastVnstatTxMB),
			zap.Int64("currentRxMB", data.RxMB),
			zap.Int64("currentTxMB", data.TxMB),
			zap.Float64("rxDecreaseRatio", float64(data.RxMB)/float64(record.LastVnstatRxMB)),
			zap.Float64("txDecreaseRatio", float64(data.TxMB)/float64(record.LastVnstatTxMB)))
	} else {
		// 正常增量计算：当前值 - 上次基准值
		deltaIn = data.RxMB - record.LastVnstatRxMB
		deltaOut = data.TxMB - record.LastVnstatTxMB
		deltaTotal = deltaIn + deltaOut // 使用增量和统一计算，避免依赖 data.TotalMB
	}

	// 只有正增量才更新（防止异常数据）
	if deltaIn > 0 || deltaOut > 0 || deltaTotal > 0 {
		// 使用原子更新避免并发竞态条件
		// 直接在SQL中累加，而不是先读取再更新
		err := global.APP_DB.Exec(`
			UPDATE traffic_records 
			SET traffic_in = traffic_in + ?,
				traffic_out = traffic_out + ?,
				total_used = total_used + ?,
				interface_name = ?,
				last_sync_at = ?,
				last_vnstat_rx_mb = ?,
				last_vnstat_tx_mb = ?,
				vnstat_version = CASE 
					WHEN ? THEN vnstat_version + 1 
					ELSE vnstat_version 
				END
			WHERE id = ?
		`, deltaIn, deltaOut, deltaTotal, data.Interface, now, data.RxMB, data.TxMB, vnstatReset, record.ID).Error

		if err != nil {
			return fmt.Errorf("原子更新流量记录失败: %w", err)
		}

		global.APP_LOG.Debug("实例流量累积更新（原子操作）",
			zap.Uint("userID", userID),
			zap.Uint("instanceID", instanceID),
			zap.Int64("deltaIn", deltaIn),
			zap.Int64("deltaOut", deltaOut),
			zap.Int64("deltaTotal", deltaTotal),
			zap.Bool("vnstatReset", vnstatReset),
			zap.Int64("新TrafficIn", record.TrafficIn+deltaIn),
			zap.Int64("新TrafficOut", record.TrafficOut+deltaOut))
	} else {
		// 负增量或零增量的警告
		if deltaIn < 0 || deltaOut < 0 {
			global.APP_LOG.Warn("检测到负流量增量，可能是vnStat异常或时钟回退，重置基准值",
				zap.Uint("instanceID", instanceID),
				zap.Int64("deltaIn", deltaIn),
				zap.Int64("deltaOut", deltaOut),
				zap.Int64("deltaTotal", deltaTotal),
				zap.Int64("currentRx", data.RxMB),
				zap.Int64("lastRx", record.LastVnstatRxMB),
				zap.Int64("currentTx", data.TxMB),
				zap.Int64("lastTx", record.LastVnstatTxMB),
				zap.String("可能原因", "vnStat数据库损坏/NTP时钟回退/网卡流量计数器异常"))

			// 重置vnStat基准值，避免数据不一致
			err := global.APP_DB.Model(&userModel.TrafficRecord{}).
				Where("id = ?", record.ID).
				Updates(map[string]interface{}{
					"last_vnstat_rx_mb": data.RxMB,
					"last_vnstat_tx_mb": data.TxMB,
					"last_sync_at":      now,
				}).Error
			if err != nil {
				global.APP_LOG.Error("重置vnStat基准值失败",
					zap.Uint("instanceID", instanceID),
					zap.Error(err))
			} else {
				global.APP_LOG.Info("已重置vnStat基准值",
					zap.Uint("instanceID", instanceID),
					zap.Int64("newRxMB", data.RxMB),
					zap.Int64("newTxMB", data.TxMB))
			}
		} else {
			global.APP_LOG.Debug("流量增量为零，跳过更新",
				zap.Uint("instanceID", instanceID),
				zap.Int64("deltaIn", deltaIn),
				zap.Int64("deltaOut", deltaOut),
				zap.Int64("deltaTotal", deltaTotal))
		}
	}

	// 同步更新实例表的vnStat基准值（用于快速查询和API展示）
	// 注意：这里存储的是vnStat的累计值，而非TrafficRecord的月度增量
	// 如果更新失败，不影响TrafficRecord的准确性，TrafficRecord是权威数据源
	instanceUpdates := map[string]interface{}{
		"used_traffic_in":  data.RxMB, // vnStat累计接收流量（MB）
		"used_traffic_out": data.TxMB, // vnStat累计发送流量（MB）
	}
	if err := global.APP_DB.Model(&provider.Instance{}).
		Where("id = ?", instanceID).
		Updates(instanceUpdates).Error; err != nil {
		// 提升为Error级别：这会导致Instance表和TrafficRecord不一致
		global.APP_LOG.Error("更新实例vnStat基准值失败，可能导致API显示不准确",
			zap.Uint("instanceID", instanceID),
			zap.Int64("vnstatRxMB", data.RxMB),
			zap.Int64("vnstatTxMB", data.TxMB),
			zap.Error(err),
			zap.String("说明", "TrafficRecord仍然准确，仅影响Instance表查询"))
		// 不返回错误，因为TrafficRecord已成功更新
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

	// 初始化TrafficResetAt（新用户或数据迁移）
	if u.TrafficResetAt == nil {
		nextReset := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())
		u.TrafficResetAt = &nextReset
		if err := global.APP_DB.Model(&u).Update("traffic_reset_at", nextReset).Error; err != nil {
			global.APP_LOG.Error("初始化用户流量重置时间失败",
				zap.Uint("userID", userID),
				zap.Error(err))
		}
		return nil // 本月不重置，等下个月
	}

	// 检查是否到了重置时间（使用 >= 判断，确保整点00:00:00立即触发）
	if !now.Before(*u.TrafficResetAt) {
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
// 使用乐观锁防止并发场景下重复处理同一实例
func (s *Service) resumeUserInstances(userID uint) error {
	var instances []provider.Instance
	err := global.APP_DB.Where("user_id = ? AND traffic_limited = ?", userID, true).Find(&instances).Error
	if err != nil {
		return err
	}

	global.APP_LOG.Info("开始恢复用户受限实例",
		zap.Uint("userID", userID),
		zap.Int("实例数量", len(instances)))

	successCount := 0
	for _, instance := range instances {
		// 使用乐观锁：只更新traffic_limited=true的实例
		// 如果并发任务已处理，RowsAffected会是0
		result := global.APP_DB.Model(&provider.Instance{}).
			Where("id = ? AND traffic_limited = ?", instance.ID, true).
			Updates(map[string]interface{}{
				"traffic_limited": false,
				"status":          "running",
			})

		if result.Error != nil {
			global.APP_LOG.Error("恢复实例状态失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(result.Error))
			continue
		}

		if result.RowsAffected == 0 {
			// 已被其他任务处理，跳过
			global.APP_LOG.Debug("实例已被其他任务恢复，跳过",
				zap.Uint("instanceID", instance.ID))
			continue
		}

		// 成功更新状态，创建启动任务
		if err := s.createStartTaskForInstance(instance.ID, instance.UserID, instance.ProviderID); err != nil {
			global.APP_LOG.Error("创建实例启动任务失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			// 回滚状态更新
			global.APP_DB.Model(&provider.Instance{}).
				Where("id = ?", instance.ID).
				Updates(map[string]interface{}{
					"traffic_limited": true,
					"status":          "stopped",
				})
			continue
		}

		successCount++
		global.APP_LOG.Info("已创建实例启动任务",
			zap.Uint("instanceID", instance.ID),
			zap.String("instanceName", instance.Name))
	}

	global.APP_LOG.Info("用户实例恢复完成",
		zap.Uint("userID", userID),
		zap.Int("成功数量", successCount),
		zap.Int("总数量", len(instances)))

	return nil
}

// createStartTaskForInstance 创建实例启动任务（同步方法，不使用goroutine）
func (s *Service) createStartTaskForInstance(instanceID, userID, providerID uint) error {
	task := &adminModel.Task{
		TaskType:         "start",
		Status:           "pending",
		Progress:         0,
		StatusMessage:    "流量重置后自动启动实例",
		UserID:           userID,
		ProviderID:       &providerID,
		InstanceID:       &instanceID,
		TimeoutDuration:  600,
		IsForceStoppable: true,
		CanForceStop:     false,
	}

	if err := global.APP_DB.Create(task).Error; err != nil {
		return fmt.Errorf("创建启动任务失败: %w", err)
	}

	// 触发调度器处理任务
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}

	return nil
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
// 注意：此方法仅同步流量数据，不执行流量限制检查
// 流量限制检查由 ThreeTierLimitService 负责
func (s *Service) SyncAllTrafficData() error {
	global.APP_LOG.Debug("开始同步流量数据")

	// 获取所有实例（包括软删除的，用于月度切换时的基准迁移）
	// 使用 Unscoped() 可以确保月初被删除的实例也能完成基准迁移
	var instances []provider.Instance
	if err := global.APP_DB.Unscoped().
		Where("status NOT IN ?", []string{"deleting"}).
		Find(&instances).Error; err != nil {
		return fmt.Errorf("获取实例列表失败: %w", err)
	}

	global.APP_LOG.Debug("获取实例列表",
		zap.Int("实例总数", len(instances)))

	// 用于收集唯一的Provider
	providerMap := make(map[uint]bool)

	for _, instance := range instances {
		// 同步每个实例的流量（包括软删除的，用于基准迁移）
		if err := s.SyncInstanceTraffic(instance.ID); err != nil {
			global.APP_LOG.Error("同步实例流量失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
		}
		providerMap[instance.ProviderID] = true
	}

	// 同步Provider流量（汇总所有实例的流量）
	for providerID := range providerMap {
		// 检查Provider是否启用了流量控制
		var p provider.Provider
		if err := global.APP_DB.Select("enable_traffic_control").First(&p, providerID).Error; err != nil {
			global.APP_LOG.Error("查询Provider失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
			continue
		}

		// 如果未启用流量控制，跳过同步
		if !p.EnableTrafficControl {
			global.APP_LOG.Debug("Provider未启用流量控制，跳过流量同步",
				zap.Uint("providerID", providerID))
			continue
		}

		if err := s.SyncProviderTraffic(providerID); err != nil {
			global.APP_LOG.Error("同步Provider流量失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
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
