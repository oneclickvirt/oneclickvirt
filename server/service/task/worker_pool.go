package task

import (
	"context"
	"fmt"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// getOrCreateProviderPool 获取或创建Provider工作池
func (s *TaskService) getOrCreateProviderPool(providerID uint, concurrency int) *ProviderWorkerPool {
	s.poolMutex.Lock()
	defer s.poolMutex.Unlock()

	// 如果池已存在，检查并发数是否需要调整
	if pool, exists := s.providerPools[providerID]; exists {
		if pool.WorkerCount != concurrency {
			// 需要调整并发数，关闭旧池并创建新池
			pool.Cancel()
			delete(s.providerPools, providerID)
		} else {
			return pool
		}
	}

	// 创建新的工作池
	ctx, cancel := context.WithCancel(context.Background())
	pool := &ProviderWorkerPool{
		ProviderID:  providerID,
		TaskQueue:   make(chan TaskRequest, concurrency*2), // 队列大小为并发数的2倍，提供缓冲
		WorkerCount: concurrency,
		Ctx:         ctx,
		Cancel:      cancel,
		TaskService: s,
	}

	// 启动工作者
	for i := 0; i < concurrency; i++ {
		go pool.worker(i)
	}

	s.providerPools[providerID] = pool
	global.APP_LOG.Info("创建Provider工作池",
		zap.Uint("providerId", providerID),
		zap.Int("concurrency", concurrency))

	return pool
}

// worker 工作者goroutine
func (pool *ProviderWorkerPool) worker(workerID int) {
	global.APP_LOG.Info("启动Provider工作者",
		zap.Uint("providerId", pool.ProviderID),
		zap.Int("workerId", workerID))

	defer global.APP_LOG.Info("Provider工作者退出",
		zap.Uint("providerId", pool.ProviderID),
		zap.Int("workerId", workerID))

	for {
		select {
		case <-pool.Ctx.Done():
			return
		case taskReq := <-pool.TaskQueue:
			pool.executeTask(taskReq)
		}
	}
}

// executeTask 执行单个任务
func (pool *ProviderWorkerPool) executeTask(taskReq TaskRequest) {
	task := taskReq.Task
	result := TaskResult{
		Success: false,
		Error:   nil,
		Data:    make(map[string]interface{}),
	}

	// 创建任务上下文
	taskCtx, taskCancel := context.WithTimeout(pool.Ctx, time.Duration(task.TimeoutDuration)*time.Second)
	defer taskCancel()

	// 注册任务上下文
	pool.TaskService.contextMutex.Lock()
	pool.TaskService.runningContexts[task.ID] = &TaskContext{
		TaskID:     task.ID,
		Context:    taskCtx,
		CancelFunc: taskCancel,
		StartTime:  time.Now(),
	}
	pool.TaskService.contextMutex.Unlock()

	// 任务完成时清理上下文
	defer func() {
		pool.TaskService.contextMutex.Lock()
		delete(pool.TaskService.runningContexts, task.ID)
		pool.TaskService.contextMutex.Unlock()
	}()

	// 更新任务状态为运行中 - 使用SELECT FOR UPDATE确保原子性
	err := pool.TaskService.dbService.ExecuteTransaction(taskCtx, func(tx *gorm.DB) error {
		// 使用行锁查询任务，确保原子性
		var currentTask adminModel.Task
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", task.ID).
			First(&currentTask).Error; err != nil {
			return fmt.Errorf("查询任务状态失败: %v", err)
		}

		// 如果任务已经不是pending状态，说明被其他worker处理了
		if currentTask.Status != "pending" {
			return fmt.Errorf("任务状态已变更，当前状态: %s", currentTask.Status)
		}

		// 使用WHERE条件确保只有pending状态才会被更新
		result := tx.Model(&adminModel.Task{}).
			Where("id = ? AND status = ?", task.ID, "pending").
			Updates(map[string]interface{}{
				"status":     "running",
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

	if err != nil {
		result.Error = fmt.Errorf("更新任务状态失败: %v", err)
		global.APP_LOG.Warn("任务状态更新失败，可能被其他worker处理",
			zap.Uint("taskId", task.ID),
			zap.Error(err))
		// 如果状态更新失败，不发送结果，让调度器自然忽略
		return
	}

	// 执行具体任务逻辑
	taskError := pool.TaskService.executeTaskLogic(taskCtx, &task)
	if taskError != nil {
		result.Error = taskError
	} else {
		result.Success = true
	}

	// 更新任务完成状态
	errorMsg := ""
	if result.Error != nil {
		errorMsg = result.Error.Error()
	}
	pool.TaskService.CompleteTask(task.ID, result.Success, errorMsg, result.Data)

	// 发送结果
	select {
	case taskReq.ResponseCh <- result:
	case <-taskCtx.Done():
	}
}

// StartTaskWithPool 使用工作池启动任务（新的简化版本）
func (s *TaskService) StartTaskWithPool(taskID uint) error {
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

	// 确定并发数
	concurrency := 1 // 默认串行
	if provider.AllowConcurrentTasks && provider.MaxConcurrentTasks > 0 {
		concurrency = provider.MaxConcurrentTasks
	}

	// 获取或创建工作池
	pool := s.getOrCreateProviderPool(*task.ProviderID, concurrency)

	// 创建任务请求
	taskReq := TaskRequest{
		Task:       task,
		ResponseCh: make(chan TaskResult, 1),
	}

	// 发送任务到工作池（阻塞直到有空闲worker或队列有空间）
	select {
	case pool.TaskQueue <- taskReq:
		global.APP_LOG.Info("任务已发送到工作池",
			zap.Uint("taskId", taskID),
			zap.Uint("providerId", *task.ProviderID),
			zap.Int("queueLength", len(pool.TaskQueue)))
	case <-time.After(30 * time.Second):
		return fmt.Errorf("任务队列已满，发送超时")
	}

	return nil
}
