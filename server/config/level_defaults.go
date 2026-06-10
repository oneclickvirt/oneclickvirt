package config

import "strconv"

// DefaultLevelLimitInfo returns the built-in quota defaults for a user level.
// Resource units: memory/disk are MB, bandwidth is Mbps, traffic is MB.
func DefaultLevelLimitInfo(level int) (LevelLimitInfo, bool) {
	defaults := map[int]LevelLimitInfo{
		1: {
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
		},
		2: {
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
		},
		3: {
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
		},
		4: {
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
		},
		5: {
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
		},
	}

	info, ok := defaults[level]
	if !ok {
		return LevelLimitInfo{}, false
	}
	return CloneLevelLimitInfo(info), true
}

// DefaultLevelLimits returns all built-in quota level defaults.
func DefaultLevelLimits() map[int]LevelLimitInfo {
	limits := make(map[int]LevelLimitInfo, 5)
	for level := 1; level <= 5; level++ {
		if info, ok := DefaultLevelLimitInfo(level); ok {
			limits[level] = info
		}
	}
	return limits
}

// DefaultLevelLimitsConfigMap returns defaults in the shape used by configCache/YAML.
func DefaultLevelLimitsConfigMap() map[string]interface{} {
	limits := make(map[string]interface{}, 5)
	for level := 1; level <= 5; level++ {
		if info, ok := DefaultLevelLimitInfo(level); ok {
			limits[itoa(level)] = map[string]interface{}{
				"max-instances": info.MaxInstances,
				"max-resources": CloneLevelLimitInfo(info).MaxResources,
				"max-traffic":   info.MaxTraffic,
				"expiry-days":   info.ExpiryDays,
				"max-snapshots": info.MaxSnapshots,
			}
		}
	}
	return limits
}

func itoa(value int) string {
	return strconv.Itoa(value)
}

// CloneLevelLimitInfo deep-copies a LevelLimitInfo, including MaxResources.
func CloneLevelLimitInfo(info LevelLimitInfo) LevelLimitInfo {
	clone := info
	if info.MaxResources != nil {
		clone.MaxResources = make(map[string]interface{}, len(info.MaxResources))
		for key, value := range info.MaxResources {
			clone.MaxResources[key] = value
		}
	}
	return clone
}

// NormalizeLevelLimitInfo fills missing/invalid legacy quota fields with defaults.
// It intentionally does not override zero ExpiryDays or MaxSnapshots because both
// can be meaningful administrator choices.
func NormalizeLevelLimitInfo(level int, info LevelLimitInfo) LevelLimitInfo {
	defaultInfo, hasDefault := DefaultLevelLimitInfo(level)
	if !hasDefault {
		if info.MaxResources == nil {
			info.MaxResources = map[string]interface{}{}
		}
		return info
	}

	normalized := CloneLevelLimitInfo(info)
	if normalized.MaxInstances <= 0 {
		normalized.MaxInstances = defaultInfo.MaxInstances
	}
	if normalized.MaxTraffic <= 0 {
		normalized.MaxTraffic = defaultInfo.MaxTraffic
	}
	if normalized.MaxResources == nil {
		normalized.MaxResources = map[string]interface{}{}
	}

	for _, key := range []string{"cpu", "memory", "disk", "bandwidth"} {
		if !positiveResourceValue(normalized.MaxResources[key]) {
			normalized.MaxResources[key] = defaultInfo.MaxResources[key]
		}
	}

	return normalized
}

// NormalizeLevelLimits returns a new map with each level normalized.
func NormalizeLevelLimits(limits map[int]LevelLimitInfo) map[int]LevelLimitInfo {
	if limits == nil {
		return DefaultLevelLimits()
	}
	result := make(map[int]LevelLimitInfo, len(limits))
	for level, info := range limits {
		result[level] = NormalizeLevelLimitInfo(level, info)
	}
	return result
}

func positiveResourceValue(value interface{}) bool {
	num, ok := toInt(value)
	return ok && num > 0
}
