package middleware

import (
	"fmt"
	"net/http"
	auth2 "oneclickvirt/service/auth"
	"strings"

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

			c.JSON(http.StatusForbidden, common.Response{
				Code: 403,
				Msg:  "权限不足",
			})
			c.Abort()
			return
		}

		// 检查token是否需要刷新（滑动过期机制）
		if utils.ShouldRefreshToken(claims) {
			// 生成新token
			newToken, err := utils.GenerateToken(authCtx.UserID, authCtx.Username, authCtx.UserType)
			if err != nil {
				global.APP_LOG.Error("生成刷新token失败",
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
			c.JSON(http.StatusUnauthorized, common.Response{
				Code: 401,
				Msg:  "用户未认证",
			})
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
			global.APP_LOG.Warn("权限检查失败", zap.String("error", utils.FormatError(err)), zap.Uint("userID", authCtx.UserID), zap.String("resource", resource), zap.String("path", path), zap.String("method", method))
			c.JSON(http.StatusInternalServerError, common.Response{
				Code: 500,
				Msg:  "权限检查失败",
			})
			c.Abort()
			return
		}

		if !hasPermission {
			global.APP_LOG.Debug("用户权限不足", zap.Uint("userID", authCtx.UserID), zap.String("userType", authCtx.UserType), zap.String("resource", resource), zap.String("path", path), zap.String("method", method))
			c.JSON(http.StatusForbidden, common.Response{
				Code: 403,
				Msg:  "权限不足",
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// validateJWTTokenWithClaims 验证JWT Token并获取最新用户权限（返回claims用于刷新检查）
func validateJWTTokenWithClaims(c *gin.Context) (*auth.AuthContext, *jwt.MapClaims, error) {
	token := c.GetHeader("Authorization")
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
		global.APP_LOG.Warn("Token缺少JTI字段",
			zap.Any("claims", *claims))
		return nil, nil, common.NewError(common.CodeUnauthorized, "无效的认证令牌格式")
	}

	// 检查Token是否在黑名单中
	blacklistService := auth2.GetJWTBlacklistService()
	if blacklistService.IsBlacklisted(jti) {
		global.APP_LOG.Warn("尝试使用已撤销的Token",
			zap.String("jti", jti))
		return nil, nil, common.NewError(common.CodeUnauthorized, "认证令牌已失效")
	}

	// 提取用户ID
	userID, ok := (*claims)["user_id"].(float64)
	if !ok {
		return nil, nil, common.NewError(common.CodeUnauthorized, "无效的用户信息")
	}

	// 从数据库获取用户当前状态和权限（不依赖JWT中的用户类型）
	userAuth, err := getUserAuthInfo(uint(userID))
	if err != nil {
		return nil, nil, common.NewError(common.CodeUnauthorized, "获取用户权限失败")
	}

	return userAuth, claims, nil
}

// validateJWTToken 验证JWT Token并获取最新用户权限（向后兼容）
func validateJWTToken(c *gin.Context) (*auth.AuthContext, error) {
	authCtx, _, err := validateJWTTokenWithClaims(c)
	return authCtx, err
}

// getUserAuthInfo 从数据库获取用户认证信息和权限
func getUserAuthInfo(userID uint) (*auth.AuthContext, error) {
	// 获取用户基本信息和状态
	var user user.User
	if err := global.APP_DB.Select("id, username, user_type, status, level").First(&user, userID).Error; err != nil {
		// 使用Debug级别，因为这可能是过期token导致的正常情况
		global.APP_LOG.Debug("用户不存在或查询失败(可能是过期token)",
			zap.Uint("userID", userID),
			zap.Error(err))
		return nil, fmt.Errorf("用户不存在")
	}

	// 严格检查用户状态
	if user.Status != 1 {
		global.APP_LOG.Warn("用户账户已被禁用",
			zap.Uint("userID", userID),
			zap.String("username", user.Username),
			zap.Int("status", user.Status))
		return nil, fmt.Errorf("账户已被禁用")
	}

	// 使用权限服务获取用户有效权限（服务端独立验证）
	permissionService := auth2.PermissionService{}
	effectivePermission, err := permissionService.GetUserEffectivePermission(userID)
	if err != nil {
		// 权限服务失败时，记录详细日志并拒绝访问
		global.APP_LOG.Error("权限服务失败，拒绝访问以确保安全",
			zap.Uint("userID", userID),
			zap.String("username", user.Username),
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
	validTypes := map[string]bool{"user": true, "admin": true}
	if !validTypes[effectivePermission.EffectiveType] {
		global.APP_LOG.Error("权限服务返回无效的权限类型，拒绝访问",
			zap.Uint("userID", userID),
			zap.String("invalidType", effectivePermission.EffectiveType),
			zap.String("baseType", user.UserType))
		return nil, fmt.Errorf("权限类型无效")
	}

	// 双重验证管理员权限
	if effectivePermission.EffectiveType == "admin" {
		if !permissionService.VerifyAdminPrivilege(userID) {
			global.APP_LOG.Warn("管理员权限验证失败，降级为普通用户权限",
				zap.Uint("userID", userID),
				zap.String("username", user.Username))
			effectivePermission.EffectiveType = "user"
			effectivePermission.EffectiveLevel = 1
		}
	}

	// 构建认证上下文
	authCtx := &auth.AuthContext{
		UserID:       user.ID,
		Username:     user.Username,
		UserType:     effectivePermission.EffectiveType,
		Level:        effectivePermission.EffectiveLevel,
		BaseUserType: user.UserType,
		AllUserTypes: effectivePermission.AllTypes,
		IsEffective:  true,
	}

	// 记录权限获取成功的调试信息（仅在开发环境）
	if global.APP_CONFIG.System.Env == "debug" {
		global.APP_LOG.Debug("用户权限验证成功",
			zap.Uint("userID", authCtx.UserID),
			zap.String("username", authCtx.Username),
			zap.String("effectiveType", authCtx.UserType),
			zap.Int("effectiveLevel", authCtx.Level),
			zap.String("baseType", authCtx.BaseUserType),
			zap.Strings("allTypes", authCtx.AllUserTypes))
	}

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
	case "user":
		return auth.AuthLevelUser
	default:
		return auth.AuthLevelPublic
	}
}

// respondAuthError 统一的认证错误响应
func respondAuthError(c *gin.Context, err error) {
	if appErr, ok := err.(*common.AppError); ok {
		httpCode := http.StatusUnauthorized
		if appErr.Code == common.CodeForbidden {
			httpCode = http.StatusForbidden
		}
		c.JSON(httpCode, common.Response{
			Code: appErr.Code,
			Msg:  appErr.Message,
		})
	} else {
		c.JSON(http.StatusInternalServerError, common.Response{
			Code: common.CodeInternalError,
			Msg:  "认证失败",
		})
	}
	c.Abort()
}
