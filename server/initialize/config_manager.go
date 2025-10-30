package initialize

import (
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

	// 再次确保回调已注册
	configManager = config.GetConfigManager()
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

	// 先注册回调，再重新初始化配置管理器
	config.PreInitializeConfigManager(global.APP_DB, global.APP_LOG, syncConfigToGlobal)

	// 重新初始化配置管理器（会重新加载数据库配置）
	config.ReInitializeConfigManager(global.APP_DB, global.APP_LOG)

	// 注册配置同步回调
	configManager := config.GetConfigManager()
	if configManager != nil {
		configManager.RegisterChangeCallback(syncConfigToGlobal)
		global.APP_LOG.Info("配置管理器重新初始化完成并注册回调")

		// 立即同步一次配置确保 global.APP_CONFIG 是最新的
		allConfig := configManager.GetAllConfig()
		if len(allConfig) > 0 {
			// 将扁平配置转换为嵌套结构
			nestedConfig := make(map[string]interface{})
			for key, value := range allConfig {
				setNestedValue(nestedConfig, key, value)
			}
			// 同步到 global.APP_CONFIG
			for key, value := range nestedConfig {
				syncConfigToGlobal(key, nil, value)
			}
			global.APP_LOG.Info("配置已同步到全局变量", zap.Int("configCount", len(nestedConfig)))
		}
	} else {
		global.APP_LOG.Error("配置管理器重新初始化后仍为空")
	}
}

// setNestedValue 递归设置嵌套配置值（辅助函数）
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

// splitKey 分割点分隔的key
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

// syncConfigToGlobal 同步配置到全局变量
func syncConfigToGlobal(key string, oldValue, newValue interface{}) error {
	switch key {
	case "auth":
		if authConfig, ok := newValue.(map[string]interface{}); ok {
			syncAuthConfig(authConfig)
		}
	case "inviteCode", "invite-code":
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
	case "upload":
		if uploadConfig, ok := newValue.(map[string]interface{}); ok {
			syncUploadConfig(uploadConfig)
		}
	case "other":
		if otherConfig, ok := newValue.(map[string]interface{}); ok {
			syncOtherConfig(otherConfig)
		}
	}
	return nil
}

// syncAuthConfig 同步认证配置
func syncAuthConfig(authConfig map[string]interface{}) {
	global.APP_LOG.Info("同步认证配置到全局变量", zap.Any("authConfig", authConfig))

	// 支持驼峰和kebab-case两种格式
	if enablePublicRegistration, ok := authConfig["enablePublicRegistration"].(bool); ok {
		global.APP_CONFIG.Auth.EnablePublicRegistration = enablePublicRegistration
		global.APP_LOG.Info("同步enablePublicRegistration", zap.Bool("value", enablePublicRegistration))
	} else if enablePublicRegistration, ok := authConfig["enable-public-registration"].(bool); ok {
		global.APP_CONFIG.Auth.EnablePublicRegistration = enablePublicRegistration
		global.APP_LOG.Info("同步enable-public-registration", zap.Bool("value", enablePublicRegistration))
	}

	if enableEmail, ok := authConfig["enableEmail"].(bool); ok {
		global.APP_CONFIG.Auth.EnableEmail = enableEmail
		global.APP_LOG.Info("同步enableEmail", zap.Bool("value", enableEmail))
	} else if enableEmail, ok := authConfig["enable-email"].(bool); ok {
		global.APP_CONFIG.Auth.EnableEmail = enableEmail
		global.APP_LOG.Info("同步enable-email", zap.Bool("value", enableEmail))
	}

	if enableTelegram, ok := authConfig["enableTelegram"].(bool); ok {
		global.APP_CONFIG.Auth.EnableTelegram = enableTelegram
		global.APP_LOG.Info("同步enableTelegram", zap.Bool("value", enableTelegram))
	} else if enableTelegram, ok := authConfig["enable-telegram"].(bool); ok {
		global.APP_CONFIG.Auth.EnableTelegram = enableTelegram
		global.APP_LOG.Info("同步enable-telegram", zap.Bool("value", enableTelegram))
	}

	if enableQQ, ok := authConfig["enableQQ"].(bool); ok {
		global.APP_CONFIG.Auth.EnableQQ = enableQQ
		global.APP_LOG.Info("同步enableQQ", zap.Bool("value", enableQQ))
	} else if enableQQ, ok := authConfig["enable-qq"].(bool); ok {
		global.APP_CONFIG.Auth.EnableQQ = enableQQ
		global.APP_LOG.Info("同步enable-qq", zap.Bool("value", enableQQ))
	}

	if enableOAuth2, ok := authConfig["enableOAuth2"].(bool); ok {
		global.APP_CONFIG.Auth.EnableOAuth2 = enableOAuth2
		global.APP_LOG.Info("同步enableOAuth2", zap.Bool("value", enableOAuth2))
	} else if enableOAuth2, ok := authConfig["enable-oauth2"].(bool); ok {
		global.APP_CONFIG.Auth.EnableOAuth2 = enableOAuth2
		global.APP_LOG.Info("同步enable-oauth2", zap.Bool("value", enableOAuth2))
	}

	global.APP_LOG.Info("认证配置同步完成",
		zap.Bool("EnableOAuth2", global.APP_CONFIG.Auth.EnableOAuth2),
		zap.Bool("EnableEmail", global.APP_CONFIG.Auth.EnableEmail),
		zap.Bool("EnableTelegram", global.APP_CONFIG.Auth.EnableTelegram),
		zap.Bool("EnableQQ", global.APP_CONFIG.Auth.EnableQQ))
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
	// 支持驼峰和kebab-case两种格式
	if defaultLevel, ok := quotaConfig["defaultLevel"].(float64); ok {
		global.APP_CONFIG.Quota.DefaultLevel = int(defaultLevel)
	} else if defaultLevel, ok := quotaConfig["default-level"].(float64); ok {
		global.APP_CONFIG.Quota.DefaultLevel = int(defaultLevel)
	} else if defaultLevel, ok := quotaConfig["defaultLevel"].(int); ok {
		global.APP_CONFIG.Quota.DefaultLevel = defaultLevel
	} else if defaultLevel, ok := quotaConfig["default-level"].(int); ok {
		global.APP_CONFIG.Quota.DefaultLevel = defaultLevel
	}

	// 同步等级限制配置 - 支持驼峰和kebab-case
	levelLimitsKey := ""
	if _, ok := quotaConfig["levelLimits"]; ok {
		levelLimitsKey = "levelLimits"
	} else if _, ok := quotaConfig["level-limits"]; ok {
		levelLimitsKey = "level-limits"
	}

	if levelLimitsKey != "" {
		if levelLimits, ok := quotaConfig[levelLimitsKey].(map[string]interface{}); ok {
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

					// 更新最大实例数 - 支持驼峰和kebab-case
					if maxInstances, exists := limitMap["maxInstances"]; exists {
						if instances, ok := maxInstances.(float64); ok {
							levelLimit.MaxInstances = int(instances)
						} else if instances, ok := maxInstances.(int); ok {
							levelLimit.MaxInstances = instances
						}
					} else if maxInstances, exists := limitMap["max-instances"]; exists {
						if instances, ok := maxInstances.(float64); ok {
							levelLimit.MaxInstances = int(instances)
						} else if instances, ok := maxInstances.(int); ok {
							levelLimit.MaxInstances = instances
						}
					}

					// 更新最大资源 - 支持驼峰和kebab-case
					if maxResources, exists := limitMap["maxResources"]; exists {
						if resourcesMap, ok := maxResources.(map[string]interface{}); ok {
							levelLimit.MaxResources = resourcesMap
						}
					} else if maxResources, exists := limitMap["max-resources"]; exists {
						if resourcesMap, ok := maxResources.(map[string]interface{}); ok {
							levelLimit.MaxResources = resourcesMap
						}
					}

					// 更新最大流量限制 - 支持驼峰和kebab-case
					if maxTraffic, exists := limitMap["maxTraffic"]; exists {
						if traffic, ok := maxTraffic.(float64); ok {
							levelLimit.MaxTraffic = int64(traffic)
						} else if traffic, ok := maxTraffic.(int64); ok {
							levelLimit.MaxTraffic = traffic
						} else if traffic, ok := maxTraffic.(int); ok {
							levelLimit.MaxTraffic = int64(traffic)
						}
					} else if maxTraffic, exists := limitMap["max-traffic"]; exists {
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
	}

	// 同步实例类型权限配置 - 支持驼峰和kebab-case
	instanceTypePermissionsKey := ""
	if _, ok := quotaConfig["instanceTypePermissions"]; ok {
		instanceTypePermissionsKey = "instanceTypePermissions"
	} else if _, ok := quotaConfig["instance-type-permissions"]; ok {
		instanceTypePermissionsKey = "instance-type-permissions"
	}

	if instanceTypePermissionsKey != "" {
		if instanceTypePermissions, ok := quotaConfig[instanceTypePermissionsKey].(map[string]interface{}); ok {
			// minLevelForContainer
			if minLevelForContainer, exists := instanceTypePermissions["minLevelForContainer"]; exists {
				if level, ok := minLevelForContainer.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = int(level)
				} else if level, ok := minLevelForContainer.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = level
				}
			} else if minLevelForContainer, exists := instanceTypePermissions["min-level-for-container"]; exists {
				if level, ok := minLevelForContainer.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = int(level)
				} else if level, ok := minLevelForContainer.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = level
				}
			}

			// minLevelForVM
			if minLevelForVM, exists := instanceTypePermissions["minLevelForVM"]; exists {
				if level, ok := minLevelForVM.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = int(level)
				} else if level, ok := minLevelForVM.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = level
				}
			} else if minLevelForVM, exists := instanceTypePermissions["min-level-for-vm"]; exists {
				if level, ok := minLevelForVM.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = int(level)
				} else if level, ok := minLevelForVM.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = level
				}
			}

			// minLevelForDeleteContainer
			if minLevelForDeleteContainer, exists := instanceTypePermissions["minLevelForDeleteContainer"]; exists {
				if level, ok := minLevelForDeleteContainer.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = int(level)
				} else if level, ok := minLevelForDeleteContainer.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = level
				}
			} else if minLevelForDeleteContainer, exists := instanceTypePermissions["min-level-for-delete-container"]; exists {
				if level, ok := minLevelForDeleteContainer.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = int(level)
				} else if level, ok := minLevelForDeleteContainer.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = level
				}
			}

			// minLevelForDeleteVM
			if minLevelForDeleteVM, exists := instanceTypePermissions["minLevelForDeleteVM"]; exists {
				if level, ok := minLevelForDeleteVM.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = int(level)
				} else if level, ok := minLevelForDeleteVM.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = level
				}
			} else if minLevelForDeleteVM, exists := instanceTypePermissions["min-level-for-delete-vm"]; exists {
				if level, ok := minLevelForDeleteVM.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = int(level)
				} else if level, ok := minLevelForDeleteVM.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = level
				}
			}

			// minLevelForResetContainer
			if minLevelForResetContainer, exists := instanceTypePermissions["minLevelForResetContainer"]; exists {
				if level, ok := minLevelForResetContainer.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = int(level)
				} else if level, ok := minLevelForResetContainer.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = level
				}
			} else if minLevelForResetContainer, exists := instanceTypePermissions["min-level-for-reset-container"]; exists {
				if level, ok := minLevelForResetContainer.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = int(level)
				} else if level, ok := minLevelForResetContainer.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = level
				}
			}

			// minLevelForResetVM
			if minLevelForResetVM, exists := instanceTypePermissions["minLevelForResetVM"]; exists {
				if level, ok := minLevelForResetVM.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM = int(level)
				} else if level, ok := minLevelForResetVM.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM = level
				}
			} else if minLevelForResetVM, exists := instanceTypePermissions["min-level-for-reset-vm"]; exists {
				if level, ok := minLevelForResetVM.(float64); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM = int(level)
				} else if level, ok := minLevelForResetVM.(int); ok {
					global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM = level
				}
			}

			global.APP_LOG.Info("实例类型权限配置已同步到全局配置",
				zap.Int("minLevelForContainer", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer),
				zap.Int("minLevelForVM", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM),
				zap.Int("minLevelForDeleteContainer", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer),
				zap.Int("minLevelForDeleteVM", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM),
				zap.Int("minLevelForResetContainer", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer),
				zap.Int("minLevelForResetVM", global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM))
		}
	}
}

// syncSystemConfig 同步系统配置
func syncSystemConfig(systemConfig map[string]interface{}) {
	// env
	if env, ok := systemConfig["env"].(string); ok {
		global.APP_CONFIG.System.Env = env
	}

	// addr (端口)
	if addr, ok := systemConfig["addr"].(float64); ok {
		global.APP_CONFIG.System.Addr = int(addr)
	} else if addr, ok := systemConfig["addr"].(int); ok {
		global.APP_CONFIG.System.Addr = addr
	}

	// db-type
	if dbType, ok := systemConfig["dbType"].(string); ok {
		global.APP_CONFIG.System.DbType = dbType
	} else if dbType, ok := systemConfig["db-type"].(string); ok {
		global.APP_CONFIG.System.DbType = dbType
	}

	// oss-type
	if ossType, ok := systemConfig["ossType"].(string); ok {
		global.APP_CONFIG.System.OssType = ossType
	} else if ossType, ok := systemConfig["oss-type"].(string); ok {
		global.APP_CONFIG.System.OssType = ossType
	}

	// use-multipoint
	if useMultipoint, ok := systemConfig["useMultipoint"].(bool); ok {
		global.APP_CONFIG.System.UseMultipoint = useMultipoint
	} else if useMultipoint, ok := systemConfig["use-multipoint"].(bool); ok {
		global.APP_CONFIG.System.UseMultipoint = useMultipoint
	}

	// use-redis
	if useRedis, ok := systemConfig["useRedis"].(bool); ok {
		global.APP_CONFIG.System.UseRedis = useRedis
	} else if useRedis, ok := systemConfig["use-redis"].(bool); ok {
		global.APP_CONFIG.System.UseRedis = useRedis
	}

	// iplimit-count
	if limitCountIP, ok := systemConfig["limitCountIP"].(float64); ok {
		global.APP_CONFIG.System.LimitCountIP = int(limitCountIP)
	} else if limitCountIP, ok := systemConfig["iplimit-count"].(float64); ok {
		global.APP_CONFIG.System.LimitCountIP = int(limitCountIP)
	} else if limitCountIP, ok := systemConfig["limitCountIP"].(int); ok {
		global.APP_CONFIG.System.LimitCountIP = limitCountIP
	} else if limitCountIP, ok := systemConfig["iplimit-count"].(int); ok {
		global.APP_CONFIG.System.LimitCountIP = limitCountIP
	}

	// iplimit-time
	if limitTimeIP, ok := systemConfig["limitTimeIP"].(float64); ok {
		global.APP_CONFIG.System.LimitTimeIP = int(limitTimeIP)
	} else if limitTimeIP, ok := systemConfig["iplimit-time"].(float64); ok {
		global.APP_CONFIG.System.LimitTimeIP = int(limitTimeIP)
	} else if limitTimeIP, ok := systemConfig["limitTimeIP"].(int); ok {
		global.APP_CONFIG.System.LimitTimeIP = limitTimeIP
	} else if limitTimeIP, ok := systemConfig["iplimit-time"].(int); ok {
		global.APP_CONFIG.System.LimitTimeIP = limitTimeIP
	}

	// frontend-url
	if frontendURL, ok := systemConfig["frontendURL"].(string); ok {
		global.APP_CONFIG.System.FrontendURL = frontendURL
	} else if frontendURL, ok := systemConfig["frontend-url"].(string); ok {
		global.APP_CONFIG.System.FrontendURL = frontendURL
	}

	global.APP_LOG.Info("系统配置同步完成",
		zap.String("Env", global.APP_CONFIG.System.Env),
		zap.Int("Addr", global.APP_CONFIG.System.Addr),
		zap.String("DbType", global.APP_CONFIG.System.DbType),
		zap.String("FrontendURL", global.APP_CONFIG.System.FrontendURL),
		zap.Bool("UseRedis", global.APP_CONFIG.System.UseRedis),
		zap.Bool("UseMultipoint", global.APP_CONFIG.System.UseMultipoint))
}

// syncJWTConfig 同步JWT配置
func syncJWTConfig(jwtConfig map[string]interface{}) {
	if signingKey, ok := jwtConfig["signingKey"].(string); ok {
		global.APP_CONFIG.JWT.SigningKey = signingKey
	} else if signingKey, ok := jwtConfig["signing-key"].(string); ok {
		global.APP_CONFIG.JWT.SigningKey = signingKey
	}

	if expiresTime, ok := jwtConfig["expiresTime"].(string); ok {
		global.APP_CONFIG.JWT.ExpiresTime = expiresTime
	} else if expiresTime, ok := jwtConfig["expires-time"].(string); ok {
		global.APP_CONFIG.JWT.ExpiresTime = expiresTime
	}

	if bufferTime, ok := jwtConfig["bufferTime"].(string); ok {
		global.APP_CONFIG.JWT.BufferTime = bufferTime
	} else if bufferTime, ok := jwtConfig["buffer-time"].(string); ok {
		global.APP_CONFIG.JWT.BufferTime = bufferTime
	}

	if issuer, ok := jwtConfig["issuer"].(string); ok {
		global.APP_CONFIG.JWT.Issuer = issuer
	}

	global.APP_LOG.Info("JWT配置同步完成",
		zap.String("ExpiresTime", global.APP_CONFIG.JWT.ExpiresTime),
		zap.String("Issuer", global.APP_CONFIG.JWT.Issuer))
}

// syncCORSConfig 同步CORS配置
func syncCORSConfig(corsConfig map[string]interface{}) {
	if mode, ok := corsConfig["mode"].(string); ok {
		global.APP_CONFIG.Cors.Mode = mode
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

	global.APP_LOG.Info("CORS配置同步完成",
		zap.String("Mode", global.APP_CONFIG.Cors.Mode),
		zap.Int("WhitelistCount", len(global.APP_CONFIG.Cors.Whitelist)))
}

// syncCaptchaConfig 同步验证码配置
func syncCaptchaConfig(captchaConfig map[string]interface{}) {
	if enabled, ok := captchaConfig["enabled"].(bool); ok {
		global.APP_CONFIG.Captcha.Enabled = enabled
	}

	if width, ok := captchaConfig["width"].(float64); ok {
		global.APP_CONFIG.Captcha.Width = int(width)
	} else if width, ok := captchaConfig["width"].(int); ok {
		global.APP_CONFIG.Captcha.Width = width
	}

	if height, ok := captchaConfig["height"].(float64); ok {
		global.APP_CONFIG.Captcha.Height = int(height)
	} else if height, ok := captchaConfig["height"].(int); ok {
		global.APP_CONFIG.Captcha.Height = height
	}

	if length, ok := captchaConfig["length"].(float64); ok {
		global.APP_CONFIG.Captcha.Length = int(length)
	} else if length, ok := captchaConfig["length"].(int); ok {
		global.APP_CONFIG.Captcha.Length = length
	}

	if expireTime, ok := captchaConfig["expireTime"].(float64); ok {
		global.APP_CONFIG.Captcha.ExpireTime = int(expireTime)
	} else if expireTime, ok := captchaConfig["expire-time"].(float64); ok {
		global.APP_CONFIG.Captcha.ExpireTime = int(expireTime)
	} else if expireTime, ok := captchaConfig["expireTime"].(int); ok {
		global.APP_CONFIG.Captcha.ExpireTime = expireTime
	} else if expireTime, ok := captchaConfig["expire-time"].(int); ok {
		global.APP_CONFIG.Captcha.ExpireTime = expireTime
	}

	global.APP_LOG.Info("验证码配置同步完成",
		zap.Bool("Enabled", global.APP_CONFIG.Captcha.Enabled),
		zap.Int("Length", global.APP_CONFIG.Captcha.Length))
}

// syncUploadConfig 同步上传配置
func syncUploadConfig(uploadConfig map[string]interface{}) {
	// 支持驼峰和kebab-case两种格式
	if maxAvatarSize, ok := uploadConfig["maxAvatarSize"].(float64); ok {
		global.APP_CONFIG.Upload.MaxAvatarSize = int64(maxAvatarSize)
	} else if maxAvatarSize, ok := uploadConfig["max-avatar-size"].(float64); ok {
		global.APP_CONFIG.Upload.MaxAvatarSize = int64(maxAvatarSize)
	} else if maxAvatarSize, ok := uploadConfig["maxAvatarSize"].(int64); ok {
		global.APP_CONFIG.Upload.MaxAvatarSize = maxAvatarSize
	} else if maxAvatarSize, ok := uploadConfig["max-avatar-size"].(int64); ok {
		global.APP_CONFIG.Upload.MaxAvatarSize = maxAvatarSize
	} else if maxAvatarSize, ok := uploadConfig["maxAvatarSize"].(int); ok {
		global.APP_CONFIG.Upload.MaxAvatarSize = int64(maxAvatarSize)
	} else if maxAvatarSize, ok := uploadConfig["max-avatar-size"].(int); ok {
		global.APP_CONFIG.Upload.MaxAvatarSize = int64(maxAvatarSize)
	}

	global.APP_LOG.Info("上传配置同步完成",
		zap.Int64("MaxAvatarSize", global.APP_CONFIG.Upload.MaxAvatarSize))
}

// syncOtherConfig 同步其他配置
func syncOtherConfig(otherConfig map[string]interface{}) {
	// 支持驼峰和kebab-case两种格式
	if maxAvatarSize, ok := otherConfig["maxAvatarSize"].(float64); ok {
		global.APP_CONFIG.Other.MaxAvatarSize = maxAvatarSize
	} else if maxAvatarSize, ok := otherConfig["max-avatar-size"].(float64); ok {
		global.APP_CONFIG.Other.MaxAvatarSize = maxAvatarSize
	}

	if defaultLanguage, ok := otherConfig["defaultLanguage"].(string); ok {
		global.APP_CONFIG.Other.DefaultLanguage = defaultLanguage
	} else if defaultLanguage, ok := otherConfig["default-language"].(string); ok {
		global.APP_CONFIG.Other.DefaultLanguage = defaultLanguage
	}

	global.APP_LOG.Info("其他配置同步完成",
		zap.Float64("MaxAvatarSize", global.APP_CONFIG.Other.MaxAvatarSize),
		zap.String("DefaultLanguage", global.APP_CONFIG.Other.DefaultLanguage))
}
