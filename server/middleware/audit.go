package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"oneclickvirt/global"
	adminModel "oneclickvirt/model/admin"
	"oneclickvirt/utils"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	maxAuditPayloadBytes = 8192
	auditRedactedValue   = "[REDACTED]"
	auditTruncatedValue  = "[TRUNCATED]"
)

type auditResponseWriter struct {
	gin.ResponseWriter
	body bytes.Buffer
}

func newAuditResponseWriter(w gin.ResponseWriter) *auditResponseWriter {
	return &auditResponseWriter{ResponseWriter: w}
}

func (w *auditResponseWriter) Write(data []byte) (int, error) {
	w.capture(data)
	return w.ResponseWriter.Write(data)
}

func (w *auditResponseWriter) WriteString(data string) (int, error) {
	w.capture([]byte(data))
	return w.ResponseWriter.WriteString(data)
}

func (w *auditResponseWriter) BodyString() string {
	return w.body.String()
}

func (w *auditResponseWriter) capture(data []byte) {
	if len(data) == 0 || w.body.Len() >= maxAuditPayloadBytes {
		return
	}
	remaining := maxAuditPayloadBytes - w.body.Len()
	if len(data) > remaining {
		w.body.Write(data[:remaining])
		w.body.WriteString(auditTruncatedValue)
		return
	}
	w.body.Write(data)
}

func persistAuditLog(c *gin.Context, start time.Time, requestBody []byte, responseBody string) {
	if !shouldAuditRequest(c) || global.APP_DB == nil {
		return
	}

	var userID *uint
	username := ""
	if authCtx, ok := GetAuthContext(c); ok && authCtx != nil {
		uid := authCtx.UserID
		userID = &uid
		username = authCtx.Username
	} else {
		if uidRaw, exists := c.Get("user_id"); exists {
			if uid, ok := uidRaw.(uint); ok && uid > 0 {
				userID = &uid
			}
		}
		if nameRaw, exists := c.Get("username"); exists {
			if name, ok := nameRaw.(string); ok {
				username = name
			}
		}
	}

	auditLog := adminModel.AuditLog{
		UserID:     userID,
		Username:   utils.TruncateString(username, 64),
		Method:     c.Request.Method,
		Path:       utils.TruncateString(c.Request.URL.Path, 255),
		StatusCode: c.Writer.Status(),
		Latency:    time.Since(start).Milliseconds(),
		ClientIP:   utils.TruncateString(c.ClientIP(), 64),
		UserAgent:  utils.TruncateString(c.Request.UserAgent(), 255),
		Request:    buildAuditRequestPayload(c.Request.URL.RawQuery, requestBody),
		Response:   sanitizeAuditPayload(responseBody),
	}

	db := global.APP_DB
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := db.WithContext(ctx).Create(&auditLog).Error; err != nil && err != gorm.ErrInvalidDB {
			if global.APP_LOG != nil {
				global.APP_LOG.Debug("写入审计日志失败",
					zap.String("path", auditLog.Path),
					zap.String("method", auditLog.Method),
					zap.Error(err))
			}
		}
	}()
}

func shouldAuditRequest(c *gin.Context) bool {
	path := c.Request.URL.Path
	if !strings.HasPrefix(path, "/api/") {
		return false
	}
	if path == "/api/health" || path == "/api/v1/health" || path == "/api/ping" {
		return false
	}
	if strings.Contains(path, "/sftp/download") || strings.Contains(path, "/sftp/upload") {
		return false
	}
	return true
}

func buildAuditRequestPayload(rawQuery string, body []byte) string {
	payload := map[string]interface{}{}
	if rawQuery != "" {
		payload["query"] = redactRawQuery(rawQuery)
	}
	if len(body) > 0 {
		payload["body"] = sanitizeAuditPayload(string(body))
	}
	if len(payload) == 0 {
		return ""
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return auditTruncatedValue
	}
	return utils.TruncateString(string(data), maxAuditPayloadBytes)
}

func redactRawQuery(rawQuery string) string {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		if containsSensitiveInfo(rawQuery) {
			return auditRedactedValue
		}
		return utils.TruncateString(rawQuery, maxAuditPayloadBytes)
	}
	for key := range values {
		if isSensitiveAuditKey(key) {
			values[key] = []string{auditRedactedValue}
		}
	}
	return utils.TruncateString(values.Encode(), maxAuditPayloadBytes)
}

func sanitizeAuditPayload(payload string) string {
	payload = strings.ToValidUTF8(payload, "\uFFFD")
	payload = strings.TrimSpace(payload)
	if payload == "" {
		return ""
	}
	if len(payload) > maxAuditPayloadBytes {
		payload = truncateAuditPayload(payload)
	}

	var decoded interface{}
	if err := json.Unmarshal([]byte(payload), &decoded); err == nil {
		redactAuditValue(&decoded, "")
		data, err := json.Marshal(decoded)
		if err == nil {
			return truncateAuditPayload(string(data))
		}
	}

	if containsSensitiveInfo(payload) {
		return auditRedactedValue
	}
	return truncateAuditPayload(payload)
}

func truncateAuditPayload(payload string) string {
	payload = strings.ToValidUTF8(payload, "\uFFFD")
	if len(payload) <= maxAuditPayloadBytes {
		return payload
	}
	limit := maxAuditPayloadBytes - len(auditTruncatedValue)
	if limit < 0 {
		limit = 0
	}
	return strings.ToValidUTF8(payload[:limit], "") + auditTruncatedValue
}

func redactAuditValue(value *interface{}, key string) {
	if value == nil {
		return
	}
	if isSensitiveAuditKey(key) {
		*value = auditRedactedValue
		return
	}
	switch typed := (*value).(type) {
	case map[string]interface{}:
		for childKey, childValue := range typed {
			v := childValue
			redactAuditValue(&v, childKey)
			typed[childKey] = v
		}
	case []interface{}:
		for i := range typed {
			v := typed[i]
			redactAuditValue(&v, key)
			typed[i] = v
		}
	case string:
		if containsSensitiveInfo(typed) && len(typed) > 32 {
			*value = auditRedactedValue
		}
	}
}

func isSensitiveAuditKey(key string) bool {
	key = strings.ToLower(key)
	sensitiveParts := []string{
		"password", "passwd", "pwd",
		"token", "secret", "credential", "authorization", "auth",
		"key", "private", "cert", "captcha", "challenge",
		"idnumber", "id_number", "realname", "real_name",
	}
	for _, part := range sensitiveParts {
		if strings.Contains(key, part) {
			return true
		}
	}
	return false
}
