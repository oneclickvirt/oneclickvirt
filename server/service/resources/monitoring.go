package resources

import (
	"bufio"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/system"

	"go.uber.org/zap"
)

type MonitoringService struct{}

var startTime = time.Now()

// CPU usage sampling via /proc/stat
var (
	cpuUsageMu     sync.RWMutex
	cpuUsageValue  float64
	cpuSamplerOnce sync.Once
)

type cpuTimeSample struct {
	Total uint64
	Idle  uint64
}

func startCPUSampler() {
	cpuSamplerOnce.Do(func() {
		go func() {
			prev := readCPUTimes()
			ticker := time.NewTicker(5 * time.Second)
			defer ticker.Stop()
			for range ticker.C {
				curr := readCPUTimes()
				if prev.Total > 0 && curr.Total > prev.Total {
					totalDelta := curr.Total - prev.Total
					idleDelta := curr.Idle - prev.Idle
					usage := float64(totalDelta-idleDelta) / float64(totalDelta) * 100
					if usage < 0 {
						usage = 0
					}
					if usage > 100 {
						usage = 100
					}
					cpuUsageMu.Lock()
					cpuUsageValue = usage
					cpuUsageMu.Unlock()
				}
				prev = curr
			}
		}()
	})
}

func readCPUTimes() cpuTimeSample {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return cpuTimeSample{}
	}
	line := strings.SplitN(string(data), "\n", 2)[0]
	fields := strings.Fields(line)
	if len(fields) < 5 || fields[0] != "cpu" {
		return cpuTimeSample{}
	}
	var total, idle uint64
	for i := 1; i < len(fields); i++ {
		v, _ := strconv.ParseUint(fields[i], 10, 64)
		total += v
		if i == 4 {
			idle = v
		}
	}
	return cpuTimeSample{Total: total, Idle: idle}
}

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

// getDiskUsage 通过 syscall.Statfs 获取真实磁盘使用情况
func (s *MonitoringService) getDiskUsage(path string) (*system.DiskStats, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return nil, err
	}
	bsize := uint64(stat.Bsize)
	total := stat.Blocks * bsize
	free := stat.Bavail * bsize
	used := total - free
	var usage float64
	if total > 0 {
		usage = float64(used) / float64(total) * 100
	}
	return &system.DiskStats{
		Total: total,
		Used:  used,
		Free:  free,
		Usage: usage,
	}, nil
}

// getNetworkStats 通过 /proc/net/dev 获取主机网络统计信息
func (s *MonitoringService) getNetworkStats() system.NetworkStats {
	f, err := os.Open("/proc/net/dev")
	if err != nil {
		return system.NetworkStats{}
	}
	defer f.Close()

	var totalRx, totalTx, totalPktsRx, totalPktsTx uint64
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, ":") {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		iface := strings.TrimSpace(parts[0])
		if iface == "lo" {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 10 {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		rxPkts, _ := strconv.ParseUint(fields[1], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		txPkts, _ := strconv.ParseUint(fields[9], 10, 64)
		totalRx += rx
		totalTx += tx
		totalPktsRx += rxPkts
		totalPktsTx += txPkts
	}
	return system.NetworkStats{
		BytesReceived: totalRx,
		BytesSent:     totalTx,
		PacketsRecv:   totalPktsRx,
		PacketsSent:   totalPktsTx,
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

// calculateCPUUsage 通过 /proc/stat 采样计算真实CPU使用率
func (s *MonitoringService) calculateCPUUsage() float64 {
	startCPUSampler()
	cpuUsageMu.RLock()
	defer cpuUsageMu.RUnlock()
	return cpuUsageValue
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

// getSystemMemoryInfo 通过 /proc/meminfo 获取真实系统内存信息
func (s *MonitoringService) getSystemMemoryInfo() system.MemoryStats {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return s.estimateMemoryFromRuntime()
	}
	defer f.Close()

	info := make(map[string]uint64)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSuffix(parts[0], ":")
		val, _ := strconv.ParseUint(parts[1], 10, 64)
		info[key] = val * 1024 // kB → bytes
	}

	total := info["MemTotal"]
	if total == 0 {
		return s.estimateMemoryFromRuntime()
	}

	available := info["MemAvailable"]
	var used uint64
	if available > 0 {
		used = total - available
	} else {
		used = total - info["MemFree"] - info["Buffers"] - info["Cached"]
	}

	swapTotal := info["SwapTotal"]
	swapUsed := swapTotal - info["SwapFree"]

	return system.MemoryStats{
		Total:     total,
		Used:      used,
		Free:      total - used,
		Usage:     float64(used) / float64(total) * 100,
		SwapTotal: swapTotal,
		SwapUsed:  swapUsed,
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
