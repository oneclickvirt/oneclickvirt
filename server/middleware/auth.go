package middleware

import (
	"fmt"
	auth2 "oneclickvirt/service/auth"
	"oneclickvirt/service/cache"
	"strings"
	"time"

	"oneclickvirt/global"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"
	"oneclickvirt/model/user"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"go.uber.org/zap"
)

// GetAuthContext 从gin.Context获取认证上下文
func GetAuthContext(c *gin.Context) (*auth.AuthContext, bool) {
	if authCtx, exists := c.Get("auth_context"); exists {
		if authContext, ok := authCtx.(*auth.AuthContext); ok {
			return authContext, true
		}
	}
	return nil, false
}

// GetUserIDFromContext 从认证上下文中获取用户ID（全局统一函数）
func GetUserIDFromContext(c *gin.Context) (uint, error) {
	authCtx, exists := GetAuthContext(c)
	if !exists {
		return 0, fmt.Errorf("用户未认证")
	}
	return authCtx.UserID, nil
}

// RequireAuth 统一的认证中间件
func RequireAuth(minLevel auth.AuthLevel) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 公开访问直接通过
		if minLevel == auth.AuthLevelPublic {
			c.Next()
			return
		}

		// 验证JWT Token并获取最新权限
		authCtx, claims, err := validateJWTTokenWithClaims(c)
		if err != nil {
			respondAuthError(c, err)
			return
		}

		// 检查权限级别
		if !hasRequiredLevel(authCtx, minLevel) {
			global.APP_LOG.Warn("用户权限级别不足",
				zap.Uint("userID", authCtx.UserID),
				zap.String("username", authCtx.Username),
				zap.String("userType", authCtx.UserType),
				zap.String("baseUserType", authCtx.BaseUserType),
				zap.Int("userLevel", authCtx.Level),
				zap.Int("requiredLevel", int(minLevel)),
				zap.String("path", c.Request.URL.Path),
				zap.String("method", c.Request.Method))

			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "权限不足"))
			c.Abort()
			return
		}

		// 检查token是否需要刷新（滑动过期机制）
		if utils.ShouldRefreshToken(claims) {
			// 生成新token
			newToken, err := utils.GenerateToken(authCtx.UserID, authCtx.Username, authCtx.UserType)
			if err != nil {
				global.APP_LOG.Warn("生成刷新token失败",
					zap.Uint("userID", authCtx.UserID),
					zap.Error(err))
			} else {
				// 通过响应头返回新token
				c.Header("X-New-Token", newToken)
				c.Header("X-Token-Refreshed", "true")
				global.APP_LOG.Debug("Token自动刷新",
					zap.Uint("userID", authCtx.UserID),
					zap.String("username", authCtx.Username))
			}
		}

		// 设置认证上下文
		c.Set("auth_context", authCtx)
		c.Set("user_id", authCtx.UserID)
		c.Set("username", authCtx.Username)
		c.Set("user_type", authCtx.UserType)

		c.Next()
	}
}

// RequireResourcePermission 基于资源的权限验证中间件
func RequireResourcePermission(resource string) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 先确保用户已通过基础认证
		authCtx, exists := GetAuthContext(c)
		if !exists {
			common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
			c.Abort()
			return
		}

		// 使用权限服务进行精确的资源权限检查
		permissionService := auth2.PermissionService{}
		path := c.Request.URL.Path
		method := c.Request.Method

		// 检查用户是否有访问该资源的权限
		hasPermission, err := permissionService.CanAccessResource(authCtx.UserID, path, method)
		if err != nil {
			global.APP_LOG.Error("权限检查失败", zap.String("error", utils.FormatError(err)), zap.Uint("userID", authCtx.UserID), zap.String("resource", resource), zap.String("path", path), zap.String("method", method))
			common.ResponseWithError(c, common.NewError(common.CodeInternalError, "权限检查失败"))
			c.Abort()
			return
		}

		if !hasPermission {
			global.APP_LOG.Debug("用户权限不足", zap.Uint("userID", authCtx.UserID), zap.String("userType", authCtx.UserType), zap.String("resource", resource), zap.String("path", path), zap.String("method", method))
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "权限不足"))
			c.Abort()
			return
		}

		c.Next()
	}
}

// validateJWTTokenWithClaims 验证JWT Token并获取最新用户权限（返回claims用于刷新检查）
func validateJWTTokenWithClaims(c *gin.Context) (*auth.AuthContext, *jwt.MapClaims, error) {
	// 优先从 Authorization 头获取token
	token := c.GetHeader("Authorization")
	if token == "" {
		// 如果头中没有，尝试从查询参数获取（用于 WebSocket 连接）
		token = c.Query("token")
	}

	if token == "" {
		return nil, nil, common.NewError(common.CodeUnauthorized, "未提供认证令牌")
	}

	if after, ok := strings.CutPrefix(token, "Bearer "); ok {
		token = after
	}

	// 使用JWT验证逻辑
	claims, err := utils.ValidateToken(token)
	if err != nil {
		return nil, nil, common.NewError(common.CodeUnauthorized, "无效的认证令牌")
	}

	// 提取JWT Token ID (JTI)用于黑名单检查
	jti, ok := (*claims)["jti"].(string)
	if !ok || jti == "" {
		global.APP_LOG.Error("Token缺少JTI字段",
			zap.Any("claims", *claims))
		return nil, nil, common.NewError(common.CodeUnauthorized, "无效的认证令牌格式")
	}

	// 检查Token是否在黑名单中
	blacklistService := auth2.GetJWTBlacklistService()
	if blacklistService.IsBlacklisted(jti) {
		global.APP_LOG.Error("尝试使用已撤销的Token",
			zap.String("jti", jti))
		return nil, nil, common.NewError(common.CodeUnauthorized, "认证令牌已失效")
	}

	// 提取用户ID
	userID, ok := (*claims)["user_id"].(float64)
	if !ok {
		return nil, nil, common.NewError(common.CodeUnauthorized, "无效的用户信息")
	}

	// 提取 token 的签发时间（用于检查用户级吸销）
	var issuedAt time.Time
	if iat, ok := (*claims)["iat"].(float64); ok {
		issuedAt = time.Unix(int64(iat), 0)
	}

	// 从数据库获取用户当前状态和权限（不依赖JWT中的用户类型）
	userAuth, err := getUserAuthInfo(uint(userID), issuedAt)
	if err != nil {
		return nil, nil, common.NewError(common.CodeUnauthorized, "获取用户权限失败")
	}

	return userAuth, claims, nil
}

// getUserAuthInfo 从数据库获取用户认证信息和权限
// issuedAt 为 JWT 的 iat 字段，用于检查是否当前 token 已被用户级吊销
func getUserAuthInfo(userID uint, issuedAt time.Time) (*auth.AuthContext, error) {
	// 尝试从缓存获取（短TTL确保安全性）
	cacheService := cache.GetUserCacheService()
	cacheKey := cache.MakeUserAuthContextKey(userID)
	if cached, ok := cacheService.Get(cacheKey); ok {
		if authCtx, ok := cached.(*auth.AuthContext); ok {
			// 检查 token 是否已被用户级吊销
			if authCtx.TokensInvalidatedAt != nil && !issuedAt.IsZero() && issuedAt.Before(*authCtx.TokensInvalidatedAt) {
				return nil, fmt.Errorf("认证令牌已失效")
			}
			return authCtx, nil
		}
	}

	// 获取用户基本信息和状态（含 tokens_invalidated_at）
	var u user.User
	if err := global.APP_DB.Select("id, username, user_type, status, level, tokens_invalidated_at").First(&u, userID).Error; err != nil {
		// 使用Debug级别，因为这可能是过期token导致的正常情况
		global.APP_LOG.Debug("用户不存在或查询失败(可能是过期token)",
			zap.Uint("userID", userID),
			zap.Error(err))
		return nil, fmt.Errorf("用户不存在")
	}

	// 检查 token 是否在用户级吊销之前签发
	if u.TokensInvalidatedAt != nil && !issuedAt.IsZero() && issuedAt.Before(*u.TokensInvalidatedAt) {
		global.APP_LOG.Warn("用户尝试使用已吊销的Token",
			zap.Uint("userID", userID),
			zap.String("username", u.Username))
		return nil, fmt.Errorf("认证令牌已失效")
	}

	// 严格检查用户状态
	if u.Status != 1 {
		global.APP_LOG.Warn("用户账户已被禁用",
			zap.Uint("userID", userID),
			zap.String("username", u.Username),
			zap.Int("status", u.Status))
		return nil, fmt.Errorf("账户已被禁用")
	}

	// 使用权限服务获取用户有效权限（服务端独立验证）
	permissionService := auth2.PermissionService{}
	effectivePermission, err := permissionService.GetUserEffectivePermission(userID)
	if err != nil {
		// 权限服务失败时，记录详细日志并拒绝访问
		global.APP_LOG.Error("权限服务失败，拒绝访问以确保安全",
			zap.Uint("userID", userID),
			zap.String("username", u.Username),
			zap.Error(err))

		// 严格的兜底策略：权限服务失败时直接拒绝访问
		return nil, fmt.Errorf("权限验证失败，请稍后重试")
	}

	// 验证权限的一致性（防止权限服务返回异常数据）
	if effectivePermission.UserID != userID {
		global.APP_LOG.Error("权限服务返回的用户ID不匹配",
			zap.Uint("requestUserID", userID),
			zap.Uint("returnedUserID", effectivePermission.UserID))
		return nil, fmt.Errorf("权限验证失败")
	}

	// 确保有效权限类型是合法的
	validTypes := map[string]bool{"user": true, "admin": true, "normal_admin": true}
	if !validTypes[effectivePermission.EffectiveType] {
		global.APP_LOG.Error("权限服务返回无效的权限类型，拒绝访问",
			zap.Uint("userID", userID),
			zap.String("invalidType", effectivePermission.EffectiveType),
			zap.String("baseType", u.UserType))
		return nil, fmt.Errorf("权限类型无效")
	}

	// 双重验证管理员权限
	if effectivePermission.EffectiveType == "admin" || effectivePermission.EffectiveType == "normal_admin" {
		if !permissionService.VerifyAdminPrivilege(userID) {
			global.APP_LOG.Warn("管理员权限验证失败，降级为普通用户权限",
				zap.Uint("userID", userID),
				zap.String("username", u.Username))
			effectivePermission.EffectiveType = "user"
			effectivePermission.EffectiveLevel = 1
		}
	}

	// 构建认证上下文（含 TokensInvalidatedAt 以支持缓存命中）
	authCtx := &auth.AuthContext{
		UserID:              u.ID,
		Username:            u.Username,
		UserType:            effectivePermission.EffectiveType,
		Level:               effectivePermission.EffectiveLevel,
		BaseUserType:        u.UserType,
		AllUserTypes:        effectivePermission.AllTypes,
		IsEffective:         true,
		TokensInvalidatedAt: u.TokensInvalidatedAt,
	}

	// 记录权限获取成功的调试信息（仅在开发环境）
	if global.GetAppConfig().System.Env == "debug" {
		global.APP_LOG.Debug("用户权限验证成功",
			zap.Uint("userID", authCtx.UserID),
			zap.String("username", authCtx.Username),
			zap.String("effectiveType", authCtx.UserType),
			zap.Int("effectiveLevel", authCtx.Level),
			zap.String("baseType", authCtx.BaseUserType),
			zap.Strings("allTypes", authCtx.AllUserTypes))
	}

	// 缓存认证上下文（仅缓存成功的结果）
	cacheService.Set(cacheKey, authCtx, cache.TTLUserAuthContext)

	return authCtx, nil
}

// hasRequiredLevel 检查是否有足够的权限级别
func hasRequiredLevel(authCtx *auth.AuthContext, minLevel auth.AuthLevel) bool {
	// 检查用户是否有效
	if !authCtx.IsEffective {
		return false
	}

	// 根据有效权限类型获取权限级别（完全基于数据库查询的结果）
	actualLevel := getUserLevel(authCtx.UserType)

	// 双重验证：检查从权限服务获取的级别和类型计算的级别
	if authCtx.Level > 0 {
		// 使用权限服务计算的级别和类型级别中的最高值
		typeLevel := int(actualLevel)
		if authCtx.Level > typeLevel {
			actualLevel = auth.AuthLevel(authCtx.Level)
		}
	}

	return actualLevel >= minLevel
}

// getUserLevel 根据用户类型获取权限级别
func getUserLevel(userType string) auth.AuthLevel {
	switch userType {
	case "admin":
		return auth.AuthLevelAdmin
	case "normal_admin":
		return auth.AuthLevelNormalAdmin
	case "user":
		return auth.AuthLevelUser
	default:
		return auth.AuthLevelPublic
	}
}

// respondAuthError 统一的认证错误响应
func respondAuthError(c *gin.Context, err error) {
	common.ResponseWithError(c, err)
	c.Abort()
}

// RequireKYC 实名认证检查中间件（仅在系统启用且要求时检查）
func RequireKYC() gin.HandlerFunc {
	return func(c *gin.Context) {
		kycConfig := global.GetAppConfig().KYC
		if !kycConfig.EnableRealName || !kycConfig.RequireRealName {
			c.Next()
			return
		}
		if checkKYCPass(c) {
			c.Next()
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "请先完成实名认证"))
		c.Abort()
	}
}

// RequireKYCFor 细粒度实名检查，仅在对应限制开启时要求实名
func RequireKYCFor(restrictionField string) gin.HandlerFunc {
	return func(c *gin.Context) {
		kycConfig := global.GetAppConfig().KYC
		if !kycConfig.EnableRealName {
			c.Next()
			return
		}
		restricted := false
		switch restrictionField {
		case "create-instance":
			restricted = kycConfig.RestrictCreateInstance
		case "redeem-code":
			restricted = kycConfig.RestrictRedeemCode
		case "domain-bind":
			restricted = kycConfig.RestrictDomainBind
		}
		if !restricted {
			c.Next()
			return
		}
		if checkKYCPass(c) {
			c.Next()
			return
		}
		common.ResponseWithError(c, common.NewError(common.CodeForbidden, "请先完成实名认证"))
		c.Abort()
	}
}

func checkKYCPass(c *gin.Context) bool {
	authCtx, exists := GetAuthContext(c)
	if !exists {
		return false
	}
	// 管理员免检
	if authCtx.UserType == "admin" || authCtx.UserType == "normal_admin" {
		return true
	}
	var u user.User
	if err := global.APP_DB.Select("real_name_verified").First(&u, authCtx.UserID).Error; err != nil {
		return false
	}
	return u.RealNameVerified
}

// RequireNormalAdmin 普通管理员权限中间件（>=normal_admin级别）
func RequireNormalAdmin() gin.HandlerFunc {
	return RequireAuth(auth.AuthLevelNormalAdmin)
}

// RequireSuperAdmin 超级管理员权限中间件（仅admin级别,排除normal_admin）
// 内部先完成JWT验证并设置auth_context，再检查是否为超级管理员
func RequireSuperAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		// 先验证JWT Token并获取认证上下文
		authCtx, claims, err := validateJWTTokenWithClaims(c)
		if err != nil {
			respondAuthError(c, err)
			return
		}

		// 检查是否为超级管理员
		if authCtx.UserType != "admin" {
			common.ResponseWithError(c, common.NewError(common.CodeForbidden, "需要超级管理员权限"))
			c.Abort()
			return
		}

		// 检查token是否需要刷新
		if utils.ShouldRefreshToken(claims) {
			if newToken, err := utils.GenerateToken(authCtx.UserID, authCtx.Username, authCtx.UserType); err == nil {
				c.Header("X-New-Token", newToken)
				c.Header("X-Token-Refreshed", "true")
			}
		}

		// 设置认证上下文
		c.Set("auth_context", authCtx)
		c.Set("user_id", authCtx.UserID)
		c.Set("username", authCtx.Username)
		c.Set("user_type", authCtx.UserType)

		c.Next()
	}
}

// IsSuperAdmin 检查是否为超级管理员
func IsSuperAdmin(c *gin.Context) bool {
	authCtx, exists := GetAuthContext(c)
	if !exists {
		return false
	}
	return authCtx.UserType == "admin"
}

// IsNormalAdmin 检查是否为普通管理员
func IsNormalAdmin(c *gin.Context) bool {
	authCtx, exists := GetAuthContext(c)
	if !exists {
		return false
	}
	return authCtx.UserType == "normal_admin"
}

// GetOwnerAdminID 获取普通管理员的用户ID(用于节点隔离查询)
func GetOwnerAdminID(c *gin.Context) uint {
	authCtx, exists := GetAuthContext(c)
	if !exists {
		return 0
	}
	if authCtx.UserType == "normal_admin" {
		return authCtx.UserID
	}
	return 0 // 超级管理员返回0,不过滤
}
