package auth

import (
	"time"

	"gorm.io/gorm"
)

// ApiToken 用户/管理员API访问令牌模型
// 支持用户和管理员分别创建自己的API Token，用于通过API操作面板
type ApiToken struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`

	// 关联信息
	UserID   uint   `json:"userId" gorm:"not null;index:idx_user_id"`        // 所属用户ID
	Username string `json:"username" gorm:"size:64;not null"`                // 冗余字段，方便查询
	UserType string `json:"userType" gorm:"size:16;not null;index:idx_type"` // 用户类型：user/normal_admin/admin

	// Token信息
	Name        string     `json:"name" gorm:"size:128;not null"`              // Token名称（用户自定义，便于识别）
	Token       string     `json:"token" gorm:"uniqueIndex;size:128;not null"` // Token值（SHA256哈希存储）
	TokenPrefix string     `json:"tokenPrefix" gorm:"size:16;not null"`        // Token前缀（明文显示，如"tk_abc123..."）
	ExpiresAt   *time.Time `json:"expiresAt" gorm:"index"`                     // 过期时间（nil=永久有效）
	LastUsedAt  *time.Time `json:"lastUsedAt"`                                 // 最后使用时间
	UseCount    int64      `json:"useCount" gorm:"default:0"`                  // 使用次数

	// 权限范围
	// 限制Token可访问的API范围，逗号分隔的路径前缀
	// 为空表示无限制（继承用户自身的权限）
	ScopeRestriction string `json:"scopeRestriction" gorm:"size:512"`

	// 状态
	Status int `json:"status" gorm:"default:1;index:idx_status"` // 1=启用，0=禁用
}

// ApiTokenCreateRequest 创建API Token请求
type ApiTokenCreateRequest struct {
	Name       string   `json:"name" binding:"required"` // Token名称
	ExpireDays int      `json:"expireDays"`              // 过期天数（0=永久有效）
	Scopes     []string `json:"scopes"`                  // 权限范围限制
}

// ApiTokenCreateResponse 创建API Token响应
type ApiTokenCreateResponse struct {
	ID          uint       `json:"id"`
	Name        string     `json:"name"`
	Token       string     `json:"token"`       // 仅在创建时返回完整Token
	TokenPrefix string     `json:"tokenPrefix"` // Token前缀（用于列表显示）
	ExpiresAt   *time.Time `json:"expiresAt"`
}

// ApiTokenListRequest 查询API Token列表请求
type ApiTokenListRequest struct {
	Page     int    `json:"page" form:"page"`
	PageSize int    `json:"pageSize" form:"pageSize"`
	Keyword  string `json:"keyword" form:"keyword"`
	Status   *int   `json:"status" form:"status"`
}

// ApiTokenTestRequest 测试API Token请求
type ApiTokenTestRequest struct {
	Token string `json:"token" binding:"required"`
}

// ApiTokenTestResponse 测试API Token响应
type ApiTokenTestResponse struct {
	Valid     bool       `json:"valid"`
	UserID    uint       `json:"userId"`
	Username  string     `json:"username"`
	UserType  string     `json:"userType"`
	ExpiresAt *time.Time `json:"expiresAt"`
}
