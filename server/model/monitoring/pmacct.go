package monitoring

import (
	"time"

	"gorm.io/gorm"
)

// PmacctTrafficRecord pmacct流量记录（5分钟精度）
// 索引策略：使用复合索引覆盖常见查询场景，提升性能
type PmacctTrafficRecord struct {
	ID           uint   `json:"id" gorm:"primaryKey"`
	InstanceID   uint   `json:"instance_id" gorm:"index:idx_instance_year_month;index:idx_instance_timestamp;not null;uniqueIndex:uk_instance_timestamp"` // 实例ID
	UserID       uint   `json:"user_id" gorm:"index:idx_user_year_month;not null"`                                                                        // 用户ID（冗余存储，避免JOIN）
	ProviderID   uint   `json:"provider_id" gorm:"index:idx_provider_year_month;not null"`                                                                // Provider ID
	ProviderType string `json:"provider_type" gorm:"size:50;not null"`                                                                                    // Provider类型
	MappedIP     string `json:"mapped_ip" gorm:"size:64;not null"`                                                                                        // 映射的公网IP地址

	// 流量统计数据 (单位: 字节)
	RxBytes    int64 `json:"rx_bytes"`    // 接收字节数（入站流量）
	TxBytes    int64 `json:"tx_bytes"`    // 发送字节数（出站流量）
	TotalBytes int64 `json:"total_bytes"` // 总流量字节数

	// 时间维度（支持5分钟精度）
	// year和month添加到复合索引中，提升月度查询性能
	Timestamp time.Time `json:"timestamp" gorm:"index:idx_instance_timestamp;index:idx_timestamp;not null;uniqueIndex:uk_instance_timestamp"` // 精确时间戳（5分钟对齐）
	Year      int       `json:"year" gorm:"index:idx_instance_year_month;index:idx_user_year_month;index:idx_provider_year_month"`            // 年份
	Month     int       `json:"month" gorm:"index:idx_instance_year_month;index:idx_user_year_month;index:idx_provider_year_month"`           // 月份
	Day       int       `json:"day"`                                                                                                          // 日期
	Hour      int       `json:"hour"`                                                                                                         // 小时
	Minute    int       `json:"minute"`                                                                                                       // 分钟（0, 5, 10, ..., 55）

	// 元数据
	RecordTime time.Time      `json:"record_time" gorm:"index"` // 记录时间，用于清理过期数据
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"deleted_at" gorm:"index" swaggerignore:"true"`
}

// TableName 指定表名
func (PmacctTrafficRecord) TableName() string {
	return "pmacct_traffic_records"
}

// PmacctMonitor pmacct监控配置
type PmacctMonitor struct {
	ID             uint      `json:"id" gorm:"primaryKey"`
	InstanceID     uint      `json:"instance_id" gorm:"uniqueIndex;not null"` // 实例ID（唯一）
	ProviderID     uint      `json:"provider_id" gorm:"index;not null"`       // Provider ID
	ProviderType   string    `json:"provider_type" gorm:"size:50;not null"`   // Provider类型
	MappedIP       string    `json:"mapped_ip" gorm:"size:64;not null"`       // 公网映射IPv4地址（用于显示）
	MappedIPv6     string    `json:"mapped_ipv6" gorm:"size:128"`             // 公网映射IPv6地址（用于显示）
	NetworkIfaceV4 string    `json:"network_iface_v4" gorm:"size:32"`         // IPv4流量监控的网络接口名称
	NetworkIfaceV6 string    `json:"network_iface_v6" gorm:"size:32"`         // IPv6流量监控的网络接口名称
	IsEnabled      bool      `json:"is_enabled" gorm:"default:true"`          // 是否启用监控
	LastSync       time.Time `json:"last_sync"`                               // 最后同步时间

	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index" swaggerignore:"true"`
}

// TableName 指定表名
func (PmacctMonitor) TableName() string {
	return "pmacct_monitors"
}

// PmacctSummary pmacct流量汇总响应
type PmacctSummary struct {
	InstanceID uint                   `json:"instance_id"`
	MappedIP   string                 `json:"mapped_ip"`
	MappedIPv6 string                 `json:"mapped_ipv6,omitempty"`
	Today      *PmacctTrafficRecord   `json:"today"`      // 今日流量
	ThisMonth  *PmacctTrafficRecord   `json:"this_month"` // 本月流量
	AllTime    *PmacctTrafficRecord   `json:"all_time"`   // 总流量
	History    []*PmacctTrafficRecord `json:"history"`    // 历史记录
}

// PmacctQuery pmacct查询条件
type PmacctQuery struct {
	InstanceID uint      `json:"instance_id"`
	MappedIP   string    `json:"mapped_ip"`
	Year       int       `json:"year"`
	Month      int       `json:"month"`
	StartTime  time.Time `json:"start_time"`
	EndTime    time.Time `json:"end_time"`
	Limit      int       `json:"limit"`
	QueryType  string    `json:"query_type"` // "hourly", "daily", "monthly", "yearly"
}
