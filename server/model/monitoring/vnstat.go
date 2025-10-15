package monitoring

import (
	"time"

	"gorm.io/gorm"
)

// VnStatTrafficRecord vnStat流量记录
type VnStatTrafficRecord struct {
	ID           uint   `json:"id" gorm:"primaryKey"`
	InstanceID   uint   `json:"instance_id" gorm:"index;not null"`     // 实例ID
	ProviderID   uint   `json:"provider_id" gorm:"index;not null"`     // Provider ID
	ProviderType string `json:"provider_type" gorm:"size:50;not null"` // Provider类型
	Interface    string `json:"interface" gorm:"size:32;not null"`     // 网络接口名称

	// 流量统计数据 (单位: 字节)
	RxBytes    int64 `json:"rx_bytes"`    // 接收字节数
	TxBytes    int64 `json:"tx_bytes"`    // 发送字节数
	TotalBytes int64 `json:"total_bytes"` // 总流量字节数

	// 时间统计
	Year  int `json:"year"`  // 年份
	Month int `json:"month"` // 月份
	Day   int `json:"day"`   // 日期 (0表示月度统计)
	Hour  int `json:"hour"`  // 小时 (0表示日度统计)

	// vnStat原始数据
	RawData string `json:"raw_data" gorm:"type:text"` // vnStat原始JSON数据

	// 时间戳
	RecordTime time.Time      `json:"record_time"` // 记录时间
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"deleted_at" gorm:"index" swaggerignore:"true"`
}

// TableName 指定表名
func (VnStatTrafficRecord) TableName() string {
	return "vnstat_traffic_records"
}

// VnStatInterface vnStat网络接口信息
type VnStatInterface struct {
	ID         uint      `json:"id" gorm:"primaryKey"`
	InstanceID uint      `json:"instance_id" gorm:"index;not null"` // 实例ID
	ProviderID uint      `json:"provider_id" gorm:"index;not null"` // Provider ID
	Interface  string    `json:"interface" gorm:"size:32;not null"` // 接口名称
	IsEnabled  bool      `json:"is_enabled" gorm:"default:true"`    // 是否启用监控
	LastSync   time.Time `json:"last_sync"`                         // 最后同步时间

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index" swaggerignore:"true"`
}

// TableName 指定表名
func (VnStatInterface) TableName() string {
	return "vnstat_interfaces"
}

// VnStatSummary vnStat流量汇总响应
type VnStatSummary struct {
	InstanceID uint                   `json:"instance_id"`
	Interface  string                 `json:"interface"`
	Today      *VnStatTrafficRecord   `json:"today"`      // 今日流量
	ThisMonth  *VnStatTrafficRecord   `json:"this_month"` // 本月流量
	AllTime    *VnStatTrafficRecord   `json:"all_time"`   // 总流量
	History    []*VnStatTrafficRecord `json:"history"`    // 历史记录
}

// VnStatQuery vnStat查询条件
type VnStatQuery struct {
	InstanceID uint      `json:"instance_id"`
	Interface  string    `json:"interface"`
	Year       int       `json:"year"`
	Month      int       `json:"month"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Limit      int       `json:"limit"`
	QueryType  string    `json:"query_type"` // "hourly", "daily", "monthly", "yearly"
}
