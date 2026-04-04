package resources

import (
	"fmt"
	"runtime"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/system"

	"go.uber.org/zap"
)

type MonitoringService struct{}

var startTime = time.Now()

// GetSystemStats 获取系统统计信息
func (s *MonitoringService) GetSystemStats() system.SystemStats {
	return system.SystemStats{
		CPU:       s.getCPUStats(),
		Memory:    s.getMemoryStats(),
		Disk:      s.getDiskStats(),
		Network:   s.getNetworkStats(),
		Database:  s.getDatabaseStats(),
		Runtime:   s.getRuntimeStats(),
		Timestamp: time.Now(),
	}
}

// CheckHealth 检查系统健康状态
func (s *MonitoringService) CheckHealth() map[string]string {
	dbHealth := s.checkDatabaseHealth()
	diskHealth := s.checkDiskHealth()
	memHealth := s.checkMemoryHealth()

	overall := "healthy"
	if dbHealth == "unhealthy" || diskHealth == "unhealthy" || memHealth == "unhealthy" {
		overall = "unhealthy"
	} else if dbHealth == "warning" || diskHealth == "warning" || memHealth == "warning" {
		overall = "warning"
	}

	return map[string]string{
		"database": dbHealth,
		"disk":     diskHealth,
		"memory":   memHealth,
		"status":   overall,
	}
}

// GeneratePrometheusMetrics 生成Prometheus格式的指标
func (s *MonitoringService) GeneratePrometheusMetrics() string {
	runtimeStats := s.getRuntimeStats()
	memStats := s.getMemoryStats()
	cpuStats := s.getCPUStats()

	metrics := `# HELP oneclickvirt_goroutines Number of goroutines
# TYPE oneclickvirt_goroutines gauge
oneclickvirt_goroutines %d

# HELP oneclickvirt_heap_alloc Bytes allocated and still in use
# TYPE oneclickvirt_heap_alloc gauge  
oneclickvirt_heap_alloc %d

# HELP oneclickvirt_heap_sys Bytes obtained from system
# TYPE oneclickvirt_heap_sys gauge
oneclickvirt_heap_sys %d

# HELP oneclickvirt_memory_usage Memory usage percentage
# TYPE oneclickvirt_memory_usage gauge
oneclickvirt_memory_usage %.2f

# HELP oneclickvirt_cpu_cores Number of CPU cores
# TYPE oneclickvirt_cpu_cores gauge
oneclickvirt_cpu_cores %d

# HELP oneclickvirt_cpu_usage CPU usage percentage
# TYPE oneclickvirt_cpu_usage gauge
oneclickvirt_cpu_usage %.2f
`

	return fmt.Sprintf(metrics,
		runtimeStats.Goroutines,
		runtimeStats.HeapAlloc,
		runtimeStats.HeapSys,
		memStats.Usage,
		cpuStats.Cores,
		cpuStats.Usage,
	)
}

// getCPUStats 获取CPU统计信息
func (s *MonitoringService) getCPUStats() system.CPUStats {
	// 使用 runtime.NumCPU() 获取CPU核心数
	cores := runtime.NumCPU()

	// 获取系统负载平均值（在类Unix系统上）
	// 这里提供一个简化的实现，如需要更准确的CPU使用率，可以使用第三方库如 gopsutil
	usage := s.calculateCPUUsage()

	return system.CPUStats{
		Usage:     usage,
		Cores:     cores,
		LoadAvg1:  s.getLoadAverage(1),
		LoadAvg5:  s.getLoadAverage(5),
		LoadAvg15: s.getLoadAverage(15),
	}
}

// getMemoryStats 获取内存统计信息
func (s *MonitoringService) getMemoryStats() system.MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// 获取系统级别的内存信息
	// 这里使用一个更实际的方法来估算系统内存
	systemMemory := s.getSystemMemoryInfo()

	return system.MemoryStats{
		Total:     systemMemory.Total,
		Used:      systemMemory.Used,
		Free:      systemMemory.Free,
		Usage:     systemMemory.Usage,
		SwapTotal: systemMemory.SwapTotal,
		SwapUsed:  systemMemory.SwapUsed,
	}
}

// getDiskStats 获取磁盘统计信息
func (s *MonitoringService) getDiskStats() system.DiskStats {
	stat, err := s.getDiskUsage("/")
	if err != nil {
		global.APP_LOG.Warn("获取磁盘使用情况失败", zap.Error(err))
		return system.DiskStats{}
	}
	return *stat
}

// getDatabaseStats 获取数据库统计信息
func (s *MonitoringService) getDatabaseStats() system.DatabaseStats {
	stats := system.DatabaseStats{
		Uptime: time.Since(startTime).String(),
	}

	if global.APP_DB != nil {
		sqlDB, err := global.APP_DB.DB()
		if err == nil {
			stats.Connections = sqlDB.Stats().OpenConnections
			stats.MaxConnections = sqlDB.Stats().MaxOpenConnections
		}
	}

	return stats
}

// getRuntimeStats 获取Go运行时统计信息
func (s *MonitoringService) getRuntimeStats() system.RuntimeStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	return system.RuntimeStats{
		Goroutines: runtime.NumGoroutine(),
		HeapAlloc:  m.HeapAlloc,
		HeapSys:    m.HeapSys,
		HeapIdle:   m.HeapIdle,
		HeapInuse:  m.HeapInuse,
		GCCycles:   m.NumGC,
		LastGC:     time.Unix(0, int64(m.LastGC)),
		Uptime:     time.Since(startTime).String(),
	}
}

// checkDatabaseHealth 检查数据库健康状态
func (s *MonitoringService) checkDatabaseHealth() string {
	if global.APP_DB == nil {
		return "unhealthy"
	}

	sqlDB, err := global.APP_DB.DB()
	if err != nil {
		return "unhealthy"
	}

	if err := sqlDB.Ping(); err != nil {
		global.APP_LOG.Warn("数据库健康检查失败", zap.Error(err))
		return "unhealthy"
	}

	return "healthy"
}

// checkDiskHealth 检查磁盘健康状态
func (s *MonitoringService) checkDiskHealth() string {
	diskStats := s.getDiskStats()
	if diskStats.Usage > 90 {
		return "warning"
	}
	return "healthy"
}

// checkMemoryHealth 检查内存健康状态
func (s *MonitoringService) checkMemoryHealth() string {
	memStats := s.getMemoryStats()
	if memStats.Usage > 90 {
		return "warning"
	}
	return "healthy"
}

// GetSystemLogs 获取系统日志
func (s *MonitoringService) GetSystemLogs(level, limit, offset string) map[string]interface{} {
	// 这里是占位实现，实际需要根据具体日志存储方式实现
	logs := []map[string]interface{}{
		{
			"timestamp": time.Now().Format("2006-01-02 15:04:05"),
			"level":     "info",
			"message":   "系统运行正常",
			"source":    "system",
		},
		{
			"timestamp": time.Now().Add(-time.Minute).Format("2006-01-02 15:04:05"),
			"level":     "info",
			"message":   "用户登录",
			"source":    "auth",
		},
	}

	return map[string]interface{}{
		"logs":   logs,
		"total":  len(logs),
		"level":  level,
		"limit":  limit,
		"offset": offset,
	}
}

// GetOperationLogs 获取操作审计日志
func (s *MonitoringService) GetOperationLogs(userID, action, startTime, endTime, limit, offset string) map[string]interface{} {
	// 这里是占位实现，实际需要根据审计日志存储方式实现
	auditLogs := []map[string]interface{}{
		{
			"id":         1,
			"user_id":    123,
			"username":   "admin",
			"action":     "login",
			"resource":   "auth",
			"ip_address": "192.168.1.100",
			"user_agent": "Mozilla/5.0...",
			"timestamp":  time.Now().Format("2006-01-02 15:04:05"),
			"status":     "success",
			"details":    "用户登录成功",
		},
		{
			"id":         2,
			"user_id":    123,
			"username":   "admin",
			"action":     "create_user",
			"resource":   "user",
			"ip_address": "192.168.1.100",
			"user_agent": "Mozilla/5.0...",
			"timestamp":  time.Now().Add(-time.Hour).Format("2006-01-02 15:04:05"),
			"status":     "success",
			"details":    "创建用户成功",
		},
	}

	return map[string]interface{}{
		"logs":       auditLogs,
		"total":      len(auditLogs),
		"user_id":    userID,
		"action":     action,
		"start_time": startTime,
		"end_time":   endTime,
		"limit":      limit,
		"offset":     offset,
	}
}

// estimateMemoryFromRuntime 当 /proc/meminfo 不可用时的降级方案
func (s *MonitoringService) estimateMemoryFromRuntime() system.MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	estimatedTotal := uint64(8 * 1024 * 1024 * 1024)
	used := m.HeapAlloc + m.StackSys + m.MSpanSys + m.MCacheSys + m.OtherSys
	if used > estimatedTotal {
		used = estimatedTotal
	}
	free := estimatedTotal - used
	return system.MemoryStats{
		Total: estimatedTotal,
		Used:  used,
		Free:  free,
		Usage: float64(used) / float64(estimatedTotal) * 100,
	}
}
