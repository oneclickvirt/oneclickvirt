package config

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
	"gopkg.in/yaml.v3"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 配置标志文件路径和配置状态常量
const (
	ConfigModifiedFlagFile = "./storage/.config_modified" // 配置已通过API修改的标志文件
)

// 系统级配置键列表（启动必需配置，必须100%来自YAML，不能被数据库覆盖）
// 这些配置包括：
// - 数据库连接信息（必须在数据库连接前读取）
// - 服务器端口和环境配置（影响启动行为）
// - 基础系统设置（如OSS类型、是否使用Redis等）
var systemLevelConfigKeys = map[string]bool{
	// System 配置（所有 system.* 都是系统级配置）
	"system.addr":                       true,
	"system.db-type":                    true,
	"system.env":                        true,
	"system.frontend-url":               true,
	"system.iplimit-count":              true,
	"system.iplimit-time":               true,
	"system.oauth2-state-token-minutes": true,
	"system.oss-type":                   true,
	"system.provider-inactive-hours":    true,
	"system.use-multipoint":             true,
	"system.use-redis":                  true,

	// MySQL 配置（数据库连接信息，必须在连接数据库前读取）
	"mysql.path":           true,
	"mysql.port":           true,
	"mysql.config":         true,
	"mysql.db-name":        true,
	"mysql.username":       true,
	"mysql.password":       true,
	"mysql.prefix":         true,
	"mysql.singular":       true,
	"mysql.engine":         true,
	"mysql.max-idle-conns": true,
	"mysql.max-open-conns": true,
	"mysql.max-lifetime":   true,
	"mysql.log-mode":       true,
	"mysql.log-zap":        true,
	"mysql.auto-create":    true,

	// Redis 配置（如果启用Redis，也是启动必需）
	"redis.addr":     true,
	"redis.password": true,
	"redis.db":       true,

	// Zap 日志配置（日志系统启动必需）
	"zap.level":              true,
	"zap.format":             true,
	"zap.prefix":             true,
	"zap.director":           true,
	"zap.encode-level":       true,
	"zap.stacktrace-key":     true,
	"zap.max-file-size":      true,
	"zap.max-backups":        true,
	"zap.max-log-length":     true,
	"zap.retention-day":      true,
	"zap.show-line":          true,
	"zap.log-in-console":     true,
	"zap.max-string-length":  true,
	"zap.max-array-elements": true,
}

// isSystemLevelConfig 检查是否为系统级配置（启动必需，必须来自YAML）
func isSystemLevelConfig(key string) bool {
	return systemLevelConfigKeys[key]
}

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
	mu              sync.RWMutex
	db              *gorm.DB
	logger          *zap.Logger
	configCache     map[string]interface{}
	lastUpdate      time.Time
	validationRules map[string]ConfigValidationRule
	changeCallbacks []ConfigChangeCallback
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

var (
	configManager *ConfigManager
	once          sync.Once
)

// NewConfigManager 创建新的配置管理器
func NewConfigManager(db *gorm.DB, logger *zap.Logger) *ConfigManager {
	return &ConfigManager{
		db:              db,
		logger:          logger,
		configCache:     make(map[string]interface{}),
		validationRules: make(map[string]ConfigValidationRule),
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

	// 保存旧值用于回调
	oldValue := cm.configCache[key]

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

	// 检查是否包含系统级配置，禁止通过API修改
	for key := range flatConfig {
		if isSystemLevelConfig(key) {
			cm.mu.Unlock()
			return fmt.Errorf("禁止修改系统级配置: %s（该配置必须通过config.yaml修改并重启服务）", key)
		}
	}

	for key, value := range flatConfig {
		if err := cm.validateConfig(key, value); err != nil {
			cm.mu.Unlock()
			return fmt.Errorf("配置 %s 验证失败: %v", key, err)
		}
	}

	// 保存旧配置用于比较
	oldConfig := make(map[string]interface{})
	for key := range flatConfig {
		oldConfig[key] = cm.configCache[key]
	}

	// 先准备所有配置数据（事务外）
	oldValues := make(map[string]interface{})
	var configsToSave []SystemConfig
	for key, value := range flatConfig {
		oldValues[key] = cm.configCache[key]
		cm.configCache[key] = value

		// 准备配置数据
		config, err := cm.prepareConfigForDB(key, value)
		if err != nil {
			// 恢复配置
			for k, v := range oldValues {
				cm.configCache[k] = v
			}
			cm.mu.Unlock()
			return fmt.Errorf("准备配置 %s 失败: %v", key, err)
		}
		configsToSave = append(configsToSave, config)
	}

	// 使用短事务批量保存
	transactionErr := cm.db.Transaction(func(tx *gorm.DB) error {
		// 批量保存配置（使用真正的批量 UPSERT）
		if len(configsToSave) > 0 {
			if err := tx.Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value", "is_public", "updated_at"}),
			}).CreateInBatches(configsToSave, 50).Error; err != nil {
				return fmt.Errorf("批量保存配置失败: %v", err)
			}
		}
		return nil
	})

	if transactionErr != nil {
		// 恢复配置
		for k, v := range oldValues {
			cm.configCache[k] = v
		}
		cm.mu.Unlock()
		return fmt.Errorf("批量保存配置失败: %v", transactionErr)
	}

	// 创建配置修改标志文件
	if err := cm.markConfigAsModified(); err != nil {
		// 恢复配置
		for k, v := range oldValues {
			cm.configCache[k] = v
		}
		cm.logger.Error("创建配置修改标志文件失败", zap.Error(err))
		cm.mu.Unlock()
		return fmt.Errorf("创建配置修改标志文件失败: %v", err)
	}

	cm.lastUpdate = time.Now()

	// 释放锁，准备执行可能耗时的操作
	cm.mu.Unlock()

	// 同步配置到全局配置 - 使用连接符格式的配置
	// 这里在锁外执行，避免持锁时间过长
	if err := cm.syncToGlobalConfig(kebabConfig); err != nil {
		cm.logger.Error("同步配置到全局配置失败", zap.Error(err))
	}

	// 触发回调 - 使用连接符格式的配置
	// 这里在锁外执行，避免回调函数执行时间过长阻塞其他读取操作
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
					// 补全缺失配置
					if err := cm.EnsureDefaultConfigs(); err != nil {
						cm.logger.Warn("补全缺失配置项失败", zap.Error(err))
					}
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

	// 在配置加载完成后，检查并补全缺失的配置项
	if err := cm.EnsureDefaultConfigs(); err != nil {
		cm.logger.Warn("补全缺失配置项失败", zap.Error(err))
	}
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

// RegisterChangeCallback 注册配置变更回调
func (cm *ConfigManager) RegisterChangeCallback(callback ConfigChangeCallback) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.changeCallbacks = append(cm.changeCallbacks, callback)
}

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
		"upload": map[string]interface{}{
			"max-avatar-size": 5242880, // 5MB in bytes
		},
		"other": map[string]interface{}{
			"max-avatar-size":  5.0,
			"default-language": "zh",
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
