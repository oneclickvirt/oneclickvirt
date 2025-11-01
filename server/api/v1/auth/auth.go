package auth

import (
	auth2 "oneclickvirt/service/auth"
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/middleware"
	"oneclickvirt/model/auth"
	"oneclickvirt/model/common"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// Login 用户登录
// @Summary 用户登录
// @Description 用户登录接口，验证用户名密码并返回JWT token
// @Tags 认证管理
// @Accept json
// @Produce json
// @Param request body auth.LoginRequest true "登录请求参数"
// @Success 200 {object} common.Response{data=object} "登录成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 401 {object} common.Response "认证失败"
// @Router /auth/login [post]
func Login(c *gin.Context) {
	var req auth.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		global.APP_LOG.Debug("登录请求参数错误", zap.String("error", err.Error()), zap.String("ip", c.ClientIP()))
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	authService := auth2.AuthService{}
	user, token, err := authService.Login(req)
	if err != nil {
		global.APP_LOG.Warn("用户登录失败",
			zap.String("username", req.Username),
			zap.String("error", err.Error()),
			zap.String("ip", c.ClientIP()))

		// 使用认证验证服务分类错误
		authValidationService := auth2.AuthValidationService{}
		appErr := authValidationService.ClassifyLoginError(err)
		common.ResponseWithError(c, appErr)
		return
	}

	global.APP_LOG.Info("用户登录成功",
		zap.String("username", req.Username),
		zap.Uint("user_id", user.ID),
		zap.String("ip", c.ClientIP()))

	common.ResponseSuccess(c, gin.H{
		"user":  user,
		"token": token,
	})
}

// ForgotPassword 忘记密码
// @Summary 忘记密码
// @Description 发送密码重置邮件
// @Tags 认证管理
// @Accept json
// @Produce json
// @Param request body auth.ForgotPasswordRequest true "忘记密码请求参数"
// @Success 200 {object} common.Response "重置邮件发送成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /auth/forgot-password [post]
func ForgotPassword(c *gin.Context) {
	var req auth.ForgotPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	authService := auth2.AuthService{}
	if err := authService.ForgotPassword(req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}
	common.ResponseSuccess(c, nil, "密码重置邮件已发送")
}

// ResetPassword 重置密码
// @Summary 重置密码
// @Description 使用重置令牌自动生成新密码并通过用户绑定的通信渠道发送
// @Tags 认证管理
// @Accept json
// @Produce json
// @Param request body auth.ResetPasswordRequest true "重置密码请求参数"
// @Success 200 {object} common.Response "密码重置成功，新密码已发送到绑定的通信渠道"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /auth/reset-password [post]
func ResetPassword(c *gin.Context) {
	var req auth.ResetPasswordRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}
	authService := auth2.AuthService{}
	if err := authService.ResetPasswordWithToken(req.Token); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}
	common.ResponseSuccess(c, nil, "密码重置成功，新密码已发送到您绑定的通信渠道")
}

// Register 用户注册
// @Summary 用户注册
// @Description 创建新用户账户
// @Tags 认证管理
// @Accept json
// @Produce json
// @Param request body auth.RegisterRequest true "注册请求参数"
// @Success 200 {object} common.Response{data=object} "注册成功，返回用户信息和token"
// @Failure 400 {object} common.Response "请求参数错误或注册失败"
// @Router /auth/register [post]
func Register(c *gin.Context) {
	var req auth.RegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	// 使用认证验证服务检查验证码
	authValidationService := auth2.AuthValidationService{}
	if err := authValidationService.ValidateCaptchaRequired(req.CaptchaId, req.Captcha); err != nil {
		common.ResponseWithError(c, err)
		return
	}

	authService := auth2.AuthService{}
	user, token, err := authService.RegisterAndLogin(req, c.ClientIP(), c.GetHeader("User-Agent"))
	if err != nil {
		global.APP_LOG.Warn("用户注册失败",
			zap.String("username", req.Username),
			zap.String("error", err.Error()),
			zap.String("ip", c.ClientIP()))

		// 使用认证验证服务分类错误
		appErr := authValidationService.ClassifyAuthError(err)
		common.ResponseWithError(c, appErr)
		return
	}

	global.APP_LOG.Info("用户注册成功",
		zap.String("username", req.Username),
		zap.Uint("user_id", user.ID),
		zap.String("ip", c.ClientIP()))

	common.ResponseSuccess(c, gin.H{
		"user":  user,
		"token": token,
	}, "注册成功")
}

// GetCaptcha 获取验证码
// @Summary 获取验证码
// @Description 生成图片验证码用于用户验证
// @Tags 认证管理
// @Accept json
// @Produce json
// @Param width query int false "验证码宽度" default(120)
// @Param height query int false "验证码高度" default(40)
// @Success 200 {object} common.Response{data=object} "验证码生成成功"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /auth/captcha [get]
func GetCaptcha(c *gin.Context) {
	var req auth.GetCaptchaRequest
	if err := c.ShouldBindQuery(&req); err != nil {
		req.Width = 120
		req.Height = 40
	}
	authService := auth2.AuthService{}
	captcha, err := authService.GenerateCaptcha(req.Width, req.Height)
	if err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}
	common.ResponseSuccess(c, captcha)
}

// Logout 用户登出
// @Summary 用户登出
// @Description 撤销当前用户的JWT token并登出
// @Tags 认证管理
// @Accept json
// @Produce json
// @Security BearerAuth
// @Success 200 {object} common.Response "登出成功"
// @Failure 401 {object} common.Response "用户未认证"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /auth/logout [post]
func Logout(c *gin.Context) {
	// 获取认证上下文
	authCtx, exists := middleware.GetAuthContext(c)
	if !exists {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "用户未认证"))
		return
	}

	// 获取当前Token
	token := c.GetHeader("Authorization")
	if token == "" {
		common.ResponseWithError(c, common.NewError(common.CodeUnauthorized, "未提供认证令牌"))
		return
	}

	if after, ok := strings.CutPrefix(token, "Bearer "); ok {
		token = after
	}

	// 将Token添加到黑名单
	blacklistService := auth2.GetJWTBlacklistService()
	if err := blacklistService.AddToBlacklist(token, authCtx.UserID, "logout", authCtx.UserID); err != nil {
		global.APP_LOG.Error("添加Token到黑名单失败",
			zap.Error(err),
			zap.Uint("userID", authCtx.UserID))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, "登出失败，请稍后重试"))
		return
	}

	global.APP_LOG.Info("用户登出成功",
		zap.Uint("userID", authCtx.UserID),
		zap.String("username", authCtx.Username))

	common.ResponseSuccess(c, nil, "登出成功")
}

// SendVerifyCode 发送验证码
// @Summary 发送验证码
// @Description 向指定的邮箱/Telegram/QQ发送登录验证码
// @Tags 认证管理
// @Accept json
// @Produce json
// @Param request body auth.SendVerifyCodeRequest true "发送验证码请求参数"
// @Success 200 {object} common.Response "验证码发送成功"
// @Failure 400 {object} common.Response "请求参数错误"
// @Failure 500 {object} common.Response "服务器内部错误"
// @Router /auth/send-verify-code [post]
func SendVerifyCode(c *gin.Context) {
	var req auth.SendVerifyCodeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		common.ResponseWithError(c, common.NewError(common.CodeValidationError, err.Error()))
		return
	}

	// 验证图形验证码（开发模式下可以跳过）
	if req.CaptchaId != "" && req.Captcha != "" {
		// 使用验证服务验证图形验证码
		authValidationService := auth2.AuthValidationService{}
		if err := authValidationService.ValidateCaptchaRequired(req.CaptchaId, req.Captcha); err != nil {
			common.ResponseWithError(c, err)
			return
		}
	} else if global.APP_CONFIG.System.Env != "development" {
		// 只在非开发环境下强制要求验证码
		common.ResponseWithError(c, common.NewError(common.CodeCaptchaRequired, "请填写图形验证码"))
		return
	}

	authService := auth2.AuthService{}
	if err := authService.SendVerifyCode(req.Type, req.Target); err != nil {
		global.APP_LOG.Warn("发送验证码失败",
			zap.String("type", req.Type),
			zap.String("target", req.Target),
			zap.String("error", err.Error()),
			zap.String("ip", c.ClientIP()))
		common.ResponseWithError(c, common.NewError(common.CodeInternalError, err.Error()))
		return
	}

	global.APP_LOG.Info("验证码发送成功",
		zap.String("type", req.Type),
		zap.String("target", req.Target),
		zap.String("ip", c.ClientIP()))

	common.ResponseSuccess(c, nil, "验证码已发送，请查收")
}
