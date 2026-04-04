package domain

import (
	"time"

	"gorm.io/gorm"
)

// Domain 用户域名绑定记录
type Domain struct {
	ID        uint           `json:"id" gorm:"primarykey"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `json:"-" gorm:"index"`
	// 关联
	UserID     uint `json:"userId" gorm:"not null;index:idx_user_id"`
	InstanceID uint `json:"instanceId" gorm:"not null;index:idx_instance_id"`
	ProviderID uint `json:"providerId" gorm:"not null;index:idx_provider_id"`
	// 域名信息
	DomainName   string `json:"domainName" gorm:"uniqueIndex;not null;size:255"`
	Protocol     string `json:"protocol" gorm:"size:16;default:http"`
	InternalIP   string `json:"internalIP" gorm:"size:64;not null"`
	InternalPort int    `json:"internalPort" gorm:"not null"`
	EnableSSL    bool   `json:"enableSSL" gorm:"default:false"`
	// 状态
	Status    string     `json:"status" gorm:"size:16;default:pending"`
	ErrorMsg  string     `json:"errorMsg" gorm:"size:512"`
	ExpiresAt *time.Time `json:"expiresAt"`
}

// DomainConfig 域名绑定全局/节点配置
type DomainConfig struct {
	ID        uint      `json:"id" gorm:"primarykey"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
	// 关联节点(每节点一条配置)
	ProviderID uint `json:"providerId" gorm:"uniqueIndex;not null"`
	// 功能开关
	Enabled bool `json:"enabled" gorm:"default:false"`
	// 配额
	MaxDomainsPerUser int `json:"maxDomainsPerUser" gorm:"default:3"`
	// DNS配置
	DNSType       string `json:"dnsType" gorm:"size:32;default:hosts"`
	DNSConfigPath string `json:"dnsConfigPath" gorm:"size:512"`
	// Nginx反代配置
	NginxConfigPath string `json:"nginxConfigPath" gorm:"size:512"`
	NginxReloadCmd  string `json:"nginxReloadCmd" gorm:"size:512;default:systemctl reload nginx"`
	// 域名后缀限制
	AllowedSuffixes string `json:"allowedSuffixes" gorm:"size:1024"` // 允许的域名后缀(逗号分隔,空=不限)
}
