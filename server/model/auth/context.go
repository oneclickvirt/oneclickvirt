package auth

import "time"

// AuthLevel 权限级别
type AuthLevel int

const (
	AuthLevelPublic AuthLevel = 0 // 公开访问
	AuthLevelUser   AuthLevel = 1 // 普通用户
	AuthLevelAdmin  AuthLevel = 3 // 管理员
)

// AuthContext 认证上下文
type AuthContext struct {
	UserID       uint     `json:"user_id"`
	Username     string   `json:"username"`
	UserType     string   `json:"user_type"`      // 当前有效的用户类型
	Level        int      `json:"level"`          // 当前有效权限级别
	BaseUserType string   `json:"base_user_type"` // 用户基础类型
	AllUserTypes []string `json:"all_user_types"` // 用户拥有的所有权限类型
	IsEffective  bool     `json:"is_effective"`   // 权限是否有效
	// TokensInvalidatedAt 用于检查 token 是否在用户级同步吹销之前签发
	TokensInvalidatedAt *time.Time `json:"-"`
}
