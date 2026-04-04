//go:build !linux

package resources

import (
	"fmt"

	"oneclickvirt/model/system"
)

func (s *MonitoringService) calculateCPUUsage() float64 {
	return 0
}

func (s *MonitoringService) getLoadAverage(minutes int) float64 {
	return 0
}

func (s *MonitoringService) getSystemMemoryInfo() system.MemoryStats {
	return s.estimateMemoryFromRuntime()
}

func (s *MonitoringService) getDiskUsage(path string) (*system.DiskStats, error) {
	return nil, fmt.Errorf("disk stats not supported on this platform")
}

func (s *MonitoringService) getNetworkStats() system.NetworkStats {
	return system.NetworkStats{}
}
