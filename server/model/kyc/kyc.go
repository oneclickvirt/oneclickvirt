package kyc

import (
	"time"

	"gorm.io/gorm"
)

// KYCRecord 实名认证记录
type KYCRecord struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
	// 用户关联
	UserID uint `json:"userId" gorm:"uniqueIndex;not null"` // 一个用户只有一条认证记录
	// 认证信息(加密存储)
	RealName     string `json:"realName" gorm:"size:128;not null"`
	IDNumber     string `json:"-" gorm:"size:255;not null"`            // 身份证号(加密,不返回给前端)
	IDNumberHash string `json:"-" gorm:"size:64;uniqueIndex;not null"` // 身份证号SHA256哈希(查重用)
	// 认证方式
	Method string `json:"method" gorm:"size:32;default:alipay"` // 认证方式: alipay
	// 状态: pending(待审核), approved(已通过), rejected(已拒绝)
	Status       string     `json:"status" gorm:"size:16;default:pending;index"`
	ReviewedBy   uint       `json:"reviewedBy" gorm:"default:0"`  // 审核管理员ID(0=自动审核)
	ReviewedAt   *time.Time `json:"reviewedAt"`                   // 审核时间
	RejectReason string     `json:"rejectReason" gorm:"size:512"` // 拒绝原因
}
