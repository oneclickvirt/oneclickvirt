package config

import (
	"fmt"

	"go.uber.org/zap"
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
		MaxValue: 5,
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

	// 默认等级配置
	defaultLevelConfigs := map[string]map[string]interface{}{
		"1": {
			"max-instances": 1,
			"max-resources": map[string]interface{}{
				"cpu":       1,
				"memory":    350,
				"disk":      1024,
				"bandwidth": 100,
			},
			"max-traffic": 102400,
		},
		"2": {
			"max-instances": 3,
			"max-resources": map[string]interface{}{
				"cpu":       2,
				"memory":    1024,
				"disk":      20480,
				"bandwidth": 200,
			},
			"max-traffic": 204800,
		},
		"3": {
			"max-instances": 5,
			"max-resources": map[string]interface{}{
				"cpu":       4,
				"memory":    2048,
				"disk":      40960,
				"bandwidth": 500,
			},
			"max-traffic": 307200,
		},
		"4": {
			"max-instances": 10,
			"max-resources": map[string]interface{}{
				"cpu":       8,
				"memory":    4096,
				"disk":      81920,
				"bandwidth": 1000,
			},
			"max-traffic": 409600,
		},
		"5": {
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

	// 验证每个等级的配置
	for levelStr, limitValue := range levelLimitsMap {
		limitMap, ok := limitValue.(map[string]interface{})
		if !ok {
			return fmt.Errorf("等级 %s 的配置必须是对象类型", levelStr)
		}

		// 获取该等级的默认配置
		defaultConfig, hasDefault := defaultLevelConfigs[levelStr]

		// 验证并填充 max-instances
		maxInstances, exists := limitMap["max-instances"]
		if !exists || maxInstances == nil || maxInstances == 0 {
			if hasDefault {
				limitMap["max-instances"] = defaultConfig["max-instances"]
				cm.logger.Info("自动填充默认配置",
					zap.String("level", levelStr),
					zap.String("field", "max-instances"),
					zap.Any("value", defaultConfig["max-instances"]))
			} else {
				return fmt.Errorf("等级 %s 缺少 max-instances 配置且没有默认值", levelStr)
			}
		} else {
			if err := validatePositiveNumber(maxInstances, fmt.Sprintf("等级 %s 的 max-instances", levelStr)); err != nil {
				return err
			}
		}

		// 验证并填充 max-traffic
		maxTraffic, exists := limitMap["max-traffic"]
		if !exists || maxTraffic == nil || maxTraffic == 0 {
			if hasDefault {
				limitMap["max-traffic"] = defaultConfig["max-traffic"]
				cm.logger.Info("自动填充默认配置",
					zap.String("level", levelStr),
					zap.String("field", "max-traffic"),
					zap.Any("value", defaultConfig["max-traffic"]))
			} else {
				return fmt.Errorf("等级 %s 缺少 max-traffic 配置且没有默认值", levelStr)
			}
		} else {
			if err := validatePositiveNumber(maxTraffic, fmt.Sprintf("等级 %s 的 max-traffic", levelStr)); err != nil {
				return err
			}
		}

		// 验证并填充 max-resources
		maxResources, exists := limitMap["max-resources"]
		if !exists || maxResources == nil {
			if hasDefault {
				limitMap["max-resources"] = defaultConfig["max-resources"]
				cm.logger.Info("自动填充默认配置",
					zap.String("level", levelStr),
					zap.String("field", "max-resources"),
					zap.Any("value", defaultConfig["max-resources"]))
			} else {
				return fmt.Errorf("等级 %s 缺少 max-resources 配置且没有默认值", levelStr)
			}
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
					if hasDefault {
						defaultResources := defaultConfig["max-resources"].(map[string]interface{})
						resourcesMap[resource] = defaultResources[resource]
						cm.logger.Info("自动填充默认配置",
							zap.String("level", levelStr),
							zap.String("field", fmt.Sprintf("max-resources.%s", resource)),
							zap.Any("value", defaultResources[resource]))
					} else {
						return fmt.Errorf("等级 %s 的 max-resources 缺少 %s 配置且没有默认值", levelStr, resource)
					}
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
