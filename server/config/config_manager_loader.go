package config

import (
	"os"
	"time"

	"go.uber.org/zap"
)

// flattenConfig 将嵌套配置展开为扁平的 key-value 对
// 例如: {"quota": {"levelLimits": {...}}} => {"quota.levelLimits": {...}}
func (cm *ConfigManager) flattenConfig(config map[string]interface{}, prefix string) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range config {
		fullKey := key
		if prefix != "" {
			fullKey = prefix + "." + key
		}

		// 如果值是 map，递归展开
		if valueMap, ok := value.(map[string]interface{}); ok {
			// 检查是否是需要特殊处理的嵌套结构
			// 只有 level-limits 作为整体保存（因为它的结构比较复杂，包含多层嵌套）
			shouldKeepAsWhole := (key == "level-limits" || key == "levelLimits")

			if shouldKeepAsWhole {
				// 对于 level-limits，作为整体保存
				result[fullKey] = value
			} else {
				// 其他嵌套结构正常递归展开（包括 instance-type-permissions）
				nested := cm.flattenConfig(valueMap, fullKey)
				for nestedKey, nestedValue := range nested {
					result[nestedKey] = nestedValue
				}
			}
		} else {
			result[fullKey] = value
		}
	}

	return result
}

// loadConfigFromDB 从数据库加载配置
func (cm *ConfigManager) loadConfigFromDB() {
	if cm.db == nil {
		cm.logger.Error("数据库连接为空，无法加载配置")
		return
	}

	// 测试数据库连接
	sqlDB, err := cm.db.DB()
	if err != nil {
		cm.logger.Error("获取数据库连接失败，无法加载配置", zap.Error(err))
		return
	}

	if err := sqlDB.Ping(); err != nil {
		cm.logger.Error("数据库连接测试失败，无法加载配置", zap.Error(err))
		return
	}

	// 检查是否存在数据库配置数据（仅统计新格式配置项，即 key 含"."的记录）
	var configCount int64
	if err := cm.db.Model(&SystemConfig{}).Where("`key` LIKE ?", "%.%").Count(&configCount).Error; err != nil {
		cm.logger.Warn("查询数据库配置数量失败，可能是首次启动", zap.Error(err))
		configCount = 0
	}

	// 检查配置修改标志
	configModified := cm.isConfigModified()

	// 边界条件判断策略
	cm.logger.Info("配置加载策略分析",
		zap.Bool("configModified", configModified),
		zap.Int64("dbConfigCount", configCount))

	// 场景1：数据库有配置 + 标志文件存在 = 升级场景或API修改后重启
	// 策略：以数据库为准，恢复到YAML并同步到global
	if configCount > 0 && configModified {
		cm.logger.Info("场景：已修改配置的重启或升级（数据库优先）")
		if err := cm.handleDatabaseFirst(); err != nil {
			cm.logger.Error("处理数据库优先策略失败", zap.Error(err))
		}
		return
	}

	// 场景2：数据库有配置 + 标志文件不存在 = 可能是升级/重启/手动修改YAML
	// 策略：检查YAML修改时间，如果最近被修改，优先使用YAML；否则使用数据库保护用户配置
	if configCount > 0 && !configModified {
		cm.logger.Info("场景：数据库有配置但无标志文件（检查YAML是否最近修改）")

		// 检查YAML文件修改时间
		yamlInfo, err := os.Stat("config.yaml")
		if err == nil {
			yamlModTime := yamlInfo.ModTime()

			// 获取数据库中最新配置的更新时间
			var latestConfig SystemConfig
			if err := cm.db.Order("updated_at DESC").First(&latestConfig).Error; err == nil {
				dbModTime := latestConfig.UpdatedAt

				cm.logger.Info("YAML和数据库修改时间对比",
					zap.Time("yamlModTime", yamlModTime),
					zap.Time("dbModTime", dbModTime))

				// 如果YAML文件在数据库之后修改（说明用户手动修改了YAML）
				if yamlModTime.After(dbModTime) {
					cm.logger.Info("判断：YAML文件最近被修改，优先使用YAML配置")
					if err := cm.handleYAMLFirst(); err != nil {
						cm.logger.Error("处理YAML优先策略失败", zap.Error(err))
					}
					// handleYAMLFirst 内部已调用 EnsureDefaultConfigs 并在补全后再次同步全局配置
					return
				}
			}
		}

		// YAML没有更新，使用数据库配置（保护用户配置）
		cm.logger.Info("判断：数据库配置更新，优先使用数据库保护用户配置")
		// 重新创建标志文件
		if err := cm.markConfigAsModified(); err != nil {
			cm.logger.Warn("重新创建标志文件失败", zap.Error(err))
		}
		if err := cm.handleDatabaseFirst(); err != nil {
			cm.logger.Error("处理数据库优先策略失败", zap.Error(err))
		}
		return
	}

	// 场景3：数据库无配置 + 标志文件存在 = 异常情况，清除标志文件
	if configCount == 0 && configModified {
		cm.logger.Warn("场景：异常 - 标志文件存在但数据库无配置，清除标志文件")
		if err := cm.clearConfigModifiedFlag(); err != nil {
			cm.logger.Warn("清除标志文件失败", zap.Error(err))
		}
		// 继续按首次启动处理
	}

	// 场景4：数据库无配置 + 标志文件不存在 = 全新安装首次启动
	cm.logger.Info("场景：首次启动（YAML优先）")
	if err := cm.handleYAMLFirst(); err != nil {
		cm.logger.Error("处理YAML优先策略失败", zap.Error(err))
	}
	// handleYAMLFirst 内部已调用 EnsureDefaultConfigs 并在补全后再次同步到全局配置
}

// handleDatabaseFirst 处理数据库优先的策略
// 用于升级场景或API修改后重启，完全以数据库为准，不补全默认配置（尊重用户选择）
func (cm *ConfigManager) handleDatabaseFirst() error {
	cm.logger.Info("执行策略：数据库 → YAML → global（保留用户配置，不补全默认值）")

	// 1. 从数据库恢复到YAML文件
	if err := cm.RestoreConfigFromDatabase(); err != nil {
		cm.logger.Error("从数据库恢复配置失败", zap.Error(err))
		return err
	}
	cm.logger.Info("配置已从数据库恢复到YAML文件")

	// 2. 同步到全局配置（触发回调）
	if err := cm.syncDatabaseConfigToGlobal(); err != nil {
		cm.logger.Error("同步数据库配置到全局配置失败", zap.Error(err))
		return err
	}
	cm.logger.Info("数据库配置已成功同步到全局配置")

	// 不调用 EnsureDefaultConfigs()
	// 理由：用户可能在API中删除了某些配置项（如禁用某功能），应该尊重用户选择
	// 如果需要补全，应该在YAML优先场景（首次启动）时进行

	return nil
}

// shouldPreferDatabaseConfig 智能判断是否应该优先使用数据库配置
// 用于处理升级场景：数据库有配置但标志文件丢失的情况
func (cm *ConfigManager) shouldPreferDatabaseConfig() bool {
	// 策略1：检查数据库中是否有非默认配置（说明用户修改过）
	var configs []SystemConfig
	if err := cm.db.Find(&configs).Error; err != nil {
		cm.logger.Warn("查询数据库配置失败，默认使用YAML", zap.Error(err))
		return false
	}

	if len(configs) == 0 {
		return false
	}

	// 策略2：只要数据库中有任何配置数据，就认为系统已经初始化过
	// 应该优先使用数据库配置，避免用户配置丢失
	var count int64
	cm.db.Model(&SystemConfig{}).Count(&count)
	if count > 0 {
		cm.logger.Info("数据库system_configs表存在且有数据，优先使用数据库",
			zap.Int64("count", count))
		return true
	}

	// 策略3：检查数据库配置的更新时间（作为补充验证）
	// 如果最近有更新，说明是用户修改过的配置
	var latestConfig SystemConfig
	if err := cm.db.Order("updated_at DESC").First(&latestConfig).Error; err == nil {
		// 只要有配置记录，就认为应该使用数据库（移除24小时限制）
		cm.logger.Info("数据库配置存在，优先使用数据库",
			zap.Time("lastUpdate", latestConfig.UpdatedAt),
			zap.Duration("timeSince", time.Since(latestConfig.UpdatedAt)))
		return true
	}

	// 默认情况：使用YAML配置
	cm.logger.Info("判断为首次启动，使用YAML配置")
	return false
}
