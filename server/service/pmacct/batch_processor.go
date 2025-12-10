package pmacct

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"oneclickvirt/global"
	providerModel "oneclickvirt/model/provider"

	"go.uber.org/zap"
)

// BatchProcessor 自适应批量处理器（空闲时休眠，繁忙时加速）
type BatchProcessor struct {
	addQueue    chan uint  // 待添加的instanceID
	deleteQueue chan uint  // 待删除的instanceID
	mu          sync.Mutex // 保护处理逻辑
	service     *Service

	// 自适应配置
	minInterval    time.Duration // 最小间隔（繁忙时）
	maxInterval    time.Duration // 最大间隔（空闲时）
	minConcurrency int           // 最小并发数
	maxConcurrency int           // 最大并发数

	// 运行状态
	isActive   atomic.Bool // 是否处于活跃状态
	lastActive time.Time   // 最后活跃时间
	ctx        context.Context
	cancel     context.CancelFunc

	// 降级goroutine控制
	wg        sync.WaitGroup // 追踪所有后台goroutine
	semaphore chan struct{}  // 限制降级goroutine数量
}

// NewBatchProcessor 创建自适应批量处理器
func NewBatchProcessor() *BatchProcessor {
	// 使用应用级shutdown context，确保关闭时能正确停止
	ctx, cancel := context.WithCancel(global.APP_SHUTDOWN_CONTEXT)

	bp := &BatchProcessor{
		addQueue:       make(chan uint, 500),
		deleteQueue:    make(chan uint, 500),
		service:        NewService(),
		minInterval:    5 * time.Second,  // 繁忙时最快5秒处理一次
		maxInterval:    30 * time.Second, // 空闲时最桒30秒检查一次
		minConcurrency: 1,                // 空闲时最少1并发
		maxConcurrency: 10,               // 繁忙时最多10并发
		lastActive:     time.Now(),
		ctx:            ctx,
		cancel:         cancel,
		semaphore:      make(chan struct{}, 10), // 10个降级goroutine
	}

	return bp
}

// Start 启动自适应批量处理器
func (bp *BatchProcessor) Start() {
	global.APP_LOG.Info("启动pmacct自适应批量处理器",
		zap.Duration("minInterval", bp.minInterval),
		zap.Duration("maxInterval", bp.maxInterval))

	go bp.adaptiveLoop()
}

// adaptiveLoop 自适应处理循环（空闲休眠，繁忙加速）
func (bp *BatchProcessor) adaptiveLoop() {
	// 确俟timer在panic时也能停止，防止goroutine泄漏
	timer := time.NewTimer(bp.maxInterval)
	defer func() {
		timer.Stop()
		if r := recover(); r != nil {
			global.APP_LOG.Error("Batch processor主循环panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
	}()

	for {
		select {
		case <-bp.ctx.Done():
			bp.Stop()
			return
		default:
			// 计算动态间隔
			interval := bp.calculateInterval()

			// 如果队列为空且已空闲超过1分钟，进入休眠
			if bp.isIdle() {
				bp.enterIdleMode()
				continue
			}

			// 重置timer并等待间隔后处理
			timer.Reset(interval)
			select {
			case <-bp.ctx.Done():
				return
			case <-timer.C:
				bp.processBatch()
			}
		}
	}
}

// calculateInterval 根据队列负载计算动态间隔
func (bp *BatchProcessor) calculateInterval() time.Duration {
	addLen := len(bp.addQueue)
	deleteLen := len(bp.deleteQueue)
	totalLen := addLen + deleteLen

	// 根据队列长度动态调整间隔
	// 0-10项: 30秒（空闲）
	// 11-50项: 20秒
	// 51-100项: 10秒
	// 100+项: 5秒（繁忙）
	if totalLen == 0 {
		return bp.maxInterval
	} else if totalLen <= 10 {
		return 20 * time.Second
	} else if totalLen <= 50 {
		return 15 * time.Second
	} else if totalLen <= 100 {
		return 10 * time.Second
	}
	return bp.minInterval
}

// isIdle 检查是否处于空闲状态
func (bp *BatchProcessor) isIdle() bool {
	return len(bp.addQueue) == 0 &&
		len(bp.deleteQueue) == 0 &&
		time.Since(bp.lastActive) > 1*time.Minute
}

// enterIdleMode 进入空闲休眠模式
func (bp *BatchProcessor) enterIdleMode() {
	if bp.isActive.Load() {
		global.APP_LOG.Info("批量处理器进入空闲休眠模式")
		bp.isActive.Store(false)
	}

	// 确俟timer正确停止
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()

	// 休眠等待新任务唤醒
	select {
	case <-bp.ctx.Done():
		return
	case <-timer.C:
		// 定期检查是否有新任务
	}
}

// Stop 停止批量处理器
func (bp *BatchProcessor) Stop() {
	if bp.cancel != nil {
		bp.cancel()
	}

	// 等待所有后台goroutine完成
	done := make(chan struct{})
	go func() {
		defer close(done)
		bp.wg.Wait()
	}()

	// 确俟timer正确停止
	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()

	select {
	case <-done:
		global.APP_LOG.Info("pmacct自适应批量处理器已停止，所有后台任务已完成")
	case <-timer.C:
		global.APP_LOG.Warn("pmacct批量处理器关闭超时，可能有goroutine未完成",
			zap.Int("pendingAdd", len(bp.addQueue)),
			zap.Int("pendingDelete", len(bp.deleteQueue)))
	}
}

// QueueAdd 将实例添加操作加入队列（自动唤醒）
func (bp *BatchProcessor) QueueAdd(instanceID uint) {
	select {
	case bp.addQueue <- instanceID:
		bp.wakeUp()
		global.APP_LOG.Debug("pmacct添加操作已入队",
			zap.Uint("instanceID", instanceID),
			zap.Int("queueLen", len(bp.addQueue)))
	default:
		// 队列满，使用信号量控制降级goroutine数量
		select {
		case bp.semaphore <- struct{}{}:
			// 获取信号量成功，可以启动降级goroutine
			bp.wg.Add(1)
			go func(iid uint) {
				// 确保信号量一定会释放（最外层defer）
				defer func() {
					<-bp.semaphore
					bp.wg.Done()
				}()

				// panic 恢复
				defer func() {
					if r := recover(); r != nil {
						global.APP_LOG.Error("pmacct降级添加goroutine panic",
							zap.Uint("instanceID", iid),
							zap.Any("panic", r),
							zap.Stack("stack"))
					}
				}()

				// 直接执行，带超时控制
				ctx, cancel := context.WithTimeout(bp.ctx, 1*time.Minute)
				defer cancel()

				// 使用channel接收结果
				done := make(chan error, 1)
				go func() {
					defer func() {
						if r := recover(); r != nil {
							done <- fmt.Errorf("panic: %v", r)
						}
					}()
					done <- bp.service.InitializePmacctForInstance(iid)
				}()

				select {
				case err := <-done:
					if err != nil {
						global.APP_LOG.Error("降级添加pmacct失败",
							zap.Uint("instanceID", iid),
							zap.Error(err))
					}
				case <-ctx.Done():
					global.APP_LOG.Error("降级添加pmacct超时",
						zap.Uint("instanceID", iid),
						zap.Duration("timeout", 1*time.Minute))
				}
			}(instanceID)

			global.APP_LOG.Warn("批处理队列已满，使用降级处理",
				zap.Uint("instanceID", instanceID))
		default:
			// 信号量也满了，记录错误并丢弃
			global.APP_LOG.Error("批处理队列和降级goroutine池都已满，丢弃任务",
				zap.Uint("instanceID", instanceID),
				zap.Int("queueLen", len(bp.addQueue)),
				zap.Int("semaphoreUsed", len(bp.semaphore)))
		}
	}
}

// QueueDelete 将实例删除操作加入队列（自动唤醒）
func (bp *BatchProcessor) QueueDelete(instanceID uint) {
	select {
	case bp.deleteQueue <- instanceID:
		bp.wakeUp()
		global.APP_LOG.Debug("pmacct删除操作已入队",
			zap.Uint("instanceID", instanceID),
			zap.Int("queueLen", len(bp.deleteQueue)))
	default:
		// 队列满，使用信号量控制降级goroutine数量
		select {
		case bp.semaphore <- struct{}{}:
			// 获取信号量成功，可以启动降级goroutine
			bp.wg.Add(1)
			go func(iid uint) {
				// 确保信号量一定会释放（最外层defer）
				defer func() {
					<-bp.semaphore
					bp.wg.Done()
				}()

				// panic 恢复
				defer func() {
					if r := recover(); r != nil {
						global.APP_LOG.Error("pmacct降级删除goroutine panic",
							zap.Uint("instanceID", iid),
							zap.Any("panic", r),
							zap.Stack("stack"))
					}
				}()

				// 直接执行，带超时控制
				ctx, cancel := context.WithTimeout(bp.ctx, 1*time.Minute)
				defer cancel()

				// 使用channel接收结果
				done := make(chan error, 1)
				go func() {
					defer func() {
						if r := recover(); r != nil {
							done <- fmt.Errorf("panic: %v", r)
						}
					}()
					done <- bp.service.CleanupPmacctData(iid)
				}()

				select {
				case err := <-done:
					if err != nil {
						global.APP_LOG.Error("降级删除pmacct失败",
							zap.Uint("instanceID", iid),
							zap.Error(err))
					}
				case <-ctx.Done():
					global.APP_LOG.Error("降级删除pmacct超时",
						zap.Uint("instanceID", iid),
						zap.Duration("timeout", 1*time.Minute))
				}
			}(instanceID)

			global.APP_LOG.Warn("批处理队列已满，使用降级处理",
				zap.Uint("instanceID", instanceID))
		default:
			// 信号量也满了，记录错误并丢弃
			global.APP_LOG.Error("批处理队列和降级goroutine池都已满，丢弃任务",
				zap.Uint("instanceID", instanceID),
				zap.Int("queueLen", len(bp.deleteQueue)),
				zap.Int("semaphoreUsed", len(bp.semaphore)))
		}
	}
}

// wakeUp 唤醒批量处理器（从空闲模式恢复）
func (bp *BatchProcessor) wakeUp() {
	bp.lastActive = time.Now()
	if !bp.isActive.Load() {
		global.APP_LOG.Info("批量处理器被唤醒（检测到新任务）")
		bp.isActive.Store(true)
	}
}

// processBatch 批量处理累积的操作
func (bp *BatchProcessor) processBatch() {
	bp.mu.Lock()
	defer bp.mu.Unlock()

	// 收集待处理的操作
	addOps := make([]uint, 0)
	deleteOps := make([]uint, 0)

	// 从队列中取出所有待处理项
	for {
		select {
		case instanceID := <-bp.addQueue:
			addOps = append(addOps, instanceID)
		default:
			goto collectDelete
		}
	}

collectDelete:
	for {
		select {
		case instanceID := <-bp.deleteQueue:
			deleteOps = append(deleteOps, instanceID)
		default:
			goto process
		}
	}

process:
	if len(addOps) == 0 && len(deleteOps) == 0 {
		return
	}

	// 计算动态并发数
	concurrency := bp.calculateConcurrency(len(addOps) + len(deleteOps))

	global.APP_LOG.Info("开始批量处理pmacct操作",
		zap.Int("addCount", len(addOps)),
		zap.Int("deleteCount", len(deleteOps)),
		zap.Int("concurrency", concurrency))

	// 先处理删除操作
	if len(deleteOps) > 0 {
		bp.batchDelete(deleteOps, concurrency)
	}

	// 再处理添加操作
	if len(addOps) > 0 {
		bp.batchAdd(addOps, concurrency)
	}

	global.APP_LOG.Info("批量处理完成",
		zap.Int("addCount", len(addOps)),
		zap.Int("deleteCount", len(deleteOps)))

	bp.lastActive = time.Now()
}

// calculateConcurrency 根据任务数量计算动态并发数
func (bp *BatchProcessor) calculateConcurrency(taskCount int) int {
	// 1-5个任务: 1并发
	// 6-20个任务: 3并发
	// 21-50个任务: 5并发
	// 50+个任务: 10并发
	if taskCount <= 5 {
		return bp.minConcurrency
	} else if taskCount <= 20 {
		return 3
	} else if taskCount <= 50 {
		return 5
	}
	return bp.maxConcurrency
}

// batchAdd 批量添加监控（动态并发，带超时控制）
func (bp *BatchProcessor) batchAdd(instanceIDs []uint, concurrency int) {
	// 按Provider分组
	providerGroups := bp.groupByProvider(instanceIDs)

	for providerID, instances := range providerGroups {
		global.APP_LOG.Info("批量初始化流量监控",
			zap.Uint("providerID", providerID),
			zap.Int("instanceCount", len(instances)),
			zap.Int("concurrency", concurrency))

		// 并发处理同一Provider下的实例，带超时控制
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, concurrency)

		// 为整个批次设置超时（每个实例最多2分钟）
		batchTimeout := time.Duration(len(instances)) * 2 * time.Minute
		if batchTimeout > 30*time.Minute {
			batchTimeout = 30 * time.Minute // 最多30分钟
		}
		ctx, cancel := context.WithTimeout(bp.ctx, batchTimeout)
		defer cancel()

		for _, instanceID := range instances {
			wg.Add(1)
			go func(iid uint) {
				defer wg.Done()

				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()

					// 为单个实例设置超时
					instCtx, instCancel := context.WithTimeout(ctx, 2*time.Minute)
					defer instCancel()

					done := make(chan error, 1)
					go func() {
						done <- bp.service.InitializePmacctForInstance(iid)
					}()

					select {
					case err := <-done:
						if err != nil {
							global.APP_LOG.Error("批量初始化pmacct失败",
								zap.Uint("instanceID", iid),
								zap.Error(err))
						}
					case <-instCtx.Done():
						global.APP_LOG.Error("批量初始化pmacct超时",
							zap.Uint("instanceID", iid),
							zap.Duration("timeout", 2*time.Minute))
					}
				case <-ctx.Done():
					global.APP_LOG.Warn("批处理已超时，跳过实例",
						zap.Uint("instanceID", iid))
				}
			}(instanceID)
		}

		wg.Wait()
	}
}

// batchDelete 批量删除监控（动态并发，带超时控制）
func (bp *BatchProcessor) batchDelete(instanceIDs []uint, concurrency int) {
	// 按Provider分组
	providerGroups := bp.groupByProvider(instanceIDs)

	for providerID, instances := range providerGroups {
		global.APP_LOG.Info("批量清理pmacct监控",
			zap.Uint("providerID", providerID),
			zap.Int("instanceCount", len(instances)),
			zap.Int("concurrency", concurrency))

		// 并发处理同一Provider下的实例，带超时控制
		var wg sync.WaitGroup
		semaphore := make(chan struct{}, concurrency)

		// 为整个批次设置超时（每个实例最多1分钟）
		batchTimeout := time.Duration(len(instances)) * 1 * time.Minute
		if batchTimeout > 20*time.Minute {
			batchTimeout = 20 * time.Minute // 最多20分钟
		}
		ctx, cancel := context.WithTimeout(bp.ctx, batchTimeout)
		defer cancel()

		for _, instanceID := range instances {
			wg.Add(1)
			go func(iid uint) {
				defer wg.Done()

				select {
				case semaphore <- struct{}{}:
					defer func() { <-semaphore }()

					// 为单个实例设置超时
					instCtx, instCancel := context.WithTimeout(ctx, 1*time.Minute)
					defer instCancel()

					done := make(chan error, 1)
					go func() {
						done <- bp.service.CleanupPmacctData(iid)
					}()

					select {
					case err := <-done:
						if err != nil {
							global.APP_LOG.Error("批量清理pmacct失败",
								zap.Uint("instanceID", iid),
								zap.Error(err))
						}
					case <-instCtx.Done():
						global.APP_LOG.Error("批量清理pmacct超时",
							zap.Uint("instanceID", iid),
							zap.Duration("timeout", 1*time.Minute))
					}
				case <-ctx.Done():
					global.APP_LOG.Warn("批处理已超时，跳过实例",
						zap.Uint("instanceID", iid))
				}
			}(instanceID)
		}

		wg.Wait()
	}
}

// groupByProvider 按Provider分组实例
func (bp *BatchProcessor) groupByProvider(instanceIDs []uint) map[uint][]uint {
	groups := make(map[uint][]uint)

	// 批量查询所有实例的Provider信息
	type InstanceProviderInfo struct {
		ID         uint
		ProviderID uint
	}
	var instances []InstanceProviderInfo

	if len(instanceIDs) > 0 {
		if err := global.APP_DB.Model(&providerModel.Instance{}).
			Select("id, provider_id").
			Where("id IN ?", instanceIDs).
			Find(&instances).Error; err != nil {
			global.APP_LOG.Error("批量获取实例Provider信息失败", zap.Error(err))
			return groups
		}
	}

	// 按provider_id分组
	for _, instance := range instances {
		groups[instance.ProviderID] = append(groups[instance.ProviderID], instance.ID)
	}

	return groups
}

// GetBatchProcessor 获取全局批量处理器（单例）
func GetBatchProcessor() *BatchProcessor {
	batchProcessorOnce.Do(func() {
		batchProcessor = NewBatchProcessor()
		// 在应用启动时自动启动
		batchProcessor.Start()
	})
	return batchProcessor
}
