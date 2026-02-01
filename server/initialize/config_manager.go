package initialize

import (
	"fmt"
	"oneclickvirt/config"
	"oneclickvirt/global"

	"go.uber.org/zap"
)

// InitializeConfigManager 初始化配置管理器
func InitializeConfigManager() {
	// 先注册回调，再初始化配置管理器
	// 这样在 loadConfigFromDB 时就能触发回调同步到 global.APP_CONFIG
	configManager := config.GetConfigManager()
	if configManager == nil {
		// 如果配置管理器还未创建，先创建一个临时的来注册回调
		config.PreInitializeConfigManager(global.APP_DB, global.APP_LOG, syncConfigToGlobal)
	} else {
		configManager.RegisterChangeCallback(syncConfigToGlobal)
	}

	// 正式初始化配置管理器（会调用 loadConfigFromDB）
	config.InitializeConfigManager(global.APP_DB, global.APP_LOG)
}

// ReInitializeConfigManager 重新初始化配置管理器（用于系统初始化完成后）
func ReInitializeConfigManager() {
	if global.APP_DB == nil || global.APP_LOG == nil {
		global.APP_LOG.Error("重新初始化配置管理器失败: 全局数据库或日志记录器未初始化")
		return
	}

	// 先注册回调，再重新初始化配置管理器
	config.PreInitializeConfigManager(global.APP_DB, global.APP_LOG, syncConfigToGlobal)

	// 重新初始化配置管理器（会重新加载数据库配置）
	config.ReInitializeConfigManager(global.APP_DB, global.APP_LOG)

	global.APP_LOG.Info("配置管理器重新初始化完成")
}

// syncConfigToGlobal 同步配置到全局变量
func syncConfigToGlobal(key string, oldValue, newValue interface{}) error {
	switch key {
	case "auth":
		if authConfig, ok := newValue.(map[string]interface{}); ok {
			syncAuthConfig(authConfig)
		}
	case "invite-code":
		if inviteConfig, ok := newValue.(map[string]interface{}); ok {
			syncInviteCodeConfig(inviteConfig)
		}
	case "quota":
		if quotaConfig, ok := newValue.(map[string]interface{}); ok {
			syncQuotaConfig(quotaConfig)
		}
	case "system":
		if systemConfig, ok := newValue.(map[string]interface{}); ok {
			syncSystemConfig(systemConfig)
		}
	case "jwt":
		if jwtConfig, ok := newValue.(map[string]interface{}); ok {
			syncJWTConfig(jwtConfig)
		}
	case "cors":
		if corsConfig, ok := newValue.(map[string]interface{}); ok {
			syncCORSConfig(corsConfig)
		}
	case "captcha":
		if captchaConfig, ok := newValue.(map[string]interface{}); ok {
			syncCaptchaConfig(captchaConfig)
		}
	case "other":
		if otherConfig, ok := newValue.(map[string]interface{}); ok {
			syncOtherConfig(otherConfig)
		}
	}
	return nil
}

// syncAuthConfig 同步认证配置 - 只支持 kebab-case 格式
func syncAuthConfig(authConfig map[string]interface{}) {
	if v, ok := authConfig["enable-public-registration"].(bool); ok {
		global.APP_CONFIG.Auth.EnablePublicRegistration = v
	}
	if v, ok := authConfig["enable-email"].(bool); ok {
		global.APP_CONFIG.Auth.EnableEmail = v
	}
	if v, ok := authConfig["enable-telegram"].(bool); ok {
		global.APP_CONFIG.Auth.EnableTelegram = v
	}
	if v, ok := authConfig["enable-qq"].(bool); ok {
		global.APP_CONFIG.Auth.EnableQQ = v
	}
	if v, ok := authConfig["enable-oauth2"].(bool); ok {
		global.APP_CONFIG.Auth.EnableOAuth2 = v
	}
}

// syncInviteCodeConfig 同步邀请码配置
func syncInviteCodeConfig(inviteConfig map[string]interface{}) {
	if enabled, ok := inviteConfig["enabled"].(bool); ok {
		global.APP_CONFIG.InviteCode.Enabled = enabled
		global.APP_LOG.Info("同步邀请码启用状态", zap.Bool("enabled", enabled))
	} else {
		global.APP_LOG.Warn("邀请码配置中的enabled字段类型错误或不存在",
			zap.Any("value", inviteConfig["enabled"]),
			zap.String("type", fmt.Sprintf("%T", inviteConfig["enabled"])))
	}
	if required, ok := inviteConfig["required"].(bool); ok {
		global.APP_CONFIG.InviteCode.Required = required
		global.APP_LOG.Info("同步邀请码必需状态", zap.Bool("required", required))
	} else {
		global.APP_LOG.Warn("邀请码配置中的required字段类型错误或不存在",
			zap.Any("value", inviteConfig["required"]),
			zap.String("type", fmt.Sprintf("%T", inviteConfig["required"])))
	}
}

// syncQuotaConfig 同步配额配置 - 只支持 kebab-case 格式
func syncQuotaConfig(quotaConfig map[string]interface{}) {
	// 同步默认等级
	if v, ok := quotaConfig["default-level"].(float64); ok {
		global.APP_CONFIG.Quota.DefaultLevel = int(v)
	} else if v, ok := quotaConfig["default-level"].(int); ok {
		global.APP_CONFIG.Quota.DefaultLevel = v
	}

	// 同步等级限制配置
	if levelLimits, ok := quotaConfig["level-limits"].(map[string]interface{}); ok {
		if global.APP_CONFIG.Quota.LevelLimits == nil {
			global.APP_CONFIG.Quota.LevelLimits = make(map[int]config.LevelLimitInfo)
		}

		for levelStr, limitData := range levelLimits {
			if limitMap, ok := limitData.(map[string]interface{}); ok {
				var level int
				fmt.Sscanf(levelStr, "%d", &level)
				if level < 1 || level > 5 {
					continue
				}

				levelLimit := config.LevelLimitInfo{}

				if v, ok := limitMap["max-instances"].(float64); ok {
					levelLimit.MaxInstances = int(v)
				} else if v, ok := limitMap["max-instances"].(int); ok {
					levelLimit.MaxInstances = v
				}

				if v, ok := limitMap["max-resources"].(map[string]interface{}); ok {
					levelLimit.MaxResources = v
				}

				if v, ok := limitMap["max-traffic"].(float64); ok {
					levelLimit.MaxTraffic = int64(v)
				} else if v, ok := limitMap["max-traffic"].(int64); ok {
					levelLimit.MaxTraffic = v
				} else if v, ok := limitMap["max-traffic"].(int); ok {
					levelLimit.MaxTraffic = int64(v)
				}

				global.APP_CONFIG.Quota.LevelLimits[level] = levelLimit
			}
		}
	}

	// 同步实例类型权限配置
	if permissions, ok := quotaConfig["instance-type-permissions"].(map[string]interface{}); ok {
		if v, ok := permissions["min-level-for-container"].(float64); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = int(v)
		} else if v, ok := permissions["min-level-for-container"].(int); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = v
		}

		if v, ok := permissions["min-level-for-vm"].(float64); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = int(v)
		} else if v, ok := permissions["min-level-for-vm"].(int); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = v
		}

		if v, ok := permissions["min-level-for-delete-container"].(float64); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = int(v)
		} else if v, ok := permissions["min-level-for-delete-container"].(int); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = v
		}

		if v, ok := permissions["min-level-for-delete-vm"].(float64); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = int(v)
		} else if v, ok := permissions["min-level-for-delete-vm"].(int); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = v
		}

		if v, ok := permissions["min-level-for-reset-container"].(float64); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = int(v)
		} else if v, ok := permissions["min-level-for-reset-container"].(int); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = v
		}

		if v, ok := permissions["min-level-for-reset-vm"].(float64); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM = int(v)
		} else if v, ok := permissions["min-level-for-reset-vm"].(int); ok {
			global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM = v
		}
	}
}

// syncSystemConfig 同步系统配置
func syncSystemConfig(systemConfig map[string]interface{}) {
	if v, ok := systemConfig["env"].(string); ok {
		global.APP_CONFIG.System.Env = v
	}
	if v, ok := systemConfig["addr"].(float64); ok {
		global.APP_CONFIG.System.Addr = int(v)
	} else if v, ok := systemConfig["addr"].(int); ok {
		global.APP_CONFIG.System.Addr = v
	}
	if v, ok := systemConfig["db-type"].(string); ok {
		global.APP_CONFIG.System.DbType = v
	}
	if v, ok := systemConfig["oss-type"].(string); ok {
		global.APP_CONFIG.System.OssType = v
	}
	if v, ok := systemConfig["use-multipoint"].(bool); ok {
		global.APP_CONFIG.System.UseMultipoint = v
	}
	if v, ok := systemConfig["use-redis"].(bool); ok {
		global.APP_CONFIG.System.UseRedis = v
	}
	if v, ok := systemConfig["iplimit-count"].(float64); ok {
		global.APP_CONFIG.System.LimitCountIP = int(v)
	} else if v, ok := systemConfig["iplimit-count"].(int); ok {
		global.APP_CONFIG.System.LimitCountIP = v
	}
	if v, ok := systemConfig["iplimit-time"].(float64); ok {
		global.APP_CONFIG.System.LimitTimeIP = int(v)
	} else if v, ok := systemConfig["iplimit-time"].(int); ok {
		global.APP_CONFIG.System.LimitTimeIP = v
	}
	if v, ok := systemConfig["frontend-url"].(string); ok {
		global.APP_CONFIG.System.FrontendURL = v
	}
}

// syncJWTConfig 同步JWT配置
func syncJWTConfig(jwtConfig map[string]interface{}) {
	if v, ok := jwtConfig["signing-key"].(string); ok {
		global.APP_CONFIG.JWT.SigningKey = v
	}
	if v, ok := jwtConfig["expires-time"].(string); ok {
		global.APP_CONFIG.JWT.ExpiresTime = v
	}
	if v, ok := jwtConfig["buffer-time"].(string); ok {
		global.APP_CONFIG.JWT.BufferTime = v
	}
	if v, ok := jwtConfig["issuer"].(string); ok {
		global.APP_CONFIG.JWT.Issuer = v
	}
}

// syncCORSConfig 同步CORS配置
func syncCORSConfig(corsConfig map[string]interface{}) {
	if v, ok := corsConfig["mode"].(string); ok {
		global.APP_CONFIG.Cors.Mode = v
	}
	if whitelist, ok := corsConfig["whitelist"].([]interface{}); ok {
		strList := make([]string, 0, len(whitelist))
		for _, v := range whitelist {
			if str, ok := v.(string); ok {
				strList = append(strList, str)
			}
		}
		global.APP_CONFIG.Cors.Whitelist = strList
	}
}

// syncCaptchaConfig 同步验证码配置
func syncCaptchaConfig(captchaConfig map[string]interface{}) {
	if v, ok := captchaConfig["enabled"].(bool); ok {
		global.APP_CONFIG.Captcha.Enabled = v
	}
	if v, ok := captchaConfig["width"].(float64); ok {
		global.APP_CONFIG.Captcha.Width = int(v)
	} else if v, ok := captchaConfig["width"].(int); ok {
		global.APP_CONFIG.Captcha.Width = v
	}
	if v, ok := captchaConfig["height"].(float64); ok {
		global.APP_CONFIG.Captcha.Height = int(v)
	} else if v, ok := captchaConfig["height"].(int); ok {
		global.APP_CONFIG.Captcha.Height = v
	}
	if v, ok := captchaConfig["length"].(float64); ok {
		global.APP_CONFIG.Captcha.Length = int(v)
	} else if v, ok := captchaConfig["length"].(int); ok {
		global.APP_CONFIG.Captcha.Length = v
	}
	if v, ok := captchaConfig["expire-time"].(float64); ok {
		global.APP_CONFIG.Captcha.ExpireTime = int(v)
	} else if v, ok := captchaConfig["expire-time"].(int); ok {
		global.APP_CONFIG.Captcha.ExpireTime = v
	}
}

// syncOtherConfig 同步其他配置
func syncOtherConfig(otherConfig map[string]interface{}) {
	if v, ok := otherConfig["default-language"].(string); ok {
		global.APP_CONFIG.Other.DefaultLanguage = v
	}
}
