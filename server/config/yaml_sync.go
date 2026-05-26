package config

import (
	"fmt"
	"os"
	"strings"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)
func (cm *ConfigManager) RestoreConfigFromDatabase() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.logger.Info("开始从数据库恢复配置到YAML文件")

	// 从数据库读取所有新格式配置（key 含"."，排除旧下划线格式遗留数据）
	var configs []SystemConfig
	if err := cm.db.Where("`key` LIKE ?", "%.%").Find(&configs).Error; err != nil {
		cm.logger.Error("从数据库读取配置失败", zap.Error(err))
		return fmt.Errorf("从数据库读取配置失败: %v", err)
	}

	if len(configs) == 0 {
		cm.logger.Warn("数据库中没有配置数据，跳过恢复")
		return nil
	}

	cm.logger.Info("从数据库读取到配置", zap.Int("count", len(configs)))

	// 过滤掉系统级配置（不能从数据库恢复，必须保持YAML中的值）
	var nonSystemConfigs []SystemConfig
	skippedSystemCount := 0
	for _, config := range configs {
		if isSystemLevelConfig(config.Key) {
			skippedSystemCount++
			cm.logger.Debug("跳过恢复系统级配置（必须来自YAML）",
				zap.String("key", config.Key))
			continue
		}
		nonSystemConfigs = append(nonSystemConfigs, config)
	}

	cm.logger.Info("过滤配置",
		zap.Int("totalCount", len(configs)),
		zap.Int("restoreCount", len(nonSystemConfigs)),
		zap.Int("skippedSystemCount", skippedSystemCount))

	if len(nonSystemConfigs) == 0 {
		cm.logger.Info("没有需要恢复的配置（所有配置都是系统级配置）")
		return nil
	}

	// 读取现有YAML文件
	file, err := os.ReadFile("config.yaml")
	if err != nil {
		cm.logger.Error("读取配置文件失败", zap.Error(err))
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	// 使用Node API解析，保持原有格式
	var node yaml.Node
	if err := yaml.Unmarshal(file, &node); err != nil {
		cm.logger.Error("解析YAML失败", zap.Error(err))
		return fmt.Errorf("解析YAML失败: %v", err)
	}

	// 使用Node API更新每个配置值（只更新非系统级配置）
	restoredCount := 0
	for _, config := range nonSystemConfigs {
		// 尝试反序列化JSON值
		value := parseConfigValue(config.Value)

		if err := updateYAMLNode(&node, config.Key, value); err != nil {
			// 只在debug级别记录配置键不存在的警告，避免日志噪音
			cm.logger.Debug("更新配置失败",
				zap.String("key", config.Key),
				zap.Error(err))
		} else {
			restoredCount++
		}
	}

	cm.logger.Info("配置恢复统计",
		zap.Int("attemptedCount", len(nonSystemConfigs)),
		zap.Int("restoredCount", restoredCount))

	// 序列化Node，保持原有key格式
	out, err := yaml.Marshal(&node)
	if err != nil {
		cm.logger.Error("序列化配置失败", zap.Error(err))
		return fmt.Errorf("序列化配置失败: %v", err)
	}

	// 写回文件
	if err := os.WriteFile("config.yaml", out, 0644); err != nil {
		cm.logger.Error("写入配置文件失败", zap.Error(err))
		return fmt.Errorf("写入配置文件失败: %v", err)
	}

	// 更新内存缓存 - 使用解析后的值，确保类型正确（只更新非系统级配置）
	for _, config := range nonSystemConfigs {
		parsedValue := parseConfigValue(config.Value)
		cm.configCache[config.Key] = parsedValue
		cm.logger.Debug("更新配置缓存",
			zap.String("key", config.Key),
			zap.String("rawValue", config.Value),
			zap.Any("parsedValue", parsedValue),
			zap.String("parsedType", fmt.Sprintf("%T", parsedValue)))
	}

	cm.logger.Info("配置已成功从数据库恢复到YAML文件")
	return nil
}

// syncYAMLConfigToDatabase 将YAML配置同步到数据库
// 优先使用YAML配置，包括空值也会被同步
func (cm *ConfigManager) syncYAMLConfigToDatabase() error {
	cm.logger.Info("开始将YAML配置同步到数据库")

	// 读取YAML文件
	file, err := os.ReadFile("config.yaml")
	if err != nil {
		return fmt.Errorf("读取配置文件失败: %v", err)
	}

	var yamlConfig map[string]interface{}
	if err := yaml.Unmarshal(file, &yamlConfig); err != nil {
		return fmt.Errorf("解析配置文件失败: %v", err)
	}

	// 使用 flattenConfig 将嵌套配置展开为扁平的 key-value 对
	// 这样可以统一处理所有配置项，包括空值
	allConfigs := cm.flattenConfig(yamlConfig, "")

	// 过滤掉系统级配置（必须100%来自YAML，不能被数据库覆盖）
	// 同时过滤掉非点分格式的 key（如 active_version、signing_key_v*），
	// 这些是外部系统（如JWT服务）写入的配置，不属于 ConfigManager 管理范围。
	configsToSync := make(map[string]interface{})
	skippedSystemConfigs := 0
	for key, value := range allConfigs {
		if isSystemLevelConfig(key) {
			skippedSystemConfigs++
			cm.logger.Debug("跳过系统级配置（必须来自YAML）",
				zap.String("key", key))
			continue
		}
		// 点分格式的 key 才属于 ConfigManager 管理的配置（如 system.addr）
		if !strings.Contains(key, ".") {
			skippedSystemConfigs++
			cm.logger.Debug("跳过非点分格式配置（不属于 ConfigManager 管理范围）",
				zap.String("key", key))
			continue
		}
		configsToSync[key] = value
	}

	cm.logger.Info("从YAML提取的配置项",
		zap.Int("totalCount", len(allConfigs)),
		zap.Int("syncCount", len(configsToSync)),
		zap.Int("skippedSystemCount", skippedSystemConfigs))

	// 准备批量保存的数据（事务外）
	var configsToSaveList []SystemConfig
	for key, value := range configsToSync {
		config, err := cm.prepareConfigForDB(key, value)
		if err != nil {
			return fmt.Errorf("准备配置 %s 失败: %v", key, err)
		}
		configsToSaveList = append(configsToSaveList, config)
	}

	// 使用短事务批量保存
	if err := cm.db.Transaction(func(tx *gorm.DB) error {
		if len(configsToSaveList) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "category"}, {Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "is_public", "updated_at"}),
			}).CreateInBatches(configsToSaveList, 50).Error; err != nil {
				return fmt.Errorf("批量保存配置失败: %v", err)
			}
		}
		return nil
	}); err != nil {
		return fmt.Errorf("批量保存配置到数据库失败: %v", err)
	}

	savedCount := len(configsToSaveList)

	cm.logger.Info("YAML配置已成功同步到数据库", zap.Int("count", savedCount))
	return nil
}

// mergeYAMLDefaultsIntoDatabase 将YAML中存在但数据库中没有的配置项插入数据库（INSERT IGNORE）
// 用于升级场景：保留DB中已有的用户配置，同时为新版本新增的配置项补充默认值
func (cm *ConfigManager) mergeYAMLDefaultsIntoDatabase() error {
	cm.logger.Info("开始将YAML新配置项合并到数据库（INSERT IGNORE）")

	// 读取YAML文件（失败不致命，直接跳过）
	file, err := os.ReadFile("config.yaml")
	if err != nil {
		cm.logger.Warn("读取配置文件失败，跳过YAML合并", zap.Error(err))
		return nil
	}

	var yamlConfig map[string]interface{}
	if err := yaml.Unmarshal(file, &yamlConfig); err != nil {
		cm.logger.Warn("解析配置文件失败，跳过YAML合并", zap.Error(err))
		return nil
	}

	allConfigs := cm.flattenConfig(yamlConfig, "")

	// 过滤掉系统级配置和非点分格式的key（同 syncYAMLConfigToDatabase）
	var configsToInsert []SystemConfig
	for key, value := range allConfigs {
		if isSystemLevelConfig(key) || !strings.Contains(key, ".") {
			continue
		}
		config, err := cm.prepareConfigForDB(key, value)
		if err != nil {
			cm.logger.Debug("准备配置失败，跳过", zap.String("key", key), zap.Error(err))
			continue
		}
		configsToInsert = append(configsToInsert, config)
	}

	if len(configsToInsert) == 0 {
		cm.logger.Info("YAML中没有可合并的配置项")
		return nil
	}

	// INSERT IGNORE：已存在的DB记录保持不变，只插入新增项
	if err := cm.db.Clauses(clause.OnConflict{DoNothing: true}).
		CreateInBatches(configsToInsert, 50).Error; err != nil {
		cm.logger.Warn("YAML配置合并到数据库失败（可忽略）", zap.Error(err))
		return nil
	}

	cm.logger.Info("YAML新配置项已合并到数据库（INSERT IGNORE）", zap.Int("attempted", len(configsToInsert)))
	return nil
}
