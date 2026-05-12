package traffic

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// ============ 实例层级流量限制 ============

// CheckAllInstancesTrafficLimit 检查所有实例的流量限制
func (s *ThreeTierLimitService) CheckAllInstancesTrafficLimit(ctx context.Context) error {
	const batchSize = 200
	var lastID uint = 0
	limitedCount := 0
	totalCount := 0

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// 游标分页：每次获取200条，按id升序，从上次最大id之后开始
		var instances []provider.Instance
		err := global.APP_DB.
			Where("id > ? AND status NOT IN (?) AND traffic_limited = ? AND (traffic_limit_reason = ? OR traffic_limit_reason = ?)",
				lastID, []string{"deleted", "deleting"}, false, "", "instance").
			Order("id ASC").
			Limit(batchSize).
			Find(&instances).Error
		if err != nil {
			return fmt.Errorf("获取实例列表失败: %w", err)
		}

		if len(instances) == 0 {
			break
		}

		// 批量预加载当前批次所有实例的 Provider 配置，避免 N+1 查询
		providerIDSet := make(map[uint]bool)
		for _, inst := range instances {
			providerIDSet[inst.ProviderID] = true
		}
		providerIDs := make([]uint, 0, len(providerIDSet))
		for id := range providerIDSet {
			providerIDs = append(providerIDs, id)
		}
		type batchProviderConfig struct {
			ID                     uint
			EnableTrafficControl   bool
			TrafficOverLimitAction string
			TrafficSpeedLimitKbps  int
		}
		var batchProviders []batchProviderConfig
		if len(providerIDs) > 0 {
			if err := global.APP_DB.Table("providers").
				Select("id, enable_traffic_control, traffic_over_limit_action, traffic_speed_limit_kbps").
				Where("id IN ?", providerIDs).
				Scan(&batchProviders).Error; err != nil {
				global.APP_LOG.Error("批量查询Provider配置失败，跳过本批次实例检查",
					zap.Error(err), zap.Int("batchSize", len(instances)))
				// 跳过本批次，推进游标，下次调度器运行时重试
				lastID = instances[len(instances)-1].ID
				totalCount += len(instances)
				if len(instances) < batchSize {
					break
				}
				continue
			}
		}
		providerConfigMap := make(map[uint]batchProviderConfig, len(batchProviders))
		for _, pc := range batchProviders {
			providerConfigMap[pc.ID] = pc
		}

		// 并发检查当前批次（上限 20 个 goroutine）
		const concurrency = 20
		sem := make(chan struct{}, concurrency)
		var wg sync.WaitGroup
		var batchLimited int32

		for _, instance := range instances {
			select {
			case <-ctx.Done():
				wg.Wait()
				return ctx.Err()
			default:
			}

			inst := instance // capture loop variable
			sem <- struct{}{}
			wg.Add(1)
			go func() {
				defer func() {
					<-sem
					wg.Done()
				}()
				pc, ok := providerConfigMap[inst.ProviderID]
				if !ok || !pc.EnableTrafficControl {
					// Provider 未启用流量统计，解除可能存在的实例层级限制
					if inst.TrafficLimited && inst.TrafficLimitReason == "instance" {
						s.unlimitInstance(inst.ID, "Provider已禁用流量统计") //nolint:errcheck
					}
					return
				}
				isLimited, err := s.checkInstanceTrafficLimitWithData(inst, pc.TrafficOverLimitAction, pc.TrafficSpeedLimitKbps)
				if err != nil {
					global.APP_LOG.Warn("检查实例流量限制失败",
						zap.Uint("instanceID", inst.ID),
						zap.Error(err))
					return
				}
				if isLimited {
					atomic.AddInt32(&batchLimited, 1)
				}
			}()
		}
		wg.Wait()
		limitedCount += int(batchLimited)

		totalCount += len(instances)
		lastID = instances[len(instances)-1].ID

		if len(instances) < batchSize {
			break
		}
	}

	global.APP_LOG.Debug("实例层级流量检查完成",
		zap.Int("总实例数", totalCount),
		zap.Int("超限实例数", limitedCount))
	return nil
}

// CheckInstanceTrafficLimit 检查单个实例的流量限制
// 返回是否被限制
func (s *ThreeTierLimitService) CheckInstanceTrafficLimit(instanceID uint) (bool, error) {
	var instance provider.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return false, fmt.Errorf("获取实例信息失败: %w", err)
	}

	// 检查实例所属 Provider 是否启用流量统计
	var p provider.Provider
	if err := global.APP_DB.Select("enable_traffic_control").First(&p, instance.ProviderID).Error; err != nil {
		global.APP_LOG.Warn("获取Provider流量统计开关失败，跳过实例检查",
			zap.Uint("instanceID", instanceID),
			zap.Uint("providerID", instance.ProviderID),
			zap.Error(err))
		return false, nil
	}

	// 如果Provider未启用流量统计，解除可能存在的实例层级限制
	if !p.EnableTrafficControl {
		if instance.TrafficLimited && instance.TrafficLimitReason == "instance" {
			return s.unlimitInstance(instanceID, "Provider已禁用流量统计")
		}
		return false, nil
	}

	// 如果实例已经被更高层级限制，跳过
	if instance.TrafficLimited && instance.TrafficLimitReason != "" && instance.TrafficLimitReason != "instance" {
		return true, nil // 已被用户或Provider层级限制
	}

	// 如果实例没有设置流量限制（MaxTraffic=0），跳过
	if instance.MaxTraffic <= 0 {
		// 如果之前是实例层级限制的，现在解除
		if instance.TrafficLimited && instance.TrafficLimitReason == "instance" {
			return s.unlimitInstance(instanceID, "实例无流量限制")
		}
		return false, nil
	}

	// 获取实例当月流量
	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	// 使用统一的流量查询服务
	queryService := NewQueryService()
	monthlyStats, err := queryService.GetInstanceMonthlyTraffic(instanceID, year, month)
	if err != nil {
		global.APP_LOG.Warn("获取实例 pmacct 流量失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return false, fmt.Errorf("获取实例流量失败: %w", err)
	}

	usedTraffic := int64(monthlyStats.ActualUsageMB)

	// 不再更新instance.used_traffic字段（已删除）

	// 检查是否超限
	if usedTraffic >= instance.MaxTraffic {
		// 实例超限，根据Provider设置决定停止或限速
		global.APP_LOG.Info("实例流量超限",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instance.Name),
			zap.Int64("usedTraffic", usedTraffic),
			zap.Int64("maxTraffic", instance.MaxTraffic))

		return s.limitInstance(instanceID, "instance", fmt.Sprintf("实例流量超限: %dMB/%dMB", usedTraffic, instance.MaxTraffic))
	}

	// 未超限，如果之前是实例层级限制的，解除限制
	if instance.TrafficLimited && instance.TrafficLimitReason == "instance" {
		return s.unlimitInstance(instanceID, "实例流量恢复正常")
	}

	return false, nil
}

// checkInstanceTrafficLimitWithData 使用预加载的实例和Provider配置检查流量限制
// 供批量检查路径使用，避免重复查询数据库（N+1问题）
func (s *ThreeTierLimitService) checkInstanceTrafficLimitWithData(instance provider.Instance, trafficOverLimitAction string, trafficSpeedLimitKbps int) (bool, error) {
	instanceID := instance.ID

	// 如果实例已经被更高层级限制，跳过
	if instance.TrafficLimited && instance.TrafficLimitReason != "" && instance.TrafficLimitReason != "instance" {
		return true, nil
	}

	// 如果实例没有设置流量限制（MaxTraffic=0），跳过
	if instance.MaxTraffic <= 0 {
		if instance.TrafficLimited && instance.TrafficLimitReason == "instance" {
			return s.unlimitInstance(instanceID, "实例无流量限制")
		}
		return false, nil
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	queryService := NewQueryService()
	monthlyStats, err := queryService.GetInstanceMonthlyTraffic(instanceID, year, month)
	if err != nil {
		global.APP_LOG.Warn("获取实例流量失败",
			zap.Uint("instanceID", instanceID),
			zap.Error(err))
		return false, fmt.Errorf("获取实例流量失败: %w", err)
	}

	usedTraffic := int64(monthlyStats.ActualUsageMB)

	if usedTraffic >= instance.MaxTraffic {
		global.APP_LOG.Info("实例流量超限",
			zap.Uint("instanceID", instanceID),
			zap.String("instanceName", instance.Name),
			zap.Int64("usedTraffic", usedTraffic),
			zap.Int64("maxTraffic", instance.MaxTraffic))

		return s.limitInstanceWithAction(instance, "instance",
			fmt.Sprintf("实例流量超限: %dMB/%dMB", usedTraffic, instance.MaxTraffic),
			trafficOverLimitAction, trafficSpeedLimitKbps)
	}

	if instance.TrafficLimited && instance.TrafficLimitReason == "instance" {
		return s.unlimitInstance(instanceID, "实例流量恢复正常")
	}

	return false, nil
}

// limitInstance 限制单个实例（原子性：实例状态更新与停止任务创建在同一事务中）
// 支持两种限制模式：stop（停机）和speed_limit（限速）
func (s *ThreeTierLimitService) limitInstance(instanceID uint, reason string, message string) (bool, error) {
	var instance provider.Instance
	if err := global.APP_DB.First(&instance, instanceID).Error; err != nil {
		return false, err
	}

	// 查询Provider的流量超限动作配置
	var p provider.Provider
	if err := global.APP_DB.Select("traffic_over_limit_action, traffic_speed_limit_kbps").
		First(&p, instance.ProviderID).Error; err != nil {
		global.APP_LOG.Warn("获取Provider限流配置失败，使用默认停机", zap.Error(err))
		p.TrafficOverLimitAction = "stop"
	}

	return s.limitInstanceWithAction(instance, reason, message, p.TrafficOverLimitAction, p.TrafficSpeedLimitKbps)
}

// limitInstanceWithAction 使用预加载的Provider动作配置限制实例（避免重复DB查询）
func (s *ThreeTierLimitService) limitInstanceWithAction(instance provider.Instance, reason string, message string, trafficOverLimitAction string, trafficSpeedLimitKbps int) (bool, error) {
	instanceID := instance.ID
	userID := instance.UserID
	providerID := instance.ProviderID

	if trafficOverLimitAction == "speed_limit" {
		// 限速模式：标记受限但不停机，创建限速任务
		updates := map[string]interface{}{
			"traffic_limited":      true,
			"traffic_limit_reason": reason,
		}
		if err := global.APP_DB.Model(&provider.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
			return false, fmt.Errorf("标记实例为限速状态失败: %w", err)
		}

		speedKbps := trafficSpeedLimitKbps
		if speedKbps <= 0 {
			speedKbps = 1024 // 默认1Mbps
		}

		global.APP_LOG.Info("实例流量超限，已限速",
			zap.Uint("instanceID", instanceID),
			zap.Int("speedLimitKbps", speedKbps),
			zap.String("reason", reason))

		return true, nil
	}

	// 停机模式（默认行为）
	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		// 原子性标记实例为受限状态
		updates := map[string]interface{}{
			"traffic_limited":      true,
			"traffic_limit_reason": reason,
			"status":               "stopped",
		}
		if err := tx.Model(&provider.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
			return fmt.Errorf("标记实例为受限状态失败: %w", err)
		}

		// 在同一事务中创建停止任务，防止状态与任务不一致
		return s.createStopTaskTx(tx, userID, instanceID, providerID, message)
	})

	if err != nil {
		return false, err
	}

	// 事务提交后触发调度器（事务外执行，避免长事务）
	if global.APP_SCHEDULER != nil {
		global.APP_SCHEDULER.TriggerTaskProcessing()
	}
	return true, nil
}

// unlimitInstance 解除单个实例的限制
func (s *ThreeTierLimitService) unlimitInstance(instanceID uint, reason string) (bool, error) {
	updates := map[string]interface{}{
		"traffic_limited":      false,
		"traffic_limit_reason": "",
	}

	if err := global.APP_DB.Model(&provider.Instance{}).Where("id = ?", instanceID).Updates(updates).Error; err != nil {
		return false, fmt.Errorf("解除实例限制失败: %w", err)
	}

	global.APP_LOG.Info("解除实例流量限制",
		zap.Uint("instanceID", instanceID),
		zap.String("reason", reason))

	return false, nil
}

