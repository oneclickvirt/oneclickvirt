package scheduler

import (
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
	isCollecting     atomic.Bool // 使用atomic避免锁
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

// GetOrCreate 获取或创建Provider状态
func (m *ProviderStateManager) GetOrCreate(providerID uint) *ProviderState {
	// 快速路径：状态已存在
	if value, ok := m.states.Load(providerID); ok {
		state := value.(*ProviderState)
		state.mu.Lock()
		state.lastAccess = time.Now()
		state.mu.Unlock()
		return state
	}

	// 慢速路径：创建新状态
	now := time.Now()
	state := &ProviderState{
		lastCollect:      time.Time{},
		createdAt:        now, // 记录创建时间
		currentRoundID:   0,
		collectStartTime: time.Time{},
		lastAccess:       now,
	}
	state.isCollecting.Store(false)

	// LoadOrStore确保并发安全
	actual, loaded := m.states.LoadOrStore(providerID, state)
	if !loaded {
		m.stateCount.Add(1)
	}

	return actual.(*ProviderState)
}

// Delete 删除Provider状态
func (m *ProviderStateManager) Delete(providerID uint) {
	if _, loaded := m.states.LoadAndDelete(providerID); loaded {
		m.stateCount.Add(-1)
	}
}

// CleanupExpired 清理过期状态（支持强制过期）
func (m *ProviderStateManager) CleanupExpired(threshold time.Duration) int {
	now := time.Now()
	cleaned := 0
	maxLifetime := 6 * time.Hour // 状态最大存活6小时（强制过期）

	m.states.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		state := value.(*ProviderState)

		state.mu.RLock()
		isExpired := now.Sub(state.lastAccess) > threshold
		isCollecting := state.isCollecting.Load()
		lifetime := now.Sub(state.createdAt)
		forcedExpire := lifetime > maxLifetime
		state.mu.RUnlock()

		shouldDelete := false
		reason := ""

		// 过期且未在采集中
		if isExpired && !isCollecting {
			shouldDelete = true
			reason = "idle_timeout"
		}

		// 强制过期（防止长期活跃的状态永不释放）
		if !shouldDelete && forcedExpire && !isCollecting {
			shouldDelete = true
			reason = "max_lifetime"
		}

		if shouldDelete {
			m.Delete(providerID)
			cleaned++
			if global.APP_LOG != nil {
				global.APP_LOG.Debug("清理Provider状态",
					zap.Uint("providerID", providerID),
					zap.String("reason", reason),
					zap.Duration("lifetime", lifetime))
			}
		}

		return true // 继续遍历
	})

	if cleaned > 0 {
		global.APP_LOG.Info("清理过期provider状态",
			zap.Int("cleaned", cleaned),
			zap.Int64("remaining", m.stateCount.Load()))
	}

	return cleaned
}

// CleanupDeleted 清理已删除的Provider状态（需要validIDs列表）
func (m *ProviderStateManager) CleanupDeleted(validIDs []uint) int {
	// 构建有效ID集合
	validSet := make(map[uint]bool, len(validIDs))
	for _, id := range validIDs {
		validSet[id] = true
	}

	cleaned := 0
	m.states.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		if !validSet[providerID] {
			state := value.(*ProviderState)
			// 只删除未在采集中的状态
			if !state.isCollecting.Load() {
				m.Delete(providerID)
				cleaned++
			}
		}
		return true
	})

	if cleaned > 0 {
		global.APP_LOG.Info("清理已删除provider的状态",
			zap.Int("cleaned", cleaned),
			zap.Int64("remaining", m.stateCount.Load()))
	}

	return cleaned
}

// Count 返回当前状态数量
func (m *ProviderStateManager) Count() int64 {
	return m.stateCount.Load()
}

// ResetIfCollectingTooLong 重置长时间采集中的状态（防止死锁）
func (m *ProviderStateManager) ResetIfCollectingTooLong(timeout time.Duration) int {
	now := time.Now()
	reset := 0

	m.states.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		state := value.(*ProviderState)

		if state.isCollecting.Load() {
			state.mu.RLock()
			elapsed := now.Sub(state.collectStartTime)
			roundID := state.currentRoundID
			state.mu.RUnlock()

			if elapsed > timeout {
				state.isCollecting.Store(false)
				global.APP_LOG.Error("Provider流量采集超时，强制解锁",
					zap.Uint("providerID", providerID),
					zap.Int64("roundID", roundID),
					zap.Duration("elapsed", elapsed))
				reset++
			}
		}

		return true
	})

	return reset
}

// StartCollecting 开始采集（返回是否成功获取锁）
func (s *ProviderState) StartCollecting() bool {
	return s.isCollecting.CompareAndSwap(false, true)
}

// FinishCollecting 完成采集
func (s *ProviderState) FinishCollecting() {
	s.isCollecting.Store(false)
}

// IsCollecting 检查是否正在采集
func (s *ProviderState) IsCollecting() bool {
	return s.isCollecting.Load()
}

// UpdateLastCollect 更新最后采集时间并返回新的roundID
func (s *ProviderState) UpdateLastCollect() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.lastCollect = time.Now()
	s.currentRoundID++
	s.collectStartTime = time.Now()

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
