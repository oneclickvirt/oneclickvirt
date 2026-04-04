package checkin

import (
	"time"

	"gorm.io/gorm"
)

// CheckinConfig 签到续期配置(每节点一条)
type CheckinConfig struct {
	ID        uint      `json:"id" gorm:"primarykey"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// 关联节点
	ProviderID uint `json:"providerId" gorm:"uniqueIndex;not null"`
	// 功能开关
	Enabled bool `json:"enabled" gorm:"default:false"` // 是否启用签到续期
	// 签到配置
	DefaultExpireDays int    `json:"defaultExpireDays" gorm:"default:7"`           // 默认实例到期天数
	RenewalDays       int    `json:"renewalDays" gorm:"default:7"`                 // 每次签到续期天数
	MaxExpireDays     int    `json:"maxExpireDays" gorm:"default:30"`              // 最大累计到期天数(0=不限)
	OverdueAction     string `json:"overdueAction" gorm:"size:16;default:stop"`    // 超期操作: stop(关停), delete(删除)
	CheckinMethod     string `json:"checkinMethod" gorm:"size:32;default:captcha"` // 签到方式: captcha(验证码)
}

// CheckinRecord 签到记录
type CheckinRecord struct {
	ID        uint      `json:"id" gorm:"primarykey"`
	CreatedAt time.Time `json:"createdAt"`
	// 关联
	UserID     uint `json:"userId" gorm:"not null;index:idx_user_id"`
	InstanceID uint `json:"instanceId" gorm:"not null;index:idx_instance_id"`
	ProviderID uint `json:"providerId" gorm:"not null;index:idx_provider_id"`
	// 签到方式
	Method string `json:"method" gorm:"size:32;not null"` // captcha, ...
	// 续期结果
	RenewalDays int        `json:"renewalDays" gorm:"not null"` // 本次续期天数
	NewExpireAt time.Time  `json:"newExpireAt" gorm:"not null"` // 续期后的到期时间
	OldExpireAt *time.Time `json:"oldExpireAt"`                 // 续期前的到期时间
}

// CheckinVerification 签到验证码(预留扩展)
type CheckinVerification struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	CreatedAt time.Time      `json:"createdAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
	// 关联
	UserID     uint      `json:"userId" gorm:"not null;index"`
	InstanceID uint      `json:"instanceId" gorm:"not null"`
	Method     string    `json:"method" gorm:"size:32;not null"` // captcha, ...
	Code       string    `json:"code" gorm:"size:32;not null"`   // 验证码
	Used       bool      `json:"used" gorm:"default:false"`
	ExpiredAt  time.Time `json:"expiredAt" gorm:"not null"`
}
