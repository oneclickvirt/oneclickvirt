package config

import (
	"fmt"
	"strconv"
	"strings"
)

// initValidationRules 初始化验证规则
func (cm *ConfigManager) initValidationRules() {
	// 认证配置验证规则
	cm.validationRules["auth.enable-email"] = ConfigValidationRule{
		Required: true,
		Type:     "bool",
	}
	cm.validationRules["auth.enable-oauth2"] = ConfigValidationRule{
		Required: false,
		Type:     "bool",
	}
	cm.validationRules["auth.email-smtp-port"] = ConfigValidationRule{
		Required: false,
		Type:     "int",
		MinValue: 1,
		MaxValue: 65535,
	}
	cm.validationRules["quota.default-level"] = ConfigValidationRule{
		Required: true,
		Type:     "int",
		MinValue: 1,
		MaxValue: 99,
	}

	// 等级限制配置验证规则
	cm.validationRules["quota.level-limits"] = ConfigValidationRule{
		Required: false,
		Type:     "object",
		Validator: func(value interface{}) error {
			return cm.validateLevelLimits(value)
		},
	}

	// 更多验证规则...
}

// validateConfig 验证配置
func (cm *ConfigManager) validateConfig(key string, value interface{}) error {
	rule, exists := cm.validationRules[key]
	if !exists {
		// 没有验证规则，直接通过
		return nil
	}

	if rule.Required && value == nil {
		return fmt.Errorf("配置项 %s 是必需的", key)
	}

	if rule.Validator != nil {
		return rule.Validator(value)
	}

	// 基础类型验证
	switch rule.Type {
	case "int":
		var intVal int
		// JSON 解析后数字可能是 int、float64 或 int64
		switch v := value.(type) {
		case int:
			intVal = v
		case float64:
			intVal = int(v)
		case int64:
			intVal = int(v)
		default:
			return fmt.Errorf("配置项 %s 类型错误，期望 int", key)
		}

		if rule.MinValue != nil && intVal < rule.MinValue.(int) {
			return fmt.Errorf("配置项 %s 的值 %d 小于最小值 %d", key, intVal, rule.MinValue)
		}
		if rule.MaxValue != nil && intVal > rule.MaxValue.(int) {
			return fmt.Errorf("配置项 %s 的值 %d 大于最大值 %d", key, intVal, rule.MaxValue)
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("配置项 %s 类型错误，期望 bool", key)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("配置项 %s 类型错误，期望 string", key)
		}
	}

	return nil
}

// validateLevelLimits 验证等级限制配置，并自动填充缺失的默认值
func (cm *ConfigManager) validateLevelLimits(value interface{}) error {
	levelLimitsMap, ok := value.(map[string]interface{})
	if !ok {
		return fmt.Errorf("levelLimits 必须是对象类型")
	}

	// 验证每个等级的配置
	for levelStr, limitValue := range levelLimitsMap {
		limitMap, ok := limitValue.(map[string]interface{})
		if !ok {
			return fmt.Errorf("等级 %s 的配置必须是对象类型", levelStr)
		}
		levelNum, err := strconv.Atoi(levelStr)
		if err != nil || levelNum < 1 || levelNum > 99 {
			return fmt.Errorf("等级 %s 必须是1-99之间的整数", levelStr)
		}
		defaultConfig, hasDefaultConfig := defaultLevelLimitConfig(levelNum)

		// 验证并填充 max-instances
		maxInstances, exists := limitMap["max-instances"]
		if !exists || maxInstances == nil || maxInstances == 0 {
			if !hasDefaultConfig {
				return fmt.Errorf("等级 %s 的 max-instances 不能为空", levelStr)
			}
			limitMap["max-instances"] = defaultConfig["max-instances"]
		} else {
			if err := validatePositiveNumber(maxInstances, fmt.Sprintf("等级 %s 的 max-instances", levelStr)); err != nil {
				return err
			}
		}

		// 验证并填充 max-traffic
		maxTraffic, exists := limitMap["max-traffic"]
		if !exists || maxTraffic == nil || maxTraffic == 0 {
			if !hasDefaultConfig {
				return fmt.Errorf("等级 %s 的 max-traffic 不能为空", levelStr)
			}
			limitMap["max-traffic"] = defaultConfig["max-traffic"]
		} else {
			if err := validatePositiveNumber(maxTraffic, fmt.Sprintf("等级 %s 的 max-traffic", levelStr)); err != nil {
				return err
			}
		}

		// 验证并填充 max-resources
		maxResources, exists := limitMap["max-resources"]
		if !exists || maxResources == nil {
			if !hasDefaultConfig {
				return fmt.Errorf("等级 %s 的 max-resources 不能为空", levelStr)
			}
			limitMap["max-resources"] = defaultConfig["max-resources"]
		} else {
			resourcesMap, ok := maxResources.(map[string]interface{})
			if !ok {
				return fmt.Errorf("等级 %s 的 max-resources 必须是对象类型", levelStr)
			}

			// 验证并填充必需的资源字段
			requiredResources := []string{"cpu", "memory", "disk", "bandwidth"}
			for _, resource := range requiredResources {
				resourceValue, exists := resourcesMap[resource]
				if !exists || resourceValue == nil || resourceValue == 0 {
					if !hasDefaultConfig {
						return fmt.Errorf("等级 %s 的 %s 不能为空", levelStr, resource)
					}
					defaultResources := defaultConfig["max-resources"].(map[string]interface{})
					resourcesMap[resource] = defaultResources[resource]
				} else {
					if err := validatePositiveNumber(resourceValue, fmt.Sprintf("等级 %s 的 %s", levelStr, resource)); err != nil {
						return err
					}
				}
			}
		}
	}

	return nil
}

func (cm *ConfigManager) validateQuotaDefaultLevelReference(flatConfig map[string]interface{}) error {
	_, defaultLevelUpdated := flatConfig["quota.default-level"]
	_, levelLimitsUpdated := flatConfig["quota.level-limits"]
	if !defaultLevelUpdated && !levelLimitsUpdated {
		return nil
	}

	defaultLevelValue, exists := flatConfig["quota.default-level"]
	if !exists {
		defaultLevelValue = cm.configCache["quota.default-level"]
	}
	defaultLevel, ok := toInt(defaultLevelValue)
	if !ok {
		return fmt.Errorf("quota.default-level 类型错误，期望 int")
	}

	levelLimitsValue, exists := flatConfig["quota.level-limits"]
	if !exists {
		levelLimitsValue = cm.configCache["quota.level-limits"]
	}
	levelLimits, ok := levelLimitsValue.(map[string]interface{})
	if !ok {
		return fmt.Errorf("quota.level-limits 必须是对象类型")
	}
	if _, exists := levelLimits[strconv.Itoa(defaultLevel)]; !exists {
		return fmt.Errorf("默认用户等级 %d 未配置资源限制", defaultLevel)
	}
	return nil
}

func (cm *ConfigManager) validateQuotaLevelsInUse(flatConfig map[string]interface{}) error {
	levelLimitsValue, exists := flatConfig["quota.level-limits"]
	if !exists || cm.db == nil {
		return nil
	}

	levelLimits, ok := levelLimitsValue.(map[string]interface{})
	if !ok {
		return fmt.Errorf("quota.level-limits 必须是对象类型")
	}
	configuredLevels := make([]int, 0, len(levelLimits))
	for levelStr := range levelLimits {
		level, err := strconv.Atoi(levelStr)
		if err != nil {
			return fmt.Errorf("等级 %s 必须是整数", levelStr)
		}
		configuredLevels = append(configuredLevels, level)
	}
	if len(configuredLevels) == 0 {
		return fmt.Errorf("至少需要配置一个用户等级")
	}

	type orphanLevel struct {
		Level int
		Count int64
	}
	var orphanLevels []orphanLevel
	if err := cm.db.Raw(
		"SELECT level, COUNT(*) AS count FROM users WHERE deleted_at IS NULL AND level NOT IN ? GROUP BY level ORDER BY level",
		configuredLevels,
	).Scan(&orphanLevels).Error; err != nil {
		return fmt.Errorf("检查用户等级占用失败: %w", err)
	}
	if len(orphanLevels) == 0 {
		return nil
	}

	parts := make([]string, 0, len(orphanLevels))
	for _, item := range orphanLevels {
		parts = append(parts, fmt.Sprintf("%d(%d人)", item.Level, item.Count))
	}
	return fmt.Errorf("以下用户等级仍被用户使用，不能删除: %s", strings.Join(parts, ", "))
}

func defaultLevelLimitConfig(level int) (map[string]interface{}, bool) {
	defaults := map[int]map[string]interface{}{
		1: {
			"max-instances": 1,
			"max-resources": map[string]interface{}{
				"cpu":       1,
				"memory":    350,
				"disk":      1024,
				"bandwidth": 100,
			},
			"max-traffic": 102400,
		},
		2: {
			"max-instances": 3,
			"max-resources": map[string]interface{}{
				"cpu":       2,
				"memory":    1024,
				"disk":      20480,
				"bandwidth": 200,
			},
			"max-traffic": 204800,
		},
		3: {
			"max-instances": 5,
			"max-resources": map[string]interface{}{
				"cpu":       4,
				"memory":    2048,
				"disk":      40960,
				"bandwidth": 500,
			},
			"max-traffic": 307200,
		},
		4: {
			"max-instances": 10,
			"max-resources": map[string]interface{}{
				"cpu":       8,
				"memory":    4096,
				"disk":      81920,
				"bandwidth": 1000,
			},
			"max-traffic": 409600,
		},
		5: {
			"max-instances": 20,
			"max-resources": map[string]interface{}{
				"cpu":       16,
				"memory":    8192,
				"disk":      163840,
				"bandwidth": 2000,
			},
			"max-traffic": 512000,
		},
	}
	defaultConfig, ok := defaults[level]
	return defaultConfig, ok
}

func toInt(value interface{}) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int8:
		return int(v), true
	case int16:
		return int(v), true
	case int32:
		return int(v), true
	case int64:
		return int(v), true
	case uint:
		return int(v), true
	case uint8:
		return int(v), true
	case uint16:
		return int(v), true
	case uint32:
		return int(v), true
	case uint64:
		return int(v), true
	case float64:
		if v != float64(int(v)) {
			return 0, false
		}
		return int(v), true
	case float32:
		if v != float32(int(v)) {
			return 0, false
		}
		return int(v), true
	default:
		return 0, false
	}
}

// validatePositiveNumber 验证数值必须为正数
func validatePositiveNumber(value interface{}, fieldName string) error {
	switch v := value.(type) {
	case int:
		if v <= 0 {
			return fmt.Errorf("%s 不能为空或小于等于0", fieldName)
		}
	case int64:
		if v <= 0 {
			return fmt.Errorf("%s 不能为空或小于等于0", fieldName)
		}
	case float64:
		if v <= 0 {
			return fmt.Errorf("%s 不能为空或小于等于0", fieldName)
		}
	case float32:
		if v <= 0 {
			return fmt.Errorf("%s 不能为空或小于等于0", fieldName)
		}
	default:
		return fmt.Errorf("%s 必须是数值类型", fieldName)
	}
	return nil
}
