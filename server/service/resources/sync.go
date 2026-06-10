package resources

import (
	"oneclickvirt/config"
	"oneclickvirt/global"
	"sort"
	"strconv"

	"go.uber.org/zap"
)

// QuotaSyncService 配额同步服务
type QuotaSyncService struct{}

// 等级限制变更记录
type LevelLimitChange struct {
	Level     int
	OldLimits *config.LevelLimitInfo
	NewLimits *config.LevelLimitInfo
}

// DetectAndSyncLevelChanges 检测等级配置变更并自动同步用户限制
func (s *QuotaSyncService) DetectAndSyncLevelChanges(oldConfig, newConfig map[string]interface{}) error {
	// 提取旧的等级限制配置
	oldLevelLimits := s.extractLevelLimits(oldConfig)

	// 提取新的等级限制配置
	newLevelLimits := s.extractLevelLimits(newConfig)

	// 检测变更
	changes := s.detectLevelLimitChanges(oldLevelLimits, newLevelLimits)

	if len(changes) == 0 {
		global.APP_LOG.Debug("等级配置无变更，跳过用户限制同步")
		return nil
	}

	global.APP_LOG.Info("检测到等级配置变更",
		zap.Int("changedLevels", len(changes)))

	// 同步变更的等级用户限制
	for _, change := range changes {
		if err := s.syncLevelUsers(change.Level, change.NewLimits); err != nil {
			global.APP_LOG.Warn("同步等级用户限制失败",
				zap.Int("level", change.Level),
				zap.Error(err))
			continue // 继续处理其他等级，不中断整个过程
		}
	}

	return nil
}

// extractLevelLimits 从配置中提取等级限制
func (s *QuotaSyncService) extractLevelLimits(configMap map[string]interface{}) map[int]config.LevelLimitInfo {
	levelLimits := make(map[int]config.LevelLimitInfo)

	// 查找 quota.levelLimits
	var quotaData interface{}

	// 先查找完整路径
	if quota, ok := configMap["quota"]; ok {
		quotaData = quota
	} else if quotaLevelLimits, ok := configMap["quota.levelLimits"]; ok {
		quotaData = map[string]interface{}{"levelLimits": quotaLevelLimits}
	}

	if quotaData == nil {
		return levelLimits
	}

	quotaMap, ok := quotaData.(map[string]interface{})
	if !ok {
		return levelLimits
	}

	levelLimitsData, exists := quotaMap["levelLimits"]
	if !exists {
		return levelLimits
	}

	levelLimitsMap, ok := levelLimitsData.(map[string]interface{})
	if !ok {
		return levelLimits
	}

	// 解析每个等级的限制
	for levelStr, limitData := range levelLimitsMap {
		if limitMap, ok := limitData.(map[string]interface{}); ok {
			level := s.parseLevelFromString(levelStr)
			if level == 0 {
				continue
			}

			levelLimit := config.LevelLimitInfo{}

			// 解析 MaxInstances
			if maxInstances, exists := firstExisting(limitMap, "max-instances", "maxInstances"); exists {
				if instances, ok := maxInstances.(float64); ok {
					levelLimit.MaxInstances = int(instances)
				} else if instances, ok := maxInstances.(int); ok {
					levelLimit.MaxInstances = instances
				}
			}

			// 解析 MaxTraffic
			if maxTraffic, exists := firstExisting(limitMap, "max-traffic", "maxTraffic"); exists {
				if traffic, ok := maxTraffic.(float64); ok {
					levelLimit.MaxTraffic = int64(traffic)
				} else if traffic, ok := maxTraffic.(int64); ok {
					levelLimit.MaxTraffic = traffic
				} else if traffic, ok := maxTraffic.(int); ok {
					levelLimit.MaxTraffic = int64(traffic)
				}
			}

			// 解析 MaxResources
			if maxResources, exists := firstExisting(limitMap, "max-resources", "maxResources"); exists {
				if resourcesMap, ok := maxResources.(map[string]interface{}); ok {
					levelLimit.MaxResources = resourcesMap
				}
			}

			if maxSnapshots, exists := firstExisting(limitMap, "max-snapshots", "maxSnapshots"); exists {
				if snapshots, ok := maxSnapshots.(float64); ok {
					levelLimit.MaxSnapshots = int(snapshots)
				} else if snapshots, ok := maxSnapshots.(int); ok {
					levelLimit.MaxSnapshots = snapshots
				}
			} else if defaultLimit, ok := config.DefaultLevelLimitInfo(level); ok {
				levelLimit.MaxSnapshots = defaultLimit.MaxSnapshots
			}

			levelLimits[level] = config.NormalizeLevelLimitInfo(level, levelLimit)
		}
	}

	return levelLimits
}

// parseLevelFromString 从字符串解析等级数字
func (s *QuotaSyncService) parseLevelFromString(levelStr string) int {
	level, err := strconv.Atoi(levelStr)
	if err != nil || level < 1 || level > 99 {
		return 0
	}
	return level
}

// detectLevelLimitChanges 检测等级限制变更
func (s *QuotaSyncService) detectLevelLimitChanges(oldLimits, newLimits map[int]config.LevelLimitInfo) []LevelLimitChange {
	var changes []LevelLimitChange

	levels := make([]int, 0, len(newLimits))
	for level := range newLimits {
		levels = append(levels, level)
	}
	sort.Ints(levels)

	for _, level := range levels {
		oldLimit, oldExists := oldLimits[level]
		newLimit, newExists := newLimits[level]

		// 如果新配置不存在该等级，跳过
		if !newExists {
			continue
		}

		// 如果旧配置不存在该等级，或者配置有变更
		if !oldExists || !s.isLevelLimitEqual(oldLimit, newLimit) {
			var oldLimitPtr *config.LevelLimitInfo
			if oldExists {
				oldLimitPtr = &oldLimit
			}

			changes = append(changes, LevelLimitChange{
				Level:     level,
				OldLimits: oldLimitPtr,
				NewLimits: &newLimit,
			})

			global.APP_LOG.Debug("检测到等级配置变更",
				zap.Int("level", level),
				zap.Bool("wasConfigured", oldExists),
				zap.Any("oldLimits", oldLimitPtr),
				zap.Any("newLimits", newLimit))
		}
	}

	return changes
}

// isLevelLimitEqual 比较两个等级限制是否相等
func (s *QuotaSyncService) isLevelLimitEqual(old, new config.LevelLimitInfo) bool {
	// 比较基本字段
	if old.MaxInstances != new.MaxInstances ||
		old.MaxTraffic != new.MaxTraffic {
		return false
	}

	// 比较 MaxResources
	return s.isMaxResourcesEqual(old.MaxResources, new.MaxResources)
}

// isMaxResourcesEqual 比较资源限制是否相等
func (s *QuotaSyncService) isMaxResourcesEqual(old, new map[string]interface{}) bool {
	if len(old) != len(new) {
		return false
	}

	for key, oldValue := range old {
		newValue, exists := new[key]
		if !exists {
			return false
		}

		// 将数值统一转换为 float64 进行比较
		oldFloat := s.convertToFloat64(oldValue)
		newFloat := s.convertToFloat64(newValue)

		if oldFloat != newFloat {
			return false
		}
	}

	return true
}

// convertToFloat64 将数值转换为 float64
func (s *QuotaSyncService) convertToFloat64(value interface{}) float64 {
	switch v := value.(type) {
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case float32:
		return float64(v)
	case float64:
		return v
	default:
		return 0
	}
}

// syncLevelUsers 同步指定等级的所有用户限制
func (s *QuotaSyncService) syncLevelUsers(level int, levelConfig *config.LevelLimitInfo) error {
	if levelConfig == nil {
		return nil
	}

	// 查询该等级的所有用户ID
	var userIDs []uint
	if err := global.APP_DB.Table("users").
		Select("id").
		Where("level = ? AND deleted_at IS NULL", level).
		Pluck("id", &userIDs).Error; err != nil {
		return err
	}

	if len(userIDs) == 0 {
		global.APP_LOG.Debug("该等级没有用户需要同步", zap.Int("level", level))
		return nil
	}

	// 构建更新数据 - 不再自动设置 total_traffic
	updateData := map[string]interface{}{
		"max_instances": levelConfig.MaxInstances,
	}

	// 从 MaxResources 中提取各项资源限制
	if levelConfig.MaxResources != nil {
		if cpu, ok := levelConfig.MaxResources["cpu"].(int); ok {
			updateData["max_cpu"] = cpu
		} else if cpu, ok := levelConfig.MaxResources["cpu"].(float64); ok {
			updateData["max_cpu"] = int(cpu)
		}

		if memory, ok := levelConfig.MaxResources["memory"].(int); ok {
			updateData["max_memory"] = memory
		} else if memory, ok := levelConfig.MaxResources["memory"].(float64); ok {
			updateData["max_memory"] = int(memory)
		}

		if disk, ok := levelConfig.MaxResources["disk"].(int); ok {
			updateData["max_disk"] = disk
		} else if disk, ok := levelConfig.MaxResources["disk"].(float64); ok {
			updateData["max_disk"] = int(disk)
		}

		if bandwidth, ok := levelConfig.MaxResources["bandwidth"].(int); ok {
			updateData["max_bandwidth"] = bandwidth
		} else if bandwidth, ok := levelConfig.MaxResources["bandwidth"].(float64); ok {
			updateData["max_bandwidth"] = int(bandwidth)
		}
	}

	// 批量更新用户限制
	if err := global.APP_DB.Table("users").
		Where("id IN ?", userIDs).
		Updates(updateData).Error; err != nil {
		return err
	}

	global.APP_LOG.Info("自动同步等级用户资源限制成功",
		zap.Int("level", level),
		zap.Int("userCount", len(userIDs)),
		zap.Any("updateData", updateData))

	return nil
}

// SyncAllUsersToCurrentConfig 将所有用户的资源限制同步到当前配置
func (s *QuotaSyncService) SyncAllUsersToCurrentConfig() error {
	global.APP_LOG.Info("开始同步所有用户到当前等级配置")

	levels := make([]int, 0, len(global.GetAppConfig().Quota.LevelLimits))
	for level := range global.GetAppConfig().Quota.LevelLimits {
		levels = append(levels, level)
	}
	sort.Ints(levels)
	for _, level := range levels {
		levelConfig := global.GetAppConfig().Quota.LevelLimits[level]
		levelConfig = config.NormalizeLevelLimitInfo(level, levelConfig)
		if err := s.syncLevelUsers(level, &levelConfig); err != nil {
			global.APP_LOG.Warn("同步等级用户失败",
				zap.Int("level", level),
				zap.Error(err))
			continue
		}
	}

	global.APP_LOG.Info("所有用户资源限制同步完成")
	return nil
}

// SyncUserToLevel 同步单个或多个用户到指定等级的资源限制
func (s *QuotaSyncService) SyncUserToLevel(level int, userIDs []uint) error {
	if len(userIDs) == 0 {
		return nil
	}

	// 获取等级配置
	levelConfig, exists := global.GetAppConfig().Quota.LevelLimits[level]
	if !exists {
		global.APP_LOG.Warn("等级配置不存在，使用默认配置", zap.Int("level", level))
		// 使用内置默认配置
		if defaultConfig, ok := config.DefaultLevelLimitInfo(level); ok {
			levelConfig = defaultConfig
		} else if defaultConfig, ok := config.DefaultLevelLimitInfo(global.GetAppConfig().Quota.DefaultLevel); ok {
			levelConfig = defaultConfig
		}
	}

	levelConfig = config.NormalizeLevelLimitInfo(level, levelConfig)
	return s.syncLevelUsers(level, &levelConfig)
}


func firstExisting(values map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		if value, exists := values[key]; exists {
			return value, true
		}
	}
	return nil, false
}