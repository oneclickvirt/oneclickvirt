package traffic

import (
	"context"
	"errors"
	"sync"
	"time"

	"oneclickvirt/global"
	provider "oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// SyncTriggerService is the lightweight limit-check trigger used by lifecycle
// hooks. Explicit Agent-backed refreshes live in service/trafficmanual so this
// lower-level package does not depend on the Agent service.
type SyncTriggerService struct {
	threeTierService *ThreeTierLimitService
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

func NewSyncTriggerService() *SyncTriggerService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SyncTriggerService{
		threeTierService: NewThreeTierLimitService(),
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Shutdown waits for already-triggered work before cancelling its context.
func (s *SyncTriggerService) Shutdown(timeout time.Duration) error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-done:
		s.cancel()
		global.APP_LOG.Info("流量同步触发服务已关闭")
		return nil
	case <-timer.C:
		// Cancel only after the grace period. Cancelling before Wait would make
		// every request-scoped trigger exit before it can do any work.
		s.cancel()
		global.APP_LOG.Warn("流量同步触发服务关闭超时")
		return context.DeadlineExceeded
	}
}

// TriggerInstanceTrafficSync only evaluates limits. It intentionally avoids a
// provider-wide remote Agent pull on lifecycle events.
func (s *SyncTriggerService) TriggerInstanceTrafficSync(instanceID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("流量同步过程中发生panic", zap.Uint("instanceID", instanceID), zap.String("reason", reason), zap.Any("panic", r))
			}
		}()

		// 检查服务是否已取消
		select {
		case <-s.ctx.Done():
			global.APP_LOG.Debug("流量同步已取消", zap.Uint("instanceID", instanceID), zap.String("reason", reason))
			return
		default:
		}
		global.APP_LOG.Info("触发实例流量同步", zap.Uint("instanceID", instanceID), zap.String("reason", reason))

		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
		defer cancel()

		var inst provider.Instance
		if err := global.APP_DB.Select("user_id").First(&inst, instanceID).Error; err != nil {
			global.APP_LOG.Warn("触发实例流量同步：获取实例信息失败", zap.Uint("instanceID", instanceID), zap.Error(err))
			return
		}
		if inst.UserID == 0 {
			// Imported or administrator-owned instances may intentionally have no
			// user binding. They can still be monitored, but user-level limits do
			// not apply and must not produce a misleading record-not-found warning.
			global.APP_LOG.Debug("同步实例流量跳过：实例未绑定用户", zap.Uint("instanceID", instanceID), zap.String("reason", reason))
			return
		}

		// 生命周期钩子只做限额检查，不在高频事件中拉取整台节点的 Agent 数据。
		if _, err := s.checkUserTrafficLimitWithContext(ctx, inst.UserID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				global.APP_LOG.Warn("同步实例流量跳过：用户不存在", zap.Uint("instanceID", instanceID), zap.Uint("userID", inst.UserID), zap.Error(err))
				return
			}
			global.APP_LOG.Error("同步实例流量失败", zap.Uint("instanceID", instanceID), zap.String("reason", reason), zap.Error(err))
		}
	}()
}

func (s *SyncTriggerService) TriggerUserTrafficSync(userID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("用户流量同步过程中发生panic", zap.Uint("userID", userID), zap.String("reason", reason), zap.Any("panic", r))
			}
		}()
		// 检查服务是否已取消
		select {
		case <-s.ctx.Done():
			global.APP_LOG.Debug("用户流量同步已取消", zap.Uint("userID", userID), zap.String("reason", reason))
			return
		default:
		}
		global.APP_LOG.Info("触发用户流量同步", zap.Uint("userID", userID), zap.String("reason", reason))
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
		defer cancel()
		// 使用三层级流量限制服务检查流量限制
		if _, err := s.checkUserTrafficLimitWithContext(ctx, userID); err != nil {
			global.APP_LOG.Error("同步用户流量失败", zap.Uint("userID", userID), zap.String("reason", reason), zap.Error(err))
			return
		}
		global.APP_LOG.Debug("用户流量同步完成", zap.Uint("userID", userID), zap.String("reason", reason))
	}()
}

func (s *SyncTriggerService) checkUserTrafficLimitWithContext(ctx context.Context, userID uint) (interface{}, error) {
	// 检查context是否已取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return s.threeTierService.CheckUserTrafficLimit(userID)
}

func (s *SyncTriggerService) TriggerProviderTrafficSync(providerID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("Provider流量同步过程中发生panic", zap.Uint("providerID", providerID), zap.String("reason", reason), zap.Any("panic", r))
			}
		}()
		// 检查服务是否已取消
		select {
		case <-s.ctx.Done():
			global.APP_LOG.Debug("Provider流量同步已取消", zap.Uint("providerID", providerID), zap.String("reason", reason))
			return
		default:
		}
		global.APP_LOG.Info("触发Provider流量同步", zap.Uint("providerID", providerID), zap.String("reason", reason))

		// 检查Provider是否启用了流量控制
		var p provider.Provider
		if err := global.APP_DB.Select("enable_traffic_control").First(&p, providerID).Error; err != nil {
			global.APP_LOG.Error("查询Provider失败", zap.Uint("providerID", providerID), zap.String("reason", reason), zap.Error(err))
			return
		}
		if !p.EnableTrafficControl {
			global.APP_LOG.Debug("Provider未启用流量控制，跳过流量同步", zap.Uint("providerID", providerID), zap.String("reason", reason))
			return
		}

		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
		defer cancel()
		// 使用三层级流量限制服务检查Provider流量限制
		if _, err := s.checkProviderTrafficLimitWithContext(ctx, providerID); err != nil {
			global.APP_LOG.Error("同步Provider流量失败", zap.Uint("providerID", providerID), zap.String("reason", reason), zap.Error(err))
			return
		}
		global.APP_LOG.Debug("Provider流量同步完成", zap.Uint("providerID", providerID), zap.String("reason", reason))
	}()
}

func (s *SyncTriggerService) checkProviderTrafficLimitWithContext(ctx context.Context, providerID uint) (interface{}, error) {
	// 检查context是否已取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	return s.threeTierService.CheckProviderTrafficLimit(providerID)
}

func (s *SyncTriggerService) TriggerDelayedInstanceTrafficSync(instanceID uint, delay time.Duration, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("延迟流量同步过程中发生panic", zap.Uint("instanceID", instanceID), zap.Duration("delay", delay), zap.String("reason", reason), zap.Any("panic", r))
			}
		}()
		global.APP_LOG.Info("计划延迟触发实例流量同步", zap.Uint("instanceID", instanceID), zap.Duration("delay", delay), zap.String("reason", reason))
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-timer.C:
			s.TriggerInstanceTrafficSync(instanceID, reason+" (延迟触发)")
		case <-s.ctx.Done():
			global.APP_LOG.Debug("延迟流量同步已取消", zap.Uint("instanceID", instanceID), zap.Duration("delay", delay))
			return
		}
	}()
}
