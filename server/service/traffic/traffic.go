package traffic

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/monitoring"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/service/vnstat"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

// TrafficLimitService 流量限制服务 - 集成vnStat数据进行流量控制
type TrafficLimitService struct {
	vnstatService  *vnstat.Service
	trafficService *TrafficService
}

// NewTrafficLimitService 创建流量限制服务
func NewTrafficLimitService() *TrafficLimitService {
	return &TrafficLimitService{
		vnstatService:  vnstat.NewService(),
		trafficService: NewTrafficService(),
	}
}

// TrafficService 流量服务
type TrafficService struct{}

// NewTrafficService 创建流量服务
func NewTrafficService() *TrafficService {
	return &TrafficService{}
}

// SyncAllTrafficData 同步所有流量数据
func (s *TrafficService) SyncAllTrafficData() error {
	global.APP_LOG.Debug("开始同步流量数据")

	// 获取所有活跃实例
	var instances []provider.Instance
	err := global.APP_DB.Where("status NOT IN ?", []string{"deleted", "deleting"}).Find(&instances).Error
	if err != nil {
		return fmt.Errorf("获取实例列表失败: %w", err)
	}

	// 这里可以实现具体的流量同步逻辑
	// 例如从各个实例收集流量数据并更新到数据库

	global.APP_LOG.Debug("流量数据同步完成", zap.Int("instanceCount", len(instances)))
	return nil
}

// CheckUserTrafficLimit 检查用户流量限制
func (s *TrafficService) CheckUserTrafficLimit(userID uint) (bool, error) {
	var user user.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return false, fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 如果用户没有流量限制，返回未超限
	if user.TotalTraffic <= 0 {
		return false, nil
	}

	// 检查是否超过流量限制
	return user.UsedTraffic >= user.TotalTraffic, nil
}

// CheckProviderTrafficLimit 检查Provider流量限制
func (s *TrafficService) CheckProviderTrafficLimit(providerID uint) (bool, error) {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 如果Provider没有流量限制，返回未超限
	if p.MaxTraffic <= 0 {
		return false, nil
	}

	// 检查是否超过流量限制
	return p.UsedTraffic >= p.MaxTraffic, nil
}

// InitUserTrafficQuota 初始化用户流量配额
func (s *TrafficService) InitUserTrafficQuota(userID uint) error {
	var user user.User
	if err := global.APP_DB.First(&user, userID).Error; err != nil {
		return fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 如果用户已有流量配额，跳过初始化
	if user.TotalTraffic > 0 {
		return nil
	}

	// 设置默认流量配额（例如100GB）
	defaultQuota := int64(100 * 1024) // 100GB in MB

	err := global.APP_DB.Model(&user).Updates(map[string]interface{}{
		"total_traffic": defaultQuota,
		"used_traffic":  0,
	}).Error

	if err != nil {
		return fmt.Errorf("初始化用户流量配额失败: %w", err)
	}

	global.APP_LOG.Info("用户流量配额初始化完成",
		zap.Uint("userID", userID),
		zap.Int64("quota", defaultQuota))

	return nil
}

// SyncAllTrafficLimitsWithVnStat 使用vnStat同步所有流量限制（已迁移到 ThreeTierLimitService）
// 此方法保留用于向后兼容，实际调用新的三层级限制服务
func (s *TrafficLimitService) SyncAllTrafficLimitsWithVnStat(ctx context.Context) error {
	global.APP_LOG.Info("调用三层级流量限制服务进行同步检查")

	// 使用新的三层级限制服务
	threeTierService := NewThreeTierLimitService()

	// 直接调用统一的检查方法
	if err := threeTierService.CheckAllTrafficLimits(ctx); err != nil {
		global.APP_LOG.Error("三层级流量限制检查失败", zap.Error(err))
		return err
	}

	global.APP_LOG.Info("三层级流量限制同步完成")
	return nil
}

// CheckUserTrafficLimitWithVnStat 使用vnStat检查用户流量限制
func (s *TrafficLimitService) CheckUserTrafficLimitWithVnStat(userID uint) (bool, string, error) {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return false, "", fmt.Errorf("获取用户信息失败: %w", err)
	}

	// 如果用户没有流量限制，返回未超限
	if u.TotalTraffic <= 0 {
		return false, "", nil
	}

	// 使用vnstat服务获取流量数据
	totalUsed, err := s.getUserMonthlyTrafficFromVnStat(userID)
	if err != nil {
		global.APP_LOG.Warn("从vnStat获取用户流量失败，使用数据库中的数据",
			zap.Uint("userID", userID),
			zap.Error(err))
		// 降级到数据库中的数据
		totalUsed = u.UsedTraffic
	}

	// 更新用户已使用流量
	err = utils.RetryableDBOperation(context.Background(), func() error {
		return global.APP_DB.Model(&u).Update("used_traffic", totalUsed).Error
	}, 3)
	if err != nil {
		return false, "", fmt.Errorf("更新用户流量使用量失败: %w", err)
	}

	// 检查是否超限
	if totalUsed >= u.TotalTraffic {
		limitReason := fmt.Sprintf("用户流量已超限：使用 %dMB，限制 %dMB",
			totalUsed, u.TotalTraffic)

		// 标记用户为受限状态
		if err := global.APP_DB.Model(&u).Update("traffic_limited", true).Error; err != nil {
			return false, "", fmt.Errorf("标记用户流量受限失败: %w", err)
		}

		global.APP_LOG.Info("用户流量超限",
			zap.Uint("userID", userID),
			zap.Int64("usedTraffic", totalUsed),
			zap.Int64("totalTraffic", u.TotalTraffic))

		return true, limitReason, nil
	}

	// 未超限，确保用户不处于受限状态
	if u.TrafficLimited {
		if err := global.APP_DB.Model(&u).Update("traffic_limited", false).Error; err != nil {
			return false, "", fmt.Errorf("取消用户流量限制状态失败: %w", err)
		}
		global.APP_LOG.Info("用户流量限制已解除",
			zap.Uint("userID", userID),
			zap.Int64("usedTraffic", totalUsed),
			zap.Int64("totalTraffic", u.TotalTraffic))
	}

	return false, "", nil
}

// CheckProviderTrafficLimitWithVnStat 使用vnStat检查Provider流量限制
func (s *TrafficLimitService) CheckProviderTrafficLimitWithVnStat(providerID uint) (bool, string, error) {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, "", fmt.Errorf("获取Provider信息失败: %w", err)
	}

	// 如果Provider没有流量限制，返回未超限
	if p.MaxTraffic <= 0 {
		return false, "", nil
	}

	// 使用vnstat服务获取Provider流量数据
	totalUsed, err := s.getProviderMonthlyTrafficFromVnStat(providerID)
	if err != nil {
		global.APP_LOG.Warn("从vnStat获取Provider流量失败，使用数据库中的数据",
			zap.Uint("providerID", providerID),
			zap.Error(err))
		// 降级到数据库中的数据
		totalUsed = p.UsedTraffic
	}

	// 更新Provider已使用流量
	if err := global.APP_DB.Model(&p).Update("used_traffic", totalUsed).Error; err != nil {
		return false, "", fmt.Errorf("更新Provider流量使用量失败: %w", err)
	}

	// 检查是否超限
	if totalUsed >= p.MaxTraffic {
		limitReason := fmt.Sprintf("Provider流量已超限：使用 %dMB，限制 %dMB",
			totalUsed, p.MaxTraffic)

		global.APP_LOG.Info("Provider流量超限",
			zap.Uint("providerID", providerID),
			zap.Int64("usedTraffic", totalUsed),
			zap.Int64("trafficLimit", p.MaxTraffic))

		return true, limitReason, nil
	}

	return false, "", nil
}

// getUserMonthlyTrafficFromVnStat 从vnStat获取用户当月总流量
func (s *TrafficLimitService) getUserMonthlyTrafficFromVnStat(userID uint) (int64, error) {
	// 获取用户的所有实例
	var instances []provider.Instance
	err := global.APP_DB.Where("user_id = ? AND status != ? AND status != ?",
		userID, "deleted", "deleting").Find(&instances).Error
	if err != nil {
		return 0, fmt.Errorf("获取用户实例失败: %w", err)
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	var totalTraffic int64 = 0

	// 累加所有实例的当月流量
	for _, instance := range instances {
		instanceTraffic, err := s.getInstanceMonthlyTrafficFromVnStat(instance.ID, year, month)
		if err != nil {
			global.APP_LOG.Warn("获取实例流量失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}
		totalTraffic += instanceTraffic
	}

	return totalTraffic, nil
}

// getInstanceMonthlyTrafficFromVnStat 从vnStat获取实例当月流量
func (s *TrafficLimitService) getInstanceMonthlyTrafficFromVnStat(instanceID uint, year, month int) (int64, error) {
	// 从vnStat记录表中获取实例的月度流量数据
	var totalBytes int64

	// 查询当月的汇总记录（day=0, hour=0表示月度汇总）
	err := global.APP_DB.Model(&monitoring.VnStatTrafficRecord{}).
		Where("instance_id = ? AND year = ? AND month = ? AND day = 0 AND hour = 0",
			instanceID, year, month).
		Select("COALESCE(SUM(rx_bytes + tx_bytes), 0)").
		Scan(&totalBytes).Error

	if err != nil {
		return 0, fmt.Errorf("查询vnStat记录失败: %w", err)
	}

	// 转换为MB
	return totalBytes / (1024 * 1024), nil
}

// getProviderMonthlyTrafficFromVnStat 从vnStat获取Provider当月总流量
func (s *TrafficLimitService) getProviderMonthlyTrafficFromVnStat(providerID uint) (int64, error) {
	// 获取Provider的所有实例
	var instances []provider.Instance
	err := global.APP_DB.Where("provider_id = ? AND status != ? AND status != ?",
		providerID, "deleted", "deleting").Find(&instances).Error
	if err != nil {
		return 0, fmt.Errorf("获取Provider实例失败: %w", err)
	}

	now := time.Now()
	year := now.Year()
	month := int(now.Month())

	var totalTraffic int64 = 0

	// 累加所有实例的当月流量
	for _, instance := range instances {
		instanceTraffic, err := s.getInstanceMonthlyTrafficFromVnStat(instance.ID, year, month)
		if err != nil {
			global.APP_LOG.Warn("获取实例流量失败",
				zap.Uint("instanceID", instance.ID),
				zap.Error(err))
			continue
		}
		totalTraffic += instanceTraffic
	}

	return totalTraffic, nil
}
