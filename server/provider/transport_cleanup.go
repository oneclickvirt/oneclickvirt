package provider

import (
	"context"
	"net/http"
	"sync"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

var (
	// 全局transport清理器
	transportCleanupManager     *TransportCleanupManager
	transportCleanupManagerOnce sync.Once
)

// TransportCleanupManager 管理所有provider的transport清理
type TransportCleanupManager struct {
	transports  map[*http.Transport]transportMetadata // 带元数据的transport管理
	mu          sync.Mutex
	lastCleanup time.Time
	ctx         context.Context
	cancel      context.CancelFunc
}

// transportMetadata Transport元数据
type transportMetadata struct {
	providerID uint
	createdAt  time.Time
	lastAccess time.Time
}

// GetTransportCleanupManager 获取transport清理管理器单例
func GetTransportCleanupManager() *TransportCleanupManager {
	transportCleanupManagerOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		transportCleanupManager = &TransportCleanupManager{
			transports:  make(map[*http.Transport]transportMetadata),
			lastCleanup: time.Now(),
			ctx:         ctx,
			cancel:      cancel,
		}
		// 启动定期清理
		go transportCleanupManager.periodicCleanup()
		// 启动过期transport清理
		go transportCleanupManager.cleanupExpiredTransports()
	})
	return transportCleanupManager
}

// RegisterTransport 注册需要清理的transport（自动去重）
func (m *TransportCleanupManager) RegisterTransport(t *http.Transport) {
	if t == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已存在
	if _, exists := m.transports[t]; !exists {
		m.transports[t] = transportMetadata{
			createdAt:  time.Now(),
			lastAccess: time.Now(),
		}
	}
}

// RegisterTransportWithProvider 注册并关联providerID
func (m *TransportCleanupManager) RegisterTransportWithProvider(t *http.Transport, providerID uint) {
	if t == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	// 更新或创建元数据
	m.transports[t] = transportMetadata{
		providerID: providerID,
		createdAt:  time.Now(),
		lastAccess: time.Now(),
	}
}

// UnregisterTransport 注销transport（简化版）
func (m *TransportCleanupManager) UnregisterTransport(t *http.Transport) {
	if t == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.transports, t)
}

// CleanupProvider 清理指定provider的所有transport（简化为单一map遍历）
func (m *TransportCleanupManager) CleanupProvider(providerID uint) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanedCount := 0
	var toDelete []*http.Transport

	// 遍历所有transport，找到匹配的providerID
	for t, meta := range m.transports {
		if meta.providerID == providerID {
			toDelete = append(toDelete, t)
		}
	}

	// 批量删除和关闭
	for _, t := range toDelete {
		if t != nil {
			t.CloseIdleConnections()
		}
		delete(m.transports, t)
		cleanedCount++
	}

	if global.APP_LOG != nil && cleanedCount > 0 {
		global.APP_LOG.Debug("Provider Transport已完全清理",
			zap.Uint("providerID", providerID),
			zap.Int("cleaned", cleanedCount))
	}
}

// periodicCleanup 定期清理空闲连接（5分钟一次）
func (m *TransportCleanupManager) periodicCleanup() {
	defer func() {
		if r := recover(); r != nil {
			if global.APP_LOG != nil {
				global.APP_LOG.Error("Transport清理goroutine panic", zap.Any("panic", r))
			}
		}
	}()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			if global.APP_LOG != nil {
				global.APP_LOG.Info("Transport清理goroutine已停止")
			}
			return
		case <-ticker.C:
			m.mu.Lock()
			now := time.Now()
			// 每5分钟清理一次空闲连接
			if now.Sub(m.lastCleanup) >= 5*time.Minute {
				for t := range m.transports {
					if t != nil {
						t.CloseIdleConnections()
					}
				}
				m.lastCleanup = now
				if global.APP_LOG != nil {
					global.APP_LOG.Debug("Transport空闲连接已清理",
						zap.Int("count", len(m.transports)))
				}
			}
			m.mu.Unlock()
		}
	}
}

// cleanupExpiredTransports 清理过期的transport对象（30分钟未访问）
func (m *TransportCleanupManager) cleanupExpiredTransports() {
	// 确保ticker在panic时也能停止，防止goroutine泄漏
	ticker := time.NewTicker(10 * time.Minute) // 每10分钟检查一次
	defer func() {
		ticker.Stop()
		if r := recover(); r != nil && global.APP_LOG != nil {
			global.APP_LOG.Error("Transport过期清理goroutine panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.cleanupExpired()
		}
	}
}

// cleanupExpired 清理过期transport（简化版）
func (m *TransportCleanupManager) cleanupExpired() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	maxAge := 30 * time.Minute // 30分钟未访问视为过期
	cleaned := 0

	// 收集需要删除的transport
	var toDelete []*http.Transport
	for t, meta := range m.transports {
		// 30分钟未访问的transport视为过期
		if now.Sub(meta.lastAccess) > maxAge {
			toDelete = append(toDelete, t)
		}
	}

	// 删除过期transport
	for _, t := range toDelete {
		// 关闭连接
		if t != nil {
			t.CloseIdleConnections()
		}
		// 从transports map中删除
		delete(m.transports, t)
		cleaned++
	}

	if cleaned > 0 && global.APP_LOG != nil {
		global.APP_LOG.Info("清理过期Transport对象",
			zap.Int("cleaned", cleaned),
			zap.Int("remaining", len(m.transports)))
	}
}

// CleanupAll 清理所有已注册的transport
func (m *TransportCleanupManager) CleanupAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleaned := 0
	for t := range m.transports {
		if t != nil {
			t.CloseIdleConnections()
			cleaned++
		}
	}

	// 清空map
	m.transports = make(map[*http.Transport]transportMetadata)

	if cleaned > 0 {
		global.APP_LOG.Info("已清理所有Provider Transport连接",
			zap.Int("count", cleaned))
	}
}

// Stop 停止清理管理器
func (m *TransportCleanupManager) Stop() {
	if m.cancel != nil {
		m.cancel()
	}
	m.CleanupAll()
}

// Count 返回当前注册的transport数量
func (m *TransportCleanupManager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.transports)
}
