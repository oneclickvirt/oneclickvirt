package traffic

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"
	"oneclickvirt/model/user"
	"oneclickvirt/service/userquota"

	"go.uber.org/zap"
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
	levelConfig, err := userquota.ResolveLevelLimit(level)
	if err != nil {
		return 102400 // 默认100GB
	}
	return levelConfig.MaxTraffic
}

// InitUserTrafficQuota 初始化用户流量配额
func (s *Service) InitUserTrafficQuota(userID uint) error {
	var u user.User
	if err := global.APP_DB.First(&u, userID).Error; err != nil {
		return err
	}

	trafficLimit := s.GetUserTrafficLimitByLevel(u.Level)
	now := time.Now()
	resetTime := time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, now.Location())

	return global.APP_DB.Model(&u).Updates(map[string]interface{}{
		"total_traffic":    trafficLimit,
		"traffic_reset_at": resetTime,
		"traffic_limited":  false,
	}).Error
}

// CheckProviderTrafficLimit 检查Provider流量限制（使用QueryService）
func (s *Service) CheckProviderTrafficLimit(providerID uint) (bool, error) {
	var p provider.Provider
	if err := global.APP_DB.First(&p, providerID).Error; err != nil {
		return false, err
	}

	now := time.Now()

	normalizedResetDay, err := NormalizeTrafficResetDay(p.TrafficResetDay)
	if err != nil {
		return false, err
	}
	p.TrafficResetDay = normalizedResetDay

	// 初始化TrafficResetAt
	if p.TrafficResetAt == nil {
		nextReset := NextTrafficResetTime(p.TrafficResetDay, now)
		p.TrafficResetAt = &nextReset
		if err := global.APP_DB.Model(&p).Update("traffic_reset_at", nextReset).Error; err != nil {
			global.APP_LOG.Warn("初始化Provider流量重置时间失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
		}
		return false, nil
	}

	// 检查是否到了重置时间
	if !now.Before(*p.TrafficResetAt) {
		threeTier := NewThreeTierLimitService()
		if err := threeTier.resetProviderTrafficLimitState(context.Background(), p, now); err != nil {
			return false, err
		}
		if err := threeTier.CheckAllUsersTrafficLimit(context.Background()); err != nil {
			return false, err
		}
		if err := threeTier.CheckAllInstancesTrafficLimit(context.Background()); err != nil {
			return false, err
		}
		return false, threeTier.RecoverTrafficStoppedInstances(context.Background())
	}

	// 使用QueryService查询当月流量
	queryService := NewQueryService()
	stats, err := queryService.GetProviderCurrentCycleTraffic(providerID)
	if err != nil {
		return false, fmt.Errorf("查询Provider流量失败: %w", err)
	}

	// 检查是否超限
	if p.MaxTraffic > 0 && int64(stats.ActualUsageMB) >= p.MaxTraffic {
		return true, nil
	}

	return false, nil
}
