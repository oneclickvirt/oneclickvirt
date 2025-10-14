package auth

import (
	"time"

	"gorm.io/gorm"
)

// JWTBlacklist JWT Token 黑名单模型
type JWTBlacklist struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	CreatedAt time.Time      `json:"createdAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
	JTI       string         `json:"jti" gorm:"uniqueIndex;not null;size:128"` // JWT Token ID
	UserID    uint           `json:"userId" gorm:"not null;index"`             // 用户ID
	ExpiresAt time.Time      `json:"expiresAt" gorm:"not null;index"`          // Token原始过期时间
	Reason    string         `json:"reason" gorm:"size:100"`                   // 撤销原因：logout, disable, security, admin
	RevokedBy uint           `json:"revokedBy" gorm:"default:0"`               // 撤销操作者ID，0表示系统自动
}

// TableName 指定表名
func (JWTBlacklist) TableName() string {
	return "jwt_blacklist"
}

// IsExpired 检查Token是否已过期（过期的Token无需在黑名单中保留）
func (jb *JWTBlacklist) IsExpired() bool {
	return time.Now().After(jb.ExpiresAt)
}

// Role 角色模型
type Role struct {
	// 基础字段
	ID        uint           `json:"id" gorm:"primarykey"` // 角色主键ID
	CreatedAt time.Time      `json:"createdAt"`            // 角色创建时间
	UpdatedAt time.Time      `json:"updatedAt"`            // 角色更新时间
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`       // 软删除时间

	// 角色信息
	Name        string `json:"name" gorm:"uniqueIndex;not null;size:64"` // 角色名称（唯一）
	Description string `json:"description" gorm:"size:255"`              // 角色描述
	Code        string `json:"code" gorm:"size:64"`                      // 角色代码（用于业务逻辑识别）
	Status      int    `json:"status" gorm:"default:1"`                  // 角色状态：0=禁用，1=启用
	Remark      string `json:"remark" gorm:"size:255"`                   // 备注信息
}
