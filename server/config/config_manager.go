package config

import (
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// SystemConfig 系统配置模型（避免循环导入）
type SystemConfig struct {
	ID          uint           `json:"id" gorm:"primarykey"`
	Category    string         `json:"category" gorm:"size:50;not null;index"`
	Key         string         `json:"key" gorm:"size:100;not null;index"`
	Value       string         `json:"value" gorm:"type:text"`
	Description string         `json:"description" gorm:"size:255"`
	Type        string         `json:"type" gorm:"size:20;not null;default:string"`
	IsPublic    bool           `json:"isPublic" gorm:"not null;default:false"`
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `json:"deletedAt" gorm:"index"`
}

func (SystemConfig) TableName() string {
	return "system_configs"
}

// ConfigManager 统一的配置管理器
type ConfigManager struct {
	mu               sync.RWMutex
	db               *gorm.DB
	logger           *zap.Logger
	configCache      map[string]interface{}
	lastUpdate       time.Time
	validationRules  map[string]ConfigValidationRule
	changeCallbacks  []ConfigChangeCallback
	rollbackVersions []ConfigSnapshot
	maxRollbackCount int
}

// ConfigValidationRule 配置验证规则
type ConfigValidationRule struct {
	Required  bool
	Type      string // string, int, bool, array, object
	MinValue  interface{}
	MaxValue  interface{}
	Pattern   string
	Validator func(interface{}) error
}

// ConfigChangeCallback 配置变更回调
type ConfigChangeCallback func(key string, oldValue, newValue interface{}) error

// ConfigSnapshot 配置快照
type ConfigSnapshot struct {
	Timestamp time.Time
	Config    map[string]interface{}
	Version   string
}

var (
	configManager *ConfigManager
	once          sync.Once
)

// NewConfigManager 创建新的配置管理器
func NewConfigManager(db *gorm.DB, logger *zap.Logger) *ConfigManager {
	return &ConfigManager{
		db:               db,
		logger:           logger,
		configCache:      make(map[string]interface{}),
		validationRules:  make(map[string]ConfigValidationRule),
		maxRollbackCount: 10,
	}
}

// GetConfigManager 获取配置管理器实例
func GetConfigManager() *ConfigManager {
	return configManager
}

// InitializeConfigManager 初始化配置管理器
func InitializeConfigManager(db *gorm.DB, logger *zap.Logger) {
	once.Do(func() {
		configManager = NewConfigManager(db, logger)
		configManager.initValidationRules()
		configManager.loadConfigFromDB()
	})
}

// ReInitializeConfigManager 重新初始化配置管理器（用于系统初始化完成后）
func ReInitializeConfigManager(db *gorm.DB, logger *zap.Logger) {
	if db == nil || logger == nil {
		if logger != nil {
			logger.Error("重新初始化配置管理器失败: 数据库或日志记录器为空")
		}
		return
	}

	// 直接重新创建配置管理器实例，绕过 sync.Once 限制
	configManager = NewConfigManager(db, logger)
	configManager.initValidationRules()
	configManager.loadConfigFromDB()

	logger.Info("配置管理器重新初始化完成")
}

// initValidationRules 初始化验证规则
func (cm *ConfigManager) initValidationRules() {
	// 认证配置验证规则
	cm.validationRules["auth.enableEmail"] = ConfigValidationRule{
		Required: true,
		Type:     "bool",
	}
	cm.validationRules["auth.enableOAuth2"] = ConfigValidationRule{
		Required: false,
		Type:     "bool",
	}
	cm.validationRules["auth.emailSMTPPort"] = ConfigValidationRule{
		Required: false,
		Type:     "int",
		MinValue: 1,
		MaxValue: 65535,
	}
	cm.validationRules["quota.defaultLevel"] = ConfigValidationRule{
		Required: true,
		Type:     "int",
		MinValue: 1,
		MaxValue: 5,
	}

	// 等级限制配置验证规则
	cm.validationRules["quota.levelLimits"] = ConfigValidationRule{
		Required: false,
		Type:     "object",
		Validator: func(value interface{}) error {
			return cm.validateLevelLimits(value)
		},
	}

	// 添加更多验证规则...
}

// GetConfig 获取配置
func (cm *ConfigManager) GetConfig(key string) (interface{}, bool) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	value, exists := cm.configCache[key]
	return value, exists
}

// GetAllConfig 获取所有配置
func (cm *ConfigManager) GetAllConfig() map[string]interface{} {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make(map[string]interface{})
	for k, v := range cm.configCache {
		result[k] = v
	}
	return result
}

// SetConfig 设置单个配置项
func (cm *ConfigManager) SetConfig(key string, value interface{}) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 验证配置值
	if err := cm.validateConfig(key, value); err != nil {
		return fmt.Errorf("配置验证失败: %v", err)
	}

	// 保存快照
	oldValue := cm.configCache[key]
	cm.createSnapshot()

	// 更新配置
	cm.configCache[key] = value
	cm.lastUpdate = time.Now()

	// 保存到数据库
	if err := cm.saveConfigToDB(key, value); err != nil {
		// 回滚
		cm.configCache[key] = oldValue
		return fmt.Errorf("保存配置到数据库失败: %v", err)
	}

	// 触发回调
	for _, callback := range cm.changeCallbacks {
		if err := callback(key, oldValue, value); err != nil {
			cm.logger.Error("配置变更回调失败",
				zap.String("key", key),
				zap.Error(err))
		}
	}

	return nil
}

// UpdateConfig 批量更新配置
func (cm *ConfigManager) UpdateConfig(config map[string]interface{}) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// 展开嵌套配置并验证
	flatConfig := cm.flattenConfig(config, "")
	for key, value := range flatConfig {
		if err := cm.validateConfig(key, value); err != nil {
			return fmt.Errorf("配置 %s 验证失败: %v", key, err)
		}
	}

	// 创建快照
	cm.createSnapshot()

	// 保存旧配置用于比较
	oldConfig := make(map[string]interface{})
	for key := range flatConfig {
		oldConfig[key] = cm.configCache[key]
	}

	// 开始事务
	tx := cm.db.Begin()

	// 更新配置
	oldValues := make(map[string]interface{})
	for key, value := range flatConfig {
		oldValues[key] = cm.configCache[key]
		cm.configCache[key] = value

		if err := cm.saveConfigToDBWithTx(tx, key, value); err != nil {
			tx.Rollback()
			// 恢复配置
			for k, v := range oldValues {
				cm.configCache[k] = v
			}
			return fmt.Errorf("保存配置 %s 失败: %v", key, err)
		}
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		// 恢复配置
		for k, v := range oldValues {
			cm.configCache[k] = v
		}
		return fmt.Errorf("提交配置事务失败: %v", err)
	}

	cm.lastUpdate = time.Now()

	// 同步配置到全局配置
	if err := cm.syncToGlobalConfig(config); err != nil {
		cm.logger.Error("同步配置到全局配置失败", zap.Error(err))
	}

	// 触发回调
	for key, newValue := range config {
		oldValue := oldValues[key]
		for _, callback := range cm.changeCallbacks {
			if err := callback(key, oldValue, newValue); err != nil {
				cm.logger.Error("配置变更回调失败",
					zap.String("key", key),
					zap.Error(err))
			}
		}
	}

	return nil
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

// validateLevelLimits 验证等级限制配置
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

		// 验证 maxInstances
		maxInstances, exists := limitMap["maxInstances"]
		if !exists {
			return fmt.Errorf("等级 %s 缺少 maxInstances 配置", levelStr)
		}
		if err := validatePositiveNumber(maxInstances, fmt.Sprintf("等级 %s 的 maxInstances", levelStr)); err != nil {
			return err
		}

		// 验证 maxTraffic
		maxTraffic, exists := limitMap["maxTraffic"]
		if !exists {
			return fmt.Errorf("等级 %s 缺少 maxTraffic 配置", levelStr)
		}
		if err := validatePositiveNumber(maxTraffic, fmt.Sprintf("等级 %s 的 maxTraffic", levelStr)); err != nil {
			return err
		}

		// 验证 maxResources
		maxResources, exists := limitMap["maxResources"]
		if !exists {
			return fmt.Errorf("等级 %s 缺少 maxResources 配置", levelStr)
		}

		resourcesMap, ok := maxResources.(map[string]interface{})
		if !ok {
			return fmt.Errorf("等级 %s 的 maxResources 必须是对象类型", levelStr)
		}

		// 验证必需的资源字段
		requiredResources := []string{"cpu", "memory", "disk", "bandwidth"}
		for _, resource := range requiredResources {
			resourceValue, exists := resourcesMap[resource]
			if !exists {
				return fmt.Errorf("等级 %s 的 maxResources 缺少 %s 配置", levelStr, resource)
			}
			if err := validatePositiveNumber(resourceValue, fmt.Sprintf("等级 %s 的 %s", levelStr, resource)); err != nil {
				return err
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
			// 先保存这一层的值（用于验证）
			result[fullKey] = value

			// 然后递归展开子配置（但不包括 levelLimits，因为它需要作为整体验证）
			if key != "levelLimits" {
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

// createSnapshot 创建配置快照
func (cm *ConfigManager) createSnapshot() {
	snapshot := ConfigSnapshot{
		Timestamp: time.Now(),
		Config:    make(map[string]interface{}),
		Version:   fmt.Sprintf("v%d", time.Now().Unix()),
	}

	for k, v := range cm.configCache {
		snapshot.Config[k] = v
	}

	cm.rollbackVersions = append(cm.rollbackVersions, snapshot)

	// 限制快照数量
	if len(cm.rollbackVersions) > cm.maxRollbackCount {
		cm.rollbackVersions = cm.rollbackVersions[1:]
	}
}

// RollbackToSnapshot 回滚到指定快照
func (cm *ConfigManager) RollbackToSnapshot(version string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	var targetSnapshot *ConfigSnapshot
	for _, snapshot := range cm.rollbackVersions {
		if snapshot.Version == version {
			targetSnapshot = &snapshot
			break
		}
	}

	if targetSnapshot == nil {
		return fmt.Errorf("未找到版本 %s 的快照", version)
	}

	// 回滚配置
	return cm.UpdateConfig(targetSnapshot.Config)
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

	var configs []SystemConfig
	if err := cm.db.Find(&configs).Error; err != nil {
		cm.logger.Error("加载配置失败", zap.Error(err))
		return
	}

	cm.logger.Info("从数据库加载配置", zap.Int("configCount", len(configs)))

	for _, config := range configs {
		cm.configCache[config.Key] = config.Value
	}
}

// saveConfigToDB 保存配置到数据库
func (cm *ConfigManager) saveConfigToDB(key string, value interface{}) error {
	return cm.saveConfigToDBWithTx(cm.db, key, value)
}

// saveConfigToDBWithTx 使用事务保存配置到数据库
func (cm *ConfigManager) saveConfigToDBWithTx(tx *gorm.DB, key string, value interface{}) error {
	// 将value转换为字符串
	valueStr := fmt.Sprintf("%v", value)

	config := SystemConfig{
		Key:   key,
		Value: valueStr,
	}

	return tx.Where("`key` = ?", key).Assign(config).FirstOrCreate(&config).Error
}

// RegisterChangeCallback 注册配置变更回调
func (cm *ConfigManager) RegisterChangeCallback(callback ConfigChangeCallback) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.changeCallbacks = append(cm.changeCallbacks, callback)
}

// GetSnapshots 获取所有快照
func (cm *ConfigManager) GetSnapshots() []ConfigSnapshot {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	result := make([]ConfigSnapshot, len(cm.rollbackVersions))
	copy(result, cm.rollbackVersions)
	return result
}

// syncToGlobalConfig 同步配置到全局配置并写回YAML文件
func (cm *ConfigManager) syncToGlobalConfig(config map[string]interface{}) error {
	// 这个方法需要导入 global 包，但为了避免循环导入，我们需要通过依赖注入或回调的方式实现
	// 暂时先记录日志，具体实现需要在初始化时注册同步回调
	cm.logger.Info("配置已更新，需要同步到全局配置", zap.Any("config", config))

	// 写回YAML文件
	if err := cm.writeConfigToYAML(config); err != nil {
		cm.logger.Error("写回YAML文件失败", zap.Error(err))
		return err
	}

	return nil
}

// writeConfigToYAML 将配置写回到YAML文件（保留原始key格式，避免驼峰转换）
func (cm *ConfigManager) writeConfigToYAML(updates map[string]interface{}) error {
	// 读取现有配置文件
	file, err := os.ReadFile("config.yaml")
	if err != nil {
		cm.logger.Error("读取配置文件失败", zap.Error(err))
		return err
	}

	// 解析YAML到map
	var config map[string]interface{}
	if err := yaml.Unmarshal(file, &config); err != nil {
		cm.logger.Error("解析YAML失败", zap.Error(err))
		return err
	}

	// 递归合并更新（保留连接符key）
	for key, value := range updates {
		setNestedValue(config, key, value)
	}

	// 序列化回YAML
	out, err := yaml.Marshal(config)
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
