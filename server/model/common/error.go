package common

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

// 错误码定义
const (
	// 成功
	CodeSuccess = 0

	// 通用错误 1000-1999
	CodeError           = 1000
	CodeInvalidParam    = 1001
	CodeInternalError   = 1002
	CodeUnauthorized    = 1003
	CodeForbidden       = 1004
	CodeNotFound        = 1005
	CodeConflict        = 1006
	CodeValidationError = 1007

	// 用户相关错误 2000-2999
	CodeUserNotFound       = 2001
	CodeUserExists         = 2002
	CodeUsernameExists     = 2003
	CodeInvalidCredentials = 2004
	CodeUserDisabled       = 2005
	CodeUserPermissionDeny = 2006

	// 角色权限相关错误 3000-3999
	CodeRoleNotFound       = 3001
	CodeRoleExists         = 3002
	CodePermissionDeny     = 3003
	CodeInvalidRole        = 3004
	CodeRoleInUse          = 3005
	CodePermissionNotFound = 3006

	// 业务相关错误 4000-4999
	CodeInviteCodeInvalid       = 4001
	CodeInviteCodeExpired       = 4002
	CodeInviteCodeUsed          = 4003
	CodeCaptchaInvalid          = 4004
	CodeCaptchaRequired         = 4005
	CodeTokenGenerateError      = 4006
	CodeOAuth2Failed            = 4007 // OAuth2认证失败
	CodeOAuth2RegistrationLimit = 4008 // OAuth2注册已达限制

	// 系统相关错误 5000-5999
	CodeConfigError      = 5001
	CodeDatabaseError    = 5002
	CodeCacheError       = 5003
	CodeExternalAPIError = 5004
	CodeRequestTooLarge  = 5005
)

// 错误信息映射
var ErrorMessages = map[int]string{
	CodeSuccess:                 "操作成功",
	CodeError:                   "操作失败",
	CodeInvalidParam:            "请求参数错误",
	CodeInternalError:           "系统内部错误",
	CodeUnauthorized:            "未授权访问",
	CodeForbidden:               "禁止访问",
	CodeNotFound:                "资源不存在",
	CodeConflict:                "资源冲突",
	CodeValidationError:         "数据验证失败",
	CodeUserNotFound:            "用户不存在",
	CodeUserExists:              "用户已存在",
	CodeUsernameExists:          "用户名已存在",
	CodeInvalidCredentials:      "用户名或密码错误",
	CodeUserDisabled:            "用户已被禁用",
	CodeUserPermissionDeny:      "用户权限不足",
	CodeRoleNotFound:            "角色不存在",
	CodeRoleExists:              "角色已存在",
	CodePermissionDeny:          "权限不足",
	CodeInvalidRole:             "无效的角色",
	CodeRoleInUse:               "角色正在使用中，无法删除",
	CodePermissionNotFound:      "权限不存在",
	CodeInviteCodeInvalid:       "邀请码无效",
	CodeInviteCodeExpired:       "邀请码已过期",
	CodeInviteCodeUsed:          "邀请码已被使用",
	CodeCaptchaInvalid:          "验证码错误",
	CodeCaptchaRequired:         "请提供验证码",
	CodeTokenGenerateError:      "令牌生成失败",
	CodeOAuth2Failed:            "OAuth2认证失败",
	CodeOAuth2RegistrationLimit: "OAuth2注册已达到限制",
	CodeConfigError:             "配置错误",
	CodeDatabaseError:           "数据库错误",
	CodeCacheError:              "缓存错误",
	CodeExternalAPIError:        "外部API调用失败",
	CodeRequestTooLarge:         "请求数据过大",
}

// AppError 统一错误结构
type AppError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

func (e *AppError) Error() string {
	if e.Details != "" {
		return fmt.Sprintf("[%d] %s: %s", e.Code, e.Message, e.Details)
	}
	return fmt.Sprintf("[%d] %s", e.Code, e.Message)
}

// NewError 创建新的错误
func NewError(code int, details ...string) *AppError {
	message := ErrorMessages[code]
	if message == "" {
		message = "未知错误"
	}

	err := &AppError{
		Code:    code,
		Message: message,
	}

	if len(details) > 0 {
		err.Details = details[0]
	}

	return err
}

// 统一响应函数
func ResponseWithError(c *gin.Context, err error) {
	if appErr, ok := err.(*AppError); ok {
		httpCode := getHTTPCode(appErr.Code)
		c.JSON(httpCode, gin.H{
			"code":    appErr.Code,
			"message": appErr.Message,
			"details": appErr.Details,
			"data":    nil,
		})
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    CodeInternalError,
			"message": ErrorMessages[CodeInternalError],
			"details": err.Error(),
			"data":    nil,
		})
	}
}

func ResponseSuccess(c *gin.Context, data interface{}, message ...string) {
	msg := ErrorMessages[CodeSuccess]
	if len(message) > 0 {
		msg = message[0]
	}

	c.JSON(http.StatusOK, gin.H{
		"code":    CodeSuccess,
		"message": msg,
		"data":    data,
	})
}

func ResponseSuccessWithPagination(c *gin.Context, data interface{}, total int64, page, pageSize int) {
	c.JSON(http.StatusOK, gin.H{
		"code":    CodeSuccess,
		"message": ErrorMessages[CodeSuccess],
		"data": gin.H{
			"list":     data,
			"total":    total,
			"page":     page,
			"pageSize": pageSize,
		},
	})
}

// 根据错误码获取HTTP状态码
func getHTTPCode(code int) int {
	switch code {
	case CodeInvalidParam, CodeValidationError, CodeCaptchaInvalid, CodeCaptchaRequired, CodeInviteCodeInvalid, CodeInviteCodeExpired, CodeInviteCodeUsed:
		return http.StatusBadRequest
	case CodeUnauthorized, CodeInvalidCredentials:
		return http.StatusUnauthorized
	case CodeForbidden, CodePermissionDeny, CodeUserPermissionDeny, CodeUserDisabled:
		return http.StatusForbidden
	case CodeNotFound, CodeUserNotFound, CodeRoleNotFound, CodePermissionNotFound:
		return http.StatusNotFound
	case CodeConflict, CodeUserExists, CodeUsernameExists, CodeRoleExists:
		return http.StatusConflict
	case CodeRequestTooLarge:
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}
