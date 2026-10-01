package task

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"oneclickvirt/constant"
	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/interfaces"
	"oneclickvirt/utils"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// getOrCreateProviderPool 获取或创建Provider工作池
func (s *TaskService) getOrCreateProviderPool(providerID uint, concurrency int) *ProviderWorkerPool {
	return s.poolManager.GetOrCreate(providerID, concurrency, s)
}

func getProviderTaskConcurrency(provider providerModel.Provider) int {
	if !provider.AllowConcurrentTasks {
		return constant.ProviderDefaultConcurrentTasks
	}
	if provider.MaxConcurrentTasks <= 0 {
		return constant.ProviderDefaultConcurrentTasks
	}
	if provider.MaxConcurrentTasks > constant.ProviderMaxConcurrentTasks {
		return constant.ProviderMaxConcurrentTasks
	}
	return provider.MaxConcurrentTasks
}

// worker 工作者goroutine
func (pool *ProviderWorkerPool) worker(workerID int) {
	global.APP_LOG.Debug("启动Provider工作者",
		zap.Uint("providerId", pool.ProviderID),
		zap.Int("workerId", workerID))

	defer global.APP_LOG.Debug("Provider工作者退出",
		zap.Uint("providerId", pool.ProviderID),
		zap.Int("workerId", workerID))

	for {
		select {
		case <-pool.Ctx.Done():
			pool.drainQueuedTasks()
			return
		case taskReq := <-pool.TaskQueue:
			if pool.Ctx.Err() != nil {
				pool.releaseQueuedTask(taskReq)
				continue
			}
			pool.executeTask(taskReq)
			atomic.AddInt64(&pool.outstanding, -1)
		}
	}
}

// executeTask 执行单个任务
func (pool *ProviderWorkerPool) executeTask(taskReq TaskRequest) {
	atomic.AddInt64(&pool.activeCount, 1)
	defer atomic.AddInt64(&pool.activeCount, -1)

	task := taskReq.Task
	result := TaskResult{
		Success: false,
		Error:   nil,
		Data:    make(map[string]interface{}),
	}
	asyncCompletion := false

	// 创建任务上下文
	taskCtx, taskCancel := context.WithTimeout(pool.Ctx, time.Duration(taskTimeoutSeconds(task))*time.Second)

	// 注册任务上下文
	atomic.AddInt64(&pool.ownedContexts, 1)
	if err := pool.TaskService.contextManager.addWithRelease(task.ID, taskCtx, taskCancel, func() { atomic.AddInt64(&pool.ownedContexts, -1) }); err != nil {
		atomic.AddInt64(&pool.ownedContexts, -1)
		taskCancel()
		global.APP_LOG.Error("注册任务上下文失败",
			zap.Uint("taskID", task.ID),
			zap.Error(err))

		pool.TaskService.queuedTasks.Delete(task.ID)
		result.Success = false
		result.Error = err
		if !errors.Is(err, ErrTaskContextExists) {
			pool.TaskService.CompleteTask(task.ID, false, err.Error(), result.Data)
		}
		select {
		case taskReq.ResponseCh <- result:
		default:
		}
		return
	}

	pool.TaskService.queuedTasks.Delete(task.ID)

	// Panic recovery机制必须在最外层，确保任何panic都会清理资源
	defer func() {
		if r := recover(); r != nil {
			// 记录panic详情
			global.APP_LOG.Error("任务执行过程中发生panic",
				zap.Uint("taskId", task.ID),
				zap.String("taskType", task.TaskType),
				zap.Any("panic", r),
				zap.Stack("stack"))

			// 更新任务状态为失败
			result.Success = false
			result.Error = fmt.Errorf("任务执行panic: %v", r)

			// 标记任务失败
			errorMsg := fmt.Sprintf("任务执行发生严重错误: %v", r)
			pool.TaskService.CompleteTask(task.ID, false, errorMsg, result.Data)

			// 尝试发送结果（可能已经超时或通道已关闭）
			select {
			case taskReq.ResponseCh <- result:
			default:
				global.APP_LOG.Warn("无法发送panic任务结果，通道可能已关闭",
					zap.Uint("taskId", task.ID))
			}
		}
		// 异步接管的任务需要保留 context，供取消/强制停止信号继续传递给后台后处理。
		if !asyncCompletion {
			pool.TaskService.contextManager.Delete(task.ID)
		}
	}()

	// 更新任务状态为运行中 - 使用SELECT FOR UPDATE确保原子性
	updateErr := pool.TaskService.dbService.ExecuteTransaction(taskCtx, func(tx *gorm.DB) error {
		// 使用行锁查询任务，确保原子性
		var currentTask adminModel.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", task.ID).
			First(&currentTask).Error; err != nil {
			return fmt.Errorf("查询任务状态失败: %v", err)
		}

		// 如果任务已经不是待执行状态，说明被其他worker处理或被取消了
		if currentTask.Status != mainTaskStatusPending && currentTask.Status != mainTaskStatusProcessing {
			return fmt.Errorf("任务状态已变更，当前状态: %s", currentTask.Status)
		}

		// 使用WHERE条件确保只有待执行状态才会被更新
		result := tx.Model(&adminModel.Task{}).
			Where("id = ? AND status IN ?", task.ID, []string{mainTaskStatusPending, mainTaskStatusProcessing}).
			Updates(map[string]interface{}{
				"status":     mainTaskStatusRunning,
				"started_at": time.Now(),
			})

		if result.Error != nil {
			return result.Error
		}

		// 检查是否真的更新了记录
		if result.RowsAffected == 0 {
			return fmt.Errorf("任务状态更新失败，可能已被其他worker处理")
		}

		return nil
	})

	if updateErr != nil {
		pool.TaskService.requeueUnstartedTask(task.ID)
		result.Error = fmt.Errorf("更新任务状态失败: %v", updateErr)
		global.APP_LOG.Warn("任务状态更新失败，可能被其他worker处理",
			zap.Uint("taskId", task.ID),
			zap.Error(updateErr))
		// StartTaskWithPool starts a response waiter before enqueueing the task.
		// Always signal that waiter on a lost claim (most commonly a user
		// cancelled a processing task) so it does not remain blocked for an hour.
		select {
		case taskReq.ResponseCh <- result:
		default:
		}
		return
	}

	// 执行具体任务逻辑
	taskError := pool.TaskService.executeTaskLogic(taskCtx, &task)

	// 判断是否为"后台goroutine接管完成"哨兵信号
	if errors.Is(taskError, interfaces.ErrAsyncCompletion) {
		// 任务逻辑已启动后台goroutine负责标记任务完成，worker pool 无需调用 CompleteTask
		asyncCompletion = true
		result.Success = true
		global.APP_LOG.Debug("任务移交后台goroutine处理，worker pool跳过CompleteTask",
			zap.Uint("taskId", task.ID))
	} else if taskError != nil {
		result.Error = taskError
		// 更新任务完成状态（失败）
		pool.TaskService.CompleteTask(task.ID, false, taskError.Error(), result.Data)
	} else {
		result.Success = true
		// 更新任务完成状态（成功）
		pool.TaskService.CompleteTask(task.ID, true, "", result.Data)
	}

	// ResponseCh is buffered and only consumed for task-completion logging.
	// Cancellation must not win a select and strand the response waiter until
	// its one-hour timeout after the worker has already finished.
	select {
	case taskReq.ResponseCh <- result:
	default:
		global.APP_LOG.Warn("任务结果通道已满，跳过重复通知",
			zap.Uint("taskId", task.ID))
	}
}

// StartTaskWithPool 使用工作池启动任务（新的简化版本）
func (s *TaskService) StartTaskWithPool(taskID uint) error {
	if s.ctx != nil && s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	// 查询任务信息
	var task adminModel.Task
	err := s.dbService.ExecuteQuery(context.Background(), func() error {
		return global.APP_DB.First(&task, taskID).Error
	})

	if err != nil {
		return fmt.Errorf("查询任务失败: %v", err)
	}

	if task.ProviderID == nil {
		return fmt.Errorf("任务没有关联Provider")
	}

	// 获取Provider配置
	var provider providerModel.Provider
	err = s.dbService.ExecuteQuery(context.Background(), func() error {
		return global.APP_DB.First(&provider, *task.ProviderID).Error
	})

	if err != nil {
		return fmt.Errorf("查询Provider失败: %v", err)
	}

	return s.StartTaskWithProvider(task, provider)
}

// StartTaskWithProvider submits a task using a Provider row already loaded by
// the scheduler. This keeps the common scheduler path from querying the same
// task and Provider a second time while preserving the CAS in trySubmit.
func (s *TaskService) StartTaskWithProvider(task adminModel.Task, provider providerModel.Provider) error {
	if s.ctx != nil && s.ctx.Err() != nil {
		return s.ctx.Err()
	}
	if task.ProviderID == nil {
		return fmt.Errorf("任务没有关联Provider")
	}
	if *task.ProviderID != provider.ID {
		return fmt.Errorf("任务与Provider不匹配")
	}
	pool := s.getOrCreateProviderPool(provider.ID, getProviderTaskConcurrency(provider))
	if pool == nil {
		return fmt.Errorf("任务服务正在关闭")
	}
	return pool.trySubmit(task)
}

// trySubmit never waits for queue space. Pending work remains durable for the
// next scheduler pass; one busy provider cannot block all other maintenance.
func (pool *ProviderWorkerPool) trySubmit(task adminModel.Task) error {
	pool.submitMu.Lock()
	if pool.closed || pool.Ctx.Err() != nil {
		pool.submitMu.Unlock()
		return fmt.Errorf("工作池已关闭")
	}
	if len(pool.TaskQueue)+pool.submitting >= cap(pool.TaskQueue) {
		pool.submitMu.Unlock()
		return fmt.Errorf("任务队列已满，等待下次调度")
	}
	if _, loaded := pool.TaskService.queuedTasks.LoadOrStore(task.ID, struct{}{}); loaded {
		pool.submitMu.Unlock()
		return fmt.Errorf("任务已在队列中")
	}
	pool.submitting++
	atomic.AddInt64(&pool.outstanding, 1)
	pool.submitMu.Unlock()

	// Never hold a pool mutex across database I/O: the manager also needs it
	// when cancelling/resizing, and must remain available to other providers.
	ctx, cancel := context.WithTimeout(pool.Ctx, 5*time.Second)
	claim := global.APP_DB.WithContext(ctx).Model(&adminModel.Task{}).
		Where("id = ? AND status = ?", task.ID, mainTaskStatusPending).
		Update("status", mainTaskStatusProcessing)
	cancel()
	pool.submitMu.Lock()
	if claim.Error != nil || claim.RowsAffected == 0 || pool.closed || pool.Ctx.Err() != nil {
		pool.submitMu.Unlock()
		// A cancelled database write may have committed before its reply was
		// interrupted. CAS the durable processing state back in both cases.
		if claim.Error != nil || claim.RowsAffected > 0 {
			pool.TaskService.requeueUnstartedTask(task.ID)
		} else {
			pool.TaskService.queuedTasks.Delete(task.ID)
		}
		atomic.AddInt64(&pool.outstanding, -1)
		pool.submitMu.Lock()
		pool.submitting--
		pool.submitMu.Unlock()
		if claim.Error != nil {
			return claim.Error
		}
		return fmt.Errorf("任务状态或工作池已变化")
	}
	task.Status = mainTaskStatusProcessing
	pool.TaskQueue <- TaskRequest{Task: task, ResponseCh: make(chan TaskResult, 1)}
	pool.submitting--
	pool.submitMu.Unlock()
	return nil
}

func (s *TaskService) requeueUnstartedTask(taskID uint) {
	defer s.queuedTasks.Delete(taskID)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := global.APP_DB.WithContext(ctx).Model(&adminModel.Task{}).
		Where("id = ? AND status = ?", taskID, mainTaskStatusProcessing).
		Update("status", mainTaskStatusPending).Error; err != nil {
		global.APP_LOG.Error("退回未执行任务失败，将由维护任务恢复", zap.Uint("taskId", taskID), zap.Error(err))
	}
}

func (pool *ProviderWorkerPool) releaseQueuedTask(req TaskRequest) {
	pool.releaseQueuedTasks([]TaskRequest{req})
}

func (pool *ProviderWorkerPool) drainQueuedTasks() {
	pool.submitMu.Lock()
	pool.closed = true
	var queued []TaskRequest
collect:
	for {
		select {
		case req := <-pool.TaskQueue:
			queued = append(queued, req)
		default:
			break collect
		}
	}
	pool.submitMu.Unlock()
	pool.releaseQueuedTasks(queued)
}

func (pool *ProviderWorkerPool) releaseQueuedTasks(requests []TaskRequest) {
	if len(requests) == 0 {
		return
	}
	ids := make([]uint, 0, len(requests))
	for _, req := range requests {
		ids = append(ids, req.Task.ID)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if global.APP_DB == nil {
		global.APP_LOG.Warn("数据库连接不存在，无法批量退回排队任务", zap.Int("count", len(ids)))
	} else if err := global.APP_DB.WithContext(ctx).Model(&adminModel.Task{}).
		Where("id IN ? AND status = ?", ids, mainTaskStatusProcessing).
		Update("status", mainTaskStatusPending).Error; err != nil {
		global.APP_LOG.Error("批量退回未执行任务失败，将由维护任务恢复", zap.Int("count", len(ids)), zap.Error(err))
	}
	for _, req := range requests {
		pool.TaskService.queuedTasks.Delete(req.Task.ID)
		atomic.AddInt64(&pool.outstanding, -1)
		select {
		case req.ResponseCh <- TaskResult{Error: fmt.Errorf("工作池已关闭，任务已退回队列")}:
		default:
		}
	}
}

func taskTimeoutSeconds(task adminModel.Task) int {
	if task.TimeoutDuration > 0 {
		return task.TimeoutDuration
	}
	return utils.GetDefaultTaskTimeout(task.TaskType)
}
