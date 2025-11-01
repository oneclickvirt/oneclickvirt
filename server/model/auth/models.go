package auth

import (
	"time"

	"gorm.io/gorm"
)

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
