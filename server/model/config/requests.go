package config

// UnifiedConfigRequest 统一配置请求
type UnifiedConfigRequest struct {
	Scope  string                 `json:"scope" binding:"required"` // public, user, admin
	Config map[string]interface{} `json:"config" binding:"required"`
}
