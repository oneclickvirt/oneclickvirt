package task

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// ProviderPoolManager Provider工作池管理器
type ProviderPoolManager struct {
	pools      sync.Map // map[uint]*ProviderWorkerPool
	count      atomic.Int64
	lastAccess sync.Map // map[uint]time.Time 记录最后访问时间
	createdAt  sync.Map // map[uint]time.Time 记录创建时间（用于强制过期）
}

// NewProviderPoolManager 创建Provider工作池管理器
func NewProviderPoolManager() *ProviderPoolManager {
	return &ProviderPoolManager{}
}

// GetOrCreate 获取或创建Provider工作池
func (m *ProviderPoolManager) GetOrCreate(providerID uint, concurrency int, taskService *TaskService) *ProviderWorkerPool {
	// 更新最后访问时间
	m.lastAccess.Store(providerID, time.Now())

	// 快速路径：工作池已存在
	if value, ok := m.pools.Load(providerID); ok {
		pool := value.(*ProviderWorkerPool)
		// 检查并发数是否需要调整
		if pool.WorkerCount == concurrency {
			return pool
		}
		// 需要调整并发数，关闭旧池并创建新池
		pool.Cancel()
		m.pools.Delete(providerID)
		m.count.Add(-1)
	}

	// 创建新的工作池
	ctx, cancel := context.WithCancel(global.APP_SHUTDOWN_CONTEXT)

	queueSize := concurrency * 2
	if queueSize > maxTaskQueueSize {
		queueSize = maxTaskQueueSize
	}

	pool := &ProviderWorkerPool{
		ProviderID:  providerID,
		TaskQueue:   make(chan TaskRequest, queueSize),
		WorkerCount: concurrency,
		Ctx:         ctx,
		Cancel:      cancel,
		TaskService: taskService,
	}

	// 启动工作者
	for i := 0; i < concurrency; i++ {
		go pool.worker(i)
	}

	m.pools.Store(providerID, pool)
	m.createdAt.Store(providerID, time.Now()) // 记录创建时间
	m.count.Add(1)

	global.APP_LOG.Info("创建Provider工作池",
		zap.Uint("providerId", providerID),
		zap.Int("concurrency", concurrency))

	return pool
}

// Delete 删除Provider工作池（完全原子性同步清理所有相关sync.Map）
func (m *ProviderPoolManager) Delete(providerID uint) {
	// 原子性操作：从所有sync.Map中删除（防止孤立条目）
	value, hadPool := m.pools.LoadAndDelete(providerID)
	m.lastAccess.Delete(providerID)
	m.createdAt.Delete(providerID)

	if hadPool {
		pool := value.(*ProviderWorkerPool)

		// 更新计数器
		m.count.Add(-1)

		// 关闭工作池（可能阻塞，但已经从所有map中删除）
		pool.Cancel()

		global.APP_LOG.Info("原子性删除Provider工作池及所有相关资源",
			zap.Uint("providerId", providerID),
			zap.Int("workerCount", pool.WorkerCount),
			zap.Int("queueSize", len(pool.TaskQueue)))
	} else {
		global.APP_LOG.Debug("工作池不存在，已执行防御性清理",
			zap.Uint("providerId", providerID))
	}
}

// CleanupIdle 清理空闲的工作池
func (m *ProviderPoolManager) CleanupIdle(idleTimeout time.Duration) int {
	now := time.Now()
	cleaned := 0
	warned := 0
	maxLifetime := 4 * time.Hour // 工作池最大存活4小时（强制过期）

	m.lastAccess.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		lastAccess := value.(time.Time)

		if poolValue, ok := m.pools.Load(providerID); ok {
			pool := poolValue.(*ProviderWorkerPool)
			queueLen := len(pool.TaskQueue)
			queueCap := cap(pool.TaskQueue)

			shouldCleanup := false
			reason := ""

			// 检查队列容量是否接近上限
			if queueLen > int(float64(queueCap)*0.8) {
				global.APP_LOG.Warn("Provider工作池队列接近上限",
					zap.Uint("providerId", providerID),
					zap.Int("queueLen", queueLen),
					zap.Int("queueCap", queueCap))
				warned++
			}

			// 检查1: 空闲超时且队列为空
			if now.Sub(lastAccess) > idleTimeout && queueLen == 0 {
				shouldCleanup = true
				reason = "idle_timeout"
			}

			// 检查2: 强制过期（防止活跃工作池永不释放）
			if !shouldCleanup {
				if createdAtValue, ok := m.createdAt.Load(providerID); ok {
					createdAt := createdAtValue.(time.Time)
					if now.Sub(createdAt) > maxLifetime && queueLen == 0 {
						shouldCleanup = true
						reason = "max_lifetime"
					}
				}
			}

			if shouldCleanup {
				m.Delete(providerID)
				cleaned++
				global.APP_LOG.Info("清理Provider工作池",
					zap.Uint("providerId", providerID),
					zap.String("reason", reason),
					zap.Duration("idleTime", now.Sub(lastAccess)))
			}
		}

		return true
	})

	if warned > 0 {
		global.APP_LOG.Info("工作池队列容量检查完成",
			zap.Int("warned", warned),
			zap.Int("cleaned", cleaned))
	}

	// 执行孤立条目清理（防御性编程，防止内存泄漏）
	m.cleanupOrphaned()

	return cleaned
}

// cleanupOrphaned 清理孤立的sync.Map条目（防御性编程，防止内存泄漏）
func (m *ProviderPoolManager) cleanupOrphaned() {
	// 收集pools中存在的所有providerID
	validIDs := make(map[uint]bool)
	m.pools.Range(func(key, value interface{}) bool {
		validIDs[key.(uint)] = true
		return true
	})

	// 清理lastAccess中的孤立条目
	orphanedLastAccess := 0
	m.lastAccess.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		if !validIDs[providerID] {
			m.lastAccess.Delete(providerID)
			orphanedLastAccess++
		}
		return true
	})

	// 清理createdAt中的孤立条目
	orphanedCreatedAt := 0
	m.createdAt.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		if !validIDs[providerID] {
			m.createdAt.Delete(providerID)
			orphanedCreatedAt++
		}
		return true
	})

	if orphanedLastAccess > 0 || orphanedCreatedAt > 0 {
		global.APP_LOG.Warn("清理Provider工作池孤立条目（防止内存泄漏）",
			zap.Int("orphanedLastAccess", orphanedLastAccess),
			zap.Int("orphanedCreatedAt", orphanedCreatedAt))
	}
}

// CleanupDeleted 清理已删除的Provider工作池
func (m *ProviderPoolManager) CleanupDeleted(validIDs []uint) int {
	validSet := make(map[uint]bool, len(validIDs))
	for _, id := range validIDs {
		validSet[id] = true
	}

	cleaned := 0
	m.pools.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		if !validSet[providerID] {
			m.Delete(providerID)
			cleaned++
		}
		return true
	})

	if cleaned > 0 {
		global.APP_LOG.Info("清理已删除Provider的工作池",
			zap.Int("cleaned", cleaned))
	}

	return cleaned
}

// Count 返回当前工作池数量
func (m *ProviderPoolManager) Count() int64 {
	return m.count.Load()
}

// CancelAll 取消所有工作池
func (m *ProviderPoolManager) CancelAll() {
	m.pools.Range(func(key, value interface{}) bool {
		pool := value.(*ProviderWorkerPool)
		pool.Cancel()
		return true
	})
}
