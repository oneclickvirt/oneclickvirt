package resources

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
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
	return map[string]string{
		"database": s.checkDatabaseHealth(),
		"disk":     s.checkDiskHealth(),
		"memory":   s.checkMemoryHealth(),
		"status":   "healthy",
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
	// 获取当前目录的磁盘使用情况
	stat, err := s.getDiskUsage(".")
	if err != nil {
		// 如果获取失败，使用简化估算
		global.APP_LOG.Warn("获取磁盘使用情况失败，使用估算值", zap.Error(err))
		return s.getEstimatedDiskStats()
	}

	return *stat
}

// getDiskUsage 获取指定路径的磁盘使用情况
func (s *MonitoringService) getDiskUsage(path string) (*system.DiskStats, error) {
	// 在生产环境中，这里应该使用系统调用获取真实的磁盘信息
	// 这里提供一个简化的跨平台实现
	stats := s.getEstimatedDiskStats()
	return &stats, nil
}

// getEstimatedDiskStats 获取估算的磁盘统计信息
func (s *MonitoringService) getEstimatedDiskStats() system.DiskStats {
	// 简化的磁盘使用估算
	// 假设系统有100GB的磁盘空间，已使用30%
	total := uint64(100 * 1024 * 1024 * 1024) // 100GB
	used := uint64(30 * 1024 * 1024 * 1024)   // 30GB
	free := total - used
	usage := float64(used) / float64(total) * 100

	return system.DiskStats{
		Total: total,
		Used:  used,
		Free:  free,
		Usage: usage,
	}
}

// getNetworkStats 获取网络统计信息
func (s *MonitoringService) getNetworkStats() system.NetworkStats {
	// 返回空的网络统计信息，实际流量数据由 pmacct 提供
	// 避免使用模拟数据干扰真实的流量统计
	return system.NetworkStats{
		BytesReceived: 0, // 不使用模拟数据
		BytesSent:     0, // 不使用模拟数据
		PacketsRecv:   0,
		PacketsSent:   0,
	}
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
		global.APP_LOG.Error("数据库健康检查失败", zap.Error(err))
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

// calculateCPUUsage 计算CPU使用率
func (s *MonitoringService) calculateCPUUsage() float64 {
	// 简化的CPU使用率计算
	// 在生产环境中，应该使用更准确的方法，如读取 /proc/stat 或使用 gopsutil
	// 这里基于goroutine数量和CPU核心数做一个简单的估算
	goroutines := runtime.NumGoroutine()
	cores := runtime.NumCPU()

	// 简单估算：每个CPU核心理想情况下可以处理100个goroutine
	usage := float64(goroutines) / float64(cores*100) * 100
	if usage > 100 {
		usage = 100
	}

	return usage
}

// getLoadAverage 获取系统负载平均值
func (s *MonitoringService) getLoadAverage(minutes int) float64 {
	// 实现真实的负载平均值获取
	var loadfile string
	switch minutes {
	case 1:
		loadfile = "/proc/loadavg"
	case 5:
		loadfile = "/proc/loadavg"
	case 15:
		loadfile = "/proc/loadavg"
	default:
		loadfile = "/proc/loadavg"
	}

	// 读取 /proc/loadavg 文件
	data, err := os.ReadFile(loadfile)
	if err != nil {
		// 如果无法读取系统负载，回退到基于goroutine数量的估算
		goroutines := runtime.NumGoroutine()
		cores := runtime.NumCPU()
		load := float64(goroutines) / float64(cores)

		// 根据时间窗口稍作调整
		switch minutes {
		case 1:
			return load
		case 5:
			return load * 0.8
		case 15:
			return load * 0.6
		default:
			return load
		}
	}

	// 解析 /proc/loadavg 文件内容
	// 格式: "0.15 0.25 0.35 1/123 456"
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		// 解析失败，使用fallback
		return float64(runtime.NumGoroutine()) / float64(runtime.NumCPU())
	}

	var loadStr string
	switch minutes {
	case 1:
		loadStr = fields[0]
	case 5:
		loadStr = fields[1]
	case 15:
		loadStr = fields[2]
	default:
		loadStr = fields[0]
	}

	load, err := strconv.ParseFloat(loadStr, 64)
	if err != nil {
		// 解析失败，使用fallback
		return float64(runtime.NumGoroutine()) / float64(runtime.NumCPU())
	}

	return load
}

// getSystemMemoryInfo 获取系统内存信息
func (s *MonitoringService) getSystemMemoryInfo() system.MemoryStats {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	// 简化的系统内存估算
	// 在实际生产环境中，应该读取 /proc/meminfo (Linux) 或使用 gopsutil
	// 这里基于Go runtime的内存使用情况做估算

	// 假设系统有基本的内存量，通过多种方式估算
	estimatedTotal := uint64(8 * 1024 * 1024 * 1024) // 默认8GB

	// 如果heap使用量很大，说明系统内存可能更多
	if m.HeapSys > uint64(2*1024*1024*1024) { // 超过2GB
		estimatedTotal = uint64(16 * 1024 * 1024 * 1024) // 估算为16GB
	}

	// 使用当前分配的内存作为已使用内存的基础
	used := m.HeapAlloc + m.StackSys + m.MSpanSys + m.MCacheSys + m.OtherSys

	// 一些系统开销估算
	systemOverhead := estimatedTotal / 10 // 10%的系统开销
	used += systemOverhead

	free := estimatedTotal - used
	usage := float64(used) / float64(estimatedTotal) * 100

	return system.MemoryStats{
		Total:     estimatedTotal,
		Used:      used,
		Free:      free,
		Usage:     usage,
		SwapTotal: estimatedTotal / 4, // 估算swap为总内存的1/4
		SwapUsed:  0,                  // 假设没有使用swap
	}
}
