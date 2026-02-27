package provider

import "time"

// ProviderIPv4Pool 服务商IPv4地址池
// 用于 dedicated_ipv4 / dedicated_ipv4_ipv6 类型服务商的独立IP管理
type ProviderIPv4Pool struct {
	ID          uint       `json:"id"           gorm:"primarykey"`
	ProviderID  uint       `json:"provider_id"  gorm:"not null;index:idx_provider_ipv4,priority:1"`
	Address     string     `json:"address"      gorm:"not null;size:45;uniqueIndex"` // 单个 IPv4 地址
	IsAllocated bool       `json:"is_allocated" gorm:"default:false;index:idx_provider_ipv4,priority:2"`
	InstanceID  *uint      `json:"instance_id"  gorm:"index"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	DeletedAt   *time.Time `json:"-"            gorm:"index"`
}

// TableName 指定表名
func (ProviderIPv4Pool) TableName() string {
	return "provider_ipv4_pools"
}
