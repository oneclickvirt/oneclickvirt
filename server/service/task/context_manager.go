package task

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// TaskContext 任务执行上下文
type TaskContext struct {
	TaskID     uint
	Context    context.Context
	CancelFunc context.CancelFunc
	StartTime  time.Time
	onRelease  func()
}

// TaskContextManager 任务上下文管理器，使用sync.Map
type TaskContextManager struct {
	contexts sync.Map // map[uint]*TaskContext
	count    atomic.Int64
	mu       sync.Mutex
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
	return m.addWithRelease(taskID, ctx, cancel, nil)
}

func (m *TaskContextManager) addWithRelease(taskID uint, ctx context.Context, cancel context.CancelFunc, release func()) error {
	if ctx == nil {
		ctx = context.Background()
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.contexts.Load(taskID); exists {
		return ErrTaskContextExists
	}
	if m.maxSize > 0 && m.count.Load() >= int64(m.maxSize) {
		return ErrContextPoolFull
	}

	taskCtx := &TaskContext{
		TaskID:     taskID,
		Context:    ctx,
		CancelFunc: cancel,
		StartTime:  time.Now(),
		onRelease:  release,
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
	m.mu.Lock()
	if value, loaded := m.contexts.LoadAndDelete(taskID); loaded {
		m.count.Add(-1)
		m.mu.Unlock()
		taskCtx := value.(*TaskContext)
		if taskCtx.CancelFunc != nil {
			taskCtx.CancelFunc()
		}
		if taskCtx.onRelease != nil {
			taskCtx.onRelease()
		}
		return
	}
	m.mu.Unlock()
}

// DeleteBatch 批量删除任务上下文
func (m *TaskContextManager) DeleteBatch(taskIDs []uint) {
	for _, taskID := range taskIDs {
		m.Delete(taskID)
	}
}

// CleanupStale is retained for compatibility. Context ownership ends only
// after the worker completion path confirms its provider call has returned.
func (m *TaskContextManager) CleanupStale() int {
	return 0
}

// ForceLimitSize is retained for compatibility. Active contexts are never
// evicted to satisfy the pool limit.
func (m *TaskContextManager) ForceLimitSize() int {
	return 0
}

// Count 返回当前context数量
func (m *TaskContextManager) Count() int64 {
	return m.count.Load()
}

// CancelAll 取消所有context
func (m *TaskContextManager) CancelAll() {
	m.contexts.Range(func(key, value interface{}) bool {
		taskCtx := value.(*TaskContext)
		if taskCtx.CancelFunc != nil {
			taskCtx.CancelFunc()
		}
		return true
	})
}

var ErrContextPoolFull = fmt.Errorf("任务上下文池已满")

var ErrTaskContextExists = fmt.Errorf("任务已有执行上下文")
