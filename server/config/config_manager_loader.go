package config

import (
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

	// 核心策略：只要数据库中有配置数据，就以数据库为唯一权威来源。
	// 这保证了通过 API 保存的配置（如 OAuth2 开关）在任何重启场景下都不会被
	// config.yaml 覆盖，无论标志文件是否存在（Docker 卷未挂载、重新部署等场景均安全）。
	if configCount > 0 {
		if configModified {
			cm.logger.Info("场景：已有数据库配置（标志文件存在），以数据库为准")
		} else {
			cm.logger.Info("场景：已有数据库配置（标志文件不存在），仍以数据库为准（保护已保存的配置）")
			// 补全标志文件，确保下次重启也走数据库路径（可选，提高一致性）
			if err := cm.markConfigAsModified(); err != nil {
				cm.logger.Warn("补全标志文件失败（可忽略）", zap.Error(err))
			}
		}
		if err := cm.handleDatabaseFirst(); err != nil {
			cm.logger.Error("处理数据库优先策略失败", zap.Error(err))
		}
		return
	}

	// 场景：数据库无配置（真正的首次启动）
	// 若标志文件意外存在（异常情况），先清除它。
	if configModified {
		cm.logger.Warn("场景：异常 - 标志文件存在但数据库无配置，清除标志文件")
		if err := cm.clearConfigModifiedFlag(); err != nil {
			cm.logger.Warn("清除标志文件失败", zap.Error(err))
		}
	}

	// 全新安装首次启动，以 YAML 为准初始化数据库
	cm.logger.Info("场景：首次启动（YAML优先）")
	if err := cm.handleYAMLFirst(); err != nil {
		cm.logger.Error("处理YAML优先策略失败", zap.Error(err))
	}
	// handleYAMLFirst 内部已调用 EnsureDefaultConfigs 并在补全后再次同步到全局配置
}

// handleDatabaseFirst 处理数据库优先的策略
// 用于升级场景或API修改后重启：DB记录的值不被覆盖，但YAML中新增的配置项会以
// INSERT IGNORE 方式补充到DB，确保新版本默认值生效。
func (cm *ConfigManager) handleDatabaseFirst() error {
	cm.logger.Info("执行策略：DB优先 + YAML新键补充 → global")

	// 1. 尝试从数据库恢复到YAML文件（容忍失败，如 config.yaml 挂载为只读时）
	if err := cm.RestoreConfigFromDatabase(); err != nil {
		cm.logger.Warn("从数据库恢复配置到YAML文件失败（将跳过YAML写入，继续同步内存配置）", zap.Error(err))
	} else {
		cm.logger.Info("配置已从数据库恢复到YAML文件")
	}

	// 2. 将YAML中新增的配置项（尚未在DB中）以INSERT IGNORE方式补充到DB
	// 场景：升级后新版本config.yaml中的新默认值 / 用户手动在config.yaml添加的新配置项
	// 已存在的DB值保持不变，不会触发配置回退
	if err := cm.mergeYAMLDefaultsIntoDatabase(); err != nil {
		cm.logger.Warn("合并YAML默认配置到数据库失败（可忽略）", zap.Error(err))
	}

	// 3. 重新从数据库加载，确保configCache包含原有DB值和新合并的YAML值
	var configs []SystemConfig
	if err := cm.db.Where("`key` LIKE ?", "%.%").Find(&configs).Error; err != nil {
		cm.logger.Error("重新加载配置失败", zap.Error(err))
		return err
	}
	cm.mu.Lock()
	for _, config := range configs {
		cm.configCache[config.Key] = parseConfigValue(config.Value)
	}
	cm.mu.Unlock()
	cm.logger.Info("配置缓存已重新加载", zap.Int("configCount", len(configs)))

	// 4. 同步到全局配置（触发回调）
	if err := cm.syncDatabaseConfigToGlobal(); err != nil {
		cm.logger.Error("同步数据库配置到全局配置失败", zap.Error(err))
		return err
	}
	cm.logger.Info("数据库配置已成功同步到全局配置")

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
