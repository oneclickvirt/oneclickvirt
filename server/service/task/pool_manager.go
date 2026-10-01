package task

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"

	"go.uber.org/zap"
)

// ProviderPoolManager Provider工作池管理器
type ProviderPoolManager struct {
	pools      sync.Map // map[uint]*ProviderWorkerPool
	count      atomic.Int64
	lastAccess sync.Map   // map[uint]time.Time 记录最后访问时间
	createdAt  sync.Map   // map[uint]time.Time 记录创建时间（用于强制过期）
	createMu   sync.Mutex // 保护工作池创建的互斥锁（防止并发创建导致goroutine泄漏）
	closed     bool       // prevents pool creation once shutdown starts
}

// NewProviderPoolManager 创建Provider工作池管理器
func NewProviderPoolManager() *ProviderPoolManager {
	return &ProviderPoolManager{}
}

// GetOrCreate 获取或创建 Provider 工作池（并发安全）
func (m *ProviderPoolManager) GetOrCreate(providerID uint, concurrency int, taskService *TaskService) *ProviderWorkerPool {
	for {
		m.lastAccess.Store(providerID, time.Now())
		value, exists := m.pools.Load(providerID)
		if !exists {
			m.createMu.Lock()
			if m.closed {
				m.createMu.Unlock()
				return nil
			}
			// Another caller may have created the pool while this caller was
			// waiting for createMu. Recheck before allocating workers.
			if _, alreadyCreated := m.pools.Load(providerID); alreadyCreated {
				m.createMu.Unlock()
				continue
			}
			pool := m.createPoolLocked(providerID, concurrency, taskService)
			m.createMu.Unlock()
			return pool
		}

		pool := value.(*ProviderWorkerPool)
		pool.submitMu.Lock()
		matching := pool.Ctx.Err() == nil && pool.WorkerCount == concurrency && !pool.retireWhenIdle
		localBusy := pool.hasLocalWork()
		pool.submitMu.Unlock()

		if matching {
			// Reopen a matching pool only after serializing with resize/delete.
			m.createMu.Lock()
			if m.closed {
				m.createMu.Unlock()
				return nil
			}
			current, stillPresent := m.pools.Load(providerID)
			if stillPresent && current == pool {
				pool.submitMu.Lock()
				if pool.Ctx.Err() == nil && pool.WorkerCount == concurrency && !pool.retireWhenIdle {
					pool.closed = false
					pool.submitMu.Unlock()
					m.createMu.Unlock()
					return pool
				}
				pool.submitMu.Unlock()
			}
			m.createMu.Unlock()
			continue
		}

		// The durable-state check can be slow (and includes async post-processing
		// whose worker has already returned), so perform it before createMu. A
		// later identity/local-work recheck makes this conservative if the pool
		// changes while the query is in flight.
		durableBusy := localBusy
		if !localBusy && pool.TaskService != nil && global.APP_DB != nil {
			durableBusy = pool.hasWork()
		}

		m.createMu.Lock()
		if m.closed {
			m.createMu.Unlock()
			return nil
		}
		current, stillPresent := m.pools.Load(providerID)
		if !stillPresent || current != pool {
			m.createMu.Unlock()
			continue
		}
		pool.submitMu.Lock()
		if pool.retireWhenIdle {
			pool.closed = true
			pool.submitMu.Unlock()
			m.createMu.Unlock()
			return pool
		}
		if pool.Ctx.Err() == nil && pool.WorkerCount == concurrency {
			pool.closed = false
			pool.submitMu.Unlock()
			m.createMu.Unlock()
			return pool
		}
		if pool.hasLocalWork() || durableBusy {
			// Stop accepting new work during a resize, but let accepted and
			// asynchronous work finish before rebuilding this pool.
			pool.closed = true
			pool.submitMu.Unlock()
			m.createMu.Unlock()
			return pool
		}
		pool.closed = true
		pool.submitMu.Unlock()

		// The pool is empty and its identity is still current. Cancel and
		// replace it while holding createMu so no concurrent creator can lose
		// the provider slot.
		pool.Cancel()
		m.pools.Delete(providerID)
		m.lastAccess.Delete(providerID)
		m.createdAt.Delete(providerID)
		m.count.Add(-1)
		newPool := m.createPoolLocked(providerID, concurrency, taskService)
		m.createMu.Unlock()
		return newPool
	}
}

// createPoolLocked allocates and starts a Provider pool. The caller must hold
// createMu; keeping creation in one helper makes the two-stage resize path
// above easier to audit.
func (m *ProviderPoolManager) createPoolLocked(providerID uint, concurrency int, taskService *TaskService) *ProviderWorkerPool {
	base := global.APP_SHUTDOWN_CONTEXT
	if taskService != nil && taskService.ctx != nil {
		base = taskService.ctx
	}
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	queueSize := concurrency * 2
	if queueSize > maxTaskQueueSize {
		queueSize = maxTaskQueueSize
	}

	pool := &ProviderWorkerPool{
		ProviderID:  providerID,
		TaskQueue:   make(chan TaskRequest, queueSize),
		WorkerCount: concurrency,
		Ctx:         ctx,
		TaskService: taskService,
	}
	atomic.StoreInt64(&pool.liveWorkers, int64(concurrency))
	pool.Cancel = func() {
		pool.submitMu.Lock()
		pool.closed = true
		cancel()
		pool.submitMu.Unlock()
	}

	m.pools.Store(providerID, pool)
	m.createdAt.Store(providerID, time.Now())
	m.lastAccess.Store(providerID, time.Now())
	m.count.Add(1)
	for i := 0; i < concurrency; i++ {
		go func(workerID int) {
			defer atomic.AddInt64(&pool.liveWorkers, -1)
			pool.worker(workerID)
		}(i)
	}

	global.APP_LOG.Info("创建Provider工作池",
		zap.Uint("providerId", providerID),
		zap.Int("concurrency", concurrency))
	return pool
}

// Delete 删除Provider工作池（完全原子性同步清理所有相关sync.Map）
func (m *ProviderPoolManager) Delete(providerID uint) {
	m.createMu.Lock()
	defer m.createMu.Unlock()
	m.deleteLocked(providerID)
}

func (m *ProviderPoolManager) deleteLocked(providerID uint) {
	value, hadPool := m.pools.Load(providerID)
	if hadPool {
		pool := value.(*ProviderWorkerPool)
		pool.submitMu.Lock()
		if pool.hasLocalWork() {
			// A deleted Provider can still own an in-flight call. Stop admission,
			// but keep the pool reachable until its accepted work has completed.
			pool.closed = true
			pool.retireWhenIdle = true
			pool.submitMu.Unlock()
			return
		}
		pool.closed = true
		pool.submitMu.Unlock()
	}

	// createMu is held by every caller, so this pool cannot be replaced between
	// the idle check and removal.
	value, hadPool = m.pools.LoadAndDelete(providerID)
	m.lastAccess.Delete(providerID)
	m.createdAt.Delete(providerID)

	if hadPool {
		pool := value.(*ProviderWorkerPool)

		// 更新计数器
		m.count.Add(-1)

		// 关闭工作池（可能阻塞，但已经从所有map中删除）
		pool.Cancel()

		global.APP_LOG.Debug("原子性删除Provider工作池及所有相关资源",
			zap.Uint("providerId", providerID),
			zap.Int("workerCount", pool.WorkerCount),
			zap.Int("queueSize", len(pool.TaskQueue)))
	} else {
		global.APP_LOG.Debug("工作池不存在，已执行防御性清理",
			zap.Uint("providerId", providerID))
	}
}

// CleanupIdle 清理空闲的工作池。调用方可以传入本轮已经批量读取的忙碌
// Provider 集合，避免按工作池逐个查询任务形成定时 N+1。
func (m *ProviderPoolManager) CleanupIdle(idleTimeout time.Duration, busyProviderSets ...map[uint]struct{}) int {
	var busyProviders map[uint]struct{}
	if len(busyProviderSets) > 0 {
		busyProviders = busyProviderSets[0]
	} else {
		busyProviders = make(map[uint]struct{})
		if global.APP_DB != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var ids []uint
			if err := global.APP_DB.WithContext(ctx).Model(&adminModel.Task{}).
				Where("status IN ?", mainTaskInFlightStatuses).Distinct().Pluck("provider_id", &ids).Error; err != nil {
				return 0
			}
			for _, id := range ids {
				busyProviders[id] = struct{}{}
			}
		}
	}
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
			activeCount := atomic.LoadInt64(&pool.activeCount)

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

			// 检查1: 空闲超时且队列为空且没有正在执行的任务。
			// 长任务执行期间队列会为空，不能把它当作空闲池关闭。
			if now.Sub(lastAccess) > idleTimeout && queueLen == 0 && activeCount == 0 {
				shouldCleanup = true
				reason = "idle_timeout"
			}

			// 检查2: 强制过期（防止活跃工作池永不释放）
			if !shouldCleanup {
				if createdAtValue, ok := m.createdAt.Load(providerID); ok {
					createdAt := createdAtValue.(time.Time)
					if now.Sub(createdAt) > maxLifetime && queueLen == 0 && activeCount == 0 {
						shouldCleanup = true
						reason = "max_lifetime"
					}
				}
			}

			if shouldCleanup {
				m.createMu.Lock()
				// Recheck identity and activity after taking the same lock as GetOrCreate.
				if current, ok := m.pools.Load(providerID); ok && current == pool {
					pool.submitMu.Lock()
					idle := !pool.hasWork(busyProviders)
					if idle {
						pool.closed = true
					}
					pool.submitMu.Unlock()
					if idle {
						m.deleteLocked(providerID)
						cleaned++
					}
				}
				m.createMu.Unlock()
				global.APP_LOG.Debug("清理Provider工作池",
					zap.Uint("providerId", providerID),
					zap.String("reason", reason),
					zap.Int64("activeCount", activeCount),
					zap.Duration("idleTime", now.Sub(lastAccess)))
			}
		}

		return true
	})

	if warned > 0 {
		global.APP_LOG.Debug("工作池队列容量检查完成",
			zap.Int("warned", warned),
			zap.Int("cleaned", cleaned))
	}

	// 执行孤立条目清理（防御性编程，防止内存泄漏）
	m.cleanupOrphaned()

	return cleaned
}

// cleanupOrphaned 清理孤立的sync.Map条目（防御性编程，防止内存泄漏）
func (m *ProviderPoolManager) cleanupOrphaned() {
	m.createMu.Lock()
	defer m.createMu.Unlock()
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
		global.APP_LOG.Debug("清理已删除Provider的工作池",
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

// CancelAllAndWait closes admission, cancels every pool and waits until queued,
// active and asynchronous task ownership has drained or ctx expires.
func (m *ProviderPoolManager) CancelAllAndWait(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.createMu.Lock()
	m.closed = true
	pools := make([]*ProviderWorkerPool, 0)
	m.pools.Range(func(_, value interface{}) bool {
		pools = append(pools, value.(*ProviderWorkerPool))
		return true
	})
	for _, pool := range pools {
		if pool.Cancel != nil {
			pool.Cancel()
		} else {
			pool.submitMu.Lock()
			pool.closed = true
			pool.submitMu.Unlock()
		}
	}
	m.createMu.Unlock()

	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		allStopped := true
		for _, pool := range pools {
			pool.submitMu.Lock()
			submitting := pool.submitting
			pool.submitMu.Unlock()
			if submitting != 0 ||
				atomic.LoadInt64(&pool.liveWorkers) != 0 ||
				atomic.LoadInt64(&pool.outstanding) != 0 ||
				atomic.LoadInt64(&pool.activeCount) != 0 ||
				atomic.LoadInt64(&pool.ownedContexts) != 0 ||
				len(pool.TaskQueue) != 0 {
				allStopped = false
				break
			}
		}
		if allStopped {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Keep the old concurrency until accepted work finishes, including async
// post-processing. Reconfiguring must not cancel a destructive operation.
func (pool *ProviderWorkerPool) hasWork(busyProviderSets ...map[uint]struct{}) bool {
	if pool.hasLocalWork() {
		return true
	}
	if len(busyProviderSets) > 0 {
		if _, ok := busyProviderSets[0][pool.ProviderID]; ok {
			return true
		}
		// The caller supplied a batched snapshot. Do not issue a fallback query
		// for this pool, otherwise cleanup regresses to one query per Provider.
		return false
	}
	if pool.TaskService != nil && global.APP_DB != nil {
		var count int64
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		err := global.APP_DB.WithContext(ctx).Model(&adminModel.Task{}).Where("provider_id = ? AND status IN ?", pool.ProviderID, []string{mainTaskStatusProcessing, mainTaskStatusRunning, mainTaskStatusCancelling}).Count(&count).Error
		return err != nil || count > 0
	}
	return false
}

func (pool *ProviderWorkerPool) hasLocalWork() bool {
	return atomic.LoadInt64(&pool.ownedContexts) > 0 ||
		atomic.LoadInt64(&pool.outstanding) > 0 ||
		atomic.LoadInt64(&pool.activeCount) > 0 ||
		len(pool.TaskQueue) > 0
}
