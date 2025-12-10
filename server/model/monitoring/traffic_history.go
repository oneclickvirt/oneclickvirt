package monitoring

import (
	"time"

	"gorm.io/gorm"
)

// InstanceTrafficHistory 实例流量历史记录（用于图表展示和缓存加速）
type InstanceTrafficHistory struct {
	ID         uint `json:"id" gorm:"primaryKey"`
	InstanceID uint `json:"instance_id" gorm:"index:idx_instance_time,priority:1;uniqueIndex:uk_instance_period,priority:1;not null"` // 实例ID
	ProviderID uint `json:"provider_id" gorm:"index:idx_provider_id;not null"`                                                        // Provider ID
	UserID     uint `json:"user_id" gorm:"index:idx_user_id;not null"`                                                                // 用户ID

	// 流量数据 (单位: MB)
	TrafficIn  int64 `json:"traffic_in"`  // 入站流量
	TrafficOut int64 `json:"traffic_out"` // 出站流量
	TotalUsed  int64 `json:"total_used"`  // 总流量

	// 时间维度
	// 添加唯一约束，防止重复聚合
	// day=0, hour=0 表示月度汇总（用于快速查询缓存）
	Year  int `json:"year" gorm:"index:idx_instance_time,priority:2;uniqueIndex:uk_instance_period,priority:2;not null"`  // 年
	Month int `json:"month" gorm:"index:idx_instance_time,priority:3;uniqueIndex:uk_instance_period,priority:3;not null"` // 月
	Day   int `json:"day" gorm:"index:idx_instance_time,priority:4;uniqueIndex:uk_instance_period,priority:4;not null"`   // 日
	Hour  int `json:"hour" gorm:"index:idx_instance_time,priority:5;uniqueIndex:uk_instance_period,priority:5;not null"`  // 小时(0-23)，0表示日度汇总

	RecordTime time.Time      `json:"record_time" gorm:"index:idx_record_time"` // 记录时间
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"deleted_at" gorm:"index:idx_deleted_at" swaggerignore:"true"`
}

// TableName 指定表名
func (InstanceTrafficHistory) TableName() string {
	return "instance_traffic_histories"
}

// ProviderTrafficHistory Provider流量历史记录（用于图表展示）
type ProviderTrafficHistory struct {
	ID         uint `json:"id" gorm:"primaryKey"`
	ProviderID uint `json:"provider_id" gorm:"index:idx_provider_time,priority:1;not null"` // Provider ID

	// 流量数据 (单位: MB)
	TrafficIn  int64 `json:"traffic_in"`  // 入站流量
	TrafficOut int64 `json:"traffic_out"` // 出站流量
	TotalUsed  int64 `json:"total_used"`  // 总流量

	// 实例统计
	InstanceCount int `json:"instance_count"` // 实例数量

	// 时间维度
	Year  int `json:"year" gorm:"index:idx_provider_time,priority:2;not null"`  // 年
	Month int `json:"month" gorm:"index:idx_provider_time,priority:3;not null"` // 月
	Day   int `json:"day" gorm:"index:idx_provider_time,priority:4;not null"`   // 日
	Hour  int `json:"hour" gorm:"index:idx_provider_time,priority:5;not null"`  // 小时(0-23)，0表示日度汇总

	RecordTime time.Time      `json:"record_time" gorm:"index"` // 记录时间
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"deleted_at" gorm:"index" swaggerignore:"true"`
}

// TableName 指定表名
func (ProviderTrafficHistory) TableName() string {
	return "provider_traffic_histories"
}

// UserTrafficHistory 用户流量历史记录（用于图表展示）
type UserTrafficHistory struct {
	ID     uint `json:"id" gorm:"primaryKey"`
	UserID uint `json:"user_id" gorm:"index:idx_user_time,priority:1;not null"` // 用户ID

	// 流量数据 (单位: MB)
	TrafficIn  int64 `json:"traffic_in"`  // 入站流量
	TrafficOut int64 `json:"traffic_out"` // 出站流量
	TotalUsed  int64 `json:"total_used"`  // 总流量

	// 实例统计
	InstanceCount int `json:"instance_count"` // 实例数量

	// 时间维度
	Year  int `json:"year" gorm:"index:idx_user_time,priority:2;not null"`  // 年
	Month int `json:"month" gorm:"index:idx_user_time,priority:3;not null"` // 月
	Day   int `json:"day" gorm:"index:idx_user_time,priority:4;not null"`   // 日
	Hour  int `json:"hour" gorm:"index:idx_user_time,priority:5;not null"`  // 小时(0-23)，0表示日度汇总

	RecordTime time.Time      `json:"record_time" gorm:"index"` // 记录时间
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	DeletedAt  gorm.DeletedAt `json:"deleted_at" gorm:"index" swaggerignore:"true"`
}

// TableName 指定表名
func (UserTrafficHistory) TableName() string {
	return "user_traffic_histories"
}
