package userquota

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"oneclickvirt/config"
	"oneclickvirt/global"
	userModel "oneclickvirt/model/user"
)

const (
	resourceCPU       = "cpu"
	resourceMemory    = "memory"
	resourceDisk      = "disk"
	resourceBandwidth = "bandwidth"
)

// HighestConfiguredLevel returns the highest user level currently configured.
func HighestConfiguredLevel() int {
	maxLevel := 1
	for level := range global.GetAppConfig().Quota.LevelLimits {
		if level > maxLevel {
			maxLevel = level
		}
	}
	return maxLevel
}

// ResolveLevelLimit returns a normalized quota definition for the requested level.
func ResolveLevelLimit(level int) (config.LevelLimitInfo, error) {
	if level < 1 || level > 99 {
		return config.LevelLimitInfo{}, fmt.Errorf("用户等级必须在1-99之间")
	}
	levelLimit, exists := global.GetAppConfig().Quota.LevelLimits[level]
	if !exists {
		if defaultLimit, ok := config.DefaultLevelLimitInfo(level); ok {
			levelLimit = defaultLimit
		} else {
			return config.LevelLimitInfo{}, fmt.Errorf("用户等级 %d 未配置资源限制", level)
		}
	}
	return config.NormalizeLevelLimitInfo(level, levelLimit), nil
}

// ResourceInt converts a resource value from config/JSON/YAML into int.
func ResourceInt(resources map[string]interface{}, key string) int {
	if resources == nil {
		return 0
	}
	value, exists := resources[key]
	if !exists {
		return 0
	}
	return AnyInt(value)
}

// AnyInt converts common numeric representations into int.
func AnyInt(value interface{}) int {
	switch v := value.(type) {
	case int:
		return v
	case int8:
		return int(v)
	case int16:
		return int(v)
	case int32:
		return int(v)
	case int64:
		return int(v)
	case uint:
		return int(v)
	case uint8:
		return int(v)
	case uint16:
		return int(v)
	case uint32:
		return int(v)
	case uint64:
		return int(v)
	case float32:
		return int(v)
	case float64:
		return int(v)
	case json.Number:
		if i, err := v.Int64(); err == nil {
			return int(i)
		}
		if f, err := v.Float64(); err == nil {
			return int(f)
		}
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0
		}
		if i, err := strconv.ParseInt(trimmed, 10, 64); err == nil {
			return int(i)
		}
		if f, err := strconv.ParseFloat(trimmed, 64); err == nil {
			return int(f)
		}
	}
	return 0
}

// BuildLimitUpdateMap builds the DB update payload for a user's current level.
func BuildLimitUpdateMap(level int) (map[string]interface{}, error) {
	levelLimit, err := ResolveLevelLimit(level)
	if err != nil {
		return nil, err
	}
	return BuildLimitUpdateMapFromLimit(level, levelLimit), nil
}

// BuildLevelAndLimitUpdateMap builds one atomic update payload containing both level and derived quota fields.
func BuildLevelAndLimitUpdateMap(level int) (map[string]interface{}, error) {
	updateData, err := BuildLimitUpdateMap(level)
	if err != nil {
		return nil, err
	}
	updateData["level"] = level
	return updateData, nil
}

// BuildLimitUpdateMapFromLimit builds the DB update payload from a specific limit definition.
func BuildLimitUpdateMapFromLimit(level int, levelLimit config.LevelLimitInfo) map[string]interface{} {
	levelLimit = config.NormalizeLevelLimitInfo(level, levelLimit)
	return map[string]interface{}{
		"total_traffic": levelLimit.MaxTraffic,
		"max_instances": levelLimit.MaxInstances,
		"max_cpu":       ResourceInt(levelLimit.MaxResources, resourceCPU),
		"max_memory":    ResourceInt(levelLimit.MaxResources, resourceMemory),
		"max_disk":      ResourceInt(levelLimit.MaxResources, resourceDisk),
		"max_bandwidth": ResourceInt(levelLimit.MaxResources, resourceBandwidth),
	}
}

// ApplyLimitFields applies normalized level quota fields to a user struct before creation/update.
func ApplyLimitFields(usr *userModel.User, level int) error {
	levelLimit, err := ResolveLevelLimit(level)
	if err != nil {
		return err
	}
	usr.MaxInstances = levelLimit.MaxInstances
	usr.MaxCPU = ResourceInt(levelLimit.MaxResources, resourceCPU)
	usr.MaxMemory = ResourceInt(levelLimit.MaxResources, resourceMemory)
	usr.MaxDisk = ResourceInt(levelLimit.MaxResources, resourceDisk)
	usr.MaxBandwidth = ResourceInt(levelLimit.MaxResources, resourceBandwidth)
	usr.TotalTraffic = levelLimit.MaxTraffic
	return nil
}

// ApplyLevelAndLimitFields applies both the level and all derived quota fields to a user struct.
func ApplyLevelAndLimitFields(usr *userModel.User, level int) error {
	usr.Level = level
	return ApplyLimitFields(usr, level)
}
