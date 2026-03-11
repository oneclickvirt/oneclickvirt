package initialize

import (
	"fmt"
	"oneclickvirt/config"
	"oneclickvirt/global"
	"sync"

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

	// 标记 ConfigManager 已从数据库完成初始化。
	// 此后 viper 文件监听器（OnConfigChange）不再覆盖 global.APP_CONFIG，
	// 防止启动阶段写入 YAML 触发的延迟 fsnotify 事件把旧快照写回内存。
	global.CONFIG_MANAGER_READY.Store(true)
}

// ReInitializeConfigManager 重新初始化配置管理器（用于系统初始化完成后）
func ReInitializeConfigManager() {
	if global.APP_DB == nil || global.APP_LOG == nil {
		global.APP_LOG.Error("重新初始化配置管理器失败: 全局数据库或日志记录器未初始化")
		return
	}

	// 不重新注册回调（InitializeConfigManager 已注册过）
	config.ReInitializeConfigManager(global.APP_DB, global.APP_LOG)

	// 确保标记已设置（ReInitialize 可能在系统初始化完成后调用）
	global.CONFIG_MANAGER_READY.Store(true)

	global.APP_LOG.Info("配置管理器重新初始化完成")
}

// appConfigWriteMu 序列化 APP_CONFIG 的 copy-on-write 写操作，读侧无需加锁
var appConfigWriteMu sync.Mutex

// syncConfigToGlobal 同步配置到全局变量（copy-on-write，读侧完全无锁）
func syncConfigToGlobal(key string, oldValue, newValue interface{}) error {
	appConfigWriteMu.Lock()
	defer appConfigWriteMu.Unlock()

	cfg := global.GetAppConfig() // 取当前快照副本

	switch key {
	case "auth":
		if authConfig, ok := newValue.(map[string]interface{}); ok {
			syncAuthConfig(&cfg, authConfig)
		}
	case "invite-code":
		if inviteConfig, ok := newValue.(map[string]interface{}); ok {
			syncInviteCodeConfig(&cfg, inviteConfig)
		}
	case "quota":
		if quotaConfig, ok := newValue.(map[string]interface{}); ok {
			syncQuotaConfig(&cfg, quotaConfig)
		}
	case "system":
		if systemConfig, ok := newValue.(map[string]interface{}); ok {
			syncSystemConfig(&cfg, systemConfig)
		}
	case "jwt":
		if jwtConfig, ok := newValue.(map[string]interface{}); ok {
			syncJWTConfig(&cfg, jwtConfig)
		}
	case "cors":
		if corsConfig, ok := newValue.(map[string]interface{}); ok {
			syncCORSConfig(&cfg, corsConfig)
		}
	case "captcha":
		if captchaConfig, ok := newValue.(map[string]interface{}); ok {
			syncCaptchaConfig(&cfg, captchaConfig)
		}
	case "other":
		if otherConfig, ok := newValue.(map[string]interface{}); ok {
			syncOtherConfig(&cfg, otherConfig)
		}
	}

	global.SetAppConfig(cfg) // 原子写入，读侧立即可见
	return nil
}

// syncAuthConfig 同步认证配置到配置副本
func syncAuthConfig(cfg *config.Server, authConfig map[string]interface{}) {
	if v, ok := authConfig["enable-public-registration"].(bool); ok {
		cfg.Auth.EnablePublicRegistration = v
	}
	if v, ok := authConfig["enable-email"].(bool); ok {
		cfg.Auth.EnableEmail = v
	}
	if v, ok := authConfig["enable-telegram"].(bool); ok {
		cfg.Auth.EnableTelegram = v
	}
	if v, ok := authConfig["enable-qq"].(bool); ok {
		cfg.Auth.EnableQQ = v
	}
	if v, ok := authConfig["enable-oauth2"].(bool); ok {
		cfg.Auth.EnableOAuth2 = v
	}
	if v, ok := authConfig["email-smtp-host"].(string); ok {
		cfg.Auth.EmailSMTPHost = v
	}
	if v, ok := authConfig["email-smtp-port"].(float64); ok {
		cfg.Auth.EmailSMTPPort = int(v)
	} else if v, ok := authConfig["email-smtp-port"].(int); ok {
		cfg.Auth.EmailSMTPPort = v
	}
	if v, ok := authConfig["email-username"].(string); ok {
		cfg.Auth.EmailUsername = v
	}
	if v, ok := authConfig["email-password"].(string); ok {
		cfg.Auth.EmailPassword = v
	}
	if v, ok := authConfig["telegram-bot-token"].(string); ok {
		cfg.Auth.TelegramBotToken = v
	}
	if v, ok := authConfig["qq-app-id"].(string); ok {
		cfg.Auth.QQAppID = v
	}
	if v, ok := authConfig["qq-app-key"].(string); ok {
		cfg.Auth.QQAppKey = v
	}
}

// syncInviteCodeConfig 同步邀请码配置到配置副本
func syncInviteCodeConfig(cfg *config.Server, inviteConfig map[string]interface{}) {
	if enabled, ok := inviteConfig["enabled"].(bool); ok {
		cfg.InviteCode.Enabled = enabled
		global.APP_LOG.Debug("同步邀请码启用状态", zap.Bool("enabled", enabled))
	} else {
		global.APP_LOG.Warn("邀请码配置中的enabled字段类型错误或不存在",
			zap.Any("value", inviteConfig["enabled"]),
			zap.String("type", fmt.Sprintf("%T", inviteConfig["enabled"])))
	}
	if required, ok := inviteConfig["required"].(bool); ok {
		cfg.InviteCode.Required = required
		global.APP_LOG.Debug("同步邀请码必需状态", zap.Bool("required", required))
	} else {
		global.APP_LOG.Warn("邀请码配置中的required字段类型错误或不存在",
			zap.Any("value", inviteConfig["required"]),
			zap.String("type", fmt.Sprintf("%T", inviteConfig["required"])))
	}
}

// syncQuotaConfig 同步配额配置到配置副本。
// LevelLimits 使用全新 map，保证 copy-on-write 安全（旧读者持有旧 map 引用，新旧 map 互不影响）。
func syncQuotaConfig(cfg *config.Server, quotaConfig map[string]interface{}) {
	if v, ok := quotaConfig["default-level"].(float64); ok {
		cfg.Quota.DefaultLevel = int(v)
	} else if v, ok := quotaConfig["default-level"].(int); ok {
		cfg.Quota.DefaultLevel = v
	}

	if levelLimits, ok := quotaConfig["level-limits"].(map[string]interface{}); ok {
		// 创建全新 map，不修改旧 map（copy-on-write 安全）
		newMap := make(map[int]config.LevelLimitInfo, len(cfg.Quota.LevelLimits)+len(levelLimits))
		for k, v := range cfg.Quota.LevelLimits {
			newMap[k] = v
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
				if v, ok := limitMap["expiry-days"].(float64); ok {
					levelLimit.ExpiryDays = int(v)
				} else if v, ok := limitMap["expiry-days"].(int); ok {
					levelLimit.ExpiryDays = v
				}
				newMap[level] = levelLimit
			}
		}
		cfg.Quota.LevelLimits = newMap
	}

	if permissions, ok := quotaConfig["instance-type-permissions"].(map[string]interface{}); ok {
		if v, ok := permissions["min-level-for-container"].(float64); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForContainer = int(v)
		} else if v, ok := permissions["min-level-for-container"].(int); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForContainer = v
		}
		if v, ok := permissions["min-level-for-vm"].(float64); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForVM = int(v)
		} else if v, ok := permissions["min-level-for-vm"].(int); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForVM = v
		}
		if v, ok := permissions["min-level-for-delete-container"].(float64); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = int(v)
		} else if v, ok := permissions["min-level-for-delete-container"].(int); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = v
		}
		if v, ok := permissions["min-level-for-delete-vm"].(float64); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForDeleteVM = int(v)
		} else if v, ok := permissions["min-level-for-delete-vm"].(int); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForDeleteVM = v
		}
		if v, ok := permissions["min-level-for-reset-container"].(float64); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForResetContainer = int(v)
		} else if v, ok := permissions["min-level-for-reset-container"].(int); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForResetContainer = v
		}
		if v, ok := permissions["min-level-for-reset-vm"].(float64); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForResetVM = int(v)
		} else if v, ok := permissions["min-level-for-reset-vm"].(int); ok {
			cfg.Quota.InstanceTypePermissions.MinLevelForResetVM = v
		}
	}
}

// syncSystemConfig 同步系统配置到配置副本
func syncSystemConfig(cfg *config.Server, systemConfig map[string]interface{}) {
	if v, ok := systemConfig["env"].(string); ok {
		cfg.System.Env = v
	}
	if v, ok := systemConfig["addr"].(float64); ok {
		cfg.System.Addr = int(v)
	} else if v, ok := systemConfig["addr"].(int); ok {
		cfg.System.Addr = v
	}
	if v, ok := systemConfig["db-type"].(string); ok {
		cfg.System.DbType = v
	}
	if v, ok := systemConfig["oss-type"].(string); ok {
		cfg.System.OssType = v
	}
	if v, ok := systemConfig["use-multipoint"].(bool); ok {
		cfg.System.UseMultipoint = v
	}
	if v, ok := systemConfig["use-redis"].(bool); ok {
		cfg.System.UseRedis = v
	}
	if v, ok := systemConfig["iplimit-count"].(float64); ok {
		cfg.System.LimitCountIP = int(v)
	} else if v, ok := systemConfig["iplimit-count"].(int); ok {
		cfg.System.LimitCountIP = v
	}
	if v, ok := systemConfig["iplimit-time"].(float64); ok {
		cfg.System.LimitTimeIP = int(v)
	} else if v, ok := systemConfig["iplimit-time"].(int); ok {
		cfg.System.LimitTimeIP = v
	}
	if v, ok := systemConfig["frontend-url"].(string); ok {
		cfg.System.FrontendURL = v
	}
}

// syncJWTConfig 同步JWT配置到配置副本（signing-key 不在此同步，由 JWTSecretService 管理）
func syncJWTConfig(cfg *config.Server, jwtConfig map[string]interface{}) {
	if v, ok := jwtConfig["expires-time"].(string); ok {
		cfg.JWT.ExpiresTime = v
	}
	if v, ok := jwtConfig["buffer-time"].(string); ok {
		cfg.JWT.BufferTime = v
	}
	if v, ok := jwtConfig["issuer"].(string); ok {
		cfg.JWT.Issuer = v
	}
}

// syncCORSConfig 同步CORS配置到配置副本
func syncCORSConfig(cfg *config.Server, corsConfig map[string]interface{}) {
	if v, ok := corsConfig["mode"].(string); ok {
		cfg.Cors.Mode = v
	}
	if whitelist, ok := corsConfig["whitelist"].([]interface{}); ok {
		strList := make([]string, 0, len(whitelist))
		for _, v := range whitelist {
			if str, ok := v.(string); ok {
				strList = append(strList, str)
			}
		}
		cfg.Cors.Whitelist = strList
	}
}

// syncCaptchaConfig 同步验证码配置到配置副本
func syncCaptchaConfig(cfg *config.Server, captchaConfig map[string]interface{}) {
	if v, ok := captchaConfig["enabled"].(bool); ok {
		cfg.Captcha.Enabled = v
	}
	if v, ok := captchaConfig["width"].(float64); ok {
		cfg.Captcha.Width = int(v)
	} else if v, ok := captchaConfig["width"].(int); ok {
		cfg.Captcha.Width = v
	}
	if v, ok := captchaConfig["height"].(float64); ok {
		cfg.Captcha.Height = int(v)
	} else if v, ok := captchaConfig["height"].(int); ok {
		cfg.Captcha.Height = v
	}
	if v, ok := captchaConfig["length"].(float64); ok {
		cfg.Captcha.Length = int(v)
	} else if v, ok := captchaConfig["length"].(int); ok {
		cfg.Captcha.Length = v
	}
	if v, ok := captchaConfig["expire-time"].(float64); ok {
		cfg.Captcha.ExpireTime = int(v)
	} else if v, ok := captchaConfig["expire-time"].(int); ok {
		cfg.Captcha.ExpireTime = v
	}
}

// syncOtherConfig 同步其他配置到配置副本
func syncOtherConfig(cfg *config.Server, otherConfig map[string]interface{}) {
	if v, ok := otherConfig["default-language"].(string); ok {
		cfg.Other.DefaultLanguage = v
	}
	if v, ok := otherConfig["logo-url"].(string); ok {
		cfg.Other.LogoURL = v
	}
	if v, ok := otherConfig["site-name"].(string); ok {
		cfg.Other.SiteName = v
	}
}
