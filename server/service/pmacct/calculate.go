package pmacct

import (
	"oneclickvirt/global"

	"go.uber.org/zap"
)

// calculatePmacctBufferSizes 根据实例带宽动态计算pmacct缓冲区大小
// 返回: pluginBufferSize, pluginPipeSize, memPoolsNumber, sqlCacheEntries
func (s *Service) calculatePmacctBufferSizes(bandwidthMbps int) (int, int, int, int) {
	// 默认最小值（适用于低带宽场景 <= 10 Mbps）
	if bandwidthMbps <= 0 {
		bandwidthMbps = 10 // 默认10Mbps
	}
	// 计算原则:
	// 1. plugin_buffer_size: 单个数据包的缓冲区，根据带宽调整
	//    - 低带宽(<=100Mbps): 5KB-10KB
	//    - 中带宽(101-500Mbps): 50KB-100KB
	//    - 高带宽(501-1000Mbps): 256KB
	//    - 超高带宽(>1000Mbps): 512KB-1MB
	// 2. plugin_pipe_size: 管道总缓冲区，支持突发流量
	//    - 目标: 能缓冲3-8秒的满载流量
	//    - 低带宽: 2-10MB，中带宽: 25-50MB，高带宽: 128-512MB
	// 3. imt_mem_pools_number: 内存池数量（Memory插件已禁用）
	// 4. sql_cache_entries: SQLite缓存条目数，根据带宽动态调整
	//    - 每分钟刷新一次，低带宽不需要大缓存
	//    - <=50Mbps: 32条目，<=100Mbps: 64条目
	//    - <=200Mbps: 128条目，<=500Mbps: 256条目
	//    - <=1000Mbps: 512条目，<=2000Mbps: 768条目，>2000Mbps: 1024条目

	var pluginBufferSize, pluginPipeSize, memPoolsNumber, sqlCacheEntries int

	switch {
	case bandwidthMbps <= 50:
		// 低带宽 (<=50 Mbps)
		pluginBufferSize = 5120          // 5 KB
		pluginPipeSize = 2 * 1024 * 1024 // 2 MB
		memPoolsNumber = 64
		sqlCacheEntries = 32 // 小缓存，每分钟刷新足够

	case bandwidthMbps <= 100:
		// 中低带宽 (51-100 Mbps)
		pluginBufferSize = 10240          // 10 KB
		pluginPipeSize = 10 * 1024 * 1024 // 10 MB
		memPoolsNumber = 96
		sqlCacheEntries = 64

	case bandwidthMbps <= 200:
		// 中等带宽 (101-200 Mbps)
		pluginBufferSize = 51200          // 50 KB
		pluginPipeSize = 25 * 1024 * 1024 // 25 MB
		memPoolsNumber = 128
		sqlCacheEntries = 128

	case bandwidthMbps <= 500:
		// 中高带宽 (201-500 Mbps)
		pluginBufferSize = 102400         // 100 KB
		pluginPipeSize = 50 * 1024 * 1024 // 50 MB
		memPoolsNumber = 192
		sqlCacheEntries = 256

	case bandwidthMbps <= 1000:
		// 高带宽 (501-1000 Mbps)
		pluginBufferSize = 256 * 1024      // 256 KB
		pluginPipeSize = 128 * 1024 * 1024 // 128 MB
		memPoolsNumber = 256
		sqlCacheEntries = 512

	case bandwidthMbps <= 2000:
		// 超高带宽 (1001-2000 Mbps)
		pluginBufferSize = 512 * 1024      // 512 KB
		pluginPipeSize = 256 * 1024 * 1024 // 256 MB
		memPoolsNumber = 384
		sqlCacheEntries = 768

	default:
		// 极高带宽 (>2 Gbps)
		pluginBufferSize = 1024 * 1024     // 1 MB
		pluginPipeSize = 512 * 1024 * 1024 // 512 MB
		memPoolsNumber = 512
		sqlCacheEntries = 1024
	}

	global.APP_LOG.Info("根据带宽计算pmacct缓冲区大小",
		zap.Int("bandwidthMbps", bandwidthMbps),
		zap.Int("pluginBufferSize", pluginBufferSize),
		zap.Int("pluginPipeSize", pluginPipeSize),
		zap.Int("memPoolsNumber", memPoolsNumber),
		zap.Int("sqlCacheEntries", sqlCacheEntries))

	return pluginBufferSize, pluginPipeSize, memPoolsNumber, sqlCacheEntries
}
