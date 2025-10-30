package config

import (
	"fmt"
	"net/http"
	"oneclickvirt/service/auth"
	"oneclickvirt/service/resources"
	"strings"

	"oneclickvirt/config"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	configModel "oneclickvirt/model/config"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// GetUnifiedConfig 获取统一配置接口
// @Summary 获取系统配置
// @Description 根据用户权限返回相应的配置信息
// @Tags 配置管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param scope query string false "配置范围" Enums(public,user,admin) default(user)
// @Success 200 {object} common.Response{data=interface{}} "获取成功"
// @Failure 401 {object} common.Response "认证失败"
// @Failure 403 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "获取失败"
// @Router /config [get]
func GetUnifiedConfig(c *gin.Context) {
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, common.Response{
			Code: 401,
			Msg:  "用户未认证",
		})
		return
	}

	// 根据请求路径自动判断 scope
	scope := c.DefaultQuery("scope", "")
	if scope == "" {
		// 如果没有提供 scope 参数，根据路径判断
		if strings.Contains(c.Request.URL.Path, "/admin/") {
			scope = "admin"
		} else if strings.Contains(c.Request.URL.Path, "/public/") {
			scope = "public"
		} else {
			scope = "user"
		}
	}

	// 根据用户权限和请求范围决定返回的配置
	configManager := config.GetConfigManager()
	if configManager == nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "配置管理器未初始化",
		})
		return
	}

	var result map[string]interface{}

	switch scope {
	case "public":
		// 公开配置，所有用户都可以访问
		result = getPublicConfig(configManager)
	case "user":
		// 用户配置，普通用户可以访问的配置
		result = getUserConfig(configManager, authCtx)
	case "admin", "global":
		// 管理员配置和全局配置，只有管理员可以访问
		permissionService := auth.PermissionService{}
		hasAdminPermission := permissionService.HasPermission(authCtx.UserID, "admin")
		if !hasAdminPermission {
			c.JSON(http.StatusForbidden, common.Response{
				Code: 403,
				Msg:  "权限不足",
			})
			return
		}
		result = getAdminConfig(configManager)
	default:
		c.JSON(http.StatusBadRequest, common.Response{
			Code: 400,
			Msg:  "无效的配置范围",
		})
		return
	}

	common.ResponseSuccess(c, result)
}

// UpdateUnifiedConfig 更新统一配置接口
// @Summary 更新系统配置
// @Description 根据用户权限更新相应的配置信息
// @Tags 配置管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Param request body configModel.UnifiedConfigRequest true "配置更新请求"
// @Success 200 {object} common.Response "更新成功"
// @Failure 400 {object} common.Response "参数错误"
// @Failure 401 {object} common.Response "认证失败"
// @Failure 403 {object} common.Response "权限不足"
// @Failure 500 {object} common.Response "更新失败"
// @Router /config [put]
func UpdateUnifiedConfig(c *gin.Context) {
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		c.JSON(http.StatusUnauthorized, common.Response{
			Code: 401,
			Msg:  "用户未认证",
		})
		return
	}

	// 解析请求体
	var rawData map[string]interface{}
	if err := c.ShouldBindJSON(&rawData); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "参数错误"))
		return
	}

	var req configModel.UnifiedConfigRequest

	// 检查是否是新的统一格式
	if scope, exists := rawData["scope"]; exists {
		if config, configExists := rawData["config"]; configExists {
			req.Scope = scope.(string)
			req.Config = config.(map[string]interface{})
		} else {
			common.ResponseWithError(c, common.NewError(common.CodeValidationError, "统一格式缺少config字段"))
			return
		}
	} else {
		// 向后兼容：直接配置数据，根据路径判断 scope
		if strings.Contains(c.Request.URL.Path, "/admin/") {
			req.Scope = "admin"
		} else {
			req.Scope = "user"
		}
		req.Config = rawData
	}

	// 验证权限
	if !hasConfigUpdatePermission(authCtx, req.Scope) {
		c.JSON(http.StatusForbidden, common.Response{
			Code: 403,
			Msg:  "权限不足",
		})
		return
	}

	configManager := config.GetConfigManager()
	if configManager == nil {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: 500,
			Msg:  "配置管理器未初始化",
		})
		return
	}

	// 根据范围过滤配置项
	filteredConfig := filterConfigByScope(req.Config, req.Scope, authCtx)

	// 更新配置
	if err := configManager.UpdateConfig(filteredConfig); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeConfigError, err.Error()))
		return
	}

	// 同步重要配置到全局配置对象（使用ConfigService确保一致性）
	if err := syncConfigToGlobalViaService(filteredConfig); err != nil {
		global.APP_LOG.Error("同步配置到全局对象失败", zap.Error(err))
		// 不阻止成功响应，因为配置已经保存到数据库
	}

	common.ResponseSuccess(c, nil, "配置更新成功")
}

// getPublicConfig 获取公开配置
func getPublicConfig(cm *config.ConfigManager) map[string]interface{} {
	allConfig := cm.GetAllConfig()
	publicConfig := make(map[string]interface{})

	// 只返回公开的配置项
	publicKeys := []string{
		"app.name",
		"app.version",
		"app.description",
		"auth.enablePublicRegistration",
	}

	for _, key := range publicKeys {
		if value, exists := allConfig[key]; exists {
			publicConfig[key] = value
		}
	}

	// 将扁平化配置转换为嵌套结构
	return unflattenConfig(publicConfig)
}

// syncConfigToGlobalViaService 同步配置到全局配置对象
func syncConfigToGlobalViaService(configData map[string]interface{}) error {
	// 同步认证配置
	if authData, exists := configData["auth"]; exists {
		if authMap, ok := authData.(map[string]interface{}); ok {
			if v, ok := authMap["enableEmail"].(bool); ok {
				global.APP_CONFIG.Auth.EnableEmail = v
			}
			if v, ok := authMap["enableTelegram"].(bool); ok {
				global.APP_CONFIG.Auth.EnableTelegram = v
			}
			if v, ok := authMap["enableQQ"].(bool); ok {
				global.APP_CONFIG.Auth.EnableQQ = v
			}
			if v, ok := authMap["enableOAuth2"].(bool); ok {
				global.APP_CONFIG.Auth.EnableOAuth2 = v
			}
			if v, ok := authMap["enablePublicRegistration"].(bool); ok {
				global.APP_CONFIG.Auth.EnablePublicRegistration = v
			}
			if v, ok := authMap["emailSMTPHost"].(string); ok {
				global.APP_CONFIG.Auth.EmailSMTPHost = v
			}
			if v, ok := authMap["emailSMTPPort"]; ok {
				if port, ok := v.(float64); ok {
					global.APP_CONFIG.Auth.EmailSMTPPort = int(port)
				} else if port, ok := v.(int); ok {
					global.APP_CONFIG.Auth.EmailSMTPPort = port
				}
			}
			if v, ok := authMap["emailUsername"].(string); ok {
				global.APP_CONFIG.Auth.EmailUsername = v
			}
			if v, ok := authMap["emailPassword"].(string); ok {
				global.APP_CONFIG.Auth.EmailPassword = v
			}
			if v, ok := authMap["telegramBotToken"].(string); ok {
				global.APP_CONFIG.Auth.TelegramBotToken = v
			}
			if v, ok := authMap["qqAppID"].(string); ok {
				global.APP_CONFIG.Auth.QQAppID = v
			}
			if v, ok := authMap["qqAppKey"].(string); ok {
				global.APP_CONFIG.Auth.QQAppKey = v
			}
			global.APP_LOG.Info("认证配置已同步到全局配置")
		}
	}

	// 同步邀请码配置
	if inviteData, exists := configData["inviteCode"]; exists {
		if inviteMap, ok := inviteData.(map[string]interface{}); ok {
			if v, ok := inviteMap["enabled"].(bool); ok {
				global.APP_CONFIG.InviteCode.Enabled = v
			}
			if v, ok := inviteMap["required"].(bool); ok {
				global.APP_CONFIG.InviteCode.Required = v
			}
			global.APP_LOG.Info("邀请码配置已同步到全局配置")
		}
	}

	// 同步配额配置
	if quotaData, exists := configData["quota"]; exists {
		if quotaMap, ok := quotaData.(map[string]interface{}); ok {
			// 同步默认等级
			if defaultLevel, exists := quotaMap["defaultLevel"]; exists {
				if level, ok := defaultLevel.(float64); ok {
					global.APP_CONFIG.Quota.DefaultLevel = int(level)
				} else if level, ok := defaultLevel.(int); ok {
					global.APP_CONFIG.Quota.DefaultLevel = level
				}
			}

			// 同步实例类型权限配置
			if instanceTypePermissions, exists := quotaMap["instanceTypePermissions"]; exists {
				if permissionsMap, ok := instanceTypePermissions.(map[string]interface{}); ok {
					if v, ok := permissionsMap["minLevelForContainer"]; ok {
						if level, ok := v.(float64); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = int(level)
						} else if level, ok := v.(int); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForContainer = level
						}
					}
					if v, ok := permissionsMap["minLevelForVM"]; ok {
						if level, ok := v.(float64); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = int(level)
						} else if level, ok := v.(int); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForVM = level
						}
					}
					if v, ok := permissionsMap["minLevelForDeleteContainer"]; ok {
						if level, ok := v.(float64); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = int(level)
						} else if level, ok := v.(int); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteContainer = level
						}
					}
					if v, ok := permissionsMap["minLevelForDeleteVM"]; ok {
						if level, ok := v.(float64); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = int(level)
						} else if level, ok := v.(int); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForDeleteVM = level
						}
					}
					if v, ok := permissionsMap["minLevelForResetContainer"]; ok {
						if level, ok := v.(float64); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = int(level)
						} else if level, ok := v.(int); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetContainer = level
						}
					}
					if v, ok := permissionsMap["minLevelForResetVM"]; ok {
						if level, ok := v.(float64); ok {
							global.APP_CONFIG.Quota.InstanceTypePermissions.MinLevelForResetVM = int(level)
						} else if level, ok := v.(int); ok {
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

			// 同步等级限制
			if levelLimitsData, exists := quotaMap["levelLimits"]; exists {
				if levelLimitsMap, ok := levelLimitsData.(map[string]interface{}); ok {
					// 保存旧配置用于检测变更
					oldLevelLimits := make(map[int]config.LevelLimitInfo)
					for k, v := range global.APP_CONFIG.Quota.LevelLimits {
						oldLevelLimits[k] = v
					}

					// 初始化等级限制map
					if global.APP_CONFIG.Quota.LevelLimits == nil {
						global.APP_CONFIG.Quota.LevelLimits = make(map[int]config.LevelLimitInfo)
					}

					// 更新等级限制
					for levelStr, limitData := range levelLimitsMap {
						if limitMap, ok := limitData.(map[string]interface{}); ok {
							// 解析等级数字
							var level int
							fmt.Sscanf(levelStr, "%d", &level)
							if level < 1 || level > 5 {
								continue
							}

							levelLimit := config.LevelLimitInfo{}

							// 解析 maxInstances
							if maxInstances, exists := limitMap["maxInstances"]; exists {
								if v, ok := maxInstances.(float64); ok {
									levelLimit.MaxInstances = int(v)
								} else if v, ok := maxInstances.(int); ok {
									levelLimit.MaxInstances = v
								}
							}

							// 解析 maxTraffic
							if maxTraffic, exists := limitMap["maxTraffic"]; exists {
								if v, ok := maxTraffic.(float64); ok {
									levelLimit.MaxTraffic = int64(v)
								} else if v, ok := maxTraffic.(int64); ok {
									levelLimit.MaxTraffic = v
								} else if v, ok := maxTraffic.(int); ok {
									levelLimit.MaxTraffic = int64(v)
								}
							}

							// 解析 maxResources
							if maxResources, exists := limitMap["maxResources"]; exists {
								if resourcesMap, ok := maxResources.(map[string]interface{}); ok {
									levelLimit.MaxResources = resourcesMap
								}
							}

							global.APP_CONFIG.Quota.LevelLimits[level] = levelLimit
						}
					}

					global.APP_LOG.Info("配额等级限制已同步到全局配置",
						zap.Int("等级数量", len(global.APP_CONFIG.Quota.LevelLimits)))

					// 检测变更并自动同步用户资源限制
					quotaSyncService := resources.QuotaSyncService{}
					oldConfig := map[string]interface{}{
						"quota": map[string]interface{}{
							"levelLimits": convertLevelLimitsToMap(oldLevelLimits),
						},
					}
					newConfig := map[string]interface{}{
						"quota": map[string]interface{}{
							"levelLimits": levelLimitsMap,
						},
					}
					if err := quotaSyncService.DetectAndSyncLevelChanges(oldConfig, newConfig); err != nil {
						global.APP_LOG.Error("自动同步用户资源限制失败", zap.Error(err))
					}
				}
			}
		}
	}

	// 同步其他配置
	if otherData, exists := configData["other"]; exists {
		if otherMap, ok := otherData.(map[string]interface{}); ok {
			if v, ok := otherMap["maxAvatarSize"].(float64); ok {
				global.APP_CONFIG.Other.MaxAvatarSize = v
			}
			if v, ok := otherMap["defaultLanguage"].(string); ok {
				global.APP_CONFIG.Other.DefaultLanguage = v
			}
			global.APP_LOG.Info("其他配置已同步到全局配置",
				zap.Float64("maxAvatarSize", global.APP_CONFIG.Other.MaxAvatarSize),
				zap.String("defaultLanguage", global.APP_CONFIG.Other.DefaultLanguage))
		}
	}

	return nil
}

// convertLevelLimitsToMap 将LevelLimits转换为map格式以便比较
func convertLevelLimitsToMap(levelLimits map[int]config.LevelLimitInfo) map[string]interface{} {
	result := make(map[string]interface{})

	for level, limitInfo := range levelLimits {
		levelStr := fmt.Sprintf("%d", level)
		limitMap := map[string]interface{}{
			"maxInstances": limitInfo.MaxInstances,
			"maxTraffic":   limitInfo.MaxTraffic,
		}

		if limitInfo.MaxResources != nil {
			limitMap["maxResources"] = limitInfo.MaxResources
		}

		result[levelStr] = limitMap
	}

	return result
}

// getUserConfig 获取用户配置（使用服务端权限验证）
func getUserConfig(cm *config.ConfigManager, authCtx *authModel.AuthContext) map[string]interface{} {
	result := make(map[string]interface{})
	permissionService := auth.PermissionService{}

	// 基础配置 - 所有用户可见
	result["auth"] = map[string]interface{}{
		"enablePublicRegistration": global.APP_CONFIG.Auth.EnablePublicRegistration,
	}

	// 配额配置 - 从 global.APP_CONFIG 获取完整配置
	levelLimits := make(map[string]interface{})
	for level, limitInfo := range global.APP_CONFIG.Quota.LevelLimits {
		levelKey := fmt.Sprintf("%d", level)
		levelLimits[levelKey] = map[string]interface{}{
			"maxInstances": limitInfo.MaxInstances,
			"maxResources": limitInfo.MaxResources,
			"maxTraffic":   limitInfo.MaxTraffic,
		}
	}

	result["quota"] = map[string]interface{}{
		"defaultLevel": global.APP_CONFIG.Quota.DefaultLevel,
		"levelLimits":  levelLimits,
	}

	// 管理员可以看到更多配置
	hasAdminPermission := permissionService.HasPermission(authCtx.UserID, "admin")
	if hasAdminPermission {
		authConfig := result["auth"].(map[string]interface{})
		authConfig["enableEmail"] = global.APP_CONFIG.Auth.EnableEmail
		authConfig["enableTelegram"] = global.APP_CONFIG.Auth.EnableTelegram
		authConfig["enableQQ"] = global.APP_CONFIG.Auth.EnableQQ
	}

	return result
}

// getAdminConfig 获取管理员配置
func getAdminConfig(cm *config.ConfigManager) map[string]interface{} {
	// 直接从 global.APP_CONFIG 构建完整配置返回
	// 确保返回所有配置项（包括默认值）
	result := make(map[string]interface{})

	// 认证配置
	result["auth"] = map[string]interface{}{
		"enableEmail":              global.APP_CONFIG.Auth.EnableEmail,
		"enableTelegram":           global.APP_CONFIG.Auth.EnableTelegram,
		"enableQQ":                 global.APP_CONFIG.Auth.EnableQQ,
		"enableOAuth2":             global.APP_CONFIG.Auth.EnableOAuth2,
		"enablePublicRegistration": global.APP_CONFIG.Auth.EnablePublicRegistration,
		"emailSMTPHost":            global.APP_CONFIG.Auth.EmailSMTPHost,
		"emailSMTPPort":            global.APP_CONFIG.Auth.EmailSMTPPort,
		"emailUsername":            global.APP_CONFIG.Auth.EmailUsername,
		"emailPassword":            global.APP_CONFIG.Auth.EmailPassword,
		"telegramBotToken":         global.APP_CONFIG.Auth.TelegramBotToken,
		"qqAppID":                  global.APP_CONFIG.Auth.QQAppID,
		"qqAppKey":                 global.APP_CONFIG.Auth.QQAppKey,
	}

	// 邀请码配置
	result["inviteCode"] = map[string]interface{}{
		"enabled":  global.APP_CONFIG.InviteCode.Enabled,
		"required": global.APP_CONFIG.InviteCode.Required,
	}

	// 配额配置 - 从 global.APP_CONFIG 获取完整的等级限制
	levelLimits := make(map[string]interface{})
	for level, limitInfo := range global.APP_CONFIG.Quota.LevelLimits {
		levelKey := fmt.Sprintf("%d", level)
		levelLimits[levelKey] = map[string]interface{}{
			"maxInstances": limitInfo.MaxInstances,
			"maxResources": limitInfo.MaxResources,
			"maxTraffic":   limitInfo.MaxTraffic,
		}
	}

	result["quota"] = map[string]interface{}{
		"defaultLevel": global.APP_CONFIG.Quota.DefaultLevel,
		"levelLimits":  levelLimits,
	}

	// 其他配置
	result["other"] = map[string]interface{}{
		"maxAvatarSize":   global.APP_CONFIG.Other.MaxAvatarSize,
		"defaultLanguage": global.APP_CONFIG.Other.DefaultLanguage,
	}

	return result
} // unflattenConfig 将扁平化的配置（如 quota.defaultLevel）转换为嵌套结构（如 quota: { defaultLevel: 1 }）
func unflattenConfig(flatConfig map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})

	for key, value := range flatConfig {
		setNestedValue(result, key, value)
	}

	return result
}

// setNestedValue 将点分隔的键设置为嵌套结构
func setNestedValue(target map[string]interface{}, key string, value interface{}) {
	keys := strings.Split(key, ".")
	current := target

	for i := 0; i < len(keys)-1; i++ {
		k := keys[i]
		if _, exists := current[k]; !exists {
			current[k] = make(map[string]interface{})
		}
		if nested, ok := current[k].(map[string]interface{}); ok {
			current = nested
		}
	}

	current[keys[len(keys)-1]] = value
}

// hasConfigUpdatePermission 检查配置更新权限（使用服务端权限验证）
func hasConfigUpdatePermission(authCtx *authModel.AuthContext, scope string) bool {
	// 使用权限服务进行服务端权限验证
	permissionService := auth.PermissionService{}

	switch scope {
	case "public":
		// 公开配置不允许更新
		return false
	case "user":
		// 普通用户配置，管理员可以更新
		// 使用权限服务验证，而不是依赖客户端传入的userType
		hasAdminPermission := permissionService.HasPermission(authCtx.UserID, "admin")
		return hasAdminPermission
	case "admin", "global":
		// 管理员配置和全局配置，只有管理员可以更新
		hasAdminPermission := permissionService.HasPermission(authCtx.UserID, "admin")
		return hasAdminPermission
	default:
		return false
	}
}

// filterConfigByScope 根据范围过滤配置（使用服务端权限验证）
func filterConfigByScope(config map[string]interface{}, scope string, authCtx *authModel.AuthContext) map[string]interface{} {
	filtered := make(map[string]interface{})
	permissionService := auth.PermissionService{}

	switch scope {
	case "user":
		// 只允许更新用户级别的配置
		allowedKeys := map[string]bool{
			"quota.defaultLevel": true,
			"quota.levelLimits":  true,
		}

		// 使用权限服务验证，而不是依赖JWT中的userType
		hasAdminPermission := permissionService.HasPermission(authCtx.UserID, "admin")
		if hasAdminPermission {
			allowedKeys["auth.enablePublicRegistration"] = true
		}

		for key, value := range config {
			if allowedKeys[key] {
				filtered[key] = value
			}
		}
	case "admin":
		// 管理员可以更新所有配置
		hasAdminPermission := permissionService.HasPermission(authCtx.UserID, "admin")
		if hasAdminPermission {
			filtered = config
		}
	case "global":
		// 全局配置，只有管理员可以更新
		hasAdminPermission := permissionService.HasPermission(authCtx.UserID, "admin")
		if hasAdminPermission {
			filtered = config
		}
	}

	return filtered
}
