package config

import (
	"os"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// EnsureDefaultConfigs 确保所有必需的配置项都存在，缺失的使用默认值补全
// 这个方法会检查数据库，只对真正缺失的配置项（YAML中也不存在的）使用默认值补全
// 细粒度到每个小配置项，不会覆盖YAML中已存在的配置（即使是空值）
func (cm *ConfigManager) EnsureDefaultConfigs() error {
	cm.logger.Info("开始检查并补全缺失的配置项")

	// 读取YAML配置
	file, err := os.ReadFile("config.yaml")
	if err != nil {
		cm.logger.Warn("读取配置文件失败，将使用默认值补全所有配置", zap.Error(err))
		file = []byte("{}")
	}

	var yamlConfig map[string]interface{}
	if err := yaml.Unmarshal(file, &yamlConfig); err != nil {
		cm.logger.Warn("解析配置文件失败，将使用默认值补全所有配置", zap.Error(err))
		yamlConfig = make(map[string]interface{})
	}

	// 展平YAML配置
	flatYAML := cm.flattenConfig(yamlConfig, "")
	cm.logger.Info("YAML配置项总数", zap.Int("count", len(flatYAML)))

	// 获取默认配置结构
	defaultConfigs := getDefaultConfigMap()

	// 展平默认配置为点分隔的键值对
	flatDefaults := cm.flattenConfig(defaultConfigs, "")
	cm.logger.Info("默认配置项总数", zap.Int("count", len(flatDefaults)))

	// 查询数据库中现有的配置
	var existingConfigs []SystemConfig
	if err := cm.db.Find(&existingConfigs).Error; err != nil {
		cm.logger.Error("查询现有配置失败", zap.Error(err))
		return err
	}

	// 构建现有配置的键集合
	existingKeys := make(map[string]bool)
	for _, cfg := range existingConfigs {
		existingKeys[cfg.Key] = true
	}

	// 查找真正缺失的配置项：既不在YAML中也不在数据库中的
	// 但要跳过系统级配置（它们必须来自YAML，不能被默认值覆盖）
	missingConfigs := make(map[string]interface{})
	for key, value := range flatDefaults {
		// 跳过系统级配置
		if isSystemLevelConfig(key) {
			cm.logger.Debug("跳过系统级配置补全（必须来自YAML）",
				zap.String("key", key))
			continue
		}

		// 只有在YAML和数据库中都不存在时，才使用默认值
		_, inYAML := flatYAML[key]
		_, inDB := existingKeys[key]
		if !inYAML && !inDB {
			missingConfigs[key] = value
			cm.logger.Debug("发现缺失的配置项",
				zap.String("key", key),
				zap.Any("defaultValue", value))
		}
	}

	if len(missingConfigs) == 0 {
		cm.logger.Info("所有配置项都已存在（在YAML或数据库中），无需补全")
		return nil
	}

	cm.logger.Info("发现真正缺失的配置项（YAML和数据库都没有）",
		zap.Int("missingCount", len(missingConfigs)),
		zap.Any("missingKeys", func() []string {
			keys := make([]string, 0, len(missingConfigs))
			for k := range missingConfigs {
				keys = append(keys, k)
			}
			return keys
		}()))

	// 批量插入缺失的配置到数据库（不创建标志文件，因为这是系统自动补全）
	if err := cm.batchSaveConfigsToDBOnly(missingConfigs); err != nil {
		cm.logger.Error("补全缺失配置失败", zap.Error(err))
		return err
	}

	cm.logger.Info("缺失的配置项补全完成", zap.Int("count", len(missingConfigs)))
	return nil
}

// getDefaultConfigMap 获取默认配置的 map 表示
func getDefaultConfigMap() map[string]interface{} {
	return map[string]interface{}{
		"auth": map[string]interface{}{
			"enable-email":               false,
			"enable-telegram":            false,
			"enable-qq":                  false,
			"enable-oauth2":              false,
			"enable-public-registration": false,
			"email-smtp-host":            "",
			"email-smtp-port":            587,
			"email-username":             "",
			"email-password":             "",
			"telegram-bot-token":         "",
			"qq-app-id":                  "",
			"qq-app-key":                 "",
		},
		"quota": map[string]interface{}{
			"default-level": 1,
			"instance-type-permissions": map[string]interface{}{
				"min-level-for-container":        1,
				"min-level-for-vm":               2,
				"min-level-for-delete-container": 2,
				"min-level-for-delete-vm":        2,
				"min-level-for-reset-container":  2,
				"min-level-for-reset-vm":         2,
			},
			"level-limits": map[string]interface{}{
				"1": map[string]interface{}{
					"max-instances": 1,
					"max-resources": map[string]interface{}{
						"cpu":    1,
						"memory": 1024,
						"disk":   10,
					},
					"max-traffic": 0,
				},
				"2": map[string]interface{}{
					"max-instances": 3,
					"max-resources": map[string]interface{}{
						"cpu":    2,
						"memory": 1024,
						"disk":   20,
					},
					"max-traffic": 0,
				},
				"3": map[string]interface{}{
					"max-instances": 5,
					"max-resources": map[string]interface{}{
						"cpu":    4,
						"memory": 2048,
						"disk":   40,
					},
					"max-traffic": 0,
				},
				"4": map[string]interface{}{
					"max-instances": 10,
					"max-resources": map[string]interface{}{
						"cpu":    8,
						"memory": 4096,
						"disk":   80,
					},
					"max-traffic": 0,
				},
				"5": map[string]interface{}{
					"max-instances": 20,
					"max-resources": map[string]interface{}{
						"cpu":    16,
						"memory": 8192,
						"disk":   160,
					},
					"max-traffic": 0,
				},
			},
		},
		"invite-code": map[string]interface{}{
			"enabled":  false,
			"required": false,
		},
		"captcha": map[string]interface{}{
			"enabled":     false,
			"width":       120,
			"height":      40,
			"length":      4,
			"expire-time": 5,
		},
		"cors": map[string]interface{}{
			"mode":      "allow-all",
			"whitelist": []string{"http://localhost:8080", "http://127.0.0.1:8080"},
		},
		"system": map[string]interface{}{
			"env":                        "public",
			"addr":                       8888,
			"db-type":                    "mysql",
			"oss-type":                   "local",
			"use-multipoint":             false,
			"use-redis":                  false,
			"iplimit-count":              100,
			"iplimit-time":               3600,
			"frontend-url":               "",
			"provider-inactive-hours":    72,
			"oauth2-state-token-minutes": 15,
		},
		"jwt": map[string]interface{}{
			"signing-key":  "",
			"expires-time": "7d",
			"buffer-time":  "1d",
			"issuer":       "oneclickvirt",
		},
		"other": map[string]interface{}{
			"default-language": "zh",
			"logo-url":         "",
			"site-name":        "",
		},
	}
}

// unflattenConfig 将扁平化的配置还原为嵌套结构
func unflattenConfig(flat map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range flat {
		keys := splitKey(key)
		if len(keys) == 0 {
			continue
		}

		current := result
		for i := 0; i < len(keys)-1; i++ {
			k := keys[i]
			if next, ok := current[k].(map[string]interface{}); ok {
				current = next
			} else {
				newMap := make(map[string]interface{})
				current[k] = newMap
				current = newMap
			}
		}

		lastKey := keys[len(keys)-1]
		current[lastKey] = value
	}

	return result
}
