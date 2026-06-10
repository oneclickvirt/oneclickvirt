package config

import "sort"

// DefaultLevelLimits 返回所有内置等级的默认限制（LevelLimitInfo 格式）。
func DefaultLevelLimits() map[int]LevelLimitInfo {
	result := make(map[int]LevelLimitInfo, 5)
	for level := 1; level <= 5; level++ {
		if info, ok := DefaultLevelLimitInfo(level); ok {
			result[level] = info
		}
	}
	return result
}

// DefaultLevelLimitsConfigMap 返回所有内置等级的默认限制（map[string]interface{} 格式，兼容旧代码）。
func DefaultLevelLimitsConfigMap() map[string]interface{} {
	result := make(map[string]interface{}, 5)
	for level := 1; level <= 5; level++ {
		if info, ok := DefaultLevelLimitInfo(level); ok {
			result[fmtLevelKey(level)] = map[string]interface{}{
				"max-instances": info.MaxInstances,
				"max-resources": info.MaxResources,
				"max-traffic":   info.MaxTraffic,
				"expiry-days":   info.ExpiryDays,
				"max-snapshots": info.MaxSnapshots,
			}
		}
	}
	return result
}

// DefaultLevelLimitInfo 返回指定等级的内置默认限制，未知等级返回 (nil, false)。
func DefaultLevelLimitInfo(level int) (LevelLimitInfo, bool) {
	defaults := [][2]interface{}{
		{1, LevelLimitInfo{
			MaxInstances: 1,
			MaxResources: map[string]interface{}{
				"cpu":       1,
				"memory":    350,
				"disk":      1024,
				"bandwidth": 100,
			},
			MaxTraffic:   102400,
			ExpiryDays:   0,
			MaxSnapshots: 1,
		}},
		{2, LevelLimitInfo{
			MaxInstances: 3,
			MaxResources: map[string]interface{}{
				"cpu":       2,
				"memory":    1024,
				"disk":      20480,
				"bandwidth": 200,
			},
			MaxTraffic:   204800,
			ExpiryDays:   0,
			MaxSnapshots: 3,
		}},
		{3, LevelLimitInfo{
			MaxInstances: 5,
			MaxResources: map[string]interface{}{
				"cpu":       4,
				"memory":    2048,
				"disk":      40960,
				"bandwidth": 500,
			},
			MaxTraffic:   307200,
			ExpiryDays:   0,
			MaxSnapshots: 5,
		}},
		{4, LevelLimitInfo{
			MaxInstances: 10,
			MaxResources: map[string]interface{}{
				"cpu":       8,
				"memory":    4096,
				"disk":      81920,
				"bandwidth": 1000,
			},
			MaxTraffic:   409600,
			ExpiryDays:   0,
			MaxSnapshots: 10,
		}},
		{5, LevelLimitInfo{
			MaxInstances: 20,
			MaxResources: map[string]interface{}{
				"cpu":       16,
				"memory":    8192,
				"disk":      163840,
				"bandwidth": 2000,
			},
			MaxTraffic:   512000,
			ExpiryDays:   0,
			MaxSnapshots: 20,
		}},
	}

	for _, entry := range defaults {
		lvl := entry[0].(int)
		info := entry[1].(LevelLimitInfo)
		if lvl == level {
			return info, true
		}
	}
	return LevelLimitInfo{}, false
}

// NormalizeLevelLimitInfo 填充 LevelLimitInfo 中的零值字段为内置默认值，确保带宽、快照等字段始终有效。
func NormalizeLevelLimitInfo(level int, info LevelLimitInfo) LevelLimitInfo {
	defaultInfo, ok := DefaultLevelLimitInfo(level)
	if !ok {
		return info
	}

	if info.MaxInstances <= 0 {
		info.MaxInstances = defaultInfo.MaxInstances
	}
	if info.MaxTraffic <= 0 {
		info.MaxTraffic = defaultInfo.MaxTraffic
	}
	if info.MaxSnapshots <= 0 {
		info.MaxSnapshots = defaultInfo.MaxSnapshots
	}
	if info.MaxResources == nil {
		info.MaxResources = make(map[string]interface{})
	}
	for _, key := range []string{"cpu", "memory", "disk", "bandwidth"} {
		if v, exists := info.MaxResources[key]; !exists || isZeroValue(v) {
			if defaultVal, ok := defaultInfo.MaxResources[key]; ok {
				info.MaxResources[key] = defaultVal
			}
		}
	}
	return info
}

// NormalizeLevelLimits 批量归一化所有等级限制。
func NormalizeLevelLimits(limits map[int]LevelLimitInfo) map[int]LevelLimitInfo {
	if limits == nil {
		return DefaultLevelLimits()
	}

	// 确保所有内置等级都存在
	for level := 1; level <= 5; level++ {
		if _, exists := limits[level]; !exists {
			if defaultInfo, ok := DefaultLevelLimitInfo(level); ok {
				limits[level] = defaultInfo
			}
		}
	}

	// 归一化每个等级
	levels := make([]int, 0, len(limits))
	for level := range limits {
		levels = append(levels, level)
	}
	sort.Ints(levels)

	for _, level := range levels {
		limits[level] = NormalizeLevelLimitInfo(level, limits[level])
	}

	return limits
}

func fmtLevelKey(level int) string {
	if level < 10 {
		return string(rune('0' + level))
	}
	return string(rune('0'+level/10)) + string(rune('0'+level%10))
}

func isZeroValue(v interface{}) bool {
	switch val := v.(type) {
	case int:
		return val == 0
	case int64:
		return val == 0
	case float64:
		return val == 0
	case float32:
		return val == 0
	default:
		return v == nil
	}
}
