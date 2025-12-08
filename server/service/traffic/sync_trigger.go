package traffic

import (
	"context"
	"sync"
	"time"

	"oneclickvirt/global"
	provider "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// SyncTriggerService 流量同步触发服务
type SyncTriggerService struct {
	service          *Service
	limitService     *LimitService
	threeTierService *ThreeTierLimitService
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

// NewSyncTriggerService 创建流量同步触发服务
func NewSyncTriggerService() *SyncTriggerService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SyncTriggerService{
		service:          NewService(),
		limitService:     NewLimitService(),
		threeTierService: NewThreeTierLimitService(),
		ctx:              ctx,
		cancel:           cancel,
	}
}

// Shutdown 优雅关闭服务，等待所有goroutine完成
func (s *SyncTriggerService) Shutdown(timeout time.Duration) error {
	s.cancel()

	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case <-done:
		global.APP_LOG.Info("流量同步触发服务已关闭")
		return nil
	case <-timer.C:
		global.APP_LOG.Warn("流量同步触发服务关闭超时")
		return context.DeadlineExceeded
	}
}

// TriggerInstanceTrafficSync 触发单个实例的流量同步
func (s *SyncTriggerService) TriggerInstanceTrafficSync(instanceID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("流量同步过程中发生panic",
					zap.Uint("instanceID", instanceID),
					zap.String("reason", reason),
					zap.Any("panic", r))
			}
		}()

		// 检查服务是否已取消
		select {
		case <-s.ctx.Done():
			global.APP_LOG.Info("流量同步已取消",
				zap.Uint("instanceID", instanceID),
				zap.String("reason", reason))
			return
		default:
		}

		global.APP_LOG.Info("触发实例流量同步",
			zap.Uint("instanceID", instanceID),
			zap.String("reason", reason))

		global.APP_LOG.Debug("流量同步触发器调用",
			zap.Uint("instanceID", instanceID),
			zap.String("reason", reason))
	}()
}

// TriggerUserTrafficSync 触发用户所有实例的流量同步
func (s *SyncTriggerService) TriggerUserTrafficSync(userID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("用户流量同步过程中发生panic",
					zap.Uint("userID", userID),
					zap.String("reason", reason),
					zap.Any("panic", r))
			}
		}()

		// 检查服务是否已取消
		select {
		case <-s.ctx.Done():
			global.APP_LOG.Info("用户流量同步已取消",
				zap.Uint("userID", userID),
				zap.String("reason", reason))
			return
		default:
		}

		global.APP_LOG.Info("触发用户流量同步",
			zap.Uint("userID", userID),
			zap.String("reason", reason))

		// 创建带超时的context
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
		defer cancel()

		// 使用三层级流量限制服务检查流量限制
		if _, err := s.checkUserTrafficLimitWithContext(ctx, userID); err != nil {
			global.APP_LOG.Error("同步用户流量失败",
				zap.Uint("userID", userID),
				zap.String("reason", reason),
				zap.Error(err))
			return
		}

		global.APP_LOG.Debug("用户流量同步完成",
			zap.Uint("userID", userID),
			zap.String("reason", reason))
	}()
}

// checkUserTrafficLimitWithContext 带context的用户流量限制检查
func (s *SyncTriggerService) checkUserTrafficLimitWithContext(ctx context.Context, userID uint) (interface{}, error) {
	// 检查context是否已取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return s.threeTierService.CheckUserTrafficLimit(userID)
}

// TriggerProviderTrafficSync 触发Provider所有实例的流量同步
func (s *SyncTriggerService) TriggerProviderTrafficSync(providerID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("Provider流量同步过程中发生panic",
					zap.Uint("providerID", providerID),
					zap.String("reason", reason),
					zap.Any("panic", r))
			}
		}()

		// 检查服务是否已取消
		select {
		case <-s.ctx.Done():
			global.APP_LOG.Info("Provider流量同步已取消",
				zap.Uint("providerID", providerID),
				zap.String("reason", reason))
			return
		default:
		}

		global.APP_LOG.Info("触发Provider流量同步",
			zap.Uint("providerID", providerID),
			zap.String("reason", reason))

		// 检查Provider是否启用了流量控制
		var p provider.Provider
		if err := global.APP_DB.Select("enable_traffic_control").First(&p, providerID).Error; err != nil {
			global.APP_LOG.Error("查询Provider失败",
				zap.Uint("providerID", providerID),
				zap.String("reason", reason),
				zap.Error(err))
			return
		}

		// 如果未启用流量控制，跳过同步
		if !p.EnableTrafficControl {
			global.APP_LOG.Debug("Provider未启用流量控制，跳过流量同步",
				zap.Uint("providerID", providerID),
				zap.String("reason", reason))
			return
		}

		// 创建带超时的context
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
		defer cancel()

		// 使用三层级流量限制服务检查Provider流量限制
		if _, err := s.checkProviderTrafficLimitWithContext(ctx, providerID); err != nil {
			global.APP_LOG.Error("同步Provider流量失败",
				zap.Uint("providerID", providerID),
				zap.String("reason", reason),
				zap.Error(err))
			return
		}

		global.APP_LOG.Debug("Provider流量同步完成",
			zap.Uint("providerID", providerID),
			zap.String("reason", reason))
	}()
}

// checkProviderTrafficLimitWithContext 带context的Provider流量限制检查
func (s *SyncTriggerService) checkProviderTrafficLimitWithContext(ctx context.Context, providerID uint) (interface{}, error) {
	// 检查context是否已取消
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	return s.threeTierService.CheckProviderTrafficLimit(providerID)
}

// TriggerDelayedInstanceTrafficSync 延迟触发实例流量同步（用于实例启动后等待稳定）
func (s *SyncTriggerService) TriggerDelayedInstanceTrafficSync(instanceID uint, delay time.Duration, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("延迟流量同步过程中发生panic",
					zap.Uint("instanceID", instanceID),
					zap.Duration("delay", delay),
					zap.String("reason", reason),
					zap.Any("panic", r))
			}
		}()

		global.APP_LOG.Info("计划延迟触发实例流量同步",
			zap.Uint("instanceID", instanceID),
			zap.Duration("delay", delay),
			zap.String("reason", reason))

		// 使用Timer避免内存泄漏
		timer := time.NewTimer(delay)
		defer timer.Stop()

		select {
		case <-timer.C:
			// 延迟结束，执行流量同步
			s.TriggerInstanceTrafficSync(instanceID, reason+" (延迟触发)")
		case <-s.ctx.Done():
			// 服务被取消
			global.APP_LOG.Info("延迟流量同步已取消",
				zap.Uint("instanceID", instanceID),
				zap.Duration("delay", delay))
			return
		}
	}()
}
