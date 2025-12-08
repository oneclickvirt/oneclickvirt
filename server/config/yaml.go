package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// handleYAMLFirst 处理YAML优先的策略
func (cm *ConfigManager) handleYAMLFirst() error {
	cm.logger.Info("执行策略：YAML → 数据库 → global")

	// 1. 同步YAML配置到数据库
	if err := cm.syncYAMLConfigToDatabase(); err != nil {
		cm.logger.Error("同步YAML配置到数据库失败", zap.Error(err))
		return err
	}
	cm.logger.Info("YAML配置已同步到数据库")

	// 2. 重新从数据库加载以确保缓存一致
	var configs []SystemConfig
	if err := cm.db.Find(&configs).Error; err != nil {
		cm.logger.Error("重新加载配置失败", zap.Error(err))
		return err
	}

	// 3. 加锁更新内存缓存
	cm.mu.Lock()
	for _, config := range configs {
		parsedValue := parseConfigValue(config.Value)
		cm.configCache[config.Key] = parsedValue
		// 调试输出
		if config.Key == "auth.enable-oauth2" {
			cm.logger.Info("加载OAuth2配置到缓存",
				zap.String("key", config.Key),
				zap.String("rawValue", config.Value),
				zap.Any("parsedValue", parsedValue),
				zap.String("parsedType", fmt.Sprintf("%T", parsedValue)))
		}
	}
	cm.mu.Unlock()
	cm.logger.Info("配置已加载到缓存", zap.Int("configCount", len(configs)))

	// 4. 同步到全局配置（触发回调）
	if err := cm.syncDatabaseConfigToGlobal(); err != nil {
		cm.logger.Error("同步配置到全局配置失败", zap.Error(err))
		return err
	}
	cm.logger.Info("配置已同步到全局配置")

	// 5. 检查并补全缺失的配置项
	if err := cm.EnsureDefaultConfigs(); err != nil {
		cm.logger.Warn("补全缺失配置项失败", zap.Error(err))
	}

	return nil
}

// writeConfigToYAML 将配置写回到YAML文件（保留原始key格式）
func (cm *ConfigManager) writeConfigToYAML(updates map[string]interface{}) error {
	// 读取现有配置文件
	file, err := os.ReadFile("config.yaml")
	if err != nil {
		cm.logger.Error("读取配置文件失败", zap.Error(err))
		return err
	}

	// 使用yaml.v3的Node API来精确控制更新，保持原有格式
	var node yaml.Node
	if err := yaml.Unmarshal(file, &node); err != nil {
		cm.logger.Error("解析YAML失败", zap.Error(err))
		return err
	}

	// 将驼峰格式的updates转换为连接符格式
	kebabUpdates := convertMapKeysToKebab(updates)
	cm.logger.Info("转换配置格式为连接符",
		zap.Int("originalCount", len(updates)),
		zap.Int("convertedCount", len(kebabUpdates)))

	// 使用Node API更新值,保持原有key格式不变
	for key, value := range kebabUpdates {
		if err := updateYAMLNode(&node, key, value); err != nil {
			// 只在debug级别记录配置键不存在的警告，避免日志噪音
			cm.logger.Debug("更新YAML节点失败", zap.String("key", key), zap.Error(err))
		}
	} // 序列化Node，这样可以保持原有的key格式
	out, err := yaml.Marshal(&node)
	if err != nil {
		cm.logger.Error("序列化YAML失败", zap.Error(err))
		return err
	}

	// 写回文件
	if err := os.WriteFile("config.yaml", out, 0644); err != nil {
		cm.logger.Error("写入配置文件失败", zap.Error(err))
		return err
	}

	cm.logger.Info("配置已成功写回YAML文件")
	return nil
}

// updateYAMLNode 使用Node API更新YAML节点的值，保持key格式不变
func updateYAMLNode(node *yaml.Node, path string, value interface{}) error {
	// 分割路径
	keys := splitKey(path)

	// 找到Document节点
	if node.Kind != yaml.DocumentNode || len(node.Content) == 0 {
		return fmt.Errorf("invalid document node")
	}

	// 从根映射开始
	current := node.Content[0]

	// 遍历路径找到目标节点
	for i := 0; i < len(keys); i++ {
		key := keys[i]

		if current.Kind != yaml.MappingNode {
			return fmt.Errorf("expected mapping node at key: %s", key)
		}

		// 在映射中查找key
		found := false
		for j := 0; j < len(current.Content); j += 2 {
			keyNode := current.Content[j]
			valueNode := current.Content[j+1]

			if keyNode.Value == key {
				found = true

				if i == len(keys)-1 {
					// 到达目标节点，更新值
					if err := setNodeValue(valueNode, value); err != nil {
						return err
					}
					return nil
				} else {
					// 继续向下遍历
					current = valueNode
				}
				break
			}
		}

		if !found {
			// key不存在，需要创建
			if i == len(keys)-1 {
				// 这是最后一个key，创建键值对
				newKeyNode := &yaml.Node{
					Kind:  yaml.ScalarNode,
					Value: key,
				}
				newValueNode := &yaml.Node{}
				if err := setNodeValue(newValueNode, value); err != nil {
					return err
				}
				// 添加到当前映射节点
				current.Content = append(current.Content, newKeyNode, newValueNode)
				return nil
			} else {
				// 中间层不存在，创建新的映射节点
				newKeyNode := &yaml.Node{
					Kind:  yaml.ScalarNode,
					Value: key,
				}
				newMapNode := &yaml.Node{
					Kind: yaml.MappingNode,
				}
				current.Content = append(current.Content, newKeyNode, newMapNode)
				current = newMapNode
			}
		}
	}

	return nil
}

// setNestedValue 递归设置嵌套配置值（通过点分隔的key）
func setNestedValue(config map[string]interface{}, key string, value interface{}) {
	keys := splitKey(key)
	if len(keys) == 0 {
		return
	}

	// 递归找到最后一层的map
	current := config
	for i := 0; i < len(keys)-1; i++ {
		k := keys[i]
		if next, ok := current[k].(map[string]interface{}); ok {
			current = next
		} else {
			// 如果中间层不存在或不是map，创建新map
			newMap := make(map[string]interface{})
			current[k] = newMap
			current = newMap
		}
	}

	// 设置最后一层的值
	lastKey := keys[len(keys)-1]
	current[lastKey] = value
}

// splitKey 分割点分隔的key（例如 "quota.level-limits" -> ["quota", "level-limits"]）
func splitKey(key string) []string {
	var result []string
	var current string

	for _, ch := range key {
		if ch == '.' {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		} else {
			current += string(ch)
		}
	}

	if current != "" {
		result = append(result, current)
	}

	return result
}

// kebabToCamel 将连接符格式转换为驼峰格式
// 例如: "enable-oauth2" -> "enableOAuth2", "enable-email" -> "enableEmail"
func kebabToCamel(s string) string {
	// 特殊词汇映射表
	specialWords := map[string]string{
		"oauth2": "OAuth2",
		"smtp":   "SMTP",
		"qq":     "QQ",
		"id":     "ID",
		"ip":     "IP",
		"url":    "URL",
		"cdn":    "CDN",
		"db":     "DB",
		"api":    "API",
		"http":   "HTTP",
		"https":  "HTTPS",
		"ssl":    "SSL",
		"tls":    "TLS",
	}

	parts := strings.Split(s, "-")
	if len(parts) == 1 {
		return s
	}

	result := parts[0]
	for i := 1; i < len(parts); i++ {
		part := parts[i]
		// 检查是否是特殊词汇
		if special, exists := specialWords[strings.ToLower(part)]; exists {
			result += special
		} else if len(part) > 0 {
			// 首字母大写
			result += strings.ToUpper(part[:1]) + part[1:]
		}
	}

	return result
}

// camelToKebab 将驼峰格式转换为连接符格式
// 例如: "enableEmail" -> "enable-email", "levelLimits" -> "level-limits"
// 特殊处理: "enableOAuth2" -> "enable-oauth2", "enableQQ" -> "enable-qq"
func camelToKebab(s string) string {
	if s == "" {
		return s
	}

	// 特殊词汇的正则表达式替换
	// 这些词汇需要特殊处理，防止被拆分成多个部分
	// 例如: OAuth2 不应该被拆分成 O-Auth2
	specialWords := []struct {
		pattern     string
		replacement string
	}{
		{`OAuth2`, `@oauth2`}, // enableOAuth2 -> enable@oauth2
		{`QQ`, `@qq`},         // enableQQ -> enable@qq
		{`SMTP`, `@smtp`},     // emailSMTP -> email@smtp
		{`IP`, `@ip`},         // serverIP -> server@ip
		{`URL`, `@url`},       // baseURL -> base@url
		{`CDN`, `@cdn`},       // enableCDN -> enable@cdn
		{`DB`, `@db`},         // maxDB -> max@db
		{`API`, `@api`},       // restAPI -> rest@api
		{`JWT`, `@jwt`},       // useJWT -> use@jwt
		{`ID`, `@id`},         // userID -> user@id
	}

	temp := s
	for _, sw := range specialWords {
		// 不使用 \b 边界，直接替换
		// 因为驼峰命名中，特殊词汇前后都是字母，没有明确的单词边界
		temp = strings.ReplaceAll(temp, sw.pattern, sw.replacement)
	}

	// 执行驼峰到kebab的转换
	var result []rune
	var lastWasUpper bool

	for i, r := range temp {
		// @ 标记表示这里需要添加分隔符
		if r == '@' {
			// 如果不是在开头，且前面有字符，添加分隔符
			if i > 0 {
				result = append(result, '-')
			}
			continue // 跳过 @，不添加到结果中
		}

		isUpper := r >= 'A' && r <= 'Z'

		// 如果当前是大写字母
		if isUpper {
			if i > 0 {
				// 如果前一个不是大写，添加分隔符
				if !lastWasUpper {
					result = append(result, '-')
				} else if i+1 < len(temp) {
					// 检查下一个字符 - 处理 HTTPServer 这种情况
					nextRune := rune(temp[i+1])
					if nextRune >= 'a' && nextRune <= 'z' {
						result = append(result, '-')
					}
				}
			}
			lastWasUpper = true
		} else {
			lastWasUpper = false
		}

		result = append(result, r)
	}

	return strings.ToLower(string(result))
}

// convertMapKeysToKebab 递归将map的key从驼峰转换为连接符格式
func convertMapKeysToKebab(data map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for key, value := range data {
		// 转换当前key
		kebabKey := camelToKebab(key)

		// 如果value是map，递归转换
		if mapValue, ok := value.(map[string]interface{}); ok {
			result[kebabKey] = convertMapKeysToKebab(mapValue)
		} else {
			result[kebabKey] = value
		}
	}
	return result
}

// ===== 配置恢复相关方法 =====

// isConfigModified 检查配置是否已被修改（标志文件是否存在）
func (cm *ConfigManager) isConfigModified() bool {
	_, err := os.Stat(ConfigModifiedFlagFile)
	return err == nil
}

// markConfigAsModified 标记配置已被修改
func (cm *ConfigManager) markConfigAsModified() error {
	// 确保目录存在
	dir := filepath.Dir(ConfigModifiedFlagFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建标志文件目录失败: %v", err)
	}

	// 创建标志文件
	file, err := os.Create(ConfigModifiedFlagFile)
	if err != nil {
		return fmt.Errorf("创建标志文件失败: %v", err)
	}
	defer file.Close()

	// 写入时间戳
	timestamp := time.Now().Format(time.RFC3339)
	if _, err := file.WriteString(fmt.Sprintf("Configuration modified at: %s\n", timestamp)); err != nil {
		return fmt.Errorf("写入标志文件失败: %v", err)
	}

	cm.logger.Info("配置修改标志文件已创建", zap.String("file", ConfigModifiedFlagFile))
	return nil
}

// clearConfigModifiedFlag 清除配置修改标志文件
func (cm *ConfigManager) clearConfigModifiedFlag() error {
	if _, err := os.Stat(ConfigModifiedFlagFile); err != nil {
		if os.IsNotExist(err) {
			// 文件不存在，无需清除
			return nil
		}
		return fmt.Errorf("检查标志文件失败: %v", err)
	}

	if err := os.Remove(ConfigModifiedFlagFile); err != nil {
		return fmt.Errorf("删除标志文件失败: %v", err)
	}

	cm.logger.Info("配置修改标志文件已清除", zap.String("file", ConfigModifiedFlagFile))
	return nil
}

// parseConfigValue 解析配置值，尝试将JSON字符串反序列化为原始类型
func parseConfigValue(valueStr string) interface{} {
	// 如果为空字符串，返回空字符串（在YAML中会显示为空值）
	if valueStr == "" {
		return ""
	}

	// 尝试JSON反序列化
	var jsonValue interface{}
	if err := json.Unmarshal([]byte(valueStr), &jsonValue); err == nil {
		// 如果成功反序列化，返回反序列化后的值
		// fmt.Printf("解析配置值: %s -> %v (类型: %T)\n", valueStr, jsonValue, jsonValue)
		return jsonValue
	}

	// 如果不是有效的JSON，返回原始字符串
	return valueStr
}

// RestoreConfigFromDatabase 从数据库恢复配置到YAML文件
func (cm *ConfigManager) RestoreConfigFromDatabase() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.logger.Info("开始从数据库恢复配置到YAML文件")

	// 从数据库读取所有配置
	var configs []SystemConfig
	if err := cm.db.Find(&configs).Error; err != nil {
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
	configsToSync := make(map[string]interface{})
	skippedSystemConfigs := 0
	for key, value := range allConfigs {
		if isSystemLevelConfig(key) {
			skippedSystemConfigs++
			cm.logger.Debug("跳过系统级配置（必须来自YAML）",
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
				Columns:   []clause.Column{{Name: "key"}},
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
