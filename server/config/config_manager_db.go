package config

import (
	"encoding/json"
	"fmt"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// saveConfigToDB 保存配置到数据库
func (cm *ConfigManager) saveConfigToDB(key string, value interface{}) error {
	return cm.saveConfigToDBWithTx(cm.db, key, value)
}

// prepareConfigForDB 准备配置数据用于数据库保存（辅助方法）
func (cm *ConfigManager) prepareConfigForDB(key string, value interface{}) (SystemConfig, error) {
	// 将value转换为字符串，处理nil值
	var valueStr string
	if value == nil {
		valueStr = ""
		cm.logger.Debug("准备nil配置值为空字符串", zap.String("key", key))
	} else {
		// 对于非nil值，根据类型进行序列化
		switch v := value.(type) {
		case string:
			valueStr = v
		case int, int8, int16, int32, int64:
			valueStr = fmt.Sprintf("%d", v)
		case uint, uint8, uint16, uint32, uint64:
			valueStr = fmt.Sprintf("%d", v)
		case float32, float64:
			valueStr = fmt.Sprintf("%v", v)
		case bool:
			valueStr = fmt.Sprintf("%t", v)
		case map[string]interface{}, []interface{}, []string, []int, []map[string]interface{}:
			// 对于复杂类型（map、slice等），使用JSON序列化
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				cm.logger.Error("序列化配置值失败", zap.String("key", key), zap.Error(err))
				return SystemConfig{}, fmt.Errorf("failed to marshal value for key %s: %w", key, err)
			}
			valueStr = string(jsonBytes)
		default:
			// 对于其他复杂类型，尝试JSON序列化
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				// 如果JSON序列化失败，记录警告并使用fmt.Sprintf作为降级方案
				cm.logger.Warn("无法JSON序列化配置值，使用字符串表示",
					zap.String("key", key),
					zap.String("type", fmt.Sprintf("%T", v)),
					zap.Error(err))
				valueStr = fmt.Sprintf("%v", v)
			} else {
				valueStr = string(jsonBytes)
			}
		}
	}

	// 判断该配置是否为公开配置
	isPublic := publicConfigKeys[key]

	cm.logger.Debug("准备配置数据",
		zap.String("key", key),
		zap.String("value", valueStr),
		zap.Bool("isPublic", isPublic))

	return SystemConfig{
		Key:      key,
		Value:    valueStr,
		IsPublic: isPublic,
	}, nil
}

// saveConfigToDBWithTx 使用事务保存配置到数据库
func (cm *ConfigManager) saveConfigToDBWithTx(tx *gorm.DB, key string, value interface{}) error {
	// 将value转换为字符串，处理nil值
	var valueStr string
	if value == nil {
		// 对于nil值，保存为空字符串，表示键存在但值为空
		valueStr = ""
		cm.logger.Debug("保存nil配置值为空字符串", zap.String("key", key))
	} else {
		// 对于非nil值，根据类型进行序列化
		switch v := value.(type) {
		case string:
			valueStr = v
		case int, int8, int16, int32, int64:
			valueStr = fmt.Sprintf("%d", v)
		case uint, uint8, uint16, uint32, uint64:
			valueStr = fmt.Sprintf("%d", v)
		case float32, float64:
			valueStr = fmt.Sprintf("%v", v)
		case bool:
			valueStr = fmt.Sprintf("%t", v)
		case map[string]interface{}, []interface{}, []string, []int, []map[string]interface{}:
			// 对于复杂类型（map、slice等），使用JSON序列化
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				cm.logger.Error("序列化配置值失败", zap.String("key", key), zap.Error(err))
				return fmt.Errorf("failed to marshal value for key %s: %w", key, err)
			}
			valueStr = string(jsonBytes)
		default:
			// 对于其他复杂类型，尝试JSON序列化
			jsonBytes, err := json.Marshal(v)
			if err != nil {
				// 如果JSON序列化失败，记录警告并使用fmt.Sprintf作为降级方案
				cm.logger.Warn("无法JSON序列化配置值，使用字符串表示",
					zap.String("key", key),
					zap.String("type", fmt.Sprintf("%T", v)),
					zap.Error(err))
				valueStr = fmt.Sprintf("%v", v)
			} else {
				valueStr = string(jsonBytes)
			}
		}
	}

	// 判断该配置是否为公开配置
	isPublic := publicConfigKeys[key]

	cm.logger.Info("保存配置到数据库",
		zap.String("key", key),
		zap.String("value", valueStr),
		zap.Bool("isPublic", isPublic))

	config := SystemConfig{
		Key:      key,
		Value:    valueStr,
		IsPublic: isPublic,
	}

	// 先尝试查找已存在的配置
	var existingConfig SystemConfig
	err := tx.Where("`key` = ?", key).First(&existingConfig).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			// 记录不存在，创建新记录
			return tx.Create(&config).Error
		}
		return err
	}

	// 记录已存在，更新所有字段（包括 is_public）
	return tx.Model(&existingConfig).Updates(map[string]interface{}{
		"value":     valueStr,
		"is_public": isPublic,
	}).Error
}

// batchSaveConfigsToDBOnly 批量保存配置到数据库（仅数据库，不创建标志文件，不触发回调）
// 用于系统自动补全默认配置，不应标记为用户修改
func (cm *ConfigManager) batchSaveConfigsToDBOnly(flatConfigs map[string]interface{}) error {
	if len(flatConfigs) == 0 {
		return nil
	}

	// 准备批量保存的数据
	var configsToSaveList []SystemConfig
	for key, value := range flatConfigs {
		config, err := cm.prepareConfigForDB(key, value)
		if err != nil {
			return fmt.Errorf("准备配置 %s 失败: %v", key, err)
		}
		configsToSaveList = append(configsToSaveList, config)
	}

	// 使用事务批量保存
	if err := cm.db.Transaction(func(tx *gorm.DB) error {
		if len(configsToSaveList) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "is_public", "updated_at"}),
			}).CreateInBatches(configsToSaveList, 50).Error; err != nil {
				return fmt.Errorf("批量保存配置失败: %v", err)
			}
		}
		return nil
	}); err != nil {
		return err
	}

	// 更新内存缓存
	cm.mu.Lock()
	for key, value := range flatConfigs {
		cm.configCache[key] = value
	}
	cm.mu.Unlock()

	cm.logger.Info("批量保存配置到数据库完成（仅数据库，未创建标志文件）",
		zap.Int("count", len(configsToSaveList)))

	return nil
}
