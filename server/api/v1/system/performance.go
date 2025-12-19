package system

import (
	"context"
	"net/http"
	"runtime"
	"sync"
	"time"

	"oneclickvirt/global"
	monitoringModel "oneclickvirt/model/monitoring"
	"oneclickvirt/service/task"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// PerformanceMetrics 性能指标
type PerformanceMetrics struct {
	// 系统基础指标
	Timestamp      time.Time `json:"timestamp"`
	GoroutineCount int       `json:"goroutine_count"`
	CPUCount       int       `json:"cpu_count"`

	// 内存指标 (MB)
	MemoryAlloc      uint64 `json:"memory_alloc"`       // 当前分配的内存
	MemoryTotalAlloc uint64 `json:"memory_total_alloc"` // 累计分配的内存
	MemorySys        uint64 `json:"memory_sys"`         // 从系统获取的内存
	MemoryHeapAlloc  uint64 `json:"memory_heap_alloc"`  // 堆上分配的内存
	MemoryHeapSys    uint64 `json:"memory_heap_sys"`    // 堆从系统获取的内存
	MemoryStackInuse uint64 `json:"memory_stack_inuse"` // 栈使用的内存

	// GC 指标
	GCCount      uint32 `json:"gc_count"`       // GC次数
	GCPauseTotal uint64 `json:"gc_pause_total"` // GC总暂停时间(ns)
	GCPauseAvg   uint64 `json:"gc_pause_avg"`   // GC平均暂停时间(ns)
	GCLastPause  uint64 `json:"gc_last_pause"`  // 上次GC暂停时间(ns)
	NextGC       uint64 `json:"next_gc"`        // 下次GC触发阈值

	// 数据库连接池状态
	DBStats *DBPoolStats `json:"db_stats,omitempty"`

	// 数据库管理器状态
	DBManagerStats *DBManagerStats `json:"db_manager_stats,omitempty"`

	// SSH 连接池状态
	SSHPoolStats *SSHPoolStats `json:"ssh_pool_stats,omitempty"`

	// 任务系统状态
	TaskStats *TaskSystemStats `json:"task_stats,omitempty"`
}

// DBManagerStats 数据库管理器统计
type DBManagerStats struct {
	Connected         bool   `json:"connected"`           // 是否已连接
	Reconnecting      bool   `json:"reconnecting"`        // 是否正在重连
	HeartbeatActive   bool   `json:"heartbeat_active"`    // 心跳是否活跃
	MaxReconnectRetry int    `json:"max_reconnect_retry"` // 最大重连次数
	ReconnectInterval string `json:"reconnect_interval"`  // 重连间隔
}

// DBPoolStats 数据库连接池统计
type DBPoolStats struct {
	MaxOpenConnections int           `json:"max_open_connections"` // 最大连接数
	OpenConnections    int           `json:"open_connections"`     // 当前打开的连接数
	InUse              int           `json:"in_use"`               // 正在使用的连接数
	Idle               int           `json:"idle"`                 // 空闲连接数
	WaitCount          int64         `json:"wait_count"`           // 等待连接的总次数
	WaitDuration       time.Duration `json:"wait_duration"`        // 等待连接的总时间
	MaxIdleClosed      int64         `json:"max_idle_closed"`      // 因超过最大空闲数而关闭的连接数
	MaxLifetimeClosed  int64         `json:"max_lifetime_closed"`  // 因超过最大生命周期而关闭的连接数
}

// SSHPoolStats SSH连接池统计
type SSHPoolStats struct {
	TotalConnections     int           `json:"total_connections"`     // 总连接数
	HealthyConnections   int           `json:"healthy_connections"`   // 健康连接数
	UnhealthyConnections int           `json:"unhealthy_connections"` // 不健康连接数
	IdleConnections      int           `json:"idle_connections"`      // 空闲连接数
	ActiveConnections    int           `json:"active_connections"`    // 活跃连接数
	MaxConnections       int           `json:"max_connections"`       // 最大连接数限制
	Utilization          float64       `json:"utilization"`           // 连接池利用率(%)
	MaxIdleTime          time.Duration `json:"max_idle_time"`         // 最大空闲时间配置
	MaxAge               time.Duration `json:"max_age"`               // 连接最大存活时间
	OldestConnectionAge  time.Duration `json:"oldest_connection_age"` // 最老连接年龄
	NewestConnectionAge  time.Duration `json:"newest_connection_age"` // 最新连接年龄
	AvgConnectionAge     time.Duration `json:"avg_connection_age"`    // 平均连接年龄
}

// TaskSystemStats 任务系统统计
type TaskSystemStats struct {
	RunningContexts int `json:"running_contexts"` // 运行中的任务上下文数量
	ProviderPools   int `json:"provider_pools"`   // Provider工作池数量
	TotalQueueSize  int `json:"total_queue_size"` // 总队列大小
}

// PerformanceHistory 性能历史记录
type PerformanceHistory struct {
	DataPoints []PerformanceMetrics `json:"data_points"`
	StartTime  time.Time            `json:"start_time"`
	EndTime    time.Time            `json:"end_time"`
}

// GetPerformanceMetrics 获取实时性能指标
func GetPerformanceMetrics(c *gin.Context) {
	metrics := collectPerformanceMetrics()

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取性能指标成功",
		"data": metrics,
	})
}

// GetPerformanceHistory 获取性能历史数据
func GetPerformanceHistory(c *gin.Context) {
	// 从查询参数获取时间范围
	durationStr := c.DefaultQuery("duration", "1h") // 默认1小时

	var duration time.Duration
	var err error

	switch durationStr {
	case "5m":
		duration = 5 * time.Minute
	case "15m":
		duration = 15 * time.Minute
	case "1h":
		duration = 1 * time.Hour
	case "6h":
		duration = 6 * time.Hour
	case "24h":
		duration = 24 * time.Hour
	default:
		duration, err = time.ParseDuration(durationStr)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{
				"code": 400,
				"msg":  "无效的时间范围参数",
			})
			return
		}
	}

	// 生成历史数据点（实际项目中应该从时序数据库或缓存中读取）
	history := generatePerformanceHistory(duration)

	c.JSON(http.StatusOK, gin.H{
		"code": 0,
		"msg":  "获取性能历史数据成功",
		"data": history,
	})
}

// collectPerformanceMetrics 收集性能指标
func collectPerformanceMetrics() *PerformanceMetrics {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	metrics := &PerformanceMetrics{
		Timestamp:        time.Now(),
		GoroutineCount:   runtime.NumGoroutine(),
		CPUCount:         runtime.NumCPU(),
		MemoryAlloc:      m.Alloc / 1024 / 1024,      // MB
		MemoryTotalAlloc: m.TotalAlloc / 1024 / 1024, // MB
		MemorySys:        m.Sys / 1024 / 1024,        // MB
		MemoryHeapAlloc:  m.HeapAlloc / 1024 / 1024,  // MB
		MemoryHeapSys:    m.HeapSys / 1024 / 1024,    // MB
		MemoryStackInuse: m.StackInuse / 1024 / 1024, // MB
		GCCount:          m.NumGC,
		GCPauseTotal:     m.PauseTotalNs,
		NextGC:           m.NextGC / 1024 / 1024, // MB
	}

	// 计算GC平均暂停时间
	if m.NumGC > 0 {
		metrics.GCPauseAvg = m.PauseTotalNs / uint64(m.NumGC)
		// 获取最后一次GC暂停时间
		if m.NumGC > 0 {
			metrics.GCLastPause = m.PauseNs[(m.NumGC+255)%256]
		}
	}

	// 收集数据库连接池状态
	if global.APP_DB != nil {
		if sqlDB, err := global.APP_DB.DB(); err == nil {
			stats := sqlDB.Stats()
			metrics.DBStats = &DBPoolStats{
				MaxOpenConnections: stats.MaxOpenConnections,
				OpenConnections:    stats.OpenConnections,
				InUse:              stats.InUse,
				Idle:               stats.Idle,
				WaitCount:          stats.WaitCount,
				WaitDuration:       stats.WaitDuration,
				MaxIdleClosed:      stats.MaxIdleClosed,
				MaxLifetimeClosed:  stats.MaxLifetimeClosed,
			}
		}
	}

	// 收集数据库管理器状态
	if dbManagerStats := global.APP_DB_MANAGER_STATS; dbManagerStats != nil {
		metrics.DBManagerStats = &DBManagerStats{
			Connected:         dbManagerStats.Connected,
			Reconnecting:      dbManagerStats.Reconnecting,
			HeartbeatActive:   dbManagerStats.HeartbeatActive,
			MaxReconnectRetry: dbManagerStats.MaxReconnectRetry,
			ReconnectInterval: dbManagerStats.ReconnectInterval,
		}
	}

	// 收集SSH连接池状态
	sshPool := utils.GetGlobalSSHPool()
	if sshPool != nil {
		enhancedStats := sshPool.GetEnhancedStats()
		metrics.SSHPoolStats = &SSHPoolStats{
			TotalConnections:     enhancedStats.TotalConnections,
			HealthyConnections:   enhancedStats.HealthyConnections,
			UnhealthyConnections: enhancedStats.UnhealthyConnections,
			IdleConnections:      enhancedStats.IdleConnections,
			ActiveConnections:    enhancedStats.ActiveConnections,
			MaxConnections:       enhancedStats.MaxConnections,
			Utilization:          enhancedStats.Utilization,
			MaxIdleTime:          enhancedStats.MaxIdleTime,
			MaxAge:               enhancedStats.MaxAge,
			OldestConnectionAge:  enhancedStats.OldestConnectionAge,
			NewestConnectionAge:  enhancedStats.NewestConnectionAge,
			AvgConnectionAge:     enhancedStats.AvgConnectionAge,
		}
	}

	// 收集任务系统状态
	taskService := task.GetTaskService()
	if taskService != nil {
		runningCtx, pools, queueSize := taskService.GetStats()
		metrics.TaskStats = &TaskSystemStats{
			RunningContexts: runningCtx,
			ProviderPools:   pools,
			TotalQueueSize:  queueSize,
		}
	}

	return metrics
}

// generatePerformanceHistory 生成性能历史数据
func generatePerformanceHistory(duration time.Duration) *PerformanceHistory {
	endTime := time.Now()
	startTime := endTime.Add(-duration)

	// 根据时间范围决定采样间隔
	var pointCount int

	switch {
	case duration <= 5*time.Minute:
		pointCount = 30
	case duration <= 1*time.Hour:
		pointCount = 60
	case duration <= 6*time.Hour:
		pointCount = 72
	default:
		pointCount = 144
	}

	dataPoints := make([]PerformanceMetrics, 0, pointCount)

	// 从数据库查询历史数据
	var dbMetrics []monitoringModel.PerformanceMetric
	err := global.APP_DB.Where("timestamp >= ? AND timestamp <= ?", startTime, endTime).
		Order("timestamp ASC").
		Find(&dbMetrics).Error

	if err != nil {
		global.APP_LOG.Warn("查询性能历史数据失败", zap.Error(err))
		// 返回当前数据点作为备选
		currentMetrics := collectPerformanceMetrics()
		dataPoints = append(dataPoints, *currentMetrics)
	} else if len(dbMetrics) == 0 {
		// 没有历史数据，返回当前数据点
		currentMetrics := collectPerformanceMetrics()
		dataPoints = append(dataPoints, *currentMetrics)
	} else {
		// 转换数据库记录为API响应格式
		for _, dbMetric := range dbMetrics {
			metric := PerformanceMetrics{
				Timestamp:        dbMetric.Timestamp,
				GoroutineCount:   dbMetric.GoroutineCount,
				CPUCount:         dbMetric.CPUCount,
				MemoryAlloc:      dbMetric.MemoryAlloc,
				MemoryTotalAlloc: dbMetric.MemoryTotalAlloc,
				MemorySys:        dbMetric.MemorySys,
				MemoryHeapAlloc:  dbMetric.MemoryHeapAlloc,
				MemoryHeapSys:    dbMetric.MemoryHeapSys,
				MemoryStackInuse: dbMetric.MemoryStackInuse,
				GCCount:          dbMetric.GCCount,
				GCPauseTotal:     dbMetric.GCPauseTotal,
				GCPauseAvg:       dbMetric.GCPauseAvg,
				GCLastPause:      dbMetric.GCLastPause,
				NextGC:           dbMetric.NextGC,
			}

			// 添加数据库连接池状态
			if dbMetric.DBMaxOpenConnections > 0 {
				metric.DBStats = &DBPoolStats{
					MaxOpenConnections: dbMetric.DBMaxOpenConnections,
					OpenConnections:    dbMetric.DBOpenConnections,
					InUse:              dbMetric.DBInUse,
					Idle:               dbMetric.DBIdle,
					WaitCount:          dbMetric.DBWaitCount,
					WaitDuration:       time.Duration(dbMetric.DBWaitDuration),
					MaxIdleClosed:      dbMetric.DBMaxIdleClosed,
					MaxLifetimeClosed:  dbMetric.DBMaxLifetimeClosed,
				}
			}

			// 添加SSH连接池状态
			if dbMetric.SSHTotalConnections > 0 || dbMetric.SSHHealthyConnections > 0 {
				metric.SSHPoolStats = &SSHPoolStats{
					TotalConnections:     dbMetric.SSHTotalConnections,
					HealthyConnections:   dbMetric.SSHHealthyConnections,
					UnhealthyConnections: dbMetric.SSHUnhealthyConnections,
					IdleConnections:      dbMetric.SSHIdleConnections,
					ActiveConnections:    dbMetric.SSHActiveConnections,
					MaxConnections:       dbMetric.SSHMaxConnections,
					Utilization:          dbMetric.SSHUtilization,
					OldestConnectionAge:  time.Duration(dbMetric.SSHOldestConnectionAge) * time.Second,
					NewestConnectionAge:  time.Duration(dbMetric.SSHNewestConnectionAge) * time.Second,
					AvgConnectionAge:     time.Duration(dbMetric.SSHAvgConnectionAge) * time.Second,
					// MaxIdleTime 和 MaxAge 不保存到数据库，因为它们是配置项
				}
			}

			// 添加任务系统状态
			if dbMetric.TaskProviderPools > 0 || dbMetric.TaskRunningContexts > 0 {
				metric.TaskStats = &TaskSystemStats{
					RunningContexts: dbMetric.TaskRunningContexts,
					ProviderPools:   dbMetric.TaskProviderPools,
					TotalQueueSize:  dbMetric.TaskTotalQueueSize,
				}
			}

			dataPoints = append(dataPoints, metric)
		}

		// 如果数据点过少，进行采样补充（简单的线性插值）
		// 或者添加当前数据点
		if len(dataPoints) < pointCount/2 {
			currentMetrics := collectPerformanceMetrics()
			dataPoints = append(dataPoints, *currentMetrics)
		}
	}

	return &PerformanceHistory{
		DataPoints: dataPoints,
		StartTime:  startTime,
		EndTime:    endTime,
	}
}

var (
	performanceMonitorOnce sync.Once
	cleanupOnce            sync.Once
)

// StartPerformanceMonitoring 启动性能监控（在应用启动时调用）
func StartPerformanceMonitoring() {
	performanceMonitorOnce.Do(func() {
		go performanceMonitoringLoop()
		// 启动定期清理任务（每天凌晨3点执行）
		cleanupOnce.Do(func() {
			go schedulePerformanceDataCleanup()
		})
	})
}

// performanceMonitoringLoop 性能监控循环
func performanceMonitoringLoop() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		metrics := collectPerformanceMetrics()

		// 记录关键指标
		global.APP_LOG.Info("性能指标",
			zap.Int("goroutines", metrics.GoroutineCount),
			zap.Uint64("memory_alloc_mb", metrics.MemoryAlloc),
			zap.Uint64("memory_sys_mb", metrics.MemorySys),
			zap.Uint32("gc_count", metrics.GCCount),
		)

		// 持久化性能指标到数据库
		if err := persistPerformanceMetrics(metrics); err != nil {
			global.APP_LOG.Error("持久化性能指标失败", zap.Error(err))
		}

		// 检查告警阈值
		checkPerformanceAlerts(metrics)
	}
}

// persistPerformanceMetrics 持久化性能指标到数据库
func persistPerformanceMetrics(metrics *PerformanceMetrics) error {
	if global.APP_DB == nil {
		return nil // 数据库未初始化时跳过
	}

	dbMetric := monitoringModel.PerformanceMetric{
		Timestamp:        metrics.Timestamp,
		GoroutineCount:   metrics.GoroutineCount,
		CPUCount:         metrics.CPUCount,
		MemoryAlloc:      metrics.MemoryAlloc,
		MemoryTotalAlloc: metrics.MemoryTotalAlloc,
		MemorySys:        metrics.MemorySys,
		MemoryHeapAlloc:  metrics.MemoryHeapAlloc,
		MemoryHeapSys:    metrics.MemoryHeapSys,
		MemoryStackInuse: metrics.MemoryStackInuse,
		GCCount:          metrics.GCCount,
		GCPauseTotal:     metrics.GCPauseTotal,
		GCPauseAvg:       metrics.GCPauseAvg,
		GCLastPause:      metrics.GCLastPause,
		NextGC:           metrics.NextGC,
	}

	// 保存数据库连接池状态
	if metrics.DBStats != nil {
		dbMetric.DBMaxOpenConnections = metrics.DBStats.MaxOpenConnections
		dbMetric.DBOpenConnections = metrics.DBStats.OpenConnections
		dbMetric.DBInUse = metrics.DBStats.InUse
		dbMetric.DBIdle = metrics.DBStats.Idle
		dbMetric.DBWaitCount = metrics.DBStats.WaitCount
		dbMetric.DBWaitDuration = int64(metrics.DBStats.WaitDuration)
		dbMetric.DBMaxIdleClosed = metrics.DBStats.MaxIdleClosed
		dbMetric.DBMaxLifetimeClosed = metrics.DBStats.MaxLifetimeClosed
	}

	// 保存SSH连接池状态
	if metrics.SSHPoolStats != nil {
		dbMetric.SSHTotalConnections = metrics.SSHPoolStats.TotalConnections
		dbMetric.SSHHealthyConnections = metrics.SSHPoolStats.HealthyConnections
		dbMetric.SSHUnhealthyConnections = metrics.SSHPoolStats.UnhealthyConnections
		dbMetric.SSHIdleConnections = metrics.SSHPoolStats.IdleConnections
		dbMetric.SSHActiveConnections = metrics.SSHPoolStats.ActiveConnections
		dbMetric.SSHMaxConnections = metrics.SSHPoolStats.MaxConnections
		dbMetric.SSHUtilization = metrics.SSHPoolStats.Utilization
		dbMetric.SSHOldestConnectionAge = int64(metrics.SSHPoolStats.OldestConnectionAge.Seconds())
		dbMetric.SSHNewestConnectionAge = int64(metrics.SSHPoolStats.NewestConnectionAge.Seconds())
		dbMetric.SSHAvgConnectionAge = int64(metrics.SSHPoolStats.AvgConnectionAge.Seconds())
	}

	// 保存任务系统状态
	if metrics.TaskStats != nil {
		dbMetric.TaskRunningContexts = metrics.TaskStats.RunningContexts
		dbMetric.TaskProviderPools = metrics.TaskStats.ProviderPools
		dbMetric.TaskTotalQueueSize = metrics.TaskStats.TotalQueueSize
	}

	// 使用create保存，不阻塞主流程
	if err := global.APP_DB.Create(&dbMetric).Error; err != nil {
		return err
	}

	return nil
}

// schedulePerformanceDataCleanup 定期清理性能数据（每天凌晨3点）
func schedulePerformanceDataCleanup() {
	// 计算到下一个凌晨3点的时间
	now := time.Now()
	next3AM := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
	if now.After(next3AM) {
		next3AM = next3AM.Add(24 * time.Hour)
	}
	initialDelay := next3AM.Sub(now)

	global.APP_LOG.Info("性能数据清理任务已安排",
		zap.Time("nextRun", next3AM),
		zap.Duration("initialDelay", initialDelay))

	// 使用可取消的timer等待，而不是阻塞式Sleep
	initialTimer := time.NewTimer(initialDelay)
	defer initialTimer.Stop()

	select {
	case <-global.APP_SHUTDOWN_CONTEXT.Done():
		global.APP_LOG.Info("性能数据清理任务在首次执行前已停止")
		return
	case <-initialTimer.C:
		// 执行第一次清理
		cleanupOldPerformanceMetrics(global.APP_SHUTDOWN_CONTEXT)
	}

	// 之后每24小时执行一次
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-global.APP_SHUTDOWN_CONTEXT.Done():
			global.APP_LOG.Info("性能数据清理任务已停止")
			return
		case <-ticker.C:
			cleanupOldPerformanceMetrics(global.APP_SHUTDOWN_CONTEXT)
		}
	}
}

// cleanupOldPerformanceMetrics 清理旧的性能指标数据（保留7天）
func cleanupOldPerformanceMetrics(ctx context.Context) {
	if global.APP_DB == nil {
		return
	}

	cutoffTime := time.Now().AddDate(0, 0, -7)

	global.APP_LOG.Info("开始清理旧性能指标数据",
		zap.Time("cutoffTime", cutoffTime))

	// 分批删除，避免一次性删除太多数据造成长事务
	batchSize := 1000
	totalDeleted := int64(0)

	for {
		// 检查是否需要停止
		select {
		case <-ctx.Done():
			global.APP_LOG.Info("性能数据清理被中断",
				zap.Int64("total_deleted", totalDeleted))
			return
		default:
		}

		// 执行单批删除
		result := global.APP_DB.Where("timestamp < ?", cutoffTime).
			Limit(batchSize).
			Delete(&monitoringModel.PerformanceMetric{})

		if result.Error != nil {
			global.APP_LOG.Error("清理旧性能指标数据失败",
				zap.Error(result.Error),
				zap.Int64("total_deleted", totalDeleted))
			return
		}

		if result.RowsAffected == 0 {
			break // 没有更多数据需要删除
		}

		totalDeleted += result.RowsAffected
		global.APP_LOG.Debug("批量清理旧性能指标数据",
			zap.Int64("batch_deleted", result.RowsAffected),
			zap.Int64("total_deleted", totalDeleted))

		// 使用可中断的sleep，避免持续占用数据库
		timer := time.NewTimer(100 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			global.APP_LOG.Info("性能数据清理在批次间被中断",
				zap.Int64("total_deleted", totalDeleted))
			return
		case <-timer.C:
			// 继续下一批
		}
	}

	if totalDeleted > 0 {
		global.APP_LOG.Info("性能数据清理完成",
			zap.Int64("total_deleted", totalDeleted),
			zap.Time("cutoff_time", cutoffTime))
	}
}

// checkPerformanceAlerts 检查性能告警
func checkPerformanceAlerts(metrics *PerformanceMetrics) {
	// Goroutine 数量告警
	if metrics.GoroutineCount > 1000 {
		global.APP_LOG.Warn("Goroutine数量过高",
			zap.Int("count", metrics.GoroutineCount),
			zap.String("level", getAlertLevel(metrics.GoroutineCount, 1000, 5000)))
	}

	// 内存使用告警
	if metrics.MemoryAlloc > 500 {
		global.APP_LOG.Warn("内存使用过高",
			zap.Uint64("alloc_mb", metrics.MemoryAlloc),
			zap.String("level", getAlertLevel(int(metrics.MemoryAlloc), 500, 1000)))
	}

	// 数据库连接池告警
	if metrics.DBStats != nil {
		utilization := float64(metrics.DBStats.InUse) / float64(metrics.DBStats.MaxOpenConnections) * 100
		if utilization > 80 {
			global.APP_LOG.Warn("数据库连接池使用率过高",
				zap.Float64("utilization", utilization),
				zap.Int("in_use", metrics.DBStats.InUse),
				zap.Int("max", metrics.DBStats.MaxOpenConnections))
		}
	}
}

// getAlertLevel 获取告警级别
func getAlertLevel(value, warningThreshold, criticalThreshold int) string {
	if value >= criticalThreshold {
		return "critical"
	} else if value >= warningThreshold {
		return "warning"
	}
	return "normal"
}
