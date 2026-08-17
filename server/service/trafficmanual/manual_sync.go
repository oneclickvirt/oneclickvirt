// Package trafficmanual coordinates operator-initiated traffic refreshes.
// It is intentionally a leaf package: it may call both Agent and traffic
// services, while lifecycle traffic code remains free of that dependency.
package trafficmanual

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"
	agentService "oneclickvirt/service/agent"
	trafficService "oneclickvirt/service/traffic"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

type SyncService struct {
	threeTierService *trafficService.ThreeTierLimitService
	ctx              context.Context
	cancel           context.CancelFunc
	wg               sync.WaitGroup
}

func NewSyncService() *SyncService {
	ctx, cancel := context.WithCancel(context.Background())
	return &SyncService{
		threeTierService: trafficService.NewThreeTierLimitService(),
		ctx:              ctx,
		cancel:           cancel,
	}
}

func (s *SyncService) Shutdown(timeout time.Duration) error {
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
		return nil
	case <-timer.C:
		s.cancel()
		return context.DeadlineExceeded
	}
}

func (s *SyncService) TriggerInstanceTrafficSync(instanceID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.logPanic("手动实例流量同步", reason)
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
		defer cancel()

		var instance providerModel.Instance
		if err := global.APP_DB.Select("user_id", "provider_id").First(&instance, instanceID).Error; err != nil {
			global.APP_LOG.Warn("手动同步实例流量失败：获取实例信息失败", zap.Uint("instanceID", instanceID), zap.Error(err))
			return
		}
		s.syncAgentOrLog(ctx, []uint{instance.ProviderID}, "手动同步实例流量", reason)
		if instance.UserID == 0 {
			return
		}
		if _, err := s.threeTierService.CheckUserTrafficLimit(instance.UserID); err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return
			}
			global.APP_LOG.Error("手动同步实例流量失败：限额检查失败", zap.Uint("instanceID", instanceID), zap.Error(err))
		}
	}()
}

func (s *SyncService) TriggerUserTrafficSync(userID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.logPanic("手动用户流量同步", reason)
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Minute)
		defer cancel()

		providerIDs, err := s.providerIDsForUsers([]uint{userID})
		if err != nil {
			global.APP_LOG.Error("手动同步用户流量失败：查询Provider失败", zap.Uint("userID", userID), zap.Error(err))
		} else {
			s.syncAgentOrLog(ctx, providerIDs, "手动同步用户流量", reason)
		}
		if _, err := s.threeTierService.CheckUserTrafficLimit(userID); err != nil {
			global.APP_LOG.Error("手动同步用户流量失败：限额检查失败", zap.Uint("userID", userID), zap.Error(err))
		}
	}()
}

// TriggerUsersTrafficSync collects each selected node once before checking the
// selected users with batched statistics queries. No remote call is made while
// a database transaction is open.
func (s *SyncService) TriggerUsersTrafficSync(userIDs []uint, reason string) {
	userIDs = uniqueSortedUserIDs(userIDs)
	if len(userIDs) == 0 {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.logPanic("批量用户流量同步", reason)
		ctx, cancel := context.WithTimeout(s.ctx, 15*time.Minute)
		defer cancel()

		providerIDs, err := s.providerIDsForUsers(userIDs)
		if err != nil {
			global.APP_LOG.Error("批量手动同步用户流量失败：查询Provider失败", zap.Int("userCount", len(userIDs)), zap.Error(err))
		} else {
			s.syncAgentOrLog(ctx, providerIDs, "批量手动同步用户流量", reason)
		}
		if err := s.threeTierService.CheckUsersTrafficLimit(ctx, userIDs); err != nil {
			global.APP_LOG.Error("批量手动同步用户流量失败：限额检查失败", zap.Int("userCount", len(userIDs)), zap.Error(err))
		}
	}()
}

func (s *SyncService) TriggerProviderTrafficSync(providerID uint, reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.logPanic("手动Provider流量同步", reason)
		ctx, cancel := context.WithTimeout(s.ctx, 10*time.Minute)
		defer cancel()

		var provider providerModel.Provider
		if err := global.APP_DB.Select("enable_traffic_control").First(&provider, providerID).Error; err != nil {
			global.APP_LOG.Error("手动同步Provider流量失败：查询Provider失败", zap.Uint("providerID", providerID), zap.Error(err))
			return
		}
		if !provider.EnableTrafficControl {
			return
		}
		s.syncAgentOrLog(ctx, []uint{providerID}, "手动同步Provider流量", reason)
		if _, err := s.threeTierService.CheckProviderTrafficLimit(providerID); err != nil {
			global.APP_LOG.Error("手动同步Provider流量失败：限额检查失败", zap.Uint("providerID", providerID), zap.Error(err))
		}
	}()
}

func (s *SyncService) TriggerAllTrafficSync(reason string) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer s.logPanic("手动全系统流量同步", reason)
		ctx, cancel := context.WithTimeout(s.ctx, 30*time.Minute)
		defer cancel()

		// nil is the explicit all-provider scope. A concrete empty slice is used
		// for a selected user set without matching instances.
		s.syncAgentOrLog(ctx, nil, "手动全系统流量同步", reason)
		if err := s.threeTierService.CheckAllTrafficLimits(ctx); err != nil {
			global.APP_LOG.Error("手动全系统流量同步失败：限额检查失败", zap.Error(err))
		}
	}()
}

func (s *SyncService) syncAgentOrLog(ctx context.Context, providerIDs []uint, operation, reason string) {
	if err := agentService.SyncConfiguredAgentTraffic(ctx, global.APP_DB, providerIDs); err != nil {
		// Keep limit state convergent from committed data when an Agent is
		// transiently unavailable. The scheduler retries the remote pull later.
		global.APP_LOG.Error(operation+"失败：Agent拉取失败，使用已落库计数继续检查", zap.String("reason", reason), zap.Error(err))
	}
}

func (s *SyncService) providerIDsForUsers(userIDs []uint) ([]uint, error) {
	userIDs = uniqueSortedUserIDs(userIDs)
	providerIDs := make([]uint, 0)
	if len(userIDs) == 0 {
		return providerIDs, nil
	}
	if err := global.APP_DB.Model(&providerModel.Instance{}).
		Where("user_id IN ?", userIDs).
		Distinct("provider_id").
		Pluck("provider_id", &providerIDs).Error; err != nil {
		return nil, fmt.Errorf("list user providers: %w", err)
	}
	return uniqueSortedProviderIDs(providerIDs), nil
}

func (s *SyncService) logPanic(operation, reason string) {
	if recovered := recover(); recovered != nil {
		global.APP_LOG.Error(operation+"过程中发生panic", zap.String("reason", reason), zap.Any("panic", recovered))
	}
}

func uniqueSortedUserIDs(values []uint) []uint {
	return uniqueSortedIDs(values)
}

func uniqueSortedProviderIDs(values []uint) []uint {
	result := uniqueSortedIDs(values)
	if result == nil {
		return []uint{}
	}
	return result
}

func uniqueSortedIDs(values []uint) []uint {
	if len(values) == 0 {
		return nil
	}
	seen := make(map[uint]struct{}, len(values))
	result := make([]uint, 0, len(values))
	for _, value := range values {
		if value == 0 {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
