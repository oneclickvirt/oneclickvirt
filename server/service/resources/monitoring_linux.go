//go:build linux

package resources

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"oneclickvirt/model/system"
)

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

// calculateCPUUsage 通过 /proc/stat 采样计算真实CPU使用率
func (s *MonitoringService) calculateCPUUsage() float64 {
	startCPUSampler()
	cpuUsageMu.RLock()
	defer cpuUsageMu.RUnlock()
	return cpuUsageValue
}

// getLoadAverage 获取系统负载平均值
func (s *MonitoringService) getLoadAverage(minutes int) float64 {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(data))
	if len(fields) < 3 {
		return 0
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
		return 0
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
