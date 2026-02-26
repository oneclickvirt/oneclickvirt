package config

import (
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
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

// RegisterChangeCallback 注册配置变更回调
func (cm *ConfigManager) RegisterChangeCallback(callback ConfigChangeCallback) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.changeCallbacks = append(cm.changeCallbacks, callback)
}
