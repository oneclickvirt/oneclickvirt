package traffic

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

func (s *ThreeTierLimitService) ProcessDueTrafficResets(ctx context.Context) error {
	const batchSize = 100
	now := time.Now()
	var lastID uint

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		var providers []provider.Provider
		if err := global.APP_DB.
			Select("id, traffic_reset_day, traffic_reset_at").
			Where("id > ? AND (traffic_reset_at IS NULL OR traffic_reset_at <= ?)", lastID, now).
			Order("id ASC").
			Limit(batchSize).
			Find(&providers).Error; err != nil {
			return fmt.Errorf("查询到期流量重置节点失败: %w", err)
		}
		if len(providers) == 0 {
			return nil
		}

		for _, p := range providers {
			if err := s.resetProviderTrafficLimitState(ctx, p, now); err != nil {
				global.APP_LOG.Warn("节点流量周期重置失败",
					zap.Uint("providerID", p.ID),
					zap.Error(err))
			}
			lastID = p.ID
		}

		if len(providers) < batchSize {
			return nil
		}
	}
}

func (s *ThreeTierLimitService) resetProviderTrafficLimitState(ctx context.Context, p provider.Provider, now time.Time) error {
	nextReset := NextTrafficResetTime(p.TrafficResetDay, now)
	var affectedUsers []uint

	err := global.APP_DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&provider.Provider{}).
			Where("id = ?", p.ID).
			Updates(map[string]interface{}{
				"traffic_reset_at": nextReset,
				"traffic_limited":  false,
			}).Error; err != nil {
			return err
		}

		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND traffic_limit_reason IN ?", p.ID, []string{"provider", "user", "instance"}).
			Updates(map[string]interface{}{
				"traffic_limited":      false,
				"traffic_limit_reason": "",
			}).Error; err != nil {
			return err
		}

		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND frozen_reason = ?", p.ID, "traffic_limit").
			Updates(map[string]interface{}{
				"is_frozen":     false,
				"frozen_reason": "",
				"frozen_at":     nil,
			}).Error; err != nil {
			return err
		}

		if err := tx.Model(&provider.Instance{}).
			Where("provider_id = ? AND deleted_at IS NULL", p.ID).
			Distinct("user_id").
			Pluck("user_id", &affectedUsers).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return err
	}

	global.APP_LOG.Info("节点流量周期已重置",
		zap.Uint("providerID", p.ID),
		zap.Time("nextResetAt", nextReset),
		zap.Int("affectedUsers", len(affectedUsers)))

	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	return nil
}
