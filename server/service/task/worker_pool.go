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
	return s.poolManager.GetOrCreate(providerID, concurrency, s)
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
	if err := pool.TaskService.contextManager.Add(task.ID, taskCtx, taskCancel); err != nil {
		global.APP_LOG.Error("注册任务上下文失败",
			zap.Uint("taskID", task.ID),
			zap.Error(err))

		result.Success = false
		result.Error = err
		pool.TaskService.CompleteTask(task.ID, false, err.Error(), result.Data)
		taskReq.ResponseCh <- result
		return
	}

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
		// 确保panic时也清理context
		pool.TaskService.contextManager.Delete(task.ID)
	}()

	// 任务完成时清理上下文
	defer pool.TaskService.contextManager.Delete(task.ID)

	// 更新任务状态为运行中 - 使用SELECT FOR UPDATE确保原子性
	updateErr := pool.TaskService.dbService.ExecuteTransaction(taskCtx, func(tx *gorm.DB) error {
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

	if updateErr != nil {
		result.Error = fmt.Errorf("更新任务状态失败: %v", updateErr)
		global.APP_LOG.Warn("任务状态更新失败，可能被其他worker处理",
			zap.Uint("taskId", task.ID),
			zap.Error(updateErr))
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

	// 非阻塞发送结果，防止goroutine泄漏
	// 使用timer代替time.After避免内存泄漏
	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	select {
	case taskReq.ResponseCh <- result:
		// 成功发送
	case <-taskCtx.Done():
		// 上下文已取消，放弃发送
		global.APP_LOG.Debug("任务上下文已取消，放弃发送结果",
			zap.Uint("taskId", task.ID))
	case <-timeout.C:
		// 5秒超时，防止永久阻塞
		global.APP_LOG.Warn("发送任务结果超时",
			zap.Uint("taskId", task.ID))
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

	// 创建任务请求，使用带缓冲的channel防止阻塞
	taskReq := TaskRequest{
		Task:       task,
		ResponseCh: make(chan TaskResult, 1),
	}

	// 启动goroutine等待响应或超时，防止channel泄漏
	go func() {
		defer func() {
			if r := recover(); r != nil {
				global.APP_LOG.Error("任务响应处理goroutine panic",
					zap.Uint("taskId", taskID),
					zap.Any("panic", r))
			}
		}()

		// 等待响应或超时（最长等待1小时）
		timeout := time.NewTimer(1 * time.Hour)
		defer timeout.Stop()

		select {
		case result := <-taskReq.ResponseCh:
			// 处理结果（日志记录）
			if result.Success {
				global.APP_LOG.Debug("任务执行成功",
					zap.Uint("taskId", taskID))
			} else {
				global.APP_LOG.Debug("任务执行失败",
					zap.Uint("taskId", taskID),
					zap.Error(result.Error))
			}
			// channel会自动被GC
		case <-timeout.C:
			global.APP_LOG.Warn("任务响应超时，关闭ResponseCh",
				zap.Uint("taskId", taskID))
			// 尝试drain channel
			select {
			case <-taskReq.ResponseCh:
			default:
			}
		}
	}()

	// 发送任务到工作池（阻塞直到有空闲worker或队列有空间）
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case pool.TaskQueue <- taskReq:
		global.APP_LOG.Info("任务已发送到工作池",
			zap.Uint("taskId", taskID),
			zap.Uint("providerId", *task.ProviderID),
			zap.Int("queueLength", len(pool.TaskQueue)))
	case <-timer.C:
		// 发送失败，关闭ResponseCh防止泄漏
		close(taskReq.ResponseCh)
		return fmt.Errorf("任务队列已满，发送超时")
	}

	return nil
}
