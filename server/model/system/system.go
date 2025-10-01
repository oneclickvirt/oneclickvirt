package system

import (
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// InviteCode 邀请码模型
type InviteCode struct {
	ID          uint           `json:"id" gorm:"primarykey"`
	Code        string         `json:"code" gorm:"size:32;not null;uniqueIndex"` // 邀请码
	CreatorID   uint           `json:"creatorId" gorm:"not null;index"`          // 创建者ID
	CreatorName string         `json:"creatorName" gorm:"size:50;not null"`      // 创建者名称
	Description string         `json:"description" gorm:"size:255"`              // 描述
	MaxUses     int            `json:"maxUses" gorm:"not null;default:1"`        // 最大使用次数，0表示无限制
	UsedCount   int            `json:"usedCount" gorm:"not null;default:0"`      // 已使用次数
	ExpiresAt   *time.Time     `json:"expiresAt" gorm:"index"`                   // 过期时间
	Status      int            `json:"status" gorm:"not null;default:1;index"`   // 状态：0-禁用 1-启用
	CreatedAt   time.Time      `json:"createdAt"`
	UpdatedAt   time.Time      `json:"updatedAt"`
	DeletedAt   gorm.DeletedAt `json:"deletedAt" gorm:"index"`
}

func (InviteCode) TableName() string {
	return "invite_codes"
}

// InviteCodeUsage 邀请码使用记录（仅记录成功注册）
type InviteCodeUsage struct {
	ID           uint           `json:"id" gorm:"primarykey"`
	InviteCodeID uint           `json:"inviteCodeId" gorm:"not null;index"` // 邀请码ID
	IP           string         `json:"ip" gorm:"size:45;not null"`         // 使用IP
	UserAgent    string         `json:"userAgent" gorm:"size:500"`          // 用户代理
	UsedAt       time.Time      `json:"usedAt" gorm:"not null"`             // 使用时间
	CreatedAt    time.Time      `json:"createdAt"`
	UpdatedAt    time.Time      `json:"updatedAt"`
	DeletedAt    gorm.DeletedAt `json:"-" gorm:"index"`
}

// SystemImage 系统镜像模型 - 用于管理各种操作系统镜像
type SystemImage struct {
	// 基础字段
	ID        uint           `json:"id" gorm:"primarykey"`                     // 镜像主键ID
	UUID      string         `json:"uuid" gorm:"uniqueIndex;not null;size:36"` // 镜像唯一标识符
	CreatedAt time.Time      `json:"createdAt"`                                // 镜像记录创建时间
	UpdatedAt time.Time      `json:"updatedAt"`                                // 镜像记录更新时间
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`                           // 软删除时间

	// 基本信息
	Name        string `json:"name" gorm:"not null;size:128"`        // 自定义镜像名称
	Description string `json:"description" gorm:"size:512"`          // 镜像描述
	URL         string `json:"url" gorm:"not null;size:512"`         // 镜像下载地址
	Status      string `json:"status" gorm:"default:active;size:16"` // 镜像状态：active, inactive

	// 技术规格
	ProviderType string `json:"providerType" gorm:"not null;size:32"` // 支持的Provider类型：proxmox, lxd, incus, docker
	InstanceType string `json:"instanceType" gorm:"not null;size:16"` // 实例类型：vm（虚拟机）, container（容器）
	Architecture string `json:"architecture" gorm:"not null;size:16"` // CPU架构：amd64, arm64, s390x等

	// 文件信息
	Checksum string `json:"checksum" gorm:"size:128"` // 文件校验和（用于验证完整性）
	Size     int64  `json:"size" gorm:"default:0"`    // 文件大小（字节）

	// 操作系统信息
	OSType    string `json:"osType" gorm:"size:32"`    // 操作系统类型：ubuntu, centos, debian, alpine等
	OSVersion string `json:"osVersion" gorm:"size:32"` // 操作系统版本号
	Tags      string `json:"tags" gorm:"size:255"`     // 标签列表（用逗号分隔）

	// 管理信息
	CreatedBy *uint `json:"createdBy"` // 创建者用户ID（可为空，系统镜像）
}

func (s *SystemImage) BeforeCreate(tx *gorm.DB) error {
	s.UUID = uuid.New().String()
	return nil
}

// Announcement 公告模型
type Announcement struct {
	ID            uint           `json:"id" gorm:"primarykey"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
	DeletedAt     gorm.DeletedAt `json:"-" gorm:"index"`
	Title         string         `json:"title" gorm:"not null;size:255"`
	Content       string         `json:"content" gorm:"type:longtext"`         // 改为longtext支持富文本
	ContentHTML   string         `json:"contentHtml" gorm:"type:longtext"`     // 渲染后的HTML内容
	Type          string         `json:"type" gorm:"size:32;default:homepage"` // homepage=首页公告, topbar=顶部栏公告
	Priority      int            `json:"priority" gorm:"default:0"`            // 优先级，数字越大越靠前
	Status        int            `json:"status" gorm:"default:1"`              // 1=启用 0=禁用
	IsSticky      bool           `json:"isSticky" gorm:"default:false"`        // 是否置顶
	StartTime     *time.Time     `json:"startTime"`                            // 开始时间
	EndTime       *time.Time     `json:"endTime"`                              // 结束时间
	CreatedBy     *uint          `json:"createdBy"`                            // 创建者ID，可为空
	CreatedByUser string         `json:"createdByUser" gorm:"-"`               // 创建者用户名（查询时填充）
}

// Captcha 图形验证码模型
type Captcha struct {
	ID        string         `json:"id" gorm:"primarykey;size:64"`
	Code      string         `json:"code" gorm:"not null;size:16"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"` // 添加软删除字段
	ExpiresAt time.Time      `json:"expiresAt" gorm:"index"`
}
