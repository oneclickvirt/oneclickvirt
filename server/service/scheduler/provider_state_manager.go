package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// ProviderState Provider流量采集状态
type ProviderState struct {
	lastCollect      time.Time
	createdAt        time.Time // 添加创建时间（用于强制过期）
	currentRoundID   int64
	isCollecting     bool
	retired          bool
	collectCancel    context.CancelFunc
	timeoutRequested bool
	collectStartTime time.Time
	lastAccess       time.Time
	mu               sync.RWMutex
}

// ProviderStateManager Provider状态管理器，使用sync.Map
type ProviderStateManager struct {
	states sync.Map // map[uint]*ProviderState
	// 统计信息（用于监控）
	stateCount atomic.Int64
}

// NewProviderStateManager 创建Provider状态管理器
func NewProviderStateManager() *ProviderStateManager {
	return &ProviderStateManager{}
}

// GetOrCreate returns a current entry; retired references cannot start work.
func (m *ProviderStateManager) GetOrCreate(providerID uint) *ProviderState {
	for {
		if value, ok := m.states.Load(providerID); ok {
			state := value.(*ProviderState)
			state.mu.Lock()
			retired := state.retired
			if !retired {
				state.lastAccess = time.Now()
			}
			state.mu.Unlock()
			if !retired {
				return state
			}
			continue
		}
		now := time.Now()
		state := &ProviderState{createdAt: now, lastAccess: now}
		if _, loaded := m.states.LoadOrStore(providerID, state); !loaded {
			m.stateCount.Add(1)
			return state
		}
	}
}

// Active work keeps its entry until exit, including after deletion or timeout.
func (m *ProviderStateManager) Delete(providerID uint) {
	if value, loaded := m.states.Load(providerID); loaded {
		state := value.(*ProviderState)
		if !m.deleteIdleState(providerID, state, nil) {
			state.mu.RLock()
			cancel := state.collectCancel
			state.mu.RUnlock()
			if cancel != nil {
				cancel()
			}
		}
	}
}

func (m *ProviderStateManager) deleteIdleState(providerID uint, state *ProviderState, eligible func(*ProviderState) bool) bool {
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.retired || state.isCollecting || (eligible != nil && !eligible(state)) {
		return false
	}
	state.retired = true
	if !m.states.CompareAndDelete(providerID, state) {
		return false
	}
	m.stateCount.Add(-1)
	return true
}

func (m *ProviderStateManager) CleanupExpired(threshold time.Duration) int {
	now := time.Now()
	cleaned := 0
	m.states.Range(func(key, value interface{}) bool {
		if m.deleteIdleState(key.(uint), value.(*ProviderState), func(state *ProviderState) bool {
			return now.Sub(state.lastAccess) > threshold || now.Sub(state.createdAt) > 6*time.Hour
		}) {
			cleaned++
		}
		return true
	})
	return cleaned
}

func (m *ProviderStateManager) CleanupDeleted(validIDs []uint) int {
	validSet := make(map[uint]bool, len(validIDs))
	for _, id := range validIDs {
		validSet[id] = true
	}
	cleaned := 0
	m.states.Range(func(key, value interface{}) bool {
		id := key.(uint)
		if !validSet[id] && m.deleteIdleState(id, value.(*ProviderState), nil) {
			cleaned++
		}
		return true
	})
	return cleaned
}

// Count 返回当前状态数量
func (m *ProviderStateManager) Count() int64 {
	return m.stateCount.Load()
}

// ResetIfCollectingTooLong requests cancellation once and retains ownership until exit.
func (m *ProviderStateManager) ResetIfCollectingTooLong(timeout time.Duration) int {
	now := time.Now()
	requested := 0
	m.states.Range(func(key, value interface{}) bool {
		state := value.(*ProviderState)
		state.mu.Lock()
		elapsed := now.Sub(state.collectStartTime)
		roundID := state.currentRoundID
		requestCancel := state.isCollecting && !state.timeoutRequested && elapsed > timeout
		cancel := state.collectCancel
		if requestCancel {
			state.timeoutRequested = true
		}
		state.mu.Unlock()
		if requestCancel {
			if cancel != nil {
				cancel()
			}
			if global.APP_LOG != nil {
				global.APP_LOG.Error("Provider流量采集超时，已请求取消并等待退出",
					zap.Uint("providerID", key.(uint)), zap.Int64("roundID", roundID), zap.Duration("elapsed", elapsed))
			}
			requested++
		}
		return true
	})
	return requested
}

func (s *ProviderState) StartCollecting() bool {
	_, started := s.StartCollectingContext(context.Background(), 5*time.Minute)
	return started
}

func (s *ProviderState) StartCollectingContext(parent context.Context, timeout time.Duration) (context.Context, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.retired || s.isCollecting {
		return nil, false
	}
	if parent == nil {
		parent = context.Background()
	}
	if parent.Err() != nil {
		return nil, false
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	s.isCollecting = true
	s.collectCancel = cancel
	s.timeoutRequested = false
	s.collectStartTime = time.Now()
	return ctx, true
}

func (s *ProviderState) FinishCollecting() {
	s.mu.Lock()
	cancel := s.collectCancel
	s.collectCancel = nil
	s.isCollecting = false
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (s *ProviderState) IsCollecting() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.isCollecting
}

// UpdateLastCollect 更新最后采集时间并返回新的roundID
func (s *ProviderState) UpdateLastCollect() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastCollect = time.Now()
	s.currentRoundID++

	return s.currentRoundID
}

// GetLastCollect 获取最后采集时间
func (s *ProviderState) GetLastCollect() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastCollect
}

// GetCurrentRoundID 获取当前轮次ID
func (s *ProviderState) GetCurrentRoundID() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.currentRoundID
}
