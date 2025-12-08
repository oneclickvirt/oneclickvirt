package task

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"oneclickvirt/global"

	"go.uber.org/zap"
)

// TaskContext 任务执行上下文
type TaskContext struct {
	TaskID     uint
	Context    context.Context
	CancelFunc context.CancelFunc
	StartTime  time.Time
}

// TaskContextManager 任务上下文管理器，使用sync.Map
type TaskContextManager struct {
	contexts sync.Map // map[uint]*TaskContext
	count    atomic.Int64
	maxSize  int
	maxAge   time.Duration
}

// NewTaskContextManager 创建任务上下文管理器
func NewTaskContextManager(maxSize int, maxAge time.Duration) *TaskContextManager {
	return &TaskContextManager{
		maxSize: maxSize,
		maxAge:  maxAge,
	}
}

// Add 添加任务上下文
func (m *TaskContextManager) Add(taskID uint, ctx context.Context, cancel context.CancelFunc) error {
	currentCount := m.count.Load()

	// 如果接近容量上限（80%），主动触发清理
	if currentCount >= int64(float64(m.maxSize)*0.8) {
		cleaned := m.CleanupStale()
		if cleaned > 0 {
			global.APP_LOG.Info("容量接近上限，主动清理陈旧context",
				zap.Int("cleaned", cleaned),
				zap.Int64("remaining", m.count.Load()))
		}
		currentCount = m.count.Load()
	}

	// 检查容量限制
	if currentCount >= int64(m.maxSize) {
		// 强制清理
		forceClean := m.ForceLimitSize()
		if forceClean > 0 {
			global.APP_LOG.Warn("容量已满，强制清理最旧context",
				zap.Int("cleaned", forceClean))
		}
		// 再次检查
		if m.count.Load() >= int64(m.maxSize) {
			return ErrContextPoolFull
		}
	}

	taskCtx := &TaskContext{
		TaskID:     taskID,
		Context:    ctx,
		CancelFunc: cancel,
		StartTime:  time.Now(),
	}

	_, loaded := m.contexts.LoadOrStore(taskID, taskCtx)
	if !loaded {
		m.count.Add(1)
	}

	return nil
}

// Get 获取任务上下文
func (m *TaskContextManager) Get(taskID uint) (*TaskContext, bool) {
	value, ok := m.contexts.Load(taskID)
	if !ok {
		return nil, false
	}
	return value.(*TaskContext), true
}

// Delete 删除任务上下文
func (m *TaskContextManager) Delete(taskID uint) {
	if value, loaded := m.contexts.LoadAndDelete(taskID); loaded {
		// 取消context
		if taskCtx, ok := value.(*TaskContext); ok {
			if taskCtx.CancelFunc != nil {
				taskCtx.CancelFunc()
			}
		}
		m.count.Add(-1)
	}
}

// DeleteBatch 批量删除任务上下文
func (m *TaskContextManager) DeleteBatch(taskIDs []uint) {
	for _, taskID := range taskIDs {
		m.Delete(taskID)
	}
}

// CleanupStale 清理陈旧的context
func (m *TaskContextManager) CleanupStale() int {
	now := time.Now()
	cleaned := 0

	m.contexts.Range(func(key, value interface{}) bool {
		taskID := key.(uint)
		taskCtx := value.(*TaskContext)

		age := now.Sub(taskCtx.StartTime)
		if age > m.maxAge {
			m.Delete(taskID)
			cleaned++
			global.APP_LOG.Warn("清理陈旧任务context",
				zap.Uint("taskID", taskID),
				zap.Duration("age", age))
		}

		return true
	})

	return cleaned
}

// ForceLimitSize 强制限制大小，优先删除已完成或失败的任务的context
func (m *TaskContextManager) ForceLimitSize() int {
	currentCount := m.count.Load()
	threshold := int64(float64(m.maxSize) * 0.8)

	if currentCount <= threshold {
		return 0
	}

	// 收集所有context及其信息
	type ctxInfo struct {
		taskID      uint
		startTime   time.Time
		contextDone bool
		age         time.Duration
	}

	var contexts []ctxInfo
	now := time.Now()

	m.contexts.Range(func(key, value interface{}) bool {
		taskID := key.(uint)
		taskCtx := value.(*TaskContext)

		// 检查 context 是否已完成
		isDone := false
		select {
		case <-taskCtx.Context.Done():
			isDone = true
		default:
		}

		contexts = append(contexts, ctxInfo{
			taskID:      taskID,
			startTime:   taskCtx.StartTime,
			contextDone: isDone,
			age:         now.Sub(taskCtx.StartTime),
		})
		return true
	})

	// 优先删除已完成的context
	var toDelete []uint
	deleteTarget := len(contexts) * 3 / 10 // 删除30%
	if deleteTarget < 1 {
		deleteTarget = 1
	}

	// 第一轮：删除已完成的context
	for _, ctx := range contexts {
		if ctx.contextDone && len(toDelete) < deleteTarget {
			toDelete = append(toDelete, ctx.taskID)
		}
	}

	// 第二轮：如果还不够，删除最旧的（年龄超过 maxAge/2 的）
	if len(toDelete) < deleteTarget {
		halfMaxAge := m.maxAge / 2
		for _, ctx := range contexts {
			if !ctx.contextDone && ctx.age > halfMaxAge && len(toDelete) < deleteTarget {
				toDelete = append(toDelete, ctx.taskID)
			}
		}
	}

	// 第三轮：如果还不够，按年龄排序删除最旧的
	if len(toDelete) < deleteTarget {
		// 过滤出未被标记删除的
		var remaining []ctxInfo
		toDeleteMap := make(map[uint]bool)
		for _, id := range toDelete {
			toDeleteMap[id] = true
		}

		for _, ctx := range contexts {
			if !toDeleteMap[ctx.taskID] {
				remaining = append(remaining, ctx)
			}
		}

		// 简单排序找出最旧的
		need := deleteTarget - len(toDelete)
		for i := 0; i < need && i < len(remaining); i++ {
			oldestIdx := i
			for j := i + 1; j < len(remaining); j++ {
				if remaining[j].startTime.Before(remaining[oldestIdx].startTime) {
					oldestIdx = j
				}
			}
			if oldestIdx != i {
				remaining[i], remaining[oldestIdx] = remaining[oldestIdx], remaining[i]
			}
			toDelete = append(toDelete, remaining[i].taskID)
		}
	}

	// 执行删除
	for _, taskID := range toDelete {
		m.Delete(taskID)
	}

	if len(toDelete) > 0 {
		global.APP_LOG.Warn("Context池容量接近上限，执行清理",
			zap.Int("deleted", len(toDelete)),
			zap.Int64("remaining", m.count.Load()))
	}

	return len(toDelete)
}

// Count 返回当前context数量
func (m *TaskContextManager) Count() int64 {
	return m.count.Load()
}

// CancelAll 取消所有context
func (m *TaskContextManager) CancelAll() {
	m.contexts.Range(func(key, value interface{}) bool {
		taskCtx := value.(*TaskContext)
		taskCtx.CancelFunc()
		return true
	})
}

var ErrContextPoolFull = fmt.Errorf("任务上下文池已满")
