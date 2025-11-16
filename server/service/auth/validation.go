package auth

import (
	"strings"

	"oneclickvirt/global"
	"oneclickvirt/model/common"
)

type AuthValidationService struct{}

// ShouldCheckCaptcha 检查是否需要验证码
func (s *AuthValidationService) ShouldCheckCaptcha() bool {
	return global.APP_CONFIG.System.Env != "development"
}

// ValidateCaptchaRequired 验证验证码是否必需
func (s *AuthValidationService) ValidateCaptchaRequired(captchaId, captcha string) *common.AppError {
	if s.ShouldCheckCaptcha() {
		if captchaId == "" || captcha == "" {
			return common.NewError(common.CodeCaptchaRequired, "请填写验证码")
		}
		// 实际验证验证码
		authService := AuthService{}
		if err := authService.verifyCaptcha(captchaId, captcha); err != nil {
			return common.NewError(common.CodeCaptchaInvalid, err.Error())
		}
	}
	return nil
}

// ClassifyAuthError 分类认证错误
func (s *AuthValidationService) ClassifyAuthError(err error) *common.AppError {
	if err == nil {
		return nil
	}

	// 检查是否是AppError类型
	if appErr, ok := err.(*common.AppError); ok {
		return appErr
	}

	// 根据错误信息返回不同的错误码
	errMsg := err.Error()
	switch {
	case errMsg == "用户名已存在":
		return common.NewError(common.CodeUserExists, errMsg)
	case errMsg == "注册功能已被禁用":
		return common.NewError(common.CodeForbidden, errMsg)
	case errMsg == "验证码错误" || errMsg == "验证码已过期":
		return common.NewError(common.CodeCaptchaInvalid, errMsg)
	case errMsg == "邀请码不能为空" || errMsg == "邀请码无效" || errMsg == "邀请码已被使用" || errMsg == "邀请码已达到最大使用次数" || errMsg == "邀请码已过期":
		return common.NewError(common.CodeInviteCodeInvalid, errMsg)
	case strings.Contains(errMsg, "密码长度") || strings.Contains(errMsg, "密码必须包含") || strings.Contains(errMsg, "密码不能包含"):
		return common.NewError(common.CodeValidationError, errMsg)
	case errMsg == "用户已被禁用，有问题请联系管理员":
		return common.NewError(common.CodeUserDisabled, errMsg)
	default:
		return common.NewError(common.CodeInternalError, errMsg)
	}
}

// ClassifyLoginError 分类登录错误
func (s *AuthValidationService) ClassifyLoginError(err error) *common.AppError {
	if err == nil {
		return nil
	}

	// 检查是否是AppError类型
	if appErr, ok := err.(*common.AppError); ok {
		return appErr
	}

	errMsg := err.Error()

	// 根据错误信息返回不同的错误码
	switch {
	case errMsg == "用户已被禁用，有问题请联系管理员":
		return common.NewError(common.CodeUserDisabled, errMsg)
	case errMsg == "验证码错误" || errMsg == "验证码已过期":
		return common.NewError(common.CodeCaptchaInvalid, errMsg)
	case errMsg == "请填写验证码":
		return common.NewError(common.CodeCaptchaRequired, errMsg)
	default:
		// 默认返回用户名或密码错误
		return common.NewError(common.CodeInvalidCredentials, "用户名或密码错误")
	}
}
