package config

import (
	"fmt"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
)

// syncToGlobalConfig 同步配置到全局配置并写回YAML文件
func (cm *ConfigManager) syncToGlobalConfig(config map[string]interface{}) error {
	// 这个方法需要导入 global 包，但为了避免循环导入，需要通过依赖注入或回调的方式实现
	// 暂时先记录日志，具体实现需要在初始化时注册同步回调
	cm.logger.Info("配置已更新，需要同步到全局配置", zap.Any("config", config))

	// 写回YAML文件
	if err := cm.writeConfigToYAML(config); err != nil {
		cm.logger.Error("写回YAML文件失败", zap.Error(err))
		return err
	}

	return nil
}

// setNodeValue 设置节点的值
func setNodeValue(node *yaml.Node, value interface{}) error {
	// 处理nil值 - 写入空值（null）
	if value == nil {
		node.Kind = yaml.ScalarNode
		node.Tag = "!!null"
		node.Value = ""
		return nil
	}

	switch v := value.(type) {
	case string:
		node.Kind = yaml.ScalarNode
		node.Tag = "!!str"
		node.Value = v
	case int:
		node.Kind = yaml.ScalarNode
		node.Tag = "!!int"
		node.Value = fmt.Sprintf("%d", v)
	case int64:
		node.Kind = yaml.ScalarNode
		node.Tag = "!!int"
		node.Value = fmt.Sprintf("%d", v)
	case float64:
		node.Kind = yaml.ScalarNode
		// 如果是整数，转换为int显示
		if v == float64(int64(v)) {
			node.Tag = "!!int"
			node.Value = fmt.Sprintf("%d", int64(v))
		} else {
			node.Tag = "!!float"
			node.Value = fmt.Sprintf("%g", v)
		}
	case bool:
		node.Kind = yaml.ScalarNode
		node.Tag = "!!bool"
		if v {
			node.Value = "true"
		} else {
			node.Value = "false"
		}
	case map[string]interface{}:
		// 对于复杂类型（如level-limits），序列化为YAML子结构
		subYAML, err := yaml.Marshal(v)
		if err != nil {
			return err
		}
		var subNode yaml.Node
		if err := yaml.Unmarshal(subYAML, &subNode); err != nil {
			return err
		}
		// 复制子节点的内容
		if subNode.Kind == yaml.DocumentNode && len(subNode.Content) > 0 {
			*node = *subNode.Content[0]
		}
	default:
		// 其他类型尝试序列化
		subYAML, err := yaml.Marshal(v)
		if err != nil {
			return fmt.Errorf("unsupported value type: %T", v)
		}
		var subNode yaml.Node
		if err := yaml.Unmarshal(subYAML, &subNode); err != nil {
			return err
		}
		if subNode.Kind == yaml.DocumentNode && len(subNode.Content) > 0 {
			*node = *subNode.Content[0]
		}
	}
	return nil
}

// syncDatabaseConfigToGlobal 将数据库中的配置同步到全局配置
// 系统级配置（system, mysql, redis, zap）已经在启动时从YAML加载到global，
// 这里只同步业务配置（auth, quota, invite-code等）到global
func (cm *ConfigManager) syncDatabaseConfigToGlobal() error {
	// 构建嵌套配置结构
	nestedConfig := make(map[string]interface{})

	// 将扁平配置转换为嵌套结构（过滤系统级配置）
	cm.logger.Info("开始构建嵌套配置",
		zap.Int("flatConfigCount", len(cm.configCache)))

	skippedSystemCount := 0
	for key, value := range cm.configCache {
		// 跳过系统级配置（它们已经在启动时从YAML加载）
		if isSystemLevelConfig(key) {
			skippedSystemCount++
			cm.logger.Debug("跳过系统级配置同步（已从YAML加载）",
				zap.String("key", key))
			continue
		}

		cm.logger.Debug("处理配置项",
			zap.String("key", key),
			zap.Any("value", value))
		setNestedValue(nestedConfig, key, value)
	}

	cm.logger.Info("嵌套配置构建完成",
		zap.Int("nestedConfigCount", len(nestedConfig)),
		zap.Int("skippedSystemCount", skippedSystemCount),
		zap.Any("topLevelKeys", func() []string {
			keys := make([]string, 0, len(nestedConfig))
			for k := range nestedConfig {
				keys = append(keys, k)
			}
			return keys
		}()))

	// 遍历配置并同步到全局配置
	// 这里需要导入 global 包，但为了避免循环导入
	// 通过回调机制来实现同步
	for key, value := range nestedConfig {
		cm.logger.Info("触发配置同步回调",
			zap.String("key", key),
			zap.String("valueType", fmt.Sprintf("%T", value)))

		for _, callback := range cm.changeCallbacks {
			if err := callback(key, nil, value); err != nil {
				cm.logger.Error("同步配置到全局变量失败",
					zap.String("key", key),
					zap.Error(err))
			}
		}
	}

	return nil
}

// ReloadFromYAML 从 YAML 文件重新加载配置
// 用于手动修改 config.yaml 后重新加载配置
// 执行流程：YAML → 数据库 → 回调 → global.APP_CONFIG
func (cm *ConfigManager) ReloadFromYAML() error {
	cm.logger.Info("开始从YAML文件重新加载配置")

	// 1. 清除配置修改标志（因为现在 YAML 是最新的基准）
	if err := cm.clearConfigModifiedFlag(); err != nil {
		cm.logger.Warn("清除配置修改标志失败", zap.Error(err))
	}

	// 2. 将 YAML 同步到数据库
	if err := cm.syncYAMLConfigToDatabase(); err != nil {
		cm.logger.Error("同步YAML到数据库失败", zap.Error(err))
		return fmt.Errorf("同步YAML到数据库失败: %v", err)
	}
	cm.logger.Info("YAML配置已同步到数据库")

	// 3. 从数据库重新加载到缓存
	var configs []SystemConfig
	if err := cm.db.Find(&configs).Error; err != nil {
		cm.logger.Error("从数据库重新加载配置失败", zap.Error(err))
		return fmt.Errorf("从数据库重新加载配置失败: %v", err)
	}

	cm.mu.Lock()
	cm.configCache = make(map[string]interface{})
	for _, config := range configs {
		parsedValue := parseConfigValue(config.Value)
		cm.configCache[config.Key] = parsedValue
	}
	cm.mu.Unlock()
	cm.logger.Info("配置已重新加载到缓存", zap.Int("configCount", len(configs)))

	// 4. 通过回调同步到 global.APP_CONFIG
	if err := cm.syncDatabaseConfigToGlobal(); err != nil {
		cm.logger.Error("同步配置到全局配置失败", zap.Error(err))
		return fmt.Errorf("同步配置到全局配置失败: %v", err)
	}
	cm.logger.Info("配置已同步到全局配置")

	// 不创建配置修改标志文件
	// 理由：这是从YAML热加载，不是通过API修改
	// 下次启动时应该依然以YAML为准，而不是数据库

	cm.logger.Info("从YAML文件重新加载配置完成")
	return nil
}
