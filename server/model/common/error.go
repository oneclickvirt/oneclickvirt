package common

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 统一错误码 — 直接使用 HTTP 状态码，不再使用自定义错误码
const (
	CodeSuccess         = http.StatusOK                    // 200
	CodeBadRequest      = http.StatusBadRequest            // 400
	CodeUnauthorized    = http.StatusUnauthorized          // 401
	CodeForbidden       = http.StatusForbidden             // 403
	CodeNotFound        = http.StatusNotFound              // 404
	CodeConflict        = http.StatusConflict              // 409
	CodeTooLarge        = http.StatusRequestEntityTooLarge // 413
	CodeTooManyRequests = http.StatusTooManyRequests       // 429
	CodeInternalError   = http.StatusInternalServerError   // 500
	CodeBadGateway      = http.StatusBadGateway            // 502
	CodeUnavailable     = http.StatusServiceUnavailable    // 503

	// 向后兼容别名 — 所有旧常量映射到对应的 HTTP 状态码
	CodeError                   = CodeBadRequest    // was 1000
	CodeInvalidParam            = CodeBadRequest    // was 1001
	CodeValidationError         = CodeBadRequest    // was 1007
	CodeUserNotFound            = CodeNotFound      // was 2001
	CodeUserExists              = CodeConflict      // was 2002
	CodeUsernameExists          = CodeConflict      // was 2003
	CodeInvalidCredentials      = CodeUnauthorized  // was 2004
	CodeUserDisabled            = CodeForbidden     // was 2005
	CodeUserPermissionDeny      = CodeForbidden     // was 2006
	CodeRoleNotFound            = CodeNotFound      // was 3001
	CodeRoleExists              = CodeConflict      // was 3002
	CodePermissionDeny          = CodeForbidden     // was 3003
	CodeInvalidRole             = CodeBadRequest    // was 3004
	CodeRoleInUse               = CodeConflict      // was 3005
	CodePermissionNotFound      = CodeNotFound      // was 3006
	CodeInviteCodeInvalid       = CodeBadRequest    // was 4001
	CodeInviteCodeExpired       = CodeBadRequest    // was 4002
	CodeInviteCodeUsed          = CodeBadRequest    // was 4003
	CodeCaptchaInvalid          = CodeBadRequest    // was 4004
	CodeCaptchaRequired         = CodeBadRequest    // was 4005
	CodeTokenGenerateError      = CodeInternalError // was 4006
	CodeOAuth2Failed            = CodeBadRequest    // was 4007
	CodeOAuth2RegistrationLimit = CodeBadRequest    // was 4008
	CodeConfigError             = CodeBadRequest    // was 5001
	CodeDatabaseError           = CodeUnavailable   // was 5002
	CodeCacheError              = CodeUnavailable   // was 5003
	CodeExternalAPIError        = CodeBadGateway    // was 5004
	CodeRequestTooLarge         = CodeTooLarge      // was 5005
	CodeProviderHasInstances    = CodeConflict      // was 40901
)

// 错误信息映射
var ErrorMessages = map[int]string{
	CodeSuccess:         "操作成功",
	CodeBadRequest:      "数据验证失败",
	CodeUnauthorized:    "未授权访问",
	CodeForbidden:       "禁止访问",
	CodeNotFound:        "资源不存在",
	CodeConflict:        "资源冲突",
	CodeTooLarge:        "请求数据过大",
	CodeTooManyRequests: "请求过于频繁，请稍后重试",
	CodeInternalError:   "系统内部错误",
	CodeBadGateway:      "外部API调用失败",
	CodeUnavailable:     "服务暂时不可用",
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

// NewError 创建新的错误，code 直接使用 HTTP 状态码
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

// NewErrorWithMessage 创建指定 message 的错误
func NewErrorWithMessage(code int, message string, details ...string) *AppError {
	err := &AppError{
		Code:    code,
		Message: message,
	}
	if len(details) > 0 {
		err.Details = details[0]
	}
	return err
}

// ClassifyError maps a raw error to an AppError with an appropriate HTTP status code.
// Already-wrapped AppErrors pass through unchanged.
func ClassifyError(err error) *AppError {
	if err == nil {
		return nil
	}
	if appErr, ok := err.(*AppError); ok {
		return appErr
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return NewError(CodeNotFound)
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	// Not found patterns
	if strings.Contains(msg, "不存在") || strings.Contains(msg, "找不到") || strings.Contains(msg, "未找到") ||
		strings.Contains(msg, "暂无") || strings.Contains(msg, "无此") ||
		strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist") ||
		strings.Contains(lower, "no such") {
		return NewError(CodeNotFound, msg)
	}
	// Conflict patterns
	if strings.Contains(msg, "已存在") || strings.Contains(msg, "重复") ||
		strings.Contains(msg, "已被绑定") || strings.Contains(msg, "已通过") ||
		strings.Contains(lower, "already exists") || strings.Contains(lower, "duplicate") ||
		strings.Contains(lower, "conflict") {
		return NewError(CodeConflict, msg)
	}
	// Forbidden / permission patterns
	if strings.Contains(msg, "无权限") || strings.Contains(msg, "权限不足") ||
		strings.Contains(msg, "禁止") || strings.Contains(msg, "无权") ||
		strings.Contains(lower, "forbidden") ||
		strings.Contains(lower, "permission denied") || strings.Contains(lower, "access denied") {
		return NewError(CodeForbidden, msg)
	}
	// Unauthorized patterns
	if strings.Contains(msg, "未授权") || strings.Contains(msg, "未登录") ||
		strings.Contains(lower, "unauthorized") || strings.Contains(lower, "unauthenticated") {
		return NewError(CodeUnauthorized, msg)
	}
	// Validation / bad request patterns
	if strings.Contains(msg, "参数") || strings.Contains(msg, "无效") ||
		strings.Contains(msg, "格式错误") || strings.Contains(msg, "不能为空") ||
		strings.Contains(msg, "尚未完成") || strings.Contains(msg, "不支持") ||
		strings.Contains(msg, "未启用") || strings.Contains(msg, "未配置") ||
		strings.Contains(msg, "超过") || strings.Contains(msg, "过长") ||
		strings.Contains(msg, "已被冻结") || strings.Contains(msg, "已过期") ||
		strings.Contains(msg, "无法") || strings.Contains(msg, "已达到") ||
		strings.Contains(msg, "不满足") || strings.Contains(msg, "密码") ||
		strings.Contains(msg, "不允许") || strings.Contains(msg, "不能") ||
		strings.Contains(msg, "已被使用") ||
		strings.Contains(lower, "invalid") || strings.Contains(lower, "required") ||
		strings.Contains(lower, "validation") || strings.Contains(lower, "too long") ||
		strings.Contains(lower, "exceeded") || strings.Contains(lower, "frozen") ||
		strings.Contains(lower, "expired") || strings.Contains(lower, "not allowed") ||
		strings.Contains(lower, "no enabled") {
		return NewError(CodeBadRequest, msg)
	}
	return NewError(CodeInternalError, msg)
}

// 统一响应函数 — code 字段直接使用 HTTP 状态码
func ResponseWithError(c *gin.Context, err error) {
	if appErr, ok := err.(*AppError); ok {
		c.JSON(appErr.Code, gin.H{
			"code":    appErr.Code,
			"msg":     appErr.Message,
			"message": appErr.Message,
			"details": appErr.Details,
			"data":    nil,
		})
	} else {
		msg := ErrorMessages[CodeInternalError]
		c.JSON(http.StatusInternalServerError, gin.H{
			"code":    http.StatusInternalServerError,
			"msg":     msg,
			"message": msg,
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
		"code":    http.StatusOK,
		"msg":     msg,
		"message": msg,
		"data":    data,
	})
}

func ResponseSuccessWithPagination(c *gin.Context, data interface{}, total int64, page, pageSize int) {
	msg := ErrorMessages[CodeSuccess]
	c.JSON(http.StatusOK, gin.H{
		"code":    http.StatusOK,
		"msg":     msg,
		"message": msg,
		"data": gin.H{
			"list":     data,
			"total":    total,
			"page":     page,
			"pageSize": pageSize,
		},
	})
}
