package scheduler

import (
	"context"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/service/traffic"

	"go.uber.org/zap"
)

// TrafficLimitServiceInterface 流量限制服务接口
type TrafficLimitServiceInterface interface {
	SyncAllTrafficLimitsWithVnStat(ctx context.Context) error
	CheckUserTrafficLimitWithVnStat(userID uint) (bool, string, error)
	CheckProviderTrafficLimitWithVnStat(providerID uint) (bool, string, error)
}

// TrafficServiceInterface 流量服务接口
type TrafficServiceInterface interface {
	SyncAllTrafficData() error
	CheckUserTrafficLimit(userID uint) (bool, error)
	CheckProviderTrafficLimit(providerID uint) (bool, error)
	InitUserTrafficQuota(userID uint) error
}

// syncAllTrafficData 同步所有流量数据（使用vnStat）
func (s *SchedulerService) syncAllTrafficData() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过流量数据同步")
		return
	}

	// 降低流量同步的日志级别，减少频繁输出
	global.APP_LOG.Debug("开始同步流量数据（基于vnStat）")

	// 使用流量服务进行同步
	trafficService := traffic.NewTrafficService()
	if err := trafficService.SyncAllTrafficData(); err != nil {
		global.APP_LOG.Error("同步流量数据失败", zap.Error(err))
	} else {
		global.APP_LOG.Debug("流量数据同步完成")
	}
}

// checkMonthlyTrafficReset 检查月度流量重置（使用vnStat）
func (s *SchedulerService) checkMonthlyTrafficReset() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过流量重置检查")
		return
	}

	// 获取所有活跃用户
	var userIDs []uint
	if err := global.APP_DB.Model(&user.User{}).
		Where("status = ?", 1).
		Pluck("id", &userIDs).Error; err != nil {
		global.APP_LOG.Error("获取用户列表失败", zap.Error(err))
		return
	}

	// 获取所有活跃Provider
	var providerIDs []uint
	if err := global.APP_DB.Model(&provider.Provider{}).
		Where("status = ?", "active").
		Pluck("id", &providerIDs).Error; err != nil {
		global.APP_LOG.Error("获取Provider列表失败", zap.Error(err))
		return
	}

	// 使用流量限制服务检查流量
	trafficLimitService := traffic.NewTrafficLimitService()

	// 检查用户流量限制
	for _, userID := range userIDs {
		isLimited, reason, err := trafficLimitService.CheckUserTrafficLimitWithVnStat(userID)
		if err != nil {
			global.APP_LOG.Error("检查用户流量限制失败",
				zap.Uint("userID", userID),
				zap.Error(err))
		} else if isLimited {
			global.APP_LOG.Info("用户流量超限",
				zap.Uint("userID", userID),
				zap.String("reason", reason))
		}
	}

	// 检查Provider流量限制
	for _, providerID := range providerIDs {
		isLimited, reason, err := trafficLimitService.CheckProviderTrafficLimitWithVnStat(providerID)
		if err != nil {
			global.APP_LOG.Error("检查Provider流量限制失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
		} else if isLimited {
			global.APP_LOG.Info("Provider流量超限",
				zap.Uint("providerID", providerID),
				zap.String("reason", reason))
		}
	}

	global.APP_LOG.Debug("流量重置检查完成",
		zap.Int("userCount", len(userIDs)),
		zap.Int("providerCount", len(providerIDs)))

	// 清理旧的流量记录（保留最近2个月）
	trafficService := traffic.NewService()
	if err := trafficService.CleanupOldTrafficRecords(); err != nil {
		global.APP_LOG.Error("清理旧流量记录失败", zap.Error(err))
	}
}

// checkMonthlyTrafficReset 检查每月流量重置，如果需要则重置所有用户的已用流量
