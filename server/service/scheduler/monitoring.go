package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	monitoringModel "oneclickvirt/model/monitoring"
	providerModel "oneclickvirt/model/provider"
	"oneclickvirt/service/system"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

// PmacctServiceInterface pmacct服务接口
type PmacctServiceInterface interface {
	// CollectTrafficFromSQLite 从Provider的SQLite数据库采集流量数据并同步到MySQL
	// 架构：Memory(1min) -> SQLite(local) -> MySQL(remote)
	// 参数：预加载的instance和monitor数据
	CollectTrafficFromSQLite(instance *providerModel.Instance, monitor *monitoringModel.PmacctMonitor) error

	// CleanupOldPmacctData 清理过期的流量数据
	CleanupOldPmacctData(days int) error

	// ResetPmacctDaemon 完全重置pmacct守护进程和数据库
	ResetPmacctDaemon(instanceID uint) error
}

// MonitoringSchedulerService 监控调度服务
type MonitoringSchedulerService struct {
	pmacctService        PmacctServiceInterface
	stopChan             chan struct{}
	isRunning            bool
	wg                   sync.WaitGroup        // 追踪所有后台goroutine
	providerStateManager *ProviderStateManager // Provider状态管理器
	lastResetTime        sync.Map              // map[uint]time.Time - pmacct重置时间记录
	lastResetCleanup     time.Time             // 最后清理时间
	mu                   sync.Mutex            // 保护 lastResetCleanup
}

// NewMonitoringSchedulerService 创建监控调度服务
func NewMonitoringSchedulerService(pmacctService PmacctServiceInterface) *MonitoringSchedulerService {
	return &MonitoringSchedulerService{
		pmacctService:        pmacctService,
		stopChan:             make(chan struct{}),
		isRunning:            false,
		providerStateManager: NewProviderStateManager(),
		lastResetCleanup:     time.Now(),
	}
}

// Start 启动监控调度器
func (s *MonitoringSchedulerService) Start(ctx context.Context) {
	if s.isRunning {
		global.APP_LOG.Warn("监控调度器已在运行中")
		return
	}

	s.isRunning = true
	global.APP_LOG.Info("启动监控调度器")

	// 启动pmacct流量数据收集任务
	go s.startPmacctCollection(ctx)

	// 启动清理任务
	go s.startCleanupTask(ctx)

	// 启动pmacct守护进程重置任务
	go s.startPmacctResetTask(ctx)
}

// Stop 停止监控调度器
func (s *MonitoringSchedulerService) Stop() {
	if !s.isRunning {
		return
	}

	global.APP_LOG.Info("停止监控调度器")
	close(s.stopChan)
	s.isRunning = false

	// 等待所有goroutine完成（最多30秒）
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()

	select {
	case <-done:
		global.APP_LOG.Info("监控调度器所有后台任务已完成")
	case <-timer.C:
		global.APP_LOG.Warn("监控调度器关闭超时，可能有goroutine未完成")
	}
}

// IsRunning 检查调度器是否正在运行
func (s *MonitoringSchedulerService) IsRunning() bool {
	return s.isRunning
}

// DeleteProviderState 删除Provider状态（完全原子性同步清理所有相关sync.Map）
func (s *MonitoringSchedulerService) DeleteProviderState(providerID uint) {
	// 原子性操作：从所有sync.Map中删除（防止孤立条目）
	s.providerStateManager.Delete(providerID)
	s.lastResetTime.Delete(providerID)

	global.APP_LOG.Debug("原子性删除Provider状态及重置时间记录",
		zap.Uint("providerID", providerID))
}

// startPmacctCollection 启动pmacct流量数据收集任务
// 为每个启用流量控制的Provider独立管理采集周期
func (s *MonitoringSchedulerService) startPmacctCollection(ctx context.Context) {
	// 确保ticker在panic时也能停止，防止goroutine泄漏
	var checkTicker *time.Ticker
	var cleanupTicker *time.Ticker
	defer func() {
		if checkTicker != nil {
			checkTicker.Stop()
		}
		if cleanupTicker != nil {
			cleanupTicker.Stop()
		}
		if r := recover(); r != nil {
			global.APP_LOG.Error("pmacct流量收集主循环panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("pmacct数据收集任务已停止")
	}()

	global.APP_LOG.Info("启动pmacct流量数据收集任务")

	// 等待数据库初始化
	for global.APP_DB == nil {
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-s.stopChan:
			timer.Stop()
			return
		case <-timer.C:
			timer.Stop()
			continue
		}
	}

	// 主循环：周期性检查哪些provider需要采集
	checkInterval := 30 * time.Second
	checkTicker = time.NewTicker(checkInterval)

	// 定期清理已删除provider的状态（3分钟）
	cleanupTicker = time.NewTicker(3 * time.Minute)

	for {
		select {
		case <-s.stopChan:
			return

		case <-cleanupTicker.C:
			// 清理过期状态（15分钟未访问）
			s.providerStateManager.CleanupExpired(15 * time.Minute)

			// 重置长时间采集中的状态（防止死锁）
			s.providerStateManager.ResetIfCollectingTooLong(5 * time.Minute)

			// 从数据库查询有效的provider ID并清理已删除的
			var validProviderIDs []uint
			if err := global.APP_DB.Model(&providerModel.Provider{}).
				Pluck("id", &validProviderIDs).Error; err == nil {
				s.providerStateManager.CleanupDeleted(validProviderIDs)
			}

			// 清理已删除instance的pmacct重置时间记录
			s.cleanupDeletedInstanceResetTime()

		case <-checkTicker.C:
			// 获取所有启用流量控制的Provider（只查询必要字段）
			var providers []struct {
				ID                      uint
				Name                    string
				TrafficCollectInterval  int
				TrafficCollectBatchSize int
			}

			err := global.APP_DB.Model(&providerModel.Provider{}).
				Where("enable_traffic_control = ?", true).
				Select("id, name, traffic_collect_interval, traffic_collect_batch_size").
				Find(&providers).Error

			if err != nil {
				global.APP_LOG.Error("查询启用流量控制的Provider失败", zap.Error(err))
				continue
			}

			if len(providers) == 0 {
				continue
			}

			now := time.Now()

			for _, p := range providers {
				state := s.providerStateManager.GetOrCreate(p.ID)

				// 检查是否正在采集中
				if state.IsCollecting() {
					global.APP_LOG.Debug("Provider正在采集中，跳过",
						zap.Uint("providerID", p.ID),
						zap.String("providerName", p.Name))
					continue
				}

				// 判断是否到达采集间隔
				collectInterval := time.Duration(p.TrafficCollectInterval) * time.Second
				if collectInterval < 60*time.Second {
					collectInterval = 60 * time.Second
				}

				batchSize := p.TrafficCollectBatchSize
				if batchSize <= 0 {
					batchSize = 10
				}

				lastCollect := state.GetLastCollect()
				if !lastCollect.IsZero() && now.Sub(lastCollect) < collectInterval {
					continue
				}

				// 尝试获取采集锁
				if !state.StartCollecting() {
					continue // 其他goroutine已经开始采集
				}

				// 更新状态并开始新轮次
				roundID := state.UpdateLastCollect()

				global.APP_LOG.Info("开始新的流量采集轮次",
					zap.Uint("providerID", p.ID),
					zap.String("providerName", p.Name),
					zap.Int64("roundID", roundID),
					zap.Int("batchSize", batchSize))

				// 使用WaitGroup追踪异步采集goroutine
				s.wg.Add(1)
				go func(providerID uint, providerName string, roundID int64, batchSize int) {
					// 多层 defer 确保状态一定会被释放
					defer s.wg.Done()

					// 第一层：确保状态解锁（最外层，一定会执行）
					defer func() {
						state := s.providerStateManager.GetOrCreate(providerID)
						state.FinishCollecting()
						global.APP_LOG.Debug("Provider采集完成，解锁状态",
							zap.Uint("providerID", providerID),
							zap.Int64("roundID", roundID))
					}()

					// 第二层：panic恢复
					defer func() {
						if r := recover(); r != nil {
							global.APP_LOG.Error("Provider流量采集goroutine panic",
								zap.Uint("providerID", providerID),
								zap.String("providerName", providerName),
								zap.Int64("roundID", roundID),
								zap.Any("panic", r),
								zap.Stack("stack"))
						}
					}()

					// 第三层：超时保护
					ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
					defer cancel()

					// 检查服务是否已停止
					select {
					case <-s.stopChan:
						global.APP_LOG.Info("服务已停止，取消采集",
							zap.Uint("providerID", providerID),
							zap.Int64("roundID", roundID))
						return
					case <-ctx.Done():
						global.APP_LOG.Error("采集超时（启动阶段）",
							zap.Uint("providerID", providerID),
							zap.Int64("roundID", roundID))
						return
					default:
					}

					// 直接执行采集，不再嵌套goroutine
					err := s.collectProviderTrafficInBatches(providerID, batchSize, roundID)
					if err != nil {
						global.APP_LOG.Error("Provider流量批量采集失败",
							zap.Uint("providerID", providerID),
							zap.String("providerName", providerName),
							zap.Int64("roundID", roundID),
							zap.Error(err))
					} else {
						global.APP_LOG.Info("Provider流量采集完成",
							zap.Uint("providerID", providerID),
							zap.String("providerName", providerName),
							zap.Int64("轮次ID", roundID))
					}
				}(p.ID, p.Name, roundID, batchSize)
			}
		}
	}
}

// startCleanupTask 启动清理任务，定期清理过期的pmacct数据
func (s *MonitoringSchedulerService) startCleanupTask(ctx context.Context) {
	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	var ticker *time.Ticker
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
		if r := recover(); r != nil {
			global.APP_LOG.Error("pmacct数据清理任务panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("pmacct数据清理任务已停止")
	}()

	global.APP_LOG.Info("启动pmacct数据清理任务")

	// 等待数据库初始化
	for global.APP_DB == nil {
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-s.stopChan:
			timer.Stop()
			return
		case <-timer.C:
			timer.Stop()
			continue
		}
	}

	// 每小时执行一次状态修复，每天凌晨3点执行数据清理
	ticker = time.NewTicker(1 * time.Hour)

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			now := time.Now()

			// 每小时执行实例状态修复
			global.APP_LOG.Debug("开始修复卡住的实例状态")
			cleanupService := &system.InstanceCleanupService{}
			if err := cleanupService.RepairStuckInstances(); err != nil {
				global.APP_LOG.Error("修复卡住的实例状态失败", zap.Error(err))
			}

			// 只在凌晨3点执行数据清理
			if now.Hour() == 3 {
				global.APP_LOG.Info("开始清理过期的pmacct数据")
				if err := s.pmacctService.CleanupOldPmacctData(90); err != nil {
					global.APP_LOG.Error("清理过期pmacct数据失败", zap.Error(err))
				} else {
					global.APP_LOG.Info("清理过期pmacct数据成功")
				}
			}
		}
	}
}

// startPmacctResetTask 启动pmacct守护进程重置任务
// 每天定期重置所有pmacct守护进程，清空SQLite数据库并重启
// 避免SQLite文件过大和数据累积问题
func (s *MonitoringSchedulerService) startPmacctResetTask(ctx context.Context) {
	// 确俟ticker在panic时也能停止，防止goroutine泄漏
	var ticker *time.Ticker
	defer func() {
		if ticker != nil {
			ticker.Stop()
		}
		if r := recover(); r != nil {
			global.APP_LOG.Error("pmacct守护进程重置任务panic",
				zap.Any("panic", r),
				zap.Stack("stack"))
		}
		global.APP_LOG.Info("pmacct守护进程重置任务已停止")
	}()

	global.APP_LOG.Info("启动pmacct守护进程重置任务")

	// 等待数据库初始化
	for global.APP_DB == nil {
		timer := time.NewTimer(10 * time.Second)
		select {
		case <-s.stopChan:
			timer.Stop()
			return
		case <-timer.C:
			timer.Stop()
			continue
		}
	}

	// 每小时检查一次
	ticker = time.NewTicker(1 * time.Hour)

	for {
		select {
		case <-s.stopChan:
			return
		case <-ticker.C:
			now := time.Now()

			// 只在凌晨4点执行（与清理任务错开1小时）
			if now.Hour() != 4 {
				continue
			}

			global.APP_LOG.Info("开始重置pmacct守护进程")

			// 获取所有启用流量控制的Provider（只查询需要的字段）
			var providers []struct {
				ID   uint
				Name string
			}

			if err := global.APP_DB.Model(&providerModel.Provider{}).
				Where("enable_traffic_control = ?", true).
				Select("id, name").
				Find(&providers).Error; err != nil {
				global.APP_LOG.Error("查询启用流量控制的Provider失败", zap.Error(err))
				continue
			}

			// 清理已删除provider的重置记录
			validIDSet := make(map[uint]bool)
			for _, p := range providers {
				validIDSet[p.ID] = true
			}

			s.lastResetTime.Range(func(key, value interface{}) bool {
				providerID := key.(uint)
				if !validIDSet[providerID] {
					s.lastResetTime.Delete(providerID)
				}
				return true
			})

			for _, p := range providers {
				// 检查是否需要重置（每天重置一次）
				if value, ok := s.lastResetTime.Load(p.ID); ok {
					lastReset := value.(time.Time)
					if time.Since(lastReset) < 24*time.Hour {
						continue
					}
				}

				// 获取该Provider下所有启用监控的实例
				var monitors []monitoringModel.PmacctMonitor
				if err := global.APP_DB.Where("provider_id = ? AND is_enabled = ?", p.ID, true).
					Find(&monitors).Error; err != nil {
					global.APP_LOG.Error("查询Provider监控实例失败",
						zap.Uint("providerID", p.ID),
						zap.Error(err))
					continue
				}

				if len(monitors) == 0 {
					continue
				}

				global.APP_LOG.Info("开始重置Provider的pmacct守护进程",
					zap.Uint("providerID", p.ID),
					zap.String("providerName", p.Name),
					zap.Int("instanceCount", len(monitors)))

				successCount := 0
				failCount := 0

				// 逐个重置实例的pmacct守护进程
				for _, monitor := range monitors {
					if err := s.pmacctService.ResetPmacctDaemon(monitor.InstanceID); err != nil {
						global.APP_LOG.Error("重置pmacct守护进程失败",
							zap.Uint("instanceID", monitor.InstanceID),
							zap.Error(err))
						failCount++
					} else {
						global.APP_LOG.Info("重置pmacct守护进程成功",
							zap.Uint("instanceID", monitor.InstanceID))
						successCount++
					}

					// 每个实例之间间隔2秒，避免对provider造成压力
					time.Sleep(2 * time.Second)
				}

				// 更新最后重置时间
				s.lastResetTime.Store(p.ID, now)

				global.APP_LOG.Info("Provider的pmacct守护进程重置完成",
					zap.Uint("providerID", p.ID),
					zap.String("providerName", p.Name),
					zap.Int("success", successCount),
					zap.Int("failed", failCount))
			}

			global.APP_LOG.Info("pmacct守护进程重置任务完成")
		}
	}
}

// cleanupDeletedInstanceResetTime 清理已删除instance的重置时间记录
// 改进：添加独立的时间过期机制，不依赖数据库查询
func (s *MonitoringSchedulerService) cleanupDeletedInstanceResetTime() {
	s.mu.Lock()
	now := time.Now()

	// 限制清理频率（最多每小时一次）
	if now.Sub(s.lastResetCleanup) < 1*time.Hour {
		s.mu.Unlock()
		return
	}
	s.lastResetCleanup = now
	s.mu.Unlock()

	maxAge := 7 * 24 * time.Hour // 7天未更新的记录视为过期
	cleaned := 0

	// 首先按时间过期清理
	s.lastResetTime.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		lastReset := value.(time.Time)

		// 超过7天的记录直接删除（防止无限增长）
		if now.Sub(lastReset) > maxAge {
			s.lastResetTime.Delete(providerID)
			cleaned++
			global.APP_LOG.Debug("清理过期的pmacct重置时间记录",
				zap.Uint("providerID", providerID),
				zap.Duration("age", now.Sub(lastReset)))
		}
		return true
	})

	if cleaned > 0 {
		global.APP_LOG.Info("按时间清理pmacct重置时间记录",
			zap.Int("cleaned", cleaned))
	}

	// 然后尝试按数据库有效性清理（可选，允许失败）
	if global.APP_DB == nil {
		global.APP_LOG.Debug("数据库未初始化，跳过数据库验证清理")
		return
	}

	var validProviderIDs []uint
	if err := global.APP_DB.Model(&providerModel.Provider{}).
		Pluck("id", &validProviderIDs).Error; err != nil {
		global.APP_LOG.Warn("查询有效Provider列表失败，跳过数据库验证清理", zap.Error(err))
		return
	}

	// 构建有效ID集合
	validSet := make(map[uint]bool, len(validProviderIDs))
	for _, id := range validProviderIDs {
		validSet[id] = true
	}

	// 清理不在有效列表中的记录
	cleanedDB := 0
	s.lastResetTime.Range(func(key, value interface{}) bool {
		providerID := key.(uint)
		if !validSet[providerID] {
			s.lastResetTime.Delete(providerID)
			cleanedDB++
		}
		return true
	})

	if cleanedDB > 0 {
		global.APP_LOG.Info("按数据库清理已删除Provider的pmacct重置时间记录",
			zap.Int("cleaned", cleanedDB))
	}
}

// collectProviderTrafficInBatches 分批采集Provider的流量数据，确保一轮内不重复采集
func (s *MonitoringSchedulerService) collectProviderTrafficInBatches(providerID uint, batchSize int, roundID int64) error {
	// 获取该Provider下所有启用的监控实例（只查询需要的字段，避免加载所有数据）
	var totalCount int64
	err := global.APP_DB.Model(&monitoringModel.PmacctMonitor{}).
		Where("provider_id = ? AND is_enabled = ?", providerID, true).
		Count(&totalCount).Error
	if err != nil {
		return fmt.Errorf("统计Provider监控数量失败: %w", err)
	}

	if totalCount == 0 {
		global.APP_LOG.Debug("Provider无活跃监控", zap.Uint("providerID", providerID))
		return nil
	}

	global.APP_LOG.Info("开始分批采集pmacct数据",
		zap.Uint("providerID", providerID),
		zap.Int64("roundID", roundID),
		zap.Int64("totalMonitors", totalCount),
		zap.Int("batchSize", batchSize))

	// 分批查询和处理，避免一次性加载所有数据导致内存暴增
	processedCount := 0
	for offset := 0; offset < int(totalCount); offset += batchSize {
		// 批量查询monitors
		var monitors []monitoringModel.PmacctMonitor
		if err := global.APP_DB.Where("provider_id = ? AND is_enabled = ?", providerID, true).
			Limit(batchSize).
			Offset(offset).
			Find(&monitors).Error; err != nil {
			global.APP_LOG.Error("查询监控实例失败",
				zap.Uint("providerID", providerID),
				zap.Int("offset", offset),
				zap.Error(err))
			continue
		}

		if len(monitors) == 0 {
			break
		}

		// 批量预加载instances
		instanceIDs := make([]uint, len(monitors))
		for i, m := range monitors {
			instanceIDs[i] = m.InstanceID
		}

		var instances []providerModel.Instance
		if err := global.APP_DB.Where("id IN ?", instanceIDs).Find(&instances).Error; err != nil {
			global.APP_LOG.Error("预加载实例数据失败",
				zap.Uint("providerID", providerID),
				zap.Error(err))
			continue
		}

		// 构建instance映射
		instanceMap := make(map[uint]*providerModel.Instance)
		for i := range instances {
			instanceMap[instances[i].ID] = &instances[i]
		}

		// 为本批次的每个监控实例采集数据（从SQLite同步到MySQL）
		for _, monitor := range monitors {
			instance := instanceMap[monitor.InstanceID]
			if instance == nil {
				global.APP_LOG.Warn("实例不存在",
					zap.Uint("instanceID", monitor.InstanceID))
				continue
			}

			// 使用 CollectTrafficFromSQLite 从 Provider 的 SQLite 数据库采集数据
			// 传入预加载的数据，避免函数内部重复查询
			if err := s.pmacctService.CollectTrafficFromSQLite(instance, &monitor); err != nil {
				global.APP_LOG.Error("从SQLite采集流量数据失败",
					zap.Uint("monitorID", monitor.ID),
					zap.Uint("instanceID", monitor.InstanceID),
					zap.Error(err))
				// 继续处理其他监控，不中断
			} else {
				global.APP_LOG.Debug("SQLite流量数据采集成功",
					zap.Uint("instanceID", monitor.InstanceID))
			}
			processedCount++
		}

		global.APP_LOG.Debug("完成批次采集",
			zap.Uint("providerID", providerID),
			zap.Int64("roundID", roundID),
			zap.Int("batchIndex", offset/batchSize+1),
			zap.Int("batchSize", len(monitors)),
			zap.Int("processedTotal", processedCount),
			zap.Int64("total", totalCount))

		// 批次间短暂延迟，避免过载
		if offset+batchSize < int(totalCount) {
			time.Sleep(2 * time.Second)
		}
	}

	// 从TrafficRecord同步Provider流量统计
	year, month, _ := time.Now().Date()
	var totalUsedFloat float64
	err = global.APP_DB.Model(&monitoringModel.PmacctTrafficRecord{}).
		Where("provider_id = ? AND year = ? AND month = ?", providerID, year, int(month)).
		Select("COALESCE(SUM(total_bytes)/1048576, 0)").
		Scan(&totalUsedFloat).Error

	if err != nil {
		return fmt.Errorf("统计Provider流量失败: %w", err)
	}
	totalUsed := int64(totalUsedFloat)

	// 不再更新Provider.used_traffic字段（已删除）
	// 流量数据统一从pmacct_traffic_records实时聚合查询

	global.APP_LOG.Info("流量采集轮次完成",
		zap.Uint("providerID", providerID),
		zap.Int64("roundID", roundID),
		zap.Int("processedCount", processedCount),
		zap.Int64("totalCount", totalCount),
		zap.Int64("totalTrafficMB", totalUsed))

	return nil
}

// checkProviderTrafficLimits 检查Provider的流量限制（独立于采集周期）
func (s *MonitoringSchedulerService) checkProviderTrafficLimits(ctx context.Context, providerID uint) error {
	// 获取该Provider下所有实例
	var instances []providerModel.Instance
	err := global.APP_DB.Where("provider_id = ? AND status NOT IN ?",
		providerID, []string{"deleted", "deleting"}).
		Select("id, name, max_traffic, status, traffic_limited").
		Find(&instances).Error

	if err != nil {
		return fmt.Errorf("查询Provider实例失败: %w", err)
	}

	// 批量查询实例流量数据（从pmacct_traffic_records实时聚合）
	year, month, _ := time.Now().Date()
	trafficMap := make(map[uint]int64)

	if len(instances) > 0 {
		instanceIDs := make([]uint, len(instances))
		for i, inst := range instances {
			instanceIDs[i] = inst.ID
		}

		var trafficRecords []struct {
			InstanceID uint
			TotalMB    float64
		}

		err = global.APP_DB.Model(&monitoringModel.PmacctTrafficRecord{}).
			Where("provider_id = ? AND year = ? AND month = ? AND instance_id IN ?",
				providerID, year, int(month), instanceIDs).
			Select("instance_id, SUM(total_bytes)/1048576 as total_mb").
			Group("instance_id").
			Scan(&trafficRecords).Error

		if err != nil {
			return fmt.Errorf("查询实例流量记录失败: %w", err)
		}

		for _, record := range trafficRecords {
			trafficMap[record.InstanceID] = int64(record.TotalMB)
		}
	}

	// 检查每个实例的流量限制
	for _, instance := range instances {
		usedTraffic := trafficMap[instance.ID] // 从实时查询获取流量
		if instance.MaxTraffic > 0 && usedTraffic >= instance.MaxTraffic {
			// 流量超限，需要暂停实例
			if !instance.TrafficLimited && instance.Status != "stopped" && instance.Status != "suspended" {
				global.APP_LOG.Warn("实例流量超限",
					zap.Uint("instanceID", instance.ID),
					zap.String("instanceName", instance.Name),
					zap.Int64("usedTraffic", usedTraffic),
					zap.Int64("maxTraffic", instance.MaxTraffic))

				// 标记流量超限
				if err := global.APP_DB.Model(&providerModel.Instance{}).
					Where("id = ?", instance.ID).
					Updates(map[string]interface{}{
						"traffic_limited":      true,
						"traffic_limit_reason": "instance",
					}).Error; err != nil {
					global.APP_LOG.Error("标记实例流量超限失败",
						zap.Uint("instanceID", instance.ID),
						zap.Error(err))
				}

				// 实例流量超限处理：创建停止任务
				// 检查实例是否已经是停止状态
				if instance.Status != "stopped" && instance.Status != "stopping" {
					// 检查是否已有停止任务在执行
					var existingStopTask struct{}
					notFound := global.APP_DB.Model(&adminModel.Task{}).
						Select("1").
						Where("instance_id = ? AND task_type = 'stop' AND status IN ('pending', 'running')", instance.ID).
						First(&existingStopTask).Error == gorm.ErrRecordNotFound

					if notFound {
						// 创建停止任务
						stopTaskData := fmt.Sprintf(`{"instance_id":%d,"reason":"traffic_limit"}`, instance.ID)
						stopTask := &adminModel.Task{
							UserID:          instance.UserID,
							ProviderID:      &instance.ProviderID,
							InstanceID:      &instance.ID,
							TaskType:        "stop",
							Status:          "pending",
							TaskData:        stopTaskData,
							TimeoutDuration: 300, // 5分钟超时
						}

						if err := global.APP_DB.Create(stopTask).Error; err != nil {
							global.APP_LOG.Error("创建实例停止任务失败",
								zap.Uint("instanceID", instance.ID),
								zap.Error(err))
						} else {
							global.APP_LOG.Info("已创建实例停止任务（流量超限）",
								zap.Uint("instanceID", instance.ID),
								zap.Uint("taskID", stopTask.ID))
						}
					}
				}
			}
		} else if instance.TrafficLimited && usedTraffic < instance.MaxTraffic {
			// 流量恢复正常，清除限制标记
			global.APP_LOG.Info("实例流量恢复正常",
				zap.Uint("instanceID", instance.ID),
				zap.String("instanceName", instance.Name))

			if err := global.APP_DB.Model(&providerModel.Instance{}).
				Where("id = ?", instance.ID).
				Updates(map[string]interface{}{
					"traffic_limited":      false,
					"traffic_limit_reason": "",
				}).Error; err != nil {
				global.APP_LOG.Error("清除实例流量限制标记失败",
					zap.Uint("instanceID", instance.ID),
					zap.Error(err))
			}
		}
	}

	return nil
}
