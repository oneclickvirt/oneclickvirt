package resources

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/model/system"
	"oneclickvirt/utils"

	"go.uber.org/zap"
)

type MonitoringService struct{}

var startTime = time.Now()

const monitoringLogBaseDir = "./storage/logs"

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
	limitN := parseBoundedInt(limit, 100, 1, 5000)
	offsetN := parseBoundedInt(offset, 0, 0, 100000)
	normalizedLevel := normalizeLogLevel(level)
	logs, hasMore := s.readSystemLogs(normalizedLevel, limitN, offsetN)

	return map[string]interface{}{
		"logs":     logs,
		"total":    offsetN + len(logs),
		"has_more": hasMore,
		"level":    normalizedLevel,
		"limit":    limitN,
		"offset":   offsetN,
	}
}

// GetOperationLogs 获取操作审计日志
func (s *MonitoringService) GetOperationLogs(userID, action, startTime, endTime, limit, offset string) map[string]interface{} {
	limitN := parseBoundedInt(limit, 100, 1, 5000)
	offsetN := parseBoundedInt(offset, 0, 0, 100000)
	auditLogs, total, err := s.queryOperationLogs(userID, action, startTime, endTime, limitN, offsetN)
	if err != nil {
		global.APP_LOG.Warn("查询操作审计日志失败", zap.Error(err))
		return map[string]interface{}{
			"logs":       []map[string]interface{}{},
			"total":      0,
			"error":      "查询操作审计日志失败",
			"user_id":    userID,
			"action":     action,
			"start_time": startTime,
			"end_time":   endTime,
			"limit":      limitN,
			"offset":     offsetN,
		}
	}

	return map[string]interface{}{
		"logs":       auditLogs,
		"total":      total,
		"user_id":    userID,
		"action":     action,
		"start_time": startTime,
		"end_time":   endTime,
		"limit":      limitN,
		"offset":     offsetN,
	}
}

type logFileCandidate struct {
	path    string
	source  string
	level   string
	modTime time.Time
}

func (s *MonitoringService) readSystemLogs(level string, limit, offset int) ([]map[string]interface{}, bool) {
	target := limit + offset + 1
	if target <= 0 {
		target = limit + 1
	}

	files := findSystemLogFiles(level)
	entries := make([]map[string]interface{}, 0, minInt(target, 512))
	for _, file := range files {
		if len(entries) >= target {
			break
		}
		lines, err := readTailLines(file.path, target-len(entries))
		if err != nil {
			global.APP_LOG.Debug("读取系统日志文件失败",
				zap.String("path", utils.TruncateString(file.path, 120)),
				zap.Error(err))
			continue
		}
		for i := len(lines) - 1; i >= 0; i-- {
			entry := parseLogLine(lines[i], file.level, file.source)
			if level != "" && level != "all" && strings.ToLower(fmt.Sprint(entry["level"])) != level {
				continue
			}
			entries = append(entries, entry)
			if len(entries) >= target {
				break
			}
		}
	}

	hasMore := len(entries) > limit+offset
	if offset >= len(entries) {
		return []map[string]interface{}{}, hasMore
	}
	end := offset + limit
	if end > len(entries) {
		end = len(entries)
	}
	return entries[offset:end], hasMore
}

func (s *MonitoringService) queryOperationLogs(userID, action, startTimeValue, endTimeValue string, limit, offset int) ([]map[string]interface{}, int64, error) {
	if global.APP_DB == nil {
		return nil, 0, fmt.Errorf("database is not initialized")
	}

	db := global.APP_DB.Model(&adminModel.AuditLog{})
	if parsedUserID, ok := parseUintFilter(userID); ok {
		db = db.Where("user_id = ?", parsedUserID)
	}
	if action = strings.TrimSpace(action); action != "" {
		if len(action) > 128 {
			action = action[:128]
		}
		pattern := "%" + escapeLike(action) + "%"
		db = db.Where("(method LIKE ? ESCAPE '\\' OR path LIKE ? ESCAPE '\\')", pattern, pattern)
	}
	if parsedStart, ok := parseTimeFilter(startTimeValue, false); ok {
		db = db.Where("created_at >= ?", parsedStart)
	}
	if parsedEnd, ok := parseTimeFilter(endTimeValue, true); ok {
		db = db.Where("created_at <= ?", parsedEnd)
	}

	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	var logs []adminModel.AuditLog
	if err := db.Order("created_at DESC").Limit(limit).Offset(offset).Find(&logs).Error; err != nil {
		return nil, 0, err
	}

	result := make([]map[string]interface{}, 0, len(logs))
	for _, log := range logs {
		status := "success"
		if log.StatusCode >= 400 {
			status = "failed"
		}
		result = append(result, map[string]interface{}{
			"id":          log.ID,
			"user_id":     log.UserID,
			"username":    log.Username,
			"action":      strings.TrimSpace(log.Method + " " + log.Path),
			"resource":    log.Path,
			"method":      log.Method,
			"path":        log.Path,
			"status_code": log.StatusCode,
			"latency":     log.Latency,
			"ip_address":  log.ClientIP,
			"user_agent":  log.UserAgent,
			"timestamp":   log.CreatedAt.Format("2006-01-02 15:04:05"),
			"status":      status,
			"details":     log.Response,
			"request":     log.Request,
		})
	}
	return result, total, nil
}

func findSystemLogFiles(level string) []logFileCandidate {
	entries := make([]logFileCandidate, 0)
	err := filepath.WalkDir(monitoringLogBaseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(d.Name(), ".log") {
			return nil
		}
		info, statErr := d.Info()
		if statErr != nil {
			return nil
		}
		fileLevel := strings.ToLower(strings.TrimSuffix(filepath.Base(path), ".log"))
		if level != "" && level != "all" && fileLevel != level {
			return nil
		}
		entries = append(entries, logFileCandidate{
			path:    path,
			source:  strings.TrimPrefix(filepath.Dir(path), monitoringLogBaseDir),
			level:   fileLevel,
			modTime: info.ModTime(),
		})
		return nil
	})
	if err != nil {
		global.APP_LOG.Debug("扫描系统日志目录失败", zap.Error(err))
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].modTime.After(entries[j].modTime)
	})
	return entries
}

func parseLogLine(line, fallbackLevel, source string) map[string]interface{} {
	entry := map[string]interface{}{
		"timestamp": "",
		"level":     fallbackLevel,
		"message":   line,
		"source":    strings.Trim(source, string(os.PathSeparator)),
		"raw":       line,
	}

	var jsonLine map[string]interface{}
	if err := json.Unmarshal([]byte(line), &jsonLine); err == nil {
		for key, value := range jsonLine {
			entry[key] = value
		}
		if ts, ok := jsonLine["ts"]; ok {
			entry["timestamp"] = ts
		}
		if msg, ok := jsonLine["msg"]; ok {
			entry["message"] = msg
		}
		if lvl, ok := jsonLine["level"]; ok {
			entry["level"] = strings.ToLower(fmt.Sprint(lvl))
		}
		return entry
	}

	fields := strings.Fields(line)
	if len(fields) >= 3 {
		entry["timestamp"] = fields[0]
		if strings.Contains(fields[1], ":") || strings.Contains(fields[1], "T") {
			entry["timestamp"] = fields[0] + " " + fields[1]
		}
		for _, field := range fields {
			lvl := normalizeLogLevel(strings.Trim(field, "[]:"))
			if lvl != "" && lvl != "all" {
				entry["level"] = lvl
				break
			}
		}
	}
	return entry
}

func readTailLines(path string, n int) ([]string, error) {
	if n <= 0 {
		return []string{}, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	ring := make([]string, n)
	idx := 0
	count := 0
	scanner := bufio.NewScanner(f)
	buf := make([]byte, 512*1024)
	scanner.Buffer(buf, 512*1024)
	for scanner.Scan() {
		ring[idx%n] = scanner.Text()
		idx++
		count++
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if count <= n {
		return ring[:count], nil
	}
	result := make([]string, n)
	for i := 0; i < n; i++ {
		result[i] = ring[(idx+i)%n]
	}
	return result, nil
}

func normalizeLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "", "all":
		return "all"
	case "debug", "info", "warn", "warning", "error", "panic", "fatal", "dpanic":
		if strings.ToLower(strings.TrimSpace(level)) == "warning" {
			return "warn"
		}
		return strings.ToLower(strings.TrimSpace(level))
	default:
		return "all"
	}
}

func parseBoundedInt(value string, defaultValue, minValue, maxValue int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return defaultValue
	}
	if parsed < minValue {
		return minValue
	}
	if parsed > maxValue {
		return maxValue
	}
	return parsed
}

func parseUintFilter(value string) (uint, bool) {
	if strings.TrimSpace(value) == "" {
		return 0, false
	}
	parsed, err := strconv.ParseUint(strings.TrimSpace(value), 10, 64)
	if err != nil || parsed == 0 {
		return 0, false
	}
	return uint(parsed), true
}

func parseTimeFilter(value string, endOfDay bool) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	layouts := []string{time.RFC3339, "2006-01-02 15:04:05", "2006-01-02"}
	for _, layout := range layouts {
		parsed, err := time.ParseInLocation(layout, value, time.Local)
		if err == nil {
			if endOfDay && layout == "2006-01-02" {
				parsed = parsed.Add(24*time.Hour - time.Nanosecond)
			}
			return parsed, true
		}
	}
	return time.Time{}, false
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	value = strings.ReplaceAll(value, `_`, `\_`)
	return value
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
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
