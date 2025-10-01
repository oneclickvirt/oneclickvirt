package initialize

import (
	"oneclickvirt/config"
	"oneclickvirt/global"

	"go.uber.org/zap"
)

// InitializeConfigManager 初始化配置管理器
func InitializeConfigManager() {
	config.InitializeConfigManager(global.APP_DB, global.APP_LOG)

	// 注册配置同步回调
	configManager := config.GetConfigManager()
	if configManager != nil {
		configManager.RegisterChangeCallback(syncConfigToGlobal)
	}
}

// ReInitializeConfigManager 重新初始化配置管理器（用于系统初始化完成后）
func ReInitializeConfigManager() {
	if global.APP_DB == nil || global.APP_LOG == nil {
		global.APP_LOG.Error("重新初始化配置管理器失败: 全局数据库或日志记录器未初始化")
		return
	}

	// 重新初始化配置管理器
	config.ReInitializeConfigManager(global.APP_DB, global.APP_LOG)

	// 注册配置同步回调
	configManager := config.GetConfigManager()
	if configManager != nil {
		configManager.RegisterChangeCallback(syncConfigToGlobal)
		global.APP_LOG.Info("配置管理器重新初始化完成并注册回调")
	} else {
		global.APP_LOG.Error("配置管理器重新初始化后仍为空")
	}
}

// syncConfigToGlobal 同步配置到全局变量
func syncConfigToGlobal(key string, oldValue, newValue interface{}) error {
	switch key {
	case "auth":
		if authConfig, ok := newValue.(map[string]interface{}); ok {
			syncAuthConfig(authConfig)
		}
	case "inviteCode":
		if inviteConfig, ok := newValue.(map[string]interface{}); ok {
			syncInviteCodeConfig(inviteConfig)
		}
	case "quota":
		if quotaConfig, ok := newValue.(map[string]interface{}); ok {
			syncQuotaConfig(quotaConfig)
		}
	}
	return nil
}

// syncAuthConfig 同步认证配置
func syncAuthConfig(authConfig map[string]interface{}) {
	if enablePublicRegistration, ok := authConfig["enablePublicRegistration"].(bool); ok {
		global.APP_CONFIG.Auth.EnablePublicRegistration = enablePublicRegistration
	}
	if enableEmail, ok := authConfig["enableEmail"].(bool); ok {
		global.APP_CONFIG.Auth.EnableEmail = enableEmail
	}
	if enableTelegram, ok := authConfig["enableTelegram"].(bool); ok {
		global.APP_CONFIG.Auth.EnableTelegram = enableTelegram
	}
	if enableQQ, ok := authConfig["enableQQ"].(bool); ok {
		global.APP_CONFIG.Auth.EnableQQ = enableQQ
	}
}

// syncInviteCodeConfig 同步邀请码配置
func syncInviteCodeConfig(inviteConfig map[string]interface{}) {
	if enabled, ok := inviteConfig["enabled"].(bool); ok {
		global.APP_CONFIG.InviteCode.Enabled = enabled
	}
	if required, ok := inviteConfig["required"].(bool); ok {
		global.APP_CONFIG.InviteCode.Required = required
	}
}

// syncQuotaConfig 同步配额配置
func syncQuotaConfig(quotaConfig map[string]interface{}) {
	if defaultLevel, ok := quotaConfig["defaultLevel"].(float64); ok {
		global.APP_CONFIG.Quota.DefaultLevel = int(defaultLevel)
	} else if defaultLevel, ok := quotaConfig["defaultLevel"].(int); ok {
		global.APP_CONFIG.Quota.DefaultLevel = defaultLevel
	}

	// 同步等级限制配置
	if levelLimits, ok := quotaConfig["levelLimits"].(map[string]interface{}); ok {
		if global.APP_CONFIG.Quota.LevelLimits == nil {
			global.APP_CONFIG.Quota.LevelLimits = make(map[int]config.LevelLimitInfo)
		}

		for levelStr, limitData := range levelLimits {
			if limitMap, ok := limitData.(map[string]interface{}); ok {
				// 将字符串转换为整数等级
				level := 1 // 默认等级
				switch levelStr {
				case "1":
					level = 1
				case "2":
					level = 2
				case "3":
					level = 3
				case "4":
					level = 4
				case "5":
					level = 5
				}

				// 创建新的等级限制配置
				levelLimit := config.LevelLimitInfo{}

				// 更新最大实例数
				if maxInstances, exists := limitMap["maxInstances"]; exists {
					if instances, ok := maxInstances.(float64); ok {
						levelLimit.MaxInstances = int(instances)
					} else if instances, ok := maxInstances.(int); ok {
						levelLimit.MaxInstances = instances
					}
				}

				// 更新最大资源
				if maxResources, exists := limitMap["maxResources"]; exists {
					if resourcesMap, ok := maxResources.(map[string]interface{}); ok {
						levelLimit.MaxResources = resourcesMap
					}
				}

				// 更新最大流量限制
				if maxTraffic, exists := limitMap["maxTraffic"]; exists {
					if traffic, ok := maxTraffic.(float64); ok {
						levelLimit.MaxTraffic = int64(traffic)
					} else if traffic, ok := maxTraffic.(int64); ok {
						levelLimit.MaxTraffic = traffic
					} else if traffic, ok := maxTraffic.(int); ok {
						levelLimit.MaxTraffic = int64(traffic)
					}
				}

				global.APP_CONFIG.Quota.LevelLimits[level] = levelLimit
			}
		}

		global.APP_LOG.Info("配额等级限制已同步到全局配置",
			zap.Int("levelCount", len(global.APP_CONFIG.Quota.LevelLimits)))
	}

	// 同步实例类型权限配置
	if instanceTypePermissions, ok := quotaConfig["instanceTypePermissions"].(map[string]interface{}); ok {
		if minLevelForContainer, exists := instanceTypePermissions["minLevelForContainer"]; exists {
			if level, ok := minLevelForContainer.(float64); ok {
				global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = int(level)
			} else if level, ok := minLevelForContainer.(int); ok {
				global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = level
			}
		}

		if minLevelForVM, exists := instanceTypePermissions["minLevelForVM"]; exists {
			if level, ok := minLevelForVM.(float64); ok {
				global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = int(level)
			} else if level, ok := minLevelForVM.(int); ok {
				global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = level
			}
		}

		if minLevelForDelete, exists := instanceTypePermissions["minLevelForDelete"]; exists {
			if level, ok := minLevelForDelete.(float64); ok {
				global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDelete = int(level)
			} else if level, ok := minLevelForDelete.(int); ok {
				global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDelete = level
			}
		}

		global.APP_LOG.Info("实例类型权限配置已同步到全局配置",
			zap.Int("minLevelForContainer", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer),
			zap.Int("minLevelForVM", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM),
			zap.Int("minLevelForDelete", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDelete))
	}
}
