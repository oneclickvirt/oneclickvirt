package config

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"oneclickvirt/config"
	"oneclickvirt/global"
	"oneclickvirt/middleware"
	authModel "oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	configModel "oneclickvirt/model/config"
	"oneclickvirt/service/auth"
	resourceService "oneclickvirt/service/resources"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type configGetter interface {
	GetConfig(key string) (interface{}, bool)
}

func isNilConfigGetter(getter configGetter) bool {
	if getter == nil {
		return true
	}

	value := reflect.ValueOf(getter)
	return value.Kind() == reflect.Ptr && value.IsNil()
}

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
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
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
	if configManager == nil && scope != "admin" && scope != "public" {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "配置管理器未初始化"))
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
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "权限不足"))
			return
		}
		result = getAdminConfig(configManager)
	default:
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, "无效的配置范围"))
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
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
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
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "权限不足"))
		return
	}

	configManager := config.GetConfigManager()
	if configManager == nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "配置管理器未初始化"))
		return
	}

	// 根据范围过滤配置项
	filteredConfig := filterConfigByScope(req.Config, req.Scope, authCtx)

	// 验证 level_limits 配置
	if err := validateLevelLimitsConfig(filteredConfig); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	shouldSyncUserQuota := hasQuotaLevelLimitUpdate(filteredConfig)

	// 更新配置
	// UpdateConfig 会自动：
	// 1. 将配置保存到数据库（自动转换为 kebab-case 格式）
	// 2. 通过已注册的回调函数同步到 global.APP_CONFIG
	// 3. 写回到 YAML 文件
	if err := configManager.UpdateConfig(filteredConfig); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeConfigError, err.Error()))
		return
	}

	// ConfigManager.UpdateConfig 已经通过回调机制自动同步到全局配置
	// 回调函数在 initialize/config_manager.go 的 syncConfigToGlobal 中定义
	// 它会正确处理 kebab-case 和 camelCase 两种格式的键名
	if shouldSyncUserQuota {
		quotaSyncService := &resourceService.QuotaSyncService{}
		if err := quotaSyncService.SyncAllUsersToCurrentConfig(); err != nil {
			global.APP_LOG.Error("同步用户配额缓存失败", zap.Error(err))
			common.ResponseWithError(c, common.NewError(common.CodeConfigError, "配置已保存，但同步用户配额缓存失败: "+err.Error()))
			return
		}
	}

	common.ResponseSuccess(c, nil, "配置更新成功")
}

func getConfigBool(cm configGetter, key string, fallback bool) bool {
	if isNilConfigGetter(cm) {
		return fallback
	}

	value, exists := cm.GetConfig(key)
	if !exists {
		return fallback
	}

	booleanValue, ok := value.(bool)
	if !ok {
		return fallback
	}

	return booleanValue
}

func getConfigString(cm configGetter, key string, fallback string) string {
	if isNilConfigGetter(cm) {
		return fallback
	}

	value, exists := cm.GetConfig(key)
	if !exists {
		return fallback
	}

	stringValue, ok := value.(string)
	if !ok {
		return fallback
	}

	return stringValue
}

// getPublicConfig 获取公开配置
func getPublicConfig(cm *config.ConfigManager) map[string]interface{} {
	// 直接从全局配置构建，不依赖扁平化的configCache
	result := make(map[string]interface{})
	result["captchaEnabled"] = getConfigBool(cm, "captcha.enabled", global.GetAppConfig().Captcha.Enabled)
	result["oauth2Enabled"] = getConfigBool(cm, "auth.enable-oauth2", global.GetAppConfig().Auth.EnableOAuth2)
	result["registrationEnabled"] = getConfigBool(cm, "auth.enable-public-registration", global.GetAppConfig().Auth.EnablePublicRegistration)
	return result
}

// getUserConfig 获取用户配置（使用服务端权限验证）
func getUserConfig(cm *config.ConfigManager, authCtx *authModel.AuthContext) map[string]interface{} {
	result := make(map[string]interface{})
	permissionService := auth.PermissionService{}

	// 基础配置 - 所有用户可见
	result["auth"] = map[string]interface{}{
		"enablePublicRegistration": global.GetAppConfig().Auth.EnablePublicRegistration,
	}

	// 配额配置 - 从 global.APP_CONFIG 获取完整配置
	levelLimits := make(map[string]interface{})
	for level, limitInfo := range global.GetAppConfig().Quota.LevelLimits {
		limitInfo = config.NormalizeLevelLimitInfo(level, limitInfo)
		levelKey := fmt.Sprintf("%d", level)
		levelLimits[levelKey] = map[string]interface{}{
			"max-instances": limitInfo.MaxInstances,
			"max-resources": limitInfo.MaxResources,
			"max-traffic":   limitInfo.MaxTraffic,
			"expiry-days":   limitInfo.ExpiryDays,
			"max-snapshots": limitInfo.MaxSnapshots,
		}
	}

	result["quota"] = map[string]interface{}{
		"defaultLevel": global.GetAppConfig().Quota.DefaultLevel,
		"levelLimits":  levelLimits,
	}

	// 管理员可以看到更多配置
	hasAdminPermission := permissionService.HasPermission(authCtx.UserID, "admin")
	if hasAdminPermission {
		authConfig := result["auth"].(map[string]interface{})
		authConfig["enableEmail"] = global.GetAppConfig().Auth.EnableEmail
		authConfig["enableTelegram"] = global.GetAppConfig().Auth.EnableTelegram
		authConfig["enableQQ"] = global.GetAppConfig().Auth.EnableQQ
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
		"enableEmail":              global.GetAppConfig().Auth.EnableEmail,
		"enableTelegram":           global.GetAppConfig().Auth.EnableTelegram,
		"enableQQ":                 global.GetAppConfig().Auth.EnableQQ,
		"enableOAuth2":             global.GetAppConfig().Auth.EnableOAuth2,
		"enablePublicRegistration": global.GetAppConfig().Auth.EnablePublicRegistration,
		"enableKYC":                global.GetAppConfig().Auth.EnableKYC,
		"enableDomain":             global.GetAppConfig().Auth.EnableDomain,
		"emailSMTPHost":            global.GetAppConfig().Auth.EmailSMTPHost,
		"emailSMTPPort":            global.GetAppConfig().Auth.EmailSMTPPort,
		"emailUsername":            global.GetAppConfig().Auth.EmailUsername,
		"emailPassword":            global.GetAppConfig().Auth.EmailPassword,
		"telegramBotToken":         global.GetAppConfig().Auth.TelegramBotToken,
		"qqAppID":                  global.GetAppConfig().Auth.QQAppID,
		"qqAppKey":                 global.GetAppConfig().Auth.QQAppKey,
	}

	// 邀请码配置
	result["inviteCode"] = map[string]interface{}{
		"enabled":  global.GetAppConfig().InviteCode.Enabled,
		"required": global.GetAppConfig().InviteCode.Required,
	}

	// 配额配置 - 从 global.APP_CONFIG 获取完整的等级限制
	levelLimits := make(map[string]interface{})
	for level, limitInfo := range global.GetAppConfig().Quota.LevelLimits {
		limitInfo = config.NormalizeLevelLimitInfo(level, limitInfo)
		levelKey := fmt.Sprintf("%d", level)
		levelLimits[levelKey] = map[string]interface{}{
			"max-instances": limitInfo.MaxInstances,
			"max-resources": limitInfo.MaxResources,
			"max-traffic":   limitInfo.MaxTraffic,
			"expiry-days":   limitInfo.ExpiryDays,
			"max-snapshots": limitInfo.MaxSnapshots,
		}
	}

	result["quota"] = map[string]interface{}{
		"defaultLevel": global.GetAppConfig().Quota.DefaultLevel,
		"levelLimits":  levelLimits,
		"instanceTypePermissions": map[string]interface{}{
			"minLevelForContainer":       global.GetAppConfig().Quota.InstanceTypePermissions.MinLevelForContainer,
			"minLevelForVM":              global.GetAppConfig().Quota.InstanceTypePermissions.MinLevelForVM,
			"minLevelForDeleteContainer": global.GetAppConfig().Quota.InstanceTypePermissions.MinLevelForDeleteContainer,
			"minLevelForDeleteVM":        global.GetAppConfig().Quota.InstanceTypePermissions.MinLevelForDeleteVM,
			"minLevelForResetContainer":  global.GetAppConfig().Quota.InstanceTypePermissions.MinLevelForResetContainer,
			"minLevelForResetVM":         global.GetAppConfig().Quota.InstanceTypePermissions.MinLevelForResetVM,
		},
	}

	// 其他配置
	result["other"] = map[string]interface{}{
		"defaultLanguage": global.GetAppConfig().Other.DefaultLanguage,
		"logoURL":         global.GetAppConfig().Other.LogoURL,
		"siteName":        global.GetAppConfig().Other.SiteName,
	}

	// 验证码配置
	result["captcha"] = map[string]interface{}{
		"enabled": getConfigBool(cm, "captcha.enabled", global.GetAppConfig().Captcha.Enabled),
	}

	// KYC实名认证配置
	result["kyc"] = map[string]interface{}{
		"method":                 getConfigString(cm, "kyc.method", global.GetAppConfig().KYC.Method),
		"requireRealName":        global.GetAppConfig().KYC.RequireRealName,
		"restrictCreateInstance": global.GetAppConfig().KYC.RestrictCreateInstance,
		"restrictRedeemCode":     global.GetAppConfig().KYC.RestrictRedeemCode,
		"restrictDomainBind":     global.GetAppConfig().KYC.RestrictDomainBind,
		"alipayAppId":            global.GetAppConfig().KYC.AlipayAppID,
		"alipayPrivateKey":       global.GetAppConfig().KYC.AlipayPrivateKey,
		"alipayPublicKey":        global.GetAppConfig().KYC.AlipayPublicKey,
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

// validateLevelLimitsConfig validates the level-limits section of config updates.
// It supports the legacy level_limits key and the current nested quota.levelLimits shape.
func validateLevelLimitsConfig(cfg map[string]interface{}) error {
	rawLimits, ok := findLevelLimitsConfig(cfg)
	if !ok {
		return nil
	}
	limits, ok := rawLimits.(map[string]interface{})
	if !ok {
		return fmt.Errorf("levelLimits 格式无效")
	}

	for levelKey, levelVal := range limits {
		levelNum, err := strconv.Atoi(levelKey)
		if err != nil || levelNum < 1 || levelNum > 99 {
			return fmt.Errorf("levelLimits 的键必须为 1-99 的整数，无效键: %s", levelKey)
		}
		levelMap, ok := levelVal.(map[string]interface{})
		if !ok {
			return fmt.Errorf("levelLimits[%s] 格式无效", levelKey)
		}

		for _, field := range []string{"maxInstances", "max-instances", "maxTraffic", "max-traffic", "expiryDays", "expiry-days", "maxSnapshots", "max-snapshots"} {
			if val, exists := levelMap[field]; exists {
				if err := validateNonNegativeConfigNumber(val, fmt.Sprintf("levelLimits[%s].%s", levelKey, field)); err != nil {
					return err
				}
			}
		}

		if rawResources, exists := firstMapValue(levelMap, "maxResources", "max-resources"); exists {
			resources, ok := rawResources.(map[string]interface{})
			if !ok {
				return fmt.Errorf("levelLimits[%s].maxResources 格式无效", levelKey)
			}
			for _, field := range []string{"cpu", "memory", "disk", "bandwidth"} {
				if val, exists := resources[field]; exists {
					if err := validateNonNegativeConfigNumber(val, fmt.Sprintf("levelLimits[%s].maxResources.%s", levelKey, field)); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

func findLevelLimitsConfig(cfg map[string]interface{}) (interface{}, bool) {
	for _, key := range []string{"level_limits", "quota.levelLimits", "quota.level-limits"} {
		if value, exists := cfg[key]; exists {
			return value, true
		}
	}
	if quota, ok := cfg["quota"].(map[string]interface{}); ok {
		return firstMapValue(quota, "levelLimits", "level-limits")
	}
	return nil, false
}

func firstMapValue(values map[string]interface{}, keys ...string) (interface{}, bool) {
	for _, key := range keys {
		if value, exists := values[key]; exists {
			return value, true
		}
	}
	return nil, false
}

func validateNonNegativeConfigNumber(value interface{}, fieldName string) error {
	switch v := value.(type) {
	case float64:
		if v < 0 {
			return fmt.Errorf("%s 不能为负数", fieldName)
		}
	case int:
		if v < 0 {
			return fmt.Errorf("%s 不能为负数", fieldName)
		}
	case int64:
		if v < 0 {
			return fmt.Errorf("%s 不能为负数", fieldName)
		}
	case string:
		return fmt.Errorf("%s 必须为数字类型", fieldName)
	default:
		return fmt.Errorf("%s 类型无效", fieldName)
	}
	return nil
}

func hasQuotaLevelLimitUpdate(config map[string]interface{}) bool {
	_, ok := findLevelLimitsConfig(config)
	return ok
}
