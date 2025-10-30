package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
)

// 配置标志文件路径和配置状态常量
const (
	ConfigModifiedFlagFile = "./storage/.config_modified" // 配置已通过API修改的标志文件
)

// 公开配置键列表（不需要认证即可访问）
var publicConfigKeys = map[string]bool{
	"auth.enable-public-registration": true,
	"other.default-language":          true,
	"other.max-avatar-size":           true,
}

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

// PreInitializeConfigManager 预初始化配置管理器并注册回调（在InitializeConfigManager之前调用）
func PreInitializeConfigManager(db *gorm.DB, logger *zap.Logger, callback ConfigChangeCallback) {
	// 如果配置管理器还不存在，创建它但不加载配置
	if configManager == nil {
		configManager = NewConfigManager(db, logger)
		configManager.initValidationRules()
	}

	// 注册回调
	if callback != nil {
		configManager.RegisterChangeCallback(callback)
		logger.Info("配置变更回调已提前注册")
	}
}

// InitializeConfigManager 初始化配置管理器
func InitializeConfigManager(db *gorm.DB, logger *zap.Logger) {
	once.Do(func() {
		// 如果配置管理器还不存在，创建它
		if configManager == nil {
			configManager = NewConfigManager(db, logger)
			configManager.initValidationRules()
		}
		// 加载配置（此时回调已经注册好了）
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

	// 直接重新创建配置管理器实例（如果不存在）或更新现有实例
	if configManager == nil {
		configManager = NewConfigManager(db, logger)
		configManager.initValidationRules()
	} else {
		// 更新数据库和日志记录器引用
		configManager.db = db
		configManager.logger = logger
	}

	// 重新加载配置（此时回调应该已经注册好了）
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

	// 更多验证规则...
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
	// 将驼峰格式转换为连接符格式，以保持与YAML一致
	kebabConfig := convertMapKeysToKebab(config)
	cm.logger.Info("转换配置格式",
		zap.Int("originalKeys", len(config)),
		zap.Int("kebabKeys", len(kebabConfig)))

	// 展开嵌套配置并验证
	flatConfig := cm.flattenConfig(kebabConfig, "")
	cm.logger.Info("扁平化后的配置",
		zap.Int("count", len(flatConfig)),
		zap.Any("keys", func() []string {
			keys := make([]string, 0, len(flatConfig))
			for k := range flatConfig {
				keys = append(keys, k)
			}
			return keys
		}()))

	for key, value := range flatConfig {
		if err := cm.validateConfig(key, value); err != nil {
			cm.mu.Unlock()
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
			cm.mu.Unlock()
			return fmt.Errorf("保存配置 %s 失败: %v", key, err)
		}
	}

	// 在提交事务前先创建标志文件
	// 如果标志文件创建失败，回滚整个事务
	if err := cm.markConfigAsModified(); err != nil {
		tx.Rollback()
		// 恢复配置
		for k, v := range oldValues {
			cm.configCache[k] = v
		}
		cm.logger.Error("创建配置修改标志文件失败，已回滚事务", zap.Error(err))
		cm.mu.Unlock()
		return fmt.Errorf("创建配置修改标志文件失败: %v", err)
	}

	// 提交事务
	if err := tx.Commit().Error; err != nil {
		// 事务提交失败，清除标志文件
		cm.clearConfigModifiedFlag()
		// 恢复配置
		for k, v := range oldValues {
			cm.configCache[k] = v
		}
		cm.mu.Unlock()
		return fmt.Errorf("提交配置事务失败: %v", err)
	}

	cm.lastUpdate = time.Now()

	// 释放锁，准备执行可能耗时的操作
	cm.mu.Unlock()

	// 同步配置到全局配置 - 使用连接符格式的配置
	// 注意：这里在锁外执行，避免持锁时间过长
	if err := cm.syncToGlobalConfig(kebabConfig); err != nil {
		cm.logger.Error("同步配置到全局配置失败", zap.Error(err))
	}

	// 触发回调 - 使用连接符格式的配置
	// 注意：这里在锁外执行，避免回调函数执行时间过长阻塞其他读取操作
	for key, newValue := range kebabConfig {
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

// createSnapshot 创建配置快照（深拷贝）
func (cm *ConfigManager) createSnapshot() {
	snapshot := ConfigSnapshot{
		Timestamp: time.Now(),
		Config:    make(map[string]interface{}),
		Version:   fmt.Sprintf("v%d", time.Now().Unix()),
	}

	// 深拷贝配置值，避免引用类型被修改
	for k, v := range cm.configCache {
		snapshot.Config[k] = deepCopyValue(v)
	}

	cm.rollbackVersions = append(cm.rollbackVersions, snapshot)

	// 限制快照数量
	if len(cm.rollbackVersions) > cm.maxRollbackCount {
		cm.rollbackVersions = cm.rollbackVersions[1:]
	}
}

// deepCopyValue 深拷贝配置值
func deepCopyValue(v interface{}) interface{} {
	if v == nil {
		return nil
	}

	switch val := v.(type) {
	case map[string]interface{}:
		// 深拷贝 map
		copyMap := make(map[string]interface{}, len(val))
		for k, v := range val {
			copyMap[k] = deepCopyValue(v)
		}
		return copyMap
	case []interface{}:
		// 深拷贝 slice
		copySlice := make([]interface{}, len(val))
		for i, v := range val {
			copySlice[i] = deepCopyValue(v)
		}
		return copySlice
	case []string:
		// 深拷贝字符串 slice
		copyStrings := make([]string, len(val))
		copy(copyStrings, val)
		return copyStrings
	case []int:
		// 深拷贝 int slice
		copyInts := make([]int, len(val))
		copy(copyInts, val)
		return copyInts
	default:
		// 基本类型（string, int, bool, float等）直接返回
		return v
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

	// 检查是否存在数据库配置数据
	var configCount int64
	if err := cm.db.Model(&SystemConfig{}).Count(&configCount).Error; err != nil {
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

	// 场景2：数据库有配置 + 标志文件不存在 = 可能是升级后首次启动，标志文件丢失
	// 策略：智能判断 - 对比数据库和YAML的时间戳或内容
	if configCount > 0 && !configModified {
		cm.logger.Info("场景：数据库有配置但无标志文件（可能是升级场景）")
		shouldUseDatabaseConfig := cm.shouldPreferDatabaseConfig()

		if shouldUseDatabaseConfig {
			cm.logger.Info("判断：数据库配置更新，优先使用数据库配置")
			// 重新创建标志文件
			if err := cm.markConfigAsModified(); err != nil {
				cm.logger.Warn("重新创建标志文件失败", zap.Error(err))
			}
			if err := cm.handleDatabaseFirst(); err != nil {
				cm.logger.Error("处理数据库优先策略失败", zap.Error(err))
			}
		} else {
			cm.logger.Info("判断：YAML配置为初始配置，同步YAML到数据库")
			if err := cm.handleYAMLFirst(); err != nil {
				cm.logger.Error("处理YAML优先策略失败", zap.Error(err))
			}
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
}

// handleDatabaseFirst 处理数据库优先的策略
func (cm *ConfigManager) handleDatabaseFirst() error {
	cm.logger.Info("执行策略：数据库 → YAML → global")

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

	return nil
}

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
		cm.configCache[config.Key] = parseConfigValue(config.Value)
	}
	cm.mu.Unlock()
	cm.logger.Info("配置已加载到缓存", zap.Int("configCount", len(configs)))

	// 4. 同步到全局配置（触发回调）
	if err := cm.syncDatabaseConfigToGlobal(); err != nil {
		cm.logger.Error("同步配置到全局配置失败", zap.Error(err))
		return err
	}
	cm.logger.Info("配置已同步到全局配置")

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

	// 策略2：检查数据库配置的更新时间
	// 如果最近有更新，说明是用户修改过的配置
	var latestConfig SystemConfig
	if err := cm.db.Order("updated_at DESC").First(&latestConfig).Error; err == nil {
		// 如果最近24小时内有更新，优先使用数据库
		if time.Since(latestConfig.UpdatedAt) < 24*time.Hour {
			cm.logger.Info("数据库配置最近有更新，优先使用数据库",
				zap.Time("lastUpdate", latestConfig.UpdatedAt))
			return true
		}
	}

	// 策略3：对比数据库配置数量
	// 如果数据库配置数量较多（>10），说明已经初始化过
	if len(configs) > 10 {
		cm.logger.Info("数据库中有大量配置，判断为已初始化环境",
			zap.Int("configCount", len(configs)))
		return true
	}

	// 策略4：检查是否有system_configs表的数据
	// 如果表存在且有数据，说明之前运行过
	var count int64
	cm.db.Model(&SystemConfig{}).Count(&count)
	if count > 0 {
		cm.logger.Info("数据库system_configs表存在且有数据，优先使用数据库",
			zap.Int64("count", count))
		return true
	}

	// 默认情况：使用YAML配置
	cm.logger.Info("判断为首次启动，使用YAML配置")
	return false
}

// saveConfigToDB 保存配置到数据库
func (cm *ConfigManager) saveConfigToDB(key string, value interface{}) error {
	return cm.saveConfigToDBWithTx(cm.db, key, value)
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
			return fmt.Errorf("key not found: %s", key)
		}
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
		// 空字符串也使用空值表示
		if v == "" {
			node.Kind = yaml.ScalarNode
			node.Tag = "!!null"
			node.Value = ""
		} else {
			node.Kind = yaml.ScalarNode
			node.Tag = "!!str"
			node.Value = v
		}
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
func (cm *ConfigManager) syncDatabaseConfigToGlobal() error {
	// 构建嵌套配置结构
	nestedConfig := make(map[string]interface{})

	// 将扁平配置转换为嵌套结构
	for key, value := range cm.configCache {
		setNestedValue(nestedConfig, key, value)
	}

	// 遍历配置并同步到全局配置
	// 这里需要导入 global 包，但为了避免循环导入
	// 我们通过回调机制来实现同步
	for key, value := range nestedConfig {
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

// camelToKebab 将驼峰格式转换为连接符格式
// 例如: "enableEmail" -> "enable-email", "levelLimits" -> "level-limits"
// 特殊处理: "OAuth2" -> "oauth2", "QQ" -> "qq", "SMTP" -> "smtp", "ID" -> "id"
func camelToKebab(s string) string {
	// 特殊词汇映射表 - 这些词汇不应该被连接符分割
	specialWords := map[string]string{
		"OAuth2": "oauth2",
		"oauth2": "oauth2",
		"QQ":     "qq",
		"qq":     "qq",
		"SMTP":   "smtp",
		"smtp":   "smtp",
		"ID":     "id",
		"id":     "id",
		"IP":     "ip",
		"ip":     "ip",
		"URL":    "url",
		"url":    "url",
		"CDN":    "cdn",
		"cdn":    "cdn",
		"DB":     "db",
		"db":     "db",
		"API":    "api",
		"api":    "api",
		"JWT":    "jwt",
		"jwt":    "jwt",
	}

	// 直接检查是否是特殊词汇
	if mapped, ok := specialWords[s]; ok {
		return mapped
	}

	var result []rune
	var lastWasUpper bool

	for i, r := range s {
		isUpper := r >= 'A' && r <= 'Z'

		// 如果当前是大写字母
		if isUpper {
			// 检查是否是连续大写（如 ID, QQ, SMTP）
			if i > 0 {
				// 如果前一个不是大写，或者下一个是小写（驼峰边界），则添加分隔符
				if !lastWasUpper {
					result = append(result, '-')
				} else if i+1 < len(s) {
					// 检查下一个字符
					nextRune := rune(s[i+1])
					if nextRune >= 'a' && nextRune <= 'z' {
						// 这是 HTTPServer 这样的情况，在 HTTP 和 Server 之间加分隔符
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

	// 使用Node API更新每个配置值
	for _, config := range configs {
		// 尝试反序列化JSON值
		value := parseConfigValue(config.Value)

		if err := updateYAMLNode(&node, config.Key, value); err != nil {
			// 只在debug级别记录配置键不存在的警告，避免日志噪音
			cm.logger.Debug("更新配置失败",
				zap.String("key", config.Key),
				zap.Error(err))
		}
	}

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

	// 更新内存缓存
	for _, config := range configs {
		cm.configCache[config.Key] = config.Value
	}

	cm.logger.Info("配置已成功从数据库恢复到YAML文件")
	return nil
}

// syncYAMLConfigToDatabase 将YAML配置同步到数据库
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

	// 提取需要同步的配置部分
	configsToSync := make(map[string]interface{})

	// Auth配置
	if auth, ok := yamlConfig["auth"].(map[string]interface{}); ok {
		for key, value := range auth {
			configsToSync[fmt.Sprintf("auth.%s", key)] = value
		}
	}

	// Quota配置
	if quota, ok := yamlConfig["quota"].(map[string]interface{}); ok {
		if defaultLevel, exists := quota["default-level"]; exists {
			configsToSync["quota.default-level"] = defaultLevel
		}

		// level-limits 作为整体存储（序列化为JSON字符串）
		if levelLimits, ok := quota["level-limits"].(map[string]interface{}); ok {
			// 将 level-limits 转换为 JSON 字符串存储
			levelLimitsJSON, err := yaml.Marshal(levelLimits)
			if err != nil {
				cm.logger.Warn("序列化 level-limits 失败", zap.Error(err))
			} else {
				configsToSync["quota.level-limits"] = string(levelLimitsJSON)
			}
		}

		if permissions, ok := quota["instance-type-permissions"].(map[string]interface{}); ok {
			for key, value := range permissions {
				configsToSync[fmt.Sprintf("quota.instance-type-permissions.%s", key)] = value
			}
		}
	}

	// InviteCode配置
	if inviteCode, ok := yamlConfig["invite-code"].(map[string]interface{}); ok {
		for key, value := range inviteCode {
			configsToSync[fmt.Sprintf("invite-code.%s", key)] = value
		}
	}

	// OAuth2配置
	if oauth2, ok := yamlConfig["oauth2"].(map[string]interface{}); ok {
		for key, value := range oauth2 {
			configsToSync[fmt.Sprintf("oauth2.%s", key)] = value
		}
	}

	// System配置
	if system, ok := yamlConfig["system"].(map[string]interface{}); ok {
		for key, value := range system {
			configsToSync[fmt.Sprintf("system.%s", key)] = value
		}
	}

	// JWT配置
	if jwt, ok := yamlConfig["jwt"].(map[string]interface{}); ok {
		for key, value := range jwt {
			configsToSync[fmt.Sprintf("jwt.%s", key)] = value
		}
	}

	// CORS配置
	if cors, ok := yamlConfig["cors"].(map[string]interface{}); ok {
		for key, value := range cors {
			configsToSync[fmt.Sprintf("cors.%s", key)] = value
		}
	}

	// Redis配置
	if redis, ok := yamlConfig["redis"].(map[string]interface{}); ok {
		for key, value := range redis {
			configsToSync[fmt.Sprintf("redis.%s", key)] = value
		}
	}

	// CDN配置
	if cdn, ok := yamlConfig["cdn"].(map[string]interface{}); ok {
		for key, value := range cdn {
			configsToSync[fmt.Sprintf("cdn.%s", key)] = value
		}
	}

	// Task配置
	if task, ok := yamlConfig["task"].(map[string]interface{}); ok {
		for key, value := range task {
			configsToSync[fmt.Sprintf("task.%s", key)] = value
		}
	}

	// Captcha配置
	if captcha, ok := yamlConfig["captcha"].(map[string]interface{}); ok {
		for key, value := range captcha {
			configsToSync[fmt.Sprintf("captcha.%s", key)] = value
		}
	}

	// Mysql配置
	if mysql, ok := yamlConfig["mysql"].(map[string]interface{}); ok {
		for key, value := range mysql {
			configsToSync[fmt.Sprintf("mysql.%s", key)] = value
		}
	}

	// Zap配置
	if zap, ok := yamlConfig["zap"].(map[string]interface{}); ok {
		for key, value := range zap {
			configsToSync[fmt.Sprintf("zap.%s", key)] = value
		}
	}

	// Upload配置
	if upload, ok := yamlConfig["upload"].(map[string]interface{}); ok {
		for key, value := range upload {
			configsToSync[fmt.Sprintf("upload.%s", key)] = value
		}
	}

	// 批量保存到数据库
	tx := cm.db.Begin()
	savedCount := 0
	for key, value := range configsToSync {
		if err := cm.saveConfigToDBWithTx(tx, key, value); err != nil {
			tx.Rollback()
			return fmt.Errorf("保存配置 %s 到数据库失败: %v", key, err)
		}
		savedCount++
	}

	if err := tx.Commit().Error; err != nil {
		return fmt.Errorf("提交配置到数据库失败: %v", err)
	}

	cm.logger.Info("YAML配置已成功同步到数据库", zap.Int("count", savedCount))
	return nil
}
