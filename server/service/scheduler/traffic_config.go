package scheduler

import (
	"oneclickvirt/global"
	"oneclickvirt/model/provider"
)

// GetProviderTrafficConfig 获取Provider的流量统计配置
func GetProviderTrafficConfig(providerID uint) provider.TrafficStatsPreset {
	var p provider.Provider
	err := global.APP_DB.Select(
		"enable_traffic_control, traffic_stats_mode, traffic_collect_interval, traffic_collect_batch_size, "+
			"traffic_limit_check_interval, traffic_limit_check_batch_size, traffic_auto_reset_interval, "+
			"traffic_auto_reset_batch_size",
	).First(&p, providerID).Error

	if err != nil {
		// Provider不存在或查询失败，使用默认轻量模式
		return provider.GetTrafficStatsPreset(provider.TrafficStatsModeLight)
	}

	// 如果Provider未启用流量控制，返回默认配置（但不会被使用）
	if !p.EnableTrafficControl {
		return provider.GetTrafficStatsPreset(provider.TrafficStatsModeLight)
	}

	return p.GetTrafficStatsConfig()
}
