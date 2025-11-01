package scheduler

import (
	"context"

	"oneclickvirt/global"
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
	trafficService := traffic.NewService()
	if err := trafficService.SyncAllTrafficData(); err != nil {
		global.APP_LOG.Error("同步流量数据失败", zap.Error(err))
	} else {
		global.APP_LOG.Debug("流量数据同步完成")
	}
}

// checkMonthlyTrafficReset 检查月度流量重置（使用三层级流量限制）
func (s *SchedulerService) checkMonthlyTrafficReset() {
	// 检查数据库是否已初始化
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过流量重置检查")
		return
	}

	global.APP_LOG.Debug("开始三层级流量限制检查")

	// 使用新的三层级流量限制服务
	threeTierService := traffic.NewThreeTierLimitService()

	ctx := context.Background()
	if err := threeTierService.CheckAllTrafficLimits(ctx); err != nil {
		global.APP_LOG.Error("三层级流量限制检查失败", zap.Error(err))
	}

	global.APP_LOG.Debug("三层级流量限制检查完成")

	// 清理旧的流量记录（保留最近2个月）
	trafficService := traffic.NewService()
	if err := trafficService.CleanupOldTrafficRecords(); err != nil {
		global.APP_LOG.Error("清理旧流量记录失败", zap.Error(err))
	}
}
